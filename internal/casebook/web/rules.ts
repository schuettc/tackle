// rules.ts — a rule in decide's middle pane, read only, and the list of
// every rule ("rules N").
//
// renderRule(ctx, detail, hooks) draws one rule as a document: kicker,
// title, facts (on or off, matches, excluded, author, record), what it does,
// its conditions (conditions.ts, read only), what it recommends, and its
// live matches: "matches now · N · <by reason>", paginated, groupable by
// repo. Unticking a match excludes it (with an optional reason); ticking an
// excluded one includes it again. edit opens the rule editor beside the
// list (hooks.onEdit); turn on / turn off is hooks.onToggle.
//
// renderRuleList(ctx, rows, hooks) is every rule, off ones too: its name,
// on, off or not valid, what it recommends and how many items it matches,
// with ⋯ (edit, turn on / off). A row opens the rule (hooks.onOpen).

import { h, facts, buttons, card, sheet, type Button } from '/_kit/kit.js';
import type {
  Exclusion,
  MatchPreview,
  MatchRow,
  RuleDetailView,
  RuleRow,
} from './wire.d.ts';
import type { Ctx } from './app.ts';
import { conditionEditor } from './conditions.ts';
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { DANGER_DISPS, getVocab } from './decide.ts';
import { dispositionLabel, ruleMenu } from './rule-editor.ts';
import {
  authorOf,
  matchesHeading,
  reasonSummary,
  trackRecord,
} from './rules-text.ts';

/** The re-preview debounce (spec §4.3). */
export const PREVIEW_DEBOUNCE_MS = 300;

// Matches are fetched 200 at a time, like decide's list (serve's cap).
const PAGE = 200;

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export interface RuleHooks {
  /** Open the rule in the editor beside the list. */
  onEdit(): void;
  /** Turn the rule on (true) or off (false). */
  onToggle(on: boolean): Promise<void>;
  /** An exclusion or an inclusion was saved: the list may change. */
  onChanged?(): void;
}

/**
 * renderRule draws a rule as a read-only document; its matches post
 * exclusions and inclusions to serve.
 */
