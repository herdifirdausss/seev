package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidScope(t *testing.T) {
	for _, s := range AllScopes {
		assert.True(t, ValidScope(s), "%s must be a valid scope", s)
	}
	assert.False(t, ValidScope("admin:*"), "wildcard scopes are not supported in C1 (§7.2)")
	assert.False(t, ValidScope("unknown:scope"))
}

func TestAuthorizeScopes_ANDSemantics(t *testing.T) {
	principal := Principal{Scopes: []string{"transfers:write"}}
	assert.False(t, AuthorizeScopes(principal, []string{"transfers:write", "transactions:read"}),
		"holding only one of two required scopes must not authorize")

	principal.Scopes = []string{"transfers:write", "transactions:read"}
	assert.True(t, AuthorizeScopes(principal, []string{"transfers:write", "transactions:read"}))
}

func TestAuthorizeScopes_EmptyRequiredNeverAuthorizes(t *testing.T) {
	principal := Principal{Scopes: []string{"merchant:read", "accounts:read", "transactions:read"}}
	assert.False(t, AuthorizeScopes(principal, nil),
		"an empty required-scopes list must fail closed, never be treated as \"nothing needed\"")
}

func TestRequiredScopes_UnregisteredOperationFailsClosed(t *testing.T) {
	_, ok := RequiredScopes("noSuchOperation")
	assert.False(t, ok)
}

// TestScopeRegistryMatchesContract proves the registry stays in sync with
// contracts/http/b2b-v1.yaml's own x-see-scopes annotations — the two are
// hand-maintained in separate files (a YAML contract and a Go map), and
// this test is what prevents them silently drifting apart.
func TestScopeRegistryMatchesContract(t *testing.T) {
	contractScopes := map[string][]string{
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
	assert.Equal(t, len(contractScopes), len(scopeRegistry), "registry and contract operation count must match")
	for op, scopes := range contractScopes {
		got, ok := RequiredScopes(op)
		assert.True(t, ok, "operation %s missing from scope registry", op)
		assert.ElementsMatch(t, scopes, got, "scope mismatch for %s", op)
	}
}
