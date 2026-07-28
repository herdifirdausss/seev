import http from 'k6/http';
import { check } from 'k6';
import crypto from 'k6/crypto';

export const baseURL = __ENV.LOAD_BASE_URL || 'http://127.0.0.1:8080';
export const vendorBaseURL = __ENV.LOAD_VENDOR_BASE_URL || baseURL;
export const token = __ENV.LOAD_TOKEN || '';

export function stableKey(prefix) {
  return `${prefix}-${__VU}-${__ITER}`;
}

export function headers(extra = {}) {
  return Object.assign({ 'Content-Type': 'application/json', 'X-Request-ID': stableKey('request'), Authorization: token ? `Bearer ${token}` : '' }, extra);
}

export function json(method, path, payload, extra = {}) {
  const body = JSON.stringify(payload);
  return http.request(method, `${baseURL}${path}`, body, { headers: headers(extra), tags: { workload: __ENV.LOAD_WORKLOAD || 'unknown', operation: path } });
}

export function semantic(response, name, statuses = [200, 201, 202]) {
  let decoded = null;
  try { decoded = response.json(); } catch (_) { /* status/content check below is authoritative for non-JSON errors */ }
  const allowed = __ENV.LOAD_ALLOW_EXPECTED_REJECTIONS === '1' ? statuses : statuses.filter((status) => status < 400);
  check(response, { [`${name}: expected status`]: (r) => allowed.includes(r.status), [`${name}: bounded response`]: (r) => r.body.length < 1024 * 1024 });
  return decoded;
}

export function iterationOptions(rate = Number(__ENV.LOAD_RATE || 1), duration = __ENV.LOAD_DURATION || '1m') {
  return { scenarios: { workload: { executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration, preAllocatedVUs: Math.max(1, Math.min(rate * 2, 128)), maxVUs: Number(__ENV.LOAD_MAX_VUS || 256), exec: 'default' } }, thresholds: { dropped_iterations: ['count==0'] } };
}

export function signature(secret, body) { return crypto.hmac('sha256', secret, body, 'hex'); }

export function handleSummary(data) {
  const duration = data.metrics.http_req_duration && data.metrics.http_req_duration.values || {};
  const iterations = data.metrics.iterations && data.metrics.iterations.values || {};
  const dropped = data.metrics.dropped_iterations && data.metrics.dropped_iterations.values || {};
  const checks = data.metrics.checks && data.metrics.checks.values || {};
  const summary = {
    schema_version: 1,
    run_id: __ENV.LOAD_RUN_ID || 'unbound',
    profile_id: __ENV.LOAD_PROFILE || 'unbound',
    workload: __ENV.LOAD_WORKLOAD || 'unbound',
    workload_version: __ENV.LOAD_WORKLOAD_VERSION || '1',
    dataset_hash: __ENV.LOAD_DATASET_HASH || 'sha256:' + '0'.repeat(64),
    offered_wu_per_second: Number(__ENV.LOAD_RATE || 0),
    achieved_wu_per_second: Number(iterations.rate || 0),
    dropped_iterations: Number(dropped.count || 0),
    unexpected_failures: Number(checks.fails || 0),
    total_iterations: Number(iterations.count || 0),
    percentiles_ms: { p50: Number(duration['p(50)'] || 0), p95: Number(duration['p(95)'] || 0), p99: Number(duration['p(99)'] || 0) },
    drain_seconds: Number(__ENV.LOAD_DRAIN_SECONDS || 0),
    integrity_passed: false,
    gate_passed: false,
    artifact_hashes: {},
  };
  const encoded = JSON.stringify(summary) + '\n';
  const output = { stdout: encoded };
  if (__ENV.LOAD_SUMMARY_FILE) output[__ENV.LOAD_SUMMARY_FILE] = encoded;
  return output;
}
