# Plan 61 — C5 Advanced Financial Products and Period Close

**Created:** 2026-07-28
**Status:** Implementation present; runtime acceptance evidence pending
**Roadmap track:** C5 — Advanced financial products
**Activation trigger:** Accrual and fee-quote foundations are complete, and
period-close learning is intentionally activated
**Primary money owner:** LedgerService
**Journey owners:** LedgerService, Gateway, PayinService, VendorService
**Operator surface:** Admin BFF
**Supporting owners:** AssuranceService, FraudService, notification module
**Initial product scope:** Monthly savings-interest capitalization, durable
scheduled-transaction failure policy, and top-up fees
**No new application service is authorized by this plan.**

The savings, period-close, durable-schedule, and top-up-fee implementation is
present in Ledger. Keep this plan active until runtime and acceptance evidence
is recorded; see the [current-state inventory](../../reference/current-state.md).

---

## 1. Purpose

C5 evolves three existing Seev foundations into controlled financial-product
capabilities:

1. Replace direct daily balance capitalization with daily interest accrual,
   accrued-interest liability, and monthly capitalization.
2. Replace the current lightweight scheduled-transaction runner with a durable
   occurrence-level execution and failure-policy model.
3. Activate top-up fees through the existing fee-rule and fee-quote foundation,
   while preserving exact money and Payin/Ledger ownership.

C5 must add:

- a versioned savings-product and interest-rate model;
- exact daily accrual records from immutable closing snapshots;
- fractional carry so small daily interest is not silently lost;
- daily accrued-interest liability posting;
- monthly period-close readiness checks;
- idempotent per-account monthly capitalization;
- immutable closed-period evidence;
- explicit correction rather than period reopening;
- durable scheduled occurrences and execution attempts;
- missed-run, infrastructure-retry, and business-failure policies;
- current-policy and current-fee evaluation at scheduled execution time;
- an explicit decision on the historical scheduled-policy bypass;
- user-visible schedule execution history;
- top-up fee quoting and quote consumption;
- provider collection of top-up principal plus fee;
- one balanced Ledger posting for principal and fee;
- fee-revenue recognition only after successful top-up settlement;
- maker/checker controls;
- reconciliation, observability, runbooks, chaos, and acceptance evidence.

The implementation must preserve the following rules:

1. LedgerService remains the source of truth for money.
2. Ledger entries remain append-only.
3. Every financial posting remains balanced.
4. Exact money remains integer minor units.
5. Daily interest uses an immutable closing-balance snapshot.
6. Monthly capitalization never recalculates historical daily accruals.
7. A closed financial period is never mutated or reopened.
8. Corrections use explicit linked adjustment transactions.
9. One account and one period may be capitalized at most once.
10. One scheduled occurrence may post at most once.
11. Infrastructure retry and business failure are distinct.
12. A missed schedule is handled by an explicit stored policy.
13. A recurring schedule does not silently execute an unbounded backlog.
14. Scheduled execution revalidates the stored command and current
    Ledger-owned policies.
15. A changed fee rule does not silently charge more than a user's stored fee
    consent.
16. Top-up principal and fee are quoted before provider collection when the
    route is fee-bearing.
17. A failed or expired top-up recognizes no fee revenue.
18. A successful top-up posts principal and fee atomically in Ledger.
19. PayinService owns top-up lifecycle.
20. LedgerService owns fee quote and fee posting correctness.
21. VendorService receives only the exact provider collection amount.
22. No service reads another service's database.
23. Scheduler infrastructure triggers work but does not own product truth.
24. Financial-product failures never cause direct balance updates.
25. Existing IDR, fee-free top-up, daily schedule, and interest history remain
    readable and compatible.
26. No real banking, deposit, tax, or regulated savings-product claim is made.
27. No new business service is created.
28. C5 does not silently enable product behavior before all related controls
    are active.

---

## 2. Current-state foundation

C5 is an extension of existing work.

At plan creation, the repository already contains:

### 2.1 Ledger and savings foundations

- account-balance snapshots;
- a `savings_config` table keyed by account;
- an annual rate in basis points;
- a daily accrual worker;
- daily interest computed from a closing snapshot;
- a fixed `365`-day denominator;
- daily floor/truncation to integer minor units;
- deterministic key:

```text
accrue:<account_id>:<date>
```

- an `interest_accrue` posting processor;
- per-currency interest-expense system accounts;
- one-account failure isolation.

The current implementation directly posts positive daily interest into Ledger.

A snapshot lookup or posting error is logged and counted as skipped; there is
no durable daily accrual-control row proving why an account was skipped.

### 2.2 Schedule foundations

- `scheduled_transactions`;
- schedule kinds:
  - `once`;
  - `daily`;
  - `monthly`;
- supported stored commands:
  - `transfer_p2p`;
  - `transfer_pocket`;
- stored `cmd_payload`;
- pause, resume, and cancel;
- daily runner;
- deterministic key:

```text
sched:<schedule_id>:<run_date>
```

- normal Ledger posting-core reuse;
- `ErrAlreadyPosted` recovery;
- a deliberate no-day-by-day-catch-up MVP policy;
- business errors recorded on the schedule row;
- infrastructure errors leaving the schedule row unchanged;
- no durable occurrence or attempt table;
- no explicit user-selected missed-run policy;
- no bounded retry state machine;
- no full execution history.

### 2.3 Scheduler infrastructure foundations

The repository already has a shared scheduler package with:

- cron parsing;
- static job names;
- per-job timeouts;
- panic isolation;
- metrics;
- in-process lock;
- Redis lock;
- calendar extensions such as last-day expressions;
- skip-on-lock-failure behavior;
- singleton protection.

C5 reuses this package.

It does not create a second cron engine.

### 2.4 Fee foundations

The repository already contains:

- `fee_rules`;
- transaction type;
- gateway;
- currency;
- optional user-specific rule;
- flat fee;
- basis-point fee;
- minimum and maximum clamps;
- specificity resolution;
- fee quotes;
- quote expiry;
- quote consumption;
- `consumed_by_type`;
- `consumed_by_reference`;
- currency-specific fee accounts;
- payout quote integration;
- `money_in` fee quote support currently returning a zero fee because top-up
  fees were intentionally deferred.

C5 activates non-zero top-up fees by extending these foundations rather than
creating a parallel fee engine.

### 2.5 Architectural baseline

- LedgerService owns scheduled transactions, snapshots, savings configuration,
  fee rules, fee quotes, posting, and outbox.
- PayinService owns top-up intents and callback lifecycle.
- VendorService owns provider protocol and callback normalization.
- Gateway owns public HTTP.
- Admin BFF owns operator browser actions.
- AssuranceService owns cross-service consistency records.
- RabbitMQ remains at-least-once.
- Every service owns its own database.
- No application service directly queries another service's tables.

---

## 3. Activation and entry gate

## 3.1 Activation decision

C5 is activated on 2026-07-28 as a conscious learning decision for:

- monthly financial periods;
- accrual versus capitalization;
- liability accounting;
- exact fractional interest carry;
- close readiness and immutable close;
- correction after close;
- durable occurrence scheduling;
- missed-run policy;
- business-versus-infrastructure failure;
- fee consent at deferred execution;
- top-up fee collection and recognition.

The roadmap trigger is therefore satisfied only if T0 verifies that the current
accrual and fee-quote foundations remain complete on the implementation branch.

## 3.2 Required entry-gate evidence

T0 must record a fresh result for every item below.

- [ ] `make contracts` passes from a clean tree.
- [ ] OpenAPI, event, and Protobuf generation checks pass.
- [ ] Ledger migrations and integration tests pass.
- [ ] Payin and VendorService integration tests pass.
- [ ] Existing IDR business E2E passes.
- [ ] Existing fee-quote and payout-fee E2E passes.
- [ ] Existing schedule create/list/pause/resume/cancel tests pass.
- [ ] Existing schedule runner crash-window test passes.
- [ ] Existing daily interest test passes.
- [ ] Existing snapshot worker and lookup behavior are recorded.
- [ ] Existing scheduler package lock and timeout behavior are recorded.
- [ ] Current migration heads are recorded.
- [ ] Current `savings_config` rows and account types are inventoried.
- [ ] Current interest-expense account mapping is recorded by currency.
- [ ] Current `interest_accrue` processor entries are recorded.
- [ ] Current scheduled command schema and runtime validation are recorded.
- [ ] Current public schedule contract is recorded.
- [ ] Current schedule-run policy bypass is traced from Gateway to posting.
- [ ] Current fee-rule precedence and quote-consumption behavior are recorded.
- [ ] Current `money_in` quote response is recorded.
- [ ] Current Payin intent amount semantics are recorded.
- [ ] Current callback amount/currency checks are recorded.
- [ ] Current Payin-to-Ledger posting contract is recorded.
- [ ] Existing top-up reversal/chargeback behavior is recorded.
- [ ] Existing event and notification semantics for top-up are recorded.
- [ ] Existing retention rules for schedules and financial records are recorded.
- [ ] Exact baseline commit is recorded.
- [ ] No unrelated large Ledger or Payin migration is in flight.

## 3.3 Entry-gate deliverables

```text
docs/evidence/c5-entry-gate.md
docs/reference/c5-current-accrual-inventory.md
docs/reference/c5-current-schedule-inventory.md
docs/reference/c5-current-fee-quote-inventory.md
docs/reference/c5-current-topup-inventory.md
docs/reference/c5-policy-bypass-review.md
```

## 3.4 Gate policy

The following may begin before every gate item is green:

- documentation;
- exact-interest math;
- schema drafts;
- synthetic fixtures;
- OpenAPI and Protobuf design;
- template/UI wireframes;
- threat modeling;
- dry-run close tooling.

The following may not merge before the gate is green:

- disabling legacy daily-interest posting;
- monthly capitalization;
- a new schedule execution policy;
- a schedule backfill;
- a non-zero top-up fee;
- mandatory fee-quote enforcement;
- a new accrued-interest system account;
- a public financial-product contract.

---

## 4. Locked scope

C5 contains three product tracks.

```text
C5-A Monthly interest capitalization
C5-B Durable scheduled-transaction failure policy
C5-C Top-up fees
```

They share:

- exact money;
- idempotency;
- maker/checker;
- period and execution evidence;
- event governance;
- observability;
- retention;
- operational controls.

They do not share one generic financial-product state machine.

Each owner keeps its own domain tables.

---

## 5. Explicit non-goals

C5 does not include:

- a new ProductService;
- a new SchedulerService;
- a new FeeService;
- compound interest more frequent than monthly;
- term deposits;
- early-withdrawal penalties;
- promotional interest;
- tiered balance bands;
- average-daily-balance interest;
- negative interest;
- loan interest;
- overdraft interest;
- tax withholding;
- zakat calculation;
- deposit insurance;
- real bank deposit claims;
- configurable day-count conventions beyond the locked baseline;
- public self-service savings enrollment unless separately activated;
- retroactive rewriting of historical daily interest;
- reopening a closed period;
- global Ledger freeze during period close;
- a general accounting general ledger;
- arbitrary operator-created posting formulas;
- arbitrary cron expressions from public users;
- second-level user schedules;
- unbounded schedule catch-up;
- a user-defined retry algorithm;
- cross-service distributed transactions;
- provider collection fees separate from the platform top-up fee;
- partial top-up fee refunds;
- fee financing from another currency;
- implicit currency conversion;
- marketing promotions;
- production legal disclosures;
- real provider pricing;
- production tax/accounting certification;
- a generic workflow engine;
- a generic job-control framework for every service;
- synchronous dependency of Ledger posting on Analytics or Notifications.

---

## 6. Locked architecture decisions

## 6.1 No new application service

LedgerService continues owning:

- savings product/rate configuration;
- daily interest accrual;
- accrued-interest liability;
- financial periods;
- monthly capitalization;
- scheduled transaction definitions;
- scheduled occurrences;
- Ledger-native scheduled-execution policy;
- fee rules;
- fee quotes;
- quote consumption;
- principal-and-fee posting.

PayinService continues owning:

- top-up intent;
- provider collection lifecycle;
- callback;
- settlement request;
- recovery.

Gateway continues owning:

- public authentication;
- request contract;
- Fraud pre-screen where currently applicable;
- user-facing orchestration.

VendorService continues owning:

- mock vendor amount;
- callback signature/protocol;
- callback normalization.

### 6.2 Three product modules, one Ledger boundary

Suggested Ledger internal layout:

