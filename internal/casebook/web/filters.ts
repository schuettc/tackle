// filters.ts — the Attention list's saved filters: a row of chips under the
// view chips, and the editor that opens in the middle pane.
//
// makeFilterBar(ctx, hooks) draws one toggle chip per filter (serve's
// GET /api/filters, in id order), each with a ⋯ menu (edit, delete), then
// `+ filter`. Selecting is the caller's (hooks.onSelect with the new
// selection; the address keeps it), and so is editing (hooks.onEdit: the
// page opens filterEditor beside the list). A filter that doesn't validate
// shows marked, with serve's message as its title: it can be edited or
// deleted, not selected.
//
// filterEditor(ctx, row, hooks) is the editor: its title, the name with
// save and cancel, then the rules' condition editor (conditions.ts), so
// adding a condition moves neither. It previews nothing itself: every edit
// goes to hooks.onDraft, and the page lists the draft's matches in the list
// on the left. serve validates: a 400 shows under the condition it names
// (showError), a 409 under the name.

import { h, ApiError } from '/_kit/kit.js';
import type { Condition, FilterRow, FiltersView } from './wire.d.ts';
import type { Ctx } from './app.ts';
import { conditionEditor, setConditionError } from './conditions.ts';
import { conditionErrorIndex, slug } from './rules-text.ts';
import { menuNav } from './menu-nav.ts';
import { toggleFilter } from './filters-text.ts';

export interface FilterBar {
  el: HTMLElement;
  paint(rows: FilterRow[], selected: string[]): void;
  setError(message: string | null): void;
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function makeFilterBar(
  ctx: Ctx,
  hooks: {
    onSelect(ids: string[]): void;
    onChanged(): void;
    onDeleted(id: string): void;
    onEdit(row: FilterRow | null): void;
    onMakeRule?(row: FilterRow): void;
  },
): FilterBar {
  let rows: FilterRow[] = [];
  let selected: string[] = [];

  const chipsEl = h('div', {
    class: 'kit-chips cb-filter-chips',
    'data-group': 'filters',
  });
  const addBtn = h(
    'button',
    {
      type: 'button',
      class: 'kit-chip cb-filter-add',
      onclick() {
        hooks.onEdit(null);
      },
    },
    '+ filter',
  );
  const errEl = h('p', { class: 'cb-filter-err', hidden: true });
  const el = h(
    'div',
    { class: 'cb-filters', 'aria-label': 'saved filters' },
    h('span', { class: 'cb-bar-label' }, 'filters'),
    chipsEl,
    errEl,
  );

  // ---- the chips ---------------------------------------------------------

  // menuOpen is the chip whose ⋯ menu is open (its id), so a repaint keeps
  // it open.
  let closeMenu: (() => void) | null = null;

  function chip(row: FilterRow): HTMLElement {
    const id = row.filter.id;
    const on = selected.includes(id);
    const b = h(
      'button',
      {
        type: 'button',
        class: 'kit-chip cb-filter' + (on ? ' on' : ''),
        'data-id': id,
        'aria-pressed': String(on),
        onclick() {
          const next = toggleFilter(rows, selected, id);
          if (next !== selected) hooks.onSelect(next);
        },
      },
      row.filter.name || id,
    );
    if (row.invalid) {
      b.setAttribute('data-invalid', '');
      b.setAttribute('aria-disabled', 'true');
      b.title = row.invalid;
    }
    const more = h('button', {
      type: 'button',
      class: 'cb-filter-more',
      'aria-label': `${row.filter.name || id}: edit or delete`,
      'aria-haspopup': 'menu',
      'aria-expanded': 'false',
      onclick() {
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
          onclick() {
            setMenu(false);
            run();
          },
        },
        label,
      );
    const menu = h(
      'div',
      { class: 'cb-filter-menu', role: 'menu', hidden: true },
      item('edit', () => hooks.onEdit(row)),
      ...(hooks.onMakeRule && !row.invalid
        ? [item('make a rule', () => hooks.onMakeRule?.(row))]
        : []),
      item('delete', () => void remove(id)),
    );
    const onEsc = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.isComposing) return;
      e.preventDefault();
      e.stopPropagation();
      setMenu(false);
      more.focus();
    };
    function setMenu(open: boolean): void {
      if (open) closeMenu?.();
      menu.hidden = !open;
      more.setAttribute('aria-expanded', String(open));
      if (open) {
        closeMenu = () => setMenu(false);
        document.addEventListener('keydown', onEsc, true);
        menu.querySelector<HTMLElement>('.cb-filter-menu-item')?.focus();
      } else {
        if (closeMenu) closeMenu = null;
        document.removeEventListener('keydown', onEsc, true);
      }
    }
    menuNav(menu, '.cb-filter-menu-item', more, () => setMenu(false));
    return h('span', { class: 'cb-filter-item', 'data-id': id }, b, more, menu);
  }

