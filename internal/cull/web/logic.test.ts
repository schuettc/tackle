import { test } from 'node:test';
import assert from 'node:assert/strict';
import { concern, recommendation, strength } from './model.ts';
import { buckets, byConcern } from './bulk.ts';

// Synthetic items built here; no project data.
type Flags = Partial<
  Record<
    | 'tautological'
    | 'mock_only'
    | 'framework_behavior'
    | 'incidental_detail'
    | 'not_missed',
    number
  >
>;

function mkTest(
  id: string,
  p: { cut: number; keep: number; review: number },
  flags: Flags = {},
  rv = 2,
): TestItem {
  const n = (k: keyof Flags): Noul => ({
    type: 'noul',
    noul: flags[k] ?? 0,
  });
  return {
    id,
    kind: 'test',
    hash: id,
    file: 'a_test.py',
    name: id,
    verdict: '',
    rule: '',
    model: '',
    state: {} as TestState,
    jev: {
      verdict: { type: 'choice', choice: '', probabilities: p, confidence: 1 },
      regression_value: { type: 'score', score: rv },
      tautological: n('tautological'),
      mock_only: n('mock_only'),
      framework_behavior: n('framework_behavior'),
      incidental_detail: n('incidental_detail'),
      not_missed: n('not_missed'),
    },
  };
}

function mkGroup(
  id: string,
  p: { consolidate: number; keep_separate: number; review: number },
  exact = 0,
): GroupItem {
  const n = (v: number): Noul => ({ type: 'noul', noul: v });
  return {
    id,
    kind: 'group',
    hash: id,
    file: 'a_test.py',
    name: id,
    verdict: '',
    rule: '',
    model: '',
    state: { tests: [{}, {}] } as GroupState,
    jev: {
      verdict: { type: 'choice', choice: '', probabilities: p, confidence: 1 },
      same_behavior: n(0.8),
      exact_duplicate: n(exact),
      loss_if_merged: n(0.1),
    },
  };
}

const cutP = { cut: 0.6, keep: 0.3, review: 0.1 };

test('byConcern lists all six rows in the fixed order', () => {
  const items = [
    mkTest('none', cutP, {}),
    mkTest('mock', cutP, { mock_only: 0.9 }),
    mkTest('lib', cutP, { framework_behavior: 0.9 }),
    mkTest('missed', cutP, { not_missed: 0.9 }),
    mkTest('detail', cutP, { incidental_detail: 0.9 }),
    mkTest('taut', cutP, { tautological: 0.9 }),
  ];
  assert.deepEqual(
    byConcern(items).map((r) => r.concern),
    [
      'restates the code',
      'pins a detail',
      'might not be missed',
      'tests a library',
      'mocks only',
      'no clear reason',
    ],
  );
});

test('concern threshold: 0.35 counts, 0.3499 does not', () => {
  assert.equal(
    concern(mkTest('a', cutP, { mock_only: 0.35 }))?.short,
    'mocks only',
  );
  assert.equal(concern(mkTest('b', cutP, { mock_only: 0.3499 })), null);
});

test('exact duplicate clause: 0.7 adds it, 0.6999 does not', () => {
  const g = { consolidate: 0.6, keep_separate: 0.3, review: 0.1 };
  const clause = 'one of these looks like an exact duplicate';
  assert.ok(recommendation(mkGroup('a', g, 0.7)).includes(clause));
  assert.ok(!recommendation(mkGroup('b', g, 0.6999)).includes(clause));
});

test('strength boundary: 0.5 leaning, 0.4999 slightly', () => {
  assert.equal(strength(0.5), 'leaning');
  assert.equal(strength(0.4999), 'slightly');
});

test('equal act probability orders by id', () => {
  const items = [mkTest('c', cutP), mkTest('a', cutP), mkTest('b', cutP)];
  assert.deepEqual(
    buckets(items, new Map()).cut.map((i) => i.id),
    ['a', 'b', 'c'],
  );
});

test('a review-lean group lands in review and says it cannot decide', () => {
  const g = mkGroup('g', { consolidate: 0.3, keep_separate: 0.2, review: 0.5 });
  const b = buckets([g], new Map());
  assert.deepEqual(
    b.review.map((i) => i.id),
    ['g'],
  );
  assert.ok(recommendation(g).startsWith("Jev can't decide (merge 0.30). "));
});

test("the approved mockup's sentence, exactly", () => {
  const t = mkTest(
    'm',
    { cut: 0.59, keep: 0.05, review: 0.36 },
    { incidental_detail: 0.52, not_missed: 0.51 },
    2,
  );
  assert.equal(
    recommendation(t),
    "Jev leans cut (0.59). Jev's main concern: it pins a detail that could change harmlessly (wording, formatting, structure) (0.52). Protects behavior rated real but minor.",
  );
});
