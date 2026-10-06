// progress.ts — the dock between the message cards and the composer
// (spec §3.5 items 4–6), plus the folded `worked for` record.
//
//   makeBatchTray   — the dashed `batch · N drafts` card with `send N`; each
//                     draft can be edited, moved up or down, or removed.
//                     It sits at the end of the message cards (it scrolls
//                     with them, as in the mock).
//   makeProgressLine — the agent's live line: amber dot, `<text> · n of total`,
//                     `Ns ago`, and a thin bar when the agent reports n of
//                     total. After 4 minutes without an update it reads
//                     `no progress for 4m`. Cleared when the turn settles.
//   makeWaitingStrip — `waiting · N queued for the end of this turn`.
//   workedFold      — a settled turn's `worked for Xm Ys`, folded into the
//                     thread; it opens to the progress line's history.

import { h, card, fold, noteField } from '/_kit/kit.js';
import type { Ctx } from './app.ts';
import type { Message, MessagesView, MessageView, Progress } from './wire.d.ts';
import { pluralize } from './decide-math.ts';
import { movedNote } from './composer.ts';

// ---- batch tray ------------------------------------------------------------

export interface BatchTrayHandle {
  el: HTMLElement;
  /** Load the draft batch of a thread (0 clears the tray). */
  load(thread: number): Promise<void>;
  /** Reload the current thread's batch (after a drafts or batch event). */
  reload(): Promise<void>;
}

/** What the tray asks of the dock before it sends a batch. */
export interface BatchTrayDock {
  /** Why nothing can be sent now ('' when it can): see ComposerDock.blocked. */
  blocked(): string;
  /** Say it where Court looks (the composer's note). */
  say(text: string): void;
  /** The session the page is attached to: serve refuses another's thread. */
  currentSession(): string;
  /** serve refused the send: the thread moved (see ComposerDock.threadMoved). */
  threadMoved(): void;
}

