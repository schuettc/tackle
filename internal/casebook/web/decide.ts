// decide.ts — the decide sheet (single and bulk) and selection wiring.
//
// The server is the single source of truth for the decision vocabulary
// (which dispositions are valid per kind, which need an `until` condition,
// and the supported `until` forms).  GET /api/decisions/vocabulary is fetched
// once and cached; the sheet and the selection wiring read from it.
//
// Exported pure functions (no server calls):
//   kindFromKey(key)               — "pr:owner/repo#1" → "pr"
//   allowedForKind(vocab, kind)    — allowed dispositions for one kind
//   allowedForKeys(vocab, keys)    — intersection for a mixed-kind selection
//
// openDecideSheet(ctx, keys, onDone) opens a kit sheet() with:
//   - disposition picker (buttons for each intersected disposition)
//   - optional `until` field (shown/hidden by vocab's needs_until per kind)
//   - note field
//   - live preview ("close 4 items")
//   - debounced dry-run validation of `until` (server's error message shown)
//   - a filled "Decide N" button that POSTs to /api/decide
//
// wireSelection(ctx, listHandle) subscribes to the shared selection store and
// updates the bar primary button and the .cb-sel-count element.

import {
  sheet,
  h,
  noteField,
  type SheetHandle,
  type ListHandle,
  type KeyBinding,
} from '/_kit/kit.js';
import type { DecideResult, DecisionVocabView, ItemView } from './wire.d.ts';
import type { Ctx } from './app.ts';
import {
  kindFromKey,
  allowedForKind,
  allowedForKeys,
  pluralize,
} from './decide-math.ts';

// Re-export the pure functions so callers only need one import.
export { kindFromKey, allowedForKind, allowedForKeys };

// ---- module-level vocab cache -----------------------------------------------

let _vocab: DecisionVocabView | null = null;

/** getVocab fetches and caches the decision vocabulary from the server.
 *  Call it from any module that needs the vocab (cached after first call). */
export async function getVocab(ctx: Ctx): Promise<DecisionVocabView> {
  if (_vocab !== null) return _vocab;
  _vocab = await ctx.api.get<DecisionVocabView>('/decisions/vocabulary');
  return _vocab;
}

// ---- helpers ----------------------------------------------------------------

/** Returns true when a given disposition requires an `until` condition for
 *  any of the selected keys' kinds (after intersection they'll all agree). */
function dispositionNeedsUntil(
  vocab: DecisionVocabView,
  keys: string[],
  disp: string,
): boolean {
  const selectedKinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  return (vocab.kinds ?? [])
    .filter((k) => selectedKinds.includes(k.kind))
    .some((k) => (k.needs_until ?? []).includes(disp));
}

// Dispositions that are destructive (rendered with danger styling).
// Exported so item.ts can import it instead of duplicating.
export const DANGER_DISPS = new Set(['close', 'delete', 'archive']);

// ---- openDecideSheet --------------------------------------------------------

/**
 * openDecideSheet fetches the decision vocabulary (cached after the first
 * call) and opens a kit sheet with:
 *  - disposition picker (buttons for the intersection of allowed dispositions)
 *  - optional `until` field (shown when the disposition needs it, per vocab)
 *  - note field
 *  - live preview ("close 4 items")
 *  - debounced dry-run until validation (server's error message shown while typing)
 *  - a filled "Decide N" button that POSTs once to /api/decide
 *
 * On success, calls onDone(keys) with the keys that were decided.
 * Esc or a backdrop click closes the sheet without deciding.
 *
 * Optional `seed` pre-populates disposition/until/note (used by change…).
 * Optional `customPost` replaces the default /api/decide POST (also used by
 * change… to post to /api/proposals/change instead).
 */
export function openDecideSheet(
  ctx: Ctx,
  keys: string[],
  onDone: (decided: string[]) => void,
  seed?: { disposition?: string; until?: string; note?: string },
  customPost?: (disp: string, until: string, note: string) => Promise<string[]>,
): void {
  void getVocab(ctx).then((vocab) => {
    openDecideSheetWithVocab(ctx, keys, vocab, onDone, seed, customPost);
  });
}

