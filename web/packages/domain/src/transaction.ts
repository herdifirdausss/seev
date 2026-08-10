import type { AccountId, TransactionId, RequestId } from './identifiers.js';
import type {
  WorkflowStatus,
  LedgerStatus,
  DeliveryStatus,
  ReconciliationStatus,
  UnknownStatus,
} from './status.js';
import type { ParsedTimestamp } from './timestamps.js';
import type { Money } from '@seev/money';

export type EntryDirection = 'debit' | 'credit';

export interface LedgerEntry {
  readonly accountId: AccountId;
  readonly direction: EntryDirection;
  readonly amount: Money;
  readonly description?: string | undefined;
}

// Frontend domain model for a transaction evidence record.
// Workflow truth and Ledger truth answer different questions; neither collapses
// to a single invented SUCCESS flag.
export interface TransactionEvidence {
  readonly id: TransactionId;
  readonly requestId?: RequestId | undefined;
  readonly type: string;
  readonly amount: Money;
  readonly workflowStatus: WorkflowStatus | UnknownStatus;
  readonly ledgerStatus?: LedgerStatus | UnknownStatus | undefined;
  readonly deliveryStatus?: DeliveryStatus | UnknownStatus | undefined;
  readonly reconciliationStatus?: ReconciliationStatus | UnknownStatus | undefined;
  readonly entries: readonly LedgerEntry[];
  readonly createdAt: ParsedTimestamp;
  readonly updatedAt?: ParsedTimestamp | undefined;
}

// Pure balanced-entry check used ONLY to explain fixture evidence.
// Not an alternative Ledger authority — the Ledger's own balance trigger
// is the authoritative proof.
export function hasBalancedEntries(entries: readonly LedgerEntry[]): boolean {
  const byCurrency = new Map<string, bigint>();
  for (const entry of entries) {
    const current = byCurrency.get(entry.amount.currency) ?? 0n;
    const delta =
      entry.direction === 'credit' ? entry.amount.amountMinor : -entry.amount.amountMinor;
    byCurrency.set(entry.amount.currency, current + delta);
  }
  for (const net of byCurrency.values()) {
    if (net !== 0n) return false;
  }
  return true;
}
