package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/herdifirdausss/seev/pkg/response"
)

type claimsKey struct{}

// Claims represents the JWT payload.
type Claims struct {
	UserID   string `json:"sub"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	KYCLevel int    `json:"kyc_level"`
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
	Iss      string `json:"iss,omitempty"`
}

// IsExpired reports whether the token has passed its expiry timestamp.
func (c *Claims) IsExpired() bool {
	return time.Now().Unix() > c.Exp
}

// jwtHeader is the standard HS256 JWT header, pre-encoded.
var jwtHeader = base64.RawURLEncoding.EncodeToString(
	[]byte(`{"alg":"HS256","typ":"JWT"}`),
)

// GenerateToken creates a signed HS256 JWT token for the given claims.
func GenerateToken(secret string, claims Claims) (string, error) {
	claims.Iat = time.Now().Unix()

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("jwt generate: marshal: %w", err)
	}

	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := jwtHeader + "." + encodedPayload
	sig := sign([]byte(secret), []byte(signingInput))

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ParseToken validates the signature and expiry, returning Claims on success.
// expectedIssuer is checked against claims.Iss only when non-empty — this
// keeps deployments that haven't configured JWT_ISSUER working exactly as
// before (docs/plan/10 Task T6).
func ParseToken(secret, tokenString, expectedIssuer string) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("jwt parse: malformed token")
	}

	// Constant-time signature verification
	signingInput := parts[0] + "." + parts[1]
	expectedSig := sign([]byte(secret), []byte(signingInput))

	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("jwt parse: decode signature: %w", err)
	}

	if !hmac.Equal(expectedSig, actualSig) {
		return nil, fmt.Errorf("jwt parse: invalid signature")
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("jwt parse: decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("jwt parse: unmarshal: %w", err)
	}

	if claims.IsExpired() {
		return nil, fmt.Errorf("jwt parse: token expired")
	}

	if expectedIssuer != "" && claims.Iss != expectedIssuer {
		return nil, fmt.Errorf("jwt parse: unexpected issuer")
	}

	return &claims, nil
}

// WithAuth validates a Bearer JWT and stores claims in ctx. issuer is passed
// straight to ParseToken — empty means issuer validation is skipped
// (backward compatible, docs/plan/10 Task T6).
func WithAuth(secret, issuer string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				response.Unauthorized(w, "missing Authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				response.Unauthorized(w, "Authorization header must be 'Bearer <token>'")
				return
			}

			claims, err := ParseToken(secret, parts[1], issuer)
			if err != nil {
				response.Unauthorized(w, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, claims.UserID)
			ctx = context.WithValue(ctx, claimsKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClaims retrieves JWT claims from ctx. Returns nil if not present.
func GetClaims(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey{}).(*Claims)
	return c
}

// WithRole ensures the authenticated user has one of the allowed roles.
// Must be used after WithAuth.
func WithRole(roles ...string) Middleware {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				response.Unauthorized(w, "unauthenticated")
				return
			}
			if !allowed[claims.Role] {
				response.Forbidden(w, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func sign(secret, data []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return mac.Sum(nil)
}
