import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('p2p');
  const quote = json('POST', '/api/v1/ledger/fees/quote', { transaction_type: 'transfer_p2p', amount: '1000', currency: 'IDR' }, { 'Idempotency-Key': `${key}-quote` });
  semantic(quote, 'p2p quote', [200, 401, 403]);
  const transfer = json('POST', '/api/v1/ledger/transactions', { idempotency_key: key, type: 'transfer_p2p', amount: '1000', target_user_id: __ENV.LOAD_TARGET_USER_ID || '00000000-0000-0000-0000-000000000002' }, { 'Idempotency-Key': key });
  semantic(transfer, 'p2p transfer', [201, 400, 401, 403, 409]);
}
