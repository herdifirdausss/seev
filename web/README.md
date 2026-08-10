# Seev Web

Browser applications for the Seev reference implementation.

> **Not a production product.** Seev is an open-source learning project under
> Apache-2.0. These applications are reference implementations that demonstrate
> the frontend platform foundation. They do not constitute a licensed financial
> product, are not certified for real money, and are not suitable for
> production use.

## Applications

| Application | Port | Purpose |
|---|---|---|
| `apps/wallet` | 3000 | Consumer wallet shell (fixture-backed reference screen only) |
| `apps/operator` | 3001 | Operator shell (fixture-backed reference screen only) |

The **current authoritative operator console** is the Admin BFF embedded
HTML/htmx console. The `apps/operator` shell is a foundation scaffold that
does not yet implement any live operator journey. Do not treat it as a
replacement for the current console.

## Running

### Mode 1 — FE only (fixture mode, no backend required)

All current screens use MSW-backed synthetic fixtures. No backend service needs
to be running.

```sh
# One app at a time:
make web-dev-wallet    # http://localhost:3000
make web-dev-operator  # http://localhost:3001

# Both apps simultaneously (interleaved logs):
make web-dev
```

Or directly with pnpm from the repo root:

```sh
pnpm --dir web/apps/wallet   run dev
pnpm --dir web/apps/operator run dev
```

### Mode 2 — FE + BE (live backend integration, future plans)

> **Not applicable to Plan 56.** Live backend integration requires a follow-up
> execution plan. The steps below document the intended flow for that plan.

1. Start the backend services:
   ```sh
   make docker-up   # starts BE + infra with the "app" profile
   ```

2. Create a per-app `.env.local` from the example file:
   ```sh
   cp web/apps/wallet/.env.example   web/apps/wallet/.env.local
   cp web/apps/operator/.env.example web/apps/operator/.env.local
   # Edit .env.local to set live service URLs
   ```

3. Start the FE in development mode (MSW disabled when env vars are set):
   ```sh
   make web-dev-wallet
   make web-dev-operator
   ```

   - Wallet calls **Gateway** (`:8080`) and **Auth** (`:8082`) — never internal services
   - Operator calls **Admin BFF** (`:8095`) — never internal services directly

### Mode 3 — Production containers

Build and run as Docker containers (context is the `web/` directory):

```sh
# Build wallet image
docker build --build-arg APP=wallet -t seev/wallet web/

# Build operator image
docker build --build-arg APP=operator --build-arg PORT=3001 -t seev/operator web/

# Run
docker run -p 3000:3000 seev/wallet
docker run -p 3001:3001 seev/operator
```

Via Docker Compose (opt-in `frontend` profile, does not start with `make docker-up`):

```sh
docker compose --profile frontend build
docker compose --profile frontend up wallet
docker compose --profile frontend up operator
docker compose --profile frontend up wallet operator
```

## Quality gates

```sh
make web-install        # install dependencies (frozen lockfile)
make web-format-check   # formatting (read-only)
make web-lint           # ESLint
make web-typecheck      # TypeScript strict check
make web-test           # unit/component tests
make web-build          # production build (both apps)
make web-boundary-check # package dependency rules
make web-check          # all of the above in sequence
```

`make web-check` runs as part of `make verify-static`.

## Reference screens

Both applications contain fixture-backed reference screens. These screens:

- Display clearly labeled synthetic data (⚠ Fixture mode banner always visible)
- Do not connect to any live backend service
- Are accessible only from this README and local fixture navigation

**Wallet:** `http://localhost:3000/reference/transaction`  
**Operator:** `http://localhost:3001/reference/transaction`

## Shared packages

| Package | Purpose |
|---|---|
| `packages/config` | Shared ESLint, TypeScript, Prettier, and Vitest configuration |
| `packages/domain` | Branded identifiers, status types, timestamp, and transaction domain models |
| `packages/money` | Exact `bigint` minor-unit money representation, arithmetic, and formatting |
| `packages/api-client` | Transport layer, normalized errors, and API boundary primitives |
| `packages/testing` | Synthetic fixtures and Mock Service Worker handlers (dev/test only) |
| `packages/ui` | Semantic, accessible React components for financial data |

## Package dependency rules

```
apps/wallet   ──┐
                ├──► packages/ui
apps/operator ──┤──► packages/domain
                ├──► packages/money
                ├──► packages/api-client
                ├──► packages/testing (test code only)
                └──► packages/config (tooling only)

packages/ui ──────► packages/domain, packages/money
packages/api-client ─► packages/domain, packages/money
packages/domain ──► packages/money
```

Forbidden:
- Shared packages importing application code
- Wallet importing Operator source (or vice versa)
- `domain` importing React, Next.js, or query libraries
- `money` importing UI or transport code
- Production code importing `packages/testing`
- Applications calling Ledger, Fraud, Assurance, or internal service listeners directly

## Browser trust boundaries

- **Wallet** may only call Auth and Gateway public surfaces (`:8080`, `:8082`)
- **Operator** may only call Admin BFF browser/admin surfaces (`:8095`)
- Neither application may call internal gRPC listeners, databases, Redis,
  RabbitMQ, or metrics endpoints

## Environment variables

Each application has an `.env.example` that documents available variables.
Copy it to `.env.local` for per-machine overrides (git-ignored).

Server-only variables (no `NEXT_PUBLIC_` prefix) are never exposed to browser
bundles. The bundle scanner at `scripts/check-client-bundle.mjs` verifies this
after every production build.

## Fixture mode

All Plan 56 screens use fixture data only. Fixture mode:

- Shows a persistent ⚠ banner on every fixture-backed screen
- Is excluded from production builds (runtime check in MockDataBanner)
- Uses synthetic values only — no real personal data, tokens, or production payloads

## Current limitations

This is the Plan 56 platform foundation. The following are **not implemented**:

- Live authentication, registration, or session flows
- Live balance, transaction list, or transaction detail screens
- Top-up, transfer, withdrawal, or any money-moving journey
- Modern operator views replacing the current htmx console
- Notification, KYC, scheduling, or statement journeys
- Any connection to a real backend service

These features require separate execution plans after this foundation passes
acceptance review.
