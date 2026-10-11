// decide.test.ts — unit tests for the pure decision-vocabulary helpers.
//
// Run with: node --test decide.test.ts
// (Node 24 strips TypeScript natively; no compiler step needed.)
//
// These tests exercise allowedForKeys and kindFromKey without any network
// calls or server: they pass a fixture vocab built from item.Allowed's
// definitions (the same values served by GET /api/decisions/vocabulary).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  kindFromKey,
  allowedForKind,
  allowedForKeys,
  keyWithoutKind,
  pluralize,
  leftOpenMeta,
  navTargets,
  shortKey,
} from './decide-math.ts';
import type { DecisionVocabView, GroupView } from './wire.d.ts';

// ---- fixture vocab (mirrors item.Allowed for all five kinds) ----------------
//
// This must stay consistent with internal/casebook/item/decision.go:
//   KindRepo:     {Keep, Archive, Delete, Wait, Watch, Ignore}
//   KindPR:       {Keep, Close, Merge,   Wait, Watch, Ignore}
//   KindIssue:    {Keep, Close,           Wait, Watch, Ignore}
//   KindBranch:   {Keep, Delete,          Wait, Watch, Ignore}
//   KindWorktree: {Keep, Delete,          Wait,        Ignore}
//
// The Go test TestDecisionVocabulary verifies the server returns exactly these.

const fixtureVocab: DecisionVocabView = {
  kinds: [
    {
      kind: 'repo',
      allowed: ['keep', 'archive', 'delete', 'wait', 'watch', 'ignore'],
      needs_until: ['wait', 'watch'],
    },
    {
      kind: 'pr',
      allowed: ['keep', 'close', 'merge', 'wait', 'watch', 'ignore'],
      needs_until: ['wait', 'watch'],
    },
    {
      kind: 'issue',
      allowed: ['keep', 'close', 'wait', 'watch', 'ignore'],
      needs_until: ['wait', 'watch'],
    },
    {
      kind: 'branch',
      allowed: ['keep', 'delete', 'wait', 'watch', 'ignore'],
      needs_until: ['wait', 'watch'],
    },
    {
      kind: 'worktree',
      allowed: ['keep', 'delete', 'wait', 'ignore'],
      needs_until: ['wait'],
    },
  ],
  until_forms: [
    { op: 'date', syntax: 'date(YYYY-MM-DD)', example: 'date(2026-12-01)' },
    {
      op: 'merged',
      syntax: 'merged(<pr>)',
      example: 'merged(pr:owner/repo#1)',
    },
    {
      op: 'closed',
      syntax: 'closed(<pr|issue>)',
      example: 'closed(issue:owner/repo#1)',
    },
    {
      op: 'inactive',
      syntax: 'inactive(<duration>)',
      example: 'inactive(90d)',
    },
    {
      op: 'released',
      syntax: 'released(<repo>)',
      example: 'released(repo:owner/repo)',
    },
  ],
};

// ---- keyWithoutKind ---------------------------------------------------------

describe('keyWithoutKind', () => {
  test('strips kind prefix from issue key', () => {
    assert.equal(keyWithoutKind('issue:schuettc/hail#4'), 'schuettc/hail#4');
  });
  test('strips kind prefix from pr key', () => {
    assert.equal(keyWithoutKind('pr:schuettc/hail#3'), 'schuettc/hail#3');
  });
  test('strips kind prefix from repo key', () => {
    assert.equal(keyWithoutKind('repo:schuettc/hail'), 'schuettc/hail');
  });
  test('strips kind prefix from branch key', () => {
    assert.equal(
      keyWithoutKind('branch:schuettc/hail@main'),
      'schuettc/hail@main',
    );
  });
  test('returns key unchanged when no colon', () => {
    assert.equal(keyWithoutKind('nokindhere'), 'nokindhere');
  });
  test('returns empty string for empty input', () => {
    assert.equal(keyWithoutKind(''), '');
  });
});

// ---- kindFromKey -------------------------------------------------------------

