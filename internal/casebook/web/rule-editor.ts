// rule-editor.ts — rules on decide's list (Court, 2026-10-17: a rule is a
// filter that recommends; both live with the items, one editor, one
// preview).
//
// makeRuleRow(ctx, hooks) is the row under the filters: "+ rule" and
// "rules N". ruleMenu(row, hooks) is a rule's ⋯ (edit, turn off or on), on
// its group's header and in the rule list.
//
// ruleEditor(ctx, row, seed, hooks) is the filter editor with one more line:
// what the rule proposes (a decision, and a note). Saving turns the rule on
// (POST /api/rules/save); there is no separate activate.

import { h, ApiError } from '/_kit/kit.js';
import type {
  Condition,
  DecisionVocabView,
  MatchPreview,
  RuleDetailView,
  RuleRow,
} from './wire.d.ts';
import type { Ctx } from './app.ts';
import { conditionEditor, setConditionError } from './conditions.ts';
import { conditionErrorIndex, slug, typesOf } from './rules-text.ts';
import { getVocab, DANGER_DISPS } from './decide.ts';
import { menuNav } from './menu-nav.ts';

const KINDS = ['repo', 'pr', 'issue', 'branch', 'worktree'];
// Not now and watch need an until; this editor doesn't offer them yet.
const OFFERED = (d: string) => d !== 'wait' && d !== 'watch';

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/**
 * dispositionLabel is how a decision reads for the types a rule matches,
 * from the decision vocabulary ("Delete it", "Archive it on GitHub"); for
 * several types, the plainest of their wordings ("Close it", not the PR's
 * "Close it without merging").
 */
export function dispositionLabel(
  vocab: DecisionVocabView | null,
  types: string[] | null,
  d: string,
): string {
  let best = '';
  for (const k of types ?? KINDS) {
    const kv = vocab?.kinds?.find((x) => x.kind === k);
    const c = kv?.choices?.find((x) => x.disposition === d);
    if (c && (!best || c.label.length < best.length)) best = c.label;
  }
  return best || d;
}

/**
 * ruleMenu is a rule's ⋯ and its menu: edit, turn off (or on). It goes on
 * a rule group's header and on each row of the rule list.
 */
export function ruleMenu(
  row: RuleRow,
  hooks: { onEdit(): void; onToggle(): void },
): HTMLElement {
  const id = row.rule.id;
  const off = row.rule.status !== 'active';
  const more = h('button', {
    type: 'button',
    class: 'cb-filter-more cb-rule-more',
    'aria-label': `${row.rule.name || id}: edit, turn ${off ? 'on' : 'off'}`,
    'aria-haspopup': 'menu',
    'aria-expanded': 'false',
    onclick(e: Event) {
      e.stopPropagation();
      setMenu(menu.hidden);
    },
  });
  more.textContent = '\u22ef';
  const item = (label: string, run: () => void): HTMLElement =>
    h(
      'button',
      {
        type: 'button',
        class: 'cb-filter-menu-item',
        role: 'menuitem',
        'data-act': label,
        onclick(e: Event) {
          e.stopPropagation();
          setMenu(false);
          run();
        },
      },
      label,
    );
  const menu = h(
    'div',
    { class: 'cb-filter-menu', role: 'menu', hidden: true },
    item('edit', () => hooks.onEdit()),
    item(off ? 'turn on' : 'turn off', () => hooks.onToggle()),
  );
  const onEsc = (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || e.isComposing) return;
    e.preventDefault();
    e.stopPropagation();
    setMenu(false);
    more.focus();
  };
  // A click anywhere else closes it.
  const onAway = (e: MouseEvent) => {
    if (!wrap.contains(e.target as Node)) setMenu(false);
  };
  function setMenu(open: boolean): void {
    menu.hidden = !open;
    more.setAttribute('aria-expanded', String(open));
    if (open) {
      document.addEventListener('keydown', onEsc, true);
      document.addEventListener('mousedown', onAway, true);
      menu.querySelector<HTMLElement>('.cb-filter-menu-item')?.focus();
    } else {
      document.removeEventListener('keydown', onEsc, true);
      document.removeEventListener('mousedown', onAway, true);
    }
  }
  menuNav(menu, '.cb-filter-menu-item', more, () => setMenu(false));
  const wrap = h(
    'span',
    { class: 'cb-filter-item cb-rule-menu', 'data-id': id },
    more,
    menu,
  );
  return wrap;
}

