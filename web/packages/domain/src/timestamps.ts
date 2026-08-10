// ISO 8601 timestamp handling — always retain the original offset.
// Format with an explicit time zone; never rely on browser-relative phrases
// for financial or audit evidence.

export interface ParsedTimestamp {
  readonly iso: string;
  readonly epochMs: number;
}

export function parseTimestamp(raw: string): ParsedTimestamp {
  if (!raw || typeof raw !== 'string') {
    throw new Error('Timestamp must be a non-empty string');
  }
  const ms = Date.parse(raw);
  if (isNaN(ms)) {
    throw new Error(`Invalid timestamp: ${raw}`);
  }
  return { iso: raw, epochMs: ms };
}

export function formatTimestamp(
  ts: ParsedTimestamp,
  options: { timeZone: string; locale?: string } = { timeZone: 'UTC' },
): string {
  const fmt = new Intl.DateTimeFormat(options.locale ?? 'en-US', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    timeZone: options.timeZone,
    timeZoneName: 'short',
  });
  return fmt.format(ts.epochMs);
}
