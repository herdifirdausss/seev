# Ledger use cases

This domain-named package is the Ledger composition and use-case root. The
public facade in `services/ledger/module.go` re-exports the stable API while
the financial implementation remains private to this service.

| Capability | Location |
|---|---|
| Atomic posting boundary | `handle/` |
| Maker-checker adjustments | `adjustments/` |
| Disbursement and disputes | `disbursement/`, `dispute/` |
| Interest and financial products | `interest/`, `provision/` |
| Reconciliation and scheduling | `recon/`, `schedule/` |
| FX decisions | `fx/` |
| Closure and command execution | `closure/`, `command/` |

Repositories, models, processors, transports, workers, and policy remain
parallel explicit boundaries under `../repository/`, `../model/`,
`../processors/`, `../transport/`, `../worker/`, and `../../policy/`.
Do not move posting logic into transport or repositories.
