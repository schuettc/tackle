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

// shownOps are the operators a row offers: is / is not for a fixed-value
// field (a bool, is alone), a text field's without the lists; in and
// not-in are ticked, never chosen.
function shownOps(f: Field): string[] {
  if (f.type === BOOL) return ['is'];
  if (picks(f)) return ['is', 'is-not'];
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
  return { field: f.name, op, value: picks(f) ? (choices(f)[0] ?? '') : '' };
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

  const changed = () => onChange(conds.map((c) => ({ ...c })));

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
    if (picks(f)) {
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
              `.cb-cond[data-index="${conds.indexOf(c)}"] .cb-cond-picks`,
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
        c.op = v;
        const input = rowsEl.querySelector<HTMLInputElement>(
          `.cb-cond[data-index="${conds.indexOf(c)}"] input.cb-cond-v`,
        );
        if (input) input.placeholder = placeholder(f, v);
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
    setMenu(false);
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
  function fillMenu(): void {
    const kind = fieldOf('kind');
    const types = typesOf(conds, kind?.values ?? []);
    const used = new Set(conds.map((c) => c.field));
    options.replaceChildren(
      ...fields
        .filter((f) => fieldApplies(f, types) && fieldFound(f, search.value))
        .map((f) => {
          const taken = used.has(f.name);
          const where = types ? '' : fieldTypes(f, kind);
          const hint = taken
            ? 'already used, change it above'
            : [f.help, where].filter((x) => x).join(' \u00b7 ');
          return h(
            'div',
            {
              class: 'cb-cond-menu-row',
              role: 'option',
              'data-field': f.name,
              'aria-disabled': String(taken),
              'aria-selected': 'false',
              onpointerdown(e: PointerEvent) {
                e.preventDefault(); // keep focus in the search box
              },
              onclick() {
                if (!taken) addCondition(f);
              },
            },
            h('span', { class: 'cb-cond-menu-f' }, fieldLabel(f)),
            hint ? h('span', { class: 'cb-cond-menu-help' }, hint) : null,
          );
        }),
    );
    setActive(offered()[0]);
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
  if (editable) el.append(addWrap);
  // The vocabulary labels the rows, makes a draft's editable and fills its
  // dropdown. A read-only table without it still reads, in raw names.
  void Promise.all([ruleVocabulary(ctx), ruleOperators(ctx)])
    .then(([fs, os]) => {
      fields = fs;
      ops = os;
      render();
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
