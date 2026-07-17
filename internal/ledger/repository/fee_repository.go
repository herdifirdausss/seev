package repository

//go:generate mockgen -source=fee_repository.go -destination=fee_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

const feeRuleColumns = `id, tx_type, gateway, currency, user_id, flat_minor_units,
percent_basis_pts, fee_gateway, enabled, created_at, updated_at`

// resolveFeeRuleQuery's ordering encodes the specificity matrix: exact
// user+route, user default, route default, then global default.
const resolveFeeRuleQuery = `
SELECT flat_minor_units, percent_basis_pts, fee_gateway
FROM fee_rules
WHERE enabled
  AND tx_type = $1
  AND currency = $2
  AND (user_id = $3 OR user_id IS NULL)
  AND gateway IN ($4, '')
ORDER BY (user_id IS NOT NULL) DESC, (gateway <> '') DESC
LIMIT 1`

// getFeeQuoteQuery is a non-consuming read of a still-valid quote.
const getFeeQuoteQuery = `
	SELECT amount, fee_amount, fee_gateway
	FROM fee_quotes
	WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now()`

// tryConsumeFeeQuoteQuery atomically marks a quote consumed ONLY when every
// quoted dimension matches the request exactly (transaction_type, currency,
// amount) — a mismatched attempt affects 0 rows rather than silently
// burning the quote. Concurrency safety: two callers racing this UPDATE for
// the same quote_id are serialized by Postgres' own row lock — exactly one
// affects a row.
const tryConsumeFeeQuoteQuery = `
	UPDATE fee_quotes SET consumed_at = now(), consumed_by_ref = $6
	WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now()
	  AND transaction_type = $3 AND currency = $4 AND amount = $5
	RETURNING fee_amount, fee_gateway`

// feeQuoteExistsQuery is used only after tryConsumeFeeQuoteQuery affects 0
// rows, to classify the failure: a row still existing here (unconsumed,
// unexpired) means the UPDATE's extra WHERE clauses are what rejected it
// (mismatch); otherwise it was truly missing/expired/already consumed.
const feeQuoteExistsQuery = `
	SELECT 1 FROM fee_quotes
	WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now()`

// QueryRower is the minimal read capability fee-quote consumption needs —
// satisfied by both *sql.Tx (so it can run INSIDE the posting transaction;
// a rollback un-consumes automatically) and database.DatabaseSQL (so a
// caller with no active transaction of its own can still consume a quote).
type QueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// FeeRepository is raw data access over fee_rules and fee_quotes. It
// applies no business rules of its own — sql.ErrNoRows passes through
// unchanged for the caller (feepolicy.Policy) to interpret.
type FeeRepository interface {
	ListRules(ctx context.Context) ([]model.FeeRule, error)
	CreateRule(ctx context.Context, rule model.FeeRule) (model.FeeRule, error)
	// UpdateRule returns sql.ErrNoRows if no rule with that ID exists.
	UpdateRule(ctx context.Context, rule model.FeeRule) (model.FeeRule, error)
	// ResolveRule returns sql.ErrNoRows if no enabled rule matches.
	ResolveRule(ctx context.Context, txType, currency string, userID uuid.UUID, gateway string) (flatMinorUnits, percentBasisPts int64, feeGateway string, err error)

	InsertQuote(ctx context.Context, q model.FeeQuote) error
	// GetQuote returns sql.ErrNoRows if the quote is missing, expired, or
	// already consumed.
	GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (amount, feeAmount decimal.Decimal, feeGateway string, err error)
	// TryConsumeQuote returns sql.ErrNoRows if no row satisfied every WHERE
	// clause (mismatch OR truly expired/consumed/missing — caller
	// disambiguates via QuoteExists).
	TryConsumeQuote(ctx context.Context, exec QueryRower, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error)
	// QuoteExists reports whether an unconsumed, unexpired quote row exists
	// (ignoring type/currency/amount) — used only to classify a failed
	// TryConsumeQuote as mismatch vs truly-expired.
	QuoteExists(ctx context.Context, exec QueryRower, quoteID, userID uuid.UUID) (bool, error)
}

