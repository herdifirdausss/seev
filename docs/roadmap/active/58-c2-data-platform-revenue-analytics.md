# Plan 58 — C2 Data Platform and Revenue Analytics

**Created:** 2026-07-28
**Status:** Implementation in progress; runtime evidence pending
**Roadmap track:** C2 — Data Platform and Revenue Analytics
**Activation trigger:** Conscious CDC/data-platform learning decision
**Depends on:** Stable source contracts, A8 observability foundation, A9 contract governance
**Optional enrichment:** C1 Merchant/B2B API after its merchant model exists
**Primary source owners:** LedgerService, PayinService, PayoutService
**Deferred source owners:** VendorService, Gateway merchant module, FraudService
**Analytics stack:** Debezium PostgreSQL connectors, Kafka Connect, Redpanda, ClickHouse, dbt, Metabase
**No money-movement flow may depend on this platform.**

---

## 1. Purpose

Build a local, production-inspired analytical data platform for Seev that can:

- capture approved PostgreSQL changes through WAL-based CDC;
- preserve source-service ownership and database isolation;
- ingest immutable change events into an analytical store;
- transform raw CDC data into typed, tested warehouse models;
- calculate payment volume, transaction success, recognized fee revenue, and
  modeled unit economics;
- expose business dashboards without loading OLTP databases;
- reconcile analytical facts back to LedgerService's immutable entries;
- detect stale, incomplete, duplicated, or inconsistent analytical data;
- demonstrate recovery from source, connector, broker, warehouse, and BI
  failures;
- remain optional and removable without affecting transactional correctness.

C2 is an analytical projection. It is not a new source of truth.

The implementation must preserve these rules:

1. LedgerService remains the source of truth for money.
2. Service databases remain independently owned.
3. No application service reads another service's database.
4. The analytics platform is read-only with respect to application databases.
5. CDC is at-least-once; downstream models must tolerate duplicate delivery.
6. Data freshness is never equivalent to transactional correctness.
7. Dashboards may be stale without blocking payment execution.
8. Exact money remains integer minor units.
9. Revenue must be derived from recognized ledger postings, not merely from fee
   quotes or transaction amount.
10. Sensitive fields must be excluded or pseudonymized before they enter the
    analytical platform.
11. Existing regulatory/compliance reporting views remain where they are until
    separate evidence authorizes migration.
12. RabbitMQ remains the domain-event and operational messaging boundary.
13. Redpanda is used only as the CDC log in C2.
14. No additional application/business service is authorized by this plan.

---

## 2. Activation and entry gate

### 2.1 Activation decision

C2 is activated on 2026-07-28 as a conscious learning decision for:

- PostgreSQL logical replication;
- WAL-based CDC;
- Kafka-compatible ingestion;
- OLAP modeling;
- data-quality engineering;
- ledger-backed revenue analytics;
- reconciliation;
- failure recovery.

This activation does not claim that current OLTP analytics are already causing
production performance problems.

### 2.2 Required entry-gate evidence

C2 implementation may begin only after T0 records the current result of all
items below.

- [ ] `make contracts` passes from a clean tree.
- [ ] Current generated event and Protobuf artifacts are clean.
- [ ] Current source-service database migrations pass.
- [ ] Existing Ledger, Payin, and Payout integration tests pass.
- [ ] Existing business, callback, and smoke journeys remain green.
- [ ] Current database names, migration heads, schemas, and table ownership are
      recorded.
- [ ] Current PostgreSQL image/version and logical-decoding support are
      confirmed.
- [ ] Source tables and columns are classified before publication.
- [ ] Every selected source table has an explicit business owner.
- [ ] Every selected source column has an explicit analytical purpose.
- [ ] Sensitive columns are excluded or pseudonymized.
- [ ] The exact baseline commit is recorded.
- [ ] Required local machine resources are measured.
- [ ] The initial analytics profile can run independently from the full
      observability profile.

### 2.3 Gate policy

The following may start before the gate is fully green:

- architecture documentation;
- warehouse naming conventions;
- source-column inventory;
- privacy classification;
- Compose scaffolding;
- ClickHouse and dbt proof of concept with synthetic fixtures;
- dashboard wireframes.

The following may not merge before the gate is green:

- logical replication slot creation;
- source publication creation;
- a connector containing unreviewed table or column patterns;
- ingestion of raw payload, destination, identity, token, or credential fields;
- a dashboard presented as authoritative financial reporting;
- a source migration added solely for an undocumented analytical join.

---

## 3. Locked architecture decisions

## 3.1 One-way analytical boundary

The only permitted dependency direction is:

```text
OLTP PostgreSQL databases
        |
        | PostgreSQL logical WAL
        v
Debezium PostgreSQL connectors
        |
        | Kafka Connect records
        v
Redpanda CDC topics
        |
        | ClickHouse Kafka Engine
        v
ClickHouse raw CDC layer
        |
        | dbt transformations and tests
        v
staging -> core -> mart -> control
        |
        v
Metabase business dashboards
```

No arrow may point back into an OLTP database.

### 3.2 Product path independence

This platform may fail completely while the following continue:

- authentication;
- transfer posting;
- balance reads from LedgerService;
- pay-in creation and callback processing;
- payout creation and callback processing;
- transactional outbox publication;
- merchant API requests;
- operational notifications.

No money-flow code may:

- call ClickHouse;
- call Metabase;
- call Redpanda;
- wait for Debezium;
- inspect a warehouse reconciliation result before committing;
- use a mart as an authorization or balance source.

### 3.3 Redpanda is not a RabbitMQ replacement

C2 introduces Redpanda because Debezium and Kafka Connect use Kafka-compatible
interfaces and because an appendable replayable log is useful for CDC.

RabbitMQ remains responsible for:

- domain-event delivery;
- transactional workflow integration;
- service consumers;
- operational notifications;
- existing retry and dead-letter behavior.

C2 must not migrate current RabbitMQ traffic.

### 3.4 No analytics application service

C2 introduces infrastructure and transformation code, not a tenth application
service.

Permitted runtime components:

```text
Redpanda
Kafka Connect with Debezium PostgreSQL plugin
ClickHouse
dbt runner
Metabase
optional connector-init job
optional warehouse-init job
optional reconciliation runner
```

These are analytical infrastructure components, not business-service owners.

### 3.5 Initial source scope

Phase 1 sources:

```text
LedgerService PostgreSQL
PayinService PostgreSQL
PayoutService PostgreSQL
```

Phase 2 sources, only after specific gates:

```text
VendorService PostgreSQL
Gateway merchant tables after C1 exists
FraudService decision facts
```

Explicitly excluded from the first implementation:

```text
AuthService identity tables
KYC document metadata
sessions and tokens
Admin BFF browser/session data
raw vendor callback bodies
raw payout destinations
raw audit details
```

### 3.6 Warehouse choice

ClickHouse is selected because the C2 workload is analytical:

- append-heavy CDC ingestion;
- exact aggregates over many financial rows;
- column-oriented scans;
- partitioned historical facts;
- materialized transformations;
- dashboard query concurrency.

PostgreSQL remains the operational source of truth.

### 3.7 Transformation choice

dbt is selected as the transformation and quality layer.

Responsibilities:

- source declarations;
- typed staging models;
- incremental transformations;
- conformed dimensions;
- fact tables;
- marts;
- reusable macros;
- schema tests;
- custom financial tests;
- documentation lineage.

dbt is not responsible for CDC transport.

### 3.8 BI choice

Metabase is selected for local learning and portfolio demonstration.

Metabase receives a read-only ClickHouse credential and may query only approved
schemas/views.

Metabase may not connect directly to service PostgreSQL databases in C2.

### 3.9 At-least-once semantics

CDC ingestion is at-least-once.

Therefore:

- duplicate events are expected;
- broker offsets are transport identity;
- source primary key plus source ordering metadata determine row state;
- warehouse models must not rely solely on eventual background merge;
- reconciliation must detect missing and duplicated logical rows;
- source table updates must be modeled deliberately;
- deletes must remain observable.

### 3.10 Existing reporting authority

Current LedgerService reporting and regulatory/compliance views remain
authoritative for their existing purpose.

C2 may reproduce comparable metrics for validation, but it may not silently
replace:

- regulatory views;
- operational balance endpoints;
- settlement evidence;
- account statements;
- compliance exports.

Any future migration of these responsibilities requires a separate plan and
evidence.

---

## 4. Product and learning scope

C2 delivers five capabilities.

### 4.1 CDC platform

- publication and replication-slot lifecycle;
- Debezium connector configuration;
- approved source-table and source-column capture;
- schema-change handling;
- snapshot and streaming;
- offset recovery;
- topic retention;
- lag and WAL-retention monitoring.

### 4.2 Analytical warehouse

- append-only raw CDC;
- typed current-state staging models;
- historical and lifecycle facts;
- conformed dimensions;
- exact money modeling;
- incremental rebuilds;
- deterministic deduplication;
- explicit deletion handling.

### 4.3 Revenue analytics

- gross processed volume;
- successful transaction count;
- recognized fee revenue;
- fee quote conversion;
- provider-level success rates;
- payout and pay-in lifecycle duration;
- modeled variable vendor cost;
- modeled contribution margin;
- freshness and confidence annotations.

### 4.4 Reconciliation and quality

- CDC completeness;
- source-to-warehouse row and checksum comparison;
- debit-credit invariants;
- transaction-entry consistency;
- recognized-fee reconciliation;
- pay-in/payout-to-ledger reconciliation;
- mart-to-dashboard consistency;
- stale-data detection.

### 4.5 Business dashboards

- executive volume and revenue;
- pay-in performance;
- payout performance;
- fee and quote conversion;
- modeled unit economics;
- warehouse health and reconciliation.

---

## 5. Explicit non-goals

C2 does not include:

- changing LedgerService's source-of-truth model;
- replacing PostgreSQL with ClickHouse;
- using ClickHouse for balance or authorization reads;
- moving existing regulatory reporting;
- a cloud-managed warehouse;
- cloud Kafka, Confluent Cloud, BigQuery, Snowflake, or Databricks;
- real merchant billing;
- vendor invoice ingestion;
- production profitability claims;
- machine learning;
- real-time fraud decisions;
- reverse ETL;
- writing analytical decisions back to operational services;
- exposing raw CDC topics publicly;
- exposing raw ClickHouse tables to Metabase users;
- capturing all tables by wildcard;
- capturing all columns by wildcard;
- copying raw JSON payloads “just in case”;
- retaining authentication, KYC, token, or destination secrets;
- arbitrary PII search;
- customer-level behavioral profiling;
- cross-service fuzzy joins by amount and timestamp;
- automatic schema evolution that silently changes business meaning;
- exactly-once transport claims;
- a generic enterprise data catalog;
- production data governance certification;
- replacing RabbitMQ;
- adding a custom stream-processing framework;
- adding Flink or Spark in the initial implementation;
- a Kubernetes deployment;
- multi-region or disaster-recovery claims;
- C1 dependency for the first Ledger/Payin/Payout slice.

---

## 6. Repository layout

Add an isolated analytics workspace:

```text
analytics/
├── README.md
├── compose/
│   ├── docker-compose.analytics.yml
│   └── profiles.md
├── connect/
│   ├── Dockerfile
│   ├── plugins/
│   ├── connectors/
│   │   ├── ledger-postgres.json
│   │   ├── payin-postgres.json
│   │   └── payout-postgres.json
│   └── scripts/
│       ├── apply-connectors.sh
│       ├── validate-connectors.sh
│       ├── pause-connectors.sh
│       ├── resume-connectors.sh
│       └── delete-connectors.sh
├── redpanda/
│   ├── topic-specs.yaml
│   └── scripts/
├── clickhouse/
│   ├── config/
│   ├── users/
│   ├── migrations/
│   │   ├── raw/
│   │   ├── staging/
│   │   ├── core/
│   │   ├── mart/
│   │   └── control/
│   └── scripts/
├── dbt/
│   ├── dbt_project.yml
│   ├── packages.yml
│   ├── profiles.example.yml
│   ├── macros/
│   ├── models/
│   │   ├── sources/
│   │   ├── staging/
│   │   ├── core/
│   │   ├── marts/
│   │   └── control/
│   ├── seeds/
│   ├── snapshots/
│   └── tests/
├── metabase/
│   ├── collections/
│   ├── dashboards/
│   └── setup/
├── reconciliation/
│   ├── cmd/
│   ├── internal/
│   ├── queries/
│   └── fixtures/
├── contracts/
│   ├── sources.yaml
│   ├── privacy.yaml
│   ├── topics.yaml
│   ├── models.yaml
│   └── metrics.yaml
├── fixtures/
│   ├── cdc/
│   ├── warehouse/
│   └── reconciliation/
└── scripts/
    ├── e2e.sh
    ├── chaos.sh
    ├── backfill.sh
    ├── reset-warehouse.sh
    └── verify-no-sensitive-columns.sh
```

Repository-level additions:

```text
docs/roadmap/active/58-c2-data-platform-revenue-analytics.md
docs/architecture/data-platform.md
docs/reference/analytics-metrics.md
docs/reference/analytics-data-contracts.md
docs/threat-models/data-platform.md
docs/runbooks/
Makefile
docker-compose.yml or compose include wiring
```

---

## 7. Infrastructure profiles

C2 must be usable on constrained local hardware.

### 7.1 `analytics-core`

Contains:

```text
Redpanda
Kafka Connect + Debezium
ClickHouse
connector-init job
warehouse-init job
dbt runner
```

### 7.2 `analytics-ui`

Contains:

```text
Metabase
```

### 7.3 `analytics-ops`

Optional:

```text
Redpanda Console
ClickHouse client/admin helper
connector diagnostics helper
```

### 7.4 Profile rules

- analytics does not start by default;
- the full app does not automatically start Metabase;
- observability and analytics can be started separately;
- a documented low-memory mode exists;
- component memory and CPU limits are explicit;
- all host ports bind to localhost by default;
- CI may run headless without Metabase;
- connector, warehouse, and dbt tests do not require the UI profile;
- a disposable reset path exists.

### 7.5 Initial resource budget

T0 must measure actual consumption.

Initial local guardrails:

```text
Redpanda:       bounded single-node developer configuration
Kafka Connect:  one worker, bounded heap
ClickHouse:     bounded memory and background-thread settings
dbt:            one or two threads
Metabase:       optional, bounded JVM heap
```

No exact capacity claim is accepted until measured.

---

## 8. Source data contract

## 8.1 Contract manifest

Create:

```text
analytics/contracts/sources.yaml
```

Each captured table must declare:

```yaml
service: ledger
database: seev_ledger
schema: public
table: ledger_entries
owner: LedgerService
primary_key:
  - id
capture_mode: full_row
classification: financial
retention_class: financial_analytics
columns:
  - name: id
    action: include
    purpose: immutable entry identity
  - name: transaction_id
    action: include
    purpose: transaction relationship
  - name: account_id
    action: include
    purpose: debit and credit reconciliation
  - name: amount
    action: include
    purpose: exact money fact
  - name: created_at
    action: include
    purpose: event and report time
```

Every selected column needs:

- owner;
- analytical purpose;
- classification;
- include, exclude, or pseudonymize action;
- retention;
- join behavior;
- expected type;
- nullability;
- transformation notes.

### 8.2 Allowlist-only capture

Debezium connectors must use explicit:

- schema include list;
- table include list;
- column include/exclude rules.

A connector may not capture `public.*`.

### 8.3 Initial Ledger source tables

T0 must confirm exact names and columns. Expected sources:

```text
accounts
account_balances
ledger_transactions
ledger_entries
fee_quotes
```

Rules:

- `ledger_entries` is the financial fact source;
- `account_balances` is a projection and reconciliation aid;
- `ledger_transactions` supplies business headers/status;
- `accounts` supplies owner and account classification;
- `fee_quotes` supplies intent/conversion analysis, not recognized revenue.

Potentially useful later:

```text
balance_snapshots
posting rule/config tables
currency or transaction-type reference tables
```

These require explicit review before capture.

### 8.4 Initial Payin source tables

Expected:

```text
payin intents/requests
payin lifecycle/status table
payin provider attempt table where present
payin webhook event metadata where safe
payin outbox event metadata where useful
```

Exclude:

```text
raw callback body
raw vendor request/response
tokens
credentials
payer PII
unbounded JSON
```

### 8.5 Initial Payout source tables

Expected:

```text
payout_requests
payout lifecycle/status history where present
payout provider attempt table where present
payout outbox event metadata where useful
```

Exclude:

```text
destination JSON
bank account number
recipient name
raw vendor request/response
credentials
callback payload
```

### 8.6 Deferred source tables

VendorService may be added only after:

- Plan 54 acceptance is complete;
- safe callback/provider-attempt metadata is separated from raw payloads;
- deterministic links to Payin/Payout are documented;
- no credential or raw callback field enters the connector.

Gateway merchant tables may be added only after C1 exists and its privacy model
is complete.

### 8.7 Pseudonymization

Identifiers needed for cross-service analysis but not for business display must
be pseudonymized consistently.

Examples:

```text
user_id
customer_id
merchant_internal_id where public ID is not required
```

Use deterministic salted hashing at the connector boundary.

Requirements:

- same source identifier produces the same pseudonym across approved sources;
- salt is loaded from a secret file;
- salt is not stored in Git;
- salt rotation requires a documented rebuild;
- public dashboards do not expose raw internal identity;
- account and transaction IDs may remain exact where required for financial
  reconciliation, but are not displayed in broad business dashboards.

### 8.8 Prohibited fields

The source-contract validator must reject known patterns such as:

```text
password
password_hash
token
secret
authorization
credential
private_key
access_key
refresh_token
session
cookie
raw_payload
raw_request
raw_response
destination
account_number
document
kyc
```

Pattern checks supplement, but do not replace, manual review.

---

## 9. Correlation and join contract

Analytics may not infer financial relationships using only:

- equal amount;
- close timestamp;
- same user;
- same vendor;
- same status.

### 9.1 Required deterministic identifiers

T0 must inventory:

```text
request_id
transaction_id
ledger_transaction_id
external_reference
provider_reference
payin_id
payout_id
hold_transaction_id
settlement_transaction_id
reversal_transaction_id
fee_quote_id
consumed_by_reference
logical event_id
```

### 9.2 Correlation matrix

Create:

```text
analytics/contracts/correlation-matrix.md
```

Example:

| Business journey | Source record | Target record | Deterministic key | Owner |
|---|---|---|---|---|
| Payout hold | payout request | ledger transaction | hold_transaction_id | Payout |
| Payout settlement | payout request | ledger transaction | settlement_transaction_id | Payout |
| Pay-in credit | pay-in | ledger transaction | ledger_transaction_id | Payin |
| Fee recognition | transaction | fee account entries | transaction_id + fee account | Ledger |
| Fee quote conversion | fee quote | consuming operation | consumed_by_reference | Ledger |

### 9.3 Missing-correlation policy

When a required deterministic link does not exist:

1. document the missing link;
2. identify the source owner;
3. add an additive source-service migration;
4. populate the field through the owner service;
5. include it in the owner contract/event where needed;
6. backfill only with deterministic evidence;
7. leave unverifiable old rows explicitly unlinked.

Do not add a cross-database foreign key.

Do not let the analytics platform write the correlation into the source.

---

## 10. PostgreSQL logical-replication design

## 10.1 Source prerequisites

For each source database:

- logical WAL must be enabled;
- a dedicated replication user must exist;
- a publication must be explicit;
- a replication slot must have a stable name;
- connection limits must be bounded;
- WAL retention must be monitored;
- database restart behavior must be tested.

### 10.2 Dedicated users

Recommended naming:

```text
seev_analytics_ledger
seev_analytics_payin
seev_analytics_payout
```

The user receives only:

- LOGIN;
- REPLICATION where required;
- CONNECT to the source database;
- USAGE on approved schemas;
- SELECT on approved tables;
- access needed for the logical decoding plugin.

It receives no:

- INSERT;
- UPDATE;
- DELETE;
- TRUNCATE;
- schema creation;
- table ownership;
- application role;
- superuser privileges.

### 10.3 Publications

Recommended names:

```text
seev_analytics_ledger_pub
seev_analytics_payin_pub
seev_analytics_payout_pub
```

Publications are manually defined from the reviewed allowlist.

No `FOR ALL TABLES`.

### 10.4 Replication slots

Recommended names:

