// choices.test.ts — unit tests for the decide step's pure helpers: the
// next undecided item to open after a decision, the until condition a Not now
// chip makes, the cards a selection of several kinds shares, and the sheet's
// fill label.
//
// Run with: node --test choices.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  nextInView,
  latestSearch,
  MORE,
  notNowUntil,
  choicesForKeys,
  fillLabel,
  recommendLine,
  agreeGroups,
  agreeText,
  recommendedLine,
  pastedKeys,
  lookIntoCard,
  lookIntoAll,
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

describe('nextInView', () => {
  const order = ['a', 'b', 'c', 'd'];

  test('in the middle: the next undecided after the current one', () => {
    assert.equal(nextInView(order, 'b', ['a', 'c', 'd'], true), 'c');
  });

  test('skips decided ones after the current one', () => {
    assert.equal(nextInView(order, 'a', ['d'], true), 'd');
  });

  test('the last one: wraps to an undecided one before it', () => {
    assert.equal(nextInView(order, 'd', ['b'], true), 'b');
  });

  test('the current one already gone from the order: the first undecided', () => {
    assert.equal(nextInView(['a', 'c', 'd'], 'b', ['c', 'd'], true), 'c');
  });

  test('none left: null', () => {
    assert.equal(nextInView(order, 'c', [], true), null);
    assert.equal(nextInView(order, 'c', ['c'], true), null);
  });

  test('an undecided key outside the order still counts (a reload added it)', () => {
    assert.equal(nextInView(['a', 'b'], 'b', ['z'], true), 'z');
  });

  test('the next one not fetched yet: more, not a wrap', () => {
    // Page two holds b..d; serve's first page (a, then what slid up) is in.
    assert.equal(nextInView(order, 'b', ['a'], false), MORE);
  });

  test('the next one in a later page once fetched', () => {
    assert.equal(nextInView(order, 'b', ['a', 'c', 'd'], true), 'c');
  });

  test('the last loaded row: the row past it, before any wrap', () => {
    assert.equal(nextInView(['a', 'b'], 'b', ['a'], false), MORE);
    assert.equal(nextInView(['a', 'b'], 'b', ['a', 'x', 'y'], true), 'x');
  });

  test('a later row gone while an even later one is in: passed over', () => {
    assert.equal(nextInView(order, 'a', ['b', 'd'], false), 'b');
    assert.equal(nextInView(order, 'b', ['d'], false), 'd');
  });

  test('nothing after it, not complete: more before wrapping', () => {
    assert.equal(nextInView(order, 'd', ['a'], false), MORE);
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

// A pending proposal on key from source.
function pending(key: string, disposition: string, source = 'pi:s1', id = 0) {
  return {
    key,
    proposal: { id, key, disposition, source, state: 'pending' },
  };
}

describe('recommendLine', () => {
  test("the row's line: the agent and serve's label for its kind", () => {
    assert.equal(
      recommendLine(VOCAB, 'issue:o/r#1', 'pi:abc', 'close'),
      'pi recommends Close it',
    );
    assert.equal(
      recommendLine(VOCAB, 'branch:o/r@x', 'rule:stale', 'delete'),
      'rule recommends Delete it',
    );
  });

  test('watch reads as Not now', () => {
    assert.equal(
      recommendLine(VOCAB, 'issue:o/r#1', 'pi:abc', 'watch'),
      'pi recommends Not now',
    );
  });

  test('before the vocabulary loads: the disposition', () => {
    assert.equal(
      recommendLine(null, 'issue:o/r#1', 'pi:abc', 'close'),
      'pi recommends close',
    );
  });
});

describe('agreeGroups', () => {
  test('two or more sharing an agent and a choice make a group, in list order', () => {
    const items = [
      pending('issue:o/r#1', 'keep', 'pi:s1', 1),
      pending('issue:o/r#2', 'close', 'pi:s1', 2),
      pending('issue:o/r#3', 'keep', 'pi:s1', 3),
      { key: 'issue:o/r#4' },
      pending('issue:o/r#5', 'keep', 'pi:s2', 5),
      pending('issue:o/r#6', 'close', 'rule:x', 6),
    ];
    const got = agreeGroups(VOCAB, items);
    assert.equal(got.length, 1);
    assert.deepEqual(got[0].ids, [1, 3, 5]);
    assert.deepEqual(got[0].keys, [
      'issue:o/r#1',
      'issue:o/r#3',
      'issue:o/r#5',
    ]);
    assert.equal(got[0].label, 'Leave it open');
    assert.equal(got[0].agent, 'pi');
    assert.equal(got[0].outward, false);
  });

  test('settled proposals and one-item choices make none', () => {
    const items = [
      pending('issue:o/r#1', 'keep', 'pi:s1', 1),
      {
        key: 'issue:o/r#2',
        proposal: {
          id: 2,
          key: 'issue:o/r#2',
          disposition: 'keep',
          source: 'pi:s1',
          state: 'rejected',
        },
      },
    ];
    assert.deepEqual(agreeGroups(VOCAB, items), []);
  });

  test('an outward choice is marked, kinds worded differently are joined', () => {
    const got = agreeGroups(VOCAB, [
      pending('issue:o/r#1', 'keep', 'pi:s1', 1),
      pending('branch:o/r@x', 'keep', 'pi:s1', 2),
      pending('issue:o/r#3', 'close', 'pi:s1', 3),
      pending('issue:o/r#4', 'close', 'pi:s1', 4),
    ]);
    assert.deepEqual(
      got.map((g) => [g.label, g.outward, g.ids.length]),
      [
        ['Leave it open / Keep it', false, 2],
        ['Close it', true, 2],
      ],
    );
  });
});

describe('agreeText', () => {
  const g = (outward: boolean) => ({
    agent: 'pi',
    disposition: outward ? 'close' : 'keep',
    label: outward ? 'Close it' : 'Leave it open',
    outward,
    ids: [1, 2, 3],
    keys: ['a', 'b', 'c'],
  });
  test('a group to agree with', () => {
    assert.deepEqual(agreeText(g(false)), {
      says: 'pi recommends Leave it open for 3',
      action: 'agree with all 3',
    });
  });
  test('an outward group goes to To apply', () => {
    assert.deepEqual(agreeText(g(true)), {
      says: 'pi recommends Close it for 3',
      action: 'send all 3 to To apply',
    });
  });
});

describe('recommendedLine', () => {
  test('the counts', () => {
    assert.equal(recommendedLine(18, 13), '18 recommended \u00b7 13 not yet');
    assert.equal(recommendedLine(0, 0), '0 recommended \u00b7 0 not yet');
  });
});

describe('pastedKeys', () => {
  test('a pull request URL', () => {
    assert.deepEqual(
      pastedKeys('https://github.com/owner/repo/pull/58', 'pr'),
      ['pr:owner/repo#58'],
    );
  });
  test('an issues URL, for a PR or issue', () => {
    assert.deepEqual(
      pastedKeys('https://github.com/owner/repo/issues/58', 'pr-or-issue'),
      ['issue:owner/repo#58'],
    );
  });
  test('an issues URL is no PR', () => {
    assert.equal(
      pastedKeys('https://github.com/owner/repo/issues/58', 'pr'),
      null,
    );
  });
  test('a trailing slash, a tab, a #fragment and a query string', () => {
    for (const url of [
      'https://github.com/owner/repo/pull/58/',
      'https://github.com/owner/repo/pull/58/files',
      'https://github.com/owner/repo/pull/58#issuecomment-1',
      'https://github.com/owner/repo/pull/58?notification_referrer_id=x',
      '  https://www.github.com/Owner/Repo/pull/58/files?w=1#diff  ',
    ])
      assert.deepEqual(pastedKeys(url, 'pr'), ['pr:owner/repo#58'], url);
  });
  test('a key, with or without its kind', () => {
    assert.deepEqual(pastedKeys('pr:owner/repo#58', 'pr'), [
      'pr:owner/repo#58',
    ]);
    assert.deepEqual(pastedKeys('owner/repo#58', 'pr'), ['pr:owner/repo#58']);
    assert.deepEqual(pastedKeys('owner/repo#58', 'pr-or-issue'), [
      'pr:owner/repo#58',
      'issue:owner/repo#58',
    ]);
    assert.deepEqual(pastedKeys('issue:o.x/r_y-z#7', 'pr-or-issue'), [
      'issue:o.x/r_y-z#7',
    ]);
  });
  test('a repo', () => {
    assert.deepEqual(pastedKeys('https://github.com/owner/repo', 'repo'), [
      'repo:owner/repo',
    ]);
    assert.deepEqual(
      pastedKeys('https://github.com/owner/repo/releases/', 'repo'),
      ['repo:owner/repo'],
    );
    assert.deepEqual(pastedKeys('owner/repo', 'repo'), ['repo:owner/repo']);
    assert.deepEqual(pastedKeys('repo:owner/repo', 'repo'), [
      'repo:owner/repo',
    ]);
    assert.equal(
      pastedKeys('https://github.com/owner/repo/pull/58', 'repo'),
      null,
    );
  });
  test('junk, a title or a number is no key', () => {
    for (const s of [
      '',
      'zzqx',
      'Add a retry to the uploader',
      'tackle#58',
      '#58',
      '58',
      'https://example.com/owner/repo/pull/58',
      'https://github.com/owner/repo/pull/abc',
      'https://github.com/owner',
      'pr:owner/repo#0',
      'branch:owner/repo@main',
    ])
      assert.equal(pastedKeys(s, 'pr'), null, s);
  });
});

describe('latestSearch', () => {
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

  test('an older answer landing after newer input publishes nothing', async () => {
    const answers = new Map<string, (found: string[]) => void>();
    const published: string[] = [];
    let stale = 0;
    const search = latestSearch(
      (text) => new Promise<string[]>((r) => answers.set(text, r)),
      (text) => published.push(text),
      20,
      () => stale++,
    );
    search.input('ret');
    await wait(40);
    assert.ok(answers.has('ret'), "the search for 'ret' is under way");
    // Newer input; its own search waits for the pause in typing.
    search.input('retry');
    assert.equal(stale, 2, 'each input makes the shown options stale at once');
    answers.get('ret')?.(['old']);
    await wait(0);
    assert.deepEqual(published, [], "the answer for 'ret' is dropped");
    await wait(40);
    answers.get('retry')?.(['new']);
    await wait(0);
    assert.deepEqual(published, ['retry']);
  });

  test('cancel drops a search under way', async () => {
    const answers = new Map<string, (found: string[]) => void>();
    const published: string[] = [];
    const search = latestSearch(
      (text) => new Promise<string[]>((r) => answers.set(text, r)),
      (text) => published.push(text),
      0,
      () => {},
    );
    search.input('a');
    await wait(10);
    search.cancel();
    answers.get('a')?.([]);
    await wait(0);
    assert.deepEqual(published, []);
  });
});

// The look-into words as serve sends them (the vocabulary's look_into).
const LOOK = {
  label: 'Look into it',
  says: "{session} checks its CI and recent activity, finds what's wrong, and comes back with a recommendation. Nothing is decided yet.",
  message: 'Look into {key}: check it.',
  message_many: 'Look into these items: {keys}. For each, check it.',
};

describe('lookIntoCard', () => {
  test("the card: serve's label, its sentence with the session named, the message for the key", () => {
    assert.deepEqual(lookIntoCard(LOOK, 'pi \u00b7 a', 'pr:o/r#1'), {
      label: 'Look into it',
      says: "pi \u00b7 a checks its CI and recent activity, finds what's wrong, and comes back with a recommendation. Nothing is decided yet.",
      message: 'Look into pr:o/r#1: check it.',
    });
  });
});

describe('lookIntoAll', () => {
  test("one message naming every key, and the foot's action", () => {
    assert.deepEqual(lookIntoAll(LOOK, 'pi \u00b7 a', ['a#1', 'a#2', 'a#3']), {
      action: 'ask pi \u00b7 a to look into all 3',
      message: 'Look into these items: a#1, a#2, a#3. For each, check it.',
    });
  });
});
