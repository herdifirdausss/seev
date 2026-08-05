package schedule

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// ─── NormalizePolicy ────────────────────────────────────────────────────────

func TestNormalizePolicy_DefaultsFilled(t *testing.T) {
	got, err := NormalizePolicy("daily", model.ScheduledPolicy{})
	require.NoError(t, err)
	assert.Equal(t, model.ScheduleMissedSkip, got.MissedRunPolicy)
	assert.Equal(t, defaultCatchUpLimit, got.CatchUpLimit)
	assert.Equal(t, defaultInfrastructureAttempts, got.MaxInfrastructureAttempts)
	assert.Equal(t, int64(defaultRetryWindow/time.Second), got.RetryWindowSeconds)
	assert.Equal(t, defaultFailureThreshold, got.ConsecutiveFailureThreshold)
	assert.Equal(t, "current_policy_with_consent_cap", got.FeeMode)
}

func TestNormalizePolicy_MonthlyDefaultIsRunOnceLatest(t *testing.T) {
	got, err := NormalizePolicy("monthly", model.ScheduledPolicy{})
	require.NoError(t, err)
	assert.Equal(t, model.ScheduleMissedRunOnceLatest, got.MissedRunPolicy)
}

func TestNormalizePolicy_InvalidMissedRunPolicy(t *testing.T) {
	_, err := NormalizePolicy("daily", model.ScheduledPolicy{
		MissedRunPolicy:             "teleport",
		CatchUpLimit:                1,
		MaxInfrastructureAttempts:   1,
		RetryWindowSeconds:          1,
		ConsecutiveFailureThreshold: 1,
		FeeMode:                     "current_policy_with_consent_cap",
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestNormalizePolicy_CatchUpLimitBounds(t *testing.T) {
	good := model.ScheduledPolicy{
		MissedRunPolicy:             model.ScheduleMissedRunOnceLatest,
		MaxInfrastructureAttempts:   1,
		RetryWindowSeconds:          1,
		ConsecutiveFailureThreshold: 1,
		FeeMode:                     "current_policy_with_consent_cap",
	}
	cases := []struct {
		limit   int
		wantErr bool
	}{
		{-1, true},
		{0, false},
		{7, false},
		{8, true},
	}
	for _, c := range cases {
		p := good
		p.CatchUpLimit = c.limit
		_, err := NormalizePolicy("daily", p)
		if c.wantErr {
			assert.ErrorIs(t, err, apperror.ErrValidation, "limit=%d", c.limit)
		} else {
			assert.NoError(t, err, "limit=%d", c.limit)
		}
	}
}

func TestNormalizePolicy_MaxInfrastructureAttemptsBounds(t *testing.T) {
	// Note: 0 gets replaced by the default before validation, so the lower
	// invalid value is -1 (not 0).
	good := model.ScheduledPolicy{
		MissedRunPolicy:             model.ScheduleMissedRunOnceLatest,
		CatchUpLimit:                1,
		RetryWindowSeconds:          1,
		ConsecutiveFailureThreshold: 1,
		FeeMode:                     "current_policy_with_consent_cap",
	}
	cases := []struct {
		attempts int
		wantErr  bool
	}{
		{-1, true},
		{1, false},
		{20, false},
		{21, true},
	}
	for _, c := range cases {
		p := good
		p.MaxInfrastructureAttempts = c.attempts
		_, err := NormalizePolicy("daily", p)
		if c.wantErr {
			assert.ErrorIs(t, err, apperror.ErrValidation, "attempts=%d", c.attempts)
		} else {
			assert.NoError(t, err, "attempts=%d", c.attempts)
		}
	}
}

func TestNormalizePolicy_RetryWindowMustBePositive(t *testing.T) {
	// -1 is used because 0 is treated as "unset" and filled with the default.
	_, err := NormalizePolicy("daily", model.ScheduledPolicy{
		MissedRunPolicy:             model.ScheduleMissedRunOnceLatest,
		CatchUpLimit:                1,
		MaxInfrastructureAttempts:   1,
		RetryWindowSeconds:          -1,
		ConsecutiveFailureThreshold: 1,
		FeeMode:                     "current_policy_with_consent_cap",
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestNormalizePolicy_ConsecutiveFailureThresholdBounds(t *testing.T) {
	// Note: 0 gets replaced by the default before validation.
	good := model.ScheduledPolicy{
		MissedRunPolicy:           model.ScheduleMissedRunOnceLatest,
		CatchUpLimit:              1,
		MaxInfrastructureAttempts: 1,
		RetryWindowSeconds:        1,
		FeeMode:                   "current_policy_with_consent_cap",
	}
	cases := []struct {
		threshold int
		wantErr   bool
	}{
		{-1, true},
		{1, false},
		{20, false},
		{21, true},
	}
	for _, c := range cases {
		p := good
		p.ConsecutiveFailureThreshold = c.threshold
		_, err := NormalizePolicy("daily", p)
		if c.wantErr {
			assert.ErrorIs(t, err, apperror.ErrValidation, "threshold=%d", c.threshold)
		} else {
			assert.NoError(t, err, "threshold=%d", c.threshold)
		}
	}
}

func TestNormalizePolicy_NegativeMaxFeeAmountRejected(t *testing.T) {
	neg := int64(-1)
	_, err := NormalizePolicy("daily", model.ScheduledPolicy{
		MissedRunPolicy:             model.ScheduleMissedRunOnceLatest,
		CatchUpLimit:                1,
		MaxInfrastructureAttempts:   1,
		RetryWindowSeconds:          1,
		ConsecutiveFailureThreshold: 1,
		FeeMode:                     "current_policy_with_consent_cap",
		MaxFeeAmount:                &neg,
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestNormalizePolicy_UnsupportedFeeModeRejected(t *testing.T) {
	_, err := NormalizePolicy("daily", model.ScheduledPolicy{
		MissedRunPolicy:             model.ScheduleMissedRunOnceLatest,
		CatchUpLimit:                1,
		MaxInfrastructureAttempts:   1,
		RetryWindowSeconds:          1,
		ConsecutiveFailureThreshold: 1,
		FeeMode:                     "best_guess",
	})
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

// ─── PlanMissed ─────────────────────────────────────────────────────────────

// validPolicy is a fully-populated policy for PlanMissed tests — tests override
// only the field under test rather than repeating every field.
func validPolicy(missedRunPolicy string, catchUpLimit int) model.ScheduledPolicy {
	return model.ScheduledPolicy{
		MissedRunPolicy:             missedRunPolicy,
		CatchUpLimit:                catchUpLimit,
		MaxInfrastructureAttempts:   5,
		RetryWindowSeconds:          86400,
		ConsecutiveFailureThreshold: 3,
		FeeMode:                     "current_policy_with_consent_cap",
	}
}

func TestPlanMissed_Skip_RunsLatestOnly(t *testing.T) {
	// 3 due daily dates; skip policy should Planned=[latest] Skipped=[first 2]
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	plan, err := PlanMissed("daily", start, asOf, nil, validPolicy(model.ScheduleMissedSkip, 7), time.UTC)
	require.NoError(t, err)
	assert.Len(t, plan.Planned, 1, "skip must only plan the latest date")
	assert.Len(t, plan.Skipped, 2, "skip must mark the earlier dates as skipped")
	assert.Equal(t, asOf, plan.Planned[0])
}

func TestPlanMissed_Skip_NothingDue_OnlySkipped(t *testing.T) {
	// asOf is earlier than the latest date so the latest date is not today.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) // latest is Jan 2, asOf = Jan 2 — should plan 1
	plan, err := PlanMissed("daily", start, asOf, nil, validPolicy(model.ScheduleMissedSkip, 7), time.UTC)
	require.NoError(t, err)
	// Jan 1 and Jan 2 both <= asOf; latest is Jan 2 which equals asOf
	assert.Len(t, plan.Planned, 1)
	assert.Len(t, plan.Skipped, 1)
}

func TestPlanMissed_RunOnceLatest_PlansLatest(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)

	plan, err := PlanMissed("daily", start, asOf, nil, validPolicy(model.ScheduleMissedRunOnceLatest, 7), time.UTC)
	require.NoError(t, err)
	assert.Len(t, plan.Planned, 1)
	assert.Equal(t, asOf, plan.Planned[0])
	assert.Len(t, plan.Skipped, 4, "the other 4 dates should be skipped")
}

func TestPlanMissed_CatchUpBounded_LimitEnforced(t *testing.T) {
	// 10 daily due dates, limit=7 → Planned=7 (newest first), Skipped=3
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC) // Jan 1–10 = 10 dates
	limit := 7

	plan, err := PlanMissed("daily", start, asOf, nil, validPolicy(model.ScheduleMissedCatchUpBounded, limit), time.UTC)
	require.NoError(t, err)
	assert.Len(t, plan.Planned, limit, "catch-up must plan exactly limit dates")
	assert.Len(t, plan.Skipped, 3, "remaining must be skipped")
	// Planned should be the 7 most-recent dates (Jan 4–10)
	assert.Equal(t, time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), plan.Planned[0])
	assert.Equal(t, asOf, plan.Planned[6])
}

func TestPlanMissed_CatchUpBounded_FewerThanLimit(t *testing.T) {
	// Only 3 dates available, limit=7 → Planned=3, Skipped=0
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)

	plan, err := PlanMissed("daily", start, asOf, nil, validPolicy(model.ScheduleMissedCatchUpBounded, 7), time.UTC)
	require.NoError(t, err)
	assert.Len(t, plan.Planned, 3)
	assert.Len(t, plan.Skipped, 0)
}

func TestPlanMissed_Once_SingleDate(t *testing.T) {
	start := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	plan, err := PlanMissed("once", start, asOf, nil, validPolicy(model.ScheduleMissedRunOnceLatest, 7), time.UTC)
	require.NoError(t, err)
	assert.Len(t, plan.Planned, 1)
	assert.Len(t, plan.Skipped, 0)
}

func TestPlanMissed_Monthly_MissedMonths(t *testing.T) {
	// Monthly on the 5th; start Jan 5, asOf Mar 5 → 3 candidates
	day := 5
	start := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)

	plan, err := PlanMissed("monthly", start, asOf, &day, validPolicy(model.ScheduleMissedRunOnceLatest, 7), time.UTC)
	require.NoError(t, err)
	// run_once_latest → plans March 5 only, skips Jan 5 and Feb 5
	assert.Len(t, plan.Planned, 1)
	assert.Equal(t, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), plan.Planned[0])
	assert.Len(t, plan.Skipped, 2)
}

func TestPlanMissed_Monthly_MissingDayOfMonth_Rejected(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	_, err := PlanMissed("monthly", start, asOf, nil, validPolicy(model.ScheduleMissedRunOnceLatest, 7), time.UTC)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestPlanMissed_UnknownKind_Rejected(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := PlanMissed("biweekly", start, asOf, nil, validPolicy(model.ScheduleMissedRunOnceLatest, 7), time.UTC)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestPlanMissed_StartAfterAsOf_EmptyPlan(t *testing.T) {
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	plan, err := PlanMissed("daily", start, asOf, nil, validPolicy(model.ScheduleMissedRunOnceLatest, 7), time.UTC)
	require.NoError(t, err)
	assert.Empty(t, plan.Planned)
	assert.Empty(t, plan.Skipped)
}

// ─── OccurrenceIdempotencyKey ────────────────────────────────────────────────

func TestOccurrenceIdempotencyKey_Format(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	date := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	key := OccurrenceIdempotencyKey(id, date)
	assert.Equal(t, fmt.Sprintf("sched:%s:2026-07-04", id), key)
	assert.True(t, strings.HasPrefix(key, "sched:"), "key must begin with 'sched:'")
	assert.Equal(t, 2, strings.Count(key, ":"), "key must contain exactly 2 colon separators (3 segments)")
}

func TestOccurrenceIdempotencyKey_DateNormalized(t *testing.T) {
	id := uuid.New()
	noon := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	midnight := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	// The key must depend on the calendar date, not the time component.
	assert.Equal(t, OccurrenceIdempotencyKey(id, midnight), OccurrenceIdempotencyKey(id, noon))
}

// ─── CommandDigest ───────────────────────────────────────────────────────────

func TestCommandDigest_Deterministic(t *testing.T) {
	cmd := model.ScheduleCommand{
		Type: "transfer_p2p", Version: 1, Amount: "10000",
		Currency: "IDR", TargetUserID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	d1, err := CommandDigest(cmd)
	require.NoError(t, err)
	d2, err := CommandDigest(cmd)
	require.NoError(t, err)
	assert.Equal(t, d1, d2, "same command must always produce the same digest")
}

func TestCommandDigest_DifferentCommands_DifferentDigests(t *testing.T) {
	base := model.ScheduleCommand{Type: "transfer_p2p", Version: 1, Amount: "10000"}
	modified := base
	modified.Amount = "20000"

	d1, err := CommandDigest(base)
	require.NoError(t, err)
	d2, err := CommandDigest(modified)
	require.NoError(t, err)
	assert.NotEqual(t, d1, d2)
}
