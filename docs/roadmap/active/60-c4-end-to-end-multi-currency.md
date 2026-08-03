# Plan 60 — C4 End-to-End Multi-Currency Activation

**Created:** 2026-07-28
**Status:** Implementation present; runtime acceptance evidence pending
**Roadmap track:** C4 — End-to-end multi-currency activation
**Activation trigger:** Conscious FX and multi-currency learning decision
**Initial supported currencies:** IDR and USD
**Initial FX pair:** IDR/USD in both conversion directions
**Primary money owner:** LedgerService
**Journey owners:** Gateway, PayinService, PayoutService, VendorService
**Control and operator surface:** Admin BFF
**Supporting owners:** FraudService, AssuranceService, notification module
**Rate source:** Versioned database-managed mock rates
**No real bank corridor, real market feed, or real-money claim is authorized.**

The implementation is present in the current Ledger, Gateway, Admin BFF, and
shared currency paths. Keep this plan active until the C4 entry/final evidence
and runtime journey checks are recorded; see the [current-state inventory](../../reference/current-state.md).

---

## 1. Purpose

Activate the repository's existing currency primitives across the complete
money journey.

C4 must make non-IDR behavior observable and safe for:

- wallet/account provisioning;
- balance reads;
- same-currency peer transfer;
- top-up;
- payout;
- fee resolution and fee quotes;
- fraud and policy evaluation;
- statements, reporting, notifications, and operator tools;
- explicit FX quote and conversion;
- FX position control;
- reconciliation and assurance;
- recovery, duplicate handling, and anti-mixing chaos tests.

The plan must preserve the following foundational rules:

1. LedgerService remains the source of truth for money.
2. Every ledger transaction is single-currency.
3. Every entry in a ledger transaction belongs to an account of that same
   currency.
4. Normal transfer, top-up, payout, refund, fee, hold, settlement, and reversal
   never perform implicit currency conversion.
5. Cross-currency movement occurs only through an explicit FX quote and
   conversion aggregate.
6. FX conversion is represented by two individually balanced single-currency
   ledger transactions linked by one conversion record.
7. Both FX legs commit atomically in one Ledger database transaction.
8. All amounts remain signed-safe integer minor units.
9. Decimal display values and FX rates never pass through binary floating point.
10. A consumed quote is never repriced.
11. A rate update does not change an existing quote or conversion.
12. A disabled currency blocks new intake but does not corrupt or hide existing
    balances and in-flight operations.
13. Currency, pair, operation, fee, route, and position policies are explicit.
14. Each service owns only its own database.
15. No service reads another service's database.
16. No real external rate provider or banking corridor is introduced.
17. Mock vendors may simulate USD only after their capability is declared.
18. No amount from different currencies is summed as though it were one amount.
19. Verification, alerts, and reports group monetary totals by currency.
20. Existing IDR contracts and journeys remain compatible.
21. C4 does not create a new application service.
22. C4 does not claim treasury, accounting, regulatory, or production FX
    completeness.

---

## 2. Existing foundation

C4 is activation work, not a ground-up multi-currency redesign.

The repository already contains:

- `accounts.currency`;
- `ledger_transactions.currency`;
- integer `BIGINT` amounts;
- `currencies(code, minor_unit, enabled)`;
- IDR exponent `0`;
- USD exponent `2`;
- `pkg/currency`;
- user and system account uniqueness that includes currency;
- currency-specific system account resolution;
- USD settlement, fee, escrow, chargeback, adjustment, confiscated, and
  suspense accounts;
- IDR/USD FX position accounts;
- `fx_out` and `fx_in` processors;
- explicit cross-currency rejection in ordinary transfer;
- fee rules keyed by currency;
- fee quotes containing currency;
- Payin top-up intents containing currency;
- Payout requests containing currency;
- Ledger `GetUserCurrency`, `ResolveFee`, and related internal operations;
- transactional outbox and versioned event governance;
- per-service databases and typed gRPC boundaries;
- product assurance and intake pause/resume controls;
- backup, privacy, internal security, observability, and contract gates.

The archived Phase 3b implementation deliberately stopped before public
end-to-end activation. Its FX flow was an internal primitive and its public
journeys remained effectively IDR-first.

C4 must first verify the live code and then close the activation gaps.

---

## 3. Activation and entry gate

### 3.1 Activation decision

C4 is activated on 2026-07-28 as a conscious learning decision for:

- exact multi-currency money modeling;
- minor-unit differences;
- currency-specific system accounts;
- explicit conversion;
- rate snapshots;
- deterministic rounding;
- FX position control;
- cross-service currency propagation;
- per-currency policy and routing;
- anti-mixing defenses;
- cross-currency recovery and reconciliation.

The roadmap trigger is therefore satisfied.

### 3.2 Required entry-gate evidence

T0 records a fresh result for every item below.

- [ ] `make contracts` passes from a clean tree.
- [ ] Protobuf, OpenAPI, event, and compatibility gates pass.
- [ ] Ledger, Payin, Payout, VendorService, Fraud, Assurance, Gateway, and
      Admin BFF tests pass.
- [ ] Current IDR business E2E passes.
- [ ] Current callback and payout recovery E2E passes.
- [ ] Existing currency package tests pass.
- [ ] Existing FX processor tests pass.
- [ ] Ledger migrations `000011` through `000013` are present and verified.
- [ ] IDR and USD registry rows are confirmed.
- [ ] Required IDR and USD system accounts are inventoried.
- [ ] Existing account-provisioning behavior by currency is recorded.
- [ ] Existing public and internal request fields carrying currency are
      inventoried.
- [ ] Every hard-coded `IDR`, exponent, formatter, default, and currencyless
      policy lookup is inventoried.
- [ ] Every database query that compares or aggregates amounts is reviewed for
      currency grouping.
- [ ] Every event carrying money is reviewed for currency.
- [ ] Every idempotency fingerprint involving money is reviewed for currency.
- [ ] Every route/vendor capability involving money is reviewed for currency.
- [ ] Existing FX implementation is inspected for atomicity across both legs.
- [ ] Existing verifier, snapshot, statement, reporting, notification, privacy,
      backup, and assurance behavior is reviewed by currency.
- [ ] Exact baseline commit and migration heads are recorded.
- [ ] There is no unrelated large Ledger migration in flight.

### 3.3 Entry-gate deliverables

```text
docs/evidence/c4-entry-gate.md
docs/reference/c4-currency-propagation-inventory.md
docs/reference/c4-hardcoded-idr-inventory.md
docs/reference/c4-system-account-matrix.md
docs/reference/c4-policy-route-matrix.md
docs/reference/c4-fx-current-state.md
```

### 3.4 Gate policy

The following may begin before every entry item is green:

- documentation;
- exact-math utilities;
- synthetic rate fixtures;
- schema drafts;
- threat modeling;
- OpenAPI and Protobuf design;
- test matrices;
- Admin BFF page wireframes.

The following may not merge before the gate is green:

- non-IDR public money writes;
- USD vendor route activation;
- public FX quote/conversion;
- a change to Ledger posting invariants;
- pair/rate publication;
- position-limit enforcement;
- a migration that changes existing IDR account or transaction semantics.

---

## 4. Locked product scope

## 4.1 Currency set

C4 activates exactly:

```text
IDR
USD
```

No third currency is added.

The architecture remains extensible, but EUR, SGD, JPY, cryptoassets, stablecoins,
and tokenized money are out of scope.

### 4.2 Currency exponents

```text
IDR minor_unit = 0
USD minor_unit = 2
```

Examples:

```text
IDR 125000  -> 125,000 rupiah
USD 125000  -> 1,250.00 dollars
```

The same integer has a different display meaning by currency.

### 4.3 Public journeys

C4 supports:

```text
IDR top-up
USD top-up through mock route
IDR same-currency transfer
USD same-currency transfer
IDR payout through mock route
USD payout through mock route
IDR -> USD explicit conversion
USD -> IDR explicit conversion
```

### 4.4 Explicit non-support

C4 does not support:

```text
IDR sender -> USD receiver in one transfer call
automatic top-up conversion
automatic payout conversion
automatic fee conversion
automatic fallback to another currency
one combined “total wallet balance”
real bank USD deposit
real international payout
real remittance
market order
limit order
forward contract
hedging
netting across external institutions
cryptocurrency
```

### 4.5 User interaction rule

A user who wants to send or withdraw USD must first own and fund a USD cash
account.

A user with only IDR may:

1. explicitly enable USD;
2. request an IDR-to-USD FX quote;
3. accept the quote;
4. receive USD;
5. perform a normal USD transfer or payout.

There is no invisible conversion inside step 5.

---

## 5. Locked architecture decisions

## 5.1 No FX service

C4 does not create `FxService`.

LedgerService owns:

- currency registry;
- currency-specific accounts;
- exact posting;
- FX pair policy;
- mock rate versions;
- FX quotes;
- FX conversions;
- FX position accounts;
- position limits;
- conversion reconciliation.

This keeps rate snapshot, quote consumption, both ledger legs, and position
checks inside one database transaction boundary.

### 5.2 Journey ownership remains unchanged

- Gateway owns public HTTP authentication and DTO mapping.
- Ledger owns account and money correctness.
- Payin owns top-up intent lifecycle.
- Payout owns payout lifecycle and hold/settle/release.
- VendorService owns mock vendor protocol and callbacks.
- Fraud owns screening.
- Assurance owns cross-service consistency checks.
- Admin BFF owns operator browser workflows.

### 5.3 IDR remains backward-compatible default

Existing IDR users and contracts remain valid.

T0 must determine where the current API permits omitted currency.

Policy:

- do not silently change existing v1 behavior;
- where v1 currently defaults to IDR, preserve that compatibility;
- all new multi-currency endpoints require explicit currency;
- documentation marks legacy IDR default behavior;
- a future major contract may remove implicit defaulting.

### 5.4 Account-per-currency wallet model

A user's wallet is a set of currency-specific Ledger accounts.

Example:

```text
user A
├── cash IDR
├── hold IDR
├── pending IDR
├── frozen IDR
├── cash USD
├── hold USD
├── pending USD
└── frozen USD
```

No account contains more than one currency.

### 5.5 Enabled currency means provisioned account family

A currency is enabled for a user when the required account family exists and is
active.

Ledger remains authoritative.

Do not introduce a duplicate Gateway wallet-balance table.

### 5.6 Normal posting remains single-currency

Every normal Ledger transaction has one currency.

All resolved accounts must match it.

This includes:

```text
money_in
transfer_p2p
fee_collect
withdraw_initiate
withdraw_settle
withdraw_cancel
refund
chargeback
freeze
release
adjustment
interest
scheduled transaction
disbursement
```

### 5.7 FX is an aggregate of two single-currency postings

One conversion contains:

```text
source leg: source user cash <-> source FX position account
target leg: target FX position account <-> target user cash
```

Both legs:

- have separate transaction IDs;
- have separate currencies;
- are balanced within their own currency;
- reference one conversion ID;
- commit atomically.

### 5.8 Atomicity is mandatory

The public conversion operation may not call two independent committed Ledger
`Post` RPCs.

