package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestNewLedgerProxy_ForwardsClientSuppliedRequestID proves the proxy
// forwards a request_id the client already sent (default Director behavior).
func TestNewLedgerProxy_ForwardsClientSuppliedRequestID(t *testing.T) {
	var gotHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Request-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy, err := newLedgerProxy(backend.URL, nil, discardLogger())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ledger/accounts", nil)
	req.Header.Set("X-Request-Id", "client-supplied-id")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	assert.Equal(t, "client-supplied-id", gotHeader)
}

// TestNewLedgerProxy_ForwardsGeneratedRequestID proves docs/plan/36 Task T2:
// a gateway-generated id (only present in ctx, not on the inbound request's
// own header) still reaches the backend through the proxy's Director wrap.
func TestNewLedgerProxy_ForwardsGeneratedRequestID(t *testing.T) {
	var gotHeader string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Request-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy, err := newLedgerProxy(backend.URL, nil, discardLogger())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ledger/accounts", nil)
	ctx := context.WithValue(req.Context(), middleware.RequestIDKey, "gateway-generated-id")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	assert.Equal(t, "gateway-generated-id", gotHeader)
}
