package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

// fakePublisher is a minimal test double for messaging.Publisher — no
// generated mock exists for it in pkg/messaging, and the interface is small
// enough that a hand-written fake is clearer than adding one.
type fakePublisher struct {
	mu             sync.Mutex
	published      []messaging.PublishOptions
	correlationIDs []string // messaging.CorrelationIDFromContext(ctx) at call time, docs/plan/36 Task T4
	failNext       int      // number of upcoming calls to fail
	err            error
}

func (f *fakePublisher) Publish(ctx context.Context, routingKey string, payload any) error {
	return f.PublishTo(ctx, messaging.PublishOptions{RoutingKey: routingKey}, payload)
}

func (f *fakePublisher) PublishTo(ctx context.Context, opts messaging.PublishOptions, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		if f.err == nil {
			f.err = errors.New("simulated publish failure")
		}
		return f.err
	}
	f.published = append(f.published, opts)
	f.correlationIDs = append(f.correlationIDs, messaging.CorrelationIDFromContext(ctx))
	return nil
}

func (f *fakePublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func newMockOutboxRepo(t *testing.T) (*repository.MockOutboxRepository, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	return repository.NewMockOutboxRepository(ctrl), ctrl
}

// waitUntil polls cond until it's true or the timeout elapses, failing the
// test on timeout. Needed because the relay's loops run on their own tickers.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestOutboxRelay_PublishesClaimedEvents(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{}

	event := model.OutboxEventRecord{ID: uuid.New(), EventType: "ledger.transaction.posted", Payload: map[string]any{"x": 1}}

	gomock.InOrder(
		repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return([]model.OutboxEventRecord{event}, nil),
		repo.EXPECT().MarkPublished(gomock.Any(), event.ID).Return(nil),
	)
	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{PollInterval: 5 * time.Millisecond})
	relay.Start(context.Background())
	defer relay.Stop()

	waitUntil(t, time.Second, func() bool { return pub.count() == 1 })
	assert.Equal(t, "ledger.transaction.posted", pub.published[0].RoutingKey)
	assert.Equal(t, event.ID.String(), pub.published[0].MessageID)
}

// TestOutboxRelay_RestoresRequestIDFromPayload proves docs/plan/36 Task T4:
// the relay's own ctx (its background loop context) never carries the
// originating request_id, so publishOne must restore it from the payload
// field the posting transaction persisted, and hand it to the publisher as
// the AMQP CorrelationId.
func TestOutboxRelay_RestoresRequestIDFromPayload(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{}

	event := model.OutboxEventRecord{
		ID: uuid.New(), EventType: "ledger.transaction.posted",
		Payload: map[string]any{"request_id": "trace-from-posting-tx"},
	}

	gomock.InOrder(
		repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return([]model.OutboxEventRecord{event}, nil),
		repo.EXPECT().MarkPublished(gomock.Any(), event.ID).Return(nil),
	)
	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{PollInterval: 5 * time.Millisecond})
	relay.Start(context.Background())
	defer relay.Stop()

	waitUntil(t, time.Second, func() bool { return pub.count() == 1 })
	assert.Equal(t, "trace-from-posting-tx", pub.correlationIDs[0])
}

func TestOutboxRelay_PublishFailure_MarksFailedNotPublished(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{failNext: 1}

	event := model.OutboxEventRecord{ID: uuid.New(), EventType: "ledger.transaction.posted", Payload: map[string]any{}}

	marked := make(chan struct{}, 1)
	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return([]model.OutboxEventRecord{event}, nil).Times(1)
	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().MarkFailed(gomock.Any(), event.ID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uuid.UUID, _ string) error {
			marked <- struct{}{}
			return nil
		})
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()
	// MarkPublished must never be called for a failed publish.

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{PollInterval: 5 * time.Millisecond})
	relay.Start(context.Background())
	defer relay.Stop()

	select {
	case <-marked:
	case <-time.After(time.Second):
		t.Fatal("MarkFailed was not called in time")
	}
}

func TestOutboxRelay_RetryLoop_ClaimsFailedEvents(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{}

	event := model.OutboxEventRecord{ID: uuid.New(), EventType: "ledger.transaction.posted", RetryCount: 1}

	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return([]model.OutboxEventRecord{event}, nil).Times(1)
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().MarkPublished(gomock.Any(), event.ID).Return(nil)
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{PollInterval: time.Hour, RetryInterval: 5 * time.Millisecond})
	relay.Start(context.Background())
	defer relay.Stop()

	waitUntil(t, time.Second, func() bool { return pub.count() == 1 })
}

