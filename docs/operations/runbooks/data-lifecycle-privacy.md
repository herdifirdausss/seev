# Runbook: Data Lifecycle and Privacy (Retention, Export, Closure)

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: operators.** This is an engineering privacy
> baseline (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md), not a claim of GDPR, Indonesian
> regulatory, or any other formal legal compliance. Follow this procedure
> only in an environment where you are authorized to touch identity,
> credential, or financial-reference data.

Covers the six operational situations Track A8 (data lifecycle and
privacy) produces: an active retention hold blocking cleanup, a failing
retention purge/redact class, a failed user-export request, a stuck or
dead-lettered account-closure request, a key-version mismatch on any of
the three dedicated `pkg/cryptox.Ring`s this track introduced, and a
failing object-store delete. Backup residuals (what an already-taken A7
backup may still contain after a redaction/closure, K12) are covered by
[dr-restore-drill.md](dr-restore-drill.md)/[backup-failure.md](backup-failure.md)/
[repository-corruption.md](repository-corruption.md), not duplicated here.
See
[docs/roadmap/archive/51-a8-data-lifecycle-privacy.md](../../roadmap/archive/51-a8-data-lifecycle-privacy.md)
for the full design (K1–K13) — this runbook is the "what to do when it
fires" companion, not the design reference.

**Current scope note:** the account-closure saga is implemented for Auth and
the five end-user data owners registered by `auth-service`: Ledger, Payin,
Payout, Fraud, and Gateway/Notify. Admin BFF owns operator-session/audit data,
not ordinary user records; Assurance stores resource evidence rather than a
user identity, so neither is a closure owner. Maker/checker operator
offboarding is available through Auth's admin privacy endpoints and reuses the
same closure saga. Final multi-service/chaos evidence remains an operator
release gate.

## Situation 1 — An active retention hold is blocking cleanup or closure

**Symptom:** a retention run's `dry_run` count includes rows you expected
purged, or `RequestClosure`/the closure saga reports `blocked` with a
reason like `"N active retention hold(s)"`.

1. Find the hold:
   ```sql
   SELECT id, scope, scope_value, reason_code, reason_note, created_by, created_at, expires_at
   FROM <service>_retention_holds WHERE status = 'active';
   ```
   (Table is prefixed per owner — `auth_retention_holds`,
   `ledger_retention_holds`, etc. — K5's own per-service-copy design; a
   hold created via one service's admin endpoint is NOT automatically
   visible to another owner unless that owner was also told to hold.)
2. Confirm the hold is genuinely still needed (legal/compliance reason)
   before touching it — this table is the last line of defense against an
   unauthorized purge, not a convenience lock to route around.
3. To release: use the admin release endpoint (requires a **different**
   admin/admin_checker identity than whoever created it — enforced both in
   Go and by `chk_<table>_hold_releaser_not_creator` at the database
   level, K5). A direct `UPDATE ... SET status='released'` bypassing that
   constraint will be rejected by the CHECK constraint if `released_by`
   equals `created_by`.
4. Re-run the retention class or closure request after release — both
   re-check the hold table fresh on their next attempt, no cache to
   invalidate.

## Situation 2 — A retention purge/redact class is failing

**Symptom:** `SeevRetentionRunFailing` or `SeevRetentionRunStale` fires
(`deploy/observability/prometheus/rules/retention.yml`).

1. Check which class and owner:
   ```promql
   increase(seev_retention_runs_total{result="error"}[1h])
   ```
   grouped by `owner`/`action` in the alert labels.
