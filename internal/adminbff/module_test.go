package adminbff

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/database"
)

type sessionRepoFake struct {
	sessions map[string]Session
}

func (f *sessionRepoFake) CreateSession(_ context.Context, s Session) error {
	if f.sessions == nil {
		f.sessions = map[string]Session{}
	}
	f.sessions[s.ID] = s
	return nil
}
func (f *sessionRepoFake) GetSession(_ context.Context, id string) (Session, error) {
	s, ok := f.sessions[id]
	if !ok || time.Now().After(s.ExpiresAt) || time.Now().After(s.AbsoluteExpiresAt) {
		return Session{}, ErrSessionNotFound
	}
	return s, nil
}
func (f *sessionRepoFake) TouchSession(_ context.Context, id string, expires time.Time) error {
	s, ok := f.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	s.ExpiresAt = expires
	s.LastSeenAt = time.Now()
	f.sessions[id] = s
	return nil
}
func (f *sessionRepoFake) DeleteSession(_ context.Context, id string) error {
	delete(f.sessions, id)
	return nil
}
func (f *sessionRepoFake) CleanupSessions(_ context.Context, now time.Time) error {
	for id, s := range f.sessions {
		if now.After(s.ExpiresAt) || now.After(s.AbsoluteExpiresAt) {
			delete(f.sessions, id)
		}
	}
	return nil
}

func TestNewOpaqueTokenIsRandom256Bit(t *testing.T) {
	one, err := NewOpaqueToken(32)
	require.NoError(t, err)
	two, err := NewOpaqueToken(32)
	require.NoError(t, err)
	require.Len(t, one, 64)
	require.NotEqual(t, one, two)
}

func TestLoginAcceptsOnlyAdminRoles(t *testing.T) {
	userID := uuid.New()
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"user":{"id":"` + userID.String() + `","email":"operator@example.test","role":"admin"}}}`))
	}))
	defer authServer.Close()

	db := &database.MockDatabaseSQL{}
	m := NewModule(db, config.AdminBFFConfig{AuthServiceURL: authServer.URL, SecureCookie: false, SessionIdleTTL: 30 * time.Minute, SessionAbsoluteTTL: 8 * time.Hour, JWTSecret: "secret"}, nil)
	fake := &sessionRepoFake{}
	m.repo = fake
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Form = map[string][]string{"email": {"operator@example.test"}, "password": {"secret"}}
	rec := httptest.NewRecorder()
	m.LoginHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.NotNil(t, rec.Result().Cookies())
	require.Len(t, fake.sessions, 1)
}

func TestCSRFRejectsMissingTokenAndAcceptsHeader(t *testing.T) {
	fake := &sessionRepoFake{sessions: map[string]Session{}}
	id := "session"
	fake.sessions[id] = Session{ID: id, UserID: uuid.New(), Email: "operator@example.test", Role: "admin", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour), AbsoluteExpiresAt: time.Now().Add(2 * time.Hour)}
	m := &Module{repo: fake, cfg: config.AdminBFFConfig{SecureCookie: false, SessionIdleTTL: time.Minute}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := m.RequireSession(m.RequireCSRF(next))
	missing := httptest.NewRequest(http.MethodPost, "/api/v1/admin/mutate", nil)
	missing.AddCookie(&http.Cookie{Name: sessionCookieName, Value: id})
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missing)
	require.Equal(t, http.StatusForbidden, missingRec.Code)

	valid := httptest.NewRequest(http.MethodPost, "/api/v1/admin/mutate", nil)
	valid.AddCookie(&http.Cookie{Name: sessionCookieName, Value: id})
	valid.Header.Set("X-CSRF-Token", "csrf")
	validRec := httptest.NewRecorder()
	handler.ServeHTTP(validRec, valid)
	require.Equal(t, http.StatusNoContent, validRec.Code)
}
