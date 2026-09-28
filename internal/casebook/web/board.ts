// board.ts — the Attention board view: four lanes sharing the same selection
// as the flat list.
//
// makeBoard(ctx, sel, filters) creates a `.cb-board` element that replaces
// the kit list's rows area while the board view is active.  It returns an
// object with:
//
//   el          — the `.cb-board` HTMLElement; the caller appends it to the
//                 kit list panel (handle.el) and removes/restores the
//                 `.kit-rows` and `.kit-foot` elements around it.
//   refresh()   — re-fetches all four lanes and repaints.
//   destroy()   — unsubscribes from the selection store; call before removing.
//
// Card clicks call sel.toggle(id); shift-clicks call
// sel.range(sel.anchor(), id, laneOrderIds) so ranges stay within the lane.
//
// Each item appears in at most one lane, in precedence order:
//   waiting → proposed → due → new
// (the server-side view categorisation is the source of truth, but we
// deduplicate client-side in precedence order to be safe).
//
// At most PAGE_SIZE cards per lane, with a "show more" button at the foot of
// each lane.
//
// Untrusted text (titles, keys) is set via textContent, never innerHTML.

import { h, type Selection } from '/_kit/kit.js';
import type { ItemsView, ItemView } from './wire.d.ts';
import type { Ctx } from './app.ts';

// PAGE_SIZE matches the list view's limit.
const PAGE_SIZE = 200;

// LANES defines the display order and the API view= values.
// Precedence is the array order: waiting > proposed > due > new.
const LANES = [
  { id: 'waiting', label: 'waiting on you' },
  { id: 'proposed', label: 'proposed' },
  { id: 'due', label: 'due' },
  { id: 'new', label: 'new' },
] as const;

type LaneId = (typeof LANES)[number]['id'];

// ---- helpers ----------------------------------------------------------------

function ageOf(it: ItemView): string {
  if (!it.created_at) return '';
  const ms = Date.now() - new Date(it.created_at).getTime();
  const days = Math.floor(ms / 86400000);
  if (days === 0) return 'today';
  if (days === 1) return '1d';
  return `${days}d`;
}

// ---- makeBoard --------------------------------------------------------------