C4 must provide an internal Ledger application operation that executes:

```text
quote validation
position validation
source posting
target posting
quote consumption
conversion status update
outbox insertion
```

inside one PostgreSQL transaction.

A crash before commit leaves neither leg posted.

A crash after commit returns the existing conversion on retry.

### 5.9 Mock rates are database-managed

No network rate dependency is introduced.

Rates are:

- synthetic;
- versioned;
- effective-dated;
- maker/checker approved;
- pair-specific;
- immutable after activation;
- queryable and auditable.

### 5.10 Quote amount is authoritative

A quote stores exact:

```text
source currency
source minor amount
target currency
target minor amount
reference rate
client rate
rate version
rounding mode
rounding remainder
expiry
```

Conversion posts the stored source and target amounts.

It does not recompute the target amount from the latest rate.

### 5.11 No binary floating point

Prohibited for money and FX:

```text
float32
float64
JavaScript Number for persisted money
PostgreSQL REAL
PostgreSQL DOUBLE PRECISION
```

Use:

```text
BIGINT minor units
PostgreSQL NUMERIC for stored rate text/value
Go math/big.Int and math/big.Rat
decimal strings at public boundaries
```

### 5.12 Rate convention

A pair has:

```text
base currency
quote currency
```

For `USD/IDR`:

```text
rate = IDR major units per 1 USD major unit
```

The quote engine derives direction-specific client rates.

The API response always includes:

```text
source_currency
target_currency
source_amount
target_amount
rate
rate_convention
```

A client never needs to infer multiplication versus division.

### 5.13 Rounding

All amounts are positive during quote calculation.

Target amount is rounded:

```text
toward zero to the target currency minor unit
```

For positive values this is floor.

Reasons:

- deterministic;
- no silent over-credit;
- simple anti-arbitrage property;
- explicit remainder.

The quote stores the discarded rational remainder for evidence.

Changing rounding mode requires a new pair-policy version.

### 5.14 Spread

Initial vertical slice uses:

```text
0 basis points
```

After core reconciliation is green, a non-zero synthetic spread may be enabled
through a versioned pair policy.

Spread is not recognized as accounting revenue merely because it exists in a
quote.

C2 may model the difference later, but Ledger revenue requires explicit
single-currency accounting entries.

### 5.15 Position accounts may be negative within limits

The existing FX position accounts allow negative balances.

C4 adds explicit per-leg lower and upper limits.

A conversion is rejected before posting if either resulting position account
would exceed its configured bound.

### 5.16 Hard limits are per currency leg

Hard position limits are stored in each account currency's minor units.

Do not use a volatile converted “single total exposure” as the transactional
hard limit.

A mark-to-market view may be used for dashboards only.

### 5.17 No external settlement claim

FX positions represent a local synthetic platform position.

They do not prove:

- bank inventory;
- nostro balance;
- liquidity;
- settlement finality;
- hedge execution;
- real profit or loss.

### 5.18 Configuration changes do not rewrite history

Rate, pair, currency, route, and limit changes affect new operations only.

Existing:

- accounts;
- intents;
- payouts;
- quotes;
- conversions;
- transactions;
- statements;

retain their original currency and policy evidence.

---

## 6. Exact monetary contract

## 6.1 Public amount representation

Use decimal strings containing integer minor units.

Example:

```json
{
  "amount": "125000",
  "currency": "USD"
}
```

This represents USD 1,250.00.

Do not accept:

```json
{
  "amount": 1250.00
}
```

### 6.2 Internal amount representation

Go domain types must avoid naked `int64` where currencies can be confused.

Recommended:

```go
type CurrencyCode string

type Money struct {
    Currency CurrencyCode
    Minor    int64
}
```

For quote math:

```go
type Rate struct {
    Value *big.Rat
}
```

The concrete package design must avoid mutable shared `big.Int`/`big.Rat`
aliasing.

### 6.3 Checked arithmetic

Every addition, subtraction, multiplication, conversion, fee calculation, and
limit comparison must detect overflow.

Conversion flow:

1. convert source minor units into an exact rational major amount;
2. apply exact rational client rate;
3. convert to target minor units;
4. round using pair policy;
5. reject non-positive target amount;
6. reject if target exceeds `int64`;
7. store exact target integer and remainder.

### 6.4 Currency code normalization

Rules:

- exactly three ASCII uppercase letters;
- trim is not silently accepted at signed financial boundaries;
- unknown code rejected;
- disabled code rejected for new intake;
- historical read remains permitted;
- no locale-specific casing.

### 6.5 Currency registry cache

Ledger owns the database registry.

Within Ledger:

- load an immutable snapshot;
- expose version;
- refresh after approved change;
- fail startup if configured default currency is absent.

Other services do not read the Ledger database.

They use purpose-built Ledger contracts for:

- supported currency metadata;
- operation policy;
- user account/currency state;
- fee resolution.

A bounded cache outside Ledger is optional only after T0 records current call
patterns and safe invalidation.

Initial C4 preference:

```text
no long-lived cross-service currency-policy cache
```

---

## 7. Currency and operation policy

## 7.1 Capability dimensions

For each currency:

```text
account_enable
topup
transfer
payout
fx_source
fx_target
statement
notification_display
```

### 7.2 Policy precedence

Effective decision:

```text
global product enable
-> currency capability
-> operation/currency policy
-> user/KYC tier policy
-> service intake control
-> route/vendor capability
-> request-specific validation
```

All layers must allow the operation.

### 7.3 Currency lifecycle

Currency status:

```text
draft
active
intake_paused
disabled
```

Semantics:

- `draft`: not visible to users;
- `active`: new account and supported operation intake allowed by capabilities;
- `intake_paused`: existing reads and in-flight completion allowed, new
  configured intake blocked;
- `disabled`: no new intake; existing balances/history still readable.

Do not delete a currency referenced by financial rows.

### 7.4 Operation policy

Prefer extending existing policy-limit structures.

Do not create a second conflicting limit engine.

T0 must map current tables/functions for:

```text
transaction type
currency
KYC tier
daily limits
per-transaction limits
fee rules
```

Add only missing capability metadata.

### 7.5 Stable errors

Required errors include:

```text
CURRENCY_INVALID
CURRENCY_DISABLED
CURRENCY_NOT_ENABLED
CURRENCY_OPERATION_DISABLED
CURRENCY_ACCOUNT_MISSING
CURRENCY_ACCOUNT_INACTIVE
CURRENCY_MISMATCH
CROSS_CURRENCY_TRANSFER_REQUIRES_FX
CURRENCY_SYSTEM_ACCOUNT_MISSING
CURRENCY_ROUTE_UNAVAILABLE
CURRENCY_LIMIT_EXCEEDED
MONEY_OVERFLOW
```

---

## 8. User currency-account provisioning

## 8.1 Internal Ledger contract

Add an additive operation such as:

```text
ProvisionUserCurrency
```

Request:

```text
user_id
currency
idempotency_key
actor metadata
```

Response:

```text
currency
account IDs by type
already_existed
```

### 8.2 Account family

Initial required family:

```text
cash
hold
pending
frozen
```

Pocket and savings accounts are not automatically created.

### 8.3 Provisioning behavior

- validate active currency;
- validate `account_enable`;
- use one transaction;
- insert missing accounts idempotently;
- insert balance projections;
- return existing active accounts on retry;
- reject conflicting account shape;
- emit audit and trace evidence;
- no starting balance is minted.

### 8.4 Public Gateway routes

Add:

```text
GET  /api/v1/currencies
GET  /api/v1/balances
GET  /api/v1/balances/{currency}
POST /api/v1/currencies/{currency}/enable
```

Existing balance route remains compatible.

### 8.5 Balance response

Never return one numeric combined total.

Example:

```json
{
  "balances": [
    {
      "currency": "IDR",
      "minor_unit": 0,
      "available": "125000",
      "hold": "0",
      "pending": "0",
      "frozen": "0"
    },
    {
      "currency": "USD",
      "minor_unit": 2,
      "available": "2500",
      "hold": "0",
      "pending": "0",
      "frozen": "0"
    }
  ]
}
```

### 8.6 Concurrency

Two concurrent enable requests must create one account family.

Required evidence:

- unique constraints;
- transaction retry behavior;
- same response on retry;
- no orphan balance row;
- no partial family.

---

## 9. Ledger single-currency hardening

## 9.1 Application invariant

Before posting:

```text
transaction currency
=
every resolved account currency
=
fee account currency
=
settlement account currency
=
hold/pending/frozen account currency
```

### 9.2 Database defense

Add a Ledger database guard for new entry insertion.

Conceptual trigger:

```text
ledger_entries.account_id currency
=
ledger_transactions.currency
```

The implementation must be measured and reviewed for query cost.

Alternative implementation is acceptable only if it provides equal database
enforcement.

### 9.3 Header account guard

Where `source_account_id` or `destination_account_id` is non-null, their
currency must equal the header currency.

These columns remain informative, but mixed-currency values are still
prohibited.

### 9.4 Posting-core API

Refactor the internal posting core to accept a transaction context and resolved
account set that already carries currency.

Do not allow processors to perform currencyless system-account lookup.

### 9.5 Lock ordering

All account locks use one deterministic order across:

- normal posting;
- FX aggregate posting;
- verifier repair where applicable;
- adjustments.

C4 must not introduce pair-dependent deadlocks.

### 9.6 Per-currency verifier

The trial-balance verifier checks:

```text
sum debit = sum credit
```

for every transaction and currency.

System-wide aggregate output is grouped by currency.

No verifier alert sums IDR and USD values.

### 9.7 Snapshot and statement behavior

- snapshots remain account/currency specific;
- statements display transaction currency;
- CSV includes currency and minor-unit semantics;
- as-of reports group by currency;
- no combined total without explicit non-authoritative conversion.

---

## 10. Same-currency transfer activation

## 10.1 Transfer rule

A normal transfer is valid only when:

```text
source cash currency
=
destination cash currency
=
request currency
=
fee quote currency
=
fee account currency
```

### 10.2 Public behavior

Existing transfer endpoint remains.

C4 activates USD after the contract inventory confirms its current shape.

For new multi-currency clients, currency must be explicit.

### 10.3 Destination resolution

Destination account is resolved by:

```text
target user + cash + requested currency
```

If the target user has not enabled USD:

```text
CURRENCY_ACCOUNT_MISSING
```

Do not auto-enable another user's currency during transfer.

### 10.4 Fee behavior

Transfer fee:

- resolved by transaction type, route/gateway, user/tier, and currency;
- quoted in transfer currency;
- collected into fee account of the same currency;
- never converted.

### 10.5 Idempotency

Request fingerprint includes:

```text
source user
target user
amount
currency
fee quote
operation
```

Same idempotency key with different currency is a mismatch.

### 10.6 Required USD transfer journey

```text
user A enables USD
user B enables USD
operator seeds user A USD using approved local adjustment or USD top-up
user A transfers USD to user B
Ledger posts balanced USD entries
notification/event contains USD
both USD balances update
IDR balances remain unchanged
```

---

## 11. Non-IDR top-up activation

