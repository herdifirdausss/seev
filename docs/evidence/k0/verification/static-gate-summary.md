# K0 static and contract verification

Captured after the K0 inventories and documentation were assembled. These
commands are repository-local and do not create cloud or Kubernetes resources.

| Command | Result |
|---|---|
| `make verify-static` | BLOCKED — the latest run stops while compiling `cmd/gateway`: the modified gateway wiring imports `internal/notify/channel/email` and `internal/notify/channel/push`, but those packages are absent from this worktree. This is an unresolved C3 worktree mismatch, not a K0 inventory change. |
| `make k0-inventory` | BLOCKED — regeneration reaches the same missing C3 packages during `go list ./cmd/...`; the committed inventory snapshot remains available for `make k0-inventory-check`. |
| `make contracts` | PASS |
| `make docs-check` | PASS — 186 Markdown files checked |
| `make k0-inventory-check` | PASS — 12 machine-readable inventories validated |
| `git diff --check` | PASS |
| `make verify-full` | DEFERRED — full disposable integration orchestration was not run after the host resource guard engaged; owner K0/K9 |

The K0 inventory artifacts are complete and machine-checkable. The repository
exit gate remains open until the owning C3 change either lands the missing
notification channel packages/API in this worktree or restores compatible
gateway wiring. K0 does not copy the separate unfinished C3 worktree into the
deployment-inventory change.

No secret values, private keys, database dumps, JWTs, or request payloads are
stored in this directory.
