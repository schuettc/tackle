// dock.ts — the agent dock (Task 6): session, threads, message cards, delivery state.
//
// The dock belongs to ONE session, the one that opened the page (see
// sessions.ts): casebook_open and `casebook serve` from a session put
// ?session=<id> in the URL. With none, the dock asks Court to choose (and
// attaches on its own only when exactly one eligible session is here). If
// the attached session leaves, the dock says so and asks again: it never
// re-targets on its own.
//
// The .kit-rail is filled with (in order from the spec §3.5):
//   1. Session header  — agent-coloured dot · name · folder · harness · AGENT ▾
//      picker; the chooser below it when no session is attached or the
//      attached one left
//   2. Thread chips    — one per thread + "+"  (active chip filled)
//   3. Message cards   — rust edge for you, amber for the agent;
//                        mono header YOU/PI with age on right;
//                        Court's cards carry the delivery-state footer;
//                        agent cards link items/rules/jobs (underlined, open the route)
//   4. Stuck delivery  — release + move to another session
//   5. Left session    — pi · <cwd> left · N queued · move to…
//   6. Batch tray      — the last card in the message area (progress.ts)
//   7. Progress line, waiting strip, composer — fixed below the message area,
//      so the composer stays pinned to the bottom of the rail (Task 7).

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
  Progress,
  SessionProgressView,
  SettledResult,
  Message,
  Attached,
} from './wire.d.ts';
import type { Ctx, DockHandle } from './app.ts';
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { fmtAge } from './time-utils.ts';
import { go } from './router.ts';
import { threadOrder } from './thread.ts';
import { makeComposer } from './composer.ts';
import { coalesced } from './coalesce.ts';
import {
  attachedState,
  pickable,
  resolveAttachment,
  sessionMeta,
  sessionTitle,
  urlSession,
  urlWithSession,
} from './sessions.ts';
import {
  makeBatchTray,
  makeProgressLine,
  makeWaitingStrip,
  workedFold,
} from './progress.ts';

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

