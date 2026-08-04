package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/platform/money/currency"
	ledgererror "github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
)

var (
	policyDecisionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "money_movement_policy_decisions_total",
		Help:      "Money-movement policy decisions by source, decision, and reason.",
	}, []string{"source", "decision", "reason"})
	executionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ledger",
		Name:      "money_movement_executions_total",
		Help:      "Money-movement execution outcomes by source and transaction type.",
	}, []string{"source", "type", "outcome"})
)

// Poster is deliberately the only low-level capability the executor needs.
// The concrete handle service is kept behind this interface and is wired only
// by the ledger composition root.
type Poster interface {
	Handle(context.Context, processors.Command) error
}

// Runner is the context-aware form of Poster. Services that retain the
// legacy Poster seam for test doubles call Run below; the composition root's
// real executor always satisfies Runner and therefore receives the source,
// actor, correlation, and execution-time policy context.
type Runner interface {
	Execute(context.Context, processors.Command, ExecutionContext) error
}

// Run keeps compatibility with older test doubles while making the
// production executor the default for every money-moving service. A fallback
// is intentionally limited to composition/test seams that only implement the
// legacy Poster interface.
func Run(ctx context.Context, poster Poster, cmd processors.Command, exec ExecutionContext) error {
	if runner, ok := poster.(Runner); ok {
		return runner.Execute(ctx, cmd, exec)
	}
	return poster.Handle(ctx, cmd)
}

// PolicyChecker is the policy contract used at execution time. The optional
// currency extension is detected structurally so older policy implementations
// remain source-compatible.
type PolicyChecker interface {
	Check(context.Context, uuid.UUID, string, decimal.Decimal) (bool, string, string, error)
	Record(context.Context, uuid.UUID, string, decimal.Decimal)
}

type currencyPolicyChecker interface {
	CheckWithCurrency(context.Context, uuid.UUID, string, decimal.Decimal, string) (bool, string, string, error)
	RecordWithCurrency(context.Context, uuid.UUID, string, decimal.Decimal, string)
}

// SubjectState is the minimum current state needed to authorize a queued
// command. It intentionally lives at the execution boundary rather than in a
// scheduler row, because status and KYC can change after scheduling.
type SubjectState = model.ExecutionSubject

// SubjectStateReader is implemented by the ledger projection of auth state.
type SubjectStateReader interface {
	GetExecutionSubject(context.Context, uuid.UUID, uuid.UUID) (model.ExecutionSubject, error)
}

// SubjectAuthorizer performs execution-time account, tenant, and KYC checks.
// A missing or stale state is an error: queued money must not become a bypass
// when an authorization dependency is unavailable.
type SubjectAuthorizer struct {
	Reader SubjectStateReader
	MinKYC int
}

func (a SubjectAuthorizer) Authorize(ctx context.Context, cmd processors.Command, exec ExecutionContext) (string, error) {
	if a.Reader == nil {
		return "subject_state_unavailable", errors.New("execution subject reader is not configured")
	}
	state, err := a.Reader.GetExecutionSubject(ctx, cmd.UserID, exec.TenantID)
	if err != nil {
		return "subject state unavailable", fmt.Errorf("execution subject lookup: %w", err)
	}
	if state.Status != "" && state.Status != "active" {
		return "subject_disabled", fmt.Errorf("subject status %q is not active", state.Status)
	}
	if state.TenantStatus != "" && state.TenantStatus != "active" {
		return "tenant_disabled", fmt.Errorf("tenant status %q is not active", state.TenantStatus)
	}
	minKYC := a.MinKYC
	if minKYC == 0 {
		minKYC = 1
	}
	if state.KYCLevel < minKYC {
		return "kyc_required", fmt.Errorf("KYC level %d is below required level %d", state.KYCLevel, minKYC)
	}
	if state.KYCVerifiedUntil != nil && !state.KYCVerifiedUntil.After(exec.EffectiveTime) {
		return "kyc_expired", fmt.Errorf("KYC verification expired at %s", state.KYCVerifiedUntil.UTC().Format(time.RFC3339))
	}
	return "", nil
}

type subjectAuthorizer interface {
	Authorize(context.Context, processors.Command, ExecutionContext) (string, error)
}

