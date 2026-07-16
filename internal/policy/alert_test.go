package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/pkg/cache"
)

// stubAlertFn records every call via a buffered channel so tests can
// deterministically observe async delivery (fireFailOpenAlert dispatches in
// its own goroutine) without a sleep-and-hope race.
type stubAlertFn struct {
	calls chan string
	err   error
}

func newStubAlertFn() *stubAlertFn {
	return &stubAlertFn{calls: make(chan string, 8)}
}

func (s *stubAlertFn) fn(ctx context.Context, severity, message string) error {
	s.calls <- severity + ":" + message
	return s.err
}

func (s *stubAlertFn) awaitCall(t *testing.T) string {
	t.Helper()
	select {
	case c := <-s.calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for alert delivery")
		return ""
	}
}

func (s *stubAlertFn) assertNoCall(t *testing.T) {
	t.Helper()
	select {
	case c := <-s.calls:
		t.Fatalf("expected no alert call, got %q", c)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestCheck_RepoError_FailsOpen_FiresAlert(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").
		Return(Limit{}, false, errors.New("db unreachable"))

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	stub := newStubAlertFn()
	e := New(repo, cache.NewMemoryCounter(), loc, nil, WithAlertFunc(stub.fn))
	t.Cleanup(func() { e.counter.(*cache.MemoryCounter).Stop() })

	allowed, _, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(1000))
	require.NoError(t, err)
	assert.True(t, allowed)

	call := stub.awaitCall(t)
	assert.Contains(t, call, "warning:")
	assert.Contains(t, call, "load limit failed")
}

func TestCheck_CounterError_FailsOpen_FiresAlert(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").Return(Limit{
		TransactionType: "transfer_p2p", MaxDailyAmount: int64Ptr(1000), Enabled: true,
	}, true, nil)

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	stub := newStubAlertFn()
	e := New(repo, failingCounter{}, loc, nil, WithAlertFunc(stub.fn))

	allowed, _, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(999999))
	require.NoError(t, err)
	assert.True(t, allowed)

	call := stub.awaitCall(t)
	assert.Contains(t, call, "read daily amount counter failed")
}

func TestCheck_NilAlertFn_NeverPanicsNeverBlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").
		Return(Limit{}, false, errors.New("db unreachable"))

	e, _ := newEngine(t, repo) // newEngine (policy_test.go) constructs with no alert func

	assert.NotPanics(t, func() {
		allowed, _, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(1000))
		require.NoError(t, err)
		assert.True(t, allowed)
	})
}

// TestFireFailOpenAlert_ThrottledWithinWindow_FiresOnceOnly proves the
// docs/plan/25 Task T5 requirement directly: "Redis outage != alert storm"
// — many fail-open events within one throttle window must fire at most one
// alert.
func TestFireFailOpenAlert_ThrottledWithinWindow_FiresOnceOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").
		Return(Limit{}, false, errors.New("db unreachable")).Times(5)

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	stub := newStubAlertFn()
	e := New(repo, cache.NewMemoryCounter(), loc, nil, WithAlertFunc(stub.fn), WithAlertThrottle(time.Minute))
	t.Cleanup(func() { e.counter.(*cache.MemoryCounter).Stop() })

	for i := 0; i < 5; i++ {
		_, _, _, err := e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(1000))
		require.NoError(t, err)
	}

	stub.awaitCall(t)      // exactly one delivery makes it through...
	stub.assertNoCall(t)   // ...and no second one within the window.
}

// TestFireFailOpenAlert_FiresAgainAfterWindowElapses proves throttling is a
// window, not a permanent silence — a still-ongoing outage keeps notifying
// on a slow cadence rather than going silent forever after the first alert.
func TestFireFailOpenAlert_FiresAgainAfterWindowElapses(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockRepository(ctrl)
	repo.EXPECT().GetEffective(gomock.Any(), gomock.Any(), "transfer_p2p").
		Return(Limit{}, false, errors.New("db unreachable")).Times(2)

	loc, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	stub := newStubAlertFn()
	e := New(repo, cache.NewMemoryCounter(), loc, nil, WithAlertFunc(stub.fn), WithAlertThrottle(50*time.Millisecond))
	t.Cleanup(func() { e.counter.(*cache.MemoryCounter).Stop() })

	_, _, _, err = e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(1000))
	require.NoError(t, err)
	stub.awaitCall(t)

	time.Sleep(80 * time.Millisecond)

	_, _, _, err = e.Check(context.Background(), uuid.New(), "transfer_p2p", decimal.NewFromInt(1000))
	require.NoError(t, err)
	stub.awaitCall(t)
}
