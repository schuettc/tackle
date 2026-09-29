// dock.ts — the agent dock (Task 6): session, threads, message cards, delivery state.
//
// The .kit-rail is filled with (in order from the spec §3.5):
//   1. Session header  — agent-coloured dot · label · AGENT ▾ picker
//   2. Thread chips    — one per thread + "+"  (active chip filled)
//   3. Message cards   — rust edge for you, amber for the agent;
//                        mono header YOU/PI with age on right;
//                        Court's cards carry the delivery-state footer;
//                        agent cards link items/rules/jobs (underlined, open the route)
//   4. Stuck delivery  — release + move to another session
//   5. Left session    — pi · <cwd> left · N queued · move to…
//
// Task 7 fills the batch tray, progress line, waiting strip and composer below.

import { h, card } from '/_kit/kit.js';
import type {
  Session,
  Thread,
  MessageView,
  SessionsView,
  ThreadsView,
  MessagesView,
  DeliveryView,
  Delivery,
} from './wire.d.ts';
import type { Ctx, DockHandle } from './app.ts';
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { fmtAge } from './time-utils.ts';
import { go } from './router.ts';

// ---- delivery state display -------------------------------------------------

// State colours map for Court's message footer.
// queued → wait, working → agent, answered → ok, failed → danger; others → muted
function stateColor(state: string): string {
  switch (state) {
    case 'queued':
      return 'var(--kit-wait)';
    case 'working':
      return 'var(--kit-agent)';
    case 'answered':
      return 'var(--kit-ok)';
    case 'failed':
      return 'var(--kit-danger)';
    default:
      return 'var(--kit-muted)';
  }
}

function stateLabel(msg: MessageView): string {
  const base = msg.state;
  // For answered state, show the count of attached items if present.
  if (base === 'answered') {
    const n = msg.attached?.keys?.length ?? 0;
    if (n > 0) return `${base} · ${pluralize(n, 'item')}`;
  }
  return base;
}

// ---- session label -----------------------------------------------------------

function sessionLabel(s: Session): string {
  if (s.label) return s.label;
  // Fallback: harness · cwd basename
  const harness = s.harness || 'agent';
  const cwd = s.cwd ? (s.cwd.split('/').filter(Boolean).pop() ?? s.cwd) : '';
  return cwd ? `${harness} · ${cwd}` : harness;
}

// ---- agent card link rendering ----------------------------------------------
// Parse simple "item:key", "rule:id", "job:id" tokens in the body and render
// them as underlined links. Real bodies from agents are plain text; the dock
// renders the body verbatim and additionally appends attachment links from
// msg.attached below the body.

function renderAgentBody(msg: MessageView): Node {
  const wrap = h('div', { class: 'cb-dock-body' });

  // Body text (plain).
  const bodyP = h('p', { class: 'cb-dock-bodytext' });
  bodyP.textContent = msg.body;
  wrap.append(bodyP);

  // Attachment links from msg.attached — underlined, signal colour.
  const { attached } = msg;
  if (attached) {
    const links: HTMLElement[] = [];
    if (attached.keys && attached.keys.length > 0) {
      // Show up to 5 item links to avoid clutter.
      const shown = attached.keys.slice(0, 5);
      for (const k of shown) {
        const a = h('a', {
          class: 'cb-dock-link',
          href: '#',
          onclick(e: Event) {
            e.preventDefault();
            go('item', encodeURIComponent(k));
          },
        });
        a.textContent = keyWithoutKind(k);
        links.push(a);
      }
      if (attached.keys.length > 5) {
        links.push(
          h(
            'span',
            { class: 'cb-dock-link-more' },
            `+${attached.keys.length - 5} more`,
          ),
        );
      }
    }
    if (attached.open) {
      const k = attached.open;
      const a = h('a', {
        class: 'cb-dock-link',
        href: '#',
        onclick(e: Event) {
          e.preventDefault();
          go('item', encodeURIComponent(k));
        },
      });
      a.textContent = keyWithoutKind(k);
      links.push(a);
    }
    if (attached.rule) {
      const id = attached.rule;
      const a = h('a', {
        class: 'cb-dock-link',
        href: '#',
        onclick(e: Event) {
          e.preventDefault();
          go('rules', encodeURIComponent(id));
        },
      });
      a.textContent = id;
      links.push(a);
    }
    if (attached.job) {
      const id = attached.job;
      const a = h('a', {
        class: 'cb-dock-link',
        href: '#',
        onclick(e: Event) {
          e.preventDefault();
          go('apply', encodeURIComponent(id));
        },
      });
      a.textContent = `job ${id}`;
      links.push(a);
    }
    if (links.length > 0) {
      const linkRow = h('div', { class: 'cb-dock-links' }, ...links);
      wrap.append(linkRow);
    }
  }

  return wrap;
}

