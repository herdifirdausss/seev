package payout

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// currentUserID extracts and parses the authenticated user's ID from the
// JWT claims already validated by pkg/middleware.WithAuth — mirrors
// internal/ledger/transport's own helper of the same name.
func currentUserID(r *http.Request) (uuid.UUID, bool) {
	raw := middleware.UserIDFromCtx(r.Context())
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func isAdmin(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker" || claims.Role == "admin_checker")
}

// errNonIntegralAmount and decimalFromString mirror
// internal/ledger/transport's own (unexported, cross-module-inaccessible)
// helpers of the same name — the ledger is minor-unit-only (docs/plan/01
// decision D2), so a fractional amount here would create/destroy money
// once posted as withdraw_initiate downstream.
var errNonIntegralAmount = errors.New("amount must be an integer (minor units, no fractional part)")

func decimalFromString(s string) (decimal.Decimal, error) {
	amt, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if !amt.Equal(amt.Truncate(0)) {
		return decimal.Decimal{}, errNonIntegralAmount
	}
	return amt, nil
}

type payoutResponse struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Amount       string    `json:"amount"`
	Currency     string    `json:"currency"`
	Vendor       string    `json:"vendor"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toPayoutResponse(req PayoutRequest) payoutResponse {
	return payoutResponse{
		ID: req.ID, UserID: req.UserID, Amount: req.Amount.String(), Currency: req.Currency,
		Vendor: req.Vendor, Status: req.Status, ErrorMessage: req.ErrorMessage,
		CreatedAt: req.CreatedAt, UpdatedAt: req.UpdatedAt,
	}
}

type createPayoutRequest struct {
	Amount      string          `json:"amount"`
	Destination json.RawMessage `json:"destination"`
}

// CreateHandler serves POST /api/v1/payout (docs/plan/23 Task T5) — creates
// a payout request for the authenticated user and drives it through
// hold -> vendor submission synchronously. A Pending or even a Failed
// vendor outcome still returns 201: the request itself was created and is
// progressing/resuming correctly, that's not a client error — poll
// GET /api/v1/payout/{id} for the eventual terminal status.
func (m *Module) CreateHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}

		var req createPayoutRequest
		if !response.Decode(w, r, &req) {
			return
		}
		if len(req.Destination) == 0 {
			response.BadRequest(w, "destination is required")
			return
		}
		amount, err := decimalFromString(req.Amount)
		if err != nil {
			if errors.Is(err, errNonIntegralAmount) {
				response.BadRequest(w, err.Error())
			} else {
				response.BadRequest(w, "amount must be a valid decimal string")
			}
			return
		}
		if !amount.IsPositive() {
			response.BadRequest(w, "amount must be positive")
			return
		}

		id, err := m.Create(r.Context(), userID, amount, req.Destination, userID.String(), "")
		if err != nil {
			switch {
			case errors.Is(err, ErrNoRoute):
				response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "NO_ROUTE", Message: "no payout route available"}})
			default:
				response.InternalServerError(w, err)
			}
			return
		}

		out, err := m.Get(r.Context(), id)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		response.Created(w, toPayoutResponse(out))
	}
}

// GetHandler serves GET /api/v1/payout/{id} (docs/plan/23 Task T5).
// Ownership is a direct comparison against payout_requests.user_id — no
// CanAccessAccount-style indirection needed the way ledger's
// account-ownership model requires, since this table already carries
// user_id directly. A different user's payout reports 404, not 403 — same
// "don't confirm existence to a non-owner" reasoning ledger's own
// ownership-checked handlers use.
func (m *Module) GetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid payout id")
			return
		}
		req, err := m.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				response.NotFound(w, "payout request not found")
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		if req.UserID != userID {
			response.NotFound(w, "payout request not found")
			return
		}
		response.OK(w, toPayoutResponse(req))
	}
}

// AdminRouter returns the payout module's admin HTTP surface, already at
// its final paths (/admin/payout/...) — mount directly, no prefix
// stripping needed (docs/plan/23 Task T5, same mounting pattern as
// internal/payin.Module.AdminRouter). Internal-router only; every handler
// is also admin-gated inside itself, defense in depth, same pattern as
// every other /admin/* surface in this codebase.
func (m *Module) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/payout/requests", m.listRequestsHandler)
	mux.HandleFunc("POST /admin/payout/requests/{id}/cancel", m.adminCancelHandler)
	mux.HandleFunc("POST /admin/payout/requests/{id}/retry", m.adminRetryHandler)
	mux.HandleFunc("GET /admin/payout/routing-rules", m.listRoutingRulesHandler)
	mux.HandleFunc("POST /admin/payout/routing-rules", m.createRoutingRuleHandler)
	mux.HandleFunc("PUT /admin/payout/routing-rules/{id}", m.updateRoutingRuleHandler)
	mux.HandleFunc("GET /admin/payout/vendor-gateways/{vendor}", m.getVendorGatewayHandler)
	mux.HandleFunc("PUT /admin/payout/vendor-gateways/{vendor}", m.putVendorGatewayHandler)
	mux.HandleFunc("POST /admin/payout/vendors/{vendor}/force-fail", m.vendorForceFailHandler)
	mux.HandleFunc("GET /admin/payout/vendors/health", m.vendorHealthHandler)
	mux.HandleFunc("GET /admin/payout/vendor-commands/dead", m.listDeadCommandsHandler)
	mux.HandleFunc("POST /admin/payout/vendor-commands/dead/{id}/replay", m.replayDeadCommandHandler)
	mux.HandleFunc("POST /admin/payout/vendor-commands/dead/replay-all", m.replayAllDeadCommandsHandler)
	mux.HandleFunc("POST /admin/payout/intake/pause", m.directPauseHandler)
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

type deadCommandResponse struct {
	ID              uuid.UUID `json:"id"`
	PayoutRequestID uuid.UUID `json:"payout_request_id"`
	Vendor          string    `json:"vendor"`
	Attempt         int       `json:"attempt"`
	Status          string    `json:"status"`
	RetryCount      int       `json:"retry_count"`
	MaxRetries      int       `json:"max_retries"`
	LastError       string    `json:"last_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func toDeadCommandResponse(c model.PayoutVendorCommand) deadCommandResponse {
	return deadCommandResponse{ID: c.ID, PayoutRequestID: c.PayoutRequestID, Vendor: c.Vendor,
		Attempt: c.Attempt, Status: c.Status, RetryCount: c.RetryCount, MaxRetries: c.MaxRetries,
		LastError: c.LastError, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func (m *Module) listDeadCommandsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err != nil || value <= 0 {
			response.BadRequest(w, "limit must be positive")
			return
		} else {
			limit = value
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if value, err := strconv.Atoi(raw); err != nil || value < 0 {
			response.BadRequest(w, "offset must be non-negative")
			return
		} else {
			offset = value
		}
	}
	commands, err := m.commandRepo.ListDeadCommands(r.Context(), limit, offset)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	out := make([]deadCommandResponse, len(commands))
	for i, command := range commands {
		out[i] = toDeadCommandResponse(command)
	}
	response.OK(w, map[string]any{"commands": out})
}

func (m *Module) replayDeadCommandHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid vendor command id")
		return
	}
	if err := m.commandRepo.ReplayDeadCommand(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrCommandNotFound) {
			response.NotFound(w, "dead vendor command not found")
		} else {
			response.InternalServerError(w, err)
		}
		return
	}
	response.OK(w, map[string]any{"replayed": true, "id": id})
}

