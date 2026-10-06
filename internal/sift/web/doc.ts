// doc.ts — the reading column. An audit round's file to change: the
// summary, the versions side by side (current, recommended, yours), the
// findings with what the chosen version does, the note, and the diff from
// the audited file (wrapping, certain fixes marked). Its files with
// nothing to change: each with its findings and the agent's reasons, agree
// or disagree (with a note). A backlog round's row (facts, the passage, the
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
import {
  certainLines,
  chosen,
  didFor,
  diffLines,
  hunks,
  snippet,
  splitLines,
  type Choice,
} from './files.ts';
import { prose } from './prose.ts';
import type { FileOut } from './api.ts';

export interface Ctx {
  home: string;
  /** The row being edited, if any, and that row as the editor opened on
   * it (the form is drawn from it). */
  editing: string | null;
  edited: Finding | null;
  noteOf(r: Finding): string;
  setNote(r: Finding, v: string): void;
  accept(rows: Finding[]): void;
  reject(rows: Finding[]): void;
  startEdit(r: Finding): void;
  cancelEdit(): void;
  saveEdit(r: Finding, f: EditForm): string | null;
  clear(r: Finding): void;
  /** A decision on some of rows is saving: theirs wait. */
  busy(rows: Finding[]): boolean;
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
    h('h1', { class: 'kit-h1' }, fieldOf(r, 'title') || r.summary),
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
    // Once edited, the page shows the edit, so accept keeps it; going back
    // to the proposal is its own step (u), which shows it again first.
    const a = r.decision?.action;
    const edited = a === 'edit';
    const busy = ctx.busy([r]);
    const bs: Button[] = [
      {
        label: edited ? '1 accept your edit' : '1 accept',
        fill: a === 'accept',
        disabled: !v || busy,
        run: () => ctx.accept([r]),
      },
      {
        label: '2 edit',
        fill: a === 'edit',
        disabled: busy,
        run: () => ctx.startEdit(r),
      },
      {
        label: '3 reject',
        fill: a === 'reject',
        danger: true,
        disabled: busy,
        run: () => ctx.reject([r]),
      },
    ];
    if (r.decision)
      bs.push({
        label: edited ? 'revert to the proposal (u)' : 'clear (u)',
        disabled: busy,
        run: () => ctx.clear(r),
      });
    parts.push(buttons(bs));
    if (r.decision)
      parts.push(
        h(
          'p',
          { class: 'sift-why' },
          `your decision: ${rowMeta(r)}${r.decision.sent ? ' · sent' : ''}`,
        ),
      );
    if (ctx.editing === r.id) parts.push(...editForm(ctx, ctx.edited ?? r));
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
            disabled: !acc.length || ctx.busy(rows),
            run: () => ctx.accept(acc),
          },
          {
            label: `3 reject ${rej.length}`,
            danger: true,
            disabled: !rej.length || ctx.busy(rows),
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
  /** The file being edited, if any, and that file as the editor opened on
   * it (the form is drawn from it). */
  editing: string | null;
  edited: FileView | null;
  /** The round's files (for linked files' paths). */
  files: FileView[];
  /** The file's findings, in line order. */
  rowsOf(f: FileView): Finding[];
  /** The audited content: a string once loaded, an Error if it could not
   * be, undefined while it loads. */
  baseOf(f: FileView): string | Error | undefined;
  /** The person's own version of f: its edit, or the last one this page
   * saw, which stays a choice after another version is picked. */
  yours(f: FileView): string | undefined;
  noteOf(f: FileView): string;
  setNote(f: FileView, v: string): void;
  /** Picks a version: current (a reject), recommended (an accept), yours
   * (an edit with the person's own version). */
  pick(f: FileView, c: Choice): void;
  startEdit(f: FileView): void;
  cancelEdit(): void;
  /** Saves the edit; an error message, or null when saved. */
  saveEdit(f: FileView, content: string): string | null;
  clear(f: FileView): void;
  /** A decision on f's group is saving: its decisions wait. */
  busy(f: FileView): boolean;
}

const kb = (n: number) => `${(n / 1000).toFixed(1)} KB`;
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

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
  const text = h('textarea', {
    class: 'sift-field sift-text sift-whole',
    'aria-label': 'your version of the whole file',
    spellcheck: false,
  }) as HTMLTextAreaElement;
  text.value = ctx.yours(f) ?? f.rec?.content ?? '';
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
      'your version · the whole file as it will be written',
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
  return h(
    'table',
    { class: 'sift-rows sift-findings' },
    h(
      'tr',
      null,
      h('th', null, 'line'),
      h('th', null, 'finding'),
      h('th', null, 'what the chosen version does'),
    ),
    rows.map((r) => {
      const a = f.rec?.findings.find((x) => x.row === r.id);
      const did = didFor(f, r.id);
      // The recommendation's own words carry its fixed / kept mark.
      const mark =
        a && did.startsWith(`${a.did}: `)
          ? [
              h('span', { class: `sift-did ${a.did}` }, a.did),
              ' ',
              did.slice(a.did.length + 2),
            ]
          : did;
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
        h('td', { class: 'p' }, mark),
      );
    }),
  );
}

