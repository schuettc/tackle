// apply-text.test.ts — unit tests for the To apply section's words.
//
// Run with: node --test apply-text.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  actionTitle,
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
  outwardGo,
  plannedKeys,
  plannableCount,
  heldLabel,
  heldWhere,
  ageText,
  observations,
} from './apply-text.ts';
import type { Job, JobStep, NeedsYou } from './wire.d.ts';

let ids = 1;
function step(
  action: string,
  lane: string,
  state = 'pending',
  extra: Partial<JobStep> = {},
): JobStep {
  return {
    id: ids++,
    job_id: 1,
    key: `k${ids}`,
    action,
    lane,
    command: `cmd ${ids}`,
    state,
    ...extra,
  };
}
function job(state: string, steps: JobStep[]): Job {
  return {
    id: 3,
    machine: 'm',
    session: '',
    state,
    paused: false,
    created_at: '2026-09-27T00:00:00Z',
    steps,
  };
}

describe('views', () => {
  test('a plan is ready, approved to paused is running, the rest done', () => {
    assert.equal(viewOf(job('planned', [])), 'ready');
    for (const s of ['approved', 'running', 'paused'])
      assert.equal(viewOf(job(s, [])), 'running');
    for (const s of ['done', 'failed', 'cancelled'])
      assert.equal(viewOf(job(s, [])), 'done');
  });
});

describe('names', () => {
  test('lanes read as the mock writes them', () => {
    assert.equal(laneLabel('casebook', 'pi'), 'casebook · local');
    assert.equal(laneLabel('agent', 'pi'), 'pi · outward');
    assert.equal(laneLabel('agent', ''), 'agent · outward');
  });
  test('a group is named by its action', () => {
    assert.equal(actionTitle('branch-delete-local'), 'Delete local branches');
    assert.equal(actionTitle('pr-close'), 'Close PRs');
    assert.equal(actionTitle('something-new'), 'something-new');
  });
  test("a job's title is its groups, and its lanes casebook's first", () => {
    const j = job('running', [
      step('repo-archive', 'agent'),
      step('branch-delete-local', 'casebook'),
      step('repo-archive', 'agent'),
    ]);
    assert.equal(jobTitle(j), 'Archive repos, delete local branches');
    assert.deepEqual(jobLanes(j), ['casebook', 'agent']);
    assert.equal(jobTitle(job('planned', [])), 'job #3');
  });
});

describe('groupSteps', () => {
  test("groups by action and lane in first-seen order, as serve's Plan.Groups", () => {
    const a = step('branch-delete-local', 'casebook');
    const b = step('pr-close', 'agent');
    const c = step('branch-delete-local', 'casebook');
    const d = step('branch-delete-remote', 'casebook');
    const g = groupSteps([a, b, c, d]);
    assert.deepEqual(
      g.map((x) => [x.action, x.lane, x.steps.map((s) => s.id)]),
      [
        ['branch-delete-local', 'casebook', [a.id, c.id]],
        ['pr-close', 'agent', [b.id]],
        ['branch-delete-remote', 'casebook', [d.id]],
      ],
    );
  });
});

describe('progress', () => {
  test('finished counts every step no lane will move again, per lane', () => {
    const p = progress(
      job('running', [
        step('a', 'casebook', 'verified'),
        step('a', 'casebook', 'skipped'),
        step('b', 'agent', 'reported'),
        step('b', 'agent', 'paused'),
        step('b', 'agent', 'pending'),
        step('b', 'agent', 'failed'),
      ]),
    );
    assert.equal(p.total, 6);
    assert.equal(p.finished, 4);
    assert.deepEqual(p.byLane, { casebook: 2, agent: 2 });
    assert.equal(p.verified, 1);
    assert.equal(p.paused, 1);
    assert.equal(p.started, true);
  });
  test('an approved job with nothing moved has not started (queued)', () => {
    assert.equal(
      progress(job('approved', [step('a', 'casebook')])).started,
      false,
    );
  });
});

describe('needs you', () => {
  test('the signal line', () => {
    assert.equal(needsLine(1, 1), '1 needs you · 1 paused');
    assert.equal(needsLine(2, 0), '2 need you');
    assert.equal(needsLine(0, 3), '3 paused');
    assert.equal(needsLine(0, 0), '');
  });
  test('open cards by job', () => {
    const c = (job_id: number, state: string): NeedsYou => ({
      id: ids++,
      job_id,
      step_id: 0,
      kind: 'text',
      question: '',
      text: '',
      state,
      answer: '',
      created_at: '',
    });
    const m = openCardsByJob([
      c(1, 'open'),
      c(1, 'open'),
      c(2, 'answered'),
      c(3, 'open'),
    ]);
    assert.equal(m.get(1), 2);
    assert.equal(m.get(2), undefined);
    assert.equal(m.get(3), 1);
  });
});

