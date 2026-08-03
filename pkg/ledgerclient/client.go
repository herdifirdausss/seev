// Package ledgerclient is the boundary-clean client for the ledger service.
package ledgerclient

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

type Command struct {
	IdempotencyKey   string
	IdempotencyScope string
	Type             string
	Amount           decimal.Decimal
	UserID           uuid.UUID
	TargetUserID     uuid.UUID
	PocketCode       string
	Currency         string
	ReferenceID      uuid.UUID
	Metadata         map[string]any
	// MerchantTenantID (Plan 57 T5) is set ONLY for type="merchant_transfer".
	MerchantTenantID uuid.UUID
}

// MerchantAccount is a merchant tenant's cash account and its current
// balance (Plan 57 T5).
type MerchantAccount struct {
	AccountID uuid.UUID
	Currency  string
	Balance   decimal.Decimal
	Status    string
}

type Transaction struct {
	ID                   uuid.UUID
	IdempotencyKey       string
	IdempotencyScope     string
	Type                 string
	Status               string
	Amount               decimal.Decimal
	Currency             string
	SourceAccountID      uuid.UUID
	DestinationAccountID uuid.UUID
	ErrorMessage         string
	ExternalRef          string
	Gateway              string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type Client struct {
	client ledgerv1.LedgerServiceClient
}

func New(conn *grpc.ClientConn) *Client {
	return &Client{client: ledgerv1.NewLedgerServiceClient(conn)}
}

func (c *Client) Post(ctx context.Context, command Command) error {
	if command.Currency != "" {
		ctx = grpcmetadata.AppendToOutgoingContext(ctx, "x-seev-currency", command.Currency)
	}
	var metadata *structpb.Struct
	var err error
	if command.Metadata != nil {
		metadata, err = structpb.NewStruct(command.Metadata)
		if err != nil {
			return fmt.Errorf("ledgerclient: encode metadata: %w", err)
		}
	}
	_, err = c.client.Post(ctx, &ledgerv1.PostRequest{
		IdempotencyKey: command.IdempotencyKey, IdempotencyScope: command.IdempotencyScope,
		Type: command.Type, Amount: command.Amount.String(), UserId: uuidString(command.UserID),
		TargetUserId: uuidString(command.TargetUserID), PocketCode: command.PocketCode,
		ReferenceId: uuidString(command.ReferenceID), Metadata: metadata,
		MerchantTenantId: uuidString(command.MerchantTenantID),
	})
	return ledgererr.FromStatus(err)
}

func (c *Client) GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (Transaction, error) {
	tx, err := c.client.GetTransactionByIdempotencyKey(ctx, &ledgerv1.GetTxByIdemKeyRequest{
		IdempotencyKey: key, IdempotencyScope: scope,
	})
	if err != nil {
		return Transaction{}, ledgererr.FromStatus(err)
	}
	return transactionFromProto(tx)
}

func (c *Client) GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error) {
	response, err := c.client.GetUserCurrency(ctx, &ledgerv1.GetUserCurrencyRequest{
		UserId: uuidString(userID), PocketCode: pocketCode,
	})
	if err != nil {
		return "", ledgererr.FromStatus(err)
	}
	return response.GetCurrency(), nil
}

// ValidateCurrency uses the existing internal Ledger RPC with an explicit
// metadata mode. This keeps the protobuf surface backward-compatible while
// allowing split Payin/Payout processes to consult Ledger-owned currency policy.
func (c *Client) ValidateCurrency(ctx context.Context, code, operation string) error {
	ctx = grpcmetadata.AppendToOutgoingContext(ctx,
		"x-seev-currency", code, "x-seev-currency-operation", operation)
	_, err := c.client.GetUserCurrency(ctx, &ledgerv1.GetUserCurrencyRequest{})
	return ledgererr.FromStatus(err)
}

// UserCurrencyEnabled asks Ledger whether the user owns the requested
// currency-specific account family. A false result is returned as an empty
// successful response so callers can distinguish a missing account from a
// transport or Ledger failure.
func (c *Client) UserCurrencyEnabled(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	ctx = grpcmetadata.AppendToOutgoingContext(ctx, "x-seev-currency-check", code)
	response, err := c.client.GetUserCurrency(ctx, &ledgerv1.GetUserCurrencyRequest{UserId: uuidString(userID)})
	if err != nil {
		return false, ledgererr.FromStatus(err)
	}
	return response.GetCurrency() == code, nil
}