```text
services/ledger/internal/ledger/
├── interest/
│   ├── product/
│   ├── rate/
│   ├── accrual/
│   ├── period/
│   ├── capitalization/
│   └── reconciliation/
├── schedule/
│   ├── definition/
│   ├── planner/
│   ├── execution/
│   ├── policy/
│   └── recovery/
└── fee/
    ├── rule/
    ├── quote/
    └── consumption/
```

The exact package layout must preserve existing public package boundaries.

### 6.3 Scheduler package is only a trigger

The shared scheduler package:

- invokes planners/runners;
- applies timeout;
- applies singleton lock;
- emits runtime metrics.

Product tables answer:

- what is due;
- whether it already ran;
- what failed;
- whether retry is allowed;
- whether a period is closed.

A missed cron tick must be recoverable from durable product state.

### 6.4 Daily interest is accrual, monthly interest is capitalization

C5 changes the economic model for newly activated periods.

Daily work:

```text
closing balance snapshot
-> exact daily accrual calculation
-> durable daily accrual row
-> accrued-interest liability posting
```

Monthly close:

```text
sum accrued liability for account/period
-> transfer liability to user savings account
-> mark capitalization item complete
```

The user's savings principal increases monthly, not daily.

### 6.5 Expense recognition and capitalization are separate

Daily accrual posting:

```text
interest expense
<-> accrued interest payable
```

Monthly capitalization posting:

```text
accrued interest payable
<-> user savings account
```

The close transaction does not recognize the expense again.

### 6.6 Historical daily postings remain historical

Legacy `interest_accrue` transactions are not reversed, rewritten, or
reclassified.

C5 starts the new model at an explicit period boundary.

### 6.7 Financial period is immutable after close

A period may be:

```text
planned
open
closing
closed
failed
cancelled_before_open
```

A `closed` period cannot become `open`.

Correction uses:

```text
interest_accrual_adjustment
interest_capitalization_adjustment
```

linked to:

- original period;
- original account;
- original accrual/capitalization;
- incident/reason;
- maker/checker approval.

### 6.8 Schedule definition and schedule occurrence are separate

A schedule is the recurring instruction.

An occurrence is one expected execution date.

A delivery attempt is one attempt to execute that occurrence.

This replaces a single schedule row carrying only last-run and last-error
information.

### 6.9 Deferred execution revalidates current state

At execution time, C5 revalidates:

- command schema;
- account ownership;
- account status;
- target status where Ledger can resolve it;
- currency;
- amount;
- current Ledger-native transaction policy;
- current daily/monthly limits;
- current fee rule;
- user's stored maximum-fee consent;
- schedule status;
- occurrence cutoff.

Creation-time validation alone is not sufficient.

### 6.10 Scheduled-policy bypass decision

C5 explicitly changes the old decision for policies Ledger owns.

The schedule runner may no longer call the posting core with a trusted stored
payload without first running the scheduled-execution evaluator.

The baseline C5 decision is:

- Ledger-owned balance, account, currency, policy-limit, and fee checks are
  mandatory at every occurrence.
- Gateway/Fraud may screen schedule creation using the existing public flow.
- C5 does not add a synchronous Ledger-to-Fraud dependency for every future
  occurrence.
- Scheduled transfers remain limited to current internal supported types,
  bounded amount limits, and operator kill switches.
- Post-transaction Fraud events continue where already implemented.
- A future requirement for real-time Fraud pre-screen on every occurrence
  needs a purpose-built orchestration contract and a separate roadmap decision.

This retained residual risk must be documented, measured, and visible.

### 6.11 Fee consent is explicit

A deferred schedule stores:

```text
fee_mode = resolve_at_execution
max_fee_amount
```

At execution:

- current fee is resolved;
- fee currency must match;
- current fee must not exceed `max_fee_amount`;
- if it exceeds the cap, the occurrence is blocked and the schedule pauses.

The system never silently charges a newly introduced fee above the user's
stored consent.

### 6.12 Top-up fee is added on top

C5 locks top-up amount semantics:

```text
requested amount = amount credited to the wallet
fee amount       = platform top-up fee
total debit      = requested amount + fee amount
```

The provider collects `total_debit`.

Ledger credits the user with the requested amount.

This preserves the existing user-facing meaning of top-up `amount`.

### 6.13 Top-up fee is recognized only on successful settlement

No fee Ledger entry is created when a top-up is:

- created;
- quoted;
- sent to provider;
- pending;
- expired;
- failed;
- cancelled before settlement.

Fee revenue is posted atomically with successful wallet credit.

### 6.14 No network call inside a financial database transaction

This applies to:

- interest close;
- scheduled posting;
- fee quote consumption;
- top-up settlement.

---

# Part A — Monthly Interest Capitalization

## 7. Savings product model

## 7.1 Product scope

The initial product is a synthetic monthly-capitalized savings product.

It supports:

```text
IDR
USD only if C4 is active and system accounts are complete
```

If C4 is not active, only IDR is enabled.

### 7.2 Product identity

A savings product declares:

```text
product code
currency
eligible account types
status
day-count convention
capitalization frequency
reporting timezone
minimum eligible closing balance
interest-expense account
accrued-interest-payable account
default rate policy
version
```

### 7.3 Product lifecycle

```text
draft
active
intake_paused
retired
```

Rules:

- draft cannot enroll;
- active can enroll and accrue;
- intake_paused blocks new enrollment but existing enrollment follows policy;
- retired blocks new enrollment and rate activation;
- historical accruals remain visible.

### 7.4 Day-count convention

C5 locks:

```text
ACT/365F
```

Meaning:

- one daily accrual for each actual calendar day;
- denominator remains `365`, including leap years;
- reporting timezone is `Asia/Jakarta`.

This preserves the current fixed-365 foundation.

### 7.5 Capitalization frequency

```text
monthly
```

Period boundaries use calendar months in `Asia/Jakarta`.

### 7.6 Eligible balance

Initial basis:

```text
positive closing balance snapshot of the enrolled account
```

No interest on:

```text
zero balance
negative balance
hold account
pending account
frozen account unless explicitly eligible
unapproved account type
```

T0 locks the exact eligible account type based on current schema.

### 7.7 Enrollment

Initial enrollment remains operator-managed through Admin BFF.

Public users receive read-only product/accrual/capitalization views.

Self-service enrollment is deferred.

### 7.8 Enrollment lifecycle

```text
pending
active
accrual_paused
ended
```

Rules:

- active accrues;
- accrual_paused stops future daily accrual;
- already accrued liability remains payable;
- ended stops future accrual;
- pending does not accrue;
- an account may have one active enrollment per product.

---

## 8. Savings rate governance

## 8.1 Immutable rate versions

The mutable `annual_rate_bps` foundation is replaced for new periods by
effective-dated rate versions.

Rate fields:

```text
product
annual_rate_bps
effective_from
effective_until
status
content hash
created by
approved by
created at
approved at
```

### 8.2 Rate lifecycle

```text
draft
pending_approval
active
retired
rejected
```

### 8.3 Maker/checker

A maker creates and submits.

A different checker approves.

Ledger enforces separation.

Admin BFF also enforces CSRF and role policy.

### 8.4 Rate overlap

One product may not have overlapping active rate windows.

### 8.5 Daily rate selection

For accrual date `D`, select the rate whose effective window covers `D`.

The daily accrual stores:

```text
rate version ID
rate bps
day-count denominator
```

A later rate change does not alter `D`.

### 8.6 Missing rate

If an active enrollment has no applicable rate:

- create a failed daily accrual item;
- emit critical product-readiness metric;
- block period close;
- do not assume zero;
- do not use latest retired rate.

### 8.7 Rate correction

Do not mutate a published rate.

Create:

- a new future rate; or
- an approved accrual adjustment for an affected closed/open period.

---

## 9. Exact daily interest math

## 9.1 Formula

For account `A` on day `D`:

```text
daily_exact_numerator
=
eligible_closing_balance_minor
× annual_rate_bps
```

```text
daily_denominator
=
10,000 × 365
```

### 9.2 Fractional carry

C5 does not floor each day independently.

For one enrollment:

```text
available_numerator
=
prior_carry_numerator
+ daily_exact_numerator
```

```text
recognized_minor
=
floor(available_numerator / daily_denominator)
```

```text
new_carry_numerator
=
available_numerator mod daily_denominator
```

This prevents small daily fractions from being discarded every day.

### 9.3 Carry scope

Carry belongs to:

```text
enrollment + currency + day-count convention
```

It moves across monthly periods.

### 9.4 Carry and rate changes

A rate change does not reset carry.

The carry represents an unpaid fractional currency amount, not a rate.

### 9.5 Carry at enrollment end

When enrollment ends:

- future daily accrual stops;
- whole recognized minor units are capitalized;
- the remaining fraction is handled by locked product policy.

Initial policy:

```text
discard remaining sub-minor carry on final closure
```

The discarded rational remainder is recorded as evidence.

A future policy may transfer it to a rounding account.

### 9.6 Integer safety

All intermediate arithmetic must avoid overflow.

Use exact integer/rational operations.

No binary floating point.

### 9.7 Zero-result day

A zero recognized amount still creates a successful accrual row containing:

- snapshot;
- rate;
- exact numerator;
- prior carry;
- new carry;
- recognized amount `0`.

This is not a skipped unknown state.

### 9.8 Negative balance

Negative or zero eligible balance produces:

```text
recognized amount = 0
carry unchanged
```

### 9.9 Snapshot immutability

Daily accrual references one closing snapshot ID and date.

It never uses the current live balance.

---

## 10. Daily accrual liability posting

## 10.1 Daily workflow

For accrual date `D`:

1. find active enrollments;
2. claim/create one daily accrual row;
3. resolve snapshot for `D`;
4. resolve applicable rate;
5. lock enrollment carry;
6. calculate exact result;
7. persist calculation;
8. if recognized amount is positive, post accrued-interest liability;
9. link Ledger transaction;
10. mark accrual completed;
11. commit;
12. continue to next account.

### 10.2 Daily posting

Conceptual entries:

```text
debit  interest expense
credit accrued interest payable
```

Currency:

```text
product currency
```

Transaction type:

```text
interest_liability_accrue
```

### 10.3 Idempotency key

```text
interest-liability:<enrollment_id>:<accrual_date>
```

### 10.4 Accrued-interest payable account

Add a system account per supported currency:

```text
type = accrued_interest_payable
```

It is not a user account.

### 10.5 Per-account isolation

One account failure does not block other accounts.

Unlike the current skipped count, each account/day has durable status and
stable error code.

### 10.6 Daily accrual statuses

```text
pending
processing
completed_zero
completed_posted
retry_wait
blocked
failed
adjusted
```

### 10.7 Retry classification

Retryable:

```text
database timeout
snapshot temporarily unavailable
lock timeout
posting infrastructure failure
```

Blocking/business:

```text
missing product account
missing rate
currency mismatch
missing system account
corrupt enrollment
snapshot permanently absent after cutoff
```

### 10.8 Retry cutoff

A daily accrual remains retryable until its period close cutoff.

After that, the period is blocked until operator resolution.

### 10.9 No silent skip

The job-level result distinguishes:

```text
completed
completed_zero
retry_wait
blocked
failed
```

---

## 11. Financial period model

## 11.1 Period identity

Unique:

```text
product + currency + calendar month
```

Example:

```text
SAVINGS_STANDARD_IDR / IDR / 2026-07
```

### 11.2 Period timezone

```text
Asia/Jakarta
```

### 11.3 Period boundaries

```text
period_start_at
period_end_at
accrual_cutoff_at
close_not_before_at
```

### 11.4 Period lifecycle

```text
planned
open
closing
closed
failed
cancelled_before_open
```

### 11.5 Open creation

A planner creates future periods before they open.

A missing future period triggers readiness alert.

### 11.6 No global transaction freeze

Period close does not stop normal wallet transactions.

The close uses immutable daily snapshots through the period end.

Transactions in the new month do not change the previous period's basis.

### 11.7 Close readiness

A period is ready only when:

- product and system accounts exist;
- every expected accrual date is accounted for;
- every eligible enrollment/day is terminal success/zero/approved adjustment;
- no retryable accrual remains;
- no blocked accrual remains;
- all liability postings reconcile;
- no duplicate accrual exists;
- snapshot completeness passes;
- carry chain is continuous;
- rate coverage is complete;
- previous period is closed;
- no product close hold is active.

### 11.8 Expected account-day inventory

The close calculates expected items from enrollment effective dates.

It must not infer completeness only from rows that happen to exist.

### 11.9 Close preview

Before close, produce:

```text
eligible enrollment count
daily accrual count
zero accrual count
posted liability amount by currency
blocked item count
missing item count
expected capitalization amount
carry opening and closing totals
reconciliation status
```