```text
seev_analytics_ledger_slot
seev_analytics_payin_slot
seev_analytics_payout_slot
```

One active connector owns one slot.

Slot lifecycle must document:

- create;
- inspect;
- pause;
- resume;
- connector recreation;
- intentional drop;
- source reset;
- stale-slot recovery.

### 10.5 WAL safety

Monitor at least:

```text
slot active state
restart LSN
confirmed flush LSN
retained WAL bytes
source disk usage
connector lag
time since last consumed event
```

Required safety control:

- alert before retained WAL threatens source-database disk;
- a runbook for pausing/dropping a disposable local connector;
- no automatic slot drop during an ordinary application shutdown;
- source health remains more important than preserving a disposable local
  analytical backlog.

### 10.6 Replica identity

For tables whose updates or deletes require full previous state:

- prefer a stable primary key;
- explicitly assess `REPLICA IDENTITY`;
- do not enable `FULL` across all tables by default;
- measure WAL amplification before broadening old-row capture.

---

## 11. Debezium and Kafka Connect design

## 11.1 Connector mode

Use Debezium PostgreSQL connector with:

```text
plugin.name=pgoutput
snapshot.mode=initial
```

T0 must pin compatible versions of:

- PostgreSQL image;
- Debezium connector;
- Kafka Connect image;
- Redpanda;
- ClickHouse;
- dbt-clickhouse;
- Metabase.

Version pins must be explicit and reproducible.

### 11.2 Connector identity

Recommended names:

```text
seev-ledger-postgres-cdc
seev-payin-postgres-cdc
seev-payout-postgres-cdc
```

### 11.3 Topic naming

```text
seev.cdc.<service>.<schema>.<table>.v1
```

Examples:

```text
seev.cdc.ledger.public.ledger_entries.v1
seev.cdc.payin.public.payin_requests.v1
seev.cdc.payout.public.payout_requests.v1
```

Schema-history and Connect internal topics use separate reserved prefixes.

### 11.4 Partitioning

Initial table topics use one partition.

Reason:

- preserve source-table event order during learning;
- make offset reasoning and replay simpler;
- reduce accidental out-of-order current-state projection.

A partition-count increase requires:

- keyed ordering design;
- measured throughput need;
- model tests for cross-partition ordering;
- updated reconciliation.

### 11.5 Topic retention

Initial local defaults:

```text
CDC data topics:             7 days
Connect offsets/config:      compacted
schema history:              compacted
dead diagnostics topic:      14 days if introduced
```

Retention is configurable and documented.

### 11.6 Event flattening

Use an approved transformation strategy that retains:

```text
operation
source table
source schema
source LSN
source commit/event time
connector event time
deleted marker
before/after data where explicitly required
```

Delete handling must preserve a logical deletion marker rather than silently
discard the row.

### 11.7 Transaction metadata

Enable and preserve transaction metadata where supported and useful.

The analytical platform may use transaction boundaries for:

- diagnostics;
- ordering;
- completeness checks.

It must not claim cross-database atomicity.

### 11.8 Heartbeats

Enable heartbeats to:

- advance offsets during low traffic;
- expose connector liveness;
- reduce ambiguity between “idle” and “stalled”;
- support lag monitoring.

Heartbeat topics are operational, not warehouse facts.

### 11.9 Schema change policy

Allowed initial changes:

```text
add nullable column
add column with compatible default after source migration review
widen compatible numeric/string type
```

Blocking changes:

```text
drop captured column
rename captured column without compatibility period
change semantic meaning
narrow numeric type
change money unit
change timestamp meaning
change primary key
change enum meaning
```

Connector validation and dbt source tests must fail visibly on an incompatible
change.

### 11.10 Connector deployment

Connector configuration is declarative in Git.

The apply script must:

- validate JSON;
- substitute secret references safely;
- create or update the connector;
- wait for connector and task state;
- fail if a task is failed;
- print no credentials;
- produce an evidence artifact.

No connector is configured manually only through a browser.

---

## 12. Redpanda design

## 12.1 Purpose

Redpanda provides:

- Kafka-compatible topic storage;
- replayable CDC log;
- Kafka Connect compatibility;
- offset-based recovery;
- local single-node developer ergonomics.

### 12.2 Security boundary

In the local environment:

- bind host interfaces to localhost;
- use internal Docker networking;
- do not publish broker ports broadly;
- separate Connect and ClickHouse credentials if SASL is enabled;
- do not place source-database credentials in topic data;
- redact connector configurations in diagnostics.

### 12.3 Topic contract

Create:

```text
analytics/contracts/topics.yaml
```

Each topic declares:

```text
owner
source connector
source table
key schema
value schema strategy
partition count
retention
classification
consumer
dedup identity
ordering guarantee
version
```

### 12.4 Broker failure posture

Redpanda outage must:

- stop CDC transport;
- create connector lag;
- potentially increase retained WAL;
- trigger alerts;
- never block or fail an OLTP transaction;
- recover from stored offsets;
- not produce duplicate analytical facts after downstream deduplication.

---

## 13. ClickHouse warehouse architecture

Use these logical databases/schemas:

```text
raw
staging
core
mart
control
```

## 13.1 `raw`

Purpose:

- preserve approved CDC event envelopes;
- enable replay and diagnostics;
- record transport identity;
- remain append-only;
- have restricted access.

Expected table:

```text
raw.cdc_events
```

Minimum columns:

```text
topic String
partition Int32
offset Int64
message_key String
payload String
source_service LowCardinality(String)
source_schema LowCardinality(String)
source_table LowCardinality(String)
operation LowCardinality(String)
source_lsn Nullable(UInt64)
source_tx_id Nullable(String)
source_timestamp DateTime64(6, 'UTC')
connector_timestamp DateTime64(6, 'UTC')
ingested_at DateTime64(6, 'UTC')
event_date Date
```

Primary transport uniqueness:

```text
(topic, partition, offset)
```

`payload` contains only the already-filtered approved event.

### 13.2 Kafka Engine ingestion

For each approved topic pattern, define a Kafka Engine ingestion table or a
small, explicit group of compatible tables.

A materialized view writes messages into `raw.cdc_events`.

Use a format that preserves the raw approved event bytes while metadata is
captured from Kafka virtual columns.

### 13.3 Raw-table engine

Use an append-oriented MergeTree family table partitioned by a bounded time
unit.

Recommended:

```text
PARTITION BY toYYYYMM(event_date)
ORDER BY (source_service, source_table, topic, partition, offset)
```

T0/T3 must confirm exact engine and retention behavior.

### 13.4 Deduplication policy

Do not assume that an eventual background merge has already removed duplicates.

Typed models must select deterministic latest/current rows using:

- source primary key;
- source LSN where available;
- Kafka partition and offset as tiebreaker;
- source operation;
- deletion marker.

Transport duplicates are removed using:

```text
topic + partition + offset
```

Logical current state is selected using:

```text
source table + source primary key + greatest source ordering tuple
```

### 13.5 `staging`

Purpose:

- parse approved payloads;
- cast types;
- normalize timestamps;
- preserve source keys;
- expose operation/deletion metadata;
- provide deterministic current-state and change-history models.

Naming:

```text
stg_ledger__accounts_current
stg_ledger__transactions_current
stg_ledger__entries
stg_ledger__fee_quotes_current
stg_payin__requests_current
stg_payout__requests_current
```

Where history is useful:

```text
stg_payin__request_changes
stg_payout__request_changes
```

### 13.6 `core`

Purpose:

- conformed dimensions;
- durable analytical facts;
- stable semantic definitions;
- deterministic keys;
- no dashboard-specific presentation logic.

### 13.7 `mart`

Purpose:

- curated aggregates;
- documented metric semantics;
- dashboard-ready views;
- bounded query cost;
- explicit freshness.

### 13.8 `control`

Purpose:

- pipeline watermarks;
- schema fingerprints;
- connector status snapshots;
- dbt invocation results;
- reconciliation runs;
- reconciliation items;
- data-quality failures;
- backfill history.

---

## 14. dbt project design

## 14.1 Model layers

```text
sources
  -> staging
      -> core
          -> marts
              -> exposure documentation
```

### 14.2 Model contracts

Every model must declare:

- description;
- owner;
- grain;
- primary key;
- money-unit semantics;
- timestamp semantics;
- freshness expectation;
- sensitive-data classification;
- upstream sources;
- downstream exposures;
- retention;
- test set.

### 14.3 Materialization strategy

Initial guidelines:

```text
small dimensions:          table or view
current-state staging:     view or incremental table
append-only large facts:   incremental
daily marts:               incremental by report date
control data:              table
```

The exact choice must be measured.

### 14.4 Incremental strategy

Incremental models must:

- use deterministic source ordering;
- process a bounded lookback window;
- tolerate late events;
- be rerunnable;
- document unique keys;
- support a full refresh in a disposable local warehouse;
- never silently skip a failed period.

### 14.5 dbt tests

Built-in tests:

```text
not_null
unique
accepted_values
relationships
source freshness
```

Custom tests:

```text
money_is_integer_minor_units
ledger_debits_equal_credits
transaction_entries_match_header
no_orphan_account_reference
no_unknown_currency
no_unknown_transaction_type
terminal_payin_has_expected_ledger_link
terminal_payout_has_expected_ledger_link
fee_revenue_matches_fee_account_entries
no_duplicate_transport_offset
one_current_row_per_source_key
deleted_rows_not_present_in_current_model
no_prohibited_columns
mart_total_matches_core_fact
```

### 14.6 Documentation

`dbt docs` output is a development artifact.

Do not publish secrets or raw sample payloads in generated documentation.

---

## 15. Conformed dimensions

## 15.1 `dim_date`

Includes:

```text
date_key
calendar_date
year
quarter
month
week
day_of_week
is_weekend
reporting_timezone
```

Reporting calendar:

```text
Asia/Jakarta
```

Raw timestamps remain UTC.

### 15.2 `dim_currency`

Includes:

```text
currency_code
minor_unit
is_active
effective_from
effective_to
```

Do not infer minor unit from floating-point formatting.

### 15.3 `dim_transaction_type`

Maps internal transaction types into stable analytical categories.

Example categories:

```text
transfer
payin
payout
fee
hold
release
reversal
adjustment
other
```

Every mapping change is versioned and tested.

### 15.4 `dim_account`

