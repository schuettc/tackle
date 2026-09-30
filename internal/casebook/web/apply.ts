// apply.ts — the To apply section (spec §5): decided items become a plan,
// a plan becomes a job, and a job runs in two lanes while Court answers what
// needs him.
//
// The list (kit list) has three views. running: approved jobs, each row its
// lanes ("casebook · local", "pi · outward"), its title, a thin progress bar
// and n/total (or "queued"), and a signal line when something needs Court
// ("1 needs you · 1 paused"). ready: the plans serve built and Court hasn't
// approved, then the decided items waiting to be applied (selectable; the
// foot plans the selection, or all of them; an item already in a job that
// hasn't finished says "in plan #N" or "in job #N" and isn't offered again:
// serve plans an item into one unfinished job at most). done: finished jobs. The foot also
// says how old serve's observations are ("observations fresh", or "… 34m
// old" past one sync interval, when a plan would be refused).
//
// #/apply/<job> opens a job. A plan (a planned job) shows its steps grouped
// by action with the exact command serve will run for each (serve's own
// command string, never composed here), approve (with a session picker
// when the plan has agent-lane steps: a session is chosen for Court only
// when it is the only one here or the one he picked in the dock; otherwise
// he picks, and approving without one is impossible) and discard. A plan
// serve refuses as stale shows serve's words and "sync first": serve syncs
// (POST /api/sync), and when the live wire says the sync is done the plan is
// asked for again. A running or finished job shows its facts, the needs-you
// cards (rust edge; a text card's post and close · edit text · close
// without comment · skip, a batch's confirm, a failed step's hand to the
// agent, the agent's paused step), and the steps table (✓ verified,
// ‖ paused, · next), with undo on a finished step serve says it can undo.
//
// The bar's primary follows the job: Pause job while it runs, Resume while
// it is paused, Approve on a plan (once it can be), Plan N / Plan all on the
// ready list, Sync first after a stale refusal. Live job, step and
// needs_you events mark the job they name; one reload runs at a time (at
// most one more queued) and fetches only those jobs. A redraw keeps what
// Court was doing: an open "edit text" field's text, the focused control
// and the caret. A reload never draws over a newer reply (an older one is
// dropped). While To apply is the active section it feeds the composer
// {job: <id>} (with the job's title for the line) for the open job and {}
// otherwise; hidden, it never touches the attached line or the bar's
// primary.

import {
  ApiError,
  list,
  h,
  facts,
  buttons,
  card,
  type Button,
  type Chip,
  type KeyBinding,
  type ListHandle,
  type Primary,
} from '/_kit/kit.js';
import type {
  AnswerResult,
  ItemView,
  ItemsView,
  Job,
  JobStep,
  JobView,
  JobsView,
  NeedsYou,
  NeedsYouView,
  PlanView,
  Session,
  SessionsView,
  Step,
  SummaryView,
  SyncEvent,
  SyncView,
  UndoResult,
} from './wire.d.ts';
import type { Ctx, Section } from './app.ts';
import { keyWithoutKind, pluralize } from './decide-math.ts';
import { DANGER_DISPS } from './decide.ts';
import {
  actionTitle,
  actionVerb,
  groupSteps,
  jobLanes,
  jobTitle,
  laneLabel,
  makeSeq,
  needsLine,
  needsSession,
  nextSteps,
  observations,
  openCardsByJob,
  outwardGo,
  plannableCount,
  plannedKeys,
  heldLabel,
  heldWhere,
  planSummary,
  progress,
  stepMark,
  viewOf,
  type ApplyView,
  type StepGroup,
} from './apply-text.ts';
import { fmtAge } from './time-utils.ts';

// Decided items are fetched 200 at a time (serve's cap, like Attention).
const PAGE = 200;
// The steps table shows this many rows at a time.
const STEPS_PAGE = 200;

const VIEWS: Array<{ id: ApplyView; label: string }> = [
  { id: 'running', label: 'running' },
  { id: 'ready', label: 'ready' },
  { id: 'done', label: 'done' },
];

type Row =
  | { t: 'job'; job: Job }
  | { t: 'item'; item: ItemView }
  | { t: 'more'; left: number };

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// "built 4m ago", "built just now".
function builtAgo(ts: string): string {
  const a = fmtAge(ts);
  return a === 'now' ? 'just now' : `${a} ago`;
}

// A destructive verb reads in danger text (delete, close, archive).
const dangerVerb = (verb: string) =>
  DANGER_DISPS.has(verb) || verb === 'remove';

// ---- the section ------------------------------------------------------------

interface Planner {
  planned(v: PlanView): void;
  refused(msg: string): void;
  /** serve had nothing to plan (422): another tab planned it first. */
  nothing(msg: string): void;
}
let planner: Planner | null = null;

/**
 * buildPlan asks serve for a plan of the given decided items (or all of
 * them). A built plan opens as #/apply/<job>; serve's refusal (409: the
 * observation is older than one sync interval) is shown in its own words,
 * with "sync first".
 */
export function buildPlan(ctx: Ctx, what: string[] | 'all'): Promise<void> {
  const body = what === 'all' ? { all: true } : { keys: what };
  return ctx.api
    .post<PlanView>('/apply/plan', body)
    .then((v) => planner?.planned(v))
    .catch((err: unknown) =>
      err instanceof ApiError && err.status === 422
        ? planner?.nothing(message(err))
        : planner?.refused(message(err)),
    );
}

// ---- keeping focus across a redraw ------------------------------------------
//
// A live event redraws the open job. What Court was doing there survives it:
// the focused control is found again in the new drawing by what it is (its
// card, step or session, its kind, its label), and a text field keeps its
// caret. (An edit's text itself is kept by the section, per card.)

interface FocusSnap {
  key: string;
  start: number | null;
  end: number | null;
}

function focusKey(el: Element): string {
  const host = el.closest(
    '[data-card],[data-step],[data-session],[data-testid]',
  );
  const hostKey = host
    ? ['card', 'step', 'session', 'testid']
        .map((a) => host.getAttribute(`data-${a}`) ?? '')
        .join('/')
    : '';
  const own =
    el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement
      ? el.className
      : (el.textContent ?? '');
  return `${hostKey}|${el.tagName}|${own}`;
}

