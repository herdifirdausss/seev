package webhook

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// Replay creates a NEW delivery for tenantID's existing deliveryID — same
// EndpointID and EventID as the original, immediately due (T7 acceptance:
// "replay creates a new delivery ID with the same event ID"). Used by both
// the tenant's own self-service API and T8's Admin BFF operator replay
// control; both resolve tenantID from their own auth/session context
// before calling this, so a single tenant-scoped method serves both.
func (s *Service) Replay(ctx context.Context, tenantID, deliveryID uuid.UUID) (model.WebhookDelivery, error) {
	original, err := s.repo.GetDelivery(ctx, tenantID, deliveryID)
	if err != nil {
		return model.WebhookDelivery{}, fmt.Errorf("merchant/webhook: get delivery to replay: %w", err)
	}

	originalID := original.ID
	replay := model.WebhookDelivery{
		ID:                 generalutil.NewV7(),
		PublicID:           "whd_" + uuid.NewString()[:16],
		TenantID:           tenantID,
		EndpointID:         original.EndpointID,
		EventID:            original.EventID,
		ReplayOfDeliveryID: &originalID,
		Status:             "pending",
		AttemptCount:       0,
	}
	if err := s.repo.CreateReplayDelivery(ctx, replay); err != nil {
		return model.WebhookDelivery{}, fmt.Errorf("merchant/webhook: create replay delivery: %w", err)
	}
	return replay, nil
}
