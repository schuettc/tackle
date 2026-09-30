// thread.test.ts — unit tests for the thread's card order.
//
// Run with: node --test thread.test.ts

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { threadOrder } from './thread.ts';
import type { MessageView } from './wire.d.ts';

// t is a timestamp s seconds after 09:00.
const t = (s: number) => new Date(Date.UTC(2026, 8, 29, 9, 0, s)).toISOString();

function msg(
  id: number,
  created: number,
  o: Partial<MessageView> & { queued?: number } = {},
): MessageView {
  const { queued, ...rest } = o;
  return {
    id,
    thread_id: 1,
    author: 'court',
    body: `m${id}`,
    attached: {},
    state: 'queued',
    created_at: t(created),
    ...(queued !== undefined ? { queued_at: t(queued) } : {}),
    ...rest,
  };
}

const ids = (ms: MessageView[]) => ms.map((m) => m.id);

test('a reordered batch shows in its batch order', () => {
  const ms = [
    msg(1, 0, { queued: 0 }),
    msg(2, 1, { batch_id: 7, batch_pos: 2, queued: 5 }),
    msg(3, 2, { batch_id: 7, batch_pos: 0, queued: 5 }),
    msg(4, 3, { batch_id: 7, batch_pos: 1, queued: 5 }),
  ];
  assert.deepEqual(ids(threadOrder(ms)), [1, 3, 4, 2]);
});

test('a batch sent after a later ↵ message shows after it', () => {
  // alpha is drafted first (id 1), "now" is sent (id 2), then the batch goes.
  const ms = [
    msg(1, 0, { batch_id: 9, batch_pos: 0, queued: 10 }),
    msg(2, 5, { queued: 5 }),
  ];
  assert.deepEqual(ids(threadOrder(ms)), [2, 1]);
});

test("the agent's reply shows at the time it was written", () => {
  const ms = [
    msg(1, 0, { queued: 0 }),
    msg(2, 4, { author: 'probe-session', state: 'answered' }),
    msg(3, 1, { batch_id: 3, batch_pos: 0, queued: 8 }),
  ];
  assert.deepEqual(ids(threadOrder(ms)), [1, 2, 3]);
});

test('messages at the same time keep id order', () => {
  const ms = [msg(3, 0, { queued: 0 }), msg(2, 0, { queued: 0 })];
  assert.deepEqual(ids(threadOrder(ms)), [2, 3]);
});
