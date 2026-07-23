# Why Seev Works This Way

> [Documentation home](../README.md) · [Reference](README.md)

> **Status: Current rationale, with target designs explicitly labeled. Audience:
> anyone asking “why not do it the easier way?”** This guide explains the
> reasons and tradeoffs without requiring source-code knowledge.

## How to read this guide

Every decision is explained with three questions:

- **Reason:** what useful property does the choice provide?
- **Risk prevented:** what could go wrong without it?
- **Cost:** what complexity or limitation does Seev accept in return?

An architecture decision is not automatically good because it sounds safe or
modern. It is useful only when its benefit justifies its cost.

## Why build a backend without a customer app?

- **Reason:** Seev focuses on the part that decides identity, workflow state,
  balances, accounting, risk, and recovery. Any mobile or web interface can be
  built on top of those contracts.
- **Risk prevented:** a polished screen can make an unsafe money system look
  complete. This repository makes the difficult backend behavior visible and
  testable first.
- **Cost:** a nontechnical visitor cannot click through a finished wallet UI.
  The beginner guide and product tour therefore carry more responsibility.

## Why is Ledger the only place where money becomes real?

- **Reason:** every balance-affecting operation follows one accounting engine
  and one set of invariants.
- **Risk prevented:** Payin, Payout, an administrator, or a notification cannot
  invent a second way to change balances.
- **Cost:** every money workflow depends on Ledger's contract and availability
  for final posting.

## Why use double-entry accounting?

- **Reason:** every movement has equal debit and credit sides. The system can
  prove that value was not created or lost inside Ledger.
- **Risk prevented:** updating only one balance can leave money without a
  matching source or destination after a bug or partial failure.
- **Cost:** even a simple transfer requires accounts, entries, balancing rules,
  and more careful reporting.

## Why is Ledger history append-only?

- **Reason:** the full sequence of original actions and corrections remains
  reconstructable.
- **Risk prevented:** an update or delete could hide how a balance reached its
  current value.
- **Cost:** corrections require compensating transactions, and reports must
  understand both the original and its correction.

## Why use whole minor units or exact decimals instead of floating point?

- **Reason:** monetary arithmetic remains exact.
- **Risk prevented:** binary floating-point rounding can produce values such as
  `0.1 + 0.2` that are not exactly `0.3` internally.
- **Cost:** APIs use decimal strings and code uses specialized amount types
  instead of convenient general-purpose numbers.

## Why require an idempotency key?

- **Reason:** one intended operation keeps one stable identity across retries.
- **Risk prevented:** a lost response followed by a retry must not create a
  second transfer, top-up, or payout.
- **Cost:** callers must generate, store, and reuse the correct key. Seev must
  persist enough result information to answer repeats consistently.

## Why create a top-up intent before accepting confirmation?

- **Reason:** Seev records who expects which amount, currency, vendor, and
  reference before an outside message arrives.
- **Risk prevented:** a vendor callback must not choose an arbitrary internal
  user or create an unexpected credit.
- **Cost:** top-ups have a multi-step lifecycle with expiry, unmatched events,
  and reconciliation instead of one immediate request.

> **Current gap:** Payin still contains a legacy unmatched-intent fallback to a
> vendor payload's `user_id`. This does not satisfy the reason above.
> [Plan 54](../roadmap/active/54-vendor-service-boundary.md) removes it and makes strict
> intent correlation mandatory.

## Why authenticate a callback and still validate its business data?

- **Reason:** a signature answers “did someone with the secret send these
  bytes?” Intent validation answers “does this message match work Seev
  expected?”
- **Risk prevented:** an authentic vendor can still send an old, duplicated,
  malformed, mismatched, or wrongly correlated event.
- **Cost:** callback handling needs both vendor-specific cryptography and
  domain-specific validation.

## Why reserve a withdrawal with a hold?

- **Reason:** the requested amount becomes unavailable while an external payout
  is unfinished.
- **Risk prevented:** the user cannot spend the same funds through another
  request while the vendor may still pay the first one.
- **Cost:** Payout must manage hold, settle, and cancel transitions and recover
  incomplete work.

## Why not fail over to another payout vendor after every timeout?

- **Reason:** a timeout means the result is unknown, not necessarily failed.
- **Risk prevented:** the first vendor may have accepted the payout before the
  connection disappeared; a second vendor could pay again.