describe('kindFromKey', () => {
  test('extracts the kind prefix from a pr key', () => {
    assert.equal(kindFromKey('pr:schuettc/hail#3'), 'pr');
  });
  test('extracts the kind prefix from an issue key', () => {
    assert.equal(kindFromKey('issue:schuettc/hail#4'), 'issue');
  });
  test('extracts the kind prefix from a repo key', () => {
    assert.equal(kindFromKey('repo:schuettc/hail'), 'repo');
  });
  test('extracts the kind prefix from a branch key', () => {
    assert.equal(kindFromKey('branch:schuettc/hail@main'), 'branch');
  });
  test('extracts the kind prefix from a worktree key', () => {
    assert.equal(
      kindFromKey('worktree:mymachine:/home/user/project'),
      'worktree',
    );
  });
  test('returns empty string for a key without a colon', () => {
    assert.equal(kindFromKey('nokindhere'), '');
  });
  test('returns empty string for an empty string', () => {
    assert.equal(kindFromKey(''), '');
  });
});

// ---- allowedForKind ---------------------------------------------------------

describe('allowedForKind', () => {
  test('returns correct dispositions for pr', () => {
    assert.deepEqual(allowedForKind(fixtureVocab, 'pr'), [
      'keep',
      'close',
      'merge',
      'wait',
      'watch',
      'ignore',
    ]);
  });
  test('returns correct dispositions for worktree (no watch)', () => {
    assert.deepEqual(allowedForKind(fixtureVocab, 'worktree'), [
      'keep',
      'delete',
      'wait',
      'ignore',
    ]);
  });
  test('returns empty array for an unknown kind', () => {
    assert.deepEqual(allowedForKind(fixtureVocab, 'unknown'), []);
  });
});

// ---- allowedForKeys ---------------------------------------------------------

describe('allowedForKeys', () => {
  test('empty key list returns empty array', () => {
    assert.deepEqual(allowedForKeys(fixtureVocab, []), []);
  });

  test('single pr key returns pr allowed set', () => {
    const allowed = allowedForKeys(fixtureVocab, ['pr:schuettc/hail#3']);
    assert.deepEqual(allowed, [
      'keep',
      'close',
      'merge',
      'wait',
      'watch',
      'ignore',
    ]);
  });

  test('mixed pr + issue intersects correctly (excludes merge)', () => {
    // pr:  keep close merge wait watch ignore
    // issue: keep close      wait watch ignore
    // intersect: keep close wait watch ignore
    const allowed = allowedForKeys(fixtureVocab, [
      'pr:schuettc/hail#3',
      'issue:schuettc/hail#4',
    ]);
    assert.ok(allowed.includes('keep'), 'keep should be allowed');
    assert.ok(allowed.includes('close'), 'close should be allowed');
    assert.ok(allowed.includes('wait'), 'wait should be allowed');
    assert.ok(allowed.includes('watch'), 'watch should be allowed');
    assert.ok(allowed.includes('ignore'), 'ignore should be allowed');
    assert.ok(
      !allowed.includes('merge'),
      'merge must be excluded (issue does not allow it)',
    );
    assert.ok(!allowed.includes('delete'), 'delete must be excluded');
    assert.ok(!allowed.includes('archive'), 'archive must be excluded');
  });

  test('mixed pr + issue + branch intersects correctly', () => {
    // pr:     keep close merge wait watch ignore
    // issue:  keep close       wait watch ignore
    // branch: keep delete      wait watch ignore
    // intersect: keep wait watch ignore
    const allowed = allowedForKeys(fixtureVocab, [
      'pr:schuettc/hail#3',
      'issue:schuettc/hail#4',
      'branch:schuettc/hail@main',
    ]);
    assert.deepEqual(allowed, ['keep', 'wait', 'watch', 'ignore']);
  });

  test('including worktree excludes watch (not in worktree allowed set)', () => {
    // worktree: keep delete wait ignore (no watch)
    // pr:       keep close merge wait watch ignore
    // intersect: keep wait ignore
    const allowed = allowedForKeys(fixtureVocab, [
      'pr:schuettc/hail#3',
      'worktree:mymachine:/home/user/project',
    ]);
    assert.ok(
      !allowed.includes('watch'),
      'watch excluded when worktree in selection',
    );
    assert.ok(
      !allowed.includes('merge'),
      'merge excluded when worktree in selection',
    );
    assert.ok(
      !allowed.includes('close'),
      'close excluded when worktree in selection',
    );
    assert.ok(allowed.includes('keep'), 'keep should be allowed');
    assert.ok(allowed.includes('wait'), 'wait should be allowed');
  });

  test('unknown kind in selection produces empty intersection', () => {
    // An unknown kind has no allowed dispositions; intersection with anything is empty.
    const allowed = allowedForKeys(fixtureVocab, [
      'pr:schuettc/hail#3',
      'unknown:thing',
    ]);
    assert.deepEqual(allowed, [], 'unknown kind makes the intersection empty');
  });

  test('kinds not rendered (off-page items) are still included from key prefix alone', () => {
    // This is the key invariant: allowedForKeys uses key prefixes, not DOM elements.
    // Simulate a selection where some items are on an unrendered page:
    //   - all five kinds represented purely by their key strings
    // repo:     keep archive delete wait watch ignore
    // pr:       keep close   merge  wait watch ignore
    // issue:    keep close          wait watch ignore
    // branch:   keep delete         wait watch ignore
    // worktree: keep delete         wait       ignore
    // intersect: keep wait ignore
    const allowed = allowedForKeys(fixtureVocab, [
      'repo:schuettc/hail',
      'pr:schuettc/hail#3',
      'issue:schuettc/hail#4',
      'branch:schuettc/hail@main',
      'worktree:mymachine:/home/user/project',
    ]);
    assert.deepEqual(allowed, ['keep', 'wait', 'ignore']);
  });

  test('duplicate keys count as one kind', () => {
    const allowed = allowedForKeys(fixtureVocab, [
      'pr:schuettc/hail#1',
      'pr:schuettc/hail#2',
      'pr:schuettc/hail#3',
    ]);
    assert.deepEqual(allowed, [
      'keep',
      'close',
      'merge',
      'wait',
      'watch',
      'ignore',
    ]);
  });
});

