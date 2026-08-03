package balancev2

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/migrationkit"
	"github.com/herdifirdausss/seev/pkg/database"
)

var ErrSourceWriteDisabled = errors.New("balancev2: source writes are disabled; source rollback requires resynchronization")

type balanceSourceFunc func(context.Context, uuid.UUID) (model.AccountBalance, error)

type Runtime struct {
	db       database.DatabaseSQL
	controls *ControlRepository
	cfg      Config
	logger   *slog.Logger
	owner    string

	cacheMu      sync.Mutex
	cached       Migration
	cacheExpires time.Time

	queue    chan shadowJob
	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup

	limiterMu       sync.Mutex
	limiterSecond   time.Time
	comparisonsThis int
	cooldown        map[string]time.Time
}

type shadowJob struct {
	migrationID uuid.UUID
	accountID   uuid.UUID
	source      *SourceRow
	target      *TargetRow
	readSource  balanceSourceFunc
	targetMode  bool
}

func NewRuntime(db database.DatabaseSQL, cfg Config, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.withDefaults()
	return &Runtime{
		db: db, controls: NewControlRepository(db), cfg: cfg, logger: logger,
		owner: "ledger-balance-v2-" + uuid.NewString(),
		stop:  make(chan struct{}), cooldown: make(map[string]time.Time),
	}
}

func (r *Runtime) Config() Config { return r.cfg }

func (r *Runtime) Controls() *ControlRepository { return r.controls }

func (r *Runtime) Initialize(ctx context.Context, actor string) error {
	return r.controls.EnsureReference(ctx, r.cfg, actor)
}

func (r *Runtime) Start(ctx context.Context) {
	if !r.cfg.Enabled {
		return
	}
	r.queue = make(chan shadowJob, r.cfg.ShadowQueueSize)
	for i := 0; i < r.cfg.ShadowWorkers; i++ {
		r.wg.Add(1)
		go r.shadowWorker(ctx)
	}
	r.wg.Add(1)
	go r.lifecycleWorker(ctx)
}

func (r *Runtime) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	r.wg.Wait()
}

func (r *Runtime) Refresh() {
	r.cacheMu.Lock()
	r.cached = Migration{}
	r.cacheExpires = time.Time{}
	r.cacheMu.Unlock()
}

func (r *Runtime) migration(ctx context.Context) (Migration, error) {
	now := time.Now()
	r.cacheMu.Lock()
	if r.cacheExpires.After(now) && r.cached.ID != uuid.Nil {
		cached := r.cached
		r.cacheMu.Unlock()
		return cached, nil
	}
	r.cacheMu.Unlock()

	migration, err := r.controls.GetByName(ctx, MigrationName)
	if err != nil {
		return Migration{}, err
	}
	r.cacheMu.Lock()
	r.cached = migration
	r.cacheExpires = time.Now().Add(500 * time.Millisecond)
	r.cacheMu.Unlock()
	observeMigration(migration)
	return migration, nil
}

func (r *Runtime) EffectiveGate(ctx context.Context) (GateSnapshot, error) {
	migration, err := r.migration(ctx)
	if err != nil {
		return GateSnapshot{}, err
	}
	return r.controls.Gates(ctx, migration)
}

