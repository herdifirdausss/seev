package httpcontract

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMuxRetainsGo122MatchingAndRecordsMetadata(t *testing.T) {
	mux := New(Options{Owner: "gateway", Audience: "public", Contract: "public-v1"})
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PathValue("id")))
	})

	request := httptest.NewRequest(http.MethodGet, "/users/synthetic-001", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "synthetic-001", recorder.Body.String())

	methodNotAllowed := httptest.NewRecorder()
	mux.ServeHTTP(methodNotAllowed, httptest.NewRequest(http.MethodPost, "/users/synthetic-001", nil))
	require.Equal(t, http.StatusMethodNotAllowed, methodNotAllowed.Code)

	snapshot := mux.Snapshot()
	require.Len(t, snapshot, 1)
	require.Equal(t, Registration{Method: "GET", Path: "/users/{id}", Owner: "gateway", Audience: "public", Contract: "public-v1", OperationID: "gateway_get_users_id"}, snapshot[0])
}

func TestHandleContractUsesExplicitStableID(t *testing.T) {
	mux := New(Options{Owner: "ledger", Audience: "internal"})
	mux.HandleContract("POST /transactions", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Registration{OperationID: "ledgerPostTransactionV1"})
	require.Equal(t, "ledgerPostTransactionV1", mux.Snapshot()[0].OperationID)
}

func TestValidateRejectsDuplicateRouteOrOperation(t *testing.T) {
	valid := Registration{Method: "GET", Path: "/health", Owner: "auth", Audience: "operational", OperationID: "authHealthV1"}
	require.NoError(t, Validate([]Registration{valid}))
	require.Error(t, Validate([]Registration{valid, valid}))
	require.Error(t, Validate([]Registration{valid, {Method: "POST", Path: "/other", Owner: "auth", Audience: "operational", OperationID: "authHealthV1"}}))
}
