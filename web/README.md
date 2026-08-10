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

## Shared packages

| Package | Purpose |
|---|---|
| `packages/config` | Shared ESLint, TypeScript, Prettier, and Vitest configuration |
| `packages/domain` | Branded identifiers, status types, timestamp, and transaction domain models |
| `packages/money` | Exact `bigint` minor-unit money representation, arithmetic, and formatting |
| `packages/api-client` | Transport layer, normalized errors, and API boundary primitives |
| `packages/testing` | Synthetic fixtures and Mock Service Worker handlers (dev/test only) |
| `packages/ui` | Semantic, accessible React components for financial data |

## Local commands

```sh
# Install dependencies (requires Node 22.17.1+ and pnpm 11.21.0+)
pnpm --dir web install --frozen-lockfile

# Format check (no mutation)
pnpm --dir web run format:check

# Lint
pnpm --dir web run lint

# Type check
pnpm --dir web run typecheck

# Unit/component tests
pnpm --dir web run test

# Build both applications
pnpm --dir web run build

# Package boundary check
node web/scripts/check-boundaries.mjs

# Start wallet in development mode
pnpm --dir web/apps/wallet run dev

# Start operator in development mode
pnpm --dir web/apps/operator run dev
```

These commands are also available as Make targets:

```sh
make web-install
make web-format-check
make web-lint
make web-typecheck
make web-test
make web-build
make web-check
```

## Reference screens

Both applications contain fixture-backed reference screens. These screens:

- Display clearly labeled synthetic data (⚠ Fixture mode banner always visible)
- Do not connect to any live backend service
- Are accessible only from this README and local fixture navigation
- Must not be presented as live product behavior

**Wallet reference screen:** `http://localhost:3000/reference/transaction`
**Operator reference screen:** `http://localhost:3001/reference/transaction`

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

- **Wallet** may only call Auth and Gateway public surfaces
- **Operator** may only call Admin BFF browser/admin surfaces
- Neither application may call internal gRPC listeners, databases, Redis,
  RabbitMQ, or metrics endpoints

## Fixture mode

All Plan 56 screens use fixture data only. Fixture mode:

- Shows a persistent ⚠ banner on every fixture-backed screen
- Is excluded from production builds (runtime check in MockDataBanner)
- Uses synthetic values only — no real personal data, tokens, or production payloads

## Environment

Each application validates its environment variables at startup. Server-only
variables are never exposed to browser bundles. The bundle scanner verifies this
after every production build.

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