### 11.10 Close trigger

Use a durable close scanner.

Recommended runtime trigger:

```text
daily at 01:15 Asia/Jakarta
```

It scans every due open/failed period.

It is not dependent on firing exactly once on the first day.

---

## 12. Monthly capitalization

## 12.1 Capitalization amount

For account/enrollment/period:

```text
sum recognized daily accrual minor amounts
+ approved accrual adjustments for the period
- prior capitalization adjustment already applied
```

It must equal the linked accrued-interest liability movement.

### 12.2 Capitalization posting

Conceptual entries:

```text
debit  accrued interest payable
credit user savings account
```

Transaction type:

```text
interest_capitalize
```

### 12.3 Idempotency key

```text
interest-capitalize:<enrollment_id>:<period_id>
```

### 12.4 One item per account

The close creates one capitalization item for every eligible enrollment.

Status:

```text
pending
processing
posted
completed_zero
retry_wait
blocked
failed
adjusted
```

### 12.5 Per-account transaction

Do not post one giant transaction for every user.

Each account receives one Ledger transaction.

Benefits:

- bounded locks;
- isolated retry;
- clear statement;
- deterministic idempotency;
- no giant rollback.

### 12.6 Close completion

The period becomes `closed` only when:

- every capitalization item is terminal;
- total liability debited equals total daily liability credited;
- total user capitalization equals liability release;
- no blocked item remains;
- close checks pass;
- close summary is persisted.

### 12.7 Zero capitalization

A zero item is recorded but does not create a Ledger transaction.

### 12.8 Account status at close

Initial policy:

- active account: capitalize normally;
- account in a reversible pause: capitalize normally if posting is allowed;
- closed account: block close and require governed resolution;
- missing account: critical block.

A product/account close operation must therefore check outstanding accrued
interest before final account closure.

### 12.9 Failure recovery

A failed item is retried independently.

A previously posted item is detected through Ledger idempotency and marked
complete.

### 12.10 Period event

After close:

```text
ledger.interest_period.closed.v1
```

Per-account:

```text
ledger.interest_capitalized.v1
```

Events are inserted through Ledger outbox.

---

## 13. Period correction

## 13.1 No reopening

A closed period is immutable.

### 13.2 Correction workflow

A correction requires:

- source period;
- account/enrollment;
- calculation evidence;
- correction amount and direction;
- reason;
- maker;
- checker;
- explicit adjustment transaction;
- linkage to original accrual/capitalization;
- current open correction period.

### 13.3 Positive correction

Conceptually:

```text
interest expense
-> accrued payable or user savings according to correction stage
```

### 13.4 Negative correction

A negative correction may not silently debit a user below product/legal policy.

Initial local policy:

- negative correction requires checker;
- use an adjustment receivable/system account if immediate user debit is unsafe;
- no automatic negative-balance creation unless existing account rules allow
  it;
- operator runbook and residual risk are explicit.

### 13.5 Correction events

```text
ledger.interest_adjusted.v1
```

---

## 14. Migration from legacy daily capitalization

## 14.1 No mid-period cutover

Cutover occurs at the beginning of a calendar month.

### 14.2 Legacy mode

Existing accounts initially remain:

```text
legacy_daily_capitalization
```

### 14.3 New mode

C5 mode:

```text
monthly_liability_capitalization
```

### 14.4 Shadow period

Before cutover:

- compute new daily accrual rows in shadow mode;
- do not post liability;
- compare cumulative new calculation with legacy daily result;
- explain difference caused by fractional carry and monthly compounding;
- record acceptance evidence.

### 14.5 Cutover steps

1. choose cutover month;
2. ensure all legacy daily postings through prior month end exist;
3. close legacy evidence;
4. create product/enrollment/rate versions;
5. seed opening carry `0`;
6. switch mode effective at new period start;
7. disable legacy posting for migrated enrollment;
8. run first daily accrual;
9. verify no duplicate legacy/new interest;
10. complete first monthly close.

### 14.6 Historical statements

Legacy transactions retain type and amount.

New statements distinguish:

```text
Daily interest accrual liability
Monthly interest capitalization
Interest correction
```

User-facing statements should normally show capitalization, not internal
liability entries, unless the account statement policy includes internal
system movements.

### 14.7 Rollback before first close

Before any new liability posting:

- disable new mode;
- return to legacy at a future clean boundary.

### 14.8 Rollback after liability posting

Do not return to legacy daily posting mid-period.

Pause accrual and resolve through correction/close runbook.

---

# Part B — Durable Scheduled-Transaction Failure Policy

## 15. Schedule-definition model

## 15.1 Existing compatibility

Existing public routes and schedule definitions remain readable.

C5 expands the contract additively.

### 15.2 Definition fields

A schedule stores:

```text
user
command type
typed command version
amount
currency
target reference
pocket reference where applicable
schedule kind
timezone
local execution time
start date
end date
day of month
status
missed-run policy
catch-up limit
retry policy
business-failure policy
consecutive-failure threshold
fee mode
maximum fee amount
created request digest
version
```

### 15.3 Typed command

Stop trusting arbitrary `cmd_payload` as the sole runtime contract.

Use:

```text
command_type
command_version
structured typed columns
canonical command JSON
command digest
```

The JSON remains for compatibility/evidence, not as an unchecked executable
blob.

### 15.4 Supported command types

C5 keeps:

```text
transfer_p2p
transfer_pocket
```

No payout, top-up, FX, arbitrary Ledger adjustment, or vendor call is added.

### 15.5 Schedule kinds

Keep:

```text
once
daily
monthly
```

### 15.6 User timezone

Store an IANA timezone.

Initial default:

```text
Asia/Jakarta
```

### 15.7 Local execution time

C5 may add a user-selected local execution time with bounded precision.

Initial minimum precision:

```text
minute
```

If current public contract is date-only, rollout may preserve `00:30` default
until the additive time field is supported.

### 15.8 Monthly date policy

Keep day of month:

```text
1 through 28
```

Last-day schedules are not exposed publicly in C5.

The scheduler infrastructure's `L` support remains an internal capability.

---

## 16. Missed-run policy

## 16.1 Policies

```text
skip
run_once_latest
catch_up_bounded
```

### 16.2 `skip`

For every missed occurrence:

- create an occurrence;
- mark `skipped_missed`;
- post no money;
- continue future schedule.

### 16.3 `run_once_latest`

When one or more occurrences were missed:

- mark older missed occurrences `skipped_superseded`;
- create/execute only the latest eligible occurrence;
- use that occurrence's deterministic date/key.

### 16.4 `catch_up_bounded`

- create up to `catch_up_limit` missed occurrences;
- execute in chronological order;
- stop planning additional backlog beyond limit;
- expose truncated backlog;
- require explicit user opt-in;
- maximum configured limit is bounded.

Initial maximum:

```text
7
```

### 16.5 Safe defaults

```text
once     -> run_once_latest
daily    -> skip
monthly  -> run_once_latest
```

### 16.6 No invisible policy

The effective policy appears in:

- create response;
- schedule detail;
- execution history;
- Admin BFF;
- audit.

---

## 17. Durable occurrence model

## 17.1 Occurrence identity

Unique:

```text
schedule_id + scheduled_for
```

`scheduled_for` is an exact UTC instant derived from local date/time/timezone.

### 17.2 Occurrence statuses

```text
planned
due
screening
ready
processing
retry_wait
succeeded
failed_business
failed_terminal
blocked
skipped_missed
skipped_superseded
cancelled
expired
```

### 17.3 Occurrence idempotency key

```text
sched:<schedule_id>:<scheduled_local_date>
```

Preserve the existing date-based key for compatible date-only schedules.

If multiple same-day occurrences are introduced later, the key contract must
version.

### 17.4 Execution attempt

Each occurrence has append-only attempts:

```text
attempt number
lease owner
started at
finished at
phase
result
stable error code
retryable
policy snapshot
fee snapshot
Ledger transaction ID
```

### 17.5 Lease

Occurrence processing uses:

```text
SELECT ... FOR UPDATE SKIP LOCKED
```

Fields:

```text
lease_owner
lease_expires_at
next_attempt_at
attempt_count
```

### 17.6 Planner and dispatcher

Separate jobs:

```text
schedule occurrence planner
schedule occurrence dispatcher
```

Planner:

- computes due/missed occurrences;
- applies missed-run policy;
- creates durable rows.

Dispatcher:

- claims due/retry rows;
- evaluates;
- posts;
- records result.

### 17.7 Trigger cadence

Recommended:

```text
planner every 5 minutes
dispatcher every 1 minute
```

The exact cadence is measured.

### 17.8 Scheduler-lock failure

If a singleton lock cannot be acquired:

- job invocation is skipped;
- durable due state remains;
- next invocation recovers;
- lock failure metric/alert applies;
- no occurrence is silently lost.

---

## 18. Schedule execution evaluation

## 18.1 Phase order

1. claim occurrence;
2. parse typed command;
3. validate command version;
4. validate schedule still active;
5. validate occurrence window;
6. resolve accounts;
7. validate ownership;
8. validate target/pocket;
9. validate currency;
10. evaluate current Ledger policy;
11. resolve current fee;
12. compare fee with stored cap;
13. build canonical posting command;
14. call Ledger posting core;
15. record transaction/result.

### 18.2 Current policy

The evaluator rechecks all policy that Ledger authoritatively owns.

It must not reuse a creation-time allow decision as current truth.

### 18.3 Fee mode

Initial:

```text
resolve_at_execution
```

### 18.4 Fee cap

Schedule creation stores:

```text
max_fee_amount
```

If current fee is higher:

```text
SCHEDULE_FEE_CAP_EXCEEDED
```

The occurrence becomes `blocked`.

The schedule pauses by default.

### 18.5 Zero-fee legacy schedules

Migration policy:

- resolve current fee;
- if zero, execute normally;
- if non-zero and no legacy fee consent exists, block and pause;
- require user update/confirmation before future execution.

### 18.6 Fee quote object

Scheduled execution may use an internal short-lived fee resolution/quote created
inside the execution flow.

It must be consumed atomically with the posting or linked idempotently.

Do not store a quote created months earlier.

### 18.7 Fraud boundary

Creation continues through existing Gateway/Fraud behavior.

Execution does not add a direct Ledger-to-Fraud call in the baseline.

Controls:

- allowed command types remain narrow;
- per-occurrence amount limits;
- daily limits;
- schedule count limits;
- current Ledger policy;
- event-based Fraud observation;
- operator pause.

This is an explicit residual risk, not an accidental bypass.

---

## 19. Failure classification

## 19.1 Infrastructure retryable

Examples:

```text
database timeout
temporary lock timeout
connection reset
context deadline
temporary internal dependency failure
scheduler worker crash
```

Action:

- `retry_wait`;
- exponential backoff with jitter;
- retry until occurrence cutoff or max attempt.

### 19.2 Business transient

Examples:

```text
insufficient funds
temporary account restriction
daily limit currently exhausted
```

Initial policy:

- occurrence fails as business failure;
- do not retry repeatedly on the same day unless user policy explicitly allows
  one delayed retry;
- increment consecutive failure count;
- continue future schedule;
- pause after threshold.

### 19.3 Business terminal

Examples:

```text
target no longer exists
target ownership invalid
currency disabled permanently
command version unsupported
schedule payload corrupt
account closed
fee cap exceeded
policy permanently disallows operation
```

Action:

- `blocked` or `failed_terminal`;
- pause schedule immediately;
- user/operator action required.

### 19.4 Duplicate/already posted

`ErrAlreadyPosted` is success.

The occurrence stores the existing transaction ID where retrievable.

### 19.5 Default thresholds

Initial:

```text
max infrastructure attempts: 5
retry window:                24 hours
pause after business fails:  3 consecutive occurrences
```

Configurable within safe bounds.

### 19.6 Error storage

Store:

```text
stable code
sanitized message
phase
retryable
```

Do not store secrets or arbitrary raw payload.

---

## 20. Schedule public API

Preserve existing endpoints.

Additive endpoints:

```text
GET /api/v1/scheduled-transactions/{id}/executions
GET /api/v1/scheduled-transactions/{id}/executions/{execution_id}
POST /api/v1/scheduled-transactions/{id}/confirm-fee-cap
```

Optional user action:

```text
POST /api/v1/scheduled-transactions/{id}/retry-execution/{execution_id}
```

Only for explicitly eligible blocked/failed occurrences and with idempotency.

### 20.1 Create/update request additions

```text
timezone
local_time
missed_run_policy
catch_up_limit
max_fee_amount
consecutive_failure_threshold
```

