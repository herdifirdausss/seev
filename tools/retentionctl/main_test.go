package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOwnerClasses_FunctionNameConvention proves ownerClasses derives
// exactly the SECURITY DEFINER function names already wired by hand in
// services/ledger, services/auth, and services/adminbff's own
// StartRetentionRunner/Start methods — if the convention ever drifts
// (a class added with a name this derivation can't reconstruct), this
// must fail loudly here rather than at a live `retentionctl dry-run` call.
func TestOwnerClasses_FunctionNameConvention(t *testing.T) {
	classes, err := ownerClasses("../../config/data-retention.yaml", "ledger")
	require.NoError(t, err)

	byName := make(map[string]string, len(classes))
	for _, c := range classes {
		byName[c.Name] = c.FunctionName
	}
	assert.Equal(t, "fn_retention_purge_fee_quotes_unconsumed", byName["ledger.fee_quotes.unconsumed"])
	assert.Equal(t, "fn_retention_purge_fee_quotes_consumed", byName["ledger.fee_quotes.consumed"])
	assert.Equal(t, "fn_retention_purge_outbox_events_published", byName["ledger.outbox_events.published"])

	authClasses, err := ownerClasses("../../config/data-retention.yaml", "auth")
	require.NoError(t, err)
	authByName := make(map[string]string, len(authClasses))
	for _, c := range authClasses {
		authByName[c.Name] = c.FunctionName
	}
	assert.Equal(t, "fn_retention_purge_refresh_tokens", authByName["auth.refresh_tokens"])

	adminbffClasses, err := ownerClasses("../../config/data-retention.yaml", "adminbff")
	require.NoError(t, err)
	adminbffByName := make(map[string]string, len(adminbffClasses))
	for _, c := range adminbffClasses {
		adminbffByName[c.Name] = c.FunctionName
	}
	assert.Equal(t, "fn_retention_purge_sessions", adminbffByName["adminbff.sessions"])
}

func TestOwnerClasses_UnknownOwnerErrors(t *testing.T) {
	_, err := ownerClasses("../../config/data-retention.yaml", "nonexistent")
	require.Error(t, err)
}

func TestConnFlags_Validate_RejectsMissingOrUnsafeOwner(t *testing.T) {
	empty := ""
	dsn := "postgres://x"
	c := connFlags{owner: &empty, dsn: &dsn}
	_, err := c.validate()
	require.Error(t, err)

	unsafe := "auth; DROP TABLE users"
	c = connFlags{owner: &unsafe, dsn: &dsn}
	_, err = c.validate()
	require.Error(t, err)

	emptyDSN := ""
	valid := "auth"
	c = connFlags{owner: &valid, dsn: &emptyDSN}
	_, err = c.validate()
	require.Error(t, err)
}

func TestAuditIdentifier_PanicsOnInvalidInput(t *testing.T) {
	assert.Panics(t, func() { auditIdentifier("auth_retention_holds; DROP TABLE x") })
	assert.NotPanics(t, func() { auditIdentifier("auth_retention_holds") })
}
