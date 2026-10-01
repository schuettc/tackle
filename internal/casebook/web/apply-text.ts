// apply-text.ts — the To apply section's words and counts, as pure
// functions over serve's jobs (no kit or browser imports, so node --test can
// load it).

import type { Job, JobStep, NeedsYou, Step } from './wire.d.ts';
import { pluralize } from './decide-math.ts';

/** The job's view in the list: running (approved on), ready (a plan), done. */
export type ApplyView = 'running' | 'ready' | 'done';

export function viewOf(job: Job): ApplyView {
  switch (job.state) {
    case 'planned':
      return 'ready';
    case 'done':
    case 'failed':
    case 'cancelled':
      return 'done';
    default:
      return 'running';
  }
}

const TITLES: Record<string, string> = {
  'branch-delete-local': 'Delete local branches',
  'branch-delete-remote': 'Delete remote branches',
  'worktree-remove': 'Remove worktrees',
  'repo-archive': 'Archive repos',
  'repo-delete': 'Delete repos',
  'pr-close': 'Close PRs',
  'pr-merge': 'Merge PRs',
  'issue-close': 'Close issues',
};

/** actionTitle names a group of steps: "Delete local branches". */
export function actionTitle(action: string): string {
  return TITLES[action] ?? action;
}

const PAST: Record<string, string> = {
  'branch-delete-local': 'deleted',
  'branch-delete-remote': 'deleted on the remote',
  'worktree-remove': 'removed',
  'repo-archive': 'archived',
  'repo-delete': 'deleted',
  'pr-close': 'closed',
  'pr-merge': 'merged',
  'issue-close': 'closed',
};

/** The verb a paused step would do if Court resumes it: "close anyway". */
const VERB: Record<string, string> = {
  'branch-delete-local': 'delete',
  'branch-delete-remote': 'delete',
  'worktree-remove': 'remove',
  'repo-archive': 'archive',
  'repo-delete': 'delete',
  'pr-close': 'close',
  'pr-merge': 'merge',
  'issue-close': 'close',
};

export function actionVerb(action: string): string {
  return VERB[action] ?? 'run';
}

/**
 * laneLabel is a lane as the page names it: casebook's own steps are local,
 * the agent's go outward ("pi · outward").
 */
export function laneLabel(lane: string, agent: string): string {
  return lane === 'agent'
    ? `${agent || 'agent'} \u00b7 outward`
    : 'casebook \u00b7 local';
}

export interface StepGroup<S extends Step = Step> {
  action: string;
  lane: string;
  steps: S[];
}

/**
 * groupSteps groups steps by (action, lane) in first-seen order: what serve's
 * Plan.Groups does, for a job read back from GET /api/job.
 */
export function groupSteps<S extends Step>(steps: S[]): StepGroup<S>[] {
  const out: StepGroup<S>[] = [];
  const at = new Map<string, number>();
  for (const s of steps) {
    const k = `${s.action}\u0000${s.lane}`;
    const i = at.get(k);
    if (i === undefined) {
      at.set(k, out.length);
      out.push({ action: s.action, lane: s.lane, steps: [s] });
    } else out[i].steps.push(s);
  }
  return out;
}

/** jobTitle: its groups' titles, "Delete local branches, archive repos". */
export function jobTitle(job: Job): string {
  const titles = [
    ...new Set(groupSteps(job.steps ?? []).map((g) => actionTitle(g.action))),
  ];
  if (!titles.length) return `job #${job.id}`;
  return titles
    .map((t, i) => (i ? t.charAt(0).toLowerCase() + t.slice(1) : t))
    .join(', ');
}

/** jobLanes: the lanes a job has steps in, casebook's first. */
export function jobLanes(job: Job): string[] {
  const have = new Set((job.steps ?? []).map((s) => s.lane));
  return ['casebook', 'agent'].filter((l) => have.has(l));
}

// A step no lane will move again on its own.
const FINISHED = new Set(['verified', 'reported', 'skipped', 'failed']);

