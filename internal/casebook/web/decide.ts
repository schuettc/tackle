// decide.ts — the decide sheet (change… on a recommendation).
//
// The server is the single source of truth for the decision vocabulary
// (which dispositions are valid per kind, which need an `until` condition,
// and the supported `until` forms).  GET /api/decisions/vocabulary is fetched
// once and cached; the sheet reads from it.
//
// Exported pure functions (no server calls):
//   kindFromKey(key)               — "pr:owner/repo#1" → "pr"
//   allowedForKind(vocab, kind)    — allowed dispositions for one kind
//   allowedForKeys(vocab, keys)    — intersection for a mixed-kind selection
//
// openDecideSheet(ctx, keys, onDone) opens a kit sheet for keys: the
// item's question, the same cards the open item shows (choices.ts), a note
// field, and a filled "<choice> · <n> <kind>s" button that POSTs to
// /api/decide. Ticked items act through their group's buttons
// (decide-list.ts), not through a sheet.

import { sheet, h, noteField, type SheetHandle } from '/_kit/kit.js';
import type { DecideResult, DecisionVocabView } from './wire.d.ts';
import type { Ctx } from './app.ts';
import {
  kindFromKey,
  allowedForKind,
  allowedForKeys,
  choicesForKeys,
  fillLabel,
  pluralize,
} from './decide-math.ts';
import { renderChoices, showChoiceError, asNotNow } from './choices.ts';

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

// Dispositions that are destructive (rendered with danger styling by Rules
// and To apply). The decide step's cards draw only delete in danger.
export const DANGER_DISPS = new Set(['close', 'delete', 'archive']);

// ---- openDecideSheet --------------------------------------------------------

/**
 * openDecideSheet fetches the decision vocabulary (cached after the first
 * call) and opens a kit sheet for several items that asks the item's
 * question with the same cards the open item shows (choices.ts), then:
 *  - Not now's conditions under the cards when Not now is picked
 *  - a note field
 *  - a filled button that names the choice and the count
 *    ("Close it · 4 issues") and POSTs once to /api/decide
 *
 * Picking a card decides nothing: the fill does. Serve's errors show in the
 * sheet. On success, calls onDone(keys) with the keys that were decided.
 * Esc or a backdrop click closes the sheet without deciding.
 *
 * Optional `seed` pre-picks disposition/until/note (used by change…).
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
  const choices = choicesForKeys(vocab, keys);
  const kinds = [...new Set(keys.map(kindFromKey))];
  const question =
    kinds.length === 1
      ? ((vocab.kinds ?? []).find((k) => k.kind === kinds[0])?.question ?? '')
      : '';

  let disposition = asNotNow(seed?.disposition) ?? '';
  let until = seed?.until ?? '';
  let note = seed?.note ?? '';
  let submitting = false;
  let sh: SheetHandle | null = null;

  const labelOf = (d: string) =>
    choices.find((c) => c.disposition === d)?.label ?? d;
  const needsUntil = (d: string) =>
    choices.find((c) => c.disposition === d)?.needs_until ?? false;

  // The until a Not now condition made (or the seed's), shown under the cards.
  const untilEl = h('p', { class: 'cb-sheet-preview' });
  function updateFill(): void {
    const fill = sh?.el.querySelector<HTMLElement>('.kit-btn.fill');
    if (fill)
      fill.textContent = fillLabel(labelOf(disposition) || 'Decide', keys);
    untilEl.textContent = until ? `until ${until}` : '';
    untilEl.hidden = !until || !needsUntil(disposition);
  }

  const cards = renderChoices(ctx, kinds[0] ?? '', {
    keys,
    chosen: disposition || undefined,
    onPick(d, u) {
      disposition = d;
      until = u ?? '';
      updateFill();
    },
  });

  const noteInputEl = noteField({
    placeholder: 'optional note',
    value: note,
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

  const body = h(
    'div',
    { class: 'cb-sheet-body cb-decide-sheet' },
    question ? h('p', { class: 'cb-question' }, question) : null,
    cards,
    untilEl,
    noteRow,
  );

  async function doDecide(): Promise<void> {
    if (submitting) return;
    // The note field commits on blur or Enter; the fill reads it as typed.
    note = noteInputEl.value;
    if (!disposition) {
      showChoiceError(cards, 'Pick a choice first.');
      return;
    }
    if (needsUntil(disposition) && !until) {
      showChoiceError(cards, 'Pick when to bring it back.');
      return;
    }
    const u = needsUntil(disposition) ? until : '';
    submitting = true;
    showChoiceError(cards, '');
    try {
      let decidedKeys: string[];
      let errors: string[] = [];
      if (customPost) {
        // Custom post (e.g. /proposals/change): caller owns the endpoint.
        decidedKeys = await customPost(disposition, u, note);
      } else {
        const payload: Record<string, unknown> = { keys, disposition };
        if (u) payload['until'] = u;
        if (note) payload['note'] = note;
        const result = await ctx.api.post<DecideResult>('/decide', payload);
        decidedKeys = result.decided_keys ?? [];
        errors = result.errors ?? [];
      }
      if (decidedKeys.length > 0) {
        onDone(decidedKeys);
      }
      if (errors.length > 0) {
        showChoiceError(cards, errors.join('; '));
        submitting = false;
      } else {
        sh?.close();
      }
    } catch (err) {
      showChoiceError(
        cards,
        err instanceof Error ? err.message : 'decide failed, try again',
      );
      submitting = false;
    }
  }

  sh = sheet({
    title: `decide ${pluralize(n, 'item')}`,
    body,
    actions: [
      {
        label: fillLabel(labelOf(disposition) || 'Decide', keys),
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
  updateFill();
}
