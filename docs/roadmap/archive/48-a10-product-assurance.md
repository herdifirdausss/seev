# 48 — Track A10: Product Assurance and Emergency Intake Control

> Derived from track **A10** in [42-long-term-roadmap.md](../42-long-term-roadmap.md).
>
> **Status: complete.** T0–T6 were implemented and verified against the live
> repository. The final acceptance and chaos gate passed on 2026-07-20.

## 1. Trigger and objective

The ledger verifier proves double-entry and balance invariants inside
`seev_ledger`, but it cannot prove that the pay-in and payout state machines
still agree with the ledger. A crash between two best-effort updates could
leave a webhook, intent, payout, vendor command, or ledger transaction in an
inconsistent state.

Track A10 adds a read-only assurance service that continuously compares
pay-in, payout, and ledger projections. It stores durable evidence, exposes
operator findings, and provides an explicit emergency control for pausing new
intake. It never corrects domain data automatically and never pauses traffic
automatically.

Success criteria:

1. Incremental scans run every 60 seconds with a two-minute consistency delay
   and use lossless keyset pagination, including rows with equal timestamps.
2. A new critical mismatch is detected within three minutes after it becomes
   eligible for scanning when dependencies are healthy.
3. Dependency or persistence failures never advance a cursor or falsely
   resolve a finding.
4. A repaired finding becomes `resolved`, remains in the audit history, and
   reopens if the same mismatch returns.
5. Pausing intake takes no more than five seconds, rejects only new requests,
   and does not stop in-flight webhooks, workers, settlement, cancellation,
   replay, reconciliation, or reversal work.

## 2. Scope and boundaries

The assurance service is deliberately narrow:

- It reads pay-in, payout, and ledger data only through authenticated gRPC
  assurance contracts.
- It has its own database, `seev_assurance`, and has no credentials for any
  domain database.
- It does not replace the ledger's double-entry verifier.
- It does not change transfer ordering, lifecycle guards, fee-quote
  consumption, fraud/KYC decisions, or vendor pinning.
- It does not implement compliance rules from Plan 46 or an operator UI from
  Plan 47; it provides an internal API, CLI, metrics, and runbook.
- It does not perform automatic correction, reversal, intake pause, or pause
  expiry.
- Evidence never includes raw webhook bodies, payout destinations, secrets,
  credentials, KYC documents, or unnecessary PII.
- Monetary values remain integer minor units. No floating-point money is used.

## 3. Locked design decisions

### K1 — A separate, read-only service

The service runs as `assurance-service` on port `8096` in Compose and
`18096` for the host-binary workflow. It exposes an internal HTTP API and no
public listener or domain gRPC server. Its database role is `assurance_app`.

The assurance connection pool is bounded at five open and five idle
connections. The service has a 128 MiB container memory limit and may import
generated API clients and shared packages, but not `internal/payin`,
`internal/payout`, or `internal/ledger`.

### K2 — Additive, allowlisted owner contracts

The owner services expose additive read-only RPCs:

```text
payin.v1.PayinService.ListAssuranceRecords
payout.v1.PayoutService.ListAssuranceRecords
ledger.v1.LedgerService.BatchGetAssuranceTransactions
```

Pay-in and payout list requests use `cursor_updated_at`, `cursor_id`,
`cutoff`, and `page_size`. Pages are ordered by
`(effective_updated_at, id)`, capped at 500 records, and filtered to the
snapshot cutoff. The cursor must be supplied as a complete tuple or omitted.

The pay-in contract exposes only the fields needed to correlate intents,
webhook events, and ledger postings. Raw webhook payloads, headers, and
vendor error bodies never cross the boundary.

The payout contract exposes request state, hold and closing transaction IDs,
fee-quote information, and summarized vendor calls and commands. It never
exposes destination data or raw request/response/error payloads.

The ledger batch contract accepts up to 500 selectors by transaction ID,
idempotency tuple, or `(type, gateway, external_ref)`. It returns only
proof-safe transaction fields, lifecycle-closer information, and fee proof
derived from fee ledger entries. It also returns a limited fee-quote proof so
the assurance service can verify that a quote was consumed by the expected
`payout:<request-id>` reference.

### K3 — Cutoff, scheduler, and atomic progress

Each run sets `cutoff = started_at - 2 minutes`. Newer rows are left for a
later run rather than being evaluated while their owner-side state may still
be changing.

Pay-in and payout scans may run concurrently, but pages within each source
are processed serially. Every owner and ledger RPC has a three-second timeout.
For each page, the service lists owner records, gathers ledger selectors,
loads proof, evaluates rules, persists findings and alerts, records progress,
and advances the cursor as one assurance transaction.