/** toggleRule turns row's rule on when it is off, off when it is on. */
export async function toggleRule(ctx: Ctx, row: RuleRow): Promise<void> {
  const off = row.rule.status !== 'active';
  await ctx.api.post(off ? '/rules/activate' : '/rules/deactivate', {
    id: row.rule.id,
  });
}

export interface RuleRowBar {
  el: HTMLElement;
  paint(rows: RuleRow[]): void;
}

/**
 * makeRuleRow is the row under the filters: "+ rule" (a new rule, in the
 * editor beside the list) and "rules N" (every rule, off ones too, in the
 * middle pane).
 */
export function makeRuleRow(
  _ctx: Ctx,
  hooks: { onNew(): void; onList(): void },
): RuleRowBar {
  const addBtn = h(
    'button',
    {
      type: 'button',
      class: 'kit-chip cb-filter-add',
      'data-testid': 'new-rule',
      onclick() {
        hooks.onNew();
      },
    },
    '+ rule',
  );
  const listBtn = h(
    'button',
    {
      type: 'button',
      class: 'kit-chip cb-rules-n',
      'data-testid': 'rules-n',
      onclick() {
        hooks.onList();
      },
    },
    'rules 0',
  );
  const el = h(
    'div',
    { class: 'kit-chips cb-rule-row', 'aria-label': 'rules' },
    addBtn,
    listBtn,
  );
  return {
    el,
    paint(rows) {
      listBtn.textContent = `rules ${rows.length}`;
    },
  };
}

export interface RuleEditor {
  el: HTMLElement;
  showError(err: unknown): void;
}

/**
 * ruleEditor edits row (null: a new rule; seed fills a new one from a
 * filter). hooks.onDraft gets the conditions after every edit (the page
 * previews them in the list), hooks.onSaved the saved rule's id.
 */
