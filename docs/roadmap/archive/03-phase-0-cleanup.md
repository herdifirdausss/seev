# 03 — Phase 0: Repository Cleanup

Goal: make the repository honest (the README matches reality), keep the
structure clean (`cmd/` stays thin and libraries live in `pkg/`), remove dead
files, and prepare for Phase 1. **This phase must not change runtime behavior**
except where explicitly stated.

Execute the tasks in order. For each task: implement it, run `make test`, and
commit it.

## Task 0.1 — Move the scheduler to `pkg/scheduler`

1. Create `pkg/scheduler/scheduler.go` from
   `cmd/scheduler/scheduler_final.go`:
   - Change `package main` to `package scheduler`.
   - Remove `main()` and any signal-handling/demo block. A library must not
     handle SIGTERM; its caller owns shutdown.
   - Export the types and functions used outside the package:
     `NewScheduler`, `Cron`, `Stop`, `NewMemoryLock`, the Redis lock,
     `WithLocation`, and the metrics interface. Keep internal helpers
     unexported.
2. Move `cmd/scheduler/scheduler_test.go` and `scheduler_unit_test.go` to
   `pkg/scheduler/`, updating the package name. All tests must remain green.
3. Move `cmd/scheduler/README.md` to `pkg/scheduler/README.md`.
4. Remove `cmd/scheduler/`.

Acceptance: `go build ./...` and `go test ./pkg/scheduler/ -race` pass, and no
`package main` remains outside `cmd/server`.

## Task 0.2 — Remove `cmd/rabbitmq`

`cmd/rabbitmq/rabbitmq.go` is example code whose functionality is already
covered by `pkg/messaging`. Verify first that
`grep -rn "cmd/rabbitmq" --include="*.go" .` returns no results, then remove
the directory.

## Task 0.3 — Consolidate schema files

1. Create `docs/design/legacy-schemas/`.
2. Move these files there: `migrations/001.sql`, `migrations/002.sql`,
   `migrations/ledger.sql`, `migrations/ledgernew.sql`, and
   `internal/ledger/001.sql`.
3. Remove the empty `migrations/auth.sql` file.
4. Add `docs/design/legacy-schemas/README.md` with one paragraph:
   "These are schema drafts from before the canonical migrations (see
   `docs/roadmap/archive/04`). `ledgernew.sql` is the most complete draft and the source
   of the safeguards being ported. **Do not execute these files against a
   database.**"

The `migrations/` directory should now be empty and will be populated in Phase
1 (document 04). Confirm that the Makefile's `make migrate-up` and
`make migrate-down` targets use golang-migrate with the
`NNNNNN_name.up.sql` / `.down.sql` naming convention; fix them if necessary.

## Task 0.4 — Remove dead ledger code

1. `internal/ledger/service/migration.go` contains the old `SMALLINT` design.
   Check its users with
   `grep -rn "AccountCash\|CurrencyIDR\|AccountType(" --include="*.go" internal/ cmd/`.
   If only that file uses them, remove it.
2. Check `internal/ledger/service/transfer/transfer_service.go` with
   `grep -rn "service/transfer" --include="*.go" .`. If nothing references it,
   remove the directory. If it is referenced, inspect it and report before
   deciding.
3. `internal/ledger/model/ledger_transaction.go` contains a `model.Command`
   duplicate of `processors.Command`. Check its users with
   `grep -rn "model.Command" --include="*.go" .`, migrate them to
   `processors.Command`, and remove `model.Command`.
4. Fix the misplaced comment in
   `internal/ledger/model/ledger_entry.go`: the `LedgerEntryRecord` comment is
   attached to `EntryInstruction`.

## Task 0.5 — Fix the filename typo

Rename `internal/handler/dependencties.go` to
`internal/handler/dependencies.go` without changing its contents.

## Task 0.6 — Add the development infrastructure promised by the README

Check whether these files exist: `docker-compose.yml`, `.env.example`,
`.golangci.yml`, `.air.toml`, and `Dockerfile`. Create minimal versions of any
missing files:

- `docker-compose.yml`: PostgreSQL 16 (port 5432), Redis 7, and RabbitMQ 3
  with management enabled and health checks.
- `.env.example`: every environment variable validated by
  `internal/config/config.go`, with development-safe example values. Use that
  file as the source of truth for the list.
- `.golangci.yml`: at minimum `errcheck`, `govet`, `staticcheck`, `ineffassign`,
  `unused`, and `misspell`.
- `Dockerfile`: multi-stage, distroless, and non-root, matching the README
  claims.

## Task 0.7 — CI

Create `.github/workflows/ci.yml` for pushes and pull requests to `main`. The
job must set up the Go version from `go.mod`, then run `make lint` and
`make test`. Put integration tests (build tag `integration`) in a separate job
with `continue-on-error: false` and a PostgreSQL service container.

## Task 0.8 — Rewrite `README.md`

Update the README to match the post-Phase-0 repository: actual folders, a brief
modular-monolith architecture linked to `docs/roadmap/archive/01`, a quick start, and the
available Make targets. Remove claims about features that do not exist.

## Task 0.9 — Document module boundaries

Create `PROJECT_GUIDE.md` at the repository root for future contributors. It
must include the module-boundary rules from `docs/roadmap/archive/01`, build/test/lint
commands, error and logging conventions, and this hard rule:
"`ledger_entries` is append-only; monetary values must never use floating
point; do not change the step order in `service/handle/service.go` without
reading `docs/roadmap/archive/04`."

## Phase 0 definition of done

- [ ] `go build ./...`, `make lint`, and `make test` pass.
- [ ] `cmd/` contains only `server/`.
- [ ] `migrations/` is empty and old schemas are archived under
      `docs/design/legacy-schemas/`.
- [ ] No file identified as dead in 00-current-state.md M4/M6 remains.
- [ ] CI is green on GitHub.
- [ ] The README is accurate.
