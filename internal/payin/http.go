package payin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// AdminRouter returns the payin module's admin HTTP surface, already at
// its final paths (/admin/payin/...) — mount directly, no prefix
// stripping needed (docs/roadmap/archive/22 Task T4, same mounting pattern as
// internal/policy.Handler.Mux). Internal-router only; every handler is
// also admin-gated inside itself, defense in depth, same pattern as every
// other /admin/* surface in this codebase.
func (m *Module) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/payin/events", m.listEventsHandler)
	mux.HandleFunc("POST /admin/payin/events/{id}/replay", m.replayEventHandler)
	mux.HandleFunc("GET /admin/payin/routing-rules", m.listRoutingRulesHandler)
	mux.HandleFunc("POST /admin/payin/routing-rules", m.createRoutingRuleHandler)
	mux.HandleFunc("PUT /admin/payin/routing-rules/{id}", m.updateRoutingRuleHandler)
	mux.HandleFunc("GET /admin/payin/vendor-gateways/{vendor}", m.getVendorGatewayHandler)
	mux.HandleFunc("PUT /admin/payin/vendor-gateways/{vendor}", m.putVendorGatewayHandler)
	mux.HandleFunc("GET /admin/payin/vendors/health", m.vendorHealthHandler)
	mux.HandleFunc("POST /admin/payin/intake/pause", m.directPauseHandler)
	return mux
}

type intakePauseRequest struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func (m *Module) directPauseHandler(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.Role != "admin" {
		response.Forbidden(w, "direct pause requires admin role")
		return
	}
	var request intakePauseRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Reason == "" {
		response.BadRequest(w, "command_id and reason are required")
		return
	}
	commandID, err := uuid.Parse(request.CommandID)
	if err != nil {
		response.BadRequest(w, "command_id must be a UUID")
		return
	}
	actor := claims.UserID
	if actor == "" {
		actor = claims.Email
	}
	result, err := m.ApplyIntakeControl(r.Context(), commandID, "pause", request.ExpectedRevision, actor, request.Reason)
	if err != nil {
		if errors.Is(err, ErrIntakeRevisionMismatch) {
			response.Conflict(w, "intake revision mismatch")
			return
		}
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, result)
}

type vendorHealthResponse struct {
	Vendors []vendorgw.VendorHealth `json:"vendors"`
}

// vendorHealthHandler serves GET /admin/payin/vendors/health (docs/roadmap/archive/40
// Task T5 — the doc's own shorthand path "/admin/vendors/health" isn't
// reachable through this service's admin router, which only ever mounts
// the "/admin/payin/" subtree, see cmd/payin-service/router.go; every
// other admin route in this module is namespaced the same way, so this
// stays consistent with that convention instead). nil breaker (no
// BREAKER_* config wired, or every vendor still closed) reports an empty
// list — same "byte-identical when the feature is off" contract as the
// rest of docs/roadmap/archive/40.
func (m *Module) vendorHealthHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	vendors := []vendorgw.VendorHealth{}
	if m.breaker != nil {
		vendors = m.breaker.Snapshot(r.Context())
	}
	response.OK(w, vendorHealthResponse{Vendors: vendors})
}

func isAdmin(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker" || claims.Role == "admin_checker")
}

type webhookEventResponse struct {
	ID            uuid.UUID `json:"id"`
	Vendor        string    `json:"vendor"`
	VendorEventID string    `json:"vendor_event_id"`
	ExternalRef   string    `json:"external_ref"`
	UserID        uuid.UUID `json:"user_id"`
	Amount        string    `json:"amount"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toWebhookEventResponse(ev WebhookEvent) webhookEventResponse {
	return webhookEventResponse{
		ID: ev.ID, Vendor: ev.Vendor, VendorEventID: ev.VendorEventID, ExternalRef: ev.ExternalRef,
		UserID: ev.UserID, Amount: ev.Amount.String(), Currency: ev.Currency, Status: ev.Status,
		ErrorMessage: ev.ErrorMessage, CreatedAt: ev.CreatedAt, UpdatedAt: ev.UpdatedAt,
	}
}

type listEventsResponse struct {
	Events []webhookEventResponse `json:"events"`
}

func (m *Module) listEventsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	vendor := r.URL.Query().Get("vendor")
	status := r.URL.Query().Get("status")

	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			response.BadRequest(w, "offset must be a non-negative integer")
			return
		}
		offset = parsed
	}

	events, err := m.ListEvents(r.Context(), vendor, status, limit, offset)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	out := make([]webhookEventResponse, len(events))
	for i, ev := range events {
		out[i] = toWebhookEventResponse(ev)
	}
	response.OK(w, listEventsResponse{Events: out})
}

type replayEventResponse struct {
	Replayed bool `json:"replayed"`
}

func (m *Module) replayEventHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid event id")
		return
	}

	if err := m.ReplayEvent(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(w, "webhook event not found")
		case errors.Is(err, ErrAlreadyPosted):
			response.Conflict(w, "event already posted — replay is for received/failed events only")
		default:
			response.InternalServerError(w, err)
		}
		return
	}
	response.OK(w, replayEventResponse{Replayed: true})
}
