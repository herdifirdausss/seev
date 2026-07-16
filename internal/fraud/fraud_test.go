package fraud

import (
	"context"
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

func TestScreenMonitorFindingDoesNotSkipLaterRules(t *testing.T) {
	first := &ruleStub{verdict: model.Verdict{Reason: "flagged"}}
	second := &ruleStub{}
	module := &Module{rules: []fraudrules.Rule{first, second}}
	verdict, err := module.Screen(context.Background(), ScreenInput{})
	require.NoError(t, err)
	assert.Equal(t, "flagged", verdict.Reason)
	assert.True(t, second.called)
}
