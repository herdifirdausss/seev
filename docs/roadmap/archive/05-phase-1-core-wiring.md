# 05 — Phase 1b: Account Repository, Provisioning, HTTP API, and Wiring

Prerequisite: 04 is complete (canonical schema and passing contract test).

## Task 1b.1 — SQL `AccountRepository` implementation

`internal/ledger/repository/account_repository.go` currently contains only an
interface and a mock. Add a `pgAccountRepo` implementation that accepts
`*sql.DB` or the same query interface used by the other repositories:

| Method | SQL |
|---|---|
| `GetAccountID(userID, accountType)` | `SELECT id FROM accounts WHERE owner_type='user' AND owner_id=$1 AND type=$2 AND pocket_code IS NULL AND status='active'` |
| `GetPocketAccountID(userID, pocketCode)` | `SELECT id FROM accounts WHERE owner_type='user' AND owner_id=$1 AND type='pocket' AND pocket_code=$2 AND status='active'` |
| `GetAccountCurrency(accountID)` | `SELECT currency FROM accounts WHERE id=$1` |
| `GetSystemAccountID(accountType, qualifier)` | `SELECT id FROM accounts WHERE owner_type='system' AND type=$1 AND COALESCE(system_qualifier,'')=$2`; use an empty qualifier for adjustment/confiscated accounts |

- Wrap `sql.ErrNoRows` as `apperror.ErrAccountNotFound` with context such as
  type, qualifier, or user ID.
- For the single-currency MVP, the system-account lookup does not filter by
  currency. Add a TODO comment; this becomes relevant in Phase 3.
- Add `go-sqlmock` unit tests, following the existing `pkg/database` pattern,
  and add the relevant scenario to the integration contract test.

## Task 1b.2 — Account-provisioning service

Create `internal/ledger/service/provision/provision.go`:

```go
// CreateUserAccounts creates the standard account set for a new user. It is idempotent.
// Auth/onboarding can call it when a user registers, or it can run lazily on the first transaction.
// All account rows are created in one database transaction.
func (s *Service) CreateUserAccounts(ctx context.Context, userID uuid.UUID, currency string) ([]Account, error)

// CreatePocket creates a named pocket account for a user.
func (s *Service) CreatePocket(ctx context.Context, userID uuid.UUID, currency, pocketCode string) (Account, error)
```

Behavior:

1. Create the standard `cash`, `hold`, `pending`, and `frozen` accounts with
   the same currency. Insert `accounts` and zero-balance `account_balances`
   rows in one `db.WithTx` transaction.
2. Make the operation idempotent: use `ON CONFLICT` on
   `uq_accounts_owner DO NOTHING`, then select the resulting rows. Calling it
   twice must not return an error.
3. Set `created_by` to `'service:ledger-provision'`.
4. Validate three uppercase currency characters (MVP: `IDR`) and
   `pocket_code` against `[a-z0-9_]{1,32}`.
5. Add unit and integration tests.

## Task 1b.3 — Public module API (`internal/ledger/ledger.go`)

Create the root `ledger` package as the module's only public entry point, as
required by boundary rule 01:

```go
package ledger

// Module is the public ledger-module facade.
type Module struct { /* private: handle service, provisioning, repositories, registry */ }

func NewModule(db *database.DB, logger *slog.Logger) *Module
func (m *Module) Post(ctx, cmd Command) error
func (m *Module) ProvisionUser(ctx, userID, currency) ...
func (m *Module) GetBalance(ctx, accountID) (Balance, error)
func (m *Module) GetTransaction(ctx, txID) (Transaction, error)
func (m *Module) ListEntries(ctx, accountID, cursor, limit) ([]Entry, string, error)
func (m *Module) Router() http.Handler
func (m *Module) StartWorkers(ctx) / StopWorkers()
```

The read methods need new repository queries. Add them to the existing
repositories rather than introducing another layer:

- Balance:
  `SELECT ab.balance, a.currency, a.status, a.type FROM account_balances ab JOIN accounts a ON a.id=ab.account_id WHERE ab.account_id=$1`
  without a lock.
- Entries: use keyset pagination:
  `WHERE account_id=$1 AND (created_at, id) < ($2, $3) ORDER BY created_at DESC, id DESC LIMIT $4`.
  Encode the cursor from `created_at|id` as base64.