The cursor advances only after the complete page succeeds. Malformed pages,
RPC failures, evaluator failures, and database failures leave the previous
cursor intact. A missing page caused by an unavailable dependency is never
treated as proof that an old finding has been resolved.

### K4 — Durable finding lifecycle

The assurance database stores runs, per-source cursors, findings, alert
deliveries, and intake-control commands. Findings have a stable SHA-256
fingerprint derived from the rule, resource, amount, and currency. Repeated
observations increment `occurrence_count` instead of creating duplicate rows.

Finding states are:

```text
open → acknowledged → resolved
```

An active finding can be acknowledged for investigation, but acknowledgement
does not remove it from money-at-risk. Healthy proof resolves it. If the
violation returns, the same finding reopens and retains its history.

### K5 — Backfill without an alert storm

The first run performs a historical backfill. Baseline findings are stored
normally but suppress duplicate alert delivery until the backfill is
complete. Backfill is complete only after pay-in, payout, and ledger progress
reaches the cutoff successfully.

Critical baseline findings remain visible and must be acknowledged or
resolved by an operator, but they do not make `/ready` fail. This separates
known historical evidence from service health.

### K6 — Deterministic rules and safe evidence

The rule engine is pure and table-driven. It accepts owner projections and
ledger proof, then returns findings with a fixed rule code, severity, amount,
currency, and allowlisted evidence. It never serializes an entire owner DTO.

`amount_minor` represents the exposure associated with the finding. Active
high and critical findings are summed by currency to produce money-at-risk,
deduplicated by fingerprint. Medium correlation findings have zero exposure.

### K7 — Durable alerts without automatic action

New, reopened, and severity-escalated high or critical findings create a
durable alert-delivery record. The dispatcher retries failed webhook delivery
with backoff. Repeated scans without a state transition do not send another
alert. Alert failure never rolls back proof persistence or cursor progress.

Alert payloads contain only the rule, severity, finding/resource identifiers,
amount and currency where relevant, and an operator URL. Alert delivery
never invokes pause, correction, or reversal.

### K8 — Owner-side emergency intake control

Pay-in and payout each own a singleton intake-control row with a monotonic
revision and an idempotent command log. The create-intent and create-payout
paths check this state immediately before their side effects.

Pausing blocks exactly these operations:

- creating a new top-up intent;
- creating a new payout.

Already-paid webhooks, payout workers, settlement, cancellation, replay,
reconciliation, reversal, and read operations continue normally. The owner
database remains the source of truth; the assurance database only
orchestrates commands and records their results.

### K9 — Two-person resume

An admin or admin maker may request a resume. A different admin or admin
checker must approve it. The requester and approver can never be the same
principal, including when both use the `admin` role. Approval is idempotent,
uses the expected owner revision, and is considered successful only after the
owner confirms persistence.

If assurance is unavailable, an owner exposes a direct pause endpoint for an
`admin` principal. There is no direct-resume endpoint. Recovery must use the
two-person assurance flow.

### K10 — Internal operator API

The service provides:

```text
GET  /admin/assurance/summary
GET  /admin/assurance/findings
GET  /admin/assurance/runs
POST /admin/assurance/runs
POST /admin/assurance/findings/{id}/acknowledge
POST /admin/assurance/findings/{id}/resolve
GET  /admin/assurance/intake
POST /admin/assurance/intake/{flow}/pause
POST /admin/assurance/intake/{flow}/resume-requests
POST /admin/assurance/intake/{flow}/resume-requests/{id}/approve
```

Routes require the appropriate admin role. Finding lists support status,
severity, rule, currency, and bounded pagination filters. Mutating requests
carry an actor, reason, and idempotency key.

### K11 — Low-cardinality observability

Metrics cover run duration and failures, cursor lag, finding count, money at
risk, detection and resolution delay, alert delivery, and intake commands.
Labels are limited to fixed sources, severities, rules, currencies, actions,
and statuses; request IDs and resource IDs are never metric labels.

`/health` checks the process and assurance database. `/ready` checks the
database, owner gRPC dependencies, and backfill state. An active finding does
not make readiness fail. Grafana and the product-assurance runbook expose the
same operational model.

## 4. Rule catalog

All comparisons use exact uppercase currency codes and integer minor units.
The consistency delay is applied before evaluation so a normal in-flight
transition does not produce a false positive.

### Pay-in rules

| Rule | Invariant | Severity |
| --- | --- | --- |
| PA01 | A posted webhook has exactly one matching posted `money_in` ledger transaction, including amount, currency, gateway, and external reference. | Critical |
| PA02 | A settled intent points to a posted webhook with matching user, amount, currency, and reference. | Critical |
| PA03 | A ledger posting is present while the related intent remains pending after the consistency delay. | High |
| PA04 | A failed or blocked webhook has a posted `money_in` transaction. | Critical |
| PA-CORR | A required request correlation ID is missing without another money mismatch. | Medium |