// ---- message card rendering -------------------------------------------------

// Render a single message card (Court or agent).
function renderMsgCard(
  msg: MessageView,
  delivery: Delivery | null,
  sessions: Session[],
  ctx: Ctx,
): HTMLElement {
  const isAgent = msg.author !== 'court';

  // Build the card header: NAME · age on the right.
  const authorLabel = isAgent ? 'PI' : 'YOU';
  const age = fmtAge(msg.created_at);
  const headEl = h(
    'div',
    { class: 'cb-dock-ch' },
    h(
      'span',
      {
        class: isAgent ? 'cb-dock-ch-name cb-dock-ch-agent' : 'cb-dock-ch-name',
      },
      authorLabel,
    ),
    h('span', { class: 'cb-dock-ch-age' }, age),
  );

  // Card body.
  let bodyContent: Node;
  if (isAgent) {
    bodyContent = renderAgentBody(msg);
  } else {
    const bodyEl = h('div', { class: 'cb-dock-body' });
    const bodyP = h('p', { class: 'cb-dock-bodytext' });
    bodyP.textContent = msg.body;
    bodyEl.append(bodyP);
    bodyContent = bodyEl;
  }

  // Delivery-state footer for Court's cards.
  let stateEl: HTMLElement | null = null;
  if (!isAgent && msg.state) {
    // Show state for all non-draft states.
    const validStates = [
      'queued',
      'delivered',
      'received',
      'working',
      'answered',
      'declined',
      'failed',
      'unanswered',
      'interrupted',
    ];
    if (validStates.includes(msg.state)) {
      stateEl = h('div', {
        class: 'cb-dock-state',
        'data-state': msg.state,
        style: `color:${stateColor(msg.state)}`,
      });
      stateEl.textContent = stateLabel(msg);
    }
  }

  // Build body node: header + content + optional state footer.
  const fullBody = h(
    'div',
    { class: 'cb-dock-card-body' },
    headEl,
    bodyContent,
  );
  if (stateEl) fullBody.append(stateEl);

  // Stuck delivery: release + move to another session.
  // A delivery is stuck when it's the in-flight delivery and Stuck=true.
  const isStuckDelivery =
    !isAgent && delivery?.stuck === true && msg.delivery_id === delivery.id;

  if (isStuckDelivery && delivery) {
    // Show the stuck state in the footer.
    // Colour: the muted state colour (delivery state is 'delivered' → muted by
    // stateColor; we do NOT override with --kit-wait so it stays muted).
    // Text: "delivered <age> ago · <strong>stuck</strong>"
    //   or  "delivered just now · <strong>stuck</strong>" when age < 1 minute.
    // (fmtAge returns 'now' for < 60 s; we map that to 'just now' for readability.)
    if (stateEl) {
      const ageText = fmtAge(delivery.touched_at);
      const ageStr = ageText === 'now' ? 'just now' : `${ageText} ago`;
      stateEl.textContent = '';
      stateEl.setAttribute('data-state', 'stuck');
      const stuckBold = h('strong', { style: 'font-weight:600' });
      stuckBold.textContent = 'stuck';
      stateEl.append(`delivered ${ageStr} \u00b7 `, stuckBold);
    }

    const d = delivery;
    // Use kit-btn (neutral outlined) — the same class as the DECIDE buttons —
    // so one colour means one thing (signal = you, not admin actions).
    const releaseBtn = h('button', {
      class: 'kit-btn',
      'data-action': 'release',
      onclick() {
        void releaseDelivery(ctx, d.id);
      },
    });
    releaseBtn.textContent = 'release';

    const otherSessions = sessions.filter((s) => s.id !== d.session_id);
    const moveBtn = h('button', {
      class: 'kit-btn',
      'data-action': 'move',
      onclick() {
        void openMoveSheet(ctx, d, otherSessions);
      },
    });
    moveBtn.textContent = 'move to another session';

    fullBody.append(h('div', { class: 'cb-dock-stuck' }, releaseBtn, moveBtn));
  }

  const el = card({
    edge: isAgent ? 'agent' : 'signal',
    body: fullBody,
  });
  el.classList.add('cb-dock-card');
  if (isAgent) el.classList.add('cb-dock-card--agent');
  else el.classList.add('cb-dock-card--you');
  return el;
}