// ---- stripOwnKey (item history display) -------------------------------------

import { stripOwnKey } from './decide-math.ts';

describe('stripOwnKey', () => {
  test('removes own key followed by a space', () => {
    assert.equal(
      stripOwnKey(
        'decide pr:schuettc/hail#3 \u2192 close by schuettc',
        'pr:schuettc/hail#3',
      ),
      'decide \u2192 close by schuettc',
    );
  });

  test('removes own key at end of string', () => {
    assert.equal(
      stripOwnKey('evidence for pr:schuettc/hail#3', 'pr:schuettc/hail#3'),
      'evidence for ',
    );
  });

  test('leaves a different key unchanged', () => {
    const subject = 'decide pr:schuettc/hail#5 \u2192 close by schuettc';
    assert.equal(stripOwnKey(subject, 'pr:schuettc/hail#3'), subject);
  });

  test('empty key returns subject unchanged', () => {
    const subject = 'decide \u2192 close by schuettc';
    assert.equal(stripOwnKey(subject, ''), subject);
  });

  test('subject not containing key is unchanged', () => {
    const subject = 'decide \u2192 close by schuettc';
    assert.equal(stripOwnKey(subject, 'pr:schuettc/hail#3'), subject);
  });
});

// ---- pluralize --------------------------------------------------------------

describe('pluralize', () => {
  test('1 item (singular)', () => {
    assert.equal(pluralize(1, 'item'), '1 item');
  });
  test('2 items (default plural = singular + s)', () => {
    assert.equal(pluralize(2, 'item'), '2 items');
  });
  test('0 uses plural form', () => {
    assert.equal(pluralize(0, 'item'), '0 items');
  });
  test('explicit plural overrides default', () => {
    assert.equal(pluralize(1, 'ox', 'oxen'), '1 ox');
    assert.equal(pluralize(2, 'ox', 'oxen'), '2 oxen');
  });
  test('1 selected (no noun — just number + word)', () => {
    // This is the "N selected" pattern; the noun is omitted by design.
    assert.equal(pluralize(1, 'selected'), '1 selected');
    assert.equal(pluralize(4, 'selected'), '4 selecteds'); // base, not used in UI
  });
});

