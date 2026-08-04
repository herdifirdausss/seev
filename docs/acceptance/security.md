# Database and authorization security acceptance

This document closes the repository-side controls in section 8.6 of the
[world-class engineering improvement plan](../improvement/seev-world-class-engineering-improvement-plan-en.md).
It does not substitute for the independent review required by section 12.6.

## Repository controls

| Finding | Implemented control | Repeatable evidence |
|---|---|---|
| KYC-state consistency | Auth owns status/KYC state; Ledger keeps an execution projection and requires a current active subject, tenant, and KYC state at execution. Missing projection/reader configuration fails closed. | `services/ledger/internal/ledger/command/executor_test.go`, `services/ledger/migrations/000043_money_movement_execution_subjects.up.sql`, `services/auth/internal/auth/auth_test.go` |
| Excessive database grants | Service roles are separate from migration ownership; append-only evidence tables have no update/delete grants; closure credential deletion uses a narrowly scoped `SECURITY DEFINER` function instead of a table grant. | `services/ledger/migrations/000009_rls_roles.up.sql`, `services/auth/migrations/000020_closure_finalize_function.up.sql`, `services/auth/internal/auth/security_contract_integration_test.go` |
| Dispute amount constraints | Dispute amount/currency is bounded against the original transaction at the database trigger boundary. | `services/ledger/migrations/000046_chargeback_dispute_amount_deadline.up.sql`, `services/ledger/internal/ledger/schema_contract_test.go` |
| Active-rule overlap | Fee-rule version overlap is rejected under a transaction advisory lock and maker/checker approval is enforced. | `services/ledger/migrations/000044_fee_rule_maker_checker.up.sql`, `services/ledger/internal/ledger/schema_contract_test.go` |
| Terminal-state immutability | Terminal dispute fields and immutable financial evidence are protected by database triggers and grants. | `services/ledger/migrations/000047_chargeback_dispute_terminal_immutability.up.sql`, `services/ledger/migrations/000042_money_movement_execution_audit.up.sql`, `services/ledger/internal/ledger/schema_contract_test.go` |
| Audit-column immutability | Policy decisions are inserted before posting; update/delete/truncate are revoked from `app_service`, with append-only RLS. | `services/ledger/migrations/000042_money_movement_execution_audit.up.sql`, `services/ledger/internal/ledger/command/executor_test.go` |
| Tenant boundary enforcement | Tenant IDs are explicit in execution context and merchant repository predicates; cross-tenant reads/writes return no resource. | `services/ledger/internal/repository/execution_subject_repository.go`, `services/gateway/internal/merchant/repository`, `services/gateway/internal/merchant/repository/repository_integration_test.go` |
| Privileged maintenance paths | Closure finalization invokes only `public.fn_auth_finalize_credentials(uuid)`, with fixed `search_path`, revoked PUBLIC execute, and no direct app-role table delete. | `services/auth/internal/auth/closure_worker.go`, `services/auth/migrations/000020_closure_finalize_function.up.sql`, `services/auth/internal/auth/security_contract_integration_test.go` |
| Direct internal posting access | Production code is checked for direct low-level posting calls; feature paths enter the command executor. | `services/ledger/internal/ledger/architecture_boundary_test.go`, `services/ledger/internal/ledger/ledger.go` |

## Verification

Unit and static checks:

```sh
go test ./services/ledger/internal/ledger/command ./services/auth
go test ./services/ledger -run TestProductionPostingDoesNotBypassCommandBoundary
```

PostgreSQL role and migration checks (Docker/Testcontainers required):

```sh
go test -tags=integration ./services/auth -run TestSecurity_ClosureFinalizerUsesNarrowPrivilege
go test -tags=integration ./services/ledger -run 'TestSchemaContract_(AppServiceRole|LedgerEntriesImmutable|ChargebackDispute|Fee)'
```

## External gate

The independent security review remains `evidence_required`. The scope,
finding schema, and exit criteria are defined in
[`docs/security/independent-review-scope.md`](../security/independent-review-scope.md),
and the evidence is indexed by the
[independent-review acceptance packet](independent-security-review.md).
Completion still requires an independent dated report, ranked findings,
reproduction evidence, remediation or risk-acceptance decisions, and a retest
statement. Repository tests and an internal self-review cannot satisfy this
gate.
