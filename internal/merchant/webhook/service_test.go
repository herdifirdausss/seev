package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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
	assert.ErrorIs(t, err, ErrInvalidEnvironment)
}

func TestService_CreateEndpoint_RejectsEmptySubscriptions(t *testing.T) {
	svc := NewService(newFakeWebhookRepository(), testRing(t))
	_, _, err := svc.CreateEndpoint(context.Background(), generalutil.NewV7(), "https://example.test/hook", "live", nil, nil)
	assert.ErrorIs(t, err, ErrEventsRequired)
}

func TestService_CreateEndpoint_RejectsNilTenant(t *testing.T) {
	svc := NewService(newFakeWebhookRepository(), testRing(t))
	_, _, err := svc.CreateEndpoint(context.Background(), uuid.Nil, "https://example.test/hook", "live", []string{transactionPostedExternalType}, nil)
	assert.ErrorIs(t, err, ErrTenantRequired)
}

func TestService_CreateEndpoint_RejectsEmptyURL(t *testing.T) {
	svc := NewService(newFakeWebhookRepository(), testRing(t))
	_, _, err := svc.CreateEndpoint(context.Background(), generalutil.NewV7(), "", "live", []string{transactionPostedExternalType}, nil)
	assert.ErrorIs(t, err, ErrURLRequired)
}

// TestService_CreateEndpoint_RejectsInvalidURL found a real gap during T10:
// CreateEndpoint previously accepted any non-empty string and only
// discovered a malformed URL when the relay worker's first delivery
// attempt failed, days later. validateWebhookURL now fails fast.
func TestService_CreateEndpoint_RejectsInvalidURL(t *testing.T) {
	cases := []string{
		"not-a-url",
		"ftp://example.test/hook",
		"javascript:alert(1)",
		"http://",
		"://missing-scheme",
	}
	for _, raw := range cases {
		svc := NewService(newFakeWebhookRepository(), testRing(t))
		_, _, err := svc.CreateEndpoint(context.Background(), generalutil.NewV7(), raw, "live", []string{transactionPostedExternalType}, nil)
		assert.ErrorIsf(t, err, ErrInvalidURL, "url %q should be rejected", raw)
	}
}

func TestValidateWebhookURL_AcceptsHTTPAndHTTPS(t *testing.T) {
	assert.NoError(t, validateWebhookURL("https://example.test/hook"))
	assert.NoError(t, validateWebhookURL("http://localhost:9999/hook"))
}

func FuzzValidateWebhookURL(f *testing.F) {
	seeds := []string{
		"https://example.test/hook",
		"http://localhost:9999/hook",
		"not-a-url",
		"ftp://example.test/hook",
		"javascript:alert(1)",
		"http://",
		"://missing-scheme",
		"https://user:pass@example.test:8443/hook?query=1#frag",
		"",
		"   ",
		"https://[::1]:8080/hook",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		// Must never panic — the only contract under fuzz. A nil error
		// implies an http(s) scheme with a non-empty host; any other
		// input must return a non-nil error wrapping ErrInvalidURL.
		err := validateWebhookURL(raw)
		if err == nil {
			return
		}
		if !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("validateWebhookURL(%q) returned an error not wrapping ErrInvalidURL: %v", raw, err)
		}
	})
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
