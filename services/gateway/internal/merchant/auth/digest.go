package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

// Digest computes T3 §8.2's HMAC-SHA-256 digest of the FULL plaintext key
// (not just its secret portion) using the application pepper. pepper must
// come from the existing secret-loading boundary
// (internal/platform/config.MerchantConfig.APIKeyPepper) — an empty pepper is a
// caller bug, not a runtime condition to silently tolerate, so this
// returns an error rather than computing a digest with an empty key.
func Digest(pepper, plaintextKey string) ([]byte, error) {
	if pepper == "" {
		return nil, fmt.Errorf("merchant/auth: digest: pepper is empty")
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(plaintextKey))
	return mac.Sum(nil), nil
}

// VerifyDigest reports whether plaintextKey's digest matches stored,
// using a constant-time comparison (§8.2: "comparison must be constant
// time") — never a plain byte-slice equality check, which would leak
// timing information about how many leading bytes matched.
func VerifyDigest(pepper, plaintextKey string, stored []byte) (bool, error) {
	computed, err := Digest(pepper, plaintextKey)
	if err != nil {
		return false, err
	}
	return hmac.Equal(computed, stored), nil
}
