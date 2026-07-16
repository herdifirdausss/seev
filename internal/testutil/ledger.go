package testutil

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

type (
	LedgerAccount = ledger.Account
)

// LedgerHarness adapts the in-process ledger facade to the extracted client
// contract for integration tests that still share one test database.
type LedgerHarness struct {
	module *ledger.Module
}

func NewLedgerHarness(db database.DatabaseSQL) *LedgerHarness {
	return &LedgerHarness{module: ledger.NewModule(
		db, nil, nil, ledger.WorkerConfig{}, nil, decimal.Zero, nil, nil, 0,
	)}
}

func (h *LedgerHarness) Post(ctx context.Context, command ledgerclient.Command) error {
	err := h.module.Post(ctx, ledger.Command{
		IdempotencyKey: command.IdempotencyKey, IdempotencyScope: command.IdempotencyScope,
		Type: command.Type, Amount: command.Amount, UserID: command.UserID,
		TargetUserID: command.TargetUserID, PocketCode: command.PocketCode,
		ReferenceID: command.ReferenceID, Metadata: command.Metadata,
	})
	return translateLedgerErr(err)
}

// translateLedgerErr converts this in-process harness's raw ledger errors
// (ledger.ErrAlreadyClosed / ledger.LedgerError — the module's own public
// re-exports of its internal apperror sentinels) into the same pkg/ledgererr
// sentinels a real gRPC-connected ledgerclient.Client would produce (via
// ledgererr.FromStatus decoding the wire status) — so callers like
// internal/payout's K3-race reconciliation (errors.Is against
// ledgererr.ErrAlreadyClosed) behave identically whether they're wired to
// the real network client or this test harness.
func translateLedgerErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ledger.ErrAlreadyClosed) {
		return ledgererr.ErrAlreadyClosed
	}
	var bizErr *ledger.LedgerError
	if errors.As(err, &bizErr) {
		return &ledgererr.LedgerError{Code: bizErr.Code, Message: bizErr.Message, Retryable: bizErr.Retryable}
	}
	return err
}

func (h *LedgerHarness) GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (ledgerclient.Transaction, error) {
	tx, err := h.module.GetTransactionByIdempotencyKey(ctx, key, scope)
	if err != nil {
		return ledgerclient.Transaction{}, err
	}
	return ledgerclient.Transaction{
		ID: tx.ID, IdempotencyKey: tx.IdempotencyKey, IdempotencyScope: tx.IdempotencyScope,
		Type: tx.Type, Status: tx.Status, Amount: tx.Amount, Currency: tx.Currency,
		SourceAccountID: tx.SourceAccountID, DestinationAccountID: tx.DestinationAccountID,
		ErrorMessage: tx.ErrorMessage, ExternalRef: tx.ExternalRef, Gateway: tx.Gateway,
		CreatedAt: tx.CreatedAt, UpdatedAt: tx.UpdatedAt,
	}, nil
}

func (h *LedgerHarness) GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error) {
	return h.module.GetUserCurrency(ctx, userID, pocketCode)
}

func (h *LedgerHarness) ResolveFee(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (decimal.Decimal, string, bool, error) {
	fee, feeGateway, ok := h.module.ResolveFee(ctx, userID, txType, gateway, currency, amount)
	return fee, feeGateway, ok, nil
}

// CreateQuote delegates to the in-process ledger module — lets
// integration tests outside internal/ledger (e.g. internal/payout's own,
// docs/plan/38 Task T5) create a real fee quote to consume without
// importing the module-private internal/ledger/feepolicy package.
func (h *LedgerHarness) CreateQuote(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, ttl time.Duration) (ledger.Quote, error) {
	return h.module.CreateQuote(ctx, userID, txType, gateway, currency, amount, ttl)
}

// ConsumeFeeQuote delegates to the in-process ledger module — its feePolicy
// is wired to the SAME test database (docs/plan/38 Task T5), so quote
// creation and consumption observe the same rows. Its error already comes
// back as *ledger.LedgerError (ledger.Module.ConsumeFeeQuote translates the
// raw feepolicy sentinels itself, precisely so this harness can reuse the
// SAME translateLedgerErr used by Post above instead of needing its own
// classification logic here.
func (h *LedgerHarness) ConsumeFeeQuote(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (decimal.Decimal, string, error) {
	fee, feeGateway, err := h.module.ConsumeFeeQuote(ctx, quoteID, userID, txType, currency, amount, ref)
	return fee, feeGateway, translateLedgerErr(err)
}

// ApplyKycTier delegates to the in-process ledger module (docs/plan/39 Task
// T5) — lets integration tests outside internal/ledger (e.g.
// internal/auth's own) exercise the real ApplyKycTier wiring through the
// same Provisioner-shaped surface a real ledgerclient.Client offers.
func (h *LedgerHarness) ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int) error {
	return h.module.ApplyKycTier(ctx, userID, int32(kycLevel))
}

func (h *LedgerHarness) ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) error {
	_, err := h.module.ProvisionUser(ctx, userID, currency)
	return err
}

func (h *LedgerHarness) ListAccounts(ctx context.Context, userID uuid.UUID) ([]LedgerAccount, error) {
	return h.module.ListAccounts(ctx, userID)
}