Contains analytical classification, not live balance authority.

Possible fields:

```text
account_id
owner_type
owner_pseudonym
account_type
currency
status
is_system_account
is_fee_account
effective_from
effective_to
```

### 15.5 `dim_vendor`

Contains only non-secret provider identity and analytical categorization.

### 15.6 `dim_gateway`

Provides source/payment-channel classification where available.

### 15.7 `dim_merchant`

Deferred until C1 exists.

It may contain:

```text
merchant public ID
environment
status
created date
non-sensitive segment
```

No key, secret, contact PII, or private onboarding document.

---

## 16. Core fact model

## 16.1 `fact_ledger_entry`

Grain:

```text
one immutable ledger entry
```

Fields include:

```text
entry_id
transaction_id
account_id
entry_side
amount_minor
currency
transaction_type
created_at_utc
report_date_jakarta
source_lsn
source_topic
source_partition
source_offset
```

Rules:

- exact integer amount;
- immutable analytical row;
- one source entry maps to one logical fact;
- transport duplicates are removed;
- no recalculated balance becomes a source-of-truth fact.

### 16.2 `fact_ledger_transaction`

Grain:

```text
one ledger transaction
```

Fields include:

```text
transaction_id
idempotency_scope
transaction_type
status
amount_minor
currency
request_id where approved
created_at_utc
posted_at_utc
report_date_jakarta
debit_total_minor
credit_total_minor
entry_count
is_balanced
```

### 16.3 `fact_fee_quote`

Grain:

```text
one fee quote
```

Fields:

```text
fee_quote_id
quote_type
quoted_base_amount_minor
quoted_fee_amount_minor
currency
status
created_at_utc
expires_at_utc
consumed_at_utc
consumed_by_type
consumed_by_reference
```

This fact represents pricing intent, not recognized revenue.

### 16.4 `fact_fee_revenue`

Grain:

```text
one recognized fee movement per ledger transaction and fee account
```

Derivation:

- identify approved system fee accounts;
- use immutable ledger entries;
- determine net movement using account normal-balance rules;
- include only posted transactions;
- account for reversal/refund entries;
- retain source transaction linkage.

Do not derive recognized revenue from:

- `fee_quotes.fee_amount`;
- transaction amount;
- configured fee schedule alone;
- callback success without Ledger posting.

### 16.5 `fact_payin_lifecycle`

Grain:

```text
one pay-in
```

Fields:

```text
payin_id
user_pseudonym
merchant_public_id when applicable
amount_minor
currency
vendor
channel
created_at_utc
first_dispatch_at_utc
terminal_at_utc
status
terminal_reason_category
ledger_transaction_id
provider_attempt_count
callback_count
duration_to_terminal_ms
```

### 16.6 `fact_payout_lifecycle`

Grain:

```text
one payout
```

Fields:

```text
payout_id
user_pseudonym
merchant_public_id when applicable
amount_minor
currency
vendor
rail
created_at_utc
hold_at_utc
dispatch_at_utc
terminal_at_utc
status
hold_transaction_id
settlement_transaction_id
release_or_reversal_transaction_id
provider_attempt_count
callback_count
duration_to_terminal_ms
```

No destination detail is included.

### 16.7 `fact_provider_attempt`

Only create if source services contain safe, structured attempt metadata.

Grain:

```text
one provider dispatch attempt
```

Fields may include:

```text
owner_resource_id
owner_type
vendor
attempt_number
started_at_utc
finished_at_utc
result_category
http_status_class
error_category
latency_ms
```

Do not store raw request/response bodies.

### 16.8 `fact_daily_unit_economics`

Grain:

```text
report_date + currency + product + vendor + environment
```

Fields:

```text
processed_volume_minor
successful_transaction_count
failed_transaction_count
recognized_fee_revenue_minor
modeled_variable_vendor_cost_minor
modeled_contribution_margin_minor
cost_basis
source_freshness_seconds
reconciliation_status
```

`cost_basis` initial value:

```text
modeled
```

This fact must not be labeled actual profitability until real invoice/settlement
cost data exists.

---

## 17. Revenue and unit-economics semantics

## 17.1 Gross processed volume

Define separately per product:

```text
payin GPV
payout GPV
transfer volume
```

Do not add different economic concepts into one number without a documented
aggregate definition.

Default inclusion:

- terminal successful operation;
- exact transaction amount;
- report date based on the authoritative posting or terminal timestamp;
- reversal handling documented.

### 17.2 Recognized fee revenue

Recognized only when fee movement is posted into an approved fee system account
in LedgerService.

Formula at a documented cutoff:

```text
recognized fee revenue
=
fee-account credit movements
- fee-account debit/reversal movements
```

The precise debit/credit sign convention depends on the Ledger account model and
must be locked in T1/T6.

### 17.3 Fee quote conversion

Metrics:

```text
quotes created
quotes consumed
quotes expired
quote conversion rate
average quoted fee
quoted fee versus recognized fee delta
```

A consumed quote still does not independently prove recognized revenue.

### 17.4 Modeled vendor cost

C2 initially lacks vendor invoices.

Use a versioned analytical seed:

```text
analytics/dbt/seeds/vendor_cost_schedule.csv
```

Fields:

```text
effective_from
effective_to
product
vendor
rail
currency
fixed_cost_minor
variable_rate_basis_points
minimum_cost_minor
maximum_cost_minor
source_note
model_version
```

Rules:

- data must be synthetic or clearly sourced;
- cost is marked modeled;
- effective-date join is deterministic;
- seed updates are reviewed;
- no secret commercial agreement is committed;
- dashboard displays model version and cost basis.

### 17.5 Contribution margin

```text
modeled contribution margin
=
recognized fee revenue
- modeled variable vendor cost
```

This excludes:

- salaries;
- infrastructure;
- fraud losses;
- chargebacks unless modeled separately;
- taxes;
- fixed commercial commitments;
- capital costs;
- regulatory costs.

Do not label it net profit.

### 17.6 Currency rule

Do not aggregate different currencies into one monetary total without an
explicit FX conversion model.

C2 v1 dashboards:

- filter or group by currency;
- never perform implicit FX conversion;
- label counts separately from money;
- retain integer minor units through warehouse facts.

---

## 18. Time semantics

### 18.1 Source time

Source timestamps are stored and transported in UTC.

### 18.2 Reporting date

Business daily reporting uses:

```text
Asia/Jakarta
```

The conversion occurs in a documented warehouse macro.

### 18.3 Timestamp roles

Every fact must distinguish where applicable:

```text
created_at
posted_at
dispatched_at
callback_at
terminal_at
source_commit_at
connector_captured_at
warehouse_ingested_at
modeled_at
```

Do not use one generic `timestamp` column.

### 18.4 Cutoff semantics

A reconciliation compares source and warehouse only up to a safe shared cutoff.

Cutoff evidence may include:

```text
source LSN
connector offset
source commit timestamp
warehouse maximum ingested LSN
reconciliation started_at
```

Rows beyond the safe cutoff are excluded from both expected and actual values.

### 18.5 Late-arriving events

Models must:

- process a configurable lookback;
- update affected daily partitions;
- preserve original event time;
- expose last-updated time;
- let historical dashboard totals change after late arrival;
- record a reconciliation rerun.

---

## 19. Data-quality and reconciliation framework

## 19.1 Principles

Reconciliation is:

- deterministic;
- cutoff-aware;
- exact for integer money;
- independently rerunnable;
- visible;
- non-blocking to OLTP;
- capable of recording mismatches without hiding them.

### 19.2 Control tables

Create:

```text
control.reconciliation_runs
control.reconciliation_items
control.pipeline_watermarks
control.schema_fingerprints
control.dbt_invocations
control.data_quality_failures
control.backfill_runs
```

### 19.3 Run statuses

```text
running
passed
failed
stale
cancelled
```

### 19.4 Reconciliation item fields

```text
run_id
check_name
source_service
source_table_or_metric
warehouse_model
currency
report_date
cutoff_type
cutoff_value
expected_value
actual_value
delta_value
severity
status
details
created_at
```

Details must not contain sensitive source rows.

### 19.5 Level 1 — Pipeline completeness

Checks:

- connector running;
- task running;
- source slot active;
- source retained-WAL bound;
- topic latest offset;
- ClickHouse consumed offset;
- raw maximum source LSN;
- freshness age;
- duplicate transport offsets;
- unexpected topic/table.

### 19.6 Level 2 — Source row-state reconciliation

For mutable source tables:

- source row count at safe cutoff where feasible;
- warehouse current-row count;
- deleted-row count;
- bucketed checksums over stable approved columns;
- maximum updated timestamp;
- sample deterministic primary-key lookup.

Avoid unbounded full-source scans on every run.

Use bounded date/key buckets and scheduled deeper checks.

### 19.7 Level 3 — Ledger invariants

For each posted transaction:

```text
sum(debit entries) = sum(credit entries)
```

For each currency and report period:

```text
total debits = total credits
```

Additional checks:

```text
every entry references one known transaction
every entry references one known account
posted transaction has expected entry count or rule-valid count
header amount agrees with documented posting rule
no mutable/delete event for immutable entry without incident
```

### 19.8 Level 4 — Fee-revenue reconciliation

For the cutoff:

```text
warehouse fact_fee_revenue
=
net movement in approved fee accounts
```

Group by:

```text
currency
report date
transaction type
fee account
```

### 19.9 Level 5 — Pay-in and payout reconciliation

Examples:

```text
successful pay-in amount = linked Ledger credit amount
successful payout amount = linked settlement amount
failed payout hold = linked release/reversal where required
terminal owner state has expected Ledger links
no owner resource links to another operation's Ledger transaction
```

Unlinked legacy rows are classified, not silently ignored.

### 19.10 Level 6 — Mart reconciliation

Examples:

```text
daily GPV mart = sum of included core lifecycle facts
recognized revenue mart = sum of fact_fee_revenue
unit economics revenue = recognized revenue
dashboard card query = approved mart query
```

### 19.11 Mismatch policy

Severity:

```text
info
warning
critical
```

Critical examples:

- ledger debit/credit mismatch;
- missing posted ledger transaction;
- duplicated logical entry;
- recognized revenue mismatch;
- source slot WAL retention threatening disk.

A mismatch does not rewrite source data.

It creates:

- control evidence;
- metric;
- alert where appropriate;
- runbook path;
- investigation record.

---

## 20. Reconciliation runner

A small repository-local CLI is permitted because it is an operational tool,
not a business service.

Recommended location:

```text
analytics/reconciliation/cmd/reconcile
```

Responsibilities:

- read approved source summaries using read-only credentials;
- read ClickHouse control/core/mart data;
- establish a safe cutoff;
- run bounded checks;
- persist results to ClickHouse control tables;
- exit non-zero on configured critical failures;
- output machine-readable evidence.

Restrictions:

- no source writes;
- no source row repair;
- no secret values in output;
- no arbitrary SQL from user input;
- no long unbounded transaction;
- statement timeout required;
- per-source concurrency bounded.

---

## 21. Dashboard specification

## 21.1 Dashboard 1 — Executive overview

Cards:

```text
successful processed volume by currency
successful transaction count
recognized fee revenue by currency
modeled vendor cost
modeled contribution margin
pay-in success rate
payout success rate
data freshness
latest reconciliation status
```

Filters:

```text
date range
environment
currency
product
vendor
```

Required labels:

```text
Recognized revenue
Modeled cost
Modeled contribution margin
Data updated at
Reconciliation status
```

### 21.2 Dashboard 2 — Pay-in performance

Cards:

```text
created pay-ins
successful pay-ins
failed/expired pay-ins
success rate
median and p95 time to terminal
provider attempts
callback duplicates
volume by vendor
failure category
```

### 21.3 Dashboard 3 — Payout performance

Cards:

```text
created payouts
successful payouts
failed payouts
success rate
hold-to-terminal duration
settlement duration
release/reversal count
volume by vendor/rail
failure category
```

### 21.4 Dashboard 4 — Fee and quote conversion

Cards:

```text
quotes created
quotes consumed
quotes expired
conversion rate
quoted fee
recognized fee
quote-to-recognition delta
recognized revenue by transaction type
```

### 21.5 Dashboard 5 — Unit economics

Cards:

```text
recognized revenue
modeled variable cost
modeled contribution margin
margin per successful operation
margin by product
margin by vendor
cost model version
```

The dashboard must prominently display:

```text
Costs are modeled, not invoiced actuals.
```

### 21.6 Dashboard 6 — Data platform health

Cards:

```text
connector status
source-to-warehouse lag
oldest unconsumed event
retained WAL
raw ingestion rate
dbt latest run
failed data tests
reconciliation failures
schema changes
```

This operational dashboard may live in Grafana rather than Metabase when the
data already exists in Prometheus. Business users should not need connector
internals.

### 21.7 Dashboard governance

- dashboard queries use approved marts;
- no direct raw-schema access;
- no user-level PII filters;
- no arbitrary SQL editor for ordinary BI role;
- dashboard definition changes are versioned where practical;
- metric definitions link to documentation;
- every dashboard exposes freshness;
- stale data is visually obvious;
- no total money card mixes currencies.

---

## 22. Metabase access model

ClickHouse roles:

```text
cdc_ingest
dbt_transform
bi_readonly
ops_readonly
reconciliation_read
reconciliation_write_control
```

### 22.1 `bi_readonly`

May read:

```text
mart
approved core views
metadata required for BI
```

May not read:

```text
raw
restricted staging
control details containing identifiers
system tables beyond necessary metadata
```

### 22.2 Metabase rules

- no source PostgreSQL connections;
- no write permission;
- credentials loaded from secret files or approved secret workflow;
- localhost/internal network only;
- default admin credentials prohibited;
- sample database disabled;
- usage telemetry decision documented;
- backups are local and synthetic only;
- exports must not contain prohibited identifiers.

---

## 23. Security and privacy model

## 23.1 Threats

Update the threat model for:

- replication credential leakage;
- connector configuration leakage;
- broad table wildcard capture;
- accidental PII/raw-payload ingestion;
- pseudonym salt leakage;
- cross-environment data mixing;
- Metabase over-privilege;
- ClickHouse public exposure;
- replay of sensitive historical data;
- malicious schema change;
- unbounded WAL retention;
- dashboard inference;
- CSV export leakage;
- analytical user querying raw facts;
- stale data presented as current;
- revenue metric manipulation through incorrect mapping.

### 23.2 Network controls

- internal Docker network;
- host bindings use `127.0.0.1`;
- no public broker;
- no public ClickHouse;
- no public Kafka Connect;
- Metabase local only;
- source DB allows connector network only;
- application services do not need analytics credentials.

### 23.3 Secrets

Secrets include:

```text
replication passwords
Kafka/Redpanda credentials where enabled
ClickHouse credentials
Metabase database password
pseudonymization salt
```

Rules:

- never committed;
- never printed;
- loaded through approved secret-file workflow;
- example files contain placeholders;
- startup fails when required secret is absent;
- log-capture tests redact connector URLs.

### 23.4 Environment isolation

Every CDC event and warehouse fact must carry environment identity.

Rules:

- sandbox/test and local-live simulation are not mixed by accident;
- connector prefix includes environment;
- ClickHouse partitions or models include environment;
- dashboards default to non-live local environment;
- a future real environment needs separate credentials and infrastructure.

### 23.5 Data minimization

Capture only what a named metric or reconciliation check requires.

“Could be useful later” is not a valid purpose.

### 23.6 Deletion and correction propagation

For mutable/non-financial source data:

- CDC deletion marker reaches staging;
- current models exclude deleted rows;
- historical facts retain only what policy permits;
- marts are rebuilt or incrementally corrected;
- control evidence records deletion processing.

Immutable Ledger entries must not be deleted. A delete event for such a table is
a critical incident.

---

## 24. Observability

## 24.1 Source/connector metrics

Monitor:

```text
connector state
task state
snapshot state
snapshot rows remaining where available
seconds since last event
source commit to connector lag
replication slot active
retained WAL bytes
connector restart count
connector error count
schema-history errors
```

### 24.2 Broker metrics

Monitor:

```text
topic bytes
latest offset
consumer group lag
under-replicated partitions where applicable
broker memory
disk usage
request latency
topic retention pressure
```

### 24.3 ClickHouse metrics

Monitor:

```text
Kafka ingestion lag
consumer errors
raw inserted rows
duplicate transport offsets
query duration
failed queries
merge backlog
part count
disk usage
memory pressure
dbt model duration
```

### 24.4 Data-quality metrics

Suggested bounded metrics:

```text
seev_analytics_connector_up{source}
seev_analytics_cdc_lag_seconds{source}
seev_analytics_slot_retained_bytes{source}
seev_analytics_ingested_events_total{source,table,operation}
seev_analytics_transport_duplicates_total{source,table}
seev_analytics_dbt_run_total{result}
seev_analytics_dbt_model_duration_seconds{layer,result}
seev_analytics_reconciliation_total{check,status,severity}
seev_analytics_reconciliation_delta{check,currency}
seev_analytics_data_freshness_seconds{model}
seev_analytics_schema_change_total{source,table,classification}
```

Do not label metrics with:

```text
user ID
account ID
transaction ID
payin/payout ID
request ID
topic offset
raw error message
```

### 24.5 Tracing

CDC is asynchronous and may not preserve one distributed trace.

Use trace links where source metadata provides an approved trace/request ID.

Do not manufacture one long span from source transaction through dashboard.

### 24.6 Alerts

Required alerts:

```text
connector task failed
CDC freshness above threshold
replication slot inactive unexpectedly
retained WAL above warning/critical threshold
Redpanda consumer lag growing
ClickHouse ingestion stopped
dbt run failed
critical reconciliation failed
schema change incompatible
raw sensitive-column validator failed
Metabase unable to query approved mart
```

Every alert links to a runbook.

---

## 25. Runbooks

Create:

```text
docs/runbooks/analytics-connector-failed.md
docs/runbooks/analytics-replication-slot-lag.md
docs/runbooks/analytics-source-wal-pressure.md
docs/runbooks/analytics-redpanda-outage.md
docs/runbooks/analytics-clickhouse-ingestion-stalled.md
docs/runbooks/analytics-dbt-failure.md
docs/runbooks/analytics-schema-change.md
docs/runbooks/analytics-reconciliation-failed.md
docs/runbooks/analytics-sensitive-data-incident.md
docs/runbooks/analytics-metabase-outage.md
docs/runbooks/analytics-full-rebuild.md
```

Each runbook must contain:

- symptoms;
- user/business impact;
- confirmation commands;
- immediate safety action;
- recovery;
- data-loss/duplicate assessment;
- reconciliation steps;
- rollback;
- evidence to record.

---

## 26. Backfill, snapshot, and rebuild

## 26.1 Initial snapshot

Each connector begins with an initial snapshot of only approved tables/columns.

Before snapshot:

- estimate table size;
- estimate duration;
- confirm source load;
- confirm disk headroom;
- set statement/lock safeguards where supported;
- record start LSN and connector state.

### 26.2 Snapshot ordering

Recommended first source:

```text
LedgerService
```

Order:

```text
accounts
ledger_transactions
ledger_entries
fee_quotes
```

Then:

```text
PayinService
PayoutService
```

### 26.3 Incremental source addition

When adding a table:

- update source contract;
- review privacy;
- update publication;
- add connector include rule;
- perform an incremental/ad hoc snapshot where supported;
- add raw/staging/model tests;
- reconcile;
- update lineage.

### 26.4 Warehouse rebuild

The local warehouse is disposable.

A full rebuild must be documented:

```text
stop BI writes/queries where necessary
pause ClickHouse consumption
record source and topic offsets
drop/recreate warehouse schemas
replay retained CDC or rerun source snapshots
run dbt full-refresh
run all tests
run reconciliation
resume dashboards
```

### 26.5 Retention dependency

Because Redpanda retention is bounded, a warehouse rebuild may require a new
source snapshot after old CDC events expire.

This is acceptable for local C2 and must be explicit.

---

## 27. Retention

Initial configurable local defaults:

```text
Redpanda CDC topics:         7 days
ClickHouse raw CDC:          30 days
staging change history:      90 days unless required longer
control/reconciliation:      180 days
daily analytical marts:      24 months
dbt invocation artifacts:    30 days
Metabase application data:   local-development policy
```

Rules:

