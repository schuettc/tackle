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
  hasKindCondition,
  kindHint,
  refusedDisposition,
  slug,
  fieldLabel,
  opLabel,
  valueLabel,
  conditionText,
  pickedValues,
  pickedCondition,
  typesOf,
  fieldApplies,
  fieldTypes,
  fieldFound,
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
  test('serve counts conditions from 1; the index is the row, from 0', () => {
    assert.equal(
      conditionErrorIndex(
        'condition 2: field "age" op "older-than": bad duration "7x"',
      ),
      1,
    );
    assert.equal(conditionErrorIndex('condition 1: unknown field "x"'), 0);
  });
  test('-1 when no condition is named', () => {
    assert.equal(conditionErrorIndex('bad request body: EOF'), -1);
    assert.equal(
      conditionErrorIndex('condition 0: not a number serve writes'),
      -1,
    );
    assert.equal(conditionErrorIndex('propose: wait needs an until'), -1);
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

describe('the proposal without a kind condition (P1)', () => {
  const all = [
    'keep',
    'archive',
    'delete',
    'wait',
    'watch',
    'ignore',
    'close',
    'merge',
  ];
  const noKind = ['keep', 'wait', 'ignore'];
  test('hasKindCondition reads only kind conditions', () => {
    assert.equal(hasKindCondition([]), false);
    assert.equal(
      hasKindCondition([{ field: 'landed', op: 'is', value: 'all-machines' }]),
      false,
    );
    assert.equal(
      hasKindCondition([{ field: 'kind', op: 'is-not', value: 'repo' }]),
      true,
    );
  });
  test('the picker says what a kind condition would add', () => {
    assert.equal(
      kindHint([], noKind, all),
      'add a kind condition to propose archive, close, delete, merge or watch',
    );
  });
  test('no hint once the rule has a kind condition, or nothing is missing', () => {
    assert.equal(
      kindHint([{ field: 'kind', op: 'is', value: 'branch' }], noKind, all),
      '',
    );
    assert.equal(kindHint([], all, all), '');
    assert.equal(kindHint([], [], all), '');
  });
  test('a lone missing disposition reads without a list', () => {
    assert.equal(
      kindHint(
        [],
        ['keep', 'wait', 'ignore', 'merge'],
        ['keep', 'wait', 'ignore', 'merge', 'close'],
      ),
      'add a kind condition to propose close',
    );
  });
  test("a refused disposition names the cause in serve's words", () => {
    assert.equal(
      refusedDisposition('archive', [], noKind),
      'archive can\u2019t be proposed for every kind of item (the rule has no kind condition) (allowed: keep, wait, ignore)',
    );
    assert.equal(
      refusedDisposition(
        'archive',
        [{ field: 'kind', op: 'is', value: 'branch' }],
        ['keep', 'delete'],
      ),
      'archive can\u2019t be proposed for what this rule matches (allowed: keep, delete)',
    );
  });
});

describe('slug', () => {
  test('a name becomes an id: lower case, a-z 0-9 and hyphens', () => {
    assert.strictEqual(slug('Outgoing PRs'), 'outgoing-prs');
    assert.strictEqual(slug('  Repos: not pushed!! '), 'repos-not-pushed');
  });
});

describe('labels', () => {
  const kind = {
    name: 'kind',
    label: 'type',
    help: 'repository, pull request, issue, branch or worktree',
    type: 0,
    ops: ['is'],
    values: ['repo', 'pr'],
    value_labels: { repo: 'repository', pr: 'pull request' },
  };
  const bot = {
    name: 'bot',
    label: 'bot',
    help: '',
    type: 3,
    ops: ['is'],
    values: null,
    value_labels: { true: 'yes', false: 'no' },
  };
  const ops = [
    { name: 'is', label: 'is' },
    { name: 'is-not', label: 'is not' },
    { name: 'not-in', label: 'is none of' },
  ];
  test('a field reads its label, else its name', () => {
    assert.equal(fieldLabel(kind), 'type');
    assert.equal(fieldLabel({ name: 'x', label: '' }), 'x');
  });
  test('an operator reads its label, else its name', () => {
    assert.equal(opLabel(ops, 'not-in'), 'is none of');
    assert.equal(opLabel(ops, 'glob'), 'glob');
  });
  test('a value reads its label, else itself', () => {
    assert.equal(valueLabel(kind, 'pr'), 'pull request');
    assert.equal(valueLabel(bot, 'false'), 'no');
    assert.equal(valueLabel(kind, 'other'), 'other');
    assert.equal(valueLabel(undefined, 'pr'), 'pr');
  });
  test('a condition reads in labels; in lists label each value', () => {
    assert.equal(
      conditionText({ field: 'kind', op: 'is', value: 'pr' }, [kind], ops),
      'type is pull request',
    );
    assert.equal(
      conditionText(
        { field: 'kind', op: 'not-in', value: 'repo, pr' },
        [kind],
        ops,
      ),
      'type is not repository or pull request',
    );
    assert.equal(
      conditionText({ field: 'gone', op: 'is', value: 'x' }, [kind], ops),
      'gone is x',
    );
  });
});

describe('a fixed-value field picks its values', () => {
  const kind = {
    name: 'kind',
    label: 'type',
    help: '',
    type: 0,
    ops: ['is', 'is-not', 'in', 'not-in'],
    values: ['repo', 'pr', 'issue', 'branch', 'worktree'],
    value_labels: {
      repo: 'repository',
      pr: 'pull request',
      issue: 'issue',
      branch: 'branch',
      worktree: 'worktree',
    },
    kinds: null,
  };
  const unpushed = {
    name: 'unpushed',
    label: 'unpushed commits',
    help: 'commits on no remote',
    type: 3,
    ops: ['is', 'is-not'],
    values: null,
    value_labels: { true: 'yes', false: 'no' },
    kinds: ['branch', 'worktree'],
  };
  const updated = {
    name: 'updated',
    label: 'last activity',
    help: 'last comment or update, push, or commit',
    type: 2,
    ops: ['older-than', 'newer-than'],
    values: null,
    value_labels: null,
    kinds: null,
  };
  const ops = [
    { name: 'is', label: 'is' },
    { name: 'is-not', label: 'is not' },
  ];
  test('a condition reads as ticked values, and not', () => {
    assert.deepEqual(pickedValues({ field: 'kind', op: 'is', value: 'pr' }), {
      not: false,
      values: ['pr'],
    });
    assert.deepEqual(
      pickedValues({ field: 'kind', op: 'in', value: 'issue, pr' }),
      { not: false, values: ['issue', 'pr'] },
    );
    assert.deepEqual(
      pickedValues({ field: 'kind', op: 'not-in', value: 'repo,worktree' }),
      { not: true, values: ['repo', 'worktree'] },
    );
    assert.deepEqual(
      pickedValues({ field: 'kind', op: 'is-not', value: 'repo' }),
      { not: true, values: ['repo'] },
    );
  });
  test('one value is is, several in; not makes them is-not and not-in', () => {
    assert.deepEqual(pickedCondition('kind', false, ['issue']), {
      field: 'kind',
      op: 'is',
      value: 'issue',
    });
    assert.deepEqual(pickedCondition('kind', false, ['issue', 'pr']), {
      field: 'kind',
      op: 'in',
      value: 'issue,pr',
    });
    assert.deepEqual(pickedCondition('kind', true, ['repo', 'worktree']), {
      field: 'kind',
      op: 'not-in',
      value: 'repo,worktree',
    });
    assert.deepEqual(pickedCondition('kind', true, ['repo']), {
      field: 'kind',
      op: 'is-not',
      value: 'repo',
    });
  });
  test('the row reads "type is issue or pull request"', () => {
    assert.equal(
      conditionText(
        pickedCondition('kind', false, ['issue', 'pr']),
        [kind],
        ops,
      ),
      'type is issue or pull request',
    );
    assert.equal(
      conditionText(
        pickedCondition('kind', true, ['repo', 'worktree']),
        [kind],
        ops,
      ),
      'type is not repository or worktree',
    );
  });
  test('the type condition leaves its types; fields apply to every one', () => {
    const all = kind.values;
    assert.equal(typesOf([], all), null);
    assert.deepEqual(
      typesOf([{ field: 'kind', op: 'in', value: 'issue,pr' }], all),
      ['pr', 'issue'],
    );
    assert.deepEqual(
      typesOf([{ field: 'kind', op: 'is-not', value: 'repo' }], all),
      ['pr', 'issue', 'branch', 'worktree'],
    );
    assert.equal(fieldApplies(unpushed, ['issue', 'pr']), false);
    assert.equal(fieldApplies(unpushed, ['branch', 'worktree']), true);
    assert.equal(fieldApplies(unpushed, ['branch', 'issue']), false);
    assert.equal(fieldApplies(unpushed, null), true);
    assert.equal(fieldApplies(updated, ['issue']), true);
  });
  test('a field that applies to some types names them', () => {
    assert.equal(fieldTypes(unpushed, kind), 'branch, worktree');
    assert.equal(fieldTypes(updated, kind), '');
  });
  test('the search finds a field by label or help', () => {
    assert.equal(fieldFound(updated, 'upd'), true);
    assert.equal(fieldFound(updated, 'Last'), true);
    assert.equal(fieldFound(unpushed, 'upd'), false);
    assert.equal(fieldFound(unpushed, ''), true);
  });
});
