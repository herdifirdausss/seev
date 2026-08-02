package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

type expiryRepoFake struct {
	repository.KYCRepository
	userIDs []uuid.UUID
}

func (f *expiryRepoFake) ListExpiredKYCUsers(context.Context, int) ([]uuid.UUID, error) {
	return f.userIDs, nil
}

type expiryDowngraderFake struct {
	calls []struct {
		userID uuid.UUID
		level  int
		by     string
	}
	err error
}

func (f *expiryDowngraderFake) DowngradeKYC(_ context.Context, userID uuid.UUID, level int, decidedBy, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, struct {
		userID uuid.UUID
		level  int
		by     string
	}{userID, level, decidedBy})
	return nil
}

func TestExpiryJobRunOnceDowngradesEveryExpiredUserToL0(t *testing.T) {
	userA, userB := uuid.New(), uuid.New()
	repo := &expiryRepoFake{userIDs: []uuid.UUID{userA, userB}}
	dg := &expiryDowngraderFake{}
	lock := scheduler.NewMemoryLock(time.Minute)
	defer lock.Stop()

	job := NewExpiryJob(repo, dg, lock, time.Hour, nil)
	require.NoError(t, job.RunOnce(context.Background()))

	require.Len(t, dg.calls, 2)
	for _, call := range dg.calls {
		require.Equal(t, 0, call.level)
		require.Equal(t, "system-expiry", call.by)
	}
}

func TestExpiryJobRunOnceContinuesPastAPerUserDowngradeFailure(t *testing.T) {
	repo := &expiryRepoFake{userIDs: []uuid.UUID{uuid.New()}}
	dg := &expiryDowngraderFake{err: requireErr("ledger unavailable")}
	lock := scheduler.NewMemoryLock(time.Minute)
	defer lock.Stop()

	job := NewExpiryJob(repo, dg, lock, time.Hour, nil)
	require.NoError(t, job.RunOnce(context.Background()), "a per-user downgrade failure must not fail the whole pass")
	require.Empty(t, dg.calls)
}

func TestExpiryJobRejectsMissingDependencies(t *testing.T) {
	job := NewExpiryJob(nil, nil, nil, time.Hour, nil)
	require.Error(t, job.RunOnce(context.Background()))
}

func TestExpiryJobDefaultsIntervalWhenNonPositive(t *testing.T) {
	job := NewExpiryJob(nil, nil, nil, 0, nil)
	require.Equal(t, time.Hour, job.interval)
}