  function paint(r: FilterRow[], sel: string[]): void {
    rows = r;
    selected = sel;
    closeMenu?.();
    chipsEl.replaceChildren(...rows.map(chip), addBtn);
  }

  function setError(msg: string | null): void {
    errEl.hidden = msg === null;
    errEl.textContent = msg ?? '';
  }

  async function remove(id: string): Promise<void> {
    try {
      await ctx.api.post<FiltersView>('/filters/delete', { id });
      hooks.onDeleted(id);
      hooks.onChanged();
    } catch (err) {
      setError(message(err));
    }
  }

  paint([], []);
  return { el, paint, setError };
}

export interface FilterEditor {
  el: HTMLElement;
  /** showError shows serve's refusal of the draft (null clears it). */
  showError(err: unknown): void;
}

/**
 * filterEditor edits row (null: a new filter). hooks.onDraft gets the
 * conditions after every edit (and once at the start), hooks.onSaved the
 * saved filter's id, hooks.onCancel a cancel.
 */
export function filterEditor(
  ctx: Ctx,
  row: FilterRow | null,
  hooks: {
    onDraft(match: Condition[]): void;
    onSaved(id: string): void;
    onCancel(): void;
  },
): FilterEditor {
  let match: Condition[] = (row?.filter.match ?? []).map((c) => ({ ...c }));
  const editor = conditionEditor(
    ctx,
    { match, status: 'draft' },
    (m: Condition[]) => {
      match = m;
      hooks.onDraft(match);
    },
  );
  // A filter that doesn't validate opens with serve's reason under the
  // condition it names.
  if (row?.invalid) {
    setConditionError(editor, conditionErrorIndex(row.invalid), row.invalid);
  }

  const name = h('input', {
    class: 'cb-filter-name',
    type: 'text',
    placeholder: 'name',
    'aria-label': 'filter name',
    onkeydown(e: KeyboardEvent) {
      if (e.key === 'Enter' && !e.isComposing) {
        e.preventDefault();
        void save();
      }
    },
  }) as HTMLInputElement;
  if (row) name.value = row.filter.name;
  const nameErr = h('p', { class: 'cb-filter-name-err', hidden: true });
  function showNameError(msg: string | null): void {
    nameErr.hidden = msg === null;
    nameErr.textContent = msg ?? '';
  }

  function showError(err: unknown): void {
    if (err === null) {
      // A filter that doesn't validate keeps its reason until it is fixed.
      if (!row?.invalid || match.length) setConditionError(editor, -1, null);
      return;
    }
    const msg = message(err);
    setConditionError(editor, conditionErrorIndex(msg), msg);
  }

  async function save(): Promise<void> {
    const n = name.value.trim();
    const id = row ? row.filter.id : slug(n);
    showNameError(null);
    try {
      await ctx.api.post<FiltersView>(
        row ? '/filters/save' : '/filters/save?create=1',
        { id, name: n, match },
      );
      hooks.onSaved(id);
    } catch (err) {
      const msg = message(err);
      if (err instanceof ApiError && err.status === 409) {
        showNameError(msg);
        return;
      }
      const i = conditionErrorIndex(msg);
      if (i < 0 && /name|filter id/.test(msg)) {
        showNameError(msg);
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
      { class: 'cb-filter-edit', 'data-testid': 'filter-editor' },
      h(
        'h1',
        { class: 'kit-h1 cb-filter-title' },
        row ? `edit ${row.filter.name || row.filter.id}` : 'new filter',
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
      editor,
    ),
  );
  return { el, showError };
}
