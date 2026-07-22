package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/auth/worker"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

func (m *Module) queueKYCApplyRetry(ctx context.Context, retry model.KYCApplyRetry, cause error) error {
	// The request context may already be cancelled because the ledger call
	// timed out. Durable recovery must not depend on the caller staying
	// connected, so use a short detached persistence context.
	queueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if enqueueErr := m.repo.EnqueueKYCApplyRetry(queueCtx, retry); enqueueErr != nil {
		return fmt.Errorf("auth: persist kyc apply retry: %w (original: %v)", enqueueErr, cause)
	}
	kycApplyRetriesQueuedTotal.Inc()
	return &KYCApplyQueuedError{RetryID: retry.ID, Cause: cause}
}

// RetryKYCApply re-runs the full limits-first approval flow for a claimed
// intent. A non-pending submission is already converged (for example an
// admin approved it manually), so it is treated as a successful no-op.
func (m *Module) RetryKYCApply(ctx context.Context, retry model.KYCApplyRetry) error {
	if retry.Direction == "downgrade" {
		user, err := m.repo.GetUserByID(ctx, retry.UserID)
		if err != nil {
			return err
		}
		if user.KYCLevel <= retry.Level {
			return nil
		}
		return m.DowngradeKYC(ctx, retry.UserID, retry.Level, retry.DecidedBy, retry.DecisionReason)
	}
	submission, err := m.repo.GetKYCSubmission(ctx, retry.SubmissionID)
	if err != nil {
		return err
	}
	if submission.Status != "pending" {
		return nil
	}
	err = m.approveSubmission(ctx, submission, "system-retry")
	if errors.Is(err, repository.ErrKYCSubmissionNotPending) {
		// A manual admin approval may have won the row lock after the initial
		// read. Re-read once and converge the intent to succeeded instead of
		// needlessly burning another retry.
		latest, readErr := m.repo.GetKYCSubmission(ctx, retry.SubmissionID)
		if readErr == nil && latest.Status != "pending" {
			return nil
		}
	}
	return err
}

// NewKYCApplyRetryJob wires the auth-owned relay. Keeping construction here
// means cmd/auth-service only depends on the auth facade and never reaches
// into repository internals.
func (m *Module) NewKYCApplyRetryJob(redisClient *redis.Client, logger *slog.Logger) *worker.RetryJob {
	var lock scheduler.LockProvider
	if redisClient != nil {
		instanceID, err := os.Hostname()
		if err != nil || instanceID == "" {
			instanceID = uuid.NewString()
		}
		lock = scheduler.NewRedisLock(redisClient, instanceID)
	} else {
		lock = scheduler.NewMemoryLock(2 * time.Minute)
	}
	return worker.NewRetryJob(m.repo, m, lock, logger)
}
