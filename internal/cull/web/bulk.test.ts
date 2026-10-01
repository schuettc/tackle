import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { buckets, byConcern, bySize, bulkTargets } from './bulk.ts';

const items = JSON.parse(
  readFileSync(new URL('./testdata/items.json', import.meta.url), 'utf8'),
) as Item[];
const tests = items.filter((i) => i.kind === 'test');
const groups = items.filter((i) => i.kind === 'group') as GroupItem[];
const ids = (xs: Item[]) => xs.map((x) => x.name);
const none = new Map<string, unknown>();

test('test buckets by lean, ordered by cut probability desc', () => {
  const b = buckets(tests, none);
  assert.deepEqual(Object.keys(b).sort(), [
    'answered',
    'cut',
    'keep',
    'review',
  ]);
  assert.deepEqual(ids(b.cut), [
    'test_grid_keeps_unicode_names',
    'test_route_orders_max_size',
  ]);
  assert.equal(b.keep.length, 2);
  assert.deepEqual(ids(b.review), ['test_ledger_caps_zero_rate']);
  assert.deepEqual(b.answered, []);
});

test('keep bucket ordering is by act probability desc (0.35 before 0.30)', () => {
  const b = buckets(tests, none);
  assert.deepEqual(ids(b.keep), [
    'test_schedule_flags_zero_rate',
    'test_basket_caps_duplicate_ids',
  ]);
});

test('group buckets', () => {
  const b = buckets(groups, none);
  assert.deepEqual(Object.keys(b).sort(), [
    'answered',
    'consolidate',
    'keep_separate',
  ]);
  assert.equal(b.consolidate.length, 1);
  assert.equal(b.keep_separate.length, 3);
  assert.deepEqual(
    b.keep_separate.map((g) => g.id),
    [
      'group:a5686b52fa826957',
      'group:d0f051be6ca1f66b',
      'group:9b41e4175f6181c2',
    ],
  );
});

test('answered items live only in answered', () => {
  const answers = new Map<string, unknown>([[tests[0].id, { value: 'cut' }]]);
  const b = buckets(tests, answers);
  assert.deepEqual(ids(b.answered), [tests[0].name]);
  assert.deepEqual(ids(b.cut), ['test_route_orders_max_size']);
});

test('byConcern order and omission of empty rows', () => {
  const rows = byConcern(tests as TestItem[]);
  assert.deepEqual(
    rows.map((r) => r.concern),
    ['restates the code', 'pins a detail', 'no clear reason'],
  );
  const restates = rows[0];
  assert.deepEqual(ids(restates.items), [
    'test_route_orders_max_size',
    'test_schedule_flags_zero_rate',
  ]);
  assert.ok(Math.abs(restates.meanP - 0.44) < 1e-9);
  assert.equal(rows[2].items.length, 2);
});

test('bySize groups member counts', () => {
  const rows = bySize(groups);
  assert.deepEqual(
    rows.map((r) => r.size),
    ['2', '3'],
  );
  assert.equal(rows[0].items.length, 3);
  assert.equal(rows[1].items.length, 1);
  const big = structuredClone(groups[0]);
  big.id = 'big';
  big.state.tests = Array.from({ length: 6 }, (_, i) => ({
    name: `t${i}`,
    source: '',
  }));
  assert.equal(bySize([big]).at(-1)!.size, '5+');
});

test('bulkTargets skips items answered one at a time', () => {
  const answers = new Map<string, unknown>([[tests[1].id, { value: 'keep' }]]);
  const got = bulkTargets(tests, answers);
  assert.equal(got.length, tests.length - 1);
  assert.ok(!got.some((i) => i.id === tests[1].id));
});
