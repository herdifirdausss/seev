// Backend-owned status dimensions. The frontend maps known values to labels
// and icons; unknown values render a controlled fallback — never success.

export type WorkflowStatus =
  | 'pending'
  | 'processing'
  | 'posted'
  | 'settled'
  | 'failed'
  | 'reversed'
  | 'cancelled'
  | 'expired';

export type LedgerStatus = 'pending' | 'posted' | 'reversed' | 'failed';

export type DeliveryStatus = 'pending' | 'submitted' | 'delivered' | 'failed' | 'dead';

export type ReconciliationStatus = 'unmatched' | 'matched' | 'disputed' | 'resolved';

export type KycStatus = 'none' | 'pending' | 'approved' | 'rejected' | 'expired';

export type RiskLevel = 'low' | 'medium' | 'high' | 'critical';

export type SeverityLevel = 'info' | 'warning' | 'error' | 'critical';

// Explicit unknown sentinel — do not default to success.
export interface UnknownStatus {
  readonly kind: 'unknown';
  readonly raw: string;
}

export function unknownStatus(raw: string): UnknownStatus {
  return { kind: 'unknown', raw };
}

export function isUnknownStatus(value: unknown): value is UnknownStatus {
  return (
    typeof value === 'object' &&
    value !== null &&
    'kind' in value &&
    (value as Record<string, unknown>).kind === 'unknown'
  );
}

export interface StatusMeta {
  readonly label: string;
  readonly description: string;
  readonly isTerminal: boolean;
  readonly isFinal: boolean;
}

const WORKFLOW_STATUS_META: Record<WorkflowStatus, StatusMeta> = {
  pending: {
    label: 'Pending',
    description: 'Awaiting processing',
    isTerminal: false,
    isFinal: false,
  },
  processing: {
    label: 'Processing',
    description: 'Being processed',
    isTerminal: false,
    isFinal: false,
  },
  posted: { label: 'Posted', description: 'Recorded in ledger', isTerminal: false, isFinal: false },
  settled: { label: 'Settled', description: 'Funds settled', isTerminal: true, isFinal: true },
  failed: { label: 'Failed', description: 'Processing failed', isTerminal: true, isFinal: true },
  reversed: {
    label: 'Reversed',
    description: 'Transaction reversed',
    isTerminal: true,
    isFinal: true,
  },
  cancelled: {
    label: 'Cancelled',
    description: 'Cancelled before completion',
    isTerminal: true,
    isFinal: true,
  },
  expired: { label: 'Expired', description: 'Request expired', isTerminal: true, isFinal: true },
};

export function workflowStatusMeta(status: WorkflowStatus): StatusMeta {
  return WORKFLOW_STATUS_META[status];
}

export function parseWorkflowStatus(raw: string): WorkflowStatus | UnknownStatus {
  if (raw in WORKFLOW_STATUS_META) {
    return raw as WorkflowStatus;
  }
  return unknownStatus(raw);
}
