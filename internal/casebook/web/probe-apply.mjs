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

import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startServe, probeGit } from './serve.mjs';
import { createAgent } from './agent.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const hashOf = (f) =>
  createHash('sha256').update(readFileSync(f)).digest('hex');

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

// shoot takes the light and the dark screenshot (or only those named),
// asserting the theme. Focus is let go first: a focused control's ring is
// the probe's click, not the page.
async function shoot(t, pg, name, themes = ['light', 'dark']) {
  const bgs = { light: 'rgb(244, 245, 248)', dark: 'rgb(20, 22, 29)' };
  for (const theme of themes) {
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
    await pg.evaluate(() => document.activeElement?.blur());
    const focused = await pg.evaluate(
      () => document.activeElement === document.body,
    );
    t.check(
      `/tmp/t9-${name}-${theme}.png is ${theme} (theme ${got.theme}, body ${got.bg}), nothing focused`,
      got.theme === theme && got.bg === bgs[theme] && focused,
    );
    await pg.screenshot({ path: `/tmp/t9-${name}-${theme}.png` });
  }
  for (let i = 0; i < 3; i++) {
    const th = await pg.evaluate(() => document.documentElement.dataset.theme);
    if (th === 'light') break;
    await pg.click('button.kit-ctl:has-text("theme")');
  }
}

