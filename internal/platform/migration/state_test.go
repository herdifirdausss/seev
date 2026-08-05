package migrationkit

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// allStates enumerates every declared State so tests can assert against the
// full lifecycle without silently skipping a state added later.
var allStates = []State{
	Draft, Validated, TargetReady, Backfilling, DualWriteShadow, ShadowRead,
	CanaryRead, RampingRead, TargetPrimary, SourceWriteDisabled, Observation,
	Completed, Paused, RollingBack, RolledBack, Failed, CancelledBeforeWrite,
}

func TestValidateTransition_HappyPathSequence(t *testing.T) {
	sequence := []State{
		Draft, Validated, TargetReady, Backfilling, DualWriteShadow, ShadowRead,
		CanaryRead, RampingRead, TargetPrimary, SourceWriteDisabled, Observation,
		Completed,
	}
	for i := 0; i < len(sequence)-1; i++ {
		require.NoErrorf(t, ValidateTransition(sequence[i], sequence[i+1]),
			"%s -> %s must be allowed", sequence[i], sequence[i+1])
	}
}

func TestValidateTransition_RejectsSelfTransition(t *testing.T) {
	for _, s := range allStates {
		err := ValidateTransition(s, s)
		require.Errorf(t, err, "%s -> %s (self) must be rejected", s, s)
		require.True(t, errors.Is(err, ErrInvalidTransition))
	}
}

func TestValidateTransition_RejectsFromTerminalStates(t *testing.T) {
	for _, terminal := range []State{Completed, RolledBack, CancelledBeforeWrite} {
		for _, to := range allStates {
			if to == terminal {
				continue
			}
			err := ValidateTransition(terminal, to)
			require.Errorf(t, err, "%s -> %s must be rejected: %s is terminal", terminal, to, terminal)
		}
	}
}

func TestValidateTransition_RejectsUnlistedTransition(t *testing.T) {
	cases := []struct{ from, to State }{
		{Draft, Backfilling},
		{Draft, TargetPrimary},
		{Backfilling, CanaryRead},
		{ShadowRead, TargetPrimary},
		{TargetPrimary, Draft},
		{Observation, Backfilling},
	}
	for _, c := range cases {
		err := ValidateTransition(c.from, c.to)
		require.Errorf(t, err, "%s -> %s must be rejected", c.from, c.to)
		require.True(t, errors.Is(err, ErrInvalidTransition))
	}
}

func TestValidateTransition_PausedResumesToAnyActiveState(t *testing.T) {
	// §7.4: paused stores the previous active state; resuming is a return to
	// that state, not a fixed allowlist entry like the forward lifecycle.
	for _, to := range allStates {
		err := ValidateTransition(Paused, to)
		if to == Paused || IsTerminal(to) {
			require.Errorf(t, err, "Paused -> %s must be rejected", to)
			continue
		}
		require.NoErrorf(t, err, "Paused -> %s (active) must be allowed", to)
	}
}

func TestValidateTransition_RollbackReachableFromEveryNonTerminalState(t *testing.T) {
	for _, from := range allStates {
		if IsTerminal(from) || !IsActive(from) || from == RollingBack {
			continue
		}
		err := ValidateTransition(from, RollingBack)
		require.NoErrorf(t, err, "%s -> RollingBack must be allowed (§7.2 rollback exists from every active state)", from)
	}
}

func TestValidateTransition_FailedOnlyResumesOrRollsBack(t *testing.T) {
	require.NoError(t, ValidateTransition(Failed, RollingBack))
	require.NoError(t, ValidateTransition(Failed, Paused))
	require.Error(t, ValidateTransition(Failed, TargetPrimary))
	require.Error(t, ValidateTransition(Failed, Completed))
}

func TestIsSourcePrimary_IsTargetPrimary(t *testing.T) {
	targetPrimaryStates := map[State]bool{
		CanaryRead: true, RampingRead: true, TargetPrimary: true,
		SourceWriteDisabled: true, Observation: true, Completed: true,
	}
	for _, s := range allStates {
		wantTargetPrimary := targetPrimaryStates[s]
		require.Equalf(t, wantTargetPrimary, IsTargetPrimary(s), "IsTargetPrimary(%s)", s)
		require.Equalf(t, !wantTargetPrimary, IsSourcePrimary(s), "IsSourcePrimary(%s)", s)
	}
}

func TestRequiresGate(t *testing.T) {
	gated := map[State]bool{
		ShadowRead: true, CanaryRead: true, RampingRead: true, TargetPrimary: true,
		SourceWriteDisabled: true, Observation: true, Completed: true,
	}
	for _, s := range allStates {
		require.Equalf(t, gated[s], RequiresGate(s), "RequiresGate(%s)", s)
	}
}

func TestRequiresChecker_DangerousDestinationsAlwaysRequireChecker(t *testing.T) {
	// §15.4: maker/checker required for source write disable and source
	// retirement stages regardless of the current read percentage.
	for _, to := range []State{SourceWriteDisabled, Observation, Completed, RollingBack} {
		require.True(t, RequiresChecker(RampingRead, to, 0), "%s must always require a checker", to)
	}
}

func TestRequiresChecker_PauseAndResumeNeverRequireChecker(t *testing.T) {
	require.False(t, RequiresChecker(RampingRead, Paused, 9999))
	require.False(t, RequiresChecker(Paused, RampingRead, 9999))
}

func TestRequiresChecker_RampThreshold(t *testing.T) {
	// §15.4: 25% -> 50% and 50% -> 100% require a checker; below that an
	// operator may act alone.
	require.False(t, RequiresChecker(RampingRead, RampingRead, 2500), "25% ramp must not require a checker on its own")
	require.True(t, RequiresChecker(RampingRead, RampingRead, 2501), "just above 25% must require a checker")
	require.True(t, RequiresChecker(RampingRead, TargetPrimary, 5000), "ramp to 50% from RampingRead must require a checker")
}
