// conditions.ts — a rule's or a filter's conditions table and its
// `+ condition` dropdown (spec §4.1, §4.3).
//
// conditionEditor(ctx, rule, onChange) draws the [[match]] list as the
// reading column's hairline table: field · operator · value. A draft's table
// is editable, one row per field (OR within a field, AND between fields):
// a fixed-value field (enum, bool) ticks its values (several are one in or
// not-in condition) with "is" / "is not", never free text; a text field
// takes a value with "is" / "is not" (title also "matches pattern"); a
// duration or count field keeps its operators. Each row has a remove
// button. `+ condition` opens a dropdown over the editor (it moves
// nothing): a search box, then the fields that apply to every type the type
// condition leaves, a field already used shown but not offered. An active
// rule's table is read-only: serve only edits drafts.
//
// Fields, operators and values show serve's plain labels (GET
// /api/rules/vocabulary); the conditions and the files keep the raw names:
// data-name / data-value carry them.
//
// Every edit calls onChange with the new list at once; re-previewing (and its
// 300 ms debounce) is the page's job. Validation is serve's:
// setConditionError shows its message under the condition it names.

import { h } from '/_kit/kit.js';
import type {
  Condition,
  Field,
  NoteToken,
  Operator,
  ValueCount,
  ValuesView,
  VocabularyView,
} from './wire.d.ts';
import type { Ctx } from './app.ts';
import {
  conditionText,
  fieldApplies,
  fieldFound,
  fieldLabel,
  fieldTypes,
  opLabel,
  pickedCondition,
  pickedValues,
  typesOf,
  valueLabel,
} from './rules-text.ts';

// Field types, as serve's rules.FieldType numbers them.
const ENUM = 0;
const DURATION = 2;
const BOOL = 3;
const COUNT = 4;

let vocab: Promise<VocabularyView> | null = null;

// The message each editor shows (setConditionError), to redraw it with.
const errors = new WeakMap<HTMLElement, { index: number; message: string }>();

// ruleVocabularyView fetches serve's rule vocabulary once.
function ruleVocabularyView(ctx: Ctx): Promise<VocabularyView> {
  if (!vocab) {
    vocab = ctx.api.get<VocabularyView>('/rules/vocabulary');
    vocab.catch(() => {
      vocab = null; // try again next time
    });
  }
  return vocab;
}

/** ruleVocabulary is the field vocabulary (fetched once). */
export function ruleVocabulary(ctx: Ctx): Promise<Field[]> {
  return ruleVocabularyView(ctx).then((v) => v.fields ?? []);
}

/** ruleOperators are every operator with its label (fetched once). */
function ruleOperators(ctx: Ctx): Promise<Operator[]> {
  return ruleVocabularyView(ctx).then((v) => v.operators ?? []);
}

/** noteTokens are the placeholders a rule's note may use (serve's). */
export function noteTokens(ctx: Ctx): Promise<NoteToken[]> {
  return ruleVocabularyView(ctx).then((v) => v.note_tokens ?? []);
}

// picks reports whether a field's values are ticked from a fixed list.
function picks(f: Field): boolean {
  return f.type === BOOL || (f.type === ENUM && (f.values?.length ?? 0) > 0);
}

function choices(f: Field): string[] {
  return f.type === BOOL ? ['true', 'false'] : (f.values ?? []);
}

// TYPED are the fields whose values are picked from what casebook has seen,
// or typed (serve's GET /api/rules/values).
const TYPED = new Set(['repo', 'owner', 'author', 'label']);
function typed(f: Field): boolean {
  return TYPED.has(f.name);
}

const NOUNS: Record<string, string> = {
  repo: 'repositories',
  owner: 'owners',
  author: 'authors',
  label: 'labels',
};

// globRe is a value pattern as a regular expression: * matches within one
// path segment (it never crosses a /), as serve's path.Match does.
export function globRe(p: string): RegExp {
  const body = p
    .split('*')
    .map((s) => s.replace(/[.+?^${}()|[\]\\]/g, '\\$&'))
    .join('[^/]*');
  return new RegExp(`^${body}$`);
}

/**
 * patternSuggestions are the * patterns offered above a field's values: a
 * typed pattern with what it matches; for repositories, owner/* for an
 * owner the text finds and * /name for a name under several owners.
 */
