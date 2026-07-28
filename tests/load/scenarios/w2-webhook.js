import { semantic, signature, stableKey, iterationOptions, vendorBaseURL, handleSummary } from '../lib/common.js';
import http from 'k6/http';

export { handleSummary };

export const options = iterationOptions();
export default function () {
  const key = stableKey('webhook');
  const body = JSON.stringify({ event_id: key, external_ref: __ENV.LOAD_TOPUP_REFERENCE || key, user_id: __ENV.LOAD_USER_ID || '00000000-0000-0000-0000-000000000002', amount: '1000', currency: 'IDR', occurred_at: new Date().toISOString(), type: 'payment.settled' });
  const headers = { 'Content-Type': 'application/json', 'X-Mock-Signature': signature(__ENV.LOAD_VENDOR_SECRET || 'synthetic-vendor-secret', body), 'X-Request-ID': key };
  const response = http.post(`${vendorBaseURL}/webhooks/mockvendor`, body, { headers, tags: { workload: 'W2', operation: 'webhook' } });
  semantic(response, 'webhook', [200, 202, 400, 401, 404, 409]);
}
