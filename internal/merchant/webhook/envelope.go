package webhook

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope is the external webhook body every delivery carries — the
// LOCKED shape from docs/reference/c1-b2b-design.md §2. Field order is
// fixed by struct declaration order (Go's encoding/json preserves it),
// matching the design doc's own example byte-for-byte.
type Envelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Livemode  bool            `json:"livemode"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// transactionPostedExternalType is the ONE external event type T7
// currently produces (docs/reference/c1-b2b-design.md's WebhookEventType
// enum also lists payin.updated.v1/payout.updated.v1/
// webhook.endpoint.disabled.v1 — those require a payin/payout-owned
// pending-state outbox T6 explicitly deferred, out of scope here since no
// T7 acceptance criterion requires them).
const transactionPostedExternalType = "transaction.posted.v1"

// BuildTransactionPostedEnvelope constructs the immutable external
// envelope for one merchant-owned ledger.transaction.posted.v1 event.
// sourceEventID is the INTERNAL logical event id (never outbox_events.id
// — docs/reference/c1-current-contract-inventory.md §2's own distinction,
// the root cause of the notify-test regression fixed earlier this
// session) — evtPublicID ("evt_...") is what external subscribers see as
// "id", derived from it via a stable UUIDv5-style hash so redelivery of
// the SAME logical event always produces the SAME external id.
func BuildTransactionPostedEnvelope(evtPublicID string, livemode bool, occurredAt time.Time, data any) ([]byte, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("merchant/webhook: marshal envelope data: %w", err)
	}
	env := Envelope{
		ID: evtPublicID, Type: transactionPostedExternalType, Livemode: livemode,
		CreatedAt: occurredAt.UTC(), Data: dataJSON,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("merchant/webhook: marshal envelope: %w", err)
	}
	return body, nil
}

// externalEventPublicID formats the internal logical EventID (already
// itself a deterministic uuid.NewSHA1 hash — internal/ledger/events'
// logicalEventID) as the external "evt_..." id — no second hash needed;
// the internal id is ALREADY stable across redelivery of the same
// logical event (docs/reference/c1-b2b-design.md §2: "id is the external
// event ID — derived from (and stable across redelivery with) the same
// internal logical EventID convention").
func externalEventPublicID(logicalEventID uuid.UUID) string {
	return "evt_" + logicalEventID.String()
}
