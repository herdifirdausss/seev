# Contributing

> **License notice:** Seev is licensed under
> [Apache-2.0](LICENSE). Unless explicitly stated otherwise, contributions
> intentionally submitted for inclusion in this repository are provided under
> the same license, as described in Section 5.

Thanks for considering a contribution. This document covers the mechanics
of getting a change in; read [Architecture](docs/reference/architecture.md) and
[Onboarding](docs/development/onboarding.md) first if you're new to the codebase, and
[Project guide](docs/development/project-guide.md) before touching service boundaries,
financial invariants, or security controls — those rules are not optional
style preferences.

## Before you start

- Search open issues and PRs first to avoid duplicate work.
- For anything larger than a small fix (a new endpoint, a schema change, a
  new service capability), open an issue describing the approach before
  writing code. This repo has strong existing conventions (see the
  [project guide](docs/development/project-guide.md) for package layout and
  financial invariants), and it's much cheaper to align on approach before
  the diff exists.
- Documentation and small, well-contained fixes (typos, a missing test, a
  lint warning) don't need an issue first — just open the PR.

## Development setup

Follow [README.md](README.md)'s "Local quick start" to get the stack
running, then [Onboarding](docs/development/onboarding.md)'s "trace one request
end-to-end" section to build a working mental model before changing
anything load-bearing (in particular,
`internal/ledger/service/handle/service.go`'s `execTransfer` — read the
comment above it first). If you're about to change a specific service,
read its entry in [Services](docs/reference/services.md) first — its full
endpoint/job surface and dependencies are there, not just the pieces
your change happens to touch.

## Making a change

- Follow the naming and folder conventions in
  [Onboarding](docs/development/onboarding.md#naming-conventions) and
  [Project guide](docs/development/project-guide.md#package-layout-conventions) — new
  code should be indistinguishable in style from what's already there.
- Regenerate mocks after changing an interface
  (`go generate ./internal/<service>/repository/...`); never hand-edit a
  generated `_mock.go` file.
- Add or update tests alongside the code you change:
  `<file>_test.go` for unit tests (no Docker required),
  `<file>_integration_test.go` behind the `integration` build tag for
  anything needing a real Postgres/Redis.
- Keep commits focused — one logical change per commit is easier to
  review and revert than one large mixed diff.

### Documentation changes

- Follow the [documentation style guide](docs/development/documentation-style.md).
- Write public documentation in plain English and define unfamiliar terms on
  first use or link to the [glossary](docs/reference/glossary.md).
- Keep current behavior, target designs, and historical decisions explicitly
  labeled. Never document a planned control as if it already protects the
  runtime.
- Explain why a concept exists before listing its package, endpoint, schema,
  or command details.
- Link to one canonical explanation instead of copying a definition into
  several files.

## Before opening a pull request

Run the normal local verification gate. It overlaps with CI and adds a few
cheap checks that are useful before opening a pull request:

```bash
go build ./...
go vet ./...
make lint
make test
make docs-check
make proto-lint   # only if you changed api/proto/
git diff --check
```

```bash
go test -tags=integration -race ./...
```

Reach for the heavier gate only when it applies — money movement,
persistence, messaging, service startup, or shared test bootstrap changes:

```bash
make verify-full
```

See [Project guide](docs/development/project-guide.md#build-and-verification) for exactly
where that line is drawn.

## CI

Every push and PR runs `.github/workflows/ci.yml`: documentation link checks,
`actionlint` +
`ShellCheck` on the workflows/scripts themselves, an external-action
SHA-pin policy check, `golangci-lint`, unit tests, the `integration` suite
(testcontainers-backed, no external services needed), and a full
smoke-container run building and exercising all eight service images.
Documentation-only changes run the documentation check and skip the three
heavy jobs automatically — you don't need to do anything special for a docs
PR to stay fast. The
`ci-gate` job is the one required check; all others feed into it. See
[Operations](docs/operations/README.md#4-cicd-github) for the full breakdown of
every job, the weekly scheduled full-stack run, and the supply-chain
tooling behind it.

## Pull request expectations

- Describe *why*, not just *what* — the diff already shows what changed.
- Call out anything you deliberately left out of scope, and why.
- If you touched a financial invariant, a service boundary, or a security
  control, say so explicitly in the description so reviewers know to look
  closer.
- Keep the PR focused on one concern. A drive-by refactor bundled with a
  bug fix makes both harder to review — split them.

## Reporting bugs and security issues

Use GitHub Issues for regular bugs. **Do not open a public issue for a
security vulnerability** — see [SECURITY.md](SECURITY.md) instead.

## Code of conduct

Participation in this project is governed by
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
