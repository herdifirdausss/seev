import { describe, it, expect } from 'vitest';
import {
  parseWorkflowStatus,
  workflowStatusMeta,
  unknownStatus,
  isUnknownStatus,
} from './status.js';

describe('parseWorkflowStatus', () => {
  it('recognizes settled', () => {
    const s = parseWorkflowStatus('settled');
    expect(isUnknownStatus(s)).toBe(false);
    expect(s).toBe('settled');
  });

  it('returns unknown for unrecognized value', () => {
    const s = parseWorkflowStatus('some_future_status');
    expect(isUnknownStatus(s)).toBe(true);
  });

  it('unknown does not map to success', () => {
    const s = parseWorkflowStatus('SUCCESS');
    expect(isUnknownStatus(s)).toBe(true);
    if (isUnknownStatus(s)) {
      expect(s.raw).toBe('SUCCESS');
    }
  });
});

describe('workflowStatusMeta', () => {
  it('settled is terminal and final', () => {
    const meta = workflowStatusMeta('settled');
    expect(meta.isTerminal).toBe(true);
    expect(meta.isFinal).toBe(true);
  });

  it('pending is not terminal', () => {
    const meta = workflowStatusMeta('pending');
    expect(meta.isTerminal).toBe(false);
  });
});

describe('unknownStatus', () => {
  it('creates and identifies unknown status', () => {
    const u = unknownStatus('mystery');
    expect(isUnknownStatus(u)).toBe(true);
    expect(u.raw).toBe('mystery');
  });
});
