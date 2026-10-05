// doc.ts — the reading column: one row (facts, the passage, the proposal,
// accept / edit / reject, the note, the whole file) or one group (its rows
// and the decisions that take them all). It only draws; app.ts holds state.

import {
  buttons,
  card,
  facts,
  fold,
  h,
  noteField,
  type Button,
} from '/_kit/kit.js';
import {
  PLAIN_VERDICTS,
  displayPath,
  fieldOf,
  fixOf,
  groupTargets,
  isFix,
  rowMeta,
  rowTitle,
  verdictOf,
  type EditForm,
  type Group,
  type View,
} from './model.ts';
import { prose } from './prose.ts';
import type { FileOut } from './api.ts';

export interface Ctx {
  home: string;
  view: View;
  /** The row being edited, if any. */
  editing: string | null;
  noteOf(r: Finding): string;
  setNote(r: Finding, v: string): void;
  accept(rows: Finding[]): void;
  reject(rows: Finding[]): void;
  startEdit(r: Finding): void;
  cancelEdit(): void;
  saveEdit(r: Finding, f: EditForm): string | null;
  clear(r: Finding): void;
  undo(rows: Finding[]): void;
  redo(rows: Finding[]): void;
  open(key: string): void;
  file(r: Finding): Promise<FileOut>;
  /** The apply that already wrote this row's repo, if any. */
  appliedIn(r: Finding): ApplyRecord | undefined;
}

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

const where = (r: Finding, home: string) => {
  const p = displayPath(r.source, home);
  return r.source.start ? `${p}:${r.source.start}` : p;
};

function noteBlock(ctx: Ctx, r: Finding): HTMLElement[] {
  const field = noteField({
    value: ctx.noteOf(r),
    placeholder: 'why (n)',
    onCommit: (v) => ctx.setNote(r, v),
  });
  // The kit's Enter handler (added first) commits; leave the field after it
  // so the next page key (1, 2, 3, j) is not typed into the note.
  field.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.isComposing) field.blur();
  });
  return [h('div', { class: 'kit-label' }, 'note'), field];
}

function wholeFile(ctx: Ctx, r: Finding): HTMLElement {
  const body = h('div', { class: 'kit-muted' }, 'loading…');
  const f = fold(
    r.source.ref ? `the whole file · at ${r.source.ref}` : 'the whole file',
    body,
  );
  let loaded = false;
  f.addEventListener('toggle', () => {
    if (!f.open || loaded) return;
    loaded = true;
    ctx.file(r).then(
      (out) => {
        const mark: [number, number] | undefined = r.source.start
          ? [r.source.start, r.source.end || r.source.start]
          : undefined;
        body.replaceChildren(
          prose(out.content, { mark }),
          out.truncated
            ? h('p', { class: 'sift-why' }, 'cut short: the file is over 2 MB')
            : '',
        );
        body.className = '';
        body.querySelector('.mark')?.scrollIntoView?.({ block: 'nearest' });
      },
      (err: Error) => {
        loaded = false;
        body.textContent = `could not load the file: ${err.message}`;
      },
    );
  });
  return f;
}

