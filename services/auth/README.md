# Auth

Auth owns identity and the user lifecycle: credentials, sessions, KYC,
operator access, privacy requests, and account closure.

## Ownership

- Owns users, credentials, sessions, KYC documents/status, closure state, and
  Auth retention work.
- `repository/` is the only persistence boundary; handlers do not issue SQL.
- KYC vendor adapters are isolated under `internal/adapter/kycvendor/` and are never
  exposed to other services.

## Layout

- `cmd/auth/` — process composition and route registration.
- `internal/transport/http/` — HTTP request validation, authorization, and response
  mapping.
- `internal/auth/` — identity, KYC, privacy, closure, and lifecycle use
  cases.
- `internal/repository/` — Auth-owned repositories and mocks.
- `internal/auth/model/` and `internal/worker/` — domain data and background jobs.
- `migrations/` — Auth-owned schema changes.

## Runtime and verification

Other services use the public facade in `services/auth/module.go`; they do not
import `services/auth/internal`. Compile with
`go test -run '^$' ./services/auth/...` and run integration tests with the
repository's Postgres test environment.
