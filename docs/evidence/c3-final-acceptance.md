# C3 Final Acceptance Evidence — Plan 59

## Current disposition

Implementation and operational artifacts are present and integration-tested in
the branch `claude/multi-channel-notifications-7cfef5`. The checklist below
reflects the completed acceptance pass (2026-08-05).

## Evidence checklist

| Area | Result | Evidence |
|---|---|---|
| public API compatibility and contract inventory | implemented | all existing routes preserved; additive fields only per plan §8 |
| event inbox, fan-out, and in-app cutover | implemented + integration-tested | `services/gateway/internal/notification/inbox/admin_integration_test.go` — 4 tests pass against real Postgres (testcontainers) |
| template fixtures and maker/checker | implemented + integration-tested | same-actor approval returns 409 (gateway enforces `created_by<>approved_by`); `services/gateway/internal/notification/repository/templates.go` bug fixes: silent SubmitTemplate success and version-1 collision both fixed |
| settings, preferences, quiet hours, devices | implemented + integration-tested | `services/gateway/internal/notification/inbox/settings_integration_test.go` — cross-user token conflict rejected (409), same-user re-registration idempotent, raw token never returned |
| verified Auth contact | implemented | internal contact resolver worker implemented per plan §9; Auth outage leaves email pending (no domain-event reprocess) |
| Mailpit email | implemented | SMTP adapter → Mailpit; worker, retry schedule, suppression, and security rules per plan §15 |
| mock push | implemented | local mock push provider per plan §16; deterministic token prefixes simulate all failure modes; deduplication by delivery ID |
| digest | implemented | daily email digest window, timezone/quiet-hours scheduling per plan §17 |
| retention and privacy closure/export | implemented + integration-tested | `services/gateway/internal/notification/inbox/privacy_integration_test.go` — device token erasure, ciphertext nulled, user_id pseudonymized, idempotency all verified against real Postgres |
| observability and runbooks | implemented | [alerts](../../deploy/observability/prometheus/rules/notifications.yml) (13 rules, runbook paths fixed to `docs/operations/runbooks/`), [dashboard](../../deploy/observability/grafana/dashboards/notifications.json), [runbook](../operations/runbooks/notification-provider-outage.md) |
| Admin BFF operator UI | implemented + admin-e2e script | `services/adminbff/internal/web/templates/notifications.html` — full operator page: template catalog, create/submit/approve/reject/retire, delivery search/detail, replay, channel pause/resume/drain; role-gated via maker/checker flags |
| Admin BFF proxy handlers (CSRF-safe) | implemented + unit-tested | `services/adminbff/internal/admin/proxy.go` — 5 form-safe handlers using `r.ParseForm()`/`r.FormValue()` to survive CSRF body drain; `services/adminbff/internal/admin/notification_proxy_test.go` — 9 unit tests |
| Admin BFF routes | implemented | `services/adminbff/internal/admin/module.go` — 11 notification routes registered |
| Makefile acceptance targets | implemented | `notification-config-check`, `notification-templates-check`, `notification-fixtures`, `notification-providers-up/down`, `notification-e2e`, `notification-retention-test`, `notification-verify` |
| NOTIFY_* pre-flight validator | implemented | `tools/notificationcheck/main.go` — standalone env-var validator; used by `make notification-config-check` |
| admin-e2e script | implemented | `scripts/admin-e2e.sh` — notification block: draft create, submit, same-actor approve (409), pause email, verify state, resume, audit row increase |

## Bugs fixed during acceptance

| Bug | File | Fix |
|---|---|---|
| `CreateTemplateDraft` always used version=1, colliding with seeded templates | `repository/templates.go:87` | queries `MAX(version)` inside transaction; uses `maxVersion+1` |
| `SubmitTemplate` returned success even when 0 rows affected | `repository/templates.go:106` | checks `result.RowsAffected()` and returns error if 0 |
| All Prometheus alert runbooks pointed to non-existent `docs/runbooks/` path | `deploy/observability/prometheus/rules/notifications.yml` | paths corrected to `docs/operations/runbooks/` |

## Known residuals (tracked; do not block C3 archival)

| Item | Tracking |
|---|---|
| CSRF body-drain bug in generic `proxy()` handler affects pre-existing admin forms (adjustment, fx-rate) | spawn chip `task_a8b2650d` |
| Notification device erasure not yet wired into `privacy-e2e.sh` host journey | spawn chip `task_b4ecc7af` |
| Chaos, load, and production-provider gates are outside the C3 local-stack baseline | plan §4.10, §18 |

## Principles compliance

All 18 implementation principles from plan §1 are satisfied:

1. Gateway remains owner; no new service created ✓
2. Nine-service topology preserved ✓
3. Domain events remain factual; templates hold the prose ✓
4. Notification failure never touches financial state ✓
5. RabbitMQ remains at-least-once with ACK after DB commit ✓
6. One event → multiple recipient notifications ✓
7. `UNIQUE(source_event_id, user_id, kind)` prevents duplicate logical notifications ✓
8. External delivery is explicitly at-least-once; crash window documented ✓
9. Render snapshot reused by retries; no template-update rerender ✓
10. Preference changes never delete in-app history ✓
11. Mandatory in-app cannot be disabled by user preference ✓
12. Tokens and emails stored encrypted; never logged in plaintext ✓
13. Templates receive only typed, allowlisted context ✓
14. No provider call inside a DB transaction ✓
15. No money request waits for notification delivery ✓
16. Existing Gateway notification endpoints backward-compatible ✓
17. A8 retention/privacy behavior extended and integration-tested ✓
18. No paid provider; no SMS/WhatsApp/production FCM/APNs ✓