func TestOutboxRelay_ReaperResetsStuckEvents(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{}

	reaped := make(chan struct{}, 1)
	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, time.Duration) (int, error) {
			select {
			case reaped <- struct{}{}:
			default:
			}
			return 2, nil
		}).AnyTimes()

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{
		PollInterval: time.Hour, RetryInterval: time.Hour, ReaperInterval: 5 * time.Millisecond,
	})
	relay.Start(context.Background())
	defer relay.Stop()

	select {
	case <-reaped:
	case <-time.After(time.Second):
		t.Fatal("ReapStuck was not called in time")
	}
}

func TestOutboxRelay_ClaimError_DoesNotPanic(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{}

	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down")).AnyTimes()
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{PollInterval: 5 * time.Millisecond})
	relay.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
	relay.Stop() // must return without hanging or panicking
}

func TestOutboxRelay_Stop_WaitsForLoopsToExit(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()
	pub := &fakePublisher{}

	repo.EXPECT().ClaimPending(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ClaimFailedForRetry(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().ReapStuck(gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountByStatuses(gomock.Any(), gomock.Any()).Return(map[string]int{}, nil).AnyTimes()

	relay := NewOutboxRelay(repo, pub, nil, OutboxRelayConfig{PollInterval: time.Millisecond})
	relay.Start(context.Background())

	done := make(chan struct{})
	go func() {
		relay.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return in time")
	}
}

func TestOutboxRelayConfig_Defaults(t *testing.T) {
	cfg := OutboxRelayConfig{}
	cfg.applyDefaults()
	require.Equal(t, defaultPollInterval, cfg.PollInterval)
	require.Equal(t, defaultRetryInterval, cfg.RetryInterval)
	require.Equal(t, defaultReaperInterval, cfg.ReaperInterval)
	require.Equal(t, defaultStuckAfter, cfg.StuckAfter)
	require.Equal(t, defaultBatchSize, cfg.BatchSize)
}

// ─── refreshGauges (docs/plan/11 Task T6) ──────────────────────────────────────

func TestRefreshGauges_SingleQuery_SetsBothGauges(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()

	// Exactly ONE CountByStatuses call proves both gauges come from a
	// single round trip, not two sequential CountByStatus calls.
	repo.EXPECT().
		CountByStatuses(gomock.Any(), []string{"pending", "dead"}).
		Return(map[string]int{"pending": 7, "dead": 2}, nil).
		Times(1)

	relay := NewOutboxRelay(repo, &fakePublisher{}, nil, OutboxRelayConfig{})
	relay.refreshGauges(context.Background())

	assert.Equal(t, float64(7), testutil.ToFloat64(outboxPendingGauge))
	assert.Equal(t, float64(2), testutil.ToFloat64(outboxDeadGauge))
}

func TestRefreshGauges_MissingStatusInResult_TreatedAsZero(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()

	// No "dead" key at all in the returned map (e.g. zero dead events —
	// GROUP BY simply omits statuses with no matching rows).
	repo.EXPECT().
		CountByStatuses(gomock.Any(), []string{"pending", "dead"}).
		Return(map[string]int{"pending": 3}, nil).
		Times(1)

	relay := NewOutboxRelay(repo, &fakePublisher{}, nil, OutboxRelayConfig{})
	relay.refreshGauges(context.Background())

	assert.Equal(t, float64(3), testutil.ToFloat64(outboxPendingGauge))
	assert.Equal(t, float64(0), testutil.ToFloat64(outboxDeadGauge))
}

func TestRefreshGauges_QueryError_GaugesUnchanged(t *testing.T) {
	repo, ctrl := newMockOutboxRepo(t)
	defer ctrl.Finish()

	repo.EXPECT().
		CountByStatuses(gomock.Any(), []string{"pending", "dead"}).
		Return(nil, errors.New("db down")).
		Times(1)

	relay := NewOutboxRelay(repo, &fakePublisher{}, nil, OutboxRelayConfig{})

	// Establish a known baseline, then prove a failed refresh doesn't
	// stomp it with zeros — better to show stale data than wrong data.
	outboxPendingGauge.Set(42)
	relay.refreshGauges(context.Background())

	assert.Equal(t, float64(42), testutil.ToFloat64(outboxPendingGauge))
}