func (r *Runtime) SourceWriteAllowed(ctx context.Context) (bool, error) {
	if !r.cfg.Enabled {
		return true, nil
	}
	// Source-write disablement is a hard safety boundary. Do not use the
	// short read-path cache here: an operator transition must take effect
	// before the next authoritative posting on every process.
	migration, err := r.controls.GetByName(ctx, MigrationName)
	if errors.Is(err, ErrNoActiveMigration) || errors.Is(err, ErrMigrationNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return migration.SourceWriteEnabled, nil
}

func (r *Runtime) migrationForTx(ctx context.Context, tx *sql.Tx) (Migration, error) {
	return scanMigration(tx.QueryRowContext(ctx, `SELECT `+migrationColumns+` FROM data_migrations WHERE name = $1 FOR SHARE`, MigrationName))
}

// SourceWriteAllowedTx linearizes the source-authority decision with the
// posting transaction. The control transition uses FOR UPDATE on this same
// row, so either the posting commits under the old state before the transition,
// or the posting observes the committed disabled state and aborts.
func (r *Runtime) SourceWriteAllowedTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	if !r.cfg.Enabled {
		return true, nil
	}
	migration, err := r.migrationForTx(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrMigrationNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return migration.SourceWriteEnabled, nil
}

// WriteForPosting is called after the authoritative v1 update and before the
// ledger transaction commits. Strict mode returns an error so entries, v1,
// target, and outbox all roll back together. Shadow mode records a bounded,
// durable gap after rolling back only the failed target statement.
func (r *Runtime) WriteForPosting(ctx context.Context, tx *sql.Tx, accountIDs []uuid.UUID, transactionID uuid.UUID) error {
	started := time.Now()
	if !r.cfg.Enabled || r.cfg.DisableTargetWrites {
		return nil
	}
	// Read and lock the durable control row in the posting transaction. This
	// makes strictness and source authority linearizable with operator control
	// transitions, rather than merely minimizing a cache race.
	migration, err := r.migrationForTx(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNoActiveMigration) || errors.Is(err, ErrMigrationNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !migration.SourceWriteEnabled {
		return ErrSourceWriteDisabled
	}
	effective := migration
	if migration.State == string(migrationkit.Paused) && migration.PreviousState != "" {
		effective.State = migration.PreviousState
	}
	backfillShadow := effective.State == string(migrationkit.Backfilling)
	if !backfillShadow && (!targetWriteStage(migration) || !migration.TargetWriteEnabled) {
		return nil
	}
	strict := !backfillShadow && (migration.StrictDualWrite || effective.State == string(migrationkit.TargetPrimary) || effective.State == string(migrationkit.RampingRead) || effective.State == string(migrationkit.CanaryRead) || effective.State == string(migrationkit.SourceWriteDisabled) || effective.State == string(migrationkit.Observation))
	mode := "shadow"
	if strict {
		mode = "strict"
	}

	if _, savepointErr := tx.ExecContext(ctx, `SAVEPOINT c6_balance_v2_target_write`); savepointErr != nil {
		return fmt.Errorf("balancev2: create target write savepoint: %w", savepointErr)
	}
	sources := make([]SourceRow, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		source, sourceErr := readSourceForUpdate(ctx, tx, accountID)
		if sourceErr != nil {
			_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT c6_balance_v2_target_write`)
			dualWriteTotal.WithLabelValues(MigrationName, "source_error", mode).Inc()
			dualWriteDuration.WithLabelValues(MigrationName, "source_error").Observe(time.Since(started).Seconds())
			if strict {
				return sourceErr
			}
			return r.recordShadowWriteGaps(ctx, migration.ID, sources, accountID, 0)
		}
		sources = append(sources, source)
		target, transformErr := Transform(source, &transactionID)
		if transformErr != nil {
			_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT c6_balance_v2_target_write`)
			dualWriteTotal.WithLabelValues(MigrationName, "transform_error", mode).Inc()
			if strict {
				return transformErr
			}
			return r.recordShadowWriteGaps(ctx, migration.ID, sources, accountID, source.SourceVersion)
		}
		if _, upsertErr := upsertTarget(ctx, tx, target); upsertErr != nil {
			_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT c6_balance_v2_target_write`)
			dualWriteTotal.WithLabelValues(MigrationName, "target_error", mode).Inc()
			dualWriteDuration.WithLabelValues(MigrationName, "target_error").Observe(time.Since(started).Seconds())
			if strict {
				return upsertErr
			}
			return r.recordShadowWriteGaps(ctx, migration.ID, sources, accountID, source.SourceVersion)
		}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT c6_balance_v2_target_write`); err != nil {
		return fmt.Errorf("balancev2: release target write savepoint: %w", err)
	}
	dualWriteTotal.WithLabelValues(MigrationName, "success", mode).Inc()
	dualWriteDuration.WithLabelValues(MigrationName, "success").Observe(time.Since(started).Seconds())
	return nil
}

func (r *Runtime) recordShadowWriteGaps(ctx context.Context, migrationID uuid.UUID, sources []SourceRow, failedAccount uuid.UUID, failedVersion int64) error {
	seen := make(map[uuid.UUID]bool, len(sources)+1)
	for _, source := range sources {
		seen[source.AccountID] = true
		if err := r.controls.RecordComparison(ctx, migrationID, Comparison{
			AccountID: source.AccountID, ResourceLayer: "live_write", Result: "write_gap",
			Classification: ClassificationLiveWriteGap, Severity: "critical",
			SourceVersion: source.SourceVersion, FieldMask: FieldVersion,
			ErrorCode: "projection_write_error",
		}); err != nil {
			r.logger.Warn("balancev2: could not persist shadow write gap", "error", err)
		}
	}
	if failedAccount != uuid.Nil && !seen[failedAccount] {
		if err := r.controls.RecordComparison(ctx, migrationID, Comparison{
			AccountID: failedAccount, ResourceLayer: "live_write", Result: "write_gap",
			Classification: ClassificationLiveWriteGap, Severity: "critical",
			SourceVersion: failedVersion, FieldMask: FieldVersion,
			ErrorCode: "projection_write_error",
		}); err != nil {
			r.logger.Warn("balancev2: could not persist shadow write gap", "error", err)
		}
	}
	return nil
}

// EnsureForAccount closes the provisioning mutation path: a new account gets
// its target row in the same transaction when target writes are active.
func (r *Runtime) EnsureForAccount(ctx context.Context, tx *sql.Tx, accountID uuid.UUID) error {
	if !r.cfg.Enabled || r.cfg.DisableTargetWrites {
		return nil
	}
	// Provisioning is also a source mutation path. Use the current durable
	// lifecycle state so a newly created account cannot miss a freshly enabled
	// strict target-write stage.
	migration, err := r.migrationForTx(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNoActiveMigration) || errors.Is(err, ErrMigrationNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !migration.SourceWriteEnabled {
		return ErrSourceWriteDisabled
	}
	targetProvisioningStage := migration.State == string(migrationkit.Backfilling) || targetWriteStage(migration)
	if !targetProvisioningStage || (!migration.TargetWriteEnabled && migration.State != string(migrationkit.Backfilling)) {
		return nil
	}
	source, err := readSource(ctx, tx, accountID)
	if err != nil {
		return err
	}
	target, err := Transform(source, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT c6_balance_v2_provision`); err != nil {
		return fmt.Errorf("balancev2: provision target savepoint: %w", err)
	}
	_, err = upsertTarget(ctx, tx, target)
	if err != nil && !migration.StrictDualWrite {
		if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT c6_balance_v2_provision`); rollbackErr != nil {
			return fmt.Errorf("balancev2: rollback provision target write: %w", rollbackErr)
		}
		if gapErr := r.controls.RecordComparison(ctx, migration.ID, Comparison{
			AccountID: accountID, ResourceLayer: "live_write", Result: "write_gap",
			Classification: ClassificationLiveWriteGap, Severity: "critical",
			SourceVersion: source.SourceVersion, FieldMask: FieldVersion,
			ErrorCode: "provisioning_projection_write_error",
		}); gapErr != nil {
			r.logger.Warn("balancev2: could not persist provisioning write gap", "error", gapErr)
		}
		_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT c6_balance_v2_provision`)
		return nil
	}
	if err == nil {
		_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT c6_balance_v2_provision`)
	}
	return err
}

func (r *Runtime) RequestRepair(ctx context.Context, mismatchID uuid.UUID, actor, reason string) (Repair, error) {
	return r.controls.CreateRepair(ctx, mismatchID, actor, reason)
}

// ApproveRepair requires the approver to resupply the account identifier. The
// control tables retain only its layer-specific hash, so approval evidence
// never turns the durable mismatch log into an account directory.
func (r *Runtime) ApproveRepair(ctx context.Context, repairID, accountID uuid.UUID, approver, reason string) (Repair, error) {
	if r.cfg.DisableTargetWrites {
		return Repair{}, fmt.Errorf("balancev2: target repairs are disabled by emergency configuration")
	}
	repair, err := r.controls.GetRepair(ctx, repairID)
	if err != nil {
		return Repair{}, err
	}
	mismatch, err := r.controls.GetMismatch(ctx, repair.MismatchID)
	if err != nil {
		return Repair{}, err
	}
	if mismatch.MigrationID != repair.MigrationID {
		return Repair{}, ErrMigrationNotFound
	}
	migration, err := r.controls.Get(ctx, repair.MigrationID)
	if err != nil {
		return Repair{}, err
	}
	if !targetWriteStage(migration) || !migration.TargetWriteEnabled {
		return Repair{}, fmt.Errorf("balancev2: repairs are unavailable before target writes")
	}
	knownResource := false
	for _, layer := range []string{"source_target", "live_write", "source_ledger", "target_ledger"} {
		if bytes.Equal(repair.ResourceKeyHash, resourceKeyHash(accountID, layer)) || bytes.Equal(mismatch.ResourceKeyHash, resourceKeyHash(accountID, layer)) {
			knownResource = true
			break
		}
	}
	if !knownResource {
		return Repair{}, fmt.Errorf("balancev2: account does not match the repair resource")
	}
	repair, err = r.controls.ApproveRepair(ctx, repairID, approver, reason)
	if err != nil {
		return Repair{}, err
	}
	if err := r.controls.MarkRepairRunning(ctx, repairID); err != nil {
		return Repair{}, err
	}
	var repairErr error
	var verifiedSource SourceRow
	var verifiedTarget TargetRow
	repairErr = r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		source, sourceErr := readSourceForUpdate(ctx, tx, accountID)
		if sourceErr != nil {
			return sourceErr
		}
		currentMigration, migrationErr := r.migrationForTx(ctx, tx)
		if migrationErr != nil {
			return migrationErr
		}
		if !targetWriteStage(currentMigration) || !currentMigration.TargetWriteEnabled {
			return fmt.Errorf("balancev2: target repair stage changed before repair commit")
		}
		if repair.ExpectedSourceVersion != nil && source.SourceVersion < *repair.ExpectedSourceVersion {
			return fmt.Errorf("balancev2: source version regressed during repair")
		}
		target, transformErr := Transform(source, nil)
		if transformErr != nil {
			return transformErr
		}
		if err := replaceTarget(ctx, tx, target); err != nil {
			return err
		}
		persisted, readErr := targetForAccount(ctx, tx, accountID)
		if readErr != nil {
			return readErr
		}
		comparison := CompareRows(source, persisted)
		if comparison.Result != "match" {
			return fmt.Errorf("balancev2: repaired target did not verify")
		}
		verifiedSource = source
		verifiedTarget = *persisted
		return nil
	})
	if finishErr := r.controls.FinishRepair(ctx, repairID, repair.MismatchID, repairErr == nil, repairErrorCode(repairErr)); finishErr != nil && repairErr == nil {
		repairErr = finishErr
	}
	if repairErr != nil {
		repairsTotal.WithLabelValues(MigrationName, repair.RepairType, "failed").Inc()
		return Repair{}, repairErr
	}
	verifiedComparison := CompareRows(verifiedSource, &verifiedTarget)
	verifiedComparison.ResourceLayer = mismatchResourceLayer(mismatch)
	if compareErr := r.controls.RecordComparison(ctx, repair.MigrationID, verifiedComparison); compareErr != nil {
		r.logger.Error("balancev2: record post-repair verification failed", "error", compareErr)
		return Repair{}, compareErr
	}
	repairsTotal.WithLabelValues(MigrationName, repair.RepairType, "completed").Inc()
	return r.controls.GetRepair(ctx, repairID)
}

