// thread.ts — the order of a thread's cards. Pure, so node --test can load it.
//
// serve lists a thread's messages by id (the order they were written), but it
// delivers Court's messages by (queued_at, batch_id, batch_pos, id)
// (deliver.go, Delivery.Messages). A draft batch is written early and queued
// when `send N` is pressed, and Court can reorder it first. So the thread
// shows each of Court's messages at the time it was queued, in the order it
// is delivered, and every other message (the agent's replies, a turn's
// `worked for …`) at the time it was written.

import type { MessageView } from './wire.d.ts';

// at is when a message takes its place in the thread, in ms.
function at(m: MessageView): number {
  const ts =
    m.author === 'court' ? (m.queued_at ?? m.created_at) : m.created_at;
  return Date.parse(ts);
}

export function threadOrder(ms: MessageView[]): MessageView[] {
  return [...ms].sort(
    (a, b) =>
      at(a) - at(b) ||
      (a.batch_id ?? 0) - (b.batch_id ?? 0) ||
      (a.batch_pos ?? 0) - (b.batch_pos ?? 0) ||
      a.id - b.id,
  );
}
