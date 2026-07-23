# 25 — Phase 5: Business Shell MVP

Prerequisite: [plan 22](22-phase4a-payin-vendorgw.md) and [plan 23](23-phase4b-payout-orchestration.md). This phase follows the auth outline in [plan 24](24-extraction-playbook.md), the module-prefix rule from [plan 21](21-service-topology-review.md), and the verification rules from [plan 09](09-hardening-review.md).

## Objective and business decisions

The ledger and payment primitives exist, but an end user still cannot use the product end to end. This phase closes that gap:

```text
register → login → top up → paid P2P transfer → paid withdrawal
         → in-app notification → operator revenue and health views
```

Revenue comes from withdrawal and P2P transfer fees; top-ups are free. Notifications use an in-app inbox and the existing RabbitMQ outbox. Email, push notifications, KYC, complex VA/QRIS intents, and fees on pending withdrawal settlement are out of scope.

## T1 — Authentication module

### Implementation

1. Add migration `000021_auth` with `auth_users`, `auth_credentials`, and `auth_refresh_tokens`, including forced RLS and minimal grants.
2. Add `internal/auth` with register, login, refresh, profile, profile update, and bootstrap-admin operations.
3. Hash passwords with bcrypt cost 12. Generate opaque 32-byte refresh tokens, store only their SHA-256 hashes, and rotate them on every refresh. Reuse of a revoked token revokes all tokens for that user.
4. Issue JWTs through the existing `middleware.GenerateToken` contract; ledger, policy, and middleware claims do not change.
5. Register the ledger account through a structural `Provisioner` interface. Login performs lazy re-provisioning to self-heal a partially completed registration.
6. Replace the public 501 placeholders for register, login, refresh, and `/users/me`. Authentication endpoints use IP/path rate limiting without requiring JWT; `/users/me` remains authenticated.
7. Create an idempotent bootstrap admin from `AUTH_BOOTSTRAP_ADMIN_EMAIL` and `AUTH_BOOTSTRAP_ADMIN_PASSWORD`. Passwords never enter migration files or version control.

### Result

The migration, auth facade, handlers, refresh rotation, replay containment, JWT integration, lazy provisioning, and bootstrap admin were implemented. `PUT /users/me` was also completed because it was an existing placeholder.

Eighteen unit tests and five integration tests passed, covering duplicate emails, wrong credentials without account enumeration, disabled users, refresh reuse, bootstrap idempotency, JWT middleware compatibility, and provisioning of the four ledger accounts. Migration 000021 passed up/down/up verification.

## T2 — Transfer and withdrawal fees

### Design

Add environment-configured flat and basis-point fees for `transfer_p2p` and `withdraw_settle`. The default is zero, preserving the previous behavior. Fee rules are exposed through the ledger facade so consumers do not import the private fee-policy package.

Withdrawal fees are charged at settlement, not at initiation. The hold remains the full requested amount; settlement credits `amount - fee` and the platform fee account receives the difference. Cancellation refunds the full hold and charges no fee.

### Result

Fee configuration, facade methods, injected transport policy, inline settlement fee legs, and payout fee resolution were implemented. Validation rejects negative fees and basis points above 10,000. Integration tests prove the three balanced legs for a charged withdrawal and a full no-fee cancellation. Chaos payout recovery also passed with fees enabled.

## T3 — Top-up intents

### Design

Extend `internal/payin` with migration `000022_payin_topup_intents` and pending/settled/expired intents. A user creates an intent with `POST /api/v1/topup`; the response contains a vendor-facing reference and expiry.

The webhook uses the existing `external_ref` field to resolve the intent, cross-checks amount and currency, and uses the intent's user rather than exposing an internal user ID to the vendor. For backward compatibility, a webhook without a matching pending intent may still use its payload `user_id`.

Settlement posts money first and conditionally marks the intent settled, making redelivery and a crash between the two operations safe. Expiry is lazy and is applied when an intent is read or processed; no new expiry worker is needed.

### Result

Migration 000022, authenticated create/get endpoints, intent lookup, user resolution, mismatch handling, idempotent settlement, and lazy expiry were implemented. The lookup occurs before inserting the webhook event so the persisted event already contains the resolved user. Eleven unit tests and three integration tests passed, including full intent-to-webhook flow, redelivery, and mismatch replay.

## T4 — In-app notifications

### Design

Add migration `000023_notify` with `notif_notifications` and a unique `(event_id, user_id)` key for at-least-once delivery. Enrich `TransactionPosted` with optional user and target-user fields without changing the event schema version.

The new `internal/notify` consumer declares `ledger.events.notifications`, filters relevant transaction types, maps recipients, inserts with `ON CONFLICT DO NOTHING`, acknowledges success or duplicates, and negatively acknowledges retryable failures up to five attempts.

Public endpoints:

```text
GET  /api/v1/notifications?limit=&before=
POST /api/v1/notifications/{id}/read
```

### Result

The RabbitMQ consumer, recipient mapping, deduplication, authenticated inbox routes, and worker lifecycle were implemented. Withdrawal cancellation also produces a notification, including admin-cancelled payouts, which is intentional. Nine unit tests, golden event tests, and two integration tests passed.

## T5 — Operator improvements

Add admin-gated list endpoints for reconciliation batches and dead outbox events. Extend the policy engine with an optional alert callback. Every fail-open policy branch emits a warning at most once per 60 seconds, including the failed dimension, so a Redis outage is visible without creating an alert storm.

### Result

Repositories, facades, routes, pagination validation, policy alert throttling, and wiring through the existing alert webhook configuration were implemented. Seven unit tests and two integration tests passed.

## T6 — Business end-to-end acceptance test

`scripts/business-e2e.sh` now exercises:

1. Register and log in two users with real JWTs.
2. Create a top-up intent, deliver a signed webhook, verify the settled intent, balance, and notification.
3. Transfer P2P with a fee and verify both balances, the platform fee account, and both notifications.
4. Withdraw with a fee, verify settlement, then verify an asynchronous cancellation refunds the full amount with no fee.
5. Check ledger balance, projection consistency, fee revenue, dead outbox events, reconciliation batches, and notifications.

### Result

The complete journey passed against the live stack. Build, vet, unit, integration, race, smoke, and chaos verification passed. The business shell MVP is therefore usable through the public API without direct SQL or internal trusted-caller shortcuts.

## Final verification

```bash
go build ./...
go vet ./...
go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/chaos-test.sh all
```