func repairErrorCode(err error) string {
	if err == nil {
		return ""
	}
	return "repair_failed"
}

func mismatchResourceLayer(mismatch Mismatch) string {
	const prefix = "sha256:ledger-account:"
	for _, layer := range []string{"source_target", "live_write", "source_ledger", "target_ledger"} {
		if mismatch.ResourcePublicKey == prefix+layer {
			return layer
		}
	}
	return "source_target"
}

func targetWriteStage(migration Migration) bool {
	state := migration.State
	if state == string(migrationkit.Paused) {
		state = migration.PreviousState
	}
	switch state {
	case string(migrationkit.DualWriteShadow), string(migrationkit.ShadowRead), string(migrationkit.CanaryRead), string(migrationkit.RampingRead), string(migrationkit.TargetPrimary), string(migrationkit.SourceWriteDisabled), string(migrationkit.Observation), string(migrationkit.Completed):
		return true
	default:
		return false
	}
}

func (r *Runtime) ReadBalance(ctx context.Context, accountID uuid.UUID, source balanceSourceFunc) (model.AccountBalance, error) {
	if !r.cfg.Enabled || r.cfg.EmergencySourceRead || r.cfg.DisableTargetWrites {
		return source(ctx, accountID)
	}
	migration, err := r.migration(ctx)
	if errors.Is(err, ErrNoActiveMigration) || errors.Is(err, ErrMigrationNotFound) {
		return source(ctx, accountID)
	}
	if err != nil {
		return source(ctx, accountID)
	}
	observeMigration(migration)
	paused := migration.State == string(migrationkit.Paused)
	effective := migration
	if paused {
		effective.State = string(migrationkit.ShadowRead)
		effective.ReadPercentageBasisPoints = 0
	}
	if !migrationkit.IsTargetPrimary(migrationkit.State(effective.State)) || effective.ReadPercentageBasisPoints <= 0 {
		value, sourceErr := source(ctx, accountID)
		if sourceErr != nil {
			return model.AccountBalance{}, sourceErr
		}
		value = r.attachSourceVersion(ctx, value)
		if !paused && shadowComparisonsAllowed(migrationkit.State(effective.State)) && shouldSample(accountID, migration.Name, effective.ShadowPercentageBasisPoints, r.cfg.ShadowSampleBasisPoints) {
			r.enqueue(shadowJob{migrationID: migration.ID, accountID: accountID, source: sourceRowFromBalance(value), readSource: source})
		}
		return value, nil
	}
	blocked, blockErr := r.controls.IsBlocked(ctx, migration.ID, accountID)
	if blockErr != nil {
		if migration.SourceFallbackEnabled && r.cfg.SourceFallback {
			sourceFallbackTotal.WithLabelValues(MigrationName, "control_plane_error").Inc()
			value, sourceErr := source(ctx, accountID)
			if sourceErr != nil {
				return model.AccountBalance{}, sourceErr
			}
			return r.attachSourceVersion(ctx, value), nil
		}
		return model.AccountBalance{}, blockErr
	}
	if blocked {
		sourceFallbackTotal.WithLabelValues(MigrationName, "open_mismatch").Inc()
		value, sourceErr := source(ctx, accountID)
		if sourceErr != nil {
			return model.AccountBalance{}, sourceErr
		}
		return r.attachSourceVersion(ctx, value), nil
	}
	targetCtx, cancel := context.WithTimeout(ctx, r.cfg.TargetReadTimeout)
	target, targetErr := targetForAccount(targetCtx, r.db, accountID)
	var sourceVersion int64
	versionErr := r.db.QueryRowContext(targetCtx, `SELECT version FROM account_balances WHERE account_id = $1`, accountID).Scan(&sourceVersion)
	cancel()
	if targetErr == nil && versionErr == nil && target != nil && target.SourceVersion >= 0 && target.SourceVersion == sourceVersion && ChecksumMatches(*target) && migrationkit.InCohort(accountID.String(), migration.Name, effective.ReadPercentageBasisPoints) {
		targetReadsTotal.WithLabelValues(MigrationName, "success").Inc()
		if shadowComparisonsAllowed(migrationkit.State(effective.State)) && shouldSample(accountID, migration.Name, effective.ShadowPercentageBasisPoints, r.cfg.ShadowSampleBasisPoints) {
			r.enqueue(shadowJob{migrationID: migration.ID, accountID: accountID, target: target, readSource: source, targetMode: true})
		}
		return target.ToBalance(), nil
	}
	reason := "target_error"
	if targetErr == nil && target == nil {
		reason = "target_missing"
	} else if versionErr != nil {
		reason = "source_version_check_error"
	} else if targetErr == nil && target != nil && target.SourceVersion != sourceVersion {
		reason = "version_mismatch"
	} else if targetErr == nil && target != nil && !ChecksumMatches(*target) {
		reason = "checksum_failure"
	} else if targetErr == nil && target != nil {
		reason = "ineligible_cohort"
	}
	targetReadsTotal.WithLabelValues(MigrationName, reason).Inc()
	if migration.SourceFallbackEnabled && r.cfg.SourceFallback {
		sourceFallbackTotal.WithLabelValues(MigrationName, reason).Inc()
		value, sourceErr := source(ctx, accountID)
		if sourceErr != nil {
			return model.AccountBalance{}, sourceErr
		}
		return r.attachSourceVersion(ctx, value), nil
	}
	if targetErr != nil {
		return model.AccountBalance{}, targetErr
	}
	return model.AccountBalance{}, fmt.Errorf("balancev2: target balance unavailable for %s", accountID)
}