export function makeBatchTray(ctx: Ctx, dock: BatchTrayDock): BatchTrayHandle {
  let thread = 0;
  let batch = 0;
  let drafts: Message[] = [];
  // Only the latest load may render: loads race when events arrive together.
  let loadSeq = 0;

  const label = h('span', { class: 'kit-card-head cb-batch-label' });
  const sendBtn = h('button', {
    class: 'kit-btn fill cb-batch-send',
    'data-testid': 'batch-send',
    onclick() {
      void sendBatch();
    },
  });
  const list = h('div', { class: 'cb-batch-drafts' });
  const el = card({
    body: h(
      'div',
      {},
      h('div', { class: 'cb-batch-head' }, label, sendBtn),
      list,
    ),
  });
  el.classList.add('cb-batch');
  el.setAttribute('data-testid', 'batch-tray');
  el.hidden = true;

  // The field of the draft Court is editing. A redraw (a live reload: a
  // drafts or message event) waits until his edit ends: it would take the
  // field from under his typing, and its leaving commits what he has typed
  // so far. The edit's end draws the tray as it is by then.
  let editing: HTMLElement | null = null;

  function render(): void {
    if (editing?.isConnected) return;
    editing = null;
    el.hidden = drafts.length === 0;
    if (!drafts.length) {
      list.replaceChildren();
      return;
    }
    label.textContent = `batch \u00b7 ${pluralize(drafts.length, 'draft')}`;
    sendBtn.textContent = `send ${drafts.length}`;
    list.replaceChildren(...drafts.map((d, i) => draftRow(d, i)));
  }

  function actionBtn(
    text: string,
    action: string,
    aria: string,
    run: () => void,
    disabled = false,
  ): HTMLElement {
    return h(
      'button',
      {
        class: 'cb-batch-act',
        'data-action': action,
        'aria-label': aria,
        disabled,
        onclick: run,
      },
      text,
    );
  }

  function draftRow(d: Message, i: number): HTMLElement {
    const text = h('span', { class: 'cb-batch-text' }, d.body);
    const row = h(
      'div',
      { class: 'cb-batch-draft', 'data-draft-id': String(d.id) },
      text,
      h(
        'span',
        { class: 'cb-batch-acts' },
        actionBtn('edit', 'edit', `edit draft ${i + 1}`, () =>
          editDraft(d, text),
        ),
        actionBtn(
          '\u2191',
          'up',
          `move draft ${i + 1} up`,
          () => void move(i, -1),
          i === 0,
        ),
        actionBtn(
          '\u2193',
          'down',
          `move draft ${i + 1} down`,
          () => void move(i, 1),
          i === drafts.length - 1,
        ),
        actionBtn(
          '\u00d7',
          'remove',
          `remove draft ${i + 1}`,
          () => void remove(d.id),
        ),
      ),
    );
    return row;
  }

  // editDraft swaps the text for the kit's noteField: ↵ or leaving commits a
  // changed value, Esc reverts and leaves. The row comes back either way.
  function editDraft(d: Message, text: HTMLElement): void {
    const done = () => {
      if (editing === field) editing = null;
      render();
    };
    const field = noteField({
      value: d.body,
      onCommit(v: string) {
        const body = v.trim();
        if (body && body !== d.body) {
          d.body = body;
          // A reload while he edited brought the draft anew: it shows his.
          const now = drafts.find((x) => x.id === d.id);
          if (now) now.body = body;
          void ctx.api
            .post('/drafts/edit', { id: d.id, body })
            .catch((err: unknown) => {
              console.error('[batch] edit:', err);
              void reload();
            });
        }
        done();
      },
    });
    field.classList.add('cb-batch-edit');
    field.setAttribute('aria-label', 'edit draft');
    // Leaving without a change (Esc, or a blur with nothing changed) doesn't
    // commit, so the row is restored here.
    field.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.key === 'Escape') done();
    });
    field.addEventListener('blur', () => {
      if (field.isConnected) done();
    });
    editing = field;
    text.replaceWith(field);
    field.focus();
    field.select();
  }

  async function move(i: number, delta: number): Promise<void> {
    const j = i + delta;
    if (j < 0 || j >= drafts.length) return;
    const next = [...drafts];
    [next[i], next[j]] = [next[j], next[i]];
    drafts = next;
    render();
    try {
      await ctx.api.post('/batches/reorder', {
        batch,
        ids: drafts.map((d) => d.id),
      });
    } catch (err) {
      console.error('[batch] reorder:', err);
      await reload();
    }
  }

  async function remove(id: number): Promise<void> {
    drafts = drafts.filter((d) => d.id !== id);
    render();
    try {
      await ctx.api.post('/drafts/remove', { id });
    } catch (err) {
      console.error('[batch] remove:', err);
      await reload();
    }
  }

  async function sendBatch(): Promise<void> {
    if (!batch || !drafts.length) return;
    // A batch goes to the attached session while it is here, and nowhere
    // else: the drafts stay until Court chooses.
    const why = dock.blocked();
    if (why) {
      dock.say(why);
      return;
    }
    const sent = batch;
    drafts = [];
    render();
    try {
      await ctx.api.post('/batches/send', {
        batch: sent,
        session: dock.currentSession(),
      });
    } catch (err) {
      console.error('[batch] send:', err);
      const moved = movedNote(err);
      if (moved) {
        dock.say(moved);
        dock.threadMoved();
      }
      await reload();
    }
  }

  async function load(t: number): Promise<void> {
    thread = t;
    const seq = ++loadSeq;
    if (!t) {
      batch = 0;
      drafts = [];
      render();
      return;
    }
    try {
      const mv = await ctx.api.get<MessagesView>('/messages', {
        thread: String(t),
      });
      if (seq !== loadSeq) return;
      batch = mv.batch;
      drafts = mv.drafts ?? [];
      render();
    } catch (err) {
      console.error('[batch] load:', err);
    }
  }

  function reload(): Promise<void> {
    return load(thread);
  }

  return { el, load, reload };
}

