// filters-text.test.ts — unit tests for the saved filters' words.
//
// Run with: node --test filters-text.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { parseFilterIds, toggleFilter, filterLabel } from './filters-text.ts';
import type { FilterRow } from './wire.d.ts';

const rows: FilterRow[] = [
  { filter: { id: 'incoming', name: 'Incoming', match: [] } },
  { filter: { id: 'quiet-14d', name: 'Not updated in 14 days', match: [] } },
  {
    filter: { id: 'bad', name: 'Bad', match: [] },
    invalid: 'condition 1: unknown field "colour"',
  },
];

describe('parseFilterIds', () => {
  test('splits on commas, trims, drops empties and repeats', () => {
    assert.deepStrictEqual(parseFilterIds('incoming, quiet-14d,,incoming'), [
      'incoming',
      'quiet-14d',
    ]);
  });
  test('nothing is no filters', () => {
    assert.deepStrictEqual(parseFilterIds(null), []);
    assert.deepStrictEqual(parseFilterIds(''), []);
  });
});

describe('toggleFilter', () => {
  test('adds a filter, then removes it', () => {
    const one = toggleFilter(rows, [], 'incoming');
    assert.deepStrictEqual(one, ['incoming']);
    assert.deepStrictEqual(toggleFilter(rows, one, 'quiet-14d'), [
      'incoming',
      'quiet-14d',
    ]);
    assert.deepStrictEqual(toggleFilter(rows, one, 'incoming'), []);
  });
  test('an invalid or unknown filter leaves the selection', () => {
    assert.deepStrictEqual(toggleFilter(rows, ['incoming'], 'bad'), [
      'incoming',
    ]);
    assert.deepStrictEqual(toggleFilter(rows, ['incoming'], 'nope'), [
      'incoming',
    ]);
  });
});

describe('filterLabel', () => {
  test('the names, joined', () => {
    assert.strictEqual(
      filterLabel(rows, ['incoming', 'quiet-14d']),
      'Incoming + Not updated in 14 days',
    );
  });
  test('an unknown filter by its id', () => {
    assert.strictEqual(filterLabel(rows, ['nope']), 'nope');
  });
});