2. Check the service's own logs around the failure — `pkg/retentionworker`
   logs the class name and the underlying SQL error, never row-level data
   (K13's own no-PII-in-logs constraint).
3. Common causes:
   - **A SECURITY DEFINER purge/redact function was renamed or dropped** —
     `pkg/retentionworker.Runner`'s `Class{FunctionName: ...}` is a fixed
     literal checked at `NewRunner` construction time (a typo here panics
     at boot, not silently at first run) — if this is failing at RUNTIME
     instead, the function existed at boot but was removed later (a bad
     migration rollback). Check `\df fn_retention_purge_*`/`fn_retention_purge_*_redact`
     in the target database.
   - **The app role lost EXECUTE on the function** — retention functions
     are `SECURITY DEFINER` specifically so `app_service` never needs
     direct table-level DELETE/UPDATE (K4's own least-privilege design);
     confirm with `\df+ fn_retention_purge_<class>` that `app_service` is
     still in the ACL.
   - **A genuinely new blocking condition** (unexpected FK, a new column
     the function doesn't know about) — this is a real schema-drift bug,
     escalate rather than working around it with a manual `DELETE`.
4. Never purge manually with a raw `DELETE`/`UPDATE` to "unblock" the
   alert — every purge/redact path is a `SECURITY DEFINER` function
   specifically so retention actions are auditable and consistent; a
   manual bypass produces neither.

## Situation 3 — A user's export request failed or never left 'collecting'

**Symptom:** `SeevPrivacyExportStuckCollecting` fires, or a user reports
`GET /api/v1/users/me/privacy/requests/{id}` stuck at `status: "failed"`.

1. Look up the request (never log/print `error_message` verbatim in a
   ticket visible outside the operator — it can legitimately be empty of
   PII, but treat it as sensitive by default):
   ```sql
   SELECT id, user_id, status, requested_at, ready_at, error_message
   FROM privacy_requests WHERE request_type = 'export' AND id = '<request-id>';
   ```
2. `status='collecting'` with no progress for >15 minutes means the
   assembly worker crashed mid-build (or was never running —
   `StartPrivacyExportWorker` returns `(nil, nil)` with no error if
   `EXPORT_KEK_V<N>`/document-store config is missing, which silently
   disables the whole feature — check the service actually started the
   worker in its own boot logs). Restarting `auth-service` is safe:
   `AssembleOnePendingExport` re-claims any `pending` row, but a row stuck
   in `collecting` from a crashed attempt needs a manual reset first:
   ```sql
   UPDATE privacy_requests SET status = 'pending', updated_at = now()
   WHERE id = '<request-id>' AND status = 'collecting';
   ```
   (Safe: nothing was ever uploaded before `status='ready'` is set —
   `buildAndUploadExport` builds entirely in memory before the one upload
   call, so a re-run from `pending` never leaves a duplicate object
   behind.)
3. `status='failed'` — read `error_message` (truncated to 500 chars,
   `internal/auth/privacy_worker.go`'s `truncateErrorMessage`). The most
   common real cause is a document-store outage during upload; retry by
   resetting to `pending` the same way as step 2 once the store is back.
4. Never construct a decrypted export by hand as a workaround — the whole
   point of the export ring's per-request AAD binding is that only the
   worker's own code path can correctly seal/open it; a manual export
   bypasses the manifest/exclusions machinery K9 requires.

## Situation 4 — A closure request is stuck or dead-lettered

**Symptom:** `SeevPrivacyClosureStuck` or `SeevPrivacyClosureDead` fires
(`deploy/observability/prometheus/rules/retention.yml`).

1. Look up the request and its checkpoint progress:
   ```sql
   SELECT id, user_id, status, retry_count, next_attempt_at, last_error, owner_checkpoints
   FROM privacy_requests WHERE request_type = 'closure' AND id = '<request-id>';
   ```
   `owner_checkpoints` tells you exactly how far the saga got —
   `{"ledger": {"phase": "prepared"}}` means ledger's `Prepare` already
   succeeded and the NEXT call will attempt `Commit`; `{"ledger":
   {"phase": "committed", ...}}` means only auth's own local finalize
   step remains.
2. **`status='blocked'`** — this is NOT a bug. It means a K10 blocking
   condition genuinely fired (`last_error` names it: non-zero balance,
   open transaction lifecycle, active schedule/disbursement, pending KYC,
   active retention hold). The user must resolve the underlying condition
   and submit a **new** closure request — `blocked` is terminal by design,
   never auto-retried (see Situation 1 above if the blocker is a
   retention hold specifically).
3. **`status IN ('preparing','committing')` stuck >30 minutes** — the
   worker itself likely isn't ticking (check `auth-service`'s own health/
   liveness, and confirm `StartClosureWorker` actually started — same
   silent-disable-if-unconfigured caveat as export above, gated on
   `CLOSURE_KEK_V<N>` + the ledger closure client being wired). Restarting
   `auth-service` is safe to attempt: `ProcessOnePendingClosure` resumes
   from the row's current `status` — it never re-does a step whose owner
   checkpoint already recorded success, and ledger's own `Commit` is
   independently idempotent regardless (`TestClosure_Commit_IdempotentUnderReplay`
   proves replaying it changes nothing).
4. **`status='dead'`** — `retry_count` reached the cap (5) on a
   **transient** failure (network/DB, not a business block). Diagnose the
   underlying cause from `last_error` and ledger-service's own logs around
   the failure window first. Once fixed, requeue for a fresh retry budget:
   ```sql
   UPDATE privacy_requests
   SET status = CASE owner_checkpoints ? 'ledger' WHEN true THEN
                   CASE owner_checkpoints->'ledger'->>'phase'
                     WHEN 'committed' THEN 'committing'
                     WHEN 'prepared'  THEN 'preparing'
                     ELSE 'pending'
                   END
                 ELSE 'pending' END,
       retry_count = 0, next_attempt_at = NULL, last_error = NULL, updated_at = now()
   WHERE id = '<request-id>' AND status = 'dead';
   ```
   This resumes from the last durably-recorded checkpoint, not from
   scratch — re-running `Prepare`/`Commit` on an already-succeeded step is
   harmless (both are idempotent) but wasteful and slower.
5. Throughout: the user's `auth_users.status` stays `'closing'` (login
   already rejected) for the entire time a request is `blocked`/stuck/dead
   — this is intentional (K10: "the account remains disabled" while a
   failure is being resolved forward), not a separate bug to fix.
6. Never manually flip `auth_users.status` to `'closed'` or hand-edit
   `accounts.owner_id` to "unstick" a closure — that produces exactly the
   inconsistent cross-service state (auth thinks closed, ledger doesn't
   know the surrogate) the whole saga design exists to prevent.

## Situation 5 — Key-version mismatch on a privacy-track key ring

**Symptom:** `ErrKeyVersionUnavailable` in logs, or export/closure
requests failing immediately (not after building/committing anything) with
a decrypt/encrypt error.

This track introduced **three** dedicated `pkg/cryptox.Ring`s beyond the
shared `Cryptox` ring, each its own key namespace (K2): `EXPORT_KEK_V<N>`
(T4, export archives), `CLOSURE_KEK_V<N>` (T5, active-subject ciphertext
during a closure saga), and `LEDGER_IDEMPOTENCY_KEY_V<N>` (T3, a
`DigestRing`, not an encryption ring — see below). Follow
[cryptox-key-rotation.md](cryptox-key-rotation.md)'s Step 1
expand/backfill/contract model for any of these — the mechanics are
identical, only the env var prefix and the service that owns it differ:

| Ring | Env prefix | Owner service | What breaks if misconfigured |
| --- | --- | --- | --- |
| Export | `EXPORT_KEK_V<N>` | auth-service | `RequestExport` returns `ErrExportStorageUnavailable`; worker silently doesn't start (no crash) |
| Closure | `CLOSURE_KEK_V<N>` | auth-service | `RequestClosure` returns `ErrClosureUnavailable`; worker silently doesn't start |
| Ledger idempotency digest | `LEDGER_IDEMPOTENCY_KEY_V<N>` | ledger-service | **boot fails** — this one is mandatory, never optional (T3: money-safety dedup, not a privacy convenience) |

`CLOSURE_KEK_V<N>`'s ciphertext is short-lived by design (destroyed at
`closureStepFinalize`, K10's own "destroy the active-saga ciphertext") —
if a closure request is stuck **because** of a closure-ring key mismatch,
retiring the wrong key version mid-saga is recoverable by restoring that
key version (same as any other ring), unlike a permanently-retired export
archive whose only copy was sealed under a now-gone key.

## Situation 6 — Object-store deletes are failing

**Symptom:** `SeevObjectOutboxDeleteFailing` fires
(`deploy/observability/prometheus/rules/retention.yml`, T1.8) — an export
archive's object was enqueued for deletion (successful download or TTL
expiry, K9) but `pkg/objectoutbox.Worker` can't actually remove it from
the store.

1. This is fail-safe by design: `object_outbox` metadata never claims an
   object is gone until the store confirms the delete succeeded — a
   failing delete leaves the row pending for retry, never silently drops
   it. There is no risk of the metadata "lying," only a growing backlog.
2. Check the document store's own health first (connectivity, auth,
   quota) — this is almost always an infrastructure problem, not an
   application bug.
