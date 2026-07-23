package feepolicy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// DefaultQuoteTTL is used when the caller passes ttl <= 0 (docs/roadmap/archive/38
// Task T2) — overridden by FEE_QUOTE_TTL, wired in internal/config.
const DefaultQuoteTTL = 10 * time.Minute

// ErrQuoteExpired covers not-found, already-consumed, AND expired — these
// are deliberately not distinguished to the client (docs/roadmap/archive/38 Task T2):
// none of the three is actionable differently than "request a new quote".
var ErrQuoteExpired = errors.New("fee quote not found, expired, or already consumed")

// ErrQuoteMismatch means the quote exists, is unconsumed, and unexpired,
// but the transaction attempting to consume it doesn't match what was
// quoted (transaction_type, currency, or exact amount differs) — the
// caller tampered with (or simply changed) the request between quoting and
// posting. Deliberately does NOT burn the quote (docs/roadmap/archive/38 Task T4 test
// requirement: "the quote status must not change" on mismatch) — a client bug or
// legitimate amount change shouldn't force a needless re-quote.
var ErrQuoteMismatch = errors.New("fee quote does not match this request")

// CreateQuote prices txType/gateway/currency/amount via the same Resolve
// used at posting time (identical specificity matrix, no separate pricing
// path to drift) and persists a single-use quote valid for ttl (<=0 falls
// back to DefaultQuoteTTL). A quote is created even when no fee rule
// matches (FeeAmount zero) — "no fee configured" is a valid, quotable fact.
func (p *Policy) CreateQuote(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, ttl time.Duration) (Quote, error) {
	if ttl <= 0 {
		ttl = DefaultQuoteTTL
	}
	fee, feeGateway, _ := p.Resolve(ctx, userID, txType, gateway, currency, amount)

	q := Quote{
		ID: uuid.New(), UserID: userID, TransactionType: txType, Gateway: gateway,
		Currency: currency, Amount: amount, FeeAmount: fee, FeeGateway: feeGateway,
		ExpiresAt: time.Now().Add(ttl),
	}
	if err := p.repo.InsertQuote(ctx, q); err != nil {
		return Quote{}, err
	}
	return q, nil
}

// GetQuote is a read-only lookup — it does NOT consume the quote. Returns
// ErrQuoteExpired under the same collapsed not-found/expired/consumed
// semantics as ConsumeQuote (see that sentinel's own doc comment).
func (p *Policy) GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (Quote, error) {
	q := Quote{ID: quoteID, UserID: userID}
	amount, feeAmount, feeGateway, err := p.repo.GetQuote(ctx, quoteID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Quote{}, ErrQuoteExpired
	}
	if err != nil {
		return Quote{}, err
	}
	q.Amount, q.FeeAmount, q.FeeGateway = amount, feeAmount, feeGateway
	return q, nil
}

// ConsumeQuote is the single-use, atomic, exact-match consumption path
// (docs/roadmap/archive/38 Task T2/T4). ref is persisted as consumed_by_ref
// ('tx:<uuid>' | 'payout:<uuid>') for audit — which entity spent this
// quote. exec is *sql.Tx (to run INSIDE the posting transaction — a
// rollback un-consumes automatically) or the plain pool via
// ConsumeQuoteStandalone.
func (p *Policy) ConsumeQuote(ctx context.Context, exec repository.QueryRower, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error) {
	fee, feeGateway, err = p.repo.TryConsumeQuote(ctx, exec, quoteID, userID, txType, currency, amount, ref)
	if err == nil {
		return fee, feeGateway, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return decimal.Zero, "", err
	}

	// tryConsumeFeeQuoteQuery's WHERE clauses rejected every row: a benign
	// race is possible (another caller consumes it between the two calls)
	// — harmless, since QuoteExists only selects an error MESSAGE, never
	// money; TryConsumeQuote above is the sole source of truth for whether
	// consumption happened.
	exists, existsErr := p.repo.QuoteExists(ctx, exec, quoteID, userID)
	if existsErr != nil {
		return decimal.Zero, "", existsErr
	}
	if !exists {
		return decimal.Zero, "", ErrQuoteExpired
	}
	return decimal.Zero, "", ErrQuoteMismatch
}

// ConsumeQuoteStandalone is ConsumeQuote for a caller with no active
// transaction of its own to run inside (docs/roadmap/archive/38 Task T5: payout's
// consumption is a short, separate gRPC-triggered operation — the
// payout_requests row is ITS OWN commitment, not something that shares a
// tx with the ledger side at all).
func (p *Policy) ConsumeQuoteStandalone(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (decimal.Decimal, string, error) {
	return p.ConsumeQuote(ctx, p.db, quoteID, userID, txType, currency, amount, ref)
}
