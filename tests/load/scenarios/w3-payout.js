import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('payout');
  const quote = json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'withdraw_hold', amount: '1000', currency: 'IDR' }, { 'Idempotency-Key': `${key}-quote` });
  semantic(quote, 'payout quote', [200, 401, 403]);
  const create = json('POST', '/api/v1/payout', { idempotency_key: key, amount: '1000', currency: 'IDR', destination: { bank_code: '014', account_no: __ENV.LOAD_ACCOUNT_NO || '1234567890' } }, { 'Idempotency-Key': key });
  const payload = semantic(create, 'payout create', [201, 400, 401, 403, 409]);
  if (payload && payload.data && payload.data.id) semantic(json('GET', `/api/v1/payout/${payload.data.id}`, null), 'payout status', [200, 401, 403, 404]);
}