## 11.1 No conversion

A USD top-up credits USD.

It never credits IDR and never runs FX.

### 11.2 Payin validation

At intent creation:

- validate active currency;
- validate top-up capability;
- validate user currency account exists;
- validate per-currency amount limits;
- choose a route that declares the same currency;
- persist amount and currency;
- return explicit currency.

### 11.3 Route capability

Payin routing must include or resolve:

```text
vendor
gateway
currency
operation
priority
enabled
```

A route that supports IDR cannot be used for USD unless USD is explicitly
declared.

### 11.4 Mock VendorService capability

Add a mock USD pay-in route.

The mock protocol must:

- echo exact amount and currency;
- reject unsupported currency;
- produce deterministic callback fixtures;
- support duplicate and out-of-order callback tests;
- remain local only.

### 11.5 Callback correlation

Payin continues validating:

```text
intent reference
vendor
amount
currency
status
```

A callback with correct amount but wrong currency is rejected and cannot post
money.

### 11.6 Ledger posting

Successful USD callback posts `money_in` using:

```text
USD settlement account for selected vendor
USD user cash account
USD fee account if C5 later adds a top-up fee
```

### 11.7 Intake policy change

If USD top-up becomes paused after an intent is created:

- new intents are rejected;
- an existing valid confirmed intent may complete according to locked in-flight
  policy;
- behavior is documented and tested;
- no callback is silently converted or redirected.

### 11.8 Required cases

- USD intent success;
- duplicate callback;
- wrong amount;
- wrong currency;
- wrong vendor;
- expired intent;
- route unavailable;
- currency paused before creation;
- currency paused after creation;
- Ledger unavailable;
- callback replay after recovery.

---

## 12. Non-IDR payout activation

## 12.1 No conversion

A USD payout debits/holds USD.

It never consumes IDR.

### 12.2 Payout validation

At request creation:

- validate active currency;
- validate payout capability;
- validate user cash/hold account family;
- validate currency-specific policy;
- validate fee quote currency;
- choose route/vendor supporting the currency;
- validate destination's declared mock corridor if applicable;
- persist exact amount and currency.

### 12.3 Hold lifecycle

All payout Ledger operations use the same currency:

```text
withdraw_initiate
withdraw_pending
withdraw_settle
withdraw_cancel
fee_collect
reversal
```

### 12.4 Mock USD payout

VendorService mock adapter:

- declares USD capability;
- accepts only synthetic destinations;
- echoes amount/currency;
- produces deterministic success, pending, failure, timeout, and duplicate
  callback behavior;
- never represents a real international or bank corridor.

### 12.5 Route failover

Failover candidates must match:

```text
operation
currency
destination/corridor class
```

A USD payout may not fail over to an IDR-only route.

### 12.6 Fee behavior

Payout fee:

- quoted in payout currency;
- collected in payout currency;
- stored with payout reference;
- never repriced;
- never deducted from another currency.

### 12.7 Failure correctness

- failed payout releases USD hold exactly once;
- settled payout closes USD hold exactly once;
- duplicate callback does not create duplicate Ledger state;
- IDR balances remain unchanged;
- status query does not mutate money;
- route outage does not trigger FX.

---

## 13. FX pair and rate governance

## 13.1 Initial pair

Canonical pair:

```text
base_currency = USD
quote_currency = IDR
pair_code = USDIDR
```

Supported user directions:

```text
USD -> IDR
IDR -> USD
```

### 13.2 Pair policy

A pair policy declares:

```text
pair
status
source/target direction enable
minimum source amount per direction
maximum source amount per direction
quote TTL
rounding mode
spread basis points
position-account qualifier
position limits
policy version
```

### 13.3 Rate version

A rate version contains:

```text
pair
reference rate
spread basis points or linked pair policy
effective_from
effective_until
status
content hash
created_by
approved_by
created_at
approved_at
```

### 13.4 Rate lifecycle

```text
draft
pending_approval
active
expired
retired
rejected
```

Only one applicable active rate may exist at a timestamp.

### 13.5 Maker/checker

A maker creates and submits.

A different checker approves.

Ledger enforces the separation.

Admin BFF also enforces UI roles and CSRF.

### 13.6 Rate overlap

Database exclusion/validation prevents overlapping active windows for one pair.

A future rate may be scheduled.

### 13.7 Rate direction calculation

For canonical `USD/IDR` rate `R`:

#### USD to IDR

```text
target IDR major = source USD major × client bid rate
```

#### IDR to USD

```text
target USD major = source IDR major ÷ client ask rate
```

The quote response contains the exact directional rate used.

### 13.8 Spread calculation

When spread is non-zero:

```text
bid = reference_rate × (1 - spread_bps / 10000)
ask = reference_rate × (1 + spread_bps / 10000)
```

All operations are exact rational arithmetic.

### 13.9 Rate safety

Reject:

- zero/negative rate;
- unsupported scale;
- invalid pair;
- identical source and target;
- inactive pair;
- expired rate;
- overlapping active rate;
- rate producing zero target amount;
- arithmetic overflow;
- inconsistent reciprocal fixture.

### 13.10 Rate source transparency

Public response marks:

```text
rate_source = mock
```

Documentation and UI must not imply live market pricing.

---

## 14. FX quote contract

## 14.1 Public endpoints

```text
GET  /api/v1/fx/pairs
POST /api/v1/fx/quotes
GET  /api/v1/fx/quotes/{quote_id}
```

### 14.2 Create quote request

```json
{
  "source_currency": "IDR",
  "target_currency": "USD",
  "source_amount": "160000"
}
```

### 14.3 Quote response

```json
{
  "id": "fxq_...",
  "source": {
    "currency": "IDR",
    "amount": "160000",
    "minor_unit": 0
  },
  "target": {
    "currency": "USD",
    "amount": "1000",
    "minor_unit": 2
  },
  "rate": {
    "value": "16000.000000000000000000",
    "convention": "IDR per USD",
    "source": "mock",
    "spread_basis_points": 0
  },
  "rounding": {
    "mode": "toward_zero"
  },
  "expires_at": "2026-07-28T09:00:30Z",
  "status": "active"
}
```

Values are illustrative fixtures, not real rates.

### 14.4 Quote ownership

A quote belongs to one authenticated user.

Another user receives not-found semantics.

### 14.5 Preconditions

Before quote creation:

- both currencies active;
- pair/direction active;
- source currency enabled for user;
- target currency enabled for user;
- amount within pair policy;
- current approved rate available;
- position capability not globally paused.

Quote creation does not reserve user funds or position capacity.

### 14.6 Quote status

```text
active
consumed
expired
cancelled
```

### 14.7 Quote expiry

Initial default:

```text
30 seconds
```

Configurable per pair policy.

### 14.8 Quote idempotency

Quote creation supports an idempotency key.

Identity:

```text
user + source currency + target currency + source amount + request key
```

Same key/different request returns a stable mismatch.

### 14.9 Stored evidence

Store:

```text
user
pair and direction
source amount/currency/exponent
target amount/currency/exponent
reference rate
client rate
spread
rounding mode
remainder numerator/denominator or canonical representation
rate version
pair-policy version
expires
request fingerprint
```

---

## 15. FX conversion contract

## 15.1 Public endpoints

```text
POST /api/v1/fx/conversions
GET  /api/v1/fx/conversions/{conversion_id}
```

### 15.2 Request

```json
{
  "quote_id": "fxq_...",
  "expected_source_amount": "160000",
  "expected_target_amount": "1000"
}
```

Header:

```text
Idempotency-Key
```

Expected amounts prevent accidental acceptance of a stale UI state.

### 15.3 Response

```json
{
  "id": "fxc_...",
  "status": "posted",
  "quote_id": "fxq_...",
  "source": {
    "currency": "IDR",
    "amount": "160000",
    "transaction_id": "..."
  },
  "target": {
    "currency": "USD",
    "amount": "1000",
    "transaction_id": "..."
  },
  "created_at": "...",
  "posted_at": "..."
}
```

### 15.4 Conversion statuses

```text
pending
posted
failed
```

A validation rejection before insertion may return no conversion row.

A transaction rollback must not leave a misleading `pending` row unless a
separate recovery design explicitly requires it.

### 15.5 Conversion idempotency

Unique identity:

```text
user + conversion operation + idempotency key
```

Also:

```text
one successfully consumed quote -> one conversion
```

Same key/same quote returns existing conversion.

Same key/different quote returns mismatch.

Different key/same consumed quote returns existing/quote-consumed conflict,
never a second conversion.

### 15.6 Quote locking

Conversion locks the quote row.

Checks:

- owner;
- active;
- not expired by database time;
- expected amounts match;
- rate/policy versions still valid for quote consumption rules;
- not consumed.

A policy/rate becoming inactive after quote creation does not invalidate an
already active quote unless an explicit emergency pair pause is configured to
block consumption.

### 15.7 Emergency pair pause

Pair control distinguishes:

```text
new_quotes_paused
conversions_paused
```

This allows operators to stop new quotes while deciding whether valid quotes
may finish.

---

## 16. Atomic FX posting

## 16.1 Transaction flow

Within one Ledger PostgreSQL transaction:

1. lock conversion idempotency record or create deterministic claim;
2. lock quote;
3. validate owner, status, expiry, and expected amounts;
4. resolve user source cash account;
5. resolve user target cash account;
6. resolve source and target FX position accounts;
7. validate all account currencies and statuses;
8. lock all four account-balance rows in deterministic UUID order;
9. validate source user balance;
10. calculate resulting source position balance;
11. calculate resulting target position balance;
12. validate per-leg position limits;
13. insert conversion aggregate;
14. post source-currency ledger transaction;
15. post target-currency ledger transaction;
16. mark quote consumed;
17. mark conversion posted and link both transaction IDs;
18. insert normal transaction outbox events;
19. insert aggregate FX-conversion outbox event;
20. commit.

No network call occurs in this transaction.

### 16.2 Source leg

Conceptual:

```text
user source cash
<-> source-currency FX position account
```

Amount:

```text
quote.source_amount
```

Transaction type:

```text
fx_out
```

### 16.3 Target leg

Conceptual:

```text
target-currency FX position account
<-> user target cash
```

Amount:

```text
quote.target_amount
```

Transaction type:

```text
fx_in
```

### 16.4 Linkage

Both transaction records carry:

```text
conversion_id
quote_id
pair
direction
leg
counterpart_transaction_id
```

Use additive metadata/columns according to current Ledger schema and contract
rules.

### 16.5 Posting-core reuse

Do not duplicate balance-update, entry-insert, fee, outbox, or invariant logic.

Extract/reuse an internal transaction-scoped posting primitive.

The public/general `Post` contract remains single-transaction.

### 16.6 Failure atomicity

Required injection points:

```text
after quote lock
after account lock
after source transaction header
after source entries
after source balance update
before target transaction header
after target entries
before quote consumption
before outbox insert
before commit
```

Every injected failure must roll back:

- both legs;
- balance changes;
- quote consumption;
- conversion;
- outbox events.

### 16.7 Crash after commit

Retry returns the existing conversion through deterministic idempotency.

