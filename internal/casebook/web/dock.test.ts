// dock.test.ts — unit tests for dock.ts helpers.
//
// Run with: node --test dock.test.ts
// (Node 24 strips TypeScript natively; no compiler step needed.)

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { fmtAge } from './time-utils.ts';

// Helper: build an ISO timestamp that is `ms` milliseconds in the past.
function ago(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

describe('fmtAge', () => {
  test('under 1 minute returns "now"', () => {
    assert.equal(fmtAge(ago(0)), 'now');
    assert.equal(fmtAge(ago(500)), 'now');
    assert.equal(fmtAge(ago(30_000)), 'now');
    assert.equal(fmtAge(ago(59_000)), 'now');
  });

  test('minutes', () => {
    assert.equal(fmtAge(ago(60_000)), '1m');
    assert.equal(fmtAge(ago(6 * 60_000)), '6m');
    assert.equal(fmtAge(ago(59 * 60_000)), '59m');
  });

  test('hours', () => {
    assert.equal(fmtAge(ago(60 * 60_000)), '1h');
    assert.equal(fmtAge(ago(23 * 60 * 60_000)), '23h');
  });

  test('days', () => {
    assert.equal(fmtAge(ago(24 * 60 * 60_000)), '1d');
    assert.equal(fmtAge(ago(29 * 24 * 60 * 60_000)), '29d');
  });

  test('months use "mo" not "m" to avoid collision with minutes', () => {
    // 30 days ≈ 1 month
    assert.equal(fmtAge(ago(30 * 24 * 60 * 60_000)), '1mo');
    assert.equal(fmtAge(ago(6 * 30 * 24 * 60 * 60_000)), '6mo');
    assert.equal(fmtAge(ago(11 * 30 * 24 * 60 * 60_000)), '11mo');
  });

  test('years', () => {
    assert.equal(fmtAge(ago(12 * 30 * 24 * 60 * 60_000)), '1y');
    assert.equal(fmtAge(ago(24 * 30 * 24 * 60 * 60_000)), '2y');
  });

  test('fails if under-a-minute changes from "now"', () => {
    // This test documents the invariant: removing the "now" branch
    // would break this check.
    const result = fmtAge(ago(30_000)); // 30 seconds ago
    assert.equal(result, 'now', 'under 1 minute must return "now"');
  });

  test('fails if months use "M" instead of "mo"', () => {
    // This test catches the regression where months were formatted as M
    // (clashing with minutes under uppercase).
    const result = fmtAge(ago(6 * 30 * 24 * 60 * 60_000));
    assert.equal(result, '6mo', 'months must use "mo" not "M"');
    assert.notEqual(
      result,
      '6M',
      'months must not collide with uppercase minutes',
    );
  });
});
