package payin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
)

// ─── CreateTopupIntent ───────────────────────────────────────────────────

func TestCreateTopupIntent_NoRoute(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected

	m := &Module{repo: repo, poster: stubPoster{}, registry: vendorgw.NewRegistry(), routing: stubRouting{}}
	_, err := m.CreateTopupIntent(context.Background(), uuid.New(), decimal.NewFromInt(50_000))
	assert.ErrorIs(t, err, ErrNoRoute)
}

func TestCreateTopupIntent_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	var inserted model.TopupIntent
	repo.EXPECT().InsertTopupIntent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, intent model.TopupIntent) error {
			inserted = intent
			return nil
		})

	verifier := stubVerifier{name: "acme"}
	userID := uuid.New()
	m := &Module{repo: repo, poster: stubPoster{}, registry: registryWith(verifier), routing: routeTo("acme", "bca"), topupTTL: time.Hour}

	intent, err := m.CreateTopupIntent(context.Background(), userID, decimal.NewFromInt(500_000))
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(intent.Reference, "TOP-"), "reference must have the TOP- prefix")
	assert.Equal(t, userID, intent.UserID)
	assert.Equal(t, "IDR", intent.Currency)
	assert.Equal(t, model.TopupStatusPending, intent.Status)
	assert.True(t, intent.Amount.Equal(decimal.NewFromInt(500_000)))
	assert.WithinDuration(t, time.Now().Add(time.Hour), intent.ExpiresAt, 5*time.Second)
	assert.Equal(t, inserted.Reference, intent.Reference, "returned intent must match what was persisted")
}

func TestCreateTopupIntent_DefaultTTL_WhenUnset(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().InsertTopupIntent(gomock.Any(), gomock.Any()).Return(nil)

	verifier := stubVerifier{name: "acme"}
	m := &Module{repo: repo, poster: stubPoster{}, registry: registryWith(verifier), routing: routeTo("acme", "bca")} // topupTTL zero value

	intent, err := m.CreateTopupIntent(context.Background(), uuid.New(), decimal.NewFromInt(1000))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), intent.ExpiresAt, 5*time.Second,
		"topupTTL <= 0 must default to 24h")
}

// ─── GetTopupIntent (ownership check lives at the HTTP layer; this tests
// the lazy-expiry flip the HTTP layer's GetHandler relies on) ───────────

func TestGetTopupIntent_NotFound_ErrTopupIntentNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().GetTopupIntent(gomock.Any(), id).Return(model.TopupIntent{}, repository.ErrNotFound)

	m := &Module{repo: repo, logger: discardLogger()}
	_, err := m.GetTopupIntent(context.Background(), id)
	assert.ErrorIs(t, err, ErrTopupIntentNotFound)
}

func TestGetTopupIntent_StalePending_LazilyFlipsToExpired(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().GetTopupIntent(gomock.Any(), id).Return(model.TopupIntent{
		ID: id, Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(-time.Hour),
	}, nil)
	repo.EXPECT().MarkTopupIntentExpired(gomock.Any(), id).Return(nil)

	m := &Module{repo: repo, logger: discardLogger()}
	intent, err := m.GetTopupIntent(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.TopupStatusExpired, intent.Status, "the RETURNED struct must reflect the flip immediately")
}

func TestGetTopupIntent_StillPending_NoFlip(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().GetTopupIntent(gomock.Any(), id).Return(model.TopupIntent{
		ID: id, Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	// MarkTopupIntentExpired must NOT be called.

	m := &Module{repo: repo, logger: discardLogger()}
	intent, err := m.GetTopupIntent(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.TopupStatusPending, intent.Status)
}

func TestGetTopupIntent_AlreadySettled_NoFlip(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().GetTopupIntent(gomock.Any(), id).Return(model.TopupIntent{
		ID: id, Status: model.TopupStatusSettled, ExpiresAt: time.Now().Add(-time.Hour),
	}, nil)
	// MarkTopupIntentExpired must NOT be called — only 'pending' rows expire.

	m := &Module{repo: repo, logger: discardLogger()}
	intent, err := m.GetTopupIntent(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.TopupStatusSettled, intent.Status)
}

// ─── HandleWebhook <-> topup intent resolution ──────────────────────────

func topupEvent(externalRef string, amount int64) *vendorgw.PayinEvent {
	return &vendorgw.PayinEvent{
		Vendor: "acme", VendorEventID: "evt-topup-1", ExternalRef: externalRef,
		UserID: uuid.New(), Amount: decimal.NewFromInt(amount), Currency: "IDR", OccurredAt: time.Now(),
	}
}

// TestHandleWebhook_ResolvesTopupIntentUser proves the core property
// (docs/plan/25 Task T3): the vendor never learns the internal user_id —
// the webhook payload's OWN user_id (payload.UserID, deliberately
// different from the intent's) must be IGNORED in favor of the intent's
// user_id when a matching pending intent is found by reference.
func TestHandleWebhook_ResolvesTopupIntentUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	ev := topupEvent("TOP-abc", 50_000)
	intentUserID := uuid.New()
	require.NotEqual(t, intentUserID, ev.UserID, "test setup: intent user must differ from payload user")

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "TOP-abc").Return(model.TopupIntent{
		Reference: "TOP-abc", UserID: intentUserID, Amount: ev.Amount, Currency: ev.Currency,
		Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(time.Hour),
	}, true, nil)

	var gotCmd ledgerclient.Command
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			assert.Equal(t, intentUserID, e.UserID, "the PERSISTED event row must carry the intent's user_id, not the payload's")
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "TOP-abc", gomock.Any()).Return(true, nil)

	poster := stubPoster{fn: func(_ context.Context, cmd ledgerclient.Command) error {
		gotCmd = cmd
		return nil
	}}
	m := &Module{repo: repo, poster: poster, registry: registryWith(stubVerifier{name: "acme", event: ev}),
		routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, intentUserID, gotCmd.UserID, "money_in must credit the INTENT's user, not the payload's")
}

