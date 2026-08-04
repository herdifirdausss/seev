// Package migrationkit contains owner-independent mechanics for service-owned
// live migrations. It has no knowledge of Ledger tables or transforms.
package migrationkit

import "errors"

var (
	ErrInvalidTransition = errors.New("migrationkit: invalid state transition")
	ErrInvalidPercentage = errors.New("migrationkit: invalid rollout percentage")
	ErrPaused            = errors.New("migrationkit: migration is paused")
)
