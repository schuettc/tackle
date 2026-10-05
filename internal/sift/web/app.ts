// app.ts — the sift review page: the bar, the list, the reading column, the
// route (#/open[/<entry>]), the live client and every decision.
//
// An audit round is decided one file at a time: the list has one entry per
// file (linked files together), and the reading column shows the file's
// recommendation as a diff. While the agent is still recommending, the
// page says how far it got and takes no decision. A backlog round is
// decided one item at a time, in groups.
//
// State lives here; doc.ts only draws. Every decision is applied to the
// page at once and saved as it is given; a failure rolls it back and says
// so in the bar, a 409 reloads.

import {
  bar,
  createKeys,
  h,
  initTheme,
  list,
  live,
  type Chip,
  type LiveStatus,
  type StatusTone,
} from '/_kit/kit.js';
import { ApiError, client, newApi, type DecisionIn } from './api.ts';
import {
  fileDoc,
  groupDoc,
  message,
  rowDoc,
  type Ctx,
  type FileCtx,
} from './doc.ts';
import {
  decideLocal,
  fileEntries,
  fileMeta,
  filesProgress,
  holdPrints,
  printsFor,
  unsentFiles,
  type FileEntry,
} from './files.ts';
import {
  displayPath,
  editDecision,
  editTarget,
  entries,
  groupTargets,
  groupsOf,
  markSent,
  nextOpen,
  progress,
  rowMeta,
  rowTitle,
  unsent,
  type Entry,
} from './model.ts';

const qs = new URLSearchParams(location.search);
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

type Item = Entry | (FileEntry & { kind: 'file' });

function frag(key?: string | null): string {
  return `#/open${key ? `/${encodeURIComponent(key)}` : ''}`;
}

function parseRoute(hash: string): string | null {
  const m = /^#\/open(?:\/(.+))?$/.exec(hash);
  if (!m?.[1]) return null;
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return null;
  }
}

function signature(r: Review): string {
  return (
    `${r.round?.id ?? 0}|${r.progress?.state}|${r.sends}|${r.applies.map((a) => a.repo + a.state).join(',')}|` +
    r.files
      .map((f) => {
        const d = f.decision;
        return `${f.key}:${f.fingerprint}:${d ? `${d.action}/${d.content?.length ?? 0}/${d.note ?? ''}/${d.sent ? 1 : 0}` : ''}`;
      })
      .join(';') +
    '|' +
    r.rows
      .map((x) => {
        const d = x.decision;
        return `${x.id}:${x.verdict ?? ''}:${x.text ?? ''}:${d ? `${d.action}/${d.verdict ?? ''}/${d.text ?? ''}/${d.title ?? ''}/${d.note ?? ''}/${d.sent ? 1 : 0}` : ''}`;
      })
      .join(';')
  );
}

const perItem = (r: Review | null) =>
  r?.round?.kind === 'backlog' || r?.round?.kind === 'intake';

