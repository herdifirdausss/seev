// Package objectoutbox is the shared primitive docs/roadmap/active/51-a8-data-lifecycle-privacy.md
// T1.6 (K6) requires before any KYC/export object cleanup: a transactional
// outbox for deleting objects from an object store. K6's contract:
// "Object deletion uses an outbox: first persist a delete intent, then
// delete the encrypted object idempotently, then mark metadata
// redacted/deleted. A storage outage never causes metadata to claim that
// an object was removed."
//
// Enqueue persists the delete intent (a row in the owner's own
// `<owner>_object_delete_outbox` table, created by that owner's own
// migration — this package never creates schema). Worker drains it: for
// each pending row, call Store.Delete, and only on success mark the
// outbox row 'done' and run the caller-supplied metadata UPDATE — both in
// one Postgres transaction. A failure at any point (store or metadata
// update) leaves the row 'pending' for the next poll; Store.Delete must be
// idempotent (treat "not found" as success) since a prior attempt may have
// already removed the object before a later step failed.
package objectoutbox

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// DefaultBatchSize bounds one ProcessOnce call's claimed rows.
const DefaultBatchSize = 100

// outboxTablePattern is a defensive, Go-side constraint on the table name
// this package interpolates into SQL (never user input — every caller
// passes a compile-time constant), matching pkg/retentionworker's
// functionNamePattern convention.
var outboxTablePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*_object_delete_outbox$`)

// Store is the minimal capability Worker needs from an object store.
// Delete must be idempotent: deleting an already-absent key is success,
// not an error, since a prior attempt may have removed the object before
// a later step (the metadata update) failed.
type Store interface {
	Delete(ctx context.Context, key string) error
}

// Target binds one ref_table this owner's outbox can drain to the exact
// statement that marks that table's row deleted/redacted. MetadataUpdateSQL
// must take exactly one parameter, $1, the ref_id.
type Target struct {
	RefTable          string
	MetadataUpdateSQL string
}

// Row is one claimed outbox entry.
type Row struct {
	ID        uuid.UUID
	RefTable  string
	RefID     uuid.UUID
	ObjectKey string
	Attempts  int
}

// dbTxer is the minimal capability Worker needs — narrower than
// pkg/database.DatabaseSQL, matching this repo's own narrow-interface
// convention (pkg/retentionworker.dbQuerier, internal/payout/worker's
// resumer). WithTx matches database.DatabaseSQL's own transaction
// convention (auto rollback on error/panic, commit on success) rather than
// this package managing a raw *sql.Tx itself.
type dbTxer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) error
}

// Worker drains one owner's object-delete outbox. One Worker belongs to
// exactly one owner service, one Postgres database, and one Store — K6
// "each service owns its scheduler, repository call, and metrics for its
// tables" applies here the same as pkg/retentionworker.
type Worker struct {
	owner       string
	db          dbTxer
	store       Store
	targets     map[string]Target
	outboxTable string
	logger      *slog.Logger
	batchSize   int
}

// Option configures a Worker beyond its required fields.
type Option func(*Worker)

// WithLogger overrides the default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(w *Worker) { w.logger = l } }

// WithBatchSize overrides DefaultBatchSize.
func WithBatchSize(n int) Option { return func(w *Worker) { w.batchSize = n } }

// NewWorker validates outboxTable and every Target.RefTable up front (fail
// at construction, not at the first poll).
func NewWorker(owner string, db dbTxer, store Store, targets []Target, opts ...Option) (*Worker, error) {
	if owner == "" {
		return nil, fmt.Errorf("objectoutbox: owner is required")
	}
	if db == nil {
		return nil, fmt.Errorf("objectoutbox: db is required")
	}
	if store == nil {
		return nil, fmt.Errorf("objectoutbox: store is required")
	}
	outboxTable := owner + "_object_delete_outbox"
	if !outboxTablePattern.MatchString(outboxTable) {
		return nil, fmt.Errorf("objectoutbox: owner %q produces an invalid outbox table name %q", owner, outboxTable)
	}
	targetMap := make(map[string]Target, len(targets))
	for _, t := range targets {
		if t.RefTable == "" || t.MetadataUpdateSQL == "" {
			return nil, fmt.Errorf("objectoutbox: target with empty RefTable or MetadataUpdateSQL")
		}
		if _, exists := targetMap[t.RefTable]; exists {
			return nil, fmt.Errorf("objectoutbox: duplicate target %q", t.RefTable)
		}
		targetMap[t.RefTable] = t
	}
	w := &Worker{
		owner: owner, db: db, store: store, targets: targetMap,
		outboxTable: outboxTable, logger: slog.Default(), batchSize: DefaultBatchSize,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// Enqueue persists a delete intent: INSERT ... ON CONFLICT (ref_table,
// ref_id) DO NOTHING, so calling it more than once for the same row is
// always safe. Must be called against the same owner's outboxTable this
// Worker was constructed with — callers use the package-level Enqueue
// helper directly against their own transaction/db handle at the point
// they decide an object must go (e.g. inside a retention function's
// caller, or an admin action), which may be a different *sql.Tx than
// anything this Worker later uses to drain it.
func (w *Worker) Enqueue(ctx context.Context, refTable string, refID uuid.UUID, objectKey string) error {
	return Enqueue(ctx, w.db, w.owner, refTable, refID, objectKey)
}

// Enqueue is Worker.Enqueue without requiring a constructed Worker —
// usable directly from a request-scoped *sql.Tx by any code that decides
// an object must be deleted, even before that owner's Worker exists or is
// running.
func Enqueue(ctx context.Context, db interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, owner, refTable string, refID uuid.UUID, objectKey string) error {
	outboxTable := owner + "_object_delete_outbox"
	if !outboxTablePattern.MatchString(outboxTable) {
		return fmt.Errorf("objectoutbox: owner %q produces an invalid outbox table name %q", owner, outboxTable)
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (id, ref_table, ref_id, object_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (ref_table, ref_id) DO NOTHING`, outboxTable) //nolint:gosec // outboxTable is derived from a caller-supplied owner constant, validated above, never user input.
	_, err := db.ExecContext(ctx, query, uuid.New(), refTable, refID, objectKey)
	if err != nil {
		return fmt.Errorf("objectoutbox: enqueue %s/%s: %w", refTable, refID, err)
	}
	return nil
}

