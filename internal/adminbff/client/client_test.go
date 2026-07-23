package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceClientDo_ForwardsIdentityAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/payout/requests" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer operator-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	c := New("payout", server.URL, DefaultHTTPClient())
	status, headers, body, err := c.Do(context.Background(), "operator-token", http.MethodPost, "/admin/payout/requests", []byte(`{"amount":"1"}`))
	if err != nil || status != http.StatusCreated || headers.Get("Content-Type") != "application/json" || string(body) != `{"success":true}` {
		t.Fatalf("status=%d headers=%v body=%s err=%v", status, headers, body, err)
	}
}

func TestServiceClientDo_MapsUnavailableAndRetainsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"dependency down"}}`))
	}))
	defer server.Close()
	c := New("ledger", server.URL, DefaultHTTPClient())
	status, _, body, err := c.Do(context.Background(), "", http.MethodGet, "/health", nil)
	if status != http.StatusBadGateway || string(body) == "" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("status=%d body=%s err=%v", status, body, err)
	}
}

func TestServiceClientDo_MapsDownstream4xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"checker required"}}`))
	}))
	defer server.Close()
	c := New("ledger", server.URL, DefaultHTTPClient())
	status, _, body, err := c.Do(context.Background(), "", http.MethodPost, "/admin/ledger/adjustments", []byte(`{}`))
	var downstreamErr *DownstreamError
	if status != http.StatusForbidden || string(body) == "" || !errors.As(err, &downstreamErr) || downstreamErr.Message != "checker required" {
		t.Fatalf("status=%d body=%s err=%v", status, body, err)
	}
}
