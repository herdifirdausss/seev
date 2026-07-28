import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('mixed');
  const selector = __ITER % 20;
  if (selector < 7) semantic(json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'transfer_p2p', amount: '1000', currency: 'IDR' }, { 'Idempotency-Key': key }), 'mixed quote', [200, 401, 403]);
  else if (selector < 11) semantic(json('POST', '/api/v1/topup', { amount: '1000' }, { 'Idempotency-Key': key }), 'mixed topup', [201, 400, 401, 403, 409]);
  else if (selector < 14) semantic(json('POST', '/api/v1/payout', { idempotency_key: key, amount: '1000', currency: 'IDR', destination: 'synthetic-destination' }, { 'Idempotency-Key': key }), 'mixed payout', [201, 400, 401, 403, 409]);
  else if (selector < 19) semantic(json('GET', '/api/v1/notifications', null), 'mixed notifications', [200, 401, 403]);
  else semantic(json('GET', '/api/v1/users/me', null), 'mixed profile', [200, 401, 403]);
}
