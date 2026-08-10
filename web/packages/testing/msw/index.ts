import { http, HttpResponse } from 'msw';
import { fixtures } from '../fixtures/index.js';

// MSW handlers for the Plan 56 fixture contract.
// These handlers serve deterministic data; production builds must not include them.

export const walletHandlers = [
  http.get('/api/v1/fixture/transactions/:id', ({ params }) => {
    const id = params['id'];
    if (id === fixtures.settled.id) {
      return HttpResponse.json(fixtures.settled);
    }
    if (id === fixtures.processing.id) {
      return HttpResponse.json(fixtures.processing);
    }
    if (id === fixtures.failed.id) {
      return HttpResponse.json(fixtures.failed);
    }
    return HttpResponse.json({ message: 'Not found' }, { status: 404 });
  }),

  http.get('/api/v1/fixture/transactions', () => {
    return HttpResponse.json([fixtures.settled, fixtures.processing, fixtures.failed]);
  }),
];

export const operatorHandlers = [
  http.get('/api/v1/admin/fixture/transactions/:id', ({ params }) => {
    const id = params['id'];
    if (id === fixtures.settled.id) {
      return HttpResponse.json(fixtures.settled);
    }
    return HttpResponse.json({ message: 'Not found' }, { status: 404 });
  }),
];

export const allHandlers = [...walletHandlers, ...operatorHandlers];
