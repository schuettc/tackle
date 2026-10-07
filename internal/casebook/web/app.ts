// app.ts — the casebook page shell.
//
// boot() mounts the app: the bar (brand, sections with counts, status, live
// pill, theme), the live client, the API client, the keyboard layer, and the
// router. Each section fills the list+reading column pair; the agent dock stub
// occupies the rail.
//
// Later tasks import registerSection and registerDock to fill in the sections.

import {
  bar,
  type BarHandle,
  createApi,
  type Api,
  createKeys,
  type Keys,
  type KeyBinding,
  type ListNav,
  live,
  type LiveStatus,
  initTheme,
  h,
  type Primary,
} from '/_kit/kit.js';
import type { Attached, SummaryView } from './wire.d.ts';
import { onRoute, dispatchCurrent, go, type Route } from './router.ts';
import { makeKeyLayer } from './key-layer.ts';

// ---- public contracts -------------------------------------------------------

export interface Ctx {
  api: Api;
  bar: BarHandle;
  keys: Keys;
  route: Router;
  on(type: string, cb: (data: unknown) => void): void;
  setPrimary(p: Primary | null): void;
  /**
   * setAttached sets what goes with Court's next message: the composer's
   * `attached:` line. Each section calls it when its context changes and
   * when it is shown: Attention with the selection's keys (or the open item),
   * Rules (Task 8) with {rule: id}, Apply (Task 9) with {job: id}.
   */
  setAttached(a: Attached, jobTitle?: string): void;
  /** focusComposer puts the cursor in the dock's message field. */
  focusComposer(): void;
  /**
   * The attached session's agent ("pi", "claude"; "" before one is known).
   * The dock sets it; sections that name the agent (Rules' "or ask pi to
   * draft one") read it and hear when it changes.
   */
  agentName(): string;
  setAgentName(name: string): void;
  onAgentName(cb: (name: string) => void): void;
  /**
   * The dock's session when it was chosen: the session that opened the page
   * (the URL's ?session=) or the one Court picked in the dock ('' until
   * then; the dock attaching to the only session here on its own is not a
   * choice). To apply offers it for a plan's outward steps.
   */
  dockSession(): string;
  setDockSession(id: string): void;
  onDockSession(cb: (id: string) => void): void;
  /**
   * The session the composer sends to now, while it is here (its id and
   * the name the dock's header shows), else null: none attached, or the
   * attached one left. The dock sets it; Attention's "ask … to recommend
   * the rest" reads it and hears when it changes.
   */
  dockTarget(): DockTarget | null;
  setDockTarget(t: DockTarget | null): void;
  onDockTarget(cb: (t: DockTarget | null) => void): void;
  /**
   * askSession sends body to the dock's session through the composer's send
   * path (queued until its turn ends), with attached (nothing by default)
   * and the message's purpose (serve's: "look-into"; none by default).
   * Resolves '' when posted, else why not.
   */
  askSession(
    body: string,
    attached?: Attached,
    purpose?: string,
  ): Promise<string>;
}

/** The session the dock's composer sends to. */
export interface DockTarget {
  id: string;
  name: string;
}

export interface Router {
  current(): Route;
  go(section: string, sub?: string): void;
}

export interface Section {
  id: 'attention' | 'rules' | 'apply';
  list: HTMLElement;
  read: HTMLElement;
  /**
   * show makes the section active for a route. The active section owns the
   * composer's attached context: it calls ctx.setAttached here and whenever
   * its context changes while it is active.
   */
  show(sub: string): void;
  /** hide tells the section it is no longer active (another one is shown). */
  hide(): void;
  /**
   * onLive receives every live event, active or not, so a hidden section's
   * state (its selection, its counts) stays current for when it comes back.
   *
   * The stream starts where the page's first summary left the log (its
   * cursor): what the page shows on load comes from its own first reads,
   * never from replaying the log. 'gap' says serve pruned events this page
   * had not heard (it was away that long): every section reloads what it
   * shows.
   */
  onLive(type: string, data: unknown): void;
  /**
   * primary is the bar's one filled button for this section now. app.ts sets
   * it after show(); while active, a section calls ctx.setPrimary when it
   * changes, and never while hidden.
   */
  primary(): Primary | null;
  /**
   * keys are the section's own keys that work now (Attention's d, a / r;
   * Rules' a, activate; Apply's a and p). app.ts asks after every show() and
   * binds them while the section is shown, and only then: a hidden section
   * keeps its open item and selection, and a key must not reach them. It
   * also lets two sections bind the same key, which the kit's registry
   * otherwise refuses (a clash throws). Return the same array while nothing
   * changed.
   *
   * A section never calls ctx.keys.register for a key of its own: that binds
   * it page-wide. ctx.keys.register is for page-wide keys (the dock's). A key
   * that clashes when its section is shown is reported on the console; the
   * probe fails on that, and the section still shows.
   */
  keys?(): KeyBinding[];
  /**
   * listKeys is the list the family's list keys drive while the section shows
   * (createKeys({list}): j/↓ k/↑ o/↵, x ⇧x when its rows select, / when it
   * has a search field), or null when none is shown (Attention's board).
   * app.ts asks after every show(), like keys.
   */
  listKeys?(): { nav: ListNav; selects: boolean } | null;
}