It never reposts either leg.

---

## 17. FX position handling

## 17.1 Position account model

Reuse existing:

```text
type = fx_conversion
system_qualifier = IDRUSD
currency = IDR or USD
allow_negative = true
```

### 17.2 Position meaning

Each account balance shows the platform's accumulated synthetic position in that
currency under the Ledger's account-sign convention.

T0 must document the exact debit/credit sign interpretation.

### 17.3 Per-leg limits

For each pair and currency:

```text
minimum allowed balance
maximum allowed balance
warning threshold
critical threshold
```

Values use that currency's minor units.

### 17.4 Position pre-check

Under account locks, calculate:

```text
projected source position
projected target position
```

Reject when outside hard range:

```text
FX_POSITION_LIMIT_EXCEEDED
```

No partial leg is posted.

### 17.5 Position report

Admin view returns:

```text
pair
currency
account ID
balance minor units
minor unit exponent
warning/critical state
lower/upper limit
last conversion timestamp
```

It does not combine IDR and USD into one authoritative balance.

### 17.6 Mark-to-market view

An optional operational estimate may show:

```text
base-equivalent synthetic exposure
```

using the latest mock reference rate.

It is:

- non-authoritative;
- timestamped;
- clearly marked estimated;
- not used for posting or hard-limit enforcement.

### 17.7 Position rebalancing

No real external rebalancing exists.

Local synthetic rebalance uses the existing governed adjustment mechanism:

- maker/checker;
- reason;
- one currency at a time;
- explicit position account;
- normal Ledger transaction;
- audit;
- verifier evidence.

Do not directly update `account_balances`.

### 17.8 Pair intake controls

Operators can:

```text
pause new quotes
pause conversion consumption
resume
lower limits
schedule rate
retire pair
```

Existing balances and historical conversions remain visible.

---

## 18. Proposed Ledger schema

T0 determines exact migration numbers after the live migration head.

## 18.1 Extend `currencies`

Prefer additive columns or a separate capability table.

Potential fields:

```text
status
display_name
symbol
account_enable_enabled
topup_enabled
transfer_enabled
payout_enabled
fx_source_enabled
fx_target_enabled
version
updated_at
```

A separate `currency_capabilities` table is preferred if it avoids mixing static
ISO metadata with mutable product policy.

## 18.2 `fx_pairs`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
pair_code TEXT UNIQUE NOT NULL
base_currency CHAR(3) NOT NULL
quote_currency CHAR(3) NOT NULL
status TEXT NOT NULL
position_qualifier TEXT NOT NULL
quote_ttl_seconds INTEGER NOT NULL
rounding_mode TEXT NOT NULL
version BIGINT NOT NULL
created_by TEXT NOT NULL
updated_by TEXT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (base_currency, quote_currency)
CHECK (base_currency <> quote_currency)
```

## 18.3 `fx_pair_directions`

```text
pair_id UUID NOT NULL
source_currency CHAR(3) NOT NULL
target_currency CHAR(3) NOT NULL
enabled BOOLEAN NOT NULL
min_source_amount BIGINT NOT NULL
max_source_amount BIGINT NOT NULL
spread_basis_points BIGINT NOT NULL
new_quotes_paused BOOLEAN NOT NULL
conversions_paused BOOLEAN NOT NULL
version BIGINT NOT NULL
updated_at TIMESTAMPTZ NOT NULL
PRIMARY KEY (pair_id, source_currency, target_currency)
```

### 18.4 `fx_rate_versions`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
pair_id UUID NOT NULL
reference_rate NUMERIC(38,18) NOT NULL
status TEXT NOT NULL
effective_from TIMESTAMPTZ NOT NULL
effective_until TIMESTAMPTZ NULL
content_hash BYTEA NOT NULL
created_by TEXT NOT NULL
submitted_by TEXT NULL
approved_by TEXT NULL
rejected_by TEXT NULL
created_at TIMESTAMPTZ NOT NULL
submitted_at TIMESTAMPTZ NULL
approved_at TIMESTAMPTZ NULL
retired_at TIMESTAMPTZ NULL
rejection_reason TEXT NULL
CHECK (reference_rate > 0)
```

Prevent overlapping approved/active intervals.

### 18.5 `fx_quotes`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
user_id UUID NOT NULL
pair_id UUID NOT NULL
direction TEXT NOT NULL
source_currency CHAR(3) NOT NULL
target_currency CHAR(3) NOT NULL
source_amount BIGINT NOT NULL
target_amount BIGINT NOT NULL
source_minor_unit SMALLINT NOT NULL
target_minor_unit SMALLINT NOT NULL
reference_rate NUMERIC(38,18) NOT NULL
client_rate NUMERIC(38,18) NOT NULL
rate_convention TEXT NOT NULL
spread_basis_points BIGINT NOT NULL
rounding_mode TEXT NOT NULL
rounding_remainder_numerator NUMERIC NOT NULL
rounding_remainder_denominator NUMERIC NOT NULL
rate_version_id UUID NOT NULL
pair_policy_version BIGINT NOT NULL
status TEXT NOT NULL
request_key TEXT NULL
request_digest BYTEA NULL
expires_at TIMESTAMPTZ NOT NULL
consumed_at TIMESTAMPTZ NULL
consumed_by_conversion_id UUID NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
CHECK (source_amount > 0)
CHECK (target_amount > 0)
CHECK (source_currency <> target_currency)
```

### 18.6 `fx_conversions`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
user_id UUID NOT NULL
quote_id UUID NOT NULL UNIQUE
idempotency_key TEXT NOT NULL
idempotency_digest BYTEA NOT NULL
status TEXT NOT NULL
source_transaction_id UUID NULL
target_transaction_id UUID NULL
source_currency CHAR(3) NOT NULL
source_amount BIGINT NOT NULL
target_currency CHAR(3) NOT NULL
target_amount BIGINT NOT NULL
failure_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
posted_at TIMESTAMPTZ NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (user_id, idempotency_key)
```

### 18.7 `fx_position_limits`

```text
pair_id UUID NOT NULL
currency CHAR(3) NOT NULL
minimum_balance BIGINT NOT NULL
maximum_balance BIGINT NOT NULL
warning_lower BIGINT NOT NULL
warning_upper BIGINT NOT NULL
version BIGINT NOT NULL
created_by TEXT NOT NULL
updated_by TEXT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
PRIMARY KEY (pair_id, currency)
CHECK (minimum_balance < maximum_balance)
```

### 18.8 Ledger transaction linkage

Additive options:

```text
conversion_id UUID NULL
fx_quote_id UUID NULL
fx_leg TEXT NULL
counterpart_transaction_id UUID NULL
```

The exact design must respect current transaction metadata conventions.

### 18.9 Database guards

Add:

- quote-consumption uniqueness;
- conversion idempotency;
- active-rate overlap protection;
- pair direction validation;
- source/target currency consistency;
- ledger-entry account-currency guard;
- position-limit query indexes;
- quote expiry and active indexes;
- user quote/conversion keyset indexes.

---

## 19. Internal contracts

## 19.1 Currency metadata

Additive Ledger gRPC operations:

```text
ListCurrencies
GetCurrency
GetCurrencyOperationPolicy
```

### 19.2 User currency accounts

```text
ProvisionUserCurrency
ListUserCurrencyAccounts
GetUserBalanceByCurrency
```

Preserve existing `GetUserCurrency` until consumers migrate.

### 19.3 FX

```text
ListFXPairs
CreateFXQuote
GetFXQuote
ExecuteFXConversion
GetFXConversion
```

### 19.4 Admin

Use internal/admin HTTP or typed existing convention:

```text
list/update currency capability
list/create/submit/approve/reject/retire rate
list/update pair direction
list/update position limits
view positions
pause/resume quote/conversion intake
```

### 19.5 Compatibility

Every Protobuf change must pass:

- lint;
- breaking check;
- semantic field-number policy;
- generated artifact gate;
- tolerant old/new consumer test;
- staged rollout harness.

No existing field changes meaning.

---

## 20. Public API contract

## 20.1 Currency discovery

```text
GET /api/v1/currencies
```

Response includes:

```text
code
minor_unit
status
enabled operations
user_enabled
```

### 20.2 Wallet currencies

```text
POST /api/v1/currencies/{currency}/enable
GET  /api/v1/balances
GET  /api/v1/balances/{currency}
```

### 20.3 Transfer

Existing endpoint remains.

Currency is explicitly documented and validated.

### 20.4 Top-up

Existing endpoint remains.

Currency capability and route are enforced.

### 20.5 Payout

Existing endpoint remains.

Currency capability, route, fee quote, and hold accounts are enforced.

### 20.6 FX

```text
GET  /api/v1/fx/pairs
POST /api/v1/fx/quotes
GET  /api/v1/fx/quotes/{id}
POST /api/v1/fx/conversions
GET  /api/v1/fx/conversions/{id}
```

### 20.7 Error envelope

Use the current public error shape.

Add stable codes:

```text
FX_PAIR_UNAVAILABLE
FX_DIRECTION_DISABLED
FX_RATE_UNAVAILABLE
FX_QUOTE_EXPIRED
FX_QUOTE_ALREADY_CONSUMED
FX_QUOTE_MISMATCH
FX_QUOTE_NOT_FOUND
FX_CONVERSION_IN_PROGRESS
FX_POSITION_LIMIT_EXCEEDED
FX_CONVERSIONS_PAUSED
FX_TARGET_AMOUNT_ZERO
FX_RATE_INVALID
```

### 20.8 Pagination

Quotes and conversions use keyset pagination if list endpoints are later added.

Offset pagination is not required by C4.

---

## 21. Per-service changes

## 21.1 Gateway

Gateway must:

- expose currency and balance endpoints;
- preserve existing IDR behavior;
- validate public amount strings;
- never format minor units using hard-coded exponent;
- map Ledger errors consistently;
- expose explicit FX routes;
- propagate idempotency keys;
- avoid local FX arithmetic;
- avoid local rate caching as an authority;
- include currency in request fingerprints and logs;
- never combine balances.

### 21.2 LedgerService

Ledger must:

- own all currency/pair/rate/quote/conversion data;
- harden single-currency posting;
- provision account families;
- execute both FX legs atomically;
- enforce position limits;
- produce per-currency statements/verifier output;
- emit aggregate FX event;
- expose admin controls;
- preserve IDR behavior.

### 21.3 PayinService

Payin must:

- validate currency operation capability through typed Ledger contract;
- require enabled user currency account;
- route by currency;
- preserve currency in intent/callback/assurance data;
- reject amount/currency mismatch;
- post to currency-matched Ledger accounts;
- maintain in-flight behavior during pause.

### 21.4 PayoutService

Payout must:

- validate currency operation capability;
- resolve fee quote in the same currency;
- route by currency;
- preserve currency across hold, dispatch, callback, settle, release, and
  assurance;
- reject destination/route currency mismatch;
- never run implicit FX.

### 21.5 VendorService

VendorService must:

- declare adapter capabilities by operation and currency;
- support synthetic USD fixtures;
- reject unsupported currency;
- include amount/currency in signed/normalized callback;
- preserve no user ID authority;
- keep real secrets/corridors out of scope.

