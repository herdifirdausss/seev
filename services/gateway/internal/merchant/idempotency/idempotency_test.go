package idempotency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

// fakeIdempotencyRepository is an in-memory stand-in for
// repository.IdempotencyRepository that reproduces the SAME atomicity
// guarantees as the real Postgres implementation (ON CONFLICT DO NOTHING /
// UPDATE ... WHERE state = ...), so concurrency tests against the fake
// exercise the exact same compare-and-swap semantics the Service depends
// on, guarded here by a mutex instead of a row lock.
type fakeIdempotencyRepository struct {
	mu      sync.Mutex
	records map[string]model.IdempotencyRecord // key: tenantID|operationID|idempotencyKey
	byID    map[uuid.UUID]string               // record ID -> records map key
}

func newFakeIdempotencyRepository() *fakeIdempotencyRepository {
	return &fakeIdempotencyRepository{
		records: map[string]model.IdempotencyRecord{},
		byID:    map[uuid.UUID]string{},
	}
}

func recKey(tenantID uuid.UUID, operationID, idempotencyKey string) string {
	return tenantID.String() + "|" + operationID + "|" + idempotencyKey
}

func (f *fakeIdempotencyRepository) Claim(_ context.Context, rec model.IdempotencyRecord) (bool, model.IdempotencyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := recKey(rec.TenantID, rec.OperationID, rec.IdempotencyKey)
	if existing, ok := f.records[key]; ok {
		return false, existing, nil
	}
	rec.State = "processing"
	f.records[key] = rec
	f.byID[rec.ID] = key
	return true, model.IdempotencyRecord{}, nil
}

func (f *fakeIdempotencyRepository) Complete(_ context.Context, tenantID, id uuid.UUID, httpStatus int, responseBody, responseHeaders []byte, resourceID *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.byID[id]
	if !ok {
		return errNotFound
	}
	rec := f.records[key]
	if rec.TenantID != tenantID {
		return errNotFound
	}
	rec.State = "completed"
	rec.HTTPStatus = &httpStatus
	rec.ResponseBody = responseBody
	rec.ResponseHeaders = responseHeaders
	rec.ResourceID = resourceID
	rec.LeaseOwner = nil
	rec.LeaseExpiresAt = nil
	f.records[key] = rec
	return nil
}

func (f *fakeIdempotencyRepository) Fail(_ context.Context, tenantID, id uuid.UUID, errorCode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.byID[id]
	if !ok {
		return errNotFound
	}
	rec := f.records[key]
	if rec.TenantID != tenantID {
		return errNotFound
	}
	rec.State = "failed"
	rec.ErrorCode = &errorCode
	rec.LeaseOwner = nil
	rec.LeaseExpiresAt = nil
	f.records[key] = rec
	return nil
}

func (f *fakeIdempotencyRepository) GetByKey(_ context.Context, tenantID uuid.UUID, operationID, idempotencyKey string) (model.IdempotencyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[recKey(tenantID, operationID, idempotencyKey)]
	if !ok {
		return model.IdempotencyRecord{}, errNotFound
	}
	return rec, nil
}

func (f *fakeIdempotencyRepository) TakeoverExpiredLease(_ context.Context, tenantID, id uuid.UUID, newLeaseOwner string, newLeaseExpiresAt time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.byID[id]
	if !ok {
		return false, nil
	}
	rec := f.records[key]
	if rec.TenantID != tenantID || rec.State != "processing" {
		return false, nil
	}
	if rec.LeaseExpiresAt == nil || !rec.LeaseExpiresAt.Before(time.Now()) {
		return false, nil
	}
	rec.LeaseOwner = &newLeaseOwner
	rec.LeaseExpiresAt = &newLeaseExpiresAt
	f.records[key] = rec
	return true, nil
}

func (f *fakeIdempotencyRepository) ReclaimFailed(_ context.Context, tenantID, id uuid.UUID, newLeaseOwner string, newLeaseExpiresAt time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.byID[id]
	if !ok {
		return false, nil
	}
	rec := f.records[key]
	if rec.TenantID != tenantID || rec.State != "failed" {
		return false, nil
	}
	rec.State = "processing"
	rec.LeaseOwner = &newLeaseOwner
	rec.LeaseExpiresAt = &newLeaseExpiresAt
	rec.ErrorCode = nil
	f.records[key] = rec
	return true, nil
}

func (f *fakeIdempotencyRepository) StateCounts(context.Context) (map[string]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int{}
	for _, rec := range f.records {
		counts[rec.State]++
	}
	return counts, nil
}

func (f *fakeIdempotencyRepository) CountStuckLeases(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	now := time.Now()
	for _, rec := range f.records {
		if rec.State == "processing" && rec.LeaseExpiresAt != nil && rec.LeaseExpiresAt.Before(now) {
			count++
		}
	}
	return count, nil
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "merchant: not found" }

var errNotFound = notFoundErr{}

func newTestService(repo *fakeIdempotencyRepository) *Service {
	return NewService(repo, time.Hour, "test-owner")
}

func TestService_Begin_NewClaim(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()

	decision, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", []byte(`{"amount":100}`))
	require.NoError(t, err)
	assert.Equal(t, OutcomeNew, decision.Outcome)
	assert.NotEmpty(t, decision.DownstreamKey)
}

func TestService_Begin_ReplayAfterCompleted(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()
	body := []byte(`{"amount":100}`)

	first, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	require.Equal(t, OutcomeNew, first.Outcome)
	require.NoError(t, svc.Complete(context.Background(), tenantID, first.RecordID, 201, []byte(`{"id":"tx_1"}`), nil, nil))

	second, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	assert.Equal(t, OutcomeReplay, second.Outcome)
	assert.Equal(t, first.RecordID, second.RecordID)
	assert.Equal(t, first.DownstreamKey, second.DownstreamKey, "a replay must reuse the exact same downstream key as the original attempt")
	assert.Equal(t, "completed", second.Existing.State)
}

