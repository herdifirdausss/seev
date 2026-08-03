package rules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/internal/fraud/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type AmountThresholdRule struct {
	threshold             decimal.Decimal
	thresholdsByCurrency  map[string]decimal.Decimal
	mode                  Mode
	resolver              ModeResolver
	repo                  repository.ScreeningRepository
	logger                *slog.Logger
}

func NewAmountThresholdRuleWithResolver(threshold decimal.Decimal, fallback Mode, resolver ModeResolver, repo repository.ScreeningRepository, logger *slog.Logger) *AmountThresholdRule {
	return NewAmountThresholdRuleWithCurrencyThresholds(threshold, nil, fallback, resolver, repo, logger)
}

func NewAmountThresholdRule(threshold decimal.Decimal, mode Mode, repo repository.ScreeningRepository, logger *slog.Logger) *AmountThresholdRule {
	return NewAmountThresholdRuleWithCurrencyThresholds(threshold, nil, mode, nil, repo, logger)
}

// NewAmountThresholdRuleWithCurrencyThresholds keeps the legacy global
// threshold as a fallback while allowing each currency to have an independent
// minor-unit threshold. The map is copied so configuration cannot change a
// live rule unexpectedly.
func NewAmountThresholdRuleWithCurrencyThresholds(threshold decimal.Decimal, thresholds map[string]decimal.Decimal, mode Mode, resolver ModeResolver, repo repository.ScreeningRepository, logger *slog.Logger) *AmountThresholdRule {
	if logger == nil {
		logger = slog.Default()
	}
	copyThresholds := make(map[string]decimal.Decimal, len(thresholds))
	for code, value := range thresholds {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code != "" && value.IsPositive() {
			copyThresholds[code] = value
		}
	}
	return &AmountThresholdRule{threshold: threshold, thresholdsByCurrency: copyThresholds, mode: mode, resolver: resolver, repo: repo, logger: logger}
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
	threshold := r.thresholdForCurrency(input.Currency)
	if !threshold.IsPositive() || input.Amount.LessThan(threshold) {
		return model.Verdict{}, nil
	}
	reason := fmt.Sprintf("amount %s >= threshold %s", input.Amount, threshold)
	return r.record(ctx, input, reason, mode)
}

func (r *AmountThresholdRule) thresholdForCurrency(code string) decimal.Decimal {
	if threshold, ok := r.thresholdsByCurrency[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return threshold
	}
	return r.threshold
}

func (r *AmountThresholdRule) record(ctx context.Context, input model.ScreenInput, reason string, mode Mode) (model.Verdict, error) {
	verdict := model.Verdict{Reason: reason}
	eventVerdict := "flagged"
	if mode == ModeBlock {
		verdict.Block = true
		eventVerdict = "blocked"
	}
	screeningTotal.WithLabelValues(r.Name(), eventVerdict).Inc()
	event := model.ScreeningEvent{
		ID: generalutil.NewV7(), TxType: input.TxType, UserID: input.UserID,
		Amount: input.Amount, Currency: input.Currency, Rule: r.Name(),
		Verdict: eventVerdict, Reason: reason,
		RequestID: input.RequestID, Flow: input.Flow,
	}
	verdict.Event = &event
	return verdict, nil
}
