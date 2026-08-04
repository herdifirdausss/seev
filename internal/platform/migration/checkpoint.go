package migrationkit

import "time"

// Checkpoint is the owner-independent shape persisted by a resumable worker.
// LastSourceKey is intentionally opaque: the owner chooses its key encoding.
type Checkpoint struct {
	ID               string
	MigrationID      string
	WorkerKind       string
	PartitionKey     string
	LastSourceKey    string
	WatermarkVersion int64
	ProcessedCount   int64
	UpdatedCount     int64
	SkippedCount     int64
	FailedCount      int64
	LeaseOwner       string
	LeaseExpiresAt   time.Time
	Status           string
	UpdatedAt        time.Time
}

const (
	CheckpointPending   = "pending"
	CheckpointRunning   = "running"
	CheckpointCompleted = "completed"
	CheckpointFailed    = "failed"
)
