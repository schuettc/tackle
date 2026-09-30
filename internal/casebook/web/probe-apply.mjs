// probe-apply.mjs — the To apply section's probe scenarios (Task 9), run by
// probe.mjs with its check helpers (t.check, t.checkList, t.until,
// t.eventually).
//
// Each scenario group starts its own serve (serve.mjs) with its own data:
// real clones under the serve's temp home with landed branches (seedClones:
// the casebook lane's git runs there and nowhere else), dormant repos and a
// bot's PR in the GitHub cache (seedRepos). serve's gh is the probe's fake
// (serve.mjs): nothing the probe starts reaches GitHub, and every gh call
// serve makes is checked. The agent is the fake agent (agent.mjs) over
// serve's own routes: presence, casebook_job_step, casebook_job_ask.

import { execSync } from 'node:child_process';
import { join } from 'node:path';
import { startServe } from './serve.mjs';
import { createAgent } from './agent.mjs';

const SESSION = 'probe-t9-pi';
const LABEL = 'pi \u00b7 t9-apply';
const OTHER = 'probe-t9-other';
const OTHER_LABEL = 'claude \u00b7 elsewhere';

const dormant = (name) => ({
  repo: `schuettc/${name}`,
  pushed_at: '2023-01-10T00:00:00Z',
  default_branch: 'main',
  prs: [],
  issues: [],
});
const T9_SEED = {
  seedClones: [{ name: 't9-local', branches: ['feat/l1', 'feat/l2'] }],
  seedRepos: [
    dormant('t9-dormant-a'),
    dormant('t9-dormant-b'),
    {
      repo: 'schuettc/t9-prs',
      pushed_at: '2026-09-20T00:00:00Z',
      default_branch: 'main',
      prs: [
        {
          repo: 'schuettc/t9-prs',
          number: 7,
          title: 'bump lodash from 4.17.20 to 4.17.21',
          author: 'dependabot[bot]',
          state: 'OPEN',
          created_at: '2026-03-01T00:00:00Z',
          updated_at: '2026-03-01T00:00:00Z',
        },
      ],
      issues: [],
    },
  ],
};
const BR = (b) => `branch:schuettc/t9-local@${b}`;
const PR = 'pr:schuettc/t9-prs#7';
const REPO_A = 'repo:schuettc/t9-dormant-a';
const REPO_B = 'repo:schuettc/t9-dormant-b';

// The page's state, read in one place.
const read = {
  primary: (pg) =>
    pg.$eval('.kit-primary', (e) => (e.hidden ? '' : e.textContent.trim())),
  attached: (pg) =>
    pg.$eval('[data-testid="composer-attached"]', (e) => e.textContent),
  cards: (pg) =>
    pg.$$eval('.cb-job .cb-needs-card', (els) =>
      els.map((e) => e.dataset.kind),
    ),
  step: (pg, id) =>
    pg
      .$eval(`.cb-steps .cb-step[data-step="${id}"]`, (e) => ({
        state: e.dataset.state,
        glyph: e.querySelector('.cb-step-g').textContent,
        text: e.querySelector('.cb-step-t').textContent,
        undo: !!e.querySelector('.cb-undo'),
      }))
      .catch(() => null),
};

// cssColor resolves a CSS colour expression to its computed rgb() string.
function cssColor(pg, expr) {
  return pg.evaluate((e) => {
    const d = document.createElement('div');
    d.style.color = e;
    document.body.append(d);
    const v = getComputedStyle(d).color;
    d.remove();
    return v;
  }, expr);
}

const pressWithFocus = async (pg, k) => {
  await pg.evaluate(() => {
    window.dispatchEvent(new Event('focus'));
    document.body.dispatchEvent(new FocusEvent('focus'));
  });
  await pg.keyboard.press(k);
};

// shoot takes the light and the dark screenshot, asserting the theme.
async function shoot(t, pg, name) {
  const bgs = { light: 'rgb(244, 245, 248)', dark: 'rgb(20, 22, 29)' };
  for (const theme of ['light', 'dark']) {
    for (let i = 0; i < 3; i++) {
      const th = await pg.evaluate(
        () => document.documentElement.dataset.theme,
      );
      if (th === theme) break;
      await pg.click('button.kit-ctl:has-text("theme")');
    }
    const got = await pg.evaluate(() => ({
      theme: document.documentElement.dataset.theme,
      bg: getComputedStyle(document.body).backgroundColor,
    }));
    t.check(
      `/tmp/t9-${name}-${theme}.png is ${theme} (theme ${got.theme}, body ${got.bg})`,
      got.theme === theme && got.bg === bgs[theme],
    );
    await pg.screenshot({ path: `/tmp/t9-${name}-${theme}.png` });
  }
  for (let i = 0; i < 3; i++) {
    const th = await pg.evaluate(() => document.documentElement.dataset.theme);
    if (th === 'light') break;
    await pg.click('button.kit-ctl:has-text("theme")');
  }
}

