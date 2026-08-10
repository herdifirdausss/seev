// Deterministic synthetic fixtures for Plan 56 reference screen.
// All values are invented — no real personal data, tokens, or production payloads.
// Fixture mode must be visibly labeled; production builds exclude this module.

import { money } from '@seev/money';
import type { TransactionEvidence } from '@seev/domain';
import { transactionId, accountId, requestId, parseTimestamp } from '@seev/domain';

const FIXTURE_TX_ID = transactionId('a1b2c3d4-e5f6-7890-abcd-ef1234567890');
const FIXTURE_REQUEST_ID = requestId('req-fixture-001');
const FIXTURE_SENDER_ACCOUNT = accountId('11111111-0000-0000-0000-000000000001');
const FIXTURE_RECEIVER_ACCOUNT = accountId('22222222-0000-0000-0000-000000000002');
const FIXTURE_FEE_ACCOUNT = accountId('00000000-0000-0000-0000-fee000000001');

const CREATED_AT = parseTimestamp('2026-01-15T10:00:00.000Z');
const SETTLED_AT = parseTimestamp('2026-01-15T10:00:05.123Z');

export const fixtures = {
  /** Settled P2P transfer — balanced entries, all status dimensions present. */
  settled: {
    id: FIXTURE_TX_ID,
    requestId: FIXTURE_REQUEST_ID,
    type: 'transfer_p2p',
    amount: money(10000n, 'IDR'), // Rp 10,000
    workflowStatus: 'settled' as const,
    ledgerStatus: 'posted' as const,
    deliveryStatus: 'delivered' as const,
    entries: [
      {
        accountId: FIXTURE_SENDER_ACCOUNT,
        direction: 'debit' as const,
        amount: money(10500n, 'IDR'),
      },
      {
        accountId: FIXTURE_RECEIVER_ACCOUNT,
        direction: 'credit' as const,
        amount: money(10000n, 'IDR'),
      },
      { accountId: FIXTURE_FEE_ACCOUNT, direction: 'credit' as const, amount: money(500n, 'IDR') },
    ],
    createdAt: CREATED_AT,
    updatedAt: SETTLED_AT,
  } satisfies TransactionEvidence,

  /** Processing — uncertain state, do not imply success or failure. */
  processing: {
    id: transactionId('b2c3d4e5-f6a7-8901-bcde-f12345678901'),
    type: 'transfer_p2p',
    amount: money(50000n, 'IDR'),
    workflowStatus: 'processing' as const,
    ledgerStatus: 'pending' as const,
    entries: [],
    createdAt: parseTimestamp('2026-01-15T11:00:00.000Z'),
  } satisfies TransactionEvidence,

  /** Failed — terminal negative state. */
  failed: {
    id: transactionId('c3d4e5f6-a7b8-9012-cdef-123456789012'),
    type: 'withdraw_initiate',
    amount: money(100000n, 'IDR'),
    workflowStatus: 'failed' as const,
    ledgerStatus: 'failed' as const,
    entries: [],
    createdAt: parseTimestamp('2026-01-15T09:00:00.000Z'),
    updatedAt: parseTimestamp('2026-01-15T09:01:00.000Z'),
  } satisfies TransactionEvidence,

  /** Empty state — no transactions. */
  empty: [] as TransactionEvidence[],
} as const;

export type FixtureVariant = keyof typeof fixtures;