function openDecideSheetWithVocab(
  ctx: Ctx,
  keys: string[],
  vocab: DecisionVocabView,
  onDone: (decided: string[]) => void,
  seed?: { disposition?: string; until?: string; note?: string },
  customPost?: (disp: string, until: string, note: string) => Promise<string[]>,
): void {
  const n = keys.length;
  const allowed = allowedForKeys(vocab, keys);

  let disposition = seed?.disposition ?? '';
  let until = seed?.until ?? '';
  let note = seed?.note ?? '';
  let submitting = false;
  let sh: SheetHandle | null = null;
  let dryRunTimer: ReturnType<typeof setTimeout> | null = null;
  let lastDryRunError: string | null = null;

  // ---- preview ---------------------------------------------------------------

  const previewEl = h('p', { class: 'cb-sheet-preview' });
  // Seed the preview text with the seeded disposition if provided.
  previewEl.textContent = disposition
    ? `${disposition} ${pluralize(n, 'item')}`
    : pluralize(n, 'item');

  function updatePreview(): void {
    previewEl.textContent = `${disposition || '\u2026'} ${pluralize(n, 'item')}`;
  }

  // ---- until field -----------------------------------------------------------

  const untilInputEl = h('input', {
    type: 'text',
    class: 'cb-sheet-input',
    placeholder:
      (vocab.until_forms ?? []).map((f) => f.syntax).join(', ') ||
      'date(YYYY-MM-DD), inactive(90d) \u2026',
  }) as HTMLInputElement;
  // Seed the until field if a seed value was provided.
  if (seed?.until) {
    untilInputEl.value = seed.until;
  }

  const untilRow = h(
    'div',
    { class: 'cb-sheet-row' },
    h('label', { class: 'cb-sheet-label' }, 'until'),
    untilInputEl,
  );
  // Show the until row if the seed has a value or if the seeded disposition needs it.
  untilRow.hidden =
    !seed?.until && !dispositionNeedsUntil(vocab, keys, disposition);

  // ---- error display ---------------------------------------------------------

  const errEl = h('p', { class: 'cb-sheet-err' });
  errEl.hidden = true;

  // ---- dry-run validator ------------------------------------------------------

  async function runDryRun(): Promise<void> {
    if (!disposition) return;
    if (!dispositionNeedsUntil(vocab, keys, disposition)) return;
    const currentUntil = untilInputEl.value.trim();
    if (!currentUntil) {
      lastDryRunError = `until is required for ${disposition}`;
      errEl.textContent = lastDryRunError;
      errEl.hidden = false;
      return;
    }
    try {
      const result = await ctx.api.post<DecideResult>('/decide', {
        keys,
        disposition,
        until: currentUntil,
        dry_run: true,
      });
      if (result.errors && result.errors.length > 0) {
        lastDryRunError = result.errors.join('; ');
        errEl.textContent = lastDryRunError;
        errEl.hidden = false;
      } else {
        lastDryRunError = null;
        errEl.hidden = true;
      }
    } catch {
      // Ignore network errors during debounced typing; the submit will catch them.
    }
  }

  untilInputEl.addEventListener('input', () => {
    until = untilInputEl.value;
    lastDryRunError = null;
    errEl.hidden = true;
    if (dryRunTimer !== null) clearTimeout(dryRunTimer);
    dryRunTimer = setTimeout(() => void runDryRun(), 400);
  });

  // ---- note field ------------------------------------------------------------

  const noteInputEl = noteField({
    placeholder: 'optional note',
    value: seed?.note ?? '',
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

  // ---- disposition buttons ---------------------------------------------------

  const dispContainer = h('div', { class: 'cb-sheet-disps' });

  for (const d of allowed) {
    const isSeeded = d === disposition;
    const btn = h(
      'button',
      {
        type: 'button',
        class:
          'cb-sheet-disp' +
          (DANGER_DISPS.has(d) ? ' cb-sheet-disp--danger' : '') +
          (isSeeded ? ' on' : ''),
        onclick() {
          disposition = d;
          updatePreview();
          const needsUntil = dispositionNeedsUntil(vocab, keys, d);
          untilRow.hidden = !needsUntil;
          errEl.hidden = true;
          lastDryRunError = null;
          if (dryRunTimer !== null) clearTimeout(dryRunTimer);
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
    if (dispositionNeedsUntil(vocab, keys, disposition)) {
      const currentUntil = untilInputEl.value.trim();
      if (!currentUntil) {
        errEl.textContent = `until is required for ${disposition}`;
        errEl.hidden = false;
        return;
      }
      // Cancel any pending debounce and run a blocking dry run before submit.
      if (dryRunTimer !== null) clearTimeout(dryRunTimer);
      await runDryRun();
      if (lastDryRunError) {
        // runDryRun already updated errEl.
        return;
      }
      until = currentUntil;
    } else {
      until = '';
    }
    submitting = true;
    errEl.hidden = true;
    try {
      let decidedKeys: string[];
      let errors: string[] = [];
      if (customPost) {
        // Custom post (e.g. /proposals/change): caller owns the endpoint.
        decidedKeys = await customPost(disposition, until, note);
      } else {
        const payload: Record<string, unknown> = { keys, disposition };
        if (until) payload['until'] = until;
        if (note) payload['note'] = note;
        const result = await ctx.api.post<DecideResult>('/decide', payload);
        decidedKeys = result.decided_keys ?? [];
        errors = result.errors ?? [];
      }
      if (decidedKeys.length > 0) {
        onDone(decidedKeys);
      }
      if (errors.length > 0) {
        errEl.textContent = errors.join('; ');
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
    title: `decide ${pluralize(n, 'item')}`,
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
      if (dryRunTimer !== null) clearTimeout(dryRunTimer);
    },
  });
}

// ---- wireSelection ----------------------------------------------------------

/**
 * wireSelection subscribes to the list's shared selection store and:
 *  1. Sets the bar primary button to "Decide N" (or hides it when empty).
 *  2. Updates the .cb-sel-count element inside the list (in the foot).
 *  3. Returns the "d" binding (open the decide sheet for the selection) for
 *     the section to list in its Section.keys, so "d" acts only while that
 *     section is shown (app.ts registers section keys on show).
 *
 * Call after the list is created; pass the same ctx the section uses.
 *
 * After a decide, calls list.deselect(decidedIds) so the kit drops those ids
 * from the selection store.
 */
export function wireSelection(
  ctx: Ctx,
  listHandle: ListHandle<ItemView>,
): KeyBinding {
  function openSheetForSelection(): void {
    const keys = listHandle.selectedIds();
    if (keys.length === 0) return;
    openDecideSheet(ctx, keys, (decided) => {
      listHandle.deselect(decided);
    });
  }

  listHandle.selection.onChange((ids) => {
    const countEl = listHandle.el.querySelector('.cb-sel-count');
    if (countEl instanceof HTMLElement) {
      if (ids.length > 0) {
        countEl.textContent = `${ids.length} selected`;
        countEl.hidden = false;
      } else {
        countEl.hidden = true;
      }
    }

    if (ids.length === 0) {
      ctx.setPrimary(null);
    } else {
      ctx.setPrimary({
        label: `Decide ${ids.length}`,
        run: openSheetForSelection,
      });
    }
  });

  return {
    keys: 'd',
    label: 'decide selection',
    group: 'page',
    run() {
      openSheetForSelection();
    },
  };
}