func (c *Client) ResolveFee(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (decimal.Decimal, string, bool, error) {
	response, err := c.client.ResolveFee(ctx, &ledgerv1.ResolveFeeRequest{
		Type: txType, Gateway: gateway, Currency: currency, Amount: amount.String(), UserId: uuidString(userID),
	})
	if err != nil {
		return decimal.Zero, "", false, ledgererr.FromStatus(err)
	}
	fee, err := decimal.NewFromString(response.GetFee())
	if err != nil {
		return decimal.Zero, "", false, fmt.Errorf("ledgerclient: invalid fee in response: %w", err)
	}
	return fee, response.GetFeeGateway(), response.GetOk(), nil
}

func (c *Client) ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) error {
	_, err := c.client.ProvisionUser(ctx, &ledgerv1.ProvisionUserRequest{UserId: uuidString(userID), Currency: currency})
	return ledgererr.FromStatus(err)
}

// ProvisionMerchant is Plan 57 T5's additive RPC — idempotently provisions
// a merchant tenant's cash account, returning its account id.
func (c *Client) ProvisionMerchant(ctx context.Context, tenantID uuid.UUID, currency string) (uuid.UUID, error) {
	resp, err := c.client.ProvisionMerchant(ctx, &ledgerv1.ProvisionMerchantRequest{TenantId: uuidString(tenantID), Currency: currency})
	if err != nil {
		return uuid.Nil, ledgererr.FromStatus(err)
	}
	return parseUUID(resp.GetAccountId())
}

// GetMerchantAccount resolves a tenant's cash account and current balance
// in one round trip — tenantID is the ONLY identity this call accepts; the
// account id is always resolved server-side (Plan 57 T5).
func (c *Client) GetMerchantAccount(ctx context.Context, tenantID uuid.UUID) (MerchantAccount, error) {
	resp, err := c.client.GetMerchantAccount(ctx, &ledgerv1.GetMerchantAccountRequest{TenantId: uuidString(tenantID)})
	if err != nil {
		return MerchantAccount{}, ledgererr.FromStatus(err)
	}
	accountID, err := parseUUID(resp.GetAccountId())
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("ledgerclient: invalid account id: %w", err)
	}
	balance, err := decimal.NewFromString(resp.GetBalance())
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("ledgerclient: invalid balance: %w", err)
	}
	return MerchantAccount{AccountID: accountID, Currency: resp.GetCurrency(), Balance: balance, Status: resp.GetStatus()}, nil
}

// ListMerchantTransactions returns tenantID's own transactions, newest
// first — tenantID is the ONLY identity this call accepts; the account id
// is always resolved server-side, so no caller can list another tenant's
// transactions (Plan 57 T5). before* are cursor fields for the next page;
// zero values mean "start from the most recent transaction".
func (c *Client) ListMerchantTransactions(ctx context.Context, tenantID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]Transaction, error) {
	req := &ledgerv1.ListMerchantTransactionsRequest{
		TenantId: uuidString(tenantID), Limit: int32(limit), BeforeId: uuidString(beforeID),
	}
	if !beforeCreatedAt.IsZero() {
		req.BeforeCreatedAt = timestamppb.New(beforeCreatedAt)
	}
	resp, err := c.client.ListMerchantTransactions(ctx, req)
	if err != nil {
		return nil, ledgererr.FromStatus(err)
	}
	txs := make([]Transaction, 0, len(resp.GetTransactions()))
	for _, tx := range resp.GetTransactions() {
		t, err := transactionFromProto(tx)
		if err != nil {
			return nil, err
		}
		txs = append(txs, t)
	}
	return txs, nil
}

