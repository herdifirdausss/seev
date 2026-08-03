package schedule

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
)

const (
	defaultCatchUpLimit            = 7
	defaultInfrastructureAttempts  = 5
	defaultRetryWindow             = 24 * time.Hour
	defaultFailureThreshold        = 3
	maximumCatchUpLimit            = 7
	maximumInfrastructureAttempts  = 20
	maximumFailureThreshold        = 20
	maximumSchedulePlanningHorizon = 3660
	defaultScheduleLocalTime       = "00:30"
)

// DefaultPolicy is the compatibility policy for a newly created or migrated
// schedule.  Daily schedules preserve the historical skip-missed behavior;
// once/monthly schedules run the latest due occurrence once.  Catch-up is
// opt-in and always bounded.
func DefaultPolicy(kind string) model.ScheduledPolicy {
	policy := model.ScheduledPolicy{
		MissedRunPolicy:             model.ScheduleMissedRunOnceLatest,
		CatchUpLimit:                defaultCatchUpLimit,
		MaxInfrastructureAttempts:   defaultInfrastructureAttempts,
		RetryWindowSeconds:          int64(defaultRetryWindow / time.Second),
		ConsecutiveFailureThreshold: defaultFailureThreshold,
		FeeMode:                     "current_policy_with_consent_cap",
	}
	if kind == "daily" {
		policy.MissedRunPolicy = model.ScheduleMissedSkip
	}
	return policy
}

// NormalizePolicy fills defaults and validates the persisted policy before it
// can influence scheduling or money movement.
func NormalizePolicy(kind string, policy model.ScheduledPolicy) (model.ScheduledPolicy, error) {
	defaults := DefaultPolicy(kind)
	if policy.MissedRunPolicy == "" {
		policy.MissedRunPolicy = defaults.MissedRunPolicy
	}
	if policy.CatchUpLimit == 0 {
		policy.CatchUpLimit = defaults.CatchUpLimit
	}
	if policy.MaxInfrastructureAttempts == 0 {
		policy.MaxInfrastructureAttempts = defaults.MaxInfrastructureAttempts
	}
	if policy.RetryWindowSeconds == 0 {
		policy.RetryWindowSeconds = defaults.RetryWindowSeconds
	}
	if policy.ConsecutiveFailureThreshold == 0 {
		policy.ConsecutiveFailureThreshold = defaults.ConsecutiveFailureThreshold
	}
	if policy.FeeMode == "" {
		policy.FeeMode = defaults.FeeMode
	}
	if policy.MissedRunPolicy != model.ScheduleMissedSkip &&
		policy.MissedRunPolicy != model.ScheduleMissedRunOnceLatest &&
		policy.MissedRunPolicy != model.ScheduleMissedCatchUpBounded {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: invalid missed_run_policy", apperror.ErrValidation)
	}
	if policy.CatchUpLimit < 0 || policy.CatchUpLimit > maximumCatchUpLimit {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: catch_up_limit must be between 0 and %d", apperror.ErrValidation, maximumCatchUpLimit)
	}
	if policy.MissedRunPolicy == model.ScheduleMissedCatchUpBounded && policy.CatchUpLimit == 0 {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: catch_up_bounded requires catch_up_limit", apperror.ErrValidation)
	}
	if policy.MaxInfrastructureAttempts < 1 || policy.MaxInfrastructureAttempts > maximumInfrastructureAttempts {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: max_infrastructure_attempts must be between 1 and %d", apperror.ErrValidation, maximumInfrastructureAttempts)
	}
	if policy.RetryWindowSeconds < 1 {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: retry_window_seconds must be positive", apperror.ErrValidation)
	}
	if policy.ConsecutiveFailureThreshold < 1 || policy.ConsecutiveFailureThreshold > maximumFailureThreshold {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: consecutive_failure_threshold must be between 1 and %d", apperror.ErrValidation, maximumFailureThreshold)
	}
	if policy.MaxFeeAmount != nil && *policy.MaxFeeAmount < 0 {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: max_fee_amount cannot be negative", apperror.ErrValidation)
	}
	if policy.FeeMode != defaults.FeeMode {
		return model.ScheduledPolicy{}, fmt.Errorf("%w: unsupported fee_mode", apperror.ErrValidation)
	}
	return policy, nil
}

// OccurrencePlan separates planned occurrences from dates deliberately
// skipped because of a missed-run policy.  The planner is pure: persisting
// either result is a caller concern, which makes the policy independently
// reviewable and deterministic.
type OccurrencePlan struct {
	Planned []time.Time
	Skipped []time.Time
}

