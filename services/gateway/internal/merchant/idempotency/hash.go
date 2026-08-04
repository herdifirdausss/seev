// Package idempotency is services/gateway/internal/merchant's durable idempotent-write
// enforcement (docs/roadmap/archive/57-c1-merchant-b2b-api.md §3.1, T4) —
// canonical request hashing, deterministic downstream key derivation, and
// the claim/lease/complete/replay lifecycle on top of
// services/gateway/internal/merchant/repository.IdempotencyRepository's persistence.
package idempotency

import (
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
)

// CanonicalRequestHash hashes exactly the request shape that must match
// for a retry to be considered "the same request": the operation ID (an
// operation's method+path are already fixed by its OpenAPI operationId,
// so hashing those separately would be redundant) and the raw request
// body bytes. Two requests with the same Idempotency-Key but a different
// hash are IDEMPOTENCY_KEY_REUSED (§6.7) — the caller reused a key for a
// genuinely different request, which this package never silently allows.
func CanonicalRequestHash(operationID string, body []byte) []byte {
	h := sha256.New()
	h.Write([]byte(operationID))
	h.Write([]byte{0})
	h.Write(body)
	return h.Sum(nil)
}

// DownstreamKey derives a stable idempotency key to send to the owner
// service (Ledger/Payin/Payout) for one (tenant, operation, merchant key)
// triple — every retry of the SAME merchant request must produce the
// EXACT same downstream key, so the owner service's own idempotency guard
// (not this one) is what ultimately prevents a duplicate financial
// operation if this layer's own record is ever lost or bypassed. Derived
// deterministically (SHA-256, not random) specifically so it needs no
// storage of its own beyond merchant_idempotency_records.downstream_key.
func DownstreamKey(tenantID uuid.UUID, operationID, idempotencyKey string) string {
	h := sha256.New()
	h.Write([]byte("b2b"))
	h.Write([]byte{0})
	h.Write([]byte(tenantID.String()))
	h.Write([]byte{0})
	h.Write([]byte(operationID))
	h.Write([]byte{0})
	h.Write([]byte(idempotencyKey))
	return fmt.Sprintf("b2b_%x", h.Sum(nil))
}
