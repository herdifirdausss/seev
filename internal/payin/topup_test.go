package payin

import (
	"context"
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
)

func TestCreateTopupIntent_NoRoute(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	m := &Module{repo: repo, poster: stubPoster{}, registry: vendorgw.NewRegistry(), routing: stubRouting{}}
	_, err := m.CreateTopupIntent(context.Background(), uuid.New(), decimal.NewFromInt(50_000))
	assert.ErrorIs(t, err, ErrNoRoute)
}

func TestCreateTopupIntent_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	var inserted model.TopupIntent
	repo.EXPECT().InsertTopupIntent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, intent model.TopupIntent) error { inserted = intent; return nil })
	m := &Module{repo: repo, poster: stubPoster{}, registry: registryWith(stubVerifier{name: "acme"}), routing: routeTo("acme", "bca"), topupTTL: time.Hour}
	userID := uuid.New()
	intent, err := m.CreateTopupIntent(context.Background(), userID, decimal.NewFromInt(500_000))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(intent.Reference, "TOP-"))
	assert.Equal(t, userID, intent.UserID)
	assert.Equal(t, "IDR", intent.Currency)
	assert.Equal(t, model.TopupStatusPending, intent.Status)
	assert.True(t, intent.Amount.Equal(decimal.NewFromInt(500_000)))
	assert.WithinDuration(t, time.Now().Add(time.Hour), intent.ExpiresAt, 5*time.Second)
	assert.Equal(t, inserted.Reference, intent.Reference)
}

func TestCreateTopupIntent_DefaultTTL_WhenUnset(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().InsertTopupIntent(gomock.Any(), gomock.Any()).Return(nil)
	m := &Module{repo: repo, poster: stubPoster{}, registry: registryWith(stubVerifier{name: "acme"}), routing: routeTo("acme", "bca")}
	intent, err := m.CreateTopupIntent(context.Background(), uuid.New(), decimal.NewFromInt(1000))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), intent.ExpiresAt, 5*time.Second)
}

func TestGetTopupIntent_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().GetTopupIntent(gomock.Any(), id).Return(model.TopupIntent{}, repository.ErrNotFound)
	m := &Module{repo: repo, logger: discardLogger()}
	_, err := m.GetTopupIntent(context.Background(), id)
	assert.ErrorIs(t, err, ErrTopupIntentNotFound)
}

func TestGetTopupIntent_ExpiresPendingIntent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	id := uuid.New()
	repo.EXPECT().GetTopupIntent(gomock.Any(), id).Return(model.TopupIntent{ID: id, Status: model.TopupStatusPending, ExpiresAt: time.Now().Add(-time.Hour)}, nil)
	repo.EXPECT().MarkTopupIntentExpired(gomock.Any(), id).Return(nil)
	m := &Module{repo: repo, logger: discardLogger()}
	intent, err := m.GetTopupIntent(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.TopupStatusExpired, intent.Status)
}
