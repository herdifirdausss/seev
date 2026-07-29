package webhook

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
)

// fakeWebhookRepository is an in-memory stand-in for
// repository.WebhookRepository — matches internal/merchant/idempotency's
// own established hand-written-fake convention for this codebase (no
// gomock in internal/merchant). It reproduces the real implementation's
// key invariants that this package's tests actually depend on: ClaimDue's
// lease-based exclusivity, CreateDelivery's (endpoint_id, event_id) dedup
// for the automatic path, and CreateReplayDelivery's exemption from it.
type fakeWebhookRepository struct {
	mu sync.Mutex

	endpoints map[uuid.UUID]model.WebhookEndpoint
	events    map[uuid.UUID]model.WebhookEvent
	eventBySrc map[string]uuid.UUID // tenantID|sourceEventID|eventType -> event id
	deliveries map[uuid.UUID]model.WebhookDelivery
	attempts   []model.WebhookAttempt

	automaticKeys map[string]bool // endpointID|eventID -> exists (only for replay_of_delivery_id IS NULL rows)
}

func newFakeWebhookRepository() *fakeWebhookRepository {
	return &fakeWebhookRepository{
		endpoints:     map[uuid.UUID]model.WebhookEndpoint{},
		events:        map[uuid.UUID]model.WebhookEvent{},
		eventBySrc:    map[string]uuid.UUID{},
		deliveries:    map[uuid.UUID]model.WebhookDelivery{},
		automaticKeys: map[string]bool{},
	}
}

var _ repository.WebhookRepository = (*fakeWebhookRepository)(nil)

func (f *fakeWebhookRepository) CreateEndpoint(_ context.Context, e model.WebhookEndpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.CreatedAt = time.Now()
	e.UpdatedAt = e.CreatedAt
	f.endpoints[e.ID] = e
	return nil
}

func (f *fakeWebhookRepository) GetEndpoint(_ context.Context, tenantID, endpointID uuid.UUID) (model.WebhookEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[endpointID]
	if !ok || e.TenantID != tenantID {
		return model.WebhookEndpoint{}, repository.ErrNotFound
	}
	return e, nil
}

func (f *fakeWebhookRepository) ListEndpoints(_ context.Context, tenantID uuid.UUID) ([]model.WebhookEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.WebhookEndpoint
	for _, e := range f.endpoints {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeWebhookRepository) UpdateEndpoint(_ context.Context, e model.WebhookEndpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.endpoints[e.ID]
	if !ok || existing.TenantID != e.TenantID {
		return repository.ErrNotFound
	}
	e.CreatedAt = existing.CreatedAt
	e.UpdatedAt = time.Now()
	f.endpoints[e.ID] = e
	return nil
}

func (f *fakeWebhookRepository) DeleteEndpoint(_ context.Context, tenantID, endpointID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[endpointID]
	if !ok || e.TenantID != tenantID {
		return repository.ErrNotFound
	}
	delete(f.endpoints, endpointID)
	return nil
}

func (f *fakeWebhookRepository) DisableEndpoint(_ context.Context, endpointID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[endpointID]
	if !ok {
		return repository.ErrNotFound
	}
	e.Status = "disabled"
	now := time.Now()
	e.DisabledAt = &now
	f.endpoints[endpointID] = e
	return nil
}

func (f *fakeWebhookRepository) CreateEvent(_ context.Context, e model.WebhookEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.CreatedAt = time.Now()
	f.events[e.ID] = e
	f.eventBySrc[srcKey(e.TenantID, e.SourceEventID, e.EventType)] = e.ID
	return nil
}

func (f *fakeWebhookRepository) GetEventBySource(_ context.Context, tenantID, sourceEventID uuid.UUID, eventType string) (model.WebhookEvent, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.eventBySrc[srcKey(tenantID, sourceEventID, eventType)]
	if !ok {
		return model.WebhookEvent{}, false, nil
	}
	return f.events[id], true, nil
}

func (f *fakeWebhookRepository) GetEventByID(_ context.Context, eventID uuid.UUID) (model.WebhookEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.events[eventID]
	if !ok {
		return model.WebhookEvent{}, repository.ErrNotFound
	}
	return e, nil
}

func srcKey(tenantID, sourceEventID uuid.UUID, eventType string) string {
	return tenantID.String() + "|" + sourceEventID.String() + "|" + eventType
}

func (f *fakeWebhookRepository) CreateDelivery(_ context.Context, d model.WebhookDelivery) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := d.EndpointID.String() + "|" + d.EventID.String()
	if f.automaticKeys[key] {
		return false, nil
	}
	f.automaticKeys[key] = true
	d.CreatedAt = time.Now()
	d.UpdatedAt = d.CreatedAt
	f.deliveries[d.ID] = d
	return true, nil
}

func (f *fakeWebhookRepository) CreateReplayDelivery(_ context.Context, d model.WebhookDelivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d.ReplayOfDeliveryID == nil {
		panic("fakeWebhookRepository: CreateReplayDelivery requires ReplayOfDeliveryID")
	}
	d.CreatedAt = time.Now()
	d.UpdatedAt = d.CreatedAt
	f.deliveries[d.ID] = d
	return nil
}

