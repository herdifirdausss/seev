package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/services/auth/internal/adapter/kycvendor"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
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
	// VerifiedUntil is Level's validity deadline (nil for L0 or a level
	// approved before kyc_verified_until existed). Past this timestamp the
	// level is still enforced until the expiry worker downgrades it.
	VerifiedUntil *time.Time
}

// defaultKYCValidityTTL is used whenever Config.KYCValidityTTL is unset
// (zero) — every Config{} literal that predates this field keeps working
// with a sane default rather than expiring KYC approvals immediately.
const defaultKYCValidityTTL = 365 * 24 * time.Hour

func (m *Module) SubmitKYC(ctx context.Context, userID uuid.UUID, levelRequested int, payload map[string]any) (model.KYCSubmission, error) {
	user, err := m.users.GetUserByID(ctx, userID)
	if err != nil {
		return model.KYCSubmission{}, err
	}
	if levelRequested != user.KYCLevel+1 || levelRequested < 1 || levelRequested > 2 {
		return model.KYCSubmission{}, ErrKYCLevelSequence
	}
	if latest, latestErr := m.kyc.GetLatestKYCSubmission(ctx, userID); latestErr == nil && latest.Status == "pending" {
		return model.KYCSubmission{}, ErrKYCPending
	} else if latestErr != nil && !errors.Is(latestErr, repository.ErrKYCSubmissionNotFound) {
		return model.KYCSubmission{}, latestErr
	}
	submission := model.KYCSubmission{ID: uuid.New(), UserID: userID, LevelRequested: levelRequested, Status: "pending", Payload: payload, Provider: m.kycProvider.Name()}
	if err := m.kyc.CreateKYCSubmission(ctx, submission); err != nil {
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
				request, requestErr := m.authorizeAutomatedKYCDecision(ctx, submission.ID,
					KYCDecisionReject, verdict.Reason, "sanctions-screening", "system-sanctions-policy")
				if requestErr != nil {
					return submission, requestErr
				}
				if rejectErr := m.kyc.RejectKYCSubmission(ctx, submission.ID, request.ID, request.DecidedBy, request.RequestReason); rejectErr != nil {
					return submission, rejectErr
				}
				if _, markErr := m.markKYCDecisionApplied(ctx, request); markErr != nil {
					return submission, markErr
				}
				submission.Status = "rejected"
				return submission, nil
			}
		}
	}
	decision, err := m.kycProvider.Verify(ctx, kycvendor.Submission{UserID: userID, LevelRequested: levelRequested, Payload: payload})
	if err != nil {
		return submission, fmt.Errorf("%w: %w", ErrKYCProvider, err)
	}
	submission.ProviderRef, submission.DecisionReason = decision.Ref, decision.Reason
	switch decision.Verdict {
	case kycvendor.VerdictApprove:
		request, requestErr := m.authorizeAutomatedKYCDecision(ctx, submission.ID,
			KYCDecisionApprove, decision.Reason, "provider:"+m.kycProvider.Name(), "system-kyc-policy")
		if requestErr != nil {
			return submission, requestErr
		}
		submission.DecisionReason = request.RequestReason
		if err := m.approveSubmission(ctx, submission, request.DecidedBy, request.ID); err != nil {
			return submission, err
		}
		if _, markErr := m.markKYCDecisionApplied(ctx, request); markErr != nil {
			return submission, markErr
		}
		submission.Status = "approved"
	case kycvendor.VerdictReject:
		request, requestErr := m.authorizeAutomatedKYCDecision(ctx, submission.ID,
			KYCDecisionReject, decision.Reason, "provider:"+m.kycProvider.Name(), "system-kyc-policy")
		if requestErr != nil {
			return submission, requestErr
		}
		if err := m.kyc.RejectKYCSubmission(ctx, submission.ID, request.ID, request.DecidedBy, request.RequestReason); err != nil {
			return submission, err
		}
		if _, markErr := m.markKYCDecisionApplied(ctx, request); markErr != nil {
			return submission, markErr
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
	u, err := m.users.GetUserByID(ctx, userID)
	if err != nil {
		return KYCStatus{}, err
	}
	result := KYCStatus{Level: u.KYCLevel, VerifiedUntil: u.KYCVerifiedUntil}
	if s, err := m.kyc.GetLatestKYCSubmission(ctx, userID); err == nil {
		result.Submission = &s
	} else if !errors.Is(err, repository.ErrKYCSubmissionNotFound) {
		return KYCStatus{}, err
	}
	return result, nil
}

func (m *Module) ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error) {
	return m.kyc.ListKYCSubmissions(ctx, status)
}

