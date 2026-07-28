import { json, semantic, iterationOptions, handleSummary } from '../lib/common.js';

export { handleSummary };

// W7 is a bounded read probe used alongside the seeded 100k/1m/5m ledger-size
// ladder. The row population is prepared by the disposable seed workflow; the
// scenario itself must not create arbitrary rows during a size measurement.
export const options = iterationOptions();
export default function () {
  const transactionID = __ENV.LOAD_TRANSACTION_ID || '00000000-0000-0000-0000-000000000001';
  semantic(json('GET', `/api/v1/ledger/transactions/${transactionID}`, null), 'ledger size read', [200, 401, 403, 404]);
}
