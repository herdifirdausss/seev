package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
)

// isAdminMaker/isAdminChecker mirror ledger transport's own maker-checker
// role helpers exactly (services/ledger/internal/transport/http.go) — the "admin"
// role alone is both maker and checker (a single-operator dev/small-team
// deployment), same convention.
func isAdminMaker(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker")
}

func isAdminChecker(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_checker")
}

type proposeOperatorOffboardingRequest struct {
	TargetUserID string `json:"target_user_id"`
	Reason       string `json:"reason"`
}

type operatorOffboardingResponse struct {
	ID               uuid.UUID  `json:"id"`
	TargetUserID     uuid.UUID  `json:"target_user_id"`
	RequestedBy      string     `json:"requested_by"`
	ApprovedBy       string     `json:"approved_by,omitempty"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	ClosureRequestID *uuid.UUID `json:"closure_request_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
}

func toOperatorOffboardingResponse(r OperatorOffboardingRequest) operatorOffboardingResponse {
	out := operatorOffboardingResponse{
		ID: r.ID, TargetUserID: r.TargetUserID, RequestedBy: r.RequestedBy, ApprovedBy: r.ApprovedBy,
		Reason: r.Reason, Status: r.Status, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt,
	}
	if r.ClosureRequestID != uuid.Nil {
		out.ClosureRequestID = &r.ClosureRequestID
	}
	return out
}

func writeOperatorOffboardingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrOperatorOffboardingNotFound):
		response.NotFound(w, "operator offboarding request not found")
	case errors.Is(err, ErrOperatorOffboardingNotOperator):
		response.BadRequest(w, "target account is not an admin/operator account")
	case errors.Is(err, ErrOperatorOffboardingSelfApproval):
		response.Forbidden(w, "cannot approve or reject your own proposal")
	case errors.Is(err, ErrOperatorOffboardingAlreadyDecided):
		response.Conflict(w, "request was already decided")
	case errors.Is(err, ErrClosureUnavailable):
		response.ServiceUnavailable(w, "CLOSURE_UNAVAILABLE", "account closure is not available right now")
	case errors.Is(err, ErrUserDisabled):
		response.Forbidden(w, "target account is not active")
	default:
		response.InternalServerError(w, err)
	}
}

// AdminProposeOperatorOffboardingHandler serves POST
// /api/v1/admin/privacy/operator-offboarding — the "maker" half of K10's
// own maker/checker requirement for admin/operator account closure.
func (h *Handler) AdminProposeOperatorOffboardingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminMaker(r) {
			response.Forbidden(w, "maker privileges required")
			return
		}
		requestedBy, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		var req proposeOperatorOffboardingRequest
		if !response.Decode(w, r, &req) {
			return
		}
		targetUserID, err := uuid.Parse(req.TargetUserID)
		if err != nil {
			response.BadRequest(w, "target_user_id must be a valid UUID")
			return
		}
		if req.Reason == "" {
			response.BadRequest(w, "reason is required")
			return
		}
		proposal, err := h.module.ProposeOperatorOffboarding(r.Context(), requestedBy.String(), targetUserID, req.Reason)
		if err != nil {
			writeOperatorOffboardingError(w, err)
			return
		}
		response.Created(w, toOperatorOffboardingResponse(proposal))
	}
}

// AdminApproveOperatorOffboardingHandler serves POST
// /api/v1/admin/privacy/operator-offboarding/{id}/approve — the "checker"
// half. Starts the same closure saga self-service RequestClosure starts.
func (h *Handler) AdminApproveOperatorOffboardingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		approvedBy, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid request id")
			return
		}
		decided, err := h.module.ApproveOperatorOffboarding(r.Context(), id, approvedBy.String())
		if err != nil {
			writeOperatorOffboardingError(w, err)
			return
		}
		response.OK(w, toOperatorOffboardingResponse(decided))
	}
}

// AdminRejectOperatorOffboardingHandler serves POST
// /api/v1/admin/privacy/operator-offboarding/{id}/reject.
func (h *Handler) AdminRejectOperatorOffboardingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		approvedBy, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid request id")
			return
		}
		decided, err := h.module.RejectOperatorOffboarding(r.Context(), id, approvedBy.String())
		if err != nil {
			writeOperatorOffboardingError(w, err)
			return
		}
		response.OK(w, toOperatorOffboardingResponse(decided))
	}
}

// AdminListOperatorOffboardingHandler serves GET
// /api/v1/admin/privacy/operator-offboarding — either maker or checker may
// list (read-only), filtered by status=pending|approved|rejected (optional).
func (h *Handler) AdminListOperatorOffboardingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminMaker(r) && !isAdminChecker(r) {
			response.Forbidden(w, "admin privileges required")
			return
		}
		requests, err := h.module.ListOperatorOffboarding(r.Context(), r.URL.Query().Get("status"), 100)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		out := make([]operatorOffboardingResponse, len(requests))
		for i, req := range requests {
			out[i] = toOperatorOffboardingResponse(req)
		}
		response.OK(w, map[string]any{"requests": out})
	}
}