- source-retention policy remains authoritative;
- financial aggregate retention does not justify retaining prohibited PII;
- raw retention is shorter than curated fact retention;
- deletion handling remains tested;
- TTL behavior is monitored;
- a retention purge must not mutate source systems.

---

## 28. Task breakdown

# T0 — Entry gate and current-state inventory

### Work

- Record exact baseline commit.
- Re-read current roadmap and service ownership documentation.
- Run current repository verification.
- Inventory PostgreSQL versions and logical-replication settings.
- Inventory database names, migration heads, tables, primary keys, and sizes.
- Inventory existing reporting views.
- Inventory exact Ledger/Payin/Payout correlation identifiers.
- Inventory source fields containing JSON, PII, secrets, and financial data.
- Measure local CPU, memory, and disk budget.
- Confirm existing Compose networks and profiles.
- Confirm Prometheus/Grafana integration points.
- Decide pinned component versions through compatibility testing.
- Record every unresolved source-data gap.

### Deliverables

```text
docs/evidence/c2-entry-gate.md
docs/reference/c2-source-inventory.md
docs/reference/c2-correlation-inventory.md
docs/reference/c2-resource-baseline.md
```

### Acceptance

- [ ] Every selected source table has an owner.
- [ ] Every selected source column has a purpose.
- [ ] Prohibited fields are identified.
- [ ] Correlation gaps are explicit.
- [ ] Exact source version and baseline commit are recorded.
- [ ] Current gates remain green.
- [ ] Resource baseline is measured.
- [ ] No connector has been created from an unreviewed wildcard.

---

# T1 — Lock architecture, data contracts, metrics, and threat model

### Work

- Add the analytical architecture document.
- Add source, privacy, topic, model, and metric contracts.
- Lock environment naming.
- Lock exact money and timestamp semantics.
- Lock report timezone.
- Lock metric definitions.
- Lock revenue recognition from Ledger fee accounts.
- Lock modeled-cost semantics.
- Lock the correlation matrix.
- Lock schema-change classification.
- Update threat model.
- Add sequence and failure diagrams.
- Document what remains authoritative in OLTP.

### Required diagrams

```text
source commit to dashboard
initial snapshot
normal CDC streaming
connector restart
Redpanda outage
ClickHouse outage
schema change
warehouse rebuild
reconciliation cutoff
fee revenue derivation
pay-in/payout to Ledger correlation
```

### Acceptance

- [ ] No metric is implemented without an owner and formula.
- [ ] Revenue is not derived from fee quote or GPV.
- [ ] No currency is implicitly converted.
- [ ] No fuzzy join is authorized.
- [ ] Sensitive-data policy is reviewed.
- [ ] RabbitMQ and Redpanda boundaries are explicit.
- [ ] Existing reporting authority is documented.
- [ ] Threat-model controls have tests and owners.

---

# T2 — Source readiness and deterministic correlation

### Work

- Add replication users through controlled database setup/migration tooling.
- Add explicit publications.
- Confirm primary keys.
- Assess replica identity.
- Separate safe provider metadata from raw payload where required.
- Add missing deterministic correlation columns through source-owner
  migrations.
- Backfill only provable correlations.
- Add indexes required by bounded reconciliation queries.
- Add source-side summary queries.
- Add source-contract validation.
- Add rollback and slot-drop procedures.

### Acceptance

- [ ] Replication user cannot write.
- [ ] Publication contains only allowlisted tables.
- [ ] Every captured table has a stable key.
- [ ] No cross-database foreign key exists.
- [ ] Raw callback and destination fields remain excluded.
- [ ] Correlation tests pass.
- [ ] Source migrations preserve existing journeys.
- [ ] Reconciliation queries have statement timeouts and explain evidence.
- [ ] Logical-replication setup is reproducible.

---

# T3 — Analytics Compose platform

### Work

- Add analytics Compose file/profile.
- Add Redpanda.
- Add Kafka Connect image with pinned Debezium plugin.
- Add ClickHouse.
- Add dbt runner.
- Add optional Metabase profile.
- Add health checks.
- Add explicit volumes.
- Add localhost-only ports.
- Add secret-file integration.
- Add bounded resource configuration.
- Add init jobs.
- Add start/stop/reset commands.
- Add low-memory documentation.

### Acceptance

- [ ] `analytics-core` starts independently.
- [ ] Metabase is optional.
- [ ] No default public network exposure exists.
- [ ] Required secret absence fails clearly.
- [ ] Component versions are pinned.
- [ ] Health checks become green.
- [ ] Clean shutdown does not drop replication slots.
- [ ] Reset command affects only analytical state.
- [ ] Measured resource usage is recorded.

---

# T4 — CDC connectors and topic contracts

### Work

- Add Ledger connector first.
- Validate initial snapshot.
- Validate streaming inserts.
- Validate updates and deletes on safe mutable fixtures.
- Preserve source metadata.
- Add heartbeats.
- Add transaction metadata where useful.
- Add deterministic pseudonymization.
- Add topic specifications.
- Add connector apply/pause/resume/delete scripts.
- Add Payin connector.
- Add Payout connector.
- Add connector diagnostics and metrics.
- Add schema-change fixtures.

### Acceptance

- [ ] Connector configuration uses explicit includes.
- [ ] Prohibited columns do not appear in topic fixtures.
- [ ] Snapshot produces expected logical rows.
- [ ] Streaming resumes from offsets after restart.
- [ ] Delete marker is preserved.
- [ ] Same identity pseudonymizes consistently across sources.
- [ ] Source ordering metadata is present.
- [ ] Connector task failure is visible.
- [ ] Topic retention is configured.
- [ ] OLTP continues when Redpanda is stopped.

---

# T5 — ClickHouse raw and staging layers

### Work

- Add database/user migrations.
- Add least-privilege roles.
- Add Kafka Engine ingestion.
- Add raw event table.
- Add transport dedup view/model.
- Add typed staging models for Ledger.
- Add current-state logic.
- Add deletion handling.
- Add Payin/Payout staging.
- Add source freshness and schema fingerprinting.
- Add TTL.
- Add restricted grants.
- Add ingestion replay tests.

### Acceptance

- [ ] Raw ingestion captures topic/partition/offset.
- [ ] Duplicate transport event does not create duplicate logical staging row.
- [ ] Latest row selection is deterministic.
- [ ] Deleted mutable row disappears from current model.
- [ ] Immutable Ledger delete triggers a critical failure.
- [ ] Exact integer money survives round trip.
- [ ] UTC precision is preserved.
- [ ] BI role cannot read raw.
- [ ] Raw TTL is tested.
- [ ] ClickHouse restart resumes ingestion safely.

---

# T6 — dbt core facts and dimensions

### Work

- Add dbt sources.
- Add staging contracts.
- Add dimensions.
- Add `fact_ledger_entry`.
- Add `fact_ledger_transaction`.
- Add `fact_fee_quote`.
- Add `fact_payin_lifecycle`.
- Add `fact_payout_lifecycle`.
- Add `fact_provider_attempt` only when source metadata is safe.
- Add custom financial tests.
- Add incremental and full-refresh paths.
- Add lineage documentation.

### Acceptance

- [ ] Every model declares grain and unique key.
- [ ] Every model declares timestamp and money semantics.
- [ ] Ledger facts satisfy debit-credit tests.
- [ ] Duplicate CDC records are tolerated.
- [ ] Late events are reprocessed.
- [ ] Cross-service joins use deterministic keys.
- [ ] No prohibited field appears in dbt catalog.
- [ ] Full refresh produces the same logical totals.
- [ ] Source freshness tests work.
- [ ] dbt documentation builds.

---

# T7 — Revenue and unit-economics marts

### Work

- Identify approved Ledger fee accounts.
- Document account sign convention.
- Build `fact_fee_revenue`.
- Reconcile fee revenue to Ledger entries.
- Build daily GPV and success marts.
- Build fee-quote conversion mart.
- Add versioned synthetic/modeled vendor-cost seed.
- Build daily unit-economics mart.
- Add multi-currency safeguards.
- Add freshness and reconciliation status columns.
- Add metric documentation.

### Acceptance

- [ ] Recognized revenue uses posted Ledger entries.
- [ ] Reversal/refund reduces revenue correctly.
- [ ] Fee quote is not counted as revenue.
- [ ] GPV is not labeled revenue.
- [ ] Different currencies remain separate.
- [ ] Modeled cost is clearly identified.
- [ ] Contribution margin excludes undocumented cost categories.
- [ ] Effective-date cost joins are deterministic.
- [ ] Revenue mart reconciles exactly to fee accounts.
- [ ] Model-version changes are visible.

---

# T8 — Reconciliation and data-quality control plane

### Work

- Implement reconciliation CLI.
- Add control tables.
- Add safe-cutoff logic.
- Add pipeline completeness checks.
- Add bounded source row/checksum checks.
- Add Ledger invariant checks.
- Add fee-revenue reconciliation.
- Add Payin/Payout-to-Ledger checks.
- Add mart reconciliation.
- Add severity and status.
- Add scheduled/local invocation.
- Add evidence output.
- Add stale-run detection.

### Acceptance

- [ ] Cutoff prevents false mismatch from in-flight CDC.
- [ ] Critical money mismatch exits non-zero.
- [ ] Result is persisted.
- [ ] No source write is possible.
- [ ] Queries are bounded and timed out.
- [ ] Mismatch details contain no sensitive row.
- [ ] Re-run is idempotent or clearly versioned.
- [ ] Unlinked legacy data is classified.
- [ ] Dashboard can show latest reconciliation status.
- [ ] Reconciliation failure does not block OLTP.

---

# T9 — Metabase and curated dashboards

### Work

- Add read-only ClickHouse role.
- Add Metabase profile.
- Disable sample data.
- Add collections and permissions.
- Build approved questions/models.
- Build six dashboards.
- Add filters.
- Add freshness and reconciliation banners.
- Add modeled-cost labels.
- Add dashboard export/import workflow.
- Add BI access documentation.

### Acceptance

- [ ] Metabase has no OLTP connection.
- [ ] Metabase cannot write to ClickHouse.
- [ ] Ordinary BI role cannot access raw/staging restricted schemas.
- [ ] No dashboard mixes currencies.
- [ ] No dashboard displays PII.
- [ ] All money cards identify metric semantics.
- [ ] Unit economics is labeled modeled.
- [ ] Stale data is visible.
- [ ] Dashboard totals match marts.
- [ ] Metabase outage does not affect ingestion or OLTP.

