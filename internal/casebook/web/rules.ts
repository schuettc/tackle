// rules.ts — the Rules section (spec §4.3): the list of standing rules and a
// rule as a document in the reading column.
//
// The list (kit list) shows every rule with what it matches and excludes now
// and its track record ("849 match · 2 excluded · 3 pending"), and its status
// pill (draft, active, or not valid); views all / active / drafts. Its foot
// makes a new draft (`new rule`: an id and a name) or asks the agent to draft
// one (the composer, with `attached: section rules`). #/rules/<id> opens a rule:
// kicker, title, facts (status, matches, excluded, author), what a draft or
// an active rule does, the conditions table with `+ condition`
// (conditions.ts), the proposal, and the live matches: "matches now · N ·
// <by reason>", paginated, groupable by repo. Unticking a match adds an
// exclusion (with an optional reason); ticking an excluded one includes it.
//
// A draft's conditions and proposal are editable. Editing the conditions
// re-previews them against the live index, debounced by 300 ms (POST
// /api/rules/preview; nothing is saved). serve validates: a condition it
// refuses shows serve's message under it, and the matches wait for a valid
// rule rather than showing a preview of a broken one. A rule serve holds as
// invalid (a hand-written file, an older value, a draft not finished) says
// so, in the list and in its document; an active one says casebook skips it,
// and Court fixes it (and saves it, as a draft) or deactivates it.
//
// What Court activates is what the page shows: activate and save carry the
// rule's version, and serve refuses (409) a copy that changed. A live change
// that arrives while Court has unsaved edits doesn't replace them silently:
// the document shows the change, who made it and serve's copy, and Activate
// waits until he reloads theirs or keeps his own over it.
//
// While Rules is the active section it feeds the composer {rule: <id>} for
// the open rule (once it has loaded), {section: 'rules'} when he asks the
// agent from the foot, and {} otherwise; hidden, it never touches the
// attached line or the bar's primary, though it keeps itself current from
// live events.

import {
  list,
  h,
  facts,
  buttons,
  card,
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
  RuleAction,
  RuleDetailView,
  RuleRow,
  RulesView,
} from './wire.d.ts';
import type { Ctx, Section } from './app.ts';
import {
  conditionEditor,
  noteTokens,
  setConditionError,
} from './conditions.ts';
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { DANGER_DISPS, getVocab } from './decide.ts';
import { menuNav } from './menu-nav.ts';
import {
  authorOf,
  conditionErrorIndex,
  kindHint,
  matchesHeading,
  refusedDisposition,
  reasonSummary,
  rowKicker,
  sameAction,
  sameConditions,
  sameRule,
  viewCounts,
} from './rules-text.ts';

// Between the items of a proposal hint: the dot stays with the item before.
const HINT_SEP = '\u00a0\u00b7 ';

/** The re-preview debounce (spec §4.3). */
export const PREVIEW_DEBOUNCE_MS = 300;

// Matches are fetched 200 at a time, like the Attention list (serve's cap).
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

const isConflict = (err: unknown) =>
  err instanceof ApiError && err.status === 409;

// A rule id from a name: lower case, a-z 0-9 and hyphens (serve's rule).
function slug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60);
}

// ---- the rule document -----------------------------------------------------

/** What the section needs from an open rule's document. */
export interface RuleDoc {
  el: HTMLElement;
  id: string;
  /** Unsaved edits to the conditions or the proposal. */
  dirty(): boolean;
  /** The bar's primary for this rule: Activate or Deactivate. */
  primary(): Primary | null;
  /** serve's current rule (a live change), and who changed it if known. */
  update(detail: RuleDetailView, by: string): void;
  /** The index changed: preview again (debounced), keeping the depth shown. */
  refresh(): void;
}

export interface RuleHooks {
  /** The primary changed (status, edits, a conflict). */
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
  let saved: RuleDetailView = detail; // serve's copy, as shown
  let base: Rule = detail.rule;
  let work: Condition[] = [...(base.match ?? [])]; // Court's edits
  let workPropose: RuleAction = { ...base.propose };
  let exclude: Exclusion[] = base.exclude ?? [];
  let preview: MatchPreview = detail.matches;
  // What serve says the rule may propose, for its conditions (with each
  // preview; kept while the conditions are refused).
  let dispositions: string[] = detail.matches.dispositions ?? [];
  let shown: MatchRow[] = preview.page ?? [];
  let invalid: string | null = null; // the conditions, as serve judged them
  let conflict: { detail: RuleDetailView; by: string } | null = null;
  let grouped = false;
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  // How many rows Court has asked to see: "show more" raises it, an edit
  // starts again at a page, and so does serve's copy of the rule changing
  // (draw, applyExclusions). A preview in flight reads it as it pages, so a
  // re-preview landing while "show more" loads still brings what he asked
  // for; loadMore's own answer is then dropped (older, or its rows already
  // shown).
  let opened = PAGE;
  // One request at a time (Activate, Deactivate, propose once), in the order
  // Court asked. serve announces a change before it replies, so the page can
  // show the next primary while a request is still in flight; a click on it
  // then waits for the reply and runs after it. No click is dropped.
  let queue: Promise<void> = Promise.resolve();
  const inTurn = (task: () => Promise<void>): Promise<void> => {
    const run = queue.then(task);
    queue = run.catch(() => {});
    return run;
  };

