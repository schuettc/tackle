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
  live,
  type LiveStatus,
  initTheme,
  h,
  type Primary,
} from '/_kit/kit.js';
import type { Attached, SummaryView } from './wire.d.ts';
import { onRoute, dispatchCurrent, go, type Route } from './router.ts';
import { makeKeyBinder } from './section-keys.ts';

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
  setAttached(a: Attached): void;
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
   */
  onLive(type: string, data: unknown): void;
  /**
   * primary is the bar's one filled button for this section now. app.ts sets
   * it after show(); while active, a section calls ctx.setPrimary when it
   * changes, and never while hidden.
   */
  primary(): Primary | null;
  /**
   * keys are the section's own keys (Attention's a / r / d and its /
   * search; Rules' a, activate; Apply its own). app.ts registers them with
   * ctx.keys when the section is shown and unregisters them when another
   * section is shown, so they act only in
   * the active section: a hidden section keeps its open item and selection,
   * and a key must not reach them. It also lets two sections bind the same
   * key, which the kit's registry otherwise refuses (a clash throws).
   *
   * A section never calls ctx.keys.register for a key of its own: that binds
   * it page-wide. ctx.keys.register is for page-wide keys (the dock's). A key
   * that clashes when its section is shown is reported on the console; the
   * probe fails on that, and the section still shows.
   */
  keys?: KeyBinding[];
}

export interface DockHandle {
  el: HTMLElement;
  setAttached(a: Attached): void;
  focusComposer(): void;
  currentThread(): number;
  currentSession(): string;
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

export function boot(): void {
  if (booted) return;
  booted = true;

  // ---- API client -----------------------------------------------------------

  const api = createApi({
    onStale(err) {
      void err;
      handle.setLive('stale');
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

  // ---- sections -------------------------------------------------------------

  // The app grid (.kit-app): bar | list+read | rail
  const app = h('div', { class: 'kit-app' });
  app.append(handle.el);

  // Build context for section makers
  let currentRoute: Route = { section: 'attention', sub: '' };

  // The docks are made after the sections; a section may set what's attached
  // before then, so the latest value is kept and handed to each dock.
  const dockHandles: DockHandle[] = [];
  let lastAttached: Attached = {};
  let agent = '';
  const agentListeners: Array<(name: string) => void> = [];

  const ctx: Ctx = {
    api,
    bar: handle,
    keys: createKeys(),
    route: {
      current: () => currentRoute,
      go,
    },
    on: onLiveEvent,
    setPrimary(p) {
      handle.setPrimary(p);
    },
    setAttached(a) {
      lastAttached = a;
      for (const d of dockHandles) d.setAttached(a);
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
    d.setAttached(lastAttached);
    dockHandles.push(d);
  }
  if (dockHandles.length > 0) {
    for (const d of dockHandles) rail.append(d.el);
  }
  app.append(rail);

  // Mount the app
  document.body.append(app);

  // ---- live client ----------------------------------------------------------

  const liveClient = live({
    events: '/api/events',
    poll: '/api/state',
    onEvent(e) {
      emitLive(e.type, e.data);
      // Every section hears every event; only the active one feeds the
      // composer's attached line (Section.show/hide).
      for (const sec of sections.values()) sec.onLive(e.type, e.data);
    },
    onStatus(s: LiveStatus) {
      handle.setLive(s);
    },
    fetch: (url, init) => fetch(url, init),
  });

  // ---- section keys ---------------------------------------------------------

  // Only the active section's keys are registered (Section.keys). A route
  // within the same section keeps them; a route to another section swaps
  // them. A clash is reported on the console (the probe fails on it) and the
  // section still shows.
  const bindSectionKeys = makeKeyBinder(
    (b) => ctx.keys.register(b),
    (m) => console.error(m),
  );

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
    bindSectionKeys(activeSec);
    if (activeSec) {
      activeSec.show(r.sub);
      // The bar's primary follows the active section.
      ctx.setPrimary(activeSec.primary());
    } else {
      ctx.setPrimary(null);
      // A section that isn't built yet (Apply before Task 9) has no
      // context: nothing is attached while it is shown.
      ctx.setAttached({});
    }
  });

  // ---- summary (counts) -----------------------------------------------------

  void api
    .get<SummaryView>('/summary')
    .then((s) => {
      const counts = s.counts ?? {};
      // 'all' is the attention total. The Rules section counts its rules
      // itself (the summary has no rules count); apply arrives with Task 9.
      handle.setCount('attention', counts['all'] ?? 0);
      handle.setCount('apply', counts['apply'] ?? 0);

      const minsAgo = s.synced_at
        ? Math.round((Date.now() - new Date(s.synced_at).getTime()) / 60000)
        : null;
      const statusText =
        minsAgo !== null ? `synced ${minsAgo}m ago · ${s.machine}` : s.machine;
      handle.setStatus(
        s.offline_queued > 0
          ? `offline · ${s.offline_queued} queued`
          : statusText,
        { tone: s.offline_queued > 0 ? 'danger' : 'muted' },
      );
    })
    .catch(() => {
      // summary failure is non-fatal; the live client will retry
    });

  // Refresh summary on index events (rebuild).
  onLiveEvent('index', () => {
    void api
      .get<SummaryView>('/summary')
      .then((s) => {
        const counts = s.counts ?? {};
        handle.setCount('attention', counts['all'] ?? 0);
        handle.setCount('apply', counts['apply'] ?? 0);
      })
      .catch(() => {});
  });

  // ---- initial route --------------------------------------------------------

  dispatchCurrent();

  // keep live client from being GC'd
  void liveClient;
}