describe('the steps table', () => {
  test('✓ verified, ‖ paused, · next and queued', () => {
    assert.deepEqual(stepMark(step('pr-close', 'agent', 'verified'), false), {
      glyph: '✓',
      text: 'closed · verified',
      tone: 'ok',
    });
    assert.equal(
      stepMark(
        step('pr-close', 'agent', 'paused', { detail: 'new comment' }),
        false,
      ).text,
      'paused · new comment',
    );
    assert.equal(
      stepMark(step('pr-close', 'agent', 'paused'), false).glyph,
      '‖',
    );
    assert.deepEqual(stepMark(step('pr-close', 'agent'), true), {
      glyph: '·',
      text: 'next',
      tone: 'wait',
    });
    assert.equal(stepMark(step('pr-close', 'agent'), false).text, 'queued');
    assert.equal(
      stepMark(step('branch-delete-remote', 'casebook', 'verified'), false)
        .text,
      'deleted on the remote · verified',
    );
    assert.equal(
      stepMark(
        step('branch-delete-local', 'casebook', 'verified', { undone_at: 'x' }),
        false,
      ).text,
      'undone',
    );
  });
  test('the next step is the first pending one of each lane', () => {
    const a = step('x', 'casebook', 'verified');
    const b = step('x', 'casebook');
    const c = step('x', 'casebook');
    const d = step('y', 'agent', 'running');
    const e = step('y', 'agent');
    assert.deepEqual([...nextSteps([a, b, c, d, e])], [b.id, e.id]);
  });
});

describe('plans', () => {
  test('the summary and whether approving needs a session', () => {
    const local = step('branch-delete-local', 'casebook');
    const out = step('pr-close', 'agent');
    assert.equal(
      planSummary([local, local, out]),
      '3 steps · 2 local · 1 outward',
    );
    assert.equal(planSummary([local]), '1 step · 1 local');
    assert.equal(needsSession([local]), false);
    assert.equal(needsSession([local, out]), true);
  });
});

describe('ordering', () => {
  test('only the latest request may draw', () => {
    const s = makeSeq();
    const first = s.next();
    const second = s.next();
    assert.equal(s.isLatest(first), false);
    assert.equal(s.isLatest(second), true);
  });
});

describe('outwardGo', () => {
  test('the verb agrees with the count', () => {
    assert.equal(outwardGo(1), 'The outward step goes');
    assert.equal(outwardGo(3), 'The 3 outward steps go');
  });
});

describe('plannedKeys and plannableCount', () => {
  const j = (id: number, state: string, keys: string[]): Job =>
    ({
      id,
      state,
      machine: 'm',
      created_at: '2026-09-27T00:00:00Z',
      paused: false,
      steps: keys.map((k) => step('pr-close', 'agent', 'pending', { key: k })),
    }) as unknown as Job;
  const jobs = [
    j(1, 'planned', ['pr:o/r#1', 'pr:o/r#2']),
    j(2, 'running', ['pr:o/r#3']),
    j(3, 'cancelled', ['pr:o/r#4']),
    j(4, 'planned', ['pr:o/r#2', 'pr:o/r#2']),
    j(5, 'approved', ['pr:o/r#5']),
    j(6, 'paused', ['pr:o/r#6']),
    j(7, 'done', ['pr:o/r#7']),
    j(8, 'failed', ['pr:o/r#8']),
    j(9, 'planned', ['pr:o/r#3']),
  ];
  test('every unfinished job holds its keys; a started job before a plan, then the newest', () => {
    const m = plannedKeys(jobs);
    assert.deepEqual(
      [...m.entries()].map(([k, v]) => `${k} ${heldLabel(v)}`).sort(),
      [
        'pr:o/r#1 in plan #1',
        'pr:o/r#2 in plan #4',
        'pr:o/r#3 in job #2',
        'pr:o/r#5 in job #5',
        'pr:o/r#6 in job #6',
      ],
    );
  });
  test('plan all leaves out what is in a plan or a job', () => {
    const m = plannedKeys(jobs);
    const loaded = ['pr:o/r#1', 'pr:o/r#3', 'pr:o/r#9'];
    assert.equal(plannableCount(3, loaded, m, true), 1);
    // Not all loaded: a held key not seen yet is one of the total.
    assert.equal(plannableCount(10, ['pr:o/r#9'], m, false), 5);
    assert.equal(plannableCount(2, loaded, m, true), 0);
  });
  test('the foot says where the held items are', () => {
    const m = plannedKeys(jobs);
    assert.equal(
      heldWhere(new Map([...m].filter(([, v]) => v.planned))),
      'in a plan',
    );
    assert.equal(
      heldWhere(new Map([...m].filter(([, v]) => !v.planned))),
      'in a job',
    );
    assert.equal(heldWhere(m), 'in a plan or a job');
  });
});

describe('observations', () => {
  const t0 = Date.parse('2026-09-27T12:00:00Z');
  test('fresh within one sync interval, then its age', () => {
    assert.deepEqual(observations(t0, 30 * 60e3, t0 + 29 * 60e3), {
      text: 'observations fresh',
      stale: false,
    });
    assert.deepEqual(observations(t0, 30 * 60e3, t0 + 34 * 60e3), {
      text: 'observations 34m old',
      stale: true,
    });
    assert.equal(observations(t0, 2000, t0 + 5000).text, 'observations 5s old');
  });
  test('ageText', () => {
    assert.equal(ageText(40e3), '40s');
    assert.equal(ageText(3 * 3600e3), '3h');
    assert.equal(ageText(50 * 3600e3), '2d');
  });
});
