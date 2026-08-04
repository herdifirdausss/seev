package feepolicy

import (
	"context"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// Approval exposes fee configuration as a maker-checker workflow. A draft or
// submitted version is never visible to quote resolution; approval atomically
// updates the active projection after the database overlap/actor constraints
// pass.
type Approval struct {
	repo repository.FeeRuleApprovalRepository
}

func NewApproval(repo repository.FeeRuleApprovalRepository) *Approval { return &Approval{repo: repo} }

func (a *Approval) CreateDraft(ctx context.Context, rule Rule, actor, reason string) (model.FeeRuleVersion, error) {
	return a.repo.CreateFeeRuleDraft(ctx, rule, actor, reason)
}

func (a *Approval) Submit(ctx context.Context, id uuid.UUID, actor string) (model.FeeRuleVersion, error) {
	return a.repo.SubmitFeeRule(ctx, id, actor)
}

func (a *Approval) Approve(ctx context.Context, id uuid.UUID, actor, reason string) (model.FeeRuleVersion, error) {
	return a.repo.ApproveFeeRule(ctx, id, actor, reason)
}

func (a *Approval) Reject(ctx context.Context, id uuid.UUID, actor, reason string) (model.FeeRuleVersion, error) {
	return a.repo.RejectFeeRule(ctx, id, actor, reason)
}

func (a *Approval) ListVersions(ctx context.Context, ruleID uuid.UUID) ([]model.FeeRuleVersion, error) {
	return a.repo.ListFeeRuleVersions(ctx, ruleID)
}
