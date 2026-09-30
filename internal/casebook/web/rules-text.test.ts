// rules-text.test.ts — unit tests for the Rules section's words.
//
// Run with: node --test rules-text.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  trackRecord,
  authorOf,
  reasonSummary,
  matchesHeading,
  conditionErrorIndex,
  viewCounts,
  sameConditions,
  sameRule,
  rowKicker,
} from './rules-text.ts';

describe('trackRecord', () => {
  test('the spec’s form: accepted · rejected · pending', () => {
    assert.equal(
      trackRecord({ accepted: 87, rejected: 4, pending: 3 }),
      '87 accepted · 4 rejected · 3 pending',
    );
  });
  test('zero parts are left out', () => {
    assert.equal(
      trackRecord({ accepted: 89, rejected: 0, pending: 0 }),
      '89 accepted',
    );
    assert.equal(
      trackRecord({ accepted: 0, rejected: 0, pending: 3 }),
      '3 pending',
    );
  });
  test('a rule with no record yet has none to show', () => {
    assert.equal(trackRecord({ accepted: 0, rejected: 0, pending: 0 }), '');
  });
});

describe('rowKicker', () => {
  const none = { accepted: 0, rejected: 0, pending: 0 };
  test('the mock\'s "849 match · 2 excluded"', () => {
    assert.equal(
      rowKicker({ matches: 849, excluded: 2, record: none }),
      '849 match \u00b7 2 excluded',
    );
  });
  test('then the record', () => {
    assert.equal(
      rowKicker({
        matches: 3,
        excluded: 0,
        record: { accepted: 87, rejected: 4, pending: 3 },
      }),
      '3 match \u00b7 0 excluded \u00b7 87 accepted \u00b7 4 rejected \u00b7 3 pending',
    );
  });
  test('an invalid rule says so instead of counting', () => {
    assert.equal(
      rowKicker({
        matches: 0,
        excluded: 1,
        record: none,
        invalid: 'condition 1: x',
      }),
      'not valid',
    );
  });
});

describe('authorOf', () => {
  test('an agent session (harness:session) is the agent', () => {
    assert.deepEqual(authorOf('pi:abc-123'), { agent: true, name: 'pi' });
    assert.deepEqual(authorOf('claude:s1'), { agent: true, name: 'claude' });
  });
  test('the configured user is "you"', () => {
    assert.deepEqual(authorOf('schuettc'), { agent: false, name: 'you' });
    assert.deepEqual(authorOf('court'), { agent: false, name: 'you' });
  });
});

describe('reasonSummary / matchesHeading', () => {
  const by = [
    { reason: 'in main', count: 394 },
    { reason: 'via merged PR', count: 455 },
  ];
  test('counts by reason, lower case', () => {
    assert.equal(reasonSummary(by), '394 in main · 455 via merged pr');
  });
  test('a lone "matched" says nothing more than the total', () => {
    assert.equal(reasonSummary([{ reason: 'matched', count: 3 }]), '');
    assert.equal(
      matchesHeading(3, [{ reason: 'matched', count: 3 }]),
      'matches now · 3',
    );
  });
  test('the heading', () => {
    assert.equal(
      matchesHeading(849, by),
      'matches now · 849 · 394 in main · 455 via merged pr',
    );
    assert.equal(matchesHeading(0, []), 'matches now · 0');
  });
});

describe('conditionErrorIndex', () => {
  test('reads the index serve names', () => {
    assert.equal(
      conditionErrorIndex(
        'condition 2: field "age" op "older-than": bad duration "7x"',
      ),
      2,
    );
    assert.equal(conditionErrorIndex('condition 0: unknown field "x"'), 0);
  });
  test('-1 when no condition is named', () => {
    assert.equal(conditionErrorIndex('bad request body: EOF'), -1);
  });
});

describe('viewCounts', () => {
  test('all, active and drafts', () => {
    assert.deepEqual(
      viewCounts([
        { status: 'draft' },
        { status: 'active' },
        { status: 'draft' },
      ]),
      { all: 3, active: 1, drafts: 2 },
    );
  });
});

describe('sameConditions', () => {
  test('compares field, op and value in order', () => {
    const a = [{ field: 'kind', op: 'is', value: 'branch' }];
    assert.equal(
      sameConditions(a, [{ field: 'kind', op: 'is', value: 'branch' }]),
      true,
    );
    assert.equal(
      sameConditions(a, [{ field: 'kind', op: 'is', value: 'pr' }]),
      false,
    );
    assert.equal(sameConditions(a, []), false);
  });
});

describe('sameRule', () => {
  const rule = {
    id: 'r',
    name: 'R',
    status: 'draft',
    created_by: 'court',
    created_at: '2026-09-27T10:00:00Z',
    edited_at: '2026-09-27T10:00:00Z',
    match: [{ field: 'kind', op: 'is', value: 'branch' }],
    propose: { disposition: 'delete' },
    exclude: [{ key: 'branch:o/r@x', reason: 'keep', by: 'court', at: 't' }],
  };
  test('the same rule, whatever its key order', () => {
    const { exclude, ...rest } = rule;
    assert.equal(sameRule(rule, { exclude, ...rest }), true);
  });
  test('a status, an exclusion or its reason, or an edit differs', () => {
    assert.equal(sameRule(rule, { ...rule, status: 'active' }), false);
    assert.equal(sameRule(rule, { ...rule, exclude: [] }), false);
    assert.equal(
      sameRule(rule, {
        ...rule,
        exclude: [{ ...rule.exclude[0], reason: 'other' }],
      }),
      false,
    );
    assert.equal(sameRule(rule, { ...rule, edited_at: 'later' }), false);
    assert.equal(
      sameRule(rule, { ...rule, propose: { disposition: 'keep' } }),
      false,
    );
  });
});