export function makeBoard(
  ctx: Ctx,
  sel: Selection,
  filters: () => Record<string, string>,
  onOpen?: (key: string, laneId: string) => void,
  onRefresh?: (totalUniqueItems: number) => void,
): {
  el: HTMLElement;
  refresh(): Promise<void>;
  destroy(): void;
  totalItems(): number;
  allKeys(): string[];
} {
  // The board element fills the space left by hiding .kit-rows inside the kit
  // list panel.  `.cb-board` has `flex: 1; overflow-x: auto` from casebook.css
  // so it fills the column and lets users scroll sideways across four lanes.
  const el = h('div', { class: 'cb-board' });

  // Per-lane state: loaded items and the API total (for "show more").
  type LaneState = { items: ItemView[]; total: number };
  const laneState = new Map<LaneId, LaneState>(
    LANES.map(({ id }) => [id, { items: [], total: 0 }]),
  );

  // DOM references to each lane's rows container and "show more" button.
  const laneRowsEl = new Map<LaneId, HTMLElement>();
  const laneMoreEl = new Map<LaneId, HTMLElement>();

  // ---- build lane DOM -------------------------------------------------------

  for (const { id, label } of LANES) {
    const headEl = h('div', { class: 'cb-lane-head' });
    headEl.textContent = label;

    const rowsEl = h('div', { class: 'cb-lane-rows' });
    laneRowsEl.set(id, rowsEl);

    const moreEl = h('button', { class: 'cb-lane-more', hidden: true });
    moreEl.textContent = 'show more';
    moreEl.addEventListener('click', () => {
      void loadMore(id);
    });
    laneMoreEl.set(id, moreEl);

    el.append(
      h('div', { class: 'cb-lane', 'data-lane': id }, headEl, rowsEl, moreEl),
    );
  }

  // ---- card rendering -------------------------------------------------------

  function buildCard(it: ItemView, laneId: LaneId): HTMLElement {
    const selected = sel.has(it.key);

    // Selection box — same visual pattern as the list's .kit-box.
    const box = h('span', { class: 'kit-box' + (selected ? ' on' : '') });

    // Kind + key in monospace (untrusted, set via textContent).
    const kk = h('div', { class: 'cb-card-kk' });
    kk.textContent = `${it.kind} · ${it.key}`;

    // Title (untrusted, set via textContent).
    const titleEl = h('div', { class: 'cb-card-title' });
    titleEl.textContent = it.title ?? it.key;

    const card = h(
      'div',
      {
        class: 'cb-board-card' + (selected ? ' on' : ''),
        tabindex: '0',
        'data-id': it.key,
      },
      h('div', { class: 'cb-card-head' }, box, kk),
      titleEl,
    );

    const age = ageOf(it);
    if (age) {
      const ageEl = h('div', { class: 'cb-card-age' });
      ageEl.textContent = age;
      card.append(ageEl);
    }

    if (it.proposal) {
      // Proposal disposition shown in agent colour (via `.cb-card-prop`).
      const propEl = h('div', { class: 'cb-card-prop' });
      propEl.textContent = `${it.proposal.disposition} proposed`;
      card.append(propEl);
    }

    // Title click: open the item in the reading column.  Stop propagation
    // so the card's selection toggle does not also fire.
    titleEl.addEventListener('click', (e: MouseEvent) => {
      e.stopPropagation();
      onOpen?.(it.key, laneId);
      ctx.route.go('item', it.key);
    });

    // Click: toggle; shift-click: range within lane.
    card.addEventListener('click', (e: MouseEvent) => {
      const state = laneState.get(laneId);
      const orderedIds = state?.items.map((i) => i.key) ?? [];
      if (e.shiftKey) {
        const anchor = sel.anchor();
        if (anchor !== null) {
          sel.range(anchor, it.key, orderedIds);
        } else {
          sel.toggle(it.key);
        }
      } else {
        sel.toggle(it.key);
      }
    });

    // Keyboard: o or Enter on a focused card opens the item.
    card.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.key === 'o' || e.key === 'Enter') {
        e.preventDefault();
        onOpen?.(it.key, laneId);
        ctx.route.go('item', it.key);
      }
    });

    return card;
  }

  // ---- repaint --------------------------------------------------------------

  // Repaint a single lane's cards from laneState.
  function repaintLane(laneId: LaneId): void {
    const rowsEl = laneRowsEl.get(laneId);
    const moreEl = laneMoreEl.get(laneId);
    if (!rowsEl || !moreEl) return;

    const state = laneState.get(laneId)!;
    rowsEl.replaceChildren(...state.items.map((it) => buildCard(it, laneId)));

    const remaining = state.total - state.items.length;
    if (remaining > 0) {
      moreEl.textContent = `show ${Math.min(PAGE_SIZE, remaining)} more`;
      moreEl.hidden = false;
    } else {
      moreEl.hidden = true;
    }
  }

  // Update the selected state on all cards without rebuilding them.
  function repaintSelection(): void {
    for (const { id: laneId } of LANES) {
      const rowsEl = laneRowsEl.get(laneId);
      if (!rowsEl) continue;
      const state = laneState.get(laneId)!;
      const cards = rowsEl.querySelectorAll<HTMLElement>('.cb-board-card');
      cards.forEach((card, i) => {
        const it = state.items[i];
        if (!it) return;
        const selected = sel.has(it.key);
        card.classList.toggle('on', selected);
        card.querySelector('.kit-box')?.classList.toggle('on', selected);
      });
    }
  }

  // ---- data fetching --------------------------------------------------------

  async function fetchLane(
    laneId: LaneId,
  ): Promise<{ items: ItemView[]; total: number }> {
    const q: Record<string, string> = {
      ...filters(),
      view: laneId,
      offset: '0',
      limit: String(PAGE_SIZE),
    };
    const data = await ctx.api.get<ItemsView>('/items', q);
    return { items: data.items ?? [], total: data.total };
  }

  // Total unique items across all lanes (updated after each refresh).
  let boardTotalUnique = 0;

  // Re-fetch all four lanes in parallel; deduplicate by key in precedence order.
  async function refresh(): Promise<void> {
    const results = await Promise.allSettled(
      LANES.map(({ id }) => fetchLane(id)),
    );

    const seenIds = new Set<string>();
    for (let i = 0; i < LANES.length; i++) {
      const { id } = LANES[i];
      const result = results[i];
      if (result.status === 'fulfilled') {
        const { items, total } = result.value;
        // Keep only items not already shown in a higher-precedence lane.
        const unique = items.filter((it) => !seenIds.has(it.key));
        unique.forEach((it) => seenIds.add(it.key));
        // Adjust total so that "show N more" reflects only the items this lane
        // will actually show after de-duplication.
        //   - If the API returned every item for this lane on the first page
        //     (items.length >= total), we know the full set and the displayed
        //     count is exactly unique.length — no more pages to show.
        //   - If there are more pages (items.length < total), we keep the raw
        //     total because additional pages may contain keys not yet in other
        //     lanes.
        const adjustedTotal = items.length >= total ? unique.length : total;
        laneState.set(id, { items: unique, total: adjustedTotal });
      }
      repaintLane(id);
    }

    // Count total unique items across all lanes for the board-mode select-all.
    let uniqueCount = 0;
    for (const { id } of LANES) {
      uniqueCount += laneState.get(id)?.items.length ?? 0;
    }
    boardTotalUnique = uniqueCount;
    onRefresh?.(boardTotalUnique);
  }

  // Load the next page of a lane (called from the "show more" button).
  async function loadMore(laneId: LaneId): Promise<void> {
    const state = laneState.get(laneId)!;
    if (state.items.length >= state.total) return;

    const q: Record<string, string> = {
      ...filters(),
      view: laneId,
      offset: String(state.items.length),
      limit: String(PAGE_SIZE),
    };
    try {
      const data = await ctx.api.get<ItemsView>('/items', q);
      // Exclude keys already in other lanes.
      const existingKeys = new Set<string>(
        LANES.flatMap(({ id }) =>
          id === laneId
            ? []
            : (laneState.get(id)?.items.map((it) => it.key) ?? []),
        ),
      );
      const more = (data.items ?? []).filter((it) => !existingKeys.has(it.key));
      laneState.set(laneId, {
        items: [...state.items, ...more],
        total: data.total,
      });
      repaintLane(laneId);
    } catch {
      // non-fatal; leave the lane as-is
    }
  }

  // ---- selection subscription -----------------------------------------------

  // Repaint selection state whenever the shared store changes.
  const unsubSel = sel.onChange(() => {
    repaintSelection();
  });

  // Initial load.
  void refresh();

  // ---- public API -----------------------------------------------------------

  function destroy(): void {
    unsubSel();
  }

  // Total unique items across all lanes (de-duplicated), updated after each
  // refresh().  Used by the attention foot's "select all N in view" button.
  function totalItems(): number {
    return boardTotalUnique;
  }

  // All unique keys across all lanes in precedence order.  Used by the
  // "select all" action when the board view is active.
  function allKeys(): string[] {
    return LANES.flatMap(
      ({ id }) => laneState.get(id)?.items.map((it) => it.key) ?? [],
    );
  }

  return { el, refresh, destroy, totalItems, allKeys };
}