// GetMerchantTransaction resolves ONE of tenantID's own transactions by id
// (Plan 57 T10 follow-up) — tenantID is the ONLY identity this call
// accepts; the account-membership check happens server-side, so no caller
// can ever read another tenant's transaction.
func (c *Client) GetMerchantTransaction(ctx context.Context, tenantID, txID uuid.UUID) (Transaction, error) {
	tx, err := c.client.GetMerchantTransaction(ctx, &ledgerv1.GetMerchantTransactionRequest{
		TenantId: uuidString(tenantID), TransactionId: uuidString(txID),
	})
	if err != nil {
		return Transaction{}, ledgererr.FromStatus(err)
	}
	return transactionFromProto(tx)
}

// ConsumeFeeQuote is docs/roadmap/archive/38 Task T5's additive RPC — a rejection
// (quote expired/mismatch) decodes generically into *ledgererr.LedgerError
// with Code "QUOTE_EXPIRED"/"QUOTE_MISMATCH" via ledgererr.FromStatus (no
// dedicated sentinel needed — same free gRPC-parity mechanism as
// SCREENING_BLOCKED, as explained by docs/roadmap/archive/38 Task T4's results).
func (c *Client) ConsumeFeeQuote(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, consumedByRef string) (decimal.Decimal, string, error) {
	response, err := c.client.ConsumeFeeQuote(ctx, &ledgerv1.ConsumeFeeQuoteRequest{
		QuoteId: quoteID.String(), UserId: uuidString(userID), TransactionType: txType,
		Currency: currency, Amount: amount.String(), ConsumedByRef: consumedByRef,
	})
	if err != nil {
		return decimal.Zero, "", ledgererr.FromStatus(err)
	}
	fee, err := decimal.NewFromString(response.GetFeeAmount())
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("ledgerclient: invalid fee_amount in response: %w", err)
	}
	return fee, response.GetFeeGateway(), nil
}

// ApplyKycTier is docs/roadmap/archive/39 Task T5's additive RPC — upserts the
// caller's effective policy_limits from the policy_tier_limits template for
// kycLevel. An unrecognized kyc_level surfaces as a gRPC InvalidArgument
// (not decoded into *ledgererr.LedgerError — that shape is reserved for
// business-state failures, this is a caller input error); callers that need
// to distinguish it can check status.Code(err) == codes.InvalidArgument.
func (c *Client) ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int) error {
	_, err := c.client.ApplyKycTier(ctx, &ledgerv1.ApplyKycTierRequest{UserId: uuidString(userID), KycLevel: int32(kycLevel)})
	return ledgererr.FromStatus(err)
}

func transactionFromProto(tx *ledgerv1.Transaction) (Transaction, error) {
	id, err := parseUUID(tx.GetId())
	if err != nil {
		return Transaction{}, fmt.Errorf("ledgerclient: invalid transaction id: %w", err)
	}
	sourceID, err := parseUUID(tx.GetSourceAccountId())
	if err != nil {
		return Transaction{}, fmt.Errorf("ledgerclient: invalid source account id: %w", err)
	}
	destinationID, err := parseUUID(tx.GetDestinationAccountId())
	if err != nil {
		return Transaction{}, fmt.Errorf("ledgerclient: invalid destination account id: %w", err)
	}
	amount, err := decimal.NewFromString(tx.GetAmount())
	if err != nil {
		return Transaction{}, fmt.Errorf("ledgerclient: invalid transaction amount: %w", err)
	}
	result := Transaction{
		ID: id, IdempotencyKey: tx.GetIdempotencyKey(), IdempotencyScope: tx.GetIdempotencyScope(),
		Type: tx.GetType(), Status: tx.GetStatus(), Amount: amount, Currency: tx.GetCurrency(),
		SourceAccountID: sourceID, DestinationAccountID: destinationID,
		ErrorMessage: tx.GetErrorMessage(), ExternalRef: tx.GetExternalRef(), Gateway: tx.GetGateway(),
	}
	if tx.CreatedAt != nil {
		result.CreatedAt = tx.CreatedAt.AsTime()
	}
	if tx.UpdatedAt != nil {
		result.UpdatedAt = tx.UpdatedAt.AsTime()
	}
	return result, nil
}

func uuidString(value uuid.UUID) string {
	if value == uuid.Nil {
		return ""
	}
	return value.String()
}

func parseUUID(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(value)
}
