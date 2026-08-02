import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';
import { seedP2PPair } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// setup() runs once, registering+funding a POOL of real sender/recipient
// pairs (lib/seed.js — Phase 0 §24.1), so every VU transfers against an
// actually-funded account instead of relying on a manually pre-supplied
// SEEV_LOAD_TOKEN/SEEV_LOAD_TARGET_USER_ID. A pool, not one pair: K7's own
// design intent ("W1 uses disjoint funded account pairs") — concurrent
// transfers across several genuinely different accounts, not one pair hit
// repeatedly. (KYC level-1's transfer_p2p daily count policy limit is
// raised to effectively unbounded for this disposable database —
// scripts/load-postgres-init/04-load-policy-limits.sh — so pool size here
// is purely about realism, not dodging that limit.)
export function setup() {
  return seedP2PPair();
}

export default function (data) {
  const key = stableKey('p2p');
  const pair = data.pairs[Math.floor(Math.random() * data.pairs.length)];
  const quote = json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'transfer_p2p', amount: '1000', currency: 'IDR' }, { 'Idempotency-Key': `${key}-quote` }, pair.token);
  semantic(quote, 'p2p quote', [200, 201]);
  const transfer = json('POST', '/api/v1/ledger/transactions', { idempotency_key: key, type: 'transfer_p2p', amount: '1000', target_user_id: pair.targetUserId }, { 'Idempotency-Key': key }, pair.token);
  semantic(transfer, 'p2p transfer', [201]);
}