// sessionItem is a session as a list entry (the picker, the chooser, the
// move sheets): the agent dot, its name (or folder), and the muted rest.
function sessionItem(s: Session, extra = ''): HTMLElement {
  const el = h(
    'button',
    { class: 'cb-dock-pick-item', 'data-session': s.id },
    h('span', { class: 'cb-dock-dot' }),
    h('span', { class: 'cb-dock-pick-title' }, sessionTitle(s)),
  );
  const meta = [sessionMeta(s), extra].filter(Boolean).join(' \u00b7 ');
  if (meta) el.append(h('span', { class: 'cb-dock-pick-meta' }, meta));
  return el;
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
  agentName: string,
): HTMLElement {
  // A settled turn's progress folds into the thread as `worked for …`.
  if (msg.state === 'worked') return workedFold(msg);

  const isAgent = msg.author !== 'court';

  // Build the card header: NAME · age on the right.
  const authorLabel = isAgent ? agentName.toUpperCase() : 'YOU';
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

    const otherSessions = pickable(sessions).filter(
      (s) => s.id !== d.session_id,
    );
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
    const el = sessionItem(s);
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
    const el = sessionItem(s);
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

// The picker offers the eligible sessions only (live, not workers), by name.
function buildSessionPicker(
  sessions: Session[],
  currentId: string,
  onSelect: (id: string) => void,
): HTMLElement {
  const items: HTMLElement[] = pickable(sessions).map((s) => {
    const el = sessionItem(s, s.busy ? 'busy' : '');
    if (s.id === currentId) el.classList.add('cb-dock-pick-item--on');
    el.onclick = () => {
      onSelect(s.id);
      picker.remove();
    };
    return el;
  });
  if (items.length === 0) {
    items.push(
      h('p', { class: 'cb-dock-pick-empty' }, 'no agent sessions are here'),
    );
  }

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
  // The URL's session is read once, at the first load: after that the
  // attachment is what Court (or the lone-session rule) made it.
  let urlRead = false;
  // auto: the attachment is the lone-session guess, not a choice. It holds
  // only while that session is the only eligible one here.
  let auto = false;

  // ---- DOM elements --------------------------------------------------------

  // Session header: top row [● name] [AGENT ▾]; below it, muted, the
  // session's folder · harness; then the left status row when it left.
  const sessionDot = h('span', { class: 'cb-dock-dot' });
  const sessionLabelEl = h('span', {
    class: 'cb-dock-session-label',
    'data-testid': 'dock-session-name',
  });
  const sessionMetaEl = h('div', {
    class: 'cb-dock-meta-row',
    'data-testid': 'dock-session-meta',
  });
  const sessionPickerBtn = h('button', {
    class: 'cb-dock-agent-btn',
    'aria-label': 'pick agent session',
    onclick(e: Event) {
      e.stopPropagation();
      const p = buildSessionPicker(sessions, currentSessionId, (id) => {
        void attach(id, true);
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
    sessionMetaEl,
    leftStatusRow,
  );

  // The chooser: shown when no session is attached, or the attached one
  // left. It never picks for Court.
  const chooser = h('div', {
    class: 'cb-dock-chooser',
    'data-testid': 'dock-chooser',
  });
  chooser.hidden = true;

  // Thread chips row.
  const threadChips = h('div', { class: 'cb-dock-threads' });

  // Message cards area.
  const messageArea = h('div', {
    class: 'cb-dock-messages',
    'data-testid': 'dock-messages',
  });

  // ---- Task 7: batch tray, progress line, waiting strip, composer ---------

  // blocked is why nothing can be sent now ('' when it can): no session is
  // attached, or the attached one left. The composer and the batch tray both
  // ask; neither sends anywhere else in its place.
  const blocked = (): string => {
    switch (attachedState(currentSessionId, sessions)) {
      case 'none':
        return 'choose a session first';
      case 'left':
      case 'unknown':
        return 'your session left';
    }
    return '';
  };

  // The batch tray is the last card in the scrolling message area (mock).
  const batchTray = makeBatchTray(ctx, {
    blocked,
    say: (text) => composer.say(text),
    currentSession: () => currentSessionId,
    threadMoved: () => void loadThreads(),
  });
  const progLine = makeProgressLine();
  const waitStrip = makeWaitingStrip();
  const composer = makeComposer(ctx, {
    currentThread: () => currentThreadId,
    currentSession: () => currentSessionId,
    blocked,
    threadMoved: () => void loadThreads(),
    threadCreated(t: Thread) {
      threads = [...threads, t];
      currentThreadId = t.id;
      renderThreadChips();
      void batchTray.load(t.id);
    },
  });

  // Root rail element: the message area scrolls; everything below it is
  // fixed, so the composer stays pinned to the bottom of the rail.
  const rail = h('div', { class: 'cb-dock-inner' });
  rail.append(
    sessionHeader,
    chooser,
    threadChips,
    messageArea,
    progLine.el,
    waitStrip.el,
    composer.el,
  );

  // agentName is the current session's harness ("pi", "claude").
  function agentName(): string {
    return sessions.find((s) => s.id === currentSessionId)?.harness || 'agent';
  }

  // ---- render helpers -------------------------------------------------------

  function renderHeader() {
    const state = attachedState(currentSessionId, sessions);
    sessionHeader.setAttribute('data-attach', state);
    const sess = sessions.find((s) => s.id === currentSessionId);
    // The session the composer sends to, while it is here (blocked() is '').
    ctx.setDockTarget(
      sess && !blocked() ? { id: sess.id, name: sessionTitle(sess) } : null,
    );
    if (!sess) {
      // No session, or one serve no longer knows: the dot is muted and the
      // header says which.
      sessionDot.style.background = 'var(--kit-muted)';
      sessionLabelEl.textContent =
        state === 'unknown' ? 'session not here' : 'no session';
      sessionMetaEl.textContent = '';
      sessionHeader.removeAttribute('title');
      leftStatusRow.hidden = true;
      if (state === 'unknown') sessionHeader.setAttribute('data-left', '1');
      else sessionHeader.removeAttribute('data-left');
      renderChooser(state, null);
      return;
    }

    // left is server-computed: true when last_seen > LeftAfter threshold.
    const stale = !!sess.left;
    // Let CSS drive the dot colour for both states (finding 3):
    //   .cb-dock-dot               { background: var(--kit-agent) }  // present
    //   .cb-dock-header[data-left] .cb-dock-dot { background: var(--kit-muted) } // left
    // Clear any prior inline style so the CSS cascade applies cleanly.
    sessionDot.style.removeProperty('background');
    // The session's name (or folder), then its folder and harness, muted.
    sessionLabelEl.textContent = sessionTitle(sess);
    sessionMetaEl.textContent = sessionMeta(sess);
    sessionHeader.title = sess.id;

    if (stale) {
      // Second row: "left · N queued · move to…" with flex-gap spacing (finding 4).
      // Build as separate span/button children so the CSS gap gives even spacing.
      leftStatusRow.textContent = '';
      const moveLink = h('button', {
        class: 'cb-dock-move-link',
        'data-testid': 'dock-move-link',
        onclick(e: Event) {
          e.stopPropagation();
          const others = pickable(sessions).filter((s) => s.id !== sess.id);
          void openSessionMoveSheet(ctx, sess.id, others);
        },
      });
      moveLink.textContent = 'move to\u2026';
      const parts: Node[] = [h('span', {}, 'left')];
      if (sess.queued > 0) {
        parts.push(h('span', { class: 'cb-dock-sep' }, '\u00b7'));
        parts.push(h('span', {}, `${sess.queued} queued`));
      }
      parts.push(h('span', { class: 'cb-dock-sep' }, '\u00b7'));
      parts.push(moveLink);
      leftStatusRow.append(...parts);
      leftStatusRow.hidden = false;
      sessionHeader.setAttribute('data-left', '1');
    } else {
      leftStatusRow.hidden = true;
      leftStatusRow.textContent = '';
      sessionHeader.removeAttribute('data-left');
    }
    renderChooser(state, sess);
  }

  // renderChooser shows the chooser when there is no session to talk to:
  // none attached (the threads and messages hide: there are none), or the
  // attached one left (its threads stay readable below). Choosing attaches
  // the page to that session; nothing is chosen for Court.
  function renderChooser(
    state: 'none' | 'here' | 'left' | 'unknown',
    sess: Session | null,
  ) {
    const none = state === 'none';
    threadChips.hidden = none;
    messageArea.hidden = none;
    chooser.textContent = '';
    if (state === 'here') {
      chooser.hidden = true;
      return;
    }
    chooser.hidden = false;
    chooser.setAttribute('data-state', state);
    const offered = pickable(sessions).filter((s) => s.id !== currentSessionId);
    const lead = h('p', {
      class: 'cb-dock-chooser-lead',
      'data-testid': 'dock-chooser-lead',
    });
    if (none) {
      lead.textContent =
        offered.length > 0
          ? 'Choose the session your messages go to.'
          : 'No agent session is here. Ask one to casebook_open this page.';
    } else {
      const who = sess ? sessionTitle(sess) : 'This page\u2019s session';
      lead.textContent =
        offered.length > 0
          ? `${who} left. Messages wait for it; choose a session to keep talking.`
          : `${who} left. Messages wait for it; no other session is here.`;
    }
    chooser.append(lead);
    for (const s of offered) {
      const el = sessionItem(s, s.busy ? 'busy' : '');
      el.onclick = () => {
        void attach(s.id, true);
      };
      chooser.append(el);
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
    // The draft batch is the last card (it hides itself when empty). It
    // stays put while the cards before it are drawn again: moved out and
    // back, a draft Court is editing in it would lose his focus, and the
    // edit would commit what he had typed so far.
    for (const c of [...messageArea.children]) {
      if (c !== batchTray.el) c.remove();
    }
    if (batchTray.el.parentNode !== messageArea) {
      messageArea.append(batchTray.el);
    }
    const cards: HTMLElement[] = [];
    if (messages.length === 0) {
      cards.push(h('p', { class: 'cb-dock-empty' }, 'no messages'));
    }
    const name = agentName();
    for (const msg of messages) {
      cards.push(renderMsgCard(msg, currentDelivery, sessions, ctx, name));
    }
    batchTray.el.before(...cards);
    // Auto-scroll to the latest message.
    messageArea.scrollTop = messageArea.scrollHeight;
  }

  // ---- data fetching --------------------------------------------------------

  // loadSessions asks for the sessions the dock shows: serve's eligible
  // ones (the chooser, "move to…") and the attached one, whatever its
  // state (its header says when it has left). Coalesced: a burst of live
  // events is one request in flight and one queued, never one each.
  const loadSessions = coalesced(fetchSessions);
  async function fetchSessions() {
    try {
      const attached =
        currentSessionId || (urlRead ? '' : urlSession(location.href));
      const sv = await ctx.api.get<SessionsView>(
        '/sessions',
        attached ? { session: attached } : undefined,
      );
      sessions = sv.sessions ?? [];
      if (!currentSessionId) {
        // The URL's session (read once), else the lone eligible session,
        // else none: Court chooses.
        const fromUrl = urlRead ? '' : urlSession(location.href);
        urlRead = true;
        const r = resolveAttachment(fromUrl, sessions);
        if (r.id) {
          await attach(r.id, !r.auto);
          return;
        }
      } else if (auto && pickable(sessions).length > 1) {
        // A second session arrived: the lone-session guess no longer
        // holds. The dock goes back to asking (Court's text stays).
        detach();
        return;
      } else {
        // Refresh the session data for the current one, including the delivery
        // so the dock detects stuck state and the left flag without a reload.
        await loadDelivery();
      }
      renderHeader();
      updateSessionParts();
    } catch (err) {
      console.error('[dock] loadSessions:', err);
    }
  }

  // attach makes id the page's session. explicit: the session opened the
  // page (the URL) or Court chose it: the URL carries it, so a reload keeps
  // it, and To apply offers it for a plan. The lone-session attachment is
  // not a choice: it shows in the header but isn't written anywhere, so a
  // reload with more sessions here asks again.
  async function attach(id: string, explicit: boolean) {
    auto = !explicit;
    if (explicit) {
      const next = urlWithSession(location.href, id);
      if (next !== location.href) {
        history.replaceState(history.state, '', next);
      }
      ctx.setDockSession(id);
    }
    if (id === currentSessionId) {
      renderHeader();
      return;
    }
    await switchSession(id);
  }

  async function loadThreads() {
    if (!currentSessionId) return;
    try {
      const tv = await ctx.api.get<ThreadsView>('/threads', {
        session: currentSessionId,
      });
      threads = tv.threads ?? [];
      // A thread moved to another session (a stuck delivery moved, "move
      // to…") is no longer this session's: it stops being the open one.
      if (!threads.some((t) => t.id === currentThreadId)) {
        currentThreadId = threads.length > 0 ? threads[0].id : 0;
      }
      renderThreadChips();
      await loadMessages();
      await loadDelivery();
      await batchTray.load(currentThreadId);
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
      // A batch's cards show in its batch order (the order it is delivered).
      messages = threadOrder(mv.messages ?? []);
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

  // loadProgress shows the session's current progress line on load (the
  // live 'progress' event keeps it current afterwards).
  //
  // A live progress or settled event is newer than any load asked before
  // it: a load whose answer lands after one (serve answered before the
  // agent reported, the reply came late) is dropped, never shown over it.
  // serve's line carries its own clock (now): the line's age is now −
  // updated_at by serve's clock, counted on in the page's, so a page whose
  // clock differs from serve's still reads the right age.
  let progSeq = 0;
  async function loadProgress() {
    const sid = currentSessionId;
    const mine = ++progSeq;
    if (!sid) {
      progLine.clear();
      return;
    }
    try {
      const pv = await ctx.api.get<SessionProgressView>('/session/progress', {
        session: sid,
      });
      if (sid !== currentSessionId || mine !== progSeq) return;
      if (pv.progress) {
        const age = Date.parse(pv.now) - Date.parse(pv.progress.updated_at);
        progLine.set(pv.progress, Date.now() - Math.max(0, age));
      } else {
        progLine.clear();
      }
    } catch (err) {
      console.error('[dock] loadProgress:', err);
    }
  }

  // updateSessionParts refreshes what depends on the current session's row:
  // the waiting strip's count and the agent's name in the composer.
  //
  // The strip describes messages held behind a running turn, so it shows
  // only while serve says the session is busy (a delivery in flight) and
  // present. A left session's queued work is Task 6's header (left · N
  // queued · move to…); an idle session's queue has no turn to wait for.
  function updateSessionParts() {
    const sess = sessions.find((s) => s.id === currentSessionId);
    const turn = !!sess && sess.busy && !sess.left;
    waitStrip.setQueued(turn ? sess.queued : 0);
    composer.setAgent(sess?.harness ?? '');
    ctx.setAgentName(sess?.harness ?? '');
  }

  // detach drops a lone-session guess: no session, so the chooser asks.
  function detach() {
    auto = false;
    currentSessionId = '';
    currentThreadId = 0;
    threads = [];
    messages = [];
    currentDelivery = null;
    void batchTray.load(0);
    progLine.clear();
    renderHeader();
    renderThreadChips();
    renderMessages();
    updateSessionParts();
  }

  async function switchSession(id: string) {
    currentSessionId = id;
    currentThreadId = 0;
    // The tray holds the previous session's batch until the new threads
    // load: empty it now, so it can never send that batch here.
    void batchTray.load(0);
    threads = [];
    messages = [];
    currentDelivery = null;
    progLine.clear();
    renderHeader();
    renderThreadChips();
    renderMessages();
    updateSessionParts();
    await Promise.all([loadThreads(), loadProgress()]);
  }

  async function switchThread(id: number) {
    currentThreadId = id;
    messages = [];
    currentDelivery = null;
    renderThreadChips();
    renderMessages();
    await loadMessages();
    await loadDelivery();
    await batchTray.load(id);
  }

  async function newThread() {
    // A thread is opened with the attached session, and only while it is here.
    const sessId = currentSessionId;
    const why = blocked();
    if (why) {
      composer.say(why);
      return;
    }
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
      await batchTray.load(t.id);
    } catch (err) {
      console.error('[dock] newThread:', err);
    }
  }

  // ---- live events ---------------------------------------------------------

  ctx.on('sessions', (data: unknown) => {
    void loadSessions();
    // "move to…" took this session's threads to another one: they are no
    // longer this session's, so the dock drops them.
    const d = data as { moved_from?: string } | null;
    if (d?.moved_from && d.moved_from === currentSessionId) {
      void loadThreads();
    }
  });

  ctx.on('thread', (data: unknown) => {
    // A new or moved thread — refresh threads for the current session.
    void loadThreads();
    void data; // silence unused warning
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
    if (d.from && d.from === currentSessionId && d.session !== d.from) {
      // A delivery moved away from this session took its thread with it:
      // reload the threads, so the moved one is no longer shown (or sent
      // to) as this session's.
      void loadThreads();
      void loadSessions();
    } else if (isOurs) {
      void loadMessages().then(() => loadDelivery());
      // Also refresh sessions to update queued count for the waiting strip.
      void loadSessions();
    }
  });

  // progress: the agent set its live line. It arrived now, so its age
  // counts from now in page time.
  ctx.on('progress', (data: unknown) => {
    const p = data as Progress;
    if (p.session_id !== currentSessionId) return;
    progSeq++; // a load in flight is older than this
    progLine.set(p, Date.now());
  });

  // settled: the turn ended. The line clears; serve has folded it into the
  // thread as a `worked for …` message, which the reload shows.
  ctx.on('settled', (data: unknown) => {
    const d = data as SettledResult;
    if (d.session !== currentSessionId) return;
    progSeq++; // a load in flight is older than this
    progLine.clear();
    void loadMessages();
    void loadSessions();
  });

  // gap: serve pruned events this page never heard (it was away that
  // long). Whatever they said, the dock reloads all it shows.
  ctx.on('gap', () => {
    void loadSessions();
    void loadThreads();
    void loadProgress();
  });

  // drafts: a draft was edited, removed or reordered.
  ctx.on('drafts', () => {
    void batchTray.reload();
  });

  // batch: a batch was sent; its drafts are now queued cards.
  ctx.on('batch', () => {
    void batchTray.reload();
    void loadMessages();
    void loadSessions();
  });

  // message: Court posted a message (sent or drafted).
  ctx.on('message', (data: unknown) => {
    const m = data as Message;
    if (m.thread_id === currentThreadId) {
      void loadMessages();
      if (m.state === 'draft') void batchTray.reload();
    }
    // The queued count may have changed.
    void loadSessions();
  });

  // The progress line's age and `no progress for 4m` follow the clock.
  setInterval(() => progLine.tick(), 1000);

  // ---- keys (spec §3.6) ------------------------------------------------------

  ctx.keys.register({
    keys: '.',
    label: 'focus the composer',
    group: 'agent',
    run() {
      composer.focus();
    },
  });
  ctx.keys.register({
    keys: '\u2318\u21b5',
    label: 'add to batch',
    group: 'agent',
    inField: true,
    run(e: KeyboardEvent) {
      if (e.target !== composer.input) return false;
      composer.addToBatch();
    },
  });

  // ---- initial load ---------------------------------------------------------
  void loadSessions();

  // ---- public interface ----------------------------------------------------
  return {
    el: rail,
    setAttached(a: Attached, jobTitle?: string): void {
      composer.setAttached(a, jobTitle);
    },
    focusComposer(): void {
      composer.focus();
    },
    currentThread(): number {
      return currentThreadId;
    },
    currentSession(): string {
      return currentSessionId;
    },
    sendText(body: string, attached?: Attached): Promise<string> {
      return composer.sendText(body, attached);
    },
  };
}
