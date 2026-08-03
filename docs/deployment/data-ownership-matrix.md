# Data ownership matrix

Each service has one PostgreSQL database and one runtime role. The migration
identity is separate from the application role, and cross-service SQL is
forbidden. Redis and RabbitMQ are shared infrastructure with explicit
namespace/topology ownership.

| Owner | Database | Runtime role | Migration path | Retention / backup |
|---|---|---|---|---|
| ledger | seev_ledger | ledger_app | migrations/ledger (37 at baseline) | ledger policy; backup-agent operations path |
| auth | seev_auth | auth_app | migrations/auth (18) | auth policy; backup-agent operations path |
| payin | seev_payin | payin_app | migrations/payin (16) | payin policy; backup-agent operations path |
| payout | seev_payout | payout_app | migrations/payout (16) | payout policy; backup-agent operations path |
| fraud | seev_fraud | fraud_app | migrations/fraud (9) | fraud policy; backup-agent operations path |
| gateway | seev_gateway | gateway_app | migrations/gateway (9) | gateway policy; backup-agent operations path |
| vendor | seev_vendor | vendor_app | migrations/vendor (4) | vendor policy; backup-agent operations path |
| admin-bff | seev_adminbff | adminbff_app | migrations/adminbff (7) | admin policy; backup-agent operations path |
| assurance | seev_assurance | assurance_app | migrations/assurance (9) | assurance policy; backup-agent operations path |

Shared stores:

- Redis DB 0 is non-authoritative cache/state with owner-specific prefixes,
  bounded TTLs, and no restore dependency.
- RabbitMQ owns durable ledger.events topology; queue ownership and DLQs are
  in [messaging.yaml](../../deploy/inventory/messaging.yaml).
- Local object storage holds synthetic KYC/export objects only. The production
  object-store contract is deferred to K7.
- Backup repository, certificates, and observability volumes are operations or
  platform-security surfaces, never application evidence payloads.

Sources: scripts/postgres-init, cmd/migrate, migrations/*,
config/data-retention.yaml, and
[data-stores.yaml](../../deploy/inventory/data-stores.yaml).
