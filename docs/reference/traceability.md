# Concept-to-Code Traceability

> [Documentation home](../README.md) · [Reference](README.md)

> **Status: Current. Audience: readers who understand the story and now want to
> verify it.** This page does not teach each feature again. It points from the
> plain-language claim to its owner, implementation, durable data, and proof.

## Why this map exists

A public repository should make important claims inspectable. “Top-up is
idempotent” is more trustworthy when a reader can find:

1. the document explaining what that means;
2. the service that owns the decision;
3. the code implementing it;
4. the schema preserving it; and
5. the test or journey proving it.

Use the [visual story](../learn/visual-story.md), [beginner guide](../learn/beginner-guide.md),
[product tour](../learn/product-tour.md), or [why guide](rationale.md) for explanation. Use
this page only when you want to inspect evidence.

## How code is normally followed

```mermaid
flowchart LR
    Start[cmd: start and connect] --> Transport[HTTP or gRPC entry]
    Transport --> Domain[Business decision]
    Domain --> Repository[Data interface]
    Repository --> Migration[Database shape]
    Domain --> Test[Unit and integration proof]
    Test --> Journey[End-to-end proof]
```

- `cmd/` creates dependencies and starts a process.
- A transport handler translates an HTTP or gRPC request.
- Domain code decides what is allowed.
- A repository stores or reads owned data.
- Migrations define the durable database structure.
- Tests prove small rules; scripts prove complete journeys and recovery.

## System startup and boundaries

| Question | Source |
|---|---|
| Which programs can be started? | Service entrypoints under [`cmd/`](../../cmd/) |
| How are local services and dependencies connected? | [`docker-compose.yml`](../../docker-compose.yml) |
| Which service imports are forbidden? | [`boundary_test.go`](../../boundary_test.go) |
| Where is configuration loaded? | [`internal/config/config.go`](../../internal/config/config.go) |
| What proves fresh containers can start together? | [`scripts/smoke-container.sh`](../../scripts/smoke-container.sh) |

The executable system currently has eight services. Utilities under `cmd/`,
such as the certificate generator and documentation checker, are not services
because they do not keep listening for requests.

## Identity and KYC

