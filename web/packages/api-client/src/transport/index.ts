import { fromHttpStatus, ContractMismatchError } from '../errors/index.js';
import type { ApiError } from '../errors/index.js';

export interface TransportOptions {
  baseUrl: string;
  timeoutMs?: number;
  credentials?: RequestCredentials;
  getRequestId?: () => string;
}

export type TransportResult<T> =
  | { ok: true; data: T; requestId: string | undefined }
  | { ok: false; error: ApiError; requestId: string | undefined };

export async function transportFetch<T>(
  opts: TransportOptions,
  path: string,
  operationId: string,
  init: RequestInit = {},
  parseBody: (raw: unknown) => T,
): Promise<TransportResult<T>> {
  const requestId = opts.getRequestId?.();
  const controller = new AbortController();
  const timeoutId = setTimeout(() => {
    controller.abort();
  }, opts.timeoutMs ?? 10_000);

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'application/json',
    ...(requestId ? { 'X-Request-Id': requestId } : {}),
    ...(init.headers as Record<string, string> | undefined),
  };

  try {
    const response = await fetch(`${opts.baseUrl}${path}`, {
      ...init,
      headers,
      credentials: opts.credentials ?? 'include',
      signal: controller.signal,
    });

    const returnedRequestId = response.headers.get('X-Request-Id') ?? requestId;

    if (!response.ok) {
      let bodyJson: { message?: string } = {};
      try {
        bodyJson = (await response.json()) as { message?: string };
      } catch {
        // ignore parse failure
      }
      return {
        ok: false,
        error: fromHttpStatus(response.status, bodyJson, {
          operationId,
          requestId: returnedRequestId ?? undefined,
        }),
        requestId: returnedRequestId ?? undefined,
      };
    }

    const raw: unknown = await response.json();
    try {
      const data = parseBody(raw);
      return { ok: true, data, requestId: returnedRequestId ?? undefined };
    } catch (parseErr) {
      throw new ContractMismatchError({
        operationId,
        requestId: returnedRequestId ?? undefined,
        reasonCode: parseErr instanceof Error ? parseErr.message : 'PARSE_FAILED',
      });
    }
  } catch (err) {
    if (err instanceof ContractMismatchError) throw err;

    const isTimeout = err instanceof DOMException && err.name === 'AbortError';
    return {
      ok: false,
      error: fromHttpStatus(isTimeout ? 408 : 503, {}, { operationId, requestId }),
      requestId,
    };
  } finally {
    clearTimeout(timeoutId);
  }
}