### 20.2 Update semantics

Updating a schedule:

- increments version;
- affects future unplanned occurrences;
- does not mutate succeeded/failed history;
- planned future occurrences may be cancelled/replanned through a controlled
  transition;
- does not change an occurrence already processing.

### 20.3 Cancel semantics

Cancellation:

- blocks new occurrence planning;
- cancels unclaimed future occurrences;
- cannot undo posted transaction;
- does not delete history.

---

# Part C — Top-Up Fees

## 21. Top-up fee semantics

## 21.1 User-visible amounts

```text
wallet credit amount = requested amount
fee amount           = resolved top-up fee
provider total debit = requested amount + fee amount
```

Example synthetic values:

```text
requested wallet credit: IDR 100,000
top-up fee:              IDR 2,500
provider total debit:    IDR 102,500
```

### 21.2 Existing amount compatibility

The existing Payin `amount` remains the wallet credit amount.

Add:

```text
fee_amount
total_debit
```

Existing zero-fee rows satisfy:

```text
fee_amount = 0
total_debit = amount
```

### 21.3 Fee application

Add fee-quote metadata:

```text
fee_application = added_on_top
```

for `money_in`.

### 21.4 Fee currency

```text
fee currency = top-up currency
```

No FX.

### 21.5 Fee recognition

Recognized only when:

- provider success is accepted;
- Payin settles;
- Ledger posts wallet credit and fee.

### 21.6 Failed top-up

A failed/expired/cancelled top-up:

- creates no fee Ledger entry;
- creates no fee revenue;
- keeps quote consumption evidence if the quote was committed to the intent;
- does not reuse the consumed quote for another top-up.

### 21.7 Full reversal

C5 initial policy:

A governed full reversal of a successfully posted fee-bearing top-up reverses:

- user principal credit;
- platform fee credit;
- provider settlement debit;

through one explicit balanced compensating Ledger transaction.

Partial top-up reversal is out of scope.

T0 must verify the current reversal/chargeback path before activation.

---

## 22. Top-up fee rule and quote

## 22.1 Rule

Reuse `fee_rules`.

Transaction type:

```text
money_in
```

Dimensions:

```text
gateway
currency
user specificity
flat amount
basis points
minimum
maximum
enabled
effective rule precedence
```

### 22.2 Quote request

Reuse/additively extend the existing fee-quote API.

Input:

```text
transaction type = money_in
gateway
currency
amount = requested wallet credit
```

### 22.3 Quote response

Add/ensure:

```text
amount
fee_amount
total_debit
currency
gateway
fee_application
expires_at
quote ID
```

### 22.4 Quote contract

```text
total_debit = amount + fee_amount
```

Checked integer arithmetic is mandatory.

### 22.5 Quote TTL

Use current fee-quote TTL unless T0 identifies a mismatch with provider intent
creation.

### 22.6 Quote required policy

Rollout stages:

1. quote optional while every active `money_in` rule resolves zero;
2. quote required for every route with a non-zero active fee;
3. quote required for all new-version top-up clients;
4. legacy fee-free clients remain compatible until contract deprecation.

### 22.7 Quote ownership and equality

Validate:

```text
user
transaction type
gateway
currency
amount
status
expiry
```

A mismatched quote cannot create an intent.

### 22.8 Quote consumption reference

```text
consumed_by_type = payin
consumed_by_reference = <payin_id>
```

Consumption is idempotent.

---

## 23. Payin intent lifecycle with fee

## 23.1 Create flow

1. Gateway receives top-up request and fee quote ID.
2. Gateway validates public shape and user.
3. PayinService creates a deterministic pending intent with:
   - requested amount;
   - currency;
   - gateway/vendor;
   - quote ID.
4. PayinService calls Ledger to validate and consume the quote using
   `payin:<intent_id>`.
5. Ledger returns immutable fee snapshot.
6. PayinService persists:
   - fee amount;
   - total debit;
   - rule/gateway evidence.
7. PayinService sends `total_debit` to VendorService.
8. Intent moves to provider-pending.
9. Public response exposes all three amounts.

### 23.2 Cross-service crash windows

#### Intent committed, quote not consumed

Recovery retries quote consumption.

#### Quote consumed, fee snapshot not stored

Recovery queries/retries consumption by same reference and stores the same
snapshot.

#### Fee snapshot stored, vendor request lost

Vendor dispatch idempotency/recovery follows current Payin/Vendor design.

### 23.3 Provider amount

VendorService receives:

```text
total_debit
currency
provider reference
```

It does not calculate the fee.

### 23.4 Callback validation

Payin validates:

```text
provider reference
vendor
gateway
currency
total_debit
status
signature/protocol
```

A callback matching requested wallet credit but not `total_debit` is rejected.

### 23.5 Quote expiry after consumption

A consumed quote remains valid for the linked intent.

It is not repriced at callback time.

### 23.6 Fee-rule change after intent creation

The intent uses its consumed quote.

The new rule affects only new intents.

---

## 24. Ledger money-in posting with fee

## 24.1 Ledger authority

Payin may not invent or alter the fee amount.

Settlement request includes:

```text
payin ID
fee quote ID
provider reference
currency
```

Ledger resolves the consumed quote and verifies linkage.

### 24.2 One balanced posting

Conceptual entries:

```text
debit  vendor settlement account: total_debit
credit user cash account:         requested amount
credit fee revenue account:       fee amount
```

When fee is zero, behavior is economically equivalent to the current two-entry
posting.

### 24.3 Transaction header

T0 must lock the current header semantics.

C5 policy:

- primary transaction `amount` remains the requested wallet credit amount;
- metadata/additive fields contain:
  - fee amount;
  - total debit;
  - fee quote ID;
  - payin ID;
  - provider reference.

This preserves user-facing top-up amount semantics.

### 24.4 Fee account

Resolve by:

```text
money_in
gateway
currency
```

The fee account currency must match.

### 24.5 Idempotency

Top-up settlement idempotency includes:

```text
payin ID
currency
requested amount
fee quote ID
fee amount
total debit
```

### 24.6 Duplicate callback

Returns/observes the existing Ledger transaction.

No duplicate principal or fee.

### 24.7 Posting failure

If Ledger is unavailable after provider success:

- Payin remains recoverable;
- callback evidence remains;
- retry uses the same quote and idempotency identity;
- no repricing;
- no second vendor collection.

---

## 25. Top-up events and notification

## 25.1 Owner event

Payin lifecycle event includes additive exact fields:

```text
requested_amount
fee_amount
total_debit
currency
fee_quote_id
status
```

### 25.2 Ledger event

The posted money-in event includes:

```text
transaction amount
fee amount
total debit
currency
payin ID
fee quote ID
```

### 25.3 Notification

User message should distinguish:

```text
wallet credited
fee paid
total provider debit
```

If C3 is not active, existing in-app rendering must at least show the credited
amount correctly.

### 25.4 C2 compatibility

Recognized top-up fee revenue comes from Ledger fee-account entries, not from:

- fee rule;
- quote;
- provider request;
- Payin success flag alone.

---

## 26. Proposed Ledger schema

T0 chooses exact migration numbers after the current head.

## 26.1 `savings_products`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
product_code TEXT UNIQUE NOT NULL
name TEXT NOT NULL
currency CHAR(3) NOT NULL
status TEXT NOT NULL
day_count_convention TEXT NOT NULL
capitalization_frequency TEXT NOT NULL
timezone TEXT NOT NULL
minimum_eligible_balance BIGINT NOT NULL
interest_expense_account_id UUID NOT NULL
interest_payable_account_id UUID NOT NULL
version BIGINT NOT NULL
created_by TEXT NOT NULL
updated_by TEXT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
```

## 26.2 `savings_rate_versions`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
product_id UUID NOT NULL
annual_rate_bps INTEGER NOT NULL
status TEXT NOT NULL
effective_from DATE NOT NULL
effective_until DATE NULL
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
CHECK (annual_rate_bps >= 0)
```

Initial upper bound may preserve the current `2000` bps limit unless T0
authorizes another product bound.

## 26.3 `savings_enrollments`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
product_id UUID NOT NULL
account_id UUID NOT NULL
user_id UUID NOT NULL
status TEXT NOT NULL
mode TEXT NOT NULL
effective_from DATE NOT NULL
effective_until DATE NULL
carry_numerator NUMERIC NOT NULL
carry_denominator NUMERIC NOT NULL
version BIGINT NOT NULL
created_by TEXT NOT NULL
updated_by TEXT NOT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (product_id, account_id, effective_from)
```

## 26.4 `interest_periods`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
product_id UUID NOT NULL
currency CHAR(3) NOT NULL
period_year INTEGER NOT NULL
period_month INTEGER NOT NULL
period_start_at TIMESTAMPTZ NOT NULL
period_end_at TIMESTAMPTZ NOT NULL
accrual_cutoff_at TIMESTAMPTZ NOT NULL
close_not_before_at TIMESTAMPTZ NOT NULL
status TEXT NOT NULL
expected_item_count BIGINT NOT NULL
completed_item_count BIGINT NOT NULL
blocked_item_count BIGINT NOT NULL
total_accrued_amount BIGINT NOT NULL
total_capitalized_amount BIGINT NOT NULL
opened_at TIMESTAMPTZ NULL
closing_started_at TIMESTAMPTZ NULL
closed_at TIMESTAMPTZ NULL
failed_at TIMESTAMPTZ NULL
last_error_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (product_id, period_year, period_month)
```

## 26.5 `interest_daily_accruals`

```text
id UUID PRIMARY KEY
period_id UUID NOT NULL
enrollment_id UUID NOT NULL
account_id UUID NOT NULL
accrual_date DATE NOT NULL
snapshot_id UUID NULL
closing_balance BIGINT NULL
rate_version_id UUID NULL
annual_rate_bps INTEGER NULL
exact_numerator NUMERIC NULL
denominator NUMERIC NULL
opening_carry_numerator NUMERIC NULL
recognized_amount BIGINT NULL
closing_carry_numerator NUMERIC NULL
status TEXT NOT NULL
attempt_count INTEGER NOT NULL
next_attempt_at TIMESTAMPTZ NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
ledger_transaction_id UUID NULL
error_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (enrollment_id, accrual_date)
```

## 26.6 `interest_capitalization_items`

```text
id UUID PRIMARY KEY
period_id UUID NOT NULL
enrollment_id UUID NOT NULL
account_id UUID NOT NULL
capitalization_amount BIGINT NOT NULL
status TEXT NOT NULL
attempt_count INTEGER NOT NULL
next_attempt_at TIMESTAMPTZ NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
ledger_transaction_id UUID NULL
error_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (period_id, enrollment_id)
```

## 26.7 `interest_period_checks`

```text
id UUID PRIMARY KEY
period_id UUID NOT NULL
check_name TEXT NOT NULL
status TEXT NOT NULL
expected_value TEXT NULL
actual_value TEXT NULL
severity TEXT NOT NULL
details JSONB NULL
checked_at TIMESTAMPTZ NOT NULL
UNIQUE (period_id, check_name)
```