// ---- release / move ---------------------------------------------------------

async function releaseDelivery(ctx: Ctx, id: number): Promise<void> {
  try {
    await ctx.api.post('/deliveries/release', { id });
  } catch (err) {
    console.error('[dock] release delivery:', err);
  }
}

async function moveDelivery(
  ctx: Ctx,
  id: number,
  sessionId: string,
): Promise<void> {
  try {
    await ctx.api.post('/deliveries/move', { id, session: sessionId });
  } catch (err) {
    console.error('[dock] move delivery:', err);
  }
}

// Simple inline session picker for "move to another session".
function openMoveSheet(
  ctx: Ctx,
  delivery: Delivery,
  targets: Session[],
): Promise<void> {
  // Build a small inline list of target sessions.
  const items = targets.map((s) => {
    const el = h('button', { class: 'cb-dock-pick-item' });
    el.textContent = sessionLabel(s);
    el.onclick = () => {
      void moveDelivery(ctx, delivery.id, s.id);
      sheet.close();
    };
    return el;
  });

  if (items.length === 0) {
    const noOther = h(
      'p',
      { class: 'cb-dock-pick-empty' },
      'no other sessions',
    );
    items.push(noOther);
  }

  const content = h('div', { class: 'cb-dock-pick-list' }, ...items);

  const sheet = {
    el: h('div', { class: 'cb-dock-pick-sheet' }, content),
    close() {
      this.el.remove();
    },
  };

  document.body.append(sheet.el);

  // Close on Esc or backdrop click.
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      sheet.close();
      document.removeEventListener('keydown', onKey);
    }
  }
  document.addEventListener('keydown', onKey);

  return Promise.resolve();
}

// Move all threads (and queued messages) from a left session to a target.
async function moveSession(
  ctx: Ctx,
  fromSession: string,
  toSession: string,
): Promise<void> {
  try {
    await ctx.api.post('/sessions/move', {
      session: fromSession,
      target: toSession,
    });
  } catch (err) {
    console.error('[dock] move session:', err);
  }
}

// Sheet for moving a left session\'s queued work to another session.
function openSessionMoveSheet(
  ctx: Ctx,
  fromSession: string,
  targets: Session[],
): Promise<void> {
  const items = targets.map((s) => {
    const el = h('button', { class: 'cb-dock-pick-item' });
    el.textContent = sessionLabel(s);
    el.onclick = () => {
      void moveSession(ctx, fromSession, s.id);
      sheet.close();
    };
    return el;
  });

  if (items.length === 0) {
    const noOther = h(
      'p',
      { class: 'cb-dock-pick-empty' },
      'no other sessions',
    );
    items.push(noOther);
  }

  const content = h('div', { class: 'cb-dock-pick-list' }, ...items);

  const sheet = {
    el: h('div', { class: 'cb-dock-pick-sheet' }, content),
    close() {
      this.el.remove();
    },
  };

  document.body.append(sheet.el);

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      sheet.close();
      document.removeEventListener('keydown', onKey);
    }
  }
  document.addEventListener('keydown', onKey);

  return Promise.resolve();
}

