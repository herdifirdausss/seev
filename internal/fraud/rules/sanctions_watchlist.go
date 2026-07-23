package rules

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	"github.com/herdifirdausss/seev/internal/fraud/sanctions"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

type SanctionsMatcher interface {
	MatchSanctions(context.Context, string, string) (bool, error)
}

type SanctionsWatchlistRule struct {
	mode     Mode
	resolver ModeResolver
	matcher  SanctionsMatcher
	logger   *slog.Logger
}

func NewSanctionsWatchlistRule(fallback Mode, resolver ModeResolver, matcher SanctionsMatcher, logger *slog.Logger) *SanctionsWatchlistRule {
	if logger == nil {
		logger = slog.Default()
	}
	return &SanctionsWatchlistRule{mode: fallback, resolver: resolver, matcher: matcher, logger: logger}
}

func (r *SanctionsWatchlistRule) Name() string { return "sanctions_watchlist" }

func (r *SanctionsWatchlistRule) Screen(ctx context.Context, input model.ScreenInput) (model.Verdict, error) {
	if input.SubjectName == "" || r.matcher == nil {
		return model.Verdict{}, nil
	}
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
	matched, err := r.matcher.MatchSanctions(ctx, sanctions.NormalizeName(input.SubjectName), input.BirthDate)
	if err != nil {
		return model.Verdict{}, fmt.Errorf("sanctions matcher: %w", err)
	}
	if !matched {
		return model.Verdict{}, nil
	}
	reason := "subject matched sanctions watchlist"
	verdict := model.Verdict{Reason: reason}
	eventVerdict := "flagged"
	if mode == ModeBlock {
		verdict.Block = true
		eventVerdict = "blocked"
	}
	screeningTotal.WithLabelValues(r.Name(), eventVerdict).Inc()
	event := model.ScreeningEvent{ID: generalutil.NewV7(), TxType: input.TxType, UserID: input.UserID,
		Amount: input.Amount, Currency: input.Currency, Rule: r.Name(), Verdict: eventVerdict,
		Reason: reason, RequestID: input.RequestID, Flow: input.Flow}
	verdict.Event = &event
	return verdict, nil
}