// ---- the left-open group (Leave it open stays in the list) ------------------

describe('left open', () => {
  test('a row says "left open · date", the decision\'s date', () => {
    const now = new Date('2026-10-07T12:00:00Z');
    assert.equal(
      leftOpenMeta('2026-10-06T09:00:00Z', now),
      'left open \u00b7 Oct 6',
    );
    // Another year shows its year.
    assert.equal(
      leftOpenMeta('2025-12-30T09:00:00Z', now),
      'left open \u00b7 Dec 30, 2025',
    );
    assert.equal(leftOpenMeta(undefined, now), 'left open');
  });
});

// ---- « ‹ › »: the open item's place in the list ----------------------------

describe('navTargets', () => {
  const g = (id: string, label: string, total: number): GroupView => ({
    id,
    source: 'pi:s1',
    disposition: id,
    label,
    outward: false,
    many: '',
    total,
  });
  const groups = [
    g('close', 'Close it without merging', 27),
    g('keep', 'Leave it open', 2),
    g('merge', 'Merge it', 2),
  ];
  // The loaded rows in group order, a collapsed group's included.
  const order = [
    { key: 'pr:o/r#1', group: 'close' },
    { key: 'pr:o/r#2', group: 'close' },
    { key: 'pr:o/r#3', group: 'close' },
    { key: 'pr:o/r#4', group: 'keep' },
    { key: 'pr:o/r#5', group: 'keep' },
    { key: 'pr:o/r#6', group: 'merge' },
    { key: 'pr:o/r#7', group: 'merge' },
  ];

  test('the first item of the first group: no prev, no prevGroup, pos 1', () => {
    const n = navTargets(order, groups, 'pr:o/r#1');
    assert.equal(n.prev, undefined);
    assert.equal(n.prevGroup, undefined);
    assert.equal(n.pos, 1);
    assert.equal(n.total, 27);
    assert.equal(n.label, 'Close it without merging');
    assert.equal(n.next, 'pr:o/r#2');
    assert.equal(n.nextGroup, 'pr:o/r#4');
  });

  test("a group's last item: next is the next group's first item", () => {
    const n = navTargets(order, groups, 'pr:o/r#5');
    assert.equal(n.next, 'pr:o/r#6');
    assert.equal(n.prev, 'pr:o/r#4');
    assert.equal(n.pos, 2);
    assert.equal(n.total, 2);
    assert.equal(n.label, 'Leave it open');
    // The previous group's first item, not its last.
    assert.equal(n.prevGroup, 'pr:o/r#1');
    assert.equal(n.nextGroup, 'pr:o/r#6');
  });

  test('the last group: no nextGroup', () => {
    const n = navTargets(order, groups, 'pr:o/r#6');
    assert.equal(n.nextGroup, undefined);
    assert.equal(n.next, 'pr:o/r#7');
    assert.equal(n.prevGroup, 'pr:o/r#4');
    const last = navTargets(order, groups, 'pr:o/r#7');
    assert.equal(last.next, undefined);
    assert.equal(last.nextGroup, undefined);
  });

  test('an item not in the rows: pos 0, nothing to move to', () => {
    const n = navTargets(order, groups, 'pr:o/r#99');
    assert.deepEqual(n, { pos: 0, total: 0, label: '' });
  });
});

describe('shortKey', () => {
  test("a key without its kind and owner: what a nav button's tooltip names", () => {
    assert.equal(
      shortKey('pr:recreational-spreadsheeting/nfl-dk#467'),
      'nfl-dk#467',
    );
    assert.equal(
      shortKey('branch:schuettc/hail@feat/client'),
      'hail@feat/client',
    );
    assert.equal(shortKey('repo:schuettc/hail'), 'hail');
  });
});
