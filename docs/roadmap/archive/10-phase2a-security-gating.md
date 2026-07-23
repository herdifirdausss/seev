# 10 — Phase 2a: Security and API Gating

> Prerequisite: read sections A and D of [09-hardening-review.md](09-hardening-review.md).
> Run `make test` after each task. Before marking T1 or T2 complete, run
> `go test -tags=integration -race ./...` in Docker because both tasks change
> wiring or SQL.

## T1 — Separate the internal router for system transaction types

**Problem:** `internal/ledger/transport/http.go:19-29` (`adminOnlyTypes`) gates
only seven types through one public router. Ordinary users can call other
system-account operations, including `money_in`, `refund`, withdrawal-settle
variants, `escrow_release`, `escrow_refund`, and `fee_collect` through
`/api/v1/ledger/transactions`.

**Decision (K1):** use two routers and two HTTP listeners:

- **Public router** (`internal/handler/router.go`, `APP_PORT`) accepts only
  end-user operations: `transfer_p2p`, `transfer_pocket`,
  `withdraw_initiate`, and `escrow_hold`. Every other type returns 403.
- **Internal router** listens on a separate address, defaulting to
  `127.0.0.1:8081` (`INTERNAL_APP_PORT` and `INTERNAL_APP_BIND_ADDR`). It is
  intended for service-to-service calls, payment-gateway webhook handling, and
  operations tooling on an internal network. Never expose it to the internet.

### Implementation

1. Replace `adminOnlyTypes` in `internal/ledger/transport/http.go` with an
   explicit public allowlist:

   ```go
   var publicUserTypes = map[string]bool{
       "transfer_p2p":      true,
       "transfer_pocket":   true,
       "withdraw_initiate": true,
       "escrow_hold":       true,
   }
   ```

2. Keep `NewRouter(svc Service) http.Handler` for the public API. In
   `postTransaction`, reject any type not in `publicUserTypes` with
   `response.Forbidden(w, "this transaction type is not available on the public API")`.
3. Add `NewInternalRouter(svc Service) http.Handler`. It allows all registered
   processor types, while reusing the existing handler structure rather than
   duplicating it. A per-router allowlist supplied through a closure is one
   simple way to share the handler.
4. Keep the old admin check as a second defense inside the internal router.
   `freeze_*`, `adjustment_*`, `reversal`, and `chargeback` still require the
   admin role even on the internal network.
5. Add `Module.InternalRouter() http.Handler` in `internal/ledger/ledger.go`.
6. Add `AppConfig.InternalPort` and `AppConfig.InternalBindAddr`, defaulting to
   8081 and `127.0.0.1`, plus `INTERNAL_APP_PORT` and
   `INTERNAL_APP_BIND_ADDR`. In production, reject `0.0.0.0` by default;
   if an operator explicitly overrides it, accept the value but log a startup
   warning rather than hard-failing container-network deployments.
7. Add `NewInternalRouter` to `internal/handler/router.go`. Mount the ledger
   internal router with the same authentication and JSON middleware, but do not
   apply the public rate limit. JWT authentication remains mandatory; use a
   service role for internal callers. Global security headers remain useful;
   CORS is not relevant to an internal-only listener.
8. Run two `http.Server` instances in one process: the public listener on
   `:8080` and the internal listener on `127.0.0.1:8081`. Both must shut down on
   SIGINT/SIGTERM without leaks or panics. Extend `Server.StartWithSignals` to
   accept multiple servers, or use `sync.Once` around shared cleanup.
9. Document `INTERNAL_APP_PORT=8081` and
   `INTERNAL_APP_BIND_ADDR=127.0.0.1` in `.env.example`, with a warning that
   the port is for service-to-service and operations traffic only.

### Required tests

- Public `money_in` request → 403.
- Internal `money_in` request with a non-admin service token → 201.
- Internal `freeze_initiate` request with a non-admin token → 403.
- `transfer_p2p` works through both routers.
- Manually confirm that an internal listener bound to `127.0.0.1` is not
  reachable from another container or host network.

### Definition of done

- [ ] `go build ./...` and `make test` pass.
- [ ] The public router rejects every type outside the four-item allowlist.
- [ ] The internal router accepts all registered types but still admin-gates
      the seven compliance types.
- [ ] Both listeners run together and shut down cleanly under `go test -race`.
- [ ] `.env.example` and README document the internal port.

## T2 — Scope idempotency by user ID

**Problem:** `transport/http.go:100-107` builds `processors.Command` without
setting `IdempotencyScope`. The unique index therefore puts every user in the
same namespace.

### Implementation

1. In the public router, set `IdempotencyScope: userID.String()` after reading
   the user ID from the JWT.
2. In the internal router, scope by the internal caller rather than the target
   user. The recommended MVP design adds an optional
   `IdempotencyScope` request field for trusted callers and falls back to the
   user ID when it is empty. A service-account scope such as
   `internal:<role>:<userID>` is also acceptable.
3. Add `IdempotencyScope string `json:"idempotency_scope,omitempty"`` to the
   request DTO. Honor it only on the internal router; the public router must
   overwrite it with the JWT user ID to prevent spoofing.

### Required tests and definition of done

