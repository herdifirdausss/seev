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
	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/middleware"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
)

// Re-exported so callers never need to import internal/payin/model.
type TopupIntent = model.TopupIntent

type feeQuoteConsumer interface {
	ConsumeFeeQuote(context.Context, uuid.UUID, uuid.UUID, string, string, decimal.Decimal, string) (decimal.Decimal, string, error)
}

type feeQuoteConsumerWithGateway interface {
	ConsumeFeeQuoteWithGateway(context.Context, uuid.UUID, uuid.UUID, string, string, string, decimal.Decimal, string) (decimal.Decimal, string, error)
}

type feeResolverWithError interface {
	ResolveFee(context.Context, uuid.UUID, string, string, string, decimal.Decimal) (decimal.Decimal, string, bool, error)
}

type feeResolverWithoutError interface {
	ResolveFee(context.Context, uuid.UUID, string, string, string, decimal.Decimal) (decimal.Decimal, string, bool)
}

// feeSnapshotUpdater is implemented by the concrete Payin repository after
// the C5 migration. It is structural so existing generated repository mocks
// and fee-free callers remain source-compatible during rollout.
type feeSnapshotUpdater interface {
	UpdateTopupFeeSnapshot(context.Context, uuid.UUID, uuid.UUID, decimal.Decimal, decimal.Decimal, string, time.Time) error
}

type feeQuoteIntentLookup interface {
	GetTopupIntentByFeeQuoteID(context.Context, uuid.UUID) (model.TopupIntent, bool, error)
}

// CreateTopupIntent starts a user-initiated top-up (docs/roadmap/archive/25 Task T3):
// the returned Reference is what the user quotes at the vendor — the
// vendor never learns the internal user_id, only this opaque reference,
// which travels back in the settling webhook's existing ExternalRef field.
func (m *Module) CreateTopupIntent(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (TopupIntent, error) {
	return m.createTopupIntent(ctx, userID, amount, "", nil)
}

func (m *Module) CreateTopupIntentWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, requestedCurrency string) (TopupIntent, error) {
	return m.createTopupIntent(ctx, userID, amount, requestedCurrency, nil)
}

// CreateTopupIntentWithFeeQuote consumes a Ledger-owned, single-use quote
// before opening provider collection. A non-zero fee must be quoted before
// provider collection so the provider amount and settlement posting use the
// same immutable financial snapshot.
func (m *Module) CreateTopupIntentWithFeeQuote(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, quoteID uuid.UUID) (TopupIntent, error) {
	if quoteID == uuid.Nil {
		return TopupIntent{}, fmt.Errorf("payin: fee quote id is required")
	}
	return m.createTopupIntent(ctx, userID, amount, "", &quoteID)
}

// CreateTopupIntentWithCurrencyAndFeeQuote combines the additive currency
// metadata bridge with the C5 fee-quote flow.
func (m *Module) CreateTopupIntentWithCurrencyAndFeeQuote(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, requestedCurrency string, quoteID uuid.UUID) (TopupIntent, error) {
	if quoteID == uuid.Nil {
		return TopupIntent{}, fmt.Errorf("payin: fee quote id is required")
	}
	return m.createTopupIntent(ctx, userID, amount, requestedCurrency, &quoteID)
}

