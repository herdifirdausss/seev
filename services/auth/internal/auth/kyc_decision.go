package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
)

const (
	KYCDecisionApprove = "approve"
	KYCDecisionReject  = "reject"
)

type KYCDecisionRequest = model.KYCDecisionRequest

var (
	ErrKYCDecisionRequestNotFound = repository.ErrKYCDecisionRequestNotFound
	ErrKYCDecisionRequestPending  = repository.ErrKYCDecisionRequestPending
	ErrKYCDecisionSelfApproval    = repository.ErrKYCDecisionSelfApproval
	ErrKYCDecisionAlreadyDecided  = repository.ErrKYCDecisionAlreadyDecided
)

// RequestKYCDecision is the maker half of manual KYC review. Repeated
// delivery of the exact same request is idempotent; a conflicting pending
// proposal is reported instead of silently replacing its audit evidence.
func (m *Module) RequestKYCDecision(ctx context.Context, submissionID uuid.UUID, requestedBy, decision, reason string) (KYCDecisionRequest, error) {
	if requestedBy == "" || reason == "" || (decision != KYCDecisionApprove && decision != KYCDecisionReject) {
		return KYCDecisionRequest{}, ErrValidation
	}
	submission, err := m.kyc.GetKYCSubmission(ctx, submissionID)
	if err != nil {
		return KYCDecisionRequest{}, err
	}
	if submission.Status != "pending" {
		return KYCDecisionRequest{}, repository.ErrKYCSubmissionNotPending
	}
	request := KYCDecisionRequest{
		ID: uuid.New(), SubmissionID: submissionID, RequestedDecision: decision,
		RequestReason: reason, RequestedBy: requestedBy, Status: "pending",
		CreatedAt: time.Now().UTC(),
	}
	if err := m.kyc.CreateKYCDecisionRequest(ctx, request); err != nil {
		if !errors.Is(err, repository.ErrKYCDecisionRequestPending) {
			return KYCDecisionRequest{}, err
		}
		existing, readErr := m.kyc.GetPendingKYCDecisionRequest(ctx, submissionID)
		if readErr != nil {
			return KYCDecisionRequest{}, readErr
		}
		if existing.RequestedBy == requestedBy &&
			existing.RequestedDecision == decision &&
			existing.RequestReason == reason {
			return existing, nil
		}
		return existing, repository.ErrKYCDecisionRequestPending
	}
	return request, nil
}

func (m *Module) authorizeAutomatedKYCDecision(ctx context.Context, submissionID uuid.UUID, decision, reason, requestedBy, decidedBy string) (KYCDecisionRequest, error) {
	if reason == "" {
		reason = "automated KYC decision"
	}
	request, err := m.RequestKYCDecision(ctx, submissionID, requestedBy, decision, reason)
	if err != nil {
		return KYCDecisionRequest{}, err
	}
	return m.kyc.AuthorizeKYCDecisionRequest(ctx, request.ID, decidedBy)
}

// ApproveKYCDecision is the checker half. Authorization is committed before
// the ledger policy call, so a timeout can be retried without losing who
// approved the effect. A crash after authorization is recoverable by safely
// calling this method again.
func (m *Module) ApproveKYCDecision(ctx context.Context, requestID uuid.UUID, decidedBy string) (KYCDecisionRequest, error) {
	if decidedBy == "" {
		return KYCDecisionRequest{}, ErrValidation
	}
	request, err := m.kyc.AuthorizeKYCDecisionRequest(ctx, requestID, decidedBy)
	if errors.Is(err, repository.ErrKYCDecisionAlreadyDecided) {
		request, err = m.kyc.GetKYCDecisionRequest(ctx, requestID)
		if err == nil && request.Status != "authorized" {
			err = repository.ErrKYCDecisionAlreadyDecided
		}
	}
	if err != nil {
		return KYCDecisionRequest{}, err
	}
	if request.RequestedBy == decidedBy {
		return KYCDecisionRequest{}, repository.ErrKYCDecisionSelfApproval
	}
	if request.AppliedAt != nil {
		return request, nil
	}

	submission, err := m.kyc.GetKYCSubmission(ctx, request.SubmissionID)
	if err != nil {
		return KYCDecisionRequest{}, err
	}
	expectedStatus := "approved"
	if request.RequestedDecision == KYCDecisionReject {
		expectedStatus = "rejected"
	}
	if submission.Status != "pending" {
		if submission.Status != expectedStatus || submission.DecisionRequestID != request.ID {
			return KYCDecisionRequest{}, repository.ErrKYCSubmissionNotPending
		}
		if submission.Status == "approved" {
			if err := m.syncApprovedKYCExecutionSubject(ctx, submission); err != nil {
				return request, err
			}
		}
		return m.markKYCDecisionApplied(ctx, request)
	}

	if request.RequestedDecision == KYCDecisionApprove {
		submission.DecisionReason = request.RequestReason
		if err := m.approveSubmission(ctx, submission, request.DecidedBy, request.ID); err != nil {
			return request, err
		}
	} else {
		if err := m.kyc.RejectKYCSubmission(ctx, submission.ID, request.ID, request.DecidedBy, request.RequestReason); err != nil {
			return request, err
		}
	}
	return m.markKYCDecisionApplied(ctx, request)
}

func (m *Module) markKYCDecisionApplied(ctx context.Context, request KYCDecisionRequest) (KYCDecisionRequest, error) {
	if err := m.kyc.MarkKYCDecisionRequestApplied(ctx, request.ID); err != nil {
		return KYCDecisionRequest{}, err
	}
	return m.kyc.GetKYCDecisionRequest(ctx, request.ID)
}

// RejectKYCDecision is a checker rejection of the maker's proposal. It does
// not reject the customer's KYC submission; another reviewed proposal may be
// created afterward.
func (m *Module) RejectKYCDecision(ctx context.Context, requestID uuid.UUID, decidedBy, reason string) (KYCDecisionRequest, error) {
	if decidedBy == "" || reason == "" {
		return KYCDecisionRequest{}, ErrValidation
	}
	request, err := m.kyc.RejectKYCDecisionRequest(ctx, requestID, decidedBy, reason)
	if err != nil {
		return KYCDecisionRequest{}, err
	}
	return request, nil
}

func (m *Module) ListKYCDecisionRequests(ctx context.Context, status string, limit int) ([]KYCDecisionRequest, error) {
	if status != "" && status != "pending" && status != "authorized" && status != "rejected" {
		return nil, fmt.Errorf("%w: invalid KYC decision request status", ErrValidation)
	}
	return m.kyc.ListKYCDecisionRequests(ctx, status, limit)
}
