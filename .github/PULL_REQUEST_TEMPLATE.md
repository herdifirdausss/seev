## What and why

<!-- What changed, and why — the diff already shows what, focus on why. -->

## Scope

<!-- Anything deliberately left out of scope, and why. -->

- [ ] This touches a financial invariant, service boundary, or security
      control ([project guide](../docs/development/project-guide.md)) — called
      out explicitly below if so.
- [ ] New/changed interfaces have regenerated mocks
      (`go generate ./internal/<service>/repository/...`).
- [ ] Tests added/updated alongside the change.
- [ ] I understand that, unless explicitly stated otherwise, my contribution
      is submitted under [Apache-2.0](../LICENSE).

## Verification

<!-- Which gate did you run locally? -->

- [ ] `go build ./... && go vet ./... && make lint && make test`
- [ ] `go test -tags=integration -race ./...`
- [ ] `make verify-full` (only if this touches money movement, persistence,
      messaging, or service startup — see the
      [project guide](../docs/development/project-guide.md))

## Related issues

<!-- Closes #... -->
