// thread.test.ts — unit tests for the thread's card order.
//
// Run with: node --test thread.test.ts

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { threadOrder } from './thread.ts';
import type { MessageView } from './wire.d.ts';

function msg(id: number, batch?: number, pos?: number): MessageView {
  return {
    id,
    thread_id: 1,
    author: 'court',
    body: `m${id}`,
    attached: {},
    state: 'queued',
    created_at: '2026-09-29T09:00:00Z',
    ...(batch ? { batch_id: batch, batch_pos: pos } : {}),
  };
}

test('a reordered batch shows in its batch order, in its place', () => {
  const ms = [msg(1), msg(2, 7, 2), msg(3, 7, 0), msg(4, 7, 1), msg(5)];
  assert.deepEqual(
    threadOrder(ms).map((m) => m.id),
    [1, 3, 4, 2, 5],
  );
});

test('messages without a batch keep their order', () => {
  const ms = [msg(3), msg(1), msg(2)];
  assert.deepEqual(
    threadOrder(ms).map((m) => m.id),
    [3, 1, 2],
  );
});
