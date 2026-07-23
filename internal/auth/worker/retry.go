// Package worker contains auth-owned background jobs.  It is deliberately
// not part of auth's public facade; only internal/auth and cmd/auth-service
// construct the relay.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

const (
	defaultBatchSize = 50
	maxRetryCount    = 10
	retryLease       = 45 * time.Second
	retryTick        = 30 * time.Second
	lockTTL          = 5 * time.Minute
)

type retryApprover interface {
	RetryKYCApply(context.Context, model.KYCApplyRetry) error
}

// RetryJob drains auth's durable KYC policy-application intents. Claiming is
// DB-transactional and leased, while the distributed lock (the same
// pkg/scheduler LockProvider used by the other service workers) ensures only
// one relay pass runs across auth replicas. The shared cron parser is
// intentionally minute-granular, so this worker uses an explicit 30-second
// ticker while retaining the shared lock semantics.
type RetryJob struct {
	repo     repository.KYCRepository
	approver retryApprover
	lock     scheduler.LockProvider
	logger   *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewRetryJob(repo repository.KYCRepository, approver retryApprover, lock scheduler.LockProvider, logger *slog.Logger) *RetryJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &RetryJob{repo: repo, approver: approver, lock: lock, logger: logger}
}

// Start begins the 30-second relay cadence.  It is safe to call once; callers
// should call Stop during auth-service shutdown.
func (j *RetryJob) Start(ctx context.Context) error {
	j.mu.Lock()
	if j.cancel != nil {
		j.mu.Unlock()
		return fmt.Errorf("auth kyc retry job already started")
	}
	jobCtx, cancel := context.WithCancel(ctx)
	j.cancel = cancel
	j.done = make(chan struct{})
	done := j.done
	j.mu.Unlock()

	go func() {
		defer close(done)
		if err := j.RunOnce(jobCtx); err != nil && jobCtx.Err() == nil {
			j.logger.Error("auth kyc retry relay initial pass failed", slog.Any("error", err))
		}
		ticker := time.NewTicker(retryTick)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := j.RunOnce(jobCtx); err != nil {
					j.logger.Error("auth kyc retry relay failed", slog.Any("error", err))
				}
			case <-jobCtx.Done():
				return
			}
		}
	}()
	return nil
}

func (j *RetryJob) Stop() {
	j.mu.Lock()
	cancel, done := j.cancel, j.done
	j.cancel, j.done = nil, nil
	j.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	if stopper, ok := j.lock.(interface{ Stop() }); ok {
		stopper.Stop()
	}
}

// RunOnce executes one locked relay pass immediately.  It is exported for
// deterministic integration/chaos tests and for an eventual operator trigger.
func (j *RetryJob) RunOnce(ctx context.Context) error {
	if j.repo == nil || j.approver == nil || j.lock == nil {
		return fmt.Errorf("auth kyc retry job is not fully configured")
	}
	ok, err := j.lock.TryLock(ctx, "auth:kyc-apply-retry", lockTTL)
	if err != nil {
		return fmt.Errorf("auth kyc retry lock: %w", err)
	}
	if !ok {
		return nil
	}
	defer func() { _ = j.lock.Unlock(context.Background(), "auth:kyc-apply-retry") }()

	intents, err := j.repo.ClaimKYCApplyRetries(ctx, defaultBatchSize, retryLease)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if err := j.process(ctx, intent); err != nil {
			j.logger.Error("auth kyc retry intent failed", slog.String("retry_id", intent.ID.String()), slog.Any("error", err))
		}
	}
	return nil
}

func (j *RetryJob) process(ctx context.Context, intent model.KYCApplyRetry) error {
	kycApplyRetryAttemptsTotal.Inc()
	if err := j.approver.RetryKYCApply(ctx, intent); err == nil {
		return j.repo.MarkKYCApplyRetrySucceeded(ctx, intent.ID)
	} else {
		nextCount := intent.RetryCount + 1
		dead := nextCount >= maxRetryCount
		next := time.Now().Add(retryBackoff(nextCount))
		if dead {
			kycApplyRetriesDeadTotal.Inc()
			j.logger.Error("auth kyc retry intent dead",
				slog.String("retry_id", intent.ID.String()), slog.Int("retry_count", nextCount), slog.Any("error", err))
		}
		return j.repo.MarkKYCApplyRetryFailure(ctx, intent.ID, nextCount, next, truncateError(err), dead)
	}
}

func retryBackoff(retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	base := 30 * time.Second
	for i := 1; i < retryCount && base < 30*time.Minute; i++ {
		base *= 2
	}
	if base > 30*time.Minute {
		base = 30 * time.Minute
	}
	// Add bounded jitter so several replicas recovering together do not form
	// a thundering herd.  The cap keeps a retry within the operational window.
	return base + time.Duration(rand.Int63n(int64(base/4)+1))
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}