### Payout rules

| Rule | Invariant | Severity |
| --- | --- | --- |
| PO01 | A payout beyond the initial state has a matching posted `withdraw_initiate` hold. | Critical |
| PO02 | A settled payout has a matching `withdraw_settle` closer, or a cancelled payout has a matching `withdraw_cancel` closer. | Critical |
| PO03 | A rejected payout has no hold, and a failed payout does not leave a hold open. | Critical |
| PO04 | A terminal payout has no live vendor command, and a request has no competing live commands. | High or critical |
| PO05 | A submitted/vendor-pending payout is stuck beyond 15 minutes or has a dead vendor command. | High |
| PO06 | A vendor does not change after an accepted or uncertain attempt. | Critical |
| PO07 | The fee quote, payout fee, and booked ledger fee agree and the quote was consumed by the same payout. | Critical |
| PO-CORR | A required request correlation ID is missing without another money mismatch. | Medium |

## 5. Implementation results

### T0 — Scope and inventory

The repository inventory, service name, ports, database role, boundaries,
and dependency on the completed admin maker/checker roles were verified on
2026-07-19. The plan index and long-term roadmap were updated.

### T1 — Owner assurance contracts

Added the additive pay-in, payout, and ledger RPCs, generated code, bounded
keyset pagination, cutoff validation, sensitive-field masking, lifecycle
proof, fee proof, and duplicate-posting detection. Proto generation, lint,
breaking-change checks, and contract tests passed.

### T2 — Assurance engine and persistence

Added `cmd/assurance-service`, the assurance module, its database migration,
backfill and incremental cursors, three-second RPC timeouts, deterministic
finding persistence, metrics, health/readiness endpoints, Compose wiring,
bootstrap scripts, CI build enumeration, and the operator API foundation.

### T3/T4 — Rule engine and alerting

Implemented PA01–PA04 and PO01–PO07 as pure rule functions using integer
minor-unit values and SHA-256 fingerprints. Added evidence allowlisting,
deduplication, reopen and escalation behavior, durable alert retries, and
webhook delivery without scan rollback or automatic domain action.

### T5 — Emergency intake control

Added owner migrations `payin/000006_intake_control` and
`payout/000007_intake_control`, owner-side revision checks, idempotent
commands, gRPC control methods, assurance pause/resume orchestration, direct
admin-only pause fallback, and two-person resume approval.

### T6 — Operations and verification

Added `scripts/product-assurance.sh`, Grafana panels, the
[product-assurance runbook](../../operations/runbooks/product-assurance.md), filters and
role checks, and chaos scenarios for assurance restart, ledger outage, and
owner outage during intake pause.

The final gate on 2026-07-20 passed:

- unit, integration, race, vet, lint, and proto checks;
- real-Postgres equal-timestamp pagination with 600 records;
- smoke, business E2E, and container checks;
- chaos scenarios 1–14, including scenarios 12–14 for assurance;
- assurance health, readiness, metrics, CLI summary/list/run, and
  clean-volume startup.

## 6. Operational constraints

1. The owner state is authoritative for intake status.
2. A successful empty page is different from a dependency failure; only the
   former may contribute to cursor progress or finding resolution.
3. Offset pagination is forbidden; the timestamp-plus-ID tuple must match the
   owner `ORDER BY` exactly.
4. Full gRPC responses, raw payloads, secrets, and credentials must not be
   written to logs.
5. Acknowledge is not resolve, and manual resolve is not permanent
   suppression.
6. Tests must inject read-model or correlation faults rather than modifying
   ledger entries directly.
7. The assurance container must never receive a domain database DSN.

## 7. Definition of Done

- [x] The service, database, role, ports, migrations, configuration, Compose,
      CI, bootstrap, boundary map, dashboard, runbook, and health checks are
      complete.
- [x] Assurance has no domain database access and no sensitive fields in its
      wire contracts, evidence, logs, or metrics.
- [x] All pay-in and payout rules are active with durable findings,
      money-at-risk, alert retries, reopen behavior, and backfill suppression.
- [x] Cursor semantics are lossless and dependency failures preserve work.
- [x] Emergency pause/resume provides revision checks, idempotency, retries,
      owner persistence, and two-person approval without stopping in-flight
      work.
- [x] The API, CLI, metrics, dashboard, and runbook support operators without
      requiring a UI.
- [x] Functional, integration, race, smoke, business E2E, chaos, container,
      proto, lint, vet, and local CI-equivalent gates are green.
