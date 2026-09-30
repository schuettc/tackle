// rules.ts — the Rules section (spec §4.3): the list of standing rules and a
// rule as a document in the reading column.
//
// The list (kit list) shows every rule with its status pill and its track
// record ("87 accepted · 4 rejected · 3 pending"); views all / active /
// drafts. #/rules/<id> opens a rule: kicker, title, facts (status, matches,
// excluded, author), what a draft or an active rule does, the conditions
// table with `+ condition` (conditions.ts), the proposal, and the live
// matches: "matches now · N · <by reason>", paginated, groupable by repo.
// Unticking a match adds an exclusion (with an optional reason); ticking an
// excluded one includes it again.
//
// Editing a draft's conditions re-previews them against the live index,
// debounced by 300 ms (POST /api/rules/preview; nothing is saved). A
// condition serve refuses shows serve's message under it, and the matches
// wait for a valid rule rather than showing a preview of a broken one.
// `save draft` saves the edits; `propose once` turns the current matches into
// proposals. The bar's primary follows the rule: Activate on a draft,
// Deactivate on an active rule. An agent's draft shows its author, and only
// Court's Activate (this page) activates it: serve refuses an agent's.
//
// While Rules is the active section it feeds the composer {rule: <id>} for
// the open rule and {} for none; hidden, it never touches the attached line
// or the bar's primary, though it keeps itself current from live events.

import {
  list,
  h,
  facts,
  buttons,
  sheet,
  ApiError,
  type Button,
  type Chip,
  type KeyBinding,
  type ListHandle,
  type Primary,
} from '/_kit/kit.js';
import type {
  Condition,
  Exclusion,
  MatchPreview,
  MatchRow,
  ProposeResult,
  Rule,
  RuleDetailView,
  RuleRow,
  RulesView,
} from './wire.d.ts';
import type { Ctx, Section } from './app.ts';
import { conditionEditor, setConditionError } from './conditions.ts';
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { DANGER_DISPS } from './decide.ts';
import {
  authorOf,
  conditionErrorIndex,
  matchesHeading,
  reasonSummary,
  sameConditions,
  sameRule,
  trackRecord,
  viewCounts,
} from './rules-text.ts';

/** The re-preview debounce (spec §4.3). */
export const PREVIEW_DEBOUNCE_MS = 300;

// Matches are fetched 200 at a time, like the Attention list.
const PAGE = 200;

type View = 'all' | 'active' | 'drafts';
const VIEWS: Array<{ id: View; label: string }> = [
  { id: 'all', label: 'all' },
  { id: 'active', label: 'active' },
  { id: 'drafts', label: 'drafts' },
];

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// ---- the rule document -----------------------------------------------------

/** What the section needs from an open rule's document. */
export interface RuleDoc {
  el: HTMLElement;
  id: string;
  /** Unsaved condition edits. */
  dirty(): boolean;
  /** The bar's primary for this rule: Activate or Deactivate. */
  primary(): Primary | null;
  /** serve's current rule (a live change): redraw unless Court is editing. */
  update(detail: RuleDetailView): void;
  /** The index changed: preview again, now. */
  repreview(): void;
}

export interface RuleHooks {
  /** The primary changed (status, or edits to save first). */
  primaryChanged(): void;
  /** serve saved something (edit, exclusion, lifecycle): refresh the list. */
  saved(): void;
}

/**
 * renderRule draws a rule as a document and keeps it live: edits
 * re-preview, exclusions and lifecycle post to serve.
 */
