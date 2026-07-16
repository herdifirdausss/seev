package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const authTestSecret = "supersecretkeythatisatleast32chars!"

type fakeAuthHandlers struct {
	refreshCount int
}

func jsonHandler(status int, body map[string]any) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
}

func (f *fakeAuthHandlers) RegisterHandler() http.HandlerFunc {
	return jsonHandler(http.StatusCreated, map[string]any{"id": "user-1", "refresh_token": "refresh-1"})
}

func (f *fakeAuthHandlers) LoginHandler() http.HandlerFunc {
	return jsonHandler(http.StatusOK, map[string]any{"access_token": "access-1", "refresh_token": "refresh-2"})
}

func (f *fakeAuthHandlers) RefreshHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.refreshCount++
		jsonHandler(http.StatusOK, map[string]any{
			"old_refresh_token": request.RefreshToken,
			"refresh_token":     "rotated-" + time.Now().Format("150405.000000000"),
		}).ServeHTTP(w, r)
	})
}

func (f *fakeAuthHandlers) MeHandler() http.HandlerFunc {
	return jsonHandler(http.StatusOK, map[string]any{"id": "user-1"})
}

func (f *fakeAuthHandlers) UpdateMeHandler() http.HandlerFunc {
	return jsonHandler(http.StatusOK, map[string]any{"id": "user-1", "full_name": "Updated"})
}

func authRouterTestConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{Env: "development", BaseURL: "http://localhost:8082"},
		JWT: config.JWTConfig{Secret: authTestSecret, AccessExpiry: 15 * time.Minute},
	}
}

func authRequest(t *testing.T, router http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthServiceRouter_PublicFlows(t *testing.T) {
	handlers := &fakeAuthHandlers{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := publicRouter(authRouterTestConfig(), handlers, nil, log)

	register := authRequest(t, router, http.MethodPost, "/api/v1/auth/register", `{}`, "")
	assert.Equal(t, http.StatusCreated, register.Code)

	login := authRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{}`, "")
	assert.Equal(t, http.StatusOK, login.Code)

	refreshOne := authRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"old-token"}`, "")
	refreshTwo := authRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"next-token"}`, "")
	assert.Equal(t, http.StatusOK, refreshOne.Code)
	assert.Equal(t, http.StatusOK, refreshTwo.Code)
	var first, second map[string]any
	require.NoError(t, json.Unmarshal(refreshOne.Body.Bytes(), &first))
	require.NoError(t, json.Unmarshal(refreshTwo.Body.Bytes(), &second))
	assert.NotEqual(t, first["refresh_token"], second["refresh_token"])
	assert.Equal(t, 2, handlers.refreshCount)

	unauthorized := authRequest(t, router, http.MethodGet, "/api/v1/users/me", "", "")
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	accessToken, err := middleware.GenerateToken(authTestSecret, middleware.Claims{
		UserID: "user-1", Role: "user", Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	me := authRequest(t, router, http.MethodGet, "/api/v1/users/me", "", accessToken)
	assert.Equal(t, http.StatusOK, me.Code)
}

func TestAuthServiceInternalRouter(t *testing.T) {
	recorder := httptest.NewRecorder()
	internalRouter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
}
