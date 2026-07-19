package rules

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/internal/fraud/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type AmountThresholdRule struct {
	threshold decimal.Decimal
	mode      Mode
	resolver  ModeResolver
	repo      repository.ScreeningRepository
	logger    *slog.Logger
}

func NewAmountThresholdRuleWithResolver(threshold decimal.Decimal, fallback Mode, resolver ModeResolver, repo repository.ScreeningRepository, logger *slog.Logger) *AmountThresholdRule {
	rule := NewAmountThresholdRule(threshold, fallback, repo, logger)
	rule.resolver = resolver
	return rule
}

func NewAmountThresholdRule(threshold decimal.Decimal, mode Mode, repo repository.ScreeningRepository, logger *slog.Logger) *AmountThresholdRule {
	if logger == nil {
		logger = slog.Default()
	}
	return &AmountThresholdRule{threshold: threshold, mode: mode, repo: repo, logger: logger}
}

func (r *AmountThresholdRule) Name() string { return "amount_threshold" }

func (r *AmountThresholdRule) Screen(ctx context.Context, input model.ScreenInput) (model.Verdict, error) {
	mode := r.mode
	if r.resolver != nil {
		resolved, err := r.resolver.ResolveMode(ctx, r.Name())
		if err == nil {
			mode = resolved
		}
	}
	if mode == ModeOff {
		return model.Verdict{}, nil
	}
	if input.Amount.LessThan(r.threshold) {
		return model.Verdict{}, nil
	}
	reason := fmt.Sprintf("amount %s >= threshold %s", input.Amount, r.threshold)
	return r.record(ctx, input, reason, mode)
}

func (r *AmountThresholdRule) record(ctx context.Context, input model.ScreenInput, reason string, mode Mode) (model.Verdict, error) {
	verdict := model.Verdict{Reason: reason}
	eventVerdict := "flagged"
	if mode == ModeBlock {
		verdict.Block = true
		eventVerdict = "blocked"
	}
	screeningTotal.WithLabelValues(r.Name(), eventVerdict).Inc()
	if err := r.repo.InsertEvent(ctx, model.ScreeningEvent{
		ID: generalutil.NewV7(), TxType: input.TxType, UserID: input.UserID,
		Amount: input.Amount, Currency: input.Currency, Rule: r.Name(),
		Verdict: eventVerdict, Reason: reason,
		RequestID: input.RequestID, Flow: input.Flow,
	}); err != nil {
		r.logger.Error("fraud: persist screening event failed", "error", err, "rule", r.Name(), "user_id", input.UserID)
	}
	return verdict, nil
}
