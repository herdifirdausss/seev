import { describe, it, expect } from 'vitest';
import { requestId, transactionId, accountId, userId, idempotencyKey } from './identifiers.js';

const VALID_UUID = 'a1b2c3d4-e5f6-7890-abcd-ef1234567890';
const INVALID_UUID = 'not-a-uuid';

describe('transactionId', () => {
  it('accepts valid UUID', () => {
    expect(transactionId(VALID_UUID)).toBe(VALID_UUID);
  });
  it('rejects non-UUID', () => {
    expect(() => transactionId(INVALID_UUID)).toThrow('UUID');
  });
  it('rejects empty', () => {
    expect(() => transactionId('')).toThrow();
  });
});

describe('accountId', () => {
  it('accepts valid UUID', () => {
    expect(accountId(VALID_UUID)).toBe(VALID_UUID);
  });
  it('rejects non-UUID', () => {
    expect(() => accountId('abc')).toThrow('UUID');
  });
});

describe('userId', () => {
  it('accepts valid UUID', () => {
    expect(userId(VALID_UUID)).toBe(VALID_UUID);
  });
  it('rejects short string', () => {
    expect(() => userId('short')).toThrow('UUID');
  });
});

describe('requestId', () => {
  it('accepts any non-empty string', () => {
    expect(requestId('req-001')).toBe('req-001');
  });
  it('rejects empty', () => {
    expect(() => requestId('')).toThrow();
  });
  it('rejects whitespace-only', () => {
    expect(() => requestId('   ')).toThrow();
  });
});

describe('idempotencyKey', () => {
  it('accepts any non-empty string', () => {
    expect(idempotencyKey('key-abc')).toBe('key-abc');
  });
});
