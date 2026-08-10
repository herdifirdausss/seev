import type { Metadata } from 'next';
import type { TransactionEvidence } from '@seev/domain';
import { fixtures } from '@seev/testing';
import { hasBalancedEntries, isUnknownStatus, formatTimestamp } from '@seev/domain';
import { format } from '@seev/money';

export const metadata: Metadata = {
  title: 'Transaction Evidence Reference — Seev Wallet (Fixture)',
};

// This screen consumes only the reviewed fixture contract.
// It is intentionally linked from web/README.md only — not from product navigation.
// All data is synthetic; no live backend is required.
export default function TransactionReferencePage() {
  const tx: TransactionEvidence = fixtures.settled;
  const balanced = hasBalancedEntries(tx.entries);

  return (
    <div className="max-w-3xl mx-auto p-8">
      {/* Fixture mode indicator — mandatory per K16 */}
      <div
        role="banner"
        aria-label="Fixture mode — synthetic data only"
        className="mb-6 bg-amber-50 border border-amber-300 rounded p-3 text-sm text-amber-900 font-medium"
      >
        ⚠ Fixture mode — data is synthetic and does not reflect live balances or transactions
      </div>

      <h1 className="text-2xl font-semibold mb-2">Transaction Evidence</h1>
      <p className="text-sm text-gray-500 mb-6">
        This screen demonstrates how workflow truth and ledger truth answer different questions.
        Neither dimension collapses to a single success or failure flag.
      </p>

      {/* Identity */}
      <section aria-labelledby="identity-heading" className="mb-6">
        <h2 id="identity-heading" className="text-lg font-medium mb-3">
          Identity
        </h2>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Transaction ID</dt>
          <dd className="font-mono text-xs break-all">{tx.id}</dd>
          <dt className="text-gray-500">Request ID</dt>
          <dd className="font-mono text-xs">{tx.requestId ?? '—'}</dd>
          <dt className="text-gray-500">Type</dt>
          <dd>{tx.type}</dd>
          <dt className="text-gray-500">Amount</dt>
          <dd>{format(tx.amount, { locale: 'id-ID' })}</dd>
          <dt className="text-gray-500">Created</dt>
          <dd>{formatTimestamp(tx.createdAt, { timeZone: 'Asia/Jakarta', locale: 'en-US' })}</dd>
          {tx.updatedAt && (
            <>
              <dt className="text-gray-500">Updated</dt>
              <dd>
                {formatTimestamp(tx.updatedAt, { timeZone: 'Asia/Jakarta', locale: 'en-US' })}
              </dd>
            </>
          )}
        </dl>
      </section>

      {/* Status dimensions */}
      <section aria-labelledby="status-heading" className="mb-6">
        <h2 id="status-heading" className="text-lg font-medium mb-3">
          Status
        </h2>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Workflow</dt>
          <dd>
            <span
              className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${tx.workflowStatus === 'settled' ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-700'}`}
              aria-label={`Workflow status: ${isUnknownStatus(tx.workflowStatus) ? 'unknown' : tx.workflowStatus}`}
            >
              {isUnknownStatus(tx.workflowStatus) ? 'Unknown' : tx.workflowStatus}
            </span>
          </dd>
          <dt className="text-gray-500">Ledger</dt>
          <dd>
            <span className="inline-block px-2 py-0.5 rounded text-xs font-medium bg-indigo-100 text-indigo-800">
              {tx.ledgerStatus === undefined
                ? '—'
                : isUnknownStatus(tx.ledgerStatus)
                  ? 'Unknown'
                  : tx.ledgerStatus}
            </span>
          </dd>
          {tx.deliveryStatus !== undefined && (
            <>
              <dt className="text-gray-500">Delivery</dt>
              <dd>
                <span className="inline-block px-2 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-800">
                  {isUnknownStatus(tx.deliveryStatus) ? 'Unknown' : tx.deliveryStatus}
                </span>
              </dd>
            </>
          )}
        </dl>
      </section>

      {/* Ledger entries */}
      <section aria-labelledby="entries-heading" className="mb-6">
        <h2 id="entries-heading" className="text-lg font-medium mb-1">
          Ledger Entries{' '}
          <span className={`text-sm font-normal ${balanced ? 'text-green-700' : 'text-red-700'}`}>
            ({balanced ? 'balanced' : 'unbalanced — fixture data inconsistency'})
          </span>
        </h2>
        <p className="text-xs text-gray-400 mb-3">
          Balanced verification above is an educational display only. The Ledger service&apos;s own
          constraint trigger is the authoritative proof.
        </p>
        <div className="overflow-x-auto">
          <table className="w-full text-sm border-collapse" aria-label="Ledger entries">
            <caption className="sr-only">Debit and credit entries for this transaction</caption>
            <thead>
              <tr className="bg-gray-50 text-left">
                <th scope="col" className="px-3 py-2 font-medium text-gray-700">
                  Account
                </th>
                <th scope="col" className="px-3 py-2 font-medium text-gray-700">
                  Direction
                </th>
                <th scope="col" className="px-3 py-2 font-medium text-gray-700 text-right">
                  Amount
                </th>
              </tr>
            </thead>
            <tbody>
              {tx.entries.map((entry, i) => (
                <tr key={i} className="border-t border-gray-100">
                  <td className="px-3 py-2 font-mono text-xs">{entry.accountId}</td>
                  <td className="px-3 py-2">
                    <span
                      className={`px-1.5 py-0.5 rounded text-xs font-medium ${entry.direction === 'debit' ? 'bg-red-50 text-red-700' : 'bg-green-50 text-green-700'}`}
                      aria-label={entry.direction === 'debit' ? 'Debit' : 'Credit'}
                    >
                      {entry.direction}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-right font-mono">
                    {format(entry.amount, { locale: 'id-ID' })}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