| Layer | Source |
|---|---|
| Plain explanation | [Product tour: identity, login, and KYC](../learn/product-tour.md#journey-1-identity-login-and-kyc) |
| Owner and interfaces | [Services: Auth](services.md#auth) |
| HTTP entry and authentication | [`internal/auth/http.go`](../../internal/auth/http.go), [`internal/auth/auth.go`](../../internal/auth/auth.go) |
| KYC decision and recovery | [`internal/auth/kyc.go`](../../internal/auth/kyc.go), [`internal/auth/worker/retry.go`](../../internal/auth/worker/retry.go) |
| Owned data | [`migrations/auth/`](../../migrations/auth/) |
| Focused proof | [`internal/auth/kyc_integration_test.go`](../../internal/auth/kyc_integration_test.go), [`internal/ledger/grpcserver/kyc_tier_integration_test.go`](../../internal/ledger/grpcserver/kyc_tier_integration_test.go) |
| Complete proof | KYC section in [`scripts/business-e2e.sh`](../../scripts/business-e2e.sh) |

The key cross-service rule is “limits first, claim second”: Ledger receives the
new policy tier before Auth exposes the upgraded KYC claim in refreshed tokens.

## Top-up and vendor callback

| Layer | Source |
|---|---|
| Plain explanation | [Visual story: top-up ticket](../learn/visual-story.md#scene-2-mia-asks-to-add-100000), [Product tour: adding money](../learn/product-tour.md#journey-3-adding-money) |
| Public entry | [`internal/handler/topup.go`](../../internal/handler/topup.go), [`internal/handler/webhook.go`](../../internal/handler/webhook.go) |
| Internal contract | [`api/proto/seev/payin/v1/payin.proto`](../../api/proto/seev/payin/v1/payin.proto) |
| Intent and callback decisions | [`internal/payin/topup.go`](../../internal/payin/topup.go), [`internal/payin/payin.go`](../../internal/payin/payin.go) |
| Vendor adapter boundary today | [`internal/vendorgw/`](../../internal/vendorgw/) |
| Owned data | [`migrations/payin/`](../../migrations/payin/) |
| Focused proof | [`internal/payin/topup_test.go`](../../internal/payin/topup_test.go), [`internal/payin/payin_integration_test.go`](../../internal/payin/payin_integration_test.go) |
| Complete proof | Top-up section in [`scripts/business-e2e.sh`](../../scripts/business-e2e.sh) |

The tests also expose the current legacy unmatched-intent fallback. The target
replacement is [plan 54](../roadmap/active/54-vendor-service-boundary.md); it is not current
code.

## Fee quote and user-to-user transfer

| Layer | Source |
|---|---|
| Plain explanation | [Worked balance example](../learn/product-tour.md#a-worked-example-with-visible-balances), [Why store fee quotes?](rationale.md#why-store-fee-quotes) |
| HTTP entry | [`internal/ledger/transport/http.go`](../../internal/ledger/transport/http.go) |
| Fee selection and quote consumption | [`internal/ledger/feepolicy/feepolicy.go`](../../internal/ledger/feepolicy/feepolicy.go), [`internal/ledger/feepolicy/quote.go`](../../internal/ledger/feepolicy/quote.go) |
| Transfer accounting | [`internal/ledger/processors/transfer_p2p.go`](../../internal/ledger/processors/transfer_p2p.go) |
| Atomic posting engine | [`internal/ledger/service/handle/service.go`](../../internal/ledger/service/handle/service.go) |
| Owned data | [`migrations/ledger/`](../../migrations/ledger/) |
| Focused proof | [`internal/ledger/execquote_integration_test.go`](../../internal/ledger/execquote_integration_test.go), [`internal/ledger/service/handle/service_test.go`](../../internal/ledger/service/handle/service_test.go) |
| Complete proof | Transfer and fee-quote sections in [`scripts/business-e2e.sh`](../../scripts/business-e2e.sh) |

The transfer processor shows the exact fee semantics: the sender loses the
requested amount, while the receiver gets that amount minus the fee and the
fee account gets the remainder.

## Withdrawal and vendor uncertainty

| Layer | Source |
|---|---|
| Plain explanation | [Visual story: withdrawal hold](../learn/visual-story.md#scene-6-mia-asks-to-withdraw-20000), [Product tour: withdrawing money](../learn/product-tour.md#journey-5-withdrawing-money) |
| Public entry | [`internal/handler/payout.go`](../../internal/handler/payout.go) |
| Internal contract | [`api/proto/seev/payout/v1/payout.proto`](../../api/proto/seev/payout/v1/payout.proto) |
| Workflow and state transitions | [`internal/payout/orchestrate.go`](../../internal/payout/orchestrate.go), [`internal/payout/payout.go`](../../internal/payout/payout.go) |
| Durable vendor dispatch | [`internal/payout/relay.go`](../../internal/payout/relay.go), [`internal/payout/worker/vendor_relay.go`](../../internal/payout/worker/vendor_relay.go) |
| Crash recovery | [`internal/payout/worker/resume.go`](../../internal/payout/worker/resume.go) |
| Hold close accounting | [`internal/ledger/processors/withdraw_settle.go`](../../internal/ledger/processors/withdraw_settle.go), [`internal/ledger/processors/withdraw_cancel.go`](../../internal/ledger/processors/withdraw_cancel.go) |
| Owned data | [`migrations/payout/`](../../migrations/payout/) |
| Race and recovery proof | [`internal/payout/race_integration_test.go`](../../internal/payout/race_integration_test.go), payout scenarios in [`scripts/chaos-test.sh`](../../scripts/chaos-test.sh) |

The durable command proves that work survives a crash. The vendor-call outcome
and pinned request prove why an uncertain result cannot blindly fail over.

## Ledger event and notification

| Layer | Source |
|---|---|
| Wire contract | [`docs/reference/events.md`](events.md) and [`internal/ledger/events/events.go`](../../internal/ledger/events/events.go) |
| Outbox storage and relay | [`internal/ledger/repository/outbox_event_repository.go`](../../internal/ledger/repository/outbox_event_repository.go), [`internal/ledger/worker/outbox_relay.go`](../../internal/ledger/worker/outbox_relay.go) |
| Notification consumer | [`internal/notify/notify.go`](../../internal/notify/notify.go) |
| Notification storage | [`migrations/gateway/`](../../migrations/gateway/) |
| Focused proof | [`internal/notify/notify_integration_test.go`](../../internal/notify/notify_integration_test.go), [`internal/ledger/worker/outbox_relay_test.go`](../../internal/ledger/worker/outbox_relay_test.go) |
| Complete proof | Notification checks in [`scripts/business-e2e.sh`](../../scripts/business-e2e.sh) |

Current notification behavior consumes generic Ledger events. Plan 54 defines
the target owner-domain terminal events for Payin and Payout.

## Fraud and sanctions screening

| Layer | Source |
|---|---|
| Owner and boundaries | [Services: Fraud](services.md#fraud) |
| Synchronous decision | [`internal/fraud/fraud.go`](../../internal/fraud/fraud.go), [`internal/fraud/rules/`](../../internal/fraud/rules/) |
| Asynchronous event processing | [`internal/fraud/consumer.go`](../../internal/fraud/consumer.go) |
| Sanctions data | [`internal/fraud/sanctions/`](../../internal/fraud/sanctions/), [`cmd/sanctions-loader/`](../../cmd/sanctions-loader/) |
| Owned data | [`migrations/fraud/`](../../migrations/fraud/) |
| Proof | [`internal/fraud/fraud_test.go`](../../internal/fraud/fraud_test.go), [`internal/fraud/consumer_integration_test.go`](../../internal/fraud/consumer_integration_test.go) |

Failure policy is decided at each caller boundary. Fraud never writes a Ledger
balance.

## Operator controls and audit

| Layer | Source |
|---|---|
| Plain explanation | [Product tour: operator actions](../learn/product-tour.md#journey-7-operator-actions) |
| Operator service | [`internal/adminbff/`](../../internal/adminbff/) |
| Sessions and login | [`internal/adminbff/session.go`](../../internal/adminbff/session.go), [`internal/adminbff/login.go`](../../internal/adminbff/login.go) |
| Proxy and audit | [`internal/adminbff/proxy.go`](../../internal/adminbff/proxy.go), [`internal/adminbff/audit.go`](../../internal/adminbff/audit.go) |
| Ledger maker-checker | [`internal/ledger/service/adjustments/adjustments.go`](../../internal/ledger/service/adjustments/adjustments.go) |
| Owned data | [`migrations/adminbff/`](../../migrations/adminbff/) |
| Complete proof | [`scripts/admin-e2e.sh`](../../scripts/admin-e2e.sh) |

Admin BFF provides a controlled interface, but the owning service repeats the
important authorization rule so direct calls cannot bypass it.

## Reconciliation and independent assurance

| Layer | Source |
|---|---|
| Plain explanation | [Product tour: reconciliation](../learn/product-tour.md#journey-8-reconciliation), [independent assurance](../learn/product-tour.md#journey-9-independent-assurance) |
| External reconciliation | [`internal/ledger/service/recon/recon.go`](../../internal/ledger/service/recon/recon.go) |
| Assurance correlation | [`internal/assurance/correlation.go`](../../internal/assurance/correlation.go), [`internal/assurance/rules/rules.go`](../../internal/assurance/rules/rules.go) |
| Finding lifecycle | [`internal/assurance/finding.go`](../../internal/assurance/finding.go) |
| Emergency intake control | [`internal/payin/intake.go`](../../internal/payin/intake.go), [`internal/payout/intake.go`](../../internal/payout/intake.go) |
| Owned data | [`migrations/assurance/`](../../migrations/assurance/) |
| Operational proof | Assurance scenarios in [`scripts/chaos-test.sh`](../../scripts/chaos-test.sh), [`scripts/product-assurance.sh`](../../scripts/product-assurance.sh) |

Reconciliation compares Ledger with outside reports. Assurance compares Seev's
internally owned records. Neither silently rewrites history.

## Internal security and observation

| Layer | Source |
|---|---|
| Security assumptions | [Threat model](../security/threat-model.md) |
| mTLS identity | [`pkg/tlsx/`](../../pkg/tlsx/) |
| gRPC authentication and middleware | [`pkg/grpcx/`](../../pkg/grpcx/) |
| HTTP request controls | [`pkg/middleware/`](../../pkg/middleware/) |
| Structured masking | [`pkg/logger/`](../../pkg/logger/) |
| Tracing | [`pkg/tracing/`](../../pkg/tracing/) |
| Dashboards and alerts | [`deploy/observability/`](../../deploy/observability/) |
| Proof | [`scripts/rotation-drill.sh`](../../scripts/rotation-drill.sh), security scenarios in [`scripts/chaos-test.sh`](../../scripts/chaos-test.sh) |

This evidence proves repository behavior, not production certification. Cloud
perimeters, real vendor links, legal requirements, and production secret
management remain deployment-specific.

## When this page and the code disagree

Executable code, schema, and tests are the current source of truth. Correct
this traceability page in the same change that moves an implementation path.
Do not update a link to a target file that does not exist yet, and do not use a
historical plan as evidence for current runtime behavior.
