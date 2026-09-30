// composer.ts — the composer at the bottom of the agent dock (spec §3.5 item 7).
//
// The composer is the dock's one raised surface (--kit-lift). Top to bottom:
//   1. `attached: <value>` — what goes with the message: the selection, the
//      open item, the rule or the job. Sections feed it through
//      ctx.setAttached; the value is in the signal colour and editable.
//   2. The message field, `Message <agent>…`.
//   3. The key hint `↵ send · ⌘↵ add to batch`.
//
// ↵ posts the message with batch:false (it goes into the delivery queue);
// ⌘↵ posts it with batch:true (it goes into the thread's draft batch).
//
// Editing the attached line: focusing the value shows it in the words the
// delivery uses ("pr:o/r#1 pr:o/r#2", "open pr:o/r#4", "rule x", "job 3");
// ↵ or leaving the field commits, Esc reverts. The edit holds until the
// message is sent, then the line follows the page again. Words that aren't
// keys, rules or jobs are dropped, and the line re-renders from what was
// parsed, so the value shown is exactly what is sent.

import { h } from '/_kit/kit.js';
import type { Attached, Thread } from './wire.d.ts';
import type { Ctx } from './app.ts';
import {
  attachedLabel,
  attachedText,
  parseAttached,
  isEmpty,
  sameAttached,
} from './attached.ts';

/** What the composer needs from the dock. */
export interface ComposerDock {
  currentThread(): number;
  currentSession(): string;
  /** Called after a message is posted into a thread the composer created. */
  threadCreated(t: Thread): void;
}

export interface ComposerHandle {
  el: HTMLElement;
  /** The page's current context (sections call ctx.setAttached). */
  setAttached(a: Attached): void;
  /** The agent's name for the placeholder: the session's harness. */
  setAgent(name: string): void;
  /** Focus the message field (the `.` key). */
  focus(): void;
  /** Add the field's text to the batch (the `⌘↵` key). */
  addToBatch(): void;
  /** The message field, so the `⌘↵` binding can scope itself to it. */
  input: HTMLTextAreaElement;
}

// threadName names a new thread from its first request (spec §3.5 item 2).
function threadName(body: string): string {
  const words = body.split(/\s+/).filter(Boolean).slice(0, 5).join(' ');
  return words.length > 40 ? words.slice(0, 39) + '\u2026' : words;
}

