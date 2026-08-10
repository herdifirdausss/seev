package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestValidateResolvedStagingValues(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	input := `
global:
  appEnv: staging
  image: {digest: ` + digest + `}
  postgres: {host: db.staging.example, sslMode: verify-full}
  redis: {addr: cache.staging.example:6379}
  rabbitmq: {host: broker.staging.example}
data: {enabled: false}
networkPolicy:
  managedDataCIDRs:
    postgres: [10.50.1.0/24]
    redis: [10.50.2.0/24]
    rabbitmq: [10.50.3.0/24]
migrations:
  sslMode: verify-full
  image: {digest: ` + digest + `}
egress: {replicas: 2}
traefik: {allowedCallbackCIDRs: [203.0.113.0/24]}
services:
  ledger:
    enabled: true
    replicas: 2
    network: {ingressFrom: [], egressToServices: [], datastores: {postgres: true}}
  vendor:
    enabled: true
    replicas: 2
    egressProxyRequired: true
    callbackCIDRs: 203.0.113.0/24
    trustedProxyCIDRs: 10.40.0.0/16
    network: {ingressFrom: [], egressToServices: [], datastores: {postgres: true}}
    env: {VENDOR_MOCKVENDOR_ENABLED: "false", MOCKVENDOR2_ENABLED: "false"}
`
	values := map[string]any{}
	require.NoError(t, yaml.Unmarshal([]byte(input), &values))
	require.Empty(t, validate("staging", values))
}

func TestValidateRejectsPromotionBlockers(t *testing.T) {
	values := map[string]any{
		"global": map[string]any{"appEnv": "staging", "image": map[string]any{"digest": "sha256:REPLACE_WITH_DIGEST"}},
		"data":   map[string]any{"enabled": true},
	}
	failures := strings.Join(validate("staging", values), "\n")
	require.Contains(t, failures, "unresolved placeholder")
	require.Contains(t, failures, "data.enabled must be false")
	require.Contains(t, failures, "managed endpoint")
}
