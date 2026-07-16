package feepolicy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DefaultQuoteTTL is used when the caller passes ttl <= 0 (docs/plan/38
// Task T2) — overridden by FEE_QUOTE_TTL, wired in internal/config.
const DefaultQuoteTTL = 10 * time.Minute

// ErrQuoteExpired covers not-found, already-consumed, AND expired — these
// are deliberately not distinguished to the client (docs/plan/38 Task T2):
// none of the three is actionable differently than "request a new quote".
var ErrQuoteExpired = errors.New("fee quote not found, expired, or already consumed")

// ErrQuoteMismatch means the quote exists, is unconsumed, and unexpired,
// but the transaction attempting to consume it doesn't match what was
// quoted (transaction_type, currency, or exact amount differs) — the
// caller tampered with (or simply changed) the request between quoting and
// posting. Deliberately does NOT burn the quote (docs/plan/38 Task T4 test
// requirement: "quote tidak berubah status" on mismatch) — a client bug or
// legitimate amount change shouldn't force a needless re-quote.
var ErrQuoteMismatch = errors.New("fee quote does not match this request")

// Quote is a fee locked in for a specific (user, tx_type, currency, amount)
// combination until ExpiresAt, single-use.
type Quote struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	TransactionType string
	Gateway         string
	Currency        string
	Amount          decimal.Decimal
	FeeAmount       decimal.Decimal
	FeeGateway      string
	ExpiresAt       time.Time
}

// execer is the minimal read capability ConsumeQuote needs — satisfied by
// both *sql.Tx (so it can run INSIDE the posting transaction; a rollback
// un-consumes automatically) and database.DatabaseSQL (so payout's own
// short-lived, separate consumption — docs/plan/38 Task T5 — doesn't need
// to thread a *sql.Tx across a gRPC boundary).
type execer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

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
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO fee_quotes (id, user_id, transaction_type, gateway, currency, amount, fee_amount, fee_gateway, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		q.ID, q.UserID, q.TransactionType, q.Gateway, q.Currency, q.Amount, q.FeeAmount, q.FeeGateway, q.ExpiresAt)
	if err != nil {
		return Quote{}, err
	}
	return q, nil
}

// getQuoteQuery is a non-consuming read of a still-valid quote — used by
// GetQuote (docs/plan/38 Task T4's account-resolution peek, see
// internal/ledger/service/handle.Service.Handle's own doc comment for why
// this is needed) and has NO side effects, unlike consumeQuoteQuery.
const getQuoteQuery = `
	SELECT amount, fee_amount, fee_gateway
	FROM fee_quotes
	WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now()`

// GetQuote is a read-only lookup — it does NOT consume the quote. Returns
// ErrQuoteExpired under the same collapsed not-found/expired/consumed
// semantics as ConsumeQuote (see that sentinel's own doc comment).
func (p *Policy) GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (Quote, error) {
	q := Quote{ID: quoteID, UserID: userID}
	err := p.db.QueryRowContext(ctx, getQuoteQuery, quoteID, userID).Scan(&q.Amount, &q.FeeAmount, &q.FeeGateway)
	if errors.Is(err, sql.ErrNoRows) {
		return Quote{}, ErrQuoteExpired
	}
	if err != nil {
		return Quote{}, err
	}
	return q, nil
}

// consumeQuoteQuery atomically marks a quote consumed ONLY when every
// quoted dimension matches the request exactly (transaction_type, currency,
// amount) — this is what makes a mismatched attempt a no-op (0 rows) rather
// than silently burning the quote (see ErrQuoteMismatch's own doc comment).
// Concurrency safety: two callers racing this UPDATE for the same quote_id
// are serialized by Postgres' own row lock — exactly one affects a row.
const consumeQuoteQuery = `
	UPDATE fee_quotes SET consumed_at = now(), consumed_by_ref = $6
	WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now()
	  AND transaction_type = $3 AND currency = $4 AND amount = $5
	RETURNING fee_amount, fee_gateway`

// classifyQuoteQuery runs ONLY after consumeQuoteQuery affects 0 rows, to
// pick the right sentinel: if a row still exists here (unconsumed,
// unexpired) the UPDATE's extra WHERE clauses are what rejected it →
// ErrQuoteMismatch; otherwise it was truly missing/expired/already
// consumed → ErrQuoteExpired. A benign race is possible (another caller
// consumes the row between the two statements) — harmless, since this
// query only selects an error MESSAGE, never money; the UPDATE above is
// the sole source of truth for whether consumption happened.
const classifyQuoteQuery = `
	SELECT 1 FROM fee_quotes
	WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now()`

// ConsumeQuote is the single-use, atomic, exact-match consumption path
// (docs/plan/38 Task T2/T4). ref is persisted as consumed_by_ref
// ('tx:<uuid>' | 'payout:<uuid>') for audit — which entity spent this
// quote.
func (p *Policy) ConsumeQuote(ctx context.Context, exec execer, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error) {
	scanErr := exec.QueryRowContext(ctx, consumeQuoteQuery, quoteID, userID, txType, currency, amount, ref).Scan(&fee, &feeGateway)
	if scanErr == nil {
		return fee, feeGateway, nil
	}
	if !errors.Is(scanErr, sql.ErrNoRows) {
		return decimal.Zero, "", scanErr
	}

	var exists int
	classifyErr := exec.QueryRowContext(ctx, classifyQuoteQuery, quoteID, userID).Scan(&exists)
	if errors.Is(classifyErr, sql.ErrNoRows) {
		return decimal.Zero, "", ErrQuoteExpired
	}
	if classifyErr != nil {
		return decimal.Zero, "", classifyErr
	}
	return decimal.Zero, "", ErrQuoteMismatch
}

// ConsumeQuoteStandalone is ConsumeQuote for a caller with no active
// transaction of its own to run inside (docs/plan/38 Task T5: payout's
// consumption is a short, separate gRPC-triggered operation — the
// payout_requests row is ITS OWN commitment, not something that shares a
// tx with the ledger side at all).
func (p *Policy) ConsumeQuoteStandalone(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (decimal.Decimal, string, error) {
	return p.ConsumeQuote(ctx, p.db, quoteID, userID, txType, currency, amount, ref)
}