3. Once the store is healthy again, the worker's own next poll drains the
   backlog automatically — no manual replay command is needed (unlike
   ledger's dead-lettered outbox events, `object_outbox` rows don't have a
   separate "dead" terminal state; they simply retry indefinitely with
   backoff).
4. Never manually delete the object directly against the store while
   bypassing the outbox row — that produces the exact "metadata says one
   thing, store says another" state this design exists to prevent. Let
   the worker's own retry do it, or fix the underlying store issue.

## Related

- [docs/roadmap/archive/51-a8-data-lifecycle-privacy.md](../../roadmap/archive/51-a8-data-lifecycle-privacy.md) — the full track design (K1–K13) and every task's Result section.
- [cryptox-key-rotation.md](cryptox-key-rotation.md) — the shared field-encryption ring; the expand/backfill/contract model this runbook's Situation 5 reuses.
- [internal/auth/privacy.go](../../../internal/auth/privacy.go), [privacy_worker.go](../../../internal/auth/privacy_worker.go) — export request/assembly/download.
- [internal/auth/closure.go](../../../internal/auth/closure.go), [closure_worker.go](../../../internal/auth/closure_worker.go) — closure request/saga.
- [internal/ledger/service/closure](../../../internal/ledger/service/closure) — ledger's own Prepare/Commit owner contract.
- [pkg/retentionworker](../../../pkg/retentionworker) — the shared retention-class runner every owner's purge/redact classes use.
- [deploy/observability/prometheus/rules/retention.yml](../../../deploy/observability/prometheus/rules/retention.yml) — every alert this runbook responds to.