## 26.8 `interest_adjustments`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
source_period_id UUID NOT NULL
enrollment_id UUID NOT NULL
source_accrual_id UUID NULL
source_capitalization_id UUID NULL
amount BIGINT NOT NULL
direction TEXT NOT NULL
status TEXT NOT NULL
reason TEXT NOT NULL
created_by TEXT NOT NULL
approved_by TEXT NULL
ledger_transaction_id UUID NULL
created_at TIMESTAMPTZ NOT NULL
approved_at TIMESTAMPTZ NULL
posted_at TIMESTAMPTZ NULL
```

## 26.9 System account

Add per supported currency:

```text
accrued_interest_payable
```

Existing:

```text
interest_expense
```

is reused.

## 26.10 Extend `scheduled_transactions`

Add conceptually:

```text
command_version INTEGER
command_digest BYTEA
currency CHAR(3)
timezone TEXT
local_time TIME
missed_run_policy TEXT
catch_up_limit INTEGER
max_fee_amount BIGINT NULL
consecutive_failure_threshold INTEGER
consecutive_failure_count INTEGER
last_planned_at TIMESTAMPTZ NULL
version BIGINT
paused_reason TEXT NULL
```

Legacy `cmd_payload` remains during expand/contract.

## 26.11 `scheduled_occurrences`

```text
id UUID PRIMARY KEY
public_id TEXT UNIQUE NOT NULL
schedule_id UUID NOT NULL
schedule_version BIGINT NOT NULL
scheduled_for TIMESTAMPTZ NOT NULL
scheduled_local_date DATE NOT NULL
status TEXT NOT NULL
idempotency_key TEXT NOT NULL
policy_snapshot JSONB NOT NULL
fee_amount BIGINT NULL
fee_quote_id UUID NULL
ledger_transaction_id UUID NULL
attempt_count INTEGER NOT NULL
next_attempt_at TIMESTAMPTZ NULL
lease_owner TEXT NULL
lease_expires_at TIMESTAMPTZ NULL
error_code TEXT NULL
created_at TIMESTAMPTZ NOT NULL
updated_at TIMESTAMPTZ NOT NULL
UNIQUE (schedule_id, scheduled_for)
UNIQUE (idempotency_key)
```

## 26.12 `scheduled_execution_attempts`

```text
id UUID PRIMARY KEY
occurrence_id UUID NOT NULL
attempt_number INTEGER NOT NULL
phase TEXT NOT NULL
result TEXT NOT NULL
retryable BOOLEAN NOT NULL
error_code TEXT NULL
ledger_transaction_id UUID NULL
started_at TIMESTAMPTZ NOT NULL
finished_at TIMESTAMPTZ NULL
created_at TIMESTAMPTZ NOT NULL
UNIQUE (occurrence_id, attempt_number)
```

## 26.13 `scheduled_execution_policies`

A separate policy table is optional.

Initial preference:

Store normalized fields on the schedule and a snapshot on every occurrence.

Avoid premature generic policy abstraction.

---

## 27. Proposed Payin schema

Add additive fields to the current intent/request table.

```text
fee_quote_id UUID NULL
fee_rule_id UUID NULL
fee_gateway TEXT NULL
fee_amount BIGINT NOT NULL DEFAULT 0
total_debit BIGINT NULL
fee_application TEXT NOT NULL DEFAULT 'added_on_top'
fee_quote_consumed_at TIMESTAMPTZ NULL
fee_snapshot_version INTEGER NOT NULL DEFAULT 1
```

Backfill:

```text
fee_amount = 0
total_debit = amount
```

### 27.1 Checks

```text
fee_amount >= 0
total_debit >= amount
total_debit = amount + fee_amount
```

The exact implementation may use application validation plus database checks.

### 27.2 No cross-database foreign key

`fee_quote_id` is an application reference to Ledger.

---

## 28. Internal contracts

## 28.1 Savings and period close

Additive Ledger contracts:

```text
ListSavingsProducts
GetSavingsProduct
ListSavingsEnrollments
GetSavingsEnrollment
ListInterestAccruals
GetInterestPeriod
ListInterestPeriods
GetInterestCapitalization
PreviewInterestPeriodClose
RunInterestPeriodClose
RetryInterestPeriodItem
```

Admin-only operations:

```text
CreateSavingsProduct
UpdateSavingsProductStatus
CreateSavingsRate
SubmitSavingsRate
ApproveSavingsRate
RejectSavingsRate
EnrollSavingsAccount
PauseSavingsEnrollment
EndSavingsEnrollment
CreateInterestAdjustment
ApproveInterestAdjustment
```

### 28.2 Schedule

Additive:

```text
ListScheduledOccurrences
GetScheduledOccurrence
RetryScheduledOccurrence
ConfirmScheduledFeeCap
```

### 28.3 Fee quote and top-up

Extend or add:

```text
ValidateAndConsumeFeeQuote
GetConsumedFeeQuote
PostMoneyInWithFeeQuote
```

Contracts must be idempotent by:

```text
consumed_by_type + consumed_by_reference
```

### 28.4 Compatibility

Every Protobuf change must pass:

- lint;
- breaking check;
- field-number policy;
- generated artifact gate;
- tolerant old/new consumer tests;
- staged rollout harness.

---

## 29. Public API additions

## 29.1 Savings read APIs

```text
GET /api/v1/savings/products
GET /api/v1/savings/enrollments
GET /api/v1/savings/enrollments/{id}
GET /api/v1/savings/enrollments/{id}/accruals
GET /api/v1/savings/enrollments/{id}/periods
```

Initial public API is read-only.

### 29.2 Accrual response

Expose:

```text
date
closing balance
rate bps
recognized accrued amount
capitalization status
currency
```

Do not expose internal system-account IDs by default.

### 29.3 Pending interest

Expose a clearly non-balance field:

```text
accrued_not_yet_capitalized
```

It is not included in available balance.

### 29.4 Schedule execution history

As defined in Section 20.

### 29.5 Top-up response

Add:

```text
amount
fee_amount
total_debit
currency
fee_quote_id
```

Existing `amount` remains compatible.

### 29.6 Error codes

Interest:

```text
SAVINGS_PRODUCT_UNAVAILABLE
SAVINGS_ENROLLMENT_NOT_FOUND
SAVINGS_RATE_MISSING
INTEREST_SNAPSHOT_MISSING
INTEREST_PERIOD_NOT_READY
INTEREST_PERIOD_CLOSED
INTEREST_CAPITALIZATION_BLOCKED
INTEREST_ADJUSTMENT_REQUIRES_APPROVAL
```

Schedule:

```text
SCHEDULE_POLICY_INVALID
SCHEDULE_OCCURRENCE_NOT_FOUND
SCHEDULE_OCCURRENCE_NOT_RETRYABLE
SCHEDULE_FEE_CAP_EXCEEDED
SCHEDULE_COMMAND_VERSION_UNSUPPORTED
SCHEDULE_MISSED_BACKLOG_TRUNCATED
SCHEDULE_PAUSED_AFTER_FAILURE
```

Top-up:

```text
TOPUP_FEE_QUOTE_REQUIRED
TOPUP_FEE_QUOTE_INVALID
TOPUP_FEE_QUOTE_EXPIRED
TOPUP_FEE_QUOTE_MISMATCH
TOPUP_TOTAL_DEBIT_MISMATCH
TOPUP_FEE_POSTING_MISMATCH
```

---

## 30. Per-service changes

## 30.1 Gateway

Gateway must:

- expose additive savings read routes;
- expose schedule execution history and policy fields;
- preserve existing schedule API;
- require/propagate top-up quote where policy applies;
- display amount, fee, and total debit unambiguously;
- include fee quote in request equality;
- avoid local fee calculation;
- avoid local interest calculation;
- preserve Fraud creation screening;
- map stable errors.

### 30.2 LedgerService

Ledger must:

- own savings product/rate/enrollment;
- own daily accrual and carry;
- own interest periods and capitalization;
- own schedule occurrence state;
- own scheduled current-policy evaluation;
- own fee quote and consumption;
- own principal-and-fee posting;
- emit events;
- preserve posting invariants.

### 30.3 PayinService

Payin must:

- store quote linkage and immutable fee snapshot;
- send total debit to VendorService;
- validate callback total debit;
- preserve requested wallet credit separately;
- recover quote-consumption crash windows;
- ask Ledger to post using the consumed quote;
- never calculate or reprice fee;
- recognize no fee on failed intent.

### 30.4 VendorService

VendorService must:

- receive total debit;
- echo/normalize total debit and currency;
- reject amount mismatch;
- remain unaware of fee-rule logic;
- preserve idempotency and callback safety.

### 30.5 AssuranceService

Assurance must add:

- daily accrual completeness findings;
- period close reconciliation;
- capitalization-to-liability reconciliation;
- scheduled occurrence-to-Ledger transaction reconciliation;
- top-up requested/fee/total debit/Ledger reconciliation.

### 30.6 FraudService

Fraud remains in schedule-creation path.

For top-up:

- screen the provider total debit or the currently locked public amount
  according to existing semantics;
- currency is mandatory;
- C5 must document whether fee is included in velocity.

Initial policy:

```text
top-up velocity uses total_debit
```

because that is the external collection exposure.

### 30.7 Notification module

Add kinds/events where C3 exists:

```text
interest.capitalized
schedule.execution.failed
schedule.paused
topup.succeeded_with_fee
```

Without C3, existing notification copy must at least remain exact.

### 30.8 Admin BFF

Admin BFF adds:

- savings product list/detail;
- rate maker/checker;
- enrollment control;
- period dashboard;
- close preview/run/retry;
- blocked accrual/item view;
- adjustment maker/checker;
- schedule occurrence view;
- schedule failure/retry/pause reason;
- top-up fee-rule view;
- quote/intent/settlement trace;
- product kill switches;
- audit.

---

## 31. Events

## 31.1 Interest events

```text
ledger.interest_accrued.v1
ledger.interest_capitalized.v1
ledger.interest_period.closed.v1
ledger.interest_adjusted.v1
```

### 31.2 Schedule events

```text
ledger.schedule.occurrence.succeeded.v1
ledger.schedule.occurrence.failed.v1
ledger.schedule.paused.v1
```

Do not emit a user-facing failure event for every infrastructure retry.

### 31.3 Top-up events

Extend current Payin and Ledger event schemas with:

```text
amount
fee_amount
total_debit
fee_quote_id
currency
```

### 31.4 Event idempotency

Every event has stable logical event ID.

### 31.5 Event privacy

Do not include:

```text
raw schedule command payload
full idempotency key
vendor credential
raw callback
bank destination
operator approval note
```

---

## 32. Observability

## 32.1 Interest metrics

```text
seev_interest_accruals_total{product,currency,result}
seev_interest_accrual_duration_seconds{product,currency,result}
seev_interest_accrual_due_total{product,currency,status}
seev_interest_accrual_oldest_due_age_seconds{product,currency}
seev_interest_carry_minor_fraction{product,currency}
seev_interest_periods_total{product,currency,status}
seev_interest_period_close_duration_seconds{product,currency,result}
seev_interest_capitalizations_total{product,currency,result}
seev_interest_period_reconciliation_delta{product,currency,check}
```

### 32.2 Schedule metrics

```text
seev_schedule_occurrences_total{kind,result,policy}
seev_schedule_execution_duration_seconds{command,result}
seev_schedule_retry_total{command,reason}
seev_schedule_blocked_total{command,reason}
seev_schedule_missed_total{kind,policy,result}
seev_schedule_oldest_due_age_seconds{command}
seev_schedule_paused_total{reason}
seev_schedule_fee_cap_total{currency,result}
```

### 32.3 Top-up fee metrics

```text
seev_topup_fee_quotes_total{gateway,currency,result}
seev_topup_fee_amount_minor{gateway,currency}
seev_topup_fee_quote_consumption_total{gateway,currency,result}
seev_topup_with_fee_total{gateway,currency,result}
seev_topup_total_debit_mismatch_total{gateway,currency}
seev_topup_fee_posting_reconciliation_total{gateway,currency,result}
```

### 32.4 Forbidden labels

Do not label metrics with:

```text
user ID
account ID
enrollment ID
period ID
schedule ID
occurrence ID
quote ID
payin ID
transaction ID
idempotency key
```

### 32.5 Logging

Structured logs may include:

```text
product code
currency
period
job
schedule command
stable error code
gateway
phase
trace ID
```

Do not log:

```text
raw command payload
recipient identity beyond approved public ID
fee quote secret
vendor credential
raw callback
full approval note
```

### 32.6 Tracing

Interest:

```text
snapshot
-> daily accrual
-> liability posting
-> period close
-> capitalization
-> outbox
```

Schedule:

```text
planner
-> occurrence
-> evaluation
-> fee resolution
-> posting
-> result
```

Top-up:

```text
fee quote
-> Payin intent
-> quote consumption
-> Vendor dispatch
-> callback
-> Ledger principal+fee posting
-> outbox
```

### 32.7 Alerts

Required:

```text
daily accrual backlog
missing snapshot
missing savings rate
missing interest-payable account
period not ready past deadline
period close failed
capitalization reconciliation mismatch
schedule due backlog
schedule infrastructure retry spike
schedule business-failure spike
schedule fee-cap block
schedule planner not running
top-up fee quote mismatch
top-up provider total mismatch
top-up fee posting mismatch
fee-bearing top-up route without active rule
```

Every alert links to a runbook.

---

## 33. Runbooks

Create:

```text
docs/runbooks/interest-accrual-backlog.md
docs/runbooks/interest-snapshot-missing.md
docs/runbooks/interest-rate-missing.md
docs/runbooks/interest-period-not-ready.md
docs/runbooks/interest-period-close-failed.md
docs/runbooks/interest-capitalization-mismatch.md
docs/runbooks/interest-correction.md
docs/runbooks/interest-account-close-blocked.md

docs/runbooks/schedule-occurrence-backlog.md
docs/runbooks/schedule-infrastructure-failure.md
docs/runbooks/schedule-business-failure.md
docs/runbooks/schedule-fee-cap-exceeded.md
docs/runbooks/schedule-missed-run-policy.md
docs/runbooks/schedule-policy-bypass-incident.md
docs/runbooks/schedule-replay.md