### 21.6 FraudService

Fraud must:

- receive currency for every money screen;
- key velocity buckets by currency where amounts are compared;
- apply per-currency limits;
- never add IDR and USD amounts;
- expose currency in safe decision evidence;
- maintain bounded metric labels.

If one rule needs a base-equivalent estimate, it is out of scope unless a
separate non-authoritative policy and rate-snapshot contract is created.

### 21.7 AssuranceService

Assurance must:

- compare amount and currency;
- never match by amount alone;
- verify Payin/Payout Ledger links in the same currency;
- add FX conversion assurance;
- verify both legs, quote, user, amounts, pair, and atomic linkage;
- flag missing counterpart leg;
- flag mixed-currency normal transaction;
- support intake controls by currency/pair where appropriate.

### 21.8 Notification module

Current or future notification rendering must:

- include currency;
- format by minor-unit exponent;
- avoid displaying raw minor integer as a major-unit value;
- use aggregate FX event for one conversion notification;
- avoid generating two misleading `fx_out`/`fx_in` user messages when one
  aggregate message is intended.

### 21.9 Admin BFF

Admin BFF must provide:

- currency capability view;
- pair/direction view;
- rate maker/checker workflow;
- current mock rate;
- quote/conversion inspection;
- position and limit view;
- pause/resume controls;
- USD route capability view;
- per-currency fee/policy view;
- audit history;
- no direct service DB access.

---

## 22. Events

## 22.1 Existing transaction event

Ensure every money event includes:

```text
amount
currency
transaction type
transaction ID
source/destination IDs where allowed
event ID
schema version
```

### 22.2 FX aggregate event

Add:

```text
ledger.fx_conversion.posted.v1
```

Payload:

```text
event ID
conversion ID
quote ID
user ID or approved actor reference
source transaction ID
source currency
source amount
target transaction ID
target currency
target amount
pair
rate version ID
client rate string
posted_at
```

Do not include mutable latest rate.

### 22.3 Publication

Both leg events and aggregate event are inserted into Ledger outbox in the same
database transaction.

### 22.4 Consumer policy

- notification uses aggregate event for one user-facing FX notice;
- Fraud may consume aggregate for analytics only, not synchronous approval;
- Assurance consumes aggregate and queries typed Ledger contract;
- C2 may model source/target facts separately;
- consumers deduplicate by logical event ID.

### 22.5 Evolution

All event changes follow A9 expand/contract rules.

---

## 23. Fee and quote interactions

## 23.1 Same-currency operation fee

Fee currency equals operation currency.

### 23.2 Existing fee quote

Existing fee quotes remain for transfer/payout fees.

An FX quote is a separate object.

Do not reuse a transaction-fee quote as an exchange-rate quote.

### 23.3 FX fee

C4 v1 does not add a separate explicit FX fee.

Synthetic spread may be modeled after core activation.

A future explicit fee requires:

- fee rule;
- fee quote;
- same-currency fee account;
- accounting treatment;
- public disclosure;
- tests.

### 23.4 Quote consumption

Transaction fee quote and FX quote have independent consumption semantics.

If a future operation uses both, the combined orchestration needs a separate
plan.

---

## 24. Reporting and reconciliation

## 24.1 Per-currency trial balance

Report:

```text
currency
total debit
total credit
difference
```

No combined monetary difference.

### 24.2 FX conversion reconciliation

For each posted conversion:

- quote exists;
- quote consumed once;
- source transaction posted;
- target transaction posted;
- source transaction currency/amount matches quote;
- target transaction currency/amount matches quote;
- correct FX position accounts used;
- same user owns both user accounts;
- both leg transactions reference each other;
- outbox aggregate event exists;
- position balances agree with entries.

### 24.3 Orphan detection

Critical findings:

```text
source leg without target leg
target leg without source leg
consumed quote without conversion
conversion without consumed quote
conversion with wrong position account
normal transaction containing mixed account currencies
same quote used twice
position over hard limit
```

### 24.4 Daily position report

Group by:

```text
pair
currency
position account
```

Include:

```text
opening balance
conversion inflow
conversion outflow
adjustment/rebalance
closing balance
limit state
```

### 24.5 Reporting views

Existing reporting views must be audited.

Every amount aggregate must either:

- group by currency; or
- reject multi-currency use.

No existing compliance view silently changes semantic meaning.

### 24.6 C2 compatibility

If C2 is implemented later:

- `fact_ledger_entry` remains single-currency;
- FX conversion becomes a linked aggregate fact;
- unit economics does not combine currencies without explicit modeled FX;
- rate snapshot comes from quote, not latest rate.

---

## 25. Security and threat model

Update the threat model.

## 25.1 Money-mixing threats

- USD transaction posts to IDR account;
- wrong-currency fee account;
- wrong-currency settlement account;
- callback amount matches but currency differs;
- payout hold in one currency and settle in another;
- statement combines balances;
- fraud rule compares raw amounts across currencies.

### 25.2 Quote threats

- quote tampering;
- quote used by another user;
- quote reused;
- target amount changed;
- expired quote accepted;
- rate update reprices quote;
- rounding disagreement;
- overflow;
- duplicate idempotency key with different currency.

### 25.3 FX atomicity threats

- source leg commits without target;
- target leg commits without source;
- outbox missing;
- crash after first balance update;
- deadlock;
- counterpart linkage incorrect;
- retry duplicates one leg.

### 25.4 Position threats

- limit race;
- stale position check;
- direct balance edit;
- unlimited negative position;
- pause bypass;
- rebalancing without maker/checker;
- mark-to-market shown as authoritative liquidity.

### 25.5 Rate governance threats

- maker self-approval;
- overlapping rate windows;
- zero/negative rate;
- malicious extreme rate;
- stale active rate;
- rate secret incorrectly treated as market data;
- real-market claim from mock value.

### 25.6 Vendor threats

- adapter accepts unsupported currency;
- callback signature valid but currency altered;
- failover changes currency;
- destination corridor mismatch;
- vendor-native decimal rounded differently.

### 25.7 Required control documentation

For each threat:

```text
prevention
detection
unit/integration/chaos test
alert
runbook
residual risk
owner
```

---

## 26. Observability

## 26.1 Currency metrics

```text
seev_currency_operations_total{service,operation,currency,result}
seev_currency_policy_decisions_total{operation,currency,result,reason}
seev_currency_account_provision_total{currency,result}
seev_currency_mismatch_total{service,boundary,reason}
```

### 26.2 FX metrics

```text
seev_fx_quotes_total{pair,direction,result}
seev_fx_quote_duration_seconds{pair,direction,result}
seev_fx_conversions_total{pair,direction,result}
seev_fx_conversion_duration_seconds{pair,direction,result}
seev_fx_quote_expired_total{pair,direction}
seev_fx_position_limit_decisions_total{pair,currency,result}
seev_fx_position_balance{pair,currency}
seev_fx_position_utilization_ratio{pair,currency,bound}
seev_fx_atomic_rollback_total{injection_point}
seev_fx_assurance_findings_total{finding,severity}
```

### 26.3 Service metrics

Payin/Payout route and callback metrics add bounded currency labels.

Do not use:

```text
user ID
quote ID
conversion ID
transaction ID
rate version ID
idempotency key
```

as metric labels.

### 26.4 Logs

Structured logs include:

```text
operation
currency
pair
direction
stable error code
request/trace ID
service
```

They do not include:

```text
full idempotency key
raw destination
vendor secret
private callback payload
rate approval note containing secrets
```

### 26.5 Tracing

Trace journey:

```text
Gateway request
-> currency/policy validation
-> Payin/Payout/Ledger call
-> Ledger posting
-> outbox
-> callback/event/assurance where applicable
```

FX trace:

```text
quote request
-> quote creation
-> conversion request
-> atomic source+target posting
-> aggregate outbox
-> notification/assurance
```

### 26.6 Alerts

Required:

```text
currency mismatch attempt spike
missing system account
USD route unavailable unexpectedly
FX no active rate
FX quote failure spike
FX position warning threshold
FX position hard-limit rejection spike
FX pair paused
FX assurance orphan leg
mixed-currency Ledger guard violation
per-currency verifier mismatch
mock vendor currency mismatch
```

Every alert links to a runbook.

---

## 27. Runbooks

Create:

```text
docs/runbooks/currency-disabled-with-open-balances.md
docs/runbooks/currency-system-account-missing.md
docs/runbooks/currency-route-unavailable.md
docs/runbooks/currency-mismatch-incident.md
docs/runbooks/fx-rate-unavailable.md
docs/runbooks/fx-rate-incorrect.md
docs/runbooks/fx-pair-pause.md
docs/runbooks/fx-position-warning.md
docs/runbooks/fx-position-limit.md
docs/runbooks/fx-orphan-leg.md
docs/runbooks/fx-quote-consumption-mismatch.md
docs/runbooks/fx-conversion-replay.md
docs/runbooks/fx-position-rebalance.md
docs/runbooks/multi-currency-verifier-failure.md
```

Each runbook includes:

- impact by currency;
- whether new intake must pause;
- whether in-flight operations may complete;
- confirmation queries through owned interfaces;
- source-of-truth statement;
- safe action;
- no direct balance edit warning;
- reconciliation;
- recovery;
- replay/idempotency behavior;
- evidence to record.

---

## 28. Admin BFF operations

## 28.1 Currency page

Show:

```text
code
minor unit
status
enabled capabilities
user account count where safely exposed
system-account readiness
fee/policy readiness
route readiness
```

### 28.2 Pair page

Show:

```text
pair
directions
status
quote TTL
rounding
spread
active rate
next rate
position accounts
position limits
current positions
pause state
```

### 28.3 Rate workflow

Actions:

```text
create draft
preview directional examples
submit
approve/reject
schedule
retire
```

Maker/checker required.

### 28.4 Position controls

Actions:

```text
update warning/hard limits
pause quotes
pause conversions
resume
initiate governed synthetic rebalance
```

### 28.5 Journey configuration

Show per currency:

```text
Payin routes
Payout routes
vendor capability
fee rules
tier/amount limits
intake state
```

### 28.6 Audit

Required events:

```text
currency.capability.changed
currency.status.changed
currency.user_enabled
fx.pair.created
fx.pair.changed
fx.rate.created
fx.rate.submitted
fx.rate.approved
fx.rate.rejected
fx.rate.retired
fx.position_limit.changed
fx.quotes.paused
fx.conversions.paused
fx.pair.resumed
fx.position.rebalanced
```

No audit entry includes secret credentials or raw destinations.

---

## 29. Task breakdown

# T0 — Entry gate and live currency inventory

### Work

- Record exact commit and migration heads.
- Run current verification.
- Inspect archived plan 18 assumptions against the split-service runtime.
- Inventory `pkg/currency`.
- Inventory account provisioning.
- Inventory every amount/currency DTO.
- Inventory every Protobuf field.
- Inventory OpenAPI fixtures.
- Inventory all event schemas.
- Inventory currencyless system-account lookups.
- Inventory IDR constants and formatters.
- Inventory policy, fee, quote, route, and vendor capability keys.
- Inventory verifier/report/snapshot/statement grouping.
- Inventory FX processors and atomicity.
- Inventory current tests for USD.
- Build the account and route readiness matrix.

