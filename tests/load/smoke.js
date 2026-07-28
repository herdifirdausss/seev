// B0 loader bootstrap smoke. Canonical scenarios are versioned under this
// directory; this file intentionally performs no business mutation until the
// runner has supplied a disposable manifest and seeded credentials.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  thresholds: { checks: ['rate==1.0'] },
  scenarios: {
    smoke: { executor: 'constant-arrival-rate', rate: 1, timeUnit: '1s', duration: '1s', preAllocatedVUs: 1, maxVUs: 1 },
  },
};

export default function () {
  const baseURL = __ENV.LOAD_BASE_URL || 'http://127.0.0.1:8080';
  const response = http.get(`${baseURL}/health`);
  check(response, { 'load target responds': (r) => r.status >= 200 && r.status < 400 });
}
