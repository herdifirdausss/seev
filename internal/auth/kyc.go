package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/kycvendor"
)

// ErrKYCApplyQueued marks the safe degraded response when the ledger could
// not apply policy limits inline.  The submission remains pending until the
// durable relay completes; callers can use errors.Is without parsing text.
var ErrKYCApplyQueued = errors.New("auth: kyc apply queued for retry")

// KYCApplyQueuedError retains both the durable intent id and the dependency
// error for logs/tests while exposing ErrKYCApplyQueued through errors.Is.
type KYCApplyQueuedError struct {
	RetryID uuid.UUID
	Cause   error
}

func (e *KYCApplyQueuedError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrKYCApplyQueued.Error()
	}
	return fmt.Sprintf("%s: %v", ErrKYCApplyQueued, e.Cause)
}

func (e *KYCApplyQueuedError) Unwrap() error {
	if e == nil {
		return ErrKYCApplyQueued
	}
	return errors.Join(ErrKYCApplyQueued, e.Cause)
}

type unavailableKYCProvider struct{}

func (unavailableKYCProvider) Name() string { return "unconfigured" }
func (unavailableKYCProvider) Verify(context.Context, kycvendor.Submission) (kycvendor.Decision, error) {
	return kycvendor.Decision{}, ErrKYCProvider
}

type KYCStatus struct {
	Level      int
	Submission *model.KYCSubmission
}

func (m *Module) SubmitKYC(ctx context.Context, userID uuid.UUID, levelRequested int, payload map[string]any) (model.KYCSubmission, error) {
	user, err := m.repo.GetUserByID(ctx, userID)
	if err != nil {
		return model.KYCSubmission{}, err
	}
	if levelRequested != user.KYCLevel+1 || levelRequested < 1 || levelRequested > 2 {
		return model.KYCSubmission{}, ErrKYCLevelSequence
	}
	if latest, latestErr := m.repo.GetLatestKYCSubmission(ctx, userID); latestErr == nil && latest.Status == "pending" {
		return model.KYCSubmission{}, ErrKYCPending
	} else if latestErr != nil && !errors.Is(latestErr, repository.ErrKYCSubmissionNotFound) {
		return model.KYCSubmission{}, latestErr
	}
	submission := model.KYCSubmission{ID: uuid.New(), UserID: userID, LevelRequested: levelRequested, Status: "pending", Payload: payload, Provider: m.kycProvider.Name()}
	if err := m.repo.CreateKYCSubmission(ctx, submission); err != nil {
		if errors.Is(err, repository.ErrKYCSubmissionNotPending) {
			return model.KYCSubmission{}, ErrKYCPending
		}
		return model.KYCSubmission{}, err
	}
	if m.sanctionsChecker != nil {
		subjectName, _ := payload["name"].(string)
		if subjectName == "" {
			subjectName, _ = payload["full_name"].(string)
		}
		birthDate, _ := payload["birth_date"].(string)
		if subjectName != "" {
			verdict, screenErr := m.sanctionsChecker.CheckWithSubject(ctx, "kyc", "kyc", userID, decimal.NewFromInt(1), m.cfg.DefaultCurrency, subjectName, birthDate)
			if screenErr != nil {
				return submission, fmt.Errorf("auth: sanctions screening: %w", screenErr)
			}
			if verdict.Block {
				if rejectErr := m.repo.RejectKYCSubmission(ctx, submission.ID, "sanctions", verdict.Reason); rejectErr != nil {
					return submission, rejectErr
				}
				submission.Status = "rejected"
				return submission, nil
			}
		}
	}
	decision, err := m.kycProvider.Verify(ctx, kycvendor.Submission{UserID: userID, LevelRequested: levelRequested, Payload: payload})
	if err != nil {
		return submission, fmt.Errorf("%w: %v", ErrKYCProvider, err)
	}
	submission.ProviderRef, submission.DecisionReason = decision.Ref, decision.Reason
	switch decision.Verdict {
	case kycvendor.VerdictApprove:
		if err := m.approveSubmission(ctx, submission, "system"); err != nil {
			return submission, err
		}
		submission.Status = "approved"
	case kycvendor.VerdictReject:
		if err := m.repo.RejectKYCSubmission(ctx, submission.ID, "provider", decision.Reason); err != nil {
			return submission, err
		}
		submission.Status = "rejected"
	case kycvendor.VerdictRefer:
		// The row remains pending until an admin decides it.
	default:
		return submission, fmt.Errorf("%w: provider returned unknown verdict %q", ErrKYCProvider, decision.Verdict)
	}
	return submission, nil
}

