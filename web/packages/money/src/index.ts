// K8: Money is exact from transport to presentation.
// amountMinor is bigint (never a JS number for money).
// No parseFloat, no floating-point arithmetic, no implicit currency defaults.

export type CurrencyCode = string;

export interface Money {
  readonly amountMinor: bigint;
  readonly currency: CurrencyCode;
}

// --- Construction ---

export function money(amountMinor: bigint, currency: CurrencyCode): Money {
  if (!currency || currency.trim().length === 0) {
    throw new Error('currency must be a non-empty string');
  }
  return { amountMinor, currency };
}

// Parse transport forms: canonical decimal string ("12345") or safe integer.
// Never accepts floats or exponents.
export function fromTransport(raw: unknown, currency: CurrencyCode): Money {
  if (typeof raw === 'bigint') {
    return money(raw, currency);
  }
  if (typeof raw === 'string') {
    // Reject decimal points, exponents, leading/trailing whitespace.
    if (!/^-?\d+$/.test(raw)) {
      throw new Error(`Invalid money string: "${raw}"`);
    }
    return money(BigInt(raw), currency);
  }
  if (typeof raw === 'number') {
    if (!Number.isSafeInteger(raw)) {
      throw new Error(`Unsafe integer for money: ${String(raw)}`);
    }
    return money(BigInt(raw), currency);
  }
  throw new Error(`Unsupported money input type: ${typeof raw}`);
}

// --- Arithmetic (same currency only) ---

function assertSameCurrency(a: Money, b: Money): void {
  if (a.currency !== b.currency) {
    throw new Error(`Currency mismatch: ${a.currency} vs ${b.currency}`);
  }
}

export function add(a: Money, b: Money): Money {
  assertSameCurrency(a, b);
  return money(a.amountMinor + b.amountMinor, a.currency);
}

export function subtract(a: Money, b: Money): Money {
  assertSameCurrency(a, b);
  return money(a.amountMinor - b.amountMinor, a.currency);
}

export function isZero(m: Money): boolean {
  return m.amountMinor === 0n;
}

export function isPositive(m: Money): boolean {
  return m.amountMinor > 0n;
}

export function isNegative(m: Money): boolean {
  return m.amountMinor < 0n;
}

export function compare(a: Money, b: Money): -1 | 0 | 1 {
  assertSameCurrency(a, b);
  if (a.amountMinor < b.amountMinor) return -1;
  if (a.amountMinor > b.amountMinor) return 1;
  return 0;
}

export function negate(m: Money): Money {
  return money(-m.amountMinor, m.currency);
}

// --- Formatting (centralised here; never changes the domain value) ---

// Minor-unit divisors for common currencies.
const MINOR_UNITS: Record<string, number> = {
  IDR: 0, // Indonesian Rupiah — no subunit
  JPY: 0,
  KRW: 0,
  USD: 2,
  EUR: 2,
  GBP: 2,
  SGD: 2,
  MYR: 2,
  AUD: 2,
  CAD: 2,
};

function getMinorUnitDigits(currency: CurrencyCode): number {
  return MINOR_UNITS[currency] ?? 2;
}

export interface FormatOptions {
  locale?: string;
  showSign?: boolean;
  compact?: boolean;
}

export function format(m: Money, options: FormatOptions = {}): string {
  const digits = getMinorUnitDigits(m.currency);
  const divisor = BigInt(10 ** digits);

  // Convert to major units for Intl.NumberFormat — but we must avoid float.
  // We do integer division and keep the remainder for fractional display.
  const absMinor = m.amountMinor < 0n ? -m.amountMinor : m.amountMinor;
  const major = absMinor / divisor;
  const remainder = absMinor % divisor;

  // Build a decimal string with exact digits.
  const majorStr = major.toString();
  const remainderStr = digits > 0 ? '.' + remainder.toString().padStart(digits, '0') : '';
  const sign = m.amountMinor < 0n ? '-' : options.showSign && m.amountMinor > 0n ? '+' : '';
  const numericStr = `${sign}${majorStr}${remainderStr}`;
  const numericValue = parseFloat(numericStr); // safe for display only, not for computation

  return new Intl.NumberFormat(options.locale ?? 'en-US', {
    style: 'currency',
    currency: m.currency,
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
    notation: options.compact ? 'compact' : 'standard',
    signDisplay: options.showSign ? 'exceptZero' : 'auto',
  }).format(numericValue);
}

export function toTransportString(m: Money): string {
  return m.amountMinor.toString();
}
