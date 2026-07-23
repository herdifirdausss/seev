package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

const (
	rescreenBatchSize = 100
	rescreenLockTTL   = 10 * time.Minute
)

// RescreenJob periodically re-submits approved KYC subjects to the same
// sanctions rule used at KYC time. A block is intentionally only an audited
// screening decision: this job never changes KYC level or account state.
type RescreenJob struct {
	repo     repository.KYCRepository
	checker  sanctionsChecker
	lock     scheduler.LockProvider
	currency string
	interval time.Duration
	logger   *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type sanctionsChecker interface {
	CheckWithSubject(context.Context, string, string, uuid.UUID, decimal.Decimal, string, string, string) (fraudcheck.Verdict, error)
}

// NewRescreenJob constructs the periodic job. The checker is accepted through
// the small auth-owned seam so tests can use a deterministic fake and the
// production composition can use pkg/fraudcheck.Client.
func NewRescreenJob(repo repository.KYCRepository, checker interface {
	CheckWithSubject(context.Context, string, string, uuid.UUID, decimal.Decimal, string, string, string) (fraudcheck.Verdict, error)
}, lock scheduler.LockProvider, currency string, interval time.Duration, logger *slog.Logger) *RescreenJob {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &RescreenJob{repo: repo, checker: checker, lock: lock, currency: currency, interval: interval, logger: logger}
}

// Start begins the configured cadence. An initial pass is intentionally
// deferred to the first tick so service startup cannot create a sanctions
// thundering herd across replicas.
func (j *RescreenJob) Start(ctx context.Context) error {
	j.mu.Lock()
	if j.cancel != nil {
		j.mu.Unlock()
		return fmt.Errorf("auth kyc rescreen job already started")
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
					j.logger.Error("auth kyc sanctions rescreen failed", slog.Any("error", err))
				}
			case <-jobCtx.Done():
				return
			}
		}
	}()
	return nil
}

func (j *RescreenJob) Stop() {
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
func (j *RescreenJob) RunOnce(ctx context.Context) error {
	if j.repo == nil || j.checker == nil || j.lock == nil {
		return fmt.Errorf("auth kyc rescreen job is not fully configured")
	}
	ok, err := j.lock.TryLock(ctx, "auth:kyc-sanctions-rescreen", rescreenLockTTL)
	if err != nil {
		return fmt.Errorf("auth kyc rescreen lock: %w", err)
	}
	if !ok {
		return nil
	}
	defer func() { _ = j.lock.Unlock(context.Background(), "auth:kyc-sanctions-rescreen") }()

	kycRescreenRunsTotal.Inc()
	subjects, err := j.repo.ListKYCRescreenSubjects(ctx, rescreenBatchSize)
	if err != nil {
		return err
	}
	for _, subject := range subjects {
		kycRescreenSubjectsTotal.Inc()
		if _, err := j.checker.CheckWithSubject(ctx, "kyc_rescreen", "kyc", subject.UserID, decimal.NewFromInt(1), j.currency, subject.Name, subject.BirthDate); err != nil {
			j.logger.Error("auth kyc sanctions subject failed", slog.String("user_id", subject.UserID.String()), slog.Any("error", err))
		}
	}
	return nil
}
