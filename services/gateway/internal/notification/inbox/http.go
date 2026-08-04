package notify

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/registry"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/repository"
)

// currentUserID extracts and parses the authenticated user's ID from the
// JWT claims already validated by internal/platform/security/middleware.WithAuth — mirrors
// services/payin's and services/payout's own helper of the same name.
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
	Kind      string     `json:"kind,omitempty"`
	Category  string     `json:"category,omitempty"`
	Priority  string     `json:"priority,omitempty"`
	DeepLink  string     `json:"deep_link,omitempty"`
}

func toNotificationResponse(n Notification) notificationResponse {
	return notificationResponse{
		ID: n.ID, Type: n.Type, Title: n.Title, Body: n.Body, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt,
		Kind: n.Kind, Category: n.Category, Priority: n.Priority, DeepLink: n.DeepLink,
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
		if limit > 200 {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_LIMIT_INVALID", "limit must not exceed 200")
			return
		}

		var before time.Time
		rawBefore := r.URL.Query().Get("before")
		if rawBefore == "" {
			// cursor was the original public contract. Keep accepting it as an
			// RFC3339 alias while clients migrate to the explicit before name.
			rawBefore = r.URL.Query().Get("cursor")
		}
		if rawBefore != "" {
			parsed, err := time.Parse(time.RFC3339, rawBefore)
			if err != nil {
				response.BadRequest(w, "before must be an RFC3339 timestamp")
				return
			}
			before = parsed
		}
		unread := false
		if raw := r.URL.Query().Get("unread"); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_UNREAD_INVALID", "unread must be true or false")
				return
			}
			unread = parsed
		}
		category, kind := r.URL.Query().Get("category"), r.URL.Query().Get("kind")
		if category != "" && !validCategory(category) {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_CATEGORY_INVALID", "unknown notification category")
			return
		}
		if kind != "" {
			if _, ok := registry.Lookup(kind); !ok {
				response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_KIND_INVALID", "unknown notification kind")
				return
			}
		}

		notifications, err := m.ListNotificationsFiltered(r.Context(), userID, limit, before, unread, category, kind)
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

// DetailHandler returns only the authenticated owner's row. Ownership and
// absence intentionally share the same 404 response.
func (m *Module) DetailHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.NotFound(w, "notification not found")
			return
		}
		if m.platform == nil || m.db == nil {
			n, err := m.repo.Get(r.Context(), id)
			if err != nil || n.UserID != userID {
				response.NotFound(w, "notification not found")
				return
			}
			response.OK(w, toNotificationResponse(n))
			return
		}
		n, err := m.platform.GetNotification(r.Context(), userID, id)
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(w, "notification not found")
			return
		}
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		response.OK(w, toNotificationResponse(n))
	}
}

func (m *Module) UnreadCountHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		if m.platform == nil || m.db == nil {
			response.OK(w, map[string]int{"count": 0})
			return
		}
		count, err := m.platform.UnreadCount(r.Context(), userID)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		response.OK(w, map[string]int64{"count": count})
	}
}

func (m *Module) MarkAllReadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		var req struct {
			Before *time.Time `json:"before"`
		}
		if r.Body != nil && r.ContentLength > 0 {
			if !response.Decode(w, r, &req) {
				return
			}
		}
		if m.platform == nil || m.db == nil {
			response.NoContent(w)
			return
		}
		if err := m.platform.MarkAllRead(r.Context(), userID, req.Before); err != nil {
			response.InternalServerError(w, err)
			return
		}
		response.NoContent(w)
	}
}

func validCategory(value string) bool {
	switch value {
	case model.CategoryMoneyMovement, model.CategoryAccount, model.CategorySecurity, model.CategoryCompliance, model.CategorySystem:
		return true
	default:
		return false
	}
}

// MarkReadHandler serves POST /api/v1/notifications/{id}/read (docs/roadmap/archive/25
// Task T4 step 4). Ownership is enforced at the repository layer — a
// different user's notification id reports 404, not 403, same "don't
// confirm existence to a non-owner" reasoning as services/payout/internal/
// services/payin's own GetHandlers.
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