export interface Progress {
  total: number;
  /** Steps finished (verified, reported, skipped, failed). */
  finished: number;
  /** Finished per lane, for the bar's two colours. */
  byLane: Record<string, number>;
  verified: number;
  paused: number;
  skipped: number;
  failed: number;
  /** Any step has moved, or the job is running. */
  started: boolean;
}

export function progress(job: Job): Progress {
  const p: Progress = {
    total: 0,
    finished: 0,
    byLane: {},
    verified: 0,
    paused: 0,
    skipped: 0,
    failed: 0,
    started: job.state === 'running',
  };
  for (const s of job.steps ?? []) {
    p.total++;
    if (s.state !== 'pending') p.started = true;
    if (FINISHED.has(s.state)) {
      p.finished++;
      p.byLane[s.lane] = (p.byLane[s.lane] ?? 0) + 1;
    }
    if (s.state === 'verified') p.verified++;
    if (s.state === 'paused') p.paused++;
    if (s.state === 'skipped') p.skipped++;
    if (s.state === 'failed') p.failed++;
  }
  return p;
}

/** needsLine is the list row's signal line: "1 needs you · 1 paused". */
export function needsLine(needsYou: number, paused: number): string {
  const parts: string[] = [];
  if (needsYou)
    parts.push(`${needsYou} ${needsYou === 1 ? 'needs' : 'need'} you`);
  if (paused) parts.push(`${paused} paused`);
  return parts.join(' \u00b7 ');
}

/** openCardsByJob counts the open needs-you cards of each job. */
export function openCardsByJob(cards: NeedsYou[]): Map<number, number> {
  const m = new Map<number, number>();
  for (const c of cards) {
    if (c.state !== 'open') continue;
    m.set(c.job_id, (m.get(c.job_id) ?? 0) + 1);
  }
  return m;
}

export type Tone = 'ok' | 'agent' | 'signal' | 'wait' | 'muted';

export interface StepMark {
  glyph: string;
  text: string;
  tone: Tone;
}

/**
 * stepMark is a step's line in the steps table: ✓ verified, ‖ paused,
 * · next (the next step of its lane), and so on.
 */
export function stepMark(s: JobStep, next: boolean): StepMark {
  const past = PAST[s.action] ?? 'done';
  const why = (w: string) => (s.detail ? `${w} \u00b7 ${s.detail}` : w);
  if (s.undone_at) return { glyph: '\u21ba', text: 'undone', tone: 'muted' };
  switch (s.state) {
    case 'verified':
      return { glyph: '\u2713', text: `${past} \u00b7 verified`, tone: 'ok' };
    case 'reported':
      return {
        glyph: '\u2713',
        text: `${past} \u00b7 reported`,
        tone: 'muted',
      };
    case 'running':
      return {
        glyph: '\u25b8',
        text: 'running',
        tone: s.lane === 'agent' ? 'agent' : 'signal',
      };
    case 'paused':
      return { glyph: '\u2016', text: why('paused'), tone: 'agent' };
    case 'needs_you':
      return { glyph: '!', text: 'needs you', tone: 'signal' };
    case 'failed':
      return { glyph: '\u2717', text: why('failed'), tone: 'signal' };
    case 'skipped':
      return { glyph: '\u2013', text: why('skipped'), tone: 'muted' };
    default:
      return { glyph: '\u00b7', text: next ? 'next' : 'queued', tone: 'wait' };
  }
}

/** nextSteps: the first pending step of each lane (what runs next there). */
export function nextSteps(steps: JobStep[]): Set<number> {
  const out = new Set<number>();
  const seen = new Set<string>();
  for (const s of steps) {
    if (s.state !== 'pending' || seen.has(s.lane)) continue;
    seen.add(s.lane);
    out.add(s.id);
  }
  return out;
}

