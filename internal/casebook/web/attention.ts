// attention.ts — the Attention section: list, views, filters, item detail.
//
// makeAttention(ctx) builds the kit list(), wires view + filter chips,
// fetches items from GET /api/items, and opens items in the reading column via
// renderItem() from item.ts.
//
// The shared Selection store (used by the board view in Task 4) is exported so
// Task 4 can bind to it without creating a second store.

import {
  list,
  createSelection,
  type Selection,
  h,
  type Chip,
  type ListHandle,
} from '/_kit/kit.js';
import type {
  SummaryView,
  ItemsView,
  ItemView,
  ItemDetailView,
} from './wire.d.ts';
import type { Ctx, Section } from './app.ts';
import { renderItem } from './item.ts';
import { wireSelection } from './decide.ts';

// PAGE_SIZE is the number of items fetched per page. The kit is tested to 500
// rendered rows; we paginate at 200 to stay safe.
const PAGE_SIZE = 200;

// Shared selection store — the board view (Task 4) imports this.
export const selection: Selection = createSelection();

// View chip ids map to API view= param values.
const VIEWS: Chip[] = [
  { id: 'waiting', label: 'waiting on you' },
  { id: 'new', label: 'new' },
  { id: 'due', label: 'due' },
  { id: 'proposed', label: 'proposed' },
  { id: 'board', label: '▦ board' },
];

// Filter chip ids are used as query param names.  The text filter is handled
// separately (a noteField-style input is rendered as a filter chip with no
// on/off toggle).
const FILTER_CHIPS: Chip[] = [
  { id: 'kind', label: 'kind' },
  { id: 'repo', label: 'repo' },
  { id: 'relation', label: 'relation' },
  { id: 'bot', label: 'bot/human' },
  { id: 'age', label: 'age' },
  { id: 'rule', label: 'rule' },
];

// State for the current view and active filters.
interface Filters extends Record<string, string> {
  view: string;
  kind: string;
  repo: string;
  relation: string;
  bot: string;
  age: string;
  rule: string;
  q: string;
}

function emptyFilters(): Filters {
  return {
    view: 'waiting',
    kind: '',
    repo: '',
    relation: '',
    bot: '',
    age: '',
    rule: '',
    q: '',
  };
}

// ---- URL search param helpers -----------------------------------------------

function getUrlQ(): string {
  return new URLSearchParams(location.search).get('q') ?? '';
}

function setUrlQ(q: string): void {
  const p = new URLSearchParams(location.search);
  if (q) {
    p.set('q', q);
  } else {
    p.delete('q');
  }
  const qs = p.toString();
  const newUrl = location.pathname + (qs ? '?' + qs : '') + location.hash;
  history.replaceState(null, '', newUrl);
}

function ageOf(it: ItemView): string {
  if (!it.created_at) return '';
  const ms = Date.now() - new Date(it.created_at).getTime();
  const days = Math.floor(ms / 86400000);
  if (days === 0) return 'today';
  if (days === 1) return '1d';
  return `${days}d`;
}

