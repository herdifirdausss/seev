import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('hotspot');
  semantic(json('POST', '/api/v1/topup', { amount: '1000' }, { 'Idempotency-Key': key }), 'hotspot topup', [201, 400, 401, 403, 409]);
}
