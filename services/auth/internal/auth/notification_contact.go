package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
)

// NotificationContact returns the narrow identity projection Gateway needs
// for delivery decisions. HTTP parsing and response serialization belong to
// services/auth/internal/transport/http; Auth remains the sole owner of identity storage.
func (m *Module) NotificationContact(ctx context.Context, userID uuid.UUID) (model.User, error) {
	return m.users.GetUserByID(ctx, userID)
}
