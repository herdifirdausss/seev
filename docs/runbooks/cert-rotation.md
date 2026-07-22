# Runbook: mTLS Certificate Rotation

Covers rotating the internal mini-CA and every service's leaf certificate (docs/plan/49 K2/K3/K9) — `cmd/certgen`'s SPIFFE-style identities used for every gRPC and internal-HTTP hop across all eight services. Leaves are short-lived by design (72h TTL, docs/plan/49 K3) so rotation is a routine, well-exercised operation, not a rare break-glass procedure.

## When to run this

- Scheduled: well before a leaf's 72h TTL expires. In this repo's dev/CI harness, `certgen init-ca`/`issue` run fresh on every `make certs`/`scripts/lib.sh` bootstrap, so rotation-in-place (this runbook) is the drill/procedure for a **long-lived environment** where the process isn't restarted often enough for a fresh boot to naturally reissue certs.
- Ad-hoc: after a suspected private key compromise (rotating invalidates every previously-issued leaf and, once the CA itself is regenerated, every leaf signed by the old CA — see [Step 2](#step-2--rotate)).
- Whenever `docs/security/threat-model.md`'s TM-13 fix (`pkg/tlsx/config.go`'s `ServerConfig`) or `cmd/certgen`'s issuance logic changes — a live drill is the only thing that proves rotation still behaves correctly, not just "the code compiles."

## Prerequisites

- The `certgen` binary built from this repo's `cmd/certgen` (`go build -o certgen ./cmd/certgen`, or `scripts/lib.sh`'s `build_server` for a full dev-stack build).
- Write access to the certificate directory every relevant process was started with `TLS_CERT_DIR` pointed at (`deploy/certs/` in Compose, `$CERT_DIR` in the test harness).
- For a live drill specifically: `scripts/rotation-drill.sh` (standalone, builds and runs its own throwaway ledger-service instance — does not touch a real running stack).

## Step 1 — Understand what "rotate" actually does

`certgen rotate --out <dir>` regenerates the CA itself (new key, new cert) **and** reissues every known-service leaf against it, in one pass. This is deliberately more aggressive than `certgen issue` (which only reissues one service's leaf against the *existing* CA) — a CA rotation is what actually invalidates certificates that might have leaked, since a leaf alone being reissued doesn't stop an attacker who has the CA's own private key from minting new valid leaves.

## Step 2 — Rotate

```bash
./certgen rotate --out /path/to/cert/dir
```

Every running process whose `TLS_CERT_DIR` points at that same directory picks up the new CA + its own new leaf via `pkg/tlsx`'s poll-based hot-reload (`defaultPollInterval = 5s`) — **no process restart, no listener rebind**. This is true for both directions independently:

- **Outgoing dials** (`pkg/tlsx.ClientConfig`) re-verify the peer's chain fresh on every handshake — always current, no propagation lag on this side.
- **Incoming connections** (`pkg/tlsx.ServerConfig`) also re-verify fresh on every handshake as of the TM-13 fix — but a server can only present/trust what its *own* `CertSource` has most recently polled off disk, so there is a bounded (≤ ~1× poll interval, ~5s worst case, empirically ~2s observed live) window right after rotation where a **freshly-reissued** client might be transiently rejected by a server that hasn't polled yet. This is expected and self-healing — see [Step 4](#step-4--if-it-doesnt-self-heal).

## Step 3 — Verify

Run the drill:

```bash
./scripts/rotation-drill.sh
```

It proves, against a real (throwaway) `ledger-service` instance:

1. **Zero-downtime**: no request fails before rotation; every transient failure lands within a bounded grace window (2× the default poll interval) right after `certgen rotate`; the loop's tail after that window is a clean, sustained run of successes.
2. **Actual rotation**: a certificate captured *before* rotation is rejected by the server *after* it (TLS handshake failure, not merely "a new cert also happens to work").

A `[ FAIL]` on either property is a real regression — do not treat it as drill flakiness without first checking `pkg/tlsx`'s own test suite (`go test ./pkg/tlsx/... -race`) and the target process's logs for `tlsx: cert reload failed` entries.

## Step 4 — If it doesn't self-heal

If `tlsx_handshake_failures_total{reason="untrusted_ca"}` (see [handshake-failure-response.md](handshake-failure-response.md)) keeps climbing well past the expected grace window (more than a couple of poll intervals, so more than ~10-15s):

1. Check the affected process's logs for `tlsx: cert reload failed, keeping previous cert in use` — this means its `CertSource` is stuck on a read error (e.g. a permissions problem, a truncated file from an interrupted write) and will **never** self-heal until the underlying read error is fixed. The previous (still valid, pre-rotation) cert stays in use safely in the meantime — this fails closed, not open.
2. Confirm the cert directory the stuck process is watching (`TLS_CERT_DIR`) is actually the *same* directory `certgen rotate` wrote to — a mismatched or stale mount is the most common real-world cause of "rotation ran but nothing changed."
3. If the read error can't be resolved live, restart the affected process — `NewCertSource` fails loudly (refuses to boot) if it can't load a valid cert/key/CA at startup, so a restart either fixes itself or gives an unambiguous boot-time error to act on.

## Related

- [pkg/tlsx/config.go](../../pkg/tlsx/config.go), [pkg/tlsx/source.go](../../pkg/tlsx/source.go) — the hot-reload + verification implementation.
- [cmd/certgen/main.go](../../cmd/certgen/main.go) — `init-ca` / `issue` / `rotate`.
- [scripts/rotation-drill.sh](../../scripts/rotation-drill.sh) — the live proof this runbook tells you to run.
- [docs/security/threat-model.md](../security/threat-model.md) TM-13 — the frozen-`ClientCAs` bug this rotation model's server side used to have, found by this exact drill.
- [handshake-failure-response.md](handshake-failure-response.md) — what to do when the metrics this rotation affects start alerting.
