// app.ts — the cull review page: the bar, the list, the reading column, the
// routes (#/p/<project>[/<item id>]), the live client and every answer.
//
// State lives here; overview.ts, item.ts and group.ts only draw. Every answer
// is applied to the page at once and PUT as it is given; a failure rolls that
// change back and says so in the bar, a 409 reloads.

import {
  bar,
  buttons,
  createKeys,
  h,
  initTheme,
  list,
  live,
  type Chip,
  type LiveStatus,
  type StatusTone,
} from '/_kit/kit.js';
import { ApiError, client, newApi, type AnswerIn } from './api.ts';
import { buckets as bucketsOf, bulkTargets } from './bulk.ts';
import { groupItem } from './group.ts';
import { testItem } from './item.ts';
import { actP, concern, lean, strength } from './model.ts';
import { retryDelay } from './retry.ts';
import {
  BLIND_BUCKET,
  GROUP_BUCKETS,
  TEST_BUCKETS,
  overview,
  type BucketWords,
} from './overview.ts';

export interface Ctx {
  blind: boolean;
  rootName: string;
  answerOf(id: string): Answer | undefined;
  noteOf(it: Item): string;
  answer(items: Item[], value: string, via: 'item' | 'group'): void;
  unanswer(it: Item): void;
  setNote(it: Item, text: string): void;
  open(it: Item): void;
  /** Narrow the list to these items and open the first. */
  openSubset(items: Item[]): void;
  back(): void;
}

type Section = 'tests' | 'groups';

const NOTICE_CHANGED = 'That test changed; it was judged again.';

const qs = new URLSearchParams(location.search);
const BLIND = qs.get('blind') === '1';

const frag = (project: number, item?: string) =>
  `#/p/${project}${item ? `/${encodeURIComponent(item)}` : ''}`;

function parseRoute(hash: string): { project: number; item: string } | null {
  const m = /^#\/p\/(\d+)(?:\/(.+))?$/.exec(hash);
  if (!m) return null;
  let item = '';
  try {
    item = m[2] ? decodeURIComponent(m[2]) : '';
  } catch {
    item = '';
  }
  return { project: Number(m[1]), item };
}

const itemSort = (a: Item, b: Item) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
const basename = (p: string) => p.replace(/\/+$/, '').split('/').pop() || p;
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

function signature(r: Review): string {
  return (
    `${r.run?.id ?? 0}|` +
    r.items
      .map(
        (i) =>
          `${i.id}@${i.hash}:${i.answer ? `${i.answer.value}/${i.answer.note}/${i.answer.via}/${i.answer.sent_at ? 1 : 0}` : ''}`,
      )
      .join(';')
  );
}

