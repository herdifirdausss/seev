package httpcontract

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeprecationMiddlewareUsesRFCMetadata(t *testing.T) {
	now := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	entry := Deprecation{OperationID: "oldOperationV1", DeprecatedAt: now, Sunset: now.Add(90 * 24 * time.Hour), MigrationURL: "https://docs.example.invalid/migrate"}
	require.NoError(t, entry.Validate(now))
	handler := DeprecationMiddleware(map[string]Deprecation{"GET /old": entry}, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/old", nil))
	require.True(t, strings.HasPrefix(recorder.Header().Get("Deprecation"), "@"))
	require.Equal(t, now.Add(90*24*time.Hour).UTC().Format(http.TimeFormat), recorder.Header().Get("Sunset"))
	require.Contains(t, recorder.Header().Get("Link"), `rel="deprecation"`)
}

func TestDeprecationRejectsEarlySunsetAndNonHTTPSMigration(t *testing.T) {
	now := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	require.Error(t, (Deprecation{OperationID: "x", DeprecatedAt: now, Sunset: now.Add(-time.Hour), MigrationURL: "https://docs.example.invalid"}).Validate(now))
	require.Error(t, (Deprecation{OperationID: "x", DeprecatedAt: now, Sunset: now.Add(time.Hour), MigrationURL: "http://example.invalid"}).Validate(now))
}

func TestLoadDeprecationsEnforcesMinimumWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := []byte(`minimum_window_days: 30
entries:
  - method: GET
    path: /v1/old
    operation_id: old
    deprecated_at: "2026-01-01T00:00:00Z"
    sunset: "2026-02-01T00:00:00Z"
    migration_url: https://example.com/migrate
`)
	entries, err := LoadDeprecations(body, now)
	if err != nil || len(entries) != 1 {
		t.Fatalf("valid deprecation policy rejected: entries=%v err=%v", entries, err)
	}
	short := []byte("minimum_window_days: 7\nentries: []\n")
	if _, err := LoadDeprecations(short, now); err == nil {
		t.Fatal("shortened production deprecation window accepted")
	}
}
