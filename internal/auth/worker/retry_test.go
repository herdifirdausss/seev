package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// Embedding the full interface keeps this focused fake resilient when the
// auth repository gains unrelated methods; only methods used by RunOnce are
// overridden below.
type retryRepoFake struct {
	repository.KYCRepository
	intents   []model.KYCApplyRetry
	succeeded []uuid.UUID
	failures  []retryFailure
}

type retryFailure struct {
	id      uuid.UUID
	count   int
	dead    bool
	lastErr string
}

func (f *retryRepoFake) ClaimKYCApplyRetries(context.Context, int, time.Duration) ([]model.KYCApplyRetry, error) {
	return f.intents, nil
}

func (f *retryRepoFake) MarkKYCApplyRetrySucceeded(_ context.Context, id uuid.UUID) error {
	f.succeeded = append(f.succeeded, id)
	return nil
}

func (f *retryRepoFake) MarkKYCApplyRetryFailure(_ context.Context, id uuid.UUID, count int, _ time.Time, lastErr string, dead bool) error {
	f.failures = append(f.failures, retryFailure{id: id, count: count, lastErr: lastErr, dead: dead})
	return nil
}

type retryApproverFake struct {
	err   error
	calls int
}

func (f *retryApproverFake) RetryKYCApply(context.Context, model.KYCApplyRetry) error {
	f.calls++
	return f.err
}

func TestRetryJobRunOnceMarksSuccess(t *testing.T) {
	intent := model.KYCApplyRetry{ID: uuid.New(), Status: "pending"}
	repo := &retryRepoFake{intents: []model.KYCApplyRetry{intent}}
	approver := &retryApproverFake{}
	lock := scheduler.NewMemoryLock(time.Minute)
	defer lock.Stop()

	job := NewRetryJob(repo, approver, lock, nil)
	require.NoError(t, job.RunOnce(context.Background()))
	require.Equal(t, 1, approver.calls)
	require.Equal(t, []uuid.UUID{intent.ID}, repo.succeeded)
	require.Empty(t, repo.failures)
}

func TestRetryJobRunOnceDeadLettersAfterMaxAttempts(t *testing.T) {
	intent := model.KYCApplyRetry{ID: uuid.New(), RetryCount: maxRetryCount - 1, Status: "pending"}
	repo := &retryRepoFake{intents: []model.KYCApplyRetry{intent}}
	approver := &retryApproverFake{err: errors.New("ledger unavailable")}
	lock := scheduler.NewMemoryLock(time.Minute)
	defer lock.Stop()

	job := NewRetryJob(repo, approver, lock, nil)
	require.NoError(t, job.RunOnce(context.Background()))
	require.Len(t, repo.failures, 1)
	require.True(t, repo.failures[0].dead)
	require.Equal(t, maxRetryCount, repo.failures[0].count)
	require.Contains(t, repo.failures[0].lastErr, "ledger unavailable")
}
