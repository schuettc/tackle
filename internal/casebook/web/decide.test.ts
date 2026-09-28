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
import { kindFromKey, allowedForKind, allowedForKeys } from './decide-math.ts';
import type { DecisionVocabView } from './wire.d.ts';

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
