// attention.ts — the Attention section: list, views, search, item detail.
//
// makeAttention(ctx) builds the kit list(), wires the view chips and search,
// fetches items from GET /api/items, and opens items in the reading column via
// renderItem() from item.ts.
//
// The Selection store is exported so the page's other parts bind to it
// without creating a second store.

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
  GroupView,
  Message,
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
  countWord,
  groupButtons,
  leftOpenMeta,
  lookIntoCard,
} from './decide-math.ts';
import { showChoiceError, askClosingComment } from './choices.ts';
import { lookingLine, sessionName } from './sessions.ts';
import { coalesced } from './coalesce.ts';
import { openRejectSheet, openDiscardSheet } from './proposals.ts';

// RECOMMEND_THE_REST is what the not-recommended group's "Ask ‹session› to
// recommend N" sends the dock's session: a normal message, delivered when
// its turn ends.
export const RECOMMEND_THE_REST =
  'Please recommend the items casebook still needs a recommendation for: call casebook_next until it says done.';

// PAGE_SIZE is the number of items fetched per page. The kit is tested to 500
// rendered rows; we paginate at 200 to stay safe.
const PAGE_SIZE = 200;

// Shared selection store.
export const selection: Selection = createSelection();

// View chip ids map to API view= param values.
const VIEWS: Chip[] = [
  { id: 'waiting', label: 'waiting on you' },
  { id: 'new', label: 'new' },
  { id: 'due', label: 'due' },
  { id: 'proposed', label: 'proposed' },
];

// The list has no filter chips (Court, 2026-10-10: search only). serve's
// filter parameters stay, for agents' tools.

// State for the current view and the search text.
interface Filters {
  view: string;
  q: string;
}

function emptyFilters(): Filters {
  return { view: 'waiting', q: '' };
}

