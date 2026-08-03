package schedule

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// FeeResolver is intentionally structural. Ledger owns the current fee
// policy; the durable scheduler only asks Ledger for a price at execution time
// and applies the schedule's stored consent cap.
type FeeResolver interface {
	Resolve(context.Context, uuid.UUID, string, string, string, decimal.Decimal) (decimal.Decimal, string, bool)
}

type TransactionLookup interface {
	GetTransactionByIdempotencyKey(context.Context, string, string) (model.LedgerTransaction, error)
}

// DurableService materializes schedule configuration into immutable
// occurrence rows and executes exactly one claimed occurrence at a time.
// Scheduler infrastructure calls RunDurable; it does not own any of this
// state or money-moving policy.
type DurableService struct {
	schedules   repository.ScheduledTransactionRepository
	occurrences repository.ScheduledOccurrenceRepository
	poster      Poster
	fees        FeeResolver
	txLookup    TransactionLookup
	db          DatabaseSQL
	outbox      repository.OutboxRepository
	logger      *slog.Logger
	loc         *time.Location
}

func (s *DurableService) SetTransactionLookup(lookup TransactionLookup) {
	s.txLookup = lookup
}

func (s *DurableService) SetDatabase(db DatabaseSQL) { s.db = db }

func (s *DurableService) SetOutbox(outbox repository.OutboxRepository) { s.outbox = outbox }

func NewDurable(schedules repository.ScheduledTransactionRepository, occurrences repository.ScheduledOccurrenceRepository, poster Poster, fees FeeResolver, logger *slog.Logger, loc *time.Location) *DurableService {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &DurableService{
		schedules: schedules, occurrences: occurrences, poster: poster,
		fees: fees, logger: logger, loc: loc,
	}
}