func TestService_Begin_ConflictOnDifferentBody(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()

	_, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", []byte(`{"amount":100}`))
	require.NoError(t, err)

	decision, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", []byte(`{"amount":999}`))
	require.NoError(t, err)
	assert.Equal(t, OutcomeConflict, decision.Outcome, "same key with a different body hash must be IDEMPOTENCY_KEY_REUSED, never silently proceed")
}

func TestService_Begin_InProgressWhileLeaseStillValid(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()
	body := []byte(`{"amount":100}`)

	_, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)

	decision, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	assert.Equal(t, OutcomeInProgress, decision.Outcome, "a still-processing record with a live lease must not be re-claimed")
}

func TestService_Begin_TakesOverExpiredLease(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()
	body := []byte(`{"amount":100}`)

	first, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)

	// Simulate the original claimant crashing: force the lease into the past.
	repo.mu.Lock()
	key := recKey(tenantID, "op.transfer", "key-1")
	rec := repo.records[key]
	expired := time.Now().Add(-time.Minute)
	rec.LeaseExpiresAt = &expired
	repo.records[key] = rec
	repo.mu.Unlock()

	second, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	assert.Equal(t, OutcomeNew, second.Outcome, "an expired lease on a still-processing record must be reclaimable")
	assert.Equal(t, first.RecordID, second.RecordID)
}

func TestService_Begin_RetryAfterFailed(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()
	body := []byte(`{"amount":100}`)

	first, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	require.NoError(t, svc.Fail(context.Background(), tenantID, first.RecordID, "DOWNSTREAM_TIMEOUT"))

	second, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	assert.Equal(t, OutcomeNew, second.Outcome, "a failed record must be retryable with the same key+body")
	assert.Equal(t, first.RecordID, second.RecordID)
	assert.Equal(t, first.DownstreamKey, second.DownstreamKey)
}

// TestService_Begin_ConcurrentRetryOfFailedRecord proves the exact race
// this task found and fixed via self-review: without ReclaimFailed's
// atomic compare-and-swap, two concurrent retries of a "failed" record
// would BOTH observe Outcome=New and both re-run the downstream operation.
// Exactly one of N concurrent Begin calls against the same failed record
// must win OutcomeNew; every other must be OutcomeInProgress.
func TestService_Begin_ConcurrentRetryOfFailedRecord(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()
	body := []byte(`{"amount":100}`)

	first, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
	require.NoError(t, err)
	require.NoError(t, svc.Fail(context.Background(), tenantID, first.RecordID, "DOWNSTREAM_TIMEOUT"))

	const n = 20
	var newCount, inProgressCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			decision, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
			require.NoError(t, err)
			switch decision.Outcome {
			case OutcomeNew:
				newCount.Add(1)
			case OutcomeInProgress:
				inProgressCount.Add(1)
			default:
				t.Errorf("unexpected outcome %d for a concurrent retry of a failed record", decision.Outcome)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), newCount.Load(), "exactly one concurrent retry of a failed record must win OutcomeNew — a second winner means the downstream operation could run twice")
	assert.Equal(t, int32(n-1), inProgressCount.Load())
}

// TestService_Begin_ConcurrentNewClaims_OneWinner proves the same
// exactly-once guarantee for the plain "no prior record" race (N
// concurrent first-ever attempts for the same key).
func TestService_Begin_ConcurrentNewClaims_OneWinner(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantID := uuid.New()
	body := []byte(`{"amount":100}`)

	const n = 20
	var newCount, inProgressCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			decision, err := svc.Begin(context.Background(), tenantID, "op.transfer", "key-1", body)
			require.NoError(t, err)
			switch decision.Outcome {
			case OutcomeNew:
				newCount.Add(1)
			case OutcomeInProgress:
				inProgressCount.Add(1)
			default:
				t.Errorf("unexpected outcome %d for a concurrent first claim", decision.Outcome)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), newCount.Load())
	assert.Equal(t, int32(n-1), inProgressCount.Load())
}

func TestService_Begin_DifferentTenantsDoNotCollide(t *testing.T) {
	repo := newFakeIdempotencyRepository()
	svc := newTestService(repo)
	tenantA, tenantB := uuid.New(), uuid.New()
	body := []byte(`{"amount":100}`)

	decisionA, err := svc.Begin(context.Background(), tenantA, "op.transfer", "shared-key", body)
	require.NoError(t, err)
	assert.Equal(t, OutcomeNew, decisionA.Outcome)

	decisionB, err := svc.Begin(context.Background(), tenantB, "op.transfer", "shared-key", body)
	require.NoError(t, err)
	assert.Equal(t, OutcomeNew, decisionB.Outcome, "two tenants using the identical idempotency key must never collide")
	assert.NotEqual(t, decisionA.DownstreamKey, decisionB.DownstreamKey, "downstream keys must be tenant-scoped")
}

func TestCanonicalRequestHash_DifferentOperationIDs_ProduceDifferentHashes(t *testing.T) {
	body := []byte(`{"amount":100}`)
	transferHash := CanonicalRequestHash("op.transfer", body)
	refundHash := CanonicalRequestHash("op.refund", body)
	assert.NotEqual(t, transferHash, refundHash)
}

func TestDownstreamKey_StableAcrossCalls(t *testing.T) {
	tenantID := uuid.New()
	firstDownstreamKey := DownstreamKey(tenantID, "op.transfer", "key-1")
	secondDownstreamKey := DownstreamKey(tenantID, "op.transfer", "key-1")
	assert.Equal(t, firstDownstreamKey, secondDownstreamKey)
}