function editForm(ctx: Ctx, r: Finding): HTMLElement[] {
  const d = r.decision?.action === 'edit' ? r.decision : undefined;
  const verdict = h('input', {
    class: 'sift-field sift-verdict',
    type: 'text',
    value: d?.verdict || r.verdict || '',
    placeholder: 'verdict: delete, rewrite, merge:ID, close:done…',
    'aria-label': 'verdict',
    spellcheck: false,
  }) as HTMLInputElement;
  const title = h('input', {
    class: 'sift-field sift-title',
    type: 'text',
    value: fieldOf(r, 'title'),
    placeholder: 'title (issue rows)',
    'aria-label': 'title',
  }) as HTMLInputElement;
  const text = h('textarea', {
    class: 'sift-field sift-text',
    'aria-label': 'proposed text',
    rows: 6,
  }) as HTMLTextAreaElement;
  text.value = d?.cleared?.includes('text')
    ? ''
    : fieldOf(r, 'text') || r.passage || '';
  const err = h('p', { class: 'sift-err', role: 'alert' });
  const pick = buttons(
    PLAIN_VERDICTS.map((v) => ({
      label: v,
      run() {
        verdict.value = v;
        verdict.focus();
      },
    })),
  );
  pick.classList.add('sift-verdicts');
  const save = () => {
    const msg = ctx.saveEdit(r, {
      verdict: verdict.value,
      title: title.value,
      text: text.value,
    });
    err.textContent = msg ?? '';
  };
  const form = h(
    'form',
    {
      class: 'sift-edit',
      onsubmit: (e: Event) => {
        e.preventDefault();
        save();
      },
    },
    h('div', { class: 'kit-label' }, 'edit the proposal · verdict'),
    pick,
    verdict,
    h('div', { class: 'kit-label' }, 'title'),
    title,
    h('div', { class: 'kit-label' }, 'text'),
    text,
    err,
    buttons([
      { label: 'save edit (⌘↵)', fill: true, run: save },
      { label: 'cancel', run: () => ctx.cancelEdit() },
    ]),
  );
  queueMicrotask(() => text.focus());
  return [form];
}

/** The open row. */
export function rowDoc(ctx: Ctx, r: Finding): HTMLElement {
  const parts: (HTMLElement | string)[] = [
    h('div', { class: 'kit-kick' }, `${r.check} · ${where(r, ctx.home)}`),
    h('h1', { class: 'kit-h1' }, r.title || r.summary),
  ];
  const ev: [string, string][] = (r.evidence ?? []).map((f) => [
    f.name,
    f.value,
  ]);
  if (ev.length) parts.push(facts(ev));
  if (r.passage)
    parts.push(
      h('div', { class: 'kit-label' }, isFix(r) ? 'the passage' : 'now'),
      prose(r.passage, { start: r.source.start || 1 }),
    );
  const v = verdictOf(r);
  const text = fieldOf(r, 'text');
  if (text && v !== 'delete')
    parts.push(
      h(
        'div',
        { class: 'kit-label' },
        r.decision?.action === 'edit' && r.decision.text
          ? 'your text'
          : 'proposed text',
      ),
      prose(text, { start: r.source.start || 1 }),
    );

  if (isFix(r)) {
    const done = ctx.appliedIn(r);
    const undone = r.decision?.action === 'reject';
    parts.push(
      card({
        edge: 'wait',
        head: undone
          ? 'undone · stays as it is'
          : `applied · certain · ${fixOf(r)}`,
        body: undone
          ? 'You took this fix out of the round: sift leaves the passage alone.'
          : `${cap(r.summary)}. sift is certain of this one, so it ${fixOf(r) === 'rewrite' ? 'rewrites' : 'removes'} the passage when it applies the round, unless you undo it.`,
      }),
      h('div', { class: 'kit-label' }, 'change'),
      done
        ? h(
            'p',
            { class: 'sift-why' },
            `already applied on ${done.branch}: change it on the branch`,
          )
        : buttons([
            undone
              ? { label: 'redo (u)', run: () => ctx.redo([r]) }
              : { label: 'undo (u)', danger: true, run: () => ctx.undo([r]) },
          ]),
    );
  } else {
    const head = r.verdict
      ? `proposes · ${r.verdict}${r.destination ? ` → ${r.destination}` : ''}`
      : 'no proposal yet';
    parts.push(
      card({
        edge: r.verdict ? 'agent' : 'wait',
        head,
        body:
          r.reason ||
          (r.verdict
            ? 'No reason given.'
            : 'The agent has not proposed a verdict for this row. Edit to give it one, or reject it to leave the file as it is.'),
      }),
      h('div', { class: 'kit-label' }, 'the proposal'),
    );
    const a = r.decision?.action;
    const bs: Button[] = [
      {
        label: '1 accept',
        fill: a === 'accept',
        disabled: !r.verdict,
        run: () => ctx.accept([r]),
      },
      { label: '2 edit', fill: a === 'edit', run: () => ctx.startEdit(r) },
      {
        label: '3 reject',
        fill: a === 'reject',
        danger: true,
        run: () => ctx.reject([r]),
      },
    ];
    if (r.decision) bs.push({ label: 'clear (u)', run: () => ctx.clear(r) });
    parts.push(buttons(bs));
    if (r.decision)
      parts.push(
        h(
          'p',
          { class: 'sift-why' },
          `your decision: ${rowMeta(r)}${r.decision.sent ? ' · sent' : ''}`,
        ),
      );
    if (ctx.editing === r.id) parts.push(...editForm(ctx, r));
  }
  parts.push(...noteBlock(ctx, r));
  if (r.source.file || r.source.repo) parts.push(wholeFile(ctx, r));
  return h('div', { class: 'kit-doc' }, ...parts);
}