func (f *fakeWebhookRepository) GetDelivery(_ context.Context, tenantID, deliveryID uuid.UUID) (model.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deliveries[deliveryID]
	if !ok || d.TenantID != tenantID {
		return model.WebhookDelivery{}, repository.ErrNotFound
	}
	return d, nil
}

func (f *fakeWebhookRepository) ListDeliveries(_ context.Context, tenantID uuid.UUID, limit int) ([]model.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.WebhookDelivery
	for _, d := range f.deliveries {
		if d.TenantID == tenantID {
			out = append(out, d)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeWebhookRepository) ListDue(_ context.Context, limit int) ([]model.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.WebhookDelivery
	now := time.Now()
	for _, d := range f.deliveries {
		if (d.Status == "pending" || d.Status == "failed") && (d.NextAttemptAt == nil || d.NextAttemptAt.Before(now)) {
			out = append(out, d)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeWebhookRepository) ClaimDue(_ context.Context, limit int, leaseOwner string, leaseExpiresAt time.Time) ([]model.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	var out []model.WebhookDelivery
	for id, d := range f.deliveries {
		if d.Status != "pending" && d.Status != "failed" {
			continue
		}
		if d.NextAttemptAt != nil && d.NextAttemptAt.After(now) {
			continue
		}
		if d.LeaseOwner != nil && d.LeaseExpiresAt != nil && d.LeaseExpiresAt.After(now) {
			continue
		}
		owner := leaseOwner
		expires := leaseExpiresAt
		d.LeaseOwner = &owner
		d.LeaseExpiresAt = &expires
		f.deliveries[id] = d
		out = append(out, d)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeWebhookRepository) MarkDelivered(_ context.Context, deliveryID uuid.UUID, httpStatus int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deliveries[deliveryID]
	if !ok {
		return repository.ErrNotFound
	}
	d.Status = "delivered"
	d.LastHTTPStatus = &httpStatus
	now := time.Now()
	d.DeliveredAt = &now
	d.AttemptCount++
	d.LeaseOwner, d.LeaseExpiresAt = nil, nil
	f.deliveries[deliveryID] = d
	return nil
}

func (f *fakeWebhookRepository) MarkFailedAttempt(_ context.Context, deliveryID uuid.UUID, errorCode string, httpStatus *int, nextAttemptAt any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deliveries[deliveryID]
	if !ok {
		return repository.ErrNotFound
	}
	d.Status = "failed"
	d.LastErrorCode = &errorCode
	d.LastHTTPStatus = httpStatus
	if t, ok := nextAttemptAt.(time.Time); ok {
		d.NextAttemptAt = &t
	}
	if d.FirstAttemptAt == nil {
		now := time.Now()
		d.FirstAttemptAt = &now
	}
	d.AttemptCount++
	d.LeaseOwner, d.LeaseExpiresAt = nil, nil
	f.deliveries[deliveryID] = d
	return nil
}

func (f *fakeWebhookRepository) MarkDead(_ context.Context, deliveryID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deliveries[deliveryID]
	if !ok {
		return repository.ErrNotFound
	}
	d.Status = "dead"
	now := time.Now()
	d.DeadAt = &now
	d.LeaseOwner, d.LeaseExpiresAt = nil, nil
	f.deliveries[deliveryID] = d
	return nil
}

func (f *fakeWebhookRepository) RecordAttempt(_ context.Context, a model.WebhookAttempt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, a)
	return nil
}

func (f *fakeWebhookRepository) BacklogStats(_ context.Context) (map[string]int, *time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int{}
	var oldest *time.Time
	for _, d := range f.deliveries {
		counts[d.Status]++
		if d.Status != "pending" && d.Status != "failed" {
			continue
		}
		if oldest == nil || d.CreatedAt.Before(*oldest) {
			createdAt := d.CreatedAt
			oldest = &createdAt
		}
	}
	return counts, oldest, nil
}

// fakeTenantRepository is a minimal in-memory stand-in for
// repository.TenantRepository — this package's tests only ever need
// GetByID (consumer.go's own livemode lookup).
type fakeTenantRepository struct {
	mu      sync.Mutex
	tenants map[uuid.UUID]model.Tenant
}

func newFakeTenantRepository() *fakeTenantRepository {
	return &fakeTenantRepository{tenants: map[uuid.UUID]model.Tenant{}}
}

var _ repository.TenantRepository = (*fakeTenantRepository)(nil)

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

func (f *fakeTenantRepository) GetByPublicID(_ context.Context, publicID string) (model.Tenant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tenants {
		if t.PublicID == publicID {
			return t, nil
		}
	}
	return model.Tenant{}, repository.ErrNotFound
}

func (f *fakeTenantRepository) UpdateStatus(_ context.Context, id uuid.UUID, status, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tenants[id]
	if !ok {
		return repository.ErrNotFound
	}
	t.Status = status
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
