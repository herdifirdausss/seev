import { ledgerJson, semantic, iterationOptions, handleSummary } from '../lib/common.js';
import { seedDisbursementBatches, parseDurationSeconds } from '../lib/seed.js';

export { handleSummary };

export const options = iterationOptions();

// Not one of the canonical W1-W7 scenarios (docs/performance/reports/
// 2026-xx-baseline.md §5) — added to test a distinct hot-account shape B1
// never covered: W5 tests STEADY webhook traffic against a shared vendor
// settlement account; this tests a BURST of many disbursement batches
// (payroll/EOD-cutoff style) released together against the platform's
// singleton settlement[platform][currency] account
// (services/ledger/migrations/000015_disbursement.up.sql) — a different account,
// reached via a different code path (services/ledger/internal/ledger/disbursement),
// that no existing scenario exercises. One workload unit = one batch fully
// drained via repeated POST /run calls until done:true; setup() pre-creates
// one batch per planned iteration so the load phase measures burst
// concurrency on the settlement row, not batch-creation cost.
export function setup() {
  const rate = Number(__ENV.LOAD_RATE || 1);
  const seconds = parseDurationSeconds(__ENV.LOAD_DURATION || '1m');
  const batchCount = Math.max(Math.ceil(rate * seconds), 1);
  return seedDisbursementBatches(batchCount, 100, 1000);
}

// runDisbursement (services/ledger/internal/ledger/disbursement) processes at most
// 500 pending/failed items per call and reports done:true once the batch
// is fully resolved — itemsPerBatch (100, seed.js) is well under that cap,
// so one call normally suffices; the bounded retry loop only guards
// against a partial/false response, not expected multi-call draining.
const MAX_RUN_ATTEMPTS = 5;

export default function (data) {
  const batchId = data.batchIds[__ITER % data.batchIds.length];
  let done = false;
  for (let attempt = 0; attempt < MAX_RUN_ATTEMPTS && !done; attempt++) {
    // See tests/load/lib/seed.js's comment on seedDisbursementBatches for
    // why this is /api/v1/ledger/admin/... not /api/v1/admin/ledger/... —
    // the latter 404s due to a routing bug in services/ledger/cmd/ledger/main.go.
    const resp = ledgerJson('POST', `/api/v1/ledger/admin/disbursements/${batchId}/run`, {}, {}, data.adminToken);
    const body = semantic(resp, 'disbursement burst run', [200]);
    done = Boolean(body && body.data && body.data.done);
  }
}
