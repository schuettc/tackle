// apply.ts — the To apply section (spec §5): decided items become a plan,
// a plan becomes a job, and a job runs in two lanes while Court answers what
// needs him.
//
// The list (kit list) has three views. running: approved jobs, each row its
// lanes ("casebook · local", "pi · outward"), its title, a thin progress bar
// and n/total (or "queued"), and a signal line when something needs Court
// ("1 needs you · 1 paused"). ready: the plans serve built and Court hasn't
// approved, then the decided items waiting to be applied (selectable; the
// foot plans the selection, or all of them). done: finished jobs.
//
// #/apply/<job> opens a job. A plan (a planned job) shows its steps grouped
// by action with the exact command serve will run for each (serve's own
// command string, never composed here), and approve: with a session picker
// when the plan has agent-lane steps (approving without one is impossible
// here). A running or finished job shows its facts, the needs-you cards
// (rust edge; a text card's post and close · edit text · close without
// comment · skip, a batch's confirm, a failed step's hand to the agent, the
// agent's paused step), and the steps table (✓ verified, ‖ paused, · next),
// with undo on a finished step serve says it can undo.
//
// The bar's primary follows the job: Pause job while it runs, Resume while
// it is paused, Approve on a plan (once it can be), Plan N / Plan all on the
// ready list. Live job, step and needs_you events reload the list and the
// open job; a reload never draws over a newer one (an older reply is
// dropped). While To apply is the active section it feeds the composer
// {job: <id>} for the open job and {} otherwise; hidden, it never touches
// the attached line or the bar's primary.

import {
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
  openCardsByJob,
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
}
let planner: Planner | null = null;

/**
 * buildPlan asks serve for a plan of the given decided items (or all of
 * them). A built plan opens as #/apply/<job>; serve's refusal (409: the
 * observation is older than one sync interval) is shown in its own words.
 */
