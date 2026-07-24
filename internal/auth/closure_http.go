package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/response"
)

type createClosureRequest struct {
	Password string `json:"password"`
}

type closureRequestResponse struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	RequestedAt  time.Time  `json:"requested_at"`
	ReadyAt      *time.Time `json:"completed_at,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

func toClosureRequestResponse(r PrivacyRequest) closureRequestResponse {
	return closureRequestResponse{ID: r.ID, Status: r.Status, RequestedAt: r.RequestedAt, ReadyAt: r.ReadyAt, ErrorMessage: r.ErrorMessage}
}

func writeClosureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		response.Unauthorized(w, "invalid password")
	case errors.Is(err, ErrUserDisabled):
		response.Forbidden(w, "account disabled")
	case errors.Is(err, ErrClosureNotSelfService):
		response.Forbidden(w, "admin and operator accounts require the operator offboarding runbook")
	case errors.Is(err, ErrClosureUnavailable):
		response.ServiceUnavailable(w, "CLOSURE_UNAVAILABLE", "account closure is not available right now")
	case errors.Is(err, ErrClosureNotFound):
		response.NotFound(w, "closure request not found")
	default:
		response.InternalServerError(w, err)
	}
}

// CreateClosureHandler serves POST /api/v1/users/me/privacy/closure (K10).
func (m *Module) CreateClosureHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		var req createClosureRequest
		if !response.Decode(w, r, &req) {
			return
		}
		if req.Password == "" {
			response.BadRequest(w, "password is required")
			return
		}
		closure, err := m.RequestClosure(r.Context(), userID, req.Password)
		if err != nil {
			writeClosureError(w, err)
			return
		}
		response.Created(w, toClosureRequestResponse(closure))
	}
}

// ClosureStatusHandler serves GET /api/v1/users/me/privacy/closure/{id}.
func (m *Module) ClosureStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid session")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid request id")
			return
		}
		closure, err := m.GetClosureStatus(r.Context(), userID, id)
		if err != nil {
			writeClosureError(w, err)
			return
		}
		response.OK(w, toClosureRequestResponse(closure))
	}
}