func (r *Runtime) attachSourceVersion(ctx context.Context, value model.AccountBalance) model.AccountBalance {
	if value.AccountID == uuid.Nil {
		return value
	}
	var version int64
	if err := r.db.QueryRowContext(ctx, `SELECT version FROM account_balances WHERE account_id = $1`, value.AccountID).Scan(&version); err == nil {
		value.Version = version
	}
	return value
}

func sourceRowFromBalance(value model.AccountBalance) *SourceRow {
	return &SourceRow{
		AccountID: value.AccountID, Currency: value.Currency, AccountType: value.Type,
		Balance: value.Balance.IntPart(), AllowNegative: value.AllowNegative, SourceVersion: value.Version,
	}
}

func shouldSample(accountID uuid.UUID, name string, controlPercentage, localPercentage int) bool {
	if controlPercentage <= 0 || localPercentage <= 0 {
		return false
	}
	if localPercentage < controlPercentage {
		controlPercentage = localPercentage
	}
	return migrationkit.InCohort(accountID.String(), name, controlPercentage)
}

func shadowComparisonsAllowed(state migrationkit.State) bool {
	switch state {
	case migrationkit.Backfilling, migrationkit.DualWriteShadow,
		migrationkit.ShadowRead, migrationkit.CanaryRead,
		migrationkit.RampingRead, migrationkit.TargetPrimary,
		migrationkit.SourceWriteDisabled, migrationkit.Observation:
		return true
	default:
		return false
	}
}

