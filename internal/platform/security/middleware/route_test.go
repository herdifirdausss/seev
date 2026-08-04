package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithRoutePattern_StoresMatchedPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(http.ResponseWriter, *http.Request) {})

	var got string
	handler := WithRoutePattern(mux)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RouteFromCtx(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "GET /orders/{id}", got)
}

func TestWithRoutePattern_NoMatch_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(http.ResponseWriter, *http.Request) {})

	var got string
	handler := WithRoutePattern(mux)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RouteFromCtx(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Empty(t, got)
}

func TestEffectiveRoutePattern_PrefersCtxOverRPattern(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Pattern = "GET /stale-leaf-pattern"
	req = req.WithContext(context.WithValue(req.Context(), routeCtxKey{}, "GET /correct-from-mux-lookup"))

	assert.Equal(t, "GET /correct-from-mux-lookup", effectiveRoutePattern(req))
}

func TestEffectiveRoutePattern_FallsBackToRPattern(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Pattern = "GET /leaf-pattern"

	assert.Equal(t, "GET /leaf-pattern", effectiveRoutePattern(req))
}