// PlanMissed returns local calendar dates that should be materialized for one
// schedule.  It never emits more than seven catch-up occurrences and never
// creates an unbounded backlog.
func PlanMissed(kind string, startDate, asOf time.Time, dayOfMonth *int, policy model.ScheduledPolicy, loc *time.Location) (OccurrencePlan, error) {
	if loc == nil {
		loc = time.UTC
	}
	policy, err := NormalizePolicy(kind, policy)
	if err != nil {
		return OccurrencePlan{}, err
	}
	start := dateIn(startDate, loc)
	end := dateIn(asOf, loc)
	if start.After(end) {
		return OccurrencePlan{}, nil
	}
	if kind == "monthly" && (dayOfMonth == nil || *dayOfMonth < 1 || *dayOfMonth > 28) {
		return OccurrencePlan{}, fmt.Errorf("%w: monthly schedule day_of_month must be between 1 and 28", apperror.ErrValidation)
	}

	dates := make([]time.Time, 0)
	switch kind {
	case "once":
		dates = append(dates, start)
	case "daily":
		for cursor := start; !cursor.After(end); cursor = cursor.AddDate(0, 0, 1) {
			if len(dates) >= maximumSchedulePlanningHorizon {
				break
			}
			dates = append(dates, cursor)
		}
	case "monthly":
		for cursor := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, loc); !cursor.After(end); cursor = cursor.AddDate(0, 1, 0) {
			candidate := time.Date(cursor.Year(), cursor.Month(), *dayOfMonth, 0, 0, 0, 0, loc)
			if !candidate.Before(start) && !candidate.After(end) {
				dates = append(dates, candidate)
			}
			if len(dates) >= maximumSchedulePlanningHorizon {
				break
			}
		}
	default:
		return OccurrencePlan{}, fmt.Errorf("%w: unknown schedule kind %q", apperror.ErrValidation, kind)
	}
	if len(dates) == 0 {
		return OccurrencePlan{}, nil
	}

	switch policy.MissedRunPolicy {
	case model.ScheduleMissedSkip:
		// The compatibility policy only runs the occurrence for the current
		// local date. Older due dates are explicitly materialized as skipped
		// occurrences so the missed history is durable rather than invisible.
		if dates[len(dates)-1].Equal(end) {
			return OccurrencePlan{Planned: dates[len(dates)-1:], Skipped: dates[:len(dates)-1]}, nil
		}
		return OccurrencePlan{Skipped: dates}, nil
	case model.ScheduleMissedRunOnceLatest:
		return OccurrencePlan{Planned: dates[len(dates)-1:], Skipped: dates[:len(dates)-1]}, nil
	case model.ScheduleMissedCatchUpBounded:
		limit := min(policy.CatchUpLimit, len(dates))
		return OccurrencePlan{Planned: dates[len(dates)-limit:], Skipped: dates[:len(dates)-limit]}, nil
	default:
		return OccurrencePlan{}, fmt.Errorf("%w: unsupported missed_run_policy", apperror.ErrValidation)
	}
}

// OccurrenceTime turns a local calendar date into the exact UTC execution
// instant. The local date is the public schedule identity; the UTC instant is
// persisted and used in the idempotency key so DST transitions cannot create
// ambiguous execution identities.
func OccurrenceTime(localDate time.Time, localTime string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	if localTime == "" {
		localTime = defaultScheduleLocalTime
	}
	parsed, err := time.Parse("15:04", localTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: local_time must be HH:MM", apperror.ErrValidation)
	}
	date := dateIn(localDate, loc)
	return time.Date(date.Year(), date.Month(), date.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc).UTC(), nil
}

// OccurrenceIdempotencyKey preserves the compatible date-based contract for
// supported schedules. The exact UTC instant remains persisted separately as
// scheduled_for and is the database occurrence identity, so timezone/DST
// behavior is still unambiguous without changing the Ledger idempotency key.
func OccurrenceIdempotencyKey(scheduleID uuid.UUID, scheduledLocalDate time.Time) string {
	return fmt.Sprintf("sched:%s:%s", scheduleID, scheduledLocalDate.Format("2006-01-02"))
}

// CommandDigest is the canonical digest persisted beside the legacy payload.
// The digest is checked before execution so a corrupt or out-of-band command
// edit becomes a business-blocked occurrence rather than silently moving
// money under a different command.
func CommandDigest(command model.ScheduleCommand) ([]byte, error) {
	canonical, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	digest := md5.Sum(canonical)
	return digest[:], nil
}

func CommandDigestHex(command model.ScheduleCommand) (string, error) {
	digest, err := CommandDigest(command)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest), nil
}

func PolicySnapshot(policy model.ScheduledPolicy) (json.RawMessage, error) {
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func dateIn(value time.Time, loc *time.Location) time.Time {
	local := value.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}
