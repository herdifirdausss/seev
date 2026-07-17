// Package feepolicy resolves server-controlled transaction fees. A public
// caller can never supply fee metadata directly.
package feepolicy

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/database"
)

// Rule and Quote are aliases onto the model package's raw persisted shapes —
// kept here so existing callers (transport DTOs, integration tests) don't
// need to change their import.
type Rule = model.FeeRule
type Quote = model.FeeQuote

var ErrRuleNotFound = errors.New("fee rule not found")

// Policy resolves each fee via repo. There is deliberately no process-local
// cache: admin changes take effect on the next request. db is held only to
// hand to ConsumeQuoteStandalone as the "run this against the plain pool,
// no caller transaction" argument — Policy itself never issues SQL.
type Policy struct {
	db   database.DatabaseSQL
	repo repository.FeeRepository
}

func New(db database.DatabaseSQL, repo repository.FeeRepository) *Policy {
	return &Policy{db: db, repo: repo}
}

func (p *Policy) List(ctx context.Context) ([]Rule, error) {
	return p.repo.ListRules(ctx)
}

func (p *Policy) Create(ctx context.Context, rule Rule) (Rule, error) {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	return p.repo.CreateRule(ctx, rule)
}

func (p *Policy) Update(ctx context.Context, rule Rule) (Rule, error) {
	updated, err := p.repo.UpdateRule(ctx, rule)
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
	if p == nil || p.repo == nil || currency == "" {
		return decimal.Zero, "", false
	}

	flat, bps, ruleFeeGateway, err := p.repo.ResolveRule(ctx, txType, currency, userID, gateway)
	if err != nil {
		// sql.ErrNoRows is expected when pricing is disabled. Infrastructure
		// errors use the same conservative outcome because this API has no
		// error channel and downstream fee metadata is optional.
		return decimal.Zero, "", false
	}

	fee := decimal.NewFromInt(flat)
	if bps > 0 {
		percentage := amount.Mul(decimal.NewFromInt(bps)).
			Div(decimal.NewFromInt(10_000)).Truncate(0)
		fee = fee.Add(percentage)
	}
	if !fee.IsPositive() || fee.GreaterThanOrEqual(amount) {
		return decimal.Zero, "", false
	}
	if ruleFeeGateway == "" {
		ruleFeeGateway = "platform"
	}
	return fee, ruleFeeGateway, true
}
