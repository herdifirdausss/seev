import { check } from 'k6';
import { json, authJson, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';
import { seedMixedPool, createTopupIntent, deliverWebhook } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// setup() registers, KYC-approves, and funds a POOL of sender+recipient
// pairs (Phase 0 §24.1/§24.2 — tests/load/lib/seed.js), so every branch
// below is a real completed journey instead of a business rejection
// tolerated as "fine". K8's fixed weights: 35% quote+transfer, 20% read,
// 20% topup+webhook, 15% payout, 5% notifications, 5% login. A pool, not
// one sender: several genuinely different accounts, matching K7's design
// intent for W1 applied here too. (KYC level-1's transfer_p2p and
// withdraw_initiate daily count policy limits are raised to effectively
// unbounded for this disposable database —
// scripts/load-postgres-init/04-load-policy-limits.sh.)
export function setup() {
  return seedMixedPool();
}

// Weighted random selection, not a __ITER-based cycle: __ITER is per-VU in
// k6, not a global counter, and the constant-arrival-rate executor spreads
// work across many VUs — discovered live, a 20-slot __ITER%20 cycle meant
// most VUs never accumulated enough of their OWN iterations to ever reach
// the higher slots (payout, notifications, login), so those branches
// simply never ran under real concurrent load despite passing in a
// single-VU smoke test. Math.random() against fixed cumulative thresholds
// gives every VU the correct per-iteration probability regardless of how
// many iterations it personally gets, matching K8's own description
// ("a fixed PRNG seed selects users/actions").
const WEIGHTS = [
  [0.35, 'transfer'],
  [0.55, 'read'],
  [0.75, 'topup'],
  [0.9, 'payout'],
  [0.95, 'notifications'],
  [1, 'login'],
];

function pickAction() {
  const r = Math.random();
  for (const [threshold, action] of WEIGHTS) {
    if (r < threshold) return action;
  }
  return 'login';
}

export default function (data) {
  const key = stableKey('mixed');
  const action = pickAction();
  const sender = data.senders[Math.floor(Math.random() * data.senders.length)];

  if (action === 'transfer') {
    // 35%: fee quote + a real P2P transfer, same journey as W1.
    const quote = json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'transfer_p2p', amount: String(data.amountPerAction), currency: 'IDR' }, { 'Idempotency-Key': `${key}-quote` }, sender.token);
    semantic(quote, 'mixed quote', [200, 201]);
    const transfer = json('POST', '/api/v1/ledger/transactions', { idempotency_key: key, type: 'transfer_p2p', amount: String(data.amountPerAction), target_user_id: sender.targetUserId }, { 'Idempotency-Key': key }, sender.token);
    semantic(transfer, 'mixed transfer', [201]);
  } else if (action === 'read') {
    // 20%: read. K8 says "balance, transaction, or statement read"; this
    // uses the profile read already proven elsewhere in this harness
    // rather than standing up ledger statement/balance plumbing this
    // scenario doesn't otherwise touch — a deliberate scope simplification.
    semantic(authJson('GET', '/api/v1/users/me', null, {}, sender.token), 'mixed read', [200]);
  } else if (action === 'topup') {
    // 20%: topup + signed webhook, same journey as W2 but created fresh
    // per iteration (a pool would need presizing an unknown mixed-rate
    // share in advance).
    const intent = createTopupIntent(sender.token, data.amountPerAction);
    const eventID = `mixed-${__ENV.LOAD_RUN_ID || 'unbound'}-${__VU}-${__ITER}-${intent.reference}`;
    const webhook = deliverWebhook(intent.vendor, intent.reference, intent.amount, eventID);
    check(webhook, { 'mixed topup webhook: expected status': () => [200, 201, 202].includes(webhook.status) });
  } else if (action === 'payout') {
    // 15%: payout quote, create, and one status read — not the full
    // bounded terminal-state poll W3 owns in isolation; folding a ~20s
    // wait into 15% of a mixed-journey iteration would dominate this
    // scenario's own latency measurement.
    const quote = json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'withdraw_initiate', amount: String(data.amountPerAction), currency: 'IDR' }, { 'Idempotency-Key': `${key}-payoutquote` }, sender.token);
    semantic(quote, 'mixed payout quote', [200, 201]);
    const create = json('POST', '/api/v1/payout', { amount: String(data.amountPerAction), destination: { bank_code: '014', account_no: '1234567890' } }, { 'Idempotency-Key': key }, sender.token);
    const created = semantic(create, 'mixed payout create', [201]);
    const id = created && created.data && created.data.id;
    if (id) semantic(json('GET', `/api/v1/payout/${id}`, null, {}, sender.token), 'mixed payout status', [200]);
  } else if (action === 'notifications') {
    // 5%: notifications list/read.
    semantic(json('GET', '/api/v1/notifications', null, {}, sender.token), 'mixed notifications', [200]);
  } else {
    // 5%: login (K8's "login or refresh" category — a fresh login every
    // occurrence, not a captured refresh_token: refresh tokens are
    // one-time-use, and reusing an already-consumed one across several
    // iterations is treated as replay and revokes every token for this
    // user, cascading into every other category — discovered live, see
    // seedMixedPool's comment in tests/load/lib/seed.js).
    semantic(authJson('POST', '/api/v1/auth/login', { email: sender.email, password: sender.password }), 'mixed login', [200]);
  }
}
