import { semantic, iterationOptions, handleSummary } from '../lib/common.js';
import { seedWebhookPool, deliverWebhook } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// setup() pre-creates one pending topup intent per planned iteration
// (Phase 0 §24.2/§24.1 — tests/load/lib/seed.js) so the load phase measures
// webhook settlement, not intent-creation cost or manual seeding.
export function setup() {
  return seedWebhookPool();
}

// Every 10th iteration is an exact redelivery: same event_id, same
// reference as the delivery one iteration earlier in this same VU's own
// history (k6 iterations within a VU run strictly in order, so that prior
// delivery has always already happened). This is K7's "separately tagged
// 10% exact webhook redelivery stream" — real duplicate-delivery traffic,
// not a second unique event for the same intent.
const REDELIVERY_EVERY_N = 10;

function eventIDFor(vu, poolIndex) {
  return `w2-${__ENV.LOAD_RUN_ID || 'unbound'}-vu${vu}-i${poolIndex}`;
}

export default function (data) {
  const poolIndex = __ITER % data.intents.length;
  const isRedelivery = __ITER > 0 && __ITER % REDELIVERY_EVERY_N === REDELIVERY_EVERY_N - 1;
  const targetIndex = isRedelivery ? (__ITER - 1) % data.intents.length : poolIndex;
  const intent = data.intents[targetIndex];
  const eventID = eventIDFor(__VU, targetIndex);
  const response = deliverWebhook(intent.vendor, intent.reference, intent.amount, eventID);
  semantic(response, isRedelivery ? 'webhook redelivery' : 'webhook', [200, 201, 202]);
}
