# `<capability>` acceptance

| Field | Value |
|---|---|
| Owner | `<team/person>` |
| Commit | `<sha>` |
| Environment | `<environment>` |
| Rollout/migration | `<link>` |
| Rollback | `<link>` |

## Acceptance checklist

- [ ] Reachable only through the intended route/listener.
- [ ] Authentication and authorization are explicit.
- [ ] Tenant/subject ownership is enforced at the service and database layer.
- [ ] Integration tests cover dependency success, failure, timeout, duplicate,
      delayed delivery, and retry.
- [ ] End-to-end path is executed with retained correlation IDs.
- [ ] Metrics, structured logs, and traces identify the operation without
      exposing sensitive payloads.
- [ ] Alerts have thresholds, owners, and an actionable runbook.
- [ ] Retry, recovery, and replay are safe and idempotent.
- [ ] Migration is separate from application startup and rollback is tested.
- [ ] Capacity assumptions and limits are documented.
- [ ] Final evidence links are attached and the scorecard is updated.
