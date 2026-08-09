# Data

> [Documentation home](../README.md) · [Data](README.md)

> **Status: Current.** Generated evidence and policy for data lifecycle and
> privacy. The [roadmap history](../roadmap/history.md) records the Plan 51
> outcome; `config/data-retention.yaml` is the current policy source of truth.

| Page | Use it for |
|---|---|
| [retention.md](retention.md) | The full retention/classification matrix — every table, object class, and Redis/RabbitMQ/log/backup class, its owner, sensitivity, and purge or redaction rule |

`retention.md` is generated from
[config/data-retention.yaml](../../config/data-retention.yaml) by
`tools/retentioncheck`; do not hand-edit it. Run `make retention-docs` after
changing the policy file, and `make retention-check` to verify the two stay
in sync (both wired into CI).