export function renderRule(
  ctx: Ctx,
  detail: RuleDetailView,
  hooks: RuleHooks,
): HTMLElement {
  const rule = detail.rule;
  const id = rule.id;
  const off = rule.status !== 'active';
  let exclude: Exclusion[] = rule.exclude ?? [];
  let preview: MatchPreview = detail.matches;
  let shown: MatchRow[] = preview.page ?? [];
  let grouped = false;
  let seq = 0;

  const doc = h('article', {
    class: 'cb-rule',
    'data-rule': id,
    'data-status': rule.status,
  });
  doc.toggleAttribute('data-invalid', !!detail.invalid);
  const el = h('div', { class: 'kit-doc' }, doc);

  let factsEl = h('div');
  const heading = h('h3', { class: 'kit-label cb-matches-label' });
  const groupChips = h('span', { class: 'cb-matches-view' });
  const matchesEl = h('div', {
    class: 'kit-table cb-matches',
    'data-testid': 'matches',
  });
  const note = h('p', { class: 'cb-rule-note', role: 'status' });

  function drawFacts(): void {
    const who = authorOf(rule.created_by);
    const byEl = h('b', who.agent ? { class: 'cb-by-agent' } : null, who.name);
    const rows: Array<[string, string | Node]> = [
      [
        'status',
        `${off ? 'off' : 'on'}${detail.invalid ? ' · not valid' : ''}`,
      ],
      ['matches', String(preview.total)],
      ['excluded', String(exclude.length)],
      ['by', byEl],
    ];
    const rec = trackRecord(detail.record);
    if (rec) rows.push(['record', rec]);
    const next = facts(rows);
    factsEl.replaceWith(next);
    factsEl = next;
  }

  // The card for a rule serve holds as invalid: its message.
  const invalidEl = detail.invalid
    ? card({
        edge: 'signal',
        head: 'not valid',
        body: h(
          'div',
          null,
          h('p', { class: 'cb-invalid-msg' }, detail.invalid),
          h(
            'p',
            { class: 'cb-invalid-help' },
            'casebook recommends nothing from this rule until it is valid. Edit it to fix it.',
          ),
        ),
      })
    : h('div');

  // What it recommends, as it reads for the types the rule matches
  // ("Delete it"), once the vocabulary is in.
  const p = rule.propose;
  const tr = (label: string, value: Node) =>
    h(
      'div',
      { class: 'kit-tr' },
      h('span', { class: 'cb-cond-f' }, label),
      h('span'),
      value,
    );
  const dispEl = h(
    'span',
    {
      class:
        'cb-cond-v cb-disp-ro' +
        (DANGER_DISPS.has(p.disposition) ? ' cb-danger' : ''),
      'data-value': p.disposition,
    },
    p.disposition,
  );
  void getVocab(ctx)
    .then((vocab) => {
      dispEl.textContent = dispositionLabel(
        vocab,
        preview.kinds ?? null,
        p.disposition,
      );
    })
    .catch(() => {});
  const proposeEl = h(
    'div',
    { class: 'kit-table cb-propose', 'data-testid': 'propose' },
    tr('decision', dispEl),
    p.until ? tr('until', h('span', { class: 'cb-cond-v' }, p.until)) : null,
    p.note ? tr('note', h('span', { class: 'cb-cond-v' }, p.note)) : null,
  );

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
    heading.textContent = matchesHeading(preview.total, preview.by_reason);
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

  async function loadMore(): Promise<void> {
    const from = shown.length;
    const mine = seq;
    try {
      const p = await ctx.api.post<MatchPreview>(
        `/rules/preview?offset=${from}&limit=${PAGE}`,
        { ...rule, exclude },
      );
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
  // its exclusions and its matches (page one).
  function applyExclusions(d: RuleDetailView): void {
    seq++;
    exclude = d.rule.exclude ?? [];
    preview = d.matches;
    shown = preview.page ?? [];
    drawMatches();
    drawFacts();
    hooks.onChanged?.();
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

  const bs: Button[] = [
    { label: 'edit', run: () => hooks.onEdit() },
    {
      label: off ? 'turn on' : 'turn off',
      run: () => {
        note.textContent = '';
        void hooks.onToggle(off).catch((err: unknown) => {
          note.textContent = message(err);
        });
      },
    },
  ];
  const actionsEl = h('div', { class: 'cb-rule-actions' }, buttons(bs), note);

  doc.replaceChildren(
    h('p', { class: 'kit-kick' }, `rule \u00b7 ${off ? 'off' : 'on'}`),
    h('h1', { class: 'kit-h1' }, rule.name || id),
    factsEl,
    h(
      'p',
      null,
      off
        ? 'Off: it recommends nothing until you turn it on.'
        : 'On: undecided items that match get this recommendation, now and after every sync. Nothing is decided until you accept it.',
    ),
    invalidEl,
    h('h3', { class: 'kit-label' }, 'when an undecided item matches all of'),
    conditionEditor(
      ctx,
      { match: rule.match, status: rule.status, editable: false },
      () => {},
    ),
    h('h3', { class: 'kit-label' }, 'recommend'),
    proposeEl,
    h('div', { class: 'cb-matches-head' }, heading, groupChips),
    matchesEl,
    actionsEl,
  );
  drawFacts();
  drawGroupChips();
  drawMatches();
  return el;
}

export interface RuleListHooks {
  onOpen(row: RuleRow): void;
  onEdit(row: RuleRow): void;
  onToggle(row: RuleRow): Promise<void>;
}

/**
 * renderRuleList is every rule, off ones too, in the middle pane: each
 * row its name, on / off / not valid, its decision and how many items it
 * matches, and ⋯ (edit, turn on / off). A row opens the rule.
 */
export function renderRuleList(
  ctx: Ctx,
  rows: RuleRow[],
  hooks: RuleListHooks,
): HTMLElement {
  const note = h('p', { class: 'cb-rule-note', role: 'status' });
  const decision = (row: RuleRow) =>
    h('span', { class: 'cb-rules-row-d' }, row.rule.propose.disposition);
  const decisions: Array<[RuleRow, HTMLElement]> = [];
  const line = (row: RuleRow): HTMLElement => {
    const off = row.rule.status !== 'active';
    const d = decision(row);
    decisions.push([row, d]);
    const state = row.invalid ? 'not valid' : off ? 'off' : 'on';
    return h(
      'li',
      {
        class: 'cb-rules-row' + (off ? ' cb-rule-off' : ''),
        'data-id': row.rule.id,
      },
      h(
        'button',
        {
          type: 'button',
          class: 'cb-rules-row-open',
          title: row.invalid ?? '',
          onclick() {
            hooks.onOpen(row);
          },
        },
        h('span', { class: 'cb-rules-row-name' }, row.rule.name || row.rule.id),
        h(
          'span',
          {
            class: 'cb-rules-row-state',
            'data-state': state,
          },
          state,
        ),
        d,
        h(
          'span',
          { class: 'cb-rules-row-n' },
          row.invalid ? '' : pluralize(row.matches, 'match', 'matches'),
        ),
      ),
      ruleMenu(row, {
        onEdit: () => hooks.onEdit(row),
        onToggle: () => {
          note.textContent = '';
          void hooks.onToggle(row).catch((err: unknown) => {
            note.textContent = message(err);
          });
        },
      }),
    );
  };
  const el = h(
    'div',
    { class: 'kit-doc' },
    h(
      'article',
      { class: 'cb-rules-doc', 'data-testid': 'rule-list' },
      h('h1', { class: 'kit-h1' }, pluralize(rows.length, 'rule')),
      h(
        'p',
        null,
        'A rule recommends a decision for the undecided items it matches. Its recommendations are a group in the list, accepted in one go.',
      ),
      rows.length
        ? h('ul', { class: 'cb-rules-rows' }, ...rows.map(line))
        : h('p', { class: 'cb-read-empty-prompt' }, 'No rules yet.'),
      note,
    ),
  );
  void getVocab(ctx)
    .then((vocab) => {
      for (const [row, d] of decisions)
        d.textContent = dispositionLabel(
          vocab,
          null,
          row.rule.propose.disposition,
        );
    })
    .catch(() => {});
  return el;
}
