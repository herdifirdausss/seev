import { describe, it, expect } from 'vitest';
import {
  makeApiError,
  fromHttpStatus,
  isMutationSafeToRetry,
  ContractMismatchError,
} from './index.js';

describe('makeApiError', () => {
  it('marks DEPENDENCY_UNAVAILABLE as retryable', () => {
    expect(makeApiError('DEPENDENCY_UNAVAILABLE', 'down').retryable).toBe(true);
  });

  it('marks TIMEOUT as retryable', () => {
    expect(makeApiError('TIMEOUT', 'timed out').retryable).toBe(true);
  });

  it('marks RATE_LIMITED as retryable', () => {
    expect(makeApiError('RATE_LIMITED', 'slow down').retryable).toBe(true);
  });

  it('marks VALIDATION_ERROR as non-retryable', () => {
    expect(makeApiError('VALIDATION_ERROR', 'bad input').retryable).toBe(false);
  });

  it('marks AUTHORIZATION_DENIED as non-retryable', () => {
    expect(makeApiError('AUTHORIZATION_DENIED', 'forbidden').retryable).toBe(false);
  });

  it('passes through operationId and requestId', () => {
    const err = makeApiError('UNKNOWN_SERVER_ERROR', 'oops', {
      operationId: 'getUser',
      requestId: 'req-1',
    });
    expect(err.operationId).toBe('getUser');
    expect(err.requestId).toBe('req-1');
  });
});

describe('fromHttpStatus', () => {
  it('maps 400 to VALIDATION_ERROR', () => {
    expect(fromHttpStatus(400).code).toBe('VALIDATION_ERROR');
  });

  it('maps 401 to AUTHENTICATION_REQUIRED', () => {
    expect(fromHttpStatus(401).code).toBe('AUTHENTICATION_REQUIRED');
  });

  it('maps 403 to AUTHORIZATION_DENIED', () => {
    expect(fromHttpStatus(403).code).toBe('AUTHORIZATION_DENIED');
  });

  it('maps 409 to IDEMPOTENCY_CONFLICT', () => {
    expect(fromHttpStatus(409).code).toBe('IDEMPOTENCY_CONFLICT');
  });

  it('maps 422 to BUSINESS_REJECTION', () => {
    expect(fromHttpStatus(422).code).toBe('BUSINESS_REJECTION');
  });

  it('maps 429 to RATE_LIMITED with retryAfterMs', () => {
    const err = fromHttpStatus(429, { message: 'slow' });
    expect(err.code).toBe('RATE_LIMITED');
    expect(err.retryAfterMs).toBe(60_000);
  });

  it('maps 408 to TIMEOUT', () => {
    expect(fromHttpStatus(408).code).toBe('TIMEOUT');
  });

  it('maps 503 to DEPENDENCY_UNAVAILABLE', () => {
    expect(fromHttpStatus(503).code).toBe('DEPENDENCY_UNAVAILABLE');
  });

  it('maps 500 to UNKNOWN_SERVER_ERROR', () => {
    expect(fromHttpStatus(500).code).toBe('UNKNOWN_SERVER_ERROR');
  });

  it('uses body message when present', () => {
    expect(fromHttpStatus(400, { message: 'field required' }).message).toBe('field required');
  });

  it('falls back to default message when body has none', () => {
    expect(fromHttpStatus(400, {}).message).toBe('An error occurred');
  });

  it('passes operationId and requestId through', () => {
    const err = fromHttpStatus(401, {}, { operationId: 'login', requestId: 'r1' });
    expect(err.operationId).toBe('login');
    expect(err.requestId).toBe('r1');
  });
});

describe('isMutationSafeToRetry', () => {
  it('always returns false regardless of error code', () => {
    expect(isMutationSafeToRetry(makeApiError('DEPENDENCY_UNAVAILABLE', 'down'))).toBe(false);
    expect(isMutationSafeToRetry(makeApiError('TIMEOUT', 'slow'))).toBe(false);
  });
});

describe('ContractMismatchError', () => {
  it('carries operationId, reasonCode, and requestId', () => {
    const e = new ContractMismatchError({
      operationId: 'createTransfer',
      requestId: 'req-42',
      reasonCode: 'missing_field',
    });
    expect(e.operationId).toBe('createTransfer');
    expect(e.requestId).toBe('req-42');
    expect(e.reasonCode).toBe('missing_field');
    expect(e.code).toBe('CONTRACT_MISMATCH');
  });

  it('message includes operationId and reasonCode', () => {
    const e = new ContractMismatchError({ operationId: 'op', reasonCode: 'PARSE_FAILED' });
    expect(e.message).toContain('op');
    expect(e.message).toContain('PARSE_FAILED');
  });

  it('is an instanceof Error', () => {
    const e = new ContractMismatchError({ operationId: 'op', reasonCode: 'bad' });
    expect(e).toBeInstanceOf(Error);
  });
});
