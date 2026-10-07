// choices.ts — the decide step's answer cards and the Not now picker.
//
// renderChoices(ctx, kind, opts) returns an element holding:
//   - the choice cards, two to a row: a number, serve's label and serve's
//     sentence for what happens; the recommended one says "recommended" (in
//     the agent's colour), the chosen one has the signal border;
//   - Not now's conditions (serve's not_now forms) as chips under the cards,
//     shown when Not now is picked, with one field when a condition asks for a
//     value (a date, a PR key, a repo key);
//   - an error line (.cb-choice-err) for serve's answer when it refuses.
//
// Picking a card calls opts.onPick(disposition); Not now calls
// opts.onPick('wait', until) once its condition is complete. Every word the
// cards and chips show comes from GET /api/decisions/vocabulary. The element
// is the only state: a number key clicks its nth .cb-choice, and the caller
// shows an error with showChoiceError(root, message).

import { h } from '/_kit/kit.js';
import type { ChoiceVocab, NotNowForm } from './wire.d.ts';
import type { Ctx } from './app.ts';
import { getVocab } from './decide.ts';
import { choicesForKeys, notNowUntil } from './decide-math.ts';

export interface ChoicesOpts {
  /** The disposition a pending proposal recommends. */
  recommended?: string;
  /** The disposition decided (or picked in a sheet) now. */
  chosen?: string;
  /** A selection's keys: its shared cards rather than kind's. */
  keys?: string[];
  onPick(d: string, until?: string): void;
}

/** asNotNow maps watch, which the page no longer offers, onto Not now. */
export function asNotNow(d: string | undefined): string | undefined {
  return d === 'watch' ? 'wait' : d;
}

// What each value a Not now form asks for is written as: key syntax, not
// decision wording.
const ASKS_PLACEHOLDER: Record<string, string> = {
  pr: 'pr:owner/repo#1',
  'pr-or-issue': 'pr:owner/repo#1 or issue:owner/repo#1',
  repo: 'repo:owner/name',
};

/** showChoiceError shows message on root's error line (hides it when ''). */
export function showChoiceError(root: ParentNode, message: string): void {
  const err = root.querySelector<HTMLElement>('.cb-choice-err');
  if (!err) return;
  err.textContent = message;
  err.hidden = !message;
}

export function renderChoices(
  ctx: Ctx,
  kind: string,
  opts: ChoicesOpts,
): HTMLElement {
  const grid = h('div', { class: 'cb-choices' });
  const notNow = h('div', { class: 'cb-notnow', hidden: true });
  const err = h('p', { class: 'cb-choice-err', hidden: true });
  const root = h('div', { class: 'cb-choices-wrap' }, grid, notNow, err);

  void getVocab(ctx).then((vocab) => {
    const choices: ChoiceVocab[] = opts.keys
      ? choicesForKeys(vocab, opts.keys)
      : ((vocab.kinds ?? []).find((k) => k.kind === kind)?.choices ?? []);
    const rec = asNotNow(opts.recommended);
    const cards = choices.map((c, i) =>
      choiceCard(c, i + 1, c.disposition === rec),
    );
    grid.append(...cards);
    if (opts.chosen) mark(asNotNow(opts.chosen));
    notNow.append(...notNowPicker(vocab.not_now ?? []));
  });

  function mark(d: string | undefined): void {
    for (const card of grid.querySelectorAll<HTMLElement>('.cb-choice')) {
      const on = card.dataset.d === d;
      card.classList.toggle('on', on);
      const k = card.querySelector<HTMLElement>('.cb-choice-k');
      if (k) k.textContent = kickText(card);
    }
  }

  function kickText(card: HTMLElement): string {
    const parts = [card.dataset.n ?? ''];
    if (card.classList.contains('rec')) parts.push('recommended');
    if (card.classList.contains('on')) parts.push('chosen');
    return parts.join(' \u00b7 ');
  }

  function choiceCard(c: ChoiceVocab, n: number, rec: boolean): HTMLElement {
    const card = h(
      'button',
      {
        type: 'button',
        class:
          'kit-card cb-choice' +
          (rec ? ' rec' : '') +
          (c.disposition === 'delete' ? ' danger' : ''),
        dataset: { d: c.disposition, n: String(n) },
        onclick() {
          showChoiceError(root, '');
          mark(c.disposition);
          if (c.needs_until) {
            notNow.hidden = false;
            return;
          }
          notNow.hidden = true;
          opts.onPick(c.disposition);
        },
      },
      h('span', { class: 'cb-choice-k' }),
      h('span', { class: 'cb-choice-label' }, c.label),
      h('span', { class: 'cb-choice-says' }, c.says),
    );
    const k = card.querySelector<HTMLElement>('.cb-choice-k');
    if (k) k.textContent = kickText(card);
    return card;
  }

  // notNowPicker is the chips (one per form) and the one field a form that
  // asks for a value shows.
  function notNowPicker(forms: NotNowForm[]): HTMLElement[] {
    const input = h('input', { class: 'cb-sheet-input' }) as HTMLInputElement;
    let form: NotNowForm | null = null;
    const commit = (): void => {
      if (!form) return;
      const until = notNowUntil(form, input.value, new Date());
      if (until) opts.onPick('wait', until);
    };
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        commit();
      }
    });
    // A date input is complete when a date is picked.
    input.addEventListener('change', () => {
      if (input.type === 'date') commit();
    });
    const set = h(
      'button',
      { type: 'button', class: 'kit-btn', onclick: commit },
      'set',
    );
    const field = h(
      'div',
      { class: 'cb-notnow-field', hidden: true },
      input,
      set,
    );
    const chips = h(
      'div',
      { class: 'kit-chips cb-notnow-chips' },
      forms.map((f) =>
        h(
          'button',
          {
            type: 'button',
            class: 'kit-chip',
            dataset: { id: f.id },
            onclick(e: MouseEvent) {
              showChoiceError(root, '');
              for (const c of chips.querySelectorAll('.kit-chip'))
                c.classList.toggle('on', c === e.currentTarget);
              form = f;
              if (!f.asks || f.asks === 'days') {
                field.hidden = true;
                commit();
                return;
              }
              input.type = f.asks === 'date' ? 'date' : 'text';
              input.value = '';
              input.placeholder = ASKS_PLACEHOLDER[f.asks] ?? '';
              field.hidden = false;
              input.focus();
            },
          },
          f.label,
        ),
      ),
    );
    return [chips, field];
  }

  return root;
}