## Task 1b.4 — HTTP transport (`internal/ledger/transport/http.go`)

Mount these routes below `/api/v1/ledger`; all requests pass through the
existing authentication middleware:

| Route | Handler | Notes |
|---|---|---|
| `POST /transactions` | Post a transaction | `user_id` comes from the JWT claim, never the body. Admin-only types (`adjustment_*`, `freeze_*`, `reversal`, and system-triggered `escrow_*`) require `middleware.WithRole("admin")`. |
| `GET /transactions/{id}` | Transaction details and entries | Return 404 when the transaction does not belong to the user, unless the caller is an admin. |
| `GET /accounts` | List the user's accounts | Read the user ID from the claim. |
| `GET /accounts/{id}/balance` | Get a balance | Check ownership. |
| `GET /accounts/{id}/entries?cursor=&limit=` | Get a statement | Default limit 50, maximum 200. |
| `POST /accounts/pockets` | Create a pocket | Body: `{"pocket_code":"travel"}`. |

Request body for `POST /transactions`:

```json
{
  "idempotency_key": "client-generated-uuid",   // required, 8..128 chars
  "type": "transfer_p2p",                        // must be registered
  "amount": "150000",                            // decimal string, integer minor unit, > 0
  "target_user_id": "…",                         // required for transfer_p2p
  "pocket_code": "travel",                       // optional
  "reference_id": "…",                           // optional UUID
  "metadata": {"gateway": "bca"}                 // processor-specific
}
```

Successful response: `201` with
`{ "status": "posted", "idempotency_key": "…" }` in the `pkg/response`
envelope. An idempotent replay returns `200` with the same body.

Map errors to HTTP with one unit-tested `apperrToStatus` function:

| Error | HTTP |
|---|---|
| `ErrValidation`, `ErrEmptyIdempotencyKey`, `ErrUnknownProcessor` | 400 |
| `ErrInsufficientBalance` | 422 |
| `ErrAccountNotFound` | 404 |
| `ErrAccountSuspended/Closed`, `ErrCurrencyMismatch` | 422 |
| `ErrStillProcessing` | 409 |
| `ErrPreviousFailed` | 409, with a message explaining that the key was already used by a failed transaction and a new key is required |
| Other errors | 500; do not expose internal details in the body, but log them |

Every handler must apply the body-size limit from middleware, validate fields,
and convert the request to `processors.Command`. Test handlers with `httptest`
and a mocked `Module`.

## Task 1b.5 — Composition-root wiring

1. Add `Ledger *ledger.Module` to `internal/handler/dependencies.go`.
2. Construct `ledger.NewModule(db, log)` in `cmd/server/main.go` and add it to
   the dependencies.
3. Mount the module in `internal/handler/router.go` with
   `apiMux.Handle("/ledger/", http.StripPrefix("/ledger", authed(deps.Ledger.Router())))`.
   Remove unused placeholders, or keep `/auth/*` for the future auth module;
   do not remove health/readiness routes.
4. Add `GET /metrics` with `promhttp` to the root mux without authentication.
   Do not expose it publicly in production; document that restriction in the
   README.

## Task 1b.6 — Minimal metrics and tracing

In `service/handle`, add the
`ledger_transactions_total{type,status}` counter and
`ledger_post_duration_seconds{type}` histogram. Add an OTel `ledger.Handle`
span with type and idempotency-scope attributes. **Never log the full
idempotency key or monetary amount in a public span.** Register the collectors
in `NewModule`.

## Definition of done for 05

- [ ] Complete the local-server flow (`make dev`, `docker compose up`):
      provision (through a registration stub or provisioning endpoint) →
      `money_in` (admin or simulated gateway) → `transfer_p2p` between two
      users → inspect balances and entries through the API. Document the curl
      sequence in `docs/roadmap/verify-mvp.md`.
- [ ] API-level idempotency and concurrency test: two parallel requests with
      the same key produce one 201 and one 200/409, never duplicate a posting.
- [ ] `make lint`, `make test`, and the integration contract test pass.
- [ ] Boundary check:
      `grep -rn "internal/ledger/" --include="*.go" cmd/ internal/handler/`
      finds only the root `internal/ledger` import, never a ledger subpackage
      imported from outside the module.
