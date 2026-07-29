package lifecycle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// fakeLifecycleRepository is an in-memory stand-in for
// repository.LifecycleRepository — matches this codebase's established
// hand-written-fake convention for internal/merchant subpackages (no
// gomock). Reproduces the real implementation's key invariant this
// package's tests actually depend on: Create's dedup on (tenant, action)
// while status='pending'.
type fakeLifecycleRepository struct {
	mu       sync.Mutex
	requests map[uuid.UUID]model.TenantLifecycleRequest
	pending  map[string]uuid.UUID // tenantID|action -> request id, only while pending
}

func newFakeLifecycleRepository() *fakeLifecycleRepository {
	return &fakeLifecycleRepository{requests: map[uuid.UUID]model.TenantLifecycleRequest{}, pending: map[string]uuid.UUID{}}
}

func pendingKey(tenantID uuid.UUID, action string) string { return tenantID.String() + "|" + action }

func (f *fakeLifecycleRepository) Create(_ context.Context, req model.TenantLifecycleRequest) (bool, model.TenantLifecycleRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := pendingKey(req.TenantID, req.Action)
	if existingID, ok := f.pending[key]; ok {
		return false, f.requests[existingID], nil
	}
	req.Status = "pending"
	req.CreatedAt = time.Now()
	f.requests[req.ID] = req
	f.pending[key] = req.ID
	return true, model.TenantLifecycleRequest{}, nil
}

func (f *fakeLifecycleRepository) GetByID(_ context.Context, id uuid.UUID) (model.TenantLifecycleRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.requests[id]
	if !ok {
		return model.TenantLifecycleRequest{}, repository.ErrNotFound
	}
	return req, nil
}

func (f *fakeLifecycleRepository) GetPending(_ context.Context, tenantID uuid.UUID, action string) (model.TenantLifecycleRequest, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.pending[pendingKey(tenantID, action)]
	if !ok {
		return model.TenantLifecycleRequest{}, false, nil
	}
	return f.requests[id], true, nil
}

func (f *fakeLifecycleRepository) List(_ context.Context, tenantID uuid.UUID, status string, limit int) ([]model.TenantLifecycleRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.TenantLifecycleRequest
	for _, req := range f.requests {
		if req.TenantID != tenantID {
			continue
		}
		if status != "" && req.Status != status {
			continue
		}
		out = append(out, req)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeLifecycleRepository) Decide(_ context.Context, id uuid.UUID, status, approvedBy string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.requests[id]
	if !ok || req.Status != "pending" {
		return false, nil
	}
	req.Status = status
	req.ApprovedBy = approvedBy
	now := time.Now()
	req.DecidedAt = &now
	f.requests[id] = req
	delete(f.pending, pendingKey(req.TenantID, req.Action))
	return true, nil
}

// fakeTenantRepository is a minimal in-memory stand-in for
// repository.TenantRepository — this package's tests only need
// GetByID/UpdateStatus.
type fakeTenantRepository struct {
	mu      sync.Mutex
	tenants map[uuid.UUID]model.Tenant
}

func newFakeTenantRepository() *fakeTenantRepository {
	return &fakeTenantRepository{tenants: map[uuid.UUID]model.Tenant{}}
}

func (f *fakeTenantRepository) Create(_ context.Context, t model.Tenant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants[t.ID] = t
	return nil
}

func (f *fakeTenantRepository) GetByID(_ context.Context, id uuid.UUID) (model.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return model.Tenant{}, repository.ErrNotFound
	}
	return t, nil
}

func (f *fakeTenantRepository) GetByPublicID(context.Context, string) (model.Tenant, error) {
	return model.Tenant{}, repository.ErrNotFound
}

func (f *fakeTenantRepository) UpdateStatus(_ context.Context, id uuid.UUID, status, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return repository.ErrNotFound
	}
	t.Status = status
	switch status {
	case "active":
		t.ActivatedBy = &actor
	case "suspended":
		t.SuspendedBy = &actor
	}
	f.tenants[id] = t
	return nil
}