func (m *Module) createTopupIntent(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, requestedCurrency string, quoteID *uuid.UUID) (TopupIntent, error) {
	if userID == uuid.Nil {
		return TopupIntent{}, fmt.Errorf("%w: user id is required", ErrInvalidAmount)
	}
	if err := currencyreg.ValidatePositiveMinorAmount(amount); err != nil {
		return TopupIntent{}, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if !amount.BigInt().IsInt64() {
		return TopupIntent{}, fmt.Errorf("%w: amount must fit Ledger int64", ErrInvalidAmount)
	}
	if err := m.ensureIntakeOpen(ctx); err != nil {
		return TopupIntent{}, err
	}
	currency := requestedCurrency
	if currency != "" {
		if err := currencyreg.ValidateCode(currency); err != nil {
			return TopupIntent{}, fmt.Errorf("payin: invalid currency: %w", err)
		}
	}
	if currency == "" {
		var err error
		currency, err = m.poster.GetUserCurrency(ctx, userID, "")
		if err != nil {
			return TopupIntent{}, fmt.Errorf("payin: resolve user currency: %w", err)
		}
	}
	if validator, ok := m.poster.(interface {
		ValidateCurrency(context.Context, string, string) error
	}); ok {
		if err := validator.ValidateCurrency(ctx, currency, "topup"); err != nil {
			return TopupIntent{}, fmt.Errorf("payin: currency policy: %w", err)
		}
	}
	if accountReader, ok := m.poster.(interface {
		UserCurrencyEnabled(context.Context, uuid.UUID, string) (bool, error)
	}); ok {
		enabled, err := accountReader.UserCurrencyEnabled(ctx, userID, currency)
		if err != nil {
			return TopupIntent{}, fmt.Errorf("payin: check currency account: %w", err)
		}
		if !enabled {
			return TopupIntent{}, &ledgererr.LedgerError{Code: "CURRENCY_NOT_ENABLED", Message: fmt.Sprintf("currency account is not enabled: %s", currency)}
		}
	}
	vendor, gateway, err := m.ResolveTopupRoute(ctx, userID, currency, amount)
	if err != nil {
		return TopupIntent{}, err
	}
	intentID := generalutil.NewV7()

	ttl := m.topupTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	intent := model.TopupIntent{
		ID:        intentID,
		Reference: "TOP-" + generalutil.NewV7().String(),
		UserID:    userID,
		Amount:    amount,
		FeeAmount: decimal.Zero,
		TotalDebit: amount,
		FeeQuoteID: quoteID,
		FeeApplication: model.TopupFeeApplicationAddedOnTop,
		FeeSnapshotVersion: 1,
		Currency:  currency,
		Vendor:    vendor,
		Gateway:   gateway,
		Status:    model.TopupStatusPending,
		ExpiresAt: time.Now().Add(ttl),
		RequestID: middleware.RequestIDFromCtx(ctx),
	}
	if quoteID != nil {
		if _, ok := m.repo.(feeSnapshotUpdater); ok {
			if err := m.repo.InsertTopupIntent(ctx, intent); err != nil {
				lookup, lookupOK := m.repo.(feeQuoteIntentLookup)
				if !lookupOK {
					return TopupIntent{}, fmt.Errorf("payin: insert topup intent: %w", err)
				}
				existing, found, lookupErr := lookup.GetTopupIntentByFeeQuoteID(ctx, *quoteID)
				if lookupErr != nil {
					return TopupIntent{}, fmt.Errorf("payin: recover topup fee quote intent: %w", lookupErr)
				}
				if !found {
					return TopupIntent{}, fmt.Errorf("payin: insert topup intent: %w", err)
				}
				if existing.UserID != userID || existing.Currency != currency || !existing.Amount.Equal(amount) {
					return TopupIntent{}, fmt.Errorf("payin: fee quote is already bound to a different topup intent")
				}
				intent, err = m.completeQuotedIntent(ctx, existing)
				if err != nil {
					return TopupIntent{}, err
				}
			} else {
				intent, err = m.completeQuotedIntent(ctx, intent)
				if err != nil {
					return TopupIntent{}, err
				}
			}
		} else {
			// Compatibility fallback for a pre-C5 repository. Production uses
			// the provisional row path above; this branch keeps old mocks and
			// fee-free migration harnesses usable until the repository rolls.
			fee, feeGateway, consumedAt, resolveErr := m.resolveTopupFinancials(ctx, userID, "money_in", gateway, currency, amount, quoteID, topupQuoteReference(intent.ID))
			if resolveErr != nil {
				return TopupIntent{}, resolveErr
			}
			intent.FeeAmount, intent.FeeGateway, intent.FeeQuoteConsumedAt = fee, feeGateway, consumedAt
			intent.TotalDebit = amount.Add(fee)
			if !intent.TotalDebit.BigInt().IsInt64() {
				return TopupIntent{}, fmt.Errorf("payin: topup total debit exceeds int64")
			}
			if err := m.repo.InsertTopupIntent(ctx, intent); err != nil {
				return TopupIntent{}, fmt.Errorf("payin: insert topup intent: %w", err)
			}
		}
	} else {
		fee, feeGateway, _, resolveErr := m.resolveTopupFinancials(ctx, userID, "money_in", gateway, currency, amount, nil, "")
		if resolveErr != nil {
			return TopupIntent{}, resolveErr
		}
		if fee.IsPositive() {
			return TopupIntent{}, fmt.Errorf("%w: non-zero top-up fees require a valid fee quote", ErrFeeQuoteRequired)
		}
		intent.FeeAmount, intent.FeeGateway = fee, feeGateway
		intent.TotalDebit = amount.Add(fee)
		if !intent.TotalDebit.BigInt().IsInt64() {
			return TopupIntent{}, fmt.Errorf("payin: topup total debit exceeds int64")
		}
		if err := m.repo.InsertTopupIntent(ctx, intent); err != nil {
			return TopupIntent{}, fmt.Errorf("payin: insert topup intent: %w", err)
		}
	}
	if m.vendorSession != nil {
		if err := m.vendorSession.CreatePayinSession(ctx, vendor, intent.ID.String(), intent.TotalDebit, currency, intent.RequestID); err != nil {
			return TopupIntent{}, fmt.Errorf("payin: create VendorService session: %w", err)
		}
	}
	return intent, nil
}

func topupQuoteReference(intentID uuid.UUID) string {
	return "payin:" + intentID.String()
}

func (m *Module) completeQuotedIntent(ctx context.Context, intent model.TopupIntent) (model.TopupIntent, error) {
	if intent.FeeQuoteID == nil { return model.TopupIntent{}, fmt.Errorf("payin: quoted intent is missing fee_quote_id") }
	if intent.FeeQuoteConsumedAt != nil && !intent.TotalDebit.IsZero() {
		intent.NormalizeFinancials()
		return intent, nil
	}
	updater, ok := m.repo.(feeSnapshotUpdater)
	if !ok { return model.TopupIntent{}, fmt.Errorf("payin: fee snapshot recovery is unavailable") }
	txType := "money_in"
	if intent.MerchantTenantID != uuid.Nil { txType = "merchant_payin_credit" }
	gateway := intent.Gateway
	if gateway == "" && intent.Vendor != "" {
		mapping, found, mappingErr := m.routing.GetVendorGateway(ctx, intent.Vendor)
		if mappingErr != nil {
			return model.TopupIntent{}, fmt.Errorf("payin: recover topup gateway mapping: %w", mappingErr)
		}
		if !found {
			return model.TopupIntent{}, fmt.Errorf("payin: recover topup gateway mapping: vendor %q has no gateway", intent.Vendor)
		}
		gateway = mapping.Gateway
	}
	fee, feeGateway, consumedAt, err := m.resolveTopupFinancials(ctx, intent.UserID, txType, gateway, intent.Currency, intent.Amount, intent.FeeQuoteID, topupQuoteReference(intent.ID))
	if err != nil { return model.TopupIntent{}, err }
	totalDebit := intent.Amount.Add(fee)
	if !totalDebit.BigInt().IsInt64() { return model.TopupIntent{}, fmt.Errorf("payin: topup total debit exceeds int64") }
	if consumedAt == nil { return model.TopupIntent{}, fmt.Errorf("payin: consumed fee quote did not return a consumption timestamp") }
	if err := updater.UpdateTopupFeeSnapshot(ctx, intent.ID, *intent.FeeQuoteID, fee, totalDebit, feeGateway, *consumedAt); err != nil {
		return model.TopupIntent{}, fmt.Errorf("payin: persist topup fee snapshot: %w", err)
	}
	intent.FeeAmount, intent.FeeGateway, intent.FeeQuoteConsumedAt = fee, feeGateway, consumedAt
	intent.TotalDebit, intent.FeeSnapshotVersion = totalDebit, 1
	return intent, nil
}

func (m *Module) resolveTopupFinancials(ctx context.Context, ownerID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, quoteID *uuid.UUID, consumedByRef string) (decimal.Decimal, string, *time.Time, error) {
	if quoteID != nil {
		if consumer, ok := m.poster.(feeQuoteConsumerWithGateway); ok {
			fee, feeGateway, err := consumer.ConsumeFeeQuoteWithGateway(ctx, *quoteID, ownerID, txType, gateway, currency, amount, consumedByRef)
			if err != nil {
				return decimal.Zero, "", nil, fmt.Errorf("payin: consume topup fee quote: %w", err)
			}
			if fee.IsNegative() || !fee.Equal(fee.Truncate(0)) || !fee.BigInt().IsInt64() {
				return decimal.Zero, "", nil, fmt.Errorf("payin: Ledger returned an invalid topup fee")
			}
			now := time.Now().UTC()
			return fee, feeGateway, &now, nil
		}
		consumer, ok := m.poster.(feeQuoteConsumer)
		if !ok {
			return decimal.Zero, "", nil, fmt.Errorf("payin: Ledger fee-quote consumption is unavailable")
		}
		fee, feeGateway, err := consumer.ConsumeFeeQuote(ctx, *quoteID, ownerID, txType, currency, amount, consumedByRef)
		if err != nil {
			return decimal.Zero, "", nil, fmt.Errorf("payin: consume topup fee quote: %w", err)
		}
		if fee.IsNegative() || !fee.Equal(fee.Truncate(0)) || !fee.BigInt().IsInt64() {
			return decimal.Zero, "", nil, fmt.Errorf("payin: Ledger returned an invalid topup fee")
		}
		now := time.Now().UTC()
		return fee, feeGateway, &now, nil
	}
	if resolver, ok := m.poster.(feeResolverWithError); ok {
		fee, feeGateway, matched, err := resolver.ResolveFee(ctx, ownerID, txType, gateway, currency, amount)
		if err != nil {
			return decimal.Zero, "", nil, fmt.Errorf("payin: resolve topup fee: %w", err)
		}
		if !matched {
			return decimal.Zero, "", nil, nil
		}
		if fee.IsNegative() || !fee.Equal(fee.Truncate(0)) || !fee.BigInt().IsInt64() {
			return decimal.Zero, "", nil, fmt.Errorf("payin: Ledger returned an invalid topup fee")
		}
		return fee, feeGateway, nil, nil
	}
	if resolver, ok := m.poster.(feeResolverWithoutError); ok {
		fee, feeGateway, matched := resolver.ResolveFee(ctx, ownerID, txType, gateway, currency, amount)
		if !matched {
			return decimal.Zero, "", nil, nil
		}
		if fee.IsNegative() || !fee.Equal(fee.Truncate(0)) || !fee.BigInt().IsInt64() {
			return decimal.Zero, "", nil, fmt.Errorf("payin: Ledger returned an invalid topup fee")
		}
		return fee, feeGateway, nil, nil
	}
	return decimal.Zero, "", nil, nil
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
	intent.NormalizeFinancials()
	if intent.Status == model.TopupStatusPending && intent.FeeQuoteID != nil && intent.FeeQuoteConsumedAt == nil {
		intent, err = m.completeQuotedIntent(ctx, intent)
		if err != nil { return TopupIntent{}, err }
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
