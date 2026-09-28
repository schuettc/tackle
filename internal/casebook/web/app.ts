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
  live,
  type LiveStatus,
  initTheme,
  h,
  type Primary,
} from '/_kit/kit.js';
import type { SummaryView } from './wire.d.ts';
import { onRoute, dispatchCurrent, go, type Route } from './router.ts';

// ---- public contracts -------------------------------------------------------

export interface Ctx {
  api: Api;
  bar: BarHandle;
  keys: Keys;
  route: Router;
  on(type: string, cb: (data: unknown) => void): void;
  setPrimary(p: Primary | null): void;
}

export interface Router {
  current(): Route;
  go(section: string, sub?: string): void;
}

export interface Section {
  id: 'attention' | 'rules' | 'apply';
  list: HTMLElement;
  read: HTMLElement;
  show(sub: string): void;
  onLive(type: string, data: unknown): void;
  primary(): Primary | null;
}

export interface DockHandle {
  el: HTMLElement;
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

  // Casebook mark: a simple inline SVG placeholder glyph on the signal colour.
  // The final mark is drawn in P1 and reviewed by Court before release.
  const mark = h(
    'svg',
    {
      viewBox: '0 0 18 18',
      fill: 'none',
      xmlns: 'http://www.w3.org/2000/svg',
      'aria-hidden': 'true',
    },
    h('rect', {
      x: '2',
      y: '2',
      width: '14',
      height: '14',
      rx: '3',
      fill: 'var(--kit-signal)',
    }),
    h('path', {
      d: 'M5.5 9h7M5.5 6h5M5.5 12h4',
      stroke: 'var(--kit-signal-ink)',
      'stroke-width': '1.5',
      'stroke-linecap': 'round',
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

  const dockHandles: DockHandle[] = [];
  for (const make of dockMakers) {
    dockHandles.push(make(ctx));
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
      // Dispatch to the active section.
      const sec = sections.get(currentRoute.section);
      if (sec) sec.onLive(e.type, e.data);
    },
    onStatus(s: LiveStatus) {
      handle.setLive(s);
    },
    fetch: (url, init) => fetch(url, init),
  });

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
      if (active) sec.show(r.sub);
    }

    // With no registered sections, nothing to hide/show.
  });

  // ---- summary (counts) -----------------------------------------------------

  void api
    .get<SummaryView>('/summary')
    .then((s) => {
      const counts = s.counts ?? {};
      // 'all' is the attention total; rules/apply counts arrive in later tasks.
      handle.setCount('attention', counts['all'] ?? 0);
      handle.setCount('rules', counts['rules'] ?? 0);
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
        handle.setCount('rules', counts['rules'] ?? 0);
        handle.setCount('apply', counts['apply'] ?? 0);
      })
      .catch(() => {});
  });

  // ---- initial route --------------------------------------------------------

  dispatchCurrent();

  // keep live client from being GC'd
  void liveClient;
}
