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
  Decision,
  DecideResult,
  AcceptResult,
  Committed,
  DecisionUndoResult,
  DecisionVocabView,
} from './wire.d.ts';
import type { Ctx, Section } from './app.ts';
import { renderItem } from './item.ts';
import { wireSelection, getVocab } from './decide.ts';
import {
  keyWithoutKind,
  pluralize,
  nextInView,
  MORE,
  recommendLine,
  recommendedLine,
  agreeGroups,
  leftOpenMeta,
  leftOpenLabel,
  type AgreeGroup,
} from './decide-math.ts';
import { showChoiceError, askClosingComment } from './choices.ts';
import { makeBoard } from './board.ts';
import {
  agreeWithAll,
  bulkProposalActions,
  openRejectSheet,
  type BulkProposalActions,
} from './proposals.ts';

// RECOMMEND_THE_REST is what "ask … to recommend the rest" sends the dock's
// session: a normal message, delivered when its turn ends.
export const RECOMMEND_THE_REST =
  'Please recommend the items casebook still needs a recommendation for: call casebook_next until it says done.';

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

// isLeftOpen: a kept item still open, in a view's left-open group.
function isLeftOpen(it: ItemView): boolean {
  return it.status === 'left-open';
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
  // The view's left-open group (kept items still open: they need no
  // decision, so the view's count and move-on leave them out). Its rows
  // follow the items that need a decision, once every page of those is
  // loaded; shown is what the list renders.
  let leftOpen: ItemView[] = [];
  let leftOpenTotal = 0;
  let shown: ItemView[] = [];
  // currentOpenKey is the key of the item currently shown in the reading column;
  // used by the 'a' and 'r' key bindings.
  let currentOpenKey: string | null = null;
  // The detail the reading column shows (its item is currentOpenKey).
  let shownDetail: ItemDetailView | null = null;
  const viewCounts: Record<string, number> = {}; // last known counts per view chip id
  // lastOrder is the view's keys as the list last loaded them: the order the
  // next undecided item is found in after a decision (reload() empties
  // loadedItems while it waits, so a decision mid-reload reads this).
  let lastOrder: string[] = [];
  // deciding is the key this tab is deciding now (one at a time).
  let deciding: string | null = null;
  // lastUndo is the last decision made in this tab, for u: the item, the
  // decision it had before (null: none) and the one this tab's decide or
  // accept committed (serve's reply), which undo expects to find.
  let lastUndo: {
    key: string;
    prev: Decision | null;
    expect: Committed;
  } | null = null;
  let boardHandle: ReturnType<typeof makeBoard> | null = null;
  // serve's decision vocabulary (the rows' "pi recommends Close it" and the
  // agree-with-all labels); null until it loads.
  let vocab: DecisionVocabView | null = null;
  // The summary's counts of the items that need a decision: recommended
  // and not yet (null until the summary loads).
  let recCounts: { rec: number; notYet: number } | null = null;
  // syncingOpen: the list's open row is being moved to the open item (no
  // onOpen for it).
  let syncingOpen = false;

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
    shownDetail = null;
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
          // The board searches its lanes as the list searches its rows.
          if (boardHandle) void boardHandle.refresh();
          else void reload();
        }, 200);
      },
    },
    row(it: ItemView) {
      // Display the key without its kind prefix — the kind is already shown in the kicker.
      const displayKey = it.kind ? keyWithoutKind(it.key) : it.key;
      const kindKey = it.kind ? `${it.kind} · ${displayKey}` : displayKey;
      const left = isLeftOpen(it);
      const age = left ? leftOpenMeta(it.decision?.decided_at) : ageOf(it);
      const proposal =
        !left && it.proposal && it.proposal.state === 'pending'
          ? recommendLine(
              vocab,
              it.key,
              it.proposal.source,
              it.proposal.disposition,
            )
          : undefined;
      return {
        id: it.key,
        key: kindKey,
        // Fallback: use display key (without kind prefix) so titleless items
        // like branch:schuettc/hail@feat/client show "schuettc/hail@feat/client".
        title: it.title ?? keyWithoutKind(it.key),
        meta: age,
        // An item new activity brought back says so ("new activity since
        // you left it open"), before any recommendation.
        sub:
          [it.new_activity ? it.due_reason : undefined, proposal]
            .filter(Boolean)
            .join(' \u00b7 ') || undefined,
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
      if (syncingOpen) return;
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
      leftOpen = data.left_open ?? [];
      leftOpenTotal = data.left_open_total ?? 0;
      lastOrder = loadedItems.map((it) => it.key);
      offset = loadedItems.length;
      setRows();
      markOpenRow();
      updateAgree();
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
      leftOpen = data.left_open ?? [];
      leftOpenTotal = data.left_open_total ?? 0;
      lastOrder = loadedItems.map((it) => it.key);
      offset = loadedItems.length;
      totalItems = data.total;
      totalItemsForView = data.total;
      setRows();
      markOpenRow();
      updateAgree();
      updateFoot();
      updateProposalBulk(selection.ids());
    } catch {
      // non-fatal
    } finally {
      loading = false;
    }
  }

  // setRows renders the items that need a decision and, once every page of
  // them is loaded, the left-open group after them: a quiet divider
  // ("left open · N", the group's own count) and muted rows.
  function setRows(): void {
    const all = offset >= totalItems;
    const group = all ? leftOpen : [];
    shown = [...loadedItems, ...group];
    handle.setItems(shown);
    const rowEls = handle.el.querySelectorAll<HTMLElement>(
      '.kit-rows > .kit-row',
    );
    loadedItems.forEach((it, i) => {
      if (it.new_activity) rowEls[i]?.classList.add('cb-new-activity');
    });
    if (!group.length) return;
    for (let i = loadedItems.length; i < shown.length; i++) {
      rowEls[i]?.classList.add('cb-left-open');
    }
    rowEls[loadedItems.length]?.before(
      h(
        'div',
        { class: 'cb-left-open-head', role: 'presentation' },
        leftOpenLabel(leftOpenTotal),
      ),
    );
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

  // markOpenRow moves the list's open row to the item the reading column
  // shows (the next item move-on opened goes through the route, not a click
  // on its row). Nothing when the row already is the open one, so a reload
  // leaves the cursor where it was.
  function markOpenRow(): void {
    if (!currentOpenKey || boardHandle) return;
    const i = shown.findIndex((it) => it.key === currentOpenKey);
    if (i < 0) return;
    const rowEl = handle.el.querySelectorAll('.kit-rows > .kit-row')[i];
    if (rowEl?.classList.contains('open')) return;
    syncingOpen = true;
    try {
      handle.open(i);
    } finally {
      syncingOpen = false;
    }
  }

  // ---- recommendations: the line at the top, agree with all at the foot ---

  // The line at the top of the list: "n recommended · m not yet", and while
  // some aren't and the dock has a session here, the offer to ask it.
  const recCountsEl = h('span', { class: 'cb-rec-counts' });
  const askBtn = h('button', {
    class: 'kit-btn cb-rec-ask',
    type: 'button',
    hidden: true,
    onclick() {
      void askTheRest();
    },
  }) as HTMLButtonElement;
  const askNote = h('span', { class: 'cb-rec-note' });
  const recLine = h(
    'div',
    { class: 'cb-rec-line', hidden: true },
    recCountsEl,
    askBtn,
    askNote,
  );
  handle.el.querySelector('.kit-lh')?.append(recLine);
  // asked is the session this page asked to recommend the rest: the offer
  // gives way to "asked …" for it, so one click sends one message.
  let asked = '';

  function renderRecLine(): void {
    if (!recCounts) {
      recLine.hidden = true;
      return;
    }
    recLine.hidden = false;
    recCountsEl.textContent = recommendedLine(recCounts.rec, recCounts.notYet);
    // Everything recommended: a later item that needs one is offered anew.
    if (recCounts.notYet === 0) asked = '';
    const t = ctx.dockTarget();
    const offer = recCounts.notYet > 0 && !!t;
    askBtn.hidden = !offer || asked === t?.id;
    if (t) askBtn.textContent = `ask ${t.name} to recommend the rest`;
    askNote.textContent = offer && asked === t?.id ? `asked ${t.name}` : '';
  }
  ctx.onDockTarget(() => renderRecLine());

  async function askTheRest(): Promise<void> {
    const t = ctx.dockTarget();
    if (!t || askBtn.disabled) return;
    askBtn.disabled = true;
    try {
      const why = await ctx.askSession(RECOMMEND_THE_REST);
      if (why) askNote.textContent = why;
      else asked = t.id;
    } finally {
      askBtn.disabled = false;
    }
    if (asked) renderRecLine();
  }

  // refreshSummary reads serve's summary: the view chips' counts and the
  // recommended line.
  function refreshSummary(): void {
    void ctx.api
      .get<SummaryView>('/summary')
      .then((sv) => {
        applyCounts(sv.counts);
        recCounts = {
          rec: sv.recommended ?? 0,
          notYet: sv.not_recommended ?? 0,
        };
        renderRecLine();
      })
      .catch(() => {});
  }

  // The foot's agree-with-all band sits above the selection foot.
  const agree = agreeWithAll(agreeAll);
  handle.el.querySelector('.kit-foot')?.before(agree.el);

  function updateAgree(): void {
    agree.update(vocab && !boardHandle ? agreeGroups(vocab, loadedItems) : []);
  }

  // agreeAll accepts exactly a group's proposals. An outward choice only
  // goes to To apply. When the open item was one of them, the next
  // undecided item opens; with none open, the first undecided one does.
  async function agreeAll(g: AgreeGroup): Promise<void> {
    if (deciding) return;
    const open = currentOpenKey;
    deciding = open ?? '\u0000agree';
    const order = await orderFor(open);
    try {
      const r = await ctx.api.post<AcceptResult>('/proposals/accept', {
        ids: g.ids,
      });
      if (!r.accepted) {
        showReadError((r.errors ?? []).join('; ') || 'nothing was accepted');
        return;
      }
      selection.deselect(g.keys);
      lastUndo = null;
      if (!open || g.keys.includes(open)) {
        await moveOn(open ?? '', order);
      } else {
        void reload();
      }
      const errs = r.errors ?? [];
      if (errs.length) showReadError(errs.join('; '));
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    } finally {
      deciding = null;
    }
  }

  async function openDetail(key: string): Promise<void> {
    currentOpenKey = key;
    markOpenRow();
    feedAttached();
    try {
      const detail = await ctx.api.get<ItemDetailView>('/item', { key });
      // Another item opened meanwhile: this answer is no longer shown.
      if (currentOpenKey !== key) return;
      paintDetail(key, detail);
    } catch {
      // non-fatal; leave the reading column
    }
  }

  // paintDetail renders detail (key's) in the reading column.
  function paintDetail(key: string, detail: ItemDetailView): void {
    shownDetail = detail;
    const el = renderItem(ctx, detail, {
      // Re-render after a reject or a change from the recommendation card.
      onRefresh: () => void openDetail(key),
      decide: (d, until, note) => void decideOpen(key, d, until, note),
      accept: () => void acceptOpen(),
    });
    readEl.replaceChildren(el);
  }

  // midInput: Court is in the middle of answering the open item: its
  // closing-comment field is open with text typed, or its Not now picker
  // is open. A re-render then would wipe what he is doing.
  function midInput(): boolean {
    const comment = readEl.querySelector<HTMLElement>('.cb-close-comment');
    const typed =
      comment?.querySelector<HTMLInputElement>('input')?.value ?? '';
    if (comment && !comment.hidden && typed !== '') return true;
    const notNow = readEl.querySelector<HTMLElement>('.cb-notnow');
    return !!notNow && !notNow.hidden;
  }

  // proposalsChanged: a proposal for the open item came, or went (a live
  // "proposals" event naming key). The item re-renders, so its
  // recommendation card and the "recommended" mark appear or go; while
  // Court is mid-input it doesn't, and a quiet line under the question
  // says what changed, with "show" to re-render.
  let freshSeq = 0;
  async function proposalsChanged(key: string): Promise<void> {
    const mine = ++freshSeq;
    let detail: ItemDetailView;
    try {
      detail = await ctx.api.get<ItemDetailView>('/item', { key });
    } catch {
      return;
    }
    // Only the newest answer, and only for the item still open (and not
    // one this tab is deciding: it moves on).
    if (mine !== freshSeq || currentOpenKey !== key || deciding === key) return;
    if (!midInput()) {
      paintDetail(key, detail);
      return;
    }
    const p = detail.item.proposal;
    const says =
      p && p.state === 'pending'
        ? recommendLine(vocab, key, p.source, p.disposition)
        : 'the recommendation changed';
    const show = h(
      'button',
      {
        type: 'button',
        class: 'cb-link cb-rec-show',
        onclick: () => void openDetail(key),
      },
      'show',
    );
    const line = h(
      'p',
      { class: 'cb-rec-fresh' },
      h('span', { class: 'cb-rec-fresh-says' }, says),
      ' \u00b7 ',
      show,
    );
    const old = readEl.querySelector('.cb-rec-fresh');
    if (old) old.replaceWith(line);
    else readEl.querySelector('.cb-question')?.after(line);
  }

  // ---- the decide step: decide, move on, undo ------------------------------

  // viewLabel is the view's name in the summary ("waiting on you").
  function viewLabel(): string {
    return VIEWS.find((v) => v.id === filters.view)?.label ?? filters.view;
  }

  // showReadError shows serve's message in the reading column: on the open
  // item's cards, else under the summary.
  function showReadError(message: string): void {
    if (readEl.querySelector('.cb-choice-err')) {
      showChoiceError(readEl, message);
      return;
    }
    readEl.firstElementChild?.append(
      h('p', { class: 'cb-choice-err' }, message),
    );
  }

  // showDone is the reading column when the view has nothing left.
  function showDone(): void {
    currentOpenKey = null;
    shownDetail = null;
    readEl.replaceChildren(
      h(
        'div',
        { class: 'cb-read-empty' },
        h('p', { class: 'cb-read-empty-section kit-label' }, 'attention'),
        h('p', { class: 'cb-read-done' }, `Nothing left in ${viewLabel()}.`),
      ),
    );
    feedAttached();
  }

  // openKey opens key in the reading column (and the URL), as a click does.
  // When the URL already names key (the summary after its decision keeps
  // it), no route change comes, so it opens here.
  function openKey(key: string): void {
    const before = location.hash;
    ctx.route.go('item', key);
    if (location.hash === before) {
      void openDetail(key);
      void reload();
    }
  }

  // remember records a decision this tab made on key, for u: the decision
  // it had before, and the one serve's reply says the decide or accept
  // committed. With no such reply, u does nothing rather than guess.
  function remember(
    key: string,
    prev: Decision | null,
    made: Record<string, Committed> | null,
  ): void {
    const expect = made?.[key];
    lastUndo = expect ? { key, prev, expect } : null;
  }

  // orderFor is the view's order to move on from key in: the list's rows,
  // or, when key is on a page the list no longer holds (opening an item
  // reloads the first page), the view fetched a page at a time up to key.
  async function orderFor(key: string | null): Promise<string[]> {
    if (!key || lastOrder.includes(key)) return [...lastOrder];
    const keys: string[] = [];
    try {
      for (let total = Infinity; keys.length < total;) {
        const data = await ctx.api.get<ItemsView>(
          '/items',
          buildQuery(keys.length),
        );
        const got = (data.items ?? []).map((it) => it.key);
        keys.push(...got);
        total = Math.min(total, data.total);
        if (!got.length || got.includes(key)) break;
      }
    } catch {
      return [...lastOrder];
    }
    return keys.includes(key) ? keys : [...lastOrder];
  }

  // moveOn opens the next undecided item in the view's order (order: the
  // view's keys when key was decided), or the view's summary when none is
  // left. The undecided ones are the view as serve lists it now, fetched a
  // page at a time until the next one is known (at most the view's total),
  // so an item decided elsewhere meanwhile is passed over, none is skipped,
  // and an item on a later page is found before wrapping to the first.
  async function moveOn(key: string, order: string[]): Promise<void> {
    const fresh: string[] = [];
    let next: string | null | typeof MORE = MORE;
    try {
      for (let total = Infinity; next === MORE;) {
        const data = await ctx.api.get<ItemsView>(
          '/items',
          buildQuery(fresh.length),
        );
        const got = (data.items ?? []).map((it) => it.key);
        fresh.push(...got);
        total = Math.min(total, data.total);
        const complete = got.length === 0 || fresh.length >= total;
        next = nextInView(order, key, fresh, complete);
      }
    } catch {
      if (key) void openDetail(key);
      return;
    }
    // Court opened something else meanwhile: leave it open.
    if ((currentOpenKey ?? '') !== key) return;
    if (next) openKey(next);
    else {
      showDone();
      void reload();
    }
  }

  // decideOpen decides the open item (a card, a number key) and moves on.
  async function decideOpen(
    key: string,
    disposition: string,
    until?: string,
    note?: string,
  ): Promise<void> {
    if (deciding) return;
    deciding = key;
    const order = await orderFor(key);
    const prev =
      shownDetail?.item.key === key
        ? (shownDetail.item.decision ?? null)
        : null;
    try {
      const payload: Record<string, unknown> = { keys: [key], disposition };
      if (until) payload['until'] = until;
      // A close's closing comment ('' or none: it closes without one).
      if (note) payload['note'] = note;
      const r = await ctx.api.post<DecideResult>('/decide', payload);
      if (!(r.decided_keys ?? []).length) {
        showReadError((r.errors ?? []).join('; ') || 'nothing was decided');
        return;
      }
      selection.deselect([key]);
      remember(key, prev, r.decisions);
      await moveOn(key, order);
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    } finally {
      deciding = null;
    }
  }

  // acceptOpen accepts the open item's recommendation (a, or the card's
  // accept) and moves on. A close first asks for Court's closing comment
  // (the field a close card opens, empty: the recommendation's reason is
  // written to Court and is never the comment); its buttons accept, with
  // that comment or none.
  async function acceptOpen(note?: string): Promise<void> {
    const p = shownProposal();
    const key = currentOpenKey;
    if (!p || !key || deciding) return;
    if (
      note === undefined &&
      p.disposition === 'close' &&
      askClosingComment(readEl, (n) => void acceptOpen(n))
    )
      return;
    deciding = key;
    const order = await orderFor(key);
    const prev = shownDetail?.item.decision ?? null;
    try {
      const body: Record<string, unknown> = { ids: [p.id] };
      if (note) body['note'] = note;
      const r = await ctx.api.post<AcceptResult>('/proposals/accept', body);
      if (!r.accepted) {
        showReadError((r.errors ?? []).join('; ') || 'nothing was accepted');
        return;
      }
      selection.deselect([key]);
      remember(key, prev, r.decisions);
      await moveOn(key, order);
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    } finally {
      deciding = null;
    }
  }

  // undo takes back the last decision this tab made, in one step serve
  // checks: it restores the decision the item had before (or clears it when
  // it had none) only while the item's decision is still the one this tab
  // committed. serve refuses otherwise (409); its message shows.
  async function undo(): Promise<void> {
    const u = lastUndo;
    if (!u || deciding) return;
    deciding = u.key;
    try {
      await ctx.api.post<DecisionUndoResult>('/decisions/undo', {
        key: u.key,
        expect: u.expect,
        restore: u.prev
          ? {
              disposition: u.prev.disposition,
              until: u.prev.until ?? '',
              note: u.prev.note ?? '',
            }
          : null,
      });
      lastUndo = null;
      openKey(u.key);
    } catch (err) {
      // A refusal stands: this decision can't be undone any more.
      lastUndo = null;
      showReadError(err instanceof Error ? err.message : String(err));
    } finally {
      deciding = null;
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

  // shownProposal is the pending proposal the reading column shows: what a
  // and r act on. Not the list's row (the list may be reloading, its rows
  // gone until the answer comes).
  function shownProposal() {
    const d = shownDetail;
    if (!d || d.item.key !== currentOpenKey) return null;
    const p = d.item.proposal;
    return p && p.state === 'pending' ? p : null;
  }

  // 'a' and 'r' accept/reject the open item's proposal. Like "d", they are
  // Section.keys: app.ts registers them only while Attention is shown, so on
  // #/rules they cannot reach the open item Attention keeps while hidden, and
  // Rules and Apply are free to bind their own a/r.
  const acceptKey: KeyBinding = {
    keys: 'a',
    label: 'accept proposal',
    group: 'page',
    run() {
      void acceptOpen();
    },
  };

  const rejectKey: KeyBinding = {
    keys: 'r',
    label: 'reject proposal',
    group: 'page',
    run() {
      const p = shownProposal();
      if (!p || !currentOpenKey) return;
      openRejectSheet(ctx, [p.id], () => {
        selection.deselect([currentOpenKey!]);
        void openDetail(currentOpenKey!);
        void reload();
      });
    },
  };

  // u undoes the last decision this tab made; 1..9 pick the open item's
  // nth card (a click on it: Not now opens its conditions).
  const undoKey: KeyBinding = {
    keys: 'u',
    label: 'undo the last decision',
    group: 'page',
    run() {
      if (!lastUndo) return false;
      void undo();
    },
  };
  const choiceKeys: KeyBinding[] = [1, 2, 3, 4, 5, 6, 7, 8, 9].map((n) => ({
    keys: String(n),
    label: `pick choice ${n}`,
    group: 'page',
    run() {
      if (!currentOpenKey) return false;
      const card = readEl.querySelectorAll<HTMLElement>(
        '.cb-choices .cb-choice',
      )[n - 1];
      if (!card) return false;
      card.click();
    },
  }));

  const listKeys: KeyBinding[] = [
    decideKey,
    acceptKey,
    rejectKey,
    undoKey,
    ...choiceKeys,
  ];
  // The board is not a list (no j/k/o/x), but it has the list's search
  // field: / focuses it there too.
  const searchKey: KeyBinding = {
    keys: '/',
    label: 'search',
    group: 'family',
    run() {
      handle.focusSearch?.();
    },
  };
  const boardKeys: KeyBinding[] = [decideKey, searchKey];

  // Load summary for initial counts and the recommended line.
  refreshSummary();
  // The vocabulary words the rows' recommendations and agree with all.
  void getVocab(ctx)
    .then((v) => {
      vocab = v;
      setRows();
      updateAgree();
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
    agree.update([]);
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
    updateAgree();
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
    // The list's keys (j k o ↵ x ⇧x, / its search) come from the kit, bound
    // to this list while it shows. The board is not a list: it has no
    // cursor, and its cards hide the reading column, so there only "d"
    // (the shared selection) and "/" (the search, which filters the lanes)
    // work.
    listKeys: () => (boardHandle ? null : { nav: handle, selects: true }),
    keys: () => (boardHandle ? boardKeys : listKeys),
    onLive(type: string, data: unknown) {
      if (type === 'gap') {
        // Events this page never heard were pruned: reload all it shows
        // (the counts, the list or board, and the open item).
        refreshSummary();
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
        if (currentOpenKey && currentOpenKey !== deciding) {
          void openDetail(currentOpenKey);
        }
      } else if (type === 'index') {
        // One reload per index event: apply counts from the event payload and
        // refresh the list (or the board when it is active).
        const s = data as { counts?: Record<string, number> };
        applyCounts(s?.counts ?? null);
        // The recommended line's counts change with the index too.
        refreshSummary();
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
        // The open item decided elsewhere: show its decision (not while this
        // tab is deciding it, which moves on).
        if (
          currentOpenKey &&
          currentOpenKey !== deciding &&
          decidedKeys.includes(currentOpenKey)
        ) {
          void openDetail(currentOpenKey);
        }
        // Re-fetch summary so view-chip counts stay correct.
        refreshSummary();
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      } else if (type === 'proposals') {
        // proposals event: an agent's or a rule's new pending proposals, or
        // proposals accepted, rejected or changed. Payload shape:
        // {ids: number[], keys: string[], state: string, source?: string};
        // keys are the items the proposals are for.
        const propPayload = data as {
          ids?: number[];
          keys?: string[];
          state?: string;
        };
        const propKeys = propPayload?.keys ?? [];
        const propState = propPayload?.state ?? '';

        // When proposals are settled (accepted, rejected, changed), deselect
        // their items so the selection count stays correct.
        if (propState !== 'pending' && propKeys.length > 0) {
          selection.deselect(propKeys);
        }
        // The open item's recommendation came or went, in any state: by
        // keys, as the open item may not be on the loaded page.
        if (
          currentOpenKey &&
          currentOpenKey !== deciding &&
          propKeys.includes(currentOpenKey)
        ) {
          void proposalsChanged(currentOpenKey);
        }

        // Re-fetch summary to update view-chip counts (particularly 'proposed').
        refreshSummary();
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