export function renderRule(
  ctx: Ctx,
  detail: RuleDetailView,
  hooks: RuleHooks,
): RuleDoc {
  const id = detail.rule.id;
  let base: Rule = detail.rule; // serve's saved rule
  let work: Condition[] = [...(base.match ?? [])]; // Court's edits
  let exclude: Exclusion[] = base.exclude ?? [];
  let preview: MatchPreview = detail.matches;
  let shown: MatchRow[] = preview.page ?? [];
  let invalid: string | null = null;
  let grouped = false;
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let busy = false;

  const doc = h('article', { class: 'cb-rule', 'data-rule': id });
  const el = h('div', { class: 'kit-doc' }, doc);

  const dirty = () => !sameConditions(work, base.match ?? []);
  const isDraft = () => base.status !== 'active';
  const body = (): Rule => ({ ...base, match: work, exclude });

  // ---- parts that update in place ----

  let factsEl = h('div');
  let editor = h('div');
  const heading = h('h3', { class: 'kit-label cb-matches-label' });
  const groupChips = h('span', { class: 'cb-matches-view' });
  const matchesEl = h('div', {
    class: 'kit-table cb-matches',
    'data-testid': 'matches',
  });
  const actionsEl = h('div', { class: 'cb-rule-actions' });
  const note = h('p', { class: 'cb-rule-note', role: 'status' });

  function drawFacts(): void {
    const who = authorOf(base.created_by);
    const byEl = h('b', who.agent ? { class: 'cb-by-agent' } : null, who.name);
    const next = facts([
      ['status', base.status],
      ['matches', invalid ? '\u2014' : String(preview.total)],
      ['excluded', String(exclude.length)],
      ['by', byEl],
    ]);
    factsEl.replaceWith(next);
    factsEl = next;
  }

  function prose(): HTMLElement {
    const who = authorOf(base.created_by);
    const lifecycle =
      'Once active, casebook proposes new matches after every sync, and never overrides an item that\u2019s already decided.';
    if (!isDraft()) {
      return h(
        'p',
        null,
        'Active: casebook proposes new matches after every sync, and never overrides an item that\u2019s already decided.',
      );
    }
    if (who.agent) {
      return h(
        'p',
        null,
        h('span', { class: 'cb-by-agent' }, who.name),
        ' drafted this rule. It proposes nothing until you activate it, and only you can. ',
        lifecycle,
      );
    }
    return h(
      'p',
      null,
      'A draft proposes nothing until you activate it. ',
      lifecycle,
    );
  }

  function proposalTable(): HTMLElement {
    const p = base.propose;
    const tr = (label: string, value: string, cls = '') =>
      h(
        'div',
        { class: 'kit-tr' },
        h('span', { class: 'cb-cond-f' }, label),
        h('span'),
        h('span', { class: 'cb-cond-v' + cls }, value),
      );
    return h(
      'div',
      { class: 'kit-table cb-propose', 'data-testid': 'propose' },
      tr(
        'disposition',
        p.disposition,
        DANGER_DISPS.has(p.disposition) ? ' cb-danger' : '',
      ),
      p.until ? tr('until', p.until) : null,
      p.note ? tr('note', p.note) : null,
    );
  }

  // ---- matches ----

  function box(on: boolean, label: string, run: () => void): HTMLElement {
    return h('span', {
      class: 'kit-box',
      role: 'checkbox',
      tabindex: 0,
      'aria-checked': String(on),
      'aria-label': label,
      onclick(e: Event) {
        e.stopPropagation();
        run();
      },
      onkeydown(e: KeyboardEvent) {
        if (e.key === ' ' || e.key === 'Enter') {
          e.preventDefault();
          run();
        }
      },
    });
  }

  function matchRow(m: MatchRow): HTMLElement {
    const why =
      m.reason === 'matched' ? m.title || m.status : m.reason.toLowerCase();
    return h(
      'div',
      { class: 'cb-mr on', 'data-key': m.key, title: m.title || m.key },
      box(true, `exclude ${m.key}`, () => untick(m.key)),
      h('span', { class: 'cb-mr-k' }, keyWithoutKind(m.key)),
      h('span', { class: 'cb-mr-w' }, why),
    );
  }

  function excludedRow(x: Exclusion): HTMLElement {
    return h(
      'div',
      { class: 'cb-mr x', 'data-key': x.key },
      box(false, `include ${x.key}`, () => void include(x.key)),
      h('span', { class: 'cb-mr-k' }, keyWithoutKind(x.key)),
      h(
        'span',
        { class: 'cb-mr-w' },
        x.reason ? `excluded \u00b7 ${x.reason}` : 'excluded',
      ),
    );
  }

  function drawMatches(): void {
    matchesEl.removeAttribute('data-invalid');
    if (invalid) {
      heading.textContent = 'matches now';
      const n = conditionErrorIndex(invalid);
      matchesEl.setAttribute('data-invalid', '');
      matchesEl.replaceChildren(
        h(
          'div',
          { class: 'cb-mr cb-mr-wait' },
          h('span'),
          h(
            'span',
            { class: 'cb-mr-k' },
            n >= 0
              ? `not previewed: condition ${n + 1} is not valid`
              : 'not previewed: the rule is not valid',
          ),
          h('span'),
        ),
      );
      groupChips.hidden = true;
      return;
    }
    heading.textContent = matchesHeading(preview.total, preview.by_reason);
    groupChips.hidden = false;
    const out: HTMLElement[] = [];
    if (grouped) {
      const rest = [...shown];
      for (const g of preview.groups ?? []) {
        const mine = rest.filter((m) => (m.repo || m.key) === g.repo);
        for (const m of mine) rest.splice(rest.indexOf(m), 1);
        const why = reasonSummary(g.reasons);
        out.push(
          h(
            'div',
            { class: 'cb-mgroup', 'data-repo': g.repo },
            h('span', { class: 'cb-mgroup-repo' }, g.repo),
            h(
              'span',
              { class: 'cb-mr-w' },
              String(g.count) + (why ? ` \u00b7 ${why}` : ''),
            ),
          ),
          ...mine.map(matchRow),
        );
      }
      out.push(...rest.map(matchRow));
    } else {
      out.push(...shown.map(matchRow));
    }
    out.push(...exclude.map(excludedRow));
    const more = preview.total - shown.length;
    if (more > 0) {
      out.push(
        h(
          'div',
          { class: 'cb-mr cb-mr-more' },
          h('span'),
          h('span', { class: 'cb-mr-k' }, `\u2026 ${more} more`),
          h(
            'button',
            {
              type: 'button',
              class: 'cb-link',
              onclick() {
                void loadMore();
              },
            },
            `show ${Math.min(PAGE, more)} more`,
          ),
        ),
      );
    }
    if (out.length === 0) {
      out.push(
        h(
          'div',
          { class: 'cb-mr cb-mr-wait' },
          h('span'),
          h('span', { class: 'cb-mr-k' }, 'nothing matches now'),
          h('span'),
        ),
      );
    }
    matchesEl.replaceChildren(...out);
  }

  function drawGroupChips(): void {
    const chip = (label: string, on: boolean) =>
      h(
        'button',
        {
          type: 'button',
          class: 'kit-chip' + (on ? ' on' : ''),
          'aria-pressed': String(on),
          onclick() {
            grouped = label === 'by repo';
            drawGroupChips();
            drawMatches();
          },
        },
        label,
      );
    groupChips.replaceChildren(
      chip('list', !grouped),
      chip('by repo', grouped),
    );
  }

  // setPreview takes a fresh first page of matches.
  function setPreview(p: MatchPreview): void {
    invalid = null;
    setConditionError(editor, -1, null);
    preview = p;
    shown = p.page ?? [];
    drawMatches();
    drawFacts();
  }

  function setInvalid(msg: string): void {
    invalid = msg;
    setConditionError(editor, conditionErrorIndex(msg), msg);
    drawMatches();
    drawFacts();
  }

  // ---- re-preview ----

  async function previewNow(): Promise<void> {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    const mine = ++seq;
    try {
      const p = await ctx.api.post<MatchPreview>('/rules/preview', body());
      if (mine === seq) setPreview(p);
    } catch (err) {
      if (mine !== seq) return;
      if (err instanceof ApiError && err.status === 400)
        setInvalid(err.message);
      else note.textContent = `preview failed: ${message(err)}`;
    }
  }

  function schedule(): void {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      void previewNow();
    }, PREVIEW_DEBOUNCE_MS);
  }

  async function loadMore(): Promise<void> {
    const q = `?offset=${shown.length}&limit=${PAGE}`;
    const mine = seq;
    try {
      const p = await ctx.api.post<MatchPreview>(`/rules/preview${q}`, body());
      if (mine !== seq) return;
      shown = [...shown, ...(p.page ?? [])];
      preview = { ...p, page: shown };
      drawMatches();
    } catch (err) {
      note.textContent = `more matches failed: ${message(err)}`;
    }
  }

  // ---- exclusions ----

  // applyServe takes serve's rule after an exclusion or an inclusion: the
  // exclusions are serve's; the matches are serve's unless Court has
  // unsaved edits, which are previewed again with the new exclusions.
  function applyExclusions(d: RuleDetailView): void {
    exclude = d.rule.exclude ?? [];
    base = { ...base, exclude };
    if (dirty()) {
      void previewNow();
    } else {
      seq++; // a preview in flight is older than this
      setPreview(d.matches);
    }
    hooks.saved();
  }

  function untick(key: string): void {
    const reason = h('input', {
      class: 'kit-note',
      type: 'text',
      placeholder: 'reason (optional)',
      'aria-label': 'reason',
    }) as HTMLInputElement;
    const err = h('p', { class: 'cb-sheet-err', hidden: true });
    let sending = false;
    const go = async () => {
      if (sending) return;
      sending = true;
      try {
        const d = await ctx.api.post<RuleDetailView>('/rules/exclude', {
          id,
          key,
          reason: reason.value.trim(),
        });
        sh.close();
        applyExclusions(d);
      } catch (e) {
        err.textContent = message(e);
        err.hidden = false;
        sending = false;
      }
    };
    reason.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.key === 'Enter' && !e.isComposing) {
        e.preventDefault();
        void go();
      }
    });
    const sh = sheet({
      title: `exclude ${keyWithoutKind(key)}`,
      body: h(
        'div',
        { class: 'cb-sheet-body' },
        h(
          'p',
          { class: 'cb-sheet-preview' },
          'This rule skips it from now on. It stays listed here, unticked.',
        ),
        h(
          'div',
          { class: 'cb-sheet-row' },
          h('label', { class: 'cb-sheet-label' }, 'reason'),
          reason,
        ),
        err,
      ),
      actions: [
        { label: 'exclude', fill: true, run: () => void go() },
        { label: 'cancel', run: () => sh.close() },
      ],
    });
    sh.el.dataset.testid = 'exclude-sheet';
    reason.focus();
  }

  async function include(key: string): Promise<void> {
    try {
      const d = await ctx.api.post<RuleDetailView>('/rules/include', {
        id,
        key,
      });
      applyExclusions(d);
    } catch (err) {
      note.textContent = `not included: ${message(err)}`;
    }
  }

  // ---- saving and lifecycle ----

  // save posts Court's edits; false (with serve's message shown) on refusal.
  async function save(): Promise<boolean> {
    try {
      const d = await ctx.api.post<RuleDetailView>('/rules/draft', body());
      draw(d);
      hooks.saved();
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.status === 400)
        setInvalid(err.message);
      else note.textContent = `not saved: ${message(err)}`;
      return false;
    }
  }

  async function lifecycle(verb: 'activate' | 'deactivate'): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      if (verb === 'activate' && dirty() && !(await save())) return;
      const d = await ctx.api.post<RuleDetailView>(`/rules/${verb}`, { id });
      draw(d);
      hooks.saved();
    } catch (err) {
      note.textContent = `not ${verb}d: ${message(err)}`;
    } finally {
      busy = false;
    }
  }

  async function proposeOnce(): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      if (dirty() && !(await save())) return;
      const r = await ctx.api.post<ProposeResult>('/rules/propose-once', {
        id,
      });
      note.replaceChildren(
        `${pluralize(r.proposed, 'match', 'matches')} proposed \u00b7 `,
        h(
          'button',
          {
            type: 'button',
            class: 'cb-link',
            onclick() {
              ctx.route.go('attention', 'proposed');
            },
          },
          'see them in attention',
        ),
      );
      hooks.saved();
    } catch (err) {
      note.textContent = `not proposed: ${message(err)}`;
    } finally {
      busy = false;
    }
  }

  function drawActions(): void {
    const bs: Button[] = [];
    if (isDraft()) {
      if (dirty()) bs.push({ label: 'save draft', run: () => void save() });
      bs.push({ label: 'propose once', run: () => void proposeOnce() });
    }
    actionsEl.replaceChildren(bs.length ? buttons(bs) : '', note);
  }

  function onEdit(m: Condition[]): void {
    const was = dirty();
    work = m;
    note.textContent = '';
    schedule();
    if (dirty() !== was) {
      drawActions();
      hooks.primaryChanged();
    }
  }

  // draw lays the whole document out from serve's rule.
  function draw(d: RuleDetailView): void {
    if (timer !== null) clearTimeout(timer);
    timer = null;
    seq++;
    base = d.rule;
    work = [...(base.match ?? [])];
    exclude = base.exclude ?? [];
    preview = d.matches;
    shown = preview.page ?? [];
    invalid = null;
    doc.dataset.status = base.status;
    editor = conditionEditor(ctx, base, onEdit);
    factsEl = h('div');
    const head = h('div', { class: 'cb-matches-head' }, heading, groupChips);
    doc.replaceChildren(
      h(
        'p',
        { class: 'kit-kick' },
        `rule \u00b7 ${base.status} \u00b7 rules/${id}.toml`,
      ),
      h('h1', { class: 'kit-h1' }, base.name || id),
      factsEl,
      prose(),
      h('h3', { class: 'kit-label' }, 'when an undecided item matches all of'),
      editor,
      h('h3', { class: 'kit-label' }, 'propose'),
      proposalTable(),
      head,
      matchesEl,
      actionsEl,
    );
    note.textContent = '';
    drawFacts();
    drawGroupChips();
    drawMatches();
    drawActions();
    hooks.primaryChanged();
  }

  draw(detail);

  return {
    el,
    id,
    dirty,
    primary() {
      return isDraft()
        ? { label: 'Activate', run: () => void lifecycle('activate') }
        : { label: 'Deactivate', run: () => void lifecycle('deactivate') };
    },
    update(d) {
      if (dirty()) {
        // Court is editing: keep his edits, take serve's exclusions.
        exclude = d.rule.exclude ?? [];
        base = { ...d.rule, match: base.match };
        void previewNow();
        return;
      }
      if (!sameRule(d.rule, base) || d.matches.total !== preview.total) {
        draw(d);
      }
    },
    repreview() {
      void previewNow();
    },
  };
}

