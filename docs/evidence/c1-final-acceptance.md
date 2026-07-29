# C1 Final Acceptance Evidence — Plan 57 T10

> [Documentation home](../../README.md) · [Roadmap](../roadmap/README.md) ·
> [Plan 57](../roadmap/active/57-c1-merchant-b2b-api.md)

This is the T10 final-verification evidence required by
[Plan 57 §28](../roadmap/active/57-c1-merchant-b2b-api.md#28-documentation-deliverables)
before C1 can be considered complete. See
[Plan 57's own T10 Result section](../roadmap/active/57-c1-merchant-b2b-api.md#t10--verification-chaos-and-release-evidence)
for the full narrative; this document is the command-log/evidence
counterpart.

**Disposition: core-complete, plan stays active.** Two follow-up gaps
(§23.8 race-test items 2-6, and non-precision-targeted chaos coverage in
§24.2-24.6) are deferred as "T10b" — the plan is not archived until those
close, matching the same pattern A8 used for its own T6/T6b split.

## Two bugs found and fixed this pass

1. **T9's global kill switch was never wired to the B2B router.** Commit
   `d818370`. `RequireB2BEnabled` existed since T9 but no B2B route existed
   to gate; still never mounted once T10's router landed. Verified live via
   `merchant-e2e.sh` before/after, plus `TestB2BRouter_GlobalKillSwitchGatesEveryRoute`.
2. **Live-activation bypass via key/tenant environment mismatch.** Commit
   `86b4824`. `KeyService.CreateKey` never checked a requested key's
   environment against its tenant's own `Environment`, so a sandbox tenant
   (auto-activates, no checker approval) could receive a "live" key,
   bypassing the maker/checker draft→active gate a real live tenant
   requires. Fixed at both the issuance layer (`CreateKey`) and the
   request-authentication layer (`RequireMerchantAuth`), each independently
   fail-closed.

## Final gate command log

Run from the repository root, after freeing disk space (95%→61%, ~11 GB of
stale `/tmp/seev-*` build/lint/module caches from this session's own
verification work) and removing 67 leaked testcontainers instances (66
Postgres, 1 RabbitMQ) that were causing unrelated transient failures.

```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ make lint
"golangci-lint run ./..."
0 issues.

$ make contracts
go run ./cmd/contractgenerate
go test ./api/contracts        ok
go run ./cmd/contractcheck -mode breaking
go test ./pkg/httpcontract ./api/contracts   ok

$ go run ./cmd/doccheck
doccheck: checked 139 Markdown files; required guides, language,
visual and interactive assets, local links, and anchors are valid

$ go test ./...
91 packages ok, 0 failures (packages with no test files excluded)

$ go test -race ./...
91 packages ok, 0 failures, 0 data races reported

$ go test -tags=integration ./...
0 failures (after the disk-space/container cleanup above; the initial
run before cleanup showed one transient pkg/database testcontainers
timeout, confirmed non-reproducible by an isolated re-run before the
cleanup, and fully clean after)

$ ./scripts/smoke-test.sh all
=== ALL SMOKE ASSERTIONS PASSED === (19/19)

$ ./scripts/business-e2e.sh
=== FULL BUSINESS JOURNEY PASSED === (84/84)

$ ./scripts/admin-e2e.sh
admin-e2e completed (5/5)

$ ./scripts/merchant-e2e.sh
merchant-e2e completed (25/25, includes the new global-kill-switch leg)

$ ./scripts/chaos-test.sh 21
=== ALL CHAOS ASSERTIONS PASSED ===
(new merchant B2B transfer kill-9-mid-posting scenario)

$ ./scripts/privacy-e2e-host.sh
FAILS reproducibly (2/2 runs) at the closure leg — see "Known issue" below.
```

## Known issue: `privacy-e2e-host.sh` (out of this plan's scope)

Reproduced twice: the script dies silently right after "registered
closure-leg user" is printed, before "closure requested." Log analysis
(`assurance-service.log` showing a scheduled-run connection-refused to
payin-service's gRPC port at almost the same instant `payin-service.log`
shows a graceful shutdown) points to `scripts/privacy-e2e.sh`'s own
`assurance_run()` helper manually triggering an assurance run via
`curl -sf`, racing the `ASSURANCE_INTERVAL=1s` background scheduler that
`privacy-e2e-host.sh` also configures — a collision (likely a 409 from the
manual trigger) kills the script under `set -e`, which tears down every
service mid-run.

```text
$ git log --oneline -- scripts/privacy-e2e.sh scripts/privacy-e2e-host.sh internal/assurance
```

confirms none of these files have been touched by any commit in Plan 57 —
this is pre-existing tooling from the (already archived) plan 51
data-lifecycle-privacy work, unrelated to the merchant B2B surface. Filed
as its own follow-up task rather than fixed here, since fixing it is
outside this plan's blast radius.

## Cross-tenant matrix (§23.7) — audit result

| Case | Status |
|---|---|
| Tenant A reads tenant B resource | covered |
| Tenant A mutates tenant B resource | covered |
| Tenant A reuses tenant B idempotency key text | covered |
| Tenant A replays tenant B delivery | covered |
| Tenant A targets tenant B source account | not applicable — source account is always server-derived from the caller's own tenant, never a request field |
| Test key accesses live tenant | fixed this pass (`86b4824`) |
| Live key accesses sandbox tenant | fixed this pass (`86b4824`) |
| Suspended tenant reads | covered, but stricter than the plan's stated default (fails closed uniformly rather than allowing reads) — T10b |
| Suspended tenant writes | covered (see above) |

## Race tests (§23.8) — audit result

`go test -race ./internal/merchant/...` (plain and `-tags=integration`) is
clean. Of the 7 required scenarios: concurrent-same-idempotency-key is
fully covered (3 tests); duplicate-owner-events is covered functionally but
not under real goroutine contention; the remaining five (key
rotation/revocation vs. request, concurrent webhook workers, concurrent
replay, concurrent endpoint-disable vs. delivery, concurrent tenant
suspension vs. financial write) have no test coverage — deferred as T10b.

## Secret scan

- `merchant_api_keys.secret_digest` — `bytea` (HMAC digest, not plaintext).
- `merchant_webhook_endpoints.secret_ciphertext` — `bytea` (encrypted).
- No plaintext secret column exists in the schema.
- Every service log from a full `merchant-e2e.sh` run grepped for the
  `mk_sandbox_`/`mk_live_` API-key prefix pattern: zero matches.

## Residual risks (T10b, tracked as follow-up work)

1. Race tests for key rotation/revocation-vs-request, concurrent webhook
   workers, concurrent replay, concurrent endpoint-disable-vs-delivery, and
   concurrent tenant-suspension-vs-financial-write.
2. Chaos scenarios precisely targeting merchant-specific RabbitMQ-down,
   Redis-down, webhook-receiver-down, and database-failure cases (currently
   covered only generically, via the shared ledger/payin/payout/webhook
   machinery's own pre-existing chaos scenarios).
3. Suspended-tenant read-vs-write policy: currently fails closed uniformly;
   §23.7's stated default policy allows reads for reconciliation.
4. `scripts/privacy-e2e-host.sh`'s pre-existing `assurance_run()` /
   `ASSURANCE_INTERVAL` race (tracked separately, not a Plan 57 item).