docs/runbooks/topup-fee-quote-mismatch.md
docs/runbooks/topup-total-debit-mismatch.md
docs/runbooks/topup-fee-posting-failed.md
docs/runbooks/topup-provider-success-ledger-pending.md
docs/runbooks/topup-fee-reversal.md
```

Each runbook includes:

- impact;
- product/money correctness statement;
- source of truth;
- safe immediate control;
- whether intake should pause;
- whether in-flight work may finish;
- idempotent retry;
- correction rather than direct update;
- reconciliation;
- evidence to record.

---

## 34. Security and threat model

Update the repository threat model.

## 34.1 Interest threats

- daily balance instead of snapshot;
- snapshot date mismatch;
- mutable historical rate;
- lost fractional interest;
- carry duplication;
- missing daily accrual hidden as zero;
- expense posted without liability;
- liability capitalized twice;
- closed period mutated;
- account closed before capitalization;
- operator self-approves correction;
- interest account currency mismatch.

### 34.2 Schedule threats

- corrupt stored command;
- command changed after occurrence planning;
- missed run executes unexpectedly;
- unbounded catch-up drains account;
- stale creation-time policy reused;
- fee introduced without consent;
- schedule bypasses current limit;
- duplicate occurrence;
- infrastructure retry creates duplicate money;
- target changed/invalid;
- operator replays successful occurrence;
- schedule worker lock failure hides backlog.

### 34.3 Top-up fee threats

- fee quote belongs to another user;
- quote amount/gateway/currency mismatch;
- provider collects wrong total;
- Payin changes fee amount;
- fee repriced after quote consumption;
- fee posted on failed top-up;
- principal posted without fee;
- fee posted without principal;
- duplicate callback duplicates fee;
- reversal leaves fee revenue incorrect;
- overflow in amount plus fee.

### 34.4 Admin threats

- maker self-approval;
- unauthorized period close;
- direct period reopen;
- direct schedule success mutation;
- fee-rule activation without quote readiness;
- raw user/command data exposure;
- CSRF.

### 34.5 Required control format

For every threat:

```text
prevention
detection
test
alert
runbook
residual risk
owner
```

---

## 35. Configuration

Suggested:

```text
INTEREST_MONTHLY_ENABLED=false
INTEREST_SHADOW_MODE=true
INTEREST_DEFAULT_TIMEZONE=Asia/Jakarta
INTEREST_ACCRUAL_BATCH_SIZE=100
INTEREST_ACCRUAL_WORKERS=2
INTEREST_ACCRUAL_LEASE_DURATION=2m
INTEREST_ACCRUAL_RETRY_LIMIT=5
INTEREST_PERIOD_CLOSE_ENABLED=false
INTEREST_PERIOD_CLOSE_BATCH_SIZE=100
INTEREST_PERIOD_CLOSE_WORKERS=2
INTEREST_PERIOD_CLOSE_NOT_BEFORE=01:15

SCHEDULE_OCCURRENCE_ENABLED=false
SCHEDULE_PLANNER_CRON=*/5 * * * *
SCHEDULE_DISPATCHER_CRON=* * * * *
SCHEDULE_PLANNER_LOOKBACK_DAYS=31
SCHEDULE_MAX_CATCH_UP=7
SCHEDULE_MAX_INFRA_ATTEMPTS=5
SCHEDULE_RETRY_WINDOW=24h
SCHEDULE_DEFAULT_CONSECUTIVE_FAILURE_LIMIT=3
SCHEDULE_DELIVERY_BATCH_SIZE=50
SCHEDULE_WORKERS=2
SCHEDULE_LEASE_DURATION=2m

TOPUP_FEE_ENABLED=false
TOPUP_FEE_QUOTE_REQUIRED=false
TOPUP_FEE_GATEWAYS=
TOPUP_FEE_MAX_AMOUNT=
```

Rules:

- all new financial behavior disabled by default;
- invalid values fail startup;
- catch-up has a hard maximum;
- no unsafe unlimited retry option;
- no direct close-force flag without owner-side approval;
- no insecure fee bypass config.

---

## 36. Task breakdown

# T0 — Entry gate and current-state inventory

### Work

- Record exact commit and migration heads.
- Run current repository gates.
- Trace current daily accrual math and postings.
- Inventory snapshot timing and completeness.
- Inventory savings account types and currencies.
- Inventory current schedule schema/API/worker.
- Trace schedule creation and execution policy paths.
- Record current no-catch-up behavior.
- Record business/infra failure handling.
- Inventory shared scheduler jobs and locks.
- Inventory fee rules and fee quotes.
- Trace `money_in` zero-fee behavior.
- Trace top-up intent, vendor amount, callback, and Ledger posting.
- Trace reversal/chargeback.
- Inventory events, notifications, Assurance, retention, and privacy.
- Produce blast-radius matrix.

### Acceptance

- [ ] Every foundation is verified in code.
- [ ] Every legacy behavior is reproducible.
- [ ] Current daily-interest accounting entries are known.
- [ ] Current policy bypass is explicitly documented.
- [ ] Current top-up amount semantics are known.
- [ ] All blockers have owners.
- [ ] Existing IDR journeys remain green.

---

# T1 — Lock product contracts, accounting, and threat model

### Work

- Lock product scope and non-goals.
- Lock daily accrual and monthly capitalization accounting.
- Lock exact fraction/carry math.
- Lock ACT/365F.
- Lock period lifecycle.
- Lock close readiness and correction.
- Lock schedule policies and defaults.
- Lock fee consent.
- Lock top-up fee added-on-top semantics.
- Lock quote/Payin/Ledger ownership.
- Add OpenAPI/Protobuf/event drafts.
- Update threat model.
- Add sequence/state/failure diagrams.

### Required diagrams

```text
daily interest accrual
monthly period close
capitalization retry
closed-period correction
legacy-to-monthly cutover

schedule planning
missed-run policies
execution evaluation
fee-cap block
infrastructure retry
business pause
crash after posting before occurrence update

top-up quote
quote consumption
provider collection
callback
principal+fee Ledger posting
provider success with Ledger outage
full top-up fee reversal
```

### Acceptance

- [ ] Accounting entries are explicit.
- [ ] No period reopen path exists.
- [ ] Carry math is deterministic.
- [ ] Schedule policy is user-visible.
- [ ] Old policy bypass decision is resolved.
- [ ] Top-up amount/fee/total semantics are explicit.
- [ ] Threat controls have tests and owners.

---

# T2 — Exact interest math and product/rate foundation

### Work

- Implement exact numerator/denominator math.
- Implement carry.
- Add property/fuzz tests.
- Add savings product schema.
- Add rate-version schema.
- Add maker/checker.
- Add enrollment schema.
- Add interest-payable system accounts.
- Add Admin BFF product/rate/enrollment pages.
- Add read-only public product/enrollment views.
- Add contract fixtures.

### Acceptance

- [ ] No daily fraction is silently lost.
- [ ] Rate versions are immutable.
- [ ] Overlap is blocked.
- [ ] Maker cannot approve own rate.
- [ ] Product currency and system accounts match.
- [ ] Carry survives rate change.
- [ ] Zero/negative balance is deterministic.
- [ ] Public pending interest is not labeled available balance.

---

# T3 — Durable daily accrual

### Work

- Add period and daily accrual tables.
- Add planner.
- Add worker leasing.
- Resolve snapshot/rate.
- Calculate carry.
- Post daily liability.
- Add retries and blocked states.
- Add metrics.
- Add retention.
- Add shadow mode.
- Add reconciliation.

### Acceptance

- [ ] One enrollment/day creates one row.
- [ ] Snapshot is immutable basis.
- [ ] Positive recognized amount posts once.
- [ ] Zero creates evidence without posting.
- [ ] Missing snapshot/rate is visible.
- [ ] Worker restart recovers.
- [ ] One account failure does not block others.
- [ ] Liability equals completed positive accruals.
- [ ] Legacy daily posting remains untouched in shadow mode.

---

# T4 — Monthly period close and capitalization

### Work

- Add close scanner.
- Add readiness checks.
- Add preview.
- Add capitalization items.
- Add per-account posting.
- Add close summary.
- Add events.
- Add retry.
- Add correction workflow.
- Add Assurance.
- Add Admin BFF period controls.
- Add runbooks.

### Acceptance

- [ ] Period cannot close with missing accrual.
- [ ] Every account posts at most once.
- [ ] Liability release equals user capitalization.
- [ ] Worker crash recovers.
- [ ] Closed period is immutable.
- [ ] Correction uses explicit adjustment.
- [ ] Account-close block works.
- [ ] Close event is atomic with final state or safely recoverable.
- [ ] No global Ledger freeze is required.

---

# T5 — Legacy interest migration

### Work

- Add modes.
- Add bounded backfill to product/enrollment.
- Run shadow comparison.
- Select clean cutover month.
- Disable legacy runner per migrated enrollment.
- Activate new accrual.
- Complete first close.
- Update statements and docs.
- Add rollback controls.

### Acceptance

- [ ] No overlapping daily/new accrual.
- [ ] Historical legacy entries remain unchanged.
- [ ] Shadow differences are explained.
- [ ] Cutover begins at month boundary.
- [ ] First close reconciles.
- [ ] Rollback policy is exercised.
- [ ] User statements remain understandable.

---

# T6 — Schedule occurrence schema and planner

### Work

- Add schedule policy fields.
- Add typed command version/digest.
- Add occurrence/attempt tables.
- Add planner.
- Implement missed-run policies.
- Add catch-up bounds.
- Migrate legacy schedules.
- Add execution history API.
- Add Admin BFF occurrence view.
- Add metrics.

### Acceptance

- [ ] One expected date creates one occurrence.
- [ ] Planner restart does not duplicate.
- [ ] Skip/run-once/catch-up policies work.
- [ ] Catch-up never exceeds limit.
- [ ] Legacy policy is safely defaulted.
- [ ] Schedule detail shows effective policy.
- [ ] Existing schedule API remains compatible.

---

# T7 — Schedule evaluator and dispatcher

### Work

- Add occurrence leasing.
- Revalidate command.
- Revalidate current Ledger policy.
- Resolve current fee.
- Enforce fee cap.
- Classify failures.
- Add retries.
- Add pause thresholds.
- Post with deterministic key.
- Record existing transaction on duplicate.
- Add replay controls.
- Add worker metrics/traces.

### Acceptance

- [ ] Corrupt stored payload never posts.
- [ ] Current policy is evaluated.
- [ ] New fee above cap blocks.
- [ ] Infra retry does not duplicate.
- [ ] Business failure follows policy.
- [ ] Successful occurrence cannot replay.
- [ ] Lock failure does not lose due work.
- [ ] Current supported schedule types remain green.
- [ ] Fraud residual risk is documented.

---

# T8 — Schedule migration and policy-bypass evidence

### Work

- Classify all legacy schedules.
- Add fee-consent migration policy.
- Pause legacy schedules that would newly incur a fee.
- Run shadow occurrence planning.
- Compare old/new due results.
- Cut dispatcher.
- Preserve old key format.
- Disable legacy runner.
- Exercise rollback.
- Update docs and runbooks.

### Acceptance

- [ ] No duplicate old/new execution.
- [ ] No silent newly charged fee.
- [ ] Missed occurrence behavior is explicit.
- [ ] Existing completed history remains.
- [ ] Legacy runner can be safely disabled.
- [ ] Rollback does not repost money.
- [ ] Policy review evidence is linked.

---

# T9 — Top-up fee contract and Payin schema

### Work

- Add fee fields to Payin.
- Backfill zero-fee rows.
- Extend quote response.
- Add quote-required policy.
- Add create validation.
- Add quote-consumption recovery.
- Persist fee snapshot.
- Send total debit to VendorService.
- Add public response fixtures.
- Add Gateway request equality.

### Acceptance

- [ ] Existing fee-free top-up remains compatible.
- [ ] Non-zero route requires valid quote.
- [ ] Quote user/type/gateway/currency/amount match.
- [ ] Amount plus fee overflow fails.
- [ ] Quote consumption is idempotent.
- [ ] Crash windows recover.
- [ ] Vendor receives total debit.
- [ ] Payin never calculates fee.

---

# T10 — Top-up principal-and-fee settlement

### Work

- Extend VendorService fixtures.
- Validate callback total debit.
- Add Ledger quote-linked money-in operation.
- Post three entries.
- Add fee account resolution.
- Add duplicate handling.
- Add provider-success/Ledger-outage recovery.
- Add full reversal behavior.
- Extend events.
- Add Assurance.

### Acceptance

- [ ] User receives requested amount.
- [ ] Provider settlement uses total debit.
- [ ] Fee account receives fee.
- [ ] Entries balance.
- [ ] Failed top-up posts no fee.
- [ ] Duplicate callback posts once.
- [ ] Rule change does not reprice intent.
- [ ] Wrong total debit fails.
- [ ] Full reversal reverses principal and fee.
- [ ] Fee revenue reconciles to Ledger.

---

# T11 — Admin BFF, audit, and controls

### Work

- Add savings product/rate/enrollment controls.
- Add period close preview/run/retry.
- Add corrections.
- Add schedule occurrence/policy pages.
- Add top-up fee rule/quote trace.
- Add kill switches.
- Add CSRF.
- Add maker/checker.
- Add redacted audit.
- Add synthetic preview fixtures.

### Acceptance

- [ ] Unauthorized operator cannot mutate.
- [ ] Maker/checker is enforced owner-side.
- [ ] Closed period cannot reopen.
- [ ] Successful occurrence cannot replay.
- [ ] Fee rule activation is audited.
- [ ] Secrets/raw payloads are absent.
- [ ] Existing Admin BFF routes remain green.

---

# T12 — Observability, privacy, retention, and runbooks

### Work

- Add metrics and dashboards.
- Add alerts.
- Add runbooks.
- Add interest/schedule/fee retention.
- Update privacy export.
- Update backup/restore verification.
- Validate metric cardinality.
- Add product status panels.
- Add reconciliation panels.

### Acceptance

- [ ] All backlog ages are visible.
- [ ] Period readiness is visible.
- [ ] Schedule policy failures are visible.
- [ ] Top-up fee mismatches are visible.
- [ ] Every alert has a runbook.
- [ ] Retention jobs are bounded.
- [ ] Privacy export is updated.
- [ ] Restore verifies product tables.
- [ ] No high-cardinality label exists.

---

# T13 — E2E, chaos, load, and final evidence

### Work

- Add `scripts/interest-period-e2e.sh`.
- Add `scripts/schedule-policy-e2e.sh`.
- Add `scripts/topup-fee-e2e.sh`.
- Add `scripts/c5-chaos.sh`.
- Add load scenarios.
- Test every crash window.
- Test month boundary.
- Test leap day under ACT/365F.
- Test scheduler lock loss.
- Test provider/Ledger outages.
- Test DB restart.
- Test correction.
- Run clean-tree gate.
- Record residual risks.
- Update roadmap.
- Archive only after evidence.

### Acceptance

- [ ] First monthly close passes.
- [ ] Carry properties pass.
- [ ] No duplicate capitalization.
- [ ] Schedule policies pass.
- [ ] No duplicate occurrence posting.
- [ ] Fee-cap protection passes.
- [ ] Fee-bearing top-up passes.
- [ ] Failed top-up posts no fee.
- [ ] Principal+fee posting is atomic.
- [ ] Existing IDR journeys remain green.
- [ ] Chaos matrix passes.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are explicit.

---

## 37. Recommended pull-request sequence

```text
PR 1  — C5 entry evidence, architecture, contracts, threat model
PR 2  — Exact interest math, product/rate/enrollment schema
PR 3  — Daily accrual rows, carry, and liability posting
PR 4  — Period close, capitalization, reconciliation, correction
PR 5  — Legacy interest shadow/cutover and first-close evidence
PR 6  — Schedule policy fields, occurrence and attempt schema
PR 7  — Schedule planner and missed-run policies
PR 8  — Schedule evaluator, fee cap, dispatcher, retry and pause
PR 9  — Legacy schedule migration and old-runner cutover
PR 10 — Payin top-up fee fields and fee-quote contract
PR 11 — Quote consumption, Vendor total debit, Ledger principal+fee posting
PR 12 — Admin BFF, events, Assurance, notification, privacy
PR 13 — Observability, runbooks, chaos, load, final evidence
```

Split further when a migration or owner-service change is large.

Do not combine interest close, schedule engine migration, and top-up fee
activation in one PR.

---

## 38. Dependency graph

```text
T0 Entry gate
  |
  v