export function patternSuggestions(
  field: string,
  all: ValueCount[],
  typedQ: string,
): { value: string; count: number; hint: string }[] {
  const out: { value: string; count: number; hint: string }[] = [];
  const sum = (xs: ValueCount[]) => xs.reduce((n, x) => n + x.count, 0);
  if (typedQ.includes('*')) {
    const re = globRe(typedQ);
    const m = all.filter((x) => re.test(x.value));
    out.push({ value: typedQ, count: sum(m), hint: `${m.length} match` });
    return out;
  }
  if (field !== 'repo' || !typedQ) return out;
  const q = typedQ.toLowerCase();
  const by = (part: 0 | 1) => {
    const g = new Map<string, ValueCount[]>();
    for (const x of all) {
      const k = x.value.split('/')[part] ?? '';
      const xs = g.get(k) ?? [];
      xs.push(x);
      g.set(k, xs);
    }
    return g;
  };
  for (const [owner, xs] of by(0)) {
    if (xs.length > 1 && owner.toLowerCase().includes(q))
      out.push({
        value: `${owner}/*`,
        count: sum(xs),
        hint: `all ${xs.length} repos`,
      });
  }
  for (const [name, xs] of by(1)) {
    if (xs.length > 1 && name.toLowerCase().includes(q))
      out.push({
        value: `*/${name}`,
        count: sum(xs),
        hint: `under ${xs.length} owners`,
      });
  }
  return out.sort((a, b) => b.count - a.count);
}

const seen = new Map<string, Promise<ValueCount[]>>();
// seenValues fetches a field's values once per page load.
function seenValues(ctx: Ctx, field: string): Promise<ValueCount[]> {
  let p = seen.get(field);
  if (!p) {
    p = ctx.api
      .get<ValuesView>(`/rules/values?field=${encodeURIComponent(field)}`)
      .then((v) => v.values ?? [])
      .catch(() => []);
    seen.set(field, p);
  }
  return p;
}

// shownOps are the operators a row offers: is / is not for a fixed-value
// field (a bool, is alone), a text field's without the lists; in and
// not-in are ticked, never chosen.
function shownOps(f: Field): string[] {
  if (f.type === BOOL) return ['is'];
  if (picks(f) || typed(f)) return ['is', 'is-not'];
  return (f.ops ?? []).filter((o) => o !== 'in' && o !== 'not-in');
}

// placeholder is a real example: the field's help's "e.g. …".
function placeholder(f: Field, op: string): string {
  if (f.type === DURATION) return '<n>h, <n>d or <n>w';
  if (f.type === COUNT) return 'a count, e.g. 0';
  if (op === 'matches') return 'a regular expression';
  return /e\.g\. ([^\s,;]+)/.exec(f.help ?? '')?.[1] ?? '';
}

// A new condition: the field's first operator and, ticked, its first value.
function newCondition(f: Field): Condition {
  const op = shownOps(f)[0] ?? f.ops?.[0] ?? 'is';
  // A fixed-value field starts with nothing ticked (a yes/no field with its
  // first value): ticking is the choice.
  const first = picks(f) && f.type !== ENUM ? (choices(f)[0] ?? '') : '';
  return { field: f.name, op, value: first };
}

// select is a picker of options' values, each shown by its label.
function select(
  cls: string,
  label: string,
  options: string[],
  labelOf: (v: string) => string,
  value: string,
  onPick: (v: string) => void,
): HTMLSelectElement {
  const el = h(
    'select',
    { class: cls, 'aria-label': label },
    ...options.map((o) => h('option', { value: o }, labelOf(o))),
  ) as HTMLSelectElement;
  if (!options.includes(value)) {
    el.prepend(h('option', { value }, labelOf(value)));
  }
  el.value = value;
  el.addEventListener('change', () => onPick(el.value));
  return el;
}

/**
 * conditionEditor draws a rule's conditions: editable for a draft (or when
 * rule.editable says so: an invalid active rule, to be fixed), read-only for
 * an active rule. onChange receives the whole new list after every edit.
 */
