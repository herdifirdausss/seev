import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('resolver');
  const cardinality = Number(__ENV.LOAD_KEY_CARDINALITY || 10);
  const amount = String(1000 + (__ITER % cardinality));
  semantic(json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'transfer_p2p', amount, currency: 'IDR' }, { 'Idempotency-Key': key }), 'resolver quote', [200, 400, 401, 403]);
}