### Acceptance

- [ ] Every money boundary has a currency decision.
- [ ] Every hard-coded IDR occurrence is classified.
- [ ] Existing primitives are separated from missing activation work.
- [ ] Current FX partial-failure behavior is known.
- [ ] No source-service assumption is based only on archive prose.
- [ ] Exact migration and contract gaps are listed.
- [ ] Existing IDR journeys remain green.

---

# T1 — Lock contracts, invariants, rate math, and threat model

### Work

- Lock IDR/USD scope.
- Lock same-currency journey rules.
- Lock no implicit FX rule.
- Lock money and rate representations.
- Lock pair convention.
- Lock rounding.
- Lock spread baseline.
- Lock quote and conversion semantics.
- Lock atomic posting design.
- Lock position meaning and limits.
- Lock currency lifecycle.
- Lock in-flight behavior during pause.
- Lock errors.
- Update threat model.
- Add sequence/state/failure diagrams.
- Add contract fixtures before handlers.

### Required diagrams

```text
enable USD wallet
USD transfer
USD top-up
USD payout
create FX quote
execute FX conversion
atomic two-leg posting
position-limit rejection
rate maker/checker
currency pause with in-flight top-up
pair pause with active quote
conversion retry after lost response
```

### Acceptance

- [ ] No arithmetic ambiguity remains.
- [ ] No implicit conversion path exists.
- [ ] Both FX legs are one DB transaction.
- [ ] Rate/quote history is immutable.
- [ ] Position hard limit is transactional.
- [ ] Existing IDR compatibility is explicit.
- [ ] Threat controls have owners and tests.

---

# T2 — Exact-money and currency package hardening

### Work

- Review and extend `pkg/currency`.
- Add immutable `Money` and exact `Rate` helpers where appropriate.
- Add checked arithmetic.
- Add decimal-string parser.
- Add exact rate parser.
- Add exponent scaling.
- Add target rounding.
- Add remainder evidence.
- Add display formatting.
- Add overflow boundaries.
- Add fuzz/property tests.
- Ban float use in money packages through lint/static checks where practical.

### Acceptance

- [ ] IDR exponent 0 works.
- [ ] USD exponent 2 works.
- [ ] No silent rounding.
- [ ] Toward-zero conversion is deterministic.
- [ ] Overflow fails.
- [ ] Zero target fails.
- [ ] Round-trip property does not mint either currency.
- [ ] Parser rejects exponent mismatch.
- [ ] No money path uses float.
- [ ] Existing package API remains compatible or migrates safely.

---

# T3 — Currency capability and user account activation

### Work

- Add currency lifecycle/capability schema.
- Add Ledger metadata contracts.
- Add user currency provisioning.
- Add account-family idempotency.
- Add Gateway currencies/balances routes.
- Preserve existing balance route.
- Add Admin BFF currency page.
- Add per-currency account readiness.
- Add migration/backfill.
- Add concurrency tests.

### Acceptance

- [ ] IDR existing account remains unchanged.
- [ ] USD family can be enabled exactly once.
- [ ] Enabling USD creates no balance.
- [ ] Disabled currency blocks new enable.
- [ ] Balance list never combines currencies.
- [ ] Cross-user access fails.
- [ ] Concurrent enable is safe.
- [ ] Admin capability change is audited.
- [ ] Existing Auth provisioning remains green.

---

# T4 — Ledger anti-mixing enforcement

### Work

- Add currency-carrying resolved-account types.
- Eliminate currencyless system-account lookup.
- Add posting validation.
- Add database guard.
- Add header-account validation.
- Review every processor.
- Review fee collection.
- Review scheduled/accrual/adjustment paths.
- Update verifier and statements.
- Add mixed-currency corruption fixtures.
- Measure trigger/query cost.

### Acceptance

- [ ] Every processor passes correct currency.
- [ ] Wrong-currency system account fails before mutation.
- [ ] Database rejects mixed entry insertion.
- [ ] Header account mismatch fails.
- [ ] Trial balance is grouped by currency.
- [ ] Statement/CSV includes currency.
- [ ] Existing IDR posting throughput stays within documented bound.
- [ ] Mixed-currency chaos creates no ledger row.

---

# T5 — USD same-currency transfer

### Work

- Add/verify USD transfer policy.
- Add USD fee rules or explicit zero-fee rule.
- Activate Gateway contract.
- Resolve target USD account.
- Add request equality including currency.
- Add event fixtures.
- Add notification formatting.
- Add Fraud per-currency rule behavior.
- Add E2E and duplicate tests.

### Acceptance

- [ ] USD-to-USD transfer posts once.
- [ ] IDR-to-USD normal transfer is rejected.
- [ ] Missing target USD account is explicit.
- [ ] USD fee uses USD fee account.
- [ ] IDR balances do not change.
- [ ] Event amount/currency is correct.
- [ ] Duplicate request returns existing transaction.
- [ ] Same key/different currency mismatches.
- [ ] Fraud does not combine currencies.

---

# T6 — USD top-up

### Work

- Add currency-aware Payin capability validation.
- Add/verify currency-aware route rule.
- Add mock USD VendorService capability.
- Add USD settlement account mapping.
- Add strict callback currency validation.
- Add pause/in-flight behavior.
- Add Assurance currency matching.
- Add admin route visibility.
- Add E2E and callback chaos.

### Acceptance

- [ ] USD intent and callback settle once.
- [ ] Wrong-currency callback never posts.
- [ ] Duplicate callback is safe.
- [ ] USD route cannot fall back to IDR route.
- [ ] User USD account is required.
- [ ] USD settlement account is used.
- [ ] Intake pause behavior is proven.
- [ ] IDR top-up remains green.
- [ ] No real vendor claim exists.

---

# T7 — USD payout

### Work

- Add currency-aware Payout validation.
- Add USD payout route.
- Add mock vendor USD corridor.
- Add same-currency fee quote.
- Review hold/settle/cancel processors.
- Add route failover constraints.
- Add currency-aware Assurance.
- Add admin route/health visibility.
- Add E2E, duplicate, timeout, and callback chaos.

### Acceptance

- [ ] USD hold, settle, and cancel are correct.
- [ ] Failed payout releases USD once.
- [ ] IDR remains untouched.
- [ ] Wrong-currency fee quote is rejected.
- [ ] USD route cannot fail over to IDR-only adapter.
- [ ] Duplicate callback is safe.
- [ ] Lost response is recoverable.
- [ ] IDR payout remains green.
- [ ] No implicit FX exists.

---

# T8 — FX pair, rate, and quote governance

### Work

- Add pair/direction schema.
- Add rate-version schema.
- Add maker/checker.
- Add overlap protection.
- Add position-limit schema.
- Seed synthetic USD/IDR pair.
- Add quote schema.
- Implement exact quote engine.
- Add Gateway quote routes.
- Add Admin rate/pair pages.
- Add expiry and idempotency.
- Add contract and snapshot fixtures.

### Acceptance

- [ ] Rate is exact and positive.
- [ ] Overlapping active rates are blocked.
- [ ] Maker cannot self-approve.
- [ ] Direction math is correct.
- [ ] Quote target amount is deterministic.
- [ ] Quote stores all policy/rate evidence.
- [ ] Quote belongs to one user.
- [ ] Expired quote is read-only and not consumable.
- [ ] Rate update does not mutate quote.
- [ ] API marks rate source as mock.

---

# T9 — Atomic FX conversion

### Work

- Add conversion schema.
- Add internal transaction-scoped posting primitive.
- Add conversion gRPC operation.
- Lock quote and account rows.
- Enforce deterministic account lock order.
- Simulate position result.
- Enforce limits.
- Post both legs.
- Link transactions.
- Consume quote.
- Add aggregate outbox event.
- Add Gateway conversion route.
- Add idempotency and lost-response recovery.
- Add failure injection at every atomic boundary.

### Acceptance

- [ ] Valid IDR-to-USD conversion posts both legs.
- [ ] Valid USD-to-IDR conversion posts both legs.
- [ ] Each leg balances in its own currency.
- [ ] Failure at any injection point posts neither leg.
- [ ] Retry after commit returns existing conversion.
- [ ] Quote can be consumed once.
- [ ] Expected amount mismatch rejects.
- [ ] User source insufficiency rejects.
- [ ] Position limit rejects before posting.
- [ ] Outbox events are atomic.

---

# T10 — FX position operations and reconciliation

### Work

- Add position queries.
- Add Admin position dashboard.
- Add warning/critical metrics.
- Add pair pause controls.
- Add governed synthetic rebalance.
- Add daily position report.
- Add conversion reconciliation.
- Add orphan detection.
- Add Assurance FX record.
- Add runbooks.
- Add mark-to-market estimate clearly non-authoritative.

### Acceptance

- [ ] Position is shown per leg/currency.
- [ ] Hard limit is transactionally enforced.
- [ ] Concurrent conversions cannot bypass limit.
- [ ] Pause stops intended operation.
- [ ] Rebalance uses normal Ledger adjustment.
- [ ] No direct balance update exists.
- [ ] Every conversion reconciles.
- [ ] Missing counterpart leg is critical.
- [ ] Estimate is not used for posting.
- [ ] Alerts and runbooks work.

---

# T11 — Cross-cutting policy, reporting, notifications, and privacy

### Work

- Update Fraud currency keys.
- Update Assurance matching.
- Update notifications/formatting.
- Update snapshots/statements/CSV.
- Review regulatory/reporting views.
- Update Admin policy pages.
- Update privacy export.
- Update backup/restore verification.
- Update C2 contract notes.
- Update C3 template context notes.
- Add per-currency retention where required.

### Acceptance

- [ ] No amount aggregate mixes currencies.
- [ ] Fraud limits are per currency.
- [ ] Assurance matches amount and currency.
- [ ] Notification displays correct USD decimals.
- [ ] CSV is unambiguous.
- [ ] Privacy export includes all currencies.
- [ ] Restore verification checks USD and FX.
- [ ] Existing reporting semantics are preserved.
- [ ] No sensitive vendor destination is added to events/logs.

---

# T12 — Observability, controls, and runbooks

### Work

- Add currency and FX metrics.
- Add dashboards.
- Add position panels.
- Add route/capability panels.
- Add alerts.
- Add runbooks.
- Add intake controls.
- Add rate-age indication.
- Add quote/conversion latency.
- Validate metric cardinality.
- Add operational evidence links.

### Acceptance

- [ ] USD journey health is visible separately.
- [ ] Pair/direction health is visible.
- [ ] Rate absence is visible.
- [ ] Position warning/limit is visible.
- [ ] Currency mismatch is visible.
- [ ] Mixed-currency guard violation alerts.
- [ ] Every alert has a runbook.
- [ ] No user/quote/transaction IDs in metrics.
- [ ] Product health remains distinct from FX position health.

---

# T13 — E2E, chaos, load, and final evidence

