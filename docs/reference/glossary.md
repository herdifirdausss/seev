# Seev Glossary

> [Documentation home](../README.md) · [Reference](README.md)

This glossary defines project and financial terms in plain English. Technical
documents should link here instead of inventing a different definition.

## Accounting and product terms

### Account

A named place in the Ledger that holds one side of accounting entries. It is
not the same thing as a user's login account.

### Append-only

Existing records are not silently changed or deleted. A correction is written
as a new record so the full history remains visible.

### Balance projection

A fast, stored summary of an account's balance. It can be rebuilt from the
permanent ledger entries if necessary.

### Compensating transaction

A new accounting transaction that corrects the effect of an earlier one while
leaving the original history visible.

### Double-entry accounting

Every money movement has equal debit and credit sides. This lets the system
prove that money was neither created nor lost inside the Ledger.

### Fee quote

A stored price promise created before a transfer or withdrawal. It records the
operation, amount, currency, user, fee, and expiry so the charged fee cannot
silently differ from the price previously shown.

### Hold

Money reserved for a possible withdrawal. It cannot be spent elsewhere, but
the payout is not yet final.

### Fraud screening

Checking an identity or money-related action against risk rules before or
after processing. Fraud can block at documented boundaries but never edits a
wallet balance itself.

### Intent correlation

Matching an outside payment message to the internal Payin or Payout record
that expected it. The internal record—not a vendor-supplied user identifier—
decides which workflow and user are involved.

### KYC

“Know Your Customer”: checks used to establish a user's identity. Seev uses
levels that control which actions and limits are available.

### Maker-checker

A two-person approval rule for sensitive operator actions. One authorized
person proposes the action and a different authorized person approves it.

### Ledger

The permanent source of truth for money movement. In Seev, a balance change is
real only after the Ledger posts a balanced transaction.

### Minor units

Whole-number currency units used to avoid floating-point mistakes. For IDR,
`100000` represents IDR 100,000; for a currency with cents, the minor unit is
usually one cent.

### Payin or top-up

Money entering a user's wallet through an external payment vendor.

### Payout or withdrawal

Money leaving a user's wallet through an external payout vendor.

### Settlement

The point at which a payment or withdrawal is treated as successfully
completed according to the owning domain's rules.

### Settlement rail

The external payment path or system account representing money sent toward a
bank or payout network. In this repository, real bank movement is mocked.

### Top-up intent

Payin's stored expectation that a particular user plans to add a particular
amount and currency through a selected vendor. An intent is not money.

### Vendor

An outside payment company or bank integration. The repository uses mock
vendors for local learning.

## Reliability terms

### Assurance finding

A durable record that Assurance creates when independently owned service
records appear to disagree. A finding starts investigation; it does not change
money by itself.

### At-least-once delivery

A message may be delivered more than once so it is not silently lost. The
receiver must make repeated delivery safe.

### Circuit breaker

A guard that temporarily stops calls to a repeatedly failing dependency. It
allows recovery and prevents every new request from repeating the same slow
failure.

### Event

A durable message announcing that a fact has already been recorded, such as a
Ledger transaction being posted. Consumers can react later and independently.

### Durable command

A stored instruction for background work. Because it is saved before network
dispatch, another worker can continue it after a crash.

### Idempotency key

A stable identifier for one intended operation. Reusing it returns or
continues the original result instead of performing the operation twice.

### Outbox

A database table written in the same transaction as important business data.
A worker later publishes its rows as events, so a temporary broker failure
does not lose the event.

### Reconciliation

Comparing Seev's records with an external report to find missing, duplicated,
or mismatched transactions.

### Recovery worker

A background process that finds durable unfinished work and safely continues
it after a crash or temporary dependency failure.

### Retry

Trying an operation again after a temporary failure. Retries are safe only
when the operation is idempotent or otherwise protected from duplication.

### State machine

A defined list of statuses and the transitions allowed between them. It stops
an operation from jumping into an impossible or unsafe state.

### Uncertain state

A visible status used when Seev cannot yet prove whether an outside operation
succeeded or failed. Recovery gathers evidence instead of guessing.

## Architecture and security terms

### API

A defined way for one program to request work or data from another program.

### Backend

The part of an application that users usually do not see. It receives requests,
applies rules, communicates with other systems, and stores durable records. A
phone or web interface is commonly called the frontend.

### Authentication

Proving who a person or service is. A password, token, or service certificate
can participate in authentication.

### Authorization

Deciding what an authenticated person or service is allowed to do.

### BFF

“Backend for Frontend”: a service designed around the needs of one user
interface. Seev's Admin BFF supports the operator console.

### Container

A packaged way to run a program with its required files and configuration.
Docker Compose starts Seev's local service containers.

### CSRF protection

A browser security control that helps prevent another website from tricking a
logged-in operator's browser into sending an unwanted action.

### Database

Organized durable storage that remains after a request or process ends. Each
Seev service owns a separate PostgreSQL database.

### Deployment

A released and running copy of software in an environment. A service boundary
creates independent deployment work in addition to a code boundary.

### Callback or webhook

An HTTP request an outside system sends to report that something happened.
Because it arrives from outside, it must be authenticated, validated, and safe
to receive more than once.

### Fail-closed

Reject or pause an action when a required safety dependency is unavailable.
This favors safety over availability.

### Fail-open

Continue an action when an optional dependency is unavailable, while recording
the degradation. This favors availability and is only safe for explicitly
chosen checks.

### Gateway

The public front door that accepts end-user wallet requests and connects them
to internal services. In the current implementation it also receives Payin
vendor callbacks; plan 54 proposes moving that vendor boundary elsewhere.

### gRPC

The typed request protocol used for most internal service-to-service calls in
Seev.

### HMAC signature

A value calculated from a message and a shared secret. It helps prove that a
vendor callback was created by someone who knows that secret and that its body
was not changed in transit.

It is a digital proof, not a handwritten signature. It authenticates the
message but does not replace matching the message to an expected Payin or
Payout record.

### Operator

An authorized person who monitors or administers the system. Sensitive
operator actions remain subject to roles, audit logs, and maker-checker rules.

### Process

A currently running program. One service can have one or more processes, while
the service name describes the responsibility rather than a particular copy.

### Repository

Depending on context, this word has two meanings. The public Seev repository
is the complete collection of code, documentation, and configuration. Inside
the Go code, a repository component is the layer that reads and writes a
service's database.

### Rate limit

A bound on how many requests a caller may make in a time period. It reduces
abuse and protects capacity but does not replace authentication.

### Request ID

An identifier carried across services, logs, traces, stored records, and
events so one request can be followed end to end. It does not authorize the
request or deduplicate a money movement.

### mTLS

Mutual TLS: both sides of an internal network connection present certificates
and verify each other's allowed service identity.

### SPIFFE-style URI SAN

The URI inside a service certificate that Seev uses as the service's internal
identity, such as `spiffe://seev/payin`. Seev does not use the certificate's
Common Name as that identity.

### Service

A separately started program with a focused responsibility and its own data.
Services communicate through explicit APIs or events instead of reading each
other's database.

### Source of truth

The authoritative place used to decide a fact when copies or summaries
disagree. The Ledger is the source of truth for money movement, while Payin
and Payout own their business workflow states.

### Database transaction

A group of database writes that either all succeed or all roll back. This is
different from a financial transaction, although a financial transaction is
often recorded inside one database transaction.

### VendorService

A target service proposed in plan 54. It would own direct vendor connectivity
while Payin and Payout continue to own intent validation and business state.
It is not part of the current runtime.