// gitIn runs git in a probe clone (under the serve's temp home only),
// hermetically (serve.mjs probeGit).
function gitIn(home, clone, args) {
  try {
    return probeGit(join(home, 'clones', clone), args, home);
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

export async function applyScenarios(shared, t) {
  // A browser context of their own: another scenario's fake clock (a
  // context's clock is shared by its pages) must not age these pages'
  // observations, and the foot reads the real, advancing time.
  const context = await shared.browser().newContext();
  try {
    await applyScenariosIn(context, t);
  } finally {
    await context.close();
  }
}

async function applyScenariosIn(context, t) {
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
    const footColour = () =>
      pg.$eval('.cb-apply-foot-n', (e) => ({
        text: e.textContent,
        colour: getComputedStyle(e).color,
      }));
    const fgC = await cssColor(pg, 'var(--kit-fg)');
    const signalC = await cssColor(pg, 'var(--kit-signal)');
    const plainFoot = await footColour();
    check(
      `with nothing selected the foot's total is plain text, not the selection's signal ("${plainFoot.text}", ${plainFoot.colour})`,
      plainFoot.text === '5 decided items' &&
        plainFoot.colour === fgC &&
        fgC !== signalC,
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
    const selFoot = await footColour();
    check(
      `a selection's count is in signal (${selFoot.colour})`,
      selFoot.colour === signalC,
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
    const planPosts = [];
    const onPlanPost = (r) => {
      if (r.method() === 'POST' && r.url().includes('/api/apply/plan'))
        planPosts.push(r.url());
    };
    pg.on('request', onPlanPost);
    // Twice, fast: a double press.
    await pg.click('.kit-primary', { clickCount: 2 });
    const planView = await (await planResp).json();
    const jobId = planView.job.id;
    await pg.waitForTimeout(500);
    pg.off('request', onPlanPost);
    const jobsAfterPlan = (await agent.api('GET', '/api/jobs')).jobs ?? [];
    check(
      `a double press of "Plan all" asks serve once (${planPosts.length} POST /api/apply/plan, ${jobsAfterPlan.length} plan)`,
      planPosts.length === 1 && jobsAfterPlan.length === 1,
    );
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
    // The decided items it holds are marked as in it, and not plannable.
    const readyRows = await pg.$$eval(
      '.cb-apply-list .kit-row[data-key]',
      (els) =>
        els.map((e) => ({
          key: e.dataset.key,
          meta: e.querySelector('.kit-meta')?.textContent,
          box: !!e.querySelector('.kit-box'),
        })),
    );
    check(
      `the items in the plan say so ("in plan #${jobId}") and can't be selected (${readyRows.map((r) => r.meta).join(', ')})`,
      readyRows.length === 5 &&
        readyRows.every((r) => r.meta === `in plan #${jobId}` && !r.box),
    );
    const footAfterPlan = await pg.$eval('.cb-apply-foot-sel', (e) => ({
      n: e.querySelector('.cb-apply-foot-n')?.textContent,
      btns: [...e.querySelectorAll('.kit-btn')].map((b) => b.textContent),
    }));
    check(
      `nothing is left for "plan all": the foot says "${footAfterPlan.n}" and offers no plan (${footAfterPlan.btns.join(',') || 'none'})`,
      footAfterPlan.n === '5 decided items in a plan' &&
        footAfterPlan.btns.length === 0,
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
    const noSession = await pg.$eval(
      '[data-testid="no-session"]',
      (e) => e.textContent,
    );
    check(
      `its verb agrees with the count ("${noSession}")`,
      noSession.startsWith(
        'The 3 outward steps go to an agent session, and none is here.',
      ),
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
      const picker = await pg.evaluate(() => ({
        checked: [
          ...document.querySelectorAll(
            '.cb-session[aria-checked="true"], .cb-session.on',
          ),
        ].length,
        approve: [...document.querySelectorAll('.cb-approve .kit-btn')].map(
          (b) => b.textContent,
        ),
        pick: document.querySelector('[data-testid="pick-session"]')
          ?.textContent,
        what: document.querySelector('.cb-approve-what')?.textContent,
      }));
      check(
        `with two sessions here none is chosen for Court: no session marked, no approve, only discard (${picker.approve.join(',')}; "${picker.pick}")`,
        picker.checked === 0 &&
          picker.approve.join(',') === 'discard' &&
          picker.pick === '2 sessions are here and none is chosen yet.' &&
          (await read.primary(pg)) === '',
      );
      check(
        `the sentence's verb agrees ("${picker.what}")`,
        picker.what ===
          '7 steps \u00b7 4 local \u00b7 3 outward. The 3 outward steps go to:',
      );
      await pressWithFocus(pg, 'a');
      check(
        '"a" with no session chosen is refused: the page says why, serve still has a plan',
        (await until(
          pg,
          () =>
            document.querySelector('.cb-approve .cb-apply-note')
              ?.textContent ===
            'not approved: no session is chosen for the outward steps',
        )) && (await job(jobId)).job.state === 'planned',
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
        'with a session chosen the plan offers approve and discard',
        (await pg.$$eval('.cb-approve .kit-btn', (bs) =>
          bs.map((b) => b.textContent).join(','),
        )) === 'approve,discard',
      );
      const planTitle = await pg.$eval(
        '.cb-plan .kit-h1',
        (e) => e.textContent,
      );
      check(
        `the plan is attached to the composer with its title ("${await read.attached(pg)}")`,
        (await read.attached(pg)) ===
          `job #${jobId} \u00b7 ${planTitle.toLowerCase()}`,
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
      const tones = await pg.$eval(`.cb-step[data-step="${l1.id}"]`, (e) => ({
        glyph: getComputedStyle(e.querySelector('.cb-step-g')).color,
        text: getComputedStyle(e.querySelector('.cb-step-t')).color,
      }));
      const okC = await cssColor(pg, 'var(--kit-ok)');
      const mutedC = await cssColor(pg, 'var(--kit-muted)');
      check(
        `only the glyph carries the step's tone: ✓ in ok (${tones.glyph}), its text muted (${tones.text})`,
        tones.glyph === okC && tones.text === mutedC && okC !== mutedC,
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

      // ---- a burst of live events -----------------------------------------
      console.log('\nscenario: to apply — a burst of live events');
      await pg.waitForTimeout(500); // the resume's own reloads settle
      const fetched = [];
      const onFetch = (r) => {
        const u = r.url();
        if (/\/api\/(jobs|job|needs-you)(\?|$)/.test(u))
          fetched.push(new URL(u).pathname);
      };
      pg.on('request', onFetch);
      // 30 job events about the open job, at once (a resume of a job that
      // isn't paused changes nothing and announces it), then a pause.
      const BURST = 30;
      await Promise.all(
        Array.from({ length: BURST }, () =>
          agent.api('POST', '/api/jobs/resume', { id: jobId }),
        ),
      );
      await agent.api('POST', '/api/jobs/pause', { id: jobId });
      const settled = await until(
        pg,
        () => document.querySelector('.kit-primary')?.textContent === 'Resume',
      );
      await pg.waitForTimeout(600);
      pg.off('request', onFetch);
      check(
        `${BURST + 1} live events fetch a bounded few times, one reload in flight and one queued (${fetched.length} fetches), and the page ends current (paused)`,
        settled && fetched.length >= 1 && fetched.length <= 4,
      );
      check(
        `an event about a job fetches that job only, never the whole list (${[...new Set(fetched)].join(', ')})`,
        fetched.every((p) => p === '/api/job'),
      );
      await agent.api('POST', '/api/jobs/resume', { id: jobId });
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Pause job',
      );

      // ---- a stale reload never draws over a newer one ------------------
      console.log('\nscenario: to apply — reloads in order');
      await pg.waitForTimeout(500);
      // Hold every /api/job reload: the first (running) is released after
      // Court's own pause has drawn, the rest after the check.
      const heldJob = [];
      await pg.route(/\/api\/job\?/, async (route) => {
        const resp = await route.fetch();
        const body = await resp.text();
        heldJob.push(() =>
          route.fulfill({ response: resp, body }).catch(() => {}),
        );
      });
      // An event about the job (a resume that changes nothing): a reload,
      // held, whose reply says running.
      await agent.api('POST', '/api/jobs/resume', { id: jobId });
      const heldOne = await eventually(async () => heldJob.length >= 1);
      // Court pauses: serve's reply (paused) draws.
      await pg.click('.kit-primary');
      const pausedDrawn = await until(
        pg,
        () => document.querySelector('.kit-primary')?.textContent === 'Resume',
      );
      await heldJob.shift()?.();
      await pg.waitForTimeout(500);
      const after = await read.primary(pg);
      const kick = await pg.$eval('.cb-job .kit-kick', (e) => e.textContent);
      for (const f of heldJob.splice(0)) await f();
      await pg.unroute(/\/api\/job\?/);
      check(
        `an older reload (running) answered after Court's pause (paused) is not drawn (primary "${after}", kicker "${kick}")`,
        heldOne && pausedDrawn && after === 'Resume' && kick.endsWith('paused'),
      );
      await pg.click('.kit-primary');
      await eventually(async () => (await job(jobId)).job.paused === false);
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Pause job',
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
      const field = '.cb-needs-card[data-kind="text"] textarea.cb-needs-edit';
      await pg.fill(field, '');
      await pg.keyboard.type('Closing: already ');
      // While Court types, the job moves: a step starts (a step event) and
      // job events arrive; each redraws the open job.
      const fieldBefore = await pg.$(field);
      await agent.api('POST', '/api/agent/job-step', {
        session: SESSION,
        job: jobId,
        step: repoA.id,
        state: 'started',
      });
      for (let i = 0; i < 5; i++)
        await agent.api('POST', '/api/jobs/resume', { id: jobId });
      const redrawn = await until(
        pg,
        (id) =>
          document.querySelector(`.cb-step[data-step="${id}"]`)?.dataset
            .state === 'running',
        repoA.id,
      );
      await pg.waitForTimeout(300);
      const replaced = await pg.evaluate(
        ([el, sel]) => el !== document.querySelector(sel),
        [fieldBefore, field],
      );
      await pg.keyboard.type('on main through #9.');
      const ed = await pg
        .$eval(field, (e) => ({
          value: e.value,
          focused: document.activeElement === e,
          caret: e.selectionStart,
        }))
        .catch(() => ({ value: '(no field)', focused: false, caret: -1 }));
      check(
        `typing in "edit text" while step events redraw the job: the text, focus and caret survive ("${ed.value}", focused ${ed.focused}, caret ${ed.caret}; redrawn ${redrawn && replaced})`,
        redrawn &&
          replaced &&
          ed.value === edited &&
          ed.focused &&
          ed.caret === edited.length,
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

      // The agent pauses the step it started with its reason: an amber card.
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
        'the open job is attached to the composer, with its title',
        (await read.attached(pg)) ===
          `job #${jobId} \u00b7 ${planTitle.toLowerCase()}`,
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
          (want) =>
            document.querySelector('[data-testid="composer-attached"]')
              ?.textContent === want &&
            document.querySelector('.kit-primary')?.textContent === 'Resume',
          `job #${jobId} \u00b7 ${planTitle.toLowerCase()}`,
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

      // ---- discarding a plan ---------------------------------------------
      console.log('\nscenario: to apply — a plan can be discarded');
      await pg.evaluate((id) => {
        location.hash = `#/apply/${id}`;
      }, second.job.id);
      await until(
        pg,
        (id) => document.querySelector('.cb-plan')?.dataset.job === String(id),
        second.job.id,
      );
      const one = await pg.$eval('.cb-approve-what', (e) => e.textContent);
      check(
        `one outward step: "goes" ("${one}")`,
        one === '1 step \u00b7 1 outward. The outward step goes to:',
      );
      check(
        `the item in it is marked in the ready list ("in plan #${second.job.id}")`,
        (await pg.$eval(
          `.cb-apply-list .kit-row[data-key="${REPO_B}"] .kit-meta`,
          (e) => e.textContent,
        )) === `in plan #${second.job.id}`,
      );
      const cancelPost = pg
        .waitForResponse((r) => r.url().includes('/api/apply/cancel'))
        .catch(() => null);
      await pg.click('.cb-approve .kit-btn:has-text("discard")');
      const cancelBody = (await cancelPost)?.request().postDataJSON();
      check(
        `discard posts /api/apply/cancel for the plan (${JSON.stringify(cancelBody)}) and serve has it cancelled`,
        cancelBody?.plan_id === second.job.id &&
          (await eventually(
            async () => (await job(second.job.id)).job.state === 'cancelled',
          )),
      );
      check(
        'the ready list shows again, without the plan, and its item is plannable again',
        await until(
          pg,
          ([id, key]) =>
            location.hash === '#/apply' &&
            document
              .querySelector('.kit-chip[data-id="ready"]')
              ?.classList.contains('on') &&
            !document.querySelector(
              `.cb-apply-list .kit-row[data-job="${id}"]`,
            ) &&
            document.querySelector(
              `.cb-apply-list .kit-row[data-key="${key}"] .kit-meta`,
            )?.textContent === 'archive' &&
            !!document.querySelector(
              `.cb-apply-list .kit-row[data-key="${key}"] .kit-box`,
            ),
          [second.job.id, REPO_B],
        ),
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
// and no command, and offers "sync first". The sync is serve's own (POST
// /api/sync, App.Sync) and hermetic: this serve's casebook-data remote is
// the committed bundle on disk, it scans no roots, and its gh is the probe's
// fake. When it is done the plan is built again, once.
async function staleScenario(context, t, serveHandle) {
  const { check, checkList, until, eventually } = t;
  console.log(
    '\nscenario: to apply — a stale plan is refused in serve\u2019s words, and offers sync first',
  );
  const agent = createAgent(serveHandle.base, serveHandle.token);
  const pg = await context.newPage();
  const errors = [];
  pg.on('pageerror', (e) => errors.push(String(e)));
  try {
    await pg.setViewportSize({ width: 1600, height: 900 });
    await pg.goto(serveHandle.url + '#/apply', {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    });
    await pg.click('.kit-chip[data-id="ready"]');
    const okC = await cssColor(pg, 'var(--kit-ok)');
    const mutedC = await cssColor(pg, 'var(--kit-muted)');
    // Court decides with the page open (and live): serve rebuilds its index
    // (built_at now) and says so; the page's foot follows serve's
    // observation age.
    await until(
      pg,
      () => document.querySelector('.kit-live')?.dataset.state === 'live',
    );
    await agent.api('POST', '/api/decide', {
      keys: ['repo:schuettc/t9-stale'],
      disposition: 'archive',
    });
    const sum0 = await agent.api('GET', '/api/summary');
    check(
      `serve gives the page its sync interval (${sum0.sync_interval_ms} ms) and the index's age (built_at)`,
      sum0.sync_interval_ms === 2000 && !!Date.parse(sum0.built_at),
    );
    const freshSeen = await until(
      pg,
      (ok) => {
        const e = document.querySelector('.cb-apply-obs');
        return (
          e?.textContent === 'observations fresh' &&
          e.dataset.state === 'fresh' &&
          getComputedStyle(e).color === ok &&
          document.querySelector('.kit-primary')?.textContent === 'Plan all'
        );
      },
      okC,
      1800,
    );
    const obsNow = await pg.$eval('.cb-apply-obs', (e) => e.textContent);
    check(
      `within one sync interval of the rebuild the foot says the observations are fresh, in ok ("${obsNow}")`,
      freshSeen,
    );
    // Past the sync interval since the index was built: the foot says how
    // old they are, on its own (no event), muted.
    check(
      'past one sync interval the foot says how old the observations are, muted',
      await until(
        pg,
        (muted) => {
          const e = document.querySelector('.cb-apply-obs');
          return (
            /^observations \d+s old$/.test(e?.textContent ?? '') &&
            e.dataset.state === 'stale' &&
            getComputedStyle(e).color === muted
          );
        },
        mutedC,
        5000,
      ),
    );
    // serve agrees: its index is older than the interval.
    await eventually(async () => {
      const s = await agent.api('GET', '/api/summary');
      return Date.now() - Date.parse(s.built_at) > 2300;
    });
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
    const offer = await pg.$eval('[data-testid="refusal"]', (e) => ({
      edge: getComputedStyle(e).borderLeftColor,
      btns: [...e.querySelectorAll('.kit-btn')].map(
        (b) => b.textContent + (b.classList.contains('fill') ? '*' : ''),
      ),
    }));
    check(
      `the refusal offers "sync first" (${offer.btns.join(', ')})`,
      offer.btns.join(',') === 'sync first*',
    );
    const around = await pg.evaluate(() => ({
      filled: [
        ...document.querySelectorAll(
          '[data-testid="apply-overview"] .kit-btn.fill',
        ),
      ].map((b) => b.textContent),
      primary: document.querySelector('.kit-primary')?.textContent,
    }));
    check(
      `refused, sync first is the one offer: the only filled button in the document, and the bar's primary (${around.filled.join(', ')}; "${around.primary}")`,
      around.filled.join(',') === 'sync first' &&
        around.primary === 'Sync first',
    );
    check(
      'no job was made',
      ((await agent.api('GET', '/api/jobs')).jobs ?? []).length === 0,
    );
    await shoot(t, pg, 'stale', ['light']);

    // sync first: serve syncs (once), the page says so, and the plan is
    // built again when it is done.
    const ghBefore = serveHandle.ghCalls().length;
    const bundle = join(here, 'testdata', 'home', 'data', 'repo.bundle');
    const bundleBefore = hashOf(bundle);
    await pg.evaluate(() => {
      window.__sawSyncing = false;
      new MutationObserver(() => {
        if (document.querySelector('[data-testid="syncing"]'))
          window.__sawSyncing = true;
      }).observe(document.body, { subtree: true, childList: true });
    });
    const syncPosts = [];
    const planPosts = [];
    const onReq = (q) => {
      if (q.method() !== 'POST') return;
      if (q.url().includes('/api/sync')) syncPosts.push(q.url());
      if (q.url().includes('/api/apply/plan')) planPosts.push(q.url());
    };
    pg.on('request', onReq);
    const replanned = pg
      .waitForResponse(
        (x) => x.url().includes('/api/apply/plan') && x.status() === 200,
        { timeout: 30000 },
      )
      .catch(() => null);
    await pg.click('[data-testid="refusal"] .kit-btn:has-text("sync first")');
    const plan = await (await replanned)?.json().catch(() => null);
    await pg.waitForTimeout(300);
    pg.off('request', onReq);
    check(
      `sync first asks serve to sync once (${syncPosts.length} POST /api/sync), and the page said it was syncing`,
      syncPosts.length === 1 &&
        (await pg.evaluate(() => window.__sawSyncing === true)),
    );
    check(
      `when the sync is done the plan is built again, once, and opens (${planPosts.length} POST /api/apply/plan; job ${plan?.job?.id})`,
      planPosts.length === 1 &&
        !!plan &&
        (await until(
          pg,
          (id) =>
            location.hash === `#/apply/${id}` &&
            document.querySelector('.cb-plan')?.dataset.job === String(id),
          plan.job.id,
        )),
    );
    checkList(
      "the plan is the refused one: the stale repo's archive",
      (plan?.job?.steps ?? []).map((st) => `${st.action} ${st.key}`),
      ['repo-archive repo:schuettc/t9-stale'],
    );
    const sum1 = await agent.api('GET', '/api/summary');
    check(
      `serve's index was rebuilt by the sync (built_at ${sum0.built_at} → ${sum1.built_at}) and it is no longer syncing`,
      Date.parse(sum1.built_at) > Date.parse(sum0.built_at) + 2000 &&
        sum1.syncing === false,
    );
    check(
      'serve has one plan (no second sync, no second plan)',
      ((await agent.api('GET', '/api/jobs')).jobs ?? []).length === 1,
    );
    // Nothing outward ran: every gh call went to the probe's fake, each a
    // read (the fake refuses what it doesn't know), each with the
    // fail-closed environment; casebook-data's remote is the bundle on disk.
    const ghSync = serveHandle.ghCalls().slice(ghBefore);
    const outward =
      /\b(archive|unarchive|close|reopen|merge|delete|edit|create|comment|review)\b|(^| )(-X|--method)( |$)/;
    check(
      `nothing outward ran in the sync: ${ghSync.length} gh calls, all to the fake, none that changes GitHub (${[...new Set(ghSync.map((c) => c.split(' ').slice(0, 2).join(' ')))].join('; ')})`,
      ghSync.length > 0 && ghSync.every((c) => !outward.test(c)),
    );
    const env = serveHandle.ghEnv();
    check(
      `every gh call saw the fail-closed environment (GH_HOST github.invalid, a token that isn't one, the probe's gh config; ${env.length} calls)`,
      env.length === serveHandle.ghCalls().length &&
        env.every(
          (l) =>
            l ===
            `github.invalid probe-not-a-token ${join(serveHandle.home, 'gh')}`,
        ),
    );
    const origin = probeGit(
      join(serveHandle.home, 'data', 'repo'),
      'remote get-url origin',
      serveHandle.home,
    );
    check(
      `casebook-data's remote, which the sync pulls from and pushes to, is the bundle on disk (${origin})`,
      origin.endsWith('/testdata/home/data/repo.bundle') &&
        !/^(https?|ssh|git):|@/.test(origin),
    );
    check(
      'the committed bundle is untouched by the sync (nothing was pushed to it)',
      hashOf(bundle) === bundleBefore,
    );
    check(`no page errors (${errors.join(' | ')})`, errors.length === 0);
  } finally {
    await pg.close();
  }
}
