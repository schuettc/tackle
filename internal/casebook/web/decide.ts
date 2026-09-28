// decide.ts — the decide sheet (single and bulk) and selection wiring.
//
// openDecideSheet(ctx, keys, allowed, onDone) opens a kit sheet() with a
// disposition picker, an optional `until` field, a note, and a live preview
// ("close 4 items").  On success it calls onDone(keys) so the caller can
// deselect decided ids and trigger a refresh.
//
// wireSelection(ctx, listHandle) subscribes to the shared selection store and
// (a) updates the primary bar button ("Decide N"), and (b) updates the
// .cb-sel-count element in the list foot.  The "d" key is registered to open
// the decide sheet for the current selection.
//
// allowedForKind(kind) mirrors Go's item.Allowed() — the ONLY dispositions
// the server accepts for a given item kind.

import {
  sheet,
  h,
  noteField,
  type SheetHandle,
  type ListHandle,
} from '/_kit/kit.js';
import type { DecideResult, ItemView } from './wire.d.ts';
import type { Ctx } from './app.ts';

// ---- disposition tables (mirror Go item.Allowed()) -------------------------

// These must stay in sync with internal/casebook/item/decision.go.  The server
// validates the disposition anyway; this client-side table lets us show only
// valid choices and provide a clear "until required" message.
const KIND_ALLOWED: Record<string, string[]> = {
  repo: ['keep', 'archive', 'delete', 'wait', 'watch', 'ignore'],
  pr: ['keep', 'close', 'merge', 'wait', 'watch', 'ignore'],
  issue: ['keep', 'close', 'wait', 'watch', 'ignore'],
  branch: ['keep', 'delete', 'wait', 'watch', 'ignore'],
  worktree: ['keep', 'delete', 'wait', 'ignore'],
};

const FALLBACK_ALLOWED = ['keep', 'close', 'wait', 'watch', 'ignore'];

// Dispositions that require an `until` condition.
const NEEDS_UNTIL = new Set(['wait', 'watch']);

// Dispositions that are destructive (rendered with danger styling).
// Exported so item.ts can import it instead of duplicating.
export const DANGER_DISPS = new Set(['close', 'delete', 'archive']);

/** allowedForKind returns the valid dispositions for one item kind. */
export function allowedForKind(kind: string): string[] {
  return KIND_ALLOWED[kind] ?? FALLBACK_ALLOWED;
}

/**
 * kindFromKey extracts the kind from a key by taking the prefix before the
 * first colon.  "pr:schuettc/hail#3" → "pr".
 */
function kindFromKey(key: string): string {
  const i = key.indexOf(':');
  return i > 0 ? key.slice(0, i) : '';
}

/**
 * allowedForKeys returns the intersection of allowed dispositions for every
 * key in the slice, derived from each key's kind prefix.  This is the correct
 * way to compute allowed for a selection: it works for keys on unrendered
 * pages (where no ItemView is available) as well as rendered ones.
 */
export function allowedForKeys(keys: string[]): string[] {
  if (keys.length === 0) return FALLBACK_ALLOWED;
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  if (kinds.length === 0) return FALLBACK_ALLOWED;
  const sets = kinds.map((k) => new Set(allowedForKind(k)));
  const first = [...(sets[0] ?? new Set(FALLBACK_ALLOWED))];
  return first.filter((d) => sets.every((s) => s.has(d)));
}

// ---- until validation -------------------------------------------------------

// Validates an `until` string client-side (mirrors Go item.ParseUntil).
// Returns an error message, or null if valid.
function validateUntil(s: string): string | null {
  const v = s.trim();
  if (!v) {
    return 'until is required for wait/watch — e.g. date(2026-12-01) or inactive(90d)';
  }
  const open = v.indexOf('(');
  if (open <= 0 || !v.endsWith(')')) {
    return 'until must be one of: date(YYYY-MM-DD), merged(<pr>), closed(<pr|issue>), inactive(90d), released(<repo>)';
  }
  const op = v.slice(0, open);
  const arg = v.slice(open + 1, v.length - 1).trim();
  if (!['date', 'merged', 'closed', 'inactive', 'released'].includes(op)) {
    return `unknown until op "${op}": use date, merged, closed, inactive, or released`;
  }
  if (op === 'date' && !/^\d{4}-\d{2}-\d{2}$/.test(arg)) {
    return 'date must be YYYY-MM-DD';
  }
  return null;
}

