// choices.test.ts — unit tests for the decide step's pure helpers: the
// next undecided item to open after a decision, the until condition a Not now
// chip makes, the cards a selection of several kinds shares, and the sheet's
// fill label.
//
// Run with: node --test choices.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  nextUndecided,
  notNowUntil,
  choicesForKeys,
  fillLabel,
} from './decide-math.ts';
import type { DecisionVocabView, NotNowForm } from './wire.d.ts';

// The forms as serve sends them (item.NotNowForms).
const FORMS: NotNowForm[] = [
  {
    id: 'in-1w',
    label: 'in 1 week',
    template: 'date(%s)',
    asks: 'days',
    days: 7,
  },
  {
    id: 'in-1m',
    label: 'in 1 month',
    template: 'date(%s)',
    asks: 'days',
    days: 30,
  },
  { id: 'on-date', label: 'on a date…', template: 'date(%s)', asks: 'date' },
  {
    id: 'pr-merges',
    label: 'when a PR merges…',
    template: 'merged(%s)',
    asks: 'pr',
  },
  {
    id: 'quiet-90',
    label: 'when it goes quiet for 90 days',
    template: 'inactive(90d)',
    asks: '',
  },
];
const form = (id: string) => FORMS.find((f) => f.id === id)!;
// A fixed clock: midday on 2026-10-06, local time.
const NOW = new Date(2026, 9, 6, 12, 0, 0);

describe('nextUndecided', () => {
  const order = ['a', 'b', 'c', 'd'];

  test('in the middle: the next undecided after the current one', () => {
    assert.equal(nextUndecided(order, 'b', new Set(['a', 'c', 'd'])), 'c');
  });

  test('skips decided ones after the current one', () => {
    assert.equal(nextUndecided(order, 'a', new Set(['d'])), 'd');
  });

  test('the last one: wraps to an undecided one before it', () => {
    assert.equal(nextUndecided(order, 'd', new Set(['b'])), 'b');
  });

  test('the current one already gone from the order: the first undecided', () => {
    assert.equal(nextUndecided(['a', 'c', 'd'], 'b', new Set(['c', 'd'])), 'c');
  });

  test('none left: null', () => {
    assert.equal(nextUndecided(order, 'c', new Set()), null);
    assert.equal(nextUndecided(order, 'c', new Set(['c'])), null);
  });

  test('an undecided key outside the order still counts (a reload added it)', () => {
    assert.equal(nextUndecided(['a', 'b'], 'b', new Set(['z'])), 'z');
  });
});

describe('notNowUntil', () => {
  test('in 1 week: today + the days serve sends', () => {
    assert.equal(notNowUntil(form('in-1w'), '', NOW), 'date(2026-10-13)');
  });

  test('in 1 month: today + 30', () => {
    assert.equal(notNowUntil(form('in-1m'), '', NOW), 'date(2026-11-05)');
  });

  test('a PR merges: the key fills the hole', () => {
    assert.equal(
      notNowUntil(form('pr-merges'), 'pr:schuettc/tackle#5', NOW),
      'merged(pr:schuettc/tackle#5)',
    );
  });

  test('a form that asks is incomplete without its value', () => {
    assert.equal(notNowUntil(form('pr-merges'), '  ', NOW), null);
    assert.equal(notNowUntil(form('on-date'), '', NOW), null);
  });

  test('on a date: the date fills the hole', () => {
    assert.equal(
      notNowUntil(form('on-date'), '2026-12-01', NOW),
      'date(2026-12-01)',
    );
  });

  test('a fixed form is complete as it is', () => {
    assert.equal(notNowUntil(form('quiet-90'), '', NOW), 'inactive(90d)');
  });
});

const VOCAB: DecisionVocabView = {
  kinds: [
    {
      kind: 'issue',
      allowed: ['keep', 'close', 'wait', 'watch', 'ignore'],
      needs_until: ['wait', 'watch'],
      question: 'What should happen to this issue?',
      choices: [
        c('keep', 'Leave it open', 'stays'),
        c('close', 'Close it', 'goes to To apply', true),
        c('wait', 'Not now', 'hidden', false, true),
        c('ignore', 'Stop tracking it', 'never asks'),
      ],
    },
    {
      kind: 'branch',
      allowed: ['keep', 'delete', 'wait', 'watch', 'ignore'],
      needs_until: ['wait', 'watch'],
      question: 'What should happen to this branch?',
      choices: [
        c('keep', 'Keep it', 'stays as it is'),
        c('delete', 'Delete it', 'goes to To apply', true),
        c('wait', 'Not now', 'hidden', false, true),
        c('ignore', 'Stop tracking it', 'never asks'),
      ],
    },
  ],
  until_forms: null,
  not_now: FORMS,
};
function c(
  disposition: string,
  label: string,
  says: string,
  outward = false,
  needs_until = false,
) {
  return { disposition, label, says, outward, needs_until };
}

describe('choicesForKeys', () => {
  test('one kind: its choices as serve lists them', () => {
    const got = choicesForKeys(VOCAB, ['issue:o/r#1', 'issue:o/r#2']);
    assert.deepEqual(
      got.map((x) => x.label),
      ['Leave it open', 'Close it', 'Not now', 'Stop tracking it'],
    );
  });

  test('several kinds: the shared choices, differing labels joined', () => {
    const got = choicesForKeys(VOCAB, ['issue:o/r#1', 'branch:o/r@x']);
    assert.deepEqual(
      got.map((x) => [x.disposition, x.label]),
      [
        ['keep', 'Leave it open / Keep it'],
        ['wait', 'Not now'],
        ['ignore', 'Stop tracking it'],
      ],
    );
  });
});

describe('fillLabel', () => {
  test('the choice and the count, in the kind’s noun', () => {
    assert.equal(
      fillLabel('Close it', ['issue:o/r#1', 'issue:o/r#2']),
      'Close it · 2 issues',
    );
    assert.equal(
      fillLabel('Merge it', ['pr:o/r#1']),
      'Merge it · 1 pull request',
    );
    assert.equal(
      fillLabel('Keep it', ['repo:o/a', 'repo:o/b']),
      'Keep it · 2 repositories',
    );
  });

  test('several kinds: items', () => {
    assert.equal(
      fillLabel('Not now', ['issue:o/r#1', 'branch:o/r@x']),
      'Not now · 2 items',
    );
  });
});