export function ruleEditor(
  ctx: Ctx,
  row: RuleRow | null,
  seed: { name: string; match: Condition[] } | null,
  hooks: {
    onDraft(match: Condition[]): void;
    onSaved(id: string): void;
    onCancel(): void;
  },
): RuleEditor {
  let match: Condition[] = (row?.rule.match ?? seed?.match ?? []).map((c) => ({
    ...c,
  }));
  let disposition = row?.rule.propose.disposition ?? '';
  let vocab: DecisionVocabView | null = null;
  let offered: string[] = [];
  let kinds: string[] | null = null; // serve's: the types it can match
  let seq = 0;

  const dispEl = h('span', {
    class: 'cb-cond-picks cb-rule-disps',
    role: 'group',
    'aria-label': 'what it proposes',
  });
  const note = h('input', {
    class: 'cb-filter-name cb-rule-propose-note',
    type: 'text',
    placeholder: 'note, e.g. landed {how}; nothing unpushed',
    'aria-label': 'note',
  }) as HTMLInputElement;
  note.value = row?.rule.propose.note ?? '';
  const proposeErr = h('p', { class: 'cb-filter-name-err', hidden: true });

  function paintDisps(): void {
    const types = kinds ?? typesOf(match, KINDS);
    dispEl.replaceChildren(
      ...offered.map((d) =>
        h(
          'button',
          {
            type: 'button',
            class:
              'kit-chip cb-cond-pick' +
              (d === disposition ? ' on' : '') +
              (DANGER_DISPS.has(d) ? ' cb-rule-danger' : ''),
            'data-value': d,
            'aria-pressed': String(d === disposition),
            onclick() {
              disposition = d;
              proposeErr.hidden = true;
              paintDisps();
            },
          },
          dispositionLabel(vocab, types, d),
        ),
      ),
    );
    if (!offered.length) {
      dispEl.append(
        h(
          'span',
          { class: 'cb-rule-hint' },
          'add a condition to see what it can propose',
        ),
      );
    }
  }

  // What a rule may propose follows its conditions: serve's preview says
  // which decisions every type it can match allows.
  async function refreshOffered(): Promise<void> {
    const mine = ++seq;
    const done = match.filter((c) => c.value.trim() !== '');
    if (!done.length) {
      offered = [];
      paintDisps();
      return;
    }
    try {
      const p = await ctx.api.post<MatchPreview>('/rules/preview?limit=0', {
        match: done,
      });
      if (mine !== seq) return;
      offered = (p.dispositions ?? []).filter(OFFERED);
      kinds = p.kinds ?? null;
      if (disposition && !offered.includes(disposition)) disposition = '';
      paintDisps();
    } catch {
      // the conditions are refused: the editor shows why; keep the chips
    }
  }

  const editor = conditionEditor(
    ctx,
    { match, status: 'draft' },
    (m: Condition[]) => {
      match = m;
      hooks.onDraft(match);
      void refreshOffered();
    },
  );
  if (row?.invalid) {
    setConditionError(editor, conditionErrorIndex(row.invalid), row.invalid);
  }

  const name = h('input', {
    class: 'cb-filter-name',
    type: 'text',
    placeholder: 'name',
    'aria-label': 'rule name',
    onkeydown(e: KeyboardEvent) {
      if (e.key === 'Enter' && !e.isComposing) {
        e.preventDefault();
        void save();
      }
    },
  }) as HTMLInputElement;
  name.value = row?.rule.name ?? seed?.name ?? '';
  const nameErr = h('p', { class: 'cb-filter-name-err', hidden: true });
  const showNameError = (msg: string | null) => {
    nameErr.hidden = msg === null;
    nameErr.textContent = msg ?? '';
  };

  function showError(err: unknown): void {
    if (err === null) {
      if (!row?.invalid || match.length) setConditionError(editor, -1, null);
      return;
    }
    const msg = message(err);
    setConditionError(editor, conditionErrorIndex(msg), msg);
  }

  async function save(): Promise<void> {
    const n = name.value.trim();
    const id = row ? row.rule.id : slug(n);
    showNameError(null);
    proposeErr.hidden = true;
    if (!disposition) {
      proposeErr.textContent = 'choose what the rule proposes';
      proposeErr.hidden = false;
      return;
    }
    try {
      await ctx.api.post<RuleDetailView>(
        row ? '/rules/save' : '/rules/save?create=1',
        {
          id,
          name: n,
          match,
          propose: { disposition, note: note.value.trim() },
        },
      );
      hooks.onSaved(id);
    } catch (err) {
      const msg = message(err);
      if (err instanceof ApiError && err.status === 409) {
        showNameError(msg);
        return;
      }
      const i = conditionErrorIndex(msg);
      if (i < 0 && /name|rule id/.test(msg)) {
        showNameError(msg);
        return;
      }
      if (i < 0 && /propose|disposition/.test(msg)) {
        proposeErr.textContent = msg;
        proposeErr.hidden = false;
        return;
      }
      setConditionError(editor, i, msg);
    }
  }

  const el = h(
    'div',
    { class: 'kit-doc' },
    h(
      'article',
      { class: 'cb-filter-edit', 'data-testid': 'rule-editor' },
      h(
        'h1',
        { class: 'kit-h1 cb-filter-title' },
        row ? `edit ${row.rule.name || row.rule.id}` : 'new rule',
      ),
      h(
        'div',
        { class: 'cb-filter-acts' },
        name,
        h(
          'button',
          {
            type: 'button',
            class: 'kit-btn fill cb-filter-save',
            onclick() {
              void save();
            },
          },
          'save',
        ),
        h(
          'button',
          {
            type: 'button',
            class: 'kit-btn cb-filter-cancel',
            onclick() {
              hooks.onCancel();
            },
          },
          'cancel',
        ),
      ),
      nameErr,
      h(
        'div',
        { class: 'cb-rule-propose' },
        h('span', { class: 'cb-rule-propose-l' }, 'propose'),
        dispEl,
        note,
      ),
      proposeErr,
      h(
        'p',
        { class: 'cb-rule-hint' },
        'Saving turns it on: matching items get this recommendation now and after every sync. Nothing is decided until you accept it.',
      ),
      editor,
    ),
  );
  void getVocab(ctx).then((v) => {
    vocab = v;
    paintDisps();
  });
  void refreshOffered();
  return { el, showError };
}