// ProcessOnce claims up to batchSize pending rows and drains each: delete
// from the store, then mark done + update metadata in one transaction. One
// row's failure does not stop the batch. Safe to call from multiple
// process replicas concurrently — claiming uses `FOR UPDATE SKIP LOCKED`.
func (w *Worker) ProcessOnce(ctx context.Context) (processed, failed int, err error) {
	rows, err := w.claimPending(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, row := range rows {
		if procErr := w.processRow(ctx, row); procErr != nil {
			failed++
			w.logger.Error("objectoutbox: process row failed",
				slog.String("owner", w.owner), slog.String("ref_table", row.RefTable),
				slog.String("ref_id", row.RefID.String()), slog.Any("error", procErr))
			continue
		}
		processed++
	}
	lastBatchFailedGauge.WithLabelValues(w.owner).Set(float64(failed))
	return processed, failed, nil
}

func (w *Worker) claimPending(ctx context.Context) ([]Row, error) {
	query := fmt.Sprintf(`
		WITH claimed AS (
			UPDATE %s
			SET status = 'processing', updated_at = now()
			WHERE id IN (
				SELECT id FROM %s
				WHERE status = 'pending'
				ORDER BY created_at ASC
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, ref_table, ref_id, object_key, attempts
		)
		SELECT id, ref_table, ref_id, object_key, attempts FROM claimed`,
		w.outboxTable, w.outboxTable) //nolint:gosec // w.outboxTable is validated against outboxTablePattern in NewWorker, never user input.
	sqlRows, err := w.db.QueryContext(ctx, query, w.batchSize)
	if err != nil {
		return nil, fmt.Errorf("objectoutbox: claim pending: %w", err)
	}
	defer sqlRows.Close()

	var rows []Row
	for sqlRows.Next() {
		var r Row
		if err := sqlRows.Scan(&r.ID, &r.RefTable, &r.RefID, &r.ObjectKey, &r.Attempts); err != nil {
			return nil, fmt.Errorf("objectoutbox: scan claimed row: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, sqlRows.Err()
}

func (w *Worker) processRow(ctx context.Context, row Row) error {
	target, ok := w.targets[row.RefTable]
	if !ok {
		return w.markFailed(ctx, row, fmt.Errorf("no registered Target for ref_table %q", row.RefTable))
	}
	if err := w.store.Delete(ctx, row.ObjectKey); err != nil {
		return w.markFailed(ctx, row, fmt.Errorf("store delete: %w", err))
	}

	markDoneQuery := fmt.Sprintf(`UPDATE %s SET status = 'done', updated_at = now() WHERE id = $1`, w.outboxTable) //nolint:gosec // w.outboxTable validated in NewWorker.
	err := w.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, markDoneQuery, row.ID); err != nil {
			return fmt.Errorf("mark outbox row done: %w", err)
		}
		if _, err := tx.ExecContext(ctx, target.MetadataUpdateSQL, row.RefID); err != nil {
			return fmt.Errorf("mark metadata deleted for %s/%s: %w", row.RefTable, row.RefID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	deletedTotal.WithLabelValues(w.owner, row.RefTable).Inc()
	return nil
}

// markFailed reverts a claimed row to 'pending' (never 'failed' —K6
// requires the object outage to be retried, not abandoned) with its
// attempt count and last error recorded for observability.
func (w *Worker) markFailed(ctx context.Context, row Row, cause error) error {
	query := fmt.Sprintf(`
		UPDATE %s SET status = 'pending', attempts = attempts + 1, last_error = $2, updated_at = now()
		WHERE id = $1`, w.outboxTable) //nolint:gosec // w.outboxTable validated in NewWorker.
	if _, err := w.db.ExecContext(ctx, query, row.ID, cause.Error()); err != nil {
		return fmt.Errorf("%w (and mark-failed itself errored: %s)", cause, err)
	}
	failuresTotal.WithLabelValues(w.owner, row.RefTable).Inc()
	return cause
}

// Start launches a background poll loop calling ProcessOnce every
// interval, matching internal/ledger/worker.OutboxRelay's Start/Stop
// lifecycle shape. Call the returned stop func to cancel and wait for the
// in-flight batch to finish.
func (w *Worker) Start(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, _, err := w.ProcessOnce(ctx); err != nil {
					w.logger.Error("objectoutbox: process once failed", slog.String("owner", w.owner), slog.Any("error", err))
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
