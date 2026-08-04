# Public route matrix

The public edge is intentionally small. Traefik owns transport routing and
request-shaping middleware; Seev services retain authentication, authorization,
signature, idempotency, and financial policy.

| Host | Path | Owner | Edge policy | First-stage exposure |
|---|---|---|---|---|
| `api.local.seev.test` / `api.dev.seev.example` | `/api/v1/auth/*` | Auth | HTTPS, request ID, body cap, rate limit | local + cloud |
| `api.local.seev.test` / `api.dev.seev.example` | `/api/v1/*` | Gateway | HTTPS, request ID, body cap, rate limit | local + cloud |
| `api.local.seev.test` / `api.dev.seev.example` | `/api/v1/b2b/*` | Gateway | HTTPS, API-key scope remains application-owned | local + cloud, feature flag off by default |
| `callback.local.seev.test` / `callback.dev.seev.example` | `/webhooks/{vendor}` | VendorService | HTTPS, POST only, body cap, source CIDR defense-in-depth, Traefik-to-VendorService mTLS | local + cloud |
| `admin.local.seev.test` | `/` and `/api/v1/admin/*` | Admin BFF | local-only test route; cloud remains private | local only |

Never route `/health`, `/ready`, `/metrics`, internal admin APIs, gRPC, or the
Traefik dashboard through the public Gateway. The callback route must not be a
wildcard route to Gateway.

Source references: `contracts/http/*.yaml`, `contracts/http/webhooks-v1.yaml`,
`services/*/cmd/*/main.go`, and `services/vendor-service/internal/callback.go`.

The authoritative machine-readable route record is
[deploy/inventory/routes.yaml](../../deploy/inventory/routes.yaml). K0 also
records health, readiness, metrics, admin, and privacy routes there so they
cannot be accidentally promoted to public edge exposure.
