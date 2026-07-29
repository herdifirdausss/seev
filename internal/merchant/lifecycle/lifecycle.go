// Package lifecycle is Plan 57 T8's maker-checker gate on sensitive
// merchant tenant status transitions (docs/roadmap/active/57-c1-merchant-b2b-api.md
// §16.3: "live-mode activation: checker", "tenant closure: checker") —
// mirrors internal/auth's own operator-offboarding propose/approve/reject
// shape, generalized to two Action kinds instead of one hardcoded
// operation.
package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

const (
	ActionActivate = "activate"
	ActionClose    = "close"
)

// targetStatus maps a lifecycle Action to the tenant status Approve
// applies — the only two transitions this package gates.
var targetStatus = map[string]string{
	ActionActivate: "active",
	ActionClose:    "closed",
}

var (
	ErrInvalidAction  = errors.New("merchant: invalid tenant lifecycle action")
	ErrNotFound       = errors.New("merchant: tenant lifecycle request not found")
	ErrSelfApproval   = errors.New("merchant: cannot approve or reject your own tenant lifecycle proposal")
	ErrAlreadyDecided = errors.New("merchant: tenant lifecycle request was already decided")
)

// Service gates Propose (maker) / Approve|Reject (checker) around
// TenantRepository.UpdateStatus — the actual status flip already exists
// (T2/T3); this package only adds the two-person gate in front of it.
type Service struct {
	repo    repository.LifecycleRepository
	tenants repository.TenantRepository
}

func NewService(repo repository.LifecycleRepository, tenants repository.TenantRepository) *Service {
	if repo == nil {
		panic("merchant/lifecycle: NewService requires a non-nil LifecycleRepository")
	}
	if tenants == nil {
		panic("merchant/lifecycle: NewService requires a non-nil TenantRepository")
	}
	return &Service{repo: repo, tenants: tenants}
}

// Propose is the "maker" half — any operator may propose activating or
// closing a tenant; the two-person gate is enforced at Approve, not here.
// Idempotent: an existing pending proposal for this (tenant, action) is
// returned instead of erroring (Create's own ON CONFLICT DO NOTHING
// convention, matching internal/auth.ProposeOperatorOffboarding's
// duplicate-as-success behavior).
func (s *Service) Propose(ctx context.Context, tenantID uuid.UUID, action, requestedBy, reason string) (model.TenantLifecycleRequest, error) {
	if _, ok := targetStatus[action]; !ok {
		return model.TenantLifecycleRequest{}, ErrInvalidAction
	}
	if requestedBy == "" {
		return model.TenantLifecycleRequest{}, fmt.Errorf("merchant/lifecycle: requested_by (caller identity) is required")
	}
	if reason == "" {
		return model.TenantLifecycleRequest{}, fmt.Errorf("merchant/lifecycle: reason is required")
	}
	if _, err := s.tenants.GetByID(ctx, tenantID); err != nil {
		return model.TenantLifecycleRequest{}, err
	}

	req := model.TenantLifecycleRequest{
		ID: generalutil.NewV7(), TenantID: tenantID, Action: action, RequestedBy: requestedBy, Reason: reason,
	}
	created, existing, err := s.repo.Create(ctx, req)
	if err != nil {
		return model.TenantLifecycleRequest{}, err
	}
	if !created {
		return existing, nil
	}
	req.Status = "pending"
	return req, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (model.TenantLifecycleRequest, error) {
	req, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return model.TenantLifecycleRequest{}, ErrNotFound
	}
	return req, err
}

func (s *Service) List(ctx context.Context, tenantID uuid.UUID, status string, limit int) ([]model.TenantLifecycleRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.repo.List(ctx, tenantID, status, limit)
}

// Approve is the "checker" half. approvedBy must differ from the original
// requester — checked here for a clear typed error, AND enforced by the
// migration's own CHECK constraint as the backstop (same belt-and-braces
// discipline as ledger's adjustments.Service.Approve and
// internal/auth.ApproveOperatorOffboarding). On success, applies the
// gated transition via TenantRepository.UpdateStatus — the same method
// T2/T3's own operator flows already use for the ungated transitions
// (suspend has no checker gate per §16.3, so it still calls UpdateStatus
// directly, outside this package).
func (s *Service) Approve(ctx context.Context, id uuid.UUID, approvedBy string) (model.TenantLifecycleRequest, error) {
	req, err := s.Get(ctx, id)
	if err != nil {
		return model.TenantLifecycleRequest{}, err
	}
	if req.RequestedBy == approvedBy {
		return model.TenantLifecycleRequest{}, ErrSelfApproval
	}
	if req.Status != "pending" {
		return model.TenantLifecycleRequest{}, ErrAlreadyDecided
	}

	matched, err := s.repo.Decide(ctx, id, "approved", approvedBy)
	if err != nil {
		return model.TenantLifecycleRequest{}, err
	}
	if !matched {
		return model.TenantLifecycleRequest{}, ErrAlreadyDecided
	}

	if err := s.tenants.UpdateStatus(ctx, req.TenantID, targetStatus[req.Action], approvedBy); err != nil {
		return model.TenantLifecycleRequest{}, fmt.Errorf("merchant/lifecycle: apply approved transition: %w", err)
	}

	req.Status, req.ApprovedBy = "approved", approvedBy
	return req, nil
}

// Reject declines a pending proposal — no tenant status change.
// approvedBy must differ from the requester, same as Approve.
func (s *Service) Reject(ctx context.Context, id uuid.UUID, approvedBy string) (model.TenantLifecycleRequest, error) {
	req, err := s.Get(ctx, id)
	if err != nil {
		return model.TenantLifecycleRequest{}, err
	}
	if req.RequestedBy == approvedBy {
		return model.TenantLifecycleRequest{}, ErrSelfApproval
	}
	if req.Status != "pending" {
		return model.TenantLifecycleRequest{}, ErrAlreadyDecided
	}

	matched, err := s.repo.Decide(ctx, id, "rejected", approvedBy)
	if err != nil {
		return model.TenantLifecycleRequest{}, err
	}
	if !matched {
		return model.TenantLifecycleRequest{}, ErrAlreadyDecided
	}

	req.Status, req.ApprovedBy = "rejected", approvedBy
	return req, nil
}
