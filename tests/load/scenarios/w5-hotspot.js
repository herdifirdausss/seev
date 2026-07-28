import { json, semantic, stableKey, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('hotspot');
  const gateway = __ENV.LOAD_GATEWAY || 'mockvendor';
  semantic(json('POST', '/api/v1/topup', { amount: '1000', gateway }, { 'Idempotency-Key': key }), 'hotspot topup', [201, 400, 401, 403, 409]);
}
