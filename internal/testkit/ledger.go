package testutil

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/contracts/clients/ledger"
	"github.com/herdifirdausss/seev/contracts/clients/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger"
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
		db, nil, nil, ledger.WorkerConfig{}, nil, decimal.Zero, nil, nil, 0, testDigestRing(), testCryptoxRing(),
	)}
}

// testDigestRing is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's (K7) fixed test key
// for every integration test that reaches ledger through this shared
// harness — ledger.NewModule requires a real, non-nil ring (money-safety
// deduplication, unlike T2's optional field-encryption rings), so this
// package is the single place that constructs one, keeping every OTHER
// service's own cross-service integration test (payin, payout, auth,
// notify) unchanged.
func testDigestRing() *cryptox.DigestRing {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 17)
	}
	ring, err := cryptox.NewDigestRing(map[int][]byte{1: key}, 1)
	if err != nil {
		panic(err)
	}
	return ring
}

// testCryptoxRing keeps the shared ledger harness usable after A8's
// encryption contract removed the reconciliation plaintext columns.
func testCryptoxRing() *cryptox.Ring {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 43)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	if err != nil {
		panic(err)
	}
	return ring
}

func (h *LedgerHarness) Post(ctx context.Context, command ledgerclient.Command) error {
	err := h.module.Post(ctx, ledger.Command{
		IdempotencyKey: command.IdempotencyKey, IdempotencyScope: command.IdempotencyScope,
		Type: command.Type, Amount: command.Amount, UserID: command.UserID,
		TargetUserID: command.TargetUserID, PocketCode: command.PocketCode, Currency: command.Currency,
		ReferenceID: command.ReferenceID, Metadata: command.Metadata,
		MerchantTenantID: command.MerchantTenantID,
	})
	return translateLedgerErr(err)
}

// translateLedgerErr converts this in-process harness's raw ledger errors
// (ledger.ErrAlreadyClosed / ledger.LedgerError — the module's own public
// re-exports of its internal apperror sentinels) into the same contracts/clients/ledger/errors
// sentinels a real gRPC-connected ledgerclient.Client would produce (via
// ledgererr.FromStatus decoding the wire status) — so callers like
// services/payout's K3-race reconciliation (errors.Is against
// ledgererr.ErrAlreadyClosed) behave identically whether they're wired to
// the real network client or this test harness. NotFound cases (Plan 57
// T10 follow-up) become a genuine grpc status error rather than the raw
// apperror sentinel, matching what status.FromError needs to see for
// services/gateway/internal/merchant/client's own translateError to classify them as
// ErrNotFound instead of falling through to ErrOwnerUnavailable.
func translateLedgerErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ledger.ErrAlreadyClosed) {
		return ledgererr.ErrAlreadyClosed
	}
	if errors.Is(err, ledger.ErrTransactionNotFound) || errors.Is(err, ledger.ErrAccountNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	var bizErr *ledger.LedgerError
	if errors.As(err, &bizErr) {
		return &ledgererr.LedgerError{Code: bizErr.Code, Message: bizErr.Message, Retryable: bizErr.Retryable}
	}
	return err
}

// Module exposes the underlying in-process *ledger.Module — for tests that
// need surface this thin client-shaped harness doesn't re-expose (e.g.
// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5's closure saga tests, which wrap
// Module().ClosureRouter() in an httptest.Server to exercise auth's real
// HTTP closure client against a real handler instead of an in-process
// bypass).
func (h *LedgerHarness) Module() *ledger.Module { return h.module }

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
// integration tests outside services/ledger (e.g. services/payout's own,
// docs/roadmap/archive/38 Task T5) create a real fee quote to consume without
// importing the module-private services/ledger/internal/feepolicy package.
func (h *LedgerHarness) CreateQuote(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, ttl time.Duration) (ledger.Quote, error) {
	return h.module.CreateQuote(ctx, userID, txType, gateway, currency, amount, ttl)
}

