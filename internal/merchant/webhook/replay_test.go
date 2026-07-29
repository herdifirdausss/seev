package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

func TestService_Replay_NewDeliveryIDSameEventID(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()

	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, "https://example.test/hook", "sandbox")
	original := model.WebhookDelivery{
		ID: generalutil.NewV7(), PublicID: "whd_orig", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "dead",
	}
	_, err := repo.CreateDelivery(context.Background(), original)
	require.NoError(t, err)

	replay, err := svc.Replay(context.Background(), tenantID, original.ID)
	require.NoError(t, err)

	assert.NotEqual(t, original.ID, replay.ID, "replay must create a NEW delivery id")
	assert.Equal(t, original.EventID, replay.EventID, "replay must carry the SAME event id")
	assert.Equal(t, original.EndpointID, replay.EndpointID)
	require.NotNil(t, replay.ReplayOfDeliveryID)
	assert.Equal(t, original.ID, *replay.ReplayOfDeliveryID)
	assert.Equal(t, "pending", replay.Status)

	stored, err := repo.GetDelivery(context.Background(), tenantID, replay.ID)
	require.NoError(t, err)
	assert.Equal(t, replay.ID, stored.ID)
}

func TestService_Replay_ExemptFromAutomaticDedup(t *testing.T) {
	// The automatic path's (endpoint_id, event_id) unique constraint would
	// reject a second row for the same pair — CreateReplayDelivery must
	// bypass that (it's a different code path, not a second CreateDelivery
	// call), so multiple replays of the same original delivery succeed.
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()

	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, "https://example.test/hook", "sandbox")
	original := model.WebhookDelivery{
		ID: generalutil.NewV7(), PublicID: "whd_orig", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "dead",
	}
	_, err := repo.CreateDelivery(context.Background(), original)
	require.NoError(t, err)

	replay1, err := svc.Replay(context.Background(), tenantID, original.ID)
	require.NoError(t, err)
	replay2, err := svc.Replay(context.Background(), tenantID, original.ID)
	require.NoError(t, err)

	assert.NotEqual(t, replay1.ID, replay2.ID)
	assert.Len(t, repo.deliveries, 3, "original + 2 replays, all persisted independently")
}

func TestService_Replay_WrongTenantNotFound(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()
	otherTenantID := generalutil.NewV7()

	endpoint, event, _ := seedEndpointAndEvent(t, repo, ring, tenantID, "https://example.test/hook", "sandbox")
	original := model.WebhookDelivery{
		ID: generalutil.NewV7(), PublicID: "whd_orig", TenantID: tenantID,
		EndpointID: endpoint.ID, EventID: event.ID, Status: "dead",
	}
	_, err := repo.CreateDelivery(context.Background(), original)
	require.NoError(t, err)

	_, err = svc.Replay(context.Background(), otherTenantID, original.ID)
	assert.Error(t, err, "replay must be tenant-scoped — another tenant's delivery id must not resolve")
}