- **Cost:** some withdrawals remain pending while Payout queries, retries safely,
  or waits for operator evidence. Availability is deliberately sacrificed to
  prevent duplicate payout.

## Why charge a payout fee only when settlement succeeds?

- **Reason:** the fee represents a completed withdrawal service.
- **Risk prevented:** charging during the hold could strand a fee when the
  withdrawal is later cancelled.
- **Cost:** the final settlement posting has an additional accounting leg and
  must consume the exact fee decision.

## Why store fee quotes?

- **Reason:** the price shown to a user remains the price used by the posting,
  even if an operator changes the underlying rule in between.
- **Risk prevented:** silent repricing and quote tampering.
- **Cost:** quotes expire, are single-use, require matching fields, and need
  durable storage and cleanup.

## Why store an outbox row with the Ledger transaction?

- **Reason:** the money record and the promise to publish its event commit
  together.
- **Risk prevented:** a crash after posting but before publishing must not make
  downstream systems miss the event forever.
- **Cost:** a relay worker, retry policy, dead-letter operations, lag metrics,
  handling for deliveries that exhaust retries, lag metrics, and consumer
  deduplication are required.

## Why can notification happen later than money movement?

- **Reason:** notification delivery is useful but not part of accounting
  correctness.
- **Risk prevented:** a broken message broker or notification handler must not
  roll back or block a valid Ledger posting.
- **Cost:** the user interface must tolerate eventual delivery and obtain
  authoritative status from the owning service when needed.

> **Target improvement:** plan 54 changes Payin/Payout terminal notifications
> from generic Ledger events to owner-domain final events, preventing a success
> message while local workflow finalization is incomplete.

## Why does each service own a separate database?

- **Reason:** data ownership follows business responsibility and can be
  enforced operationally.
- **Risk prevented:** one service cannot silently change another service's
  state or depend on undocumented table details.
- **Cost:** cross-service work needs APIs, authentication, retries,
  observability, and reconciliation. A local join becomes a distributed
  workflow.

## Why not put every capability in one service?

- **Reason:** identity, accounting, vendor workflows, risk, operator tools, and
  independent assurance have different data, failure, scaling, and security
  concerns.
- **Risk prevented:** one deployment or incident need not expose or stop every
  capability.
- **Cost:** eight current services are harder to run and understand than one
  program. Seev therefore started as one deployable program with clearly
  separated internal modules, then extracted services only after explicit
  triggers.

## Why is Auth a separate public entry point?

- **Reason:** registration and login have their own credential, token, and
  abuse controls rather than being ordinary wallet composition requests.
- **Risk prevented:** Gateway does not become the owner or storage boundary for
  passwords and KYC identity data.
- **Cost:** clients and deployments must understand two public entry points in
  the current topology.

## Why use both direct requests and asynchronous events?

- **Reason:** direct requests answer work that needs an immediate decision;
  events announce committed facts to independent followers.
- **Risk prevented:** making a balance posting wait for notifications,
  analytics, or post-transaction fraud processing creates unnecessary failure
  coupling.
- **Cost:** developers must understand two communication models, eventual
  delivery, and duplicate messages.

## Why use Redis if it is not a source of truth?

- **Reason:** Redis is efficient for short-lived counters, rate limits, cache,
  coordination, and shared circuit-breaker state.
- **Risk prevented:** high-frequency temporary data does not overload durable
  business tables.
- **Cost:** every use needs an explicit outage policy. Durable money state can
  never exist only in Redis.

## Why is Fraud separate from Ledger?

- **Reason:** risk rules can evolve and process asynchronous patterns without
  owning accounting data.
- **Risk prevented:** risk analysis cannot directly edit balances, and Ledger
  does not become a container for every compliance concern.
- **Cost:** each call boundary must explicitly choose whether an unavailable
  risk dependency blocks or permits the action.

## Why do different failures use fail-open or fail-closed behavior?

- **Reason:** not every dependency protects the same risk. Optional tracing can
  fail without changing a money decision; a required velocity check may not.
- **Risk prevented:** one universal policy would either stop too much work or
  allow unsafe work.
- **Cost:** every boundary needs documented behavior, tests, metrics, and review
  instead of relying on a single global default.