export interface DockHandle {
  el: HTMLElement;
  setAttached(a: Attached, jobTitle?: string): void;
  focusComposer(): void;
  currentThread(): number;
  currentSession(): string;
  /** sendText: the composer's sendText (see Ctx.askSession). */
  sendText(
    body: string,
    attached?: Attached,
    purpose?: string,
  ): Promise<string>;
}

// ---- section + dock registry ------------------------------------------------

type SectionMaker = (ctx: Ctx) => Section;
type DockMaker = (ctx: Ctx) => DockHandle;

const sectionMakers: SectionMaker[] = [];
const dockMakers: DockMaker[] = [];

export function registerSection(make: SectionMaker): void {
  sectionMakers.push(make);
}

export function registerDock(make: DockMaker): void {
  dockMakers.push(make);
}

// ---- live event bus ---------------------------------------------------------

const liveListeners = new Map<string, Array<(data: unknown) => void>>();

function onLiveEvent(type: string, cb: (data: unknown) => void): void {
  let list = liveListeners.get(type);
  if (!list) {
    list = [];
    liveListeners.set(type, list);
  }
  list.push(cb);
}

function emitLive(type: string, data: unknown): void {
  const list = liveListeners.get(type);
  if (list) for (const cb of list) cb(data);
}

// ---- boot -------------------------------------------------------------------

let booted = false;

// boot asks for the summary first, then mounts the page: the summary's
// cursor is where the live stream starts, and every first read the page
// makes (the sections', the dock's) is sent after serve read that cursor,
// so an event published meanwhile is heard, not missed. Without a summary
// (serve not answering) the page still mounts, and its stream starts
// without a cursor.
export function boot(): void {
  if (booted) return;
  booted = true;
  void fetch('/api/summary', { credentials: 'same-origin' })
    .then((r) => (r.ok ? (r.json() as Promise<SummaryView>) : null))
    .catch(() => null)
    .then((first) => start(first));
}

