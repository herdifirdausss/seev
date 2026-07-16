// Package accrual computes and posts daily interest for savings-product
// accounts (docs/plan/19 Task T3, decision S8). The amount basis is always
// a SNAPSHOT balance (account_balance_snapshots, docs/plan/15 Task T1),
// never a live balance — RunDue is the only place this computation
// happens; the interest_accrue processor it posts through never
// recomputes or re-checks the amount, just posts the two entries.
package accrual

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// DatabaseSQL is the thin interface over the connection pool this service
// needs — mirrors this session's other service packages' own narrow
// redefinitions.
type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// Poster is the subset of ledgerhandle.Service this package needs.
type Poster interface {
	Handle(ctx context.Context, cmd processors.Command) error
}

// BalanceReader is the subset of repository.SnapshotRepository this
// package needs — a snapshot-basis balance lookup, never a live one.
type BalanceReader interface {
	BalanceAsOf(ctx context.Context, accountID uuid.UUID, date time.Time) (decimal.Decimal, error)
}

// bpsDenominator / daysPerYear implement docs/plan/19 Task T3's locked
// formula: floor(balance * annual_rate_bps / 10000 / 365).
const (
	bpsDenominator = 10000
	daysPerYear    = 365
)

type Service struct {
	db       DatabaseSQL
	repo     repository.SavingsRepository
	snapshot BalanceReader
	poster   Poster
	logger   *slog.Logger
}

func New(db DatabaseSQL, repo repository.SavingsRepository, snapshot BalanceReader, poster Poster, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, repo: repo, snapshot: snapshot, poster: poster, logger: logger}
}

// SetConfig registers (or re-registers) an account as interest-bearing.
func (s *Service) SetConfig(ctx context.Context, accountID uuid.UUID, annualRateBps int, enabled bool) error {
	if accountID == uuid.Nil {
		return fmt.Errorf("%w: account_id is required", apperror.ErrValidation)
	}
	if annualRateBps < 0 || annualRateBps > 2000 {
		return fmt.Errorf("%w: annual_rate_bps must be between 0 and 2000", apperror.ErrValidation)
	}
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.Upsert(ctx, tx, model.SavingsConfig{AccountID: accountID, AnnualRateBps: annualRateBps, Enabled: enabled})
	})
}

// GetConfig returns one account's savings config.
func (s *Service) GetConfig(ctx context.Context, accountID uuid.UUID) (model.SavingsConfig, error) {
	return s.repo.Get(ctx, accountID)
}

// ListConfigs returns every registered savings account (enabled or not).
func (s *Service) ListConfigs(ctx context.Context) ([]model.SavingsConfig, error) {
	return s.repo.ListAll(ctx)
}

// RunDue computes and posts interest for every enabled savings account,
// using the closing balance snapshot for asOf as the basis (docs/plan/19
// Task T3 step 3) — NEVER the account's current live balance. Returns how
// many accounts were accrued vs skipped (zero-interest accounts, and any
// lookup/post error, both count as skipped — an error on one account must
// never block the rest, docs/plan/19's core resilience posture).
func (s *Service) RunDue(ctx context.Context, asOf time.Time) (accrued, skipped int) {
	configs, err := s.repo.ListEnabled(ctx)
	if err != nil {
		s.logger.Error("accrual: list enabled savings config failed", slog.Any("error", err))
		return 0, 0
	}

	for _, cfg := range configs {
		balance, err := s.snapshot.BalanceAsOf(ctx, cfg.AccountID, asOf)
		if err != nil {
			s.logger.Error("accrual: balance lookup failed, skipping", slog.String("account_id", cfg.AccountID.String()), slog.Any("error", err))
			skipped++
			continue
		}
		interest := DailyInterest(balance, cfg.AnnualRateBps)
		if !interest.IsPositive() {
			// Not an error — a zero/negative balance, or a balance too
			// small for this rate to round up to at least 1 minor unit,
			// simply has nothing to accrue today (docs/plan/19 Task T3
			// step 3: "hasil 0 -> tidak posting").
			skipped++
			continue
		}

		key := accrualIdempotencyKey(cfg.AccountID, asOf)
		cmd := processors.Command{
			IdempotencyKey: key,
			Type:           "interest_accrue",
			Amount:         interest,
			Metadata: map[string]any{
				"account_id":   cfg.AccountID.String(),
				"accrual_date": asOf.Format("2006-01-02"),
				"rate_bps":     fmt.Sprintf("%d", cfg.AnnualRateBps),
			},
		}
		postErr := s.poster.Handle(ctx, cmd)
		if postErr == nil || errors.Is(postErr, apperror.ErrAlreadyPosted) {
			// ErrAlreadyPosted means a prior run already posted this exact
			// (account, date) key and the job is being retried after a
			// crash/restart — idempotent by design (docs/plan/19 Task T3
			// step 4), no special handling needed since this row carries no
			// "last run" state of its own to update.
			accrued++
		} else {
			s.logger.Error("accrual: post failed, skipping", slog.String("account_id", cfg.AccountID.String()), slog.Any("error", postErr))
			skipped++
		}
	}
	return accrued, skipped
}

// DailyInterest implements the locked formula (docs/plan/19 Task T3 step
// 3): floor(balance * annual_rate_bps / 10000 / 365). A non-positive
// balance always yields zero — interest never accrues on a negative or
// empty balance.
func DailyInterest(balance decimal.Decimal, annualRateBps int) decimal.Decimal {
	if !balance.IsPositive() || annualRateBps <= 0 {
		return decimal.Zero
	}
	return balance.
		Mul(decimal.NewFromInt(int64(annualRateBps))).
		Div(decimal.NewFromInt(bpsDenominator)).
		Div(decimal.NewFromInt(daysPerYear)).
		Truncate(0) // floor — safe via Truncate since the operand is always non-negative here
}

func accrualIdempotencyKey(accountID uuid.UUID, asOf time.Time) string {
	return "accrue:" + accountID.String() + ":" + asOf.Format("2006-01-02")
}
