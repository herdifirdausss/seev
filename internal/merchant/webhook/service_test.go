package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/pkg/generalutil"
)

func TestNewService_PanicsOnNilDeps(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)

	assert.Panics(t, func() { NewService(nil, ring) })
	assert.Panics(t, func() { NewService(repo, nil) })
}

func TestService_CreateEndpoint_SecretShownOnceAndStoredEncrypted(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()

	endpoint, secret, err := svc.CreateEndpoint(context.Background(), tenantID, "https://example.test/hook", "live", []string{transactionPostedExternalType}, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, secret)
	assert.NotContains(t, string(endpoint.SecretCiphertext), secret, "the stored ciphertext must never contain the plaintext secret")

	opened, err := ring.Open(secretAAD(endpoint.ID), endpoint.SecretCiphertext)
	require.NoError(t, err)
	assert.Equal(t, secret, string(opened), "the ciphertext must decrypt back to the exact secret that was returned")
}

func TestService_CreateEndpoint_RejectsInvalidEnvironment(t *testing.T) {
	svc := NewService(newFakeWebhookRepository(), testRing(t))
	_, _, err := svc.CreateEndpoint(context.Background(), generalutil.NewV7(), "https://example.test/hook", "production", []string{transactionPostedExternalType}, nil)
	assert.Error(t, err)
}

func TestService_CreateEndpoint_RejectsEmptySubscriptions(t *testing.T) {
	svc := NewService(newFakeWebhookRepository(), testRing(t))
	_, _, err := svc.CreateEndpoint(context.Background(), generalutil.NewV7(), "https://example.test/hook", "live", nil, nil)
	assert.Error(t, err)
}

func TestService_RotateSecret_ChangesSecretAndCiphertext(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()

	endpoint, firstSecret, err := svc.CreateEndpoint(context.Background(), tenantID, "https://example.test/hook", "live", []string{transactionPostedExternalType}, nil)
	require.NoError(t, err)

	secondSecret, err := svc.RotateSecret(context.Background(), tenantID, endpoint.ID)
	require.NoError(t, err)
	assert.NotEqual(t, firstSecret, secondSecret)

	stored, err := repo.GetEndpoint(context.Background(), tenantID, endpoint.ID)
	require.NoError(t, err)
	opened, err := ring.Open(secretAAD(endpoint.ID), stored.SecretCiphertext)
	require.NoError(t, err)
	assert.Equal(t, secondSecret, string(opened))
}

func TestExternalEventPublicID_StableAcrossRedelivery(t *testing.T) {
	id := generalutil.NewV7()
	first := externalEventPublicID(id)
	second := externalEventPublicID(id)
	assert.Equal(t, first, second)
	assert.Equal(t, "evt_"+id.String(), first)
}

func TestBuildTransactionPostedEnvelope_FieldShape(t *testing.T) {
	occurredAt := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	body, err := BuildTransactionPostedEnvelope("evt_abc", true, occurredAt, map[string]string{"amount": "100"})
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"id": "evt_abc",
		"type": "transaction.posted.v1",
		"livemode": true,
		"created_at": "2026-01-15T10:30:00Z",
		"data": {"amount": "100"}
	}`, string(body))
}

func TestService_SetEndpointStatus_RejectsInvalidValue(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()
	endpoint, _, err := svc.CreateEndpoint(context.Background(), tenantID, "https://example.test/hook", "live", []string{transactionPostedExternalType}, nil)
	require.NoError(t, err)

	err = svc.SetEndpointStatus(context.Background(), tenantID, endpoint.ID, "paused")
	assert.Error(t, err)
}

func TestService_DeleteEndpoint(t *testing.T) {
	repo := newFakeWebhookRepository()
	ring := testRing(t)
	svc := NewService(repo, ring)
	tenantID := generalutil.NewV7()
	endpoint, _, err := svc.CreateEndpoint(context.Background(), tenantID, "https://example.test/hook", "live", []string{transactionPostedExternalType}, nil)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteEndpoint(context.Background(), tenantID, endpoint.ID))
	_, err = svc.GetEndpoint(context.Background(), tenantID, endpoint.ID)
	assert.Error(t, err)
}