// PolicyDecisionSink persists an immutable decision before money is posted.
// A sink error fails closed because an un-audited money movement is not
// acceptable for the shared execution path.
type PolicyDecisionSink interface {
	RecordPolicyDecision(context.Context, model.PolicyDecision) error
}

// Executor is the sole command execution boundary. Handle implements the
// legacy Poster shape for internal service constructors; new entry points use
// Execute so they can provide a source and full execution context.
type Executor struct {
	poster     Poster
	policy     PolicyChecker
	authorizer subjectAuthorizer
	audit      PolicyDecisionSink
	logger     *slog.Logger
}

func NewExecutor(poster Poster, policy PolicyChecker, authorizer subjectAuthorizer, audit PolicyDecisionSink, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{poster: poster, policy: policy, authorizer: authorizer, audit: audit, logger: logger}
}

// Handle is the compatibility adapter for trusted internal workers. It still
// enters the executor, but deliberately identifies the source as an internal
// service so end-user velocity policy is not applied to a system accrual or a
// maker-checker service that has already performed its own authorization.
func (e *Executor) Handle(ctx context.Context, cmd processors.Command) error {
	return e.Execute(ctx, cmd, ExecutionContext{Source: "internal-service", RequestOrigin: "internal-service"})
}

func (e *Executor) Execute(ctx context.Context, cmd processors.Command, exec ExecutionContext) error {
	if e == nil || e.poster == nil {
		return errors.New("money-movement executor is not configured")
	}
	exec = exec.normalized(cmd.UserID)
	if exec.Currency == "" {
		exec.Currency = cmd.Currency
	}
	// Public and queued end-user money movement must never run without the
	// immutable policy-decision sink. A constructor mistake must stop the
	// command before posting rather than silently losing the audit record.
	if subjectGateApplies(exec.Source) && e.audit == nil {
		return errors.New("money-movement policy audit sink is not configured")
	}
	if err := currency.ValidatePositiveMinorAmount(cmd.Amount); err != nil {
		if auditErr := e.recordDecision(ctx, cmd, exec, false, "amount_invalid", err.Error()); auditErr != nil {
			return auditErr
		}
		executionTotal.WithLabelValues(exec.Source, cmd.Type, "rejected").Inc()
		return err
	}

	if reason, err := e.authorizeSubject(ctx, cmd, exec); err != nil {
		if auditErr := e.recordDecision(ctx, cmd, exec, false, reason, err.Error()); auditErr != nil {
			return auditErr
		}
		executionTotal.WithLabelValues(exec.Source, cmd.Type, "rejected").Inc()
		return err
	}

	if e.policy != nil && policyApplies(exec.Source) {
		allowed, reason, detail, err := e.checkPolicy(ctx, cmd, exec)
		if err != nil {
			if auditErr := e.recordDecision(ctx, cmd, exec, false, "policy_error", err.Error()); auditErr != nil {
				return auditErr
			}
			executionTotal.WithLabelValues(exec.Source, cmd.Type, "error").Inc()
			return fmt.Errorf("money-movement policy check: %w", err)
		}
		if !allowed {
			if auditErr := e.recordDecision(ctx, cmd, exec, false, reason, detail); auditErr != nil {
				return auditErr
			}
			executionTotal.WithLabelValues(exec.Source, cmd.Type, "rejected").Inc()
			return ledgererror.NewBizErr(ledgererror.ErrPolicyLimitExceeded,
				fmt.Sprintf("policy limit exceeded (%s): %s", reason, detail))
		}
		if auditErr := e.recordDecision(ctx, cmd, exec, true, "", ""); auditErr != nil {
			return auditErr
		}
	} else {
		// Internal services still get an audit record when a sink is configured;
		// this makes bypasses visible without applying end-user quota rules.
		if auditErr := e.recordDecision(ctx, cmd, exec, true, "policy_not_applicable", ""); auditErr != nil {
			return auditErr
		}
	}

	if err := e.poster.Handle(ctx, cmd); err != nil {
		outcome := "error"
		if isBusinessError(err) {
			outcome = "rejected"
		}
		executionTotal.WithLabelValues(exec.Source, cmd.Type, outcome).Inc()
		return err
	}
	if e.policy != nil && policyApplies(exec.Source) {
		e.recordPolicyUsage(ctx, cmd, exec)
	}
	executionTotal.WithLabelValues(exec.Source, cmd.Type, "posted").Inc()
	return nil
}

