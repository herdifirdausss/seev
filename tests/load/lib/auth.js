import { json, semantic } from './common.js';

export function login() {
  const response = json('POST', '/api/v1/auth/login', { email: __ENV.LOAD_EMAIL || 'load-user-000000@example.invalid', password: __ENV.LOAD_PASSWORD || 'synthetic-not-a-secret' });
  const body = semantic(response, 'login', [200]);
  return body && body.data ? (body.data.access_token || body.data.token || '') : '';
}
