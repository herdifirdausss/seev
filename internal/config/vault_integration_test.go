//go:build integration

// Package config_test proves docs/roadmap/archive/49 K7's two boot paths end to end
// against a REAL dev-mode Vault container (not just the mocked-HTTP unit
// tests in vault_test.go): a service boots normally with VAULT_ADDR/
// VAULT_TOKEN unset (today's env-only behavior, byte for byte), and a
// service seeded in Vault picks up the Vault value in preference to env.
package config_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/herdifirdausss/seev/internal/config"
)

// writeVaultSecret writes a KV v2 secret via the same HTTP shape
// pkg/config's own vaultGetenv reads (POST {addr}/v1/secret/data/{path}
// with {"data": {...}}) — verified live against a real Vault container.
func writeVaultSecret(t *testing.T, addr, token, service string, kv map[string]string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"data": kv})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, addr+"/v1/secret/data/"+service, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "vault secret write must succeed")
}

// setupVaultTestContainer starts a real dev-mode Vault (docs/roadmap/archive/49 K7)
// with a fixed root token — dev mode auto-unseals and auto-mounts the
// secret/ KV v2 engine, verified live before this test was written.
func setupVaultTestContainer(t *testing.T) (addr, token string) {
	t.Helper()
	ctx := context.Background()
	const rootToken = "integration-test-root-token"

	req := testcontainers.ContainerRequest{
		Image:        "hashicorp/vault:latest",
		ExposedPorts: []string{"8200/tcp"},
		Env: map[string]string{
			"VAULT_DEV_ROOT_TOKEN_ID":  rootToken,
			"VAULT_DEV_LISTEN_ADDRESS": "0.0.0.0:8200",
		},
		CapAdd:     []string{"IPC_LOCK"},
		WaitingFor: wait.ForHTTP("/v1/sys/health").WithPort("8200/tcp").WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "8200")
	require.NoError(t, err)
	return "http://" + host + ":" + port.Port(), rootToken
}

func TestLoad_WithoutVaultConfigured_BehavesIdenticalToEnvOnly(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("APP_NAME", "ledger-service")
	t.Setenv("JWT_SECRET", "env-only-secret-at-least-32-characters-long")
	t.Setenv("JWT_ISSUER", "seev-integration")
	t.Setenv("POSTGRES_USER", "u")
	t.Setenv("POSTGRES_PASSWORD", "env-postgres-password")
	t.Setenv("POSTGRES_DB", "d")
	t.Setenv("RABBITMQ_HOST", "localhost")
	t.Setenv("RABBITMQ_PORT", "5672")
	t.Setenv("RABBITMQ_USERNAME", "guest")
	t.Setenv("RABBITMQ_PASSWORD", "guest")
	t.Setenv("RABBITMQ_VHOST", "/")
	t.Setenv("RABBITMQ_EXCHANGE", "app.exchange")

	cfg, err := config.LoadAuthService()
	require.NoError(t, err)
	require.Equal(t, "env-only-secret-at-least-32-characters-long", cfg.JWT.Secret)
	require.Equal(t, "env-postgres-password", cfg.Postgres.Password)
}

func TestLoad_WithVaultSeeded_VaultValueWinsOverEnv(t *testing.T) {
	addr, token := setupVaultTestContainer(t)

	// Seed secret/ledger-service via the same KV v2 write shape verified
	// live against a real Vault container while designing K7.
	writeVaultSecret(t, addr, token, "ledger-service", map[string]string{
		"JWT_SECRET": "vault-sourced-secret-at-least-32-characters",
	})

	t.Setenv("VAULT_ADDR", addr)
	t.Setenv("VAULT_TOKEN", token)
	t.Setenv("APP_NAME", "ledger-service")
	t.Setenv("JWT_SECRET", "env-fallback-secret-should-not-win-here-xx")
	t.Setenv("JWT_ISSUER", "seev-integration")
	t.Setenv("POSTGRES_USER", "u")
	t.Setenv("POSTGRES_PASSWORD", "env-postgres-password")
	t.Setenv("POSTGRES_DB", "d")
	t.Setenv("RABBITMQ_HOST", "localhost")
	t.Setenv("RABBITMQ_PORT", "5672")
	t.Setenv("RABBITMQ_USERNAME", "guest")
	t.Setenv("RABBITMQ_PASSWORD", "guest")
	t.Setenv("RABBITMQ_VHOST", "/")
	t.Setenv("RABBITMQ_EXCHANGE", "app.exchange")

	cfg, err := config.LoadAuthService()
	require.NoError(t, err)
	require.Equal(t, "vault-sourced-secret-at-least-32-characters", cfg.JWT.Secret, "Vault must win over env when both are set")
	// POSTGRES_PASSWORD was never written to Vault — must still fall
	// through to env, proving the overlay is per-key, not all-or-nothing.
	require.Equal(t, "env-postgres-password", cfg.Postgres.Password)
}