func (r *Runtime) enqueue(job shadowJob) {
	if r.queue == nil || !r.allowComparison(job.accountID) {
		return
	}
	select {
	case r.queue <- job:
	default:
		shadowReadsTotal.WithLabelValues(MigrationName, "queue_dropped").Inc()
	}
}

func (r *Runtime) allowComparison(accountID uuid.UUID) bool {
	now := time.Now()
	r.limiterMu.Lock()
	defer r.limiterMu.Unlock()
	if r.limiterSecond.IsZero() || now.Sub(r.limiterSecond) >= time.Second {
		r.limiterSecond = now.Truncate(time.Second)
		r.comparisonsThis = 0
	}
	if r.comparisonsThis >= r.cfg.ShadowMaxRPS {
		shadowReadsTotal.WithLabelValues(MigrationName, "rate_limited").Inc()
		return false
	}
	key := accountID.String()
	if previous, ok := r.cooldown[key]; ok && now.Sub(previous) < r.cfg.ShadowPerAccountCooldown {
		return false
	}
	if len(r.cooldown) >= 10_000 {
		r.cooldown = make(map[string]time.Time)
	}
	r.cooldown[key] = now
	r.comparisonsThis++
	return true
}

func (r *Runtime) shadowWorker(parent context.Context) {
	defer r.wg.Done()
	for {
		select {
		case <-r.stop:
			return
		case <-parent.Done():
			return
		case job := <-r.queue:
			started := time.Now()
			runCtx, cancel := context.WithTimeout(context.Background(), r.cfg.ShadowTimeout)
			comparison := r.compareJob(runCtx, job)
			cancel()
			shadowCompareDuration.WithLabelValues(MigrationName, comparison.Result).Observe(time.Since(started).Seconds())
			shadowReadsTotal.WithLabelValues(MigrationName, comparison.Result).Inc()
			if err := r.controls.RecordComparison(context.Background(), job.migrationID, comparison); err != nil {
				r.logger.Error("balancev2: record shadow comparison failed", "error", err)
			} else if comparison.Severity == "critical" {
				r.Refresh()
			}
		}
	}
}