/** One choice of version: its key and name, what it is, and the first
 * lines it changes. */
function choice(
  ctx: FileCtx,
  f: FileView,
  c: Choice,
  key: string,
  what: string,
  lines: string[] | null,
  sign: '-' | '+',
): HTMLElement {
  const on = chosen(f) === c;
  const busy = ctx.busy(f);
  return h(
    'div',
    {
      class: `sift-choice ${c}${on ? ' chosen' : ''}`,
      role: 'button',
      tabindex: 0,
      'aria-pressed': on ? 'true' : 'false',
      'aria-disabled': busy ? 'true' : undefined,
      onclick: () => !busy && ctx.pick(f, c),
      onkeydown: (e: KeyboardEvent) => {
        if ((e.key === 'Enter' || e.key === ' ') && !busy) {
          e.preventDefault();
          e.stopPropagation();
          ctx.pick(f, c);
        }
      },
    },
    h('div', { class: 'sift-ck' }, `${key} · ${c}${on ? ' · chosen' : ''}`),
    h('b', null, what),
    lines === null
      ? h('div', { class: 'sift-snip kit-muted' }, 'loading…')
      : lines.length
        ? h(
            'div',
            { class: `sift-snip ${sign === '-' ? 'del' : 'add'}` },
            lines.map((l) =>
              h('div', null, l === '…' ? l : `${sign} ${l || ' '}`),
            ),
          )
        : h('div', { class: 'sift-snip kit-muted' }, 'no change'),
  );
}

