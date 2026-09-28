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
import { keyWithoutKind } from './decide-math.ts';
import { makeBoard } from './board.ts';

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
  const viewCounts: Record<string, number> = {}; // last known counts per view chip id
  let boardHandle: ReturnType<typeof makeBoard> | null = null;

  // ---- empty reading-column state ------------------------------------------

  // Render the empty state for the reading column (no item open).
  // Shows the section name, current view count, and a prompt.
  function renderReadEmpty(): HTMLElement {
    const nameEl = h('p', { class: 'cb-read-empty-section' }, 'attention');
    const countEl = h(
      'p',
      { class: 'cb-read-empty-count' },
      `${totalItemsForView} items`,
    );
    const promptEl = h(
      'p',
      { class: 'cb-read-empty-prompt' },
      'Select an item to see it here.',
    );
    return h('div', { class: 'cb-read-empty' }, nameEl, countEl, promptEl);
  }

  // Update the count shown in the reading-column empty state (if it is showing).
  function updateReadEmptyCount(): void {
    const countEl = readEl.querySelector<HTMLElement>('.cb-read-empty-count');
    if (countEl) {
      countEl.textContent = `${totalItemsForView} items`;
    }
  }

  // Show the empty reading-column state (replaces any open item detail).
  function showReadEmpty(): void {
    readEl.replaceChildren(renderReadEmpty());
  }

  // Initialise the reading column with the empty state so it is never blank.
  showReadEmpty();

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
    // Board mode: select all unique items across all four lanes.
    if (boardHandle) {
      handle.selectAll(boardHandle.allKeys());
      return;
    }
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
      // Display the key without its kind prefix — the kind is already shown in the kicker.
      const displayKey = it.kind ? keyWithoutKind(it.key) : it.key;
      const kindKey = it.kind ? `${it.kind} · ${displayKey}` : displayKey;
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
      updateReadEmptyCount();
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

  // Helper to build the view chip array with the latest counts.
  function viewChips(activeId: string): Chip[] {
    return VIEWS.map((v) => ({
      ...v,
      on: v.id === activeId,
      count: viewCounts[v.id],
    }));
  }

  // Update view chip counts from SummaryView (or from the index live event).
  // Stores counts so every subsequent setChips call can include them.
  function applyCounts(counts: Record<string, number> | null): void {
    if (!counts) return;
    // Merge into local store; only update keys that are present in the payload.
    Object.assign(viewCounts, counts);
    handle.setChips('view', viewChips(filters.view));
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

  // ---- board helpers -------------------------------------------------------

  // getBoardFilters returns the active filters (excluding `view`) for the board.
  // The board overrides `view` per lane; all other filters still apply.
  function getBoardFilters(): Record<string, string> {
    const p: Record<string, string> = {};
    if (filters.kind) p['kind'] = filters.kind;
    if (filters.repo) p['repo'] = filters.repo;
    if (filters.relation) p['relation'] = filters.relation;
    if (filters.bot) p['bot'] = filters.bot;
    if (filters.age) p['age'] = filters.age;
    if (filters.rule) p['rule'] = filters.rule;
    if (filters.q) p['q'] = filters.q;
    return p;
  }

  // mountBoard hides the kit's rows/foot and inserts the board into the list
  // panel.  The kit's `.kit-rows` and `.kit-foot` are internal class names
  // stable in kit v0.11.0 (see localweb/page/assets/kit.css).
  //
  // It also adds `.cb-board` to the `.kit-app` grid element so the CSS can
  // collapse the reading column and let the list panel span the full width.
  function mountBoard(): void {
    if (boardHandle) return;
    // Span the list+reading columns by adding a class to the app grid element.
    // Note: use 'cb-board-active', NOT 'cb-board' — the latter is the board
    // container element's own class, and matching it on .kit-app would cause
    // '.cb-board { display: flex }' to convert the grid to flexbox.
    handle.el.closest('.kit-app')?.classList.add('cb-board-active');
    const kitRows = handle.el.querySelector<HTMLElement>('.kit-rows');
    const kitFoot = handle.el.querySelector<HTMLElement>('.kit-foot');
    if (kitRows) kitRows.hidden = true;
    if (kitFoot) kitFoot.hidden = true;
    boardHandle = makeBoard(
      ctx,
      selection,
      getBoardFilters,
      (key, laneId) => {
        // Record the card's lane as the active view so that when the item is
        // opened the list reloads with that view (brief §4, item 2).
        filters.view = laneId;
      },
      (total) => {
        // After each board refresh, update the foot's "select all N in view"
        // button so it reflects the de-duplicated count across all lanes.
        if (!footEl) return;
        const selAll = footEl.querySelector('.cb-sel-all');
        if (selAll instanceof HTMLElement) {
          selAll.textContent = `select all ${total} in view`;
        }
      },
    );
    handle.el.append(boardHandle.el);
  }

  // unmountBoard removes the board, restores the kit's rows/foot, and removes
  // the app-level `.cb-board` class so the grid returns to normal layout.
  function unmountBoard(): void {
    if (!boardHandle) return;
    // Restore normal list+reading grid layout.
    handle.el.closest('.kit-app')?.classList.remove('cb-board-active');
    boardHandle.destroy();
    boardHandle.el.remove();
    boardHandle = null;
    const kitRows = handle.el.querySelector<HTMLElement>('.kit-rows');
    const kitFoot = handle.el.querySelector<HTMLElement>('.kit-foot');
    if (kitRows) kitRows.hidden = false;
    if (kitFoot) kitFoot.hidden = false;
  }

  // ---- show(sub) -- router hook --------------------------------------------

  function show(sub: string): void {
    if (sub === 'board') {
      // Restore search text from URL so getBoardFilters() picks it up.
      const urlQ = getUrlQ();
      if (urlQ !== filters.q) {
        filters.q = urlQ;
        handle.setSearch(urlQ);
      }
      // Activate the board view chip so it is highlighted, not the last list
      // view chip (brief fix-round item 3).
      filters.view = 'board';
      handle.setChips('view', viewChips('board'));
      mountBoard();
      return;
    }

    // Leaving board view: unmount board and restore the kit list.
    unmountBoard();

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
      handle.setChips('view', viewChips(filters.view));
      showReadEmpty();
      void reload();
    } else if (sub) {
      // Not a view id: treat as an item key from a direct #/item/<key> link
      // (e.g. opened from a board card's title).  The onOpen callback in
      // makeBoard already set filters.view to the card's lane, so reload()
      // will use that view.  Update the chips to reflect it.
      handle.setChips('view', viewChips(filters.view));
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
        // refresh the list (or the board when it is active).
        const s = data as { counts?: Record<string, number> };
        applyCounts(s?.counts ?? null);
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      } else if (type === 'decided' || type === 'proposals') {
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      }
    },
    primary() {
      return null;
    },
  };
}
