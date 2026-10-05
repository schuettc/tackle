// doc.ts — the reading column. An audit round's file: the summary, the
// diff from the audited file to the recommendation (wrapping, certain
// fixes marked), accept / edit / reject, the findings with what the
// rewrite did, the note. A backlog round's row (facts, the passage, the
// proposal, accept / edit / reject, the note, the whole file) or group
// (its rows and the decisions that take them all). It only draws; app.ts
// holds state.

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
  groupTargets,
  rowMeta,
  rowTitle,
  verdictOf,
  type EditForm,
  type Group,
} from './model.ts';
import { certainLines, diffLines, hunks, splitLines } from './files.ts';
import { prose } from './prose.ts';
import type { FileOut } from './api.ts';

export interface Ctx {
  home: string;
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
  open(key: string): void;
  file(r: Finding): Promise<FileOut>;
}

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
      h('div', { class: 'kit-label' }, 'now'),
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

  {
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
  {
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

// ---- an audit round's file --------------------------------------------------

/** What the file view needs from app.ts. */
export interface FileCtx {
  home: string;
  /** The file being edited, if any. */
  editing: string | null;
  /** The round's files (for linked files' paths). */
  files: FileView[];
  /** The file's findings, in line order. */
  rowsOf(f: FileView): Finding[];
  /** The audited content: a string once loaded, an Error if it could not
   * be, undefined while it loads. */
  baseOf(f: FileView): string | Error | undefined;
  noteOf(f: FileView): string;
  setNote(f: FileView, v: string): void;
  accept(f: FileView): void;
  reject(f: FileView): void;
  startEdit(f: FileView): void;
  cancelEdit(): void;
  /** Saves the edit; an error message, or null when saved. */
  saveEdit(f: FileView, content: string): string | null;
  clear(f: FileView): void;
}

const kb = (n: number) => `${(n / 1000).toFixed(1)} KB`;
const word: Record<string, string> = {
  accept: 'accepted',
  edit: 'edited',
  reject: 'rejected',
};

/** The diff from the audited file to what would be written, wrapping, with
 * the lines of certain findings marked. */
export function diffView(
  base: string,
  after: string,
  certain: Set<number>,
): HTMLElement {
  const hs = hunks(diffLines(base, after), 3);
  if (!hs.length)
    return h('p', { class: 'sift-why' }, 'No change: the file stays as it is.');
  return h(
    'div',
    { class: 'sift-diff' },
    hs.flatMap((hk) => [
      h('div', { class: 'sift-hunk' }, hk.header),
      ...hk.lines.map((l) => {
        const cert =
          l.kind === '-' && l.old !== undefined && certain.has(l.old);
        const cls = l.kind === '-' ? 'del' : l.kind === '+' ? 'add' : 'ctx';
        return h(
          'div',
          {
            class: `sift-dl ${cls}${cert ? ' cert' : ''}`,
            title: cert
              ? 'a certain finding: the rewrite must fix it'
              : undefined,
          },
          h(
            'span',
            { class: 'sift-n', 'aria-hidden': 'true' },
            l.old ? String(l.old) : '',
          ),
          h(
            'span',
            { class: 'sift-n', 'aria-hidden': 'true' },
            l.new ? String(l.new) : '',
          ),
          h(
            'span',
            { class: 'sift-sign', 'aria-hidden': 'true' },
            cert ? '!' : l.kind,
          ),
          h('span', { class: 'sift-t' }, l.text),
        );
      }),
    ]),
  );
}

function fileEdit(ctx: FileCtx, f: FileView): HTMLElement {
  const d = f.decision?.action === 'edit' ? f.decision : undefined;
  const text = h('textarea', {
    class: 'sift-field sift-text sift-whole',
    'aria-label': 'the whole recommended file',
    spellcheck: false,
  }) as HTMLTextAreaElement;
  text.value = d?.content ?? f.rec?.content ?? '';
  text.rows = Math.min(40, Math.max(12, splitLines(text.value).length + 2));
  const err = h('p', { class: 'sift-err', role: 'alert' });
  const save = () => {
    err.textContent = ctx.saveEdit(f, text.value) ?? '';
  };
  queueMicrotask(() => text.focus());
  return h(
    'form',
    {
      class: 'sift-edit',
      onsubmit: (e: Event) => {
        e.preventDefault();
        save();
      },
    },
    h(
      'div',
      { class: 'kit-label' },
      'edit · the whole file as it will be written',
    ),
    text,
    err,
    buttons([
      { label: 'save edit (⌘↵)', fill: true, run: save },
      { label: 'cancel', run: () => ctx.cancelEdit() },
    ]),
  );
}

function findingsTable(f: FileView, rows: Finding[]): HTMLElement {
  const did = new Map((f.rec?.findings ?? []).map((a) => [a.row, a]));
  return h(
    'table',
    { class: 'sift-rows sift-findings' },
    h(
      'tr',
      null,
      h('th', null, 'line'),
      h('th', null, 'finding'),
      h('th', null, 'what the rewrite did'),
    ),
    rows.map((r) => {
      const a = did.get(r.id);
      return h(
        'tr',
        { class: r.certain ? 'cert' : '' },
        h(
          'td',
          { class: 'n' },
          r.source.start ? String(r.source.start) : 'file',
        ),
        h(
          'td',
          { class: 'p' },
          h(
            'div',
            { class: 'sift-check' },
            r.certain ? `${r.check} · certain` : r.check,
          ),
          rowTitle(r),
        ),
        h(
          'td',
          { class: 'p' },
          a
            ? [h('span', { class: `sift-did ${a.did}` }, a.did), ' ', a.how]
            : '—',
        ),
      );
    }),
  );
}

/** The open file. */
export function fileDoc(ctx: FileCtx, f: FileView): HTMLElement {
  const rows = ctx.rowsOf(f);
  const certain = rows.filter((r) => r.certain).length;
  const others = f.group
    .filter((k) => k !== f.key)
    .map((k) => ctx.files.find((x) => x.key === k))
    .filter((x): x is FileView => !!x);
  const where = displayPath(f.source, ctx.home);
  const ev: [string, string][] = [
    ['size', `${kb(f.size)} → ${kb(f.after)} · budget ${kb(f.budget)}`],
    [
      'findings',
      certain ? `${rows.length}, ${certain} certain` : String(rows.length),
    ],
  ];
  if (others.length)
    ev.push([
      'linked',
      others.map((o) => displayPath(o.source, ctx.home)).join(', '),
    ]);
  if (f.source.ref)
    ev.push([
      'read at',
      f.commit ? `${f.source.ref} (${f.commit.slice(0, 10)})` : f.source.ref,
    ]);
  const parts: (HTMLElement | string)[] = [
    h('div', { class: 'kit-kick' }, `${f.class} file · ${where}`),
    h('h1', { class: 'kit-h1' }, where),
    h('p', { class: 'sift-lead sift-summary' }, f.rec?.summary ?? ''),
    facts(ev),
  ];
  const edited = f.decision?.action === 'edit';
  const after = edited ? (f.decision?.content ?? '') : (f.rec?.content ?? '');
  const base = ctx.baseOf(f);
  parts.push(
    h(
      'div',
      { class: 'kit-label' },
      edited ? 'the change · your edit' : 'the change',
    ),
  );
  if (base instanceof Error)
    parts.push(
      h(
        'p',
        { class: 'sift-err' },
        `could not load the audited file: ${base.message}`,
      ),
    );
  else if (base === undefined)
    parts.push(h('p', { class: 'kit-muted sift-loading' }, 'loading…'));
  else parts.push(diffView(base, after, certainLines(rows)));

  // Once edited, the page shows the edit, so accept keeps it; going back
  // to the recommendation is its own step (u), which shows it again first.
  const a = f.decision?.action;
  const bs: Button[] = [
    {
      label: edited ? '1 accept your edit' : '1 accept',
      fill: a === 'accept',
      run: () => ctx.accept(f),
    },
    { label: '2 edit', fill: a === 'edit', run: () => ctx.startEdit(f) },
    {
      label: '3 reject',
      fill: a === 'reject',
      danger: true,
      run: () => ctx.reject(f),
    },
  ];
  if (f.decision)
    bs.push({
      label: edited ? 'revert to the recommendation (u)' : 'clear (u)',
      run: () => ctx.clear(f),
    });
  const decide = buttons(bs);
  decide.classList.add('sift-decide');
  parts.push(decide);
  const said: string[] = [];
  if (f.decision)
    said.push(
      `your decision: ${word[f.decision.action]}${f.decision.sent ? ' · sent' : ''}`,
    );
  if (others.length)
    said.push(
      `decided together with ${others.map((o) => displayPath(o.source, ctx.home)).join(', ')}: the recommendation moves text between them`,
    );
  if (said.length)
    parts.push(h('p', { class: 'sift-why' }, said.join('. ') + '.'));
  if (ctx.editing === f.key) parts.push(fileEdit(ctx, f));

  parts.push(
    h('div', { class: 'kit-label' }, 'findings'),
    findingsTable(f, rows),
  );
  const field = noteField({
    value: ctx.noteOf(f),
    placeholder: 'why (n)',
    onCommit: (v) => ctx.setNote(f, v),
  });
  field.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.isComposing) field.blur();
  });
  parts.push(h('div', { class: 'kit-label' }, 'note'), field);
  return h('div', { class: 'kit-doc sift-file' }, ...parts);
}
