package schedule

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	repository_mock "github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

func newMockOccurrenceRepo(t *testing.T) (*repository_mock.MockScheduledOccurrenceRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository_mock.NewMockScheduledOccurrenceRepository(ctrl), ctrl
}

// validScheduleRow returns an active daily schedule with a parseable p2p command
// and all durable-scheduler fields populated so commandFromRow succeeds.
func validScheduleRow(scheduleID, userID uuid.UUID) model.ScheduledTransaction {
	targetID := uuid.New()
	payload, _ := json.Marshal(model.ScheduleCommand{
		Type: "transfer_p2p", Version: 1, Amount: "10000",
		TargetUserID: targetID,
	})
	return model.ScheduledTransaction{
		ID: scheduleID, UserID: userID, Status: "active",
		ScheduleKind: "daily", CommandType: "transfer_p2p",
		CommandVersion: 1, CmdPayload: payload,
		Currency:                    "IDR",
		RunAtDate:                   time.Now().UTC().Add(-24 * time.Hour),
		MaxInfrastructureAttempts:   5,
		RetryWindowSeconds:          86400,
		ConsecutiveFailureThreshold: 3,
		FeeMode:                     "current_policy_with_consent_cap",
	}
}

// validOccurrence returns a due occurrence for the given IDs and attempt count.
func validOccurrence(occurrenceID, scheduleID uuid.UUID, attempts int) model.ScheduledOccurrence {
	return model.ScheduledOccurrence{
		ID: occurrenceID, ScheduleID: scheduleID,
		Status:             model.ScheduleOccurrenceDue,
		IdempotencyKey:     OccurrenceIdempotencyKey(scheduleID, time.Now()),
		AttemptCount:       attempts,
		ScheduledLocalDate: time.Now().UTC(),
	}
}

// ─── ExecuteOccurrence ────────────────────────────────────────────────────────

func TestExecuteOccurrence_InactiveSchedule_CancelledNotFailed(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	userID := uuid.New()
	occurrence := validOccurrence(occurrenceID, scheduleID, 0)
	inactiveRow := validScheduleRow(scheduleID, userID)
	inactiveRow.Status = "paused"

	occ.EXPECT().Claim(gomock.Any(), occurrenceID, "worker", gomock.Any()).Return(occurrence, nil)
	occ.EXPECT().RecordAttempt(gomock.Any(), gomock.Any()).Return(nil)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(inactiveRow, nil)
	occ.EXPECT().FinishAttempt(gomock.Any(), occurrenceID, gomock.Any(), "cancelled", false, "SCHEDULE_NOT_ACTIVE", gomock.Any()).Return(nil)
	occ.EXPECT().SetStatus(gomock.Any(), occurrenceID, model.ScheduleOccurrenceCancelled, "SCHEDULE_NOT_ACTIVE", gomock.Any(), gomock.Any()).Return(nil)

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	ok, err := svc.ExecuteOccurrence(context.Background(), occurrenceID, "worker")

	require.NoError(t, err)
	assert.False(t, ok, "inactive schedule must not count as success")
}

