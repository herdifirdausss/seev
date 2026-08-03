package client

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/pkg/ledgerclient"
)

// merchantTransferType is the Command.Type LedgerService's own
// MerchantTransfer processor registers under (internal/ledger/processors/merchant_transfer.go).
const merchantTransferType = "merchant_transfer"

// TransactionResult is the Gateway-side projection of a ledger transaction
// for the B2B accounts/transactions/transfers surface — deliberately its
// own type, not pkg/ledgerclient.Transaction, so internal/merchant/api's
// own DTO mapping stays decoupled from that package's shape.
type TransactionResult struct {
	ID                   uuid.UUID
	Type                 string
	Status               string
	Amount               decimal.Decimal
	Currency             string
	SourceAccountID      uuid.UUID
	DestinationAccountID uuid.UUID
	CreatedAt            time.Time
}

// AccountResult is the Gateway-side projection of a merchant tenant's own
// cash account and balance.
type AccountResult struct {
	AccountID uuid.UUID
	Currency  string
	Balance   decimal.Decimal
	Status    string
}

// ledgerPoster is the narrow structural interface LedgerClient depends on
// — satisfied by both *pkg/ledgerclient.Client (the real gRPC-connected
// client, used in production) and *internal/testutil.LedgerHarness (the
// in-process, shared-test-database stand-in payout/auth/notify's own
// integration tests already use for exactly this reason). Narrowing to
// an interface here, rather than depending on the concrete
// *ledgerclient.Client, is what makes a genuine end-to-end test of this
// package possible without a fake/mocked Ledger — this codebase's own
// established "never fake the thing whose own correctness you're trying
// to prove" convention.
type ledgerPoster interface {
	Post(ctx context.Context, command ledgerclient.Command) error
	GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (ledgerclient.Transaction, error)
	GetMerchantAccount(ctx context.Context, tenantID uuid.UUID) (ledgerclient.MerchantAccount, error)
	ListMerchantTransactions(ctx context.Context, tenantID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]ledgerclient.Transaction, error)
	GetMerchantTransaction(ctx context.Context, tenantID, txID uuid.UUID) (ledgerclient.Transaction, error)
}

// LedgerClient is the typed Gateway-side client to LedgerService's
// merchant-only RPCs (Plan 57 T10 follow-up: transfers, account, and
// transaction reads — T5/T6 built the RPCs, nothing ever called them over
// HTTP until now). Reuses ledgerPoster's Command/Post abstraction
// (LedgerService has no dedicated CreateMerchantTransfer RPC — a transfer
// is just another Command.Type, exactly like every other transaction type
// this codebase already posts through Post).
type LedgerClient struct {
	ledger ledgerPoster
}

func NewLedgerClient(ledger ledgerPoster) *LedgerClient {
	if ledger == nil {
		panic("merchant/client: NewLedgerClient requires a non-nil ledger client")
	}
	return &LedgerClient{ledger: ledger}
}

// CreateTransfer posts a merchant_transfer command (§3.3: "source = tenant
// account, destination as supplied") and returns the resulting
// transaction. downstreamKey is Gateway's own idempotency.DownstreamKey,
// scoped by tenantID (matching the merchant_transfer processor's own test
// coverage) — a retry with the same key recovers the ORIGINAL transaction
// via Ledger's own Post-time idempotency dedup
// (FindConflictOrDuplicate), never posting twice.
func (c *LedgerClient) CreateTransfer(ctx context.Context, tenantID, destinationAccountID uuid.UUID, amount decimal.Decimal, currency, downstreamKey string) (TransactionResult, error) {
	scope := tenantID.String()
	err := c.ledger.Post(ctx, ledgerclient.Command{
		IdempotencyKey: downstreamKey, IdempotencyScope: scope, Type: merchantTransferType,
		Amount: amount, Currency: currency, MerchantTenantID: tenantID,
		Metadata: map[string]any{"destination_account_id": destinationAccountID.String()},
	})
	if err != nil {
		return TransactionResult{}, translateError(err)
	}
	tx, err := c.ledger.GetTransactionByIdempotencyKey(ctx, downstreamKey, scope)
	if err != nil {
		return TransactionResult{}, translateError(err)
	}
	return transactionResultFromLedgerClient(tx), nil
}

// GetTransaction resolves one of tenantID's own transactions by id
// (backs both GET /transactions/{id} and GET /transfers/{id} — a merchant
// transaction has no separate "transfer" resource type). Tenant scoping
// happens inside LedgerService's own GetMerchantTransaction RPC (§7.3) —
// this method never re-derives it.
func (c *LedgerClient) GetTransaction(ctx context.Context, tenantID, txID uuid.UUID) (TransactionResult, error) {
	tx, err := c.ledger.GetMerchantTransaction(ctx, tenantID, txID)
	if err != nil {
		return TransactionResult{}, translateError(err)
	}
	return transactionResultFromLedgerClient(tx), nil
}

// ListTransactions returns tenantID's own transactions, newest first.
// beforeCreatedAt/beforeID are cursor fields for the next page (zero
// values mean "start from the most recent transaction") — tenant scoping
// happens inside LedgerService's own ListMerchantTransactions RPC (§7.3).
// Cursor encoding/decoding is the api package's own concern, not this
// client's — it passes already-decoded values here.
func (c *LedgerClient) ListTransactions(ctx context.Context, tenantID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]TransactionResult, error) {
	txs, err := c.ledger.ListMerchantTransactions(ctx, tenantID, beforeCreatedAt, beforeID, limit)
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]TransactionResult, 0, len(txs))
	for _, tx := range txs {
		out = append(out, transactionResultFromLedgerClient(tx))
	}
	return out, nil
}

// GetAccount resolves tenantID's own cash account and current balance —
// the account id is NEVER accepted from a caller, only ever resolved
// server-side (§7.3), mirroring the admin surface's own account read.
func (c *LedgerClient) GetAccount(ctx context.Context, tenantID uuid.UUID) (AccountResult, error) {
	acc, err := c.ledger.GetMerchantAccount(ctx, tenantID)
	if err != nil {
		return AccountResult{}, translateError(err)
	}
	return AccountResult{AccountID: acc.AccountID, Currency: acc.Currency, Balance: acc.Balance, Status: acc.Status}, nil
}

func transactionResultFromLedgerClient(tx ledgerclient.Transaction) TransactionResult {
	return TransactionResult{
		ID: tx.ID, Type: tx.Type, Status: tx.Status, Amount: tx.Amount, Currency: tx.Currency,
		SourceAccountID: tx.SourceAccountID, DestinationAccountID: tx.DestinationAccountID,
		CreatedAt: tx.CreatedAt,
	}
}
