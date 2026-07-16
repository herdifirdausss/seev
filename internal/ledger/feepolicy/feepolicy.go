// Package feepolicy resolves server-controlled transaction fees from the
// ledger database. A public caller can never supply fee metadata directly.
package feepolicy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/pkg/database"
)

const resolveQuery = `
SELECT flat_minor_units, percent_basis_pts, fee_gateway
FROM fee_rules
WHERE enabled
  AND tx_type = $1
  AND currency = $2
  AND (user_id = $3 OR user_id IS NULL)
  AND gateway IN ($4, '')
ORDER BY (user_id IS NOT NULL) DESC, (gateway <> '') DESC
LIMIT 1`

// Rule is the persisted pricing configuration selected by Resolve.
type Rule struct {
	ID              uuid.UUID
	TxType          string
	Gateway         string
	Currency        string
	UserID          *uuid.UUID
	FlatMinorUnits  int64
	PercentBasisPts int64
	FeeGateway      string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

var ErrRuleNotFound = errors.New("fee rule not found")

// Policy resolves each fee directly from PostgreSQL. There is deliberately
// no process-local cache: admin changes take effect on the next request.
type Policy struct {
	db database.DatabaseSQL
}

func New(db database.DatabaseSQL) *Policy { return &Policy{db: db} }

const ruleColumns = `id, tx_type, gateway, currency, user_id, flat_minor_units,
percent_basis_pts, fee_gateway, enabled, created_at, updated_at`

func scanRule(scanner interface{ Scan(...any) error }) (Rule, error) {
	var rule Rule
	err := scanner.Scan(&rule.ID, &rule.TxType, &rule.Gateway, &rule.Currency, &rule.UserID,
		&rule.FlatMinorUnits, &rule.PercentBasisPts, &rule.FeeGateway, &rule.Enabled,
		&rule.CreatedAt, &rule.UpdatedAt)
	return rule, err
}

func (p *Policy) List(ctx context.Context) ([]Rule, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+ruleColumns+` FROM fee_rules ORDER BY tx_type, currency, gateway, user_id NULLS FIRST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]Rule, 0)
	for rows.Next() {
		rule, scanErr := scanRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (p *Policy) Create(ctx context.Context, rule Rule) (Rule, error) {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	return scanRule(p.db.QueryRowContext(ctx, `INSERT INTO fee_rules
		(id, tx_type, gateway, currency, user_id, flat_minor_units, percent_basis_pts, fee_gateway, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING `+ruleColumns,
		rule.ID, rule.TxType, rule.Gateway, rule.Currency, rule.UserID, rule.FlatMinorUnits,
		rule.PercentBasisPts, rule.FeeGateway, rule.Enabled))
}

func (p *Policy) Update(ctx context.Context, rule Rule) (Rule, error) {
	updated, err := scanRule(p.db.QueryRowContext(ctx, `UPDATE fee_rules SET
		tx_type=$2, gateway=$3, currency=$4, user_id=$5, flat_minor_units=$6,
		percent_basis_pts=$7, fee_gateway=$8, enabled=$9
		WHERE id=$1 RETURNING `+ruleColumns,
		rule.ID, rule.TxType, rule.Gateway, rule.Currency, rule.UserID, rule.FlatMinorUnits,
		rule.PercentBasisPts, rule.FeeGateway, rule.Enabled))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrRuleNotFound
	}
	return updated, err
}

// Resolve performs one lookup whose ordering encodes the specificity matrix:
// exact user+route, user default, route default, then global default.
// Missing rows and database errors both fail closed to "no fee"; fee pricing
// must never turn an otherwise valid posting into a malformed command.
func (p *Policy) Resolve(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (feeAmount decimal.Decimal, feeGateway string, ok bool) {
	if p == nil || p.db == nil || currency == "" {
		return decimal.Zero, "", false
	}

	var rule Rule
	err := p.db.QueryRowContext(ctx, resolveQuery, txType, currency, userID, gateway).
		Scan(&rule.FlatMinorUnits, &rule.PercentBasisPts, &rule.FeeGateway)
	if err != nil {
		// sql.ErrNoRows is expected when pricing is disabled. Infrastructure
		// errors use the same conservative outcome because this API has no
		// error channel and downstream fee metadata is optional.
		return decimal.Zero, "", false
	}

	fee := decimal.NewFromInt(rule.FlatMinorUnits)
	if rule.PercentBasisPts > 0 {
		percentage := amount.Mul(decimal.NewFromInt(rule.PercentBasisPts)).
			Div(decimal.NewFromInt(10_000)).Truncate(0)
		fee = fee.Add(percentage)
	}
	if !fee.IsPositive() || fee.GreaterThanOrEqual(amount) {
		return decimal.Zero, "", false
	}
	if rule.FeeGateway == "" {
		rule.FeeGateway = "platform"
	}
	return fee, rule.FeeGateway, true
}