export function boot(): void {
  // ---- state -----------------------------------------------------------------
  let review: Review | null = null;
  let project = 0;
  let section: Section = 'tests';
  let bucket = 'cut';
  let openId: string | null = null;
  let subset: Set<string> | null = null;
  let notice = '';
  let lastFrag = '';
  let lastView = '';
  let syncing = false;
  let stale = false;
  let inflight = 0;
  let loading = false;
  let dirty = false;
  let liveHandle: ReturnType<typeof live> | null = null;
  let statusTimer: ReturnType<typeof setTimeout> | undefined;
  let prevLive: LiveStatus = 'live';
  let loadAttempt = 0;
  let loadTimer: ReturnType<typeof setTimeout> | undefined;
  const conflicted: (() => void)[] = []; // undo for answers a 409 dropped
  let flashing = false;
  let reloadNote = '';
  const pending = new Map<string, string>(); // notes waiting for an answer

  // ---- api -------------------------------------------------------------------
  const gated = (input: RequestInfo | URL, init?: RequestInit) =>
    stale
      ? Promise.reject(new Error('cull serve restarted: this tab has stopped'))
      : fetch(input, init);
  const api = client(
    newApi({
      fetch: gated,
      onStale() {
        goStale();
      },
    }),
  );

  // ---- derived ---------------------------------------------------------------
  const rootName = () => (review ? basename(review.project.root) : '');
  const sectionKind = (s: Section) => (s === 'tests' ? 'test' : 'group');
  const itemsOf = (s: Section): Item[] =>
    (review?.items ?? []).filter((i) => i.kind === sectionKind(s));
  const answerOf = (id: string) =>
    review?.items.find((i) => i.id === id)?.answer;
  const answerMap = () =>
    new Map(
      (review?.items ?? [])
        .filter((i) => i.answer)
        .map((i) => [i.id, i.answer]),
    );
  const wordsOf = (s: Section): BucketWords[] =>
    BLIND ? [BLIND_BUCKET(s)] : s === 'tests' ? TEST_BUCKETS : GROUP_BUCKETS;
  const bucketIds = (s: Section) => [
    ...wordsOf(s).map((w) => w.id),
    'answered',
  ];

  function bucketsNow(s: Section): Record<string, Item[]> {
    const xs = itemsOf(s);
    const am = answerMap();
    if (BLIND) {
      return {
        open: xs.filter((x) => !am.has(x.id)).sort(itemSort),
        answered: xs.filter((x) => am.has(x.id)).sort(itemSort),
      };
    }
    const b = bucketsOf(xs, am);
    for (const w of wordsOf(s)) b[w.id] ??= [];
    return b;
  }
  const firstNonEmpty = (s: Section): string => {
    const b = bucketsNow(s);
    return wordsOf(s).find((w) => b[w.id].length)?.id ?? wordsOf(s)[0].id;
  };
  const naturalBucket = (it: Item): string =>
    it.answer ? 'answered' : BLIND ? 'open' : lean(it);
  const listItems = (): Item[] => {
    const xs = bucketsNow(section)[bucket] ?? [];
    return subset ? xs.filter((x) => subset!.has(x.id)) : xs;
  };
  const unsent = () =>
    (review?.items ?? []).filter((i) => i.answer && !i.answer.sent_at).length;
  const openCount = (s: Section) => itemsOf(s).filter((i) => !i.answer).length;

  // ---- bar -------------------------------------------------------------------
  const b = bar({
    brand: { name: 'cull' },
    sections: [
      { id: 'tests', label: 'tests', count: 0 },
      { id: 'groups', label: 'groups', count: 0 },
    ],
    active: 'tests',
    onSection(id) {
      section = id === 'groups' ? 'groups' : 'tests';
      bucket = firstNonEmpty(section);
      subset = null;
      openId = null;
      notice = '';
      go(frag(project));
    },
    status: '',
    staleText: 'restarted · continued in a new tab',
  });
  const theme = initTheme('cull', b.themeControl);
  const forced = qs.get('theme');
  if (forced === 'light' || forced === 'dark') theme.set(forced);

  function baseStatus(): string {
    if (!review) return '';
    if (!review.run) return 'no review yet — run cull check';
    const r = review.run;
    const s = r.summary ?? {};
    const mode = r.mode === 'suite' ? 'whole suite' : r.mode;
    return `${basename(review.project.root)} · ${mode} · ${r.total.toLocaleString('en-US')} tests · ${(s.cut ?? 0).toLocaleString('en-US')} cut, ${(s.consolidate ?? 0).toLocaleString('en-US')} merges and ${(s.keep ?? 0).toLocaleString('en-US')} keeps settled without you`;
  }
  // A message in the bar stays for a while; renders leave it alone meanwhile.
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
    if (!stale) b.setSection(section);
    for (const s of ['tests', 'groups'] as const) {
      const n = openCount(s);
      b.setCount(s, n || itemsOf(s).length === 0 ? n : '✓');
    }
    const n = unsent();
    b.setPrimary(
      n
        ? { label: `Send ${plural(n, 'answer')}`, run: () => void send() }
        : null,
    );
  }

  // ---- list ------------------------------------------------------------------
  const read = h('main', { class: 'kit-read' });
  const selCount = h('span', { class: 'kit-n' }, '');
  const footBtns = h('span', { style: 'display:contents' });
  const l = list<Item>({
    label: '',
    views: [],
    onChip(_, id) {
      bucket = id;
      subset = null;
      openId = null;
      notice = '';
      go(frag(project));
    },
    row(x) {
      const a = answerOf(x.id)?.value;
      const meta = (p: number) => a ?? (BLIND ? '·' : p.toFixed(2));
      const file = x.file.replace(/^tests\//, '');
      if (x.kind === 'test') {
        const p = actP(x);
        const key = BLIND
          ? file
          : `${concern(x)?.short ?? 'no clear reason'} · ${file}`;
        const lw = lean(x) === 'review' ? 'undecided' : lean(x);
        return {
          id: x.id,
          key,
          title: x.name,
          meta: meta(p),
          sub: a ? `answered ${a}` : BLIND ? '' : `${strength(p)} ${lw}`,
          selectable: true,
        };
      }
      const p = actP(x);
      return {
        id: x.id,
        key: `${x.state.tests.length} tests · ${file}`,
        title: x.state.tests.map((m) => m.name).join(', '),
        meta: meta(p),
        sub: a
          ? `answered ${a}`
          : BLIND
            ? ''
            : `merge ${p.toFixed(2)} · same behavior ${x.jev.same_behavior.noul.toFixed(2)}`,
        selectable: true,
      };
    },
    onOpen(x) {
      if (syncing) return;
      go(frag(project, x.id));
    },
    onSelect(sel) {
      selCount.textContent = sel.length ? `${sel.length} selected` : '';
      footBtns.replaceChildren(footButtons(sel));
    },
    foot: h(
      'span',
      { style: 'display:contents' },
      selCount,
      h('span', { style: 'margin-left:auto' }),
      footBtns,
    ),
  });

  // The foot never overwrites an answer: in answered it only unanswers; in an
  // open bucket it answers what has no answer yet.
  function footButtons(sel: Item[]): Node | string {
    if (!sel.length) return '';
    if (bucket === 'answered') {
      return buttons([
        {
          label: `unanswer ${sel.length}`,
          run() {
            l.clearSelection();
            sel.forEach(unanswer);
          },
        },
      ]);
    }
    const targets = bulkTargets(sel, answerMap());
    if (!targets.length) return '';
    const [no, yes] =
      section === 'tests' ? ['keep', 'cut'] : ['separate', 'merge'];
    return buttons([
      {
        label: `${no} ${targets.length}`,
        run: () => ctx.answer(bulkTargets(sel, answerMap()), no, 'group'),
      },
      {
        label: `${yes} ${targets.length}`,
        danger: yes === 'cut',
        run: () => ctx.answer(bulkTargets(sel, answerMap()), yes, 'group'),
      },
    ]);
  }

  function chips(): void {
    const bk = bucketsNow(section);
    const out: Chip[] = [
      ...wordsOf(section).map((w) => ({ id: w.id, label: w.label })),
      { id: 'answered', label: 'answered' },
    ]
      .map((w) => ({ ...w, count: bk[w.id].length, on: bucket === w.id }))
      .filter((c) => c.count > 0 || c.on || c.id === 'answered');
    l.setChips('view', out);
  }

  // ---- ctx -------------------------------------------------------------------
  const ctx: Ctx = {
    blind: BLIND,
    get rootName() {
      return rootName();
    },
    answerOf,
    noteOf: (it) => pending.get(it.id) ?? it.answer?.note ?? '',
    answer,
    unanswer,
    setNote,
    open: (it) => go(frag(project, it.id)),
    openSubset(items) {
      if (!items.length) return;
      subset = new Set(items.map((i) => i.id));
      go(frag(project, items[0].id));
    },
    back() {
      go(frag(project));
    },
  };

  // ---- rendering -------------------------------------------------------------
  function message(kick: string, title: string, body: string): HTMLElement {
    return h(
      'div',
      { class: 'kit-doc' },
      h('div', { class: 'kit-kick' }, kick),
      h('h1', { class: 'kit-h1' }, title),
      h('p', { class: 'lead' }, body),
    );
  }

  function render(): void {
    refreshBar();
    if (!review) return;
    if (!flashing) b.setStatus(baseStatus());
    if (!review.run) {
      l.setItems([]);
      chips();
      read.replaceChildren(
        message(
          `cull · ${rootName()}`,
          'No review yet',
          `Run cull check in ${review.project.root}; this page shows what it could not settle.`,
        ),
      );
      return;
    }
    if (!review.items.length) {
      l.setItems([]);
      chips();
      read.replaceChildren(
        message(
          `cull · ${rootName()}`,
          `nothing to review in ${review.project.root}`,
          'Everything in the last run was settled without you.',
        ),
      );
      return;
    }
    // The open item decides the section and bucket; a bucket that no longer
    // holds it (answered, unanswered) follows it.
    const it = openId ? review.items.find((i) => i.id === openId) : undefined;
    if (it) {
      section = it.kind === 'test' ? 'tests' : 'groups';
      if (!(bucketsNow(section)[bucket] ?? []).some((x) => x.id === it.id)) {
        bucket = naturalBucket(it);
        subset = null;
      } else if (subset && !subset.has(it.id)) subset = null;
    } else openId = null;
    if (!bucketIds(section).includes(bucket)) bucket = firstNonEmpty(section);
    refreshBar();
    chips();
    const shown = listItems();
    l.setItems(shown);
    footBtns.replaceChildren(footButtons(l.selected()));
    const view = `${section}/${bucket}/${openId ?? ''}`;
    if (it) {
      const idx = shown.findIndex((x) => x.id === it.id);
      if (idx >= 0) {
        syncing = true;
        l.open(idx);
        syncing = false;
      }
      // A half-typed note survives a re-render (another tab's change).
      const oldNote = read.querySelector<HTMLInputElement>('.kit-note');
      const hadFocus = !!oldNote && document.activeElement === oldNote;
      const keepNote =
        oldNote &&
        lastView === view &&
        (document.activeElement === oldNote ||
          oldNote.value !== ctx.noteOf(it));
      const doc = it.kind === 'test' ? testItem(ctx, it) : groupItem(ctx, it);
      if (keepNote) doc.querySelector('.kit-note')?.replaceWith(oldNote);
      read.replaceChildren(doc);
      if (keepNote && hadFocus) oldNote.focus();
    } else {
      const words = wordsOf(section).find((w) => w.id === bucket) ?? null;
      read.replaceChildren(
        overview(ctx, {
          section,
          words,
          xs: bucketsNow(section)[bucket] ?? [],
          all: itemsOf(section),
          answers: answerMap(),
          answeredOf: (id) => answerOf(id)?.value,
          allOpen: openCount('tests') + openCount('groups'),
          notice,
        }),
      );
    }
    if (view !== lastView) read.scrollTop = 0;
    lastView = view;
  }

  // ---- routes ----------------------------------------------------------------
  function go(f: string): void {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const r = parseRoute(f);
    openId = r?.item || null;
    if (openId) notice = '';
    render();
  }

  function onHash(): void {
    if (location.hash === lastFrag) return;
    lastFrag = location.hash;
    route();
  }

  function route(): void {
    const r = parseRoute(location.hash);
    if (!r) {
      review = null;
      project = 0;
      l.setItems([]);
      refreshBar();
      read.replaceChildren(
        h(
          'div',
          { class: 'kit-doc' },
          h('p', { class: 'lead' }, 'open this page with cull serve <path>'),
        ),
      );
      return;
    }
    if (r.project !== project || !review) {
      project = r.project;
      loadAttempt = 0;
      openId = r.item || null;
      void load();
      return;
    }
    openId = r.item || null;
    if (openId) notice = '';
    render();
  }

  // ---- loading and live ------------------------------------------------------
  async function load(): Promise<void> {
    clearTimeout(loadTimer);
    try {
      const r = await api.review(project);
      loadAttempt = 0;
      review = r;
      const known = openId && r.items.some((i) => i.id === openId);
      if (!known) openId = null;
      section = 'tests';
      bucket = firstNonEmpty('tests');
      if (
        !itemsOf('tests').some((i) => !i.answer) &&
        itemsOf('groups').some((i) => !i.answer)
      ) {
        section = 'groups';
        bucket = firstNonEmpty('groups');
      }
      if (!openId && lastFrag !== frag(project)) {
        lastFrag = frag(project);
        history.replaceState(null, '', frag(project));
      }
      render();
      startLive(r.cursor);
    } catch (err) {
      review = null;
      l.setItems([]);
      refreshBar();
      if (err instanceof ApiError && err.isStale) return;
      const notFound = err instanceof ApiError && err.status === 404;
      if (!notFound) {
        loadTimer = setTimeout(() => void load(), retryDelay(loadAttempt++));
      }
      const msg = notFound
        ? 'cull does not know that project; open this page with cull serve <path>'
        : 'cull serve is not answering; retrying.';
      read.replaceChildren(
        h('div', { class: 'kit-doc' }, h('p', { class: 'lead' }, msg)),
      );
    }
  }

  function applyReview(r: Review): void {
    const was = review;
    if (was && signature(was) === signature(r)) {
      was.cursor = r.cursor;
      return;
    }
    if (openId) {
      const shownHash = was?.items.find((i) => i.id === openId)?.hash;
      if (!r.items.some((i) => i.id === openId && i.hash === shownHash)) {
        openId = null;
        notice = NOTICE_CHANGED;
        lastFrag = frag(project);
        history.replaceState(null, '', lastFrag);
      }
    }
    review = r;
    render();
  }

  async function reload(): Promise<boolean> {
    if (!project) return true;
    if (inflight > 0 || loading) {
      dirty = true;
      return true;
    }
    loading = true;
    try {
      do {
        dirty = false;
        applyReview(await api.review(project));
        conflicted.length = 0;
      } while (dirty && inflight === 0);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.isStale) return false;
      if (conflicted.length) {
        // The answers a 409 dropped cannot be replaced by the server's truth.
        conflicted.splice(0).forEach((u) => u());
        render();
        flash('Could not reload; refresh the page.', 'danger');
      } else {
        flash(`could not reload: ${(err as Error).message}`, 'danger');
      }
      return false;
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
      onEvent(e) {
        const d = (e.data ?? {}) as { project?: number };
        if (e.type === 'reset' || d.project === project) void reload();
      },
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

  // ---- answers ---------------------------------------------------------------
  function persist(op: () => Promise<void>, undo: () => void): void {
    inflight++;
    op()
      .catch((err: unknown) => {
        if (err instanceof ApiError && err.status === 409) {
          // The run moved on under this answer: drop it and reload.
          reloadNote = 'The review changed; reloaded.';
          conflicted.push(undo);
          dirty = true;
          return;
        }
        if (err instanceof ApiError && err.isStale) return;
        undo();
        render();
        flash(`Not saved: ${(err as Error).message ?? String(err)}`, 'danger');
      })
      .finally(() => {
        inflight--;
        if (inflight === 0 && dirty) {
          void reload().then((ok) => {
            if (ok && reloadNote) flash(reloadNote);
            reloadNote = '';
          });
        }
      });
  }

  function answer(items: Item[], value: string, via: 'item' | 'group'): void {
    if (!review?.run || !items.length) return;
    const run = review.run.id;
    const prev = items.map((it) => it.answer);
    const waiting = items.map((it) => pending.get(it.id));
    const viewing = openId;
    const idx = viewing ? listItems().findIndex((x) => x.id === viewing) : -1;
    const body: AnswerIn[] = items.map((it) => {
      const note = pending.get(it.id) ?? it.answer?.note ?? '';
      pending.delete(it.id);
      it.answer = { value, note, via, blind: BLIND };
      return {
        id: it.id,
        hash: it.hash,
        kind: it.kind,
        value,
        note,
        via,
        blind: BLIND,
      };
    });
    if (via === 'group') l.clearSelection();
    let next = viewing;
    if (
      via === 'item' &&
      viewing &&
      items.some((i) => i.id === viewing) &&
      bucket !== 'answered'
    ) {
      const rest = listItems();
      next = rest.length
        ? rest[Math.min(Math.max(idx, 0), rest.length - 1)].id
        : null;
    }
    if (next !== viewing) go(frag(project, next ?? undefined));
    else render();
    const p = project;
    persist(
      () => api.put(p, run, body),
      () =>
        items.forEach((it, i) => {
          it.answer = prev[i];
          const w = waiting[i];
          if (w !== undefined) pending.set(it.id, w);
        }),
    );
  }

  function unanswer(it: Item): void {
    const prev = it.answer;
    if (!prev) return;
    delete it.answer;
    render();
    const p = project;
    persist(
      () => api.del(p, it.id, it.hash),
      () => {
        it.answer = prev;
      },
    );
  }

  function setNote(shown: Item, text: string): void {
    // The field may outlive a re-render: write to the item the page holds now.
    const it = review?.items.find((i) => i.id === shown.id) ?? shown;
    const a = it.answer;
    if (!a || !review?.run) {
      if (text) pending.set(it.id, text);
      else pending.delete(it.id);
      return;
    }
    if (a.note === text) return;
    const old = a.note;
    a.note = text;
    const p = project;
    const run = review.run.id;
    persist(
      () =>
        api.put(p, run, [
          {
            id: it.id,
            hash: it.hash,
            kind: it.kind,
            value: a.value,
            note: text,
            via: a.via as 'item' | 'group',
            blind: a.blind,
          },
        ]),
      () => {
        a.note = old;
      },
    );
  }

  async function send(): Promise<void> {
    try {
      const n = await api.send(project);
      const now = new Date().toISOString();
      for (const it of review?.items ?? [])
        if (it.answer && !it.answer.sent_at) it.answer.sent_at = now;
      refreshBar();
      flash(
        n
          ? `Sent ${plural(n, 'answer')}. The agent is told in a later release; cull check uses them now.`
          : 'Nothing to send.',
      );
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`Not sent: ${(err as Error).message}`, 'danger');
    }
  }

  // ---- keys ------------------------------------------------------------------
  const keys = createKeys({ list: l });
  const current = (): Item | undefined =>
    openId ? review?.items.find((i) => i.id === openId) : undefined;
  const group = 'answer';
  keys.register({
    keys: '1',
    label: 'keep / separate',
    group,
    run() {
      const it = current();
      if (it) answer([it], it.kind === 'test' ? 'keep' : 'separate', 'item');
    },
  });
  keys.register({
    keys: '2',
    label: 'cut / merge',
    group,
    run() {
      const it = current();
      if (it) answer([it], it.kind === 'test' ? 'cut' : 'merge', 'item');
    },
  });
  if (!BLIND) {
    keys.register({
      keys: 'a',
      label: 'accept cull’s recommendation',
      group,
      run() {
        const it = current();
        if (!it) return;
        const lv = lean(it);
        if (lv === 'review') return;
        answer(
          [it],
          it.kind === 'test' ? lv : lv === 'consolidate' ? 'merge' : 'separate',
          'item',
        );
      },
    });
  }
  keys.register({
    keys: 'u',
    label: 'unanswer',
    group,
    run() {
      const it = current();
      if (it) unanswer(it);
    },
  });
  keys.register({
    keys: 'b',
    label: 'back to the overview',
    group,
    run: () => ctx.back(),
  });
  keys.register({
    keys: 'n',
    label: 'note',
    group,
    run() {
      document.querySelector<HTMLInputElement>('.kit-note')?.focus();
    },
  });

  // ---- mount -----------------------------------------------------------------
  const app = document.getElementById('app') ?? document.body;
  app.classList.add('kit-app');
  app.append(b.el, l.el, read);
  b.setLive('polling');
  window.addEventListener('hashchange', onHash);
  lastFrag = location.hash;
  route();
}

boot();
