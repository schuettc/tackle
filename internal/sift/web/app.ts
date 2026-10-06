// app.ts — the sift review page: the bar, the list, the reading column, the
// route (#/open[/<entry>]), the live client and every decision.
//
// An audit round is decided one file at a time, in two sections. To
// change: one list entry per file the recommendation changes (linked files
// together), and the reading column asks which version the file should
// have: 1 current (a reject), 2 recommended (an accept), or the person's
// own (e writes it, an edit; 3 picks it again). Nothing to change: one list
// entry for the files the agent keeps as they are, each agreed with (an
// accept, which mutes its findings) or disagreed with (a reject with a
// note, which goes back to the agent). While the agent is still
// recommending, the page says how far it got and takes no decision. A
// backlog round is decided one item at a time, in groups.
//
// State lives here; doc.ts only draws; decide.ts makes the decisions:
// every decision, clear, note and Send goes through it. A decision shows
// once the server has it, from the snapshot it returns; while it saves, its
// group's decisions (keys and buttons) and Send wait, and while Send is in
// flight everything waits. A 409 reloads the group and says so in the bar.
// An edit form is bound to the item as it was when the form opened (its
// snapshot): it is drawn from it and saved against it, and a 409 reopens
// it on the item as it is now, for the person to redo the edit.

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
import { ApiError, client, newApi } from './api.ts';
import {
  createDecider,
  type FileSnap,
  type RowSnap,
  type Saved,
} from './decide.ts';
import {
  fileDoc,
  groupDoc,
  message,
  noChangeDoc,
  rowDoc,
  type Ctx,
  type FileCtx,
  type NoChangeCtx,
} from './doc.ts';
import {
  fileEntries,
  fileMeta,
  filesProgress,
  noChangeFiles,
  unsentFiles,
  type Choice,
  type FileEntry,
} from './files.ts';
import {
  displayPath,
  editDecision,
  entries,
  groupTargets,
  groupsOf,
  nextOpen,
  progress,
  rowMeta,
  rowTitle,
  unsent,
  verdictOf,
  type Entry,
} from './model.ts';

const qs = new URLSearchParams(location.search);
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

/** The nothing-to-change section: one entry for all its files. */
interface NoChangeEntry {
  kind: 'nochange';
  key: string;
  files: FileView[];
}

type Item = Entry | (FileEntry & { kind: 'file' }) | NoChangeEntry;

