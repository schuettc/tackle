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
  type KeyBinding,
  type Primary,
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
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { makeBoard } from './board.ts';
import {
  agentFromSource,
  bulkProposalActions,
  openRejectSheet,
  type BulkProposalActions,
} from './proposals.ts';

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
  let propFoot: BulkProposalActions | null = null;
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;
  let totalItemsForView = 0; // updated after every load; used by select-all
  // currentOpenKey is the key of the item currently shown in the reading column;
  // used by the 'a' and 'r' key bindings.
  let currentOpenKey: string | null = null;
  const viewCounts: Record<string, number> = {}; // last known counts per view chip id
  let boardHandle: ReturnType<typeof makeBoard> | null = null;

  // ---- empty reading-column state ------------------------------------------

  // Render the empty state for the reading column (no item open).
  // Shows the section name, current view count, and a prompt.
  function renderReadEmpty(): HTMLElement {
    // Use .kit-label for the eyebrow (uppercase mono label, matches the
    // reading-column section headers in item.ts and the kit's own eyebrow
    // style).
    const nameEl = h(
      'p',
      { class: 'cb-read-empty-section kit-label' },
      'attention',
    );
    const countEl = h(
      'p',
      { class: 'cb-read-empty-count' },
      pluralize(totalItemsForView, 'item'),
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
      countEl.textContent = pluralize(totalItemsForView, 'item');
    }
  }

  // feedAttached tells the composer what goes with the next message: the
  // selection's keys, or the open item when nothing is selected.
  //
  // Only while Attention is the active section: a live decided/proposals
  // event can change the selection while another section is shown, and that
  // section's context (a rule, a job) must not be overwritten.
  let active = false;
  function feedAttached(): void {
    if (!active) return;
    const ids = selection.ids();
    if (ids.length > 0) ctx.setAttached({ keys: ids });
    else if (currentOpenKey) ctx.setAttached({ open: currentOpenKey });
    else ctx.setAttached({});
  }

  // Show the empty reading-column state (replaces any open item detail).
  function showReadEmpty(): void {
    currentOpenKey = null;
    readEl.replaceChildren(renderReadEmpty());
    feedAttached();
  }

  // Initialise the reading column with the empty state so it is never blank.
  showReadEmpty();

  // ---- foot: show-more + selection count + select all in view ---------------

  function buildFoot(): HTMLElement {
    // Left side: selection count (shown when N>0) and select-all button
    // (hidden once every item in view is already selected).
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

    // Right side: bulk proposal buttons (proposed view only) and show-more.
    // .cb-prop-bulk and .cb-foot-more are pushed right via margin-left:auto
    // on .cb-foot-right (see casebook.css).
    // Bulk proposal buttons (only visible in the 'proposed' view).
    propFoot = bulkProposalActions(ctx, (keys: string[]) => {
      selection.deselect(keys);
      void reload();
    });

    const footRight = h(
      'div',
      { class: 'cb-foot-right' },
      propFoot.el,
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

    footEl = h('div', { class: 'cb-foot' }, selCount, selAllBtn, footRight);
    return footEl;
  }

  /**
   * updateProposalBulk recalculates which selected items have pending proposals
   * and updates the bulk accept/reject button labels and visibility.
   * Only shown when filters.view === 'proposed'.
   */
  function updateProposalBulk(selectedIds: string[]): void {
    if (!propFoot) return;
    if (filters.view !== 'proposed') {
      propFoot.update([], []);
      return;
    }
    const withProps = loadedItems.filter(
      (it) =>
        it.proposal &&
        it.proposal.state === 'pending' &&
        selectedIds.includes(it.key),
    );
    propFoot.update(
      withProps.map((it) => it.proposal!.id),
      withProps.map((it) => it.key),
    );
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
        ? `${agentFromSource(it.proposal.source)} proposes ${it.proposal.disposition}`
        : undefined;
      return {
        id: it.key,
        key: kindKey,
        // Fallback: use display key (without kind prefix) so titleless items
        // like branch:schuettc/hail@feat/client show "schuettc/hail@feat/client".
        title: it.title ?? keyWithoutKind(it.key),
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
      updateProposalBulk(selection.ids());
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
      updateProposalBulk(selection.ids());
    } catch {
      // non-fatal
    } finally {
      loading = false;
    }
  }

  function updateFoot(): void {
    if (!footEl) return;

    // Update the select-all button text and hide it once all items are selected.
    const selAll = footEl.querySelector<HTMLElement>('.cb-sel-all');
    if (selAll instanceof HTMLElement) {
      selAll.textContent = `select all ${totalItemsForView} in view`;
      const allSelected =
        totalItemsForView > 0 && selection.ids().length >= totalItemsForView;
      selAll.hidden = allSelected;
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
    currentOpenKey = key;
    feedAttached();
    try {
      const detail = await ctx.api.get<ItemDetailView>('/item', { key });
      const el = renderItem(ctx, detail, () => {
        // Re-render after accept/reject/change from the proposal card.
        void openDetail(key);
      });
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
  // Resets every known view to 0 first so views that drop to zero items do
  // not retain a stale count. The server omits zero-count views from event
  // payloads, so Object.assign alone would leave stale counts in place.
  function applyCounts(counts: Record<string, number> | null): void {
    if (!counts) return;
    // Zero all API-backed views before merging ("board" is UI-only — no count).
    for (const v of VIEWS) {
      if (v.id !== 'board') viewCounts[v.id] = 0;
    }
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

  // Wire selection → primary button and foot count (Task 4). Its "d" key is
  // one of this section's keys (Section.keys), live only while it is shown.
  // The bar's primary is the active section's: wireSelection's "Decide N" is
  // kept here and reaches the bar only while Attention is shown (app.ts asks
  // primary() on show).
  let decidePrimary: Primary | null = null;
  const decideKey = wireSelection(
    {
      ...ctx,
      setPrimary(p) {
        decidePrimary = p;
        if (active) ctx.setPrimary(p);
      },
    },
    handle,
  );

  // Also update proposal bulk buttons and select-all visibility when selection changes.
  selection.onChange((ids) => {
    updateProposalBulk(ids);
    // Hide "select all N in view" once every item in view is already selected.
    if (footEl) {
      const selAll = footEl.querySelector<HTMLElement>('.cb-sel-all');
      if (selAll) {
        const allSelected =
          totalItemsForView > 0 && ids.length >= totalItemsForView;
        selAll.hidden = allSelected;
      }
    }
    feedAttached();
  });

  // 'a' and 'r' accept/reject the open item's proposal. Like "d", they are
  // Section.keys: app.ts registers them only while Attention is shown, so on
  // #/rules they cannot reach the open item Attention keeps while hidden, and
  // Rules and Apply are free to bind their own a/r.
  const acceptKey: KeyBinding = {
    keys: 'a',
    label: 'accept proposal',
    group: 'page',
    run() {
      if (!currentOpenKey) return;
      const it = loadedItems.find((x) => x.key === currentOpenKey);
      if (!it?.proposal || it.proposal.state !== 'pending') return;
      void ctx.api
        .post('/proposals/accept', { ids: [it.proposal.id] })
        .then(() => {
          selection.deselect([currentOpenKey!]);
          void openDetail(currentOpenKey!);
          void reload();
        })
        .catch(() => {});
    },
  };

  const rejectKey: KeyBinding = {
    keys: 'r',
    label: 'reject proposal',
    group: 'page',
    run() {
      if (!currentOpenKey) return;
      const it = loadedItems.find((x) => x.key === currentOpenKey);
      if (!it?.proposal || it.proposal.state !== 'pending') return;
      openRejectSheet(ctx, [it.proposal.id], () => {
        selection.deselect([currentOpenKey!]);
        void openDetail(currentOpenKey!);
        void reload();
      });
    },
  };

  // "/" focuses the search field (kit v0.11.0). It is one of this section's
  // keys (Section.keys), bound only while Attention is shown: on #/rules the
  // field is hidden, and Rules may bind "/" for itself.
  const searchKey: KeyBinding = {
    keys: '/',
    label: 'search',
    group: 'page',
    run() {
      handle.focusSearch?.();
    },
  };

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
    // Becoming active: the composer shows Attention's selection or open item.
    active = true;
    feedAttached();
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
      // Reset proposal bulk foot when leaving/entering proposed view.
      updateProposalBulk(selection.ids());
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
    hide() {
      active = false;
    },
    keys: [decideKey, acceptKey, rejectKey, searchKey],
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
      } else if (type === 'decided') {
        // A live decided event can originate from the CLI or another client.
        // Deselect the decided keys so the bar's "Decide N" doesn't include
        // items that are already gone (the server's payload shape is
        // {keys: string[], disposition, by, proposed_by}).
        const decidedPayload = data as { keys?: string[] };
        const decidedKeys = decidedPayload?.keys ?? [];
        if (decidedKeys.length > 0) {
          selection.deselect(decidedKeys);
        }
        // Re-fetch summary so view-chip counts stay correct.
        void ctx.api
          .get<SummaryView>('/summary')
          .then((s) => applyCounts(s.counts))
          .catch(() => {});
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      } else if (type === 'proposals') {
        // proposals event is emitted by accept/reject/change or by the agent.
        // Payload shape: {ids: number[], state: string, source?: string}.
        const propPayload = data as {
          ids?: number[];
          state?: string;
          source?: string;
        };
        const propIds = propPayload?.ids ?? [];
        const propState = propPayload?.state ?? '';

        // When proposals are settled (accepted, rejected, changed), deselect
        // the affected item keys so the selection count stays correct.
        if (
          propState === 'accepted' ||
          propState === 'rejected' ||
          propState === 'changed'
        ) {
          const propIdSet = new Set(propIds);
          const affectedKeys = loadedItems
            .filter((it) => it.proposal && propIdSet.has(it.proposal.id))
            .map((it) => it.key);
          if (affectedKeys.length > 0) {
            selection.deselect(affectedKeys);
          }
          // Re-render the open item if it was affected.
          if (currentOpenKey && affectedKeys.includes(currentOpenKey)) {
            void openDetail(currentOpenKey);
          }
        }

        // Re-fetch summary to update view-chip counts (particularly 'proposed').
        void ctx.api
          .get<SummaryView>('/summary')
          .then((s) => applyCounts(s.counts))
          .catch(() => {});
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      }
    },
    primary() {
      return decidePrimary;
    },
  };
}