// ---- the section -----------------------------------------------------------

export function makeRules(ctx: Ctx): Section {
  const readEl = h('div', { class: 'kit-read cb-rules-read' });
  let rows: RuleRow[] = [];
  let view: View = 'all';
  let active = false;
  let openId: string | null = null;
  let doc: RuleDoc | null = null;
  let opening = 0;
  let painting = false;

  const statusOf = (r: RuleRow) => r.rule.status;
  const inView = (r: RuleRow) =>
    view === 'all' ||
    (view === 'active' ? statusOf(r) === 'active' : statusOf(r) !== 'active');

  const handle: ListHandle<RuleRow> = list<RuleRow>({
    label: 'rules',
    views: VIEWS.map((v) => ({ ...v, on: v.id === view })),
    openOnMove: false,
    row(r: RuleRow) {
      const who = authorOf(r.rule.created_by);
      const draft = r.rule.status !== 'active';
      return {
        id: r.rule.id,
        key: trackRecord(r.record),
        title: r.rule.name || r.rule.id,
        sub:
          who.agent && draft
            ? `drafted by ${who.name} \u00b7 awaiting your review`
            : undefined,
        meta: r.rule.status,
      };
    },
    onChip(group, id) {
      if (group !== 'view') return;
      view = id as View;
      paintList();
    },
    onOpen(r: RuleRow) {
      if (painting) return; // paintList marking the open row
      ctx.route.go('rules', r.rule.id);
    },
  });
  handle.el.classList.add('cb-rules-list');

  // paintList draws the list and marks each row's status as a pill (the
  // kit's row has no pill slot: its meta span takes the kit's pill classes).
  function paintList(): void {
    const c = viewCounts(rows.map((r) => r.rule));
    const shown = rows.filter(inView);
    handle.setChips(
      'view',
      VIEWS.map((v): Chip => ({ ...v, on: v.id === view, count: c[v.id] })),
    );
    handle.setItems(shown);
    const els = handle.el.querySelectorAll<HTMLElement>('.kit-row');
    els.forEach((rowEl, i) => {
      const r = shown[i];
      if (!r) return;
      rowEl.dataset.rule = r.rule.id;
      const meta = rowEl.querySelector('.kit-meta');
      meta?.classList.add(
        'kit-pill',
        r.rule.status === 'active' ? 'ok' : 'wait',
      );
    });
    // The open rule's row reads as open (a route, not a click, may have
    // opened it).
    const openAt = shown.findIndex((r) => r.rule.id === openId);
    if (openAt >= 0) {
      painting = true;
      try {
        handle.open(openAt);
      } finally {
        painting = false;
      }
    }
    ctx.bar.setCount('rules', c.all);
    if (!doc && !openId) drawEmpty();
  }

  async function loadList(): Promise<void> {
    try {
      const v = await ctx.api.get<RulesView>('/rules');
      rows = v.rules ?? [];
      paintList();
    } catch {
      // non-fatal; the list stays as it was
    }
  }

  function drawEmpty(): void {
    readEl.replaceChildren(
      h(
        'div',
        { class: 'cb-read-empty' },
        h('p', { class: 'cb-read-empty-section kit-label' }, 'rules'),
        h(
          'p',
          { class: 'cb-read-empty-count' },
          pluralize(rows.length, 'rule'),
        ),
        h(
          'p',
          { class: 'cb-read-empty-prompt' },
          'Select a rule to see it here.',
        ),
      ),
    );
  }

  // feed tells the composer and the bar about the open rule, only while
  // Rules is the active section.
  function feed(): void {
    if (!active) return;
    ctx.setAttached(openId ? { rule: openId } : {});
    ctx.setPrimary(doc ? doc.primary() : null);
  }

  const hooks: RuleHooks = {
    primaryChanged() {
      if (active) ctx.setPrimary(doc ? doc.primary() : null);
    },
    saved() {
      void loadList();
    },
  };

  async function open(id: string): Promise<void> {
    const mine = ++opening;
    try {
      const d = await ctx.api.get<RuleDetailView>('/rule', { id });
      if (mine !== opening || openId !== id) return;
      doc = renderRule(ctx, d, hooks);
      readEl.replaceChildren(doc.el);
      readEl.scrollTop = 0;
      feed();
    } catch (err) {
      if (mine !== opening) return;
      doc = null;
      readEl.replaceChildren(
        h(
          'div',
          { class: 'cb-read-empty' },
          h('p', { class: 'cb-read-empty-section kit-label' }, 'rules'),
          h('p', { class: 'cb-read-empty-prompt' }, message(err)),
        ),
      );
      feed();
    }
  }

  function close(): void {
    opening++;
    openId = null;
    doc = null;
    drawEmpty();
    feed();
  }

  // "a" activates the open draft: the bar's primary, from the keyboard. Like
  // every section key it is bound only while Rules is shown (Attention's "a"
  // accepts a proposal).
  const activateKey: KeyBinding = {
    keys: 'a',
    label: 'activate the open draft',
    group: 'page',
    run() {
      if (!doc) return;
      const p = doc.primary();
      if (p?.label === 'Activate') p.run();
    },
  };

  void loadList();

  return {
    id: 'rules',
    list: handle.el,
    read: readEl,
    keys: [activateKey],
    show(sub: string) {
      active = true;
      const id = sub;
      if (!id) {
        if (openId) close();
        else feed();
      } else if (id !== openId) {
        openId = id;
        doc = null;
        feed();
        void open(id);
      } else {
        feed();
      }
      paintList();
    },
    hide() {
      active = false;
    },
    onLive(type: string, data: unknown) {
      if (type === 'rules') {
        void loadList();
        const ev = data as { id?: string };
        if (doc && openId && (!ev?.id || ev.id === openId)) {
          const d = doc;
          void ctx.api
            .get<RuleDetailView>('/rule', { id: openId })
            .then((detail) => {
              if (doc === d) d.update(detail);
            })
            .catch(() => {});
        }
      } else if (type === 'index' || type === 'decided') {
        // The live index moved: the open rule's matches are recomputed.
        doc?.repreview();
        if (type === 'index') void loadList();
      } else if (type === 'proposals') {
        void loadList(); // track records
      }
    },
    primary() {
      return doc ? doc.primary() : null;
    },
  };
}