// PlanSchedule materializes all policy-selected dates up to asOf. Re-running
// it is safe because (schedule_id, scheduled_for) is unique and the returned
// row is always the existing occurrence when a planner retry races another
// planner.
func (s *DurableService) PlanSchedule(ctx context.Context, scheduleID uuid.UUID, asOf time.Time) ([]model.ScheduledOccurrence, error) {
	row, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	if row.Status != "active" {
		return nil, nil
	}
	policy := policyFromSchedule(row)
	loc := scheduleLocation(row.Timezone, s.loc)
	start := row.RunAtDate
	if row.LastRunDate != nil {
		start = row.LastRunDate.AddDate(0, 0, 1)
	}
	plan, err := PlanMissed(row.ScheduleKind, start, asOf, row.DayOfMonth, policy, loc)
	if err != nil {
		return nil, err
	}
	commandErr := error(nil)
	if _, err := commandFromRow(row); err != nil {
		commandErr = err
	}
	snapshot, err := PolicySnapshot(policy)
	if err != nil {
		return nil, fmt.Errorf("schedule: encode policy snapshot: %w", err)
	}
	result := make([]model.ScheduledOccurrence, 0, len(plan.Planned))
	var lastSkipped time.Time
	commandErrorCode := scheduleCommandErrorCode(commandErr)
	for _, date := range append(plan.Skipped, plan.Planned...) {
		scheduledFor, timeErr := OccurrenceTime(date, row.LocalTime, loc)
		if timeErr != nil {
			return nil, timeErr
		}
		occurrence := model.ScheduledOccurrence{
			ScheduleID:         row.ID,
			ScheduleVersion:    row.Version,
			ScheduledFor:       scheduledFor,
			ScheduledLocalDate: date,
			Status:             model.ScheduleOccurrenceDue,
			IdempotencyKey:     OccurrenceIdempotencyKey(row.ID, date),
			PolicySnapshot:     snapshot,
		}
		stored, createErr := s.occurrences.CreateOrGet(ctx, occurrence)
		if createErr != nil {
			return nil, createErr
		}
		if containsDate(plan.Skipped, date) && stored.Status == model.ScheduleOccurrencePlanned {
			skipStatus := model.ScheduleOccurrenceSkippedSuperseded
			skipCode := "MISSED_RUN_SUPERSEDED"
			switch policy.MissedRunPolicy {
			case model.ScheduleMissedSkip:
				skipStatus = model.ScheduleOccurrenceSkippedMissed
				skipCode = "MISSED_RUN_POLICY"
			case model.ScheduleMissedCatchUpBounded:
				skipCode = "MISSED_BACKLOG_TRUNCATED"
			}
			if statusErr := s.occurrences.SetStatus(ctx, stored.ID, skipStatus, skipCode, nil, nil); statusErr != nil {
				return nil, statusErr
			}
			if lastSkipped.IsZero() || date.After(lastSkipped) {
				lastSkipped = date
			}
			stored.Status = skipStatus
		}
		if commandErr != nil {
			if !containsDate(plan.Skipped, date) && (stored.Status == model.ScheduleOccurrencePlanned ||
				stored.Status == model.ScheduleOccurrenceDue || stored.Status == model.ScheduleOccurrenceReady ||
				stored.Status == model.ScheduleOccurrenceRetryWait) {
				finishedAt := time.Now().UTC()
				errorCode := commandErrorCode
				if attemptErr := s.occurrences.RecordAttempt(ctx, model.ScheduledExecutionAttempt{
					OccurrenceID: stored.ID, AttemptNumber: 1, Phase: "validation", Result: "blocked",
					Retryable: false, ErrorCode: &errorCode, StartedAt: finishedAt, FinishedAt: &finishedAt,
				}); attemptErr != nil {
					return nil, attemptErr
				}
				if statusErr := s.occurrences.SetStatus(ctx, stored.ID, model.ScheduleOccurrenceBlocked, commandErrorCode, nil, nil); statusErr != nil {
					return nil, statusErr
				}
				if emitErr := s.emitScheduleFailure(ctx, stored, row, model.ScheduleOccurrenceBlocked, commandErrorCode, false); emitErr != nil {
					return nil, emitErr
				}
			}
			continue
		}
		if stored.Status == model.ScheduleOccurrenceDue || stored.Status == model.ScheduleOccurrencePlanned || stored.Status == model.ScheduleOccurrenceReady || stored.Status == model.ScheduleOccurrenceRetryWait {
			result = append(result, stored)
		}
	}
	if !lastSkipped.IsZero() {
		if runErr := s.occurrences.SetScheduleLastRun(ctx, row.ID, lastSkipped, row.ScheduleKind == "once"); runErr != nil {
			return nil, runErr
		}
	}
	if plannedErr := s.occurrences.SetScheduleLastPlanned(ctx, row.ID, asOf.UTC()); plannedErr != nil {
		return nil, plannedErr
	}
	if commandErr != nil {
		if blockErr := s.occurrences.BlockSchedule(ctx, row.ID, commandErrorCode); blockErr != nil {
			return nil, blockErr
		}
		if emitErr := s.emitSchedulePaused(ctx, row.ID, commandErrorCode); emitErr != nil {
			return nil, emitErr
		}
	}
	return result, nil
}

// RunDurable is the worker-facing entry point. Legacy rows are still selected
// through the old repository interface; a concrete C5 repository additionally
// exposes ListAllActive so a monthly schedule that was missed on its exact
// day is still eligible for run_once_latest/catch_up_bounded planning.
func (s *DurableService) RunDurable(ctx context.Context, asOf time.Time) (executed, failed int, err error) {
	rows, listErr := s.listCandidates(ctx, asOf)
	if listErr != nil {
		return 0, 0, fmt.Errorf("schedule: list durable candidates: %w", listErr)
	}
	for _, row := range rows {
		planned, planErr := s.PlanSchedule(ctx, row.ID, asOf)
		if planErr != nil {
			failed++
			s.logger.Error("schedule: planning failed", slog.String("schedule_id", row.ID.String()), slog.Any("error", planErr))
			continue
		}
		for _, occurrence := range planned {
			if occurrence.ScheduledFor.After(asOf.UTC()) {
				continue
			}
			if occurrence.Status == model.ScheduleOccurrenceSkippedMissed {
				_ = s.occurrences.SetScheduleLastRun(ctx, row.ID, occurrence.ScheduledLocalDate, row.ScheduleKind == "once")
				continue
			}
			ok, execErr := s.ExecuteOccurrence(ctx, occurrence.ID, "schedule-worker")
			if execErr != nil {
				failed++
				s.logger.Error("schedule: occurrence execution failed", slog.String("occurrence_id", occurrence.ID.String()), slog.Any("error", execErr))
				continue
			}
			if ok {
				executed++
			}
		}
	}
	return executed, failed, nil
}

