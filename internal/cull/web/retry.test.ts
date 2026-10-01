import { test } from 'node:test';
import assert from 'node:assert/strict';
import { retryDelay } from './retry.ts';

test('retryDelay backs off 1 s, 2 s, 5 s, then every 10 s', () => {
  assert.deepEqual(
    [0, 1, 2, 3, 4, 50].map(retryDelay),
    [1000, 2000, 5000, 10000, 10000, 10000],
  );
});