function start(first: SummaryView | null): void {
  // ---- API client -----------------------------------------------------------

  // A restarted serve no longer knows this tab's token (spec §2.4): the first
  // 401, from the API or the live poll, makes the tab stale. It says so in the
  // live pill, and stops: no stream, no polls, and no API call leaves it
  // again (serve has reopened the page in a new tab).
  let stale = false;
  const gated = (input: RequestInfo | URL, init?: RequestInit) =>
    stale
      ? Promise.reject(
          new Error('casebook serve restarted: this tab has stopped'),
        )
      : fetch(input, init);
  const api = createApi({
    fetch: gated,
    onStale() {
      goStale();
    },
  });

  // ---- bar ------------------------------------------------------------------

  // Casebook mark: the open-casebook glyph (resting frame of mark.svg) inlined
  // as DOM so the kit bar renders it at whatever size the brand slot needs.
  // Shape-matches favicon.svg and mark.svg's resting frame (PALETTE.md).
  //
  // SVG elements must be created with createElementNS (SVG namespace) so the
  // browser renders them as vector graphics.  h() uses document.createElement
  // which creates HTML elements that have no intrinsic size or SVG rendering.
  function svgEl(
    tag: string,
    attrs: Record<string, string>,
    ...children: SVGElement[]
  ): SVGElement {
    const NS = 'http://www.w3.org/2000/svg';
    const el = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
    for (const child of children) el.appendChild(child);
    return el;
  }

  const mark = svgEl(
    'svg',
    {
      viewBox: '0 0 64 64',
      fill: 'none',
      xmlns: 'http://www.w3.org/2000/svg',
      'aria-hidden': 'true',
    },
    // ink tile
    svgEl('rect', { width: '64', height: '64', rx: '14', fill: '#14161d' }),
    // open book outline
    svgEl('path', {
      d: 'M32 18C25 13 16 13 8 15V49C16 47 25 47 32 52C39 47 48 47 56 49V15C48 13 39 13 32 18Z',
      fill: 'none',
      stroke: '#d98f66',
      'stroke-width': '5',
      'stroke-linejoin': 'round',
    }),
    // spine
    svgEl('path', { d: 'M32 18V52', stroke: '#d98f66', 'stroke-width': '4.4' }),
    // one neutral rule on the left page
    svgEl('path', {
      d: 'M15 29H25',
      stroke: '#9aa0ab',
      'stroke-width': '5',
      'stroke-linecap': 'round',
    }),
    // tick on the right page (fully drawn — resting frame)
    svgEl('path', {
      d: 'M37 33L42 38L50 26',
      fill: 'none',
      stroke: '#d98f66',
      'stroke-width': '6',
      'stroke-linecap': 'round',
      'stroke-linejoin': 'round',
    }),
  );

  const handle = bar({
    brand: { name: 'casebook', mark },
    sections: [
      { id: 'attention', label: 'attention' },
      { id: 'rules', label: 'rules' },
      { id: 'apply', label: 'to apply' },
    ],
    active: 'attention',
    onSection(id) {
      go(id);
    },
    staleText: 'restarted · continued in a new tab',
  });

  initTheme('casebook', handle.themeControl);

  // ---- disconnected -----------------------------------------------------------

  // serve isn't answering (spec §2.4): the pill says "disconnected", and a
  // banner under the bar says so across the page, with a retry that
  // reconnects now rather than at the next poll. Reconnecting clears both.
  const retryBtn = h(
    'button',
    {
      class: 'kit-btn',
      type: 'button',
      'data-testid': 'down-retry',
      onclick: () => retry(),
    },
    'retry',
  ) as HTMLButtonElement;
  const banner = h(
    'div',
    { class: 'cb-down', role: 'alert', 'data-testid': 'down-banner' },
    h(
      'span',
      { class: 'cb-down-text' },
      'disconnected \u00b7 the casebook server isn\u2019t answering; this page keeps trying',
    ),
    retryBtn,
  );
  banner.hidden = true;
  let down = false;
  function setDown(on: boolean): void {
    // Back from down: ask for the bar's counts and status again (one asked
    // while serve wasn't answering never came).
    if (down && !on && !stale) loadSummary();
    down = on;
    banner.hidden = !on;
    retryBtn.disabled = false;
    retryBtn.textContent = 'retry';
  }

  // ---- push failed -----------------------------------------------------------

  // serve's last push failed for a reason other than the network (a sync
  // conflict it can't resolve, the remote refusing): the status says
  // "push failed · N queued", and this one-line strip under the bar says why,
  // in serve's words. It goes when a push succeeds or nothing is queued
  // (serve clears push_error); plain offline has no strip.
  const pushText = h('span', { class: 'cb-down-text' });
  const pushStrip = h(
    'div',
    {
      class: 'cb-down cb-push-error',
      role: 'alert',
      'data-testid': 'push-error',
    },
    pushText,
  );
  pushStrip.hidden = true;
  function paintPushError(reason: string): void {
    const text = reason ? `push failed \u00b7 ${reason}` : '';
    if (pushText.textContent !== text) pushText.textContent = text;
    pushStrip.title = reason;
    pushStrip.hidden = !reason;
  }

  // ---- sections -------------------------------------------------------------

  // The app grid (.kit-app): bar | the disconnected banner | the push strip |
  // list+read | rail
  const app = h('div', { class: 'kit-app' });
  app.append(handle.el, banner, pushStrip);

  // Build context for section makers
  let currentRoute: Route = { section: 'attention', sub: '' };

  // The docks are made after the sections; a section may set what's attached
  // before then, so the latest value is kept and handed to each dock.
  const dockHandles: DockHandle[] = [];
  let lastAttached: Attached = {};
  let lastTitle = '';
  let agent = '';
  const agentListeners: Array<(name: string) => void> = [];
  let dockSession = '';
  const dockSessionListeners: Array<(id: string) => void> = [];
  let dockTarget: DockTarget | null = null;
  const dockTargetListeners: Array<(t: DockTarget | null) => void> = [];

  // The keyboard layer: always the shown section's (its list, its keys),
  // plus the page-wide keys registered on ctx.keys (key-layer.ts).
  const layer = makeKeyLayer(
    (list) => createKeys(list ? { list } : {}),
    (m) => console.error(m),
  );

  const ctx: Ctx = {
    api,
    bar: handle,
    keys: layer.keys,
    route: {
      current: () => currentRoute,
      go,
    },
    on: onLiveEvent,
    setPrimary(p) {
      handle.setPrimary(p);
    },
    setAttached(a, jobTitle = '') {
      lastAttached = a;
      lastTitle = jobTitle;
      for (const d of dockHandles) d.setAttached(a, jobTitle);
    },
    focusComposer() {
      dockHandles[0]?.focusComposer();
    },
    agentName: () => agent,
    setAgentName(name) {
      if (name === agent) return;
      agent = name;
      for (const cb of agentListeners) cb(name);
    },
    onAgentName(cb) {
      agentListeners.push(cb);
    },
    dockSession: () => dockSession,
    setDockSession(id) {
      if (id === dockSession) return;
      dockSession = id;
      for (const cb of dockSessionListeners) cb(id);
    },
    onDockSession(cb) {
      dockSessionListeners.push(cb);
    },
    dockTarget: () => dockTarget,
    setDockTarget(t) {
      if (t?.id === dockTarget?.id && t?.name === dockTarget?.name) return;
      dockTarget = t ? { ...t } : null;
      for (const cb of dockTargetListeners) cb(dockTarget);
    },
    onDockTarget(cb) {
      dockTargetListeners.push(cb);
    },
    askSession(body, attached, purpose) {
      const d = dockHandles[0];
      return d
        ? d.sendText(body, attached, purpose)
        : Promise.resolve('no agent session');
    },
  };

  // Mount sections (later tasks call registerSection before boot; Task 1 ships
  // empty shells so each section may render nothing for now).
  const sections = new Map<string, Section>();

  for (const make of sectionMakers) {
    const sec = make(ctx);
    sections.set(sec.id, sec);
    // Each section's list and read are hidden until routed to.
    sec.list.hidden = true;
    sec.read.hidden = true;
    app.append(sec.list, sec.read);
  }

  // Fallback empty section panels when no sections are registered yet.
  // These are replaced once the section modules call registerSection.
  if (sections.size === 0) {
    const list = h('div', { class: 'kit-list' });
    const read = h('div', { class: 'kit-read' });
    app.append(list, read);
  }

  // ---- dock stub ------------------------------------------------------------

  const rail = h('div', { class: 'kit-rail' });

  for (const make of dockMakers) {
    const d = make(ctx);
    d.setAttached(lastAttached, lastTitle);
    dockHandles.push(d);
  }
  if (dockHandles.length > 0) {
    for (const d of dockHandles) rail.append(d.el);
  }
  app.append(rail);

  // Mount the app
  document.body.append(app);

  // ---- live client ----------------------------------------------------------

  // The stream, polling /api/state?since=<cursor> every 2 s while it is down
  // (the kit's live()). A poll that answers means serve is back.
  function startLive(cursor?: string): ReturnType<typeof live> {
    return live({
      events: '/api/events',
      poll: '/api/state',
      cursor,
      onEvent(e) {
        // Events this page never heard were pruned: the bar asks again too.
        if (e.type === 'gap') loadSummary();
        emitLive(e.type, e.data);
        // Every section hears every event; only the active one feeds the
        // composer's attached line (Section.show/hide).
        for (const sec of sections.values()) sec.onLive(e.type, e.data);
      },
      onStatus,
      fetch: async (url, init) => {
        const res = await gated(url, init);
        if (res.ok && down && !stale) {
          setDown(false);
          handle.setLive('polling');
        }
        return res;
      },
    });
  }

  function onStatus(s: LiveStatus): void {
    if (stale) return;
    switch (s) {
      case 'stale':
        goStale();
        return;
      case 'down':
        setDown(true);
        handle.setLive('down', 'disconnected');
        return;
      case 'live':
        setDown(false);
        handle.setLive('live');
        return;
      case 'polling':
        // A stream that failed says "polling" before any poll has answered:
        // while serve is known to be down, it stays down until one does.
        if (!down) handle.setLive('polling');
        return;
    }
  }

  // retry reconnects now: a new stream from the same cursor, so nothing
  // missed is lost. If serve is still down, the poll says so again.
  function retry(): void {
    if (stale) return;
    const cursor = liveClient.cursor();
    liveClient.stop();
    retryBtn.disabled = true;
    retryBtn.textContent = 'retrying\u2026';
    liveClient = startLive(cursor);
  }

  function goStale(): void {
    if (stale) return;
    stale = true;
    liveClient?.stop();
    setDown(false);
    handle.setLive('stale');
  }

  // From the first summary's cursor: never a replay of the log.
  let liveClient = startLive(
    first && typeof first.cursor === 'number'
      ? String(first.cursor)
      : undefined,
  );

  // ---- keys -----------------------------------------------------------------

  // g a / g r / g p switch sections (spec §3.6), like the bar's controls.
  for (const [k, id, label] of [
    ['g a', 'attention', 'go to attention'],
    ['g r', 'rules', 'go to rules'],
    ['g p', 'apply', 'go to apply'],
  ] as const) {
    ctx.keys.register({ keys: k, label, run: () => go(id) });
  }

  // ---- routing --------------------------------------------------------------

  onRoute((r) => {
    currentRoute = r;

    // Map hash sections to valid section ids; default to attention.
    const sectionId =
      r.section === 'rules'
        ? 'rules'
        : r.section === 'apply'
          ? 'apply'
          : 'attention';

    handle.setSection(sectionId);

    for (const [id, sec] of sections) {
      const active = id === sectionId;
      sec.list.hidden = !active;
      sec.read.hidden = !active;
      if (!active) sec.hide();
    }
    const activeSec = sections.get(sectionId);
    if (activeSec) {
      activeSec.show(r.sub);
      // The keyboard layer follows what show() put up (Attention's board
      // or its list): only the shown section's list and keys are bound.
      layer.bind(activeSec);
      // The bar's primary follows the active section.
      ctx.setPrimary(activeSec.primary());
    } else {
      layer.bind(undefined);
      ctx.setPrimary(null);
      // No section for this route: nothing is attached while it is shown.
      ctx.setAttached({});
    }
  });

  // ---- summary (counts, status) -------------------------------------------

  // The bar's counts and its status: "synced 4m ago · <machine>", or
  // "offline · N queued" (danger) while decisions wait to be pushed. Asked
  // at boot, again whenever the index moves (a decision moves it), whenever
  // serve's background push of decisions ends ("push": it follows serve's
  // answer to a decide, so a decision it couldn't push shows without a
  // reload), and again on coming back from down.
  // An older answer never paints over a newer one; a newer one that fails
  // leaves the older one standing.
  let summaryAsked = 0;
  let summaryShown = 0;
  let summary: SummaryView | null = null;
  function loadSummary(): void {
    const mine = ++summaryAsked;
    void api
      .get<SummaryView>('/summary')
      .then((s) => showSummary(s, mine))
      .catch(() => {
        // non-fatal: the next index or decision asks again
      });
  }
  function showSummary(s: SummaryView, mine: number): void {
    if (mine < summaryShown) return;
    summaryShown = mine;
    summary = s;
    const counts = s.counts ?? {};
    // 'all' is the attention total; 'to-apply' the decided items waiting
    // to be applied. The Rules section counts its rules itself (the
    // summary has no rules count).
    handle.setCount('attention', counts['all'] ?? 0);
    handle.setCount('apply', counts['to-apply'] ?? 0);
    paintStatus();
  }
  // paintStatus draws the status from the last summary; "synced Nm ago"
  // follows the clock (no request: a stale tab keeps its words).
  function paintStatus(): void {
    const s = summary;
    if (!s) return;
    paintPushError(s.push_error ?? '');
    if (s.offline_queued > 0) {
      const why = s.push_error ? 'push failed' : 'offline';
      handle.setStatus(`${why} \u00b7 ${s.offline_queued} queued`, {
        tone: 'danger',
      });
      return;
    }
    const synced = s.synced_at ? Date.parse(s.synced_at) : NaN;
    const mins = Number.isFinite(synced)
      ? Math.max(0, Math.floor((Date.now() - synced) / 60000))
      : null;
    handle.setStatus(
      mins !== null ? `synced ${mins}m ago \u00b7 ${s.machine}` : s.machine,
      { tone: 'muted' },
    );
  }
  setInterval(paintStatus, 15000);
  // The boot's summary is the bar's first: no second request for it.
  if (first) showSummary(first, ++summaryAsked);
  else loadSummary();
  onLiveEvent('index', loadSummary);
  onLiveEvent('push', loadSummary);

  // ---- initial route --------------------------------------------------------

  dispatchCurrent();
}
