package fraud

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/fraud/rules"
)

type modeRepoFake struct {
	modes map[string]string
	sets  int
}

func (r *modeRepoFake) GetRuleMode(_ context.Context, rule string) (string, string, time.Time, bool, error) {
	value, ok := r.modes[rule]
	return value, "operator-1", time.Unix(10, 0), ok, nil
}

func (r *modeRepoFake) SetRuleMode(_ context.Context, rule, mode, _ string) error {
	r.modes[rule] = mode
	r.sets++
	return nil
}

func TestRuleModeResolverCachesAndSetInvalidates(t *testing.T) {
	repo := &modeRepoFake{modes: map[string]string{"amount_threshold": "monitor"}}
	m := &Module{modeRepo: repo}
	m.modeResolver = newRuleModeResolver(repo, rules.ModeOff, nil)

	mode, _, _, err := m.GetRuleMode(context.Background(), "amount_threshold")
	require.NoError(t, err)
	require.Equal(t, rules.ModeMonitor, mode)
	require.NoError(t, m.SetRuleMode(context.Background(), "amount_threshold", rules.ModeBlock, "operator-1"))
	mode, _, _, err = m.GetRuleMode(context.Background(), "amount_threshold")
	require.NoError(t, err)
	require.Equal(t, rules.ModeBlock, mode)
	require.Equal(t, 1, repo.sets)
}

func TestSetRuleModeRejectsUnknownRuleAndMode(t *testing.T) {
	repo := &modeRepoFake{modes: map[string]string{}}
	m := &Module{modeRepo: repo, modeResolver: newRuleModeResolver(repo, rules.ModeOff, nil)}
	require.ErrorIs(t, m.SetRuleMode(context.Background(), "unknown", rules.ModeBlock, "operator-1"), ErrInvalidRuleMode)
	require.ErrorIs(t, m.SetRuleMode(context.Background(), "amount_threshold", rules.Mode("bad"), "operator-1"), ErrInvalidRuleMode)
}
