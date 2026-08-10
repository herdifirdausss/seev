// Normalized error taxonomy — each class carries distinct retry semantics.
// Human-readable backend messages are presentation text, not parser control flow.

export type ApiErrorCode =
  | 'VALIDATION_ERROR'
  | 'AUTHENTICATION_REQUIRED'
  | 'AUTHORIZATION_DENIED'
  | 'BUSINESS_REJECTION'
  | 'IDEMPOTENCY_CONFLICT'
  | 'RATE_LIMITED'
  | 'DEPENDENCY_UNAVAILABLE'
  | 'TIMEOUT'
  | 'CONTRACT_MISMATCH'
  | 'UNKNOWN_SERVER_ERROR';

export interface ApiError {
  readonly code: ApiErrorCode;
  readonly operationId?: string | undefined;
  readonly requestId?: string | undefined;
  readonly message: string;
  readonly retryable: boolean;
  readonly retryAfterMs?: number | undefined;
}

export function makeApiError(
  code: ApiErrorCode,
  message: string,
  options: {
    operationId?: string | undefined;
    requestId?: string | undefined;
    retryAfterMs?: number | undefined;
  } = {},
): ApiError {
  const retryable =
    code === 'DEPENDENCY_UNAVAILABLE' || code === 'TIMEOUT' || code === 'RATE_LIMITED';
  return {
    code,
    message,
    retryable,
    ...options,
  };
}

// Mutation operations are NOT automatically retried regardless of retryable flag.
// Only the caller decides if retrying a mutation is safe.
export function isMutationSafeToRetry(_error: ApiError): false {
  return false;
}

export class ContractMismatchError extends Error {
  readonly code = 'CONTRACT_MISMATCH' as const;
  readonly operationId: string;
  readonly requestId: string | undefined;
  readonly reasonCode: string;

  constructor(opts: { operationId: string; requestId?: string | undefined; reasonCode: string }) {
    super(`Contract mismatch in operation ${opts.operationId}: ${opts.reasonCode}`);
    this.name = 'ContractMismatchError';
    this.operationId = opts.operationId;
    this.requestId = opts.requestId;
    this.reasonCode = opts.reasonCode;
  }
}

export function fromHttpStatus(
  status: number,
  body: { message?: string } = {},
  options: { operationId?: string | undefined; requestId?: string | undefined } = {},
): ApiError {
  const msg = body.message ?? 'An error occurred';

  if (status === 400) return makeApiError('VALIDATION_ERROR', msg, options);
  if (status === 401) return makeApiError('AUTHENTICATION_REQUIRED', msg, options);
  if (status === 403) return makeApiError('AUTHORIZATION_DENIED', msg, options);
  if (status === 409) return makeApiError('IDEMPOTENCY_CONFLICT', msg, options);
  if (status === 422) return makeApiError('BUSINESS_REJECTION', msg, options);
  if (status === 429)
    return makeApiError('RATE_LIMITED', msg, {
      ...options,
      retryAfterMs: 60_000,
    });
  if (status === 503) return makeApiError('DEPENDENCY_UNAVAILABLE', msg, options);
  if (status >= 500) return makeApiError('UNKNOWN_SERVER_ERROR', msg, options);
  return makeApiError('UNKNOWN_SERVER_ERROR', msg, options);
}