function focusSnapshot(root: HTMLElement): FocusSnap | null {
  const a = document.activeElement;
  if (!a || a === document.body || !root.contains(a)) return null;
  const field =
    a instanceof HTMLTextAreaElement || a instanceof HTMLInputElement
      ? a
      : null;
  return {
    key: focusKey(a),
    start: field ? field.selectionStart : null,
    end: field ? field.selectionEnd : null,
  };
}

function focusRestore(root: HTMLElement, snap: FocusSnap | null): void {
  if (!snap) return;
  const els = root.querySelectorAll<HTMLElement>(
    'button, textarea, input, a[href], [tabindex]',
  );
  for (const el of els) {
    if (focusKey(el) !== snap.key) continue;
    el.focus({ preventScroll: true });
    if (
      (el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement) &&
      snap.start !== null
    ) {
      el.setSelectionRange(snap.start, snap.end ?? snap.start);
    }
    return;
  }
}

export function makeApply(ctx: Ctx): Section {
  const readEl = h('div', { class: 'kit-read cb-apply-read' });
  let jobs: Job[] = [];
  let cards: NeedsYou[] = [];
  let sessions: Session[] = [];
  let items: ItemView[] = [];
  let itemsTotal = 0;
  let machine = '';
  let view: ApplyView = 'running';
  let active = false;
  let openId: number | null = null;
  let open: JobView | null = null;
  // serve's groups for the plan it just built (PlanView.groups), by job.
  let planGroups: { job: number; groups: StepGroup<Step>[] } | null = null;
  // The session Court picked for a plan's outward steps ('' until he does).
  let courtSession = '';
  let stepsShown = STEPS_PAGE;
  let refusal = '';
  // serve's words when a plan found nothing to plan (422): shown while
  // nothing is plannable, no "sync first" (a sync wouldn't help).
  let nothingNote = '';
  let note = '';
  let busy = false;
  let planning = false;
  let painting = false;
  // serve's observation age: when the index was built, one sync interval,
  // and whether a sync (POST /api/sync) is running.
  let builtAt = 0;
  let intervalMs = 0;
  let syncing = false;
  let syncError = '';
  // What a refused plan asked for, built again once a sync Court asked for
  // here (sync first, in this tab) is done.
  let afterSync: string[] | 'all' | null = null;
  // An open "edit text" field's text, by card id: a redraw keeps it.
  const editors = new Map<number, string>();
  const itemsSeq = makeSeq();
  const listSeq = makeSeq();
  const jobSeq = makeSeq();

  const present = () => sessions.filter((s) => !s.left);
  // The session a plan's outward steps go to: Court's pick while it is here;
  // else the one he picked in the dock, if it is here; else the only session
  // here. With two or more here and none picked, none: Court chooses.
  const planSession = (): string => {
    const ps = present();
    const here = (id: string) => !!id && ps.some((s) => s.id === id);
    if (here(courtSession)) return courtSession;
    if (here(ctx.dockSession())) return ctx.dockSession();
    return ps.length === 1 ? ps[0].id : '';
  };
  // The agent a job's outward steps go to: its session's, or for a plan,
  // the session chosen for it.
  const agentOf = (job: Job): string => {
    const id =
      job.state === 'planned' && !job.session ? planSession() : job.session;
    const s = sessions.find((x) => x.id === id);
    return s?.harness || ctx.agentName() || 'agent';
  };

  // ---- the list ----

  const footCount = h('span', { class: 'cb-apply-foot-n' });
  const footBtns = h('span', { class: 'cb-apply-foot-btns' });
  const footSel = h('div', { class: 'cb-apply-foot-sel' }, footCount, footBtns);
  const obsText = h('span', { 'data-testid': 'observations' });
  const obsEl = h(
    'div',
    { class: 'cb-apply-obs' },
    h('i', { class: 'cb-apply-obs-dot', 'aria-hidden': 'true' }),
    obsText,
  );
  const foot = h('div', { class: 'cb-apply-foot' }, footSel, obsEl);

  // In a job that hasn't finished (a plan, or a job approved, running or
  // paused): key → that job.
  const inPlan = () => plannedKeys(jobs);
  const plannable = () =>
    plannableCount(
      itemsTotal,
      items.map((i) => i.key),
      inPlan(),
      itemsTotal <= items.length,
    );

  const handle: ListHandle<Row> = list<Row>({
    label: 'to apply',
    views: VIEWS.map((v) => ({ ...v, on: v.id === view })),
    openOnMove: false,
    row(r: Row) {
      if (r.t === 'more') {
        return {
          id: 'more',
          title: `\u2026 ${pluralize(r.left, 'more decided item')}`,
          meta: 'show more',
        };
      }
      if (r.t === 'item') {
        const it = r.item;
        const plan = inPlan().get(it.key);
        return {
          id: it.key,
          key: `${it.kind} \u00b7 ${keyWithoutKind(it.key)}`,
          title: it.title ?? keyWithoutKind(it.key),
          meta: plan ? heldLabel(plan) : (it.decision?.disposition ?? ''),
          selectable: !plan,
        };
      }
      const j = r.job;
      const p = progress(j);
      const lanes = jobLanes(j)
        .map((l) => laneLabel(l, agentOf(j)))
        .join(' + ');
      const planned = j.state === 'planned';
      return {
        id: `job:${j.id}`,
        key: planned ? `plan \u00b7 job #${j.id}` : lanes || `job #${j.id}`,
        title: jobTitle(j),
        meta: planned
          ? pluralize(p.total, 'step')
          : p.started || viewOf(j) === 'done'
            ? `${p.finished}/${p.total}`
            : 'queued',
        sub: needsLine(cardsFor(j.id), p.paused) || undefined,
      };
    },
    onChip(group, id) {
      if (group !== 'view') return;
      view = id as ApplyView;
      paintList();
      feed();
    },
    onOpen(r: Row, i: number) {
      if (painting) return;
      if (r.t === 'job') ctx.route.go('apply', String(r.job.id));
      else if (r.t === 'item') {
        // An item in a job opens that job; any other is selected.
        const plan = inPlan().get(r.item.key);
        if (plan) ctx.route.go('apply', String(plan.id));
        else handle.toggle(i);
      } else void loadItems(items.length);
    },
    onSelect() {
      paintFoot();
      if (active) ctx.setPrimary(primary());
      if (openId === null) drawOverview();
    },
    foot,
  });
  handle.el.classList.add('cb-apply-list');

  // What needs Court (a paused step is counted as paused, not twice).
  const cardsFor = (jobId: number) =>
    openCardsByJob(cards.filter((c) => c.kind !== 'paused')).get(jobId) ?? 0;
  const selectedKeys = () => {
    const planned = inPlan();
    return handle
      .selectedIds()
      .filter((k) => !k.startsWith('job:') && k !== 'more' && !planned.has(k));
  };
  // A discarded plan is gone: it is listed nowhere.
  const listed = () => jobs.filter((j) => j.state !== 'cancelled');

  function rowsFor(v: ApplyView): Row[] {
    const js = listed()
      .filter((j) => viewOf(j) === v)
      .sort((a, b) => b.id - a.id)
      .map((job): Row => ({ t: 'job', job }));
    if (v !== 'ready') return js;
    const its = items.map((item): Row => ({ t: 'item', item }));
    const more: Row[] =
      itemsTotal > items.length
        ? [{ t: 'more', left: itemsTotal - items.length }]
        : [];
    return [...js, ...its, ...more];
  }

  function counts(): Record<ApplyView, number> {
    const c = { running: 0, ready: itemsTotal, done: 0 };
    for (const j of listed()) c[viewOf(j)]++;
    return c;
  }

  function paintList(): void {
    const c = counts();
    handle.setChips(
      'view',
      VIEWS.map((v): Chip => ({
        ...v,
        on: v.id === view,
        count: c[v.id] || undefined,
      })),
    );
    const shown = rowsFor(view);
    handle.setItems(shown);
    const planned = inPlan();
    const els = handle.el.querySelectorAll<HTMLElement>('.kit-row');
    els.forEach((rowEl, i) => {
      const r = shown[i];
      if (!r) return;
      if (r.t !== 'job') {
        if (r.t === 'item') {
          rowEl.dataset.key = r.item.key;
          const plan = planned.get(r.item.key);
          if (plan) {
            rowEl.dataset.plan = String(plan.id);
            rowEl.classList.add('cb-in-plan');
          }
        } else rowEl.classList.add('cb-apply-more');
        return;
      }
      decorateJobRow(rowEl, r.job);
    });
    const openAt = shown.findIndex((r) => r.t === 'job' && r.job.id === openId);
    if (openAt >= 0) {
      painting = true;
      try {
        handle.open(openAt);
      } finally {
        painting = false;
      }
    }
    paintFoot();
  }

  // A job row: its lanes in their colours, the progress bar under the
  // title (one segment per lane), the signal line.
  function decorateJobRow(rowEl: HTMLElement, j: Job): void {
    rowEl.dataset.job = String(j.id);
    rowEl.classList.add('cb-job-row');
    const p = progress(j);
    const kicker = rowEl.querySelector<HTMLElement>('.kit-kicker');
    if (kicker && j.state !== 'planned') {
      const lanes = jobLanes(j);
      if (lanes.length) {
        kicker.replaceChildren(
          ...lanes.flatMap((l, i) => [
            i ? ' + ' : '',
            h(
              'span',
              { class: `cb-lanename cb-lanename-${l}`, 'data-lane': l },
              laneLabel(l, agentOf(j)),
            ),
          ]),
        );
      }
    } else kicker?.classList.add('cb-plan-kicker');
    const meta = rowEl.querySelector<HTMLElement>('.kit-meta');
    if (meta?.textContent === 'queued') meta.classList.add('cb-queued');
    rowEl.querySelector('.kit-sub')?.classList.add('cb-needs-line');
    if (j.state !== 'planned' && (p.started || viewOf(j) === 'done')) {
      const bar = h('div', {
        class: 'cb-pb',
        role: 'progressbar',
        'aria-valuemin': '0',
        'aria-valuemax': String(p.total),
        'aria-valuenow': String(p.finished),
      });
      for (const l of jobLanes(j)) {
        const n = p.byLane[l] ?? 0;
        if (!n) continue;
        bar.append(
          h('span', {
            class: `cb-pb-${l}`,
            style: `width:${(100 * n) / Math.max(1, p.total)}%`,
          }),
        );
      }
      rowEl.querySelector('.kit-title')?.after(bar);
    }
  }

  // The foot: on ready, the selection and what it plans; always, how old the
  // observations are that a plan would be built from.
  function paintFoot(): void {
    paintObs();
    if (view !== 'ready' || !itemsTotal) {
      footCount.textContent = '';
      footBtns.replaceChildren();
      footSel.hidden = true;
      return;
    }
    footSel.hidden = false;
    const n = selectedKeys().length;
    const can = plannable();
    const held = itemsTotal - can;
    footCount.textContent = n
      ? `${n} selected`
      : can
        ? pluralize(can, 'decided item')
        : `${pluralize(held, 'decided item')} ${heldWhere(inPlan())}`;
    // Signal is the selection's colour; a plain total isn't.
    footCount.classList.toggle('cb-sel', n > 0);
    // While a refusal stands its card is the offer (sync first): planning
    // again would be refused again, so the foot offers no plan.
    const bs: Button[] = [];
    if (n && !refusal)
      bs.push({ label: `plan ${n}`, run: () => plan(selectedKeys()) });
    if (can && !refusal)
      bs.push({ label: `plan all ${can}`, run: () => plan('all') });
    footBtns.replaceChildren(bs.length ? buttons(bs) : '');
  }

  function paintObs(): void {
    const o = builtAt
      ? observations(builtAt, intervalMs, Date.now())
      : { text: '', stale: false };
    obsText.textContent = syncing ? 'syncing observations\u2026' : o.text;
    obsEl.dataset.state = syncing ? 'syncing' : o.stale ? 'stale' : 'fresh';
    obsEl.hidden = !builtAt && !syncing;
  }
  // The age moves on its own: the foot says so without an event.
  const obsTimer = setInterval(paintObs, 1000);
  void obsTimer;

  // ---- loading ----
  //
  // Live events don't each fetch: they mark what changed, and one reload
  // runs at a time (after a short window that gathers a burst), with at most
  // one more queued behind it that takes everything marked meanwhile. An
  // event about a job fetches that job (GET /api/job, which carries its open
  // cards), never the whole list.

  const dirty = {
    all: false,
    items: false,
    sessions: false,
    summary: false,
    jobs: new Set<number>(),
  };
  let flushing = false;
  let queued = false;

  function mark(f: (d: typeof dirty) => void): void {
    f(dirty);
    if (flushing) {
      queued = true;
      return;
    }
    flushing = true;
    // A short window gathers the events of a burst into the first reload.
    setTimeout(() => void flush(), 40);
  }

  async function flush(): Promise<void> {
    try {
      do {
        queued = false;
        const d = {
          all: dirty.all,
          items: dirty.items,
          sessions: dirty.sessions,
          summary: dirty.summary,
          jobs: [...dirty.jobs],
        };
        dirty.all = dirty.items = dirty.sessions = dirty.summary = false;
        dirty.jobs.clear();
        // An event that names no job reloads the list, and the open job.
        const some = d.all && openId !== null ? [openId] : d.all ? [] : d.jobs;
        await Promise.all([
          d.all ? loadJobs() : null,
          some.length ? loadSome(some) : null,
          d.items ? loadItems() : null,
          d.sessions ? loadSessions() : null,
          d.summary ? loadSummary() : null,
        ]);
      } while (queued);
    } finally {
      flushing = false;
    }
  }

  // patchJob puts a job serve sent into the list, with its open cards.
  function patchJob(v: JobView): void {
    const i = jobs.findIndex((j) => j.id === v.job.id);
    if (i >= 0) jobs[i] = v.job;
    else jobs = [...jobs, v.job];
    cards = [
      ...cards.filter((c) => c.job_id !== v.job.id),
      ...(v.needs_you ?? []).filter((c) => c.state === 'open'),
    ];
  }

  async function loadJobs(): Promise<void> {
    const mine = listSeq.next();
    try {
      const [j, c] = await Promise.all([
        ctx.api.get<JobsView>('/jobs'),
        ctx.api.get<NeedsYouView>('/needs-you'),
      ]);
      if (!listSeq.isLatest(mine)) return;
      jobs = j.jobs ?? [];
      cards = c.cards ?? [];
      // The open job is drawn from its own, sequenced replies.
      if (open) patchJob(open);
      paintList();
    } catch {
      // non-fatal: the list stays as it was
    }
  }

  // loadSome fetches the jobs live events were about.
  async function loadSome(ids: number[]): Promise<void> {
    const openMine =
      openId !== null && ids.includes(openId) ? jobSeq.next() : 0;
    const at = openId;
    const got = await Promise.all(
      ids.map((id) =>
        ctx.api.get<JobView>('/job', { id: String(id) }).catch(() => null),
      ),
    );
    let drawn: JobView | null = null;
    got.forEach((v, i) => {
      if (!v) return;
      if (ids[i] === at) {
        // A newer reply (Court's own action) drew it: this one is older.
        if (!jobSeq.isLatest(openMine) || openId !== at) return;
        drawn = v;
      }
      patchJob(v);
    });
    if (drawn) setOpen(drawn);
    else paintList();
  }

  async function loadItems(offset = 0): Promise<void> {
    const mine = itemsSeq.next();
    try {
      const v = await ctx.api.get<ItemsView>('/items', {
        view: 'to-apply',
        limit: String(PAGE),
        offset: String(offset),
      });
      if (!itemsSeq.isLatest(mine)) return;
      const got = v.items ?? [];
      items = offset ? [...items, ...got] : got;
      itemsTotal = v.total;
      // A decided item that left the list (applied, or decided again) leaves
      // the selection too.
      const keep = new Set(items.map((i) => i.key));
      const gone = selectedKeys().filter((k) => !keep.has(k));
      if (gone.length && offset === 0 && itemsTotal <= items.length) {
        handle.deselect(gone);
      }
      paintList();
      if (active) ctx.setPrimary(primary());
      if (openId === null) drawOverview();
    } catch {
      // non-fatal
    }
  }

  async function loadSessions(): Promise<void> {
    try {
      const v = await ctx.api.get<SessionsView>('/sessions');
      sessions = v.sessions ?? [];
      paintList();
      if (open?.job.state === 'planned') drawOpen();
      if (active) ctx.setPrimary(primary());
    } catch {
      // non-fatal
    }
  }

  async function loadSummary(): Promise<void> {
    try {
      const s = await ctx.api.get<SummaryView>('/summary');
      machine = s.machine;
      builtAt = Date.parse(s.built_at) || 0;
      intervalMs = s.sync_interval_ms;
      syncing = s.syncing;
      paintObs();
      if (openId === null) drawOverview();
    } catch {
      // non-fatal
    }
  }

  // loadJob loads the job a route opened; only the newest request may draw.
  async function loadJob(id: number): Promise<void> {
    const mine = jobSeq.next();
    try {
      const v = await ctx.api.get<JobView>('/job', { id: String(id) });
      if (!jobSeq.isLatest(mine) || openId !== id) return;
      setOpen(v);
    } catch (err) {
      if (!jobSeq.isLatest(mine) || openId !== id) return;
      open = null;
      readEl.replaceChildren(
        h(
          'div',
          { class: 'cb-read-empty' },
          h('p', { class: 'cb-read-empty-section kit-label' }, 'to apply'),
          h('p', { class: 'cb-read-empty-prompt' }, message(err)),
        ),
      );
      feed();
    }
  }

  // setOpen draws a job serve sent (a reload, or a reply to Court's own
  // action, which is sequenced like a reload).
  function setOpen(v: JobView): void {
    const first = open?.job.id !== v.job.id;
    open = v;
    // The job is listed under its view: a job opened by URL shows its chip.
    if (v.job.state !== 'cancelled') view = viewOf(v.job);
    patchJob(v);
    // An edit whose card has closed has nothing left to edit.
    for (const id of [...editors.keys()]) {
      if (!(v.needs_you ?? []).some((c) => c.id === id && c.state === 'open'))
        editors.delete(id);
    }
    if (first) stepsShown = STEPS_PAGE;
    paintList();
    drawOpen();
    if (first) readEl.scrollTop = 0;
    feed();
  }

  // act posts one of Court's actions on the open job; a JobView reply is
  // drawn in order with the reloads. ok runs once serve has taken it, before
  // the job is drawn again.
  async function act<T>(
    path: string,
    body: unknown,
    ok?: () => void,
  ): Promise<void> {
    if (busy) return;
    busy = true;
    note = '';
    const mine = jobSeq.next();
    try {
      const r = await ctx.api.post<T>(path, body);
      ok?.();
      const jv = r as unknown as JobView;
      if (jv && typeof jv === 'object' && 'job' in jv && jv.job) {
        if (jobSeq.isLatest(mine) && openId === jv.job.id) setOpen(jv);
        else patchJob(jv);
      } else if (openId !== null) {
        const id = openId;
        mark((d) => d.jobs.add(id));
      }
    } catch (err) {
      note = message(err);
      drawOpen();
    } finally {
      busy = false;
    }
  }

  // ---- planning ----

  let asked: string[] | 'all' = [];
  // plan asks serve once: a second press while it is asking does nothing.
  function plan(what: string[] | 'all'): void {
    if (planning) return;
    planning = true;
    refusal = '';
    nothingNote = '';
    asked = what;
    void buildPlan(ctx, what).finally(() => {
      planning = false;
    });
  }

  planner = {
    planned(v: PlanView) {
      refusal = '';
      afterSync = null;
      syncError = '';
      // What was planned leaves the selection.
      if (asked === 'all') handle.clearSelection();
      else handle.deselect(asked);
      openId = v.job.id;
      planGroups = {
        job: v.job.id,
        groups: (v.groups ?? []).map((g) => ({
          action: g.action,
          lane: g.lane,
          steps: g.steps ?? [],
        })),
      };
      setOpen({ job: v.job, needs_you: [] });
      ctx.route.go('apply', String(v.job.id));
    },
    nothing(msg: string) {
      // What this tab offered was planned elsewhere: say so in serve's
      // words, and learn what is in a plan now (so "plan all" goes).
      nothingNote = msg;
      if (openId === null) drawOverview();
      void loadJobs().then(() => {
        if (openId === null) drawOverview();
        if (active) ctx.setPrimary(primary());
      });
      void loadItems();
    },
    refused(msg: string) {
      refusal = msg;
      if (openId === null) {
        drawOverview();
        paintFoot();
        if (active) ctx.setPrimary(primary());
      } else ctx.route.go('apply');
    },
  };

  // syncFirst asks serve to sync (one sync, however often it is asked); the
  // live wire says when it is done, and the refused plan is built again.
  async function syncFirst(): Promise<void> {
    syncError = '';
    // Armed here, in the tab where Court asked, before serve is asked (its
    // done can be heard before its reply): another tab's sync re-plans
    // nothing here.
    afterSync = asked;
    try {
      const v = await ctx.api.post<SyncView>('/sync', {});
      syncing = v.running;
    } catch (err) {
      afterSync = null;
      syncError = message(err);
    }
    paintObs();
    if (openId === null) drawOverview();
    if (active) ctx.setPrimary(primary());
  }

  function onSync(e: SyncEvent): void {
    if (e.state === 'running') {
      syncing = true;
      syncError = '';
    } else {
      syncing = false;
      syncError = e.state === 'failed' ? (e.error ?? 'failed') : '';
      mark((d) => (d.summary = true));
      if (e.state === 'done' && afterSync !== null) {
        const what = afterSync;
        afterSync = null;
        refusal = '';
        plan(what);
      } else if (e.state === 'done' && refusal) {
        // Another tab's sync made the observations fresh: the refusal no
        // longer holds, and nothing is planned here unasked.
        refusal = '';
        paintFoot();
      }
    }
    paintObs();
    if (openId === null) drawOverview();
    if (active) ctx.setPrimary(primary());
  }

  // ---- the reading column ----

  function refusalCard(): HTMLElement {
    const body = h(
      'div',
      null,
      h('p', { 'data-testid': 'plan-refused' }, refusal),
    );
    if (syncing) {
      body.append(
        h(
          'p',
          { class: 'cb-apply-note', role: 'status', 'data-testid': 'syncing' },
          'syncing\u2026 the plan is built again when the sync is done',
        ),
      );
    } else if (syncError) {
      body.append(
        h(
          'p',
          {
            class: 'cb-apply-note',
            role: 'status',
            'data-testid': 'sync-failed',
          },
          `not synced: ${syncError}`,
        ),
      );
    }
    const el = card({
      edge: 'signal',
      head: 'not planned',
      body,
      actions: syncing
        ? undefined
        : [{ label: 'sync first', fill: true, run: () => void syncFirst() }],
    });
    el.dataset.testid = 'refusal';
    return el;
  }

  function drawOverview(): void {
    if (openId !== null) return;
    const n = selectedKeys().length;
    const can = plannable();
    const doc = h(
      'article',
      { class: 'cb-apply-doc', 'data-testid': 'apply-overview' },
      h(
        'p',
        { class: 'kit-kick' },
        `to apply${machine ? ` \u00b7 ${machine}` : ''}`,
      ),
      h(
        'h1',
        { class: 'kit-h1' },
        itemsTotal
          ? `${pluralize(itemsTotal, 'decided item')} to apply`
          : 'Nothing decided is waiting to be applied',
      ),
      facts([
        ['running', String(counts().running)],
        ['plans', String(jobs.filter((j) => j.state === 'planned').length)],
        [heldWhere(inPlan()), String(itemsTotal - can)],
        ['selected', String(n)],
      ]),
      h(
        'p',
        null,
        'A plan shows the exact command for every step before anything runs. This machine applies what is on it; GitHub steps run once, from whichever machine applies them.',
      ),
    );
    // Refused, the offer is to sync first (planning again would be refused
    // again): no plan buttons beside it.
    if (refusal) doc.append(refusalCard());
    else if (nothingNote && !can && !n)
      doc.append(
        h(
          'p',
          {
            class: 'cb-apply-note',
            role: 'status',
            'data-testid': 'plan-nothing',
          },
          nothingNote,
        ),
      );
    else if (itemsTotal && (n || can)) {
      const bs: Button[] = [];
      if (n)
        bs.push({
          label: `plan ${n} selected`,
          run: () => plan(selectedKeys()),
        });
      if (can)
        bs.push({
          label: `plan all ${can}`,
          fill: !n,
          run: () => plan('all'),
        });
      doc.append(buttons(bs));
    }
    const keep = focusSnapshot(readEl);
    readEl.replaceChildren(h('div', { class: 'kit-doc' }, doc));
    focusRestore(readEl, keep);
  }

  function drawOpen(): void {
    if (!open) return;
    const keep = focusSnapshot(readEl);
    const j = open.job;
    const doc =
      j.state === 'planned' || j.state === 'cancelled'
        ? renderPlan(j)
        : renderJob(ctx, open, jobHooks);
    readEl.replaceChildren(h('div', { class: 'kit-doc' }, doc));
    focusRestore(readEl, keep);
  }

  // The plan: its groups with every step's exact command, and approve (or,
  // discarded, what it was).
  function renderPlan(j: Job): HTMLElement {
    const steps = j.steps ?? [];
    const groups: StepGroup<Step>[] =
      planGroups?.job === j.id ? planGroups.groups : groupSteps(steps);
    const agent = agentOf(j);
    const discarded = j.state === 'cancelled';
    const doc = h(
      'article',
      { class: 'cb-apply-doc cb-plan', 'data-job': String(j.id) },
      h(
        'p',
        { class: 'kit-kick' },
        discarded
          ? `plan \u00b7 job #${j.id} \u00b7 ${j.machine} \u00b7 discarded`
          : `plan \u00b7 job #${j.id} \u00b7 ${j.machine} \u00b7 built ${builtAgo(j.created_at)}`,
      ),
      h('h1', { class: 'kit-h1' }, jobTitle(j)),
      facts([
        ['steps', String(steps.length)],
        ['local', String(steps.filter((s) => s.lane !== 'agent').length)],
        ['outward', String(steps.filter((s) => s.lane === 'agent').length)],
      ]),
    );
    if (!steps.length && !discarded) {
      doc.append(
        h(
          'p',
          { 'data-testid': 'plan-empty' },
          'Nothing in this plan runs from this machine: no step is on it, and nothing outward.',
        ),
        discardButtons(j),
      );
      return doc;
    }
    for (const g of groups) {
      doc.append(
        h(
          'h3',
          { class: 'kit-label cb-group-label' },
          h(
            'span',
            { class: `cb-lanename cb-lanename-${g.lane}` },
            laneLabel(g.lane, agent),
          ),
          ` \u00b7 ${actionTitle(g.action)} \u00b7 ${g.steps.length}`,
        ),
        h(
          'div',
          {
            class: 'kit-table cb-plan-group',
            'data-action': g.action,
            'data-lane': g.lane,
          },
          ...g.steps.map((s) =>
            h(
              'div',
              { class: 'kit-tr cb-plan-step', 'data-key': s.key },
              h('span', { class: 'cb-step-k' }, keyWithoutKind(s.key)),
              h('code', { class: 'cb-cmd' }, s.command),
            ),
          ),
        ),
      );
    }
    if (discarded) {
      doc.append(
        h(
          'p',
          { class: 'cb-approve-none', 'data-testid': 'plan-discarded' },
          'This plan was discarded before it was approved: nothing in it ran.',
        ),
      );
    } else doc.append(approveBlock(j));
    return doc;
  }

  function discardButtons(j: Job): HTMLElement {
    return buttons([{ label: 'discard', run: () => void discard(j.id) }]);
  }

  // approve: with a session picker when the plan has agent-lane steps, and
  // discard.
  function approveBlock(j: Job): HTMLElement {
    const steps = j.steps ?? [];
    const outward = steps.filter((s) => s.lane === 'agent').length;
    const el = h('div', { class: 'cb-approve', 'data-testid': 'approve' });
    el.append(h('h3', { class: 'kit-label' }, 'approve'));
    const bs: Button[] = [];
    if (needsSession(steps)) {
      const ps = present();
      const chosen = planSession();
      if (!ps.length) {
        el.append(
          h(
            'p',
            { class: 'cb-approve-none', 'data-testid': 'no-session' },
            `${outwardGo(outward)} to an agent session, and none is here. When one attaches, it appears here to choose.`,
          ),
        );
      } else {
        el.append(
          h(
            'p',
            { class: 'cb-approve-what' },
            `${planSummary(steps)}. ${outwardGo(outward)} to:`,
          ),
          h(
            'div',
            {
              class: 'cb-sessions',
              role: 'radiogroup',
              'aria-label': 'session',
              'data-testid': 'session-picker',
            },
            ...ps.map((s) =>
              h(
                'button',
                {
                  type: 'button',
                  role: 'radio',
                  class: 'kit-chip cb-session' + (s.id === chosen ? ' on' : ''),
                  'aria-checked': String(s.id === chosen),
                  'data-session': s.id,
                  onclick() {
                    courtSession = s.id;
                    note = '';
                    drawOpen();
                    if (active) ctx.setPrimary(primary());
                  },
                },
                s.label || s.id,
              ),
            ),
          ),
        );
        if (!chosen) {
          el.append(
            h(
              'p',
              { class: 'cb-approve-none', 'data-testid': 'pick-session' },
              `${ps.length} sessions are here and none is chosen yet.`,
            ),
          );
        } else bs.push({ label: 'approve', fill: true, run: () => approve() });
      }
    } else {
      el.append(
        h(
          'p',
          { class: 'cb-approve-what' },
          `${planSummary(steps)}, all local.`,
        ),
      );
      bs.push({ label: 'approve and run', fill: true, run: () => approve() });
    }
    bs.push({ label: 'discard', run: () => void discard(j.id) });
    el.append(buttons(bs));
    if (note)
      el.append(h('p', { class: 'cb-apply-note', role: 'status' }, note));
    return el;
  }

  // canApprove: a plan with steps, and a session chosen when it needs one.
  function canApprove(): boolean {
    if (!open || open.job.state !== 'planned') return false;
    const steps = open.job.steps ?? [];
    if (!steps.length) return false;
    if (!needsSession(steps)) return true;
    return !!planSession();
  }

  function approve(): void {
    if (!open || !canApprove()) return;
    const needs = needsSession(open.job.steps ?? []);
    void act<JobView>('/apply/approve', {
      plan_id: open.job.id,
      session: needs ? planSession() : '',
    });
  }

  // discard: serve cancels the unapproved plan; the ready list shows again.
  async function discard(id: number): Promise<void> {
    if (busy) return;
    busy = true;
    note = '';
    try {
      const v = await ctx.api.post<JobView>('/apply/cancel', { plan_id: id });
      patchJob(v);
      view = 'ready';
      if (openId === id) ctx.route.go('apply');
      else paintList();
    } catch (err) {
      note = message(err);
      drawOpen();
    } finally {
      busy = false;
    }
  }

  function pauseOrResume(): void {
    if (!open || viewOf(open.job) !== 'running') return;
    void act<JobView>(open.job.paused ? '/jobs/resume' : '/jobs/pause', {
      id: open.job.id,
    });
  }

  const jobHooks: JobHooks = {
    agent: (j) => agentOf(j),
    stepsShown: () => stepsShown,
    showMore() {
      stepsShown += STEPS_PAGE;
      drawOpen();
    },
    note: () => note,
    answer(card: NeedsYou, action: string, text: string, ok?: () => void) {
      void act<AnswerResult>(
        '/jobs/answer',
        { needs_you: card.id, action, text },
        ok,
      );
    },
    undo(step: JobStep) {
      void act<UndoResult>('/jobs/undo', { step: step.id });
    },
    editing: (id) => editors.get(id),
    edit(id, text) {
      if (text === null) editors.delete(id);
      else editors.set(id, text);
    },
  };

  // ---- the bar and the composer ----

  function primary(): Primary | null {
    if (open && openId !== null) {
      const j = open.job;
      if (j.state === 'planned') {
        return canApprove() ? { label: 'Approve', run: approve } : null;
      }
      if (viewOf(j) === 'running') {
        return j.paused
          ? { label: 'Resume', run: pauseOrResume }
          : { label: 'Pause job', run: pauseOrResume };
      }
      return null;
    }
    if (openId === null && refusal) {
      return syncing
        ? null
        : { label: 'Sync first', run: () => void syncFirst() };
    }
    if (openId === null && view === 'ready' && itemsTotal) {
      const n = selectedKeys().length;
      if (n) return { label: `Plan ${n}`, run: () => plan(selectedKeys()) };
      return plannable() ? { label: 'Plan all', run: () => plan('all') } : null;
    }
    return null;
  }

  // feed tells the composer and the bar what To apply shows, only while it
  // is the active section: the open job (by number, with its title) once it
  // has loaded, else nothing.
  function feed(): void {
    if (!active) return;
    if (open && openId !== null)
      ctx.setAttached({ job: String(openId) }, jobTitle(open.job));
    else ctx.setAttached({});
    ctx.setPrimary(primary());
  }

  // ---- keys (bound only while To apply is shown) ----

  const approveKey: KeyBinding = {
    keys: 'a',
    label: 'approve the open plan',
    group: 'page',
    run() {
      if (canApprove()) approve();
      else if (
        open?.job.state === 'planned' &&
        needsSession(open.job.steps ?? []) &&
        !planSession()
      ) {
        note = 'not approved: no session is chosen for the outward steps';
        drawOpen();
      }
    },
  };
  const pauseKey: KeyBinding = {
    keys: 'p',
    label: 'pause or resume the open job',
    group: 'page',
    run() {
      pauseOrResume();
    },
  };

  const applyKeys: KeyBinding[] = [approveKey, pauseKey];

  function close(): void {
    jobSeq.next(); // a reply on its way no longer draws
    openId = null;
    open = null;
    note = '';
    drawOverview();
    paintList();
    feed();
  }

  ctx.onDockSession(() => {
    if (open?.job.state === 'planned') drawOpen();
    if (active) ctx.setPrimary(primary());
  });

  void loadJobs();
  void loadItems();
  void loadSessions();
  void loadSummary();

  // Which job a live event is about.
  const jobOf = (type: string, data: unknown): number | null => {
    const d = (data ?? {}) as { id?: number; job_id?: number };
    if (type === 'job') return d.id ?? null;
    return d.job_id ?? null;
  };

  return {
    id: 'apply',
    list: handle.el,
    read: readEl,
    listKeys: () => ({ nav: handle, selects: true }),
    keys: () => applyKeys,
    show(sub: string) {
      active = true;
      const id = sub ? Number(decodeURIComponent(sub).replace(/^#/, '')) : NaN;
      if (!sub || !Number.isFinite(id)) {
        if (openId !== null) close();
        else {
          drawOverview();
          feed();
        }
      } else if (id !== openId) {
        openId = id;
        open = null;
        note = '';
        feed();
        void loadJob(id);
      } else {
        if (open) drawOpen();
        else void loadJob(id);
        feed();
      }
      paintList();
    },
    hide() {
      active = false;
    },
    onLive(type: string, data: unknown) {
      if (type === 'job' || type === 'step' || type === 'needs_you') {
        const about = jobOf(type, data);
        mark((d) => {
          if (about === null) d.all = true;
          else d.jobs.add(about);
        });
      } else if (type === 'index' || type === 'decided') {
        mark((d) => {
          d.items = true;
          if (type === 'index') d.summary = true;
        });
      } else if (type === 'sessions') {
        mark((d) => (d.sessions = true));
      } else if (type === 'sync') {
        onSync((data ?? {}) as SyncEvent);
      }
    },
    primary,
  };
}

// ---- a job --------------------------------------------------------------------

export interface JobHooks {
  agent(job: Job): string;
  stepsShown(): number;
  showMore(): void;
  note(): string;
  answer(card: NeedsYou, action: string, text: string, ok?: () => void): void;
  undo(step: JobStep): void;
  /** An open "edit text" field's text for a card (undefined: not editing). */
  editing(card: number): string | undefined;
  /** Keep (text) or drop (null) a card's open edit across redraws. */
  edit(card: number, text: string | null): void;
}

/**
 * renderJob draws a running or finished job: its facts, the cards that need
 * Court (rust edge) or say the agent paused (amber), and the steps table.
 */
export function renderJob(_ctx: Ctx, v: JobView, hooks: JobHooks): HTMLElement {
  const j = v.job;
  const steps = j.steps ?? [];
  const p = progress(j);
  const agent = hooks.agent(j);
  const open = (v.needs_you ?? []).filter((c) => c.state === 'open');
  const stepOf = (id: number) => steps.find((s) => s.id === id);
  const lanes = jobLanes(j).map((l) => laneLabel(l, agent));
  const state = j.paused && viewOf(j) === 'running' ? 'paused' : j.state;

  const doc = h(
    'article',
    { class: 'cb-apply-doc cb-job', 'data-job': String(j.id) },
    h(
      'p',
      { class: 'kit-kick' },
      [lanes.join(' + '), `job #${j.id}`, state]
        .filter(Boolean)
        .join(' \u00b7 '),
    ),
    h('h1', { class: 'kit-h1' }, jobTitle(j)),
  );
  const pairs: Array<[string, string]> = [
    ['done', `${p.verified} of ${p.total}`],
    ['needs you', String(open.filter((c) => c.kind !== 'paused').length)],
    ['paused', String(p.paused)],
  ];
  if (p.skipped) pairs.push(['skipped', String(p.skipped)]);
  if (p.failed) pairs.push(['failed', String(p.failed)]);
  const restores = steps.filter((s) => s.restore).length;
  if (restores) pairs.push(['restore records', String(restores)]);
  doc.append(facts(pairs));

  const cardsEl = h('div', { class: 'cb-needs', 'data-testid': 'needs-you' });
  for (const c of open)
    cardsEl.append(needsCard(c, stepOf(c.step_id), j, agent, hooks));
  doc.append(cardsEl);
  const note = hooks.note();
  if (note)
    doc.append(h('p', { class: 'cb-apply-note', role: 'status' }, note));

  // The steps table.
  const next = nextSteps(steps);
  const shown = steps.slice(0, hooks.stepsShown());
  doc.append(
    h('h3', { class: 'kit-label' }, 'steps'),
    h(
      'div',
      { class: 'kit-table cb-steps', 'data-testid': 'steps' },
      ...shown.map((s) => {
        const m = stepMark(s, next.has(s.id));
        return h(
          'div',
          {
            class: 'kit-tr cb-step',
            'data-step': String(s.id),
            'data-state': s.state,
            'data-key': s.key,
          },
          h('span', { class: `cb-step-g cb-tone-${m.tone}` }, m.glyph),
          h('span', { class: 'cb-step-k' }, keyWithoutKind(s.key)),
          h(
            'span',
            { class: 'cb-step-w' },
            h('span', { class: 'cb-step-t' }, m.text),
            s.undoable
              ? h(
                  'button',
                  {
                    type: 'button',
                    class: 'kit-btn cb-undo',
                    onclick() {
                      hooks.undo(s);
                    },
                  },
                  'undo',
                )
              : null,
          ),
        );
      }),
      steps.length > shown.length
        ? h(
            'div',
            { class: 'kit-tr cb-steps-more' },
            h('span'),
            h(
              'span',
              { class: 'cb-step-k' },
              `\u2026 ${pluralize(steps.length - shown.length, 'more step')}`,
            ),
            h(
              'button',
              {
                type: 'button',
                class: 'cb-link',
                onclick() {
                  hooks.showMore();
                },
              },
              'show more',
            ),
          )
        : null,
    ),
  );
  return doc;
}

// needsCard is one open card: what serve asks Court, with serve's actions.
function needsCard(
  c: NeedsYou,
  step: JobStep | undefined,
  j: Job,
  agent: string,
  hooks: JobHooks,
): HTMLElement {
  const key = step ? keyWithoutKind(step.key) : '';
  const verb = step ? actionVerb(step.action) : 'run';
  const answer = (action: string, text = '') => hooks.answer(c, action, text);
  let el: HTMLElement;
  switch (c.kind) {
    case 'text': {
      const quote = h('blockquote', { class: 'cb-needs-text' }, c.text);
      const body = h(
        'div',
        null,
        h(
          'p',
          { class: 'cb-needs-what' },
          h('code', null, key),
          ` \u00b7 ${verb} with comment`,
        ),
        quote,
      );
      const four: Button[] = [
        {
          label: 'post and close',
          fill: true,
          run: () => answer('post-and-close', c.text),
        },
        {
          label: 'edit text',
          run() {
            hooks.edit(c.id, c.text);
            startEdit(c.text, true);
          },
        },
        {
          label: 'close without comment',
          run: () => answer('close-without-comment'),
        },
        { label: 'skip', run: () => answer('skip') },
      ];
      el = card({
        edge: 'signal',
        head: c.question ? `needs you \u00b7 ${c.question}` : 'needs you',
        body,
        actions: four,
      });
      // edit text: the comment becomes a field; save sends it to serve (the
      // card stays open with the new text). While it is open its text is
      // kept by the section, so a redraw (a live event) draws it again as
      // Court left it.
      const startEdit = (text: string, focus: boolean) => {
        const field = h('textarea', {
          class: 'cb-needs-edit',
          'aria-label': 'comment',
          rows: 4,
        }) as HTMLTextAreaElement;
        field.value = text;
        field.addEventListener('input', () => hooks.edit(c.id, field.value));
        quote.replaceWith(field);
        el.querySelector('.kit-btns')?.replaceWith(
          buttons([
            {
              label: 'save text',
              fill: true,
              run: () =>
                hooks.answer(c, 'edit-text', field.value, () =>
                  hooks.edit(c.id, null),
                ),
            },
            {
              label: 'cancel',
              run() {
                hooks.edit(c.id, null);
                field.replaceWith(quote);
                el.querySelector('.kit-btns')?.replaceWith(buttons(four));
              },
            },
          ]),
        );
        if (focus) field.focus();
      };
      const draft = hooks.editing(c.id);
      if (draft !== undefined) startEdit(draft, false);
      break;
    }
    case 'batch':
      el = card({
        edge: 'signal',
        head: 'needs you \u00b7 confirm the batch',
        body: h('p', null, c.question),
        actions: [
          { label: 'confirm', fill: true, run: () => answer('confirm') },
          { label: 'skip batch', run: () => answer('skip-batch') },
        ],
      });
      break;
    case 'failed': {
      const bs: Button[] = [];
      if (j.session)
        bs.push({
          label: `hand to ${agent}`,
          run: () => answer('hand-to-agent'),
        });
      bs.push({ label: 'skip', run: () => answer('skip') });
      el = card({
        edge: 'signal',
        head: `needs you \u00b7 ${key || 'a step'} failed`,
        body: h(
          'div',
          null,
          h('p', null, c.question),
          step ? h('code', { class: 'cb-cmd' }, step.command) : null,
        ),
        actions: bs,
      });
      break;
    }
    case 'paused':
      el = card({
        edge: 'agent',
        head: `${agent} paused`,
        body: h(
          'p',
          null,
          key ? h('code', null, key) : null,
          key ? ': ' : '',
          c.question,
        ),
        actions: [
          {
            label: `${verb} anyway`,
            danger: dangerVerb(verb),
            run: () => answer('resume'),
          },
          { label: 'skip', run: () => answer('skip') },
        ],
      });
      break;
    default:
      el = card({
        edge: 'signal',
        head: `needs you \u00b7 ${c.kind}`,
        body: c.question,
      });
  }
  el.classList.add('cb-needs-card');
  el.dataset.card = String(c.id);
  el.dataset.kind = c.kind;
  return el;
}