func (r *Runtime) lifecycleWorker(parent context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.WorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-parent.Done():
			return
		case <-ticker.C:
			if err := r.runLifecycleOnce(parent); err != nil && !errors.Is(err, ErrNoActiveMigration) && !errors.Is(err, ErrMigrationNotFound) {
				r.logger.Error("balancev2: lifecycle worker iteration failed", "error", err)
			}
		}
	}
}

func (r *Runtime) runLifecycleOnce(ctx context.Context) error {
	migration, err := r.migration(ctx)
	if err != nil {
		return err
	}
	switch migration.State {
	case string(migrationkit.Backfilling):
		return r.BackfillOnce(ctx)
	case string(migrationkit.DualWriteShadow), string(migrationkit.ShadowRead),
		string(migrationkit.CanaryRead), string(migrationkit.RampingRead),
		string(migrationkit.TargetPrimary), string(migrationkit.SourceWriteDisabled),
		string(migrationkit.Observation):
		return r.ReconcileOnce(ctx)
	default:
		return nil
	}
}

func (r *Runtime) compareJob(ctx context.Context, job shadowJob) Comparison {
	source := job.source
	if source == nil {
		value, err := job.readSource(ctx, job.accountID)
		if err != nil {
			return Comparison{AccountID: job.accountID, Result: "source_error", Classification: "source_error", Severity: "warning"}
		}
		value = r.attachSourceVersion(ctx, value)
		source = sourceRowFromBalance(value)
	}
	target := job.target
	if target == nil {
		var err error
		target, err = targetForAccount(ctx, r.db, job.accountID)
		if err != nil {
			return Comparison{AccountID: job.accountID, Result: "target_error", Classification: "target_error", Severity: "warning", SourceVersion: source.SourceVersion}
		}
	}
	return CompareRows(*source, target)
}
