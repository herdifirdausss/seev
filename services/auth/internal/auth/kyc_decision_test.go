package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
)

type kycSyncProvisioner struct {
	syncErr   error
	syncCalls int
}

func (p *kycSyncProvisioner) ProvisionUser(context.Context, uuid.UUID, string) error { return nil }
func (p *kycSyncProvisioner) ApplyKycTier(context.Context, uuid.UUID, int) error     { return nil }
func (p *kycSyncProvisioner) SetExecutionSubjectState(context.Context, uuid.UUID, string, int, *time.Time) error {
	p.syncCalls++
	return p.syncErr
}

func TestRetryKYCApplyDoesNotConvergeDifferentTerminalDecision(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, kyc := newTestModule(ctrl, &stubProvisioner{})
	submissionID := uuid.New()
	requestID := uuid.New()
	otherRequestID := uuid.New()
	retry := model.KYCApplyRetry{
		SubmissionID: submissionID, DecisionRequestID: requestID,
		Direction: "upgrade", Level: 2,
	}
	kyc.EXPECT().GetKYCSubmission(gomock.Any(), submissionID).Return(model.KYCSubmission{
		ID: submissionID, Status: "rejected", DecisionRequestID: otherRequestID,
	}, nil)
	kyc.EXPECT().GetKYCDecisionRequest(gomock.Any(), requestID).Return(model.KYCDecisionRequest{
		ID: requestID, SubmissionID: submissionID, RequestedDecision: KYCDecisionApprove,
		Status: "authorized", DecidedBy: "checker-1",
	}, nil)

	err := m.RetryKYCApply(context.Background(), retry)
	require.ErrorIs(t, err, repository.ErrKYCSubmissionNotPending)
}

func TestRetryKYCApplyRaceDoesNotConvergeDifferentTerminalDecision(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, kyc := newTestModule(ctrl, &stubProvisioner{})
	submissionID := uuid.New()
	requestID := uuid.New()
	otherRequestID := uuid.New()
	retry := model.KYCApplyRetry{
		SubmissionID: submissionID, DecisionRequestID: requestID,
		Direction: "upgrade", Level: 2,
	}
	kyc.EXPECT().GetKYCSubmission(gomock.Any(), submissionID).Return(model.KYCSubmission{
		ID: submissionID, Status: "pending", LevelRequested: 2,
	}, nil)
	kyc.EXPECT().GetKYCDecisionRequest(gomock.Any(), requestID).Return(model.KYCDecisionRequest{
		ID: requestID, SubmissionID: submissionID, RequestedDecision: KYCDecisionApprove,
		Status: "authorized", DecidedBy: "checker-1",
	}, nil)
	kyc.EXPECT().ApproveKYCSubmission(gomock.Any(), submissionID, requestID, "checker-1", "", "", gomock.Any(), gomock.Any()).Return(repository.ErrKYCSubmissionNotPending)
	kyc.EXPECT().GetKYCSubmission(gomock.Any(), submissionID).Return(model.KYCSubmission{
		ID: submissionID, Status: "rejected", DecisionRequestID: otherRequestID,
	}, nil)

	err := m.RetryKYCApply(context.Background(), retry)
	require.ErrorIs(t, err, repository.ErrKYCSubmissionNotPending)
}

func TestApprovedKYCQueuesAndRetriesExecutionSubjectSync(t *testing.T) {
	ctrl := gomock.NewController(t)
	syncFailure := errors.New("ledger projection unavailable")
	provisioner := &kycSyncProvisioner{syncErr: syncFailure}
	m, users, _, kyc := newTestModule(ctrl, provisioner)
	ctx := context.Background()
	userID := uuid.New()
	submissionID := uuid.New()
	requestID := uuid.New()
	verifiedUntil := time.Now().Add(24 * time.Hour)
	submission := model.KYCSubmission{
		ID: submissionID, UserID: userID, LevelRequested: 2, Status: "pending",
		DecisionReason: "manual evidence verified",
	}
	kyc.EXPECT().ApproveKYCSubmission(gomock.Any(), submissionID, requestID, "checker-1", "", submission.DecisionReason, gomock.Any(), gomock.Any()).Return(nil)
	var queued model.KYCApplyRetry
	kyc.EXPECT().EnqueueKYCApplyRetry(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, retry model.KYCApplyRetry) error {
		queued = retry
		return nil
	})

	err := m.approveSubmission(ctx, submission, "checker-1", requestID)
	require.ErrorIs(t, err, ErrKYCApplyQueued)
	require.ErrorIs(t, err, syncFailure)
	require.Equal(t, requestID, queued.DecisionRequestID)
	require.Equal(t, 1, provisioner.syncCalls)

	provisioner.syncErr = nil
	terminal := submission
	terminal.Status = "approved"
	terminal.DecisionRequestID = requestID
	kyc.EXPECT().GetKYCSubmission(gomock.Any(), submissionID).Return(terminal, nil)
	kyc.EXPECT().GetKYCDecisionRequest(gomock.Any(), requestID).Return(model.KYCDecisionRequest{
		ID: requestID, SubmissionID: submissionID, RequestedDecision: KYCDecisionApprove,
		RequestReason: submission.DecisionReason, Status: "authorized", DecidedBy: "checker-1",
	}, nil)
	users.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{
		ID: userID, KYCLevel: 2, KYCVerifiedUntil: &verifiedUntil,
	}, nil)
	kyc.EXPECT().MarkKYCDecisionRequestApplied(gomock.Any(), requestID).Return(nil)

	require.NoError(t, m.RetryKYCApply(ctx, queued))
	require.Equal(t, 2, provisioner.syncCalls)
}