### Work

- Add `scripts/multi-currency-e2e.sh`.
- Add `scripts/fx-chaos.sh`.
- Add USD transfer load scenario.
- Add USD callback burst.
- Add FX conversion concurrency scenario.
- Test service restarts.
- Test RabbitMQ outage.
- Test Ledger DB restart.
- Test rate retirement.
- Test currency/pair pause.
- Test position-limit race.
- Test every atomic injection point.
- Test mixed-currency corruption attempts.
- Run clean-tree repository gate.
- Record residual risks.
- Update roadmap and service documentation.
- Archive only after all required evidence.

### Acceptance

- [ ] IDR regression suite passes.
- [ ] USD enable, transfer, top-up, and payout pass.
- [ ] Both FX directions pass.
- [ ] Normal cross-currency transfer fails.
- [ ] Wrong-currency callback fails.
- [ ] Wrong-currency route fails.
- [ ] Mixed Ledger entry fails.
- [ ] No partial FX conversion is observed.
- [ ] Concurrent position limits are safe.
- [ ] Duplicate HTTP/event/callback does not duplicate money.
- [ ] Restart/recovery is proven.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are explicit.

---

## 30. Recommended pull-request sequence

```text
PR 1  — C4 entry evidence, architecture, contracts, threat model
PR 2  — Exact money/rate utilities and hard-coded IDR inventory fixes
PR 3  — Currency capabilities and user USD account provisioning
PR 4  — Ledger anti-mixing guards and per-currency verifier/reporting
PR 5  — USD same-currency transfer
PR 6  — Payin/VendorService USD top-up
PR 7  — Payout/VendorService USD payout
PR 8  — FX pair/rate governance and Admin BFF maker/checker
PR 9  — FX quotes and public quote API
PR 10 — Atomic FX conversion and aggregate event
PR 11 — Position limits, Assurance, dashboard, and rebalance controls
PR 12 — Cross-cutting Fraud/notification/privacy/reporting updates
PR 13 — Observability, chaos, load, runbooks, final evidence
```

Split further where an owner-service migration is large.

Do not combine all Ledger, Payin, Payout, Vendor, Gateway, and Admin changes in
one PR.

---

## 31. Dependency graph

```text
T0 Entry gate
  |
  v
T1 Contracts/invariants/threat model
  |
  v
T2 Exact money and rate math
  |
  |---------------------------|
  v                           v
T3 Currency/account enable   T4 Ledger anti-mixing
  |                           |
  |-------------|-------------|
                v
         T5 USD transfer
                |
       |--------|--------|
       v                 v
T6 USD top-up       T7 USD payout
       |                 |
       |--------|--------|
                v
 T8 Pair/rate/quote governance
                |
                v
      T9 Atomic FX conversion
                |
                v
 T10 Position/reconciliation
                |
                v
 T11 Cross-cutting integration
                |
                v
 T12 Observability/runbooks
                |
                v
 T13 Final evidence
```

T6 and T7 may run in parallel after T3/T4/T5 foundations are stable.

---

## 32. First implementation cut

The first mergeable vertical slice should not include FX.

```text
existing IDR user
        +
enable USD account family
        +
operator-approved synthetic USD adjustment
        +
USD same-currency transfer
        +
per-currency balance API
        +
event/notification formatting
        +
verifier grouped by currency
```

This proves:

- multi-wallet model;
- account provisioning;
- exact USD minor units;
- same-currency resolution;
- fee account selection;
- idempotency by currency;
- anti-mixing;
- statements/events;
- IDR regression safety.

Only after this slice passes should USD top-up/payout activate.

---

## 33. Second implementation cut

```text
USD top-up intent
-> USD-capable mock route
-> normalized USD callback
-> strict amount/currency correlation
-> USD settlement posting
-> duplicate/recovery evidence
```

---

## 34. Third implementation cut

```text
USD payout request
-> USD hold
-> USD-capable mock route
-> vendor callback
-> USD settle or release
-> duplicate/recovery evidence
```

---

## 35. Fourth implementation cut

```text
approved mock rate
-> exact FX quote
-> explicit user acceptance
-> one atomic Ledger transaction
   containing source and target legs
-> position-limit enforcement
-> aggregate event
-> reconciliation
```

---

## 36. Test strategy

## 36.1 Unit tests

Cover:

```text
currency parsing
minor-unit exponent
money formatting
checked arithmetic
rate parsing
bid/ask derivation
division/multiplication direction
rounding toward zero
rounding remainder
position projection
pair policy
rate lifecycle
quote lifecycle
conversion state
idempotency digest
currency capability precedence
same-currency validator
route capability
fee currency
```

### 36.2 Property and fuzz tests

Properties:

- conversion output never negative;
- accepted positive quote never overflows;
- floor result is at most exact rational result;
- round-trip with non-negative spread cannot increase both holdings;
- same-currency posting never accepts mixed account currency;
- request parser never silently rounds;
- arbitrary decimal rate input never invokes float;
- extreme exponents/rates fail safely.

Fuzz:

```text
currency code
amount string
rate string
quote request
conversion request
event decoder
vendor callback
policy payload
```

### 36.3 PostgreSQL integration tests

Prove:

```text
account-family concurrency
entry currency trigger
active-rate overlap
quote idempotency
quote single consumption
conversion idempotency
atomic two-leg rollback
position-limit race
deterministic lock order
outbox atomicity
rate maker/checker
pair pause
migration/backfill
```

### 36.4 Service contract tests

For each service:

```text
valid IDR
valid USD
invalid currency
disabled currency
wrong operation capability
missing account
wrong route
wrong fee quote
wrong callback currency
compatible new response field
old consumer tolerance
```

### 36.5 End-to-end journeys

#### Journey A — Enable USD

```text
login
-> list currencies
-> enable USD
-> list balances
-> USD zero balance visible
-> duplicate enable returns same family
```

#### Journey B — USD transfer

```text
fund user A USD synthetically
-> transfer USD to user B
-> USD entries balance
-> event says USD
-> IDR unchanged
```

#### Journey C — USD top-up

```text
create USD intent
-> mock vendor confirms USD
-> Payin correlates
-> Ledger credits USD once
```

#### Journey D — USD payout success

```text
create USD payout
-> USD hold
-> mock vendor success
-> USD settle
```

#### Journey E — USD payout failure

```text
USD hold
-> vendor failure
-> USD release once
```

#### Journey F — IDR to USD FX

```text
create quote
-> accept
-> source IDR leg
-> target USD leg
-> quote consumed
-> position updated
```

#### Journey G — USD to IDR FX

Reverse direction with exact exponent handling.

### 36.6 Cross-currency negative matrix

```text
IDR source to USD target through normal transfer
USD fee quote on IDR payout
IDR settlement account on USD top-up
USD callback for IDR intent
IDR callback for USD intent
USD payout via IDR route
mixed-currency Ledger entries
same idempotency key with changed currency
FX quote used by another user
FX quote source amount changed
FX quote target amount changed
expired quote
pair paused
currency disabled
target account missing
position hard limit exceeded
```

---

## 37. Chaos matrix

## 37.1 FX atomic injection

Inject failure after each step listed in Section 16.

Expected:

```text
no source leg
no target leg
no quote consumption
no conversion row marked posted
no outbox event
balances unchanged
```

### 37.2 Lost response after FX commit

Expected:

- client retries same idempotency key;
- existing conversion returned;
- no third Ledger transaction;
- quote remains consumed once.

### 37.3 Concurrent quote consumption

Two conversion requests for one quote.

Expected:

- one posts;
- one returns existing/conflict;
- position updated once.

### 37.4 Concurrent position-limit edge

Two different conversions individually fit but together exceed limit.

Expected:

- account locks serialize;
- one may post;
- second rejects;
- limit never exceeded.

### 37.5 Currency disabled during top-up

Test before and after intent creation.

### 37.6 Currency disabled during payout

Test before request and during in-flight vendor state.

### 37.7 Rate retired after quote

Existing quote behavior follows locked consumption policy.

New quote fails until another rate is active.

### 37.8 Pair conversion pause after quote

Test `new_quotes_paused` and `conversions_paused` separately.

### 37.9 Wrong-currency vendor callback

Expected:

- callback evidence retained;
- owner rejects;
- no Ledger posting;
- assurance/alert evidence.

### 37.10 RabbitMQ outage

Expected:

- money commit and outbox remain;
- notifications/assurance catch up;
- currency remains in event;
- no reposting.

### 37.11 Ledger restart

During:

```text
USD top-up posting
USD payout posting
FX conversion
```

Expected behavior follows transaction commit boundary and idempotent recovery.

### 37.12 Mixed-entry direct SQL attempt

Through test role/fixture.

Expected database guard rejects.

### 37.13 Missing USD system account

Expected:

- operation fails before partial state;
- alert;
- runbook;
- no fallback to IDR.

### 37.14 Mock rate extreme value

Expected:

- policy/rate validation blocks unreasonable configured boundary;
- overflow protection;
- no quote.

---

## 38. Performance and resource boundaries

C4 does not make production FX capacity claims.

Engineering boundaries:

```text
no network call inside Ledger transaction
bounded quote queries
indexed active-rate lookup
bounded position locks
deterministic four-account lock order
no global account lock
no float conversions
no per-user metric label
no combined-currency scan for balance API
keyset pagination for history
bounded Admin position query
no synchronous external rate provider
```

Initial local targets to measure:

```text
USD transfer p95 regression vs IDR:       <= 10%
FX quote p95:                             <= 100ms local
FX conversion p95:                        <= 250ms local
position check added lock wait:           recorded and bounded
IDR path p95 regression:                  <= 5%
verifier runtime increase:                recorded
```

Targets are local and must be adjusted from B0 evidence.

---

## 39. Load scenarios

Add:

```text
USD P2P posting
mixed IDR/USD same-currency transfers
USD top-up callback burst
USD payout batch
FX quote burst
FX conversion concurrency
FX hot position-account contention
```

Measure:

```text
throughput
p95/p99 latency
Ledger lock waits
FX position account contention
DB pool saturation
outbox lag
callback lag
quote expiry rate
position-limit rejection
```

Important:

The FX position accounts are intentionally hot system accounts.

C4 measurements may inform B1, but must not bypass B0 activation rules.

---

## 40. Rollout stages

### Stage 0 — Foundations disabled

- schema and code present;
- USD public writes disabled;
- FX routes disabled;
- IDR unchanged.

### Stage 1 — USD wallet read/provision

- enable USD;
- zero balance;
- statements;
- no USD public money intake except governed adjustment.

### Stage 2 — USD transfer

- small local test cohort/config;
- no Payin/Payout USD yet.

### Stage 3 — USD top-up

- mock route only;
- callback chaos evidence.

### Stage 4 — USD payout

- mock route only;
- hold/settle/release evidence.

### Stage 5 — FX quote read-only

- rates/pairs maker-checker;
- quote creation;
- conversion disabled.

### Stage 6 — FX conversion

- low pair limits;
- zero spread;
- small configured amount limits;
- full atomicity and position evidence.

### Stage 7 — Synthetic spread and broader local limits

