import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { transportFetch } from './index.js';
import { ContractMismatchError } from '../errors/index.js';

const opts = { baseUrl: 'https://api.example.test', timeoutMs: 5000 };

function mockResponse(
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('transportFetch success path', () => {
  it('returns ok:true with parsed data', async () => {
    const body = { id: '1', amount: '100' };
    vi.mocked(global.fetch).mockResolvedValueOnce(mockResponse(200, body));

    const result = await transportFetch(opts, '/transfer', 'getTransfer', {}, (raw) => raw);

    expect(result.ok).toBe(true);
    if (result.ok) expect(result.data).toEqual(body);
  });

  it('propagates X-Request-Id from response headers', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce(
      mockResponse(200, {}, { 'X-Request-Id': 'server-req-id' }),
    );

    const result = await transportFetch(opts, '/ping', 'ping', {}, () => null);

    expect(result.requestId).toBe('server-req-id');
  });
});

describe('transportFetch error paths', () => {
  it('returns VALIDATION_ERROR on 400', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce(mockResponse(400, { message: 'invalid amount' }));
    const result = await transportFetch(opts, '/tx', 'createTx', {}, (r) => r);
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.code).toBe('VALIDATION_ERROR');
      expect(result.error.message).toBe('invalid amount');
    }
  });

  it('returns AUTHENTICATION_REQUIRED on 401', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce(mockResponse(401, {}));
    const result = await transportFetch(opts, '/me', 'getMe', {}, (r) => r);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.code).toBe('AUTHENTICATION_REQUIRED');
  });

  it('returns DEPENDENCY_UNAVAILABLE on network failure', async () => {
    vi.mocked(global.fetch).mockRejectedValueOnce(new Error('Network failure'));
    const result = await transportFetch(opts, '/tx', 'createTx', {}, (r) => r);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.code).toBe('DEPENDENCY_UNAVAILABLE');
  });

  it('returns TIMEOUT on AbortError', async () => {
    vi.mocked(global.fetch).mockRejectedValueOnce(
      new DOMException('The operation was aborted', 'AbortError'),
    );
    const result = await transportFetch(opts, '/tx', 'createTx', {}, (r) => r);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.code).toBe('TIMEOUT');
  });
});

describe('transportFetch contract mismatch', () => {
  it('throws ContractMismatchError when parseBody fails', async () => {
    vi.mocked(global.fetch).mockResolvedValueOnce(mockResponse(200, { foo: 'bar' }));

    await expect(
      transportFetch(opts, '/tx', 'createTx', {}, () => {
        throw new Error('missing required field');
      }),
    ).rejects.toThrow(ContractMismatchError);
  });
});
