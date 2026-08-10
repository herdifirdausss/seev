import type { Metadata } from 'next';
import type { TransactionEvidence } from '@seev/domain';
import { fixtures } from '@seev/testing';
import { hasBalancedEntries, isUnknownStatus, formatTimestamp } from '@seev/domain';
import { format } from '@seev/money';

export const metadata: Metadata = {
  title: 'Transaction Evidence Reference — Seev Operator (Fixture)',
};

// Compact operator reference proving shared components render denser evidence
// without importing any Wallet application code (K2 boundary test).
export default function OperatorTransactionReferencePage() {
  const tx: TransactionEvidence = fixtures.settled;
  const balanced = hasBalancedEntries(tx.entries);

  return (
    <div className="max-w-4xl mx-auto p-6">
      <div
        role="banner"
        aria-label="Fixture mode"
        className="mb-4 bg-amber-50 border border-amber-300 rounded p-2 text-xs text-amber-900 font-medium"
      >
        ⚠ Fixture mode — synthetic data
      </div>

      <div className="flex items-center justify-between mb-4">
        <h1 className="text-lg font-semibold">Transaction Evidence</h1>
        <span
          className={`px-2 py-0.5 rounded text-xs font-medium ${isUnknownStatus(tx.workflowStatus) ? 'bg-gray-100 text-gray-600' : tx.workflowStatus === 'settled' ? 'bg-green-100 text-green-800' : 'bg-yellow-100 text-yellow-800'}`}
        >
          {isUnknownStatus(tx.workflowStatus) ? 'Unknown' : tx.workflowStatus}
        </span>
      </div>

      <dl className="grid grid-cols-3 gap-x-4 gap-y-2 text-xs mb-4">
        <dt className="text-gray-500">TX ID</dt>
        <dd className="col-span-2 font-mono">{tx.id}</dd>
        <dt className="text-gray-500">Type</dt>
        <dd className="col-span-2">{tx.type}</dd>
        <dt className="text-gray-500">Amount</dt>
        <dd className="col-span-2 font-medium">{format(tx.amount, { locale: 'id-ID' })}</dd>
        <dt className="text-gray-500">Ledger</dt>
        <dd className="col-span-2">
          {tx.ledgerStatus === undefined
            ? '—'
            : isUnknownStatus(tx.ledgerStatus)
              ? 'Unknown'
              : tx.ledgerStatus}
        </dd>
        <dt className="text-gray-500">Created</dt>
        <dd className="col-span-2">
          {formatTimestamp(tx.createdAt, { timeZone: 'Asia/Jakarta' })}
        </dd>
      </dl>

      <div className="overflow-x-auto">
        <table className="w-full text-xs border-collapse" aria-label="Ledger entries">
          <caption className="sr-only">
            Debit and credit entries{' '}
            {balanced ? '(balanced — educational display only)' : '(unbalanced)'}
          </caption>
          <thead>
            <tr className="bg-gray-50 border-b border-gray-200">
              <th scope="col" className="px-2 py-1.5 text-left font-medium text-gray-600">
                Account
              </th>
              <th scope="col" className="px-2 py-1.5 text-left font-medium text-gray-600">
                Dir
              </th>
              <th scope="col" className="px-2 py-1.5 text-right font-medium text-gray-600">
                Amount
              </th>
            </tr>
          </thead>
          <tbody>
            {tx.entries.map((entry, i) => (
              <tr key={i} className="border-b border-gray-100">
                <td className="px-2 py-1.5 font-mono">{entry.accountId}</td>
                <td className="px-2 py-1.5">
                  <span className={entry.direction === 'debit' ? 'text-red-700' : 'text-green-700'}>
                    {entry.direction}
                  </span>
                </td>
                <td className="px-2 py-1.5 text-right font-mono">
                  {format(entry.amount, { locale: 'id-ID' })}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
