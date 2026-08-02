import { check, sleep } from 'k6';
import { json, semantic, stableKey, iterationOptions, handleSummary, unexpectedFailures } from '../lib/common.js';
import { seedPayoutSender } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// setup() registers, KYC-approves, and funds a POOL of senders for the
// whole run (Phase 0 §24.1 — tests/load/lib/seed.js), so every iteration
// is a real funded withdrawal instead of a business rejection tolerated as
// "fine". A pool, not one sender: several genuinely different accounts
// withdrawing concurrently, matching K7's design intent for W1 applied
// here too. (KYC level-1's withdraw_initiate daily count policy limit is
// raised to effectively unbounded for this disposable database —
// scripts/load-postgres-init/04-load-policy-limits.sh.)
export function setup() {
  return seedPayoutSender();
}

// Payout settlement is asynchronous (a durable command relay submits to the
// vendor and settles later — docs/roadmap/archive/45 T1), unlike W1's
// ledger transfer or W2's topup webhook, both of which settle synchronously
// within their own request. K7 defines W3's workload unit as "quote,
// create, AND terminal-status polling for one funded payout" — the poll is
// part of what's being measured here, not a separate correctness check
// bolted on afterward (contrast with W2, where polling is deliberately
// left out to avoid conflating two different latencies).
// scripts/lib.sh's wait_for_payout_status uses a ~20s budget (10 tries, 2s
// apart) for the same relay path, citing "the relay's own ~1s poll
// interval" — matched here rather than guessed (an earlier 5s budget
// during live debugging never once caught a settlement).
const TERMINAL_STATUSES = ['settled', 'failed', 'cancelled'];
const POLL_ATTEMPTS = 20;
const POLL_INTERVAL_SECONDS = 1;

export default function (data) {
  const key = stableKey('payout');
  const token = data.senders[Math.floor(Math.random() * data.senders.length)];
  // "withdraw_initiate" is the registered transaction type (ledger
  // processors withdraw_initiate.go / internal/ledger/transport/http.go:67)
  // — "withdraw_hold" was never a real type, discovered live once a real
  // token made it past the auth checks that had always masked this 400.
  const quote = json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'withdraw_initiate', amount: String(data.amountPerPayout), currency: 'IDR' }, { 'Idempotency-Key': `${key}-quote` }, token);
  semantic(quote, 'payout quote', [200, 201]);

  // createPayoutRequest (internal/handler/payout.go) only has amount,
  // destination, quote_id — no idempotency_key or currency field, and
  // response.Decode uses DisallowUnknownFields, so sending either causes a
  // hard 400 "invalid request body" (discovered live, same masking as
  // above). Idempotency is header-based, same as every other mutation here.
  const create = json('POST', '/api/v1/payout', { amount: String(data.amountPerPayout), destination: { bank_code: '014', account_no: '1234567890' } }, { 'Idempotency-Key': key }, token);
  const created = semantic(create, 'payout create', [201]);
  const id = created && created.data && created.data.id;
  if (!id) return;

  let status = created.data.status;
  for (let attempt = 0; attempt < POLL_ATTEMPTS && !TERMINAL_STATUSES.includes(status); attempt++) {
    sleep(POLL_INTERVAL_SECONDS);
    const poll = json('GET', `/api/v1/payout/${id}`, null, {}, token);
    const body = poll.status === 200 ? poll.json() : null;
    status = body && body.data && body.data.status;
  }
  const reachedTerminal = TERMINAL_STATUSES.includes(status);
  unexpectedFailures.add(reachedTerminal ? 0 : 1);
  check(status, { 'payout terminal status observed': () => reachedTerminal });
}