// ---- session picker ---------------------------------------------------------

function buildSessionPicker(
  sessions: Session[],
  currentId: string,
  onSelect: (id: string) => void,
): HTMLElement {
  const items = sessions.map((s) => {
    const busy = s.busy ? ' · busy' : ' · idle';
    const stale = s.left ? ' · left' : '';
    const label = sessionLabel(s) + busy + stale;
    const el = h('button', {
      class:
        'cb-dock-pick-item' +
        (s.id === currentId ? ' cb-dock-pick-item--on' : ''),
    });
    el.textContent = label;
    el.onclick = () => {
      onSelect(s.id);
      picker.remove();
    };
    return el;
  });

  const picker = h(
    'div',
    { class: 'cb-dock-picker', role: 'listbox' },
    ...items,
  );

  // Close on Esc.
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      picker.remove();
      document.removeEventListener('keydown', onKey);
    }
  }
  document.addEventListener('keydown', onKey);

  // Close on click outside.
  function onClick(e: MouseEvent) {
    if (!picker.contains(e.target as Node)) {
      picker.remove();
      document.removeEventListener('click', onClick);
    }
  }
  setTimeout(() => document.addEventListener('click', onClick), 0);

  return picker;
}

// ---- makeDock ---------------------------------------------------------------

export function makeDock(ctx: Ctx): DockHandle {
  // ---- state ---------------------------------------------------------------
  let sessions: Session[] = [];
  let currentSessionId = '';
  let threads: Thread[] = [];
  let currentThreadId = 0;
  let messages: MessageView[] = [];
  let currentDelivery: Delivery | null = null;
  // The last session explicitly chosen by the user — new threads default to it.
  let lastUsedSessionId = '';

  // ---- DOM elements --------------------------------------------------------

  // Session header: top row [● label] [AGENT ▾], optional second row for left status.
  const sessionDot = h('span', { class: 'cb-dock-dot' });
  const sessionLabelEl = h('span', { class: 'cb-dock-session-label' });
  const sessionPickerBtn = h('button', {
    class: 'cb-dock-agent-btn',
    'aria-label': 'pick agent session',
    onclick(e: Event) {
      e.stopPropagation();
      if (!sessions.length) return;
      const p = buildSessionPicker(sessions, currentSessionId, (id) => {
        lastUsedSessionId = id;
        void switchSession(id);
      });
      const btn = e.currentTarget as HTMLElement;
      const rect = btn.getBoundingClientRect();
      p.style.top = `${rect.bottom + 4}px`;
      p.style.right = `${window.innerWidth - rect.right}px`;
      document.body.append(p);
    },
  });
  sessionPickerBtn.textContent = 'AGENT ▾';

  // leftStatusRow is shown below the top row when the session is left.
  const leftStatusRow = h('div', { class: 'cb-dock-left-row' });
  leftStatusRow.hidden = true;

  const sessionHeader = h(
    'div',
    { class: 'cb-dock-header' },
    h(
      'div',
      { class: 'cb-dock-header-row' },
      h('span', { class: 'cb-dock-who' }, sessionDot, sessionLabelEl),
      sessionPickerBtn,
    ),
    leftStatusRow,
  );

  // Thread chips row.
  const threadChips = h('div', { class: 'cb-dock-threads' });

  // Message cards area.
  const messageArea = h('div', {
    class: 'cb-dock-messages',
    'data-testid': 'dock-messages',
  });

  // Root rail element.
  const rail = h('div', { class: 'cb-dock-inner' });
  rail.append(sessionHeader, threadChips, messageArea);

  // ---- render helpers -------------------------------------------------------

  function renderHeader() {
    const sess = sessions.find((s) => s.id === currentSessionId);
    if (!sess) {
      sessionDot.style.background = 'var(--kit-muted)';
      sessionLabelEl.textContent = 'no session';
      leftStatusRow.hidden = true;
      sessionHeader.removeAttribute('data-left');
      return;
    }

    // left is server-computed: true when last_seen > LeftAfter threshold.
    const stale = !!sess.left;
    // Let CSS drive the dot colour for both states (finding 3):
    //   .cb-dock-dot               { background: var(--kit-agent) }  // present
    //   .cb-dock-header[data-left] .cb-dock-dot { background: var(--kit-muted) } // left
    // Clear any prior inline style so the CSS cascade applies cleanly.
    sessionDot.style.removeProperty('background');
    // Always show the session label on the top row (CSS truncates it).
    sessionLabelEl.textContent = sessionLabel(sess);

    if (stale) {
      // Second row: "left · N queued · move to…" with flex-gap spacing (finding 4).
      // Build as separate span/button children so the CSS gap gives even spacing.
      leftStatusRow.textContent = '';
      const moveLink = h('button', {
        class: 'cb-dock-move-link',
        'data-testid': 'dock-move-link',
        onclick(e: Event) {
          e.stopPropagation();
          const others = sessions.filter((s) => s.id !== sess.id);
          void openSessionMoveSheet(ctx, sess.id, others);
        },
      });
      moveLink.textContent = 'move to…';
      const parts: Node[] = [h('span', {}, 'left')];
      if (sess.queued > 0) {
        parts.push(h('span', { class: 'cb-dock-sep' }, '·'));
        parts.push(h('span', {}, `${sess.queued} queued`));
      }
      parts.push(h('span', { class: 'cb-dock-sep' }, '·'));
      parts.push(moveLink);
      leftStatusRow.append(...parts);
      leftStatusRow.hidden = false;
      sessionHeader.setAttribute('data-left', '1');
    } else {
      leftStatusRow.hidden = true;
      leftStatusRow.textContent = '';
      sessionHeader.removeAttribute('data-left');
    }
  }

  function renderThreadChips() {
    threadChips.innerHTML = '';
    for (const t of threads) {
      const chip = h('button', {
        class: 'kit-chip' + (t.id === currentThreadId ? ' on' : ''),
        'data-thread': String(t.id),
        onclick() {
          void switchThread(t.id);
        },
      });
      chip.textContent = t.name;
      threadChips.append(chip);
    }
    // "+" chip — create a new thread.
    const addChip = h('button', {
      class: 'kit-chip cb-dock-add',
      'data-testid': 'dock-add-thread',
      onclick() {
        void newThread();
      },
    });
    addChip.textContent = '+';
    threadChips.append(addChip);
  }

  function renderMessages() {
    messageArea.innerHTML = '';
    if (messages.length === 0) {
      const empty = h('p', { class: 'cb-dock-empty' }, 'no messages');
      messageArea.append(empty);
      return;
    }
    for (const msg of messages) {
      const el = renderMsgCard(msg, currentDelivery, sessions, ctx);
      messageArea.append(el);
    }
    // Auto-scroll to the latest message.
    messageArea.scrollTop = messageArea.scrollHeight;
  }

  // ---- data fetching --------------------------------------------------------

  async function loadSessions() {
    try {
      const sv = await ctx.api.get<SessionsView>('/sessions');
      sessions = sv.sessions ?? [];
      if (!currentSessionId && sessions.length > 0) {
        currentSessionId = lastUsedSessionId
          ? (sessions.find((s) => s.id === lastUsedSessionId)?.id ??
            sessions[0].id)
          : sessions[0].id;
        await loadThreads();
      } else {
        // Refresh the session data for the current one, including the delivery
        // so the dock detects stuck state and the left flag without a reload.
        renderHeader();
        await loadDelivery();
      }
      renderHeader();
    } catch (err) {
      console.error('[dock] loadSessions:', err);
    }
  }

  async function loadThreads() {
    if (!currentSessionId) return;
    try {
      const tv = await ctx.api.get<ThreadsView>('/threads', {
        session: currentSessionId,
      });
      threads = tv.threads ?? [];
      if (!currentThreadId && threads.length > 0) {
        currentThreadId = threads[0].id;
      }
      renderThreadChips();
      await loadMessages();
      await loadDelivery();
    } catch (err) {
      console.error('[dock] loadThreads:', err);
    }
  }

  async function loadMessages() {
    if (!currentThreadId) {
      messages = [];
      renderMessages();
      return;
    }
    try {
      const mv = await ctx.api.get<MessagesView>('/messages', {
        thread: String(currentThreadId),
      });
      messages = mv.messages ?? [];
      renderMessages();
    } catch (err) {
      console.error('[dock] loadMessages:', err);
    }
  }

  async function loadDelivery() {
    if (!currentSessionId) return;
    try {
      const dv = await ctx.api.get<DeliveryView>('/session/delivery', {
        session: currentSessionId,
      });
      currentDelivery = dv.delivery ?? null;
      renderMessages(); // Re-render to show/hide stuck buttons.
    } catch (err) {
      console.error('[dock] loadDelivery:', err);
    }
  }

  async function switchSession(id: string) {
    currentSessionId = id;
    currentThreadId = 0;
    threads = [];
    messages = [];
    currentDelivery = null;
    renderHeader();
    renderThreadChips();
    renderMessages();
    await loadThreads();
  }

  async function switchThread(id: number) {
    currentThreadId = id;
    messages = [];
    currentDelivery = null;
    renderThreadChips();
    renderMessages();
    await loadMessages();
    await loadDelivery();
  }

  async function newThread() {
    const sessId = lastUsedSessionId || currentSessionId;
    if (!sessId) return;
    try {
      const t = await ctx.api.post<Thread>('/threads', {
        session: sessId,
        name: 'thread',
      });
      threads = [...threads, t];
      currentThreadId = t.id;
      messages = [];
      currentDelivery = null;
      renderThreadChips();
      renderMessages();
    } catch (err) {
      console.error('[dock] newThread:', err);
    }
  }

  // ---- live events ---------------------------------------------------------

  ctx.on('sessions', () => {
    void loadSessions();
  });

  ctx.on('thread', (data: unknown) => {
    // A new or moved thread — refresh threads for the current session.
    void loadThreads();
    void data; // silence unused warning
  });

  ctx.on('message', (data: unknown) => {
    // A new message was posted — if it's for the current thread, reload.
    const d = data as { thread_id?: number; thread?: number };
    const tid = d.thread_id ?? d.thread;
    if (tid === currentThreadId) {
      void loadMessages();
    }
  });

  ctx.on('messages', (data: unknown) => {
    // Delivery settled (state update for multiple messages).
    const d = data as { ids?: number[] };
    if (!d.ids) return;
    const msgIds = new Set(d.ids);
    if (messages.some((m) => msgIds.has(m.id))) {
      void loadMessages();
      void loadDelivery();
    }
  });

  ctx.on('delivery', (data: unknown) => {
    // Delivery state changed — reload both messages and delivery for current
    // session. Messages must be reloaded too because the delivery event can
    // set delivery_id on messages (when the agent picks up a delivery) which
    // the isStuckDelivery check depends on.
    // Also check 'from': a moved delivery has session=target but from=source;
    // the source dock must refresh to remove its stuck buttons.
    const d = data as { session?: string; from?: string; id?: number };
    const isOurs =
      !d.session ||
      d.session === currentSessionId ||
      d.from === currentSessionId ||
      (d.id !== undefined && currentDelivery?.id === d.id);
    if (isOurs) {
      void loadMessages().then(() => loadDelivery());
    }
  });

  // ---- initial load ---------------------------------------------------------
  void loadSessions();

  // ---- public interface ----------------------------------------------------
  return {
    el: rail,
    setAttached() {
      // Task 7 (composer) uses this. No-op for Task 6.
    },
    focusComposer() {
      // Task 7 (composer) fills this in.
    },
    currentThread(): number {
      return currentThreadId;
    },
    currentSession(): string {
      return currentSessionId;
    },
  };
}
