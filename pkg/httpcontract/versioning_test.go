package httpcontract

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// This is intentionally a test-only rollout harness. Production does not
// expose a fabricated v2 operation merely to satisfy the policy drill.
func TestVersionedHTTPRolloutKeepsV1StableDuringV2Cutover(t *testing.T) {
	mux := New(Options{Owner: "gateway", Audience: "public", Contract: "public-v1"})
	mux.HandleContract("GET /api/v1/contract-probe", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}), Registration{OperationID: "gatewayContractProbeV1"})
	mux.HandleContract("GET /api/v2/contract-probe", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"v2"}`))
	}), Registration{Contract: "public-v2", OperationID: "gatewayContractProbeV2"})
	for path, want := range map[string]string{"/api/v1/contract-probe": `{"version":"v1"}`, "/api/v2/contract-probe": `{"version":"v2"}`} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != want {
			t.Errorf("%s: got %d %q", path, recorder.Code, recorder.Body.String())
		}
	}
	if err := Validate(mux.Snapshot()); err != nil {
		t.Fatalf("versioned registrations invalid: %v", err)
	}
}
