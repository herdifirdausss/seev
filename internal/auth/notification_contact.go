package auth

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/response"
)

// NotificationContactHandler is a purpose-built internal projection for
// Gateway. It exposes only the verified-contact decision required for email
// delivery; Auth remains the sole owner of identity storage.
func (m *Module) NotificationContactHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.JSON(w, http.StatusNotFound, map[string]any{"code": "USER_NOT_FOUND"})
			return
		}
		u, err := m.users.GetUserByID(r.Context(), userID)
		if errors.Is(err, repository.ErrNotFound) {
			response.JSON(w, http.StatusNotFound, map[string]any{"code": "USER_NOT_FOUND"})
			return
		}
		if err != nil {
			response.JSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL_ERROR"})
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"user_id": u.ID, "email": u.Email, "email_verified": u.EmailVerifiedAt != nil, "user_status": u.Status, "updated_at": u.UpdatedAt})
	}
}