export function conditionEditor(
  ctx: Ctx,
  rule: { match: Condition[] | null; status: string; editable?: boolean },
  onChange: (match: Condition[]) => void,
): HTMLElement {
  const editable = rule.editable ?? rule.status === 'draft';
  const conds: Condition[] = (rule.match ?? []).map((c) => ({ ...c }));
  let fields: Field[] = [];
  let ops: Operator[] = [];
  const fieldOf = (name: string) => fields.find((f) => f.name === name);

  const el = h('div', {
    class: 'kit-table cb-conds',
    'data-testid': 'conditions',
  });
  const rowsEl = h('div', { class: 'cb-cond-rows' });
  const general = h('div', { class: 'cb-cond-err', hidden: true });
  el.append(rowsEl, general);

  const changed = () => {
    if (editable && fields.length) fillMenu();
    onChange(conds.map((c) => ({ ...c })));
  };

  // pickControl ticks a fixed-value field's values: several for an enum
  // (one in or not-in condition), one for a bool.
  function pickControl(c: Condition, f: Field): HTMLElement {
    const multi = f.type === ENUM;
    const wrap = h('div', {
      class: 'cb-cond-v cb-cond-picks',
      role: 'group',
      'aria-label': `${fieldLabel(f)} value`,
      'data-value': c.value,
    });
    const paint = () => {
      const { values } = pickedValues(c);
      wrap.dataset.value = c.value;
      for (const b of wrap.querySelectorAll<HTMLElement>('.cb-cond-pick')) {
        const on = values.includes(b.dataset.value ?? '');
        b.classList.toggle('on', on);
        b.setAttribute('aria-pressed', String(on));
      }
    };
    for (const v of choices(f)) {
      wrap.append(
        h(
          'button',
          {
            type: 'button',
            class: 'kit-chip cb-cond-pick',
            'data-value': v,
            onclick() {
              const { not, values } = pickedValues(c);
              if (multi) {
                // Kept in the order ticked: "type is issue or pull request".
                const next = values.includes(v)
                  ? values.filter((x) => x !== v)
                  : [...values, v];
                Object.assign(c, pickedCondition(c.field, not, next));
              } else {
                c.value = v;
              }
              paint();
              changed();
            },
          },
          valueLabel(f, v),
        ),
      );
    }
    paint();
    return wrap;
  }

  function valueControl(i: number, f: Field | undefined): HTMLElement {
    const c = conds[i];
    if (!editable || !f) {
      const shown =
        c.op === 'in' || c.op === 'not-in'
          ? pickedValues(c)
              .values.map((v) => valueLabel(f, v))
              .join(' or ')
          : valueLabel(f, c.value);
      return h('span', { class: 'cb-cond-v', 'data-value': c.value }, shown);
    }
    if (picks(f)) return pickControl(c, f);
    if (typed(f)) return typedControl(c, f);
    if (f.type === DURATION) return durationControl(c);
    const input = h('input', {
      class: 'cb-cond-v',
      type: 'text',
      value: c.value,
      placeholder: placeholder(f, c.op),
      spellcheck: false,
      'aria-label': `${fieldLabel(f)} value`,
    }) as HTMLInputElement;
    input.addEventListener('input', () => {
      c.value = input.value;
      changed();
    });
    return input;
  }

  // typedControl is a pick-or-type field's values: a chip per value (× takes
  // it off) and a box that suggests the values casebook has seen, most items
  // first, floating under it (it moves nothing); Enter adds the suggestion
  // or, with none, the text as typed. Several values are "or".
  function typedControl(c: Condition, f: Field): HTMLElement {
    const chips = h('span', { class: 'cb-cond-typed-chips' });
    const input = h('input', {
      class: 'cb-cond-typed-in',
      type: 'text',
      placeholder: 'search or type',
      spellcheck: false,
      autocomplete: 'off',
      'aria-label': `${fieldLabel(f)} value`,
    }) as HTMLInputElement;
    const list = h('div', {
      class: 'cb-cond-sugg',
      hidden: true,
      role: 'listbox',
    });
    const wrap = h(
      'div',
      { class: 'cb-cond-v cb-cond-typed', 'data-value': c.value },
      chips,
      h('span', { class: 'cb-cond-typed-box' }, input, list),
    );
    let at = 0;
    let shown: string[] = [];
    const values = () => pickedValues(c);
    const set = (next: string[]) => {
      Object.assign(c, pickedCondition(c.field, values().not, next));
      wrap.dataset.value = c.value;
      paint();
      changed();
    };
    const add = (v: string) => {
      const t = v.trim();
      if (!t || values().values.includes(t)) return;
      set([...values().values, t]);
      input.value = '';
      void suggest();
    };
    function paint(): void {
      chips.replaceChildren(
        ...values().values.map((v) =>
          h(
            'span',
            {
              class: 'kit-chip cb-cond-pick on cb-cond-typed-chip',
              'data-value': v,
            },
            v,
            h(
              'button',
              {
                type: 'button',
                class: 'cb-cond-typed-x',
                'aria-label': `remove ${v}`,
                onclick() {
                  set(values().values.filter((x) => x !== v));
                },
              },
              '\u00d7',
            ),
          ),
        ),
      );
    }
    function mark(): void {
      list
        .querySelectorAll<HTMLElement>('.cb-cond-sugg-row')
        .forEach((r, i) => {
          r.classList.toggle('on', i === at);
        });
    }
    const option = (value: string, count: number, hint: string, pat: boolean) =>
      h(
        'div',
        {
          class: pat ? 'cb-cond-sugg-row cb-cond-sugg-pat' : 'cb-cond-sugg-row',
          role: 'option',
          'data-value': value,
          onpointerdown(e: PointerEvent) {
            e.preventDefault(); // keep focus in the box
          },
          onclick() {
            add(value);
          },
        },
        h(
          'span',
          { class: 'cb-cond-sugg-v' },
          value,
          hint
            ? h('span', { class: 'cb-cond-sugg-hint' }, ` \u00b7 ${hint}`)
            : null,
        ),
        h('span', { class: 'cb-cond-sugg-n' }, String(count)),
      );
    async function suggest(): Promise<void> {
      const typedQ = input.value.trim();
      const q = typedQ.toLowerCase();
      const have = new Set(values().values);
      const all = await seenValues(ctx, f.name);
      // Text with a * lists what the pattern matches; other text, what
      // contains it.
      const re = typedQ.includes('*') ? globRe(typedQ) : null;
      const hits = all.filter(
        (x) =>
          !have.has(x.value) &&
          (re ? re.test(x.value) : !q || x.value.toLowerCase().includes(q)),
      );
      const pats = patternSuggestions(f.name, all, typedQ).filter(
        (p) => !have.has(p.value),
      );
      shown = [...pats.map((p) => p.value), ...hits.map((x) => x.value)];
      at = 0;
      const noun = NOUNS[f.name] ?? 'values';
      list.replaceChildren(
        h(
          'div',
          { class: 'cb-cond-sugg-head' },
          `${hits.length} ${noun}${q ? ` matching \u201c${typedQ}\u201d` : ''}`,
        ),
        ...pats.map((p) => option(p.value, p.count, p.hint, true)),
        ...hits.map((x) => option(x.value, x.count, '', false)),
      );
      list.hidden = document.activeElement !== input || shown.length === 0;
      mark();
    }
    input.addEventListener('focus', () => void suggest());
    input.addEventListener('input', () => void suggest());
    input.addEventListener('blur', () => {
      list.hidden = true;
    });
    input.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.isComposing) return;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        if (!shown.length) return;
        at =
          (at + (e.key === 'ArrowDown' ? 1 : -1) + shown.length) % shown.length;
        mark();
      } else if (e.key === 'Enter') {
        e.preventDefault();
        const q = input.value.trim();
        // A typed value exactly as written wins over a suggestion that
        // only contains it.
        add(
          !list.hidden && shown[at] && shown[at] !== q && !shown.includes(q)
            ? shown[at]
            : q,
        );
      } else if (e.key === 'Escape' && !list.hidden) {
        e.preventDefault();
        e.stopPropagation();
        list.hidden = true;
      } else if (
        e.key === 'Backspace' &&
        input.value === '' &&
        values().values.length
      ) {
        set(values().values.slice(0, -1));
      }
    });
    paint();
    return wrap;
  }

  // durationControl is a duration as a number and a unit ("2" "days"),
  // stored as casebook's <n>h / <n>d / <n>w; "ago" follows "more than".
  function durationControl(c: Condition): HTMLElement {
    if (c.op === 'before' || c.op === 'on-or-after') return dateControl(c);
    const m = /^(\d+)([hdw])$/.exec(c.value.trim());
    const num = h('input', {
      class: 'cb-cond-num',
      type: 'number',
      min: '1',
      step: '1',
      value: m ? m[1] : '',
      placeholder: '2',
      'aria-label': 'how many',
    }) as HTMLInputElement;
    const unit = h(
      'select',
      { class: 'cb-cond-unit', 'aria-label': 'unit' },
      ...(
        [
          ['h', 'hours'],
          ['d', 'days'],
          ['w', 'weeks'],
        ] as const
      ).map(([v, l]) => h('option', { value: v }, l)),
    ) as HTMLSelectElement;
    unit.value = m ? m[2] : 'd';
    const set = () => {
      const n = num.value.trim();
      c.value =
        /^\d+$/.test(n) && Number(n) > 0 ? `${Number(n)}${unit.value}` : '';
      wrap.dataset.value = c.value;
      changed();
    };
    num.addEventListener('input', set);
    unit.addEventListener('change', set);
    const wrap = h(
      'span',
      { class: 'cb-cond-v cb-cond-dur', 'data-value': c.value },
      num,
      unit,
      c.op === 'older-than' ? h('span', { class: 'cb-cond-ago' }, 'ago') : null,
    );
    return wrap;
  }

  // dateControl is a calendar day (the browser's date picker), stored as
  // YYYY-MM-DD.
  function dateControl(c: Condition): HTMLElement {
    const day = h('input', {
      class: 'cb-cond-date',
      type: 'date',
      value: /^\d{4}-\d{2}-\d{2}$/.test(c.value) ? c.value : '',
      'aria-label': 'date',
    }) as HTMLInputElement;
    const wrap = h(
      'span',
      { class: 'cb-cond-v cb-cond-dur', 'data-value': c.value },
      day,
    );
    day.addEventListener('input', () => {
      c.value = day.value;
      wrap.dataset.value = c.value;
      changed();
    });
    return wrap;
  }

  // opControl is the row's operator: a picked field's is / is not stands
  // for in / not-in too (several values ticked).
  function opControl(c: Condition, f: Field | undefined): HTMLElement {
    // A list reads as is / is not ("type is issue or pull request").
    const readOp = c.op === 'in' ? 'is' : c.op === 'not-in' ? 'is-not' : c.op;
    if (!editable || !f) {
      return h(
        'span',
        { class: 'cb-cond-o', 'data-value': c.op },
        opLabel(ops, readOp),
      );
    }
    if (picks(f) || typed(f)) {
      return select(
        'cb-cond-o',
        `${fieldLabel(f)} operator`,
        shownOps(f),
        (v) => opLabel(ops, v),
        f.type === BOOL ? c.op : readOp,
        (v) => {
          if (f.type === BOOL) c.op = v;
          else {
            const { values } = pickedValues(c);
            Object.assign(c, pickedCondition(c.field, v === 'is-not', values));
          }
          rowsEl
            .querySelector<HTMLElement>(
              `.cb-cond[data-index="${conds.indexOf(c)}"] .cb-cond-picks, .cb-cond[data-index="${conds.indexOf(c)}"] .cb-cond-typed`,
            )
            ?.setAttribute('data-value', c.value);
          changed();
        },
      );
    }
    return select(
      'cb-cond-o',
      `${fieldLabel(f)} operator`,
      shownOps(f),
      (v) => opLabel(ops, v),
      c.op,
      (v) => {
        // A date and a duration don't share a value: switching clears it.
        const isDay = (o: string) => o === 'before' || o === 'on-or-after';
        if (isDay(v) !== isDay(c.op)) c.value = '';
        c.op = v;
        const input = rowsEl.querySelector<HTMLInputElement>(
          `.cb-cond[data-index="${conds.indexOf(c)}"] input.cb-cond-v`,
        );
        if (input) input.placeholder = placeholder(f, v);
        // A duration's "ago" follows "more than" only.
        const ago = rowsEl.querySelector<HTMLElement>(
          `.cb-cond[data-index="${conds.indexOf(c)}"] .cb-cond-dur`,
        );
        if (ago) ago.replaceWith(durationControl(c));
        changed();
      },
    );
  }

  function row(i: number): HTMLElement {
    const c = conds[i];
    const f = fieldOf(c.field);
    const remove = editable
      ? h(
          'button',
          {
            type: 'button',
            class: 'cb-cond-rm',
            'aria-label': `remove ${conditionText(c, fields, ops)}`,
            onclick() {
              conds.splice(i, 1);
              render();
              changed();
            },
          },
          '\u00d7',
        )
      : null;
    return h(
      'div',
      { class: 'cb-cond', 'data-index': i },
      h(
        'div',
        { class: 'kit-tr cb-cond-row' + (editable ? ' cb-cond-edit' : '') },
        h(
          'span',
          { class: 'cb-cond-f', 'data-name': c.field },
          f ? fieldLabel(f) : c.field,
        ),
        opControl(c, f),
        valueControl(i, f),
        remove,
      ),
      h('div', { class: 'cb-cond-err', hidden: true }),
    );
  }

  function render(): void {
    rowsEl.replaceChildren(...conds.map((_, i) => row(i)));
    // A message serve gave stays with its condition across a redraw (the
    // vocabulary arriving redraws the rows).
    const e = errors.get(el);
    if (e) setConditionError(el, e.index, e.message);
  }

  // ---- + condition: a dropdown over the editor -----------------------------

  const search = h('input', {
    class: 'cb-cond-search',
    type: 'text',
    placeholder: 'find a field',
    spellcheck: false,
    'aria-label': 'find a field',
    autocomplete: 'off',
  }) as HTMLInputElement;
  const options = h('div', {
    class: 'cb-cond-menu-list',
    role: 'listbox',
    'aria-label': 'fields',
  });
  const menu = h(
    'div',
    { class: 'cb-cond-menu', hidden: true },
    search,
    options,
  );
  const add = h(
    'button',
    {
      type: 'button',
      class: 'cb-cond-add',
      'aria-expanded': 'false',
      'aria-haspopup': 'listbox',
      onclick() {
        setMenu(menu.hidden);
      },
    },
    '+ condition',
  );
  const addWrap = h('div', { class: 'cb-cond-add-wrap' }, add, menu);
  // Focus leaving it (Tab) closes it.
  addWrap.addEventListener('focusout', (e: FocusEvent) => {
    if (!menu.hidden && !addWrap.contains(e.relatedTarget as Node | null))
      setMenu(false);
  });

  // Esc closes the open dropdown wherever focus is (it is part of the
  // document, not a sheet: the page's keys are not suspended for it).
  function onEsc(e: KeyboardEvent): void {
    if (e.key !== 'Escape' || e.isComposing) return;
    if (!el.isConnected) {
      document.removeEventListener('keydown', onEsc, true);
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    setMenu(false);
    add.focus();
  }
  // A click outside closes it.
  function onDown(e: PointerEvent): void {
    if (!el.isConnected || !addWrap.contains(e.target as Node)) setMenu(false);
  }

  function setMenu(open: boolean): void {
    // The chips are always shown in an editable table: nothing closes them.
    if (editable) return;
    menu.hidden = !open;
    add.setAttribute('aria-expanded', String(open));
    if (open) {
      search.value = '';
      fillMenu();
      document.addEventListener('keydown', onEsc, true);
      document.addEventListener('pointerdown', onDown, true);
      search.focus();
    } else {
      document.removeEventListener('keydown', onEsc, true);
      document.removeEventListener('pointerdown', onDown, true);
    }
  }

  function addCondition(f: Field): void {
    conds.push(newCondition(f));
    render();
    changed();
    const last = rowsEl.lastElementChild;
    last?.querySelector<HTMLElement>('.cb-cond-v, .cb-cond-pick')?.focus();
  }

  // offered are the dropdown's rows a pick may choose (not a used field).
  const offered = () => [
    ...options.querySelectorAll<HTMLElement>(
      '.cb-cond-menu-row:not([aria-disabled="true"])',
    ),
  ];
  function setActive(to: HTMLElement | undefined): void {
    for (const r of options.querySelectorAll<HTMLElement>(
      '.cb-cond-menu-row',
    )) {
      const on = r === to;
      r.classList.toggle('on', on);
      r.setAttribute('aria-selected', String(on));
    }
    to?.scrollIntoView({ block: 'nearest' });
  }

  // fillMenu lists the fields that apply to every type the type condition
  // leaves (with none, every field, a field that applies to some types
  // naming them), narrowed by the search; a used field shows, not offered.
  // fillMenu shows every field that applies, as chips grouped by the types
  // it applies to (Court, 2026-10-17: the whole list at once, short); a
  // chip's hint is its tooltip; a used field is greyed.
  function fillMenu(): void {
    const kind = fieldOf('kind');
    const types = typesOf(conds, kind?.values ?? []);
    const used = new Set(conds.map((c) => c.field));
    const groups = new Map<string, HTMLElement[]>();
    for (const f of fields) {
      if (!fieldFound(f, search.value)) continue;
      // A chip never disappears (the block keeps its height): a used field
      // or one the chosen types don't have is greyed.
      const applies = fieldApplies(f, types);
      const taken = used.has(f.name) || !applies;
      const where = fieldTypes(f, kind) || 'any type';
      const chip = h(
        'button',
        {
          type: 'button',
          class: 'cb-cond-menu-row',
          'data-field': f.name,
          'aria-disabled': String(taken),
          title: used.has(f.name)
            ? 'already used, change it below'
            : !applies
              ? `doesn't apply to the chosen type`
              : (f.help ?? ''),
          onclick() {
            if (!taken) addCondition(f);
          },
        },
        fieldLabel(f),
      );
      if (!groups.has(where)) groups.set(where, []);
      groups.get(where)!.push(chip);
    }
    options.replaceChildren(
      ...[...groups].map(([where, chips]) =>
        h(
          'div',
          { class: 'cb-cond-group' },
          h('span', { class: 'cb-cond-group-l' }, where),
          h('span', { class: 'cb-cond-group-c' }, ...chips),
        ),
      ),
    );
  }

  search.addEventListener('input', () => fillMenu());
  search.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.isComposing) return;
    const list = offered();
    const at = list.findIndex((r) => r.classList.contains('on'));
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!list.length) return;
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setActive(list[(at + step + list.length) % list.length]);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const f = fieldOf(list[at]?.dataset.field ?? '');
      if (f) addCondition(f);
    }
  });

  render();
  if (editable) {
    // The field chips go first and keep their height; conditions are added
    // under them, so the editor only grows downward (Court, 2026-10-17).
    menu.hidden = false;
    add.hidden = true;
    el.prepend(addWrap);
  }
  // The vocabulary labels the rows, makes a draft's editable and fills its
  // dropdown. A read-only table without it still reads, in raw names.
  void Promise.all([ruleVocabulary(ctx), ruleOperators(ctx)])
    .then(([fs, os]) => {
      fields = fs;
      ops = os;
      render();
      if (editable) fillMenu();
      el.dataset.ready = 'true';
    })
    .catch((err: unknown) => {
      if (!editable) {
        el.dataset.ready = 'true';
        return;
      }
      general.textContent =
        err instanceof Error ? err.message : 'the vocabulary did not load';
      general.hidden = false;
    });
  return el;
}

/**
 * setConditionError shows serve's validation message under the condition it
 * names (index), or under the table when it names none (-1); null clears it.
 */
export function setConditionError(
  editor: HTMLElement,
  index: number,
  message: string | null,
): void {
  if (message === null) errors.delete(editor);
  else errors.set(editor, { index, message });
  for (const e of editor.querySelectorAll<HTMLElement>('.cb-cond-err')) {
    e.hidden = true;
    e.textContent = '';
  }
  for (const r of editor.querySelectorAll('.cb-cond[data-invalid]')) {
    r.removeAttribute('data-invalid');
  }
  if (message === null) return;
  const r = editor.querySelector<HTMLElement>(
    `.cb-cond[data-index="${index}"]`,
  );
  const slot = r
    ? r.querySelector<HTMLElement>('.cb-cond-err')
    : editor.querySelector<HTMLElement>(':scope > .cb-cond-err');
  r?.setAttribute('data-invalid', '');
  if (slot) {
    slot.textContent = message;
    slot.hidden = false;
  }
}
