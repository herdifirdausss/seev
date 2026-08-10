import type { WorkflowStatus } from '@seev/domain';
import { isUnknownStatus } from '@seev/domain';
import type { UnknownStatus } from '@seev/domain';

interface StatusBadgeProps {
  status: WorkflowStatus | UnknownStatus;
  className?: string;
}

const STATUS_STYLES: Record<WorkflowStatus, { bg: string; label: string }> = {
  pending: { bg: 'bg-yellow-100 text-yellow-800', label: 'Pending' },
  processing: { bg: 'bg-blue-100 text-blue-800', label: 'Processing' },
  posted: { bg: 'bg-indigo-100 text-indigo-800', label: 'Posted' },
  settled: { bg: 'bg-green-100 text-green-800', label: 'Settled' },
  failed: { bg: 'bg-red-100 text-red-800', label: 'Failed' },
  reversed: { bg: 'bg-gray-100 text-gray-800', label: 'Reversed' },
  cancelled: { bg: 'bg-gray-100 text-gray-700', label: 'Cancelled' },
  expired: { bg: 'bg-orange-100 text-orange-800', label: 'Expired' },
};

// Status is understandable without color — text label is always present.
// Unknown status renders an explicit fallback, never success.
export function StatusBadge({ status, className = '' }: StatusBadgeProps) {
  if (isUnknownStatus(status)) {
    return (
      <span
        className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium bg-gray-100 text-gray-600 ${className}`}
        aria-label={`Unknown status: ${status.raw}`}
      >
        Unknown
      </span>
    );
  }

  const { bg, label } = STATUS_STYLES[status];
  return (
    <span
      className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${bg} ${className}`}
      aria-label={`Status: ${label}`}
    >
      {label}
    </span>
  );
}