type feeRepo struct {
	db database.DatabaseSQL
}

// NewFeeRepository constructs a FeeRepository over the given pool handle.
func NewFeeRepository(db database.DatabaseSQL) FeeRepository {
	return &feeRepo{db: db}
}

func scanFeeRule(scanner interface{ Scan(...any) error }) (model.FeeRule, error) {
	var rule model.FeeRule
	err := scanner.Scan(&rule.ID, &rule.TxType, &rule.Gateway, &rule.Currency, &rule.UserID,
		&rule.FlatMinorUnits, &rule.PercentBasisPts, &rule.FeeGateway, &rule.Enabled,
		&rule.CreatedAt, &rule.UpdatedAt)
	return rule, err
}

func (r *feeRepo) ListRules(ctx context.Context) ([]model.FeeRule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+feeRuleColumns+` FROM fee_rules ORDER BY tx_type, currency, gateway, user_id NULLS FIRST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]model.FeeRule, 0)
	for rows.Next() {
		rule, scanErr := scanFeeRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (r *feeRepo) CreateRule(ctx context.Context, rule model.FeeRule) (model.FeeRule, error) {
	return scanFeeRule(r.db.QueryRowContext(ctx, `INSERT INTO fee_rules
		(id, tx_type, gateway, currency, user_id, flat_minor_units, percent_basis_pts, fee_gateway, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING `+feeRuleColumns,
		rule.ID, rule.TxType, rule.Gateway, rule.Currency, rule.UserID, rule.FlatMinorUnits,
		rule.PercentBasisPts, rule.FeeGateway, rule.Enabled))
}

func (r *feeRepo) UpdateRule(ctx context.Context, rule model.FeeRule) (model.FeeRule, error) {
	return scanFeeRule(r.db.QueryRowContext(ctx, `UPDATE fee_rules SET
		tx_type=$2, gateway=$3, currency=$4, user_id=$5, flat_minor_units=$6,
		percent_basis_pts=$7, fee_gateway=$8, enabled=$9
		WHERE id=$1 RETURNING `+feeRuleColumns,
		rule.ID, rule.TxType, rule.Gateway, rule.Currency, rule.UserID, rule.FlatMinorUnits,
		rule.PercentBasisPts, rule.FeeGateway, rule.Enabled))
}

func (r *feeRepo) ResolveRule(ctx context.Context, txType, currency string, userID uuid.UUID, gateway string) (flatMinorUnits, percentBasisPts int64, feeGateway string, err error) {
	err = r.db.QueryRowContext(ctx, resolveFeeRuleQuery, txType, currency, userID, gateway).
		Scan(&flatMinorUnits, &percentBasisPts, &feeGateway)
	return flatMinorUnits, percentBasisPts, feeGateway, err
}

func (r *feeRepo) InsertQuote(ctx context.Context, q model.FeeQuote) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO fee_quotes (id, user_id, transaction_type, gateway, currency, amount, fee_amount, fee_gateway, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		q.ID, q.UserID, q.TransactionType, q.Gateway, q.Currency, q.Amount, q.FeeAmount, q.FeeGateway, q.ExpiresAt)
	return err
}

func (r *feeRepo) GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (amount, feeAmount decimal.Decimal, feeGateway string, err error) {
	err = r.db.QueryRowContext(ctx, getFeeQuoteQuery, quoteID, userID).Scan(&amount, &feeAmount, &feeGateway)
	return amount, feeAmount, feeGateway, err
}

func (r *feeRepo) TryConsumeQuote(ctx context.Context, exec QueryRower, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error) {
	err = exec.QueryRowContext(ctx, tryConsumeFeeQuoteQuery, quoteID, userID, txType, currency, amount, ref).Scan(&fee, &feeGateway)
	return fee, feeGateway, err
}

func (r *feeRepo) QuoteExists(ctx context.Context, exec QueryRower, quoteID, userID uuid.UUID) (bool, error) {
	var exists int
	err := exec.QueryRowContext(ctx, feeQuoteExistsQuery, quoteID, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check quote exists: %w", err)
	}
	return true, nil
}
