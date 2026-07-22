# Runbook: mTLS Handshake Failure Response

Triggered by: a rise in `tlsx_handshake_failures_total` (docs/plan/49 K10) — server-side mTLS handshakes rejected by `pkg/tlsx.ServerConfig`'s `VerifyConnection`, labeled by `reason` and, when trustworthy, the peer's `identity`. A Grafana panel for this metric plus `tlsx_cert_expiry_seconds` lives on the "Seev / mTLS Security" dashboard (`deploy/observability/grafana/dashboards/mtls-security.json`).

Two reasons map to this runbook, and they mean very different things:

- **`reason="untrusted_ca"`** — the peer's certificate chain didn't verify against this server's current CA pool. `identity` is always `"unknown"` for this reason: the chain never verified, so the peer's claimed identity can't be trusted enough to even log, let alone act on.
- **`reason="identity_not_allowed"`** — the peer's cert chain verified fine (it *is* signed by our own CA), but its URI SAN identity isn't on this specific listener's allowlist (docs/plan/49 K4). `identity` here is real and safe to use — it necessarily comes from `cmd/certgen`'s closed set of known identities, since only our own CA could have signed it.

## Step 1 — Which reason is it?

```promql
sum by (reason, identity) (increase(tlsx_handshake_failures_total[15m]))
```

## Step 2a — `untrusted_ca`: is this just rotation propagation?

This is the **expected, self-healing** shape right after a `certgen rotate` (docs/plan/49 TM-13; see [cert-rotation.md](cert-rotation.md)): a brief, bounded spike (roughly 2× the 5s default poll interval) as newly-reissued clients reach servers that haven't polled the new CA yet.

1. Check whether a rotation happened recently (operator action, or `docs/runbooks/cert-rotation.md`'s drill/procedure log if one is kept). If yes, and the spike stopped within ~10-15s of the rotation, **this is expected — no action needed.**
2. If there was **no recent rotation**, or the failures are **sustained** past that window, treat it as a real incident:
   - **A genuinely untrusted peer is trying to connect** — someone/something presenting a cert not signed by this environment's CA at all. Identify the source IP from the affected process's own log line (`http: TLS handshake error from <addr>: ...`, or the gRPC server's equivalent) — `tlsx_handshake_failures_total` itself carries no source-IP label (deliberately: an unverified peer's own claims, including any header it might send, are exactly what's NOT trusted here — keying a Prometheus label off attacker-controlled input would be a cardinality-injection risk, see `pkg/tlsx/metrics.go`'s own doc comment).
   - **A misconfigured `TLS_CERT_DIR` mount** — a process pointed at the wrong certificate directory presents/trusts the wrong CA entirely. Check the affected process's `TLS_CERT_DIR` against the deployment's canonical cert directory.
   - **A stuck `CertSource`** that never picked up a past rotation at all — check its logs for `tlsx: cert reload failed, keeping previous cert in use` (see [cert-rotation.md](cert-rotation.md) Step 4).

## Step 2b — `identity_not_allowed`: expected allowlist gap, or something worse?

The peer's cert is legitimately signed by our own CA — this is not a spoofing scenario. Two real causes:

1. **A new, intended hop wasn't added to the allowlist.** Cross-reference the reported `identity` against `docs/plan/49-a6-internal-security.md`'s K4 matrix and the actual `allowedClientIdentities` argument passed to `ServerConfig` at the affected listener's call site (`cmd/*/main.go`). If a legitimate new caller was added to the topology without updating that allowlist, fix the allowlist — **identity, not signature alone, is the intended access control here (K4)**; don't "fix" this by loosening it to accept any CA-signed cert.
2. **A service is calling a hop it was never supposed to reach.** If the reported `identity` is NOT a caller this listener should ever expect, this may indicate a bug (wrong `GRPC_ADDR`/URL wired to the wrong target) or a compromised service attempting lateral movement. Escalate per Step 3 rather than widening the allowlist to make the symptom go away.

## Step 3 — Escalate

If Step 2a/2b can't be resolved within 15 minutes, or Step 2b.2 is suspected (a caller reaching a hop it shouldn't), escalate to an engineer with access to the affected services' deployment config and recent change history. Hand over:

- The exact `reason`/`identity` label values and the PromQL query's output from Step 1.
- Whether a rotation happened recently and, if so, when.
- The affected listener's current `allowedClientIdentities` (K4 allowlist) versus the identity that was rejected.

## Related

- [pkg/tlsx/metrics.go](../../pkg/tlsx/metrics.go), [pkg/tlsx/config.go](../../pkg/tlsx/config.go) — metric definitions and the `VerifyConnection` logic that increments them.
- [cert-rotation.md](cert-rotation.md) — the rotation procedure whose propagation window this runbook's `untrusted_ca` path distinguishes from a real incident.
- [docs/security/threat-model.md](../security/threat-model.md) TM-13, K4 — the bug this metric is partly designed to catch a recurrence of, and the identity-allowlist model K4 established.
