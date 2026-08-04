package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "supersecretkeythatisatleast32chars!"

func validClaims() Claims {
	return Claims{
		UserID:   "user-1",
		Email:    "alice@example.com",
		Role:     "user",
		KYCLevel: 1,
		Exp:      time.Now().Add(15 * time.Minute).Unix(),
	}
}

func mustGenerateToken(t *testing.T, claims Claims) string {
	t.Helper()
	token, err := GenerateToken(testSecret, claims)
	require.NoError(t, err)
	return token
}

// ─── GenerateToken ────────────────────────────────────────────────────────────

func TestGenerateToken_Success(t *testing.T) {
	token, err := GenerateToken(testSecret, validClaims())
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, 3, len(splitParts(token)))
}

func TestGenerateToken_SetsIat(t *testing.T) {
	before := time.Now().Unix()
	token, err := GenerateToken(testSecret, validClaims())
	require.NoError(t, err)

	claims, err := ParseToken(testSecret, token, "")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, claims.Iat, before)
}

// ─── ParseToken ───────────────────────────────────────────────────────────────

func TestParseToken_ValidToken(t *testing.T) {
	original := validClaims()
	token := mustGenerateToken(t, original)

	claims, err := ParseToken(testSecret, token, "")
	require.NoError(t, err)
	assert.Equal(t, original.UserID, claims.UserID)
	assert.Equal(t, original.Email, claims.Email)
	assert.Equal(t, original.Role, claims.Role)
}

func TestParseToken_MalformedToken_TooFewParts(t *testing.T) {
	_, err := ParseToken(testSecret, "only.two", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed")
}

func TestParseToken_MalformedToken_TooManyParts(t *testing.T) {
	_, err := ParseToken(testSecret, "a.b.c.d", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed")
}

func TestParseToken_InvalidSignature(t *testing.T) {
	token := mustGenerateToken(t, validClaims())
	// Tamper with the signature
	parts := splitParts(token)
	parts[2] = "invalidsignature"
	tampered := parts[0] + "." + parts[1] + "." + parts[2]

	_, err := ParseToken(testSecret, tampered, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature")
}

func TestParseToken_BadSignatureEncoding(t *testing.T) {
	token := mustGenerateToken(t, validClaims())
	parts := splitParts(token)
	// Replace signature with non-base64url string
	tampered := parts[0] + "." + parts[1] + ".!!!invalid!!!"

	_, err := ParseToken(testSecret, tampered, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode signature")
}

func TestParseToken_BadPayloadEncoding(t *testing.T) {
	token := mustGenerateToken(t, validClaims())
	parts := splitParts(token)
	// Replace payload with invalid base64
	tampered := parts[0] + ".!!!invalid!!!." + parts[2]

	_, err := ParseToken(testSecret, tampered, "")
	require.Error(t, err)
}

func TestParseToken_ExpiredToken(t *testing.T) {
	claims := validClaims()
	claims.Exp = time.Now().Add(-time.Hour).Unix() // already expired
	token := mustGenerateToken(t, claims)

	_, err := ParseToken(testSecret, token, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestParseToken_WrongSecret(t *testing.T) {
	token := mustGenerateToken(t, validClaims())
	_, err := ParseToken("wrong-secret-that-is-at-least-32-chars", token, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature")
}

// ─── ParseToken issuer (docs/roadmap/archive/10 Task T6) ──────────────────────────────────

func TestParseToken_IssuerConfigured_WrongIssuer_Rejected(t *testing.T) {
	claims := validClaims()
	claims.Iss = "other-service"
	token := mustGenerateToken(t, claims)

	_, err := ParseToken(testSecret, token, "seev-api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issuer")
}

func TestParseToken_IssuerConfigured_MatchingIssuer_Accepted(t *testing.T) {
	claims := validClaims()
	claims.Iss = "seev-api"
	token := mustGenerateToken(t, claims)

	got, err := ParseToken(testSecret, token, "seev-api")
	require.NoError(t, err)
	assert.Equal(t, "seev-api", got.Iss)
}

func TestParseToken_IssuerNotConfigured_TokenWithoutIssuer_Accepted(t *testing.T) {
	// Backward compatible: expectedIssuer == "" skips the check entirely,
	// so tokens minted before JWT_ISSUER was ever configured keep working.
	token := mustGenerateToken(t, validClaims())

	_, err := ParseToken(testSecret, token, "")
	require.NoError(t, err)
}

// ─── Claims.IsExpired ─────────────────────────────────────────────────────────

func TestClaims_IsExpired_False(t *testing.T) {
	c := Claims{Exp: time.Now().Add(time.Hour).Unix()}
	assert.False(t, c.IsExpired())
}

func TestClaims_IsExpired_True(t *testing.T) {
	c := Claims{Exp: time.Now().Add(-time.Hour).Unix()}
	assert.True(t, c.IsExpired())
}

// ─── WithAuth middleware ──────────────────────────────────────────────────────

func TestWithAuth_ValidToken(t *testing.T) {
	var capturedClaims *Claims
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetClaims(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := WithAuth(testSecret, "")(inner)

	token := mustGenerateToken(t, validClaims())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, capturedClaims)
	assert.Equal(t, "user-1", capturedClaims.UserID)
}

func TestWithAuth_MissingHeader(t *testing.T) {
	handler := WithAuth(testSecret, "")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithAuth_MalformedHeader_NoSpace(t *testing.T) {
	handler := WithAuth(testSecret, "")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearertoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithAuth_WrongScheme(t *testing.T) {
	handler := WithAuth(testSecret, "")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithAuth_InvalidToken(t *testing.T) {
	handler := WithAuth(testSecret, "")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.token.at.all")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithAuth_ExpiredToken(t *testing.T) {
	claims := validClaims()
	claims.Exp = time.Now().Add(-time.Hour).Unix()
	token := mustGenerateToken(t, claims)

	handler := WithAuth(testSecret, "")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithAuth_CaseInsensitiveBearer(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := WithAuth(testSecret, "")(inner)

	token := mustGenerateToken(t, validClaims())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "BEARER "+token) // uppercase
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWithAuth_StoresUserIDInContext(t *testing.T) {
	var userID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID = UserIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := WithAuth(testSecret, "")(inner)

	token := mustGenerateToken(t, validClaims())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "user-1", userID)
}

// ─── GetClaims ────────────────────────────────────────────────────────────────

func TestGetClaims_PresentInContext(t *testing.T) {
	c := &Claims{UserID: "u1", Role: "admin"}
	ctx := context.WithValue(context.Background(), claimsKey{}, c)
	retrieved := GetClaims(ctx)
	assert.Equal(t, c, retrieved)
}

func TestGetClaims_NotInContext(t *testing.T) {
	assert.Nil(t, GetClaims(context.Background()))
}

// ─── WithRole ─────────────────────────────────────────────────────────────────

func TestWithRole_AllowedRole(t *testing.T) {
	claims := &Claims{UserID: "u1", Role: "admin"}
	ctx := context.WithValue(context.Background(), claimsKey{}, claims)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := WithRole("admin", "superadmin")(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestWithRole_DisallowedRole(t *testing.T) {
	claims := &Claims{UserID: "u1", Role: "user"}
	ctx := context.WithValue(context.Background(), claimsKey{}, claims)

	handler := WithRole("admin")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestWithRole_NoClaims(t *testing.T) {
	handler := WithRole("admin")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func splitParts(token string) []string {
	parts := make([]string, 0, 3)
	start := 0
	for i, ch := range token {
		if ch == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	parts = append(parts, token[start:])
	return parts
}
