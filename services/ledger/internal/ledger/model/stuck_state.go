package model

import "time"

// StuckStateSnapshot is the small operational view used by the scanner. It
// contains no payloads or customer data.
type StuckStateSnapshot struct {
	OutboxPendingCount      int
	OutboxOldestPendingAt   time.Time
	ScheduleDueCount        int
	ScheduleProcessingCount int
	DisputeDueCount         int
}
