package fraud

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/herdifirdausss/seev/internal/fraud/repository"
	"github.com/herdifirdausss/seev/internal/fraud/rules"
)

const ruleModeCacheTTL = 10 * time.Second

var supportedRules = map[string]bool{
	"amount_threshold":    true,
	"velocity_anomaly":    true,
	"sanctions_watchlist": true,
}

type cachedRuleMode struct {
	mode      rules.Mode
	expiresAt time.Time
}

type ruleModeResolver struct {
	repo     repository.RuleModeRepository
	fallback rules.Mode
	logger   *slog.Logger
	mu       sync.Mutex
	cache    map[string]cachedRuleMode
}

func newRuleModeResolver(repo repository.RuleModeRepository, fallback rules.Mode, logger *slog.Logger) *ruleModeResolver {
	return &ruleModeResolver{repo: repo, fallback: fallback, logger: logger, cache: make(map[string]cachedRuleMode)}
}

func (r *ruleModeResolver) ResolveMode(ctx context.Context, rule string) (rules.Mode, error) {
	now := time.Now()
	r.mu.Lock()
	if cached, ok := r.cache[rule]; ok && now.Before(cached.expiresAt) {
		r.mu.Unlock()
		return cached.mode, nil
	}
	r.mu.Unlock()
	if r.repo == nil {
		return r.fallback, nil
	}
	value, _, _, found, err := r.repo.GetRuleMode(ctx, rule)
	if err != nil {
		r.logger.Warn("fraud: rule mode lookup failed, using env fallback", slog.String("rule", rule), slog.Any("error", err))
		return r.fallback, err
	}
	mode := r.fallback
	if found {
		mode = rules.ParseMode(value)
	}
	r.mu.Lock()
	r.cache[rule] = cachedRuleMode{mode: mode, expiresAt: now.Add(ruleModeCacheTTL)}
	r.mu.Unlock()
	return mode, nil
}

func (r *ruleModeResolver) invalidate(rule string, mode rules.Mode) {
	r.mu.Lock()
	r.cache[rule] = cachedRuleMode{mode: mode, expiresAt: time.Now().Add(ruleModeCacheTTL)}
	r.mu.Unlock()
}

func validateRuleMode(rule string, mode rules.Mode) error {
	if !supportedRules[rule] || (mode != rules.ModeOff && mode != rules.ModeMonitor && mode != rules.ModeBlock) {
		return fmt.Errorf("%w: rule=%q mode=%q", ErrInvalidRuleMode, rule, mode)
	}
	return nil
}

func (m *Module) GetRuleMode(ctx context.Context, rule string) (rules.Mode, string, time.Time, error) {
	if !supportedRules[rule] {
		return rules.ModeOff, "", time.Time{}, ErrInvalidRuleMode
	}
	if m.modeRepo == nil {
		return m.modeResolver.fallback, "env", time.Time{}, nil
	}
	mode, updatedBy, updatedAt, found, err := m.modeRepo.GetRuleMode(ctx, rule)
	if err != nil {
		return rules.ModeOff, "", time.Time{}, err
	}
	if !found {
		return m.modeResolver.fallback, "env", time.Time{}, nil
	}
	return rules.ParseMode(mode), updatedBy, updatedAt, nil
}

func (m *Module) SetRuleMode(ctx context.Context, rule string, mode rules.Mode, updatedBy string) error {
	if err := validateRuleMode(rule, mode); err != nil {
		return err
	}
	if updatedBy == "" {
		return fmt.Errorf("%w: updated_by is required", ErrInvalidRuleMode)
	}
	if m.modeRepo == nil {
		return fmt.Errorf("fraud: rule mode repository unavailable")
	}
	if err := m.modeRepo.SetRuleMode(ctx, rule, string(mode), updatedBy); err != nil {
		return err
	}
	m.modeResolver.invalidate(rule, mode)
	return nil
}