- [ ] Two users may use the same key and create independent transactions.
- [ ] The database accepts identical keys when their scopes differ.
- [ ] Replaying a key by the same user remains idempotent.
- [ ] `make test` and the integration test pass.

## T3 — Metadata allowlist and server-side fees

**Problem:** The request body is passed verbatim to processors, allowing a
client to set `gateway`, `fee_amount`, and `fee_gateway`.

### Implementation

1. Add an internal `feepolicy` package with rules keyed by
   `<transaction_type>:<gateway>`, containing a flat fee, percentage in basis
   points, and the destination fee gateway. A hard-coded MVP map is sufficient;
   an admin-configurable policy can come in Phase 3.
2. In the public transport:
   - validate `gateway` against `internal/ledger/constant`;
   - ignore client-supplied `fee_amount` and `fee_gateway` (log and strip them
     for backward compatibility);
   - calculate the fee with `feepolicy.Resolve` and inject the result into
     command metadata;
   - forward only allowlisted metadata keys such as `note`, `external_ref`,
     and `authorized_by`.
3. The internal router may accept explicit fee fields from trusted callers, but
   still validates them with the existing fee validator.
4. Processors do not need to change; they continue reading server-built command
   metadata.

### Required tests and definition of done

- [ ] Client fee fields are ignored on the public router.
- [ ] Unknown gateways return 400.
- [ ] Unrecognized metadata keys do not reach the processor.
- [ ] `make test` passes and public metadata is allowlisted.

## T4 — Require integral minor-unit amounts

**Problem:** `decimal.NewFromString` accepts fractions, the positive-amount
validator checks only `> 0`, and `IntPart()` silently truncates balances.

### Implementation

1. Make `decimalFromString` reject fractional amounts for the MVP. All current
   currencies use zero decimal places, so the minimal guard is:

   ```go
   func decimalFromString(s string) (decimal.Decimal, error) {
       amount, err := decimal.NewFromString(s)
       if err != nil {
           return decimal.Decimal{}, err
       }
       if !amount.Equal(amount.Truncate(0)) {
           return decimal.Decimal{}, errors.New("amount must be an integer")
       }
       return amount, nil
   }
   ```

   Add a TODO to move this check to the account currency's exponent when
   multi-currency support is enabled.
2. Add `IntegralAmountValidator` and include it in every processor that uses
   `PositiveAmountValidator`. This protects direct module callers and the
   internal router.
3. In `UpdateBalances`, return an error if a non-integral balance reaches the
   repository instead of calling `IntPart()`.

### Required tests and definition of done

- [ ] Transport rejects `amount: "100.5"` with 400.
- [ ] The validator rejects 100.5 and accepts 100.
- [ ] The repository returns an error instead of truncating.
- [ ] `make test` and `go test -tags=integration -race ./...` pass.

## T5 — Per-transaction amount cap

**Problem:** `MaxAmountValidator` exists but is not wired to a processor and no
configuration limit exists.

1. Add `LedgerConfig.MaxAmountPerTxMinorUnits` or a global equivalent, loaded
   from `LEDGER_MAX_AMOUNT_PER_TX`. Use a finite, documented default such as
   `1_000_000_000`. This is a safety ceiling, not a product limit; product
   limits belong to [08 S1](08-phase-3-scale.md).
2. Pass the cap into the handle service from `NewModule`.
3. Check `cmd.Amount` against the cap at the start of `execTransfer`, before
   the idempotency gate, so an over-limit request does not create a failed
   transaction row. Return `ErrAmountTooLarge`.

### Required tests and definition of done

- [ ] An amount above the cap returns `ErrAmountTooLarge` without repository
      calls.
- [ ] An amount below the cap follows the normal path.
- [ ] The environment variable, default, and safety-ceiling meaning are in
      `.env.example`.

## T6 — JWT issuer, proxy-aware HSTS, and metrics protection

1. **JWT `iss` and `nbf`:** add `Iss` to `Claims`, populate it from
   `JWTConfig.Issuer`, and validate it in `ParseToken` when an issuer is
   configured. Keep issuer validation disabled when the configuration is empty
   for backward compatibility, but warn once at production startup. `nbf` can
   remain a documented TODO until there is a concrete use case.
2. **Trusted proxy HSTS:** allow `X-Forwarded-Proto: https` to trigger HSTS
   only when `AppConfig.TrustProxyHeaders` is explicitly enabled. Default it to
   false and add `TRUST_PROXY_HEADERS`.
3. **Protect `/metrics`:** register it only on the internal router created in
   T1, not on the public listener. Update the README and `.env.example` to
   describe the listener boundary.

### Required tests and definition of done

- [ ] A wrong configured issuer is rejected; an empty issuer remains backward
      compatible.
- [ ] HSTS trusts the forwarded protocol only with explicit opt-in.
- [ ] `/metrics` is reachable only through the internal router.
- [ ] `make test` passes.

## Execution order

T1 → T2. T2 depends slightly on T1's public/internal router split. T3–T6 are
independent and may be executed in any order.

## Phase 2a final verification

```bash
go build ./...
make lint
make test
go test -tags=integration -race ./...   # Docker must be running
```

Manual smoke test: start the stack, apply migrations, and run `money_in` through
the public port (expect 403) and the internal port (expect 201).
