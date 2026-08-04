// Package balancev2 owns Ledger's reference C6 migration. It contains the
// schema-specific transform, repositories, control plane, and read/write
// runtime. Generic lifecycle mechanics live in internal/platform/migration.
package balancev2

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/shopspring/decimal"
)

const (
	MigrationName    = "ledger-balance-projection-v1-v2"
	Resource         = "ledger_balance_projection"
	SourceVersion    = "v1"
	TargetVersion    = "v2"
	TransformVersion = 1
)

const (
	FieldAvailable = 1 << iota
	FieldReserved
	FieldPending
	FieldRestricted
	FieldCurrency
	FieldVersion
	FieldChecksum
	FieldTargetMissing
	FieldAccountType
	FieldAllowNegative
)

const (
	ClassificationBackfillMissing      = "backfill_missing"
	ClassificationStaleBackfill        = "stale_backfill"
	ClassificationLiveWriteGap         = "live_write_gap"
	ClassificationTransformBug         = "transform_bug"
	ClassificationTargetCorruption     = "target_corruption"
	ClassificationSourceCorruption     = "source_corruption"
	ClassificationSharedProjectionBug  = "shared_projection_bug"
	ClassificationVersionRegression    = "version_regression"
	ClassificationUnsupportedLegacyRow = "unsupported_legacy_row"
)

const (
	MismatchOpen          = "open"
	MismatchClassified    = "classified"
	MismatchRepairPending = "repair_pending"
	MismatchRepairing     = "repairing"
	MismatchRepaired      = "repaired"
	MismatchVerified      = "verified"
	MismatchIgnored       = "ignored_with_reason"
	MismatchBlocked       = "blocked"
)

const (
	RunSample      = "sample"
	RunBucket      = "bucket"
	RunFull        = "full"
	RunIncident    = "incident"
	RunPreCutover  = "pre_cutover"
	RunPostCutover = "post_cutover"
)

// Config is the bounded local safety configuration. DATA_MIGRATION_ENABLED
// is deliberately false by default; database state cannot make a disabled
// process perform migration work.
type Config struct {
	Enabled                  bool
	EmergencySourceRead      bool
	DisableTargetWrites      bool
	BackfillBatchSize        int
	BackfillWorkers          int
	BackfillSleep            time.Duration
	BackfillStatementTimeout time.Duration
	BackfillLockTimeout      time.Duration
	ShadowWorkers            int
	ShadowQueueSize          int
	ShadowTimeout            time.Duration
	ShadowSampleBasisPoints  int
	ShadowMaxRPS             int
	ShadowPerAccountCooldown time.Duration
	TargetReadTimeout        time.Duration
	SourceFallback           bool
	SourceFallbackConfigured bool
	ReconcileBatchSize       int
	RepairBatchSize          int
	RepairWorkers            int
	WorkerInterval           time.Duration
	BaselineCommit           string
}

func (c Config) withDefaults() Config {
	if c.BackfillBatchSize <= 0 {
		c.BackfillBatchSize = 100
	}
	if c.BackfillWorkers <= 0 {
		c.BackfillWorkers = 1
	}
	if c.BackfillSleep <= 0 {
		c.BackfillSleep = 50 * time.Millisecond
	}
	if c.BackfillStatementTimeout <= 0 {
		c.BackfillStatementTimeout = 5 * time.Second
	}
	if c.BackfillLockTimeout <= 0 {
		c.BackfillLockTimeout = 500 * time.Millisecond
	}
	if c.ShadowWorkers <= 0 {
		c.ShadowWorkers = 4
	}
	if c.ShadowQueueSize <= 0 {
		c.ShadowQueueSize = 1000
	}
	if c.ShadowTimeout <= 0 {
		c.ShadowTimeout = 50 * time.Millisecond
	}
	if c.ShadowSampleBasisPoints <= 0 {
		c.ShadowSampleBasisPoints = 10000
	}
	if c.ShadowMaxRPS <= 0 {
		c.ShadowMaxRPS = 100
	}
	if c.ShadowPerAccountCooldown <= 0 {
		c.ShadowPerAccountCooldown = time.Minute
	}
	if c.TargetReadTimeout <= 0 {
		c.TargetReadTimeout = 50 * time.Millisecond
	}
	if c.ReconcileBatchSize <= 0 {
		c.ReconcileBatchSize = 100
	}
	if c.RepairBatchSize <= 0 {
		c.RepairBatchSize = 50
	}
	if c.RepairWorkers <= 0 {
		c.RepairWorkers = 1
	}
	if c.WorkerInterval <= 0 {
		c.WorkerInterval = time.Second
	}
	if c.BaselineCommit == "" {
		c.BaselineCommit = "unknown"
	}
	if c.BackfillBatchSize > 1000 {
		c.BackfillBatchSize = 1000
	}
	if c.BackfillWorkers > 16 {
		c.BackfillWorkers = 16
	}
	if c.ShadowWorkers > 32 {
		c.ShadowWorkers = 32
	}
	if c.ShadowQueueSize > 10_000 {
		c.ShadowQueueSize = 10_000
	}
	if c.ShadowSampleBasisPoints > 10_000 {
		c.ShadowSampleBasisPoints = 10_000
	}
	if c.ShadowMaxRPS > 10_000 {
		c.ShadowMaxRPS = 10_000
	}
	if c.ReconcileBatchSize > 1000 {
		c.ReconcileBatchSize = 1000
	}
	if c.RepairBatchSize > 500 {
		c.RepairBatchSize = 500
	}
	if c.RepairWorkers > 16 {
		c.RepairWorkers = 16
	}
	// The safe default is source fallback. The composition root marks the
	// setting configured only after parsing the explicit operator flag.
	if !c.SourceFallbackConfigured {
		c.SourceFallback = true
		c.EmergencySourceRead = true
	}
	return c
}