func TestExecuteOccurrence_InfraFailure_MovesToRetryWait(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	userID := uuid.New()
	occurrence := validOccurrence(occurrenceID, scheduleID, 0) // attempt 0 < max 5
	schedRow := validScheduleRow(scheduleID, userID)
	infraErr := assertPlainError{}

	occ.EXPECT().Claim(gomock.Any(), occurrenceID, "worker", gomock.Any()).Return(occurrence, nil)
	occ.EXPECT().RecordAttempt(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(schedRow, nil)
	occ.EXPECT().SetFee(gomock.Any(), occurrenceID, int64(0), gomock.Any()).Return(nil)
	occ.EXPECT().FinishAttempt(gomock.Any(), occurrenceID, gomock.Any(), "infra_failure", true, "LEDGER_INFRA_FAILURE", gomock.Any()).Return(nil)
	occ.EXPECT().SetStatus(gomock.Any(), occurrenceID, model.ScheduleOccurrenceRetryWait, "LEDGER_INFRA_FAILURE", gomock.Any(), gomock.Any()).Return(nil)

	svc := NewDurable(sched, occ, &fakePoster{err: infraErr}, nil, nil, nil)
	ok, err := svc.ExecuteOccurrence(context.Background(), occurrenceID, "worker")

	assert.False(t, ok)
	assert.Error(t, err, "infra error must be surfaced as the returned error")
}

func TestExecuteOccurrence_InfraFailureExhausted_Blocked(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	userID := uuid.New()
	// AttemptCount equals MaxInfrastructureAttempts (5) → exhausted on next infra failure.
	occurrence := validOccurrence(occurrenceID, scheduleID, 5)
	schedRow := validScheduleRow(scheduleID, userID)
	infraErr := assertPlainError{}

	occ.EXPECT().Claim(gomock.Any(), occurrenceID, "worker", gomock.Any()).Return(occurrence, nil)
	occ.EXPECT().RecordAttempt(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(schedRow, nil)
	occ.EXPECT().SetFee(gomock.Any(), occurrenceID, int64(0), gomock.Any()).Return(nil)
	occ.EXPECT().FinishAttempt(gomock.Any(), occurrenceID, gomock.Any(), "infra_failure", true, "LEDGER_INFRA_FAILURE", gomock.Any()).Return(nil)
	occ.EXPECT().BlockSchedule(gomock.Any(), scheduleID, "infrastructure_retry_exhausted").Return(nil)
	occ.EXPECT().SetStatus(gomock.Any(), occurrenceID, model.ScheduleOccurrenceBlocked, "INFRA_RETRY_EXHAUSTED", gomock.Any(), gomock.Any()).Return(nil)

	svc := NewDurable(sched, occ, &fakePoster{err: infraErr}, nil, nil, nil)
	ok, err := svc.ExecuteOccurrence(context.Background(), occurrenceID, "worker")

	assert.False(t, ok)
	assert.Error(t, err)
}

func TestExecuteOccurrence_BusinessFailure_BlockedAtThreshold(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	userID := uuid.New()
	occurrence := validOccurrence(occurrenceID, scheduleID, 0)
	schedRow := validScheduleRow(scheduleID, userID)
	bizErr := apperror.NewBizErr(apperror.ErrInsufficientFunds, "insufficient funds")

	occ.EXPECT().Claim(gomock.Any(), occurrenceID, "worker", gomock.Any()).Return(occurrence, nil)
	occ.EXPECT().RecordAttempt(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(schedRow, nil)
	occ.EXPECT().SetFee(gomock.Any(), occurrenceID, int64(0), gomock.Any()).Return(nil)
	// RecordScheduleBusinessFailure returns blocked=true (threshold reached)
	occ.EXPECT().FinishAttempt(gomock.Any(), occurrenceID, gomock.Any(), "business_failure", false, "LEDGER_BUSINESS_FAILURE", gomock.Any()).Return(nil)
	occ.EXPECT().RecordScheduleBusinessFailure(gomock.Any(), scheduleID, "LEDGER_BUSINESS_FAILURE", schedRow.ConsecutiveFailureThreshold).Return(true, nil)
	occ.EXPECT().SetStatus(gomock.Any(), occurrenceID, model.ScheduleOccurrenceBlocked, "LEDGER_BUSINESS_FAILURE", gomock.Any(), gomock.Any()).Return(nil)

	svc := NewDurable(sched, occ, &fakePoster{err: bizErr}, nil, nil, nil)
	ok, err := svc.ExecuteOccurrence(context.Background(), occurrenceID, "worker")

	assert.False(t, ok)
	assert.Error(t, err)
}

func TestExecuteOccurrence_ErrAlreadyPosted_TreatedAsSuccess(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	userID := uuid.New()
	occurrence := validOccurrence(occurrenceID, scheduleID, 0)
	schedRow := validScheduleRow(scheduleID, userID)

	occ.EXPECT().Claim(gomock.Any(), occurrenceID, "worker", gomock.Any()).Return(occurrence, nil)
	occ.EXPECT().RecordAttempt(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(schedRow, nil)
	occ.EXPECT().SetFee(gomock.Any(), occurrenceID, int64(0), gomock.Any()).Return(nil)
	occ.EXPECT().FinishAttempt(gomock.Any(), occurrenceID, gomock.Any(), "succeeded", false, "", gomock.Any()).Return(nil)
	occ.EXPECT().SetStatus(gomock.Any(), occurrenceID, model.ScheduleOccurrenceSucceeded, "", gomock.Any(), gomock.Any()).Return(nil)
	occ.EXPECT().RecordScheduleSuccess(gomock.Any(), scheduleID).Return(nil)
	occ.EXPECT().SetScheduleLastRun(gomock.Any(), scheduleID, gomock.Any(), false).Return(nil)

	svc := NewDurable(sched, occ, &fakePoster{err: apperror.ErrAlreadyPosted}, nil, nil, nil)
	ok, err := svc.ExecuteOccurrence(context.Background(), occurrenceID, "worker")

	require.NoError(t, err)
	assert.True(t, ok, "ErrAlreadyPosted must be counted as a successful execution")
}

// ─── RetryOccurrence ─────────────────────────────────────────────────────────

func TestRetryOccurrence_NonTerminalStatus_Rejected(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	occurrenceID := uuid.New()
	occ.EXPECT().Get(gomock.Any(), occurrenceID).Return(model.ScheduledOccurrence{
		ID: occurrenceID, Status: model.ScheduleOccurrenceRetryWait,
	}, nil)

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	err := svc.RetryOccurrence(context.Background(), occurrenceID)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestRetryOccurrence_ScheduleBlocked_Rejected(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	occ.EXPECT().Get(gomock.Any(), occurrenceID).Return(model.ScheduledOccurrence{
		ID: occurrenceID, ScheduleID: scheduleID, Status: model.ScheduleOccurrenceBlocked,
	}, nil)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(model.ScheduledTransaction{
		ID: scheduleID, Status: "blocked",
	}, nil)

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	err := svc.RetryOccurrence(context.Background(), occurrenceID)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestRetryOccurrence_ValidTerminalStatus_RequeuesViaSetStatus(t *testing.T) {
	// MockScheduledOccurrenceRepository does not implement Retry(), so the
	// code falls back to SetStatus — which is the correct observable behavior
	// when the Retry method is unavailable.
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	occurrenceID := uuid.New()
	occ.EXPECT().Get(gomock.Any(), occurrenceID).Return(model.ScheduledOccurrence{
		ID: occurrenceID, ScheduleID: scheduleID, Status: model.ScheduleOccurrenceFailedBusiness,
	}, nil)
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(model.ScheduledTransaction{
		ID: scheduleID, Status: "active",
	}, nil)
	occ.EXPECT().SetStatus(gomock.Any(), occurrenceID, model.ScheduleOccurrenceReady, "", gomock.Any(), gomock.Any()).Return(nil)

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	err := svc.RetryOccurrence(context.Background(), occurrenceID)
	require.NoError(t, err)
}

// ─── ConfirmFeeCap ───────────────────────────────────────────────────────────

func TestConfirmFeeCap_NegativeAmount_Rejected(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	err := svc.ConfirmFeeCap(context.Background(), uuid.New(), uuid.New(), -1)
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestConfirmFeeCap_WrongOwner_Rejected(t *testing.T) {
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	owner := uuid.New()
	caller := uuid.New() // different from owner
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(model.ScheduledTransaction{
		ID: scheduleID, UserID: owner,
	}, nil)

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	err := svc.ConfirmFeeCap(context.Background(), scheduleID, caller, 100)
	assert.ErrorIs(t, err, apperror.ErrScheduledTransactionNotOwned)
}

func TestConfirmFeeCap_RepoDoesNotSupportIt_Rejected(t *testing.T) {
	// MockScheduledOccurrenceRepository does not implement ConfirmFeeCap().
	// The service must return ErrValidation in this case.
	sched, ctrl1 := newMockScheduleRepo(t)
	defer ctrl1.Finish()
	occ, ctrl2 := newMockOccurrenceRepo(t)
	defer ctrl2.Finish()

	scheduleID := uuid.New()
	userID := uuid.New()
	sched.EXPECT().GetByID(gomock.Any(), scheduleID).Return(model.ScheduledTransaction{
		ID: scheduleID, UserID: userID,
	}, nil)

	svc := NewDurable(sched, occ, &fakePoster{}, nil, nil, nil)
	err := svc.ConfirmFeeCap(context.Background(), scheduleID, userID, 500)
	assert.ErrorIs(t, err, apperror.ErrValidation, "repository without ConfirmFeeCap must be rejected as unavailable")
}