// LEFT_OPEN is the left-open group's id among the groups' (collapsed).
const LEFT_OPEN = 'left-open';

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
  // reloading: a first-page load is on its way; loadingMore: a next page
  // is. loads counts the first-page loads started, so a next page asked
  // for before one started is not added to the rows it replaced.
  let reloading = false;
  let loadingMore = false;
  let loads = 0;
  let footEl: HTMLElement | null = null;
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;
  let totalItemsForView = 0; // updated after every load
  // The view's groups (serve's order and counts, every page) and the ones
  // Court collapsed (in memory only). A collapsed group's rows are not
  // given to the list, so j and k pass over them.
  let groups: GroupView[] = [];
  // The left-open group starts collapsed.
  const collapsed = new Set<string>([LEFT_OPEN]);
  // What the page knows of each item it has seen in a group (loaded rows
  // and a group fetched whole): its group and its pending proposal's id.
  // A ticked item on a page not loaded is counted and acted on from here.
  const groupOfKey = new Map<string, string>();
  const proposalOfKey = new Map<string, number>();
  // The groups whose button is running (their buttons wait).
  const busyGroups = new Set<string>();
  // The view's left-open group (kept items still open: they need no
  // decision, so the view's count and move-on leave them out). It is the
  // last group, once every page of the items that need a decision is
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
  // serve's decision vocabulary (the groups' buttons, the summary, the
  // recommendation line); null until it loads.
  let vocab: DecisionVocabView | null = null;
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

  // ---- foot: the pinned summary ------------------------------------------

  // The foot is pinned to the bottom of the list: the selection's count,
  // the view's summary ("31 items, all recommended by ‹session›"), "discard
  // all N" while the view has agent recommendations, and "show N more"
  // while more pages exist.
  const summaryEl = h('span', { class: 'cb-summary' });
  const discardAllBtn = h('button', {
    class: 'kit-btn cb-discard-all',
    type: 'button',
    hidden: true,
    onclick() {
      discardView();
    },
  }) as HTMLButtonElement;

  function buildFoot(): HTMLElement {
    const selCount = h(
      'span',
      { class: 'cb-sel-count', hidden: true },
      '0 selected',
    );
    const footRight = h(
      'div',
      { class: 'cb-foot-right' },
      discardAllBtn,
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
    footEl = h('div', { class: 'cb-foot' }, selCount, summaryEl, footRight);
    return footEl;
  }

  // ---- list -----------------------------------------------------------------

  const handle: ListHandle<ItemView> = list<ItemView>({
    label: 'attention',
    views: VIEWS.map((v) => ({ ...v, on: v.id === filters.view })),
    filters: [],
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
      const left = isLeftOpen(it);
      const age = left ? leftOpenMeta(it.decision?.decided_at) : ageOf(it);
      // In a recommendation's group the row gives its reason (the group's
      // header names the choice).
      const reason =
        !left && it.group && it.proposal?.state === 'pending'
          ? it.proposal.note || undefined
          : undefined;
      return {
        id: it.key,
        key: kindKey,
        // Fallback: use display key (without kind prefix) so titleless items
        // like branch:schuettc/hail@feat/client show "schuettc/hail@feat/client".
        title: it.title ?? keyWithoutKind(it.key),
        meta: age,
        // An item new activity brought back says so ("new activity since
        // you left it open"), before any recommendation's reason.
        sub:
          [it.new_activity ? it.due_reason : undefined, reason]
            .filter(Boolean)
            .join(' \u00b7 ') || undefined,
        selectable: true,
      };
    },
    onChip(group, id) {
      if (group === 'view') {
        filters.view = id;
        ctx.route.go('attention', id);
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

  // The groups' CSS (indented rows, one row height, the reserved
  // scrollbar) is Attention's alone.
  handle.el.classList.add('cb-attention');

  // ---- API ------------------------------------------------------------------

  function buildQuery(pageOffset: number): Record<string, string> {
    const p: Record<string, string> = {
      view: filters.view,
      offset: String(pageOffset),
      limit: String(PAGE_SIZE),
    };
    if (filters.q) p['q'] = filters.q;
    return p;
  }

  // reload loads the list's first page for the view and search as they
  // are when it runs. Coalesced (coalesce.ts): one load in flight and at
  // most one queued, so a view or search change made while a
  // load is on its way runs once that load ends, never dropped (casebook
  // 0.4.7: it was, and the list kept the old view). A load whose query
  // changed while it was on its way paints nothing: the newer query's load
  // paints.
  const reload = coalesced(reloadNow);

  // queryKey is a load's query, comparable (the page offset aside).
  function queryKey(): string {
    return JSON.stringify(buildQuery(0));
  }

  async function reloadNow(): Promise<void> {
    reloading = true;
    loads++;
    const asked = queryKey();
    try {
      offset = 0;
      loadedItems = [];
      const data = await ctx.api.get<ItemsView>('/items', buildQuery(0));
      if (queryKey() !== asked) {
        // Every change to the query asks for a reload; this one makes sure.
        void reload();
        return;
      }
      totalItems = data.total;
      totalItemsForView = data.total;
      loadedItems = data.items ?? [];
      leftOpen = data.left_open ?? [];
      leftOpenTotal = data.left_open_total ?? 0;
      groups = data.groups ?? [];
      viewRecs = data.agent_recommended ?? 0;
      know(loadedItems);
      lastOrder = loadedItems.map((it) => it.key);
      offset = loadedItems.length;
      setRows();
      markOpenRow();
      updateFoot();
      updateReadEmptyCount();
    } catch {
      // non-fatal; leave the list as-is
    } finally {
      reloading = false;
    }
  }

  // loadMore adds the next page ("show more"). Not while the first page is
  // loading (it starts the rows again); a page that comes back after a
  // first-page load started is not added (the rows it followed are gone).
  async function loadMore(): Promise<void> {
    if (reloading || loadingMore || offset >= totalItems) return;
    loadingMore = true;
    const mine = loads;
    try {
      const data = await ctx.api.get<ItemsView>('/items', buildQuery(offset));
      if (mine !== loads) return;
      const next = data.items ?? [];
      loadedItems = [...loadedItems, ...next];
      leftOpen = data.left_open ?? [];
      leftOpenTotal = data.left_open_total ?? 0;
      groups = data.groups ?? [];
      viewRecs = data.agent_recommended ?? 0;
      know(next);
      lastOrder = loadedItems.map((it) => it.key);
      offset = loadedItems.length;
      totalItems = data.total;
      totalItemsForView = data.total;
      setRows();
      markOpenRow();
      updateFoot();
    } catch {
      // non-fatal
    } finally {
      loadingMore = false;
    }
  }

  // setRows renders the items that need a decision, under their groups'
  // headers (a collapsed group's header alone), and, once every page of
  // them is loaded, the left-open group last: its header and (expanded)
  // its muted rows.
  function setRows(): void {
    const withLeft = offset >= totalItems && leftOpenTotal > 0;
    const left = withLeft && !collapsed.has(LEFT_OPEN) ? leftOpen : [];
    const open = loadedItems.filter((it) => !collapsed.has(it.group));
    shown = [...open, ...left];
    handle.setItems(shown);
    const rowEls = handle.el.querySelectorAll<HTMLElement>(
      '.kit-rows > .kit-row',
    );
    open.forEach((it, i) => {
      if (it.new_activity) rowEls[i]?.classList.add('cb-new-activity');
    });
    // "‹session› is looking into it" (muted, agent colour) on the rows a
    // session is looking into.
    // It goes where the row's sub line goes: in the text column, under the
    // title (and any recommendation's reason), one line.
    shown.forEach((it, i) => {
      const col = rowEls[i]?.querySelector('.kit-title')?.parentElement;
      if (!col) return;
      col.querySelector('.cb-row-looking')?.remove();
      const says = lookingLine(it.looking);
      if (says) col.append(h('div', { class: 'cb-row-looking' }, says));
    });
    placeGroupHeads(open, rowEls);
    if (!withLeft) return;
    for (let i = open.length; i < shown.length; i++) {
      rowEls[i]?.classList.add('cb-left-open');
    }
    const head = leftOpenHead();
    heads.push(head);
    const first = rowEls[open.length];
    if (first) first.before(head.el);
    else handle.el.querySelector('.kit-rows')?.append(head.el);
  }

  // ---- the groups ------------------------------------------------------------

  // know records the group and the pending proposal of each item.
  function know(items: ItemView[]): void {
    for (const it of items) {
      if (!it.group) continue;
      groupOfKey.set(it.key, it.group);
      if (it.proposal?.state === 'pending')
        proposalOfKey.set(it.key, it.proposal.id);
      else proposalOfKey.delete(it.key);
    }
  }

  // tickedIn is the ticked items of group g (on any page).
  function tickedIn(g: GroupView): string[] {
    return selection.ids().filter((k) => groupOfKey.get(k) === g.id);
  }

  // heads are the shown groups' headers, painted again when the selection
  // or the attached session changes.
  let heads: Array<{ el: HTMLElement; paint(): void }> = [];

  // placeGroupHeads puts each group with loaded rows under its header, in
  // serve's order: before its first row, or (collapsed) where its rows
  // would be.
  function placeGroupHeads(
    open: ItemView[],
    rowEls: NodeListOf<HTMLElement>,
  ): void {
    const body = handle.el.querySelector<HTMLElement>('.kit-rows');
    heads = [];
    if (!body) return;
    const loaded = new Set(loadedItems.map((it) => it.group));
    let last: Element | null = null;
    for (const g of groups) {
      if (!loaded.has(g.id)) continue;
      const head = groupHead(g);
      heads.push(head);
      const first = open.findIndex((it) => it.group === g.id);
      if (first >= 0) {
        rowEls[first]?.before(head.el);
        let end = first;
        while (end + 1 < open.length && open[end + 1].group === g.id) end++;
        last = rowEls[end] ?? head.el;
      } else {
        if (last) last.after(head.el);
        else body.prepend(head.el);
        last = head.el;
      }
    }
  }

  // headShell is a group's header, sticky over its rows: the caret, the
  // group's checkbox (box; a blank of its width without one), its label
  // (hint under it) and its count; then the line its buttons go on, one
  // line of one height whether or not they are there. The caret and the
  // label collapse and expand it (id: the group's).
  function headShell(
    id: string,
    label: string,
    n: number,
    box: HTMLElement | null,
    hint: HTMLElement | null,
    data: Record<string, string>,
  ): { el: HTMLElement; acts: HTMLElement } {
    const isOpen = !collapsed.has(id);
    const toggleOpen = (): void => {
      if (collapsed.has(id)) collapsed.delete(id);
      else collapsed.add(id);
      setRows();
      markOpenRow();
    };
    const top = h(
      'div',
      { class: 'cb-group-top' },
      h(
        'button',
        {
          type: 'button',
          class: 'cb-group-caret',
          'aria-expanded': String(isOpen),
          'aria-label': label,
          onclick: toggleOpen,
        },
        isOpen ? '\u25bc' : '\u25b6',
      ),
      box ?? h('span', { class: 'cb-group-nobox' }),
      h(
        'span',
        { class: 'cb-group-name', onclick: toggleOpen },
        h('span', { class: 'cb-group-label' }, label),
        hint,
      ),
      h('span', { class: 'cb-group-n' }, String(n)),
    );
    const acts = h('div', { class: 'cb-group-acts' });
    const el = h(
      'div',
      {
        class: 'cb-group' + (isOpen ? '' : ' collapsed'),
        role: 'presentation',
        dataset: data,
      },
      top,
      acts,
    );
    return { el, acts };
  }

  // groupButton is one of group id's buttons: while one runs, the group's
  // buttons wait.
  function groupButton(
    id: string,
    cls: string,
    label: string,
    run: () => Promise<void> | void,
    off = false,
  ): HTMLElement {
    return h(
      'button',
      {
        type: 'button',
        class: 'kit-btn ' + cls,
        disabled: off || busyGroups.has(id),
        onclick() {
          if (busyGroups.has(id)) return;
          busyGroups.add(id);
          paintAll();
          void Promise.resolve(run()).finally(() => {
            busyGroups.delete(id);
            paintAll();
          });
        },
      },
      label,
    );
  }

  // groupHead is one group's header: a checkbox that ticks every item of
  // the group (every page), an outward group's "goes to To apply first"
  // under its label; its buttons act on the whole group or its ticked
  // items.
  function groupHead(g: GroupView): { el: HTMLElement; paint(): void } {
    const box = h('span', {
      class: 'cb-group-box',
      role: 'checkbox',
      'aria-checked': 'false',
      'aria-label': g.label,
      onclick() {
        void tickGroup(g);
      },
    });
    const hint =
      g.outward && vocab?.to_apply_hint
        ? h('span', { class: 'cb-group-where' }, vocab.to_apply_hint)
        : null;
    const { el, acts } = headShell(g.id, g.label, g.total, box, hint, {
      group: g.id,
      d: g.disposition,
    });
    const button = (
      cls: string,
      label: string,
      run: () => Promise<void> | void,
      off = false,
    ): HTMLElement => groupButton(g.id, cls, label, run, off);

    function paint(): void {
      const ticked = tickedIn(g);
      const n = ticked.length;
      box.setAttribute(
        'aria-checked',
        n === 0 ? 'false' : n >= g.total ? 'true' : 'mixed',
      );
      const t = ctx.dockTarget();
      if (!vocab) {
        acts.replaceChildren();
        return;
      }
      if (g.id === 'none') {
        // The not recommended: only the ask, with a session attached.
        const ask =
          t && asked !== t.id
            ? button(
                'fill cb-group-ask',
                vocab.recommend_these
                  .replaceAll('{session}', t.name)
                  .replaceAll('{n}', countWord(g.total, 0, vocab.count_words)),
                askTheRest,
              )
            : null;
        const note =
          t && asked === t.id
            ? h('span', { class: 'cb-group-note' }, `asked ${t.name}`)
            : null;
        acts.replaceChildren(...[ask, note].filter((x) => x !== null));
        return;
      }
      const w = groupButtons(g, n, vocab);
      const scoped = n > 0 && n < g.total;
      // Every item it would name is being looked into already.
      const rows = loadedItems.filter((it) => it.group === g.id);
      const looked = scoped
        ? ticked.every((k) => rows.some((it) => it.key === k && it.looking))
        : rows.length >= g.total && rows.every((it) => !!it.looking);
      acts.replaceChildren(
        ...[
          scoped
            ? h(
                'span',
                { class: 'cb-group-ticked' },
                vocab.ticked.replaceAll('{n}', String(n)),
              )
            : null,
          button('fill cb-group-act', w.act, () => actGroup(g)),
          t
            ? button('cb-group-look', w.look, () => lookGroup(g), looked)
            : null,
          g.source.startsWith('rule:')
            ? null
            : button('cb-group-discard', w.discard, () => discardGroup(g)),
          n > 0
            ? h(
                'button',
                {
                  type: 'button',
                  class: 'cb-group-untick',
                  onclick() {
                    selection.deselect(tickedIn(g));
                  },
                },
                vocab.untick,
              )
            : null,
        ].filter((x) => x !== null),
      );
    }
    paint();
    return { el, paint };
  }

  // leftOpenHead is the left-open group's header (serve's label, its count
  // across pages). Its items need no decision: no checkbox, no choice, no
  // discard; with a session attached, Look into, naming every left-open
  // item (every page).
  function leftOpenHead(): { el: HTMLElement; paint(): void } {
    const { el, acts } = headShell(
      LEFT_OPEN,
      vocab?.left_open ?? '',
      leftOpenTotal,
      null,
      null,
      { group: LEFT_OPEN },
    );
    function paint(): void {
      if (!vocab || !ctx.dockTarget()) {
        acts.replaceChildren();
        return;
      }
      const look = vocab.look_many.replaceAll(
        '{n}',
        countWord(leftOpenTotal, 0, vocab.count_words),
      );
      const looked =
        leftOpen.length >= leftOpenTotal &&
        leftOpen.every((it) => !!it.looking);
      acts.replaceChildren(
        groupButton(LEFT_OPEN, 'cb-group-look', look, lookLeftOpen, looked),
      );
    }
    paint();
    return { el, paint };
  }

  // lookLeftOpen asks the attached session to look into every left-open
  // item.
  async function lookLeftOpen(): Promise<void> {
    try {
      await lookInto(await leftOpenKeys());
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    }
  }

  // leftOpenKeys is every left-open item's key in the view (every page).
  async function leftOpenKeys(): Promise<string[]> {
    const out: string[] = [];
    for (let total = Infinity; out.length < total;) {
      const data = await ctx.api.get<ItemsView>('/items', {
        ...viewFilters(),
        limit: String(PAGE_SIZE),
        left_offset: String(out.length),
      });
      const got = (data.left_open ?? []).map((it) => it.key);
      out.push(...got);
      total = data.left_open_total;
      if (!got.length) break;
    }
    return out;
  }

  function paintAll(): void {
    for (const head of heads) head.paint();
  }

  // groupFilters is the view and its filters as GET /api/items and
  // /proposals/withdraw take them, narrowed to group g.
  function groupFilters(g: GroupView): Record<string, string> {
    return { ...viewFilters(), group: g.id };
  }

  // fetchGroup is every item of group g, every page (the filters apply).
  async function fetchGroup(g: GroupView): Promise<ItemView[]> {
    const out: ItemView[] = [];
    for (let total = Infinity; out.length < total;) {
      const data = await ctx.api.get<ItemsView>('/items', {
        ...groupFilters(g),
        offset: String(out.length),
        limit: String(PAGE_SIZE),
      });
      const got = data.items ?? [];
      out.push(...got);
      total = data.total;
      if (!got.length) break;
    }
    know(out);
    return out;
  }

  // tickGroup ticks every item of g (every page), or, all ticked, unticks
  // them.
  async function tickGroup(g: GroupView): Promise<void> {
    const ticked = tickedIn(g);
    if (ticked.length >= g.total) {
      selection.deselect(ticked);
      return;
    }
    try {
      const items = await fetchGroup(g);
      selection.all(items.map((it) => it.key));
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    }
  }

  // target is what a group's button acts on: its ticked items, or with
  // none ticked every item of the group (every page).
  async function target(
    g: GroupView,
  ): Promise<{ keys: string[]; ids: number[] }> {
    let keys = tickedIn(g);
    if (!keys.length) keys = (await fetchGroup(g)).map((it) => it.key);
    const ids: number[] = [];
    for (const k of keys) {
      const id = proposalOfKey.get(k);
      if (id !== undefined) ids.push(id);
    }
    return { keys, ids };
  }

  // actGroup accepts the recommendations of a group (or its ticked items).
  // An outward choice only goes to To apply. When the open item was one of
  // them, the next undecided item opens; with none open, the first
  // undecided one does.
  async function actGroup(g: GroupView): Promise<void> {
    if (deciding) return;
    const open = currentOpenKey;
    deciding = open ?? '\u0000group';
    try {
      const { keys, ids } = await target(g);
      if (!ids.length) return;
      const order = await orderFor(open);
      const r = await ctx.api.post<AcceptResult>('/proposals/accept', {
        ids,
      });
      if (!r.accepted) {
        showReadError((r.errors ?? []).join('; ') || 'nothing was accepted');
        return;
      }
      selection.deselect(keys);
      lastUndo = null;
      if (!open || keys.includes(open)) {
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

  // lookGroup asks the attached session to look into a group's items (or
  // its ticked ones).
  async function lookGroup(g: GroupView): Promise<void> {
    try {
      await lookInto((await target(g)).keys);
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    }
  }

  // lookInto sends the attached session one message naming keys, all of
  // them attached, with the look-into purpose (serve marks each while it is
  // underway, and the rows say so). It decides nothing.
  async function lookInto(keys: string[]): Promise<void> {
    if (!ctx.dockTarget() || !vocab) return;
    try {
      if (!keys.length) return;
      const message = vocab.look_into.message_many.replaceAll(
        '{keys}',
        keys.join(', '),
      );
      const why = await ctx.askSession(message, { keys }, 'look-into');
      if (why) {
        showReadError(why);
        return;
      }
      await reload();
    } catch (err) {
      showReadError(err instanceof Error ? err.message : String(err));
    }
  }

  // discardGroup confirms, then withdraws the group's agent
  // recommendations (every page), or exactly its ticked items'.
  function discardGroup(g: GroupView): void {
    const ticked = tickedIn(g);
    if (ticked.length && ticked.length < g.total) {
      const ids = ticked
        .map((k) => proposalOfKey.get(k))
        .filter((id): id is number => id !== undefined);
      openDiscardSheet(ctx, ids.length, viewLabel(), { ids }, discarded);
      return;
    }
    openDiscardSheet(ctx, g.total, viewLabel(), groupFilters(g), discarded);
  }

  function updateFoot(): void {
    if (!footEl) return;

    // Show or hide only the show-more button; the foot itself stays visible
    // so the selection count and the summary remain.
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
    renderSummary();

    // The foot div itself is always visible.
    footEl.hidden = false;
  }

  // renderSummary is the pinned summary: the view's items and who
  // recommended them (serve's summary words; the session in the agent's
  // colour), and "discard all N" while it has agent recommendations.
  function renderSummary(): void {
    discardAllBtn.hidden = viewRecs === 0 || !vocab;
    if (vocab)
      discardAllBtn.textContent = vocab.discard_all.replaceAll(
        '{n}',
        String(viewRecs),
      );
    if (!vocab) {
      summaryEl.replaceChildren();
      return;
    }
    const n = String(totalItemsForView);
    const recs = groups.filter((g) => g.id !== 'none');
    const r = recs.reduce((sum, g) => sum + g.total, 0);
    const makers = new Set(recs.map((g) => g.by?.id ?? g.source));
    const one =
      r > 0 &&
      r === totalItemsForView &&
      makers.size === 1 &&
      !recs[0].source.startsWith('rule:');
    if (!one) {
      summaryEl.replaceChildren(
        vocab.summary.mixed.replaceAll('{n}', n).replaceAll('{r}', String(r)),
      );
      return;
    }
    const [before, after = ''] = vocab.summary.one_session
      .replaceAll('{n}', n)
      .split('{session}');
    summaryEl.replaceChildren(
      before,
      h(
        'span',
        { class: 'cb-summary-who' },
        sessionName(recs[0].by, recs[0].source),
      ),
      after,
    );
  }

  // markOpenRow moves the list's open row to the item the reading column
  // shows (the next item move-on opened goes through the route, not a click
  // on its row). Nothing when the row already is the open one, so a reload
  // leaves the cursor where it was.
  function markOpenRow(): void {
    if (!currentOpenKey) return;
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

  // ---- recommendations: ask, discard ----------------------------------------

  // asked is the session this page asked to recommend the rest: the
  // not-recommended group's ask gives way to "asked …" for it, so one click
  // sends one message.
  let asked = '';
  ctx.onDockTarget(() => paintAll());

  async function askTheRest(): Promise<void> {
    const t = ctx.dockTarget();
    if (!t) return;
    const why = await ctx.askSession(RECOMMEND_THE_REST);
    if (why) showReadError(why);
    else asked = t.id;
  }

  // viewRecs is the view's agent recommendations (serve's count, every
  // page, the filters applied): what "discard all N" withdraws.
  let viewRecs = 0;

  // viewFilters is the view and its filters as /proposals/withdraw takes
  // them (the list's query without its page).
  function viewFilters(): Record<string, string> {
    const p = buildQuery(0);
    delete p['offset'];
    delete p['limit'];
    return p;
  }

  // discarded follows a discard: the list is read again, and the offer to
  // ask the session to recommend the rest is made anew.
  function discarded(): void {
    asked = '';
    refreshSummary();
    void reload();
  }

  // discardView confirms, then withdraws every agent recommendation in the
  // view and its filters (every page, not only the loaded rows).
  function discardView(): void {
    if (viewRecs === 0) return;
    openDiscardSheet(ctx, viewRecs, viewLabel(), viewFilters(), discarded);
  }

  // refreshSummary reads serve's summary: the view chips' counts.
  function refreshSummary(): void {
    void ctx.api
      .get<SummaryView>('/summary')
      .then((sv) => {
        applyCounts(sv.counts);
      })
      .catch(() => {});
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
      cardsReady: () => renderAsk(),
    });
    readEl.replaceChildren(el);
    renderAsk();
  }

  // ---- ask the session to look into it ------------------------------------

  // renderAsk puts "Look into it" in the open item's card grid, the last
  // card, numbered after the decisions (its number key picks it): only
  // with a session attached. It decides nothing: it sends the session
  // serve's message about the item (the vocabulary's look_into), with the
  // look-into purpose. While a session is looking into the item the card
  // says so in place of its sentence and can't be clicked.
  let askingKey = '';
  function renderAsk(): void {
    const grid = readEl.querySelector<HTMLElement>('.cb-decide .cb-choices');
    const old = grid?.querySelector('.cb-look-card');
    const key = currentOpenKey;
    const t = ctx.dockTarget();
    const words = vocab?.look_into;
    if (
      !grid ||
      !grid.dataset.cards ||
      !key ||
      !t ||
      !words ||
      shownDetail?.item.key !== key
    ) {
      old?.remove();
      return;
    }
    const w = lookIntoCard(words, t.name, key);
    const looking = lookingLine(shownDetail.item.looking);
    const n = Number(grid.dataset.cards) + 1;
    const card = h(
      'button',
      {
        type: 'button',
        class:
          'kit-card edge-agent cb-choice cb-look-card' +
          (looking ? ' looking' : ''),
        dataset: { n: String(n) },
        disabled: !!looking || askingKey === key,
        async onclick() {
          if (card.disabled) return;
          const wrap = grid.closest<HTMLElement>('.cb-choices-wrap');
          if (wrap) showChoiceError(wrap, '');
          askingKey = key;
          card.disabled = true;
          const why = await ctx.askSession(
            w.message,
            { keys: [key] },
            'look-into',
          );
          askingKey = '';
          if (why) {
            card.disabled = false;
            if (wrap) showChoiceError(wrap, why);
          } else void refreshLooking(key);
        },
      },
      h('span', { class: 'cb-choice-k' }, String(n)),
      h('span', { class: 'cb-choice-label' }, w.label),
      h('span', { class: 'cb-choice-says' }, looking || w.says),
    ) as HTMLButtonElement;
    if (old) old.replaceWith(card);
    else grid.append(card);
  }
  ctx.onDockTarget(() => renderAsk());

  // refreshLooking reads whether a session is looking into key (the open
  // item) and shows it in place, under the question, without re-rendering
  // the item (Court may be mid-input on it).
  let lookingSeq = 0;
  async function refreshLooking(key: string): Promise<void> {
    const mine = ++lookingSeq;
    let detail: ItemDetailView;
    try {
      detail = await ctx.api.get<ItemDetailView>('/item', { key });
    } catch {
      return;
    }
    if (mine !== lookingSeq || currentOpenKey !== key) return;
    if (shownDetail?.item.key !== key) return;
    shownDetail = {
      ...shownDetail,
      item: { ...shownDetail.item, looking: detail.item.looking },
    };
    const says = lookingLine(detail.item.looking);
    const el = readEl.querySelector<HTMLElement>('.cb-looking');
    if (el) {
      el.textContent = says;
      el.hidden = !says;
    }
    renderAsk();
  }

  // lookingShown: the open item or a shown row says a session is looking
  // into it (a message settling may take that away).
  function lookingShown(): boolean {
    return !!shownDetail?.item.looking || shown.some((it) => !!it.looking);
  }

  // lookingMayHaveChanged: a message moved (posted, answered, declined,
  // failed; a turn ended): the open item's marker and the rows' follow.
  function lookingMayHaveChanged(): void {
    if (currentOpenKey) void refreshLooking(currentOpenKey);
    void reload();
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
  // "proposals" event naming key), or evidence for it came (why). The item re-renders, so its
  // recommendation card and the "recommended" mark appear or go; while
  // Court is mid-input it doesn't, and a quiet line under the question
  // says what changed, with "show" to re-render.
  let freshSeq = 0;
  async function proposalsChanged(
    key: string,
    why: 'proposals' | 'evidence' = 'proposals',
  ): Promise<void> {
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
      why === 'evidence'
        ? 'new evidence'
        : p && p.state === 'pending'
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
    // Zero every view before merging.
    for (const v of VIEWS) viewCounts[v.id] = 0;
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

  // The groups' ticked counts and buttons follow the selection.
  selection.onChange(() => {
    paintAll();
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

  // Load summary for the view chips' counts.
  refreshSummary();
  // The vocabulary words the groups' buttons and the summary.
  void getVocab(ctx)
    .then((v) => {
      vocab = v;
      setRows();
      updateFoot();
      renderAsk();
    })
    .catch(() => {});

  // ---- show(sub) -- router hook --------------------------------------------

  function show(sub: string): void {
    // Becoming active: the composer shows Attention's selection or open item.
    const wasActive = active;
    active = true;
    feedAttached();
    // Restore search text from URL (?q=...) on every navigation into this section.
    const urlQ = getUrlQ();
    if (urlQ !== filters.q) {
      filters.q = urlQ;
      handle.setSearch(urlQ);
    }
    const view = sub || 'waiting';
    if (VIEWS.some((v) => v.id === view)) {
      // Known view chip: switch to it.
      filters.view = view;
      handle.setChips('view', viewChips(filters.view));
      showReadEmpty();
      void reload();
    } else if (sub) {
      // Not a view id: an item key from a #/item/<key> link. The list
      // keeps the view it shows.
      handle.setChips('view', viewChips(filters.view));
      // A row click opens its item itself, then moves the hash here: the
      // item is already open (or loading), so it isn't loaded twice. A
      // different key (back, forward, a pasted link), or coming back from
      // another section, loads it.
      if (!wasActive || sub !== currentOpenKey) void openDetail(sub);
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
    // to this list while it shows.
    listKeys: () => ({ nav: handle, selects: true }),
    keys: () => listKeys,
    onLive(type: string, data: unknown) {
      if (type === 'gap') {
        // Events this page never heard were pruned: reload all it shows
        // (the counts, the list, and the open item).
        refreshSummary();
        void reload();
        if (currentOpenKey && currentOpenKey !== deciding) {
          void openDetail(currentOpenKey);
        }
      } else if (type === 'index') {
        // One reload per index event: apply counts from the event payload and
        // refresh the list.
        const s = data as { counts?: Record<string, number> };
        applyCounts(s?.counts ?? null);
        // The recommended line's counts change with the index too.
        refreshSummary();
        void reload();
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
        void reload();
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
        void reload();
      } else if (type === 'evidence') {
        // New evidence for the open item: shown as a recommendation is.
        const key = (data as { key?: string })?.key ?? '';
        if (
          currentOpenKey &&
          currentOpenKey !== deciding &&
          key === currentOpenKey
        )
          void proposalsChanged(currentOpenKey, 'evidence');
      } else if (type === 'message') {
        // Court's message posted (this tab or another): an ask to look into
        // an item marks it.
        const keys = (data as Message | null)?.attached?.keys ?? [];
        if (
          keys.some(
            (k) => k === currentOpenKey || shown.some((it) => it.key === k),
          )
        )
          lookingMayHaveChanged();
      } else if (
        type === 'messages' ||
        type === 'settled' ||
        type === 'delivery' ||
        type === 'interrupted'
      ) {
        // A message settled (or a turn ended): a marker may go.
        if (lookingShown()) lookingMayHaveChanged();
      }
    },
    primary() {
      return decidePrimary;
    },
  };
}