---

# T10 — Observability, alerts, and runbooks

### Work

- Export connector and broker metrics.
- Export ClickHouse metrics.
- Add warehouse freshness metrics.
- Add dbt invocation metrics.
- Add reconciliation metrics.
- Add Grafana operational dashboard.
- Add alerts.
- Write runbooks.
- Validate cardinality.
- Add retention and disk alerts.
- Add sensitive-data incident workflow.

### Acceptance

- [ ] Connector failure is detected.
- [ ] WAL pressure is detected before source disk danger.
- [ ] Ingestion lag is visible end to end.
- [ ] ClickHouse failure is visible.
- [ ] dbt and reconciliation failures are visible.
- [ ] Alerts have runbooks.
- [ ] Metric labels are bounded.
- [ ] No sensitive ID appears in metrics.
- [ ] Recovery commands are exercised.
- [ ] Data-platform health is distinguishable from business health.

---

# T11 — E2E, chaos, performance, and final evidence

### Work

- Add synthetic end-to-end journey.
- Add snapshot and streaming evidence.
- Add duplicate/replay evidence.
- Add schema-change evidence.
- Add source restart.
- Add Connect restart.
- Add Redpanda outage.
- Add ClickHouse outage.
- Add Metabase outage.
- Add WAL-pressure drill with safe local bounds.
- Add full warehouse rebuild.
- Add sensitive-column scan.
- Add query-performance baseline.
- Run clean-tree verification.
- Record residual risks.
- Update roadmap and service references.
- Archive only after acceptance.

### Acceptance

- [ ] OLTP business journeys stay green through analytics outages.
- [ ] Initial snapshot and streaming totals reconcile.
- [ ] Duplicate event does not duplicate logical financial fact.
- [ ] Connector resumes after restart.
- [ ] Redpanda outage creates lag but no OLTP failure.
- [ ] ClickHouse outage recovers without logical duplication.
- [ ] Incompatible schema change fails visibly.
- [ ] Full rebuild reaches identical reconciled totals.
- [ ] Prohibited-column scan is clean.
- [ ] Dashboard queries meet the documented local target.
- [ ] Final clean-tree gate passes.
- [ ] Evidence and residual risks are linked.
- [ ] Plan status is updated truthfully.

---

## 29. Recommended pull-request sequence

```text
PR 1  — C2 entry evidence, architecture, source/privacy/metric contracts
PR 2  — Analytics Compose platform and pinned component images
PR 3  — Ledger replication user/publication and Debezium connector
PR 4  — ClickHouse raw ingestion and transport-dedup foundation
PR 5  — Ledger staging, dimensions, facts, and invariant tests
PR 6  — Reconciliation control tables and Ledger reconciliation CLI
PR 7  — Fee revenue and initial executive mart/dashboard
PR 8  — Payin source readiness, connector, staging, lifecycle fact
PR 9  — Payout source readiness, connector, staging, lifecycle fact
PR 10 — Modeled vendor cost and unit-economics mart
PR 11 — Metabase curated dashboards and access controls
PR 12 — Observability, alerts, runbooks, chaos, and final evidence
```

Split further when a source-owner migration is large.

Do not combine source schema changes, connector creation, warehouse modeling, and
dashboard publication in one unreviewable PR.

---

## 30. Dependency graph

```text
T0 Entry gate
  |
  v
T1 Architecture + contracts + privacy
  |
  |--------------------------|
  v                          v
T2 Source readiness      T3 Analytics platform
  |                          |
  |-------------|------------|
                v
       T4 CDC connectors
                |
                v
       T5 Raw + staging
                |
                v
       T6 Core facts/dimensions
          |             |
          v             v
 T7 Revenue/economics  T8 Reconciliation
          |             |
          |------|------|
                 v
          T9 Dashboards
                 |
                 v
    T10 Observability/runbooks
                 |
                 v
       T11 Final verification
```

Ledger vertical-slice work must complete before Payin/Payout expansion.

---

## 31. First implementation cut

The first mergeable end-to-end slice should include only LedgerService.

```text
Ledger accounts
Ledger transactions
Ledger entries
        |
        v
Debezium initial snapshot + WAL stream
        |
        v
Redpanda table topics
        |
        v
ClickHouse raw.cdc_events
        |
        v
staging current/history models
        |
        v
fact_ledger_entry
fact_ledger_transaction
        |
        v
debit = credit reconciliation
        |
        v
one daily volume dashboard
```

The first slice must prove:

- source allowlisting;
- least-privilege replication;
- snapshot;
- streaming;
- restart recovery;
- transport deduplication;
- exact money;
- source ordering;
- current-state modeling;
- Ledger invariants;
- freshness;
- read-only BI access;
- OLTP independence.

Do not start Payin/Payout CDC before this slice is reconciled.

---

## 32. Second implementation cut

Add fee analytics on top of the proven Ledger slice.

```text
fee_quotes
approved fee system accounts
ledger fee entries
        |
        v
fact_fee_quote
fact_fee_revenue
        |
        v
quote conversion mart
recognized revenue mart
        |
        v
revenue dashboard
```

This slice must prove:

- quote intent and recognized revenue remain separate;
- fee account mapping is explicit;
- reversal/refund behavior is correct;
- revenue reconciles to Ledger;
- currencies remain separate.

---

## 33. Third implementation cut

Add Payin and Payout lifecycle analytics.

```text
Payin/Payout owner tables
        |
        v
safe CDC fields
        |
        v
lifecycle facts
        |
        v
deterministic Ledger links
        |
        v
success, duration, provider, and unit-economics marts
```

Do not add raw vendor payloads to make joins easier.

---

## 34. Make targets

Recommended targets:

```text
make analytics-config-check
make analytics-up-core
make analytics-up-ui
make analytics-health
make analytics-connectors-validate
make analytics-connectors-apply
make analytics-connectors-status
make analytics-clickhouse-migrate
make analytics-dbt-deps
make analytics-dbt-build
make analytics-dbt-test
make analytics-reconcile
make analytics-e2e
make analytics-chaos
make analytics-reset
make analytics-down
make analytics-verify
```

Repository gate policy:

- `make verify-full` includes lightweight analytics config, schema, SQL, and
  contract checks;
- heavy runtime `analytics-e2e` is explicit or scheduled in CI;
- destructive chaos remains separate;
- final C2 acceptance runs both normal and chaos gates.

---

## 35. Verification strategy

## 35.1 Static/config validation

- Compose config validation;
- connector JSON validation;
- source allowlist validation;
- prohibited-column validation;
- ClickHouse migration syntax;
- dbt parse/compile;
- metric-contract validation;
- secret placeholder scan;
- shell lint;
- SQL lint where configured.

### 35.2 Unit tests

Cover:

- source contract parser;
- privacy classification;
- pseudonymization fixture;
- topic naming;
- CDC envelope parser;
- source-order comparison;
- delete handling;
- exact-money conversion;
- report-date conversion;
- revenue sign convention;
- cost schedule effective-date selection;
- safe-cutoff selection;
- reconciliation delta;
- sensitive-detail redaction.

### 35.3 Integration tests

Use real local:

```text
PostgreSQL
Redpanda
Kafka Connect/Debezium
ClickHouse
```

Prove:

- initial snapshot;
- streaming insert;
- mutable update;
- delete marker;
- connector restart;
- broker restart;
- ClickHouse restart;
- duplicate event;
- offset resume;
- dbt incremental run;
- full refresh;
- reconciliation.

### 35.4 Contract fixtures

Maintain fixtures for:

```text
insert event
update event
delete event
snapshot event
heartbeat
schema addition
pseudonymized field
excluded field
duplicate transport message
out-of-order fixture where simulated
```

### 35.5 Data tests

At least:

- uniqueness;
- not null;
- accepted states;
- exact money;
- debit/credit equality;
- current-row uniqueness;
- source freshness;
- no prohibited column;
- metric total consistency;
- no mixed-currency total.

### 35.6 Dashboard tests

- saved question targets approved model;
- expected filters exist;
- freshness card exists;
- reconciliation card exists;
- modeled-cost warning exists;
- no raw schema target;
- no PII field;
- totals match known synthetic fixture.

---

## 36. Chaos matrix

## 36.1 Kafka Connect crash

Action:

```text
stop/kill Connect during active CDC
```

Expected:

- OLTP succeeds;
- slot retains WAL;
- alert fires;
- connector resumes from stored offsets;
- duplicate transport events do not duplicate logical facts;
- reconciliation passes after catch-up.

### 36.2 Redpanda outage

Expected:

- connector cannot publish;
- source WAL retention grows within safe local bound;
- OLTP succeeds;
- alert fires;
- broker recovery drains backlog;
- no logical fact duplication.

### 36.3 ClickHouse outage

Expected:

- Redpanda retains CDC;
- OLTP and connectors continue within retention/capacity;
- consumer lag grows;
- alert fires;
- ingestion resumes;
- models and reconciliation catch up.

### 36.4 Source PostgreSQL restart

Expected:

- application restart behavior remains normal;
- connector reconnects;
- slot remains valid;
- no missing logical range;
- reconciliation passes.

### 36.5 Connector snapshot interruption

Expected:

- documented restart/resume behavior;
- no silent partial warehouse publication;
- snapshot status visible;
- final logical totals reconcile.

### 36.6 Duplicate event injection

Expected:

- raw may contain duplicate transport fixture if deliberately injected;
- staging/core logical key remains unique;
- revenue and volume do not double.

### 36.7 Incompatible schema change

Examples:

```text
rename amount
change amount from integer to float
drop transaction_id
change timestamp semantics
```

Expected:

- connector/model/test fails visibly;
- previous approved marts remain available where safe;
- no silent null/default conversion;
- runbook invoked.

### 36.8 Sensitive-column addition

Example:

```text
add destination_account_number
```

Expected:

- allowlist prevents automatic capture;
- contract validator requires review;
- no topic/warehouse column appears.

### 36.9 Metabase outage

Expected:

- ingestion and dbt unaffected;
- OLTP unaffected;
- dashboard unavailable only;
- recovery requires no source replay.

### 36.10 WAL pressure

Simulate safely with a small local threshold.

Expected:

- warning and critical alerts;
- runbook identifies slot;
- source safety action is clear;
- disposable analytics may be reset rather than risking source disk.

---

## 37. Performance targets

These are local engineering acceptance targets, not production capacity claims.

T0 records hardware and data volume.

Initial targets to validate and adjust:

```text
CDC median freshness under normal local load:          <= 30 seconds
CDC p95 freshness under normal local load:             <= 120 seconds
daily executive mart query on test dataset:            <= 2 seconds
filtered lifecycle dashboard query:                    <= 3 seconds
incremental dbt run after small batch:                  <= 5 minutes
reconciliation for one daily bucket:                   <= 5 minutes
OLTP p95 regression from CDC at test load:              <= 5%
source retained-WAL growth during healthy operation:    stable/bounded
```

A failed target requires diagnosis, not immediate scaling.

Potential actions:

- narrower capture;
- better source index for bounded reconciliation;
- ClickHouse partition/order adjustment;
- model materialization change;
- incremental-window adjustment;
- batch/consumer tuning;
- resource-limit adjustment.

Do not activate unrelated B1/B2/B3 optimization from C2 measurements.

---

## 38. Rollout stages

### Stage 0 — Synthetic fixtures only

- ClickHouse;
- dbt;
- Metabase;
- no source connector;
- model and dashboard semantics validated.

### Stage 1 — Ledger snapshot

- explicit Ledger publication;
- initial snapshot;
- raw/staging/core;
- no business dashboard claim;
- reconciliation required.

### Stage 2 — Ledger streaming

- live local WAL;
- restart recovery;
- lag metrics;
- volume dashboard;
- exact reconciliation.

### Stage 3 — Fee revenue

- fee-account mapping;
- quote and recognized-revenue separation;
- revenue dashboard;
- reversal testing.

### Stage 4 — Payin/Payout lifecycle

- owner-source connectors;
- deterministic links;
- lifecycle dashboards;
- unit-economics model.

### Stage 5 — Optional enrichment

Only with separate evidence:

- VendorService provider-attempt metadata;
- C1 merchant dimensions;
- Fraud analytical facts;
- additional marts.

---

## 39. Rollback

C2 rollback is isolation-first.

### 39.1 Immediate disable

Order:

1. hide or mark dashboards unavailable;
2. pause dbt/reconciliation jobs;
3. pause/delete connectors if needed;
4. stop Connect consumers;
5. inspect retained WAL;
6. drop disposable slots only through runbook;
7. preserve source health.

### 39.2 Source protection

If analytics threatens source disk:

- prioritize OLTP;
- stop/drop the disposable analytics slot when necessary;
- accept a future re-snapshot;
- record the lost analytical continuity;
- do not risk payment availability to preserve local CDC history.

### 39.3 Data rollback

Warehouse data may be:

- dropped;
- rebuilt;
- re-snapshotted;
- replayed from retained topics.

No source financial row is rolled back from C2.

---

## 40. Documentation deliverables

Add or update:

```text
docs/roadmap/active/58-c2-data-platform-revenue-analytics.md
docs/roadmap/README.md
docs/roadmap/42-long-term-roadmap.md
docs/reference/current-services.md
docs/reference/analytics-metrics.md
docs/reference/analytics-data-contracts.md
docs/reference/analytics-source-inventory.md
docs/reference/analytics-correlation-matrix.md
docs/reference/analytics-dashboard-catalog.md
docs/architecture/data-platform.md
docs/threat-models/data-platform.md
docs/evidence/c2-entry-gate.md
docs/evidence/c2-ledger-slice.md
docs/evidence/c2-final-acceptance.md
docs/runbooks/analytics-*.md
```

---

## 41. Proposed repository changes

Expected areas:

```text
analytics/
docker-compose.yml
Makefile

migrations/ledger/
migrations/payin/
migrations/payout/

configs or setup for source replication users/publications
monitoring/prometheus/
monitoring/grafana/

docs/roadmap/
docs/reference/
docs/architecture/
docs/threat-models/
docs/runbooks/
docs/evidence/
```

Source migrations are permitted only for:

- least-privilege replication setup;
- explicit publications where managed through repository tooling;
- deterministic correlation fields;
- safe provider-attempt metadata separation;
- indexes required by bounded source reconciliation.

No source schema change is justified merely to simplify one dashboard query.

---

## 42. Final verification commands

T0 must replace examples with repository-canonical commands.

Expected final sequence:

```bash
make contracts
make build-all
make test
make lint
make verify-full

make analytics-config-check
make analytics-up-core
make analytics-health
make analytics-connectors-validate
make analytics-connectors-apply
make analytics-clickhouse-migrate
make analytics-dbt-build
make analytics-reconcile
make analytics-e2e

git diff --check
git status --short
```

Separate destructive/manual gate:

```bash
make analytics-chaos
```

Optional UI gate:

```bash
make analytics-up-ui
```

Final evidence records:

- command;
- commit;
- environment;
- source dataset;
- source cutoffs;
- component versions;
- result;
- known residual risk.

---

## 43. Final definition of done

C2 is complete only when all required checks below pass.

### Architecture

- [ ] Analytics is one-way and read-only.
- [ ] No product path depends on C2.
- [ ] RabbitMQ remains the operational event bus.
- [ ] No application service was added without evidence.
- [ ] Existing reporting authority remains documented.

### CDC

- [ ] Source publications are explicit.
- [ ] Replication users are least privilege.
- [ ] Snapshot and streaming are proven.
- [ ] Connector restart is proven.
- [ ] Delete behavior is proven.
- [ ] Schema-change policy is enforced.
- [ ] WAL retention is monitored.
- [ ] Prohibited columns are absent.

### Warehouse

- [ ] Raw, staging, core, mart, and control layers exist.
- [ ] Transport and logical deduplication are deterministic.
- [ ] Exact money remains integer minor units.
- [ ] Time semantics are explicit.
- [ ] Incremental and full-refresh paths work.
- [ ] BI role cannot read raw.
- [ ] Retention is configured and tested.

### Financial correctness

- [ ] Ledger debit equals credit.
- [ ] Ledger facts reconcile to source at safe cutoff.
- [ ] Fee revenue derives from posted fee-account entries.
- [ ] Reversal/refund behavior is correct.
- [ ] Fee quotes are not counted as revenue.
- [ ] Payin/Payout facts link deterministically to Ledger.
- [ ] Currencies are not implicitly combined.

### Unit economics

- [ ] Vendor cost is labeled modeled.
- [ ] Cost model is versioned.
- [ ] Contribution margin formula is documented.
- [ ] Dashboard does not label modeled margin as profit.
- [ ] Cost and revenue currencies match.

### Privacy and security

- [ ] Source-column allowlist is reviewed.
- [ ] Raw payload/destination/credential fields are excluded.
- [ ] Identity pseudonymization is deterministic.
- [ ] Salt and credentials are not in Git.
- [ ] ClickHouse/Redpanda/Connect bind safely.
- [ ] Metabase is read-only.
- [ ] Sensitive-column scan passes.
- [ ] Threat model and incident runbook exist.

### Reliability

- [ ] Connect outage is recovered.
- [ ] Redpanda outage is recovered.
- [ ] ClickHouse outage is recovered.
- [ ] Source restart is recovered.
- [ ] Duplicate event does not duplicate facts.
- [ ] Full rebuild reaches reconciled totals.
- [ ] OLTP remains green through analytics failures.

### Operations

- [ ] Freshness, lag, WAL, dbt, and reconciliation metrics exist.
- [ ] Alerts link to runbooks.
- [ ] Metric cardinality is bounded.
- [ ] Dashboard freshness is visible.
- [ ] Critical reconciliation failure is visible.
- [ ] Local resource baseline is recorded.

### Documentation and evidence

- [ ] Metric catalog is complete.
- [ ] Data contracts are complete.
- [ ] Lineage is generated.
- [ ] Dashboard catalog is complete.
- [ ] Final clean-tree gate passes.
- [ ] Chaos evidence is recorded.
- [ ] Residual risks are explicit.
- [ ] Roadmap reflects reality.
- [ ] Plan is archived only after evidence is linked.

---

## 44. Final evidence log

Fill during execution.

| Evidence | Commit / artifact | Result | Notes |
|---|---|---:|---|
| C2 entry gate |  |  |  |
| Source/privacy review |  |  |  |
| Resource baseline |  |  |  |
| Ledger snapshot |  |  |  |
| Ledger streaming |  |  |  |
| Connector restart |  |  |  |
| Redpanda outage |  |  |  |
| ClickHouse outage |  |  |  |
| Source PostgreSQL restart |  |  |  |
| Duplicate event |  |  |  |
| Schema-compatible addition |  |  |  |
| Schema-incompatible change |  |  |  |
| Sensitive-column exclusion |  |  |  |
| Ledger debit-credit reconciliation |  |  |  |
| Source-to-warehouse reconciliation |  |  |  |
| Fee-revenue reconciliation |  |  |  |
| Payin-to-Ledger reconciliation |  |  |  |
| Payout-to-Ledger reconciliation |  |  |  |
| dbt full refresh |  |  |  |
| Warehouse rebuild |  |  |  |
| Dashboard-to-mart verification |  |  |  |
| Performance baseline |  |  |  |
| Final clean-tree gate |  |  |  |

---

## 45. Residual risks to track

The completed local C2 still does not prove:

- managed-cloud connector operations;
- production replication-slot governance;
- multi-node broker durability;
- multi-node ClickHouse replication;
- disaster recovery;
- production-scale schema registry;
- real vendor costs;
- audited revenue recognition;
- regulatory reporting correctness;
- privacy-law compliance;
- data residency;
- production BI access governance;
- long-term warehouse cost;
- production data-volume capacity;
- exact real-time freshness under peak load.

These limitations must remain visible in README, dashboards, and portfolio
claims.

---

## 46. Recommended immediate next action

Start with T0 and T1, then implement only the Ledger vertical slice.

The implementation order is:

```text
source/privacy inventory
        ->
metric and correlation contracts
        ->
analytics-core profile
        ->
Ledger connector
        ->
raw ingestion
        ->
Ledger staging/core facts
        ->
debit-credit reconciliation
        ->
one freshness-aware dashboard
```

This sequence proves the difficult boundaries before C2 expands into Payin,
Payout, vendor performance, and modeled unit economics.