// gitIn runs git in a probe clone (under the serve's temp home only).
function gitIn(home, clone, args) {
  const dir = join(home, 'clones', clone);
  if (!dir.startsWith(home)) throw new Error('git outside the probe home');
  try {
    return execSync(`git ${args}`, {
      cwd: dir,
      stdio: 'pipe',
      env: {
        ...process.env,
        HOME: home,
        GIT_CONFIG_GLOBAL: '/dev/null',
        GIT_CONFIG_NOSYSTEM: '1',
      },
    })
      .toString()
      .trim();
  } catch {
    return null;
  }
}

let serves = [];
/** stopAll stops every serve these scenarios started (probe.mjs cleanup). */
export function stopApplyServes() {
  for (const s of serves) s.stop();
  serves = [];
}

async function ownServe(opts) {
  const s = await startServe(opts);
  serves.push(s);
  return s;
}

export async function applyScenarios(context, t) {
  const serveHandle = await ownServe(T9_SEED);
  try {
    await applyScenariosOn(context, t, serveHandle);
  } finally {
    serveHandle.stop();
    serves = serves.filter((s) => s !== serveHandle);
  }
  const stale = await ownServe({
    seedRepos: [dormant('t9-stale')],
    syncInterval: '2s',
  });
  try {
    await staleScenario(context, t, stale);
  } finally {
    stale.stop();
    serves = serves.filter((s) => s !== stale);
  }
}

