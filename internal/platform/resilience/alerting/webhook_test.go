package alerting

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebhookAlerter_SendsCorrectPayload(t *testing.T) {
	var received webhookPayload
	var gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	alert := NewWebhookAlerter(srv.URL, nil)
	err := alert(context.Background(), "critical", "unbalanced transaction detected")

	require.NoError(t, err)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "critical", received.Severity)
	assert.Equal(t, "unbalanced transaction detected", received.Message)
	assert.Equal(t, "seev-ledger", received.Service)
	assert.NotEmpty(t, received.Timestamp)
	_, err = time.Parse(time.RFC3339, received.Timestamp)
	assert.NoError(t, err, "timestamp must be RFC3339")
}

func TestNewWebhookAlerter_NonSuccessStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	alert := NewWebhookAlerter(srv.URL, nil)
	err := alert(context.Background(), "critical", "test")

	assert.Error(t, err)
}

func TestNewWebhookAlerterForService_LabelsPayload(t *testing.T) {
	var received webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, NewWebhookAlerterForService(srv.URL, "seev-assurance", nil)(context.Background(), "high", "test"))
	assert.Equal(t, "seev-assurance", received.Service)
}

func TestNewWebhookAlerter_UnreachableURL_ReturnsErrorNotPanic(t *testing.T) {
	alert := NewWebhookAlerter("http://127.0.0.1:1/unreachable", nil)

	assert.NotPanics(t, func() {
		err := alert(context.Background(), "critical", "test")
		assert.Error(t, err)
	})
}

func TestNewWebhookAlerter_SlowEndpoint_TimesOutRatherThanBlockingForever(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	alert := NewWebhookAlerter(srv.URL, &http.Client{}) // no client-level timeout — defaultTimeout via context must still bound it

	start := time.Now()
	err := alert(context.Background(), "critical", "test")
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Less(t, elapsed, 8*time.Second, "must be bounded by defaultTimeout, not hang indefinitely")
}
