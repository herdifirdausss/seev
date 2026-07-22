package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// vaultGetenv wraps getenv with an overlay read from Vault's KV v2 engine
// (docs/plan/49 K7): when VAULT_ADDR and VAULT_TOKEN are both set, Vault's
// value wins for any key present in secret/<APP_NAME>; every other key
// falls through to getenv unchanged. Neither set — the default, and the
// only path CI/nightly ever exercise — returns getenv completely
// untouched, identical to today's plain-env behavior (same optionality
// pattern as REDIS_ENABLED).
//
// A Vault that's explicitly configured but unreachable or rejecting the
// token is a hard error, not a silent fallback to env: an operator who
// set VAULT_ADDR/VAULT_TOKEN is declaring intent to source secrets from
// Vault, and booting anyway on stale/wrong env values would be a worse
// surprise than failing loudly. A service simply never having written a
// secret yet (404) is not an error — it overlays nothing and every key
// falls through to env, so a fresh Vault dev instance never blocks boot.
func vaultGetenv(getenv func(string) string) (func(string) string, error) {
	addr := getenv("VAULT_ADDR")
	token := getenv("VAULT_TOKEN")
	if addr == "" || token == "" {
		return getenv, nil
	}
	service := getWithDefault(getenv, "APP_NAME", "seev")
	secrets, err := fetchVaultKV(addr, token, service)
	if err != nil {
		return nil, fmt.Errorf("config: vault fetch secret/%s: %w", service, err)
	}
	return func(key string) string {
		if v, ok := secrets[key]; ok && v != "" {
			return v
		}
		return getenv(key)
	}, nil
}

// vaultKVv2Response models Vault's KV v2 read envelope — GET
// {addr}/v1/secret/data/{path} nests the actual key/value map under
// data.data, distinct from KV v1's flatter data-only shape. Verified live
// against a real dev-mode Vault container (docs/plan/49 K7) before
// writing this struct.
type vaultKVv2Response struct {
	Data struct {
		Data map[string]string `json:"data"`
	} `json:"data"`
}

func fetchVaultKV(addr, token, service string) (map[string]string, error) {
	url := strings.TrimRight(addr, "/") + "/v1/secret/data/" + service
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	// No secret written for this service yet — not an error (a fresh dev
	// Vault instance with nothing seeded must never block boot); every key
	// simply falls through to env.
	if resp.StatusCode == http.StatusNotFound {
		return map[string]string{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("vault returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed vaultKVv2Response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return parsed.Data.Data, nil
}