async function applyScenariosOn(context, t, serveHandle) {
  const { check, checkList, until, eventually } = t;
  const agent = createAgent(serveHandle.base, serveHandle.token);
  const job = (id) => agent.api('GET', `/api/job?id=${id}`);
  const stepFor = (jv, key, action) =>
    jv.job.steps.find((s) => s.key === key && (!action || s.action === action));
  const home = serveHandle.home;

  // Court's decisions: the landed branches deleted, a dormant repo
  // archived, the bot's PR closed with a comment; one more dormant repo
  // stays decided but unplanned (for the keys check).
  await agent.api('POST', '/api/decide', {
    keys: [BR('feat/l1'), BR('feat/l2')],
    disposition: 'delete',
  });
  await agent.api('POST', '/api/decide', {
    keys: [REPO_A, REPO_B],
    disposition: 'archive',
  });
  await agent.api('POST', '/api/decide', {
    keys: [PR],
    disposition: 'close',
    note: 'superseded by #9',
  });

  const pg = await context.newPage();
  const errors = [];
  pg.on('pageerror', (e) => errors.push(String(e)));
  try {
    await pg.setViewportSize({ width: 1600, height: 900 });
    await pg.goto(serveHandle.url + '#/apply', {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    });

    // ---- the ready list and a plan ---------------------------------------
    console.log('\nscenario: to apply — the decided items and a plan');
    check(
      'the bar counts the decided items waiting to be applied ("to apply 5")',
      await until(
        pg,
        () =>
          document.querySelector('.kit-ctl[data-id="apply"] .kit-n')
            ?.textContent === '5',
      ),
    );
    await pg.click('.kit-chip[data-id="ready"]');
    const readyKeys = await (async () => {
      await until(
        pg,
        () => document.querySelectorAll('.cb-apply-list .kit-row').length === 5,
      );
      return pg.$$eval('.cb-apply-list .kit-row', (els) =>
        els.map((e) => e.dataset.key),
      );
    })();
    const served = await agent.api('GET', '/api/items?view=to-apply');
    checkList(
      "ready lists serve's to-apply items, in its order",
      readyKeys,
      served.items.map((i) => i.key),
    );
    check(
      'with nothing selected the primary is "Plan all"',
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Plan all',
      ),
    );
    // Select two rows: the primary and the foot count them.
    await pg.click(`.cb-apply-list .kit-row[data-key="${REPO_B}"] .kit-box`);
    await pg.click(`.cb-apply-list .kit-row[data-key="${PR}"] .kit-box`);
    check(
      'two selected: the primary is "Plan 2" and the foot says "2 selected"',
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Plan 2' &&
          document.querySelector('.cb-apply-foot-n')?.textContent ===
            '2 selected',
      ),
    );
    await pg.click(`.cb-apply-list .kit-row[data-key="${REPO_B}"] .kit-box`);
    await pg.click(`.cb-apply-list .kit-row[data-key="${PR}"] .kit-box`);

    // Plan all (no session is here yet).
    const planResp = pg.waitForResponse((r) =>
      r.url().includes('/api/apply/plan'),
    );
    await until(
      pg,
      () => document.querySelector('.kit-primary')?.textContent === 'Plan all',
    );
    await pg.click('.kit-primary');
    const planView = await (await planResp).json();
    const jobId = planView.job.id;
    check(
      `the plan opens as #/apply/${jobId}`,
      await until(
        pg,
        (id) =>
          location.hash === `#/apply/${id}` &&
          document.querySelector('.cb-plan')?.dataset.job === String(id),
        jobId,
      ),
    );
    // Every step shows serve's exact command, grouped as serve groups them.
    const shown = await pg.$$eval('.cb-plan-group', (gs) =>
      gs.map((g) => ({
        action: g.dataset.action,
        lane: g.dataset.lane,
        steps: [...g.querySelectorAll('.cb-plan-step')].map((s) => ({
          key: s.dataset.key,
          command: s.querySelector('.cb-cmd').textContent,
        })),
      })),
    );
    const want = planView.groups.map((g) => ({
      action: g.action,
      lane: g.lane,
      steps: g.steps.map((s) => ({ key: s.key, command: s.command })),
    }));
    checkList(
      "the plan's groups are serve's, in its order",
      shown.map((g) => `${g.lane} ${g.action} ${g.steps.length}`),
      want.map((g) => `${g.lane} ${g.action} ${g.steps.length}`),
    );
    checkList(
      "building a plan shows exact commands: every step's command is serve's, verbatim",
      shown.flatMap((g) => g.steps.map((s) => `${s.key} :: ${s.command}`)),
      want.flatMap((g) => g.steps.map((s) => `${s.key} :: ${s.command}`)),
    );
    const cmds = shown.flatMap((g) => g.steps.map((s) => s.command));
    check(
      `the commands are git's and gh's own (${cmds.length} steps: update-ref -d, push, gh pr close, gh repo archive)`,
      cmds.length === 7 &&
        cmds.some(
          (c) => c.startsWith('git -C ') && c.includes(' update-ref -d '),
        ) &&
        cmds.some((c) => c.startsWith('git -C ') && c.includes(' push ')) &&
        cmds.some((c) =>
          c.startsWith(
            "gh pr close 7 -R 'schuettc/t9-prs' --comment 'superseded by #9'",
          ),
        ) &&
        cmds.some((c) =>
          c.startsWith("gh repo archive 'schuettc/t9-dormant-a'"),
        ),
    );
    const laneColours = await pg.$$eval('.cb-group-label .cb-lanename', (els) =>
      els.map((e) => `${e.textContent}=${getComputedStyle(e).color}`),
    );
    const signal = await cssColor(pg, 'var(--kit-signal)');
    const agentC = await cssColor(pg, 'var(--kit-agent)');
    check(
      `lane labels keep their colours: casebook · local signal, pi · outward agent (${laneColours.join(', ')})`,
      laneColours.length === 4 &&
        laneColours.every((c) =>
          c.startsWith('casebook \u00b7 local=')
            ? c.endsWith(`=${signal}`)
            : c.endsWith(`=${agentC}`),
        ),
    );
    // No session is here: approving is impossible on the page.
    check(
      'with outward steps and no session here there is no approve: the page says so, and no primary',
      (await until(
        pg,
        () => !!document.querySelector('[data-testid="no-session"]'),
      )) &&
        !(await pg.$('.cb-approve .kit-btn.fill')) &&
        (await read.primary(pg)) === '',
    );
    await pressWithFocus(pg, 'a');
    await pg.waitForTimeout(400);
    check(
      '"a" with no session approves nothing (serve still has a plan)',
      (await job(jobId)).job.state === 'planned',
    );

    // Two sessions attach: the picker lists them; Court picks pi's.
    await agent.presence(SESSION, LABEL, '/home/court/t9', 'pi');
    await agent.presence(OTHER, OTHER_LABEL, '/home/court/else', 'claude');
    const keep = setInterval(() => {
      void agent.presence(SESSION, LABEL, '/home/court/t9', 'pi');
      void agent.presence(OTHER, OTHER_LABEL, '/home/court/else', 'claude');
    }, 1000);
    try {
      check(
        'the session picker lists the present sessions',
        await until(
          pg,
          ([a, b]) => {
            const ls = [
              ...document.querySelectorAll(
                '[data-testid="session-picker"] .cb-session',
              ),
            ].map((e) => e.textContent);
            return ls.length === 2 && ls.includes(a) && ls.includes(b);
          },
          [LABEL, OTHER_LABEL],
          8000,
        ),
      );
      await pg.click(`.cb-session[data-session="${SESSION}"]`);
      check(
        'choosing a session marks it, and the primary is "Approve"',
        await until(
          pg,
          (id) =>
            document
              .querySelector(`.cb-session[data-session="${id}"]`)
              ?.getAttribute('aria-checked') === 'true' &&
            document.querySelector('.kit-primary')?.textContent === 'Approve',
          SESSION,
        ),
      );
      checkList(
        "the outward groups name the chosen session's agent",
        await pg.$$eval('.cb-group-label .cb-lanename-agent', (els) =>
          els.map((e) => e.textContent),
        ),
        ['pi \u00b7 outward', 'pi \u00b7 outward'],
      );
      check(
        'the plan is attached to the composer ("job #N")',
        (await read.attached(pg)) === `job #${jobId}`,
      );
      // The screenshot: the plan's groups and commands, and approve with
      // its session picker.
      await pg.$eval('[data-testid="approve"]', (e) =>
        e.scrollIntoView({ block: 'end' }),
      );
      await shoot(t, pg, 'plan');

      // Approve from the keyboard: "a" is To apply's approve here (Attention
      // and Rules bind "a" too).
      await pressWithFocus(pg, 'a');
      check(
        `approving starts a job: serve has job ${jobId} approved for ${SESSION}`,
        await eventually(async () => {
          const j = (await job(jobId)).job;
          return j.state !== 'planned' && j.session === SESSION;
        }),
      );
      check(
        'the job opens in the running view, its primary "Pause job"',
        await until(
          pg,
          (id) =>
            document.querySelector('.cb-job')?.dataset.job === String(id) &&
            document
              .querySelector('.kit-chip[data-id="running"]')
              ?.classList.contains('on') &&
            document.querySelector('.kit-primary')?.textContent === 'Pause job',
          jobId,
          8000,
        ),
      );

      // The section fills the list and reading columns, as Rules does.
      const geo = await pg.evaluate(() => {
        const r = (sel) => {
          const b = document.querySelector(sel).getBoundingClientRect();
          return { x: b.x, y: b.y, w: b.width, h: b.height };
        };
        return {
          list: r('.cb-apply-list'),
          read: r('.cb-apply-read'),
          doc: r('.cb-apply-read .kit-doc'),
          rail: r('.kit-rail'),
          bar: r('.kit-bar'),
          others: [...document.querySelectorAll('.kit-app > .kit-list')]
            .filter((e) => !e.classList.contains('cb-apply-list'))
            .map((e) => getComputedStyle(e).display),
        };
      });
      check(
        `the list is the 400 px list column, bar to bottom; the job the reading column; the document 640 px, centred (${JSON.stringify(geo.list)} ${JSON.stringify(geo.read)} ${geo.doc.w})`,
        geo.list.x === 0 &&
          geo.list.w === 400 &&
          geo.list.y === geo.bar.h &&
          geo.list.y + geo.list.h === 900 &&
          geo.read.x === 400 &&
          geo.read.x + geo.read.w === geo.rail.x &&
          geo.doc.w === 640 &&
          Math.abs(geo.doc.x - (geo.read.x + (geo.read.w - 640) / 2)) <= 1,
      );
      check(
        `the other sections' lists are not displayed (${geo.others.join(',')})`,
        geo.others.length === 2 && geo.others.every((d) => d === 'none'),
      );

      // The casebook lane runs, for real, in the probe's clone.
      let jv = await job(jobId);
      const l1 = stepFor(jv, BR('feat/l1'), 'branch-delete-local');
      const l1r = stepFor(jv, BR('feat/l1'), 'branch-delete-remote');
      const prStep = stepFor(jv, PR);
      const repoA = stepFor(jv, REPO_A);
      check(
        'the local steps verify live: ✓ "deleted · verified" in the steps table',
        await until(
          pg,
          (id) =>
            document.querySelector(`.cb-step[data-step="${id}"]`)?.dataset
              .state === 'verified',
          l1.id,
          10000,
        ),
      );
      const l1Row = await read.step(pg, l1.id);
      check(
        `the verified local step reads ✓ deleted · verified (${l1Row?.glyph} ${l1Row?.text})`,
        l1Row?.glyph === '\u2713' && l1Row?.text === 'deleted \u00b7 verified',
      );
      check(
        'the casebook lane deleted feat/l1 in the probe clone (git, in the temp home)',
        gitIn(home, 't9-local', 'rev-parse --verify refs/heads/feat/l1') ===
          null &&
          gitIn(home, 't9-local', 'rev-parse --verify refs/heads/main') !==
            null,
      );
      check(
        'the pending agent steps read · next and · queued',
        await until(
          pg,
          ([a, b]) => {
            const t = (id) =>
              document.querySelector(`.cb-step[data-step="${id}"] .cb-step-t`)
                ?.textContent;
            return t(a) === 'next' && t(b) === 'queued';
          },
          [prStep.id, repoA.id],
        ),
      );

      // The list row: its lanes, the bar, n/total, what needs Court.
      const row = await pg.$eval(
        `.cb-apply-list .kit-row[data-job="${jobId}"]`,
        (e) => ({
          lanes: [...e.querySelectorAll('.cb-lanename')].map(
            (l) => `${l.textContent}=${getComputedStyle(l).color}`,
          ),
          bar: [...e.querySelectorAll('.cb-pb > span')].map(
            (s) => `${s.className}=${getComputedStyle(s).backgroundColor}`,
          ),
          barH: e.querySelector('.cb-pb')?.getBoundingClientRect().height,
          meta: e.querySelector('.kit-meta')?.textContent,
          sub: e.querySelector('.kit-sub')?.textContent,
          subColour: e.querySelector('.kit-sub')
            ? getComputedStyle(e.querySelector('.kit-sub')).color
            : '',
        }),
      );
      jv = await job(jobId);
      const finished = jv.job.steps.filter((s) =>
        ['verified', 'reported', 'skipped', 'failed'].includes(s.state),
      ).length;
      checkList('the row names its lanes in their colours', row.lanes, [
        `casebook \u00b7 local=${signal}`,
        `pi \u00b7 outward=${agentC}`,
      ]);
      check(
        `the row has a thin bar, the casebook lane's part in signal (${row.bar.join(', ')}; ${row.barH}px) and n/total ${row.meta}`,
        row.barH === 3 &&
          row.bar.length === 1 &&
          row.bar[0] === `cb-pb-casebook=${signal}` &&
          row.meta === `${finished}/${jv.job.steps.length}`,
      );
      check(
        `what needs Court is a signal line ("${row.sub}")`,
        row.sub === '1 needs you' && row.subColour === signal,
      );

      // The batch card: confirm.
      check(
        'a batch card needs Court (rust edge): confirm · skip batch',
        (await until(
          pg,
          () => !!document.querySelector('.cb-needs-card[data-kind="batch"]'),
        )) &&
          (await pg.$eval('.cb-needs-card[data-kind="batch"]', (e) =>
            [...e.querySelectorAll('.kit-btn')]
              .map((b) => b.textContent)
              .join(' \u00b7 '),
          )) === 'confirm \u00b7 skip batch',
      );
      await pg.click(
        '.cb-needs-card[data-kind="batch"] .kit-btn:has-text("confirm")',
      );
      check(
        'confirming clears the batch card (serve answered it)',
        (await until(
          pg,
          () => !document.querySelector('.cb-needs-card[data-kind="batch"]'),
        )) &&
          (await eventually(async () =>
            ((await job(jobId)).needs_you ?? []).every(
              (c) => c.kind !== 'batch',
            ),
          )),
      );

      // ---- pause and resume --------------------------------------------
      console.log('\nscenario: to apply — pause and resume');
      await pg.click('.kit-primary');
      check(
        'primary "Pause job" pauses it (serve) and becomes "Resume"',
        (await eventually(
          async () => (await job(jobId)).job.paused === true,
        )) &&
          (await until(
            pg,
            () =>
              document.querySelector('.kit-primary')?.textContent === 'Resume',
          )),
      );
      check(
        'the paused job says so in its kicker',
        (await pg.$eval('.cb-job .kit-kick', (e) => e.textContent)).endsWith(
          '\u00b7 paused',
        ),
      );
      await pressWithFocus(pg, 'p');
      check(
        '"p" resumes it: serve has it running, the primary is "Pause job" again',
        (await eventually(
          async () => (await job(jobId)).job.paused === false,
        )) &&
          (await until(
            pg,
            () =>
              document.querySelector('.kit-primary')?.textContent ===
              'Pause job',
          )),
      );

      // ---- a stale reload never draws over a newer one ------------------
      console.log('\nscenario: to apply — reloads in order');
      await pg.waitForTimeout(500); // the resume's own reloads settle
      let held = null;
      let arm = true;
      await pg.route(/\/api\/job\?/, async (route) => {
        if (arm) {
          arm = false;
          const resp = await route.fetch();
          const body = await resp.text();
          held = () => route.fulfill({ response: resp, body });
          return;
        }
        await route.continue();
      });
      await agent.api('POST', '/api/jobs/pause', { id: jobId });
      const heldOne = await eventually(async () => !!held);
      const newer = pg.waitForResponse((r) => r.url().includes('/api/job?'));
      await agent.api('POST', '/api/jobs/resume', { id: jobId });
      await newer;
      await pg.waitForTimeout(200);
      const before = await read.primary(pg);
      await held?.();
      await pg.waitForTimeout(600);
      await pg.unroute(/\/api\/job\?/);
      check(
        `an older reply (paused) arriving after a newer one (running) is not drawn (primary "${before}" → "${await read.primary(pg)}")`,
        heldOne &&
          before === 'Pause job' &&
          (await read.primary(pg)) === 'Pause job' &&
          !(await pg.$eval('.cb-job .kit-kick', (e) => e.textContent)).endsWith(
            'paused',
          ),
      );

      // ---- a needs-you card (spec §10) ----------------------------------
      console.log('\nscenario: to apply — a needs-you card');
      await agent.api('POST', '/api/agent/job-step', {
        session: SESSION,
        job: jobId,
        step: prStep.id,
        state: 'started',
      });
      const draft =
        'Closing this: lodash 4.17.21 is already on main through #9. Thanks!';
      await agent.api('POST', '/api/agent/job-ask', {
        session: SESSION,
        job: jobId,
        step: prStep.id,
        question: "public comment on someone else's repo",
        text: draft,
      });
      check(
        'a needs-you card offers the four actions (a fake casebook_job_ask)',
        (await until(
          pg,
          () => !!document.querySelector('.cb-needs-card[data-kind="text"]'),
          undefined,
          8000,
        )) &&
          (await pg.$eval('.cb-needs-card[data-kind="text"]', (e) =>
            [...e.querySelectorAll('.kit-btn')]
              .map((b) => b.textContent)
              .join(' \u00b7 '),
          )) ===
            'post and close \u00b7 edit text \u00b7 close without comment \u00b7 skip',
      );
      const textCard = await pg.$eval(
        '.cb-needs-card[data-kind="text"]',
        (e) => ({
          edge: getComputedStyle(e).borderLeftColor,
          edgeW: getComputedStyle(e).borderLeftWidth,
          head: e.querySelector('.kit-card-head').textContent,
          quote: e.querySelector('blockquote').textContent,
          what: e.querySelector('.cb-needs-what').textContent,
          fill: e.querySelector('.kit-btn.fill')?.textContent,
        }),
      );
      check(
        `the card has a rust edge (${textCard.edge}, ${textCard.edgeW}) and quotes the exact text`,
        textCard.edge === signal &&
          textCard.edgeW === '3px' &&
          textCard.quote === draft &&
          textCard.fill === 'post and close',
      );
      check(
        `it says what and where ("${textCard.head}"; "${textCard.what}")`,
        textCard.head ===
          "needs you \u00b7 public comment on someone else's repo" &&
          textCard.what === 'schuettc/t9-prs#7 \u00b7 close with comment',
      );
      await pg.$eval('.cb-job .kit-kick', (e) =>
        e.scrollIntoView({ block: 'start' }),
      );
      await shoot(t, pg, 'job');
      // edit text: the text becomes a field; saving sends it to serve.
      await pg.click(
        '.cb-needs-card[data-kind="text"] .kit-btn:has-text("edit text")',
      );
      const edited = 'Closing: already on main through #9.';
      await pg.fill(
        '.cb-needs-card[data-kind="text"] textarea.cb-needs-edit',
        edited,
      );
      await pg.click(
        '.cb-needs-card[data-kind="text"] .kit-btn:has-text("save text")',
      );
      check(
        'edit text saves the new text to serve; the card stays, quoting it',
        (await eventually(async () =>
          ((await job(jobId)).needs_you ?? []).some(
            (c) => c.kind === 'text' && c.text === edited,
          ),
        )) &&
          (await until(
            pg,
            (w) =>
              document.querySelector(
                '.cb-needs-card[data-kind="text"] blockquote',
              )?.textContent === w,
            edited,
          )),
      );
      await pg.click(
        '.cb-needs-card[data-kind="text"] .kit-btn:has-text("post and close")',
      );
      check(
        'answering closes the card: "post and close" clears it (page and serve)',
        (await until(
          pg,
          () => !document.querySelector('.cb-needs-card[data-kind="text"]'),
        )) &&
          (await eventually(async () =>
            ((await job(jobId)).needs_you ?? []).every(
              (c) => c.kind !== 'text',
            ),
          )),
      );
      check(
        'serve took the approved text for the step',
        await eventually(
          async () => stepFor(await job(jobId), PR).text === edited,
        ),
      );
      // The agent posts and reports; serve verifies with a fresh gh read
      // (the probe's fake gh) and the step advances.
      await agent.api('POST', '/api/agent/job-step', {
        session: SESSION,
        job: jobId,
        step: prStep.id,
        state: 'reported',
      });
      check(
        'the step advances: ✓ closed · verified',
        await until(
          pg,
          (id) =>
            document.querySelector(`.cb-step[data-step="${id}"] .cb-step-t`)
              ?.textContent === 'closed \u00b7 verified',
          prStep.id,
          8000,
        ),
      );
      checkList(
        'serve verified it with one gh read, to the probe fake (nothing else ran gh)',
        serveHandle.ghCalls(),
        ['pr view 7 -R schuettc/t9-prs --json state'],
      );

      // The agent pauses a step with its reason: an amber card.
      await agent.api('POST', '/api/agent/job-step', {
        session: SESSION,
        job: jobId,
        step: repoA.id,
        state: 'started',
      });
      await agent.api('POST', '/api/agent/job-step', {
        session: SESSION,
        job: jobId,
        step: repoA.id,
        state: 'paused',
        detail: 'a reviewer commented 2 days ago, after you decided.',
      });
      check(
        'a paused step is an amber "pi paused" card: archive anyway (danger) · skip',
        (await until(
          pg,
          () => !!document.querySelector('.cb-needs-card[data-kind="paused"]'),
        )) &&
          (await pg.$eval('.cb-needs-card[data-kind="paused"]', (e) => {
            const bs = [...e.querySelectorAll('.kit-btn')];
            return [
              e.querySelector('.kit-card-head').textContent,
              getComputedStyle(e).borderLeftColor,
              bs
                .map(
                  (b) =>
                    b.textContent + (b.classList.contains('danger') ? '!' : ''),
                )
                .join(' \u00b7 '),
            ].join(' | ');
          })) === `pi paused | ${agentC} | archive anyway! \u00b7 skip`,
      );
      check(
        'the steps table shows it ‖ paused, and the row "1 paused"',
        (await until(
          pg,
          (id) =>
            document.querySelector(`.cb-step[data-step="${id}"] .cb-step-g`)
              ?.textContent === '\u2016',
          repoA.id,
        )) &&
          (await until(
            pg,
            (id) =>
              document.querySelector(
                `.cb-apply-list .kit-row[data-job="${id}"] .kit-sub`,
              )?.textContent === '1 paused',
            jobId,
          )),
      );
      await pg.click(
        '.cb-needs-card[data-kind="paused"] .kit-btn:has-text("skip")',
      );
      check(
        'skip clears the paused card and skips the step',
        (await until(
          pg,
          () => !document.querySelector('.cb-needs-card[data-kind="paused"]'),
        )) &&
          (await eventually(
            async () => stepFor(await job(jobId), REPO_A).state === 'skipped',
          )),
      );

      // ---- undo ----------------------------------------------------------
      console.log('\nscenario: to apply — undo');
      jv = await job(jobId);
      const undoable = jv.job.steps.filter((s) => s.undoable).map((s) => s.id);
      const withUndo = await pg.$$eval('.cb-step', (els) =>
        els
          .filter((e) => e.querySelector('.cb-undo'))
          .map((e) => Number(e.dataset.step)),
      );
      checkList(
        'undo shows on exactly the finished steps serve can undo (the local deletes, not the closed PR)',
        withUndo,
        undoable,
      );
      check(
        '(serve can undo the local and remote deletes, not the PR)',
        undoable.includes(l1.id) &&
          undoable.includes(l1r.id) &&
          !undoable.includes(prStep.id),
      );
      const undoPost = pg.waitForResponse((r) =>
        r.url().includes('/api/jobs/undo'),
      );
      await pg.click(`.cb-step[data-step="${l1.id}"] .cb-undo`);
      const undoBody = (await undoPost).request().postDataJSON();
      check(
        `undo posts /api/jobs/undo for the step (${JSON.stringify(undoBody)})`,
        undoBody?.step === l1.id,
      );
      check(
        'serve restored feat/l1 in the probe clone (git branch, in the temp home)',
        await eventually(
          async () =>
            gitIn(home, 't9-local', 'rev-parse --verify refs/heads/feat/l1') ===
            l1.expected_tip,
        ),
      );
      check(
        'the step reads ↺ undone, with no undo',
        await until(
          pg,
          (id) => {
            const e = document.querySelector(`.cb-step[data-step="${id}"]`);
            return (
              e?.querySelector('.cb-step-t')?.textContent === 'undone' &&
              !e.querySelector('.cb-undo')
            );
          },
          l1.id,
        ),
      );

      // ---- sections: context, keys, primary -----------------------------
      console.log(
        '\nscenario: to apply — only the active section feeds the composer',
      );
      check(
        'the open job is attached to the composer',
        (await read.attached(pg)) === `job #${jobId}`,
      );
      await pg.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await until(
        pg,
        () =>
          document
            .querySelector('.kit-ctl[data-id="attention"]')
            ?.classList.contains('on') ?? false,
      );
      const onAttention = await read.attached(pg);
      const primaryAttention = await read.primary(pg);
      check(
        `on Attention the bar's primary is Attention's, not "Pause job" ("${primaryAttention}")`,
        primaryAttention !== 'Pause job' && primaryAttention !== 'Resume',
      );
      // A live event under the hidden section: the job pauses.
      await agent.api('POST', '/api/jobs/pause', { id: jobId });
      await pg.waitForTimeout(800);
      check(
        `a live job event while To apply is hidden leaves the attached line alone ("${onAttention}")`,
        (await read.attached(pg)) === onAttention &&
          (await read.primary(pg)) === primaryAttention &&
          !onAttention.includes(`job #${jobId}`),
      );
      await pressWithFocus(pg, 'p');
      await pg.waitForTimeout(500);
      check(
        'on Attention, "p" does not resume the hidden job',
        (await job(jobId)).job.paused === true,
      );
      await pg.evaluate((id) => {
        location.hash = `#/apply/${id}`;
      }, jobId);
      check(
        'back on To apply the job is attached again and the primary follows its state ("Resume")',
        await until(
          pg,
          (id) =>
            document.querySelector('[data-testid="composer-attached"]')
              ?.textContent === `job #${id}` &&
            document.querySelector('.kit-primary')?.textContent === 'Resume',
          jobId,
        ),
      );
      const overlay = async () => {
        await pressWithFocus(pg, '?');
        await until(pg, () => !!document.querySelector('.kit-keys'));
        const labels = await pg.$$eval(
          '.kit-keys .kit-keys-row > span:last-child',
          (els) => els.map((e) => e.textContent),
        );
        await pg.keyboard.press('Escape');
        await until(pg, () => !document.querySelector('.kit-keys'));
        return labels;
      };
      const onApply = await overlay();
      check(
        `on To apply the overlay lists its keys, not Attention's or Rules' (${onApply.join(', ')})`,
        onApply.includes('approve the open plan') &&
          onApply.includes('pause or resume the open job') &&
          !onApply.includes('accept proposal') &&
          !onApply.includes('activate the open draft'),
      );
      await pressWithFocus(pg, 'p');
      check(
        'back on To apply, "p" resumes it',
        await eventually(async () => (await job(jobId)).job.paused === false),
      );
      await pg.evaluate(() => {
        location.hash = '#/rules';
      });
      await until(
        pg,
        () =>
          document
            .querySelector('.kit-ctl[data-id="rules"]')
            ?.classList.contains('on') ?? false,
      );
      check(
        "on Rules the primary isn't To apply's",
        !['Pause job', 'Resume', 'Approve'].includes(await read.primary(pg)),
      );

      // A second plan: "a" on Attention doesn't approve it.
      const second = await agent.api('POST', '/api/apply/plan', {
        keys: [REPO_B],
      });
      await pg.evaluate((id) => {
        location.hash = `#/apply/${id}`;
      }, second.job.id);
      await until(
        pg,
        (id) => document.querySelector('.cb-plan')?.dataset.job === String(id),
        second.job.id,
      );
      await pg.click(`.cb-session[data-session="${SESSION}"]`);
      await until(
        pg,
        () => document.querySelector('.kit-primary')?.textContent === 'Approve',
      );
      await pg.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await until(
        pg,
        () =>
          document
            .querySelector('.kit-ctl[data-id="attention"]')
            ?.classList.contains('on') ?? false,
      );
      await pressWithFocus(pg, 'a');
      await pg.waitForTimeout(600);
      check(
        'on Attention, "a" does not approve the hidden open plan',
        (await job(second.job.id)).job.state === 'planned',
      );
    } finally {
      clearInterval(keep);
    }
    check(`no page errors (${errors.join(' | ')})`, errors.length === 0);
  } finally {
    await pg.close();
  }
}

