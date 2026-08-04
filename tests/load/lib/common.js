import http from 'k6/http';
import { check } from 'k6';
import crypto from 'k6/crypto';
import { Counter } from 'k6/metrics';

export const baseURL = __ENV.LOAD_BASE_URL || 'http://127.0.0.1:8080';
export const authBaseURL = __ENV.LOAD_AUTH_BASE_URL || baseURL;
export const authInternalBaseURL = __ENV.LOAD_AUTH_INTERNAL_BASE_URL || authBaseURL;
export const vendorBaseURL = __ENV.LOAD_VENDOR_BASE_URL || baseURL;
export const payinAdminBaseURL = __ENV.LOAD_PAYIN_ADMIN_BASE_URL || baseURL;
export const ledgerBaseURL = __ENV.LOAD_LEDGER_BASE_URL || baseURL;
export const token = __ENV.LOAD_TOKEN || '';
export const unexpectedFailures = new Counter('unexpected_failures');

export function stableKey(prefix) {
  return `${prefix}-workload-${__VU}-${__ITER}`;
}

// authToken defaults to the env-supplied token (the existing, still-manual
// path) but a scenario with its own setup()-seeded identity — see
// lib/seed.js — passes its per-run token explicitly instead.
export function headers(extra = {}, authToken = token) {
  return Object.assign({ 'Content-Type': 'application/json', 'X-Request-ID': stableKey('request'), Authorization: authToken ? `Bearer ${authToken}` : '' }, extra);
}

export function json(method, path, payload, extra = {}, authToken = token) {
  const body = JSON.stringify(payload);
  return http.request(method, `${baseURL}${path}`, body, { headers: headers(extra, authToken), tags: { workload: __ENV.LOAD_WORKLOAD || 'unknown', operation: path } });
}

export function authJson(method, path, payload, extra = {}, authToken = token) {
  const body = JSON.stringify(payload);
  return http.request(method, `${authBaseURL}${path}`, body, { headers: headers(extra, authToken), tags: { workload: __ENV.LOAD_WORKLOAD || 'unknown', operation: `auth:${path}` } });
}

// ledgerJson targets ledger-service's own internal listener directly
// (ledgerBaseURL, :8091) rather than baseURL/gateway-service — gateway only
// forwards /api/v1/ledger/*, and ledger's own public :8090 listener only
// serves that same prefix; /api/v1/admin/ledger/* (the disbursement admin
// routes live under it) is mounted on internalRouter's :8091 instead
// (services/ledger/cmd/ledger/main.go:332-338).
export function ledgerJson(method, path, payload, extra = {}, authToken = token) {
  const body = JSON.stringify(payload);
  return http.request(method, `${ledgerBaseURL}${path}`, body, { headers: headers(extra, authToken), tags: { workload: __ENV.LOAD_WORKLOAD || 'unknown', operation: `ledger:${path}` } });
}

export function semantic(response, name, statuses = [200, 201, 202]) {
  let decoded = null;
  try { decoded = response.json(); } catch (_) { /* status/content check below is authoritative for non-JSON errors */ }
  const allowed = __ENV.LOAD_ALLOW_EXPECTED_REJECTIONS === '1' ? statuses : statuses.filter((status) => status < 400);
  unexpectedFailures.add(allowed.includes(response.status) ? 0 : 1);
  check(response, { [`${name}: expected status`]: (r) => allowed.includes(r.status), [`${name}: bounded response`]: (r) => (r.body || '').length < 1024 * 1024 });
  return decoded;
}

export function iterationOptions(rate = Number(__ENV.LOAD_RATE || 1), duration = __ENV.LOAD_DURATION || '1m') {
  // setupTimeout is generous because scenarios that self-seed (lib/seed.js)
  // make several sequential HTTP calls in setup() — register, login, topup,
  // signed webhook — before the load phase begins. The default 60s is fine
  // for scenarios without a real setup() and is harmless overhead here.
  const options = { setupTimeout: '120s', scenarios: { workload: { executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration, preAllocatedVUs: Math.max(1, Math.min(rate * 2, 128)), maxVUs: Number(__ENV.LOAD_MAX_VUS || 256), exec: 'default' } }, summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)'], thresholds: { dropped_iterations: ['count==0'] } };
  if (__ENV.LOAD_TLS_CERT_FILE && __ENV.LOAD_TLS_KEY_FILE) {
    options.tlsAuth = [{ cert: open(__ENV.LOAD_TLS_CERT_FILE), key: open(__ENV.LOAD_TLS_KEY_FILE) }];
    options.insecureSkipTLSVerify = true;
  }
  return options;
}

export function signature(secret, body) { return crypto.hmac('sha256', secret, body, 'hex'); }

export function handleSummary(data) {
  const duration = data.metrics.http_req_duration && data.metrics.http_req_duration.values || {};
  const iterations = data.metrics.iterations && data.metrics.iterations.values || {};
  const dropped = data.metrics.dropped_iterations && data.metrics.dropped_iterations.values || {};
  // http_reqs is k6's own built-in metric (every http.request() call is
  // counted automatically, independent of this scenario's own iteration
  // counting) — docs/performance/reports §1.1's own terminology distinguishes
  // "WU/s" (complete business journeys, i.e. iterations) from "HTTP req/s"
  // (one workload unit may issue several HTTP requests); this was tracked by
  // k6 the whole time but never surfaced into the retained summary
  // (§26 Definition of Done gap).
  const httpReqs = data.metrics.http_reqs && data.metrics.http_reqs.values || {};
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
    unexpected_failures: Number((data.metrics.unexpected_failures && data.metrics.unexpected_failures.values.count) || 0),
    total_iterations: Number(iterations.count || 0),
    total_http_reqs: Number(httpReqs.count || 0),
    http_reqs_per_second: Number(httpReqs.rate || 0),
    percentiles_ms: { p50: Number(duration.med || duration['p(50)'] || 0), p95: Number(duration['p(95)'] || 0), p99: Number(duration['p(99)'] || 0) },
    // drain_seconds/integrity_passed are placeholders — k6 exits before the
    // outbox can drain or the ledger can be verified. scripts/load-test.sh
    // patches both fields in summary.json after k6 exits, against the
    // still-live disposable stack (Phase 0 §24.3). gate_passed stays false
    // here and is not patched: full gate evaluation needs CPU/memory/pool
    // evidence this harness does not yet collect (Phase 0 §24.4).
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
