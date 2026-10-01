import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  lean,
  actP,
  concern,
  strength,
  rvLevel,
  recommendation,
  answerValue,
} from './model.ts';

const items = JSON.parse(
  readFileSync(new URL('./testdata/items.json', import.meta.url), 'utf8'),
) as Item[];
export const byName = (n: string) => items.find((i) => i.name === n)!;
const groupBy = (id: string) => items.find((i) => i.id === id) as GroupItem;

const grid = byName('test_grid_keeps_unicode_names');
const route = byName('test_route_orders_max_size');
const sched = byName('test_schedule_flags_zero_rate');
const ledger = byName('test_ledger_caps_zero_rate');
const basket = byName('test_basket_caps_duplicate_ids');

test('lean is the argmax of the verdict probabilities', () => {
  assert.equal(lean(grid), 'cut');
  assert.equal(lean(sched), 'keep');
  assert.equal(lean(ledger), 'review');
  assert.equal(lean(groupBy('group:c6211ccb7bc8c770')), 'consolidate');
  assert.equal(lean(groupBy('group:d0f051be6ca1f66b')), 'keep_separate');
});

test('actP is the cut or consolidate probability', () => {
  assert.equal(actP(grid), 0.58);
  assert.equal(actP(sched), 0.35);
  assert.equal(actP(groupBy('group:d0f051be6ca1f66b')), 0.32);
});

test('concern picks the highest flag at or above 0.35', () => {
  assert.deepEqual(concern(grid), {
    flag: 'incidental_detail',
    value: 0.72,
    short: 'pins a detail',
    long: 'it pins a detail that could change harmlessly (wording, formatting, structure)',
  });
  assert.equal(concern(route)!.short, 'restates the code');
  assert.equal(
    concern(route)!.long,
    'it may only restate what the code does, or check values it set up itself',
  );
  assert.equal(concern(sched)!.short, 'restates the code');
  assert.equal(concern(ledger), null);
  assert.equal(concern(basket), null);
});

test('concern wording for the other flags', () => {
  const mk = (flag: string): TestItem => {
    const t = structuredClone(grid) as TestItem;
    (t.jev as unknown as Record<string, Noul>).incidental_detail.noul = 0;
    (t.jev as unknown as Record<string, Noul>)[flag].noul = 0.9;
    return t;
  };
  assert.equal(concern(mk('framework_behavior'))!.short, 'tests a library');
  assert.equal(
    concern(mk('framework_behavior'))!.long,
    "it mostly checks a library or framework, not this project's code",
  );
  assert.equal(concern(mk('mock_only'))!.short, 'mocks only');
  assert.equal(
    concern(mk('mock_only'))!.long,
    'it only checks that mocks were called',
  );
  assert.equal(concern(mk('not_missed'))!.short, 'might not be missed');
  assert.equal(
    concern(mk('not_missed'))!.long,
    'a maintainer might not miss it if it were deleted',
  );
});

test('strength', () => {
  assert.equal(strength(0.5), 'leaning');
  assert.equal(strength(0.58), 'leaning');
  assert.equal(strength(0.49), 'slightly');
});

test('rvLevel', () => {
  assert.equal(rvLevel(0), 'nothing');
  assert.equal(rvLevel(0.4), 'nothing');
  assert.equal(rvLevel(1), 'cosmetic');
  assert.equal(rvLevel(2.25), 'real but minor');
  assert.equal(rvLevel(2.77), 'important');
});

test('recommendation sentences (tests)', () => {
  assert.equal(
    recommendation(grid),
    "Jev leans cut (0.58). Jev's main concern: it pins a detail that could change harmlessly (wording, formatting, structure) (0.72). Protects behavior rated real but minor.",
  );
  assert.equal(
    recommendation(sched),
    "Jev leans keep (cut 0.35). Jev's main concern: it may only restate what the code does, or check values it set up itself (0.47). Protects behavior rated important.",
  );
  assert.equal(
    recommendation(ledger),
    "Jev can't decide (cut 0.45). No single concern stood out; Jev is split on whether it earns its place. Protects behavior rated real but minor.",
  );
});

test('recommendation sentences (groups)', () => {
  assert.equal(
    recommendation(groupBy('group:c6211ccb7bc8c770')),
    'Jev leans merge (merge 0.59). Same behavior 0.61; loss if merged 0.56.',
  );
  assert.equal(
    recommendation(groupBy('group:d0f051be6ca1f66b')),
    'Jev leans separate (merge 0.32). Same behavior 0.73; loss if merged 0.40; one of these looks like an exact duplicate.',
  );
});

test('answerValue maps to the API vocabulary', () => {
  assert.equal(answerValue(grid, 'keep'), 'keep');
  assert.equal(answerValue(grid, 'cut'), 'cut');
  assert.equal(
    answerValue(groupBy('group:c6211ccb7bc8c770'), 'consolidate'),
    'merge',
  );
  assert.equal(
    answerValue(groupBy('group:c6211ccb7bc8c770'), 'keep_separate'),
    'separate',
  );
});
