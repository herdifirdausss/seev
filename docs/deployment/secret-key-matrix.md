# Secret and key matrix

K0 records secret names, consumers, rotation scope, and mount policy only.
Secret values, private-key bytes, generated credentials, and database dumps are
forbidden in this directory and in docs/evidence/k0.

| Secret category | Consumers | Sharing | Rotation / mount rule |
|---|---|---|---|
| database and broker credentials | data and messaging clients | local Compose shared; cloud scoped | coordinated rotation |
| JWT and internal service token | auth and internal policy paths | contract-shared | versioned/coordinated |
| cryptox and idempotency key rings | ledger, payout, gateway, vendor | controlled consumer set | versioned key ring |
| merchant pepper and vendor HMAC secrets | gateway and vendor | owner-scoped | rehash/provider coordinated |
| KYC provider token | auth | auth-only | provider rotation |
| export/closure KEKs | auth privacy features | feature-scoped | disabled until configured |
| bootstrap credentials | auth/admin bootstrap | one-shot operational use | rotate immediately |
| TLS CA and leaf private keys | certificate tooling and one service identity | CA or per-workload | secret-manager lifecycle |
| backup repository credentials | backup-agent | operations-only | repository lifecycle |
| alert webhook credential | assurance | assurance-only | provider lifecycle |

The current Compose shared certificate directory is a known mount-scope
finding. The Kubernetes contract is one leaf identity and one private-key
mount per workload; dev-operator is a separate operator-only identity.

Source: [secrets.yaml](../../deploy/inventory/secrets.yaml),
[mtls-identity-matrix.md](mtls-identity-matrix.md), .env.example, pkg/tlsx,
and internal/config.