// ---- openDecideSheet --------------------------------------------------------

/**
 * openDecideSheet opens a kit sheet with:
 *  - disposition picker (buttons for each allowed disposition)
 *  - optional `until` field (shown when wait/watch is selected)
 *  - note field
 *  - live preview ("close 4 items")
 *  - a filled "Decide N" button that POSTs once to /api/decide
 *
 * On success, calls onDone(keys) with the keys that were decided.
 * Esc or a backdrop click closes the sheet without deciding.
 */
export function openDecideSheet(
  ctx: Ctx,
  keys: string[],
  allowed: string[],
  onDone: (decided: string[]) => void,
): void {
  const n = keys.length;
  let disposition = '';
  let until = '';
  let note = '';
  let submitting = false;
  let sh: SheetHandle | null = null;

  // ---- preview ---------------------------------------------------------------

  const previewEl = h('p', { class: 'cb-sheet-preview' });
  previewEl.textContent = `${n} item${n === 1 ? '' : 's'}`;

  function updatePreview(): void {
    previewEl.textContent = `${disposition || '…'} ${n} item${n === 1 ? '' : 's'}`;
  }

  // ---- until field -----------------------------------------------------------

  const untilInputEl = h('input', {
    type: 'text',
    class: 'cb-sheet-input',
    placeholder: 'date(YYYY-MM-DD), merged(<pr>), inactive(90d) …',
  }) as HTMLInputElement;
  untilInputEl.addEventListener('input', () => {
    until = untilInputEl.value;
  });

  const untilRow = h(
    'div',
    { class: 'cb-sheet-row' },
    h('label', { class: 'cb-sheet-label' }, 'until'),
    untilInputEl,
  );
  untilRow.hidden = true;

  // ---- note field ------------------------------------------------------------

  const noteInputEl = noteField({
    placeholder: 'optional note',
    onCommit(v: string) {
      note = v;
    },
  });
  const noteRow = h(
    'div',
    { class: 'cb-sheet-row' },
    h('label', { class: 'cb-sheet-label' }, 'note'),
    noteInputEl,
  );

  // ---- error display ---------------------------------------------------------

  const errEl = h('p', { class: 'cb-sheet-err' });
  errEl.hidden = true;

  // ---- disposition buttons ---------------------------------------------------

  const dispContainer = h('div', { class: 'cb-sheet-disps' });

  for (const d of allowed) {
    const btn = h(
      'button',
      {
        type: 'button',
        class:
          'cb-sheet-disp' +
          (DANGER_DISPS.has(d) ? ' cb-sheet-disp--danger' : ''),
        onclick() {
          disposition = d;
          updatePreview();
          untilRow.hidden = !NEEDS_UNTIL.has(d);
          errEl.hidden = true;
          for (const el of dispContainer.querySelectorAll('.cb-sheet-disp')) {
            el.classList.toggle('on', el === btn);
          }
        },
      },
      d,
    );
    dispContainer.append(btn);
  }

  // ---- body ------------------------------------------------------------------

  const body = h(
    'div',
    { class: 'cb-sheet-body' },
    dispContainer,
    untilRow,
    noteRow,
    previewEl,
    errEl,
  );

  // ---- decide action ---------------------------------------------------------

  async function doDecide(): Promise<void> {
    if (submitting) return;
    if (!disposition) {
      errEl.textContent = 'select a disposition';
      errEl.hidden = false;
      return;
    }
    if (NEEDS_UNTIL.has(disposition)) {
      const currentUntil = untilInputEl.value.trim();
      const err = validateUntil(currentUntil);
      if (err) {
        errEl.textContent = err;
        errEl.hidden = false;
        return;
      }
      until = currentUntil;
    } else {
      until = '';
    }
    submitting = true;
    errEl.hidden = true;
    try {
      const payload: Record<string, unknown> = { keys, disposition };
      if (until) payload['until'] = until;
      if (note) payload['note'] = note;
      const result = await ctx.api.post<DecideResult>('/decide', payload);
      // decided_keys lists the keys that were actually committed.  Deselect
      // those whether or not some keys also failed, so partial success is
      // reflected immediately.
      const decidedKeys = result.decided_keys ?? [];
      if (decidedKeys.length > 0) {
        onDone(decidedKeys);
      }
      if (result.errors && result.errors.length > 0) {
        // Show per-key errors; keep the sheet open so the user can retry or
        // dismiss.  The succeeded keys have already been deselected above.
        errEl.textContent = result.errors.join('; ');
        errEl.hidden = false;
        submitting = false;
      } else {
        sh?.close();
      }
    } catch (err) {
      errEl.textContent =
        err instanceof Error ? err.message : 'decide failed — try again';
      errEl.hidden = false;
      submitting = false;
    }
  }

  // ---- open the sheet --------------------------------------------------------

  sh = sheet({
    title: `decide ${n} item${n === 1 ? '' : 's'}`,
    body,
    actions: [
      {
        label: `Decide ${n}`,
        fill: true,
        run() {
          void doDecide();
        },
      },
    ],
    onClose() {
      sh = null;
    },
  });
}