export function buildPlan(ctx: Ctx, what: string[] | 'all'): void {
  const body = what === 'all' ? { all: true } : { keys: what };
  void ctx.api
    .post<PlanView>('/apply/plan', body)
    .then((v) => planner?.planned(v))
    .catch((err: unknown) => planner?.refused(message(err)));
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
  let chosenSession = '';
  let stepsShown = STEPS_PAGE;
  let refusal = '';
  let note = '';
  let busy = false;
  let painting = false;
  const listSeq = makeSeq();
  const itemsSeq = makeSeq();
  const jobSeq = makeSeq();

  // The agent a job's outward steps go to: its session's, or for a plan,
  // the session Court has chosen for it.
  const agentOf = (job: Job): string => {
    const id =
      job.state === 'planned' && !job.session ? chosenSession : job.session;
    const s = sessions.find((x) => x.id === id);
    return s?.harness || ctx.agentName() || 'agent';
  };
  const present = () => sessions.filter((s) => !s.left);

  // ---- the list ----

  const footCount = h('span', { class: 'cb-apply-foot-n' });
  const footBtns = h('span', { class: 'cb-apply-foot-btns' });
  const foot = h('div', { class: 'cb-apply-foot' }, footCount, footBtns);

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
        return {
          id: it.key,
          key: `${it.kind} \u00b7 ${keyWithoutKind(it.key)}`,
          title: it.title ?? keyWithoutKind(it.key),
          meta: it.decision?.disposition ?? '',
          selectable: true,
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
      else if (r.t === 'item') handle.toggle(i);
      else void loadItems(items.length);
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
  const selectedKeys = () =>
    handle.selectedIds().filter((k) => !k.startsWith('job:') && k !== 'more');

  function rowsFor(v: ApplyView): Row[] {
    const js = jobs
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
    for (const j of jobs) c[viewOf(j)]++;
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
    const els = handle.el.querySelectorAll<HTMLElement>('.kit-row');
    els.forEach((rowEl, i) => {
      const r = shown[i];
      if (!r) return;
      if (r.t !== 'job') {
        if (r.t === 'item') rowEl.dataset.key = r.item.key;
        else rowEl.classList.add('cb-apply-more');
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

  function paintFoot(): void {
    if (view !== 'ready' || !itemsTotal) {
      footCount.textContent = '';
      footBtns.replaceChildren();
      foot.hidden = true;
      return;
    }
    foot.hidden = false;
    const n = selectedKeys().length;
    footCount.textContent = n
      ? `${n} selected`
      : pluralize(itemsTotal, 'decided item');
    const bs: Button[] = [];
    if (n) bs.push({ label: `plan ${n}`, run: () => plan(selectedKeys()) });
    bs.push({ label: `plan all ${itemsTotal}`, run: () => plan('all') });
    footBtns.replaceChildren(buttons(bs));
  }

  // ---- loading ----

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
      paintList();
    } catch {
      // non-fatal: the list stays as it was
    }
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
      if (chosenSession && !present().some((s) => s.id === chosenSession)) {
        chosenSession = '';
      }
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
      if (openId === null) drawOverview();
    } catch {
      // non-fatal
    }
  }

  // loadJob reloads the open job; only the newest request may draw.
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
    view = viewOf(v.job);
    const i = jobs.findIndex((j) => j.id === v.job.id);
    if (i >= 0) jobs[i] = v.job;
    else jobs = [...jobs, v.job];
    if (first) stepsShown = STEPS_PAGE;
    paintList();
    drawOpen();
    if (first) readEl.scrollTop = 0;
    feed();
  }

  // act posts one of Court's actions on the open job; a JobView reply is
  // drawn in order with the reloads.
  async function act<T>(path: string, body: unknown): Promise<void> {
    if (busy) return;
    busy = true;
    note = '';
    const mine = jobSeq.next();
    try {
      const r = await ctx.api.post<T>(path, body);
      const jv = r as unknown as JobView;
      if (jv && typeof jv === 'object' && 'job' in jv && jv.job) {
        if (jobSeq.isLatest(mine) && openId === jv.job.id) setOpen(jv);
      } else if (openId !== null) {
        await loadJob(openId);
      }
      void loadJobs();
    } catch (err) {
      note = message(err);
      drawOpen();
    } finally {
      busy = false;
    }
  }

  // ---- planning ----

  let asked: string[] | 'all' = [];
  function plan(what: string[] | 'all'): void {
    refusal = '';
    asked = what;
    buildPlan(ctx, what);
  }

  planner = {
    planned(v: PlanView) {
      refusal = '';
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
      void loadJobs();
      ctx.route.go('apply', String(v.job.id));
    },
    refused(msg: string) {
      refusal = msg;
      if (openId === null) drawOverview();
      else {
        note = msg;
        drawOpen();
      }
    },
  };

  // ---- the reading column ----

  function drawOverview(): void {
    if (openId !== null) return;
    const n = selectedKeys().length;
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
        ['selected', String(n)],
      ]),
      h(
        'p',
        null,
        'A plan shows the exact command for every step before anything runs. This machine applies what is on it; GitHub steps run once, from whichever machine applies them.',
      ),
    );
    if (refusal) {
      doc.append(
        card({
          edge: 'signal',
          head: 'not planned',
          body: h('p', { 'data-testid': 'plan-refused' }, refusal),
        }),
      );
    }
    if (itemsTotal) {
      const bs: Button[] = [];
      if (n)
        bs.push({
          label: `plan ${n} selected`,
          run: () => plan(selectedKeys()),
        });
      bs.push({
        label: `plan all ${itemsTotal}`,
        fill: !n,
        run: () => plan('all'),
      });
      doc.append(buttons(bs));
    }
    readEl.replaceChildren(h('div', { class: 'kit-doc' }, doc));
  }

  function drawOpen(): void {
    if (!open) return;
    const j = open.job;
    const doc =
      j.state === 'planned' ? renderPlan(j) : renderJob(ctx, open, jobHooks);
    readEl.replaceChildren(h('div', { class: 'kit-doc' }, doc));
  }

  // The plan: its groups with every step's exact command, and approve.
  function renderPlan(j: Job): HTMLElement {
    const steps = j.steps ?? [];
    // The session the outward steps go to: Court's choice while it is
    // here, else the first one present.
    const ps = present();
    if (!ps.some((s) => s.id === chosenSession))
      chosenSession = ps[0]?.id ?? '';
    const groups: StepGroup<Step>[] =
      planGroups?.job === j.id ? planGroups.groups : groupSteps(steps);
    const agent = agentOf(j);
    const doc = h(
      'article',
      { class: 'cb-apply-doc cb-plan', 'data-job': String(j.id) },
      h(
        'p',
        { class: 'kit-kick' },
        `plan \u00b7 job #${j.id} \u00b7 ${j.machine} \u00b7 built ${builtAgo(j.created_at)}`,
      ),
      h('h1', { class: 'kit-h1' }, jobTitle(j)),
      facts([
        ['steps', String(steps.length)],
        ['local', String(steps.filter((s) => s.lane !== 'agent').length)],
        ['outward', String(steps.filter((s) => s.lane === 'agent').length)],
      ]),
    );
    if (!steps.length) {
      doc.append(
        h(
          'p',
          { 'data-testid': 'plan-empty' },
          'Nothing in this plan runs from this machine: no step is on it, and nothing outward.',
        ),
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
    doc.append(approveBlock(j));
    return doc;
  }

  // approve: with a session picker when the plan has agent-lane steps.
  function approveBlock(j: Job): HTMLElement {
    const steps = j.steps ?? [];
    const outward = steps.filter((s) => s.lane === 'agent').length;
    const el = h('div', { class: 'cb-approve', 'data-testid': 'approve' });
    el.append(h('h3', { class: 'kit-label' }, 'approve'));
    if (needsSession(steps)) {
      const ps = present();
      if (!ps.length) {
        el.append(
          h(
            'p',
            { class: 'cb-approve-none', 'data-testid': 'no-session' },
            `The ${pluralize(outward, 'outward step')} go to an agent session, and none is here. When one attaches, it appears here to choose.`,
          ),
        );
        if (note)
          el.append(h('p', { class: 'cb-apply-note', role: 'status' }, note));
        return el;
      }
      el.append(
        h(
          'p',
          { class: 'cb-approve-what' },
          `${planSummary(steps)}. The ${pluralize(outward, 'outward step')} go to:`,
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
                class:
                  'kit-chip cb-session' + (s.id === chosenSession ? ' on' : ''),
                'aria-checked': String(s.id === chosenSession),
                'data-session': s.id,
                onclick() {
                  chosenSession = s.id;
                  drawOpen();
                  if (active) ctx.setPrimary(primary());
                },
              },
              s.label || s.id,
            ),
          ),
        ),
      );
    } else {
      el.append(
        h(
          'p',
          { class: 'cb-approve-what' },
          `${planSummary(steps)}, all local.`,
        ),
      );
    }
    el.append(
      buttons([
        {
          label: needsSession(steps) ? 'approve' : 'approve and run',
          fill: true,
          run: () => approve(),
        },
      ]),
    );
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
    return present().some((s) => s.id === chosenSession);
  }

  function approve(): void {
    if (!open || !canApprove()) return;
    const needs = needsSession(open.job.steps ?? []);
    void act<JobView>('/apply/approve', {
      plan_id: open.job.id,
      session: needs ? chosenSession : '',
    });
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
    answer(card: NeedsYou, action: string, text: string) {
      void act<AnswerResult>('/jobs/answer', {
        needs_you: card.id,
        action,
        text,
      });
    },
    undo(step: JobStep) {
      void act<UndoResult>('/jobs/undo', { step: step.id });
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
    if (openId === null && view === 'ready' && itemsTotal) {
      const n = selectedKeys().length;
      return n
        ? { label: `Plan ${n}`, run: () => plan(selectedKeys()) }
        : { label: 'Plan all', run: () => plan('all') };
    }
    return null;
  }

  // feed tells the composer and the bar what To apply shows, only while it
  // is the active section: the open job once it has loaded, else nothing.
  function feed(): void {
    if (!active) return;
    ctx.setAttached(open && openId !== null ? { job: String(openId) } : {});
    ctx.setPrimary(primary());
  }

  // ---- keys (bound only while To apply is shown) ----

  const approveKey: KeyBinding = {
    keys: 'a',
    label: 'approve the open plan',
    group: 'page',
    run() {
      if (canApprove()) approve();
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

  function close(): void {
    jobSeq.next(); // a reply on its way no longer draws
    openId = null;
    open = null;
    note = '';
    drawOverview();
    paintList();
    feed();
  }

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
    keys: [approveKey, pauseKey],
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
        void loadJobs();
        const about = jobOf(type, data);
        if (openId !== null && (about === null || about === openId)) {
          void loadJob(openId);
        }
      } else if (type === 'index' || type === 'decided') {
        void loadItems();
      } else if (type === 'sessions') {
        void loadSessions();
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
  answer(card: NeedsYou, action: string, text: string): void;
  undo(step: JobStep): void;
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
            h('span', { class: `cb-step-t cb-tone-${m.tone}` }, m.text),
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
        { label: 'edit text', run: () => edit() },
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
      // card stays open with the new text).
      const edit = () => {
        const field = h('textarea', {
          class: 'cb-needs-edit',
          'aria-label': 'comment',
          rows: 4,
        }) as HTMLTextAreaElement;
        field.value = c.text;
        quote.replaceWith(field);
        const btns = el.querySelector('.kit-btns');
        btns?.replaceWith(
          buttons([
            {
              label: 'save text',
              fill: true,
              run: () => answer('edit-text', field.value),
            },
            {
              label: 'cancel',
              run() {
                field.replaceWith(quote);
                el.querySelector('.kit-btns')?.replaceWith(buttons(four));
              },
            },
          ]),
        );
        field.focus();
      };
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