// ConsumeFeeQuote delegates to the in-process ledger module — its feePolicy
// is wired to the SAME test database (docs/roadmap/archive/38 Task T5), so quote
// creation and consumption observe the same rows. Its error already comes
// back as *ledger.LedgerError (ledger.Module.ConsumeFeeQuote translates the
// raw feepolicy sentinels itself, precisely so this harness can reuse the
// SAME translateLedgerErr used by Post above instead of needing its own
// classification logic here.
func (h *LedgerHarness) ConsumeFeeQuote(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (decimal.Decimal, string, error) {
	fee, feeGateway, err := h.module.ConsumeFeeQuote(ctx, quoteID, userID, txType, currency, amount, ref)
	return fee, feeGateway, translateLedgerErr(err)
}

// ApplyKycTier delegates to the in-process ledger module (docs/roadmap/archive/39 Task
// T5) — lets integration tests outside services/ledger (e.g.
// services/auth's own) exercise the real ApplyKycTier wiring through the
// same Provisioner-shaped surface a real ledgerclient.Client offers.
func (h *LedgerHarness) ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int) error {
	return h.module.ApplyKycTier(ctx, userID, int32(kycLevel))
}

func (h *LedgerHarness) SetExecutionSubjectState(ctx context.Context, userID uuid.UUID, status string, kycLevel int, verifiedUntil *time.Time) error {
	return h.module.SetExecutionSubjectState(ctx, userID, status, kycLevel, verifiedUntil)
}

func (h *LedgerHarness) ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) error {
	_, err := h.module.ProvisionUser(ctx, userID, currency)
	return err
}

func (h *LedgerHarness) ListAccounts(ctx context.Context, userID uuid.UUID) ([]LedgerAccount, error) {
	return h.module.ListAccounts(ctx, userID)
}

// GetMerchantAccount delegates to the in-process ledger module (Plan 57
// T5/T10 follow-up) — lets services/gateway/internal/merchant/client's own integration
// tests exercise the real merchant account read against a shared test
// database instead of a fake/mocked LedgerService.
func (h *LedgerHarness) GetMerchantAccount(ctx context.Context, tenantID uuid.UUID) (ledgerclient.MerchantAccount, error) {
	bal, err := h.module.GetMerchantAccount(ctx, tenantID)
	if err != nil {
		return ledgerclient.MerchantAccount{}, translateLedgerErr(err)
	}
	return ledgerclient.MerchantAccount{AccountID: bal.AccountID, Currency: bal.Currency, Balance: bal.Balance, Status: bal.Status}, nil
}

// ListMerchantTransactions delegates to the in-process ledger module
// (Plan 57 T5/T10 follow-up).
func (h *LedgerHarness) ListMerchantTransactions(ctx context.Context, tenantID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]ledgerclient.Transaction, error) {
	txs, err := h.module.ListMerchantTransactions(ctx, tenantID, beforeCreatedAt, beforeID, limit)
	if err != nil {
		return nil, translateLedgerErr(err)
	}
	out := make([]ledgerclient.Transaction, 0, len(txs))
	for _, tx := range txs {
		out = append(out, ledgerclient.Transaction{
			ID: tx.ID, IdempotencyKey: tx.IdempotencyKey, IdempotencyScope: tx.IdempotencyScope,
			Type: tx.Type, Status: tx.Status, Amount: tx.Amount, Currency: tx.Currency,
			SourceAccountID: tx.SourceAccountID, DestinationAccountID: tx.DestinationAccountID,
			ErrorMessage: tx.ErrorMessage, ExternalRef: tx.ExternalRef, Gateway: tx.Gateway,
			CreatedAt: tx.CreatedAt, UpdatedAt: tx.UpdatedAt,
		})
	}
	return out, nil
}

// GetMerchantTransaction delegates to the in-process ledger module (Plan
// 57 T10 follow-up).
func (h *LedgerHarness) GetMerchantTransaction(ctx context.Context, tenantID, txID uuid.UUID) (ledgerclient.Transaction, error) {
	tx, err := h.module.GetMerchantTransaction(ctx, tenantID, txID)
	if err != nil {
		return ledgerclient.Transaction{}, translateLedgerErr(err)
	}
	return ledgerclient.Transaction{
		ID: tx.ID, IdempotencyKey: tx.IdempotencyKey, IdempotencyScope: tx.IdempotencyScope,
		Type: tx.Type, Status: tx.Status, Amount: tx.Amount, Currency: tx.Currency,
		SourceAccountID: tx.SourceAccountID, DestinationAccountID: tx.DestinationAccountID,
		ErrorMessage: tx.ErrorMessage, ExternalRef: tx.ExternalRef, Gateway: tx.Gateway,
		CreatedAt: tx.CreatedAt, UpdatedAt: tx.UpdatedAt,
	}, nil
}
