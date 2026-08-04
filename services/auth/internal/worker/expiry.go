package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/scheduling"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
)

const (
	expiryBatchSize = 100
	expiryLockTTL   = 10 * time.Minute
	expiryReason    = "kyc validity period expired — re-verification required"
)

// downgrader is the small seam ExpiryJob needs from the auth facade —
// deliberately not the full Module, mirroring RescreenJob's sanctionsChecker
// seam so tests can use a fake.
type downgrader interface {
	DowngradeKYC(ctx context.Context, userID uuid.UUID, level int, decidedBy, reason string) error
}

// ExpiryJob periodically downgrades any user whose current KYC level's
// validity window (auth_users.kyc_verified_until) has passed back to L0,
// forcing re-verification through the normal SubmitKYC flow. It reuses
// DowngradeKYC's existing limits-first, durable-retry-on-failure path
// (services/auth/internal/auth/kyc.go) — nothing new is needed on the ledger side.
type ExpiryJob struct {
	repo       repository.KYCRepository
	downgrader downgrader
	lock       scheduler.LockProvider
	interval   time.Duration
	logger     *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewExpiryJob constructs the periodic job. interval<=0 defaults to 1 hour —
// deliberately tighter than RescreenJob's 24h default, since a lapsed
// re-verification requirement (unlike a sanctions rescreen) directly gates
// how much money a user can move.
func NewExpiryJob(repo repository.KYCRepository, dg downgrader, lock scheduler.LockProvider, interval time.Duration, logger *slog.Logger) *ExpiryJob {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = time.Hour
	}
	return &ExpiryJob{repo: repo, downgrader: dg, lock: lock, interval: interval, logger: logger}
}

// Start begins the configured cadence. An initial pass is intentionally
// deferred to the first tick, same rationale as RescreenJob.Start.
func (j *ExpiryJob) Start(ctx context.Context) error {
	j.mu.Lock()
	if j.cancel != nil {
		j.mu.Unlock()
		return fmt.Errorf("auth kyc expiry job already started")
	}
	jobCtx, cancel := context.WithCancel(ctx)
	j.cancel = cancel
	j.done = make(chan struct{})
	done := j.done
	j.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := j.RunOnce(jobCtx); err != nil && jobCtx.Err() == nil {
					j.logger.Error("auth kyc expiry check failed", slog.Any("error", err))
				}
			case <-jobCtx.Done():
				return
			}
		}
	}()
	return nil
}

func (j *ExpiryJob) Stop() {
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

// RunOnce executes one distributed-lock-protected pass and is exported for
// deterministic integration tests and operator-triggered recovery.
func (j *ExpiryJob) RunOnce(ctx context.Context) error {
	if j.repo == nil || j.downgrader == nil || j.lock == nil {
		return fmt.Errorf("auth kyc expiry job is not fully configured")
	}
	ok, err := j.lock.TryLock(ctx, "auth:kyc-expiry", expiryLockTTL)
	if err != nil {
		return fmt.Errorf("auth kyc expiry lock: %w", err)
	}
	if !ok {
		return nil
	}
	defer func() { _ = j.lock.Unlock(context.Background(), "auth:kyc-expiry") }()

	kycExpiryRunsTotal.Inc()
	userIDs, err := j.repo.ListExpiredKYCUsers(ctx, expiryBatchSize)
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		if err := j.downgrader.DowngradeKYC(ctx, userID, 0, "system-expiry", expiryReason); err != nil {
			j.logger.Error("auth kyc expiry downgrade failed", slog.String("user_id", userID.String()), slog.Any("error", err))
			continue
		}
		kycExpiryDowngradesTotal.Inc()
	}
	return nil
}