func (m *Module) replayAllDeadCommandsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	olderThan := time.Now()
	if raw := r.URL.Query().Get("older_than"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(w, "older_than must be RFC3339")
			return
		}
		olderThan = parsed
	}
	count, err := m.commandRepo.ReplayAllDeadCommands(r.Context(), olderThan)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"replayed": count})
}

type vendorHealthResponse struct {
	Vendors []vendorgw.VendorHealth `json:"vendors"`
}

// vendorHealthHandler serves GET /admin/payout/vendors/health (docs/plan/40
// Task T5 — see internal/payin/http.go's own vendorHealthHandler doc
// comment for why this is namespaced under /admin/payout/ rather than the
// doc's shorthand "/admin/vendors/health"). nil breaker reports an empty
// list.
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

// forceFailSwitch is implemented by mockvendor.PayoutProvider only
// (docs/plan/40 Task T4) — a narrow, package-local interface so this
// production module never imports the test-only mockvendor package
// directly; any registered vendor that doesn't support it simply reports
// itself as unsupported below.
type forceFailSwitch interface{ SetForceFail(fail bool) }

type vendorForceFailRequest struct {
	Fail bool `json:"fail"`
}

// vendorForceFailHandler serves POST /admin/payout/vendors/{vendor}/force-fail
// (docs/plan/40 Task T4) — test-only chaos tooling: flips a registered
// vendor's force-fail switch so every Submit against it returns a genuine
// transport-style error regardless of destination content, tripping the
// circuit breaker from realistic end-to-end traffic instead of reaching
// into breaker internals directly. A vendor that doesn't implement
// forceFailSwitch (i.e. isn't mockvendor) reports 400 — there is no
// generic way to simulate "this real vendor is down".
func (m *Module) vendorForceFailHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	vendor := r.PathValue("vendor")
	provider, ok := m.registry.Payout(vendor)
	if !ok {
		response.NotFound(w, "vendor not registered")
		return
	}
	switcher, ok := provider.(forceFailSwitch)
	if !ok {
		response.BadRequest(w, "vendor does not support force-fail")
		return
	}

	var body vendorForceFailRequest
	if !response.Decode(w, r, &body) {
		return
	}
	switcher.SetForceFail(body.Fail)
	response.OK(w, map[string]bool{"force_fail": body.Fail})
}

type listRequestsResponse struct {
	Requests []payoutResponse `json:"requests"`
}

func (m *Module) listRequestsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}

	status := r.URL.Query().Get("status")
	vendor := r.URL.Query().Get("vendor")

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

	reqs, err := m.List(r.Context(), status, vendor, limit, offset)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	out := make([]payoutResponse, len(reqs))
	for i, req := range reqs {
		out[i] = toPayoutResponse(req)
	}
	response.OK(w, listRequestsResponse{Requests: out})
}

type adminActionRequest struct {
	Reason string `json:"reason"`
}

func (m *Module) adminCancelHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid payout id")
		return
	}

	var body adminActionRequest
	if r.ContentLength > 0 {
		if !response.Decode(w, r, &body) {
			return
		}
	}

	if err := m.AdminCancel(r.Context(), id, body.Reason); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(w, "payout request not found")
		case errors.Is(err, ErrInvalidTransition):
			response.Conflict(w, err.Error())
		default:
			response.InternalServerError(w, err)
		}
		return
	}
	response.OK(w, map[string]bool{"cancelled": true})
}

func (m *Module) adminRetryHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid payout id")
		return
	}

	if err := m.AdminRetry(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(w, "payout request not found")
		case errors.Is(err, ErrInvalidTransition):
			response.Conflict(w, err.Error())
		default:
			response.InternalServerError(w, err)
		}
		return
	}
	response.OK(w, map[string]bool{"retried": true})
}