export function makeAttention(ctx: Ctx): Section {
  const readEl = h('div', { class: 'kit-read' });
  let offset = 0;
  let totalItems = 0;
  let loadedItems: ItemView[] = [];
  const filters = emptyFilters();
  let loading = false;
  let footEl: HTMLElement | null = null;
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;
  let totalItemsForView = 0; // updated after every load; used by select-all

  // ---- foot: show-more + selection count + select all in view ---------------

  function buildFoot(): HTMLElement {
    const selCount = h(
      'span',
      { class: 'cb-sel-count', hidden: true },
      '0 selected',
    );

    const selAllBtn = h(
      'button',
      {
        class: 'cb-sel-all',
        onclick() {
          void selectAllInView();
        },
      },
      'select all 0 in view',
    );

    footEl = h(
      'div',
      { class: 'cb-foot' },
      selCount,
      selAllBtn,
      h(
        'button',
        {
          class: 'cb-foot-more',
          hidden: true, // shown by updateFoot() when there are more items
          onclick() {
            void loadMore();
          },
        },
        `show ${PAGE_SIZE} more`,
      ),
    );
    return footEl;
  }

  // selectAllInView fetches every id in the current view and calls selectAll()
  // so the selection covers the whole view, even across pages.
  async function selectAllInView(): Promise<void> {
    let allIds: string[] = loadedItems.map((it) => it.key);
    if (totalItemsForView > loadedItems.length) {
      try {
        const data = await ctx.api.get<ItemsView>('/items', {
          ...buildQuery(0),
          limit: String(totalItemsForView),
        });
        allIds = (data.items ?? []).map((it) => it.key);
      } catch {
        // Non-fatal: select only what's loaded.
      }
    }
    handle.selectAll(allIds);
  }

  // ---- list -----------------------------------------------------------------

  const handle: ListHandle<ItemView> = list<ItemView>({
    label: 'attention',
    views: VIEWS.map((v) => ({ ...v, on: v.id === filters.view })),
    filters: FILTER_CHIPS,
    selection,
    openOnMove: false,
    search: {
      placeholder: 'search by key or title',
      onInput(text: string) {
        if (searchDebounceTimer !== null) clearTimeout(searchDebounceTimer);
        searchDebounceTimer = setTimeout(() => {
          searchDebounceTimer = null;
          filters.q = text;
          setUrlQ(text);
          void reload();
        }, 200);
      },
    },
    row(it: ItemView) {
      const kindKey = `${it.kind} · ${it.key}`;
      const age = ageOf(it);
      const proposal = it.proposal
        ? `${it.proposal.disposition} proposed`
        : undefined;
      return {
        id: it.key,
        key: kindKey,
        title: it.title ?? it.key,
        meta: age,
        sub: proposal,
        selectable: true,
      };
    },
    onChip(group, id) {
      if (group === 'view') {
        if (id === 'board') {
          // Board view is Task 4; navigate there.
          ctx.route.go('attention', 'board');
          return;
        }
        filters.view = id;
        ctx.route.go('attention', id);
        void reload();
      } else if (group === 'filter') {
        // Simple toggle cycle for known filter values.
        cycleFilter(id);
        void reload();
      }
    },
    onOpen(it: ItemView) {
      // Navigate to the item's hash URL and load the detail.
      ctx.route.go('item', it.key);
      void openDetail(it.key);
    },
    // onSelect: wireSelection handles the primary button via selection.onChange().
    foot: buildFoot(),
  });

  // ---- filter chip cycling --------------------------------------------------

  const filterCycles: Record<string, string[]> = {
    relation: ['', 'incoming', 'outgoing', 'own'],
    bot: ['', 'bot', 'human'],
  };

  function cycleFilter(id: string): void {
    const cycle = filterCycles[id];
    if (cycle) {
      const cur = filters[id] ?? '';
      const i = cycle.indexOf(cur);
      filters[id] = cycle[(i + 1) % cycle.length];
    }
    // kind/repo/age/rule: no cycle; the chip toggles clearing (Task 4 or later).
  }

  function updateFilterChips(): void {
    handle.setChips(
      'filter',
      FILTER_CHIPS.map((c) => ({
        ...c,
        on: Boolean((filters as Record<string, string>)[c.id]),
        label: (filters as Record<string, string>)[c.id]
          ? `${c.label}: ${(filters as Record<string, string>)[c.id]}`
          : c.label,
      })),
    );
  }

  // ---- API ------------------------------------------------------------------

  function buildQuery(pageOffset: number): Record<string, string> {
    const p: Record<string, string> = {
      view: filters.view,
      offset: String(pageOffset),
      limit: String(PAGE_SIZE),
    };
    if (filters.kind) p['kind'] = filters.kind;
    if (filters.repo) p['repo'] = filters.repo;
    if (filters.relation) p['relation'] = filters.relation;
    if (filters.bot) p['bot'] = filters.bot;
    if (filters.age) p['age'] = filters.age;
    if (filters.rule) p['rule'] = filters.rule;
    if (filters.q) p['q'] = filters.q;
    return p;
  }

  async function reload(): Promise<void> {
    if (loading) return;
    loading = true;
    try {
      offset = 0;
      loadedItems = [];
      updateFilterChips();
      const data = await ctx.api.get<ItemsView>('/items', buildQuery(0));
      totalItems = data.total;
      totalItemsForView = data.total;
      loadedItems = data.items ?? [];
      offset = loadedItems.length;
      handle.setItems(loadedItems);
      updateFoot();
    } catch {
      // non-fatal; leave the list as-is
    } finally {
      loading = false;
    }
  }

  async function loadMore(): Promise<void> {
    if (loading || offset >= totalItems) return;
    loading = true;
    try {
      const data = await ctx.api.get<ItemsView>('/items', buildQuery(offset));
      const next = data.items ?? [];
      loadedItems = [...loadedItems, ...next];
      offset = loadedItems.length;
      totalItems = data.total;
      totalItemsForView = data.total;
      handle.setItems(loadedItems);
      updateFoot();
    } catch {
      // non-fatal
    } finally {
      loading = false;
    }
  }

  function updateFoot(): void {
    if (!footEl) return;

    // Update the select-all button text with the current view total.
    const selAll = footEl.querySelector('.cb-sel-all');
    if (selAll instanceof HTMLElement) {
      selAll.textContent = `select all ${totalItemsForView} in view`;
    }

    // Show or hide only the show-more button; the foot itself stays visible
    // so the selection count and select-all remain accessible.
    const btn = footEl.querySelector<HTMLElement>('.cb-foot-more');
    if (btn) {
      const remaining = totalItems - offset;
      if (remaining > 0) {
        btn.textContent = `show ${Math.min(PAGE_SIZE, remaining)} more`;
        btn.hidden = false;
      } else {
        btn.hidden = true;
      }
    }

    // The foot div itself is always visible.
    footEl.hidden = false;
  }

  async function openDetail(key: string): Promise<void> {
    try {
      const detail = await ctx.api.get<ItemDetailView>('/item', { key });
      const el = renderItem(ctx, detail);
      readEl.replaceChildren(el);
    } catch {
      // non-fatal; leave the reading column
    }
  }

  // ---- section wiring -------------------------------------------------------

  // Update view chip counts from SummaryView.
  function applyCounts(counts: Record<string, number> | null): void {
    if (!counts) return;
    handle.setChips(
      'view',
      VIEWS.map((v) => ({
        ...v,
        on: v.id === filters.view,
        count: counts[v.id] ?? undefined,
      })),
    );
  }

  // Initial load: restore search text from URL if present.
  {
    const urlQ = getUrlQ();
    if (urlQ) {
      filters.q = urlQ;
      handle.setSearch(urlQ);
    }
  }
  void reload();

  // Wire selection → primary button and foot count (Task 4).
  wireSelection(ctx, handle);

  // Register / to focus the search field (kit v0.11.0).
  // createKeys() in app.ts is called before sections are created, so we
  // register this binding here rather than via the list option.
  if (typeof handle.focusSearch === 'function') {
    const focusFn = handle.focusSearch.bind(handle);
    try {
      ctx.keys.register({
        keys: '/',
        label: 'search',
        group: 'family',
        run() {
          focusFn();
        },
      });
    } catch {
      // Already registered (e.g. createKeys was given this list).
    }
  }

  // Load summary for initial counts.
  void ctx.api
    .get<SummaryView>('/summary')
    .then((s) => {
      applyCounts(s.counts);
    })
    .catch(() => {});

  // ---- show(sub) -- router hook --------------------------------------------

  function show(sub: string): void {
    if (sub === 'board') {
      // Board is Task 4.
      return;
    }
    // Restore search text from URL (?q=...) on every navigation into this section.
    const urlQ = getUrlQ();
    if (urlQ !== filters.q) {
      filters.q = urlQ;
      handle.setSearch(urlQ);
    }
    const view = sub || 'waiting';
    if (VIEWS.some((v) => v.id === view) && view !== 'board') {
      // Known view chip: switch to it.
      filters.view = view;
      handle.setChips(
        'view',
        VIEWS.map((v) => ({
          ...v,
          on: v.id === filters.view,
        })),
      );
      void reload();
    } else if (sub) {
      // Not a view id: treat as an item key from a direct #/item/<key> link.
      // Open the item's detail and reload the list with the current view.
      void openDetail(sub);
      void reload();
    }
  }

  return {
    id: 'attention',
    list: handle.el,
    read: readEl,
    show,
    onLive(type: string, data: unknown) {
      if (type === 'index') {
        // One reload per index event: apply counts from the event payload and
        // refresh the list.  Previously ctx.on('index') + onLive both called
        // reload(); folding them here means a single index event ⇒ one reload.
        const s = data as { counts?: Record<string, number> };
        applyCounts(s?.counts ?? null);
        void reload();
      } else if (type === 'decided' || type === 'proposals') {
        void reload();
      }
    },
    primary() {
      return null;
    },
  };
}