  const doc = h('article', { class: 'cb-rule', 'data-rule': id });
  const el = h('div', { class: 'kit-doc' }, doc);

  const dirty = () =>
    !sameConditions(work, base.match ?? []) ||
    !sameAction(workPropose, base.propose);
  const isDraft = () => base.status !== 'active';
  // An active rule is edited only while serve holds it invalid (to fix it).
  const editable = () => isDraft() || !!saved.invalid;
  const body = (): Rule => ({
    ...base,
    match: work,
    propose: workPropose,
    exclude,
  });

  // ---- parts that update in place ----

  let factsEl = h('div');
  let editor = h('div');
  const titleEl = h('h1', { class: 'kit-h1' });
  const kickEl = h('p', { class: 'kit-kick' });
  const proseEl = h('p');
  const invalidEl = h('div', { 'data-testid': 'rule-invalid' });
  const conflictEl = h('div', { 'data-testid': 'rule-conflict' });
  const proposeEl = h('div');
  const proposeErrEl = h('p', {
    class: 'cb-cond-err cb-propose-err',
    'data-testid': 'propose-err',
    hidden: true,
  });
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
      ['status', saved.invalid ? `${base.status} · not valid` : base.status],
      ['matches', invalid ? '\u2014' : String(preview.total)],
      ['excluded', String(exclude.length)],
      ['by', byEl],
    ]);
    factsEl.replaceWith(next);
    factsEl = next;
  }

  function drawProse(): void {
    const who = authorOf(base.created_by);
    const lifecycle =
      'Once active, casebook proposes new matches after every sync, and never overrides an item that\u2019s already decided.';
    if (!isDraft()) {
      proseEl.replaceChildren(
        'Active: casebook proposes new matches after every sync, and never overrides an item that\u2019s already decided.',
      );
    } else if (who.agent) {
      proseEl.replaceChildren(
        h('span', { class: 'cb-by-agent' }, who.name),
        ' drafted this rule. It proposes nothing until you activate it, and only you can. ',
        lifecycle,
      );
    } else {
      proseEl.replaceChildren(
        'A draft proposes nothing until you activate it. ',
        lifecycle,
      );
    }
  }

  // The card for a rule serve holds as invalid: its message, and for an
  // active rule that casebook skips it (rebuild does, with a notice).
  function drawInvalid(): void {
    if (!saved.invalid) {
      invalidEl.replaceChildren();
      return;
    }
    const skipped = base.status === 'active';
    invalidEl.replaceChildren(
      card({
        edge: 'signal',
        head: skipped
          ? 'not valid \u00b7 active, but skipped at every sync'
          : 'not valid',
        body: h(
          'div',
          null,
          h('p', { class: 'cb-invalid-msg' }, saved.invalid),
          h(
            'p',
            { class: 'cb-invalid-help' },
            skipped
              ? 'casebook proposes nothing from this rule until it is valid. Fix it and save it (it becomes a draft), or deactivate it.'
              : 'It can\u2019t be activated or propose anything until it is valid.',
          ),
        ),
      }),
    );
  }

  // The proposal: a draft's is editable (disposition, until, note).
  //
  // A draft's picker is built once and kept across redraws (a live preview,
  // or serve's copy of the rule drawn again): an open disposition menu
  // stays open with its chips, and Court's focus, where they were, so a
  // click on a chip never lands on one a redraw has just replaced.
  function drawPropose(): void {
    const p = workPropose;
    if (editable() && refreshPropose) {
      refreshPropose();
      return;
    }
    refreshPropose = null;
    const tr = (label: string, value: Node) =>
      h(
        'div',
        { class: 'kit-tr' },
        h('span', { class: 'cb-cond-f' }, label),
        h('span'),
        value,
      );
    if (!editable()) {
      drawChoices = () => {};
      proposeEl.replaceChildren(
        h(
          'div',
          { class: 'kit-table cb-propose', 'data-testid': 'propose' },
          tr(
            'disposition',
            h(
              'span',
              {
                class:
                  'cb-cond-v' +
                  (DANGER_DISPS.has(p.disposition) ? ' cb-danger' : ''),
              },
              p.disposition,
            ),
          ),
          p.until
            ? tr('until', h('span', { class: 'cb-cond-v' }, p.until))
            : null,
          p.note ? tr('note', h('span', { class: 'cb-cond-v' }, p.note)) : null,
        ),
      );
      return;
    }
    // The disposition: a picker of what serve says this rule may propose
    // (the dispositions every kind it can match allows; it follows the
    // conditions with each preview), open in the table when chosen.
    const pick = h('button', {
      type: 'button',
      class: 'cb-cond-v cb-disp',
      'aria-label': 'disposition',
      'aria-haspopup': 'menu',
      'aria-expanded': 'false',
      onclick() {
        setMenu(menu.hidden);
      },
    });
    const menu = h('div', {
      class: 'cb-disp-menu',
      role: 'menu',
      'aria-label': 'dispositions',
      hidden: true,
    });
    const menuChips = h('span', { class: 'cb-disp-opts' });
    // Why only some are offered: a rule with no kind condition may propose
    // only what every kind of item allows (serve's rule).
    const whyEl = h('span', {
      class: 'cb-disp-why',
      'data-testid': 'disp-why',
    });
    menu.append(
      h('span'),
      h('span'),
      h('span', { class: 'cb-disp-col' }, menuChips, whyEl),
    );
    // A chip moved or dropped by a redraw loses focus as it leaves the
    // document; that is no leaving the menu (redrawing says so).
    menuNav(menu, '.cb-disp-opt', pick, () => {
      if (!redrawing) setMenu(false);
    });
    const onEsc = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.isComposing) return;
      if (!menu.isConnected) {
        document.removeEventListener('keydown', onEsc, true);
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      setMenu(false);
      pick.focus();
    };
    function setMenu(open: boolean): void {
      menu.hidden = !open;
      pick.setAttribute('aria-expanded', String(open));
      if (open) {
        document.addEventListener('keydown', onEsc, true);
        (
          menu.querySelector<HTMLElement>('.cb-disp-opt.on') ??
          menu.querySelector<HTMLElement>('.cb-disp-opt')
        )?.focus();
      } else {
        document.removeEventListener('keydown', onEsc, true);
      }
    }
    // until is for the dispositions that wait (wait, watch): its row shows
    // for those, or when the rule already has one.
    const needsUntil = () =>
      !!workPropose.until || /^(wait|watch)$/.test(workPropose.disposition);
    const choose = (d: string) => {
      workPropose = { ...workPropose, disposition: d };
      setMenu(false);
      drawChoices();
      untilRow.hidden = untilHint.hidden = !needsUntil();
      edited();
      pick.focus();
    };
    // The chips drawn. A preview offering another list (a kind condition
    // added) keeps the chip of each disposition still offered, the same
    // element, in place: only the ones it drops leave and the new ones
    // join. A chip with Court's focus that leaves hands it to the chosen
    // one (or the first), so an open menu stays open.
    let chipsFor = '';
    drawChoices = () => {
      const cur = workPropose.disposition;
      pick.textContent = cur || 'choose\u2026';
      pick.dataset.value = cur;
      pick.classList.toggle('cb-danger', DANGER_DISPS.has(cur));
      whyEl.textContent = kindHint(work, dispositions, allDispositions);
      drawProposeErr();
      const sig = `${cur}\n${dispositions.join(' ')}`;
      if (sig === chipsFor) return;
      chipsFor = sig;
      const have = new Map(
        [...menuChips.querySelectorAll<HTMLElement>('.cb-disp-opt')].map(
          (e) => [e.dataset.disp ?? '', e],
        ),
      );
      const chips = dispositions.map((d) => {
        const chip =
          have.get(d) ??
          h(
            'button',
            {
              type: 'button',
              role: 'menuitemradio',
              'data-disp': d,
              onclick() {
                choose(d);
              },
            },
            d,
          );
        chip.setAttribute('aria-checked', String(d === cur));
        chip.className =
          'kit-chip cb-disp-opt' +
          (d === cur ? ' on' : '') +
          (DANGER_DISPS.has(d) ? ' cb-danger' : '');
        return chip;
      });
      const focused = document.activeElement;
      if (
        focused instanceof HTMLElement &&
        menuChips.contains(focused) &&
        !chips.includes(focused)
      ) {
        (
          chips.find((c) => c.classList.contains('on')) ??
          chips[0] ??
          pick
        ).focus();
      }
      keepFocus(() => {
        for (const [d, chip] of have) {
          if (!chips.includes(chip)) {
            have.delete(d);
            chip.remove();
          }
        }
        // In order: a kept chip already in place is never moved.
        chips.forEach((chip, i) => {
          const at = menuChips.children[i];
          if (at !== chip) menuChips.insertBefore(chip, at ?? null);
        });
      });
    };
    const text = (field: 'until' | 'note', placeholder: string) => {
      const input = h('input', {
        class: 'cb-cond-v',
        type: 'text',
        value: p[field] ?? '',
        placeholder,
        spellcheck: false,
        'aria-label': field,
      }) as HTMLInputElement;
      input.addEventListener('input', () => {
        const v = input.value;
        workPropose = { ...workPropose, [field]: v || undefined };
        drawProposeErr();
        edited();
      });
      return input;
    };
    // A hint under a row, in the value's column; it wraps, never truncates.
    const hint = (testid: string) =>
      h(
        'div',
        { class: 'cb-prop-hint', 'data-testid': testid },
        h('span'),
        h('span'),
        h('span', { class: 'cb-prop-hint-t' }),
      );
    const untilIn = text('until', '');
    const untilRow = tr('until', untilIn);
    const untilHint = hint('until-hint');
    untilRow.hidden = untilHint.hidden = !needsUntil();
    const noteIn = text('note', 'optional');
    const noteHint = hint('note-hint');
    // serve's words for both: the until forms it parses, the note's tokens.
    // Each item stays whole on a line; the list wraps between them.
    const items = (el: HTMLElement, lead: string, words: string[]) => {
      const out: Array<Node | string> = [];
      words.forEach((w, i) => {
        if (i) out.push(HINT_SEP);
        out.push(h('span', { class: 'cb-prop-hint-i' }, w));
      });
      el.lastElementChild!.replaceChildren(
        ...(words.length ? [lead, ...out] : []),
      );
    };
    void getVocab(ctx)
      .then((v) => {
        allDispositions = (v.kinds ?? []).flatMap((k) => k.allowed ?? []);
        drawChoices();
        const forms = v.until_forms ?? [];
        if (forms[0]) untilIn.placeholder = `e.g. ${forms[0].example}`;
        items(
          untilHint,
          '',
          forms.map((f) => f.syntax),
        );
      })
      .catch(() => {});
    void noteTokens(ctx)
      .then((ts) => {
        items(
          noteHint,
          'may use ',
          ts.map((t) => `${t.token} ${t.meaning}`),
        );
      })
      .catch(() => {});
    refreshPropose = () => {
      untilIn.value = workPropose.until ?? '';
      noteIn.value = workPropose.note ?? '';
      untilRow.hidden = untilHint.hidden = !needsUntil();
      drawChoices();
    };
    proposeEl.replaceChildren(
      h(
        'div',
        { class: 'kit-table cb-propose', 'data-testid': 'propose' },
        tr('disposition', pick),
        menu,
        untilRow,
        untilHint,
        tr('note', noteIn),
        noteHint,
      ),
      proposeErrEl,
    );
    drawChoices();
  }

  // drawChoices redraws the disposition picker (a new list from serve).
  let drawChoices: () => void = () => {};
  // refreshPropose shows the proposal in a draft's picker already built.
  let refreshPropose: (() => void) | null = null;
  // redrawing: elements are being moved in the document, not left by Court.
  let redrawing = false;

  // keepFocus runs a redraw that moves elements (a focused one loses focus
  // as it leaves the document, if only to be put back) and gives the focus
  // back to the element that had it, if it is still in the document.
  function keepFocus(redraw: () => void): void {
    const focused = document.activeElement;
    redrawing = true;
    try {
      redraw();
    } finally {
      redrawing = false;
    }
    if (
      focused instanceof HTMLElement &&
      focused !== document.activeElement &&
      focused.isConnected
    ) {
      focused.focus();
    }
  }
  // Every disposition some kind of item allows (the decision vocabulary).
  let allDispositions: string[] = [];

  // drawProposeErr says why the proposal isn't valid: serve's message for
  // the copy it holds, or, once Court changes the disposition, whether
  // this rule may propose it.
  function drawProposeErr(): void {
    let msg = '';
    const d = workPropose.disposition;
    if (
      saved.invalid?.startsWith('propose:') &&
      sameAction(workPropose, base.propose) &&
      sameConditions(work, base.match ?? [])
    ) {
      msg = saved.invalid;
    } else if (d && dispositions.length && !dispositions.includes(d)) {
      msg = refusedDisposition(d, work, dispositions);
    }
    proposeErrEl.textContent = msg;
    proposeErrEl.hidden = !msg;
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

  function waitRow(text: string): HTMLElement {
    return h(
      'div',
      { class: 'cb-mr cb-mr-wait' },
      h('span'),
      h('span', { class: 'cb-mr-k' }, text),
      h('span'),
    );
  }

  function drawMatches(): void {
    matchesEl.removeAttribute('data-invalid');
    if (invalid) {
      // serve's message is under the condition it names; this line doesn't
      // repeat (or renumber) it.
      heading.textContent = 'matches now';
      matchesEl.setAttribute('data-invalid', '');
      matchesEl.replaceChildren(
        waitRow('not previewed: the conditions aren\u2019t valid'),
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
    if (out.length === 0) out.push(waitRow('nothing matches now'));
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

  // setPreview takes fresh matches (their pages so far).
  function setPreview(p: MatchPreview): void {
    invalid = null;
    setConditionError(editor, -1, null);
    preview = p;
    shown = p.page ?? [];
    if (p.dispositions) dispositions = p.dispositions;
    drawChoices();
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

  // previewNow previews the rule as edited. depth keeps that many rows (a
  // live refresh keeps a "show more" Court opened); an edit starts again.
  async function previewNow(depth = PAGE): Promise<void> {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    const mine = ++seq;
    opened = depth;
    try {
      const b = body();
      const p = await ctx.api.post<MatchPreview>('/rules/preview', b);
      let rows = p.page ?? [];
      while (rows.length < Math.min(Math.max(depth, opened), p.total)) {
        const next = await ctx.api.post<MatchPreview>(
          `/rules/preview?offset=${rows.length}&limit=${PAGE}`,
          b,
        );
        if (mine !== seq || !(next.page ?? []).length) break;
        rows = [...rows, ...(next.page ?? [])];
      }
      if (mine === seq) setPreview({ ...p, page: rows });
    } catch (err) {
      if (mine !== seq) return;
      if (err instanceof ApiError && err.status === 400)
        setInvalid(err.message);
      else note.textContent = `preview failed: ${message(err)}`;
    }
  }

  // schedule previews after the debounce. keepDepth (a live refresh) keeps
  // as many rows as are shown when it fires; an edit starts from page one.
  function schedule(keepDepth = false): void {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      void previewNow(keepDepth ? Math.max(PAGE, opened, shown.length) : PAGE);
    }, PREVIEW_DEBOUNCE_MS);
  }

  async function loadMore(): Promise<void> {
    const from = shown.length;
    const q = `?offset=${from}&limit=${PAGE}`;
    opened = Math.max(opened, from + PAGE);
    const mine = seq;
    try {
      const p = await ctx.api.post<MatchPreview>(`/rules/preview${q}`, body());
      // Dropped when a newer preview started (it pages to what Court
      // opened), or when the rows shown changed since this asked: a
      // re-preview already in flight at the click read the raised `opened`
      // and brought these rows itself.
      if (mine !== seq || shown.length !== from) return;
      shown = [...shown, ...(p.page ?? [])];
      preview = { ...p, page: shown };
      drawMatches();
    } catch (err) {
      note.textContent = `more matches failed: ${message(err)}`;
    }
  }

  // ---- exclusions ----

  // applyExclusions takes serve's rule after an exclusion or an inclusion:
  // the exclusions are serve's; so are the matches, unless Court has
  // unsaved edits, which are previewed again with the new exclusions.
  function applyExclusions(d: RuleDetailView): void {
    exclude = d.rule.exclude ?? [];
    base = { ...base, exclude };
    if (dirty() || invalid) {
      void previewNow(Math.max(PAGE, opened, shown.length));
    } else {
      seq++; // a preview in flight is older than this
      opened = PAGE; // serve's matches: page one, as shown
      setPreview(d.matches);
    }
    drawFacts();
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
        hooks.saved();
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
      hooks.saved();
    } catch (err) {
      note.textContent = `not included: ${message(err)}`;
    }
  }

  // ---- a change underneath Court's edits ----

  function drawConflict(): void {
    if (!conflict) {
      conflictEl.replaceChildren();
      return;
    }
    const theirs = conflict.detail.rule;
    const who = conflict.by ? authorOf(conflict.by).name : '';
    const p = theirs.propose;
    const theirsBody = h(
      'div',
      null,
      h(
        'p',
        { class: 'cb-conflict-help' },
        'Your edits are kept below and not saved. Serve\u2019s copy now:',
      ),
      theirs.name !== base.name
        ? h('p', { class: 'cb-conflict-name' }, theirs.name)
        : null,
      conditionEditor(
        ctx,
        { match: theirs.match, status: 'active', editable: false },
        () => {},
      ),
      h(
        'div',
        { class: 'kit-table cb-propose' },
        h(
          'div',
          { class: 'kit-tr' },
          h('span', { class: 'cb-cond-f' }, 'disposition'),
          h('span'),
          h(
            'span',
            {
              class:
                'cb-cond-v' +
                (DANGER_DISPS.has(p.disposition) ? ' cb-danger' : ''),
              'data-testid': 'conflict-disposition',
            },
            p.disposition,
          ),
        ),
        p.until
          ? h(
              'div',
              { class: 'kit-tr' },
              h('span', { class: 'cb-cond-f' }, 'until'),
              h('span'),
              h('span', { class: 'cb-cond-v' }, p.until),
            )
          : null,
        p.note
          ? h(
              'div',
              { class: 'kit-tr' },
              h('span', { class: 'cb-cond-f' }, 'note'),
              h('span'),
              h('span', { class: 'cb-cond-v' }, p.note),
            )
          : null,
      ),
      h('p', { class: 'cb-conflict-note', role: 'status' }),
    );
    conflictEl.replaceChildren(
      card({
        edge: 'signal',
        head: who
          ? `changed while you were editing \u00b7 by ${who}`
          : 'changed while you were editing',
        body: theirsBody,
        actions: [
          {
            label: 'reload theirs',
            run() {
              const d = conflict!.detail;
              conflict = null;
              draw(d);
            },
          },
          {
            label: 'keep mine',
            run() {
              const d = conflict!.detail;
              conflict = null;
              draw(d, { match: work, propose: workPropose });
            },
          },
        ],
      }),
    );
  }

  // refuseActivate: Activate waits for Court to settle a conflict.
  function refuseActivate(): void {
    const n = conflictEl.querySelector<HTMLElement>('.cb-conflict-note');
    if (n) {
      n.textContent =
        'Activate waits: reload their copy, or keep yours over it, first.';
    }
    conflictEl.scrollIntoView?.({ block: 'nearest' });
  }

  // refuseInvalid: Activate waits for a valid rule. The card above says
  // why (serve's message is shown there, and at the field), so the note
  // only points to it.
  function refuseInvalid(): void {
    note.textContent = 'not activated: fix what\u2019s marked above';
    invalidEl.scrollIntoView?.({ block: 'nearest' });
  }

  // enterConflict fetches serve's copy after a 409 (a change the page hadn't
  // heard of yet) and shows it: as a conflict when Court has edits, else by
  // drawing it with a note that nothing was done.
  async function afterConflict(what: string): Promise<void> {
    try {
      const d = await ctx.api.get<RuleDetailView>('/rule', { id });
      if (dirty()) {
        conflict = { detail: d, by: '' };
        drawConflict();
        drawActions();
        hooks.primaryChanged();
      } else {
        draw(d);
        note.textContent = `It changed before you ${what} it; nothing was ${what}. Here is serve\u2019s copy.`;
      }
    } catch (err) {
      note.textContent = message(err);
    }
  }

  // saysWhatHeEdited: a copy of serve's whose conditions and proposal are
  // Court's unsaved edits.
  function saysWhatHeEdited(r: Rule): boolean {
    return (
      sameConditions(r.match ?? [], work) && sameAction(r.propose, workPropose)
    );
  }

  // settle ends a conflict that no longer holds: with serve's copy drawn
  // (it is newer than what the page shows), or just dropped.
  function settle(d: RuleDetailView | null): void {
    conflict = null;
    if (d) {
      draw(d);
      return;
    }
    drawConflict();
    drawActions();
    hooks.primaryChanged();
  }

  // ---- saving and lifecycle ----

  // save posts Court's edits over the version shown; false (with serve's
  // message, or the conflict, shown) on refusal.
  async function save(): Promise<boolean> {
    if (conflict) {
      refuseActivate();
      return false;
    }
    try {
      const d = await ctx.api.post<RuleDetailView>(
        `/rules/draft?version=${encodeURIComponent(saved.version)}`,
        body(),
      );
      draw(d); // drops a conflict that was this save, heard live first
      hooks.saved();
      if (conflict) {
        // A change that landed after his save (serve took the save
        // against the version shown, so the copy heard live is newer).
        const { detail: newer, by } = conflict;
        settle(newer);
        const who = by ? authorOf(by).name : 'someone';
        note.textContent = `Saved; then ${who} changed it. Here is serve\u2019s copy; nothing else was done.`;
        return false;
      }
      return true;
    } catch (err) {
      if (isConflict(err)) void afterConflict('saved');
      else if (err instanceof ApiError && err.status === 400) {
        if (conditionErrorIndex(err.message) >= 0) setInvalid(err.message);
        else note.textContent = err.message;
      } else note.textContent = `not saved: ${message(err)}`;
      return false;
    }
  }

  function lifecycle(verb: 'activate' | 'deactivate'): Promise<void> {
    return inTurn(() => lifecycleNow(verb));
  }

  async function lifecycleNow(verb: 'activate' | 'deactivate'): Promise<void> {
    // A click queued behind a request that already did what it asks (a
    // double click on Activate) has nothing left to do.
    if (verb === 'deactivate' && isDraft()) return;
    if (verb === 'activate' && !isDraft() && !dirty()) return;
    if (verb === 'activate' && conflict) {
      refuseActivate();
      return;
    }
    if (verb === 'activate' && saved.invalid && !dirty()) {
      refuseInvalid();
      return;
    }
    try {
      if (verb === 'activate' && dirty() && !(await save())) return;
      // A saved copy serve holds as invalid can't be activated: the card
      // says why, once; nothing is posted that is sure to be refused.
      if (verb === 'activate' && saved.invalid) {
        refuseInvalid();
        return;
      }
      // Activate names the copy shown; serve refuses (409) one that changed.
      const d = await ctx.api.post<RuleDetailView>(
        `/rules/${verb}`,
        verb === 'activate' ? { id, version: saved.version } : { id },
      );
      draw(d);
      hooks.saved();
    } catch (err) {
      if (isConflict(err)) void afterConflict(`${verb}d`);
      else note.textContent = `not ${verb}d: ${message(err)}`;
    }
  }

  function proposeOnce(): Promise<void> {
    return inTurn(proposeOnceNow);
  }

  async function proposeOnceNow(): Promise<void> {
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
    }
  }

  function drawActions(): void {
    const bs: Button[] = [];
    if (editable() && dirty()) {
      bs.push({
        label: isDraft() ? 'save draft' : 'save as draft',
        run: () => void save(),
      });
    }
    if (isDraft() && !saved.invalid) {
      bs.push({ label: 'propose once', run: () => void proposeOnce() });
    }
    actionsEl.replaceChildren(bs.length ? buttons(bs) : '', note);
  }

  // edited follows any edit: the proposal or the conditions.
  function edited(): void {
    note.textContent = '';
    // Court undid his edits, or made them say what serve's copy says: the
    // conflict no longer holds; serve's copy is drawn.
    if (conflict && (!dirty() || saysWhatHeEdited(conflict.detail.rule))) {
      settle(conflict.detail);
      return;
    }
    drawActions();
    hooks.primaryChanged();
  }

  function onEdit(m: Condition[]): void {
    work = m;
    schedule();
    edited();
  }

  // draw lays the whole document out from serve's copy (keep: Court's edits,
  // kept over it).
  function draw(
    d: RuleDetailView,
    keep?: { match: Condition[]; propose: RuleAction },
  ): void {
    if (timer !== null) clearTimeout(timer);
    timer = null;
    seq++;
    opened = PAGE; // serve's copy of the rule changed: page one again
    saved = d;
    base = d.rule;
    // A conflict over this very copy no longer holds.
    if (conflict && conflict.detail.version === d.version) conflict = null;
    work = keep ? keep.match : [...(base.match ?? [])];
    workPropose = keep ? keep.propose : { ...base.propose };
    exclude = base.exclude ?? [];
    preview = d.matches;
    shown = preview.page ?? [];
    dispositions = d.matches.dispositions ?? [];
    // Conditions serve refuses (or a file it can't read) aren't previewed.
    invalid =
      d.invalid &&
      !keep &&
      (conditionErrorIndex(d.invalid) >= 0 || d.invalid.startsWith('rules/'))
        ? d.invalid
        : null;
    doc.dataset.status = base.status;
    doc.toggleAttribute('data-invalid', !!d.invalid);
    kickEl.textContent = `rule \u00b7 ${base.status} \u00b7 rules/${id}.toml`;
    titleEl.textContent = base.name || id;
    editor = conditionEditor(
      ctx,
      { match: work, status: base.status, editable: editable() },
      onEdit,
    );
    factsEl = h('div');
    keepFocus(() =>
      doc.replaceChildren(
        kickEl,
        titleEl,
        factsEl,
        proseEl,
        invalidEl,
        conflictEl,
        h(
          'h3',
          { class: 'kit-label' },
          'when an undecided item matches all of',
        ),
        editor,
        h('h3', { class: 'kit-label' }, 'propose'),
        proposeEl,
        h('div', { class: 'cb-matches-head' }, heading, groupChips),
        matchesEl,
        actionsEl,
      ),
    );
    note.textContent = '';
    drawProse();
    drawInvalid();
    drawConflict();
    drawPropose();
    drawFacts();
    drawGroupChips();
    drawMatches();
    if (invalid) setInvalid(invalid);
    drawActions();
    if (keep) schedule();
    hooks.primaryChanged();
  }

  draw(detail);

  return {
    el,
    id,
    dirty,
    primary() {
      if (!isDraft()) {
        return { label: 'Deactivate', run: () => void lifecycle('deactivate') };
      }
      return { label: 'Activate', run: () => void lifecycle('activate') };
    },
    update(d, by) {
      if (d.version === saved.version) {
        // serve is back at the copy his edits are on: a change shown as a
        // conflict no longer holds.
        if (conflict) settle(null);
        // The same rule; its exclusions (or record) may have moved.
        const was = (saved.rule.exclude ?? []).map((x) => x.key).join('\n');
        const now = (d.rule.exclude ?? []).map((x) => x.key).join('\n');
        saved = { ...saved, record: d.record, invalid: d.invalid };
        if (was !== now || !sameRule(d.rule, base)) applyExclusions(d);
        return;
      }
      // No edits to keep, or serve's copy already says what his edits say
      // (his own save, heard live before its reply): not a conflict.
      if (!dirty() || saysWhatHeEdited(d.rule)) {
        conflict = null;
        draw(d);
        return;
      }
      // Court is editing: his edits stay, and the change shows, with who
      // made it; Activate waits until he settles it.
      conflict = { detail: d, by };
      drawConflict();
      drawActions();
      hooks.primaryChanged();
    },
    refresh() {
      if (!invalid || dirty()) schedule(true);
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
  let asking = false; // Court asked the agent from the foot
  let opening = 0;
  let painting = false;
  let listSeq = 0;
  let liveSeq = 0;

  const statusOf = (r: RuleRow) => r.rule.status;
  const inView = (r: RuleRow) =>
    view === 'all' ||
    (view === 'active' ? statusOf(r) === 'active' : statusOf(r) !== 'active');

  // ---- the foot: new rule, or ask the agent ----

  const askEl = h(
    'button',
    {
      type: 'button',
      class: 'cb-link cb-rules-ask',
      onclick() {
        asking = true;
        feed();
        ctx.focusComposer();
      },
    },
    '',
  );
  const nameAgent = () => {
    askEl.textContent = `or ask ${ctx.agentName() || 'the agent'} to draft one`;
  };
  nameAgent();
  ctx.onAgentName(nameAgent);
  const foot = h(
    'div',
    { class: 'cb-rules-foot' },
    h(
      'button',
      {
        type: 'button',
        class: 'kit-btn',
        'data-testid': 'new-rule',
        onclick() {
          newRule();
        },
      },
      'new rule',
    ),
    askEl,
  );

  function newRule(): void {
    const name = h('input', {
      class: 'kit-note',
      type: 'text',
      placeholder: 'e.g. Landed branches \u2192 delete',
      'aria-label': 'name',
    }) as HTMLInputElement;
    const idIn = h('input', {
      class: 'kit-note',
      type: 'text',
      placeholder: 'e.g. landed-branches',
      spellcheck: false,
      'aria-label': 'id',
    }) as HTMLInputElement;
    let idTyped = false;
    name.addEventListener('input', () => {
      if (!idTyped) idIn.value = slug(name.value);
    });
    idIn.addEventListener('input', () => {
      idTyped = true;
    });
    const err = h('p', { class: 'cb-sheet-err', hidden: true });
    let sending = false;
    const create = async () => {
      if (sending) return;
      const id = idIn.value.trim();
      const title = name.value.trim();
      if (!id || !title) {
        err.textContent = 'A rule needs a name and an id.';
        err.hidden = false;
        return;
      }
      sending = true;
      try {
        await ctx.api.post<RuleDetailView>('/rules/draft?create=1', {
          id,
          name: title,
          status: 'draft',
          match: [],
          propose: { disposition: '' },
        });
        sh.close();
        await loadList();
        ctx.route.go('rules', id);
      } catch (e) {
        err.textContent = message(e);
        err.hidden = false;
        sending = false;
      }
    };
    for (const input of [name, idIn]) {
      input.addEventListener('keydown', (e: KeyboardEvent) => {
        if (e.key === 'Enter' && !e.isComposing) {
          e.preventDefault();
          void create();
        }
      });
    }
    const sh = sheet({
      title: 'new rule',
      body: h(
        'div',
        { class: 'cb-sheet-body' },
        h(
          'div',
          { class: 'cb-sheet-row' },
          h('label', { class: 'cb-sheet-label' }, 'name'),
          name,
        ),
        h(
          'div',
          { class: 'cb-sheet-row' },
          h('label', { class: 'cb-sheet-label' }, 'id'),
          idIn,
        ),
        h(
          'p',
          { class: 'cb-sheet-preview' },
          'A draft: it proposes nothing until you activate it.',
        ),
        err,
      ),
      actions: [
        { label: 'create', fill: true, run: () => void create() },
        { label: 'cancel', run: () => sh.close() },
      ],
    });
    sh.el.dataset.testid = 'new-rule-sheet';
    name.focus();
  }

  const handle: ListHandle<RuleRow> = list<RuleRow>({
    label: 'rules',
    views: VIEWS.map((v) => ({ ...v, on: v.id === view })),
    openOnMove: false,
    row(r: RuleRow) {
      const who = authorOf(r.rule.created_by);
      const draft = r.rule.status !== 'active';
      return {
        id: r.rule.id,
        key: rowKicker(r),
        title: r.rule.name || r.rule.id,
        sub:
          who.agent && draft
            ? `drafted by ${who.name} \u00b7 awaiting your review`
            : undefined,
        meta: r.invalid ? 'not valid' : r.rule.status,
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
    foot,
  });
  handle.el.classList.add('cb-rules-list');

  // paintList draws the list and marks each row's status as a pill (the
  // kit's row has no pill slot: its meta span takes the pill classes).
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
      if (r.invalid) rowEl.title = r.invalid;
      rowEl
        .querySelector('.kit-meta')
        ?.classList.add(
          'kit-pill',
          r.invalid
            ? 'cb-pill-invalid'
            : r.rule.status === 'active'
              ? 'ok'
              : 'wait',
        );
    });
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
    nameAgent();
    if (!doc && !openId) drawEmpty();
  }

  // loadList reloads the list; an older reply never paints over a newer one.
  async function loadList(): Promise<void> {
    const mine = ++listSeq;
    try {
      const v = await ctx.api.get<RulesView>('/rules');
      if (mine !== listSeq) return;
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

  // feed tells the composer and the bar what Rules shows, only while it is
  // the active section: the open rule once it has loaded (a rule that
  // doesn't load attaches nothing), or the section when Court asks the agent.
  function feed(): void {
    if (!active) return;
    ctx.setAttached(
      doc ? { rule: doc.id } : asking ? { section: 'rules' } : {},
    );
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
      asking = false;
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
  // accepts a proposal). Activation only proposes (m2, accepted).
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

  const ruleKeys: KeyBinding[] = [activateKey];

  void loadList();

  return {
    id: 'rules',
    list: handle.el,
    read: readEl,
    // Rule rows can't be selected: the list keys here move and open.
    listKeys: () => ({ nav: handle, selects: false }),
    keys: () => ruleKeys,
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
      asking = false;
    },
    onLive(type: string, data: unknown) {
      if (type === 'rules') {
        void loadList();
        const ev = data as { id?: string; by?: string };
        if (doc && openId && (!ev?.id || ev.id === openId)) {
          const d = doc;
          const mine = ++liveSeq;
          void ctx.api
            .get<RuleDetailView>('/rule', { id: openId })
            .then((detail) => {
              // Only the newest reply, and only for the same document.
              if (mine === liveSeq && doc === d) d.update(detail, ev?.by ?? '');
            })
            .catch(() => {});
        }
      } else if (type === 'index' || type === 'decided') {
        // The live index moved: the open rule's matches are recomputed
        // (debounced: a batch of decisions is one preview).
        doc?.refresh();
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