func (f *fakeTenantRepository) SetPrimaryAccount(_ context.Context, id uuid.UUID, accountID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return repository.ErrNotFound
	}
	t.PrimaryAccountID = &accountID
	f.tenants[id] = t
	return nil
}

func seedTenant(t *testing.T, tenants *fakeTenantRepository, status string) uuid.UUID {
	t.Helper()
	id := generalutil.NewV7()
	require.NoError(t, tenants.Create(context.Background(), model.Tenant{ID: id, Status: status, Environment: "live"}))
	return id
}

func TestService_Propose_RejectsInvalidAction(t *testing.T) {
	svc := NewService(newFakeLifecycleRepository(), newFakeTenantRepository())
	_, err := svc.Propose(context.Background(), generalutil.NewV7(), "explode", "maker@x.com", "reason")
	assert.ErrorIs(t, err, ErrInvalidAction)
}

func TestService_Propose_IdempotentOnDuplicatePending(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "draft")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	first, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live")
	require.NoError(t, err)
	second, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live again")
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "a duplicate propose for the same pending (tenant, action) must return the existing request, not create a second one")
}

func TestService_Approve_RejectsSelfApproval(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "draft")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	req, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live")
	require.NoError(t, err)

	_, err = svc.Approve(context.Background(), req.ID, "maker@x.com")
	assert.ErrorIs(t, err, ErrSelfApproval)
}

func TestService_Approve_AppliesTransitionOnSuccess(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "draft")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	req, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live")
	require.NoError(t, err)

	approved, err := svc.Approve(context.Background(), req.ID, "checker@x.com")
	require.NoError(t, err)
	assert.Equal(t, "approved", approved.Status)

	tenant, err := tenants.GetByID(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "active", tenant.Status, "approving an 'activate' lifecycle request must flip the tenant to active")
	require.NotNil(t, tenant.ActivatedBy)
	assert.Equal(t, "checker@x.com", *tenant.ActivatedBy)
}

func TestService_Approve_CloseAction(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "active")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	req, err := svc.Propose(context.Background(), tenantID, ActionClose, "maker@x.com", "fraud")
	require.NoError(t, err)
	_, err = svc.Approve(context.Background(), req.ID, "checker@x.com")
	require.NoError(t, err)

	tenant, err := tenants.GetByID(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "closed", tenant.Status)
}

func TestService_Approve_AlreadyDecidedRejected(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "draft")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	req, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live")
	require.NoError(t, err)
	_, err = svc.Approve(context.Background(), req.ID, "checker@x.com")
	require.NoError(t, err)

	_, err = svc.Approve(context.Background(), req.ID, "checker2@x.com")
	assert.ErrorIs(t, err, ErrAlreadyDecided)
}

func TestService_Reject_RejectsSelfApproval(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "draft")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	req, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live")
	require.NoError(t, err)

	_, err = svc.Reject(context.Background(), req.ID, "maker@x.com")
	assert.ErrorIs(t, err, ErrSelfApproval)
}

func TestService_Reject_LeavesTenantUnchanged(t *testing.T) {
	tenants := newFakeTenantRepository()
	tenantID := seedTenant(t, tenants, "draft")
	svc := NewService(newFakeLifecycleRepository(), tenants)

	req, err := svc.Propose(context.Background(), tenantID, ActionActivate, "maker@x.com", "go live")
	require.NoError(t, err)

	rejected, err := svc.Reject(context.Background(), req.ID, "checker@x.com")
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)

	tenant, err := tenants.GetByID(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, "draft", tenant.Status, "a rejected proposal must not change the tenant's status")
}

func TestNewService_PanicsOnNilDeps(t *testing.T) {
	repo := newFakeLifecycleRepository()
	tenants := newFakeTenantRepository()
	assert.Panics(t, func() { NewService(nil, tenants) })
	assert.Panics(t, func() { NewService(repo, nil) })
}
