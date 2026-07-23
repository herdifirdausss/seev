package rules

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/fraud/model"
)

type repoStub struct{ events []model.ScreeningEvent }

func (r *repoStub) InsertEvent(_ context.Context, event model.ScreeningEvent) error {
	r.events = append(r.events, event)
	return nil
}
func (r *repoStub) ListEvents(context.Context, string, string, int, int) ([]model.ScreeningEvent, error) {
	return r.events, nil
}

type counterStub struct {
	value int64
	err   error
}

type modeResolverStub struct{ mode Mode }

func (r *modeResolverStub) ResolveMode(context.Context, string) (Mode, error) { return r.mode, nil }

type sanctionsMatcherStub struct {
	matched bool
	name    string
	birth   string
}

func (m *sanctionsMatcherStub) MatchSanctions(_ context.Context, name, birth string) (bool, error) {
	m.name, m.birth = name, birth
	return m.matched, nil
}

func (c counterStub) Get(context.Context, string) (int64, error) { return c.value, c.err }

func input(amount string) model.ScreenInput {
	return model.ScreenInput{TxType: "transfer_p2p", UserID: uuid.New(), Amount: decimal.RequireFromString(amount), Currency: "IDR"}
}

func TestAmountThresholdModes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  Mode
		block bool
	}{
		{name: "monitor flags", mode: ModeMonitor},
		{name: "block blocks", mode: ModeBlock, block: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repoStub{}
			rule := NewAmountThresholdRule(decimal.NewFromInt(100), tc.mode, repo, nil)
			verdict, err := rule.Screen(context.Background(), input("100"))
			require.NoError(t, err)
			assert.Equal(t, tc.block, verdict.Block)
			require.NotNil(t, verdict.Event)
			assert.Equal(t, "IDR", verdict.Event.Currency)
		})
	}
}

func TestAmountThresholdBelowDoesNothing(t *testing.T) {
	repo := &repoStub{}
	verdict, err := NewAmountThresholdRule(decimal.NewFromInt(100), ModeBlock, repo, nil).Screen(context.Background(), input("99"))
	require.NoError(t, err)
	assert.False(t, verdict.Block)
	assert.Empty(t, repo.events)
}

func TestAmountThresholdDynamicModeOffIsNoop(t *testing.T) {
	repo := &repoStub{}
	resolver := &modeResolverStub{mode: ModeOff}
	rule := NewAmountThresholdRuleWithResolver(decimal.NewFromInt(100), ModeBlock, resolver, repo, nil)
	verdict, err := rule.Screen(context.Background(), input("100"))
	require.NoError(t, err)
	assert.False(t, verdict.Block)
	assert.Empty(t, repo.events)

	resolver.mode = ModeBlock
	verdict, err = rule.Screen(context.Background(), input("100"))
	require.NoError(t, err)
	assert.True(t, verdict.Block)
	assert.NotNil(t, verdict.Event)
}

func TestVelocityReadsPostedCounterWithoutIncrementing(t *testing.T) {
	repo := &repoStub{}
	rule := NewVelocityAnomalyRule(2, ModeBlock, counterStub{value: 3}, repo, nil)
	rule.now = func() time.Time { return time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC) }
	verdict, err := rule.Screen(context.Background(), input("10"))
	require.NoError(t, err)
	assert.True(t, verdict.Block)
	require.NotNil(t, verdict.Event)
	assert.Equal(t, "fraud:velocity:"+verdict.Event.UserID.String()+":2026-07-15-10", VelocityKey(verdict.Event.UserID.String(), rule.now()))
}

func TestVelocityCounterError(t *testing.T) {
	verdict, err := NewVelocityAnomalyRule(2, ModeBlock, counterStub{err: errors.New("redis down")}, &repoStub{}, nil).Screen(context.Background(), input("10"))
	require.Error(t, err)
	assert.False(t, verdict.Block)
}

func TestSanctionsRuleModeAndSubject(t *testing.T) {
	matcher := &sanctionsMatcherStub{matched: true}
	resolver := &modeResolverStub{mode: ModeMonitor}
	rule := NewSanctionsWatchlistRule(ModeOff, resolver, matcher, nil)
	verdict, err := rule.Screen(context.Background(), model.ScreenInput{SubjectName: "Doe, Jane", BirthDate: "1980-01-02"})
	require.NoError(t, err)
	assert.False(t, verdict.Block)
	require.NotNil(t, verdict.Event)
	assert.Equal(t, "doe jane", matcher.name)

	resolver.mode = ModeBlock
	verdict, err = rule.Screen(context.Background(), model.ScreenInput{SubjectName: "Doe, Jane"})
	require.NoError(t, err)
	assert.True(t, verdict.Block)
}

func TestParseModeDefaultsOff(t *testing.T) {
	assert.Equal(t, ModeOff, ParseMode(""))
	assert.Equal(t, ModeOff, ParseMode("unknown"))
	assert.Equal(t, ModeMonitor, ParseMode("monitor"))
	assert.Equal(t, ModeBlock, ParseMode("block"))
}
