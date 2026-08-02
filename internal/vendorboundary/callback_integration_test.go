//go:build integration

// Proves the security-audit fix (migrations/vendor/000004_boundary_rls_and_encryption)
// end to end against real Postgres: raw_body/selected_headers are actually
// encrypted at rest (not just column-renamed), RLS is actually enabled+forced
// on both boundary tables, and the redaction retention function correctly
// nulls the new ciphertext columns.
package vendorboundary

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
)

func setupVendorBoundaryTestDB(t *testing.T) *database.DBSQL {
	t.Helper()
	ctx := context.Background()

	const dbName, dbUser, dbPassword = "seev_test", "test", "secret"
	container, err := postgres.Run(ctx, "postgres:16.14-alpine",
		postgres.WithDatabase(dbName), postgres.WithUsername(dbUser), postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)

	require.NoError(t, testutil.ApplyServiceMigrations(migrationsSourceURL(t), dsn))

	cfg := config.PostgresConfig{Host: host, Port: port.Port(), User: dbUser, Password: dbPassword, DB: dbName, SSLMode: "disable", MaxOpenConns: 10}
	db, err := database.New(ctx, cfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	return "file://../../migrations"
}

func TestBoundaryTables_RLSEnabledAndForced(t *testing.T) {
	db := setupVendorBoundaryTestDB(t)
	ctx := context.Background()

	for _, table := range []string{"vendor_callback_inbox", "vendor_outbound_attempts"} {
		var enabled, forced bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = $1`, table,
		).Scan(&enabled, &forced))
		require.True(t, enabled, "%s must have RLS enabled", table)
		require.True(t, forced, "%s must have RLS forced", table)
	}
}

func TestInboxStore_Ensure_EncryptsRawBodyAndHeaders(t *testing.T) {
	db := setupVendorBoundaryTestDB(t)
	ctx := context.Background()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 71)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)

	store := &InboxStore{db: db, ring: ring}
	plainRaw := []byte(`{"account_number":"1234567890","status":"settled"}`)
	callback := &NormalizedCallback{Vendor: "mockvendor", VendorEventID: "evt-1", Status: "settled"}
	record, err := store.Ensure(ctx, callback, plainRaw, map[string]string{"Content-Type": "application/json"}, "cidr:127.0.0.1")
	require.NoError(t, err)
	require.True(t, record.claimed)

	var rawCiphertext []byte
	var rawKeyVersion int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT raw_body_ciphertext, raw_body_key_version FROM vendor_callback_inbox WHERE id = $1`, record.id,
	).Scan(&rawCiphertext, &rawKeyVersion))

	require.NotEqual(t, plainRaw, rawCiphertext, "raw_body_ciphertext must not equal the plaintext")
	require.NotContains(t, string(rawCiphertext), "1234567890", "ciphertext must not leak the plaintext account number")
	require.Equal(t, 1, rawKeyVersion)

	opened, err := ring.Open(rawBodyAAD(record.id), rawCiphertext)
	require.NoError(t, err)
	require.Equal(t, plainRaw, opened, "decrypting with the same ring/AAD must recover the exact original bytes")
}

func TestRetentionPurgeCallbackInboxRaw_RedactsEligibleRows(t *testing.T) {
	db := setupVendorBoundaryTestDB(t)
	ctx := context.Background()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 91)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	store := &InboxStore{db: db, ring: ring}

	callback := &NormalizedCallback{Vendor: "mockvendor", VendorEventID: "evt-old", Status: "settled"}
	record, err := store.Ensure(ctx, callback, []byte("secret payload"), nil, "cidr:127.0.0.1")
	require.NoError(t, err)

	// Push the row into a terminal state, 31 days in the past — eligible for redaction.
	_, err = db.ExecContext(ctx,
		`UPDATE vendor_callback_inbox SET processing_status = 'finalized', updated_at = $2 WHERE id = $1`,
		record.id, time.Now().Add(-31*24*time.Hour))
	require.NoError(t, err)

	var affected int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT fn_retention_purge_callback_inbox_raw($1, 500, false)`, uuid.New(),
	).Scan(&affected))
	require.Equal(t, 1, affected)

	var rawCiphertext, headersCiphertext []byte
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT raw_body_ciphertext, selected_headers_ciphertext FROM vendor_callback_inbox WHERE id = $1`, record.id,
	).Scan(&rawCiphertext, &headersCiphertext))
	require.Nil(t, rawCiphertext, "raw_body_ciphertext must be redacted to NULL")
	require.Nil(t, headersCiphertext, "selected_headers_ciphertext must be redacted to NULL")
}
