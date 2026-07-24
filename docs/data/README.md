# Data

> [Documentation home](../README.md) · [Data](README.md)

> **Status: Current.** Generated evidence and policy for Track A8 (data
> lifecycle and privacy) — see
> [docs/roadmap/active/51-a8-data-lifecycle-privacy.md](../roadmap/active/51-a8-data-lifecycle-privacy.md)
> for the locked design decisions this directory implements.

| Page | Use it for |
|---|---|
| [retention.md](retention.md) | The full retention/classification matrix — every table, object class, and Redis/RabbitMQ/log/backup class, its owner, sensitivity, and purge or redaction rule |

`retention.md` is generated from
[config/data-retention.yaml](../../config/data-retention.yaml) by
`cmd/retentioncheck`; do not hand-edit it. Run `make retention-docs` after
changing the policy file, and `make retention-check` to verify the two stay
in sync (both wired into CI).
