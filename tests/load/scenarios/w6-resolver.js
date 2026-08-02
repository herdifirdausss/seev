import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';
import { seedResolverUser } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// B3's own experiment (docs/performance/reports/2026-xx-baseline.md §22):
// self-seeds one user (no funding/KYC needed — a fee quote never moves
// money) rather than requiring an operator-supplied SEEV_LOAD_TOKEN, the
// same self-seeding convention W1-W5/disbursement-burst already use.
export function setup() {
  return seedResolverUser();
}

export default function (data) {
  const key = stableKey('resolver');
  // LOAD_KEY_CARDINALITY bounds the distinct (transaction_type, amount)
  // combinations this run repeats — currency/gateway/user stay fixed, so a
  // small cardinality simulates the "repeated-key" traffic shape B3's own
  // activation criteria ("repeated-key cacheability is at least 80%")
  // requires evidence for. amount is folded into fee_rules' resolution key
  // only insofar as ResolveRule ignores it entirely (the SQL query never
  // filters on amount) — varying it here exercises the SAME resolver cache
  // key repeatedly while still producing distinct quote amounts, matching
  // a real caller's actual request shape.
  const cardinality = Number(__ENV.LOAD_KEY_CARDINALITY || 10);
  const amount = String(1000 + (__ITER % cardinality));
  // createQuote (internal/ledger/transport/http.go) returns 201 Created on
  // success, not 200 — this scenario was written but never actually run
  // until this session (§22's own "Experiment never run"), so the wrong
  // status code went uncaught; confirmed live (every request 201, all
  // counted as unexpected_failures against the old [200,...] allowlist).
  semantic(json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'transfer_p2p', amount, currency: 'IDR' }, { 'Idempotency-Key': key }, data.token), 'resolver quote', [201, 400, 401, 403]);
}
