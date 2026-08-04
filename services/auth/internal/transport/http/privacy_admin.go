package http

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
)

type adminPrivacyRequestResponse struct {
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	RequestType  string     `json:"request_type"`
	Status       string     `json:"status"`
	RequestedAt  time.Time  `json:"requested_at"`
	ReadyAt      *time.Time `json:"ready_at,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	RetryCount   int        `json:"retry_count"`
}

// AdminPrivacyRequestsHandler serves the internal admin privacy status
// projection. Query parsing and JSON shape stay in the adapter; the service
// owns the query and its data-access policy.
func (h *Handler) AdminPrivacyRequestsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requests, err := h.module.AdminListPrivacyRequests(r.Context(), r.URL.Query().Get("type"), r.URL.Query().Get("status"), 100)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		out := make([]adminPrivacyRequestResponse, len(requests))
		for i, req := range requests {
			out[i] = adminPrivacyRequestResponse{ID: req.ID, UserID: req.UserID, RequestType: req.RequestType, Status: req.Status, RequestedAt: req.RequestedAt, ReadyAt: req.ReadyAt, ErrorMessage: req.ErrorMessage, RetryCount: req.RetryCount}
		}
		response.OK(w, map[string]any{"requests": out})
	}
}
