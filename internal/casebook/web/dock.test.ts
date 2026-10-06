// dock.test.ts — unit tests for dock.ts helpers and sessions.ts.
//
// Run with: node --test dock.test.ts
// (Node 24 strips TypeScript natively; no compiler step needed.)

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { fmtAge } from './time-utils.ts';
import type { Session } from './wire.d.ts';
import {
  attachedState,
  folderOf,
  pickable,
  resolveAttachment,
  sessionMeta,
  sessionTitle,
  urlWithSession,
} from './sessions.ts';

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

// ---- sessions.ts: how a session is named, which sessions the chooser
// offers, and which session a page is attached to.

function sess(id: string, over: Partial<Session> = {}): Session {
  return {
    id,
    harness: 'pi',
    label: '',
    cwd: '/Users/court/GitHub/schuettc/tools-workspace',
    pid: 1,
    first_seen: '2026-10-06T00:00:00Z',
    last_seen: '2026-10-06T00:00:00Z',
    busy: false,
    queued: 0,
    name: '',
    worker: false,
    eligible: true,
    ...over,
  };
}

describe('naming a session', () => {
  test('a named session reads name, then folder · harness', () => {
    const s = sess('a', { name: 'tools-workspace/casebook' });
    assert.equal(sessionTitle(s), 'tools-workspace/casebook');
    assert.equal(sessionMeta(s), 'tools-workspace \u00b7 pi');
  });
  test('an unnamed session (Claude Code) reads its folder, then harness', () => {
    const s = sess('b', { harness: 'claude', cwd: '/w/luminary-meridian/' });
    assert.equal(folderOf(s), 'luminary-meridian');
    assert.equal(sessionTitle(s), 'luminary-meridian');
    assert.equal(sessionMeta(s), 'claude');
  });
  test('no name and no folder falls back to the harness, then the id', () => {
    assert.equal(sessionTitle(sess('c', { cwd: '' })), 'pi');
    assert.equal(sessionTitle(sess('d', { cwd: '', harness: '' })), 'd');
  });
});

describe('the chooser', () => {
  test('offers only eligible sessions (live, not workers), sorted by name', () => {
    const got = pickable([
      sess('w', { name: 'worker#40c0f7e1', worker: true, eligible: false }),
      sess('gone', { name: 'aaa/left', left: true, eligible: false }),
      sess('z', { name: 'tools-workspace/casebook' }),
      sess('cc', { harness: 'claude', cwd: '/w/bettor-help' }),
      sess('lm', { name: 'luminary-meridian/site' }),
    ]).map((s) => s.id);
    // bettor-help (folder, no name) sorts by what it shows.
    assert.deepEqual(got, ['cc', 'lm', 'z']);
  });
  test('sorts case-insensitively and keeps duplicates apart by id', () => {
    const got = pickable([
      sess('b', { name: 'Tools/x' }),
      sess('a', { name: 'tools/x' }),
      sess('c', { name: 'alpha' }),
    ]).map((s) => s.id);
    assert.deepEqual(got, ['c', 'a', 'b']);
  });
});

describe('attaching the page', () => {
  const two = [sess('a', { name: 'one' }), sess('b', { name: 'two' })];
  test('the URL session wins, here or not', () => {
    assert.deepEqual(resolveAttachment('b', two), { id: 'b', auto: false });
    assert.deepEqual(resolveAttachment('gone', two), {
      id: 'gone',
      auto: false,
    });
  });
  test('no URL session and two or more eligible: nothing is picked', () => {
    assert.deepEqual(resolveAttachment('', two), { id: '', auto: false });
  });
  test('no URL session and exactly one eligible: that one', () => {
    const one = [
      sess('a', { name: 'one' }),
      sess('w', { worker: true, eligible: false }),
      sess('l', { left: true, eligible: false }),
    ];
    assert.deepEqual(resolveAttachment('', one), { id: 'a', auto: true });
  });
  test('no URL session and none eligible: nothing', () => {
    assert.deepEqual(
      resolveAttachment('', [sess('l', { left: true, eligible: false })]),
      { id: '', auto: false },
    );
  });
  test('the state of the attachment', () => {
    const ss = [
      sess('here'),
      sess('left', { left: true, eligible: false }),
      sess('w', { worker: true, eligible: false }),
    ];
    assert.equal(attachedState('', ss), 'none');
    assert.equal(attachedState('here', ss), 'here');
    assert.equal(attachedState('w', ss), 'here');
    assert.equal(attachedState('left', ss), 'left');
    assert.equal(attachedState('pruned', ss), 'unknown');
  });
});

describe('the URL carries the session', () => {
  test('sets it, keeping the route and other parameters', () => {
    assert.equal(
      urlWithSession('http://127.0.0.1:1/?q=nudge#/item/pr%3Ao%2Fr%231', 'a b'),
      'http://127.0.0.1:1/?q=nudge&session=a+b#/item/pr%3Ao%2Fr%231',
    );
    assert.equal(
      urlWithSession('http://127.0.0.1:1/?session=old#/rules', 'new'),
      'http://127.0.0.1:1/?session=new#/rules',
    );
  });
  test('clears it', () => {
    assert.equal(
      urlWithSession('http://127.0.0.1:1/?session=old#/rules', ''),
      'http://127.0.0.1:1/#/rules',
    );
  });
});