const NOCHANGE = 'nochange';

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
  // The open editor's snapshot (a row's or a file's), and which opening of
  // it the form on screen is: a form from another opening is not kept.
  let rowSnap: RowSnap | null = null;
  let fileSnap: FileSnap | null = null;
  let editGen = 0;
  // The file whose disagree form is open (nothing to change).
  let disagreeing: string | null = null;
  // Each file's own version, as last saved or shown: it stays a choice
  // (yours) after another version is picked.
  const mine = new Map<string, string>();
  let filter = '';
  let search = '';
  let lastFrag = '';
  let lastView = '';
  let syncing = false;
  let stale = false;
  let loading = false;
  let dirty = false;
  let flashing = false;
  let liveHandle: ReturnType<typeof live> | null = null;
  let statusTimer: ReturnType<typeof setTimeout> | undefined;
  let prevLive: LiveStatus = 'live';
  let loadTimer: ReturnType<typeof setTimeout> | undefined;
  let shown: Item[] = [];
  // A file's audited content, read on demand (by round, file and base).
  const bases = new Map<string, string | Error>();

  // ---- api -------------------------------------------------------------------
  const gated = (input: RequestInfo | URL, init?: RequestInit) =>
    stale
      ? Promise.reject(new Error('sift serve restarted: this tab has stopped'))
      : fetch(input, init);
  const api = client(newApi({ fetch: gated, onStale: () => goStale() }));
  const decider = createDecider(api, {
    review: () => review,
    replace(r) {
      review = r;
      render();
    },
    changed() {
      // The probe waits on this before a decision key.
      if (decider.idle()) delete app.dataset.saving;
      else app.dataset.saving = '1';
      render();
      if (dirty && decider.idle()) void reload();
    },
    failed: (msg) => flash(msg, 'danger'),
  });

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
    const hit = (f: FileView) =>
      !q || displayPath(f.source, home()).toLowerCase().includes(q);
    const out: Item[] = fileEntries(files(), home())
      .filter((e) => hit(e.file))
      .map((e) => ({ ...e, kind: 'file' as const }));
    const same = noChangeFiles(files(), home()).filter(hit);
    if (same.length) out.push({ kind: 'nochange', key: NOCHANGE, files: same });
    return out;
  };
  const rowById = (id: string) => review?.rows.find((r) => r.id === id);
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
    // While a decision saves, Send waits for it.
    b.setPrimary(
      n && review?.round && !recommending()
        ? decider.idle()
          ? { label: `Send ${n}`, run: () => void send() }
          : { label: decider.sending ? 'sending…' : 'saving…', run: () => {} }
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
      if (e.kind === 'nochange') {
        const n = e.files.length;
        const kept = e.files.reduce((t, f) => t + f.rows.length, 0);
        const agreed = e.files.filter(
          (f) => f.decision?.action === 'accept',
        ).length;
        const disagreed = e.files.filter(
          (f) => f.decision?.action === 'reject',
        ).length;
        return {
          id: e.key,
          key: `nothing to change · ${plural(n, 'file')}`,
          title: `${plural(n, 'file')}, ${plural(kept, 'finding')} kept`,
          meta:
            agreed + disagreed
              ? [
                  agreed ? `${agreed} agreed` : '',
                  disagreed ? `${disagreed} disagreed` : '',
                ]
                  .filter(Boolean)
                  .join(' · ')
              : 'reasons given · not reviewed',
        };
      }
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
      els[i]?.classList.toggle('sift-nochange-row', e.kind === 'nochange');
    });
    // The list's heading counts the files to change; nothing to change
    // heads its own entry.
    const eyebrow = l.el.querySelector('.kit-eyebrow');
    if (eyebrow)
      eyebrow.textContent =
        perItem(review) || !review?.round
          ? 'items'
          : `to change · ${plural(fileEntries(files(), home()).length, 'file')}`;
  }

  // ---- reading column contexts -----------------------------------------------
  const ctx: Ctx = {
    get home() {
      return home();
    },
    get editing() {
      return editing;
    },
    get edited() {
      return rowSnap?.row ?? null;
    },
    noteOf: (r) => decider.noteOf(`r:${r.id}`) ?? r.decision?.note ?? '',
    setNote: (r, text) => decider.noteRow(r.id, text),
    accept: (rows) => decide(rows, 'accept'),
    reject: (rows) => decide(rows, 'reject'),
    startEdit(r) {
      if (ctx.busy([r])) return;
      openEdit(r.id, false);
    },
    cancelEdit() {
      closeEdit();
      render();
    },
    saveEdit,
    clear: (r) => void decider.clearRow(r.id),
    busy: (rows) => decider.busy(rows.map((r) => `r:${r.id}`)),
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
    get edited() {
      return fileSnap?.file ?? null;
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
    yours(f) {
      if (f.decision?.action === 'edit' && f.decision.content !== undefined)
        mine.set(f.key, f.decision.content);
      return mine.get(f.key);
    },
    noteOf: (f) => decider.noteOf(`f:${f.key}`) ?? f.decision?.note ?? '',
    setNote: (f, text) => decider.noteFile(f.key, text),
    pick: (f, c) => pick(f, c),
    startEdit(f) {
      if (fctx.busy(f)) return;
      openEdit(f.key, true);
    },
    cancelEdit() {
      closeEdit();
      render();
    },
    saveEdit(_f, content) {
      // Against the file as the editor opened on it, never a fresher one.
      const snap = fileSnap;
      if (!snap) return 'the editor is closed';
      const f = snap.file;
      if (!content.trim())
        return 'the file is empty: reject it to leave it as it is';
      const was =
        f.decision?.action === 'edit' ? f.decision.content : f.rec?.content;
      if (content === f.rec?.content)
        return 'this is the recommended version: pick it with 2';
      if (content === was) return 'nothing changed';
      if (fctx.busy(f)) return 'the last decision on this file is saving';
      const at = openKey;
      void decider.editFile(snap, content).then((out) => {
        if (out === 'saved') mine.set(f.key, content);
        edited(out, f.key, true, at);
      });
      return null;
    },
    clear: (f) => void decider.clearFile(f.key),
    busy: (f) => decider.busy(f.group.map((k) => `f:${k}`)),
  };

  const nctx: NoChangeCtx = {
    get home() {
      return home();
    },
    rowsOf: (f) => fctx.rowsOf(f),
    get disagreeing() {
      return disagreeing;
    },
    agree(f) {
      if (disagreeing === f.key) disagreeing = null;
      void decider.file(f.key, { action: 'accept' }).then(() => render());
    },
    agreeAll() {
      for (const f of noChangeFiles(files(), home()))
        if (!f.decision && !fctx.busy(f))
          void decider.file(f.key, { action: 'accept' });
    },
    startDisagree(f) {
      if (fctx.busy(f)) return;
      disagreeing = f.key;
      render();
    },
    cancelDisagree() {
      disagreeing = null;
      render();
    },
    disagree(f, note) {
      if (!note.trim())
        return 'say what should change: the agent rewrites the file from your note';
      if (fctx.busy(f)) return 'the last decision on this file is saving';
      void decider
        .file(f.key, { action: 'reject', note: note.trim() })
        .then((ok) => {
          if (ok && disagreeing === f.key) disagreeing = null;
          render();
        });
      return null;
    },
    clear: (f) => void decider.clearFile(f.key),
    busy: (f) => fctx.busy(f),
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
      // A half-typed note, edit or disagreement survives a re-render
      // (another tab, a reload).
      const oldNote = read.querySelector<HTMLInputElement>('.kit-note');
      const oldWhy = read.querySelector<HTMLFormElement>('.sift-disagree');
      const oldEdit = read.querySelector<HTMLFormElement>('.sift-edit');
      const focus = document.activeElement;
      const doc =
        e.kind === 'nochange'
          ? noChangeDoc(nctx, e.files)
          : e.kind === 'file'
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
      // The form kept is this opening's: one the editor reopened on (after
      // a 409) is drawn afresh from its new snapshot.
      doc
        .querySelector<HTMLElement>('.sift-edit')
        ?.setAttribute('data-gen', String(editGen));
      if (
        oldEdit &&
        lastView === vkey &&
        oldEdit.dataset.gen === String(editGen)
      )
        doc.querySelector('.sift-edit')?.replaceWith(oldEdit);
      const why = doc.querySelector<HTMLFormElement>('.sift-disagree');
      if (
        oldWhy &&
        why &&
        lastView === vkey &&
        oldWhy.closest<HTMLElement>('.sift-nc')?.dataset.key ===
          why.closest<HTMLElement>('.sift-nc')?.dataset.key
      )
        why.replaceWith(oldWhy);
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
    const same = noChangeFiles(files(), home()).length;
    return message(
      'needs you',
      `${p.total - p.decided} of ${plural(p.total, 'file')} to decide`,
      `Each file to change has one recommendation covering all its findings. Open one (↵) and pick the version it should have: 1 current, 2 recommended, or e to write your own.${linked ? ' Linked files move text between them and are picked together.' : ''}${same ? ` The agent recommends no change to ${plural(same, 'file')}: agree, or disagree and say why.` : ''} Send returns your decisions to the agent.`,
    );
  }

  // ---- routes ----------------------------------------------------------------
  function go(f: string): void {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const key = parseRoute(f);
    if (key !== openKey) {
      closeEdit();
      disagreeing = null;
    }
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

  // A reload waits while a decision saves, and one a decision overtook
  // (its snapshot landed while the review was read) reads again.
  async function reload(): Promise<void> {
    if (!decider.idle() || loading) {
      dirty = true;
      return;
    }
    loading = true;
    try {
      do {
        dirty = false;
        const epoch = decider.epoch;
        const r = await api.review();
        if (epoch !== decider.epoch || !decider.idle()) {
          dirty = true;
          continue;
        }
        if (review && signature(review) === signature(r)) {
          review.cursor = r.cursor;
          continue;
        }
        review = r;
        render();
      } while (dirty && decider.idle());
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
  /** Opens the editor on a row or file as the page shows it now: the
   * snapshot the form is drawn from and its save binds to. */
  function openEdit(key: string, file: boolean): void {
    rowSnap = file ? null : decider.snapRow(key);
    fileSnap = file ? decider.snapFile(key) : null;
    editing = key;
    editGen++;
    render();
  }

  function closeEdit(): void {
    editing = null;
    rowSnap = null;
    fileSnap = null;
  }

  /** An edit's save, landed: saved closes the editor (a row moves on, as
   * its other decisions do); changed (409) reopens it on the item as it is
   * now; busy or failed leave it open, with what was typed. */
  function edited(
    out: Saved,
    key: string,
    file: boolean,
    at: string | null,
  ): void {
    if (editing !== key) return;
    if (out === 'changed') {
      openEdit(key, file);
      flash(
        'This changed while the editor was open; the editor now shows it as it is. Redo your edit.',
        'danger',
      );
      return;
    }
    if (out !== 'saved') return;
    closeEdit();
    if (!file && at && openKey === at && at === `r:${key}`)
      go(frag(nextOpen(shown as Entry[], at)));
    else render();
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

  /** Picks a file's version, with its linked files: shown once saved;
   * current or recommended then moves to the next file, yours stays.
   * Recommended over the person's own version takes the edit back first
   * (decider.recommend); yours saves the remembered version again, as an
   * edit against the file as shown. */
  function pick(f: FileView, c: Choice): void {
    const at = `f:${f.key}`;
    const was = f.decision;
    if (was?.action === 'edit' && was.content !== undefined)
      mine.set(f.key, was.content);
    let p: Promise<boolean>;
    if (c === 'current') p = decider.file(f.key, { action: 'reject' });
    else if (c === 'recommended') p = decider.recommend(f.key);
    else {
      const content = mine.get(f.key);
      const snap = decider.snapFile(f.key);
      if (content === undefined || !snap || was?.action === 'edit') return;
      p = decider.editFile(snap, content).then((s) => s === 'saved');
    }
    void p.then((ok) => {
      if (!ok) return;
      if (editing === f.key) closeEdit();
      if (openKey === at && c !== 'yours') go(frag(nextFile(at)));
      else render();
    });
  }

  /** Row decisions: shown once saved; a single open row moves on. */
  function put(rows: Finding[], make: (r: Finding) => Decision): void {
    if (!rows.length) return;
    const single = rows.length === 1 && openKey === `r:${rows[0].id}`;
    const at = openKey;
    void decider
      .rows(rows.map((r) => ({ id: r.id, d: make(r) })))
      .then((ok) => {
        if (!ok) return;
        if (rows.some((r) => r.id === editing)) closeEdit();
        if (single && at && openKey === at)
          go(frag(nextOpen(shown as Entry[], at)));
        else render();
      });
  }

  function decide(rows: Finding[], action: 'accept' | 'reject'): void {
    // An edited row's accept keeps the edit, so its verdict counts.
    const targets =
      action === 'accept' ? rows.filter((r) => verdictOf(r)) : rows;
    if (!targets.length) {
      flash(
        'Nothing to accept: the agent proposed nothing here. Edit to give it a verdict.',
      );
      return;
    }
    put(targets, () => ({ action }));
  }

  /** Saves a row's edit: the whole form, against the row as the editor
   * opened on it, never a fresher one shown since. */
  function saveEdit(
    _r: Finding,
    f: Parameters<Ctx['saveEdit']>[1],
  ): string | null {
    const snap = rowSnap;
    if (!snap) return 'the editor is closed';
    const res = editDecision(snap.row, f);
    if (!res.ok) return res.error;
    if (ctx.busy([snap.row])) return 'the last decision on this row is saving';
    const at = openKey;
    void decider
      .editRow(snap, res.decision)
      .then((out) => edited(out, snap.row.id, false, at));
    return null;
  }

  async function send(): Promise<void> {
    try {
      // The decider marks sent what the server says it sent, and only that.
      const out = await decider.send();
      if (!out) return;
      const { sent, to } = out;
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
  // A decision key does nothing while its item's last decision saves, or
  // while Send is in flight.
  const busyNow = (e: Item) =>
    e.kind === 'nochange'
      ? e.files.some((f) => fctx.busy(f))
      : e.kind === 'file'
        ? fctx.busy(e.file)
        : ctx.busy(e.kind === 'row' ? [e.row] : e.group.rows);
  const on =
    (
      onFile: (f: FileView) => void,
      onRow: (r: Finding) => void,
      onGroup?: (rows: Finding[]) => void,
    ) =>
    () => {
      const e = current();
      if (!e || e.kind === 'nochange' || busyNow(e)) return;
      if (e.kind === 'file') onFile(e.file);
      else if (e.kind === 'row') onRow(e.row);
      else onGroup?.(e.group.rows);
    };
  keys.register({
    keys: '1',
    label: 'a file: current · an item: accept (a group: every undecided item)',
    group,
    run: on(
      (f) => fctx.pick(f, 'current'),
      (r) => decide([r], 'accept'),
      (rows) => decide(groupTargets(rows, 'accept'), 'accept'),
    ),
  });
  keys.register({
    keys: '2',
    label: 'a file: recommended · an item: edit',
    group,
    run: on(
      (f) => fctx.pick(f, 'recommended'),
      (r) => ctx.startEdit(r),
      (rows) => ctx.open(`r:${rows[0].id}`),
    ),
  });
  keys.register({
    keys: '3',
    label:
      'a file: yours, once written · an item: reject (a group: every undecided item)',
    group,
    run: on(
      (f) => {
        if (fctx.yours(f) !== undefined) fctx.pick(f, 'yours');
      },
      (r) => decide([r], 'reject'),
      (rows) => decide(groupTargets(rows, 'reject'), 'reject'),
    ),
  });
  keys.register({
    keys: 'e',
    label: 'a file: write your own version (the whole file)',
    group,
    run: on(
      (f) => fctx.startEdit(f),
      () => {},
    ),
  });
  keys.register({
    keys: 'a',
    label: 'nothing to change: agree with all',
    group,
    run() {
      const e = current();
      if (e?.kind === 'nochange') nctx.agreeAll();
    },
  });
  keys.register({
    keys: 'u',
    label: 'clear a decision',
    group,
    run: on(
      (f) => fctx.clear(f),
      (r) => ctx.clear(r),
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