// ---- wireSelection ----------------------------------------------------------

/**
 * wireSelection subscribes to the list's shared selection store and:
 *  1. Sets the bar primary button to "Decide N" (or hides it when empty).
 *  2. Updates the .cb-sel-count element inside the list (in the foot).
 *  3. Registers the "d" key to open the decide sheet for the selection.
 *
 * Call after the list is created; pass the same ctx the section uses.
 * The "d" key is registered only once (a clash is silently ignored since
 * another view may also want it, per Task 10 scope rules).
 *
 * After a decide, calls list.deselect(decidedIds) so the kit drops those ids
 * from the selection store.
 */
export function wireSelection(
  ctx: Ctx,
  listHandle: ListHandle<ItemView>,
): void {
  function openSheetForSelection(): void {
    const keys = listHandle.selectedIds();
    if (keys.length === 0) return;
    // Derive allowed dispositions from the key prefixes so that items on
    // unrendered pages (selected via select-all across pages) are counted too.
    const allowed = allowedForKeys(keys);
    openDecideSheet(ctx, keys, allowed, (decided) => {
      listHandle.deselect(decided);
      // A "decided" live event will trigger the list reload; no manual
      // reload needed here.
    });
  }

  listHandle.selection.onChange((ids) => {
    // Update the foot count element (placed inside listHandle.el by buildFoot).
    const countEl = listHandle.el.querySelector('.cb-sel-count');
    if (countEl instanceof HTMLElement) {
      if (ids.length > 0) {
        countEl.textContent = `${ids.length} selected`;
        countEl.hidden = false;
      } else {
        countEl.hidden = true;
      }
    }

    // Update the primary button.
    if (ids.length === 0) {
      ctx.setPrimary(null);
    } else {
      ctx.setPrimary({
        label: `Decide ${ids.length}`,
        run: openSheetForSelection,
      });
    }
  });

  // Register "d" to decide the selection.
  // The kit throws with a message starting "key clash:" when the key is
  // already registered (e.g. another section registered "d").  We swallow
  // only that error and rethrow anything else.
  try {
    ctx.keys.register({
      keys: 'd',
      label: 'decide selection',
      group: 'page',
      run() {
        openSheetForSelection();
      },
    });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!msg.startsWith('key clash:')) {
      throw err;
    }
    // Clash with an existing binding: this section's "d" key is taken.
  }
}
