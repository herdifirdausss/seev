package rules

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/internal/fraud/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type Counter interface {
	Get(context.Context, string) (int64, error)
}

type VelocityAnomalyRule struct {
	maxPerHour int64
	mode       Mode
	resolver   ModeResolver
	counter    Counter
	repo       repository.ScreeningRepository
	logger     *slog.Logger
	now        func() time.Time
}

func NewVelocityAnomalyRuleWithResolver(maxPerHour int64, fallback Mode, resolver ModeResolver, counter Counter, repo repository.ScreeningRepository, logger *slog.Logger) *VelocityAnomalyRule {
	rule := NewVelocityAnomalyRule(maxPerHour, fallback, counter, repo, logger)
	rule.resolver = resolver
	return rule
}

func NewVelocityAnomalyRule(maxPerHour int64, mode Mode, counter Counter, repo repository.ScreeningRepository, logger *slog.Logger) *VelocityAnomalyRule {
	if logger == nil {
		logger = slog.Default()
	}
	return &VelocityAnomalyRule{maxPerHour: maxPerHour, mode: mode, counter: counter, repo: repo, logger: logger, now: time.Now}
}

func (r *VelocityAnomalyRule) Name() string { return "velocity_anomaly" }

func (r *VelocityAnomalyRule) Screen(ctx context.Context, input model.ScreenInput) (model.Verdict, error) {
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
	count, err := r.counter.Get(ctx, VelocityKey(input.UserID.String(), r.now()))
	if err != nil {
		return model.Verdict{}, fmt.Errorf("velocity counter: %w", err)
	}
	if count <= r.maxPerHour {
		return model.Verdict{}, nil
	}

	reason := fmt.Sprintf("%d postings this hour exceeds threshold %d", count, r.maxPerHour)
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

func VelocityKey(userID string, at time.Time) string {
	return fmt.Sprintf("fraud:velocity:%s:%s", userID, at.UTC().Format("2006-01-02-15"))
}
