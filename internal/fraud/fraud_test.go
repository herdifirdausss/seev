package fraud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/fraud/model"
	fraudrules "github.com/herdifirdausss/seev/internal/fraud/rules"
)

type ruleStub struct {
	called  bool
	verdict model.Verdict
}

func (r *ruleStub) Name() string { return "stub" }
func (r *ruleStub) Screen(context.Context, model.ScreenInput) (model.Verdict, error) {
	r.called = true
	return r.verdict, nil
}

type eventRepoStub struct {
	events []model.ScreeningEvent
	err    error
}

func (r *eventRepoStub) InsertEvent(_ context.Context, event model.ScreeningEvent) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, event)
	return nil
}

func (r *eventRepoStub) ListEvents(context.Context, string, string, int, int) ([]model.ScreeningEvent, error) {
	return r.events, nil
}

func TestScreenMonitorFindingDoesNotSkipLaterRules(t *testing.T) {
	first := &ruleStub{verdict: model.Verdict{Reason: "flagged"}}
	second := &ruleStub{}
	module := &Module{rules: []fraudrules.Rule{first, second}}
	verdict, err := module.Screen(context.Background(), ScreenInput{})
	require.NoError(t, err)
	assert.Equal(t, "flagged", verdict.Reason)
	assert.True(t, second.called)
}

func TestScreenPersistsRuleEventCentrally(t *testing.T) {
	repo := &eventRepoStub{}
	event := model.ScreeningEvent{Rule: "amount_threshold", Verdict: "blocked"}
	m := &Module{repo: repo, rules: []fraudrules.Rule{&ruleStub{verdict: model.Verdict{Block: true, Event: &event}}}, spill: newEventSpill()}
	verdict, err := m.Screen(context.Background(), ScreenInput{})
	require.NoError(t, err)
	require.True(t, verdict.Block)
	require.Len(t, repo.events, 1)
	assert.Equal(t, "amount_threshold", repo.events[0].Rule)
}

func TestScreenSpillsWriteFailureAndFlushesAfterRecovery(t *testing.T) {
	repo := &eventRepoStub{err: errors.New("postgres down")}
	event := model.ScreeningEvent{Rule: "amount_threshold", Verdict: "flagged"}
	m := &Module{repo: repo, logger: slog.Default(), rules: []fraudrules.Rule{&ruleStub{verdict: model.Verdict{Reason: "flagged", Event: &event}}}, spill: newEventSpill()}
	verdict, err := m.Screen(context.Background(), ScreenInput{})
	require.NoError(t, err)
	require.False(t, verdict.Block)
	assert.Equal(t, 1, m.spill.depth())

	repo.err = nil
	m.flushSpill(context.Background())
	assert.Zero(t, m.spill.depth())
	assert.Len(t, repo.events, 1)
}

func TestEventSpillDropsOldestOnOverflow(t *testing.T) {
	spill := newEventSpill()
	for i := 0; i < maxSpillEvents+1; i++ {
		spill.enqueue(model.ScreeningEvent{Reason: fmt.Sprintf("%d", i)})
	}
	assert.Equal(t, maxSpillEvents, spill.depth())
	assert.Equal(t, uint64(1), spill.lostCount())
	first, ok := spill.peek()
	require.True(t, ok)
	assert.Equal(t, "1", first.Reason)
}
