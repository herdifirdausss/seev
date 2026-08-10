// Branded string types prevent accidental cross-identifier assignment.
// They are structurally compatible with string for serialization but
// distinct at the TypeScript level.

declare const _brand: unique symbol;
type Brand<T, B> = T & { readonly [_brand]: B };

export type RequestId = Brand<string, 'RequestId'>;
export type TraceId = Brand<string, 'TraceId'>;
export type TransactionId = Brand<string, 'TransactionId'>;
export type AccountId = Brand<string, 'AccountId'>;
export type EventId = Brand<string, 'EventId'>;
export type IdempotencyKey = Brand<string, 'IdempotencyKey'>;
export type UserId = Brand<string, 'UserId'>;

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function assertNonEmpty(value: string, name: string): void {
  if (!value || value.trim().length === 0) {
    throw new Error(`${name} must be a non-empty string`);
  }
}

export function requestId(value: string): RequestId {
  assertNonEmpty(value, 'RequestId');
  return value as RequestId;
}

export function traceId(value: string): TraceId {
  assertNonEmpty(value, 'TraceId');
  return value as TraceId;
}

export function transactionId(value: string): TransactionId {
  if (!UUID_PATTERN.test(value)) {
    throw new Error(`TransactionId must be a UUID, got: ${value}`);
  }
  return value as TransactionId;
}

export function accountId(value: string): AccountId {
  if (!UUID_PATTERN.test(value)) {
    throw new Error(`AccountId must be a UUID, got: ${value}`);
  }
  return value as AccountId;
}

export function eventId(value: string): EventId {
  assertNonEmpty(value, 'EventId');
  return value as EventId;
}

export function idempotencyKey(value: string): IdempotencyKey {
  assertNonEmpty(value, 'IdempotencyKey');
  return value as IdempotencyKey;
}

export function userId(value: string): UserId {
  if (!UUID_PATTERN.test(value)) {
    throw new Error(`UserId must be a UUID, got: ${value}`);
  }
  return value as UserId;
}
