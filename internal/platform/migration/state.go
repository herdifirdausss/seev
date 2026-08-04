package migrationkit

import "fmt"

// State is the durable lifecycle state of an owner-controlled migration.
type State string

const (
	Draft                State = "draft"
	Validated            State = "validated"
	TargetReady          State = "target_ready"
	Backfilling          State = "backfilling"
	DualWriteShadow      State = "dual_write_shadow"
	ShadowRead           State = "shadow_read"
	CanaryRead           State = "canary_read"
	RampingRead          State = "ramping_read"
	TargetPrimary        State = "target_primary"
	SourceWriteDisabled  State = "source_write_disabled"
	Observation          State = "observation"
	Completed            State = "completed"
	Paused               State = "paused"
	RollingBack          State = "rolling_back"
	RolledBack           State = "rolled_back"
	Failed               State = "failed"
	CancelledBeforeWrite State = "cancelled_before_write"
)

var terminalStates = map[State]bool{
	Completed:            true,
	RolledBack:           true,
	CancelledBeforeWrite: true,
}

var activeStates = map[State]bool{
	Draft:               true,
	Validated:           true,
	TargetReady:         true,
	Backfilling:         true,
	DualWriteShadow:     true,
	ShadowRead:          true,
	CanaryRead:          true,
	RampingRead:         true,
	TargetPrimary:       true,
	SourceWriteDisabled: true,
	Observation:         true,
	Paused:              true,
	RollingBack:         true,
	Failed:              true,
}

var allowedTransitions = map[State]map[State]bool{
	Draft: {
		Validated: true, CancelledBeforeWrite: true, Paused: true, RollingBack: true, Failed: true,
	},
	Validated: {
		TargetReady: true, CancelledBeforeWrite: true, Paused: true, RollingBack: true, Failed: true,
	},
	TargetReady: {
		Backfilling: true, CancelledBeforeWrite: true, Paused: true, RollingBack: true, Failed: true,
	},
	Backfilling: {
		DualWriteShadow: true, Paused: true, RollingBack: true, Failed: true,
	},
	DualWriteShadow: {
		ShadowRead: true, Paused: true, RollingBack: true, Failed: true,
	},
	ShadowRead: {
		CanaryRead: true, Paused: true, RollingBack: true, Failed: true,
	},
	CanaryRead: {
		RampingRead: true, ShadowRead: true, Paused: true, RollingBack: true, Failed: true,
	},
	RampingRead: {
		TargetPrimary: true, CanaryRead: true, Paused: true, RollingBack: true, Failed: true,
	},
	TargetPrimary: {
		SourceWriteDisabled: true, Observation: true, RampingRead: true, Paused: true, RollingBack: true, Failed: true,
	},
	SourceWriteDisabled: {
		Observation: true, TargetPrimary: true, Paused: true, RollingBack: true, Failed: true,
	},
	Observation: {
		Completed: true, TargetPrimary: true, Paused: true, RollingBack: true, Failed: true,
	},
	Paused: {
		RollingBack: true, Failed: true,
	},
	RollingBack: {
		RolledBack: true, Failed: true,
	},
	Failed: {
		RollingBack: true, Paused: true,
	},
}

// ValidateTransition rejects transitions not explicitly authorized by the
// C6 state machine. Persistence and optimistic concurrency belong to the
// owner-specific control repository.
func ValidateTransition(from, to State) error {
	if from == to || terminalStates[from] || !activeStates[from] {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	if from == Paused && activeStates[to] {
		return nil
	}
	if allowedTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

func IsTerminal(state State) bool { return terminalStates[state] }

func IsActive(state State) bool { return activeStates[state] }

func IsSourcePrimary(state State) bool {
	return state != CanaryRead && state != RampingRead && state != TargetPrimary && state != SourceWriteDisabled && state != Observation && state != Completed
}

func IsTargetPrimary(state State) bool {
	return state == CanaryRead || state == RampingRead || state == TargetPrimary || state == SourceWriteDisabled || state == Observation || state == Completed
}

// RequiresGate reports whether entering a state requires a fresh owner-side
// evidence snapshot. This is separate from RequiresChecker: an operator may
// be allowed to request a transition while the transition is still blocked
// by reconciliation, coverage, or backup evidence.
func RequiresGate(state State) bool {
	switch state {
	case ShadowRead, CanaryRead, RampingRead, TargetPrimary,
		SourceWriteDisabled, Observation, Completed:
		return true
	default:
		return false
	}
}

// RequiresChecker reports whether an operation must be performed by a checker
// or an explicitly privileged superuser.
func RequiresChecker(from, to State, readPercentageBasisPoints int) bool {
	if to == SourceWriteDisabled || to == Observation || to == Completed || to == RollingBack {
		return true
	}
	if to == Paused || from == Paused {
		return false
	}
	if from == RampingRead && readPercentageBasisPoints >= 5000 {
		return true
	}
	return readPercentageBasisPoints > 2500
}