Only after reconciliation and anti-arbitrage tests pass.

---

## 41. Kill switches

Required:

```text
currency account enable
currency top-up intake
currency transfer intake
currency payout intake
FX new quotes
FX quote consumption
Payin currency route
Payout currency route
mock vendor currency capability
```

A kill switch:

- is owner-side;
- is audited;
- exposes current state;
- distinguishes new intake from in-flight completion;
- does not delete balances/history.

---

## 42. Rollback

### 42.1 Immediate rollback

1. pause new FX quotes;
2. pause quote consumption if correctness risk exists;
3. pause affected non-IDR intake;
4. allow safe reads;
5. decide in-flight completion by documented policy;
6. preserve Ledger and quote/conversion evidence;
7. roll back code without dropping additive schema.

### 42.2 USD journey rollback

Disabling USD:

- blocks new operations;
- does not convert USD to IDR;
- does not delete USD accounts;
- does not hide statements;
- requires an explicit user exit/conversion policy outside C4.

### 42.3 Rate rollback

Never edit an active historical rate row.

Publish a corrected new version and pause quotes if needed.

Existing quotes follow the incident policy.

### 42.4 FX conversion rollback

A posted conversion is not deleted.

Correction uses explicit compensating conversions/adjustments with maker-checker
and incident evidence.

### 42.5 Schema rollback

Do not drop tables/columns containing financial evidence in an operational
rollback.

Schema contraction requires a later migration plan.

---

## 43. Documentation deliverables

Add or update:

```text
docs/roadmap/active/60-c4-end-to-end-multi-currency.md
docs/roadmap/README.md
docs/roadmap/42-long-term-roadmap.md

docs/reference/currencies.md
docs/reference/money-contract.md
docs/reference/fx-rates.md
docs/reference/fx-quotes.md
docs/reference/fx-conversions.md
docs/reference/fx-positions.md
docs/reference/payin.md
docs/reference/payout.md
docs/reference/ledger.md
docs/reference/events.md
docs/reference/current-services.md

docs/architecture/multi-currency.md
docs/security/threat-model.md
docs/evidence/c4-entry-gate.md
docs/evidence/c4-usd-activation.md
docs/evidence/c4-fx-atomicity.md
docs/evidence/c4-final-acceptance.md

docs/runbooks/currency-*.md
docs/runbooks/fx-*.md
```

---

## 44. Proposed repository changes

Expected areas:

```text
pkg/currency/

internal/ledger/
internal/payin/
internal/payout/
internal/vendorboundary/
internal/vendorgw/
internal/fraud/
internal/assurance/
internal/notify/
internal/adminbff/
internal/handler/

migrations/ledger/
migrations/payin/
migrations/payout/
migrations/vendor/
migrations/adminbff/ if only audit/UI state requires it

api/openapi/
api/contracts/
api/events/
api/proto/seev/
gen/

scripts/multi-currency-e2e.sh
scripts/fx-chaos.sh
tests/load/
deploy/observability/
Makefile
docs/
```

T0 narrows the actual blast radius.

---

## 45. Make targets

Recommended:

```text
make currency-contract-check
make currency-idr-scan
make currency-system-account-check
make currency-e2e
make fx-rate-fixtures
make fx-contract-check
make fx-e2e
make fx-chaos
make fx-position-reconcile
make multi-currency-verify
```

Policy:

- static exact-money/currency checks join `make verify-full`;
- repeatable USD/FX local E2E may join the full gate when resource cost is
  acceptable;
- destructive failure injection remains in `make verify-chaos`;
- no internet or paid provider is required.

---

## 46. Final verification commands

T0 replaces examples with canonical repository commands.

Expected:

```bash
make contracts
make proto
make build-all
make test
make vet
make lint
make ci-lint
make docs-check

go test -tags=integration -race ./...

make currency-contract-check
make currency-system-account-check
make currency-e2e
make fx-contract-check
make fx-e2e
make fx-position-reconcile

./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/admin-e2e.sh
./scripts/multi-currency-e2e.sh

make verify-full
git diff --check
git status --short
```

Separate chaos:

```bash
make fx-chaos
make verify-chaos
```

---

## 47. Final definition of done

C4 is complete only when all required items below pass.

### Scope and architecture

- [ ] IDR and USD are the only activated currencies.
- [ ] No FX service exists.
- [ ] No implicit conversion path exists.
- [ ] Ledger owns quotes, conversions, and positions.
- [ ] All normal transactions are single-currency.
- [ ] Both FX legs commit atomically.
- [ ] No real bank/rate-provider claim exists.

### Exact money

- [ ] All amounts are integer minor units.
- [ ] IDR and USD exponent behavior is correct.
- [ ] No float is used in financial math.
- [ ] Conversion arithmetic is exact.
- [ ] Rounding is deterministic and documented.
- [ ] Overflow and zero-target cases fail safely.
- [ ] Public APIs use decimal strings for minor units.

### Currency accounts

- [ ] USD account family can be provisioned idempotently.
- [ ] Provisioning mints no money.
- [ ] Balance API lists currencies separately.
- [ ] Existing IDR balance route remains compatible.
- [ ] Disabled currency blocks new account enable.
- [ ] Concurrent provisioning is safe.

### Ledger invariants

- [ ] Every entry account matches transaction currency.
- [ ] Wrong-currency system account is impossible.
- [ ] Database defense exists.
- [ ] Verifier groups by currency.
- [ ] Statements/CSV are currency-explicit.
- [ ] Existing IDR posting remains green.

### USD transfer

- [ ] USD same-currency transfer succeeds.
- [ ] Cross-currency normal transfer fails.
- [ ] Target USD account is required.
- [ ] USD fee uses USD account.
- [ ] Idempotency includes currency.
- [ ] IDR balances remain unchanged.

### USD top-up

- [ ] USD route is explicit.
- [ ] Mock vendor supports USD.
- [ ] Callback amount and currency are strictly correlated.
- [ ] Duplicate callback is safe.
- [ ] USD settlement account is used.
- [ ] No IDR fallback exists.
- [ ] IDR top-up remains green.

### USD payout

- [ ] USD hold/settle/release is correct.
- [ ] Fee quote currency matches.
- [ ] Route/corridor supports USD.
- [ ] Duplicate/lost response recovery is safe.
- [ ] No implicit FX exists.
- [ ] IDR payout remains green.

### FX governance

- [ ] Pair and direction policy exists.
- [ ] Rate versions are immutable.
- [ ] Maker/checker is enforced.
- [ ] Active windows do not overlap.
- [ ] Mock source is visible.
- [ ] Quote stores exact amounts/rate/policy evidence.
- [ ] Quote expiry and ownership are enforced.
- [ ] Rate update does not change quote.

### FX conversion

- [ ] IDR-to-USD succeeds.
- [ ] USD-to-IDR succeeds.
- [ ] Both legs balance independently.
- [ ] Both legs link to one conversion.
- [ ] Failure injection leaves no partial leg.
- [ ] Retry after commit returns existing conversion.
- [ ] Quote consumes once.
- [ ] Expected amounts are enforced.
- [ ] Outbox aggregate event is atomic.

### Position handling

- [ ] Per-leg limits exist.
- [ ] Position check is under account locks.
- [ ] Concurrent limit race is safe.
- [ ] Pair pause works.
- [ ] Synthetic rebalance uses governed Ledger adjustment.
- [ ] No direct balance update exists.
- [ ] Position report is per currency.
- [ ] Mark-to-market is clearly non-authoritative.

### Cross-service correctness

- [ ] Payin, Payout, Vendor, Fraud, Assurance, Gateway, and Admin carry currency.
- [ ] Fraud does not sum unlike currencies.
- [ ] Assurance matches amount and currency.
- [ ] Notifications format USD correctly.
- [ ] Reporting never combines currencies implicitly.
- [ ] Privacy/backup verification includes USD and FX.
- [ ] Events are versioned and currency-explicit.

### Security and operations

- [ ] Threat model is updated.
- [ ] Metrics and alerts exist.
- [ ] Cardinality is bounded.
- [ ] Every alert has a runbook.
- [ ] Kill switches are exercised.
- [ ] Rate/pair/currency changes are audited.
- [ ] No vendor secret or raw destination leaks.
- [ ] Recovery and correction use explicit transactions.

### Evidence

- [ ] IDR regression suite passes.
- [ ] USD E2E passes.
- [ ] FX E2E passes.
- [ ] Anti-mixing tests pass.
- [ ] Chaos matrix passes.
- [ ] Position reconciliation passes.
- [ ] Load baseline is recorded.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are explicit.
- [ ] Roadmap and current-service docs reflect reality.
- [ ] Plan is archived only after evidence links are complete.

---

## 48. Final evidence log

Fill during execution.

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| C4 entry gate |  |  |  |
| Hard-coded IDR inventory |  |  |  |
| Exact-money property tests |  |  |  |
| USD account provisioning |  |  |  |
| Ledger currency DB guard |  |  |  |
| Per-currency verifier |  |  |  |
| USD transfer E2E |  |  |  |
| USD top-up E2E |  |  |  |
| Wrong-currency callback |  |  |  |
| USD payout success |  |  |  |
| USD payout failure/release |  |  |  |
| Rate maker/checker |  |  |  |
| Quote exact-math fixtures |  |  |  |
| Quote expiry/idempotency |  |  |  |
| IDR-to-USD conversion |  |  |  |
| USD-to-IDR conversion |  |  |  |
| FX atomic injection matrix |  |  |  |
| Lost-response recovery |  |  |  |
| Position-limit race |  |  |  |
| Pair/currency pause |  |  |  |
| FX Assurance reconciliation |  |  |  |
| RabbitMQ recovery |  |  |  |
| Ledger restart recovery |  |  |  |
| Multi-currency load baseline |  |  |  |
| Final clean-tree gate |  |  |  |

---

## 49. Residual risks

A completed local C4 still does not prove:

- real foreign-currency account custody;
- real correspondent banking;
- real settlement;
- live market rate accuracy;
- market-data failover;
- slippage;
- liquidity;
- hedging;
- treasury operations;
- net-open-position regulatory compliance;
- capital requirements;
- sanctions/corridor legality;
- international payout licensing;
- tax treatment;
- realized/unrealized FX P&L accounting;
- rate-provider contractual rights;
- production decimal interoperability with every vendor;
- multi-region consistency;
- more than one currency pair;
- production hot-position-account capacity;
- customer disclosures or legal suitability.

These limits must remain visible in documentation and portfolio claims.

---

## 50. Recommended immediate next action

Start with T0 and T1.

Then implement the smallest safe activation slice:

```text
exact USD formatting/math
-> USD account-family provisioning
-> per-currency balance list
-> Ledger anti-mixing guard
-> USD same-currency transfer
-> per-currency verifier and event evidence
```

Do not begin public FX conversion first.

After the USD transfer slice is green:

```text
USD top-up
-> USD payout
-> rate governance
-> FX quote
-> atomic conversion
-> position limits and reconciliation
```

This order proves that every ordinary journey is genuinely currency-aware before
introducing cross-currency orchestration.