func (e *Executor) authorizeSubject(ctx context.Context, cmd processors.Command, exec ExecutionContext) (string, error) {
	// System suspense/correction commands may intentionally have no end-user
	// subject. They still pass policy/audit and low-level ledger invariants,
	// but must not attempt to look up uuid.Nil as an account.
	if cmd.UserID == uuid.Nil || !subjectGateApplies(exec.Source) {
		return "", nil
	}
	if e.authorizer == nil {
		return "subject_authorizer_unavailable", errors.New("money-movement subject authorizer is not configured")
	}
	return e.authorizer.Authorize(ctx, cmd, exec)
}

func (e *Executor) checkPolicy(ctx context.Context, cmd processors.Command, exec ExecutionContext) (bool, string, string, error) {
	if checker, ok := e.policy.(currencyPolicyChecker); ok && exec.Currency != "" {
		return checker.CheckWithCurrency(ctx, cmd.UserID, cmd.Type, cmd.Amount, exec.Currency)
	}
	return e.policy.Check(ctx, cmd.UserID, cmd.Type, cmd.Amount)
}

func (e *Executor) recordPolicyUsage(ctx context.Context, cmd processors.Command, exec ExecutionContext) {
	if recorder, ok := e.policy.(currencyPolicyChecker); ok && exec.Currency != "" {
		recorder.RecordWithCurrency(ctx, cmd.UserID, cmd.Type, cmd.Amount, exec.Currency)
		return
	}
	e.policy.Record(ctx, cmd.UserID, cmd.Type, cmd.Amount)
}

func (e *Executor) recordDecision(ctx context.Context, cmd processors.Command, exec ExecutionContext, allowed bool, reason, detail string) error {
	decision := "denied"
	if allowed {
		decision = "allowed"
	}
	policyDecisionsTotal.WithLabelValues(exec.Source, decision, normalizeReason(reason)).Inc()
	if e.audit == nil {
		return nil
	}
	record := model.PolicyDecision{
		ID: uuid.New(), ActorID: exec.ActorID, TenantID: exec.TenantID, UserID: cmd.UserID,
		Source: exec.Source, CorrelationID: exec.CorrelationID, RequestOrigin: exec.RequestOrigin,
		TransactionType: cmd.Type, Currency: exec.Currency, AmountMinor: decimalToInt64(cmd.Amount),
		Allowed: allowed, Reason: reason, Detail: detail, EffectiveAt: exec.EffectiveTime,
	}
	if err := e.audit.RecordPolicyDecision(ctx, record); err != nil {
		e.logger.Error("money-movement policy audit failed", "error", err, "source", exec.Source, "type", cmd.Type)
		return fmt.Errorf("money-movement policy audit: %w", err)
	}
	return nil
}

func decimalToInt64(amount decimal.Decimal) int64 {
	if !amount.Equal(amount.Truncate(0)) || !amount.BigInt().IsInt64() {
		return 0
	}
	return amount.IntPart()
}

func normalizeReason(reason string) string {
	if reason == "" {
		return "none"
	}
	reason = strings.ToLower(strings.TrimSpace(reason))
	if len(reason) > 64 {
		reason = reason[:64]
	}
	return reason
}

func policyApplies(source string) bool {
	source = strings.ToLower(source)
	return source != "internal-service" && source != "internal-worker" && source != "system"
}

func subjectGateApplies(source string) bool {
	// Only the explicitly trusted system execution sources may omit the
	// end-user subject projection. New sources must be gated by default; an
	// allowlist of gated names would turn a newly added public route into an
	// authorization bypass until someone remembered to update this switch.
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "internal-service", "internal-worker", "system":
		return false
	default:
		return true
	}
}

func isBusinessError(err error) bool {
	if err == nil {
		return false
	}
	// Keep the boundary package independent of the ledger error package. The
	// concrete transport still classifies the original error for clients.
	return strings.Contains(strings.ToLower(err.Error()), "invalid") ||
		strings.Contains(strings.ToLower(err.Error()), "insufficient") ||
		strings.Contains(strings.ToLower(err.Error()), "denied") ||
		strings.Contains(strings.ToLower(err.Error()), "expired") ||
		strings.Contains(strings.ToLower(err.Error()), "not active")
}
