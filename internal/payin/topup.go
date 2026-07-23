package payin

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// Re-exported so callers never need to import internal/payin/model.
type TopupIntent = model.TopupIntent

// CreateTopupIntent starts a user-initiated top-up (docs/roadmap/archive/25 Task T3):
// the returned Reference is what the user quotes at the vendor — the
// vendor never learns the internal user_id, only this opaque reference,
// which travels back in the settling webhook's existing ExternalRef field.
func (m *Module) CreateTopupIntent(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (TopupIntent, error) {
	if err := m.ensureIntakeOpen(ctx); err != nil {
		return TopupIntent{}, err
	}
	currency, err := m.poster.GetUserCurrency(ctx, userID, "")
	if err != nil {
		return TopupIntent{}, fmt.Errorf("payin: resolve user currency: %w", err)
	}
	vendor, _, err := m.ResolveTopupRoute(ctx, userID, currency, amount)
	if err != nil {
		return TopupIntent{}, err
	}

	ttl := m.topupTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	intent := model.TopupIntent{
		ID:        generalutil.NewV7(),
		Reference: "TOP-" + generalutil.NewV7().String(),
		UserID:    userID,
		Amount:    amount,
		Currency:  currency,
		Vendor:    vendor,
		Status:    model.TopupStatusPending,
		ExpiresAt: time.Now().Add(ttl),
		RequestID: middleware.RequestIDFromCtx(ctx),
	}
	if err := m.repo.InsertTopupIntent(ctx, intent); err != nil {
		return TopupIntent{}, fmt.Errorf("payin: insert topup intent: %w", err)
	}
	return intent, nil
}

// GetTopupIntent returns one topup intent by id, lazily flipping a stale
// 'pending' row to 'expired' first (docs/roadmap/archive/25 Task T3 step 5 — no
// background job, expiry is discovered opportunistically on read).
func (m *Module) GetTopupIntent(ctx context.Context, id uuid.UUID) (TopupIntent, error) {
	intent, err := m.repo.GetTopupIntent(ctx, id)
	if err != nil {
		if err == repository.ErrNotFound {
			return TopupIntent{}, ErrTopupIntentNotFound
		}
		return TopupIntent{}, err
	}
	if intent.Status == model.TopupStatusPending && !intent.ExpiresAt.After(time.Now()) {
		if markErr := m.repo.MarkTopupIntentExpired(ctx, intent.ID); markErr != nil {
			m.logger.Error("payin: mark topup intent expired failed",
				slog.Any("error", markErr), slog.String("intent_id", intent.ID.String()))
		} else {
			intent.Status = model.TopupStatusExpired
		}
	}
	return intent, nil
}
