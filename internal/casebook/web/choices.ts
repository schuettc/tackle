// choices.ts — the decide step's answer cards and the Not now picker.
//
// renderChoices(ctx, kind, opts) returns an element holding:
//   - the choice cards, two to a row: a number, serve's label and serve's
//     sentence for what happens; the recommended one says "recommended" (in
//     the agent's colour), the chosen one has the signal border;
//   - Not now's conditions (serve's not_now forms) as chips under the cards,
//     shown when Not now is picked, with one field when a condition asks for a
//     value: a date, or (a PR, a PR or issue, a repo) a search of the items
//     casebook tracks that also takes a pasted GitHub URL or key, offering
//     an untracked one as "‹key› · not tracked; checked on the next sync";
//   - for one item, the closing comment a close asks for (.cb-close-comment)
//     under the cards, shown when a close card is picked: the kit's note
//     field and two buttons, "close with this comment" (↵ in the field too)
//     and "close without comment" (no note); Esc cancels it;
//   - an error line (.cb-choice-err) for serve's answer when it refuses.
//
// Picking a card calls opts.onPick(disposition); Not now calls
// opts.onPick('wait', until) once its condition is complete; a close (one
// item) calls opts.onPick('close', undefined, note) from its buttons, note
// '' for none. Every word the cards and chips show comes from GET
// /api/decisions/vocabulary. The element is the only state: a number key
// clicks its nth .cb-choice, and the caller shows an error with
// showChoiceError(root, message).

import { h, noteField, buttons } from '/_kit/kit.js';
import type { ChoiceVocab, ItemsView, NotNowForm } from './wire.d.ts';
import type { Ctx } from './app.ts';
import { getVocab } from './decide.ts';
import {
  PICK_KINDS,
  kindFromKey as kindOf,
  choicesForKeys,
  notNowUntil,
  pastedKeys,
  latestSearch,
} from './decide-math.ts';

export interface ChoicesOpts {
  /** The disposition a pending proposal recommends. */
  recommended?: string;
  /** The disposition decided (or picked in a sheet) now. */
  chosen?: string;
  /** A selection's keys: its shared cards rather than kind's. */
  keys?: string[];
  /** The decided close's note: what the closing-comment field starts with. */
  note?: string;
  onPick(d: string, until?: string, note?: string): void;
}

// The disposition that closes a PR or an issue: it asks for a comment.
const CLOSE = 'close';

// The closing comment's words (the page's, like Not now's "set").
const COMMENT_PLACEHOLDER = 'closing comment, posted when you approve the plan';
const CLOSE_WITH = 'close with this comment';
const CLOSE_WITHOUT = 'close without comment';

/** asNotNow maps watch, which the page no longer offers, onto Not now. */
export function asNotNow(d: string | undefined): string | undefined {
  return d === 'watch' ? 'wait' : d;
}

// Under a search field: what it takes.
const PICK_EXAMPLE: Record<string, string> = {
  pr: 'tackle#58, a title, or https://github.com/owner/repo/pull/58',
  'pr-or-issue': 'tackle#58, a title, or https://github.com/owner/repo/pull/58',
  repo: 'tackle, a name, or https://github.com/owner/repo',
};