T1 Contracts/accounting/threat model
  |
  |----------------------------|----------------------------|
  v                            v                            v
T2 Interest foundation      T6 Schedule foundation       T9 Top-up fee contract
  |                            |                            |
  v                            v                            v
T3 Daily accrual            T7 Evaluator/dispatcher      T10 Fee settlement
  |                            |                            |
  v                            v                            |
T4 Period close             T8 Migration/cutover           |
  |                                                         |
  v                                                         |
T5 Interest migration                                      |
  |----------------------------|----------------------------|
                               v
                       T11 Admin controls
                               |
                               v
                  T12 Observability/retention
                               |
                               v
                       T13 Final evidence
```

The three product tracks may proceed in parallel after T1, but each has an
independent activation flag and acceptance gate.

---

## 39. Recommended implementation cuts

## 39.1 First cut — schedule failure policy

This is the lowest-risk operational slice because it can run in shadow mode.

```text
existing schedule definition
-> durable occurrence
-> shadow planner
-> execution history
-> no new posting path yet
```

Then:

```text
current policy evaluator
-> fee cap
-> dispatcher
-> old/new result comparison
-> controlled cutover
```

### 39.2 Second cut — top-up fee

Start with a synthetic zero-fee quote carried end-to-end.

```text
quote ID
-> Payin fee snapshot
-> total debit = amount
-> Vendor
-> callback
-> Ledger quote-linked posting
```

Then enable a small non-zero synthetic rule.

### 39.3 Third cut — interest shadow mode

```text
closing snapshot
-> exact daily accrual row
-> carry
-> no liability posting
-> compare with legacy daily result
```

### 39.4 Fourth cut — monthly capitalization

```text
daily liability
-> first complete month
-> close preview
-> per-account capitalization
-> reconciliation
```

This sequencing avoids enabling the most accounting-sensitive change before
math and period evidence exist.

---

## 40. Test strategy

## 40.1 Unit tests

Interest:

```text
ACT/365F
positive/zero/negative balance
fractional carry
rate change
month boundary
leap day
enrollment start/end
carry finalization
period readiness
capitalization amount
correction direction
```

Schedule:

```text
local-time occurrence
timezone
skip
run-once-latest
catch-up-bounded
catch-up truncation
command digest
policy evaluation
fee cap
failure classification
retry backoff
pause threshold
state transitions
```

Top-up:

```text
fee quote equality
added-on-top calculation
checked addition
quote consumption
amount/fee/total invariants
callback total match
posting command
full reversal
```

### 40.2 Property and fuzz tests

Interest properties:

- carry remains within `[0, denominator)`;
- recognized cumulative amount plus carry equals exact cumulative numerator;
- retry does not change result;
- period sum equals daily recognized sum;
- capitalization never exceeds liability;
- closed period is immutable.

Schedule properties:

- one schedule/date creates one occurrence;
- occurrence posts at most once;
- catch-up count never exceeds limit;
- skip never posts;
- successful state is terminal;
- changed fee above cap never posts.

Top-up properties:

- total debit equals amount plus fee;
- fee is non-negative;
- callback mismatch never posts;
- one Payin produces at most one principal-and-fee transaction;
- entries balance.

Fuzz:

```text
rate/balance
period date
timezone
schedule payload
fee cap
amount string
quote payload
callback payload
```

### 40.3 PostgreSQL integration tests

```text
rate overlap
maker/checker
daily accrual uniqueness
carry row locking
period uniqueness
capitalization item uniqueness
period close concurrency
closed-period guard
correction linkage
occurrence uniqueness
lease recovery
attempt numbering
schedule version update
quote consumption
Payin fee checks
Ledger three-entry posting
migration/backfill
```

### 40.4 Contract tests

Every additive operation receives:

- valid fixture;
- validation failure;
- ownership failure;
- compatibility fixture;
- idempotency fixture;
- stale-version fixture;
- stable error mapping.

### 40.5 End-to-end journeys

#### Interest A — Daily accrual

```text
snapshot
-> accrual row
-> liability posting
-> carry update
```

#### Interest B — Month close

```text
complete month
-> readiness
-> capitalization
-> closed period
-> statement/event
```

#### Interest C — Correction

```text
closed period issue
-> maker adjustment
-> checker approval
-> explicit posting
```

#### Schedule A — Successful daily

```text
planner
-> occurrence
-> evaluate
-> post
-> history
```

#### Schedule B — Missed skip

```text
worker down
-> restart
-> missed occurrence marked skipped
-> no money
```

#### Schedule C — Catch-up bounded

```text
several missed days
-> at most configured limit posts
-> older backlog visible
```

#### Schedule D — Fee cap

```text
fee rule increases
-> occurrence blocked
-> schedule paused
-> user confirms new cap
-> future occurrence succeeds
```

#### Top-up A — Fee-bearing success

```text
quote
-> Payin
-> provider total debit
-> callback
-> principal+fee posting
```

#### Top-up B — Provider failure

```text
consumed quote
-> provider failure
-> no Ledger fee
```

#### Top-up C — Lost Ledger response

```text
provider success
-> Ledger commits
-> response lost
-> retry returns existing transaction
```

---

## 41. Chaos matrix

## 41.1 Daily accrual crash

Injection:

```text
after row claim
after snapshot read
after calculation persist
after liability posting commit
before row completion update
```

Expected:

- deterministic recovery;
- one liability posting;
- one carry result.

### 41.2 Period close crash

Injection:

```text
after close state
after item generation
after some account capitalizations
after final item
before period closed update
```

Expected:

- posted items recognized;
- unposted items retried;
- no duplicate capitalization;
- close completes only after reconciliation.

### 41.3 Missing last-day snapshot

Expected:

- period remains open/failed;
- no silent close;
- alert;
- recovery/backfill;
- close succeeds later.

### 41.4 Rate missing mid-month

Expected:

- affected daily rows blocked;
- close blocked;
- approved correction/rate resolution required;
- no zero-rate assumption.

### 41.5 Scheduler planner outage

Expected:

- durable lookback finds missed occurrences;
- configured missed policy applies;
- no unbounded backlog.

### 41.6 Dispatcher crash after Ledger commit

Expected:

- retry gets `AlreadyPosted`;
- occurrence becomes succeeded;
- no duplicate money.

### 41.7 Redis scheduler lock outage

Expected:

- invocation may skip;
- durable due rows remain;
- next run recovers;
- alert fires.

### 41.8 Policy/fee change before occurrence

Expected:

- current policy/fee applies;
- cap protection works;
- no silent new fee.

### 41.9 Payin crash after quote consumption

Expected:

- same Payin reference retrieves same quote;
- intent recovers;
- no second quote use.

### 41.10 Vendor timeout after collection acceptance

Expected:

- current Vendor/Payin recovery;
- no second fee quote;
- no second provider collection;
- eventual callback posts once.

### 41.11 Ledger crash during three-entry posting

Expected:

- no partial principal or fee;
- retry posts once.

### 41.12 Duplicate callback

Expected:

- one Payin terminal state;
- one Ledger transaction;
- one fee revenue posting.

### 41.13 Full reversal crash

Expected:

- compensating transaction is idempotent;
- principal and fee reverse together.

---

## 42. Performance and resource boundaries

C5 does not make production capacity claims.

Engineering boundaries:

```text
bounded accrual batch
bounded close batch
bounded schedule occurrence batch
bounded retry
bounded catch-up
SKIP LOCKED leasing
no network in Ledger transaction
no whole-period giant transaction
no full-table schedule scan without index
no user/account metric label
no raw payload logging
no float money/interest calculation
keyset pagination for histories
bounded retention delete
```

Initial local targets to measure:

```text
daily accrual processing:           documented accounts/second
capitalization item p95:            <= 250 ms local
schedule due-to-post p95:           <= 2 minutes
planner recovery lookback:          <= 5 minutes local dataset
fee quote p95:                      current baseline + <= 10%
top-up settlement p95 regression:   <= 10%
existing Ledger IDR p95 regression: <= 5%
```

Targets are adjusted from B0 evidence.

---

## 43. Load scenarios

Add:

```text
daily accrual for 1k/10k synthetic enrollments
period close for 1k/10k accounts
concurrent capitalization workers
schedule occurrence burst
schedule missed-backlog planning
schedule fee-cap blocks
fee quote burst
fee-bearing top-up callback burst
```

Measure:

```text
DB locks
worker throughput
oldest due age
connection pool
statement timeout
outbox lag
RabbitMQ lag
Ledger posting latency
Payin callback latency
```

---

## 44. Rollout stages

### Stage 0 — Schema and code disabled

- additive schema;
- existing behavior unchanged.

### Stage 1 — Schedule shadow planner

- occurrence rows;
- no new dispatcher;
- compare expected runs.

### Stage 2 — Schedule controlled cutover

- low-risk schedules;
- zero-fee routes;
- old runner disabled after evidence.

### Stage 3 — Top-up fee plumbing with zero fee

- quote and fee fields;
- total debit equals amount;
- no user economic change.

### Stage 4 — Non-zero synthetic top-up fee

- one mock gateway/currency;
- small rule;
- explicit quote;
- full E2E and reversal evidence.

### Stage 5 — Interest shadow mode

- product/rate/enrollment;
- exact accrual rows;
- legacy posting remains authoritative.

### Stage 6 — Daily liability activation

- clean month boundary;
- selected synthetic cohort;
- close disabled until full period.

### Stage 7 — First monthly close

- preview;
- maker/checker readiness;
- small cohort;
- full reconciliation.

### Stage 8 — Broader local activation

Only after first close and chaos evidence pass.

---

## 45. Kill switches

Required:

```text
savings enrollment intake
daily interest accrual
interest liability posting
period close
interest capitalization
schedule occurrence planning
schedule occurrence dispatch
schedule catch-up
top-up fee quote requirement
top-up fee-bearing route
```

Kill-switch rules:

- owner-side;
- audited;
- current state visible;
- distinguish intake from in-flight completion;
- do not delete financial evidence;
- do not directly change balances.

---

## 46. Rollback

## 46.1 Interest rollback

Before liability posting:

- disable shadow/new mode;
- legacy remains.

After liability posting:

- pause new accrual;
- do not resume daily legacy capitalization mid-period;
- complete/correct through period workflow;
- preserve liability evidence.

After period close:

- no reopen;
- correction only.

### 46.2 Schedule rollback

- pause new planner;
- stop dispatcher;
- preserve occurrences;
- avoid running old and new dispatchers together;
- use idempotency before any legacy fallback;
- do not delete history.

### 46.3 Top-up fee rollback

- disable non-zero fee rules;
- stop requiring quote for new fee-free flow if contract allows;
- existing fee-bearing intents retain their consumed quote;
- in-flight provider collections settle using original fee;
- do not reprice or drop fee;
- preserve reversal capability.

### 46.4 Schema rollback

Do not drop financial evidence tables/columns during operational rollback.

Contraction requires later expand/contract evidence.

---

## 47. Documentation deliverables

Add or update:

```text
docs/roadmap/active/61-c5-advanced-financial-products-period-close.md
docs/roadmap/README.md
docs/roadmap/42-long-term-roadmap.md

