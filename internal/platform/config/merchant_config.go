package config

// MerchantConfig is docs/roadmap/archive/57-c1-merchant-b2b-api.md T2's "configuration
// with secure defaults" for the Gateway-owned Merchant/B2B API module.
// APIKeyPepper is T3 §8.2's application pepper for the API-key HMAC-SHA-256
// digest — it must come from the existing secret-loading boundary (file or
// env, same pattern as every other secret in this Config), never a
// hardcoded value. A missing pepper is a T3 boot-fail condition (matches
// the K5-style fail-closed precedent already set for
// INTERNAL_GRPC_TOKEN/CRYPTOX keys), not something T2 itself enforces —
// T2 only loads the value.
//
// IdempotencyDefaultTTL/QuotaFailClosed are conservative defaults per T4's
// own required posture: idempotency records expire in a bounded window
// (not forever), and financial writes fail closed on a quota-backend
// outage by default — an operator must opt out explicitly, never opt in.
type MerchantConfig struct {
	APIKeyPepper          string
	IdempotencyDefaultTTL string // Go duration string, e.g. "24h"
	QuotaFailClosed       bool
}

func loadMerchantConfig(getenv func(string) string) MerchantConfig {
	return MerchantConfig{
		APIKeyPepper:          firstNonEmpty(readOptionalSecretFile(getenv("MERCHANT_API_KEY_PEPPER_FILE")), getenv("MERCHANT_API_KEY_PEPPER")),
		IdempotencyDefaultTTL: getWithDefault(getenv, "MERCHANT_IDEMPOTENCY_DEFAULT_TTL", "24h"),
		QuotaFailClosed:       getenv("MERCHANT_QUOTA_FAIL_OPEN") != "true",
	}
}
