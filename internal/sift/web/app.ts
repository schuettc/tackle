// app.ts — the sift review page: the bar, the list (rows in groups), the
// reading column, the route (#/<view>[/<entry>]), the live client and every
// decision.
//
// State lives here; doc.ts only draws. Every decision is applied to the page
// at once and saved as it is given; a failure rolls it back and says so in
// the bar, a 409 reloads.

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
import { groupDoc, message, rowDoc, type Ctx } from './doc.ts';
import {
  checkCount,
  displayPath,
  editDecision,
  entries,
  fixDecision,
  groupTargets,
  groupsOf,
  inView,
  isBacklog,
  isFix,
  markSent,
  nextOpen,
  progress,
  rowMeta,
  rowTitle,
  unsent,
  type Entry,
  type View,
} from './model.ts';

const qs = new URLSearchParams(location.search);
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

function frag(view: View, key?: string | null): string {
  return `#/${view}${key ? `/${encodeURIComponent(key)}` : ''}`;
}

function parseRoute(hash: string): { view: View; key: string | null } {
  const m = /^#\/(open|applied)(?:\/(.+))?$/.exec(hash);
  if (!m) return { view: 'open', key: null };
  let key: string | null = null;
  try {
    key = m[2] ? decodeURIComponent(m[2]) : null;
  } catch {
    key = null;
  }
  return { view: m[1] as View, key };
}

function signature(r: Review): string {
  return (
    `${r.round?.id ?? 0}|${r.sends}|${r.applies.map((a) => a.repo + a.state).join(',')}|` +
    r.rows
      .map((x) => {
        const d = x.decision;
        return `${x.id}:${x.verdict ?? ''}:${x.text ?? ''}:${d ? `${d.action}/${d.verdict ?? ''}/${d.text ?? ''}/${d.title ?? ''}/${d.note ?? ''}/${d.sent ? 1 : 0}` : ''}`;
      })
      .join(';')
  );
}

