import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  displayPath,
  editDecision,
  entries,
  fieldOf,
  groupTargets,
  groupsOf,
  isBacklog,
  nextOpen,
  progress,
  rowTitle,
  editTarget,
  unsent,
  validVerdict,
} from './model.ts';

// Synthetic rows built here; no project data.
function mk(
  id: string,
  file: string,
  check: string,
  start: number,
  extra: Partial<Finding> = {},
): Finding {
  return {
    id,
    check,
    summary: `${check} summary`,
    source: { file, start, end: start },
    passage: `line ${start} of ${file}`,
    certain: false,
    fingerprint: `fp-${id}`,
    ...extra,
  };
}

const HOME = '/home/u';

test('validVerdict matches the Go enum', () => {
  for (const v of [
    'keep',
    'delete',
    'rewrite',
    'move',
    'merge:ab12',
    'drop:obsolete',
    'issue',
    'global',
    'private',
    'ask',
    'close:done',
    'close:obsolete',
    'close:tracked:owner/name#12',
    'close:tracked:name#3',
  ])
    assert.ok(validVerdict(v), v);
  for (const v of [
    '',
    'Keep',
    'merge:',
    'merge: ',
    'cut',
    'close',
    'close:later',
    'close:tracked:#3',
    'ask:x',
  ])
    assert.ok(!validVerdict(v), v);
});

test('displayPath: repo name and path, else ~ for home', () => {
  assert.equal(
    displayPath(
      {
        file: '/w/ws/muster/CLAUDE.md',
        repo: '/w/ws/muster',
        path: 'CLAUDE.md',
      },
      HOME,
    ),
    'muster/CLAUDE.md',
  );
  assert.equal(
    displayPath({ file: '/home/u/.claude/CLAUDE.md' }, HOME),
    '~/.claude/CLAUDE.md',
  );
  assert.equal(
    displayPath({ file: '', entry: 'MEMORY.md#3' }, HOME),
    'MEMORY.md#3',
  );
});

test('audit groups: by file, then check in the spec order, rows by line', () => {
  const rows = [
    mk('n2', '/home/u/b.md', 'negative-rule', 9),
    mk('n1', '/home/u/b.md', 'negative-rule', 3),
    mk('s1', '/home/u/b.md', 'size', 0),
    mk('d1', '/home/u/a.md', 'dead-path', 4),
  ];
  const gs = groupsOf(rows, false, HOME);
  assert.deepEqual(
    gs.map((g) => [g.kicker, g.title, g.rows.map((r) => r.id)]),
    [
      ['~/a.md', '1 dead path', ['d1']],
      ['~/b.md', '1 size finding', ['s1']],
      ['~/b.md', '2 negative rules', ['n1', 'n2']],
    ],
  );
});

test('backlog groups: issues by repo, then decisions, then closes', () => {
  const it = (id: string, v: string, dest = '') =>
    mk(id, '', 'intake', 0, {
      source: { file: '', entry: id },
      verdict: v,
      destination: dest,
    });
  const rows = [
    it('c1', 'close:done'),
    it('i2', 'issue', 'o/b'),
    it('a1', 'ask'),
    it('i1', 'issue', 'o/a'),
    it('i3', 'issue', 'o/a'),
  ];
  assert.ok(isBacklog({ kind: 'backlog' } as ReviewRound, []));
  assert.ok(isBacklog({ kind: 'on-demand' } as ReviewRound, rows));
  assert.ok(
    !isBacklog({ kind: 'on-demand' } as ReviewRound, [
      mk('x', '/f', 'size', 0),
    ]),
  );
  const gs = groupsOf(rows, true, HOME);
  assert.deepEqual(
    gs.map((g) => [g.kicker, g.title, g.rows.map((r) => r.id)]),
    [
      ['issues to file', 'o/a · 2 issues', ['i1', 'i3']],
      ['issues to file', 'o/b · 1 issue', ['i2']],
      ['decisions', '1 to decide', ['a1']],
      ['closes', '1 to close', ['c1']],
    ],
  );
  // An edited verdict moves a row to its new group.
  rows[2].decision = { action: 'edit', verdict: 'close:obsolete' };
  assert.deepEqual(
    groupsOf(rows, true, HOME).map((g) => g.title),
    ['o/a · 2 issues', 'o/b · 1 issue', '2 to close'],
  );
});

test('entries: a lone row stands alone; the open group is expanded', () => {
  const rows = [
    mk('a', '/home/u/a.md', 'dead-path', 4),
    mk('b1', '/home/u/b.md', 'negative-rule', 3),
    mk('b2', '/home/u/b.md', 'negative-rule', 9),
  ];
  const gs = groupsOf(rows, false, HOME);
  const keys = (open: string | null) => entries(gs, open).map((e) => e.key);
  assert.deepEqual(keys(null), ['r:a', `g:${gs[1].key}`]);
  assert.deepEqual(keys(`g:${gs[1].key}`), [
    'r:a',
    `g:${gs[1].key}`,
    'r:b1',
    'r:b2',
  ]);
  assert.deepEqual(keys('r:b2'), ['r:a', `g:${gs[1].key}`, 'r:b1', 'r:b2']);
  assert.ok(
    entries(gs, 'r:b2')[3].kind === 'row' && entries(gs, 'r:b2')[3].member,
  );
});

