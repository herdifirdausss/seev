package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/idempotency"
	"github.com/herdifirdausss/seev/pkg/response"
)

// idempotencyInProgressRetrySeconds bounds OutcomeInProgress's Retry-After
// (§6.7/§10.4: "409 IDEMPOTENCY_IN_PROGRESS ... with a bounded Retry-After")
// well under the idempotency package's own lease duration, so a
// well-behaved client's retry has a real chance of landing after the
// in-flight attempt either completes or its lease expires.
const idempotencyInProgressRetrySeconds = 2

// beginIdempotentWrite runs T4's claim/lease/replay decision for one
// financial POST. proceed=true means the caller must go on to invoke the
// owner service and finish with completeIdempotentWrite or
// failIdempotentWrite; every other outcome has already written the
// complete HTTP response by the time this returns.
func beginIdempotentWrite(w http.ResponseWriter, r *http.Request, svc *idempotency.Service, tenantID uuid.UUID, operationID string, body []byte) (decision idempotency.Decision, proceed bool) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		response.ErrorStatus(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
		return idempotency.Decision{}, false
	}

	decision, err := svc.Begin(r.Context(), tenantID, operationID, idempotencyKey, body)
	if err != nil {
		response.InternalServerError(w, err)
		return idempotency.Decision{}, false
	}

	switch decision.Outcome {
	case idempotency.OutcomeNew:
		return decision, true
	case idempotency.OutcomeReplay:
		writeReplay(w, decision)
		return decision, false
	case idempotency.OutcomeConflict:
		response.ErrorStatus(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "the idempotency key was already used with a different request")
		return decision, false
	default: // idempotency.OutcomeInProgress
		w.Header().Set("Retry-After", strconv.Itoa(idempotencyInProgressRetrySeconds))
		response.ErrorStatus(w, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", "a request with this idempotency key is already being processed")
		return decision, false
	}
}

func writeReplay(w http.ResponseWriter, decision idempotency.Decision) {
	httpStatus := http.StatusOK
	if decision.Existing.HTTPStatus != nil {
		httpStatus = *decision.Existing.HTTPStatus
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(decision.Existing.ResponseBody)
}

// completeIdempotentWrite marshals data as the standard success envelope,
// persists it as the idempotency record's stored response (so a future
// replay reproduces these EXACT bytes — §10.3), and writes the real HTTP
// response from the SAME marshaled bytes, never a second independent
// marshal that could drift from what was stored.
func completeIdempotentWrite(w http.ResponseWriter, r *http.Request, svc *idempotency.Service, tenantID, recordID uuid.UUID, httpStatus int, data any, resourceID string) {
	body, err := json.Marshal(response.Envelope{Success: true, Data: data})
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	if completeErr := svc.Complete(r.Context(), tenantID, recordID, httpStatus, body, []byte("{}"), &resourceID); completeErr != nil {
		// The owner-service call already succeeded and the resource
		// exists — failing to persist the idempotency record must not
		// turn into a failure response for a request that actually
		// worked. A retry that misses this record falls back to the
		// owner service's OWN downstream-key dedup (§10.4) rather than
		// this record, so no financial safety is lost, only
		// replay-response reproducibility for this one attempt.
		slog.Error("merchant/api: persist idempotency completion failed", "error", completeErr)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(body)
}

// failIdempotentWrite records the attempt as failed (Begin's own doc: "a
// failed attempt does not permanently burn the key" — a later retry with
// the same key+body is allowed to try again) and writes the error response
// for it via writeFn.
func failIdempotentWrite(w http.ResponseWriter, r *http.Request, svc *idempotency.Service, tenantID, recordID uuid.UUID, errorCode string, writeFn func(http.ResponseWriter)) {
	if err := svc.Fail(r.Context(), tenantID, recordID, errorCode); err != nil {
		slog.Error("merchant/api: persist idempotency failure failed", "error", err)
	}
	writeFn(w)
}