/** planSummary: a plan's size, "5 steps · 3 local · 2 outward". */
export function planSummary(steps: Step[]): string {
  let local = 0;
  let out = 0;
  for (const s of steps) {
    if (s.lane === 'agent') out++;
    else local++;
  }
  const parts = [pluralize(steps.length, 'step')];
  if (local) parts.push(`${local} local`);
  if (out) parts.push(`${out} outward`);
  return parts.join(' \u00b7 ');
}

/** needsSession: a plan with agent-lane steps needs a session to approve. */
export function needsSession(steps: Step[]): boolean {
  return steps.some((s) => s.lane === 'agent');
}

/**
 * makeSeq orders replies by when they were asked for: a reply to an older
 * request never replaces what a newer one drew.
 */
export function makeSeq(): { next(): number; isLatest(n: number): boolean } {
  let latest = 0;
  return {
    next: () => ++latest,
    isLatest: (n) => n === latest,
  };
}

/**
 * outwardGo starts the sentence about where a plan's outward steps go, its
 * verb agreeing: "The outward step goes", "The 3 outward steps go".
 */
export function outwardGo(n: number): string {
  return n === 1 ? 'The outward step goes' : `The ${n} outward steps go`;
}

/** Held: the unfinished job an item is in, and whether it is still a plan. */
export interface Held {
  id: number;
  planned: boolean;
}

/**
 * plannedKeys maps each item key with a step in an unfinished job (a plan
 * not yet approved or discarded, or a job approved, running or paused) to
 * that job: serve plans an item into at most one. A started job is named
 * before a plan (the newest of each, when two hold it: jobs from before
 * serve kept to one). Done, failed and cancelled jobs hold nothing.
 */
export function plannedKeys(jobs: Job[]): Map<string, Held> {
  const m = new Map<string, Held>();
  const rank = (h: Held) => (h.planned ? 0 : 1);
  for (const j of jobs) {
    if (!['planned', 'approved', 'running', 'paused'].includes(j.state))
      continue;
    const h: Held = { id: j.id, planned: j.state === 'planned' };
    for (const s of j.steps ?? []) {
      const was = m.get(s.key);
      if (
        !was ||
        rank(h) > rank(was) ||
        (rank(h) === rank(was) && h.id > was.id)
      )
        m.set(s.key, h);
    }
  }
  return m;
}

/** heldLabel is a held item's meta: "in plan #3", "in job #1". */
export function heldLabel(h: Held): string {
  return `${h.planned ? 'in plan' : 'in job'} #${h.id}`;
}

/** heldWhere says where held items are: "in a plan", "in a job", or both. */
export function heldWhere(held: Map<string, Held>): string {
  const vs = [...held.values()];
  const plans = vs.some((h) => h.planned);
  const jobs = vs.some((h) => !h.planned);
  if (plans && jobs) return 'in a plan or a job';
  return jobs ? 'in a job' : 'in a plan';
}

/**
 * plannableCount is how many decided items "plan all" would plan: serve
 * leaves out the ones already in an unfinished job (plannedKeys). total is serve's count
 * of decided items, loaded the keys the page has, all whether it has them
 * all; a planned key the page hasn't loaded is counted as one of the total.
 */
export function plannableCount(
  total: number,
  loaded: string[],
  planned: Map<string, Held>,
  all: boolean,
): number {
  const have = new Set(loaded);
  let inPlan = loaded.filter((k) => planned.has(k)).length;
  if (!all) for (const k of planned.keys()) if (!have.has(k)) inPlan++;
  return Math.max(0, total - inPlan);
}

/** ageText: a short age, "40s", "12m", "3h", "2d". */
export function ageText(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

/**
 * observations says how old the observation a plan would be built from is,
 * by serve's rule (a plan is refused once the index is older than one sync
 * interval): "observations fresh", or "observations 34m old" when stale.
 */
export function observations(
  builtAt: number,
  intervalMs: number,
  now: number,
): { text: string; stale: boolean } {
  const age = now - builtAt;
  if (!intervalMs || age <= intervalMs)
    return { text: 'observations fresh', stale: false };
  return { text: `observations ${ageText(age)} old`, stale: true };
}
