import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  certainLines,
  decideLocal,
  diffLines,
  fileEntries,
  fileMeta,
  filesProgress,
  hunks,
  printsFor,
  unsentFiles,
} from './files.ts';

// Synthetic files built here; no project data.
function mk(
  key: string,
  path: string,
  extra: Partial<FileView> = {},
): FileView {
  return {
    key,
    path,
    source: { file: path },
    class: 'repo',
    budget: 6000,
    base: 'b-' + key,
    size: 4200,
    after: 3100,
    rows: ['r-' + key],
    rec: {
      file: key,
      base: 'b-' + key,
      content: 'x\n',
      findings: [{ row: 'r-' + key, did: 'fixed', how: 'guidance' }],
      summary: 's',
    },
    decision: null,
    fingerprint: 'fp-' + key,
    group: [key],
    ...extra,
  };
}

test('diffLines: a line diff that keeps, removes and adds, with line numbers', () => {
  const ops = diffLines('a\nb\nc\nd\n', 'a\nB\nc\nd\ne\n');
  assert.deepEqual(
    ops.map((o) => `${o.kind}${o.old ?? ''}/${o.new ?? ''}:${o.text}`),
    [' 1/1:a', '-2/:b', '+/2:B', ' 3/3:c', ' 4/4:d', '+/5:e'],
  );
  assert.deepEqual(
    diffLines('same\n', 'same\n').map((o) => o.kind),
    [' '],
  );
  assert.deepEqual(
    diffLines('', 'new\n').map((o) => o.kind),
    ['+'],
  );
});

test('diffLines handles a file the size of a real one quickly', () => {
  const a = Array.from({ length: 600 }, (_, i) => `- rule ${i}`).join('\n');
  const b = a.replace('- rule 300', '- changed').replace('- rule 10\n', '');
  const t0 = Date.now();
  const ops = diffLines(a, b);
  assert.ok(Date.now() - t0 < 500);
  assert.equal(ops.filter((o) => o.kind !== ' ').length, 3);
});

test('hunks: changes with three lines of context, far changes apart', () => {
  const a = Array.from({ length: 30 }, (_, i) => `l${i + 1}`).join('\n') + '\n';
  const b = a.replace('l5\n', 'L5\n').replace('l25\n', '');
  const hs = hunks(diffLines(a, b), 3);
  assert.equal(hs.length, 2);
  assert.equal(hs[0].lines[0].old, 2);
  assert.equal(hs[0].lines.at(-1)?.old, 8);
  assert.ok(hs[1].lines.some((l) => l.kind === '-' && l.text === 'l25'));
  assert.equal(hunks(diffLines('x\n', 'x\n'), 3).length, 0);
});

test('certainLines: the audited lines of the certain findings', () => {
  const rows = [
    {
      id: 'a',
      check: 'dead-path',
      summary: '',
      source: { file: '/f', start: 3, end: 4 },
      certain: true,
      fingerprint: '',
    },
    {
      id: 'b',
      check: 'negative-rule',
      summary: '',
      source: { file: '/f', start: 9, end: 9 },
      certain: false,
      fingerprint: '',
    },
  ] as Finding[];
  assert.deepEqual([...certainLines(rows)].sort(), [3, 4]);
});

test('fileEntries: files with findings or a recommendation, linked files together', () => {
  const g = mk('g', '/home/u/.agent/AGENTS.md', {
    group: ['g', 'm'],
    class: 'global',
  });
  const m = mk('m', '/w/m/AGENTS.md', { group: ['g', 'm'] });
  const a = mk('a', '/w/a/AGENTS.md');
  const z = mk('z', '/w/z/AGENTS.md');
  const clean = mk('c', '/w/c/AGENTS.md', { rows: [], rec: null });
  const es = fileEntries([z, m, clean, a, g], '/home/u');
  assert.deepEqual(
    es.map((e) => e.file.key),
    ['g', 'm', 'a', 'z'],
  );
  assert.deepEqual(
    es.map((e) => e.linked),
    [true, true, false, false],
  );
});

test('fileMeta: sizes before and after, findings, decision', () => {
  const f = mk('a', '/w/a/AGENTS.md', { rows: ['1', '2', '3'] });
  assert.equal(fileMeta(f), '4.2 → 3.1 KB · 3 ·');
  f.decision = { action: 'edit', content: 'mine' };
  assert.equal(fileMeta(f), '4.2 → 3.1 KB · 3 · edited');
});

test('decideLocal mirrors the store: linked files decided together, an edit keeps the link', () => {
  const files = [
    mk('g', '/g', { group: ['g', 'm'] }),
    mk('m', '/m', { group: ['g', 'm'] }),
    mk('q', '/q'),
  ];
  const by = (k: string) => files.find((f) => f.key === k)!;
  decideLocal(files, 'g', { action: 'accept', note: 'yes' });
  assert.equal(by('g').decision?.action, 'accept');
  assert.equal(by('m').decision?.action, 'accept');
  assert.equal(by('m').decision?.note ?? '', '');
  assert.equal(by('q').decision, null);
  decideLocal(files, 'm', { action: 'edit', content: 'mine\n' });
  assert.equal(by('g').decision?.action, 'accept');
  assert.equal(by('m').decision?.action, 'edit');
  decideLocal(files, 'g', { action: 'accept' });
  assert.equal(by('m').decision?.action, 'edit');
  decideLocal(files, 'g', { action: 'reject' });
  assert.equal(by('m').decision?.action, 'reject');
  decideLocal(files, 'g', null);
  assert.equal(by('g').decision, null);
  assert.equal(by('m').decision, null);
  assert.deepEqual(printsFor(files, 'g'), { g: 'fp-g', m: 'fp-m' });
});

test('filesProgress and unsentFiles', () => {
  const files = [
    mk('a', '/a', { decision: { action: 'accept', sent: true } }),
    mk('b', '/b', { decision: { action: 'reject' } }),
    mk('c', '/c'),
  ];
  assert.deepEqual(filesProgress(files), { decided: 2, total: 3 });
  assert.equal(unsentFiles(files), 1);
});