test('nextOpen: the next undecided row after a decision, else the next entry', () => {
  const rows = [
    mk('a', '/home/u/b.md', 'negative-rule', 1),
    mk('b', '/home/u/b.md', 'negative-rule', 2, {
      decision: { action: 'accept' },
    }),
    mk('c', '/home/u/b.md', 'negative-rule', 3),
    mk('z', '/home/u/z.md', 'size', 0),
  ];
  const gs = groupsOf(rows, false, HOME);
  const es = entries(gs, 'r:a');
  assert.equal(nextOpen(es, 'r:a'), 'r:c');
  assert.equal(nextOpen(es, 'r:c'), 'r:z');
  assert.equal(nextOpen(es, 'r:z'), 'r:z');
});

test('progress and unsent', () => {
  const rows = [
    mk('a', '/f', 'negative-rule', 1, {
      decision: { action: 'accept', sent: true },
    }),
    mk('b', '/f', 'negative-rule', 2, { decision: { action: 'reject' } }),
    mk('c', '/f', 'negative-rule', 3),
    mk('d', '/f', 'dead-path', 4, { certain: true }),
    mk('e', '/f', 'dead-path', 5, {
      certain: true,
      decision: { action: 'reject' },
    }),
  ];
  // A certain row is decided like any other.
  assert.deepEqual(progress(rows), { decided: 3, total: 5 });
  assert.equal(unsent(rows), 2);
});

test('groupTargets: accept takes undecided rows with a proposal; reject every undecided row', () => {
  const rows = [
    mk('a', '/f', 'negative-rule', 1, { verdict: 'rewrite', text: 'x' }),
    mk('b', '/f', 'negative-rule', 2),
    mk('c', '/f', 'negative-rule', 3, {
      verdict: 'delete',
      decision: { action: 'reject' },
    }),
  ];
  assert.deepEqual(
    groupTargets(rows, 'accept').map((r) => r.id),
    ['a'],
  );
  assert.deepEqual(
    groupTargets(rows, 'reject').map((r) => r.id),
    ['a', 'b'],
  );
});

test('editDecision sends only what changed, and refuses a bad verdict or no change', () => {
  const r = mk('a', '/f', 'negative-rule', 1, {
    verdict: 'rewrite',
    text: 'old',
    title: 't',
  });
  assert.deepEqual(
    editDecision(r, { verdict: 'rewrite', title: 't', text: 'new' }),
    {
      ok: true,
      decision: { action: 'edit', text: 'new' },
    },
  );
  assert.deepEqual(
    editDecision(r, { verdict: 'delete', title: 't', text: 'old' }),
    {
      ok: true,
      decision: { action: 'edit', verdict: 'delete' },
    },
  );
  const bad = editDecision(r, { verdict: 'cut', title: 't', text: 'old' });
  assert.ok(!bad.ok && bad.error.includes('not a verdict'));
  const same = editDecision(r, { verdict: 'rewrite', title: 't', text: 'old' });
  assert.ok(!same.ok && same.error.includes('nothing changed'));
  const none = editDecision(mk('b', '/f', 'size', 0), {
    verdict: '',
    title: '',
    text: 'x',
  });
  assert.ok(!none.ok && none.error.includes('verdict'));
});

test('editDecision clears a field the user emptied (explicit, not "use the proposal")', () => {
  const r = mk('a', '/f', 'misplaced', 1, {
    verdict: 'move',
    text: 'reworded',
    title: 't',
  });
  assert.deepEqual(editDecision(r, { verdict: 'move', title: 't', text: '' }), {
    ok: true,
    decision: { action: 'edit', cleared: ['text'] },
  });
  assert.deepEqual(
    editDecision(r, { verdict: 'move', title: '  ', text: 'reworded' }),
    { ok: true, decision: { action: 'edit', cleared: ['title'] } },
  );
  r.decision = { action: 'edit', cleared: ['text'] };
  assert.equal(fieldOf(r, 'text'), '');
  assert.equal(fieldOf(r, 'title'), 't');
  // Nothing to clear when the proposal had none.
  const bare = mk('b', '/f', 'misplaced', 2, { verdict: 'delete' });
  const same = editDecision(bare, { verdict: 'delete', title: '', text: '' });
  assert.ok(!same.ok && same.error.includes('nothing changed'));
});

test('rowTitle: issue title, else the passage first line, else the summary', () => {
  assert.equal(
    rowTitle(mk('a', '/f', 'x', 1, { title: 'An issue' })),
    'An issue',
  );
  assert.equal(
    rowTitle(mk('a', '/f', 'x', 1, { passage: '\n  - Never push.\nmore' })),
    '- Never push.',
  );
  assert.equal(
    rowTitle(mk('a', '/f', 'size', 0, { passage: '' })),
    'size summary',
  );
});

test('editTarget names the row an edit chooses to merge into', () => {
  assert.equal(editTarget({ action: 'edit', verdict: 'merge:r-2' }), 'r-2');
  assert.equal(editTarget({ action: 'edit', text: 'x' }), '');
  assert.equal(editTarget({ action: 'edit', verdict: 'delete' }), '');
  assert.equal(editTarget({ action: 'accept' }), '');
});
