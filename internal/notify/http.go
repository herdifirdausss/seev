package notify

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// currentUserID extracts and parses the authenticated user's ID from the
// JWT claims already validated by pkg/middleware.WithAuth — mirrors
// internal/payin's and internal/payout's own helper of the same name.
func currentUserID(r *http.Request) (uuid.UUID, bool) {
	raw := middleware.UserIDFromCtx(r.Context())
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

type notificationResponse struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func toNotificationResponse(n Notification) notificationResponse {
	return notificationResponse{
		ID: n.ID, Type: n.Type, Title: n.Title, Body: n.Body, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt,
	}
}

type listNotificationsResponse struct {
	Notifications []notificationResponse `json:"notifications"`
}

// ListHandler serves GET /api/v1/notifications?limit=&before= (docs/roadmap/archive/25
// Task T4 step 4) — the authenticated user's own rows only, newest first,
// keyset-paginated. before is an RFC3339 timestamp (exclusive lower bound
// on created_at); omitted means "start from the most recent".
func (m *Module) ListHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}

		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				response.BadRequest(w, "limit must be a positive integer")
				return
			}
			limit = parsed
		}

		var before time.Time
		if raw := r.URL.Query().Get("before"); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.BadRequest(w, "before must be an RFC3339 timestamp")
				return
			}
			before = parsed
		}

		notifications, err := m.ListNotifications(r.Context(), userID, limit, before)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		out := make([]notificationResponse, 0, len(notifications))
		for _, n := range notifications {
			out = append(out, toNotificationResponse(n))
		}
		response.OK(w, listNotificationsResponse{Notifications: out})
	}
}

// MarkReadHandler serves POST /api/v1/notifications/{id}/read (docs/roadmap/archive/25
// Task T4 step 4). Ownership is enforced at the repository layer — a
// different user's notification id reports 404, not 403, same "don't
// confirm existence to a non-owner" reasoning as internal/payout/
// internal/payin's own GetHandlers.
func (m *Module) MarkReadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid notification id")
			return
		}
		if err := m.MarkRead(r.Context(), id, userID); err != nil {
			if errors.Is(err, ErrNotificationNotFound) {
				response.NotFound(w, "notification not found")
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		response.NoContent(w)
	}
}