// SourceRow is the exact v1 source shape consumed by the transform. The
// account type determines which semantic v2 amount receives the source
// balance; immutable ledger entries remain the financial authority.
type SourceRow struct {
	AccountID     uuid.UUID
	Currency      string
	AccountType   string
	Balance       int64
	AllowNegative bool
	SourceVersion int64
	UpdatedAt     time.Time
}

type TargetRow struct {
	AccountID          uuid.UUID
	AccountType        string
	Currency           string
	Status             string
	AllowNegative      bool
	AvailableAmount    int64
	ReservedAmount     int64
	PendingAmount      int64
	RestrictedAmount   int64
	SourceVersion      int64
	LastTransactionID  *uuid.UUID
	ProjectionChecksum []byte
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (t TargetRow) Balance() int64 {
	switch t.AccountType {
	case "hold":
		return t.ReservedAmount
	case "pending":
		return t.PendingAmount
	case "frozen":
		return t.RestrictedAmount
	default:
		return t.AvailableAmount
	}
}

func (t TargetRow) ToBalance() model.AccountBalance {
	return model.AccountBalance{
		AccountID:     t.AccountID,
		Currency:      strings.ToUpper(strings.TrimSpace(t.Currency)),
		Balance:       decimal.NewFromInt(t.Balance()),
		Status:        t.Status,
		Type:          t.AccountType,
		AllowNegative: t.AllowNegative,
		Version:       t.SourceVersion,
	}
}

// Migration is the durable operator-facing control record.
type Migration struct {
	ID                          uuid.UUID  `json:"id"`
	PublicID                    string     `json:"public_id"`
	Name                        string     `json:"name"`
	Resource                    string     `json:"resource"`
	SourceVersion               string     `json:"source_version"`
	TargetVersion               string     `json:"target_version"`
	State                       string     `json:"state"`
	PreviousState               string     `json:"previous_state,omitempty"`
	ReadPercentageBasisPoints   int        `json:"read_percentage_basis_points"`
	ShadowPercentageBasisPoints int        `json:"shadow_percentage_basis_points"`
	StrictDualWrite             bool       `json:"strict_dual_write"`
	SourceFallbackEnabled       bool       `json:"source_fallback_enabled"`
	SourceWriteEnabled          bool       `json:"source_write_enabled"`
	TargetWriteEnabled          bool       `json:"target_write_enabled"`
	TransformVersion            int        `json:"transform_version"`
	BaselineCommit              string     `json:"baseline_commit"`
	CreatedBy                   string     `json:"created_by"`
	UpdatedBy                   string     `json:"updated_by"`
	PauseReason                 string     `json:"pause_reason,omitempty"`
	FailureCode                 string     `json:"failure_code,omitempty"`
	StartedAt                   *time.Time `json:"started_at,omitempty"`
	BackfillCompletedAt         *time.Time `json:"backfill_completed_at,omitempty"`
	CutoverStartedAt            *time.Time `json:"cutover_started_at,omitempty"`
	CompletedAt                 *time.Time `json:"completed_at,omitempty"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
	Version                     int64      `json:"version"`
}

type Checkpoint struct {
	ID               uuid.UUID  `json:"id"`
	MigrationID      uuid.UUID  `json:"migration_id"`
	WorkerKind       string     `json:"worker_kind"`
	PartitionKey     string     `json:"partition_key"`
	LastSourceKey    string     `json:"last_source_key,omitempty"`
	WatermarkVersion *int64     `json:"watermark_version,omitempty"`
	ProcessedCount   int64      `json:"processed_count"`
	UpdatedCount     int64      `json:"updated_count"`
	SkippedCount     int64      `json:"skipped_count"`
	FailedCount      int64      `json:"failed_count"`
	LeaseOwner       string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt   *time.Time `json:"lease_expires_at,omitempty"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Run struct {
	ID             uuid.UUID      `json:"id"`
	MigrationID    uuid.UUID      `json:"migration_id"`
	RunType        string         `json:"run_type"`
	Status         string         `json:"status"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
	SourceCutoff   string         `json:"source_cutoff,omitempty"`
	TargetCutoff   string         `json:"target_cutoff,omitempty"`
	ProcessedCount int64          `json:"processed_count"`
	MatchCount     int64          `json:"match_count"`
	MismatchCount  int64          `json:"mismatch_count"`
	ErrorCount     int64          `json:"error_count"`
	Evidence       map[string]any `json:"evidence,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type Mismatch struct {
	ID                 uuid.UUID `json:"id"`
	MigrationID        uuid.UUID `json:"migration_id"`
	ResourceKeyHash    []byte    `json:"-"`
	ResourcePublicKey  string    `json:"resource_public_key,omitempty"`
	Classification     string    `json:"classification,omitempty"`
	Status             string    `json:"status"`
	Severity           string    `json:"severity"`
	FieldMask          int64     `json:"field_mask"`
	SourceVersion      *int64    `json:"source_version,omitempty"`
	TargetVersion      *int64    `json:"target_version,omitempty"`
	SourceChecksum     []byte    `json:"-"`
	TargetChecksum     []byte    `json:"-"`
	OccurrenceCount    int64     `json:"occurrence_count"`
	FirstSeenAt        time.Time `json:"first_seen_at"`
	LastSeenAt         time.Time `json:"last_seen_at"`
	RepairAttemptCount int       `json:"repair_attempt_count"`
	LastErrorCode      string    `json:"last_error_code,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type Repair struct {
	ID                    uuid.UUID  `json:"id"`
	MigrationID           uuid.UUID  `json:"migration_id"`
	MismatchID            uuid.UUID  `json:"mismatch_id"`
	ResourceKeyHash       []byte     `json:"-"`
	RepairType            string     `json:"repair_type"`
	ExpectedSourceVersion *int64     `json:"expected_source_version,omitempty"`
	Status                string     `json:"status"`
	AttemptCount          int        `json:"attempt_count"`
	CreatedBy             string     `json:"created_by"`
	ApprovedBy            string     `json:"approved_by,omitempty"`
	Reason                string     `json:"reason"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	ErrorCode             string     `json:"error_code,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type Transition struct {
	ID               uuid.UUID      `json:"id"`
	MigrationID      uuid.UUID      `json:"migration_id"`
	FromState        string         `json:"from_state"`
	ToState          string         `json:"to_state"`
	RequestedBy      string         `json:"requested_by"`
	ApprovedBy       string         `json:"approved_by,omitempty"`
	Reason           string         `json:"reason"`
	EvidenceSnapshot map[string]any `json:"evidence_snapshot"`
	CreatedAt        time.Time      `json:"created_at"`
}

type Comparison struct {
	AccountID uuid.UUID
	// ResourceLayer separates the source↔target projection check from the
	// source↔immutable-ledger check. It is intentionally bounded and is never
	// exposed as an arbitrary caller-controlled label.
	ResourceLayer    string
	Result           string
	Classification   string
	Severity         string
	FieldMask        int64
	SourceVersion    int64
	TargetVersion    int64
	TargetVersionSet bool
	SourceChecksum   []byte
	TargetChecksum   []byte
	ErrorCode        string
}

type GateSnapshot struct {
	Passed                bool      `json:"passed"`
	FreshAt               time.Time `json:"fresh_at"`
	UnresolvedCritical    int64     `json:"unresolved_critical"`
	TargetMissingEligible int64     `json:"target_missing_eligible"`
	ShadowComparisons     int64     `json:"shadow_comparisons"`
	ShadowMatches         int64     `json:"shadow_matches"`
	ComparisonErrors      int64     `json:"comparison_errors"`
	ShadowSuccessRatio    float64   `json:"shadow_success_ratio"`
	FallbackRate          float64   `json:"fallback_rate"`
	TargetCoverageRatio   float64   `json:"target_coverage_ratio"`
	LatestReconciliation  string    `json:"latest_reconciliation"`
	BackupFresh           bool      `json:"backup_fresh"`
	PreCutoverComplete    bool      `json:"pre_cutover_complete"`
	Reason                string    `json:"reason,omitempty"`
}
