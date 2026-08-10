import { describe, it, expect } from 'vitest';
import {
  money,
  fromTransport,
  add,
  subtract,
  compare,
  negate,
  isZero,
  isPositive,
  isNegative,
  format,
  toTransportString,
} from '../src/index.js';

describe('money construction', () => {
  it('creates money with exact bigint', () => {
    const m = money(12345n, 'IDR');
    expect(m.amountMinor).toBe(12345n);
    expect(m.currency).toBe('IDR');
  });

  it('rejects empty currency', () => {
    expect(() => money(0n, '')).toThrow('currency');
    expect(() => money(0n, '  ')).toThrow('currency');
  });

  it('allows zero', () => {
    expect(money(0n, 'USD').amountMinor).toBe(0n);
  });

  it('allows negative', () => {
    expect(money(-500n, 'USD').amountMinor).toBe(-500n);
  });
});

describe('fromTransport', () => {
  it('parses decimal string', () => {
    expect(fromTransport('12345', 'IDR').amountMinor).toBe(12345n);
  });

  it('parses negative string', () => {
    expect(fromTransport('-500', 'USD').amountMinor).toBe(-500n);
  });

  it('parses bigint', () => {
    expect(fromTransport(42n, 'USD').amountMinor).toBe(42n);
  });

  it('parses safe integer number', () => {
    expect(fromTransport(999, 'USD').amountMinor).toBe(999n);
  });

  it('rejects decimal point in string', () => {
    expect(() => fromTransport('12.50', 'USD')).toThrow('Invalid money string');
  });

  it('rejects exponent notation', () => {
    expect(() => fromTransport('1e5', 'USD')).toThrow('Invalid money string');
  });

  it('rejects whitespace', () => {
    expect(() => fromTransport(' 100', 'USD')).toThrow('Invalid money string');
  });

  it('rejects unsafe number', () => {
    expect(() => fromTransport(Number.MAX_SAFE_INTEGER + 1, 'USD')).toThrow('Unsafe integer');
  });

  it('rejects float number', () => {
    expect(() => fromTransport(1.5, 'USD')).toThrow('Unsafe integer');
  });

  it('rejects null', () => {
    expect(() => fromTransport(null, 'USD')).toThrow();
  });

  it('rejects object', () => {
    expect(() => fromTransport({}, 'USD')).toThrow();
  });
});

describe('arithmetic', () => {
  const usd100 = money(10000n, 'USD'); // $100.00
  const usd50 = money(5000n, 'USD'); // $50.00
  const eur100 = money(10000n, 'EUR');

  it('adds same currency', () => {
    expect(add(usd100, usd50).amountMinor).toBe(15000n);
  });

  it('subtracts same currency', () => {
    expect(subtract(usd100, usd50).amountMinor).toBe(5000n);
  });

  it('throws on cross-currency add', () => {
    expect(() => add(usd100, eur100)).toThrow('mismatch');
  });

  it('throws on cross-currency subtract', () => {
    expect(() => subtract(usd100, eur100)).toThrow('mismatch');
  });

  it('compare returns correct ordering', () => {
    expect(compare(usd100, usd50)).toBe(1);
    expect(compare(usd50, usd100)).toBe(-1);
    expect(compare(usd100, usd100)).toBe(0);
  });

  it('negate flips sign', () => {
    expect(negate(usd100).amountMinor).toBe(-10000n);
    expect(negate(negate(usd100)).amountMinor).toBe(10000n);
  });
});

describe('predicates', () => {
  it('isZero', () => {
    expect(isZero(money(0n, 'USD'))).toBe(true);
    expect(isZero(money(1n, 'USD'))).toBe(false);
  });

  it('isPositive', () => {
    expect(isPositive(money(1n, 'USD'))).toBe(true);
    expect(isPositive(money(0n, 'USD'))).toBe(false);
    expect(isPositive(money(-1n, 'USD'))).toBe(false);
  });

  it('isNegative', () => {
    expect(isNegative(money(-1n, 'USD'))).toBe(true);
    expect(isNegative(money(0n, 'USD'))).toBe(false);
  });
});

describe('format', () => {
  it('formats USD cents', () => {
    const result = format(money(10050n, 'USD'), { locale: 'en-US' });
    expect(result).toContain('100');
    expect(result).toContain('50');
  });

  it('formats IDR without subunit', () => {
    const result = format(money(50000n, 'IDR'), { locale: 'en-US' });
    expect(result).toContain('50,000');
  });

  it('does not change domain value when formatting', () => {
    const m = money(12345n, 'USD');
    format(m);
    expect(m.amountMinor).toBe(12345n);
  });

  it('formats zero', () => {
    const result = format(money(0n, 'USD'), { locale: 'en-US' });
    expect(result).toContain('0');
  });
});

describe('toTransportString', () => {
  it('round-trips through fromTransport', () => {
    const m = money(99999n, 'USD');
    const str = toTransportString(m);
    expect(fromTransport(str, 'USD').amountMinor).toBe(m.amountMinor);
  });

  it('handles negative', () => {
    expect(toTransportString(money(-100n, 'USD'))).toBe('-100');
  });
});
