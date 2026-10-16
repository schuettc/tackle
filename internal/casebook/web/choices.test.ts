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
  countWord,
  groupButtons,
  discardConfirm,
  pastedKeys,
  lookIntoCard,
} from './decide-math.ts';
import { sessionName } from './sessions.ts';
import type { DecisionVocabView, GroupView, NotNowForm } from './wire.d.ts';

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
  // Serve's words (decisionVocab) the helpers below fill in.
  recommends: '{session} recommends',
  discard_confirm:
    'Discard {count} in {view}? Their items go back to the agent to recommend again. Nothing is decided or rejected.',
  discard_one: '1 recommendation',
  discard_some: '{n} recommendations',
} as DecisionVocabView;
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

describe('recommendLine', () => {
  // A session serve knows, named as the rows and the recommendation box
  // name it.
  const by = {
    id: 'abc',
    harness: 'pi',
    label: '',
    cwd: '/home/court/tools-workspace',
    pid: 1,
    first_seen: '',
    last_seen: '',
    busy: false,
    queued: 0,
    name: 'tools-workspace/casebook',
    worker: false,
    eligible: true,
  };

  test("the session that recommends, by name, and serve's label for its kind", () => {
    assert.equal(
      recommendLine(VOCAB, 'issue:o/r#1', by, 'pi:abc', 'close'),
      'tools-workspace/casebook recommends Close it',
    );
  });

  test("no session known: the source's agent", () => {
    assert.equal(
      recommendLine(VOCAB, 'branch:o/r@x', undefined, 'rule:stale', 'delete'),
      'rule recommends Delete it',
    );
  });

  test('watch reads as Not now', () => {
    assert.equal(
      recommendLine(VOCAB, 'issue:o/r#1', by, 'pi:abc', 'watch'),
      'tools-workspace/casebook recommends Not now',
    );
  });
});

// The group buttons' words as serve sends them (the vocabulary).
const WORDS = { one: 'it', two: 'both', all: 'all {n}', some: '{n}' };
const GROUP_VOCAB = {
  look_many: 'Look into {n}',
  discard_many: 'Discard {n}',
  count_words: WORDS,
};

describe('countWord', () => {
  test('the whole group: it, both, all N; some ticked: N', () => {
    assert.equal(countWord(1, 0, WORDS), 'it');
    assert.equal(countWord(2, 0, WORDS), 'both');
    assert.equal(countWord(27, 0, WORDS), 'all 27');
    assert.equal(countWord(27, 3, WORDS), '3');
  });

  test('ticking every item counts as the whole group', () => {
    assert.equal(countWord(27, 27, WORDS), 'all 27');
  });
});

describe('groupButtons', () => {
  const close: GroupView = {
    id: 'pi:s1|close|Close it without merging',
    source: 'pi:s1',
    disposition: 'close',
    label: 'Close it without merging',
    outward: true,
    many: 'Close {n}',
    total: 27,
  };
  test('nothing ticked: the whole group', () => {
    assert.deepEqual(groupButtons(close, 0, GROUP_VOCAB), {
      act: 'Close all 27',
      look: 'Look into all 27',
      discard: 'Discard all 27',
    });
  });

  test('some ticked: those', () => {
    assert.deepEqual(groupButtons(close, 3, GROUP_VOCAB), {
      act: 'Close 3',
      look: 'Look into 3',
      discard: 'Discard 3',
    });
  });

  test("a choice's own wording around the count", () => {
    const keep = { ...close, many: 'Leave {n} open', total: 2 };
    assert.equal(groupButtons(keep, 0, GROUP_VOCAB).act, 'Leave both open');
  });
});

describe('discardConfirm', () => {
  test("serve's sentence, with the count and the view", () => {
    assert.equal(
      discardConfirm(VOCAB, 31, 'new'),
      VOCAB.discard_confirm
        .replace('{count}', VOCAB.discard_some.replace('{n}', '31'))
        .replace('{view}', 'new'),
    );
    assert.equal(
      discardConfirm(VOCAB, 1, 'new'),
      VOCAB.discard_confirm
        .replace('{count}', VOCAB.discard_one)
        .replace('{view}', 'new'),
    );
  });
});

describe('sessionName', () => {
  const s = {
    id: 'abc',
    harness: 'pi',
    label: '',
    cwd: '/home/court/tools-workspace',
    pid: 1,
    first_seen: '',
    last_seen: '',
    busy: false,
    queued: 0,
    name: 'tools-workspace/casebook',
    worker: false,
    eligible: true,
  };
  test('the session serve named: its title', () => {
    assert.equal(sessionName(s, 'pi:abc'), 'tools-workspace/casebook');
    assert.equal(sessionName({ ...s, name: '' }, 'pi:abc'), 'tools-workspace');
  });

  test("none named: the source's agent", () => {
    assert.equal(sessionName(undefined, 'pi:abc'), 'pi');
    assert.equal(sessionName(undefined, 'rule:stale'), 'rule');
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