docs/reference/savings-products.md
docs/reference/interest-accrual.md
docs/reference/interest-period-close.md
docs/reference/interest-corrections.md
docs/reference/scheduled-transactions.md
docs/reference/schedule-failure-policy.md
docs/reference/fee-quotes.md
docs/reference/topup-fees.md
docs/reference/payin.md
docs/reference/ledger.md
docs/reference/events.md
docs/reference/current-services.md

docs/architecture/financial-product-period-close.md
docs/security/threat-model.md

docs/evidence/c5-entry-gate.md
docs/evidence/c5-schedule-cutover.md
docs/evidence/c5-topup-fee.md
docs/evidence/c5-first-period-close.md
docs/evidence/c5-final-acceptance.md

docs/runbooks/interest-*.md
docs/runbooks/schedule-*.md
docs/runbooks/topup-fee-*.md
```

---

## 48. Proposed repository changes

Expected areas:

```text
services/ledger/internal/ledger/accrual/
services/ledger/internal/ledger/interest/
services/ledger/internal/ledger/schedule/
services/ledger/internal/ledger/fee/
services/ledger/internal/processors/
services/ledger/internal/repository/
services/ledger/internal/ledger/model/

services/payin/internal/
services/vendor-service/internal/
contracts/vendorgw/
services/assurance/internal/
services/fraud/internal/
services/gateway/internal/notification/
services/adminbff/internal/
services/gateway/internal/transport/http/

services/ledger/migrations/
services/payin/migrations/

contracts/http/
contracts/compatibility/
contracts/events/
contracts/proto/seev/
gen/

internal/scheduler/
cmd/ledger/
services/gateway/cmd/gateway/

scripts/interest-period-e2e.sh
scripts/schedule-policy-e2e.sh
scripts/topup-fee-e2e.sh
scripts/c5-chaos.sh
tests/load/
deploy/observability/
Makefile
docs/
```

T0 narrows the actual blast radius.

---

## 49. Make targets

Recommended:

```text
make interest-contract-check
make interest-math-test
make interest-shadow-compare
make interest-period-e2e
make interest-period-reconcile

make schedule-contract-check
make schedule-shadow-compare
make schedule-policy-e2e

make topup-fee-contract-check
make topup-fee-e2e
make topup-fee-reconcile

make c5-config-check
make c5-chaos
make c5-verify
```

Policy:

- static contract/math/config checks join `make verify-full`;
- repeatable local E2E may join the full gate;
- destructive failure injection remains separate;
- no paid provider or internet dependency is required.

---

## 50. Final verification commands

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

make interest-contract-check
make interest-math-test
make interest-period-e2e
make interest-period-reconcile

make schedule-contract-check
make schedule-policy-e2e

make topup-fee-contract-check
make topup-fee-e2e
make topup-fee-reconcile

./scripts/smoke-test.sh
./scripts/business-e2e.sh
./scripts/admin-e2e.sh

make verify-full
git diff --check
git status --short
```

Separate chaos:

```bash
make c5-chaos
make verify-chaos
```

---

## 51. Final definition of done

C5 is complete only when all required items below pass.

### Architecture

- [ ] No new application service exists.
- [ ] Ledger remains money owner.
- [ ] Payin remains top-up lifecycle owner.
- [ ] Scheduler infrastructure only triggers durable product work.
- [ ] No network call occurs inside a financial DB transaction.
- [ ] Existing IDR behavior remains compatible.

### Interest product

- [ ] Product/rate/enrollment model exists.
- [ ] Rate versions are immutable.
- [ ] Maker/checker is enforced.
- [ ] ACT/365F is documented.
- [ ] Daily accrual uses closing snapshot.
- [ ] Fractional carry is exact.
- [ ] Daily liability posting is balanced.
- [ ] Missing accrual is never hidden as zero.
- [ ] Period readiness is deterministic.
- [ ] Monthly capitalization posts once per account.
- [ ] Liability equals capitalization.
- [ ] Closed period cannot reopen.
- [ ] Corrections are explicit.
- [ ] Legacy history is preserved.
- [ ] First cutover month closes and reconciles.

### Schedule policy

- [ ] Definition and occurrence are separate.
- [ ] Occurrences and attempts are durable.
- [ ] Missed policy is explicit.
- [ ] Catch-up is bounded.
- [ ] Current command is revalidated.
- [ ] Current Ledger policy is re-evaluated.
- [ ] Current fee is resolved.
- [ ] Fee cap prevents silent fee increase.
- [ ] Infrastructure retry is bounded.
- [ ] Business failures follow policy.
- [ ] One occurrence posts at most once.
- [ ] Successful occurrence cannot replay.
- [ ] Legacy schedules migrate safely.
- [ ] Old/new dispatcher overlap is impossible.
- [ ] Fraud residual risk is explicit.

### Top-up fees

- [ ] Existing zero-fee top-up remains compatible.
- [ ] Non-zero fee uses existing fee rules/quotes.
- [ ] Amount is wallet credit.
- [ ] Total debit equals amount plus fee.
- [ ] Quote is owned, matched, unexpired, and consumed once.
- [ ] Payin persists immutable fee snapshot.
- [ ] Vendor receives total debit.
- [ ] Callback validates total debit.
- [ ] Ledger posts principal and fee atomically.
- [ ] Failed top-up posts no fee.
- [ ] Duplicate callback posts once.
- [ ] Rule change does not reprice intent.
- [ ] Full reversal reverses principal and fee.
- [ ] Fee revenue reconciles to fee account entries.

### Security and controls

- [ ] Threat model is updated.
- [ ] Maker/checker applies to rates/corrections.
- [ ] Closed period cannot be mutated by operator.
- [ ] Schedule replay is restricted.
- [ ] Fee activation is audited.
- [ ] CSRF and role tests pass.
- [ ] Raw payloads/secrets are absent from logs/audit.
- [ ] Kill switches are exercised.

### Reliability

- [ ] Worker leases recover.
- [ ] Scheduler lock outage recovers.
- [ ] Daily accrual crash windows are safe.
- [ ] Period close crash windows are safe.
- [ ] Schedule commit-before-state crash is safe.
- [ ] Quote-consumption crash windows recover.
- [ ] Provider-success/Ledger-outage recovers.
- [ ] No duplicate money or fee is observed.
- [ ] Retention jobs are bounded.

### Operations

- [ ] Metrics and dashboards exist.
- [ ] Alerts have runbooks.
- [ ] Cardinality is bounded.
- [ ] Period and backlog age are visible.
- [ ] Reconciliation is visible.
- [ ] Privacy export is updated.
- [ ] Backup/restore verifies new product tables.

### Evidence

- [ ] Interest math property tests pass.
- [ ] Schedule policy matrix passes.
- [ ] Top-up fee E2E passes.
- [ ] Chaos matrix passes.
- [ ] Load baseline is recorded.
- [ ] Existing business E2E remains green.
- [ ] Final clean-tree gate passes.
- [ ] Residual risks are explicit.
- [ ] Roadmap and service docs reflect reality.
- [ ] Plan is archived only after evidence links are complete.

---

## 52. Final evidence log

Fill during execution.

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| C5 entry gate |  |  |  |
| Current accrual inventory |  |  |  |
| Policy bypass review |  |  |  |
| Exact carry property tests |  |  |  |
| Savings rate maker/checker |  |  |  |
| Daily liability accrual |  |  |  |
| Accrual crash recovery |  |  |  |
| Shadow comparison |  |  |  |
| First period readiness |  |  |  |
| First monthly capitalization |  |  |  |
| Capitalization crash recovery |  |  |  |
| Period reconciliation |  |  |  |
| Closed-period correction |  |  |  |
| Legacy interest cutover |  |  |  |
| Schedule occurrence planner |  |  |  |
| Missed-run policy matrix |  |  |  |
| Schedule fee-cap block |  |  |  |
| Schedule infra retry |  |  |  |
| Schedule duplicate recovery |  |  |  |
| Legacy schedule cutover |  |  |  |
| Top-up quote contract |  |  |  |
| Quote consumption recovery |  |  |  |
| Fee-bearing top-up success |  |  |  |
| Failed top-up no-fee evidence |  |  |  |
| Duplicate callback |  |  |  |
| Provider success/Ledger outage |  |  |  |
| Full reversal |  |  |  |
| Fee revenue reconciliation |  |  |  |
| C5 load baseline |  |  |  |
| Final clean-tree gate |  |  |  |

---

## 53. Residual risks

A completed local C5 still does not prove:

- legally compliant deposit/savings products;
- customer disclosure requirements;
- tax withholding;
- deposit insurance;
- external accounting certification;
- general-ledger close;
- regulatory period close;
- real provider top-up pricing;
- chargeback economics;
- partial fee refunds;
- holiday/business-day calendars;
- multi-timezone public schedules at production scale;
- synchronous Fraud screening for every scheduled occurrence;
- multi-region scheduler singleton guarantees;
- very large account-period close capacity;
- production support tooling;
- real customer consent law;
- negative correction collection;
- interest rate risk;
- funding/liquidity for interest expense.

These limitations must remain visible in documentation and portfolio claims.

---

## 54. Recommended immediate next action

Start with T0 and T1.

Then implement the schedule occurrence model in shadow mode because it can
produce valuable evidence without changing money behavior:

```text
legacy schedule
-> durable expected occurrence
-> missed policy
-> shadow evaluator
-> execution-history comparison
```

In parallel, implement top-up fee plumbing with a zero-fee quote:

```text
fee quote
-> Payin fee snapshot
-> total debit equals amount
-> Vendor
-> Ledger quote-linked settlement
```

Start interest with calculation-only shadow mode:

```text
snapshot
-> exact accrual
-> fractional carry
-> no new posting
-> compare with legacy daily capitalization
```

Only after those three foundations are green should the plan activate:

```text
new schedule dispatcher
non-zero top-up fee
daily interest liability
monthly capitalization
```

This sequence maximizes evidence while minimizing irreversible financial
behavior changes.
