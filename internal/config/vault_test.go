package config

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vaultTestServer mocks Vault's KV v2 read endpoint (docs/plan/49 K7),
// shaped exactly as verified live against a real dev-mode Vault container:
// GET /v1/secret/data/{service} with X-Vault-Token, nesting the actual
// key/value map under data.data.
func vaultTestServer(t *testing.T, wantToken string, secretsByService map[string]map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != wantToken {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		const prefix = "/v1/secret/data/"
		service := r.URL.Path[len(prefix):]
		secrets, ok := secretsByService[service]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		var resp vaultKVv2Response
		resp.Data.Data = secrets
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestVaultGetenv_UnsetReturnsGetenvUnchanged(t *testing.T) {
	base := map[string]string{"JWT_SECRET": "from-env"}
	getenv, err := vaultGetenv(func(k string) string { return base[k] })
	require.NoError(t, err)
	assert.Equal(t, "from-env", getenv("JWT_SECRET"))
}

func TestVaultGetenv_VaultValueWinsOverEnv(t *testing.T) {
	server := vaultTestServer(t, "root-token", map[string]map[string]string{
		"ledger-service": {"JWT_SECRET": "from-vault"},
	})
	defer server.Close()

	base := map[string]string{
		"VAULT_ADDR":  server.URL,
		"VAULT_TOKEN": "root-token",
		"APP_NAME":    "ledger-service",
		"JWT_SECRET":  "from-env",
	}
	getenv, err := vaultGetenv(func(k string) string { return base[k] })
	require.NoError(t, err)
	assert.Equal(t, "from-vault", getenv("JWT_SECRET"), "Vault must win when both are present")
}

func TestVaultGetenv_FallsThroughToEnvForKeysVaultDoesNotHave(t *testing.T) {
	server := vaultTestServer(t, "root-token", map[string]map[string]string{
		"ledger-service": {"JWT_SECRET": "from-vault"},
	})
	defer server.Close()

	base := map[string]string{
		"VAULT_ADDR":        server.URL,
		"VAULT_TOKEN":       "root-token",
		"APP_NAME":          "ledger-service",
		"JWT_SECRET":        "from-env",
		"POSTGRES_PASSWORD": "env-only-password",
	}
	getenv, err := vaultGetenv(func(k string) string { return base[k] })
	require.NoError(t, err)
	assert.Equal(t, "env-only-password", getenv("POSTGRES_PASSWORD"))
}

func TestVaultGetenv_NoSecretWrittenYetFallsBackToEnvEntirely(t *testing.T) {
	server := vaultTestServer(t, "root-token", map[string]map[string]string{})
	defer server.Close()

	base := map[string]string{
		"VAULT_ADDR":  server.URL,
		"VAULT_TOKEN": "root-token",
		"APP_NAME":    "never-seeded-service",
		"JWT_SECRET":  "from-env",
	}
	getenv, err := vaultGetenv(func(k string) string { return base[k] })
	require.NoError(t, err, "a 404 (nothing seeded yet) must never block boot")
	assert.Equal(t, "from-env", getenv("JWT_SECRET"))
}

func TestVaultGetenv_WrongTokenIsHardError(t *testing.T) {
	server := vaultTestServer(t, "correct-token", map[string]map[string]string{
		"ledger-service": {"JWT_SECRET": "from-vault"},
	})
	defer server.Close()

	base := map[string]string{
		"VAULT_ADDR":  server.URL,
		"VAULT_TOKEN": "wrong-token",
		"APP_NAME":    "ledger-service",
	}
	_, err := vaultGetenv(func(k string) string { return base[k] })
	require.Error(t, err, "a Vault that's configured but rejects the token must fail closed, not silently fall back")
}

func TestVaultGetenv_UnreachableAddrIsHardError(t *testing.T) {
	base := map[string]string{
		"VAULT_ADDR":  "http://127.0.0.1:1", // reserved, nothing listens here
		"VAULT_TOKEN": "any-token",
		"APP_NAME":    "ledger-service",
	}
	_, err := vaultGetenv(func(k string) string { return base[k] })
	require.Error(t, err)
}

func TestFetchVaultKV_ParsesKVv2Envelope(t *testing.T) {
	server := vaultTestServer(t, "root-token", map[string]map[string]string{
		"payin-service": {"JWT_SECRET": "s1", "POSTGRES_PASSWORD": "s2"},
	})
	defer server.Close()

	secrets, err := fetchVaultKV(server.URL, "root-token", "payin-service")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"JWT_SECRET": "s1", "POSTGRES_PASSWORD": "s2"}, secrets)
}
