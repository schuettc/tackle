// thread.ts — the order of a thread's cards. Pure, so node --test can load it.
//
// serve lists a thread's messages by id (the order they were written). A batch
// is delivered in its batch order (Court can reorder drafts before sending),
// so the cards of one batch are shown in that order, in the batch's place.

import type { MessageView } from './wire.d.ts';

export function threadOrder(ms: MessageView[]): MessageView[] {
  const out: MessageView[] = [];
  const placed = new Set<number>();
  for (const m of ms) {
    if (!m.batch_id) {
      out.push(m);
      continue;
    }
    if (placed.has(m.batch_id)) continue;
    placed.add(m.batch_id);
    out.push(
      ...ms
        .filter((x) => x.batch_id === m.batch_id)
        .sort((a, b) => (a.batch_pos ?? 0) - (b.batch_pos ?? 0)),
    );
  }
  return out;
}
