package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
)

type fakePoster struct {
	calls int
	err   error
}

func (f *fakePoster) Handle(context.Context, processors.Command) error { f.calls++; return f.err }

type fakePolicy struct {
	checks, records int
	allowed         bool
	reason          string
}

func (f *fakePolicy) Check(context.Context, uuid.UUID, string, decimal.Decimal) (bool, string, string, error) {
	f.checks++
	return f.allowed, f.reason, "policy detail", nil
}
func (f *fakePolicy) Record(context.Context, uuid.UUID, string, decimal.Decimal) { f.records++ }

type fakeAudit struct {
	records []model.PolicyDecision
	err     error
}

func (f *fakeAudit) RecordPolicyDecision(_ context.Context, d model.PolicyDecision) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, d)
	return nil
}

type fakeSubjectReader struct {
	state SubjectState
	err   error
}

func (f fakeSubjectReader) GetExecutionSubject(context.Context, uuid.UUID, uuid.UUID) (SubjectState, error) {
	return f.state, f.err
}

func activeSubjectAuthorizer() SubjectAuthorizer {
	return SubjectAuthorizer{Reader: fakeSubjectReader{state: SubjectState{Status: "active", KYCLevel: 1}}}
}

func testCommand() processors.Command {
	return processors.Command{IdempotencyKey: "idem-123456", IdempotencyScope: "user", Type: "transfer_p2p", Amount: decimal.NewFromInt(100), UserID: uuid.New(), Currency: "IDR"}
}

func TestExecutorPolicyDecisionAndPostingAreOrdered(t *testing.T) {
	poster := &fakePoster{}
	policy := &fakePolicy{allowed: true}
	audit := &fakeAudit{}
	e := NewExecutor(poster, policy, activeSubjectAuthorizer(), audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "public-api", CorrelationID: "corr-1"})
	require.NoError(t, err)
	require.Equal(t, 1, policy.checks)
	require.Equal(t, 1, policy.records)
	require.Equal(t, 1, poster.calls)
	require.Len(t, audit.records, 1)
	require.True(t, audit.records[0].Allowed)
	require.Equal(t, "public-api", audit.records[0].Source)
}

func TestExecutorDenialDoesNotPostOrRecordUsage(t *testing.T) {
	poster := &fakePoster{}
	policy := &fakePolicy{reason: "max_daily_amount"}
	audit := &fakeAudit{}
	e := NewExecutor(poster, policy, activeSubjectAuthorizer(), audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "public-api"})
	require.Error(t, err)
	require.Equal(t, 0, poster.calls)
	require.Equal(t, 0, policy.records)
	require.Len(t, audit.records, 1)
	require.False(t, audit.records[0].Allowed)
}

func TestExecutorRejectsExpiredKYCBeforePosting(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute)
	poster := &fakePoster{}
	audit := &fakeAudit{}
	authorizer := SubjectAuthorizer{Reader: fakeSubjectReader{state: SubjectState{Status: "active", KYCLevel: 1, KYCVerifiedUntil: &past}}}
	e := NewExecutor(poster, nil, authorizer, audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "scheduler", EffectiveTime: time.Now().UTC()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
	require.Equal(t, 0, poster.calls)
	require.Equal(t, "kyc_expired", audit.records[0].Reason)
}

func TestExecutorAuditFailureStopsPosting(t *testing.T) {
	poster := &fakePoster{}
	audit := &fakeAudit{err: errors.New("db unavailable")}
	e := NewExecutor(poster, nil, activeSubjectAuthorizer(), audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "scheduler"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "policy audit")
	require.Equal(t, 0, poster.calls)
}

func TestSubjectAuthorizerWithoutReaderFailsClosed(t *testing.T) {
	reason, err := (SubjectAuthorizer{}).Authorize(
		context.Background(), testCommand(), ExecutionContext{Source: "public-api"},
	)
	require.Equal(t, "subject_state_unavailable", reason)
	require.Error(t, err)
	require.Contains(t, err.Error(), "reader is not configured")
}

func TestExecutorGatedSourceWithoutAuthorizerFailsClosed(t *testing.T) {
	poster := &fakePoster{}
	audit := &fakeAudit{}
	e := NewExecutor(poster, nil, nil, audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "public-api"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "subject authorizer")
	require.Equal(t, 0, poster.calls)
	require.Len(t, audit.records, 1)
	require.False(t, audit.records[0].Allowed)
	require.Equal(t, "subject_authorizer_unavailable", audit.records[0].Reason)
}

func TestExecutorUnrecognizedSourceFailsClosedWithoutAuthorizer(t *testing.T) {
	poster := &fakePoster{}
	audit := &fakeAudit{}
	e := NewExecutor(poster, nil, nil, audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "new-route"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "subject authorizer")
	require.Equal(t, 0, poster.calls)
}

func TestExecutorMissingSourceFailsClosedWithoutAuthorizer(t *testing.T) {
	poster := &fakePoster{}
	audit := &fakeAudit{}
	e := NewExecutor(poster, nil, nil, audit, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "subject authorizer")
	require.Equal(t, 0, poster.calls)
}

func TestExecutorGatedSourceWithoutAuditSinkFailsClosed(t *testing.T) {
	poster := &fakePoster{}
	e := NewExecutor(poster, nil, activeSubjectAuthorizer(), nil, nil)

	err := e.Execute(context.Background(), testCommand(), ExecutionContext{Source: "scheduler"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit sink")
	require.Equal(t, 0, poster.calls)
}
