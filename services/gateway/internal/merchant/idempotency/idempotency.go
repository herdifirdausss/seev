package idempotency

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/repository"
)

// Outcome is what Begin decided about one (tenant, operation,
// idempotency-key, body) attempt.
type Outcome int

const (
	// OutcomeNew: no prior record — proceed with the operation, then call
	// Complete or Fail with the returned RecordID.
	OutcomeNew Outcome = iota
	// OutcomeReplay: an identical (same hash) request already completed —
	// return Decision.Existing's stored response, do not re-run the
	// operation (T4 acceptance: "Gateway crash after downstream success
	// does not duplicate money" — a replay after the ORIGINAL response was
	// already recorded takes this path).
	OutcomeReplay
	// OutcomeConflict: same key, different body hash — 409
	// IDEMPOTENCY_KEY_REUSED.
	OutcomeConflict
	// OutcomeInProgress: same key+body, another request is still actively
	// processing (lease not expired) — 409 IDEMPOTENCY_IN_PROGRESS.
	OutcomeInProgress
)

// Decision is Begin's result.
type Decision struct {
	Outcome       Outcome
	RecordID      uuid.UUID
	DownstreamKey string
	Existing      model.IdempotencyRecord // populated for OutcomeReplay
}

// leaseDuration bounds how long a claimed record may sit in 'processing'
// before another attempt (this package's own TakeoverExpiredLease-backed
// recovery, not a separate background job) may reclaim it — T4's "add
// recovery query for interrupted processing records."
const leaseDuration = 30 * time.Second

// Service implements T4's claim/lease/complete/replay/purge lifecycle on
// top of repository.IdempotencyRepository's persistence.
type Service struct {
	repo       repository.IdempotencyRepository
	defaultTTL time.Duration
	leaseOwner string
}

// NewService constructs a Service. leaseOwner should be a stable-enough
// process identifier (e.g. hostname) for observability — it does not gate
// correctness, since TakeoverExpiredLease's own WHERE clause is what
// actually enforces exclusivity.
func NewService(repo repository.IdempotencyRepository, defaultTTL time.Duration, leaseOwner string) *Service {
	if repo == nil {
		panic("merchant/idempotency: NewService requires a non-nil IdempotencyRepository")
	}
	if defaultTTL <= 0 {
		panic("merchant/idempotency: NewService requires a positive defaultTTL")
	}
	return &Service{repo: repo, defaultTTL: defaultTTL, leaseOwner: leaseOwner}
}

// Begin attempts to claim (tenantID, operationID, idempotencyKey) for a
// new attempt whose canonical hash is CanonicalRequestHash(operationID,
// body). See Outcome's own doc comments for what each result means.
func (s *Service) Begin(ctx context.Context, tenantID uuid.UUID, operationID, idempotencyKey string, body []byte) (Decision, error) {
	hash := CanonicalRequestHash(operationID, body)
	recordID := identifiers.NewV7()
	downstreamKey := DownstreamKey(tenantID, operationID, idempotencyKey)
	now := time.Now()

	claimed, existing, err := s.repo.Claim(ctx, model.IdempotencyRecord{
		ID: recordID, TenantID: tenantID, OperationID: operationID, IdempotencyKey: idempotencyKey,
		RequestHash: hash, DownstreamKey: downstreamKey,
		LeaseOwner: new(s.leaseOwner), LeaseExpiresAt: new(now.Add(leaseDuration)),
		ExpiresAt: now.Add(s.defaultTTL),
	})
	if err != nil {
		return Decision{}, fmt.Errorf("merchant/idempotency: begin: %w", err)
	}
	if claimed {
		return Decision{Outcome: OutcomeNew, RecordID: recordID, DownstreamKey: downstreamKey}, nil
	}

	if !bytes.Equal(existing.RequestHash, hash) {
		return Decision{Outcome: OutcomeConflict, RecordID: existing.ID, DownstreamKey: existing.DownstreamKey}, nil
	}

	switch existing.State {
	case "completed":
		return Decision{Outcome: OutcomeReplay, RecordID: existing.ID, DownstreamKey: existing.DownstreamKey, Existing: existing}, nil
	case "failed":
		// A failed attempt does not permanently burn the key — the caller
		// may reasonably try again with the same key+body. ReclaimFailed
		// is the SAME compare-and-swap shape as the "processing" branch
		// below: without it, two concurrent retries of a failed record
		// would both read Outcome=New and both re-run the operation
		// (found and fixed during this task's own concurrent-retry test).
		// The downstream key is reused unchanged (derived deterministically,
		// not stored-then-mutated) so the owner service still sees one
		// logical operation across every attempt.
		reclaimed, err := s.repo.ReclaimFailed(ctx, tenantID, existing.ID, s.leaseOwner, now.Add(leaseDuration))
		if err != nil {
			return Decision{}, fmt.Errorf("merchant/idempotency: begin: reclaim failed: %w", err)
		}
		if reclaimed {
			return Decision{Outcome: OutcomeNew, RecordID: existing.ID, DownstreamKey: existing.DownstreamKey}, nil
		}
		// Another concurrent retry already reclaimed it first — treat this
		// one as in-progress rather than double-running the operation.
		return Decision{Outcome: OutcomeInProgress, RecordID: existing.ID, DownstreamKey: existing.DownstreamKey}, nil
	default: // "processing"
		took, err := s.repo.TakeoverExpiredLease(ctx, tenantID, existing.ID, s.leaseOwner, now.Add(leaseDuration))
		if err != nil {
			return Decision{}, fmt.Errorf("merchant/idempotency: begin: takeover: %w", err)
		}
		if took {
			return Decision{Outcome: OutcomeNew, RecordID: existing.ID, DownstreamKey: existing.DownstreamKey}, nil
		}
		return Decision{Outcome: OutcomeInProgress, RecordID: existing.ID, DownstreamKey: existing.DownstreamKey}, nil
	}
}

// Complete records the operation's successful outcome (§6: an operation
// that failed with the FAILED case above must call Fail instead).
func (s *Service) Complete(ctx context.Context, tenantID, recordID uuid.UUID, httpStatus int, responseBody, responseHeaders []byte, resourceID *string) error {
	return s.repo.Complete(ctx, tenantID, recordID, httpStatus, responseBody, responseHeaders, resourceID)
}

// Fail records the operation's failure — the SAME idempotency key may be
// retried afterward (Begin's "failed" case above).
func (s *Service) Fail(ctx context.Context, tenantID, recordID uuid.UUID, errorCode string) error {
	return s.repo.Fail(ctx, tenantID, recordID, errorCode)
}