// ---- progress line ---------------------------------------------------------

/** No update for this long and the line reads `no progress for Nm`. */
export const NO_PROGRESS_MS = 4 * 60 * 1000;

/** fmtAgo is the progress line's right-hand age: "8s ago", "3m ago". */
export function fmtAgo(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}

/** countText is ` · n of total` when the agent reports both, else ''. */
function countText(n?: number, total?: number): string {
  return n && total ? ` \u00b7 ${n} of ${total}` : '';
}

export interface ProgressLineHandle {
  el: HTMLElement;
  /**
   * Show a progress line. `at` is when it was last updated in page time:
   * the arrival time for a live event, updated_at for one loaded from the API.
   */
  set(p: Progress, at: number): void;
  clear(): void;
  /** Re-render the age (the dock calls this every second). */
  tick(): void;
}

export function makeProgressLine(): ProgressLineHandle {
  let prog: Progress | null = null;
  let at = 0;

  const text = h('span', { class: 'cb-prog-text' });
  const age = h('span', { class: 'cb-prog-age' });
  const fill = h('span', { class: 'cb-prog-fill' });
  const bar = h('div', { class: 'cb-prog-bar', role: 'progressbar' }, fill);
  const el = h(
    'div',
    { class: 'cb-prog', 'data-testid': 'progress-line' },
    h(
      'div',
      { class: 'cb-prog-row' },
      h('span', { class: 'cb-prog-dot' }),
      text,
      age,
    ),
    bar,
  );
  el.hidden = true;

  function render(): void {
    el.hidden = !prog;
    if (!prog) return;
    const since = Date.now() - at;
    text.textContent = prog.text + countText(prog.n, prog.total);
    const quiet = since >= NO_PROGRESS_MS;
    el.toggleAttribute('data-quiet', quiet);
    age.textContent = quiet
      ? `no progress for ${Math.floor(since / 60000)}m`
      : fmtAgo(since);
    const n = prog.n ?? 0;
    const total = prog.total ?? 0;
    bar.hidden = !(n && total);
    if (n && total) {
      fill.style.width = `${Math.min(100, (n / total) * 100)}%`;
      bar.setAttribute('aria-valuenow', String(n));
      bar.setAttribute('aria-valuemax', String(total));
    }
  }

  return {
    el,
    set(p: Progress, when: number): void {
      prog = p;
      at = when;
      render();
    },
    clear(): void {
      prog = null;
      render();
    },
    tick: render,
  };
}

// ---- waiting strip ---------------------------------------------------------

export interface WaitingStripHandle {
  el: HTMLElement;
  setQueued(n: number): void;
}

export function makeWaitingStrip(): WaitingStripHandle {
  const count = h('span', {});
  const el = h(
    'div',
    { class: 'cb-wait', 'data-testid': 'waiting-strip' },
    h('span', { class: 'cb-wait-word' }, 'waiting'),
    count,
  );
  el.hidden = true;
  return {
    el,
    setQueued(n: number): void {
      el.hidden = n <= 0;
      // "queued" is an adjective: 1 queued, 3 queued.
      count.textContent = ` \u00b7 ${n} queued for the end of this turn`;
    },
  };
}

// ---- worked fold -----------------------------------------------------------

function clock(ts: string): string {
  return new Date(ts).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  });
}

/** workedFold renders a settled turn's `worked for …` record. */
export function workedFold(msg: MessageView): HTMLElement {
  const lines = msg.worked?.lines ?? [];
  const history = h(
    'ol',
    { class: 'cb-worked-lines' },
    ...lines.map((l) =>
      h(
        'li',
        { class: 'cb-worked-line' },
        h('span', { class: 'cb-worked-at' }, clock(l.at)),
        h('span', {}, l.text + countText(l.n, l.total)),
      ),
    ),
  );
  const el = fold(msg.body, history);
  el.classList.add('cb-worked');
  el.setAttribute('data-testid', 'worked');
  return el;
}
