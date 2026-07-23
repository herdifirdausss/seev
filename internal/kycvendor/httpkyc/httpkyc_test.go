package httpkyc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/kycvendor"
)

func TestProviderSandboxContract(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"verdict":"refer","ref":"sandbox-123","reason":"manual review"}`))
	}))
	defer server.Close()

	provider, err := New(server.URL, "sandbox-token", "sandbox-kyc", server.Client())
	require.NoError(t, err)
	decision, err := provider.Verify(context.Background(), kycvendor.Submission{UserID: uuid.New(), LevelRequested: 2, Payload: map[string]any{"name": "Jane Doe"}})
	require.NoError(t, err)
	require.Equal(t, kycvendor.VerdictRefer, decision.Verdict)
	require.Equal(t, "sandbox-123", decision.Ref)
	require.Equal(t, "Bearer sandbox-token", gotAuth)
}

func TestProviderRejectsInvalidVerdict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"verdict":"unknown"}`))
	}))
	defer server.Close()
	provider, err := New(server.URL, "", "sandbox", server.Client())
	require.NoError(t, err)
	_, err = provider.Verify(context.Background(), kycvendor.Submission{UserID: uuid.New(), LevelRequested: 1})
	require.Error(t, err)
}