/** The open file to change: which version should it have? */
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
    h(
      'div',
      { class: 'kit-kick' },
      `${f.class} file · ${plural(rows.length, 'finding')} · ${where}`,
    ),
    h('h1', { class: 'kit-h1' }, 'Which version should this file have?'),
    h('p', { class: 'sift-lead sift-summary' }, f.rec?.summary ?? ''),
    facts(ev),
  ];
  const base = ctx.baseOf(f);
  const rec = f.rec?.content ?? '';
  const mine = ctx.yours(f);
  const snip = (after: string, sign: '-' | '+') =>
    typeof base === 'string' ? snippet(base, after, sign, 4) : null;
  const pick = h(
    'div',
    { class: `sift-pick${mine !== undefined ? ' three' : ''}` },
    choice(ctx, f, 'current', '1', 'Keep it as it is', snip(rec, '-'), '-'),
    choice(
      ctx,
      f,
      'recommended',
      '2',
      "Use the agent's version",
      snip(rec, '+'),
      '+',
    ),
    mine !== undefined
      ? choice(
          ctx,
          f,
          'yours',
          '3',
          'Use your own version',
          snip(mine, '+'),
          '+',
        )
      : '',
  );
  parts.push(pick);
  const busy = ctx.busy(f);
  const bs: Button[] = [
    {
      label:
        mine !== undefined
          ? 'e · edit your version'
          : 'e · write my own version',
      disabled: busy,
      run: () => ctx.startEdit(f),
    },
  ];
  if (f.decision)
    bs.push({ label: 'clear (u)', disabled: busy, run: () => ctx.clear(f) });
  const decide = buttons(bs);
  decide.classList.add('sift-decide');
  parts.push(decide);
  const said: string[] = [];
  if (f.decision?.sent) said.push('sent');
  if (others.length)
    said.push(
      `picked together with ${others.map((o) => displayPath(o.source, ctx.home)).join(', ')}: the recommendation moves text between them`,
    );
  if (busy) said.push('saving');
  if (said.length)
    parts.push(h('p', { class: 'sift-why' }, said.join('. ') + '.'));
  if (ctx.editing === f.key) parts.push(fileEdit(ctx, ctx.edited ?? f));

  parts.push(
    h(
      'div',
      { class: 'kit-label' },
      'findings, and what the chosen version does about them',
    ),
    findingsTable(f, rows),
  );
  const field = noteField({
    value: ctx.noteOf(f),
    placeholder: 'note to the agent (n)',
    onCommit: (v) => ctx.setNote(f, v),
  });
  field.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.isComposing) field.blur();
  });
  parts.push(h('div', { class: 'kit-label' }, 'note'), field);

  // The change line by line: your version's when it is the one picked,
  // else the recommendation's.
  const showMine = chosen(f) === 'yours' && mine !== undefined;
  parts.push(
    h(
      'div',
      { class: 'kit-label' },
      showMine
        ? 'the change · your version'
        : chosen(f) === 'current'
          ? 'the change · recommended, not picked'
          : 'the change · recommended',
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
  else parts.push(diffView(base, showMine ? mine : rec, certainLines(rows)));
  return h('div', { class: 'kit-doc sift-file' }, ...parts);
}

// ---- nothing to change ------------------------------------------------------

/** What the nothing-to-change view needs from app.ts. */
export interface NoChangeCtx {
  home: string;
  rowsOf(f: FileView): Finding[];
  /** The file whose disagree form is open, if any. */
  disagreeing: string | null;
  agree(f: FileView): void;
  /** Agrees with every file not decided yet. */
  agreeAll(): void;
  startDisagree(f: FileView): void;
  cancelDisagree(): void;
  /** Disagrees with a note; an error message, or null when sent. */
  disagree(f: FileView, note: string): string | null;
  clear(f: FileView): void;
  busy(f: FileView): boolean;
}

/** The files the agent recommends leaving as they are, each with its
 * findings and the reasons they are kept: agree (one, or all), or
 * disagree with a note. */
export function noChangeDoc(ctx: NoChangeCtx, files: FileView[]): HTMLElement {
  const open = files.filter((f) => !f.decision);
  const parts: (HTMLElement | string)[] = [
    h(
      'div',
      { class: 'kit-kick' },
      `nothing to change · ${plural(files.length, 'file')}`,
    ),
    h(
      'h1',
      { class: 'kit-h1' },
      'The agent recommends no change to these files',
    ),
    h(
      'p',
      { class: 'sift-lead' },
      'Each finding below has its reason. Agree, and they stop showing until the text changes. Disagree on any file and say why: it moves to "to change" once the agent has rewritten it.',
    ),
  ];
  for (const f of files) {
    const rows = ctx.rowsOf(f);
    const busy = ctx.busy(f);
    const a = f.decision?.action;
    const state =
      a === 'accept'
        ? `agreed${f.muted ? ' · muted' : ''}`
        : a === 'reject'
          ? 'disagreed: goes back to the agent'
          : '';
    const bs: Button[] = [
      {
        label: 'agree',
        fill: a === 'accept',
        disabled: busy,
        run: () => ctx.agree(f),
      },
      {
        label: 'disagree…',
        fill: a === 'reject',
        danger: true,
        disabled: busy,
        run: () => ctx.startDisagree(f),
      },
    ];
    if (f.decision)
      bs.push({ label: 'clear', disabled: busy, run: () => ctx.clear(f) });
    const head = h(
      'div',
      { class: 'sift-nc-head' },
      h(
        'div',
        { class: 'sift-nc-what' },
        h('div', { class: 'sift-nc-path' }, displayPath(f.source, ctx.home)),
        h(
          'div',
          { class: 'sift-check' },
          [
            plural(rows.length, 'finding'),
            state,
            f.decision?.sent ? 'sent' : '',
          ]
            .filter(Boolean)
            .join(' · '),
        ),
      ),
      buttons(bs),
    );
    const block = h(
      'section',
      { class: `sift-nc${a ? ` ${a}` : ''}`, dataset: { key: f.key } },
      head,
    );
    if (a === 'reject' && f.decision?.note && ctx.disagreeing !== f.key)
      block.append(
        h('p', { class: 'sift-why' }, `your note: ${f.decision.note}`),
      );
    if (ctx.disagreeing === f.key) block.append(disagreeForm(ctx, f));
    block.append(
      h(
        'table',
        { class: 'sift-rows sift-findings' },
        h(
          'tr',
          null,
          h('th', null, 'line'),
          h('th', null, 'finding'),
          h('th', null, 'why the agent keeps it'),
        ),
        rows.map((r) =>
          h(
            'tr',
            null,
            h(
              'td',
              { class: 'n' },
              r.source.start ? String(r.source.start) : 'file',
            ),
            h(
              'td',
              { class: 'p' },
              h('div', { class: 'sift-check' }, r.check),
              rowTitle(r),
            ),
            h(
              'td',
              { class: 'p' },
              f.rec?.findings.find((x) => x.row === r.id)?.how ?? '—',
            ),
          ),
        ),
      ),
    );
    parts.push(block);
  }
  const all = buttons([
    {
      label: `a · agree with all ${open.length}`,
      fill: true,
      disabled: !open.length,
      run: () => ctx.agreeAll(),
    },
  ]);
  all.classList.add('sift-agree-all');
  parts.push(all);
  return h('div', { class: 'kit-doc sift-nochange' }, ...parts);
}

function disagreeForm(ctx: NoChangeCtx, f: FileView): HTMLElement {
  const note = h('input', {
    class: 'sift-field sift-disagree-note',
    type: 'text',
    value: f.decision?.action === 'reject' ? (f.decision.note ?? '') : '',
    placeholder: 'what should change? (the agent rewrites the file from this)',
    'aria-label': 'why you disagree',
  }) as HTMLInputElement;
  const err = h('p', { class: 'sift-err', role: 'alert' });
  const send = () => {
    err.textContent = ctx.disagree(f, note.value) ?? '';
  };
  queueMicrotask(() => note.focus());
  return h(
    'form',
    {
      class: 'sift-disagree',
      onsubmit: (e: Event) => {
        e.preventDefault();
        send();
      },
    },
    note,
    err,
    buttons([
      { label: 'disagree (↵)', danger: true, fill: true, run: send },
      { label: 'cancel', run: () => ctx.cancelDisagree() },
    ]),
  );
}