export function makeComposer(ctx: Ctx, dock: ComposerDock): ComposerHandle {
  // context is what the page shows; override is Court's edit, held until send.
  let context: Attached = {};
  let override: Attached | null = null;
  let sending = false;

  const effective = (): Attached => override ?? context;

  // ---- attached line -------------------------------------------------------

  const value = h('span', {
    class: 'cb-comp-attached-value',
    contenteditable: 'true',
    role: 'textbox',
    'aria-label': 'attached',
    spellcheck: false,
    'data-testid': 'composer-attached',
  });
  const line = h(
    'div',
    { class: 'cb-comp-attached' },
    h('span', { class: 'cb-comp-attached-prefix' }, 'attached: '),
    value,
  );

  let editing = false;

  function renderAttached(): void {
    if (editing) return;
    const a = effective();
    value.textContent = isEmpty(a) ? 'nothing' : attachedLabel(a);
    value.toggleAttribute('data-empty', isEmpty(a));
    value.toggleAttribute('data-edited', override !== null);
  }

  value.addEventListener('focus', () => {
    if (editing) return;
    editing = true;
    value.textContent = attachedText(effective());
    value.removeAttribute('data-empty');
    // Select the whole value so typing replaces it.
    const range = document.createRange();
    range.selectNodeContents(value);
    const sel = window.getSelection();
    sel?.removeAllRanges();
    sel?.addRange(range);
  });

  // commit reads the edited value; an edit equal to the page's context
  // drops the override, so the line follows the page again.
  function commit(): void {
    if (!editing) return;
    editing = false;
    const parsed = parseAttached(value.textContent ?? '');
    override = sameAttached(parsed, context) ? null : parsed;
    renderAttached();
  }

  // revert leaves without committing what was typed.
  function revert(): void {
    editing = false;
    renderAttached();
  }

  value.addEventListener('blur', commit);
  value.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      commit();
      value.blur();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      revert();
      value.blur();
    }
  });

  // ---- message field -------------------------------------------------------

  const input = h('textarea', {
    class: 'cb-comp-input',
    rows: 1,
    placeholder: 'Message the agent\u2026',
    'aria-label': 'message',
    'data-testid': 'composer-input',
  }) as HTMLTextAreaElement;

  function fit(): void {
    input.style.height = 'auto';
    input.style.height = `${Math.min(input.scrollHeight, 160)}px`;
  }
  input.addEventListener('input', fit);

  // ↵ sends; ⇧↵ is a new line. ⌘↵ is a registered key (see dock.ts), so it
  // shows in the ? overlay and fires in the field through the kit.
  input.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key !== 'Enter' || e.isComposing) return;
    if (e.shiftKey || e.metaKey || e.ctrlKey || e.altKey) return;
    e.preventDefault();
    void send(false);
  });

  // ---- footer --------------------------------------------------------------

  const note = h('span', { class: 'cb-comp-note', role: 'status' });
  const footer = h(
    'div',
    { class: 'cb-comp-foot' },
    h('span', {}, '\u21b5 send \u00b7 \u2318\u21b5 add to batch'),
    note,
  );

  const el = h(
    'div',
    { class: 'cb-comp', 'data-testid': 'composer' },
    line,
    input,
    footer,
  );

  // ---- send ----------------------------------------------------------------

  async function threadFor(body: string): Promise<number> {
    const current = dock.currentThread();
    if (current) return current;
    const session = dock.currentSession();
    if (!session) return 0;
    const t = await ctx.api.post<Thread>('/threads', {
      session,
      name: threadName(body),
    });
    dock.threadCreated(t);
    return t.id;
  }

  // withoutSent removes the sent text from what the field holds now. Court
  // may have typed before it, after it, or both; the two sides meet with one
  // run of whitespace between them (or none, at the start or end). If he
  // edited inside the sent text it is no longer there as sent; what he made
  // of it is his, and it stays.
  function withoutSent(now: string, sent: string): string {
    const at = now.indexOf(sent);
    if (at < 0) return now;
    const before = now.slice(0, at);
    const after = now.slice(at + sent.length);
    if (!before.trim()) return after.replace(/^\s+/, '');
    if (/\s$/.test(before)) return before + after.replace(/^\s+/, '');
    return before + after;
  }

  async function send(batch: boolean): Promise<void> {
    const sent = input.value;
    const body = sent.trim();
    if (!body || sending) return;
    sending = true;
    note.textContent = '';
    try {
      const thread = await threadFor(body);
      if (!thread) {
        note.textContent = 'no agent session';
        return;
      }
      await ctx.api.post('/messages', {
        thread,
        body,
        attached: effective(),
        batch,
      });
      // Clear only what was sent, wherever it now sits: text typed while the
      // send was in flight (before it or after it) stays in the field, and
      // the sent text never does, so it cannot be sent twice.
      input.value = withoutSent(input.value, sent);
      fit();
      override = null;
      renderAttached();
    } catch (err) {
      note.textContent = 'not sent';
      console.error('[composer] send:', err);
    } finally {
      sending = false;
    }
  }

  renderAttached();

  return {
    el,
    input,
    setAttached(a: Attached): void {
      context = a;
      renderAttached();
    },
    setAgent(name: string): void {
      input.placeholder = `Message ${name || 'the agent'}\u2026`;
    },
    focus(): void {
      input.focus();
    },
    addToBatch(): void {
      void send(true);
    },
  };
}
