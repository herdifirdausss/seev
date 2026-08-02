import { semantic, iterationOptions, handleSummary } from '../lib/common.js';
import { seedHotspotPool, deliverWebhook } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// LOAD_HOTSPOT_VARIANT selects the K11 experiment arm: "one" (default) is
// the contended condition — every webhook in the run settles against the
// same system settlement account row. "two" is the control — half the
// pool settles against a second, independently routed system account.
// B1 activation compares throughput/p95 between alternating runs of each.
const VARIANT = __ENV.LOAD_HOTSPOT_VARIANT === 'two' ? 'two' : 'one';

export function setup() {
  return seedHotspotPool(VARIANT);
}

export default function (data) {
  const intent = data.intents[__ITER % data.intents.length];
  const eventID = `w5-${__ENV.LOAD_RUN_ID || 'unbound'}-${__VU}-${__ITER}-${intent.reference}`;
  const response = deliverWebhook(intent.vendor, intent.reference, intent.amount, eventID);
  semantic(response, `hotspot webhook (${data.variant}, ${intent.vendor})`, [200, 201, 202]);
}