/** The open group: what it holds and the decisions that take it all. */
export function groupDoc(ctx: Ctx, g: Group): HTMLElement {
  const rows = g.rows;
  const decided = rows.filter((r) => r.decision).length;
  const proposed = rows.filter((r) => r.verdict).length;
  const parts: (HTMLElement | string)[] = [
    h('div', { class: 'kit-kick' }, g.kicker),
    h('h1', { class: 'kit-h1' }, g.title),
  ];
  if (ctx.view === 'applied') {
    const live = rows.filter(
      (r) => isFix(r) && r.decision?.action !== 'reject',
    );
    const undone = rows.filter(
      (r) => isFix(r) && r.decision?.action === 'reject',
    );
    parts.push(
      facts([
        ['applied', String(live.length)],
        ['undone', String(undone.length)],
      ]),
      card({
        edge: 'wait',
        head: 'certain fixes',
        body: 'sift applies these itself. Undo any you want left as they are; it holds until you send.',
        actions: [
          {
            label: `undo ${live.length}`,
            danger: true,
            disabled: !live.length,
            run: () => ctx.undo(live),
          },
          {
            label: `redo ${undone.length}`,
            disabled: !undone.length,
            run: () => ctx.redo(undone),
          },
        ],
      }),
    );
  } else {
    const acc = groupTargets(rows, 'accept');
    const rej = groupTargets(rows, 'reject');
    parts.push(
      facts([
        ['decided', `${decided} of ${rows.length}`],
        ['with a proposal', String(proposed)],
      ]),
      card({
        edge: 'agent',
        head: 'decide the group',
        body: 'Accept takes the proposal of every undecided row that has one; reject leaves every undecided row as it is. A row you decided on its own keeps its decision.',
        actions: [
          {
            label: `1 accept ${acc.length}`,
            fill: true,
            disabled: !acc.length,
            run: () => ctx.accept(acc),
          },
          {
            label: `3 reject ${rej.length}`,
            danger: true,
            disabled: !rej.length,
            run: () => ctx.reject(rej),
          },
          { label: 'open the first', run: () => ctx.open(`r:${rows[0].id}`) },
        ],
      }),
    );
  }
  parts.push(
    h('div', { class: 'kit-label' }, 'rows'),
    h(
      'table',
      { class: 'sift-rows' },
      h(
        'tr',
        null,
        h('th', null, 'line'),
        h('th', null, 'passage'),
        h('th', null, 'proposal'),
        h('th', null, 'decision'),
      ),
      rows.map((r) =>
        h(
          'tr',
          {
            onclick: () => ctx.open(`r:${r.id}`),
            tabindex: 0,
            onkeydown: (e: KeyboardEvent) =>
              e.key === 'Enter' && ctx.open(`r:${r.id}`),
          },
          h(
            'td',
            { class: 'n' },
            r.source.start ? String(r.source.start) : '—',
          ),
          h('td', { class: 'p' }, rowTitle(r)),
          h('td', { class: 'n' }, verdictOf(r) || '—'),
          h('td', { class: 'n' }, rowMeta(r).split(' · ')[0]),
        ),
      ),
    ),
  );
  return h('div', { class: 'kit-doc' }, ...parts);
}

export function message(
  kick: string,
  title: string,
  body: string,
): HTMLElement {
  return h(
    'div',
    { class: 'kit-doc' },
    h('div', { class: 'kit-kick' }, kick),
    h('h1', { class: 'kit-h1' }, title),
    h('p', { class: 'sift-lead' }, body),
  );
}