func (s *DurableService) ListOccurrences(ctx context.Context, scheduleID, userID uuid.UUID, limit, offset int) ([]model.ScheduledOccurrence, error) {
	row, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	if userID != uuid.Nil && row.UserID != userID {
		return nil, fmt.Errorf("%w: %s", apperror.ErrScheduledTransactionNotOwned, scheduleID)
	}
	return s.occurrences.List(ctx, scheduleID, userID, limit, offset)
}

func (s *DurableService) GetOccurrence(ctx context.Context, occurrenceID, userID uuid.UUID) (model.ScheduledOccurrence, error) {
	item, err := s.occurrences.Get(ctx, occurrenceID)
	if err != nil {
		return model.ScheduledOccurrence{}, err
	}
	row, err := s.schedules.GetByID(ctx, item.ScheduleID)
	if err != nil {
		return model.ScheduledOccurrence{}, err
	}
	if userID != uuid.Nil && row.UserID != userID {
		return model.ScheduledOccurrence{}, fmt.Errorf("%w: %s", apperror.ErrScheduledTransactionNotOwned, item.ScheduleID)
	}
	return item, nil
}

// RetryOccurrence is an operator action. It requeues only a terminal
// occurrence; the original attempt rows and idempotency key remain immutable.
func (s *DurableService) RetryOccurrence(ctx context.Context, occurrenceID uuid.UUID) error {
	item, err := s.occurrences.Get(ctx, occurrenceID)
	if err != nil {
		return err
	}
	if item.Status != model.ScheduleOccurrenceFailedBusiness && item.Status != model.ScheduleOccurrenceBlocked {
		return fmt.Errorf("%w: occurrence is not retryable", apperror.ErrValidation)
	}
	row, err := s.schedules.GetByID(ctx, item.ScheduleID)
	if err != nil {
		return err
	}
	if row.Status == "blocked" {
		return fmt.Errorf("%w: schedule is blocked", apperror.ErrValidation)
	}
	if retryer, ok := s.occurrences.(interface {
		Retry(context.Context, uuid.UUID) error
	}); ok {
		if err := retryer.Retry(ctx, occurrenceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: occurrence is no longer retryable", apperror.ErrValidation)
			}
			return err
		}
		return nil
	}
	return s.occurrences.SetStatus(ctx, occurrenceID, model.ScheduleOccurrenceReady, "", nil, nil)
}

// ConfirmFeeCap applies a new user-authorized fee cap and requeues occurrences
// that were blocked because the previous cap was missing or too low.
func (s *DurableService) ConfirmFeeCap(ctx context.Context, scheduleID, userID uuid.UUID, maxFeeAmount int64) error {
	if maxFeeAmount < 0 {
		return fmt.Errorf("%w: max fee amount must not be negative", apperror.ErrValidation)
	}

	schedule, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return err
	}
	if schedule.UserID != userID {
		return apperror.ErrScheduledTransactionNotOwned
	}

	confirmer, ok := s.occurrences.(interface {
		ConfirmFeeCap(context.Context, uuid.UUID, uuid.UUID, int64) error
	})
	if !ok {
		return fmt.Errorf("%w: fee-cap confirmation is unavailable", apperror.ErrValidation)
	}
	if err := confirmer.ConfirmFeeCap(ctx, scheduleID, userID, maxFeeAmount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: schedule is not awaiting fee-cap confirmation", apperror.ErrValidation)
		}
		return err
	}
	return nil
}