## Why have Assurance if services already test themselves?

- **Reason:** Assurance compares independently owned production-like records
  after the original workflow.
- **Risk prevented:** Payin and Ledger can each be internally consistent while
  disagreeing with each other about the same top-up.
- **Cost:** Assurance needs read-only contracts, cursors, findings, alert
  delivery, and operator handling. It reports problems but does not silently
  repair them.

## Why have reconciliation as well as Assurance?

- **Reason:** Assurance compares Seev's internal domains; reconciliation
  compares Seev with external settlement evidence.
- **Risk prevented:** a perfectly balanced internal Ledger could still
  disagree with a bank or vendor that moved real funds differently.
- **Cost:** operators need external reports, matching rules, investigation, and
  governed resolution.

## Why require two operators for sensitive actions?

- **Reason:** one person proposes and another approves adjustments or risky
  recovery actions.
- **Risk prevented:** one compromised account or human mistake cannot perform
  the complete sensitive action alone.
- **Cost:** urgent recovery can take longer and requires two available,
  authorized people.

## Why enforce service identity with mTLS and an allowlist?

- **Reason:** encryption protects the connection, certificates identify both
  peers, and the receiver still decides which identity may call it.
- **Risk prevented:** being inside the network or holding any certificate is
  not enough to invoke every internal API.
- **Cost:** certificate issuance, rotation, expiry monitoring, trusted
  certificate-authority management, and per-hop allowlists become
  operational responsibilities.

## Why keep logs, metrics, traces, and request IDs?

- **Reason:** each shows a different view: discrete records, numerical trends,
  cross-service timing, and correlation for one journey.
- **Risk prevented:** operators are not forced to guess from process health or
  query sensitive databases directly during an incident.
- **Cost:** instrumentation must avoid leaking secrets or creating high-cardinality
  metric labels with too many unique values, and the observability stack
  consumes resources.

## Why run chaos tests?

- **Reason:** recovery claims are exercised by actually stopping processes or
  dependencies at controlled points.
- **Risk prevented:** a recovery design that exists only on paper may fail for
  the first time during a real incident.
- **Cost:** chaos suites are slower, require isolated state, and are more
  expensive than unit tests.

## Why should vendor traffic move to VendorService?

This is a **target**, not current behavior.

- **Reason:** vendor APIs and callbacks share machine-to-machine secrets,
  allowlists, adapters, retries, and audit needs that differ from user Gateway
  traffic.
- **Risk prevented:** Gateway does not remain a broad public callback edge, and
  Payin/Payout do not each embed direct vendor connectivity.
- **Cost:** VendorService becomes a ninth deployment and database with new
  internal contracts and another durable delivery boundary.

Payin and Payout still validate their own intent or request. VendorService
authenticates and normalizes vendor communication; it must never decide which
user receives money.

## A quick “what if we removed it?” map

| Remove this control | Likely consequence |
|---|---|
| Idempotency | A retry can repeat money movement |
| Balanced entries | One side can change without an equal counterpart |
| Append-only history | Corrections can hide the original event |
| Top-up intent | A callback can lack trustworthy internal ownership |
| Withdrawal hold | The same available funds can be spent twice |
| Payout uncertainty state | Timeout handling can trigger duplicate payout |
| Transactional outbox | Posting and event publication can permanently diverge |
| Service database ownership | Hidden cross-service coupling and unauthorized writes |
| Reconciliation | Internal records can disagree with real external settlement unnoticed |
| Assurance | Internal domain disagreement can remain invisible |
| Maker-checker | One operator can complete a sensitive change alone |
| Failure drills | Recovery remains an untested assumption |

## Check your understanding

You understand Seev's main reasoning if you can answer these without source
code:

1. Why is a pending top-up not money yet?
2. Why are signature verification and intent correlation different checks?
3. Why can a payout timeout not automatically use another vendor?
4. Why can a notification be late without losing the Ledger posting?
5. Why can a balanced Ledger still disagree with a bank?
6. Why can Assurance report or pause but not repair money?
7. Why is one database per service safer but operationally harder?
8. Which VendorService statements are target design rather than current code?

If an answer is unclear, follow its heading above, then use the
[glossary](glossary.md) for terminology and the
[product tour](../learn/product-tour.md) for the complete journey.
