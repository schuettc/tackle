// conditions.ts — a rule's conditions table and its `+ condition` editor
// (spec §4.1, §4.3).
//
// conditionEditor(ctx, rule, onChange) draws the rule's [[match]] list as the
// reading column's hairline table: field · operator · value. A draft's table
// is editable: each value is a field (a picker for an enum or bool with
// is / is-not), each operator a picker of the field's operators, each row
// has a remove button, and `+ condition` opens a menu of the vocabulary's
// fields with their operators (GET /api/rules/vocabulary); choosing an
// operator adds the condition. An active rule's table is read-only: serve
// only edits drafts.
//
// Every edit calls onChange with the new list at once; re-previewing (and its
// 300 ms debounce) is the page's job (rules.ts). Validation is serve's:
// setConditionError shows its message under the condition it names.

import { h } from '/_kit/kit.js';
import type { Condition, Field, VocabularyView } from './wire.d.ts';
import type { Ctx } from './app.ts';

// Field types, as serve's rules.FieldType numbers them.
const ENUM = 0;
const DURATION = 2;
const BOOL = 3;
const COUNT = 4;

let vocab: Promise<Field[]> | null = null;

/** ruleVocabulary fetches the field vocabulary once. */
export function ruleVocabulary(ctx: Ctx): Promise<Field[]> {
  if (!vocab) {
    vocab = ctx.api
      .get<VocabularyView>('/rules/vocabulary')
      .then((v) => v.fields ?? []);
    vocab.catch(() => {
      vocab = null; // try again next time
    });
  }
  return vocab;
}

// picks reports whether a condition's value is chosen from a list.
function picks(f: Field, op: string): boolean {
  if (f.type === BOOL) return true;
  return f.type === ENUM && (f.values?.length ?? 0) > 0 && /^is/.test(op);
}

function choices(f: Field): string[] {
  return f.type === BOOL ? ['true', 'false'] : (f.values ?? []);
}

function placeholder(f: Field, op: string): string {
  if (f.type === DURATION) return '<n>h, <n>d or <n>w';
  if (f.type === COUNT) return 'a count, e.g. 0';
  if (op === 'in' || op === 'not-in') return 'a, b, c';
  if (op === 'matches') return 'a regular expression';
  return '';
}

// A new condition's first value: a picker's first choice, else empty.
function firstValue(f: Field, op: string): string {
  return picks(f, op) ? (choices(f)[0] ?? '') : '';
}

function select(
  cls: string,
  label: string,
  options: string[],
  value: string,
  onPick: (v: string) => void,
): HTMLSelectElement {
  const el = h(
    'select',
    { class: cls, 'aria-label': label },
    ...options.map((o) => h('option', { value: o }, o)),
  ) as HTMLSelectElement;
  if (!options.includes(value)) {
    el.prepend(h('option', { value }, value));
  }
  el.value = value;
  el.addEventListener('change', () => onPick(el.value));
  return el;
}

/**
 * conditionEditor draws a rule's conditions. rule.status 'draft' is editable.
 * onChange receives the whole new list after every edit.
 */