// staleScenario: serve refuses a plan from an observation older than one
// sync interval (2s here); the page shows serve's words, which name a sync
// and no command.
async function staleScenario(context, t, serveHandle) {
  const { check, until } = t;
  console.log(
    '\nscenario: to apply — a stale plan is refused in serve\u2019s words',
  );
  const agent = createAgent(serveHandle.base, serveHandle.token);
  await agent.api('POST', '/api/decide', {
    keys: ['repo:schuettc/t9-stale'],
    disposition: 'archive',
  });
  const pg = await context.newPage();
  try {
    await pg.setViewportSize({ width: 1600, height: 900 });
    await pg.goto(serveHandle.url + '#/apply', {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    });
    await pg.click('.kit-chip[data-id="ready"]');
    await until(
      pg,
      () => document.querySelector('.kit-primary')?.textContent === 'Plan all',
    );
    // Past the sync interval since the index was built.
    await pg.waitForTimeout(2600);
    const resp = pg.waitForResponse((r) => r.url().includes('/api/apply/plan'));
    await pg.click('.kit-primary');
    const r = await resp;
    const body = await r.json();
    check(`serve refuses the plan (409: ${body.error})`, r.status() === 409);
    const shown = (await until(
      pg,
      () => !!document.querySelector('[data-testid="plan-refused"]'),
    ))
      ? await pg.$eval('[data-testid="plan-refused"]', (e) => e.textContent)
      : '';
    check(
      `the page shows serve's message verbatim ("${shown}")`,
      shown === body.error && shown.includes('sync'),
    );
    check(
      'it names a sync and no command for Court to run',
      !/\bgit |\bgh |casebook |`|\$/.test(shown),
    );
    check(
      'no job was made',
      ((await agent.api('GET', '/api/jobs')).jobs ?? []).length === 0,
    );
  } finally {
    await pg.close();
  }
}