export function boot(): void {
  // ---- state -----------------------------------------------------------------
  let review: Review | null = null;
  let openKey: string | null = null;
  let editing: string | null = null;
  let filter = '';
  let search = '';
  let lastFrag = '';
  let lastView = '';
  let syncing = false;
  let stale = false;
  let inflight = 0;
  let loading = false;
  let dirty = false;
  let flashing = false;
  let liveHandle: ReturnType<typeof live> | null = null;
  let statusTimer: ReturnType<typeof setTimeout> | undefined;
  let prevLive: LiveStatus = 'live';
  let loadTimer: ReturnType<typeof setTimeout> | undefined;
  let shown: Item[] = [];
  const pending = new Map<string, string>(); // notes waiting for a decision
  // A file's audited content, read on demand (by round, file and base).
  const bases = new Map<string, string | Error>();

  // ---- api -------------------------------------------------------------------
  const gated = (input: RequestInfo | URL, init?: RequestInit) =>
    stale
      ? Promise.reject(new Error('sift serve restarted: this tab has stopped'))
      : fetch(input, init);
  const api = client(newApi({ fetch: gated, onStale: () => goStale() }));

  // ---- derived ---------------------------------------------------------------
  const home = () => review?.home ?? '';
  const files = () => review?.files ?? [];
  // An audit round waits for every file's recommendation, a backlog round
  // for the agent's verdict on every item.
  const recommending = () => review?.progress?.state === 'recommending';
  const matchesRow = (r: Finding) => {
    if (filter && r.check !== filter) return false;
    if (!search) return true;
    const q = search.toLowerCase();
    return [
      displayPath(r.source, home()),
      r.passage ?? '',
      r.summary,
      r.title ?? '',
      r.check,
    ].some((s) => s.toLowerCase().includes(q));
  };
  const items = (): Item[] => {
    if (perItem(review))
      return entries(
        groupsOf((review?.rows ?? []).filter(matchesRow), true, home()),
        openKey,
      );
    const q = search.toLowerCase();
    return fileEntries(files(), home())
      .filter(
        (e) =>
          !q || displayPath(e.file.source, home()).toLowerCase().includes(q),
      )
      .map((e) => ({ ...e, kind: 'file' as const }));
  };
  const rowById = (id: string) => review?.rows.find((r) => r.id === id);
  const fileByKey = (k: string) => files().find((f) => f.key === k);
  /** For an edit to merge:C, C's fingerprint as the page shows it. */
  const targetPrint = (d: Decision) => {
    const id = editTarget(d);
    return id ? rowById(id)?.fingerprint : undefined;
  };
  const current = (): Item | undefined => shown.find((e) => e.key === openKey);
  const baseKey = (f: FileView) =>
    `${review?.round?.id ?? 0}:${f.key}:${f.base}`;

  // ---- bar -------------------------------------------------------------------
  const b = bar({
    brand: { name: 'sift' },
    sections: [{ id: 'review', label: 'review', count: '' }],
    active: 'review',
    status: '',
    staleText: 'restarted · continued in a new tab',
  });
  const theme = initTheme('sift', b.themeControl);
  const forced = qs.get('theme');
  if (forced === 'light' || forced === 'dark') theme.set(forced);

  function baseStatus(): string {
    if (!review) return '';
    if (!review.round) return 'no round yet — run sift check';
    const r = review.round;
    const day = r.at ? r.at.slice(0, 10) : '';
    const kind = perItem(review)
      ? r.kind
      : r.kind === 'on-demand'
        ? 'audit'
        : `${r.kind} audit`;
    const owner = r.owner ? ` · to ${r.owner}` : '';
    const state = review.progress?.state ? ` · ${review.progress.state}` : '';
    return `${kind} · round ${r.id} · ${day}${state}${owner}`;
  }
  function flash(text: string, tone: StatusTone = 'muted'): void {
    clearTimeout(statusTimer);
    flashing = true;
    b.setStatus(text, { tone });
    statusTimer = setTimeout(
      () => {
        flashing = false;
        b.setStatus(baseStatus());
      },
      tone === 'danger' ? 15000 : 10000,
    );
  }
  function refreshBar(): void {
    const p = perItem(review)
      ? progress(review?.rows ?? [])
      : filesProgress(files());
    b.setCount(
      'review',
      review?.round && !recommending() ? `${p.decided}/${p.total}` : '',
    );
    const n = unsent(review?.rows ?? []) + unsentFiles(files());
    b.setPrimary(
      n && review?.round && !recommending()
        ? { label: `Send ${n}`, run: () => void send() }
        : null,
    );
    if (!flashing) b.setStatus(baseStatus());
  }

  // ---- list ------------------------------------------------------------------
  const read = h('main', { class: 'kit-read' });
  const l = list<Item>({
    label: 'files',
    views: [],
    onChip(group, id) {
      if (group === 'filter') {
        filter = filter === id ? '' : id;
        render();
      }
    },
    search: {
      placeholder: 'search files',
      onInput(text) {
        search = text.trim();
        render();
      },
    },
    row(e) {
      if (e.kind === 'file') {
        const f = e.file;
        return {
          id: e.key,
          key: displayPath(f.source, home()),
          title:
            f.rec?.summary.split(/(?<=\.)\s/)[0] ||
            `${plural(f.rows.length, 'finding')}`,
          meta: fileMeta(f),
        };
      }
      if (e.kind === 'group') {
        const g = e.group;
        const done = g.rows.filter((r) => r.decision).length;
        return {
          id: e.key,
          key: g.kicker,
          title: g.title,
          meta: done === g.rows.length ? '✓' : `${done}/${g.rows.length}`,
        };
      }
      const r = e.row;
      const at = r.source.start ? `:${r.source.start}` : '';
      return {
        id: e.key,
        key: e.member
          ? r.source.start
            ? `line ${r.source.start}`
            : displayPath(r.source, home())
          : `${r.check} · ${displayPath(r.source, home())}${at}`,
        title: rowTitle(r),
        meta: rowMeta(r),
      };
    },
    openOnMove: true,
    onOpen(e) {
      if (syncing) return;
      go(frag(e.key));
    },
  });

  function chips(): void {
    l.setChips('view', []);
    if (!perItem(review)) {
      l.setChips('filter', []);
      return;
    }
    const counts = new Map<string, number>();
    for (const r of review?.rows ?? [])
      counts.set(r.check, (counts.get(r.check) ?? 0) + 1);
    const fs: Chip[] =
      counts.size > 1
        ? [...counts.entries()].map(([c, n]) => ({
            id: c,
            label: c,
            count: n,
            on: filter === c,
          }))
        : [];
    l.setChips('filter', fs);
  }

  // Linked files and a group's members carry a class of their own; the
  // kit's rows take none, so mark them after each render.
  function decorate(): void {
    const els = l.el.querySelectorAll<HTMLElement>('.kit-row');
    shown.forEach((e, i) => {
      els[i]?.classList.toggle('sift-group', e.kind === 'group');
      els[i]?.classList.toggle('sift-member', e.kind === 'row' && e.member);
      els[i]?.classList.toggle('sift-linked', e.kind === 'file' && e.linked);
    });
  }

  // ---- reading column contexts -----------------------------------------------
  const ctx: Ctx = {
    get home() {
      return home();
    },
    get editing() {
      return editing;
    },
    noteOf: (r) => pending.get(r.id) ?? r.decision?.note ?? '',
    setNote,
    accept: (rows) => decide(rows, 'accept'),
    reject: (rows) => decide(rows, 'reject'),
    startEdit(r) {
      editing = r.id;
      render();
    },
    cancelEdit() {
      editing = null;
      render();
    },
    saveEdit,
    clear,
    open: (key) => go(frag(key)),
    file: (r) => api.file(review?.round?.id ?? 0, r.id),
  };

  const fctx: FileCtx = {
    get home() {
      return home();
    },
    get editing() {
      return editing;
    },
    get files() {
      return files();
    },
    rowsOf: (f) =>
      f.rows
        .map((id) => rowById(id))
        .filter((r): r is Finding => !!r)
        .sort((a, c) => (a.source.start ?? 0) - (c.source.start ?? 0)),
    baseOf(f) {
      const v = bases.get(baseKey(f));
      if (v === undefined) loadBase(f);
      return v;
    },
    noteOf: (f) => pending.get(`f:${f.key}`) ?? f.decision?.note ?? '',
    setNote: setFileNote,
    accept: (f) => putFile(f, { action: 'accept' }),
    reject: (f) => putFile(f, { action: 'reject' }),
    startEdit(f) {
      editing = f.key;
      render();
    },
    cancelEdit() {
      editing = null;
      render();
    },
    saveEdit(f, content) {
      if (!content.trim())
        return 'the file is empty: reject it to leave it as it is';
      const was =
        f.decision?.action === 'edit' ? f.decision.content : f.rec?.content;
      if (content === was)
        return 'nothing changed: accept the recommendation instead';
      putFile(f, { action: 'edit', content });
      return null;
    },
    clear: clearFile,
  };

  const loadingBases = new Set<string>();
  function loadBase(f: FileView): void {
    const k = baseKey(f);
    const round = review?.round?.id;
    if (!round || loadingBases.has(k)) return;
    loadingBases.add(k);
    api.base(round, f.key).then(
      (out) => {
        bases.set(k, out.content);
        loadingBases.delete(k);
        render();
      },
      (err: Error) => {
        bases.set(k, err);
        loadingBases.delete(k);
        render();
      },
    );
  }

  // ---- rendering -------------------------------------------------------------
  function render(): void {
    refreshBar();
    chips();
    if (!review) return;
    if (!review.round) {
      shown = [];
      l.setItems([]);
      read.replaceChildren(
        message(
          'sift',
          'No round yet',
          'Run sift check; this page shows what it finds.',
        ),
      );
      return;
    }
    if (recommending()) {
      shown = [];
      l.setItems([]);
      const p = review.progress;
      read.replaceChildren(
        perItem(review)
          ? message(
              'recommending',
              `The agent is recommending: ${p.recommended} of ${plural(p.files, 'item')}`,
              'Every item arrives with what the agent recommends doing about it. This page opens for review when the last one is in.',
            )
          : message(
              'recommending',
              `The agent is recommending: ${p.recommended} of ${plural(p.files, 'file')}`,
              'Every file arrives with a recommendation: one revised version covering all its findings. This page opens for review when the last one is in.',
            ),
      );
      return;
    }
    shown = items();
    if (openKey && !shown.some((e) => e.key === openKey)) {
      // The open entry left the list or the search: forget it.
      openKey = null;
      shown = items();
    }
    l.setItems(shown);
    decorate();
    const vkey = `${openKey ?? ''}/${editing ?? ''}`;
    const e = current();
    if (e) {
      syncing = true;
      l.open(shown.indexOf(e));
      syncing = false;
      decorate();
      // A half-typed note or edit survives a re-render (another tab, a reload).
      const oldNote = read.querySelector<HTMLInputElement>('.kit-note');
      const oldEdit = read.querySelector<HTMLFormElement>('.sift-edit');
      const focus = document.activeElement;
      const doc =
        e.kind === 'file'
          ? fileDoc(fctx, e.file)
          : e.kind === 'group'
            ? groupDoc(ctx, e.group)
            : rowDoc(ctx, e.row);
      const noteNow =
        e.kind === 'file'
          ? fctx.noteOf(e.file)
          : e.kind === 'row'
            ? ctx.noteOf(e.row)
            : '';
      if (
        oldNote &&
        lastView === vkey &&
        (focus === oldNote || oldNote.value !== noteNow)
      )
        doc.querySelector('.kit-note')?.replaceWith(oldNote);
      if (oldEdit && lastView === vkey)
        doc.querySelector('.sift-edit')?.replaceWith(oldEdit);
      read.replaceChildren(doc);
      if (focus instanceof HTMLElement && read.contains(focus)) focus.focus();
    } else {
      read.replaceChildren(overview());
    }
    if (vkey !== lastView) read.scrollTop = 0;
    lastView = vkey;
  }

  function overview(): HTMLElement {
    if (perItem(review)) {
      const p = progress(review?.rows ?? []);
      if (!review?.rows.length)
        return message(
          'needs you',
          'Nothing here',
          'This round has nothing to decide.',
        );
      return message(
        'needs you',
        `${p.total - p.decided} of ${plural(p.total, 'item')} to decide`,
        'Open a group (↵) to decide it whole, or an item to decide it alone: 1 accept, 2 edit, 3 reject. Send returns your decisions to the agent.',
      );
    }
    const p = filesProgress(files());
    if (!p.total)
      return message(
        'needs you',
        'Nothing here',
        search
          ? 'Nothing matches the search.'
          : 'This round found nothing to change.',
      );
    const linked = shown.filter((e) => e.kind === 'file' && e.linked).length;
    return message(
      'needs you',
      `${p.total - p.decided} of ${plural(p.total, 'file')} to decide`,
      `Each file has one recommendation covering all its findings. Open one (↵) to read its diff, then 1 accept, 2 edit (the whole file), 3 reject.${linked ? ' Linked files move text between them and are decided together.' : ''} Send returns your decisions to the agent.`,
    );
  }

  // ---- routes ----------------------------------------------------------------
  function go(f: string): void {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const key = parseRoute(f);
    if (key !== openKey) editing = null;
    openKey = key;
    render();
  }

  function onHash(): void {
    if (location.hash === lastFrag) return;
    go(location.hash);
  }

  // ---- loading and live ------------------------------------------------------
  let loadAttempt = 0;
  async function load(): Promise<void> {
    clearTimeout(loadTimer);
    try {
      const r = await api.review();
      loadAttempt = 0;
      review = r;
      openKey = parseRoute(location.hash);
      lastFrag = location.hash;
      render();
      startLive(r.cursor);
    } catch (err) {
      review = null;
      l.setItems([]);
      refreshBar();
      if (err instanceof ApiError && err.isStale) return;
      loadTimer = setTimeout(
        () => void load(),
        [1000, 2000, 5000][loadAttempt++] ?? 10000,
      );
      read.replaceChildren(
        h(
          'div',
          { class: 'kit-doc' },
          h(
            'p',
            { class: 'sift-lead' },
            'sift serve is not answering; retrying.',
          ),
        ),
      );
    }
  }

  async function reload(): Promise<void> {
    if (inflight > 0 || loading) {
      dirty = true;
      return;
    }
    loading = true;
    try {
      do {
        dirty = false;
        const r = await api.review();
        if (review && signature(review) === signature(r)) {
          review.cursor = r.cursor;
          continue;
        }
        review = r;
        render();
      } while (dirty && inflight === 0);
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`could not reload: ${(err as Error).message}`, 'danger');
    } finally {
      loading = false;
    }
  }

  function startLive(cursor: string): void {
    if (liveHandle || stale) return;
    liveHandle = live({
      events: '/api/events',
      poll: '/api/poll',
      cursor,
      fetch: gated,
      onEvent: () => void reload(),
      onStatus(s: LiveStatus) {
        if (stale) return;
        if (s === 'stale') {
          goStale();
          return;
        }
        const was = prevLive;
        prevLive = s;
        if (s === 'down') {
          b.setLive('down', 'disconnected');
          return;
        }
        b.setLive(s);
        if (s === 'live' && was !== 'live') void reload();
      },
    });
  }

  function goStale(): void {
    if (stale) return;
    stale = true;
    liveHandle?.stop();
    b.setLive('stale');
  }

  // ---- decisions -------------------------------------------------------------
  function persist(op: () => Promise<void>, rollback: () => void): void {
    inflight++;
    op()
      .catch((err: unknown) => {
        if (err instanceof ApiError && err.isStale) return;
        rollback();
        if (err instanceof ApiError && err.status === 409) {
          flash(
            `The review changed; reloaded. ${err.message}`.trim(),
            'danger',
          );
          dirty = true;
          return;
        }
        render();
        flash(`Not saved: ${(err as Error).message ?? String(err)}`, 'danger');
      })
      .finally(() => {
        inflight--;
        if (inflight === 0 && dirty) void reload();
      });
  }

  /** The next file with no decision after key, else the next file. */
  function nextFile(key: string): string {
    const i = shown.findIndex((e) => e.key === key);
    for (let j = i + 1; j < shown.length; j++) {
      const e = shown[j];
      if (e.kind === 'file' && !e.file.decision) return e.key;
    }
    return shown[i + 1]?.key ?? key;
  }

  /** A file decision: applied to the page (with its linked files), saved
   * with the prints the page shows, and the page moves to the next file. */
  function putFile(f: FileView, d: FileDecision): void {
    const round = review?.round?.id;
    if (!round) return;
    const fs = files();
    const prints = printsFor(fs, f.key);
    const group = f.group
      .map((k) => fileByKey(k))
      .filter((x): x is FileView => !!x);
    const prev = group.map((m) => ({ m, d: m.decision, after: m.after }));
    const pendingNote = pending.get(`f:${f.key}`);
    const note = pendingNote ?? f.decision?.note ?? '';
    pending.delete(`f:${f.key}`);
    const full: FileDecision = { ...d, note };
    decideLocal(fs, f.key, full);
    editing = null;
    if (openKey === `f:${f.key}` && d.action !== 'edit')
      go(frag(nextFile(openKey)));
    else render();
    persist(
      async () =>
        holdPrints(fs, await api.decideFile(round, f.key, full, prints)),
      () => {
        for (const p of prev) {
          p.m.decision = p.d;
          p.m.after = p.after;
        }
        if (pendingNote !== undefined) pending.set(`f:${f.key}`, pendingNote);
      },
    );
  }

  function clearFile(f: FileView): void {
    const round = review?.round?.id;
    if (!round || !f.decision) return;
    const group = f.group
      .map((k) => fileByKey(k))
      .filter((x): x is FileView => !!x);
    const prev = group.map((m) => ({ m, d: m.decision, after: m.after }));
    decideLocal(files(), f.key, null);
    render();
    const fs = files();
    persist(
      async () => holdPrints(fs, await api.clearFile(round, f.key)),
      () => {
        for (const p of prev) {
          p.m.decision = p.d;
          p.m.after = p.after;
        }
      },
    );
  }

  function setFileNote(f: FileView, text: string): void {
    const d = f.decision;
    if (!d) {
      if (text) pending.set(`f:${f.key}`, text);
      else pending.delete(`f:${f.key}`);
      return;
    }
    if ((d.note ?? '') === text) return;
    const round = review?.round?.id;
    if (!round) return;
    const old = d.note;
    d.note = text;
    d.sent = false;
    const fs = files();
    persist(
      async () =>
        holdPrints(
          fs,
          await api.decideFile(
            round,
            f.key,
            { action: d.action, content: d.content, note: text },
            printsFor(fs, f.key),
          ),
        ),
      () => {
        d.note = old;
      },
    );
    refreshBar();
  }

  /** Apply row decisions locally, save them, and move on from a single row. */
  function put(rows: Finding[], make: (r: Finding) => Decision): void {
    const round = review?.round?.id;
    if (!round || !rows.length) return;
    const prev = rows.map((r) => r.decision);
    const notes = rows.map((r) => pending.get(r.id));
    const body: DecisionIn[] = rows.map((r) => {
      const d = make(r);
      const note = pending.get(r.id) ?? r.decision?.note ?? '';
      pending.delete(r.id);
      r.decision = { ...d, note };
      return {
        id: r.id,
        action: d.action,
        verdict: d.verdict,
        title: d.title,
        text: d.text,
        cleared: d.cleared,
        note,
        fingerprint: r.fingerprint,
        target_fingerprint: targetPrint(d),
      };
    });
    const single = rows.length === 1 && openKey === `r:${rows[0].id}`;
    editing = null;
    if (single) go(frag(nextOpen(shown as Entry[], openKey!)));
    else render();
    persist(
      () => api.decide(round, body),
      () =>
        rows.forEach((r, i) => {
          r.decision = prev[i];
          const n = notes[i];
          if (n !== undefined) pending.set(r.id, n);
        }),
    );
  }

  function decide(rows: Finding[], action: 'accept' | 'reject'): void {
    const targets = action === 'accept' ? rows.filter((r) => r.verdict) : rows;
    if (!targets.length) {
      flash(
        'Nothing to accept: the agent proposed nothing here. Edit to give it a verdict.',
      );
      return;
    }
    put(targets, () => ({ action }));
  }

  function saveEdit(
    r: Finding,
    f: Parameters<Ctx['saveEdit']>[1],
  ): string | null {
    const res = editDecision(r, f);
    if (!res.ok) return res.error;
    put([r], () => res.decision);
    return null;
  }

  function clear(r: Finding): void {
    const round = review?.round?.id;
    const prev = r.decision;
    if (!round || !prev) return;
    delete r.decision;
    render();
    persist(
      () => api.clear(round, r.id),
      () => {
        r.decision = prev;
      },
    );
  }

  function setNote(shownRow: Finding, text: string): void {
    const r = rowById(shownRow.id) ?? shownRow;
    const d = r.decision;
    const round = review?.round?.id;
    if (!d || !round) {
      if (text) pending.set(r.id, text);
      else pending.delete(r.id);
      return;
    }
    if ((d.note ?? '') === text) return;
    const old = d.note;
    d.note = text;
    d.sent = false;
    persist(
      () =>
        api.decide(round, [
          {
            id: r.id,
            action: d.action,
            verdict: d.verdict,
            title: d.title,
            text: d.text,
            cleared: d.cleared,
            note: text,
            fingerprint: r.fingerprint,
            target_fingerprint: targetPrint(d),
          },
        ]),
      () => {
        d.note = old;
      },
    );
    refreshBar();
  }

  async function send(): Promise<void> {
    const round = review?.round?.id;
    if (!round) return;
    try {
      const { sent, to } = await api.send(round);
      if (review) {
        markSent(review.rows);
        for (const f of review.files) if (f.decision) f.decision.sent = true;
        if (sent) review.sends++;
      }
      refreshBar();
      flash(
        sent === 0
          ? 'Nothing to send.'
          : to
            ? `Sent ${plural(sent, 'decision')} to ${to}.`
            : `Sent ${plural(sent, 'decision')}. The next agent session that opens sift gets them.`,
      );
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`Not sent: ${(err as Error).message}`, 'danger');
    }
  }

  // ---- keys ------------------------------------------------------------------
  const keys = createKeys({ list: l });
  const group = 'decide';
  const on =
    (
      onFile: (f: FileView) => void,
      onRow: (r: Finding) => void,
      onGroup?: (rows: Finding[]) => void,
    ) =>
    () => {
      const e = current();
      if (!e) return;
      if (e.kind === 'file') onFile(e.file);
      else if (e.kind === 'row') onRow(e.row);
      else onGroup?.(e.group.rows);
    };
  keys.register({
    keys: '1',
    label: 'accept (a group: every undecided item)',
    group,
    run: on(
      (f) => fctx.accept(f),
      (r) => decide([r], 'accept'),
      (rows) => decide(groupTargets(rows, 'accept'), 'accept'),
    ),
  });
  keys.register({
    keys: '2',
    label: 'edit (a file: the whole file)',
    group,
    run: on(
      (f) => fctx.startEdit(f),
      (r) => ctx.startEdit(r),
      (rows) => ctx.open(`r:${rows[0].id}`),
    ),
  });
  keys.register({
    keys: '3',
    label: 'reject (a group: every undecided item)',
    group,
    run: on(
      (f) => fctx.reject(f),
      (r) => decide([r], 'reject'),
      (rows) => decide(groupTargets(rows, 'reject'), 'reject'),
    ),
  });
  keys.register({
    keys: 'u',
    label: 'clear a decision',
    group,
    run: on(
      (f) => clearFile(f),
      (r) => clear(r),
    ),
  });
  keys.register({
    keys: 'n',
    label: 'note',
    group,
    run() {
      document.querySelector<HTMLInputElement>('.kit-note')?.focus();
    },
  });
  keys.register({
    keys: '⌘↵',
    label: 'save the edit',
    group,
    inField: true,
    run() {
      const f = document.querySelector<HTMLFormElement>('.sift-edit');
      if (!f) return false;
      f.requestSubmit();
    },
  });

  // ---- mount -----------------------------------------------------------------
  const app = document.getElementById('app') ?? document.body;
  app.classList.add('kit-app');
  app.append(b.el, l.el, read);
  b.setLive('polling');
  window.addEventListener('hashchange', onHash);
  if (!/^#\/open/.test(location.hash)) history.replaceState(null, '', frag());
  void load();
}

boot();