func (m *Module) KYC(ctx context.Context, userID uuid.UUID) (KYCStatus, error) {
	u, err := m.repo.GetUserByID(ctx, userID)
	if err != nil {
		return KYCStatus{}, err
	}
	result := KYCStatus{Level: u.KYCLevel}
	if s, err := m.repo.GetLatestKYCSubmission(ctx, userID); err == nil {
		result.Submission = &s
	} else if !errors.Is(err, repository.ErrKYCSubmissionNotFound) {
		return KYCStatus{}, err
	}
	return result, nil
}

func (m *Module) ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error) {
	return m.repo.ListKYCSubmissions(ctx, status)
}

func (m *Module) approveSubmission(ctx context.Context, submission model.KYCSubmission, decidedBy string) error {
	err := m.repo.ApproveKYCSubmission(ctx, submission.ID, decidedBy, submission.ProviderRef, submission.DecisionReason, m.provisioner.ApplyKycTier)
	if err == nil || !errors.Is(err, repository.ErrKYCApplyTier) {
		return err
	}

	// ApproveKYCSubmission owns the fast-path transaction. It has rolled back
	// completely at this point, so this insert is intentionally a separate
	// transaction and cannot advance auth_users.
	// Derive the intent id from the submission so concurrent approval callers
	// converge on one durable row and return the same retry id.
	retry := model.KYCApplyRetry{
		ID:            uuid.NewSHA1(uuid.Nil, []byte("kyc-apply:"+submission.ID.String())),
		SubmissionID:  submission.ID,
		UserID:        submission.UserID,
		Level:         submission.LevelRequested,
		Status:        "pending",
		NextAttemptAt: time.Now(),
		LastError:     truncateRetryError(err),
	}
	return m.queueKYCApplyRetry(ctx, retry, err)
}

// DowngradeKYC applies stricter ledger limits before lowering the auth claim.
func (m *Module) DowngradeKYC(ctx context.Context, userID uuid.UUID, level int, decidedBy, reason string) error {
	if reason == "" || level < 0 || level > 2 {
		return ErrValidation
	}
	user, err := m.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.KYCLevel <= level {
		return repository.ErrKYCTierConflict
	}
	err = m.repo.DowngradeKYCLevel(ctx, userID, level, decidedBy, reason, m.provisioner.ApplyKycTier)
	if err == nil || !errors.Is(err, repository.ErrKYCApplyTier) {
		return err
	}
	retry := model.KYCApplyRetry{
		ID:             uuid.NewSHA1(uuid.Nil, []byte(fmt.Sprintf("kyc-downgrade:%s:%d", userID, level))),
		UserID:         userID,
		Level:          level,
		Direction:      "downgrade",
		DecidedBy:      decidedBy,
		DecisionReason: reason,
		Status:         "pending",
		NextAttemptAt:  time.Now(),
		LastError:      truncateRetryError(err),
	}
	return m.queueKYCApplyRetry(ctx, retry, err)
}

func (m *Module) ApproveKYC(ctx context.Context, submissionID uuid.UUID, decidedBy string) error {
	s, err := m.repo.GetKYCSubmission(ctx, submissionID)
	if err != nil {
		return err
	}
	if s.Status != "pending" {
		return repository.ErrKYCSubmissionNotPending
	}
	return m.approveSubmission(ctx, s, decidedBy)
}

func (m *Module) RejectKYC(ctx context.Context, submissionID uuid.UUID, decidedBy, reason string) error {
	if reason == "" {
		return ErrValidation
	}
	return m.repo.RejectKYCSubmission(ctx, submissionID, decidedBy, reason)
}

func truncateRetryError(err error) string {
	if err == nil {
		return ""
	}
	const max = 1024
	message := err.Error()
	if len(message) > max {
		return message[:max]
	}
	return message
}
