package auth

import "slices"

// Scope registry (§7.2: "every handler declares its required scope in one
// central route registry... no handler may perform an inline ad-hoc scope
// string comparison"). Keyed by OpenAPI operationId (contracts/http/b2b-v1.yaml's
// own x-see-scopes extension, kept in sync with this map — see
// TestScopeRegistryMatchesContract) rather than by HTTP method+path, so a
// path-parameter rename can never silently desync the two.
//
// Where an operation lists more than one scope (§6.4's "Required scopes"
// pairs for transfers/payins/payouts), this registry's locked
// interpretation is ALL listed scopes are required (AND, not OR) — the
// plan's prose lists both scopes together under one resource-group
// heading without specifying per-operation splits, and requiring the full
// pair for every operation in that group is the simpler, more
// conservative reading, consistent with §8.2's "the default key template
// is least privilege" (an operator grants exactly the pair a use case
// needs, not a broader one to work around an OR-shaped registry).
var scopeRegistry = map[string][]string{
	"b2bGetMerchantV1":                 {"merchant:read"},
	"b2bListAccountsV1":                {"accounts:read"},
	"b2bGetAccountV1":                  {"accounts:read"},
	"b2bGetAccountBalanceV1":           {"accounts:read"},
	"b2bListTransactionsV1":            {"transactions:read"},
	"b2bGetTransactionV1":              {"transactions:read"},
	"b2bCreateTransferV1":              {"transfers:write", "transactions:read"},
	"b2bGetTransferV1":                 {"transfers:write", "transactions:read"},
	"b2bCreatePayinV1":                 {"payins:write", "payins:read"},
	"b2bGetPayinV1":                    {"payins:write", "payins:read"},
	"b2bCreatePayoutV1":                {"payouts:write", "payouts:read"},
	"b2bGetPayoutV1":                   {"payouts:write", "payouts:read"},
	"b2bListWebhookEndpointsV1":        {"webhooks:read"},
	"b2bCreateWebhookEndpointV1":       {"webhooks:write"},
	"b2bGetWebhookEndpointV1":          {"webhooks:read"},
	"b2bUpdateWebhookEndpointV1":       {"webhooks:write"},
	"b2bDeleteWebhookEndpointV1":       {"webhooks:write"},
	"b2bRotateWebhookEndpointSecretV1": {"webhooks:write"},
	"b2bListWebhookDeliveriesV1":       {"webhooks:read"},
	"b2bGetWebhookDeliveryV1":          {"webhooks:read"},
	"b2bReplayWebhookDeliveryV1":       {"webhooks:write"},
}

// AllScopes is §7.1's initial scope list — the only values a key MAY be
// issued (§7.2: "unknown scopes are rejected at key creation"; "wildcard
// scopes are not supported in C1").
var AllScopes = []string{
	"merchant:read",
	"accounts:read",
	"transactions:read",
	"transfers:write",
	"payins:read",
	"payins:write",
	"payouts:read",
	"payouts:write",
	"webhooks:read",
	"webhooks:write",
}

// ValidScope reports whether scope is one of AllScopes — used at key
// creation (§7.2) to reject unknown scopes outright.
func ValidScope(scope string) bool {
	return slices.Contains(AllScopes, scope)
}

// RequiredScopes returns the registered scopes for operationId. ok=false
// means the operationId is not registered — callers must fail closed
// (deny, not allow) on an unregistered operation, never treat it as
// "no scope required."
func RequiredScopes(operationID string) (scopes []string, ok bool) {
	s, ok := scopeRegistry[operationID]
	return s, ok
}

// AuthorizeScopes reports whether principal holds every scope in
// required (AND semantics — see the package doc comment above). An empty
// required slice always returns false: RequiredScopes' ok=false case must
// never be silently treated as "no scope needed."
func AuthorizeScopes(principal Principal, required []string) bool {
	if len(required) == 0 {
		return false
	}
	for _, scope := range required {
		if !principal.HasScope(scope) {
			return false
		}
	}
	return true
}