/** One thing the search offers: a key, and what the option says. */
interface PickOption {
  key: string;
  says: string;
}

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
  // A close asks for its comment only for one item: a selection's sheet (and
  // agree with all) closes as before, with the sheet's own note.
  const comment = opts.keys ? null : closeComment();
  const err = h('p', { class: 'cb-choice-err', hidden: true });
  const root = h(
    'div',
    { class: 'cb-choices-wrap' },
    grid,
    notNow,
    comment?.el ?? null,
    err,
  );

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
            comment?.close();
            notNow.hidden = false;
            return;
          }
          notNow.hidden = true;
          if (comment && c.disposition === CLOSE) {
            comment.open();
            return;
          }
          comment?.close();
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

  // closeComment is the closing-comment field a close card opens: the kit's
  // note field and its two buttons. Nothing is decided until a button (or ↵
  // in the field); Esc, wherever focus is, closes it and unpicks the card.
  function closeComment(): {
    el: HTMLElement;
    open(): void;
    close(): void;
  } {
    const field = noteField({
      value: '',
      placeholder: COMMENT_PLACEHOLDER,
      // The buttons read the field as typed; a blur's commit decides nothing.
      onCommit() {},
    });
    field.setAttribute('aria-label', 'closing comment');
    const decide = (note: string): void => {
      opts.onPick(CLOSE, undefined, note.trim());
    };
    field.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.isComposing) decide(field.value);
    });
    const el = h(
      'div',
      { class: 'cb-close-comment', hidden: true },
      field,
      buttons([
        { label: CLOSE_WITH, fill: true, run: () => decide(field.value) },
        { label: CLOSE_WITHOUT, run: () => decide('') },
      ]),
    );
    const onEsc = (e: KeyboardEvent): void => {
      if (e.key !== 'Escape' || e.isComposing) return;
      if (!el.isConnected || el.hidden) {
        document.removeEventListener('keydown', onEsc, true);
        return;
      }
      // Another field's Esc (the dock's composer, a sheet's) is its own.
      const t = e.target;
      if (
        t !== field &&
        t instanceof HTMLElement &&
        (t.isContentEditable || t.matches('input, textarea, select'))
      )
        return;
      e.preventDefault();
      e.stopPropagation();
      close();
      mark(asNotNow(opts.chosen));
    };
    function close(): void {
      if (document.activeElement === field) field.blur();
      el.hidden = true;
      document.removeEventListener('keydown', onEsc, true);
    }
    function open(): void {
      // A decided close starts from its comment; anything else, empty.
      field.value = opts.chosen === CLOSE ? (opts.note ?? '') : '';
      el.hidden = false;
      document.addEventListener('keydown', onEsc, true);
      field.focus();
    }
    return { el, open, close };
  }

  // notNowPicker is the chips (one per form) and the one field a form that
  // asks for a value shows: a date, or a search (PICK_KINDS) whose options
  // decide.
  function notNowPicker(forms: NotNowForm[]): HTMLElement[] {
    const input = h('input', { class: 'cb-sheet-input' }) as HTMLInputElement;
    let form: NotNowForm | null = null;
    const searching = (): boolean => !!form && form.asks in PICK_KINDS;
    const commit = (): void => {
      if (!form) return;
      const until = notNowUntil(form, input.value, new Date());
      if (until) opts.onPick('wait', until);
    };
    const choose = (key: string): void => {
      if (form) opts.onPick('wait', form.template.replace('%s', key));
    };
    const options = h('div', {
      class: 'cb-pick-opts',
      role: 'listbox',
      hidden: true,
    });
    const example = h('p', { class: 'cb-pick-eg', hidden: true });
    const show = (found: PickOption[]): void => {
      options.replaceChildren(
        ...found.map((o) =>
          h(
            'button',
            {
              type: 'button',
              class: 'cb-pick-opt',
              role: 'option',
              dataset: { key: o.key },
              onclick: () => choose(o.key),
            },
            o.says,
          ),
        ),
      );
      options.hidden = found.length === 0;
    };
    // The newest search wins: a slower answer to an older one is dropped.
    const search = latestSearch(
      async (text: string) => (text ? pick(form?.asks ?? '', text) : []),
      (text, found) => {
        show(found);
        input.dataset.searched = text;
      },
      150,
      // What is shown is for older text: it can't be chosen until the
      // answer for this text is in.
      () => {
        for (const b of options.querySelectorAll<HTMLButtonElement>(
          '.cb-pick-opt',
        ))
          b.disabled = true;
      },
    );
    input.addEventListener('input', () => {
      if (!searching()) return;
      delete input.dataset.searched;
      search.input(input.value.trim());
    });
    input.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      if (!searching()) {
        commit();
        return;
      }
      // Enter takes the first option, once the search for this text is in.
      const first = options.querySelector<HTMLElement>(
        '.cb-pick-opt:not(:disabled)',
      );
      if (input.dataset.searched === input.value.trim() && first) first.click();
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
      h('div', { class: 'cb-pick' }, input, example, options),
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
              search.cancel();
              show([]);
              delete input.dataset.searched;
              input.type = f.asks === 'date' ? 'date' : 'text';
              input.value = '';
              example.textContent = PICK_EXAMPLE[f.asks] ?? '';
              example.hidden = !searching();
              set.hidden = searching();
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

  // pick is what the search offers for text: the keys a pasted URL or key
  // names (each the tracked item's, or "not tracked"), or the tracked items
  // of the form's kinds whose key or title holds text.
  async function pick(asks: string, text: string): Promise<PickOption[]> {
    const kinds = PICK_KINDS[asks] ?? [];
    const tracked = async (kind: string, q: string, limit: number) =>
      (
        await ctx.api
          .get<ItemsView>('/items', {
            view: 'tracked',
            kind,
            q,
            limit: String(limit),
          })
          .catch(() => ({ items: [] }) as unknown as ItemsView)
      ).items ?? [];
    const says = (key: string, title?: string): PickOption => ({
      key,
      says: title ? `${key} \u00b7 ${title}` : key,
    });
    const pasted = pastedKeys(text, asks);
    if (pasted) {
      const hits = (
        await Promise.all(
          pasted.map(async (key) =>
            (await tracked(kindOf(key), key, 5)).find((it) => it.key === key),
          ),
        )
      ).filter((it) => !!it);
      if (hits.length) return hits.map((it) => says(it.key, it.title));
      return pasted.map((key) => ({
        key,
        says: `${key} \u00b7 not tracked; checked on the next sync`,
      }));
    }
    const lists = await Promise.all(kinds.map((k) => tracked(k, text, 8)));
    return lists.flat().map((it) => says(it.key, it.title));
  }

  return root;
}