export function boot(): void {
  // ---- state -----------------------------------------------------------------
  let review: Review | null = null;
  let view: View = 'open';
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
  let shown: Entry[] = [];
  const pending = new Map<string, string>(); // notes waiting for a decision

  // ---- api -------------------------------------------------------------------
  const gated = (input: RequestInfo | URL, init?: RequestInit) =>
    stale
      ? Promise.reject(new Error('sift serve restarted: this tab has stopped'))
      : fetch(input, init);
  const api = client(newApi({ fetch: gated, onStale: () => goStale() }));

  // ---- derived ---------------------------------------------------------------
  const home = () => review?.home ?? '';
  const rowsOf = (v: View) => (review?.rows ?? []).filter((r) => inView(r, v));
  const matches = (r: Finding) => {
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
  const groups = () =>
    groupsOf(
      rowsOf(view).filter(matches),
      isBacklog(review?.round ?? null, review?.rows ?? []),
      home(),
    );
  const rowById = (id: string) => review?.rows.find((r) => r.id === id);
  const current = (): Entry | undefined => shown.find((e) => e.key === openKey);

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
    const kind = r.kind === 'on-demand' ? 'audit' : `${r.kind} audit`;
    const owner = r.owner ? ` · to ${r.owner}` : '';
    return `${kind} · round ${r.id} · ${day}${owner}`;
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
    const p = progress(review?.rows ?? []);
    b.setCount('review', review?.round ? `${p.decided}/${p.total}` : '');
    const n = unsent(review?.rows ?? [], review?.sends ?? 0);
    b.setPrimary(
      n && review?.round
        ? { label: `Send ${n}`, run: () => void send() }
        : null,
    );
    if (!flashing) b.setStatus(baseStatus());
  }

  // ---- list ------------------------------------------------------------------
  const read = h('main', { class: 'kit-read' });
  const l = list<Entry>({
    label: 'findings',
    views: [],
    onChip(group, id) {
      if (group === 'view') {
        editing = null;
        filter = '';
        go(frag(id === 'applied' ? 'applied' : 'open'));
        return;
      }
      filter = filter === id ? '' : id;
      render();
    },
    search: {
      placeholder: 'search files and passages',
      onInput(text) {
        search = text.trim();
        render();
      },
    },
    row(e) {
      if (e.kind === 'group') {
        const g = e.group;
        const done =
          view === 'applied'
            ? g.rows.filter((r) => r.decision?.action !== 'reject').length
            : g.rows.filter((r) => r.decision).length;
        return {
          id: e.key,
          key: g.kicker,
          title: g.title,
          meta:
            done === g.rows.length && view === 'open'
              ? '✓'
              : `${done}/${g.rows.length}`,
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
      go(frag(view, e.key));
    },
  });

  function chips(): void {
    const all = review?.rows ?? [];
    l.setChips('view', [
      {
        id: 'open',
        label: 'needs you',
        count: all.filter((r) => inView(r, 'open')).length,
        on: view === 'open',
      },
      {
        id: 'applied',
        label: 'applied',
        count: all.filter((r) => inView(r, 'applied')).length,
        on: view === 'applied',
      },
    ]);
    const counts = new Map<string, number>();
    for (const r of rowsOf(view))
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

  // Members of the open group are indented under it; the kit's rows carry
  // no class of their own, so mark them after each render.
  function decorate(): void {
    const els = l.el.querySelectorAll<HTMLElement>('.kit-row');
    shown.forEach((e, i) => {
      els[i]?.classList.toggle('sift-group', e.kind === 'group');
      els[i]?.classList.toggle('sift-member', e.kind === 'row' && e.member);
    });
  }

  // ---- ctx -------------------------------------------------------------------
  const ctx: Ctx = {
    get home() {
      return home();
    },
    get view() {
      return view;
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
    undo,
    redo,
    open: (key) => go(frag(view, key)),
    file: (r) => api.file(review?.round?.id ?? 0, r.id),
    appliedIn: (r) =>
      review?.applies.find(
        (a) =>
          a.repo === r.source.repo &&
          (a.state === 'pr' || a.state === 'branch'),
      ),
  };

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
    const gs = groups();
    shown = entries(gs, openKey);
    if (openKey && !shown.some((e) => e.key === openKey)) {
      // The open row left this view or the search: forget it.
      openKey = null;
      shown = entries(gs, null);
    }
    l.setItems(shown);
    decorate();
    const vkey = `${view}/${openKey ?? ''}/${editing ?? ''}`;
    const e = current();
    if (e) {
      const idx = shown.indexOf(e);
      syncing = true;
      l.open(idx);
      syncing = false;
      decorate();
      // A half-typed note or edit survives a re-render (another tab, a reload).
      const oldNote = read.querySelector<HTMLInputElement>('.kit-note');
      const oldEdit = read.querySelector<HTMLFormElement>('.sift-edit');
      const focus = document.activeElement;
      const doc =
        e.kind === 'group' ? groupDoc(ctx, e.group) : rowDoc(ctx, e.row);
      if (
        oldNote &&
        lastView === vkey &&
        (focus === oldNote ||
          (e.kind === 'row' && oldNote.value !== ctx.noteOf(e.row)))
      )
        doc.querySelector('.kit-note')?.replaceWith(oldNote);
      if (oldEdit && lastView === vkey)
        doc.querySelector('.sift-edit')?.replaceWith(oldEdit);
      read.replaceChildren(doc);
      if (focus instanceof HTMLElement && read.contains(focus)) focus.focus();
    } else {
      read.replaceChildren(overview(gs));
    }
    if (vkey !== lastView) read.scrollTop = 0;
    lastView = vkey;
  }

  function overview(gs: ReturnType<typeof groups>): HTMLElement {
    const rows = gs.flatMap((g) => g.rows);
    if (view === 'applied')
      return message(
        'applied',
        rows.length
          ? `${plural(rows.length, 'certain fix')}`
          : 'No certain fixes',
        rows.length
          ? 'sift applies these itself when the round is applied. Open one to read it, and undo any you want left as it is; it holds until you send.'
          : 'This round has nothing sift can fix on its own.',
      );
    const p = progress(review?.rows ?? []);
    if (!rows.length)
      return message(
        'needs you',
        'Nothing here',
        filter || search
          ? 'Nothing matches the filter.'
          : 'This round has nothing to judge.',
      );
    const checks = [...new Set(rows.map((r) => r.check))]
      .map((c) => checkCount(c, rows.filter((r) => r.check === c).length))
      .join(', ');
    return message(
      'needs you',
      `${p.total - p.decided} of ${plural(p.total, 'row')} to decide, in ${plural(gs.length, 'group')}`,
      `${checks}. Open a group (↵) to decide it whole, or a row to decide it alone: 1 accept, 2 edit, 3 reject. Send returns your decisions to the agent.`,
    );
  }

  // ---- routes ----------------------------------------------------------------
  function go(f: string): void {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const r = parseRoute(f);
    if (r.view !== view) {
      filter = '';
      editing = null;
    }
    if (r.key !== openKey) editing = null;
    view = r.view;
    openKey = r.key;
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
      const route = parseRoute(location.hash);
      view = route.view;
      openKey = route.key;
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

  /** Apply decisions locally, save them, and move on from a single row. */
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
      };
    });
    const single = rows.length === 1 && openKey === `r:${rows[0].id}`;
    editing = null;
    if (single) go(frag(view, nextOpen(shown, openKey!)));
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

  // Undo and redo store a decision on a certain fix (reject, accept), sent
  // like any other: a redo is never a deletion.
  function fixOp(rows: Finding[], op: 'undo' | 'redo'): void {
    const round = review?.round?.id;
    const live = rows.filter(
      (r) => isFix(r) && (r.decision?.action === 'reject') === (op === 'redo'),
    );
    if (!round || !live.length) return;
    for (const r of live) {
      const prev = r.decision;
      const note = pending.get(r.id) ?? '';
      pending.delete(r.id);
      r.decision = fixDecision(op, note);
      persist(
        () =>
          op === 'undo' ? api.undo(round, r, note) : api.redo(round, r, note),
        () => {
          if (prev) r.decision = prev;
          else delete r.decision;
        },
      );
    }
    render();
  }

  function undo(rows: Finding[]): void {
    fixOp(rows, 'undo');
  }

  function redo(rows: Finding[]): void {
    fixOp(rows, 'redo');
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
        isFix(r) && d.action === 'reject'
          ? api.undo(round, r, text)
          : isFix(r) && d.action === 'accept'
            ? api.redo(round, r, text)
            : api.decide(round, [
                {
                  id: r.id,
                  action: d.action,
                  verdict: d.verdict,
                  title: d.title,
                  text: d.text,
                  cleared: d.cleared,
                  note: text,
                  fingerprint: r.fingerprint,
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
  const onRow =
    (fn: (r: Finding) => void, onGroup?: (rows: Finding[]) => void) => () => {
      const e = current();
      if (!e) return;
      if (e.kind === 'row') fn(e.row);
      else onGroup?.(e.group.rows);
    };
  keys.register({
    keys: '1',
    label: 'accept (a group: every undecided row)',
    group,
    run: onRow(
      (r) => !isFix(r) && decide([r], 'accept'),
      (rows) =>
        view === 'open' && decide(groupTargets(rows, 'accept'), 'accept'),
    ),
  });
  keys.register({
    keys: '2',
    label: 'edit the proposal',
    group,
    run: onRow(
      (r) => !isFix(r) && ctx.startEdit(r),
      (rows) => ctx.open(`r:${rows[0].id}`),
    ),
  });
  keys.register({
    keys: '3',
    label: 'reject (a group: every undecided row)',
    group,
    run: onRow(
      (r) => !isFix(r) && decide([r], 'reject'),
      (rows) =>
        view === 'open' && decide(groupTargets(rows, 'reject'), 'reject'),
    ),
  });
  keys.register({
    keys: 'u',
    label: 'clear a decision; undo or redo a certain fix',
    group,
    run: onRow((r) => {
      if (!isFix(r)) clear(r);
      else if (r.decision?.action === 'reject') redo([r]);
      else undo([r]);
    }),
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
  if (!/^#\/(open|applied)/.test(location.hash))
    history.replaceState(null, '', frag('open'));
  void load();
}

boot();