// ExecuteOccurrence claims and processes one occurrence. A false,nil result
// means another worker already owns or completed it.
func (s *DurableService) ExecuteOccurrence(ctx context.Context, occurrenceID uuid.UUID, owner string) (bool, error) {
	occurrence, err := s.occurrences.Claim(ctx, occurrenceID, owner, time.Now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	attemptStarted := time.Now().UTC()
	attemptNumber := max(occurrence.AttemptCount, 1)
	if attemptErr := s.occurrences.RecordAttempt(ctx, model.ScheduledExecutionAttempt{
		OccurrenceID: occurrence.ID, AttemptNumber: attemptNumber,
		Phase: "screening", Result: "started", StartedAt: attemptStarted,
	}); attemptErr != nil {
		return false, attemptErr
	}
	scheduleRow, err := s.schedules.GetByID(ctx, occurrence.ScheduleID)
	if err != nil {
		return false, err
	}
	if scheduleRow.Status != "active" {
		_ = s.occurrences.FinishAttempt(ctx, occurrence.ID, time.Now().UTC(), "cancelled", false, "SCHEDULE_NOT_ACTIVE", nil)
		_ = s.occurrences.SetStatus(ctx, occurrence.ID, model.ScheduleOccurrenceCancelled, "SCHEDULE_NOT_ACTIVE", nil, nil)
		return false, nil
	}
	command, err := commandFromRow(scheduleRow)
	if err != nil {
		return s.businessFailure(ctx, occurrence, scheduleRow, "COMMAND_INVALID", err)
	}
	metadata := cloneMetadata(command.Metadata)
	currency := command.Currency
	if currency == "" {
		currency = scheduleRow.Currency
	}
	// Fee fields in a stored command are never authoritative. The current
	// Ledger fee policy is evaluated at execution time, so remove any stale or
	// caller-supplied fee snapshot before resolving the present-day policy.
	delete(metadata, "fee_amount")
	delete(metadata, "fee_gateway")
	delete(metadata, "fee_application")
	fee, feeGateway, feeErr := s.resolveFee(ctx, scheduleRow, command, currency)
	if feeErr != nil {
		return s.businessFailure(ctx, occurrence, scheduleRow, feeErr.Error(), feeErr)
	}
	if fee.IsPositive() {
		if feeGateway == "" {
			return s.businessFailure(ctx, occurrence, scheduleRow, "FEE_POLICY_INVALID", fmt.Errorf("resolved fee gateway is missing"))
		}
		if scheduleRow.MaxFeeAmount == nil {
			return s.businessFailure(ctx, occurrence, scheduleRow, "SCHEDULE_FEE_CONSENT_REQUIRED", fmt.Errorf("fee consent cap is missing"))
		}
		if !fee.BigInt().IsInt64() {
			return s.businessFailure(ctx, occurrence, scheduleRow, "FEE_POLICY_INVALID", fmt.Errorf("resolved fee exceeds int64"))
		}
		if fee.GreaterThan(decimal.NewFromInt(*scheduleRow.MaxFeeAmount)) {
			return s.businessFailure(ctx, occurrence, scheduleRow, "SCHEDULE_FEE_CAP_EXCEEDED", fmt.Errorf("resolved fee exceeds stored consent cap"))
		}
		metadata["fee_amount"] = fee.String()
		metadata["fee_gateway"] = feeGateway
		metadata["fee_application"] = "deducted_from_transfer"
	}
	if !fee.IsNegative() {
		if setErr := s.occurrences.SetFee(ctx, occurrence.ID, fee.IntPart(), nil); setErr != nil {
			return false, setErr
		}
	}

	if attemptErr := s.occurrences.RecordAttempt(ctx, model.ScheduledExecutionAttempt{
		OccurrenceID: occurrence.ID, AttemptNumber: attemptNumber,
		Phase: "ledger_post", Result: "started", StartedAt: attemptStarted,
	}); attemptErr != nil {
		return false, attemptErr
	}
	postErr := s.poster.Handle(ctx, processors.Command{
		IdempotencyKey:   occurrence.IdempotencyKey,
		IdempotencyScope: "schedule:" + scheduleRow.UserID.String(),
		Type:             command.Type,
		Amount:           mustDecimal(command.Amount),
		UserID:           scheduleRow.UserID,
		TargetUserID:     command.TargetUserID,
		PocketCode:       command.PocketCode,
		Metadata:         metadata,
	})
	if postErr == nil || errors.Is(postErr, apperror.ErrAlreadyPosted) {
		var txID *uuid.UUID
		if s.txLookup != nil {
			tx, lookupErr := s.txLookup.GetTransactionByIdempotencyKey(ctx, occurrence.IdempotencyKey, "schedule:"+scheduleRow.UserID.String())
			if lookupErr != nil {
				// Leave the occurrence leased and processing. A later lease
				// recovery will use the same idempotency key and finish the
				// audit link once the Ledger read is available.
				return false, fmt.Errorf("schedule: recover posted transaction: %w", lookupErr)
			}
			txID = &tx.ID
		}
		if finishErr := s.occurrences.FinishAttempt(ctx, occurrence.ID, time.Now().UTC(), "succeeded", false, "", txID); finishErr != nil {
			return false, finishErr
		}
		if statusErr := s.occurrences.SetStatus(ctx, occurrence.ID, model.ScheduleOccurrenceSucceeded, "", nil, txID); statusErr != nil {
			return false, statusErr
		}
		if err := s.occurrences.RecordScheduleSuccess(ctx, scheduleRow.ID); err != nil {
			return false, err
		}
		if err := s.occurrences.SetScheduleLastRun(ctx, scheduleRow.ID, occurrence.ScheduledLocalDate, scheduleRow.ScheduleKind == "once"); err != nil {
			return false, err
		}
		if err := s.emitOutbox(ctx, model.OutboxEvent{
			AggregateType: "scheduled_occurrence", AggregateID: occurrence.ID,
			EventType: events.TypeScheduleOccurrenceSucceeded,
			Payload: events.NewScheduleOccurrenceSucceeded(occurrence.ID, scheduleRow.ID, txID,
				command.Amount, fmt.Sprintf("%d", fee.IntPart()), time.Now().UTC()).ToPayload(),
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	if isBusinessFailure(postErr) {
		return s.businessFailure(ctx, occurrence, scheduleRow, "LEDGER_BUSINESS_FAILURE", postErr)
	}

	_ = s.occurrences.FinishAttempt(ctx, occurrence.ID, time.Now().UTC(), "infra_failure", true, "LEDGER_INFRA_FAILURE", nil)
	policy := policyFromSchedule(scheduleRow)
	if occurrence.AttemptCount >= policy.MaxInfrastructureAttempts {
		_ = s.occurrences.BlockSchedule(ctx, scheduleRow.ID, "infrastructure_retry_exhausted")
		if statusErr := s.occurrences.SetStatus(ctx, occurrence.ID, model.ScheduleOccurrenceBlocked, "INFRA_RETRY_EXHAUSTED", nil, nil); statusErr != nil {
			return false, statusErr
		}
		if emitErr := s.emitScheduleFailure(ctx, occurrence, scheduleRow, model.ScheduleOccurrenceBlocked, "INFRA_RETRY_EXHAUSTED", false); emitErr != nil {
			return false, emitErr
		}
		if emitErr := s.emitSchedulePaused(ctx, scheduleRow.ID, "infrastructure_retry_exhausted"); emitErr != nil {
			return false, emitErr
		}
		return false, postErr
	}
	next := time.Now().UTC().Add(time.Duration(policy.RetryWindowSeconds) * time.Second)
	if statusErr := s.occurrences.SetStatus(ctx, occurrence.ID, model.ScheduleOccurrenceRetryWait, "LEDGER_INFRA_FAILURE", &next, nil); statusErr != nil {
		return false, statusErr
	}
	return false, postErr
}

func (s *DurableService) businessFailure(ctx context.Context, occurrence model.ScheduledOccurrence, scheduleRow model.ScheduledTransaction, code string, cause error) (bool, error) {
	_ = s.occurrences.FinishAttempt(ctx, occurrence.ID, time.Now().UTC(), "business_failure", false, code, nil)
	policy := policyFromSchedule(scheduleRow)
	status := model.ScheduleOccurrenceFailedBusiness
	if scheduleRow.ScheduleKind == "once" {
		status = model.ScheduleOccurrenceFailedTerminal
	}
	blocked, err := s.occurrences.RecordScheduleBusinessFailure(ctx, scheduleRow.ID, code, policy.ConsecutiveFailureThreshold)
	if err != nil {
		return false, err
	}
	if scheduleRow.ScheduleKind == "once" && !blocked {
		if blockErr := s.occurrences.BlockSchedule(ctx, scheduleRow.ID, "once_occurrence_terminal_failure"); blockErr != nil {
			return false, blockErr
		}
	}
	pauseReason := ""
	switch code {
	case "SCHEDULE_FEE_CAP_EXCEEDED", "FEE_EXCEEDS_CONSENT":
		pauseReason = "fee_cap_exceeded"
	case "SCHEDULE_FEE_CONSENT_REQUIRED", "FEE_CONSENT_REQUIRED":
		pauseReason = "fee_consent_required"
	}
	if pauseReason != "" {
		if blockErr := s.occurrences.BlockSchedule(ctx, scheduleRow.ID, pauseReason); blockErr != nil {
			return false, blockErr
		}
		blocked = true
	}
	if blocked {
		status = model.ScheduleOccurrenceBlocked
	}
	if statusErr := s.occurrences.SetStatus(ctx, occurrence.ID, status, code, nil, nil); statusErr != nil {
		return false, statusErr
	}
	if emitErr := s.emitScheduleFailure(ctx, occurrence, scheduleRow, status, code, false); emitErr != nil {
		return false, emitErr
	}
	if blocked || scheduleRow.ScheduleKind == "once" {
		reason := pauseReason
		if reason == "" {
			if scheduleRow.ScheduleKind == "once" {
				reason = "once_occurrence_terminal_failure"
			} else {
				reason = "consecutive_business_failures"
			}
		}
		if emitErr := s.emitSchedulePaused(ctx, scheduleRow.ID, reason); emitErr != nil {
			return false, emitErr
		}
	}
	return false, fmt.Errorf("%s: %w", code, cause)
}

func (s *DurableService) emitScheduleFailure(ctx context.Context, occurrence model.ScheduledOccurrence, row model.ScheduledTransaction, status, code string, retryable bool) error {
	return s.emitOutbox(ctx, model.OutboxEvent{
		AggregateType: "scheduled_occurrence", AggregateID: occurrence.ID,
		EventType: events.TypeScheduleOccurrenceFailed,
		Payload:   events.NewScheduleOccurrenceFailed(occurrence.ID, row.ID, status, code, retryable, time.Now().UTC()).ToPayload(),
	})
}

func (s *DurableService) emitSchedulePaused(ctx context.Context, scheduleID uuid.UUID, reason string) error {
	return s.emitOutbox(ctx, model.OutboxEvent{
		AggregateType: "scheduled_transaction", AggregateID: scheduleID,
		EventType: events.TypeSchedulePaused,
		Payload:   events.NewSchedulePaused(scheduleID, reason, time.Now().UTC()).ToPayload(),
	})
}

func (s *DurableService) emitOutbox(ctx context.Context, event model.OutboxEvent) error {
	if s.outbox == nil {
		return nil
	}
	if s.db == nil {
		return fmt.Errorf("schedule: outbox database is not configured")
	}
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.outbox.InsertEvents(ctx, tx, []model.OutboxEvent{event})
	})
}

func (s *DurableService) resolveFee(ctx context.Context, row model.ScheduledTransaction, command model.ScheduleCommand, currency string) (decimal.Decimal, string, error) {
	if s.fees == nil {
		return decimal.Zero, "", nil
	}
	gateway := ""
	if raw, ok := command.Metadata["gateway"].(string); ok {
		gateway = raw
	}
	fee, feeGateway, ok := s.fees.Resolve(ctx, row.UserID, command.Type, gateway, currency, mustDecimal(command.Amount))
	if !ok {
		return decimal.Zero, "", nil
	}
	if fee.IsNegative() || !fee.Equal(fee.Truncate(0)) {
		return decimal.Zero, "", fmt.Errorf("FEE_POLICY_INVALID")
	}
	return fee, feeGateway, nil
}

func (s *DurableService) listCandidates(ctx context.Context, asOf time.Time) ([]model.ScheduledTransaction, error) {
	if lister, ok := s.schedules.(interface {
		ListAllActive(context.Context) ([]model.ScheduledTransaction, error)
	}); ok {
		return lister.ListAllActive(ctx)
	}
	return s.schedules.ListDue(ctx, asOf)
}

func policyFromSchedule(row model.ScheduledTransaction) model.ScheduledPolicy {
	missed := row.MissedRunPolicy
	catchUp := row.CatchUpLimit
	maxInfrastructureAttempts := row.MaxInfrastructureAttempts
	retryWindowSeconds := row.RetryWindowSeconds
	threshold := row.ConsecutiveFailureThreshold
	feeMode := row.FeeMode
	if missed == "" {
		missed = DefaultPolicy(row.ScheduleKind).MissedRunPolicy
	}
	if catchUp == 0 {
		catchUp = defaultCatchUpLimit
	}
	if maxInfrastructureAttempts == 0 {
		maxInfrastructureAttempts = defaultInfrastructureAttempts
	}
	if retryWindowSeconds == 0 {
		retryWindowSeconds = int64(defaultRetryWindow / time.Second)
	}
	if threshold == 0 {
		threshold = defaultFailureThreshold
	}
	if feeMode == "" {
		feeMode = "current_policy_with_consent_cap"
	}
	return model.ScheduledPolicy{
		MissedRunPolicy:             missed,
		CatchUpLimit:                catchUp,
		MaxInfrastructureAttempts:   maxInfrastructureAttempts,
		RetryWindowSeconds:          retryWindowSeconds,
		ConsecutiveFailureThreshold: threshold,
		MaxFeeAmount:                row.MaxFeeAmount,
		FeeMode:                     feeMode,
	}
}

func commandFromRow(row model.ScheduledTransaction) (model.ScheduleCommand, error) {
	var command model.ScheduleCommand
	if err := json.Unmarshal(row.CmdPayload, &command); err != nil {
		return model.ScheduleCommand{}, fmt.Errorf("%w: cannot decode schedule command", apperror.ErrValidation)
	}
	if row.CommandVersion <= 0 || row.CommandVersion != 1 {
		return model.ScheduleCommand{}, fmt.Errorf("%w: unsupported schedule command version", apperror.ErrValidation)
	}
	if command.Version == 0 {
		command.Version = row.CommandVersion
	}
	if command.Version != row.CommandVersion || command.Version != 1 {
		return model.ScheduleCommand{}, fmt.Errorf("%w: unsupported schedule command version", apperror.ErrValidation)
	}
	if command.Type == "" {
		command.Type = row.CommandType
	}
	if !allowedTypes[command.Type] {
		return model.ScheduleCommand{}, fmt.Errorf("%w: command type is not schedulable", apperror.ErrValidation)
	}
	if row.CommandType != "" && row.CommandType != command.Type {
		return model.ScheduleCommand{}, fmt.Errorf("%w: command type digest mismatch", apperror.ErrValidation)
	}
	if len(row.CommandDigest) > 0 {
		digest := md5.Sum(row.CmdPayload)
		canonicalPayload := canonicalJSON(row.CmdPayload)
		canonicalDigest := md5.Sum(canonicalPayload)
		if !bytes.Equal(row.CommandDigest, digest[:]) && !bytes.Equal(row.CommandDigest, canonicalDigest[:]) {
			return model.ScheduleCommand{}, fmt.Errorf("%w: command digest mismatch", apperror.ErrValidation)
		}
	}
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	if command.Amount == "" {
		return model.ScheduleCommand{}, fmt.Errorf("%w: schedule amount is missing", apperror.ErrValidation)
	}
	amount, err := decimal.NewFromString(command.Amount)
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) || !amount.BigInt().IsInt64() {
		return model.ScheduleCommand{}, fmt.Errorf("%w: schedule amount is invalid", apperror.ErrValidation)
	}
	if row.Currency != "" && command.Currency != "" && row.Currency != command.Currency {
		return model.ScheduleCommand{}, fmt.Errorf("%w: schedule command currency does not match schedule", apperror.ErrValidation)
	}
	return command, nil
}

func scheduleCommandErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "unsupported schedule command version") {
		return "SCHEDULE_COMMAND_VERSION_UNSUPPORTED"
	}
	if strings.Contains(err.Error(), "command digest mismatch") {
		return "SCHEDULE_COMMAND_DIGEST_MISMATCH"
	}
	return "SCHEDULE_COMMAND_INVALID"
}

func canonicalJSON(raw []byte) []byte {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}

func scheduleLocation(timezone string, fallback *time.Location) *time.Location {
	if timezone != "" {
		if loc, err := time.LoadLocation(timezone); err == nil {
			return loc
		}
	}
	if fallback != nil {
		return fallback
	}
	return time.UTC
}

func cloneMetadata(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+4)
	maps.Copy(result, input)
	return result
}

func mustDecimal(raw string) decimal.Decimal {
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero
	}
	return value
}

func containsDate(dates []time.Time, candidate time.Time) bool {
	for _, date := range dates {
		if date.Equal(candidate) {
			return true
		}
	}
	return false
}