export function conditionEditor(
  ctx: Ctx,
  rule: { match: Condition[] | null; status: string },
  onChange: (match: Condition[]) => void,
): HTMLElement {
  const editable = rule.status === 'draft';
  const conds: Condition[] = (rule.match ?? []).map((c) => ({ ...c }));
  let fields: Field[] = [];
  const fieldOf = (name: string) => fields.find((f) => f.name === name);

  const el = h('div', {
    class: 'kit-table cb-conds',
    'data-testid': 'conditions',
  });
  const rowsEl = h('div', { class: 'cb-cond-rows' });
  const general = h('div', { class: 'cb-cond-err', hidden: true });
  el.append(rowsEl, general);

  const changed = () => onChange(conds.map((c) => ({ ...c })));

  function valueControl(i: number, f: Field | undefined): HTMLElement {
    const c = conds[i];
    if (!editable || !f) {
      return h('span', { class: 'cb-cond-v' }, c.value);
    }
    if (picks(f, c.op)) {
      return select(
        'cb-cond-v',
        `${c.field} value`,
        choices(f),
        c.value,
        (v) => {
          c.value = v;
          changed();
        },
      );
    }
    const input = h('input', {
      class: 'cb-cond-v',
      type: 'text',
      value: c.value,
      placeholder: placeholder(f, c.op),
      spellcheck: false,
      'aria-label': `${c.field} value`,
    }) as HTMLInputElement;
    input.addEventListener('input', () => {
      c.value = input.value;
      changed();
    });
    return input;
  }

  function row(i: number): HTMLElement {
    const c = conds[i];
    const f = fieldOf(c.field);
    const op =
      editable && f
        ? select('cb-cond-o', `${c.field} operator`, f.ops ?? [], c.op, (v) => {
            const wasPick = picks(f, c.op);
            c.op = v;
            if (picks(f, v) !== wasPick) {
              c.value = firstValue(f, v);
              render();
            }
            changed();
          })
        : h('span', { class: 'cb-cond-o' }, c.op);
    const remove = editable
      ? h(
          'button',
          {
            type: 'button',
            class: 'cb-cond-rm',
            'aria-label': `remove ${c.field} ${c.op}`,
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
        h('span', { class: 'cb-cond-f' }, c.field),
        op,
        valueControl(i, f),
        remove,
      ),
      h('div', { class: 'cb-cond-err', hidden: true }),
    );
  }

  function render(): void {
    rowsEl.replaceChildren(...conds.map((_, i) => row(i)));
  }

  // ---- + condition ---------------------------------------------------------

  const menu = h('div', {
    class: 'cb-cond-menu',
    role: 'menu',
    'aria-label': 'add a condition',
    hidden: true,
  });
  const add = h(
    'button',
    {
      type: 'button',
      class: 'cb-cond-add',
      'aria-expanded': 'false',
      onclick() {
        setMenu(menu.hidden);
      },
    },
    '+ condition',
  );

  // Esc closes the open menu wherever focus is (the page's keys are not
  // suspended for it: it is part of the document, not a sheet).
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

  function setMenu(open: boolean): void {
    menu.hidden = !open;
    add.setAttribute('aria-expanded', String(open));
    if (open) {
      document.addEventListener('keydown', onEsc, true);
      menu.querySelector<HTMLElement>('.cb-cond-menu-op')?.focus();
    } else {
      document.removeEventListener('keydown', onEsc, true);
    }
  }

  function addCondition(f: Field, op: string): void {
    conds.push({ field: f.name, op, value: firstValue(f, op) });
    setMenu(false);
    render();
    changed();
    const last = rowsEl.lastElementChild;
    last?.querySelector<HTMLElement>('.cb-cond-v')?.focus();
  }

  function fillMenu(): void {
    menu.replaceChildren(
      ...fields.map((f) =>
        h(
          'div',
          { class: 'cb-cond-menu-row', 'data-field': f.name },
          h('span', { class: 'cb-cond-menu-f' }, f.name),
          h(
            'span',
            { class: 'cb-cond-menu-ops' },
            ...(f.ops ?? []).map((op) =>
              h(
                'button',
                {
                  type: 'button',
                  role: 'menuitem',
                  class: 'kit-chip cb-cond-menu-op',
                  'data-op': op,
                  onclick() {
                    addCondition(f, op);
                  },
                },
                op,
              ),
            ),
          ),
        ),
      ),
    );
  }

  render();
  if (editable) {
    el.append(add, menu);
    // The vocabulary makes the rows editable and fills the menu.
    void ruleVocabulary(ctx)
      .then((fs) => {
        fields = fs;
        fillMenu();
        render();
        el.dataset.ready = 'true';
      })
      .catch((err: unknown) => {
        general.textContent =
          err instanceof Error ? err.message : 'the vocabulary did not load';
        general.hidden = false;
      });
  } else {
    el.dataset.ready = 'true';
  }
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