func (m *Module) approveSubmission(ctx context.Context, submission model.KYCSubmission, decidedBy string, decisionRequestID uuid.UUID) error {
	ttl := m.cfg.KYCValidityTTL
	if ttl == 0 {
		ttl = defaultKYCValidityTTL
	}
	validUntil := time.Now().Add(ttl)
	err := m.kyc.ApproveKYCSubmission(ctx, submission.ID, decisionRequestID, decidedBy, submission.ProviderRef, submission.DecisionReason, validUntil, m.provisioner.ApplyKycTier)
	if err == nil {
		if syncer, ok := m.provisioner.(ExecutionSubjectProvisioner); ok {
			if syncErr := syncer.SetExecutionSubjectState(ctx, submission.UserID, model.StatusActive, submission.LevelRequested, &validUntil); syncErr != nil {
				cause := fmt.Errorf("auth: synchronize approved KYC execution subject: %w", syncErr)
				return m.queueApprovedKYCRetry(ctx, submission, decisionRequestID, decidedBy, cause)
			}
		}
		return nil
	}
	if err == nil || !errors.Is(err, repository.ErrKYCApplyTier) {
		return err
	}

	// ApproveKYCSubmission owns the fast-path transaction. It has rolled back
	// completely at this point, so this insert is intentionally a separate
	// transaction and cannot advance auth_users.
	// Derive the intent id from the submission so concurrent approval callers
	// converge on one durable row and return the same retry id.
	return m.queueApprovedKYCRetry(ctx, submission, decisionRequestID, decidedBy, err)
}

func (m *Module) queueApprovedKYCRetry(ctx context.Context, submission model.KYCSubmission, decisionRequestID uuid.UUID, decidedBy string, cause error) error {
	retry := model.KYCApplyRetry{
		ID:                uuid.NewSHA1(uuid.Nil, []byte("kyc-apply:"+submission.ID.String())),
		SubmissionID:      submission.ID,
		DecisionRequestID: decisionRequestID,
		UserID:            submission.UserID,
		Level:             submission.LevelRequested,
		DecidedBy:         decidedBy,
		DecisionReason:    submission.DecisionReason,
		Status:            "pending",
		NextAttemptAt:     time.Now(),
		LastError:         truncateRetryError(cause),
	}
	return m.queueKYCApplyRetry(ctx, retry, cause)
}

func (m *Module) syncApprovedKYCExecutionSubject(ctx context.Context, submission model.KYCSubmission) error {
	syncer, ok := m.provisioner.(ExecutionSubjectProvisioner)
	if !ok {
		return nil
	}
	user, err := m.users.GetUserByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("auth: read approved KYC subject state: %w", err)
	}
	if user.KYCLevel != submission.LevelRequested || user.KYCVerifiedUntil == nil {
		return repository.ErrKYCTierConflict
	}
	if err := syncer.SetExecutionSubjectState(ctx, submission.UserID, model.StatusActive, submission.LevelRequested, user.KYCVerifiedUntil); err != nil {
		return fmt.Errorf("auth: synchronize approved KYC execution subject: %w", err)
	}
	return nil
}

// DowngradeKYC applies stricter ledger limits before lowering the auth claim.
func (m *Module) DowngradeKYC(ctx context.Context, userID uuid.UUID, level int, decidedBy, reason string) error {
	if reason == "" || level < 0 || level > 2 {
		return ErrValidation
	}
	user, err := m.users.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.KYCLevel <= level {
		return repository.ErrKYCTierConflict
	}
	err = m.kyc.DowngradeKYCLevel(ctx, userID, level, decidedBy, reason, m.provisioner.ApplyKycTier)
	if err == nil {
		if syncer, ok := m.provisioner.(ExecutionSubjectProvisioner); ok {
			if syncErr := syncer.SetExecutionSubjectState(ctx, userID, model.StatusActive, level, nil); syncErr != nil {
				return fmt.Errorf("auth: synchronize downgraded KYC execution subject: %w", syncErr)
			}
		}
		return nil
	}
	if err == nil || !errors.Is(err, repository.ErrKYCApplyTier) {
		return err
	}
	retry := model.KYCApplyRetry{
		ID:             uuid.NewSHA1(uuid.Nil, fmt.Appendf(nil, "kyc-downgrade:%s:%d", userID, level)),
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
	_, err := m.RequestKYCDecision(ctx, submissionID, decidedBy, KYCDecisionApprove, "manual KYC approval request")
	return err
}

func (m *Module) RejectKYC(ctx context.Context, submissionID uuid.UUID, decidedBy, reason string) error {
	if reason == "" {
		return ErrValidation
	}
	_, err := m.RequestKYCDecision(ctx, submissionID, decidedBy, KYCDecisionReject, reason)
	return err
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