// TestHandleWebhook_NoMatchingIntent_FallsBackToPayloadUserID proves
// backward compatibility: an ExternalRef that doesn't match any topup
// intent (e.g. a vendor's own transaction id, pre-existing payin flows)
// falls back to the payload's own user_id exactly as before this task.
func TestHandleWebhook_NoMatchingIntent_FallsBackToPayloadUserID(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	ev := topupEvent("vendor-native-ref", 50_000)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "vendor-native-ref").Return(model.TopupIntent{}, false, nil)

	var gotCmd ledgerclient.Command
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			assert.Equal(t, ev.UserID, e.UserID)
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkPosted(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkTopupIntentSettled(gomock.Any(), "vendor-native-ref", gomock.Any()).Return(false, nil)

	poster := stubPoster{fn: func(_ context.Context, cmd ledgerclient.Command) error {
		gotCmd = cmd
		return nil
	}}
	m := &Module{repo: repo, poster: poster, registry: registryWith(stubVerifier{name: "acme", event: ev}),
		routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, ev.UserID, gotCmd.UserID)
}

// TestHandleWebhook_TopupIntentAmountMismatch_BusinessFailure_NeverPosts
// proves a webhook claiming a different amount than the intent it
// references is rejected outright — never posted, marked 'failed' for
// admin visibility, classified as a non-retryable business failure so the
// vendor stops redelivering something that can never succeed.
func TestHandleWebhook_TopupIntentAmountMismatch_BusinessFailure_NeverPosts(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	ev := topupEvent("TOP-mismatch", 999_000) // intent expects 50_000

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "TOP-mismatch").Return(model.TopupIntent{
		Reference: "TOP-mismatch", UserID: uuid.New(), Amount: decimal.NewFromInt(50_000), Currency: "IDR",
		Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(time.Hour),
	}, true, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	// MarkTopupIntentSettled must NOT be called — the mismatch means money
	// never posts, so nothing should be marked settled.

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}
	m := &Module{repo: repo, poster: poster, registry: registryWith(stubVerifier{name: "acme", event: ev}),
		routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.True(t, IsBusinessFailure(err), "an amount mismatch must be classified as a non-retryable business failure")
	assert.ErrorIs(t, err, ErrTopupIntentMismatch)
	assert.Equal(t, 0, postCalls, "a mismatched intent must never reach the ledger")
}

// TestHandleWebhook_TopupIntentAlreadySettled_BusinessFailure_PreventsDoubleCredit
// proves the reuse-prevention guard: a reference that already resolved to
// a settled intent must NOT be reusable to credit a SECOND money_in under
// a different vendor_event_id (the ledger's own idempotency key is scoped
// to vendor_event_id, not to the topup reference, so this guard is the
// only thing that would otherwise allow a double-credit here).
func TestHandleWebhook_TopupIntentAlreadySettled_BusinessFailure_PreventsDoubleCredit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	ev := topupEvent("TOP-reused", 50_000)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "TOP-reused").Return(model.TopupIntent{
		Reference: "TOP-reused", UserID: uuid.New(), Amount: decimal.NewFromInt(50_000), Currency: "IDR",
		Status: model.TopupStatusSettled, ExpiresAt: time.Now().Add(time.Hour),
	}, true, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}
	m := &Module{repo: repo, poster: poster, registry: registryWith(stubVerifier{name: "acme", event: ev}),
		routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.True(t, IsBusinessFailure(err))
	assert.Equal(t, 0, postCalls, "an already-settled intent must never be reused to post a second money_in")
}

// TestHandleWebhook_TopupIntentExpired_BusinessFailure proves a settling
// webhook that arrives after the intent's expiry window is rejected as a
// business failure, not silently accepted.
func TestHandleWebhook_TopupIntentExpired_BusinessFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	ev := topupEvent("TOP-expired", 50_000)

	repo.EXPECT().GetTopupIntentByReference(gomock.Any(), "TOP-expired").Return(model.TopupIntent{
		Reference: "TOP-expired", UserID: uuid.New(), Amount: decimal.NewFromInt(50_000), Currency: "IDR",
		Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(-time.Hour), // already past expiry
	}, true, nil)
	repo.EXPECT().GetOrInsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e model.WebhookEvent) (model.WebhookEvent, error) {
			e.Status = "received"
			return e, nil
		})
	repo.EXPECT().MarkFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	postCalls := 0
	poster := stubPoster{fn: func(_ context.Context, _ ledgerclient.Command) error {
		postCalls++
		return nil
	}}
	m := &Module{repo: repo, poster: poster, registry: registryWith(stubVerifier{name: "acme", event: ev}),
		routing: routeTo("acme", "bca"), logger: discardLogger()}

	err := m.HandleWebhook(context.Background(), "acme", http.Header{}, []byte(`{}`))
	require.Error(t, err)
	assert.True(t, IsBusinessFailure(err))
	assert.ErrorIs(t, err, ErrTopupIntentExpired)
	assert.Equal(t, 0, postCalls)
}
