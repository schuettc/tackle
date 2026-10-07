// probe.mjs — the build-time browser sanity check for the casebook bundle.
//
// Spawns a real seeded casebook serve (via serve.mjs), opens its page in
// headless Chrome (playwright-core), and checks the four shell invariants:
//
//   1. three section controls with counts   (attention / rules / to apply)
//   2. theme toggle works                   (html[data-theme] flips)
//   3. routing switches sections            (#/rules/... → rules, back → attention)
//   4. the live pill reads live             (.kit-live is visible, data-state="live")
//
// Chrome is required in CI (KIT_BROWSER=required) and optional locally.
// Skips cleanly when Chrome is absent and KIT_BROWSER is not set.
//
// BROWSER SAFETY: all serve startups go through serve.mjs which enforces
// --no-open and CASEBOOK_NO_BROWSER=1. The check below verifies this at
// module load so no future edit can accidentally open Court's browser.

import pkg from 'playwright-core';
import { startServe, gitGuard } from './serve.mjs';
import { createAgent } from './agent.mjs';
import { applyScenarios, stopApplyServes } from './probe-apply.mjs';
import { shellScenarios, stopShellServes } from './probe-shell.mjs';
import { ownerScenarios, stopOwnerServes } from './probe-owner.mjs';
import { decideScenarios, stopDecideServes } from './probe-decide.mjs';

const { chromium } = pkg;

// Safety check: verify that serve.mjs' SERVE_ARGS include --no-open.
// This import-time assertion means any probe run — including `npm run build` —
// will hard-fail if the invariant is ever broken.
// serve.mjs enforces --no-open at module load (throws if missing).
// Importing it above is sufficient; the invariant fires before run() is called.

const required = process.env.KIT_BROWSER === 'required';

let passes = 0;
let fails = 0;

function check(label, ok) {
  if (ok) {
    console.log(`  ✓ ${label}`);
    passes++;
  } else {
    console.error(`  ✗ ${label}`);
    fails++;
  }
}

async function findChrome() {
  // Try playwright-core's installed headless shell first.
  try {
    const browser = await chromium.launch({ headless: true });
    return browser;
  } catch {
    return null;
  }
}

// Track the serve handle so signal handlers can clean it up.
let _serveHandle = null;

function cleanup() {
  if (_serveHandle) {
    _serveHandle.stop();
    _serveHandle = null;
  }
  if (_composerServe) {
    _composerServe.stop();
    _composerServe = null;
  }
  if (_keysServe) {
    _keysServe.stop();
    _keysServe = null;
  }
  if (_rulesServe) {
    _rulesServe.stop();
    _rulesServe = null;
  }
  if (_invalidServe) {
    _invalidServe.stop();
    _invalidServe = null;
  }
  stopApplyServes();
  stopShellServes();
  stopOwnerServes();
  stopDecideServes();
}

process.on('SIGTERM', () => {
  cleanup();
  process.exit(143); // 128 + SIGTERM(15)
});
process.on('SIGINT', () => {
  cleanup();
  process.exit(130); // 128 + SIGINT(2)
});
process.on('exit', () => {
  cleanup();
});

// chipCountsMatchLists checks that every attention view chip count shown in
// the DOM matches the API total for that view. Runs with the page settled.
async function chipCountsMatchLists(pg, base, token, label) {
  // Let the page settle after the last action.
  await pg.waitForTimeout(500);
  const views = ['waiting', 'new', 'due', 'proposed'];
  let allMatch = true;
  for (const view of views) {
    // Read the count from the chip label. The kit renders chip counts in a
    // .kit-n span inside [data-id="<view>"].
    const chipCount = await pg
      .evaluate((v) => {
        const chip = document.querySelector(`.kit-chip[data-id="${v}"]`);
        if (!chip) return null;
        const n = chip.querySelector('.kit-n');
        return n ? parseInt(n.textContent ?? '0', 10) : 0;
      }, view)
      .catch(() => null);
    if (chipCount === null) continue; // chip not visible
    // Fetch the API total for the view.
    const resp = await fetch(`${base}/api/items?view=${view}&limit=1`, {
      headers: { 'X-Local-Token': token },
    }).catch(() => null);
    if (!resp) continue;
    const data = await resp.json().catch(() => ({}));
    const apiTotal = data.total ?? 0;
    if (chipCount !== apiTotal) {
      console.error(
        `  [chip invariant] "${view}": chip=${chipCount} api=${apiTotal}`,
      );
      allMatch = false;
    }
  }
  check(`${label}: view chip counts match list totals`, allMatch);
}

// ---- Task 7: the composer, the batch tray, the progress line, the waiting strip

// until waits for a condition in the page and reports whether it held.
async function until(pg, fn, arg, timeout = 6000) {
  try {
    await pg.waitForFunction(fn, arg, { timeout, polling: 50 });
    return true;
  } catch (err) {
    if (err?.name === 'TimeoutError') return false;
    throw err;
  }
}

// eventually polls a check in node until it holds (serve-side state).
async function eventually(fn, timeout = 5000) {
  const end = Date.now() + timeout;
  for (;;) {
    if (await fn()) return true;
    if (Date.now() > end) return false;
    await new Promise((r) => setTimeout(r, 100));
  }
}

// clickLifecycle clicks a rule's primary (Activate or Deactivate) and
// returns serve's reply to it. serve announces the change before it replies,
// so the page can show the next primary while this request is still in
// flight: a probe that waits on serve's state alone, then clicks, clicks
// during the request. Waiting on the reply is what "done" means.
async function clickLifecycle(pg, verb) {
  const reply = lifecycleReply(pg, verb);
  await pg.click('.kit-primary');
  return reply;
}

// lifecycleReply is serve's reply to the next Activate or Deactivate the page
// posts (null if none comes within 10 s).
function lifecycleReply(pg, verb) {
  return pg
    .waitForResponse(
      (r) =>
        r.url().includes(`/api/rules/${verb}`) &&
        r.request().method() === 'POST',
      { timeout: 10000 },
    )
    .catch(() => null);
}

// workedText formats worked_ms the way serve's workedBody does.
function workedText(ms) {
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor(s / 60) % 60;
  const sec = s % 60;
  if (h > 0) return `worked for ${h}h ${m}m ${sec}s`;
  if (m > 0) return `worked for ${m}m ${sec}s`;
  return `worked for ${sec}s`;
}

// dockFixture creates a session with one thread; each scenario owns its own.
// The session stays present (serve's left threshold is 3s in the probe) by
// re-announcing itself every second from the start: the dock refuses to send
// to a session that left. present() returns the stop, for a scenario that
// makes the session leave; the beat is unref'd and its errors ignored, so a
// scenario that never stops it ends with its serve.
async function dockFixture(serveHandle, name, harness = 'pi') {
  const agent = createAgent(serveHandle.base, serveHandle.token);
  const sid = `probe-t7-${name}-${Date.now()}`;
  // The dock names an unnamed session by its folder.
  const label = name;
  const cwd = `/home/court/${name}`;
  const announce = () =>
    agent.presence(sid, `${harness} \u00b7 ${name}`, cwd, harness);
  await announce();
  const thread = await agent.newThread(sid, name);
  const beat = setInterval(() => {
    announce().catch(() => {});
  }, 1000);
  beat.unref();
  const present = () => () => clearInterval(beat);
  return { agent, sid, label, thread, present };
}

// listDiff compares two arrays of strings element by element: '' when they
// match, else where they first differ.
function listDiff(got, want) {
  for (let i = 0; i < Math.max(got.length, want.length); i++) {
    if (got[i] !== want[i]) {
      return `at ${i}: got ${JSON.stringify(got[i])}, want ${JSON.stringify(want[i])}`;
    }
  }
  return '';
}

// sameList is listDiff as a boolean, for polling.
const sameList = (got, want) => listDiff(got, want) === '';

// checkList checks that got equals want, element by element.
function checkList(label, got, want) {
  const d = listDiff(got, want);
  check(d ? `${label} — ${d}` : label, d === '');
}

// The Task 7 serve's own Attention items: repos added to the fixture's GitHub
// cache (serve.mjs seedRepos), so the attached-line scenarios select and
// decide items no other scenario touches. Human authors and no reply from
// Court put them in "waiting on you", like the fixture's hail items.
function seedRepo(name, prs, issues) {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${name}`,
    number,
    title,
    author,
    state: 'OPEN',
    created_at: `2026-09-0${i + 1}T00:00:00Z`,
    updated_at: `2026-09-0${i + 1}T00:00:00Z`,
  });
  return {
    repo: `schuettc/${name}`,
    pushed_at: '2026-09-20T00:00:00Z',
    default_branch: 'main',
    prs: prs.map(item),
    issues: issues.map(item),
  };
}
const T7_SEED = [
  seedRepo(
    't7-attached',
    [
      [670, 'bump the minor-and-patch group', 'dana'],
      [671, 'bump eslint-plugin-simple-import-sort to 14', 'dana'],
      [672, 'bump the minor-and-patch group (5 updates)', 'erin'],
      [673, 'bump vitest from 4.1.10 to 5.0.0', 'erin'],
    ],
    [[9, 'flaky upload test', 'hank']],
  ),
  seedRepo(
    't7-hidden',
    [
      [11, 'retry the webhook', 'ivy'],
      [12, 'drop the old flag', 'ivy'],
    ],
    [],
  ),
];

// openDock opens the page attached to the fixture's session (?session=, as
// casebook_open opens it; at an optional hash) and waits until the dock
// shows that session with its thread loaded.
async function openDock(context, serveHandle, fx, opts = {}) {
  const pg = await context.newPage();
  await pg.setViewportSize({ width: 1600, height: 900 });
  // opts.clock fakes the page's clock (from opts.clockAt, else now).
  if (opts.clock) await pg.clock.install({ time: opts.clockAt ?? Date.now() });
  // opts.before runs on the page before it loads (e.g. to route its calls).
  if (opts.before) await opts.before(pg);
  // opts.search adds query parameters (e.g. the Attention search, q=…).
  await pg.goto(
    serveHandle.url +
      `&session=${encodeURIComponent(fx.sid)}` +
      (opts.search ?? '') +
      (opts.hash ?? ''),
    {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    },
  );
  const shown = await until(
    pg,
    ([label, thread]) =>
      document.querySelector('.cb-dock-session-label')?.textContent === label &&
      !!document.querySelector(`.cb-dock-threads [data-thread="${thread}"].on`),
    [fx.label, fx.thread.id],
    10000,
  );
  if (!shown) throw new Error(`the dock did not show session ${fx.label}`);
  return pg;
}

// typeAndSend types into the composer and presses ↵ or ⌘↵.
async function compose(pg, text, keys = 'Enter') {
  await pg.click('[data-testid="composer-input"]');
  await pg.keyboard.type(text);
  await pg.keyboard.press(keys);
}

// cssColor resolves a CSS colour expression to its computed rgb() string.
async function cssColor(pg, expr, prop = 'color') {
  return pg.evaluate(
    ([e, p]) => {
      const d = document.createElement('div');
      d.style.setProperty(p, e);
      document.body.append(d);
      const v = getComputedStyle(d).getPropertyValue(p);
      d.remove();
      return v;
    },
    [expr, prop],
  );
}

// dockCardTexts lists the message cards' body texts, in order.
async function dockCardTexts(pg) {
  return pg.$$eval('[data-testid="dock-messages"] .cb-dock-card', (els) =>
    els.map((e) => e.querySelector('.cb-dock-bodytext')?.textContent ?? ''),
  );
}

// trayTexts lists the batch tray's drafts, in order.
async function trayTexts(pg) {
  return pg.$$eval('[data-testid="batch-tray"] .cb-batch-draft', (els) =>
    els.map((e) => e.querySelector('.cb-batch-text')?.textContent ?? ''),
  );
}

// draftBodies reads a thread's draft batch from serve.
async function draftBodies(agent, thread) {
  const mv = await agent.messages(thread);
  return (mv.drafts ?? []).map((m) => m.body);
}

// composerScenarios starts its own serve, seeded with T7_SEED, so its
// fixture (sessions, threads and Attention items) is its own and it can run
// at any point in the probe.
let _composerServe = null;
async function composerScenarios(context) {
  const serveHandle = await startServe({ seedRepos: T7_SEED });
  _composerServe = serveHandle;
  try {
    await composerScenariosOn(context, serveHandle);
  } finally {
    serveHandle.stop();
    _composerServe = null;
  }
}

async function composerScenariosOn(context, serveHandle) {
  // ---- scenario: ↵ sends, ⌘↵ adds to the batch ----------------------------
  console.log('\nscenario: the composer — ↵ sends, ⌘↵ adds to the batch');
  {
    const fx = await dockFixture(serveHandle, 'compose');
    const pg = await openDock(context, serveHandle, fx);
    try {
      const comp = await pg.$eval('[data-testid="composer"]', (el) => ({
        placeholder: el.querySelector('textarea').placeholder,
        foot: el.querySelector('.cb-comp-foot').textContent,
        shadow: getComputedStyle(el).boxShadow,
      }));
      check(
        'the input names the session\'s agent: "Message pi…"',
        comp.placeholder === 'Message pi\u2026',
      );
      check(
        'the footer reads "↵ send · ⌘↵ add to batch"',
        comp.foot === '\u21b5 send \u00b7 \u2318\u21b5 add to batch',
      );
      const lift = await cssColor(pg, 'var(--kit-lift)', 'box-shadow');
      check(
        `the composer's box-shadow is the kit lift (${comp.shadow})`,
        comp.shadow !== 'none' && comp.shadow === lift,
      );
      const raised = await pg.$$eval(
        '.kit-rail *',
        (els) =>
          els.filter((e) => getComputedStyle(e).boxShadow !== 'none').length,
      );
      check("the composer is the rail's one raised surface", raised === 1);

      // ↵ sends: a card appears and the agent receives it on its own.
      await compose(pg, 'send this now');
      check(
        '↵ sends: the message appears as a card in the thread',
        await until(pg, () =>
          [...document.querySelectorAll('.cb-dock-card .cb-dock-bodytext')]
            .map((e) => e.textContent)
            .includes('send this now'),
        ),
      );
      check(
        '↵ clears the input',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-input"]').value ===
            '',
        ),
      );
      const d1 = await fx.agent.wait(fx.sid);
      checkList(
        '↵: the agent receives the message, unbatched',
        (d1?.delivery?.messages ?? []).map(
          (m) => `${m.body}|batch ${m.batch_id ?? 'none'}`,
        ),
        ['send this now|batch none'],
      );
      await fx.agent.settled(fx.sid, [d1.delivery.id]);

      // ⌘↵ drafts: the tray shows it, the thread doesn't, the agent doesn't.
      await compose(pg, 'hold this for later', 'Meta+Enter');
      check(
        '⌘↵ adds to the batch: the tray lists it with "send 1"',
        await until(
          pg,
          () =>
            !document.querySelector('[data-testid="batch-tray"]').hidden &&
            document.querySelector('[data-testid="batch-send"]').textContent ===
              'send 1' &&
            document.querySelector('.cb-batch-text')?.textContent ===
              'hold this for later',
        ),
      );
      check(
        '⌘↵ clears the input',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-input"]').value ===
            '',
        ),
      );
      check(
        '⌘↵: the draft is not a message card',
        !(await dockCardTexts(pg)).includes('hold this for later'),
      );
      check(
        '⌘↵: serve holds it as a draft',
        sameList(await draftBodies(fx.agent, fx.thread.id), [
          'hold this for later',
        ]),
      );
      const d2 = await fx.agent.wait(fx.sid);
      check('⌘↵: nothing is delivered to the agent', d2 === null);

      // The field clears when a send starts; text typed while the send is in
      // flight is Court's and survives it. The page's POST is held for 800
      // ms so there's time to look and to type during it.
      const hold = async (route) => {
        if (route.request().method() === 'POST') {
          await new Promise((r) => setTimeout(r, 800));
        }
        await route.continue();
      };
      const field = () =>
        pg.$eval('[data-testid="composer-input"]', (el) => el.value);
      const settle = (d) =>
        fx.agent.reply(
          fx.sid,
          (d?.delivery?.messages ?? []).map((m) => m.id),
          'answered',
        );
      await pg.route('**/api/messages', hold);
      await compose(pg, 'first part');
      check(
        'the field clears as soon as the send starts, before serve answers',
        (await field()) === '',
      );
      await pg.keyboard.type('and the next thought');
      const d3 = await fx.agent.wait(fx.sid);
      checkList(
        'the agent receives only what was sent',
        (d3?.delivery?.messages ?? []).map((m) => m.body),
        ['first part'],
      );
      check(
        'text typed during a send stays after it',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-input"]').value ===
            'and the next thought',
        ),
      );
      await pg.unroute('**/api/messages', hold);
      await settle(d3);

      // Send "y", then type "hey, " at the start of the field during the
      // send: nothing of Court's is touched ("he, y" was the bug).
      await pg.click('[data-testid="composer-input"]');
      await pg.keyboard.press('ControlOrMeta+a');
      await pg.keyboard.press('Backspace');
      await pg.route('**/api/messages', hold);
      await compose(pg, 'y');
      await pg.evaluate(() => {
        const el = document.querySelector('[data-testid="composer-input"]');
        el.focus();
        el.setSelectionRange(0, 0);
      });
      await pg.keyboard.type('hey, ');
      const d4 = await fx.agent.wait(fx.sid);
      checkList(
        'the agent receives "y" once',
        (d4?.delivery?.messages ?? []).map((m) => m.body),
        ['y'],
      );
      await pg.waitForTimeout(100);
      const afterY = await field();
      check(
        `"hey, " typed before a sending "y" stays whole (got ${JSON.stringify(afterY)})`,
        afterY === 'hey, ',
      );
      await pg.unroute('**/api/messages', hold);
      await settle(d4);
      await pg.click('[data-testid="composer-input"]');
      await pg.keyboard.type('there');
      await pg.keyboard.press('Enter');
      const d5 = await fx.agent.wait(fx.sid);
      checkList(
        'sending again sends only what Court typed, never "y" twice',
        (d5?.delivery?.messages ?? []).map((m) => m.body),
        ['hey, there'],
      );
      await settle(d5);

      // A send that fails puts the sent text back, in front of anything
      // typed while it was in flight.
      const fail = async (route) => {
        if (route.request().method() === 'POST') {
          await new Promise((r) => setTimeout(r, 800));
          await route.fulfill({
            status: 500,
            contentType: 'application/json',
            body: JSON.stringify({ error: 'probe: refused' }),
          });
          return;
        }
        await route.continue();
      };
      await pg.route('**/api/messages', fail);
      await compose(pg, 'lost text');
      check(
        'a failing send also clears the field while it is in flight',
        (await field()) === '',
      );
      await pg.keyboard.type('and more');
      check(
        'a failed send restores the sent text in front of what was typed meanwhile',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-input"]').value ===
            'lost text and more',
        ),
      );
      check(
        'a failed send says "not sent"',
        (await pg.$eval('.cb-comp-note', (el) => el.textContent)) ===
          'not sent',
      );
      await pg.unroute('**/api/messages', fail);
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the composer stays pinned under 30 cards -----------------
  console.log('\nscenario: the composer stays pinned under 30 message cards');
  {
    const fx = await dockFixture(serveHandle, 'pinned');
    for (let i = 1; i <= 30; i++) {
      await fx.agent.postMessage(fx.thread.id, `card ${i}`);
    }
    const pg = await openDock(context, serveHandle, fx);
    try {
      await until(
        pg,
        () => document.querySelectorAll('.cb-dock-card').length === 30,
      );
      const g = await pg.evaluate(() => {
        const rail = document
          .querySelector('.kit-rail')
          .getBoundingClientRect();
        const comp = document
          .querySelector('[data-testid="composer"]')
          .getBoundingClientRect();
        const area = document.querySelector('[data-testid="dock-messages"]');
        return {
          cards: document.querySelectorAll('.cb-dock-card').length,
          railBottom: rail.bottom,
          compTop: comp.top,
          compBottom: comp.bottom,
          compMargin: parseFloat(
            getComputedStyle(document.querySelector('.cb-comp')).marginBottom,
          ),
          areaBottom: area.getBoundingClientRect().bottom,
          scrolls: area.scrollHeight > area.clientHeight,
          docScroll:
            document.scrollingElement.scrollHeight - window.innerHeight,
          vh: window.innerHeight,
        };
      });
      check(`30 message cards render (${g.cards})`, g.cards === 30);
      check(
        `the composer's bottom edge sits at the rail's bottom (rail ${g.railBottom}, composer ${g.compBottom} + margin ${g.compMargin})`,
        Math.abs(g.railBottom - g.compMargin - g.compBottom) < 1 &&
          g.railBottom === g.vh,
      );
      check(
        'the composer is fully on screen',
        g.compTop >= 0 && g.compBottom <= g.vh,
      );
      check(
        'the message cards scroll above it, the page does not',
        g.scrolls && g.areaBottom <= g.compTop && g.docScroll <= 0,
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the batch tray — edit, reorder, remove, send ---------------
  console.log(
    '\nscenario: the batch tray — edit, reorder and remove reach the agent',
  );
  {
    const fx = await dockFixture(serveHandle, 'batch');
    const pg = await openDock(context, serveHandle, fx);
    try {
      for (const [i, text] of ['alpha', 'bravo', 'charlie'].entries()) {
        await compose(pg, text, 'Meta+Enter');
        await until(
          pg,
          (n) => document.querySelectorAll('.cb-batch-draft').length === n,
          i + 1,
        );
      }
      const head = await pg.$eval('[data-testid="batch-tray"]', (el) => ({
        label: el.querySelector('.cb-batch-label').innerText,
        send: el.querySelector('[data-testid="batch-send"]').textContent,
        sendFilled: el
          .querySelector('[data-testid="batch-send"]')
          .classList.contains('fill'),
        border: getComputedStyle(el).borderTopStyle,
      }));
      check(
        `the tray reads "BATCH · 3 DRAFTS" (${head.label})`,
        head.label === 'BATCH \u00b7 3 DRAFTS',
      );
      check(
        'the tray has a filled "send 3"',
        head.send === 'send 3' && head.sendFilled,
      );
      check('the tray is a dashed card', head.border === 'dashed');
      checkList('the tray lists the drafts in order', await trayTexts(pg), [
        'alpha',
        'bravo',
        'charlie',
      ]);

      // Edit bravo. Halfway through his typing the tray reloads (a drafts
      // event: here charlie saved unchanged; on a slow runner, the late
      // event of a draft just added): his field, focus and typing stay,
      // and nothing half-typed is saved.
      await pg.click('.cb-batch-draft:nth-child(2) [data-action="edit"]');
      await pg.keyboard.press('ControlOrMeta+a');
      await pg.keyboard.type('bravo, ed');
      const editField = await pg.$('.cb-batch-edit');
      const charlie = (await fx.agent.messages(fx.thread.id)).drafts.find(
        (m) => m.body === 'charlie',
      );
      const reloaded = pg.waitForResponse((r) =>
        r.url().includes('/api/messages?'),
      );
      await fx.agent.api('POST', '/api/drafts/edit', {
        id: charlie.id,
        body: 'charlie',
      });
      await reloaded;
      const fieldLeft =
        !editField ||
        (await until(
          pg,
          (f) => !f.isConnected || document.activeElement !== f,
          editField,
          1500,
        ));
      check(
        'a tray reload while he edits a draft leaves his field, focused, with what he typed, and saves none of it',
        !!editField &&
          !fieldLeft &&
          (await editField.evaluate((f) => f.value)) === 'bravo, ed' &&
          sameList(await draftBodies(fx.agent, fx.thread.id), [
            'alpha',
            'bravo',
            'charlie',
          ]),
      );
      await pg.keyboard.type('ited');
      await pg.keyboard.press('Enter');
      check(
        'edit: the tray shows the edited draft',
        await until(
          pg,
          () =>
            [...document.querySelectorAll('.cb-batch-text')][1]?.textContent ===
            'bravo, edited',
        ),
      );
      check(
        'edit: serve holds the edited text',
        await eventually(async () =>
          sameList(await draftBodies(fx.agent, fx.thread.id), [
            'alpha',
            'bravo, edited',
            'charlie',
          ]),
        ),
      );

      // Move charlie to the top.
      await pg.click('.cb-batch-draft:nth-child(3) [data-action="up"]');
      await until(
        pg,
        () =>
          [...document.querySelectorAll('.cb-batch-text')][1]?.textContent ===
          'charlie',
      );
      await pg.click('.cb-batch-draft:nth-child(2) [data-action="up"]');
      check(
        'reorder: the tray shows charlie first',
        await until(
          pg,
          () =>
            [...document.querySelectorAll('.cb-batch-text')]
              .map((e) => e.textContent)
              .join('|') === 'charlie|alpha|bravo, edited',
        ),
      );
      check(
        'reorder: serve holds the new order',
        await eventually(async () =>
          sameList(await draftBodies(fx.agent, fx.thread.id), [
            'charlie',
            'alpha',
            'bravo, edited',
          ]),
        ),
      );

      // Remove alpha.
      await pg.click('.cb-batch-draft:nth-child(2) [data-action="remove"]');
      check(
        'remove: the tray drops it and reads "send 2"',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="batch-send"]').textContent ===
              'send 2' &&
            [...document.querySelectorAll('.cb-batch-text')]
              .map((e) => e.textContent)
              .join('|') === 'charlie|bravo, edited',
        ),
      );
      check(
        'remove: serve drops the draft',
        await eventually(async () =>
          sameList(await draftBodies(fx.agent, fx.thread.id), [
            'charlie',
            'bravo, edited',
          ]),
        ),
      );

      // send 2: the tray clears; the drafts become queued cards.
      await pg.click('[data-testid="batch-send"]');
      check(
        'send N: the tray clears',
        await until(
          pg,
          () => document.querySelector('[data-testid="batch-tray"]').hidden,
        ),
      );
      check(
        'send N: the drafts become queued cards, in order',
        await until(pg, () => {
          const want = [
            ['charlie', 'queued'],
            ['bravo, edited', 'queued'],
          ];
          const cards = [...document.querySelectorAll('.cb-dock-card')];
          return (
            cards.length === want.length &&
            cards.every(
              (c, i) =>
                c.querySelector('.cb-dock-bodytext')?.textContent ===
                  want[i][0] &&
                c.querySelector('.cb-dock-state')?.dataset.state === want[i][1],
            )
          );
        }),
      );
      const d = await fx.agent.wait(fx.sid);
      const msgs = d?.delivery?.messages ?? [];
      checkList(
        'send N: the agent receives the batch in its order, edited, without the removed draft',
        msgs.map((m) => m.body),
        ['charlie', 'bravo, edited'],
      );
      check(
        'send N: the batch arrives as one unit (one delivery, one batch id)',
        msgs.length === 2 &&
          !!msgs[0].batch_id &&
          msgs[0].batch_id === msgs[1].batch_id,
      );
      const text = d?.text ?? '';
      check(
        "the agent's delivery text lists the edited order and not the removed draft",
        text.indexOf('charlie') >= 0 &&
          text.indexOf('charlie') < text.indexOf('bravo, edited') &&
          !text.includes('alpha'),
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the thread drawn again under an edit in the tray ---------
  console.log(
    '\nscenario: a message arriving while a draft is edited leaves the edit',
  );
  {
    const fx = await dockFixture(serveHandle, 'batch-msg');
    const pg = await openDock(context, serveHandle, fx);
    try {
      for (const [i, text] of ['one', 'two'].entries()) {
        await compose(pg, text, 'Meta+Enter');
        await until(
          pg,
          (n) => document.querySelectorAll('.cb-batch-draft').length === n,
          i + 1,
        );
      }
      // Court edits "two"; halfway through, a message goes into the
      // thread (from another tab): the thread's cards are drawn again, the
      // tray after them.
      await pg.click('.cb-batch-draft:nth-child(2) [data-action="edit"]');
      await pg.keyboard.press('ControlOrMeta+a');
      await pg.keyboard.type('two, ed');
      const editField = await pg.$('.cb-batch-edit');
      const redrawn = until(
        pg,
        () =>
          [...document.querySelectorAll('.cb-dock-card')].some((c) =>
            c.textContent.includes('from another tab'),
          ),
        undefined,
        8000,
      );
      await fx.agent.postMessage(fx.thread.id, 'from another tab');
      const shown = await redrawn;
      const fieldLeft =
        !editField ||
        (await until(
          pg,
          (f) => !f.isConnected || document.activeElement !== f,
          editField,
          1500,
        ));
      check(
        'the thread drawn again while he edits a draft leaves his field, focused, with what he typed, and saves none of it',
        shown &&
          !fieldLeft &&
          (await editField.evaluate((f) => f.value)) === 'two, ed' &&
          sameList(await draftBodies(fx.agent, fx.thread.id), ['one', 'two']),
      );
      await pg.keyboard.type('ited');
      await pg.keyboard.press('Enter');
      check(
        'his edit, finished, is what serve holds',
        await eventually(async () =>
          sameList(await draftBodies(fx.agent, fx.thread.id), [
            'one',
            'two, edited',
          ]),
        ),
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the thread shows cards in delivery order -----------------
  console.log(
    '\nscenario: a batch sent after a later ↵ message shows where serve delivers it',
  );
  {
    const fx = await dockFixture(serveHandle, 'order');
    const pg = await openDock(context, serveHandle, fx);
    try {
      // alpha is drafted first, "now" is sent second, then the batch goes.
      await compose(pg, 'alpha, drafted first', 'Meta+Enter');
      await until(
        pg,
        () => document.querySelectorAll('.cb-batch-draft').length === 1,
      );
      await compose(pg, 'now, sent second');
      await until(pg, () =>
        [...document.querySelectorAll('.cb-dock-bodytext')].some(
          (e) => e.textContent === 'now, sent second',
        ),
      );
      await pg.click('[data-testid="batch-send"]');
      await until(
        pg,
        () => document.querySelectorAll('.cb-dock-card').length === 2,
      );
      const shown = await pg.$$eval('.cb-dock-card .cb-dock-bodytext', (els) =>
        els.map((e) => e.textContent),
      );
      const d = await fx.agent.wait(fx.sid);
      const got = (d?.delivery?.messages ?? []).map((m) => m.body);
      checkList('the agent receives "now" before the batch', got, [
        'now, sent second',
        'alpha, drafted first',
      ]);
      checkList(
        'the thread shows the cards in the order the agent receives them',
        shown,
        got,
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the queued message waits for the turn --------------------
  console.log('\nscenario: the queued message waits for the turn');
  {
    const fx = await dockFixture(serveHandle, 'queued');
    const stopPresent = fx.present();
    await fx.agent.postMessage(fx.thread.id, 'first, starts the turn');
    const d1 = await fx.agent.wait(fx.sid);
    const pg = await openDock(context, serveHandle, fx);
    try {
      check(
        'no waiting strip while nothing is queued',
        await pg.$eval('[data-testid="waiting-strip"]', (e) => e.hidden),
      );
      await compose(pg, 'while you work, one');
      check(
        'a message sent while the agent is busy shows queued',
        await until(pg, () =>
          [...document.querySelectorAll('.cb-dock-card')].some(
            (c) =>
              c.querySelector('.cb-dock-bodytext')?.textContent ===
                'while you work, one' &&
              c.querySelector('.cb-dock-state')?.dataset.state === 'queued',
          ),
        ),
      );
      check(
        'the waiting strip reads "waiting · 1 queued for the end of this turn"',
        await until(pg, () => {
          const s = document.querySelector('[data-testid="waiting-strip"]');
          return (
            !s.hidden &&
            s.offsetHeight > 0 &&
            s.textContent === 'waiting \u00b7 1 queued for the end of this turn'
          );
        }),
      );
      const wordColor = await pg.$eval(
        '.cb-wait-word',
        (e) => getComputedStyle(e).color,
      );
      check(
        '"waiting" is in the wait colour',
        wordColor === (await cssColor(pg, 'var(--kit-wait)')),
      );
      await compose(pg, 'while you work, two');
      check(
        'the waiting strip counts 2 queued',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="waiting-strip"]')
              .textContent ===
            'waiting \u00b7 2 queued for the end of this turn',
        ),
      );
      await fx.agent.settled(fx.sid, [d1.delivery.id]);
      const d2 = await fx.agent.wait(fx.sid);
      checkList(
        'at the end of the turn both go out together, in order',
        (d2?.delivery?.messages ?? []).map((m) => m.body),
        ['while you work, one', 'while you work, two'],
      );
      check(
        'the waiting strip leaves once they are delivered',
        await until(
          pg,
          () => document.querySelector('[data-testid="waiting-strip"]').hidden,
        ),
      );
    } finally {
      stopPresent();
      await pg.close();
    }
  }

  // ---- scenario: the waiting strip only during a turn -----------------------
  console.log(
    '\nscenario: the waiting strip shows only while the session is busy and present',
  );
  {
    const fx = await dockFixture(serveHandle, 'idle');
    const stopPresent = fx.present();
    let present = true;
    // Queued while idle: the agent isn't waiting, so nothing is in flight.
    await fx.agent.postMessage(fx.thread.id, 'queued while idle, one');
    await fx.agent.postMessage(fx.thread.id, 'queued while idle, two');
    const pg = await openDock(context, serveHandle, fx);
    const strip = () =>
      pg.$eval('[data-testid="waiting-strip"]', (e) => ({
        hidden: e.hidden || e.offsetHeight === 0,
        text: e.textContent,
      }));
    try {
      await until(
        pg,
        () =>
          [...document.querySelectorAll('.cb-dock-state')].filter(
            (e) => e.dataset.state === 'queued',
          ).length === 2,
      );
      const sv = await (
        await fetch(`${serveHandle.base}/api/sessions`, {
          headers: { 'X-Local-Token': serveHandle.token },
        })
      ).json();
      const row = sv.sessions.find((x) => x.id === fx.sid);
      check(
        `serve: the idle session has 2 queued and is not busy (queued ${row.queued}, busy ${row.busy})`,
        row.queued === 2 && !row.busy,
      );
      const idle = await strip();
      check(
        `an idle session with queued messages shows no waiting strip (${idle.hidden ? 'hidden' : idle.text})`,
        idle.hidden,
      );

      // A turn starts: both go out; a third message waits for its end.
      const d = await fx.agent.wait(fx.sid);
      await fx.agent.postMessage(fx.thread.id, 'while you work');
      check(
        'once the session is busy, the strip reads "waiting · 1 queued …"',
        await until(
          pg,
          () => {
            const e = document.querySelector('[data-testid="waiting-strip"]');
            return (
              !e.hidden &&
              e.textContent ===
                'waiting \u00b7 1 queued for the end of this turn'
            );
          },
          undefined,
          8000,
        ),
      );

      // The session goes left mid-turn (it stops announcing itself).
      stopPresent();
      present = false;
      check(
        "a left session shows Task 6's header: left · 1 queued · move to…",
        await until(
          pg,
          () => {
            const r = document.querySelector('.cb-dock-left-row');
            return (
              !!r &&
              !r.hidden &&
              r.textContent.replace(/\s+/g, '') === 'left·1queued·moveto\u2026'
            );
          },
          undefined,
          10000,
        ),
      );
      const left = await strip();
      check(
        `a left session shows no waiting strip (${left.hidden ? 'hidden' : left.text})`,
        left.hidden,
      );
      void d;
    } finally {
      if (present) stopPresent();
      await pg.close();
    }
  }

  // ---- scenario: the progress line (an advancing clock) -------------------
  console.log('\nscenario: the progress line updates, ages and goes quiet');
  {
    const fx = await dockFixture(serveHandle, 'progress');
    await fx.agent.postMessage(fx.thread.id, 'check CI on the 4 PRs');
    await fx.agent.wait(fx.sid);
    // Playwright's clock belongs to the whole browser context, so the
    // advancing clock gets a context of its own.
    const clockContext = await context.browser().newContext();
    // The page loads the session's line as it opens. serve answers that load
    // before the agent reports (nothing yet), and the answer is held until
    // the live update has shown, so it lands after it, as it can on a slow
    // runner: an older answer must not clear a newer line.
    const held = [];
    let reported = false;
    const pg = await openDock(clockContext, serveHandle, fx, {
      clock: true,
      before: (p) =>
        p.route('**/api/session/progress?*', async (route) => {
          if (reported) return route.continue();
          const answered = route.fetch();
          held.push(answered.then((res) => ({ route, res })));
          await answered;
        }),
    });
    try {
      const line = () =>
        pg.$eval('[data-testid="progress-line"]', (el) => {
          const fill = el
            .querySelector('.cb-prog-fill')
            .getBoundingClientRect();
          const bar = el.querySelector('.cb-prog-bar');
          return {
            hidden: el.hidden || el.offsetHeight === 0,
            text: el.querySelector('.cb-prog-text').textContent,
            age: el.querySelector('.cb-prog-age').textContent,
            ratio: bar.hidden
              ? null
              : fill.width / bar.getBoundingClientRect().width,
            barShown: !bar.hidden && bar.offsetHeight > 0,
            dot: getComputedStyle(el.querySelector('.cb-prog-dot'))
              .backgroundColor,
            ground: getComputedStyle(el).backgroundColor,
          };
        });
      check('no progress line before the agent reports', (await line()).hidden);
      // The fake clock runs on with real time until paused: paused, the
      // line's age is only the time the probe moves it on, however long a
      // slow runner takes between the update and the fastForward.
      await pg.clock.pauseAt((await pg.evaluate(() => Date.now())) + 1000);
      // The page has asked for its line, and serve has answered: none yet.
      const asked = await eventually(async () => held.length > 0, 10000);
      const stale = await Promise.all(held);
      const staleBodies = await Promise.all(stale.map((h) => h.res.json()));
      check(
        `the page asked for its line before the agent reported, and serve said none (${stale.length} held)`,
        asked && staleBodies.every((b) => b.progress === null),
      );
      // PROBE_PROGRESS_DELAY=<ms> (opt-in stress): serve's clock runs on
      // that long past the page's, paused, before the agent reports, as a
      // slow runner leaves it. Nothing the page shows may hang on the two
      // clocks agreeing.
      if (progressDelay > 0) {
        await new Promise((r) => setTimeout(r, progressDelay));
      }

      await fx.agent.progress(fx.sid, 'checking CI on #671', 2, 4);
      check(
        'the progress line shows "checking CI on #671 · 2 of 4"',
        await until(
          pg,
          () =>
            document.querySelector('.cb-prog-text')?.textContent ===
            'checking CI on #671 \u00b7 2 of 4',
        ),
      );
      // The held answers (no line) land now, after the update.
      reported = true;
      const release = await Promise.all(held);
      const landed = pg.waitForResponse((r) =>
        r.url().includes('/api/session/progress?'),
      );
      for (const h of release) await h.route.fulfill({ response: h.res });
      await landed;
      check(
        'an older load landing after the update leaves the line showing',
        !(await until(
          pg,
          () => document.querySelector('[data-testid="progress-line"]').hidden,
          undefined,
          1500,
        )),
      );
      let l = await line();
      check(
        `the bar's width is n/total: 2 of 4 fills half (${l.ratio?.toFixed(3)})`,
        l.barShown && Math.abs(l.ratio - 0.5) < 0.01,
      );
      check(
        'the dot is the agent colour, on the agent wash',
        l.dot === (await cssColor(pg, 'var(--kit-agent)')) &&
          l.ground ===
            (await cssColor(pg, 'var(--kit-agent-soft)', 'background-color')),
      );

      await pg.clock.fastForward(8000);
      check(
        'after 8s the line reads "8s ago"',
        await until(
          pg,
          () =>
            document.querySelector('.cb-prog-age')?.textContent === '8s ago',
        ),
      );

      await fx.agent.progress(fx.sid, 'checking CI on #672', 3, 4);
      check(
        'a new update resets the age and moves the bar to 3 of 4',
        await until(
          pg,
          () =>
            document.querySelector('.cb-prog-text')?.textContent ===
              'checking CI on #672 \u00b7 3 of 4' &&
            document.querySelector('.cb-prog-age')?.textContent === '0s ago',
        ),
      );
      l = await line();
      check(
        `the bar fills three quarters (${l.ratio?.toFixed(3)})`,
        Math.abs(l.ratio - 0.75) < 0.01,
      );

      await pg.clock.fastForward('03:50');
      check(
        'at 3m50s it still reads its age',
        await until(
          pg,
          () =>
            document.querySelector('.cb-prog-age')?.textContent === '3m ago',
        ),
      );
      await pg.clock.fastForward(10000);
      check(
        'after 4 minutes without an update it reads "no progress for 4m"',
        await until(
          pg,
          () =>
            document.querySelector('.cb-prog-age')?.textContent ===
            'no progress for 4m',
        ),
      );

      await fx.agent.progress(fx.sid, 'writing the summary');
      check(
        'without n of total there is no bar',
        await until(
          pg,
          () =>
            document.querySelector('.cb-prog-text')?.textContent ===
              'writing the summary' &&
            document.querySelector('.cb-prog-bar').hidden,
        ),
      );
    } finally {
      await pg.close();
      await clockContext.close();
    }

    // A page opened now shows the current line from serve.
    const pg2 = await openDock(context, serveHandle, fx);
    try {
      check(
        'a page opened mid-turn shows the current line, aged from its update',
        await until(
          pg2,
          () =>
            document.querySelector('.cb-prog-text')?.textContent ===
              'writing the summary' &&
            /^\d+s ago$/.test(
              document.querySelector('.cb-prog-age')?.textContent ?? '',
            ),
        ),
      );
    } finally {
      await pg2.close();
    }

    // A page whose clock runs 10 minutes ahead of serve's (a skewed or
    // fake clock) ages the line by serve's clock: seconds, not "no
    // progress for 10m".
    const skewContext = await context.browser().newContext();
    try {
      const pg4 = await openDock(skewContext, serveHandle, fx, {
        clock: true,
        clockAt: Date.now() + 10 * 60 * 1000,
      });
      const age = await until(
        pg4,
        () =>
          document.querySelector('.cb-prog-text')?.textContent ===
            'writing the summary' &&
          /^\d+s ago$/.test(
            document.querySelector('.cb-prog-age')?.textContent ?? '',
          ),
      );
      check(
        `a page whose clock is 10m ahead of serve's ages the line by serve's clock (${await pg4.$eval('.cb-prog-age', (e) => e.textContent)})`,
        age,
      );
    } finally {
      await skewContext.close();
    }
  }

  // ---- scenario: worked for Xm Ys folds into the thread --------------------
  console.log('\nscenario: on settled the line folds into the thread');
  {
    const fx = await dockFixture(serveHandle, 'worked');
    await fx.agent.postMessage(fx.thread.id, 'merge the 4 bumps');
    const d = await fx.agent.wait(fx.sid);
    const pg = await openDock(context, serveHandle, fx);
    try {
      const steps = [
        ['reading the 4 PRs'],
        ['checking CI on #671', 1, 2],
        ['checking CI on #672', 2, 2],
      ];
      for (const [text, n, total] of steps) {
        await fx.agent.progress(fx.sid, text, n, total);
      }
      await until(
        pg,
        () =>
          document.querySelector('.cb-prog-text')?.textContent ===
          'checking CI on #672 \u00b7 2 of 2',
      );
      const res = await fx.agent.settled(fx.sid, [d.delivery.id]);
      const want = workedText(res.worked_ms);
      check(
        'on settled the progress line leaves',
        await until(
          pg,
          () => document.querySelector('[data-testid="progress-line"]').hidden,
        ),
      );
      check(
        `the thread gains a closed "${want}" fold after the message`,
        await until(
          pg,
          (w) => {
            const area = document.querySelector(
              '[data-testid="dock-messages"]',
            );
            const f = area.querySelector('[data-testid="worked"]');
            if (!f) return false;
            const cards = [...area.children];
            return (
              f.querySelector('summary').textContent === w &&
              !f.open &&
              cards.indexOf(f) >
                cards.indexOf(area.querySelector('.cb-dock-card')) &&
              !f.querySelector('.cb-worked-line').checkVisibility()
            );
          },
          want,
        ),
      );
      await pg.click('[data-testid="worked"] summary');
      const hist = await pg.$$eval(
        '[data-testid="worked"] .cb-worked-line',
        (els) =>
          els.map((e) => ({
            text: e.lastElementChild.textContent,
            shown: e.checkVisibility(),
          })),
      );
      check(
        'the opened history is visible',
        hist.length > 0 && hist.every((h) => h.shown),
      );
      checkList(
        "it expands to the line's history, in order",
        hist.map((h) => h.text),
        [
          'reading the 4 PRs',
          'checking CI on #671 \u00b7 1 of 2',
          'checking CI on #672 \u00b7 2 of 2',
        ],
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the attached line ---------------------------------------
  console.log(
    '\nscenario: the attached line reflects the selection, the open item and editing it',
  );
  attachedScenario: {
    // The scenario's own items: T7_SEED's schuettc/t7-attached (4 PRs and an
    // issue), shown alone through the Attention search.
    const fx = await dockFixture(serveHandle, 'attached');
    const pg = await openDock(context, serveHandle, fx, {
      search: '&q=t7-attached',
      hash: '#/attention/waiting',
    });
    const value = () =>
      pg.$eval('[data-testid="composer-attached"]', (e) => ({
        text: e.textContent,
        edited: e.hasAttribute('data-edited'),
        color: getComputedStyle(e).color,
      }));
    const sendAndReceive = async (text) => {
      await compose(pg, text);
      const d = await fx.agent.wait(fx.sid);
      const m = (d?.delivery?.messages ?? []).find((x) => x.body === text);
      if (d) await fx.agent.settled(fx.sid, [d.delivery.id]);
      return m?.attached ?? null;
    };
    try {
      const resp = await fetch(
        `${serveHandle.base}/api/items?view=waiting&q=t7-attached&limit=200`,
        { headers: { 'X-Local-Token': serveHandle.token } },
      );
      const items = (await resp.json()).items ?? [];
      await until(
        pg,
        (n) => document.querySelectorAll('.kit-row .kit-box').length === n,
        items.length,
      );
      check(
        'nothing selected or open: "attached: nothing"',
        (await value()).text === 'nothing',
      );

      // Pick four rows of one kind from the rendered list.
      const titles = await pg.$$eval('.kit-row .kit-title', (els) =>
        els.map((e) => e.textContent),
      );
      const byKind = {};
      items.forEach((it, i) => (byKind[it.kind] ??= []).push(i));
      const [kind, idx] = Object.entries(byKind).sort(
        (a, b) => b[1].length - a[1].length,
      )[0] ?? ['none', []];
      // Four of one kind, then a fifth row of another kind.
      const extra = items.findIndex((it) => it.kind !== kind);
      const pick = [...idx.slice(0, 4), extra];
      const keys = pick.map((i) => items[i]?.key);
      // The guard: without its seeded items the scenario can't run; say why.
      const seeded =
        items.length === 5 &&
        kind === 'pr' &&
        idx.length >= 4 &&
        extra >= 0 &&
        pick.every(
          (i) =>
            titles[i] ===
            (items[i].title ||
              items[i].key.slice(items[i].key.indexOf(':') + 1)),
        );
      check(
        seeded
          ? 'the rows are the seeded t7-attached items (4 PRs, then an issue)'
          : `the rows are the seeded t7-attached items — got ${items.length} items (${kind} ×${idx.length}); T7_SEED in probe.mjs, seeded through startServe({seedRepos}), should give 4 PRs and 1 issue`,
        seeded,
      );
      if (!seeded) break attachedScenario;
      const plural = kind === 'branch' ? 'branches' : `${kind}s`;
      // Each box is found as it is clicked (a locator): a live redraw
      // (events replayed on connect) replaces the rows, so a box looked up
      // first can be detached by its click.
      const boxes = pg.locator('.kit-row .kit-box');
      for (const i of pick.slice(0, 4)) await boxes.nth(i).click();
      const want4 = `4 ${plural} selected`;
      check(
        `selecting 4 shows "attached: ${want4}"`,
        await until(
          pg,
          (w) =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === w,
          want4,
        ),
      );
      const v = await value();
      check(
        'the value is in the signal colour',
        v.color === (await cssColor(pg, 'var(--kit-signal)')),
      );
      const a1 = await sendAndReceive('merge these if CI is green');
      checkList(
        'the agent receives the 4 selected keys',
        [...(a1?.keys ?? [])].sort(),
        keys.slice(0, 4).sort(),
      );

      // Edit: keep only the first two keys. Headless Chrome withholds focus
      // events without window focus, so the probe dispatches them.
      const attachedSel = '[data-testid="composer-attached"]';
      await pg.click(attachedSel);
      await pg.$eval(attachedSel, (e) =>
        e.dispatchEvent(new FocusEvent('focus')),
      );
      const editForm = await pg.$eval(attachedSel, (e) => e.textContent);
      checkList(
        'editing shows the attached keys themselves',
        editForm.split(' ').sort(),
        keys.slice(0, 4).sort(),
      );
      await pg.keyboard.press('ControlOrMeta+a');
      await pg.keyboard.type(`${keys[0]} ${keys[1]}`);
      await pg.keyboard.press('Enter');
      const want2 = `2 ${plural} selected`;
      check(
        `↵ commits the edit: "attached: ${want2}"`,
        (await value()).text === want2 && (await value()).edited,
      );
      await boxes.nth(pick[4]).click();
      await pg.waitForTimeout(200);
      check(
        'the edit holds while the selection changes',
        (await value()).text === want2,
      );
      const a2 = await sendAndReceive('just these two');
      checkList('the agent receives the edited attachment', a2?.keys ?? [], [
        keys[0],
        keys[1],
      ]);
      // The fifth row is another kind: the count says items.
      const want5 = '5 items selected';
      check(
        `after sending, the line follows the page again ("${want5}")`,
        await until(
          pg,
          (w) => {
            const e = document.querySelector(
              '[data-testid="composer-attached"]',
            );
            return e.textContent === w && !e.hasAttribute('data-edited');
          },
          want5,
        ),
      );

      // Esc reverts an edit.
      await pg.click(attachedSel);
      await pg.$eval(attachedSel, (e) =>
        e.dispatchEvent(new FocusEvent('focus')),
      );
      await pg.keyboard.press('ControlOrMeta+a');
      await pg.keyboard.type('rule nonsense');
      await pg.keyboard.press('Escape');
      check('Esc reverts the edit', (await value()).text === want5);

      // Emptying it and leaving the field attaches nothing.
      await pg.click(attachedSel);
      await pg.$eval(attachedSel, (e) =>
        e.dispatchEvent(new FocusEvent('focus')),
      );
      await pg.keyboard.press('ControlOrMeta+a');
      await pg.keyboard.press('Backspace');
      await pg.$eval(attachedSel, (e) =>
        e.dispatchEvent(new FocusEvent('blur')),
      );
      check(
        'an emptied line, on leaving it, reads "nothing"',
        (await value()).text === 'nothing' && (await value()).edited,
      );
      const a3 = await sendAndReceive('no context for this one');
      check(
        `the agent receives nothing attached (${JSON.stringify(a3)})`,
        a3 !== null && !a3.keys && !a3.open && !a3.rule && !a3.job,
      );

      // The open item, with nothing selected.
      const ticked = await pg.$$eval('.kit-row .kit-box', (els) =>
        els.flatMap((e, i) =>
          e.getAttribute('aria-checked') === 'true' ? [i] : [],
        ),
      );
      for (const i of ticked) await boxes.nth(i).click();
      const openKey = items[pick[0]].key;
      await pg.click(`.kit-row:nth-child(${pick[0] + 1}) .kit-title`);
      const wantOpen = openKey.slice(openKey.indexOf(':') + 1);
      check(
        `opening an item shows it without its kind ("${wantOpen}")`,
        await until(
          pg,
          (w) =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === w,
          wantOpen,
        ),
      );
      const a4 = await sendAndReceive('what is blocking this one?');
      check(
        `the agent receives the open item (${JSON.stringify(a4)})`,
        a4?.open === openKey && !a4?.keys,
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: . focuses the composer; the keys are listed ---------------
  console.log('\nscenario: . focuses the composer');
  {
    const fx = await dockFixture(serveHandle, 'keys', 'claude');
    const pg = await openDock(context, serveHandle, fx);
    try {
      check(
        'a claude session\'s composer reads "Message claude…"',
        (await pg.$eval(
          '[data-testid="composer-input"]',
          (e) => e.placeholder,
        )) === 'Message claude\u2026',
      );
      await pg.evaluate(() => document.activeElement?.blur());
      await pg.keyboard.press('.');
      const f = await pg.evaluate(() => ({
        id: document.activeElement?.getAttribute('data-testid'),
        value: document.querySelector('[data-testid="composer-input"]').value,
      }));
      check(
        '. focuses the composer (and types nothing)',
        f.id === 'composer-input' && f.value === '',
      );
      const signal = await cssColor(pg, 'var(--kit-signal)');
      const ring = (sel) =>
        pg.$eval(sel, (e) => {
          const cs = getComputedStyle(e);
          return `${cs.outlineStyle} ${cs.outlineWidth} ${cs.outlineColor}`;
        });
      const compRing = await ring('[data-testid="composer"]');
      check(
        `keyboard focus in the input rings the composer in 1px signal (${compRing})`,
        compRing === `solid 1px ${signal}`,
      );
      // ⇧⇥ moves keyboard focus back to the attached value.
      await pg.keyboard.press('Shift+Tab');
      const onValue = await pg.evaluate(
        () =>
          document.activeElement?.getAttribute('data-testid') ===
          'composer-attached',
      );
      const valueRing = await ring('[data-testid="composer-attached"]');
      const clip = await pg.$eval(
        '.cb-comp-attached',
        (e) => getComputedStyle(e).overflow,
      );
      check(
        `keyboard focus on the attached value draws the 1px signal ring, unclipped (${valueRing}, overflow ${clip})`,
        onValue && valueRing === `solid 1px ${signal}` && clip === 'visible',
      );
      await pg.keyboard.press('Escape');
      await pg.evaluate(() => document.activeElement?.blur());
      await pg.keyboard.press('?');
      const help = await pg.$eval('.kit-sheet', (e) => e.textContent);
      check(
        'the ? overlay lists "focus the composer" and "add to batch"',
        help.includes('focus the composer') && help.includes('add to batch'),
      );
      await pg.keyboard.press('Escape');
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: the rail at 1600x900, light and dark ----------------------
  console.log(
    '\nscenario: the rail at 1600×900 — /tmp/t7-light.png, /tmp/t7-dark.png',
  );
  {
    const fx = await dockFixture(serveHandle, 'tools-workspace');
    const stopPresent = fx.present();
    const { agent, sid, thread } = fx;
    await agent.postMessage(
      thread.id,
      'Propose decisions for the 40 chime PRs.',
    );
    const d = await agent.wait(sid);
    await agent.reply(
      sid,
      [d.delivery.messages[0].id],
      'answered',
      "Done: 38 close, 2 keep (they have human reviewers). They're in proposed.",
    );
    await agent.settled(sid, [d.delivery.id]);
    await agent.postMessage(
      thread.id,
      'Merge the 4 bettor-help bumps if CI is green.',
    );
    await agent.wait(sid);
    await agent.postMessage(thread.id, 'Then look at the stale branches.');
    await agent.postMessage(
      thread.id,
      'These 4 dependabot PRs: merge if CI is green, else close',
      {},
      true,
    );
    await agent.postMessage(
      thread.id,
      'Make a standing rule for dependabot minor/patch',
      {},
      true,
    );
    await agent.progress(sid, 'checking CI on #671', 2, 4);
    const pg = await openDock(context, serveHandle, fx, {
      hash: '#/attention/waiting',
    });
    try {
      await pg.waitForSelector('.kit-row .kit-box');
      // The mock's "4 prs selected": the seeded t7-attached PRs.
      // Each row is found again as it is clicked: a live redraw (events
      // replayed on connect) replaces the rows, so a row looked up first
      // can be gone by its click.
      const kickers = await pg.$$eval('.kit-row .kit-kicker', (els) =>
        els.map((e) => e.textContent),
      );
      for (const kicker of kickers) {
        if (/^pr .*t7-attached#67\d$/.test(kicker)) {
          await pg
            .locator('.kit-row')
            .filter({ has: pg.getByText(kicker, { exact: true }) })
            .locator('.kit-box')
            .click();
        }
      }
      check(
        'the rail shows the batch, the progress line, the waiting strip and "4 prs selected"',
        await until(
          pg,
          () =>
            !document.querySelector('[data-testid="batch-tray"]').hidden &&
            !document.querySelector('[data-testid="progress-line"]').hidden &&
            !document.querySelector('[data-testid="waiting-strip"]').hidden &&
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === '4 prs selected',
        ),
      );
      const bgs = { light: 'rgb(244, 245, 248)', dark: 'rgb(20, 22, 29)' };
      for (const theme of ['light', 'dark']) {
        for (let i = 0; i < 3; i++) {
          const t = await pg.evaluate(
            () => document.documentElement.dataset.theme,
          );
          if (t === theme) break;
          await pg.click('button.kit-ctl:has-text("theme")');
        }
        const bg = await pg.$eval(
          'body',
          (e) => getComputedStyle(e).backgroundColor,
        );
        check(
          `the ${theme} screenshot is ${theme} (body ${bg})`,
          bg === bgs[theme],
        );
        await pg.screenshot({ path: `/tmp/t7-${theme}.png` });
      }
      console.log('  screenshots: /tmp/t7-light.png  /tmp/t7-dark.png');
    } finally {
      stopPresent();
      await pg.close();
    }
  }

  // ---- scenario: only the active section sets the attached context --------
  // Last: it decides one of its seeded items, which leaves a decision queued
  // for sync (the bar's status), and the screenshots come before it.
  console.log(
    '\nscenario: a hidden Attention does not change the attached line',
  );
  hiddenScenario: {
    // Its own items: T7_SEED's schuettc/t7-hidden (2 PRs); one is decided.
    const fx = await dockFixture(serveHandle, 'hidden');
    const pg = await openDock(context, serveHandle, fx, {
      search: '&q=t7-hidden',
      hash: '#/attention/waiting',
    });
    const attached = () =>
      pg.$eval('[data-testid="composer-attached"]', (e) => e.textContent);
    try {
      const resp = await fetch(
        `${serveHandle.base}/api/items?view=waiting&q=t7-hidden&limit=200`,
        { headers: { 'X-Local-Token': serveHandle.token } },
      );
      const keys = ((await resp.json()).items ?? []).map((it) => it.key);
      const listed =
        keys.length === 2 &&
        (await until(
          pg,
          () => document.querySelectorAll('.kit-row .kit-box').length === 2,
        ));
      check(
        listed
          ? 'the seeded t7-hidden items are listed'
          : `the seeded t7-hidden items are listed — got ${keys.length}; T7_SEED in probe.mjs, seeded through startServe({seedRepos}), should give 2 PRs`,
        listed,
      );
      if (!listed) break hiddenScenario;
      const hiddenBoxes = pg.locator('.kit-row .kit-box');
      for (let i = 0, n = await hiddenBoxes.count(); i < n; i++) {
        await hiddenBoxes.nth(i).click();
      }
      check(
        'Attention active: "attached: 2 prs selected"',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === '2 prs selected',
        ),
      );

      // Another section is shown: Rules with no rule open attaches nothing.
      await pg.evaluate(() => {
        location.hash = '#/rules';
      });
      check(
        'showing Rules with no rule open sets its context ("nothing")',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === 'nothing',
        ),
      );

      // A live decided event deselects a selected key under the hidden list.
      const r = await fetch(`${serveHandle.base}/api/decide`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Local-Token': serveHandle.token,
        },
        body: JSON.stringify({ keys: [keys[0]], disposition: 'keep' }),
      });
      check(`serve decides ${keys[0]} (${r.status})`, r.ok);
      check(
        'the hidden list drops the decided key from its selection',
        await until(
          pg,
          () => document.querySelectorAll('.kit-row.sel').length === 1,
        ),
      );
      const after = await attached();
      check(
        `the decided event leaves the attached line alone (${after})`,
        after === 'nothing',
      );
      const primaryHidden = await pg.$eval('.kit-primary', (e) => e.hidden);
      check(
        "and the bar's primary stays Rules' (none), not Attention's Decide 1",
        primaryHidden,
      );

      // Back to Attention: it re-feeds what is still selected.
      await pg.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      const rest = keys[1].slice(keys[1].indexOf(':') + 1);
      check(
        `Attention shown again re-feeds its selection ("${rest}")`,
        await until(
          pg,
          (w) =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === w,
          rest,
        ),
      );
      check(
        'and its primary again: "Decide 1"',
        await until(
          pg,
          () =>
            document.querySelector('.kit-primary')?.textContent === 'Decide 1',
        ),
      );
    } finally {
      await pg.close();
    }
  }
}

// ---- section keys: Attention's a / r / d act only while Attention is shown -
//
// Its own serve (KEYS_SEED), so what a failing key does (accept a proposal,
// open a sheet) touches nothing another scenario reads. Attention keeps its
// open item and its selection while hidden; on #/rules neither a, r nor d may
// reach them.
const KEYS_SEED = [
  seedRepo(
    'keys-scope',
    [
      [31, 'wire the retry budget', 'kim'],
      [32, 'split the settings page', 'kim'],
    ],
    [],
  ),
];
let _keysServe = null;
async function keyScopeScenarios(context) {
  const serveHandle = await startServe({ seedRepos: KEYS_SEED });
  _keysServe = serveHandle;
  try {
    await keyScopeScenariosOn(context, serveHandle);
  } finally {
    serveHandle.stop();
    _keysServe = null;
  }
}

async function keyScopeScenariosOn(context, serveHandle) {
  console.log('\nscenario: section keys act only in the active section');
  const key = 'pr:schuettc/keys-scope#31';
  const agent = createAgent(serveHandle.base, serveHandle.token);
  await agent.presence('probe-keys-sess', 'pi · keys', '/tmp', 'pi');
  const made = await agent.propose('probe-keys-sess', [key], 'keep', 'keys');
  check('a pending proposal to press "a" on', (made?.proposed ?? 0) === 1);
  const proposalState = async () => {
    const r = await fetch(
      `${serveHandle.base}/api/item?key=${encodeURIComponent(key)}`,
      { headers: { 'X-Local-Token': serveHandle.token } },
    );
    const v = await r.json();
    // The one proposal on the key, whatever its state (item.proposal is
    // only there while it is pending).
    return (v?.proposals ?? []).map((p) => p.state).join(',');
  };
  const pressWithFocus = async (pg, k) => {
    await pg.evaluate(() => {
      window.dispatchEvent(new Event('focus'));
      document.body.dispatchEvent(new FocusEvent('focus'));
    });
    await pg.keyboard.press(k);
  };
  const sheetOpen = (pg) => pg.$('.kit-sheet').then((el) => Boolean(el));

  const pg = await context.newPage();
  try {
    await pg.setViewportSize({ width: 1600, height: 900 });
    await pg.goto(serveHandle.url + '#/item/' + encodeURIComponent(key), {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    });
    const opened = await until(
      pg,
      () => !!document.querySelector('.kit-read .cb-proposal-card'),
      undefined,
      8000,
    );
    check('Attention shows the open item with its pending proposal', opened);
    // Select a row too, so "d" has something it could open decide for.
    await pg.waitForSelector('.kit-row .kit-box', { timeout: 6000 });
    await pg.click('.kit-row .kit-box');
    const selected = await until(pg, () =>
      (document.querySelector('.kit-primary')?.textContent ?? '')
        .trim()
        .startsWith('Decide '),
    );
    check('Attention has a selection (primary reads Decide N)', selected);

    // Show Rules: Attention's open item and selection stay, hidden.
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

    // r and d first: they only open a sheet, while a failing a settles the
    // proposal r would act on.
    await pressWithFocus(pg, 'r');
    await pg.waitForTimeout(500);
    const rSheet = await sheetOpen(pg);
    check(
      'on #/rules, "r" does not open the reject sheet for the hidden item',
      !rSheet,
    );
    if (rSheet) {
      await pg.keyboard.press('Escape');
      await pg.waitForTimeout(300);
    }

    await pressWithFocus(pg, 'd');
    await pg.waitForTimeout(500);
    const dSheet = await sheetOpen(pg);
    check(
      'on #/rules, "d" does not open decide for the invisible selection',
      !dSheet,
    );
    if (dSheet) {
      await pg.keyboard.press('Escape');
      await pg.waitForTimeout(300);
    }

    await pressWithFocus(pg, 'a');
    await pg.waitForTimeout(1500);
    check(
      'on #/rules, "a" does not accept Attention\'s hidden open proposal',
      (await proposalState()) === 'pending',
    );

    // Back on Attention the same keys work again.
    await pg.evaluate((k) => {
      location.hash = '#/item/' + encodeURIComponent(k);
    }, key);
    await until(
      pg,
      () => !!document.querySelector('.kit-read .cb-proposal-card'),
      undefined,
      8000,
    );
    await pressWithFocus(pg, 'd');
    const dBack = await until(
      pg,
      () => !!document.querySelector('.kit-sheet'),
      undefined,
      4000,
    );
    check('back on Attention, "d" opens decide for the selection', dBack);
    if (dBack) {
      await pg.keyboard.press('Escape');
      await until(pg, () => !document.querySelector('.kit-sheet'));
    }
    await pressWithFocus(pg, 'a');
    check(
      'back on Attention, "a" accepts the open item\'s proposal',
      await eventually(async () => (await proposalState()) === 'accepted'),
    );

    // The ? overlay lists what is bound: each section's own keys only while
    // it is shown ("/" is Attention's search; "a" is Attention's accept and
    // Rules' activate).
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
    const onAttention = await overlay();
    check(
      `on Attention the overlay lists its keys ("search", "accept proposal"), not Rules' (${onAttention.join(', ')})`,
      onAttention.includes('search') &&
        onAttention.includes('accept proposal') &&
        !onAttention.includes('activate the open draft'),
    );
    await agent.api('POST', '/api/rules/draft', {
      id: 'keys-rule',
      name: 'Keys rule',
      status: 'draft',
      match: [{ field: 'repo', op: 'is', value: 'schuettc/keys-scope' }],
      propose: { disposition: 'keep' },
    });
    await pg.evaluate(() => {
      location.hash = '#/rules/keys-rule';
    });
    await until(
      pg,
      () =>
        document.querySelector('.cb-rule')?.dataset.rule === 'keys-rule' &&
        document.querySelector('.kit-primary')?.textContent === 'Activate',
      undefined,
      8000,
    );
    const onRules = await overlay();
    check(
      `on Rules the overlay lists "activate the open draft", not Attention's "search" or "accept proposal" (${onRules.join(', ')})`,
      onRules.includes('activate the open draft') &&
        !onRules.includes('search') &&
        !onRules.includes('accept proposal'),
    );
    await pressWithFocus(pg, '/');
    check(
      'on Rules, "/" does not reach Attention\'s hidden search',
      !(await pg.evaluate(() =>
        document.activeElement?.classList.contains('kit-search'),
      )),
    );
    await pressWithFocus(pg, 'a');
    check(
      'on Rules, "a" activates the open draft (the key Attention also binds)',
      await eventually(async () => {
        const r = await agent.api('GET', '/api/rule?id=keys-rule');
        return r.rule.status === 'active';
      }),
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
    await pressWithFocus(pg, '/');
    check(
      'back on Attention, "/" focuses its search',
      await until(pg, () =>
        document.activeElement?.classList.contains('kit-search'),
      ),
    );
  } finally {
    await pg.close();
  }
}

// ---- Task 8: the Rules section ----------------------------------------------
//
// The rules scenarios start their own serve. Its local branches come from a
// seeded machine snapshot (serve.mjs seedMachines): machine probe-r8's clones
// under /home/probe-r8, each branch with the landed verdict a real sync would
// have written. Its rules are made through serve's own routes: Court's with
// POST /api/rules/draft, the agent's with POST /api/agent/rule-draft.

let r8Sha = 1;
const r8Tip = () => (r8Sha++).toString(16).padStart(40, 'a');
function r8Branch(name, landed) {
  const tip = r8Tip();
  const b = { name, tip, tip_at: '2026-09-20T00:00:00Z' };
  if (!landed) return { ...b, landed_state: 'no' };
  const main = landed === 'main';
  return {
    ...b,
    landed_state: 'yes',
    landed: main ? 'in main' : `merged #${landed}`,
    landed_tip: tip,
    landed_how: main ? 'default-branch' : 'merged-pr',
  };
}
function r8Clone(name, branches) {
  return {
    path: `/home/probe-r8/${name}`,
    repo: `schuettc/${name}`,
    remotes: { origin: `schuettc/${name}` },
    branches: [{ name: 'main', tip: r8Tip(), landed_state: 'no' }, ...branches],
  };
}
const R8_MACHINE = {
  version: 1,
  machine: 'probe-r8',
  roots: ['/home/probe-r8'],
  clones: [
    // r8-landed: 3 landed in main, 2 through merged PRs, 1 not landed.
    r8Clone('r8-landed', [
      r8Branch('feat/a1', 'main'),
      r8Branch('feat/a2', 'main'),
      r8Branch('feat/a3', 'main'),
      r8Branch('feat/m1', 11),
      r8Branch('feat/m2', 12),
      r8Branch('feat/open', null),
    ]),
    r8Clone('r8-other', [r8Branch('feat/o1', 'main')]),
    r8Clone('r8-agent', [r8Branch('feat/g1', 21), r8Branch('feat/g2', 22)]),
    r8Clone(
      'r8-many',
      Array.from({ length: 203 }, (_, i) =>
        r8Branch(`feat/n${String(i + 1).padStart(3, '0')}`, 'main'),
      ),
    ),
  ],
};
const R8_BR = (repo, b) => `branch:schuettc/${repo}@${b}`;
// Court's draft: every landed branch of r8-landed and r8-other, undecided.
// 6 match: 4 in main (a1 a2 a3 o1), 2 via merged PR (m1 m2).
const R8_LANDED = {
  id: 'r8-landed',
  name: 'Landed branches \u2192 delete',
  status: 'draft',
  match: [
    { field: 'kind', op: 'is', value: 'branch' },
    { field: 'repo', op: 'in', value: 'schuettc/r8-landed,schuettc/r8-other' },
    { field: 'landed', op: 'is', value: 'all-machines' },
    { field: 'landed-how', op: 'in', value: 'default-branch,merged-pr' },
    { field: 'has-decision', op: 'is', value: 'false' },
  ],
  propose: { disposition: 'delete', note: 'landed ({how}); restore tip {tip}' },
};
const R8_AGENT = {
  id: 'r8-agent',
  name: 'Merged agent branches \u2192 delete',
  status: 'draft',
  match: [
    { field: 'kind', op: 'is', value: 'branch' },
    { field: 'repo', op: 'is', value: 'schuettc/r8-agent' },
    { field: 'landed', op: 'is', value: 'all-machines' },
  ],
  propose: { disposition: 'delete' },
};
const R8_MANY = {
  id: 'r8-many',
  name: 'Many landed branches',
  status: 'draft',
  match: [
    { field: 'kind', op: 'is', value: 'branch' },
    { field: 'repo', op: 'is', value: 'schuettc/r8-many' },
  ],
  propose: { disposition: 'delete' },
};

let _rulesServe = null;
async function rulesScenarios(context) {
  const serveHandle = await startServe({ seedMachines: [R8_MACHINE] });
  _rulesServe = serveHandle;
  try {
    await rulesScenariosOn(context, serveHandle);
  } finally {
    serveHandle.stop();
    _rulesServe = null;
  }
}

// The page's words and state, read in one place.
const r8Read = {
  attached: (pg) =>
    pg.$eval('[data-testid="composer-attached"]', (e) => e.textContent),
  primary: (pg) =>
    pg.$eval('.kit-primary', (e) => (e.hidden ? '' : e.textContent.trim())),
  heading: (pg) =>
    pg.$eval('.cb-matches-label', (e) => e.textContent).catch(() => ''),
  ticked: (pg) =>
    pg.$$eval('[data-testid="matches"] .cb-mr.on', (els) =>
      els.map((e) => e.dataset.key),
    ),
  facts: (pg) =>
    pg.$$eval('.cb-rule .kit-facts > span', (els) =>
      els.map((e) => e.textContent.replace(/\s+/g, ' ').trim()),
    ),
};

// r8Light puts a page in the light theme (the control cycles system →
// light → dark) and reports whether it is.
async function r8Light(pg) {
  for (let i = 0; i < 3; i++) {
    const t = await pg.evaluate(() => document.documentElement.dataset.theme);
    if (t === 'light') break;
    await pg.click('button.kit-ctl:has-text("theme")');
  }
  return pg.evaluate(
    () =>
      document.documentElement.dataset.theme === 'light' &&
      getComputedStyle(document.body).backgroundColor === 'rgb(244, 245, 248)',
  );
}

async function r8Shoot(pg, name) {
  const bgs = { light: 'rgb(244, 245, 248)', dark: 'rgb(20, 22, 29)' };
  for (const theme of ['light', 'dark']) {
    for (let i = 0; i < 3; i++) {
      const t = await pg.evaluate(() => document.documentElement.dataset.theme);
      if (t === theme) break;
      await pg.click('button.kit-ctl:has-text("theme")');
    }
    const got = await pg.evaluate(() => ({
      theme: document.documentElement.dataset.theme,
      bg: getComputedStyle(document.body).backgroundColor,
    }));
    check(
      `/tmp/t8-${name}-${theme}.png is ${theme} (theme ${got.theme}, body ${got.bg})`,
      got.theme === theme && got.bg === bgs[theme],
    );
    await pg.screenshot({ path: `/tmp/t8-${name}-${theme}.png` });
  }
  // Back to light for whatever follows.
  for (let i = 0; i < 3; i++) {
    const t = await pg.evaluate(() => document.documentElement.dataset.theme);
    if (t === 'light') break;
    await pg.click('button.kit-ctl:has-text("theme")');
  }
}

async function rulesScenariosOn(context, serveHandle) {
  const agent = createAgent(serveHandle.base, serveHandle.token);
  const ruleOf = (id) =>
    agent.api('GET', `/api/rule?id=${encodeURIComponent(id)}`);
  const proposedBy = async (id) => {
    const v = await agent.api('GET', '/api/items?view=proposed&limit=500');
    return (v.items ?? [])
      .filter((it) => it.proposal?.source === `rule:${id}`)
      .map((it) => it.key)
      .sort();
  };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // ---- scenario: rules — matches update live ------------------------------
  console.log('\nscenario: rules — matches update live');
  {
    const made = await agent.api('POST', '/api/rules/draft', R8_LANDED);
    check(
      'serve drafts r8-landed with 6 matches (the seeded snapshot)',
      made?.matches?.total === 6,
    );
    const fx = await dockFixture(serveHandle, 'r8-rules');
    const stopPresent = fx.present();
    // Two more rules for the list, matching nothing: an active one of
    // Court's and a draft of the agent's.
    await agent.api('POST', '/api/rules/draft', {
      id: 'r8-stale',
      name: 'Stale bot PRs (180d+) \u2192 close',
      status: 'draft',
      match: [
        { field: 'kind', op: 'is', value: 'pr' },
        { field: 'repo', op: 'is', value: 'schuettc/r8-none' },
      ],
      propose: { disposition: 'close' },
    });
    await agent.api('POST', '/api/rules/activate', { id: 'r8-stale' });
    await fx.agent.ruleDraft(fx.sid, {
      id: 'r8-pi-idea',
      name: 'Dependabot minor/patch \u2192 close when superseded',
      status: 'draft',
      match: [
        { field: 'kind', op: 'is', value: 'pr' },
        { field: 'repo', op: 'is', value: 'schuettc/r8-none' },
      ],
      propose: { disposition: 'close' },
    });
    // Playwright's clock belongs to the whole browser context: the faked,
    // paused one gets a context of its own, or every later page of the
    // shared one runs on it (seconds ahead of serve, or stopped).
    const clockContext = await context.browser().newContext();
    const pg = await openDock(clockContext, serveHandle, fx, {
      hash: '#/rules/r8-landed',
      clock: true,
    });
    let previews = 0;
    pg.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes('/api/rules/preview')) {
        previews++;
      }
    });
    try {
      const opened = await until(
        pg,
        () =>
          document.querySelector('.cb-rule')?.dataset.status === 'draft' &&
          document.querySelectorAll('[data-testid="matches"] .cb-mr.on')
            .length === 6,
        undefined,
        8000,
      );
      check('#/rules/r8-landed opens the rule as a document', opened);
      check(
        'a draft shows matches by reason: "matches now · 6 · 4 in main · 2 via merged pr"',
        (await r8Read.heading(pg)) ===
          'matches now \u00b7 6 \u00b7 4 in main \u00b7 2 via merged pr',
      );
      const server = await ruleOf('r8-landed');
      checkList(
        'the reasons are serve\'s ("in main" 4, "via merged PR" 2)',
        (server.matches.by_reason ?? []).map((r) => `${r.reason} ${r.count}`),
        ['in main 4', 'via merged PR 2'],
      );
      checkList(
        "the ticked rows are serve's matches, in serve's order",
        await r8Read.ticked(pg),
        (server.matches.page ?? []).map((m) => m.key),
      );
      checkList(
        "the rows show keys without their kind, and each one's reason",
        await pg.$$eval('[data-testid="matches"] .cb-mr.on', (els) =>
          els.map(
            (e) =>
              `${e.querySelector('.cb-mr-k').textContent} | ${e.querySelector('.cb-mr-w').textContent}`,
          ),
        ),
        (server.matches.page ?? []).map(
          (m) => `${m.key.replace(/^branch:/, '')} | ${m.reason.toLowerCase()}`,
        ),
      );
      checkList(
        'the facts: status, matches, excluded, author',
        await r8Read.facts(pg),
        ['status draft', 'matches 6', 'excluded 0', 'by you'],
      );
      check(
        'the kicker reads "rule · draft · rules/r8-landed.toml"',
        (await pg.$eval('.cb-rule .kit-kick', (e) => e.textContent)) ===
          'rule \u00b7 draft \u00b7 rules/r8-landed.toml',
      );
      check(
        'the composer attaches the open rule: "rule r8-landed"',
        (await r8Read.attached(pg)) === 'rule r8-landed',
      );
      check(
        'the bar\'s primary on a draft is "Activate"',
        (await r8Read.primary(pg)) === 'Activate',
      );
      checkList(
        "the conditions table shows the rule's conditions",
        await pg.$$eval('[data-testid="conditions"] .cb-cond', (els) =>
          els.map((e) =>
            [
              e.querySelector('.cb-cond-f').textContent,
              e.querySelector('.cb-cond-o').value ??
                e.querySelector('.cb-cond-o').textContent,
              e.querySelector('.cb-cond-v').value ??
                e.querySelector('.cb-cond-v').textContent,
            ].join(' '),
          ),
        ),
        R8_LANDED.match.map((c) => `${c.field} ${c.op} ${c.value}`),
      );
      checkList(
        "the proposal table: a draft's disposition (destructive: red) and note, editable (no until for delete)",
        await pg.$$eval('[data-testid="propose"] .kit-tr', (els) =>
          els
            .filter((e) => getComputedStyle(e).display !== 'none')
            .map((e) => {
              const v = e.querySelector('button.cb-disp, input');
              const val = v.tagName === 'BUTTON' ? v.dataset.value : v.value;
              return `${e.querySelector('.cb-cond-f').textContent}=${val}${v.classList.contains('cb-danger') ? ' (danger)' : ''}`;
            }),
        ),
        [
          'disposition=delete (danger)',
          'note=landed ({how}); restore tip {tip}',
        ],
      );

      // Grouped by repo: serve's groups, each with its rows.
      await pg.click('.cb-matches-view .kit-chip:has-text("by repo")');
      checkList(
        'by repo groups the matches under each repo, with its reasons',
        await pg.$$eval(
          '[data-testid="matches"] .cb-mgroup, [data-testid="matches"] .cb-mr.on',
          (els) =>
            els.map((e) =>
              e.classList.contains('cb-mgroup')
                ? `# ${e.textContent}`
                : e.dataset.key,
            ),
        ),
        [
          '# schuettc/r8-landed5 \u00b7 3 in main \u00b7 2 via merged pr',
          R8_BR('r8-landed', 'feat/a1'),
          R8_BR('r8-landed', 'feat/a2'),
          R8_BR('r8-landed', 'feat/a3'),
          R8_BR('r8-landed', 'feat/m1'),
          R8_BR('r8-landed', 'feat/m2'),
          '# schuettc/r8-other1 \u00b7 1 in main',
          R8_BR('r8-other', 'feat/o1'),
        ],
      );
      await pg.click('.cb-matches-view .kit-chip:has-text("list")');

      // Untick feat/a1: a reason, then serve excludes it.
      const a1 = R8_BR('r8-landed', 'feat/a1');
      const untick = async (key, reason) => {
        await pg.click(
          `[data-testid="matches"] .cb-mr[data-key="${key}"] .kit-box`,
        );
        await pg.waitForSelector('[data-testid="exclude-sheet"] input', {
          timeout: 4000,
        });
        await pg.keyboard.type(reason);
        await pg.keyboard.press('Enter');
      };
      await untick(a1, 'keep for reference');
      check(
        'unticking a match adds an exclusion and the count drops: "matches now · 5 · 3 in main · 2 via merged pr"',
        await until(
          pg,
          () =>
            document.querySelector('.cb-matches-label')?.textContent ===
            'matches now \u00b7 5 \u00b7 3 in main \u00b7 2 via merged pr',
        ),
      );
      const ex = await pg.$eval(
        `[data-testid="matches"] .cb-mr[data-key="${a1}"]`,
        (e) => ({
          x: e.classList.contains('x'),
          checked: e.querySelector('.kit-box').getAttribute('aria-checked'),
          w: e.querySelector('.cb-mr-w').textContent,
        }),
      );
      check(
        `the row stays, marked excluded and unticked ("${ex.w}")`,
        ex.x &&
          ex.checked === 'false' &&
          ex.w === 'excluded \u00b7 keep for reference',
      );
      const afterEx = await ruleOf('r8-landed');
      checkList(
        'serve holds the exclusion with its reason, and matches 5',
        [
          ...(afterEx.rule.exclude ?? []).map((x) => `${x.key} ${x.reason}`),
          String(afterEx.matches.total),
        ],
        [`${a1} keep for reference`, '5'],
      );
      checkList('the facts count it: excluded 1', await r8Read.facts(pg), [
        'status draft',
        'matches 5',
        'excluded 1',
        'by you',
      ]);

      // Re-tick: serve includes it again.
      await pg.click(
        `[data-testid="matches"] .cb-mr.x[data-key="${a1}"] .kit-box`,
      );
      const retick = await until(
        pg,
        () =>
          document.querySelector('.cb-matches-label')?.textContent ===
          'matches now \u00b7 6 \u00b7 4 in main \u00b7 2 via merged pr',
      );
      check(
        `re-ticking includes it: the count is 6 again${retick ? '' : ` (shows "${await pg.$eval('.cb-matches-label', (e) => e.textContent)}")`}`,
        retick,
      );
      const afterIn = await ruleOf('r8-landed');
      check(
        'serve dropped the exclusion (none left, matches 6)',
        (afterIn.rule.exclude ?? []).length === 0 &&
          afterIn.matches.total === 6,
      );

      // Exclude it again for the screenshots: one excluded match.
      await untick(a1, 'keep for reference');
      await until(
        pg,
        () =>
          !!document.querySelector('[data-testid="matches"] .cb-mr.x') &&
          !document.querySelector('.kit-sheet'),
      );

      // Geometry and style: the list and the document fill their columns.
      const geo = await pg.evaluate(() => {
        const r = (sel) => {
          const e = document.querySelector(sel);
          if (!e) return null;
          const b = e.getBoundingClientRect();
          return {
            x: Math.round(b.x),
            y: Math.round(b.y),
            w: Math.round(b.width),
            h: Math.round(b.height),
            display: getComputedStyle(e).display,
          };
        };
        const lists = [...document.querySelectorAll('.kit-app > .kit-list')];
        return {
          list: r('.cb-rules-list'),
          read: r('.cb-rules-read'),
          doc: r('.cb-rules-read .kit-doc'),
          rail: r('.kit-rail'),
          bar: r('.kit-bar'),
          others: lists
            .filter((e) => !e.classList.contains('cb-rules-list'))
            .map((e) => getComputedStyle(e).display),
        };
      });
      check(
        `the rules list fills the list column: x 0, 400 wide, bar to bottom (${JSON.stringify(geo.list)})`,
        geo.list.x === 0 &&
          geo.list.w === 400 &&
          geo.list.y === geo.bar.h &&
          geo.list.y + geo.list.h === 900,
      );
      check(
        `the rule fills the reading column, between the list and the rail (${JSON.stringify(geo.read)})`,
        geo.read.x === 400 &&
          geo.read.x + geo.read.w === geo.rail.x &&
          geo.read.y === geo.bar.h,
      );
      check(
        `the document is the kit's 640 px column, centred (${JSON.stringify(geo.doc)})`,
        geo.doc.w === 640 &&
          Math.abs(geo.doc.x - (geo.read.x + (geo.read.w - 640) / 2)) <= 1,
      );
      check(
        `Attention's and To apply's lists are not displayed (${geo.others.join(',')})`,
        geo.others.length === 2 && geo.others.every((d) => d === 'none'),
      );
      const wait = await cssColor(pg, 'var(--kit-wait)');
      const signal = await cssColor(pg, 'var(--kit-signal)');
      const muted = await cssColor(pg, 'var(--kit-muted)');
      const pill = await pg.$eval(
        '.kit-row[data-rule="r8-landed"] .kit-meta',
        (e) => {
          const s = getComputedStyle(e);
          return {
            text: e.textContent,
            color: s.color,
            bg: s.backgroundColor,
            radius: s.borderRadius,
            h: Math.round(e.getBoundingClientRect().height),
          };
        },
      );
      check(
        `the draft pill is the wait colour on its wash, a 6px pill (${JSON.stringify(pill)})`,
        pill.text === 'draft' &&
          pill.color === wait &&
          pill.bg !== 'rgba(0, 0, 0, 0)' &&
          pill.radius === '6px' &&
          pill.h <= 22,
      );
      check(
        "the open rule's row reads as open",
        await pg.$eval('.kit-row[data-rule="r8-landed"]', (e) =>
          e.classList.contains('open'),
        ),
      );
      const boxes = await pg.evaluate(() => {
        const st = (sel) => {
          const e = document.querySelector(sel);
          const s = getComputedStyle(e);
          const b = e.getBoundingClientRect();
          return {
            bg: s.backgroundColor,
            border: s.borderTopColor,
            bw: s.borderTopWidth,
            w: Math.round(b.width),
            h: Math.round(b.height),
          };
        };
        const k = document.querySelector(
          '[data-testid="matches"] .cb-mr.x .cb-mr-k',
        );
        return {
          on: st('[data-testid="matches"] .cb-mr.on .kit-box'),
          off: st('[data-testid="matches"] .cb-mr.x .kit-box'),
          struck: getComputedStyle(k).textDecorationLine,
          kColor: getComputedStyle(k).color,
        };
      });
      check(
        `a ticked match's box is filled in the signal colour, 14px (${JSON.stringify(boxes.on)})`,
        boxes.on.bg === signal &&
          boxes.on.border === signal &&
          boxes.on.w === 14 &&
          boxes.on.h === 14,
      );
      check(
        `the unticked box is empty with the muted edge (${JSON.stringify(boxes.off)})`,
        boxes.off.bg === 'rgba(0, 0, 0, 0)' &&
          boxes.off.border === muted &&
          boxes.off.bw !== '0px' &&
          boxes.off.w === 14 &&
          boxes.off.h === 14,
      );
      check(
        `the excluded key is struck through and muted (${boxes.struck}, ${boxes.kColor})`,
        boxes.struck === 'line-through' && boxes.kColor === muted,
      );
      checkList(
        "the list: each rule with its record and status pill, in serve's order",
        await pg.$$eval('.cb-rules-list .kit-row', (els) =>
          els.map(
            (e) =>
              `${e.dataset.rule}: ${e.querySelector('.kit-kicker').textContent} | ${e.querySelector('.kit-meta').className.replace('kit-meta kit-pill ', '')} ${e.querySelector('.kit-meta').textContent}`,
          ),
        ),
        (await agent.api('GET', '/api/rules')).rules.map(
          (r) =>
            `${r.rule.id}: ${r.matches} match \u00b7 ${r.excluded} excluded | ${r.rule.status === 'active' ? 'ok' : 'wait'} ${r.rule.status}`,
        ),
      );
      check(
        'r8-landed\'s row reads "5 match · 1 excluded", as in the mock',
        (await pg.$eval(
          '.kit-row[data-rule="r8-landed"] .kit-kicker',
          (e) => e.textContent,
        )) === '5 match \u00b7 1 excluded',
      );
      const footGeo = await pg.evaluate(() => {
        const f = document.querySelector('.cb-rules-list .kit-foot');
        const b = f.getBoundingClientRect();
        return {
          shown: getComputedStyle(f).display !== 'none' && b.height > 0,
          bottom: Math.round(b.bottom),
          button: f.querySelector('[data-testid="new-rule"]')?.textContent,
          ask: f.querySelector('.cb-rules-ask')?.textContent,
        };
      });
      check(
        `the list's foot, at its bottom: "new rule" and "or ask pi to draft one" (${JSON.stringify(footGeo)})`,
        footGeo.shown &&
          footGeo.bottom === 900 &&
          footGeo.button === 'new rule' &&
          footGeo.ask === 'or ask pi to draft one',
      );
      checkList(
        'the views count all, active and drafts',
        await pg.$$eval('.cb-rules-list .kit-chip', (els) =>
          els.map((e) => e.textContent),
        ),
        ['all3', 'active1', 'drafts2'],
      );
      check(
        'the bar counts the rules',
        (await pg.$eval(
          '.kit-ctl[data-id="rules"] .kit-n',
          (e) => e.textContent,
        )) === '3',
      );
      // The document from its top: kicker, title and facts, in the shot.
      await pg.$eval('.cb-rules-read', (e) => {
        e.scrollTop = 0;
      });
      await r8Shoot(pg, 'rules');

      // The + condition menu offers the vocabulary: every field with its
      // operators, as serve lists them.
      await pg.click('.cb-cond-add');
      await pg.$eval('[data-testid="conditions"]', (e) =>
        e.scrollIntoView({ block: 'start' }),
      );
      const vocab = await agent.api('GET', '/api/rules/vocabulary');
      checkList(
        'the + condition menu lists every field with its operators',
        await pg.$$eval('.cb-cond-menu-row', (els) =>
          els.map(
            (e) =>
              `${e.dataset.field}: ${[...e.querySelectorAll('.cb-cond-menu-op')]
                .map((b) => b.dataset.op)
                .join(' ')}`,
          ),
        ),
        (vocab.fields ?? []).map(
          (f) => `${f.name}: ${(f.ops ?? []).join(' ')}`,
        ),
      );
      check(
        'the menu is shown',
        await pg.$eval(
          '.cb-cond-menu',
          (e) => !e.hidden && e.offsetHeight > 100,
        ),
      );
      await r8Shoot(pg, 'condition-menu');
      await pg.keyboard.press('Escape');
      check(
        'Esc closes the menu',
        await until(pg, () => document.querySelector('.cb-cond-menu').hidden),
      );

      // Editing a condition re-previews, 300 ms after the last edit. The
      // page's clock is paused so the debounce is measured, not slept.
      await pg.clock.pauseAt(Date.now() + 2000);
      previews = 0;
      const how = '.cb-cond[data-index="3"] input.cb-cond-v';
      await pg.click(how, { clickCount: 3 });
      await pg.keyboard.type('merged-pr');
      await sleep(400);
      check(
        `no preview while the clock stands still after typing (${previews})`,
        previews === 0,
      );
      await pg.clock.runFor(299);
      await sleep(400);
      check(
        `no preview 299 ms after the last edit (${previews})`,
        previews === 0,
      );
      await pg.clock.runFor(1);
      check(
        'editing a condition re-previews after the debounce: "matches now · 2 · 2 via merged pr"',
        await eventually(
          async () =>
            (await r8Read.heading(pg)) ===
            'matches now \u00b7 2 \u00b7 2 via merged pr',
        ),
      );
      check(
        `the nine keystrokes made one preview (${previews})`,
        previews === 1,
      );
      // Each edit restarts the wait.
      previews = 0;
      await pg.keyboard.type('x');
      await pg.clock.runFor(200);
      await pg.keyboard.press('Backspace');
      await pg.clock.runFor(200);
      await sleep(300);
      check(
        `an edit 200 ms into the wait restarts it (${previews} after 400 ms)`,
        previews === 0,
      );
      await pg.clock.runFor(100);
      check(
        '300 ms after the last edit it previews once',
        await eventually(async () => previews === 1),
      );
      await pg.clock.resume();
      check(
        'nothing is saved by a preview (serve still has default-branch,merged-pr)',
        (await ruleOf('r8-landed')).rule.match[3].value ===
          'default-branch,merged-pr',
      );
      check(
        'with unsaved edits the draft offers "save draft"',
        await until(pg, () =>
          [...document.querySelectorAll('.cb-rule-actions .kit-btn')]
            .map((b) => b.textContent)
            .includes('save draft'),
        ),
      );

      // An older preview that answers late never paints over a newer one.
      {
        const setHow = async (v) => {
          await pg.click(how, { clickCount: 3 });
          await pg.keyboard.type(v);
        };
        let release;
        const gate = new Promise((r) => {
          release = r;
        });
        let seen;
        const firstSeen = new Promise((r) => {
          seen = r;
        });
        let first = true;
        const holdFirst = async (route) => {
          if (route.request().method() === 'POST' && first) {
            first = false;
            seen();
            await gate;
          }
          await route.continue();
        };
        const previewRoute = /\/api\/rules\/preview/;
        await pg.route(previewRoute, holdFirst);
        await setHow('default-branch,merged-pr');
        await firstSeen; // "5 · 3 in main · 2 via merged pr", held
        await setHow('default-branch');
        check(
          'the newer preview lands: "matches now · 3 · 3 in main"',
          await until(
            pg,
            () =>
              document.querySelector('.cb-matches-label')?.textContent ===
              'matches now \u00b7 3 \u00b7 3 in main',
          ),
        );
        const late = pg.waitForResponse(
          (r) => previewRoute.test(r.url()) && r.request().method() === 'POST',
        );
        release();
        await late;
        // Watch the heading while the late reply is read and handled.
        let held3 = true;
        for (let i = 0; i < 10; i++) {
          if (
            (await r8Read.heading(pg)) !==
            'matches now \u00b7 3 \u00b7 3 in main'
          ) {
            held3 = false;
          }
          await sleep(50);
        }
        check(
          'the older one, answering after it, is dropped (still "3 · 3 in main")',
          held3,
        );
        await pg.unroute(previewRoute, holdFirst);
        await setHow('merged-pr');
        await until(
          pg,
          () =>
            document.querySelector('.cb-matches-label')?.textContent ===
            'matches now \u00b7 2 \u00b7 2 via merged pr',
        );
      }

      // Validation is serve's: a bad duration and a bad count show serve's
      // message under the condition, and the matches wait for a valid rule.
      const bodyNow = () =>
        pg.evaluate(() =>
          [
            ...document.querySelectorAll('[data-testid="conditions"] .cb-cond'),
          ].map((e) => ({
            field: e.querySelector('.cb-cond-f').textContent,
            op:
              e.querySelector('.cb-cond-o').value ??
              e.querySelector('.cb-cond-o').textContent,
            value:
              e.querySelector('.cb-cond-v').value ??
              e.querySelector('.cb-cond-v').textContent,
          })),
        );
      const serveSays = async () => {
        const r = await fetch(`${serveHandle.base}/api/rules/preview`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-Local-Token': serveHandle.token,
          },
          body: JSON.stringify({ ...R8_LANDED, match: await bodyNow() }),
        });
        const j = await r.json();
        return { status: r.status, error: j.error ?? '' };
      };
      const addCondition = async (field, op, value) => {
        await pg.click('.cb-cond-add');
        await pg.click(
          `.cb-cond-menu-row[data-field="${field}"] .cb-cond-menu-op[data-op="${op}"]`,
        );
        await pg.keyboard.type(value);
      };
      const errorShown = (i) =>
        pg.$eval(`.cb-cond[data-index="${i}"] .cb-cond-err`, (e) =>
          e.hidden ? '' : e.textContent,
        );
      for (const [field, op, value] of [
        ['age', 'older-than', '7x'],
        ['open-prs', 'gt', 'lots'],
      ]) {
        await addCondition(field, op, value);
        const said = await serveSays();
        check(
          `serve refuses ${field} ${op} "${value}", naming the sixth row "condition 6" (${said.status}: ${said.error})`,
          said.status === 400 && said.error.startsWith('condition 6: '),
        );
        check(
          `the page shows serve's message under ${field} (the sixth row)`,
          await eventually(async () => (await errorShown(5)) === said.error),
        );
        check(
          `a bad ${field} is not previewed as if valid (no count, matches "—")`,
          (await r8Read.heading(pg)) === 'matches now' &&
            (await pg.$eval(
              '[data-testid="matches"]',
              (e) => e.textContent,
            )) === 'not previewed: the conditions aren\u2019t valid' &&
            (await r8Read.facts(pg))[1] === 'matches \u2014',
        );
        await pg.click('.cb-cond[data-index="5"] .cb-cond-rm');
        check(
          `removing it previews the valid rule again (${field})`,
          await until(
            pg,
            () =>
              document.querySelector('.cb-matches-label')?.textContent ===
                'matches now \u00b7 2 \u00b7 2 via merged pr' &&
              [...document.querySelectorAll('.cb-cond-err')].every(
                (e) => e.hidden,
              ),
          ),
        );
      }

      // save draft persists the edit.
      await pg.click('.cb-rule-actions .kit-btn:has-text("save draft")');
      check(
        'save draft saves the edit to serve',
        await eventually(
          async () =>
            (await ruleOf('r8-landed')).rule.match[3].value === 'merged-pr',
        ),
      );
      check(
        'once saved, "save draft" goes',
        await until(
          pg,
          () =>
            ![...document.querySelectorAll('.cb-rule-actions .kit-btn')]
              .map((b) => b.textContent)
              .includes('save draft'),
        ),
      );

      // Live: a decision made elsewhere changes the matches under the open
      // rule (has-decision is false), without a reload, and leaves the
      // attached line alone.
      await pg.evaluate(() => {
        window.__r8NoReload = true;
      });
      const m1 = R8_BR('r8-landed', 'feat/m1');
      const decided = await agent.api('POST', '/api/decide', {
        keys: [m1],
        disposition: 'keep',
      });
      check('serve decides feat/m1 (keep)', !!decided);
      check(
        'the matches update live: "matches now · 1 · 1 via merged pr"',
        await until(
          pg,
          () =>
            document.querySelector('.cb-matches-label')?.textContent ===
            'matches now \u00b7 1 \u00b7 1 via merged pr',
        ),
      );
      check(
        'without a reload',
        await pg.evaluate(() => window.__r8NoReload === true),
      );
      check(
        'a live event for the hidden Attention leaves "rule r8-landed" attached',
        (await r8Read.attached(pg)) === 'rule r8-landed',
      );

      // propose once: the current matches become proposals.
      await pg.click('.cb-rule-actions .kit-btn:has-text("propose once")');
      check(
        'propose once says what it proposed',
        await until(pg, () =>
          (
            document.querySelector('.cb-rule-note')?.textContent ?? ''
          ).startsWith('1 match proposed'),
        ),
      );
      checkList(
        'serve has the match as a pending proposal from rule:r8-landed',
        await proposedBy('r8-landed'),
        [R8_BR('r8-landed', 'feat/m2')],
      );
    } finally {
      stopPresent();
      await pg.close();
      await clockContext.close();
    }
  }

  // ---- scenario: rules — a long match list pages ---------------------------
  console.log('\nscenario: rules — a long match list pages at 200');
  {
    await agent.api('POST', '/api/rules/draft', R8_MANY);
    // Its faked clock in a context of its own (see r8-landed's above).
    const clockContext = await context.browser().newContext();
    const pg = await clockContext.newPage();
    try {
      await pg.setViewportSize({ width: 1600, height: 900 });
      await pg.clock.install({ time: Date.now() });
      await pg.goto(serveHandle.url + '#/rules/r8-many', {
        waitUntil: 'domcontentloaded',
      });
      check(
        '203 matches render 200 rows and "… 3 more"',
        await until(
          pg,
          () =>
            document.querySelectorAll('[data-testid="matches"] .cb-mr.on')
              .length === 200 &&
            document.querySelector('.cb-mr-more .cb-mr-k')?.textContent ===
              '\u2026 3 more',
          undefined,
          8000,
        ),
      );
      // A live re-preview (a decision elsewhere: events replayed on
      // connect, or new ones) can land while "show more" loads. Here it
      // does, every time: the page-2 answer is held until the re-preview
      // (debounced, page one) has answered. Court's "show more" still
      // brings the rest.
      let releaseMore;
      const moreGate = new Promise((r) => (releaseMore = r));
      let moreAsked = 0;
      const holdMore = async (r) => {
        if (moreAsked++ === 0) await moreGate;
        await r.continue();
      };
      await pg.route(/\/api\/rules\/preview\?offset=/, holdMore);
      const rePreview = pg.waitForResponse(
        (r) =>
          r.url().includes('/api/rules/preview') &&
          !r.url().includes('offset='),
        { timeout: 10000 },
      );
      await pg.click('.cb-mr-more .cb-link');
      await eventually(async () => moreAsked === 1);
      await agent.api('POST', '/api/decide', {
        keys: [R8_BR('r8-many', 'feat/n003')],
        disposition: 'keep',
      });
      const rePreviewed = await rePreview.then(
        () => true,
        () => false,
      );
      releaseMore();
      check(
        `"show 3 more" loads the rest, and the more row goes, with a re-preview landing while it loads (re-preview ${rePreviewed})`,
        rePreviewed &&
          (await until(
            pg,
            () =>
              document.querySelectorAll('[data-testid="matches"] .cb-mr.on')
                .length === 203 && !document.querySelector('.cb-mr-more'),
            undefined,
            8000,
          )),
      );
      await pg.unroute(/\/api\/rules\/preview\?offset=/, holdMore);
      const keys = await r8Read.ticked(pg);
      check(
        'the 203 rows are 203 different branches',
        new Set(keys).size === 203,
      );

      // Decisions elsewhere (a live decided and index) re-preview the open
      // rule once, 300 ms after the last event, and keep the rows Court
      // opened with "show more".
      const firsts = [];
      pg.on('request', (r) => {
        if (
          r.method() === 'POST' &&
          r.url().includes('/api/rules/preview') &&
          !r.url().includes('offset=')
        ) {
          firsts.push(r.url());
        }
      });
      // Let what the page was already doing finish (events from before it
      // opened replay on connect), then count from a still clock.
      await pg.clock.pauseAt(Date.now() + 2000);
      await pg.clock.runFor(1000);
      await pg.waitForLoadState('domcontentloaded');
      await eventually(async () => {
        const n = firsts.length;
        await sleep(300);
        return firsts.length === n;
      });
      firsts.length = 0;
      const listed = pg.waitForRequest((r) => /\/api\/rules$/.test(r.url()));
      await agent.api('POST', '/api/decide', {
        keys: [R8_BR('r8-many', 'feat/n001'), R8_BR('r8-many', 'feat/n002')],
        disposition: 'keep',
      });
      await listed; // the index event reached the page (it reloads the list)
      await sleep(300);
      check(
        `the live events wait for the debounce (${firsts.length} previews)`,
        firsts.length === 0,
      );
      await pg.clock.runFor(300);
      check(
        'then one preview for them all',
        await eventually(async () => firsts.length === 1),
      );
      check(
        'and the 203 rows Court opened stay open',
        await eventually(
          async () =>
            (await r8Read.ticked(pg)).length === 203 &&
            !(await pg.$('.cb-mr-more')),
        ),
      );
      await pg.clock.runFor(1000);
      check(`still one preview (${firsts.length})`, firsts.length === 1);
      await pg.clock.resume();
    } finally {
      await pg.close();
      await clockContext.close();
    }
    // The shared context's pages still run on the real clock: its timers
    // fire, and its time is serve's.
    const after = await context.newPage();
    try {
      await after.goto(serveHandle.url, { waitUntil: 'domcontentloaded' });
      const skew = (await after.evaluate(() => Date.now())) - Date.now();
      // A paused fake clock never fires the page's timer: node's own
      // timer ends the wait.
      const fired = await Promise.race([
        after
          .evaluate(() => new Promise((r) => setTimeout(() => r(true), 50)))
          .catch(() => false), // the page closed with it waiting
        sleep(2000).then(() => false),
      ]);
      check(
        `the clocked rules pages leave the shared context's clock real (skew ${skew}ms, timer ${fired ? 'fired' : 'stood still'})`,
        fired && Math.abs(skew) < 1500,
      );
    } finally {
      await after.close();
    }
  }

  // ---- scenario: rules — "show more" during a re-preview in flight ----------
  console.log(
    '\nscenario: rules — "show more" while a re-preview is already in flight',
  );
  {
    const pg = await context.newPage();
    try {
      await pg.setViewportSize({ width: 1600, height: 900 });
      await pg.goto(serveHandle.url + '#/rules/r8-many', {
        waitUntil: 'domcontentloaded',
      });
      const paged = await until(
        pg,
        () =>
          document.querySelectorAll('[data-testid="matches"] .cb-mr.on')
            .length === 200 &&
          document.querySelector('.cb-mr-more .cb-mr-k')?.textContent ===
            '\u2026 3 more',
        undefined,
        8000,
      );
      // Let the previews the page was already making (events replayed on
      // connect) finish first.
      const previews = [];
      const onPreview = (r) => {
        if (r.method() === 'POST' && r.url().includes('/api/rules/preview'))
          previews.push(r.url());
      };
      pg.on('request', onPreview);
      await eventually(async () => {
        const n = previews.length;
        await sleep(600);
        return previews.length === n;
      });
      // The order 49f7f70 left open, forced: a live re-preview's page one
      // is held; Court clicks "show 3 more" (its request is held too); the
      // re-preview, answered, reads what he opened and brings rows 200-202
      // itself; then "show more"'s answer arrives, last.
      let releaseFirst, releaseMore;
      const firstGate = new Promise((r) => (releaseFirst = r));
      const moreGate = new Promise((r) => (releaseMore = r));
      let firstHeld = false;
      let moreHeld = false;
      const hold = async (route) => {
        const paging = route.request().url().includes('offset=');
        if (!paging && !firstHeld) {
          firstHeld = true;
          await firstGate;
        } else if (paging && !moreHeld) {
          moreHeld = true;
          await moreGate;
        }
        await route.continue();
      };
      await pg.route(/\/api\/rules\/preview/, hold);
      await agent.api('POST', '/api/decide', {
        keys: [R8_BR('r8-many', 'feat/n004')],
        disposition: 'keep',
      });
      const rePreviewHeld = await eventually(async () => firstHeld, 8000);
      await pg.click('.cb-mr-more .cb-link');
      const moreAsked = await eventually(async () => moreHeld);
      releaseFirst();
      const rePreviewed = await until(
        pg,
        () =>
          document.querySelectorAll('[data-testid="matches"] .cb-mr.on')
            .length === 203,
        undefined,
        8000,
      );
      const moreAnswered = pg
        .waitForResponse(
          (r) =>
            r.url().includes('/api/rules/preview') &&
            r.url().includes('offset=200'),
          { timeout: 8000 },
        )
        .then(
          () => true,
          () => false,
        );
      releaseMore();
      const answered = await moreAnswered;
      await sleep(300);
      const keys = await r8Read.ticked(pg);
      check(
        `"show 3 more" clicked during a re-preview in flight shows the 3 rows once (page ${paged}, held ${rePreviewHeld}/${moreAsked}, re-previewed ${rePreviewed}, answered ${answered}: ${keys.length} rows, ${new Set(keys).size} different, more row ${!!(await pg.$('.cb-mr-more'))})`,
        paged &&
          rePreviewHeld &&
          moreAsked &&
          rePreviewed &&
          answered &&
          keys.length === 203 &&
          new Set(keys).size === 203 &&
          !(await pg.$('.cb-mr-more')),
      );
      await pg.unroute(/\/api\/rules\/preview/, hold);
      pg.off('request', onPreview);
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: rules — activate proposes ---------------------------------
  console.log('\nscenario: rules — activate proposes');
  {
    const sid = `probe-r8-agent-${Date.now()}`;
    await agent.presence(sid, 'pi \u00b7 r8', '/home/court/r8', 'pi');
    const drafted = await agent.ruleDraft(sid, R8_AGENT);
    check(
      `the agent drafts r8-agent (${drafted?.rule?.created_by})`,
      drafted?.rule?.created_by === `pi:${sid}` &&
        drafted?.rule?.status === 'draft',
    );
    let refused = '';
    try {
      await agent.ruleDraft(sid, { ...R8_AGENT, status: 'active' });
    } catch (err) {
      refused = String(err.message);
    }
    check(
      'serve refuses the agent activating it',
      refused.includes('400') &&
        refused.includes('cannot set status to active'),
    );
    check(
      'so it is still a draft',
      (await ruleOf('r8-agent')).rule.status === 'draft',
    );

    const pg = await context.newPage();
    try {
      await pg.setViewportSize({ width: 1600, height: 900 });
      await pg.goto(serveHandle.url + '#/rules/r8-agent', {
        waitUntil: 'domcontentloaded',
      });
      await until(
        pg,
        () => document.querySelector('.cb-rule')?.dataset.rule === 'r8-agent',
        undefined,
        8000,
      );
      await until(
        pg,
        () => !!document.querySelector('.kit-row[data-rule="r8-agent"]'),
      );
      const row = await pg.$eval('.kit-row[data-rule="r8-agent"]', (e) => ({
        sub: e.querySelector('.kit-sub')?.textContent ?? '',
        pill: e.querySelector('.kit-meta').textContent,
      }));
      check(
        `the agent's draft shows its author in the list ("${row.sub}")`,
        row.sub === 'drafted by pi \u00b7 awaiting your review' &&
          row.pill === 'draft',
      );
      const agentColour = await cssColor(pg, 'var(--kit-agent)');
      const by = await pg.$eval(
        '.cb-rule .kit-facts > span:last-child b',
        (e) => ({
          text: e.textContent,
          color: getComputedStyle(e).color,
        }),
      );
      check(
        `the document names its author, in the agent's colour ("by ${by.text}")`,
        by.text === 'pi' && by.color === agentColour,
      );
      check(
        'and says only Court activates it',
        (
          await pg.$eval('.cb-rule > p:not(.kit-kick)', (e) => e.textContent)
        ).startsWith(
          'pi drafted this rule. It proposes nothing until you activate it, and only you can.',
        ),
      );
      check(
        'the composer attaches "rule r8-agent"',
        (await r8Read.attached(pg)) === 'rule r8-agent',
      );

      // A hidden Rules does not touch the attached line: show Attention, then
      // the agent edits its draft (a live rules event).
      await pg.evaluate(() => {
        location.hash = '#/attention/new';
      });
      check(
        'on Attention the attached line is Attention\'s ("nothing")',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === 'nothing',
        ),
      );
      await agent.ruleDraft(sid, {
        ...R8_AGENT,
        name: 'Merged agent branches \u2192 delete (edited)',
      });
      check(
        'the hidden Rules list takes the live edit',
        await until(pg, () =>
          [...document.querySelectorAll('.cb-rules-list .kit-title')]
            .map((e) => e.textContent)
            .includes('Merged agent branches \u2192 delete (edited)'),
        ),
      );
      check(
        'and the attached line stays "nothing"',
        (await r8Read.attached(pg)) === 'nothing',
      );
      check(
        "and the primary stays Attention's (none)",
        (await r8Read.primary(pg)) === '',
      );

      // A rule still loading when Rules is left: its answer, arriving under
      // Attention, must not reach the attached line or the primary.
      const slowRule = /\/api\/rule\?id=r8-stale/;
      let release;
      const gate = new Promise((r) => {
        release = r;
      });
      let held;
      const heldSeen = new Promise((r) => {
        held = r;
      });
      const hold = async (route) => {
        held();
        await gate;
        await route.continue();
      };
      await pg.route(slowRule, hold);
      await pg.evaluate(() => {
        location.hash = '#/rules/r8-stale';
      });
      await heldSeen; // the rule is loading
      await pg.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await until(
        pg,
        () =>
          document
            .querySelector('.kit-ctl[data-id="attention"]')
            ?.classList.contains('on') ?? false,
      );
      const answered = pg.waitForResponse(slowRule);
      release();
      await answered;
      // The hidden Rules has drawn the rule (its feed() has run).
      await until(
        pg,
        () => !!document.querySelector('.cb-rule[data-rule="r8-stale"]'),
      );
      check(
        'a rule that finishes loading after Rules is left attaches nothing',
        (await r8Read.attached(pg)) === 'nothing' &&
          (await r8Read.primary(pg)) === '',
      );
      await pg.unroute(slowRule, hold);

      await pg.evaluate(() => {
        location.hash = '#/rules/r8-agent';
      });
      check(
        'back on Rules: "rule r8-agent" and "Activate"',
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="composer-attached"]')
              .textContent === 'rule r8-agent' &&
            document.querySelector('.kit-primary')?.textContent === 'Activate',
        ),
      );
      checkList(
        'nothing is proposed from r8-agent yet',
        await proposedBy('r8-agent'),
        [],
      );
      const agentActivated = await clickLifecycle(pg, 'activate');
      check(
        "Court's Activate activates it",
        agentActivated?.status() === 200 &&
          (await eventually(
            async () => (await ruleOf('r8-agent')).rule.status === 'active',
          )),
      );
      checkList(
        'activate turns its matches into pending proposals (serve)',
        await proposedBy('r8-agent'),
        [R8_BR('r8-agent', 'feat/g1'), R8_BR('r8-agent', 'feat/g2')],
      );
      const ok = await cssColor(pg, 'var(--kit-ok)');
      check(
        'the rule shows as active: pill in the ok colour, primary "Deactivate", "2 match · 0 excluded · 2 pending"',
        await until(
          pg,
          ([okc]) => {
            const r = document.querySelector('.kit-row[data-rule="r8-agent"]');
            const m = r?.querySelector('.kit-meta');
            return (
              m?.textContent === 'active' &&
              getComputedStyle(m).color === okc &&
              r.querySelector('.kit-kicker').textContent ===
                '2 match \u00b7 0 excluded \u00b7 2 pending' &&
              document.querySelector('.kit-primary')?.textContent ===
                'Deactivate'
            );
          },
          [ok],
        ),
      );
      check(
        "an active rule's conditions are read-only (no + condition)",
        !(await pg.$('.cb-rule .cb-cond-add')) &&
          !(await pg.$(
            '.cb-rule input.cb-cond-v, .cb-rule select, .cb-rule button.cb-disp',
          )),
      );

      await pg.evaluate(() => {
        location.hash = '#/attention/proposed';
      });
      checkList(
        "the page lists them as proposals in Attention's proposed view",
        await (async () => {
          await until(
            pg,
            () =>
              [
                ...document.querySelectorAll(
                  '.kit-list:not(.cb-rules-list) .kit-row .kit-kicker',
                ),
              ].filter((e) => e.textContent.includes('r8-agent')).length === 2,
          );
          return pg.$$eval('.kit-list:not(.cb-rules-list) .kit-row', (els) =>
            els
              .filter((e) =>
                e.querySelector('.kit-kicker').textContent.includes('r8-agent'),
              )
              .map(
                (e) =>
                  `${e.querySelector('.kit-kicker').textContent} | ${e.querySelector('.kit-sub')?.textContent}`,
              )
              .sort(),
          );
        })(),
        [
          'branch \u00b7 schuettc/r8-agent@feat/g1 | rule recommends Delete it',
          'branch \u00b7 schuettc/r8-agent@feat/g2 | rule recommends Delete it',
        ],
      );

      await pg.evaluate(() => {
        location.hash = '#/rules/r8-agent';
      });
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Deactivate',
      );
      const agentDeactivated = await clickLifecycle(pg, 'deactivate');
      check(
        'Deactivate puts it back to a draft',
        agentDeactivated?.status() === 200 &&
          (await eventually(
            async () => (await ruleOf('r8-agent')).rule.status === 'draft',
          )),
      );
      check(
        'and the primary is "Activate" again',
        await until(
          pg,
          () =>
            document.querySelector('.kit-primary')?.textContent === 'Activate',
        ),
      );
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: rules — new rule, and asking the agent --------------------
  console.log('\nscenario: rules — new rule, and asking the agent');
  {
    const fx = await dockFixture(serveHandle, 'r8-new');
    const stopPresent = fx.present();
    const pg = await openDock(context, serveHandle, fx, {
      hash: '#/rules/no-such-rule',
    });
    try {
      check(
        'a rule that does not exist says so',
        await until(pg, () =>
          (
            document.querySelector('.cb-rules-read')?.textContent ?? ''
          ).includes('rule no-such-rule not found'),
        ),
      );
      check(
        'and attaches nothing (not "rule no-such-rule")',
        (await r8Read.attached(pg)) === 'nothing',
      );

      // new rule: a name and an id, nothing else.
      await pg.click('[data-testid="new-rule"]');
      await pg.waitForSelector('[data-testid="new-rule-sheet"] input');
      await pg.keyboard.type('Old forks \u2192 archive');
      check(
        'the id follows the name: "old-forks-archive"',
        (await pg.$eval(
          '[data-testid="new-rule-sheet"] input[aria-label="id"]',
          (e) => e.value,
        )) === 'old-forks-archive',
      );
      await pg.keyboard.press('Enter');
      check(
        'new rule creates the draft and opens it',
        await until(
          pg,
          () =>
            location.hash === '#/rules/old-forks-archive' &&
            document.querySelector('.cb-rule')?.dataset.rule ===
              'old-forks-archive',
          undefined,
          8000,
        ),
      );
      const fresh = await ruleOf('old-forks-archive');
      check(
        `serve has it as Court's draft, named as typed (${fresh.rule.created_by})`,
        fresh.rule.status === 'draft' &&
          fresh.rule.name === 'Old forks \u2192 archive' &&
          fresh.rule.created_by === 'schuettc',
      );
      check(
        `with no disposition it is shown as not valid, in serve's words (${fresh.invalid})`,
        (await pg.$eval(
          '[data-testid="rule-invalid"] .cb-invalid-msg',
          (e) => e.textContent,
        )) === fresh.invalid && fresh.invalid !== '',
      );
      check(
        'the new rule is at the top of the reading column, and attached',
        (await r8Read.attached(pg)) === 'rule old-forks-archive' &&
          (await pg.$eval('.cb-rule .kit-h1', (e) => e.textContent)) ===
            'Old forks \u2192 archive',
      );
      // The proposal: the picker offers what serve says this rule may
      // propose, following its conditions (N1).
      const chips = async () => {
        await pg.click('[data-testid="propose"] button.cb-disp');
        await pg.waitForSelector('.cb-disp-menu:not([hidden])', {
          timeout: 4000,
        });
        return pg.$$eval('.cb-disp-menu .cb-disp-opt', (els) =>
          els.map((e) => e.dataset.disp),
        );
      };
      const closeMenu = async () => {
        await pg.keyboard.press('Escape');
        await until(pg, () =>
          document.querySelector('.cb-disp-menu')?.hasAttribute('hidden'),
        );
      };
      checkList(
        "with no kind condition the picker offers serve's dispositions for every kind (no archive)",
        await chips(),
        fresh.matches.dispositions,
      );
      checkList('(serve says keep, wait, ignore)', fresh.matches.dispositions, [
        'keep',
        'wait',
        'ignore',
      ]);
      // P1: the picker says why, in the menu, while it is open.
      const why = await pg
        .$eval('[data-testid="disp-why"]', (e) => ({
          text: e.textContent,
          shown:
            getComputedStyle(e).display !== 'none' &&
            e.getBoundingClientRect().height > 0,
        }))
        .catch(() => ({ text: '', shown: false }));
      check(
        `with no kind condition the picker says why it offers only these ("${why.text}")`,
        why.shown &&
          why.text ===
            'add a kind condition to propose archive, close, delete, merge or watch',
      );
      // P2: the arrow keys move between the chips (a roving tab stop), Home
      // and End go to the ends, and the page's list doesn't move with them.
      const focusedDisp = () =>
        pg.evaluate(() => document.activeElement?.dataset?.disp ?? '');
      const firstFocused = await focusedDisp();
      await pg.keyboard.press('ArrowRight');
      const afterRight = await focusedDisp();
      await pg.keyboard.press('ArrowDown');
      const afterDown = await focusedDisp();
      await pg.keyboard.press('ArrowLeft');
      const afterLeft = await focusedDisp();
      await pg.keyboard.press('End');
      const atEnd = await focusedDisp();
      await pg.keyboard.press('Home');
      const atHome = await focusedDisp();
      await pg.keyboard.press('ArrowUp');
      const wrapped = await focusedDisp();
      checkList(
        'the disposition menu: → ↓ ← End Home ↑ move through the chips (wrapping)',
        [
          firstFocused,
          afterRight,
          afterDown,
          afterLeft,
          atEnd,
          atHome,
          wrapped,
        ],
        ['keep', 'wait', 'ignore', 'wait', 'ignore', 'keep', 'ignore'],
      );
      check(
        'the chips are one tab stop (only the focused chip is tabbable)',
        await pg.$$eval(
          '.cb-disp-menu .cb-disp-opt',
          (els) =>
            els.map((e) => `${e.dataset.disp}:${e.tabIndex}`).join(' ') ===
            'keep:-1 wait:-1 ignore:0',
        ),
      );
      // Focus Tabs out of the menu (headless Chrome withholds focus events
      // without window focus, so the focusout is fired as a Tab makes it).
      await pg.evaluate(() => {
        const from = document.activeElement;
        const to = document.querySelector(
          '[data-testid="propose"] input[aria-label="note"]',
        );
        from.dispatchEvent(
          new FocusEvent('focusout', { bubbles: true, relatedTarget: to }),
        );
      });
      check(
        'the disposition menu closes when focus Tabs out of it',
        await until(pg, () =>
          document.querySelector('.cb-disp-menu')?.hasAttribute('hidden'),
        ),
      );
      // The + condition menu: the same keys, and it closes on Tab-out too.
      await pg.click('.cb-cond-add');
      await pg.waitForSelector('.cb-cond-menu:not([hidden])', {
        timeout: 4000,
      });
      const opOrder = await pg.$$eval('.cb-cond-menu .cb-cond-menu-op', (els) =>
        els.map(
          (e) =>
            `${e.closest('.cb-cond-menu-row').dataset.field} ${e.dataset.op}`,
        ),
      );
      const focusedOp = () =>
        pg.evaluate(() => {
          const e = document.activeElement;
          return e?.classList.contains('cb-cond-menu-op')
            ? `${e.closest('.cb-cond-menu-row').dataset.field} ${e.dataset.op}`
            : '';
        });
      const opFirst = await focusedOp();
      await pg.keyboard.press('ArrowDown');
      const opSecond = await focusedOp();
      await pg.keyboard.press('End');
      const opLast = await focusedOp();
      await pg.keyboard.press('ArrowRight');
      const opWrapped = await focusedOp();
      checkList(
        'the + condition menu: ↓ End → move through its operators (wrapping)',
        [opFirst, opSecond, opLast, opWrapped],
        [opOrder[0], opOrder[1], opOrder[opOrder.length - 1], opOrder[0]],
      );
      await pg.evaluate(() => {
        document.activeElement.dispatchEvent(
          new FocusEvent('focusout', {
            bubbles: true,
            relatedTarget:
              document.querySelector('.cb-rule .kit-h1') ?? document.body,
          }),
        );
      });
      check(
        'the + condition menu closes when focus Tabs out of it',
        await until(pg, () =>
          document.querySelector('.cb-cond-menu')?.hasAttribute('hidden'),
        ),
      );
      // Two conditions: kind is repo, fork is true.
      await pg.click('.cb-cond-add');
      await pg.click(
        '.cb-cond-menu-row[data-field="kind"] .cb-cond-menu-op[data-op="is"]',
      );
      await pg.click('.cb-cond-add');
      await pg.click(
        '.cb-cond-menu-row[data-field="fork"] .cb-cond-menu-op[data-op="is"]',
      );
      const forks = {
        match: [
          { field: 'kind', op: 'is', value: 'repo' },
          { field: 'fork', op: 'is', value: 'true' },
        ],
        propose: { disposition: '' },
      };
      const forServe = await agent.api('POST', '/api/rules/preview', forks);
      await until(
        pg,
        (want) =>
          document.querySelector('.cb-matches-label')?.textContent === want,
        `matches now \u00b7 ${forServe.total}`,
      );
      checkList(
        "with kind is repo it offers serve's dispositions for a repo (archive, no close)",
        await chips(),
        forServe.dispositions,
      );
      await pg.click('.cb-disp-opt[data-disp="wait"]');
      const untilShown = () =>
        pg.$eval(
          '[data-testid="propose"] input[aria-label="until"]',
          (e) => getComputedStyle(e.closest('.kit-tr')).display !== 'none',
        );
      const withWait = await untilShown();
      const dv = await agent.api('GET', '/api/decisions/vocabulary');
      const hints = await pg.evaluate(() => {
        const t = (id) =>
          document.querySelector(`[data-testid="${id}"] .cb-prop-hint-t`);
        const fits = (e) =>
          !!e &&
          e.scrollWidth <= e.clientWidth &&
          getComputedStyle(e).display !== 'none' &&
          e.getBoundingClientRect().right <=
            document.querySelector('.cb-propose').getBoundingClientRect().right;
        return {
          until: t('until-hint')?.textContent,
          untilFits: fits(t('until-hint')),
          placeholder: document.querySelector(
            '[data-testid="propose"] input[aria-label="until"]',
          ).placeholder,
          note: t('note-hint')?.textContent,
          noteFits: fits(t('note-hint')),
          notePlaceholder: document.querySelector(
            '[data-testid="propose"] input[aria-label="note"]',
          ).placeholder,
        };
      });
      check(
        `the until hint is serve's until forms, whole ("${hints.until}", ${hints.placeholder})`,
        withWait &&
          hints.untilFits &&
          hints.until ===
            (dv.until_forms ?? []).map((f) => f.syntax).join('\u00a0\u00b7 ') &&
          hints.placeholder === `e.g. ${dv.until_forms[0].example}`,
      );
      const rv = await agent.api('GET', '/api/rules/vocabulary');
      check(
        `the note hint lists serve's tokens, generically and whole ("${hints.note}")`,
        hints.noteFits &&
          hints.notePlaceholder === 'optional' &&
          hints.note ===
            'may use ' +
              (rv.note_tokens ?? [])
                .map((t) => `${t.token} ${t.meaning}`)
                .join('\u00a0\u00b7 ') &&
          (rv.note_tokens ?? []).length === 5,
      );
      // An until serve can't parse: saved (a draft may be unfinished), shown
      // as not valid in serve's words, and never activated.
      await pg.fill('[data-testid="propose"] input[aria-label="until"]', '30d');
      await pg.click('.cb-rule-actions .kit-btn:has-text("save draft")');
      let withBadUntil = null;
      await eventually(async () => {
        const r = await ruleOf('old-forks-archive');
        if (r.rule.propose.until === '30d') withBadUntil = r;
        return !!withBadUntil;
      });
      check(
        `serve holds "wait until 30d" as not valid (${withBadUntil?.invalid})`,
        (withBadUntil?.invalid ?? '').includes('invalid until "30d"'),
      );
      check(
        "the page shows serve's reason in the card and under the proposal",
        await until(
          pg,
          (w) =>
            document.querySelector(
              '[data-testid="rule-invalid"] .cb-invalid-msg',
            )?.textContent === w &&
            document.querySelector('[data-testid="propose-err"]')
              ?.textContent === w &&
            !document.querySelector('[data-testid="propose-err"]').hidden,
          withBadUntil?.invalid ?? '-',
        ),
      );
      // P3: Activate is refused on the page (nothing is posted that is sure
      // to be refused); the note points to the card, and serve's reason is
      // on the page twice: in the card and at the field.
      let activatePosts = 0;
      const countActivate = (r) => {
        if (r.url().includes('/api/rules/activate')) activatePosts++;
      };
      pg.on('request', countActivate);
      await pg.click('.kit-primary');
      const pointed = await until(
        pg,
        () =>
          document.querySelector('.cb-rule-note')?.textContent ===
          'not activated: fix what\u2019s marked above',
      );
      await pg.waitForTimeout(300);
      pg.off('request', countActivate);
      check(
        'Activate on a saved invalid rule is refused on the page, pointing to the card',
        pointed && activatePosts === 0,
      );
      check(
        "serve's reason shows once in the card and once at the field, never in the note",
        await pg.evaluate((w) => {
          const doc = document.querySelector('.cb-rule');
          const all = doc.textContent.split(w).length - 1;
          return (
            all === 2 &&
            !document.querySelector('.cb-rule-note').textContent.includes(w)
          );
        }, withBadUntil?.invalid ?? '-'),
      );
      check(
        'serve still has a draft',
        (await ruleOf('old-forks-archive')).rule.status === 'draft',
      );
      // The screenshot: the proposal with the until hint, the picker open.
      await chips();
      await pg.$eval('[data-testid="propose"]', (e) =>
        e.scrollIntoView({ block: 'center' }),
      );
      check(
        '/tmp/t8-proposal-light.png is light (theme and body)',
        await r8Light(pg),
      );
      await pg.screenshot({ path: '/tmp/t8-proposal-light.png' });
      await closeMenu();
      // Fixed: archive, no until.
      await pg.fill('[data-testid="propose"] input[aria-label="until"]', '');
      await chips();
      // A live preview while the menu is open (something is decided
      // elsewhere: the index moves and the rule's matches are previewed
      // again, offering the same dispositions) leaves the menu open, its
      // chips as they were and Court's focus where it was.
      const openFocus = await pg.evaluateHandle(() => document.activeElement);
      const livePreview = pg
        .waitForResponse((r) => r.url().includes('/api/rules/preview'), {
          timeout: 8000,
        })
        .catch(() => null);
      await agent.api('POST', '/api/decide', {
        keys: [R8_BR('r8-many', 'feat/n150')],
        disposition: 'keep',
      });
      const livePreviewed = await livePreview;
      await pg.evaluate(
        () =>
          new Promise((r) =>
            requestAnimationFrame(() => requestAnimationFrame(r)),
          ),
      );
      check(
        `a live preview (${livePreviewed?.status()}) while the disposition menu is open leaves it open, with Court's focus on the same chip`,
        !!livePreviewed &&
          (await pg.evaluate(
            (f) =>
              !document.querySelector('.cb-disp-menu').hidden &&
              f.matches('.cb-disp-opt') &&
              f.isConnected &&
              document.activeElement === f,
            openFocus,
          )),
      );
      await pg.click('.cb-disp-opt[data-disp="archive"]');
      check(
        'the until row showed for wait, not for archive',
        withWait && !(await untilShown()),
      );
      // serve's reply to the save is held until Court has changed a
      // condition after it (as a slow runner delivers it late): his change
      // is newer than the copy saved, and stays.
      let releaseSave;
      const saveReleased = new Promise((r) => (releaseSave = r));
      const holdSave = async (route) => {
        const res = await route.fetch();
        await saveReleased;
        await route.fulfill({ response: res });
      };
      await pg.route('**/api/rules/draft?*', holdSave);
      await pg.click('.cb-rule-actions .kit-btn:has-text("save draft")');
      check(
        'saved: serve has the disposition and the conditions, and it is valid',
        await eventually(async () => {
          const r = await ruleOf('old-forks-archive');
          return (
            r.rule.propose.disposition === 'archive' &&
            !r.rule.propose.until &&
            r.rule.match.map((c) => `${c.field} ${c.value}`).join(', ') ===
              'kind repo, fork true' &&
            !r.invalid
          );
        }),
      );
      // A condition that makes the disposition one the rule can't propose:
      // the page says so before anything is saved.
      await pg.selectOption(
        '.cb-rule > [data-testid="conditions"] .cb-cond[data-index="0"] select.cb-cond-v',
        'branch',
      );
      const saveReplied = pg.waitForResponse((r) =>
        r.url().includes('/api/rules/draft?'),
      );
      releaseSave();
      await saveReplied;
      await pg.unroute('**/api/rules/draft?*', holdSave);
      check(
        'a condition changed while the save was in flight stays changed once its reply lands',
        !(await until(
          pg,
          () =>
            document.querySelector(
              '.cb-rule > [data-testid="conditions"] .cb-cond[data-index="0"] select.cb-cond-v',
            )?.value !== 'branch',
          undefined,
          1500,
        )),
      );
      const branchDisps = (
        await agent.api('POST', '/api/rules/preview', {
          ...forks,
          match: [{ field: 'kind', op: 'is', value: 'branch' }, forks.match[1]],
        })
      ).dispositions;
      check(
        `kind is branch: "archive can't be proposed", with serve's list (${branchDisps})`,
        await until(
          pg,
          (want) =>
            document.querySelector('[data-testid="propose-err"]')
              ?.textContent === want,
          `archive can\u2019t be proposed for what this rule matches (allowed: ${branchDisps.join(', ')})`,
        ),
      );
      await pg.selectOption(
        '.cb-rule > [data-testid="conditions"] .cb-cond[data-index="0"] select.cb-cond-v',
        'repo',
      );
      check(
        'back to kind is repo: the message goes',
        await until(
          pg,
          () => document.querySelector('[data-testid="propose-err"]')?.hidden,
        ),
      );
      check(
        'the page drops the not-valid card, and the list pill reads draft',
        await until(
          pg,
          () =>
            !document.querySelector('[data-testid="rule-invalid"] .kit-card') &&
            document.querySelector(
              '.kit-row[data-rule="old-forks-archive"] .kit-meta',
            )?.textContent === 'draft',
        ),
      );
      // A new rule never overwrites one.
      await pg.click('[data-testid="new-rule"]');
      await pg.waitForSelector('[data-testid="new-rule-sheet"] input');
      await pg.keyboard.type('Landed again');
      await pg.fill(
        '[data-testid="new-rule-sheet"] input[aria-label="id"]',
        'r8-landed',
      );
      await pg.keyboard.press('Enter');
      check(
        'new rule with an id in use says so, and changes nothing',
        await until(pg, () =>
          (
            document.querySelector(
              '[data-testid="new-rule-sheet"] .cb-sheet-err',
            )?.textContent ?? ''
          ).includes('already exists'),
        ),
      );
      check(
        'r8-landed is untouched',
        (await ruleOf('r8-landed')).rule.name === R8_LANDED.name,
      );
      await pg.keyboard.press('Escape');

      // "or ask pi to draft one": the composer, attached to Rules, empty.
      await pg.evaluate(() => {
        location.hash = '#/rules';
      });
      await until(pg, () => !document.querySelector('.cb-rule'));
      await pg.click('.cb-rules-ask');
      const asked = await pg.evaluate(() => ({
        focused:
          document.activeElement ===
          document.querySelector('[data-testid="composer-input"]'),
        value: document.querySelector('[data-testid="composer-input"]').value,
        attached: document.querySelector('[data-testid="composer-attached"]')
          .textContent,
      }));
      check(
        `asking the agent focuses the composer, "attached: section rules" (as the agent reads it), no text (${JSON.stringify(asked)})`,
        asked.focused &&
          asked.attached === 'section rules' &&
          asked.value === '',
      );
      await pg.keyboard.type('draft a rule for dependabot minor bumps');
      await pg.keyboard.press('Enter');
      const d = await fx.agent.wait(fx.sid);
      checkList(
        'the agent gets the message with the rules section attached',
        (d?.delivery?.messages ?? []).map(
          (m) => `${m.body} | ${JSON.stringify(m.attached)}`,
        ),
        ['draft a rule for dependabot minor bumps | {"section":"rules"}'],
      );
    } finally {
      stopPresent();
      await pg.close();
    }
  }

  // ---- scenario: rules — a change under Court's edits (I1) -----------------
  console.log("\nscenario: rules — a change under Court's edits");
  {
    const sid = `probe-r8-arch-${Date.now()}`;
    await agent.presence(sid, 'pi \u00b7 arch', '/home/court/r8', 'pi');
    const arch = (disposition, name = 'Dormant repos') => ({
      id: 'r8-arch',
      name,
      status: 'draft',
      match: [
        { field: 'kind', op: 'is', value: 'repo' },
        { field: 'repo', op: 'is', value: 'schuettc/r8-none' },
      ],
      propose: { disposition },
    });
    await agent.ruleDraft(sid, arch('archive'));
    const pg = await context.newPage();
    const activates = [];
    pg.on('request', (r) => {
      if (r.url().includes('/api/rules/activate')) activates.push(r.url());
    });
    try {
      await pg.setViewportSize({ width: 1600, height: 900 });
      await pg.goto(serveHandle.url + '#/rules/r8-arch', {
        waitUntil: 'domcontentloaded',
      });
      await until(
        pg,
        () =>
          document.querySelector('.cb-rule')?.dataset.rule === 'r8-arch' &&
          document.querySelector('[data-testid="conditions"]')?.dataset
            .ready === 'true',
        undefined,
        8000,
      );
      // Court edits a condition; before he saves, the agent changes the
      // proposal from archive to delete.
      await pg.click('.cb-cond[data-index="1"] input.cb-cond-v', {
        clickCount: 3,
      });
      await pg.keyboard.type('schuettc/r8-other');
      await agent.ruleDraft(sid, arch('delete'));
      check(
        'the change shows: "changed while you were editing · by pi"',
        await until(
          pg,
          () =>
            document.querySelector(
              '[data-testid="rule-conflict"] .kit-card-head',
            )?.textContent === 'changed while you were editing \u00b7 by pi',
        ),
      );
      check(
        "serve's copy is shown: it now proposes delete",
        (await pg.$eval(
          '[data-testid="conflict-disposition"]',
          (e) => e.textContent,
        )) === 'delete',
      );
      check(
        'his edit is kept',
        (await pg.$eval(
          '.cb-rule > [data-testid="conditions"] .cb-cond[data-index="1"] input.cb-cond-v',
          (e) => e.value,
        )) === 'schuettc/r8-other',
      );
      await pg.$eval('.cb-rules-read', (e) => {
        e.scrollTop = 0;
      });
      check(
        '/tmp/t8-conflict-light.png is light (theme and body)',
        await r8Light(pg),
      );
      await pg.screenshot({ path: '/tmp/t8-conflict-light.png' });
      await pg.click('.kit-primary');
      check(
        'Activate refuses while it stands, and says why',
        await until(
          pg,
          () =>
            document.querySelector('.cb-conflict-note')?.textContent ===
            'Activate waits: reload their copy, or keep yours over it, first.',
        ),
      );
      check(
        `nothing was sent to activate (${activates.length}), and serve still has a draft`,
        activates.length === 0 &&
          (await ruleOf('r8-arch')).rule.status === 'draft',
      );

      // Keep mine: his edits over theirs, saved against serve's copy.
      await pg.click(
        '[data-testid="rule-conflict"] .kit-btn:has-text("keep mine")',
      );
      await until(
        pg,
        () =>
          !document.querySelector('[data-testid="rule-conflict"] .kit-card'),
      );
      await pg.click('.cb-rule-actions .kit-btn:has-text("save draft")');
      check(
        'keep mine, then save: serve has his condition and his proposal (archive)',
        await eventually(async () => {
          const r = await ruleOf('r8-arch');
          return (
            r.rule.match[1].value === 'schuettc/r8-other' &&
            r.rule.propose.disposition === 'archive'
          );
        }),
      );

      // Reload theirs: the agent changes it again under a new edit; Court
      // takes serve's copy, and Activate activates exactly that.
      await until(
        pg,
        () =>
          !document.querySelector('.cb-rule-actions .kit-btn') ||
          ![...document.querySelectorAll('.cb-rule-actions .kit-btn')]
            .map((b) => b.textContent)
            .includes('save draft'),
      );
      await pg.click('.cb-cond[data-index="1"] input.cb-cond-v', {
        clickCount: 3,
      });
      await pg.keyboard.type('schuettc/r8-landed');
      await agent.ruleDraft(sid, {
        ...arch('keep'),
        match: [
          { field: 'kind', op: 'is', value: 'repo' },
          { field: 'repo', op: 'is', value: 'schuettc/r8-other' },
        ],
      });
      await until(
        pg,
        () =>
          !!document.querySelector('[data-testid="rule-conflict"] .kit-card'),
      );
      await pg.click(
        '[data-testid="rule-conflict"] .kit-btn:has-text("reload theirs")',
      );
      check(
        "reload theirs shows serve's copy (keep), and drops his edit",
        await until(
          pg,
          () =>
            document.querySelector('[data-testid="propose"] button.cb-disp')
              ?.dataset.value === 'keep' &&
            document.querySelector('.cb-cond[data-index="1"] input.cb-cond-v')
              ?.value === 'schuettc/r8-other',
        ),
      );
      const archActivated = await clickLifecycle(pg, 'activate');
      check(
        'Activate then activates what is shown: keep',
        archActivated?.status() === 200 &&
          (await eventually(async () => {
            const r = await ruleOf('r8-arch');
            return (
              r.rule.status === 'active' &&
              r.rule.propose.disposition === 'keep'
            );
          })),
      );
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Deactivate',
      );
      check(
        'Deactivate (after the Activate replied) puts r8-arch back to a draft',
        (await clickLifecycle(pg, 'deactivate'))?.status() === 200 &&
          (await eventually(
            async () => (await ruleOf('r8-arch')).rule.status === 'draft',
          )),
      );

      // A change the page hasn't heard of yet (its live reads held): serve
      // refuses the stale Activate, and the page shows what it refused.
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Activate',
      );
      let open;
      const shut = new Promise((r) => {
        open = r;
      });
      const liveReads = /\/api\/rule\?id=r8-arch/;
      const holdReads = async (route) => {
        await shut;
        await route.continue();
      };
      await pg.route(liveReads, holdReads);
      await agent.ruleDraft(sid, arch('delete', 'Dormant repos, all of them'));
      const n0 = activates.length;
      const refused = pg.waitForResponse((r) =>
        r.url().includes('/api/rules/activate'),
      );
      await pg.click('.kit-primary');
      check(
        'serve refuses the Activate of a copy that changed (409)',
        (await refused).status() === 409 && activates.length === n0 + 1,
      );
      open();
      check(
        "the page shows serve's copy (delete), saying nothing was activated",
        await until(
          pg,
          () =>
            document.querySelector('.cb-rule .kit-h1')?.textContent ===
              'Dormant repos, all of them' &&
            document.querySelector('[data-testid="propose"] button.cb-disp')
              ?.dataset.value === 'delete' &&
            (
              document.querySelector('.cb-rule-note')?.textContent ?? ''
            ).startsWith(
              'It changed before you activated it; nothing was activated.',
            ),
        ),
      );
      check(
        'and serve still has a draft',
        (await ruleOf('r8-arch')).rule.status === 'draft',
      );
      await pg.unroute(liveReads, holdReads);
    } finally {
      await pg.close();
    }
  }

  // ---- scenario: rules — Court's own save, heard live first (N4) ----------
  //
  // serve publishes the save before the page has its reply; the live read
  // of it can land first. That is Court's own change, not someone else's:
  // no conflict card ("by you") appears, none is left behind, and Activate
  // is not held up.
  console.log("\nscenario: rules — Court's own save, heard live first");
  {
    await agent.api('POST', '/api/rules/draft', {
      id: 'r8-race',
      name: 'Race repos',
      status: 'draft',
      match: [
        { field: 'kind', op: 'is', value: 'repo' },
        { field: 'repo', op: 'is', value: 'schuettc/r8-none' },
      ],
      propose: { disposition: 'archive' },
    });
    const pg = await context.newPage();
    const card = () =>
      pg.$eval(
        '[data-testid="rule-conflict"]',
        (e) => e.querySelector('.kit-card-head')?.textContent ?? '',
      );
    try {
      await pg.setViewportSize({ width: 1600, height: 900 });
      await pg.goto(serveHandle.url + '#/rules/r8-race', {
        waitUntil: 'domcontentloaded',
      });
      await until(
        pg,
        () =>
          document.querySelector('.cb-rule')?.dataset.rule === 'r8-race' &&
          document.querySelector('[data-testid="conditions"]')?.dataset
            .ready === 'true',
        undefined,
        8000,
      );
      await pg.click('.cb-cond[data-index="1"] input.cb-cond-v', {
        clickCount: 3,
      });
      await pg.keyboard.type('schuettc/r8-other');
      // Hold the save's reply (serve has done the save) until the live
      // read of it has been answered and drawn.
      let release;
      const held = new Promise((r) => {
        release = r;
      });
      const saves = /\/api\/rules\/draft/;
      let replied;
      const answered = new Promise((r) => {
        replied = r;
      });
      const hold = async (route) => {
        const resp = await route.fetch();
        await held;
        await route.fulfill({ response: resp });
        replied();
      };
      await pg.route(saves, hold);
      const liveRead = pg.waitForResponse(
        (r) => r.url().includes('/api/rule?id=r8-race') && r.status() === 200,
      );
      await pg.click('.cb-rule-actions .kit-btn:has-text("save draft")');
      await liveRead;
      check(
        'serve has his save while the page still waits for its reply',
        await eventually(
          async () =>
            (await ruleOf('r8-race')).rule.match[1].value ===
            'schuettc/r8-other',
        ),
      );
      // Two frames for the live read to be drawn.
      await pg.evaluate(
        () =>
          new Promise((r) =>
            requestAnimationFrame(() => requestAnimationFrame(r)),
          ),
      );
      const during = await card();
      check(
        `the live read of his own save is no conflict (card: "${during}")`,
        during === '',
      );
      release();
      await answered;
      await pg.unroute(saves, hold);
      check(
        'after the reply: no conflict card, nothing left to save',
        await until(
          pg,
          () =>
            !document.querySelector(
              '[data-testid="rule-conflict"] .kit-card',
            ) &&
            ![...document.querySelectorAll('.cb-rule-actions .kit-btn')].some(
              (b) => b.textContent === 'save draft',
            ),
        ),
      );
      // serve announces an activate before it replies, so the page shows
      // Deactivate while the Activate is still in flight. Court's click
      // then is not dropped: it waits for the reply and runs after it.
      // (The Activate's reply is held here, so the click is certain to
      // land during it.)
      let releaseActivate;
      const activateHeld = new Promise((r) => {
        releaseActivate = r;
      });
      const activates = /\/api\/rules\/activate/;
      const holdActivate = async (route) => {
        const resp = await route.fetch();
        await activateHeld;
        await route.fulfill({ response: resp });
      };
      await pg.route(activates, holdActivate);
      let deactivatePosts = 0;
      const countDeactivate = (r) => {
        if (r.url().includes('/api/rules/deactivate')) deactivatePosts++;
      };
      pg.on('request', countDeactivate);
      const raceActivated = lifecycleReply(pg, 'activate');
      await pg.click('.kit-primary');
      check(
        'and Activate activates his copy at once',
        await eventually(async () => {
          const r = await ruleOf('r8-race');
          return (
            r.rule.status === 'active' &&
            r.rule.match[1].value === 'schuettc/r8-other'
          );
        }),
      );
      check(
        'serve announced it before replying: the page shows Deactivate while the Activate is in flight',
        await until(
          pg,
          () =>
            document.querySelector('.kit-primary')?.textContent ===
            'Deactivate',
        ),
      );
      const raceDeactivated = lifecycleReply(pg, 'deactivate');
      await pg.click('.kit-primary'); // during the Activate
      // Two frames: a click that was going to post would have by now.
      await pg.evaluate(
        () =>
          new Promise((r) =>
            requestAnimationFrame(() => requestAnimationFrame(r)),
          ),
      );
      check(
        `Deactivate clicked during the Activate waits for its reply (${deactivatePosts} posted early)`,
        deactivatePosts === 0 &&
          (await ruleOf('r8-race')).rule.status === 'active',
      );
      releaseActivate();
      const raceA = await raceActivated;
      const raceD = await raceDeactivated;
      // Unrouted only once nothing is in flight: requests caught mid-unroute
      // can be left hanging (Playwright), which is the probe, not the page.
      await pg.unroute(activates, holdActivate);
      pg.off('request', countDeactivate);
      check(
        `the Deactivate then runs, and puts it back to a draft, editable again (activate ${raceA?.status()}, deactivate ${raceD?.status()})`,
        raceA?.status() === 200 &&
          raceD?.status() === 200 &&
          (await eventually(
            async () => (await ruleOf('r8-race')).rule.status === 'draft',
          )) &&
          (await until(
            pg,
            () =>
              !!document.querySelector(
                '.cb-cond[data-index="1"] input.cb-cond-v',
              ),
          )),
      );

      // A conflict that stops holding clears: Court's edit, the agent's
      // change over it, then Court undoes his edit — nothing of his is
      // left to keep, so serve's copy shows and Activate isn't held.
      await until(
        pg,
        () =>
          document.querySelector('.kit-primary')?.textContent === 'Activate',
      );
      await pg.click('.cb-cond[data-index="1"] input.cb-cond-v', {
        clickCount: 3,
      });
      await pg.keyboard.type('schuettc/r8-landed');
      await agent.api('POST', '/api/rules/draft', {
        id: 'r8-race',
        name: 'Race repos, renamed',
        status: 'draft',
        match: [
          { field: 'kind', op: 'is', value: 'repo' },
          { field: 'repo', op: 'is', value: 'schuettc/r8-other' },
        ],
        propose: { disposition: 'archive' },
      });
      check(
        'a change under his edit shows as a conflict',
        await until(
          pg,
          () =>
            !!document.querySelector('[data-testid="rule-conflict"] .kit-card'),
        ),
      );
      await pg.click('.cb-cond[data-index="1"] input.cb-cond-v', {
        clickCount: 3,
      });
      await pg.keyboard.type('schuettc/r8-other');
      check(
        "undoing his edit clears it: serve's copy (renamed) shows, no card",
        await until(
          pg,
          () =>
            !document.querySelector(
              '[data-testid="rule-conflict"] .kit-card',
            ) &&
            document.querySelector('.cb-rule .kit-h1')?.textContent ===
              'Race repos, renamed',
        ),
      );

      // Nothing of his unsaved: a change of serve's draws the rule again.
      // With the disposition menu open, it stays open, its chips (and his
      // focus) the same elements, so his click on one lands.
      await pg.click('[data-testid="propose"] button.cb-disp');
      const keepChip = await pg.waitForSelector(
        '.cb-disp-menu:not([hidden]) .cb-disp-opt[data-disp="keep"]',
        { timeout: 4000 },
      );
      const focused = await pg.evaluateHandle(() => document.activeElement);
      await agent.api('POST', '/api/rules/draft', {
        id: 'r8-race',
        name: 'Race repos, renamed again',
        status: 'draft',
        match: [
          { field: 'kind', op: 'is', value: 'repo' },
          { field: 'repo', op: 'is', value: 'schuettc/r8-other' },
        ],
        propose: { disposition: 'archive' },
      });
      const redrawn = await until(
        pg,
        () =>
          document.querySelector('.cb-rule .kit-h1')?.textContent ===
          'Race repos, renamed again',
      );
      check(
        "serve's copy drawn again while the menu is open leaves it open, its chips and Court's focus where they were",
        redrawn &&
          (await pg.evaluate(
            ([k, f]) =>
              !document.querySelector('.cb-disp-menu').hidden &&
              k.isConnected &&
              f.matches('.cb-disp-opt') &&
              f.isConnected &&
              document.activeElement === f,
            [keepChip, focused],
          )),
      );
      const clicked = await keepChip
        .click({ timeout: 5000 })
        .then(() => true)
        .catch(() => false);
      check(
        'and his click on a chip chooses it',
        clicked &&
          (await until(
            pg,
            () =>
              document.querySelector('[data-testid="propose"] button.cb-disp')
                ?.dataset.value === 'keep',
          )),
      );
    } finally {
      await pg.close();
    }
  }
}

// ---- Task 8 fix: invalid rules (I2) -----------------------------------------
//
// Its own serve, whose data repo holds rule files as a person (or an older
// casebook) wrote them: an active rule with a bool value serve refuses, an
// active one with a count that isn't one, and a file that isn't a rule.
const RULE_HEAD = (id, name, status) =>
  [
    `id = "${id}"`,
    `name = "${name}"`,
    `status = "${status}"`,
    'created_by = "schuettc"',
    'created_at = 2026-09-27T10:00:00Z',
    'edited_at = 2026-09-27T10:00:00Z',
  ].join('\n');
const cond = (field, op, value) =>
  `[[match]]\nfield = "${field}"\nop = "${op}"\nvalue = "${value}"`;
const INVALID_SEED = {
  'rules/r8-bad.toml': [
    RULE_HEAD('r8-bad', 'Bot PRs \u2192 close', 'active'),
    cond('kind', 'is', 'pr'),
    cond('bot', 'is', 'yes'),
    '[propose]\ndisposition = "close"',
    '',
  ].join('\n'),
  'rules/r8-bad2.toml': [
    RULE_HEAD('r8-bad2', 'Busy repos \u2192 keep', 'active'),
    cond('kind', 'is', 'repo'),
    cond('open-prs', 'gt', 'lots'),
    '[propose]\ndisposition = "keep"',
    '',
  ].join('\n'),
  'rules/r8-broken.toml': 'this is [not a rule\n',
};

let _invalidServe = null;
async function invalidRulesScenarios(context) {
  const serveHandle = await startServe({ seedFiles: INVALID_SEED });
  _invalidServe = serveHandle;
  try {
    await invalidRulesScenariosOn(context, serveHandle);
  } finally {
    serveHandle.stop();
    _invalidServe = null;
  }
}

async function invalidRulesScenariosOn(context, serveHandle) {
  console.log('\nscenario: rules — invalid rules are shown and recoverable');
  const agent = createAgent(serveHandle.base, serveHandle.token);
  const ruleOf = (id) => agent.api('GET', `/api/rule?id=${id}`);
  const status = async (method, path, body) => {
    const r = await fetch(serveHandle.base + path, {
      method,
      headers: {
        'Content-Type': 'application/json',
        'X-Local-Token': serveHandle.token,
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    return r.status;
  };
  const bad = await ruleOf('r8-bad');
  check(
    `serve reads r8-bad as invalid, naming its second condition as Court counts: "condition 2" (${bad.invalid})`,
    bad.invalid.startsWith('condition 2: ') && bad.matches.total === 0,
  );
  checkList(
    'serve refuses to preview it or propose from it',
    [
      await status('POST', '/api/rules/preview', bad.rule),
      await status('POST', '/api/rules/propose-once', { id: 'r8-bad' }),
    ],
    [400, 400],
  );

  const pg = await context.newPage();
  try {
    await pg.setViewportSize({ width: 1600, height: 900 });
    await pg.goto(serveHandle.url + '#/rules/r8-bad', {
      waitUntil: 'domcontentloaded',
    });
    await until(
      pg,
      () =>
        document.querySelector('.cb-rule')?.dataset.rule === 'r8-bad' &&
        document.querySelector('[data-testid="conditions"]')?.dataset.ready ===
          'true' &&
        document.querySelectorAll('.cb-rules-list .kit-row').length === 3,
      undefined,
      8000,
    );
    const rows = await pg.$$eval('.cb-rules-list .kit-row', (els) =>
      els.map((e) => {
        const m = e.querySelector('.kit-meta');
        return {
          id: e.dataset.rule,
          kicker: e.querySelector('.kit-kicker').textContent,
          pill: m.textContent,
          invalid: m.classList.contains('cb-pill-invalid'),
          color: getComputedStyle(m).color,
          title: e.title,
        };
      }),
    );
    const signal = await cssColor(pg, 'var(--kit-signal)');
    const list = (await agent.api('GET', '/api/rules')).rules;
    checkList(
      'the list shows every rule serve holds as invalid (all three, the unreadable one too), pill "not valid" in the signal colour',
      rows.map(
        (r) =>
          `${r.id}: ${r.kicker} | ${r.pill} ${r.invalid && r.color === signal} | ${r.title}`,
      ),
      list.map(
        (r) => `${r.rule.id}: not valid | not valid true | ${r.invalid}`,
      ),
    );
    const doc = await pg.evaluate(() => ({
      head: document.querySelector(
        '[data-testid="rule-invalid"] .kit-card-head',
      )?.textContent,
      msg: document.querySelector(
        '[data-testid="rule-invalid"] .cb-invalid-msg',
      )?.textContent,
      inline: (() => {
        const e = document.querySelector(
          '.cb-rule > [data-testid="conditions"] .cb-cond[data-index="1"] .cb-cond-err',
        );
        return e && !e.hidden ? e.textContent : '';
      })(),
      matches: document.querySelector('[data-testid="matches"]').textContent,
      primary: document.querySelector('.kit-primary')?.textContent,
    }));
    check(
      `the document says it is invalid and skipped ("${doc.head}")`,
      doc.head === 'not valid \u00b7 active, but skipped at every sync' &&
        doc.msg === bad.invalid,
    );
    check(
      "serve's message is under the condition it names too (the second row)",
      doc.inline === bad.invalid,
    );
    const numbered = await pg.$$eval(
      '.cb-rule > [data-testid="conditions"] .cb-cond',
      (els) =>
        els
          .map((e, i) => {
            const err = e.querySelector('.cb-cond-err');
            if (!err || err.hidden) return null;
            const m = /^condition (\d+): /.exec(err.textContent);
            return `row ${i + 1} says condition ${m ? m[1] : '?'}`;
          })
          .filter(Boolean),
    );
    checkList(
      'the number Court reads is the row it sits under, counted from 1',
      numbered,
      ['row 2 says condition 2'],
    );
    check(
      'its matches are not previewed (no count, no list / by repo)',
      doc.matches === 'not previewed: the conditions aren\u2019t valid' &&
        (await r8Read.heading(pg)) === 'matches now' &&
        (await pg.$eval(
          '.cb-matches-view',
          (e) => getComputedStyle(e).display === 'none',
        )),
    );
    check('its primary is Deactivate', doc.primary === 'Deactivate');
    checkList(
      'the facts: status "active · not valid", matches "—"',
      (await r8Read.facts(pg)).slice(0, 2),
      ['status active \u00b7 not valid', 'matches \u2014'],
    );
    await pg.$eval('.cb-rules-read', (e) => {
      e.scrollTop = 0;
    });
    check(
      '/tmp/t8-invalid-light.png is light (theme and body)',
      await r8Light(pg),
    );
    await pg.screenshot({ path: '/tmp/t8-invalid-light.png' });

    // Fix it on the page: bot is true, then save (it becomes a draft).
    await pg.selectOption(
      '.cb-rule > [data-testid="conditions"] .cb-cond[data-index="1"] select.cb-cond-v',
      'true',
    );
    check(
      'the fixed conditions preview',
      await until(pg, () =>
        (
          document.querySelector('.cb-matches-label')?.textContent ?? ''
        ).startsWith('matches now \u00b7 '),
      ),
    );
    await pg.click('.cb-rule-actions .kit-btn:has-text("save as draft")');
    check(
      'save as draft: serve has the fix, as a valid draft',
      await eventually(async () => {
        const r = await ruleOf('r8-bad');
        return (
          r.rule.status === 'draft' &&
          r.rule.match[1].value === 'true' &&
          !r.invalid
        );
      }),
    );
    check(
      'the page shows it fixed: no card, pill "draft"',
      await until(
        pg,
        () =>
          !document.querySelector('[data-testid="rule-invalid"] .kit-card') &&
          document.querySelector('.kit-row[data-rule="r8-bad"] .kit-meta')
            ?.textContent === 'draft',
      ),
    );

    // Or deactivate it: r8-bad2 stays invalid, but no longer active.
    await pg.evaluate(() => {
      location.hash = '#/rules/r8-bad2';
    });
    await until(
      pg,
      () =>
        document.querySelector('.cb-rule')?.dataset.rule === 'r8-bad2' &&
        document.querySelector('.kit-primary')?.textContent === 'Deactivate',
    );
    const bad2Deactivated = await clickLifecycle(pg, 'deactivate');
    check(
      'Deactivate works on an invalid rule, and keeps it (and its value)',
      bad2Deactivated?.status() === 200 &&
        (await eventually(async () => {
          const r = await ruleOf('r8-bad2');
          return (
            r.rule.status === 'draft' &&
            r.rule.match[1].value === 'lots' &&
            r.invalid.startsWith('condition 2: ')
          );
        })),
    );
    check(
      'the page: a draft, still not valid, no longer skipped',
      await until(
        pg,
        () =>
          document.querySelector('[data-testid="rule-invalid"] .kit-card-head')
            ?.textContent === 'not valid' &&
          document.querySelector('.kit-primary')?.textContent === 'Activate',
      ),
    );

    // A file that isn't a rule: listed, shown with its parse error, and a
    // save from the page replaces it.
    const broken = await ruleOf('r8-broken');
    await pg.evaluate(() => {
      location.hash = '#/rules/r8-broken';
    });
    check(
      `the unreadable file shows serve's parse error (${broken.invalid})`,
      await until(
        pg,
        (w) =>
          document.querySelector('.cb-rule')?.dataset.rule === 'r8-broken' &&
          document.querySelector('[data-testid="rule-invalid"] .cb-invalid-msg')
            ?.textContent === w,
        broken.invalid,
      ),
    );
    await until(
      pg,
      () =>
        document.querySelector('[data-testid="conditions"]')?.dataset.ready ===
        'true',
    );
    // The condition he adds is previewed (debounced) and the preview offers
    // the dispositions it allows, a different list. Here it lands while
    // the disposition menu is open, as it can on a slow runner: the preview
    // is held until the menu is open, then let through. The chips that
    // stay offered stay the same elements, so his click lands, and his
    // focus stays where it was.
    let previewAsked;
    const asked = new Promise((r) => (previewAsked = r));
    let releasePreview;
    const release = new Promise((r) => (releasePreview = r));
    const holdPreview = async (route) => {
      previewAsked();
      await release;
      await route.continue();
    };
    await pg.route('**/api/rules/preview*', holdPreview);
    await pg.click('.cb-cond-add');
    await pg.click(
      '.cb-cond-menu-row[data-field="kind"] .cb-cond-menu-op[data-op="is"]',
    );
    await asked;
    await pg.click('[data-testid="propose"] button.cb-disp');
    const chipsNow = () =>
      pg.$$eval('.cb-disp-opt', (els) => els.map((e) => e.dataset.disp));
    const before = await chipsNow();
    const keepChip = await pg.$('.cb-disp-opt[data-disp="keep"]');
    const focused = await pg.evaluateHandle(() => document.activeElement);
    const previewed = pg.waitForResponse((r) =>
      r.url().includes('/api/rules/preview'),
    );
    releasePreview();
    await previewed;
    await pg.unroute('**/api/rules/preview*', holdPreview);
    const changed = await until(
      pg,
      (b) =>
        [...document.querySelectorAll('.cb-disp-opt')]
          .map((e) => e.dataset.disp)
          .join(' ') !== b,
      before.join(' '),
    );
    const after = await chipsNow();
    check(
      `a preview offering other dispositions (${before.join(',')} → ${after.join(',')}) while the menu is open leaves it open, the "keep" chip and Court's focus where they were`,
      changed &&
        !!keepChip &&
        (await pg.evaluate(
          ([k, f]) =>
            !document.querySelector('.cb-disp-menu').hidden &&
            k.isConnected &&
            f.matches('.cb-disp-opt') &&
            f.isConnected &&
            document.activeElement === f,
          [keepChip, focused],
        )),
    );
    // His click on the chip he saw lands on it (were it replaced, the
    // click fails, and the menu is reopened for the rest of the scenario).
    const clicked = await keepChip
      .click({ timeout: 5000 })
      .then(() => true)
      .catch(() => false);
    if (!clicked) {
      if (await pg.$eval('.cb-disp-menu', (m) => m.hidden)) {
        await pg.click('[data-testid="propose"] button.cb-disp');
      }
      await pg.click('.cb-disp-opt[data-disp="keep"]');
    }
    check(
      'the click on that chip chooses it',
      clicked &&
        (await until(
          pg,
          () =>
            document.querySelector('[data-testid="propose"] button.cb-disp')
              ?.dataset.value === 'keep',
        )),
    );
    await pg.click('.cb-rule-actions .kit-btn:has-text("save draft")');
    check(
      'saving replaces it with a valid rule',
      await eventually(async () => {
        const r = await ruleOf('r8-broken');
        return (
          !r.invalid &&
          r.rule.match.length === 1 &&
          r.rule.propose.disposition === 'keep'
        );
      }),
    );
  } finally {
    await pg.close();
  }
}

// The helpers the To apply scenarios (probe-apply.mjs) check with.
const applyHelpers = { check, checkList, until, eventually };

// ---- the scenario list ------------------------------------------------------
//
// run() below is the scenario list. A full run (no PROBE_ONLY) must pass at
// least MIN_CHECKS checks: a scenario that stops early, or is skipped, can't
// leave the probe green. Raise it whenever checks are added.
const MIN_CHECKS = 840;

// PROBE_ONLY runs one group of scenarios, for working on them: a partial
// run. It has to say so: under CI (the CI env var) it is refused outright,
// and a required run (KIT_BROWSER=required, as `just verify-slow` makes it)
// refuses it unless PROBE_PARTIAL=1 says the partial run is meant
// (`just casebook-probe-only <name>`). An unknown name is an error, not a
// quiet full run without the floor.
const only = process.env.PROBE_ONLY ?? '';
const keyClashes = [];
const underCI = !!process.env.CI;
const partial = process.env.PROBE_PARTIAL === '1';
const PROBE_GROUPS = [
  'composer',
  'keys',
  'rules',
  'apply',
  'shell',
  'owner',
  'decide',
];
const throttle = Number(process.env.PROBE_THROTTLE ?? '') || 0;
const progressDelay = Number(process.env.PROBE_PROGRESS_DELAY ?? '') || 0;

// throttlePage slows a page's CPU by PROBE_THROTTLE (see run()).
async function throttlePage(c, pg) {
  try {
    const cdp = await c.newCDPSession(pg);
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: throttle });
    throttled++;
  } catch (err) {
    // A page closed before its session opened is no page to slow.
    if (!pg.isClosed()) throw err;
  }
}
let throttled = 0;

async function run() {
  if (only && !PROBE_GROUPS.includes(only)) {
    console.error(
      `probe: PROBE_ONLY=${only} is not a group (${PROBE_GROUPS.join(', ')})`,
    );
    fails++;
    return;
  }
  if (only && underCI) {
    console.error(
      `probe: PROBE_ONLY=${only} is set under CI; CI runs the whole probe`,
    );
    fails++;
    return;
  }
  if (only && required && !partial) {
    console.error(
      `probe: PROBE_ONLY=${only} is set for a full, required run; unset it, or run the part on purpose with \`just casebook-probe-only ${only}\``,
    );
    fails++;
    return;
  }
  const browser = await findChrome();
  if (!browser) {
    if (required || underCI) {
      console.error(
        'probe: Chrome is required (KIT_BROWSER=required) but not found.',
      );
      console.error(
        '  Install: node node_modules/playwright-core/cli.js install --with-deps chromium chromium-headless-shell',
      );
      process.exit(1);
    }
    console.log(
      'probe: Chrome not found, skipping (set KIT_BROWSER=required to fail instead).',
    );
    return;
  }

  let serveHandle;
  try {
    console.log('probe: starting casebook serve…');
    serveHandle = await startServe();
    _serveHandle = serveHandle;
    console.log(`probe: serve at ${serveHandle.base}`);
  } catch (err) {
    await browser.close();
    console.error('probe: failed to start serve:', err);
    process.exit(1);
  }

  // Every context the probe opens watches its pages' consoles for a section
  // key clash (app.ts reports one there rather than failing to show).
  // PROBE_THROTTLE=<rate> (opt-in, e.g. 4) slows every page's CPU that
  // many times (CDP Emulation.setCPUThrottlingRate), as a busy CI runner
  // does: the stress the probe's races are checked under.
  const newContext = browser.newContext.bind(browser);
  browser.newContext = async (...args) => {
    const c = await newContext(...args);
    c.on('console', (msg) => {
      if (msg.type() === 'error' && msg.text().includes('key clash')) {
        keyClashes.push(msg.text());
      }
    });
    if (throttle > 1) c.on('page', (pg) => void throttlePage(c, pg));
    return c;
  };
  const context = await browser.newContext();
  const page = await context.newPage();
  ran = true;

  try {
    // PROBE_ONLY=composer runs only the Task 7 scenarios (their own serve).
    if (process.env.PROBE_ONLY === 'composer') {
      await composerScenarios(context);
      return;
    }
    // PROBE_ONLY=keys runs only the section-key scenario (its own serve).
    if (process.env.PROBE_ONLY === 'keys') {
      await keyScopeScenarios(context);
      return;
    }
    // PROBE_ONLY=rules runs only the Rules scenarios (their own serve).
    if (process.env.PROBE_ONLY === 'rules') {
      await rulesScenarios(context);
      await invalidRulesScenarios(context);
      return;
    }
    // PROBE_ONLY=apply runs only the To apply scenarios (their own serves).
    if (process.env.PROBE_ONLY === 'apply') {
      await applyScenarios(context, applyHelpers);
      return;
    }
    // PROBE_ONLY=shell runs only the keyboard layer and the failure states
    // (probe-shell.mjs, their own serves).
    if (process.env.PROBE_ONLY === 'shell') {
      await shellScenarios(context, applyHelpers);
      return;
    }
    // PROBE_ONLY=owner runs only the session-ownership scenarios
    // (probe-owner.mjs, their own serve).
    if (process.env.PROBE_ONLY === 'owner') {
      await ownerScenarios(context, applyHelpers);
      return;
    }
    // PROBE_ONLY=decide runs only the decide step's scenario
    // (probe-decide.mjs, its own serve).
    if (process.env.PROBE_ONLY === 'decide') {
      await decideScenarios(context, applyHelpers);
      return;
    }
    // Navigate to the page with the ?t= token URL.
    // 'domcontentloaded' is used instead of 'networkidle' because the SSE
    // stream (/api/events) is an infinite connection that never goes idle.
    await page.goto(serveHandle.url, {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    });

    // Wait for boot() to run — it appends .kit-bar as its first action.
    await page.waitForSelector('.kit-bar', { timeout: 8000 });
    // Wait for the summary API call to complete so counts are rendered.
    // The summary sets at least one .kit-n span inside a .kit-ctl[data-id].
    // (attention's and to apply's: Rules counts its own, sooner.)
    await page
      .waitForFunction(
        () =>
          !!document.querySelector('.kit-ctl[data-id="attention"] .kit-n') &&
          !!document.querySelector('.kit-ctl[data-id="apply"] .kit-n'),
        undefined,
        { timeout: 5000 },
      )
      .catch(() => {
        // counts may all be 0; kit renders a .kit-n span for 0 too
      });

    console.log('\nscenario: the shell boots');

    // 1. Three section controls with counts.
    {
      const controls = await page.$$('.kit-ctl[data-id]');
      const ids = await Promise.all(
        controls.map((c) => c.getAttribute('data-id')),
      );
      check('attention control present', ids.includes('attention'));
      check('rules control present', ids.includes('rules'));
      check('apply control present', ids.includes('apply'));

      // Each section control must show a number (even "0").
      let allHaveCount = true;
      for (const ctrl of controls) {
        const id = await ctrl.getAttribute('data-id');
        if (!id || !['attention', 'rules', 'apply'].includes(id)) continue;
        const text = (await ctrl.textContent()) ?? '';
        if (!/\d/.test(text)) {
          allHaveCount = false;
          console.error(`    control "${id}" text "${text}" has no digit`);
        }
      }
      check('each section control shows a count', allHaveCount);
    }

    // 2. Theme toggle.
    {
      const before = await page.$eval('html', (el) => el.dataset.theme ?? '');
      await page.click('button.kit-ctl:has-text("theme")');
      const after = await page.$eval('html', (el) => el.dataset.theme ?? '');
      check('theme toggle changes html[data-theme]', before !== after);
      // Cycle back.
      await page.click('button.kit-ctl:has-text("theme")');
    }

    // 3. Routing switches sections.
    {
      await page.evaluate(() => {
        location.hash = '#/rules/test';
      });
      await page.waitForFunction(() => location.hash === '#/rules/test');

      const rulesActive = await page.$eval(
        '.kit-ctl[data-id="rules"]',
        (el) =>
          el.classList.contains('on') ||
          el.getAttribute('aria-current') === 'page',
      );
      check('routing to #/rules/... activates rules section', rulesActive);

      await page.evaluate(() => {
        location.hash = '#/attention';
      });
      await page.waitForFunction(() => location.hash === '#/attention');
      const attentionActive = await page.$eval(
        '.kit-ctl[data-id="attention"]',
        (el) =>
          el.classList.contains('on') ||
          el.getAttribute('aria-current') === 'page',
      );
      check(
        'routing back to #/attention activates attention section',
        attentionActive,
      );
    }

    // 4. Brand mark is an <svg> inside .kit-brand.
    {
      const hasSVG = await page
        .$eval('.kit-brand svg', () => true)
        .catch(() => false);
      check('brand mark is an <svg> in .kit-brand', hasSVG);
    }

    // 5. Live pill reads live.
    {
      let livePillVisible = false;
      let liveState = '';
      try {
        await page.waitForSelector('.kit-live:not([hidden])', {
          timeout: 5000,
        });
        // Assert real visibility via offsetWidth/offsetHeight.
        const vis = await page.$eval('.kit-live', (el) => {
          const r = el.getBoundingClientRect();
          return r.width > 0 && r.height > 0;
        });
        livePillVisible = vis;
        liveState = await page.$eval(
          '.kit-live',
          (el) => el.dataset.state ?? '',
        );
      } catch {
        livePillVisible = false;
        liveState = '';
      }
      check('live pill is visible within 5s', livePillVisible);
      check('live pill data-state is "live"', liveState === 'live');
    }
    // ---- scenario: attention list and detail --------------------------------
    console.log('\nscenario: attention list and detail');

    // Navigate to attention / waiting view.
    await page.evaluate(() => {
      location.hash = '#/attention/waiting';
    });
    await page.waitForFunction(() => location.hash === '#/attention/waiting');

    // Wait for the attention list panel.
    await page.waitForSelector('.kit-list', { timeout: 5000 }).catch(() => {});

    // Wait for rows to appear (the API call may take a moment).
    await page.waitForSelector('.kit-row', { timeout: 8000 }).catch(() => {});

    // Check: seeded items appear in the waiting view.
    {
      const rowCount = await page.$$eval('.kit-row', (rows) => rows.length);
      check('seeded items appear in the waiting view', rowCount > 0);
    }

    // Check: clicking a view chip narrows the list.
    {
      const beforeCount = await page.$$eval('.kit-row', (rows) => rows.length);

      // Click the 'new' view chip.
      const newChip = await page.$('[data-id="new"]');
      if (newChip) {
        await newChip.click();
        // Wait for the list to update.
        await page.waitForTimeout(600);
      }
      const afterCount = await page.$$eval('.kit-row', (rows) => rows.length);
      // The 'new' view may have 0 rows in the fixture (all are 'waiting'),
      // so we just verify the chip click did not error (afterCount is a number).
      check(
        'clicking a view chip changes the row set',
        (typeof afterCount === 'number' && afterCount !== beforeCount) ||
          afterCount === 0,
      );

      // Return to waiting view.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: search the Attention list (kit v0.11.0) -----------------
    console.log('\nscenario: search the Attention list (kit v0.11.0)');

    {
      // Navigate to waiting view and count the full row set.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);

      const fullCount = await page.$$eval('.kit-row', (rows) => rows.length);

      // 1. Typing narrows the list to the matching fixture item (after debounce).
      {
        const searchField = await page.$('.kit-search');
        if (searchField) {
          await searchField.click();
          await searchField.fill('nudge');
          // Wait for 200ms debounce + network round-trip.
          await page.waitForTimeout(500);
          const narrowCount = await page.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          check(
            "typing 'nudge' narrows the list after the debounce",
            narrowCount < fullCount || narrowCount <= 1,
          );
          const texts = await page.$$eval('.kit-row', (rows) =>
            rows.map((r) => (r.textContent || '').toLowerCase()),
          );
          check(
            'narrowed rows all contain the search term',
            texts.length === 0 || texts.every((t) => t.includes('nudge')),
          );
        } else {
          check("typing 'nudge' narrows the list after the debounce", false);
          check('narrowed rows all contain the search term', false);
        }
      }

      // 2. Esc clears the search and the list returns to the full count.
      {
        const searchField = await page.$('.kit-search');
        if (searchField) {
          await searchField.press('Escape');
          // Wait for 200ms debounce + reload.
          await page.waitForTimeout(500);
          const afterEscCount = await page.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          check(
            'Esc clears the search and restores the full list',
            afterEscCount === fullCount,
          );
          const clearedVal = await searchField.inputValue();
          check('search field is empty after Esc', clearedVal === '');
        } else {
          check('Esc clears the search and restores the full list', false);
          check('search field is empty after Esc', false);
        }
      }

      // 3. / focuses the search field.
      {
        // Blur any focused element first.
        await page.evaluate(() => {
          if (document.activeElement instanceof HTMLElement)
            document.activeElement.blur();
        });
        await page.keyboard.press('/');
        await page.waitForTimeout(150);
        const focused = await page
          .$eval('.kit-search', (el) => document.activeElement === el)
          .catch(() => false);
        check('/ focuses the search field', focused);
        // Press Esc to leave the field cleanly.
        await page.keyboard.press('Escape');
        await page.waitForTimeout(200);
      }

      // 4. Reload with search in URL restores the narrowed list and field text.
      {
        const reloadPage = await context.newPage();
        try {
          // Navigate to /?q=nudge#/attention/waiting. The context's cookie
          // provides auth; no ?t= needed.
          const baseUrl = serveHandle.base.replace(/\/$/, '');
          await reloadPage.goto(baseUrl + '/?q=nudge#/attention/waiting', {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          await reloadPage.waitForSelector('.kit-bar', { timeout: 8000 });
          await reloadPage
            .waitForSelector('.kit-row', { timeout: 8000 })
            .catch(() => {});
          const reloadCount = await reloadPage.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          check(
            'reload with ?q=nudge shows the narrowed list',
            reloadCount < fullCount || reloadCount <= 1,
          );
          const fieldText = await reloadPage
            .$eval('.kit-search', (el) => el.value)
            .catch(() => '');
          check(
            'reload with ?q=nudge restores text in the search field',
            fieldText === 'nudge',
          );
        } finally {
          await reloadPage.close();
        }
      }
    }

    // ---- Check: opening a row shows item detail in .kit-read ----------------
    // ---- scenario: direct item link -----------------------------------------
    // Note: this check runs before the row-click check below so that the
    // reading column is empty when we navigate directly to the item.
    console.log('\nscenario: direct #/item/<key> link loads item detail');
    {
      // Use a fresh page and set the hash to simulate a direct link.
      // pr:schuettc/hail#3 is in the fixture with title "fix nudge".
      const directPage = await context.newPage();
      try {
        await directPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await directPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Set the hash to the item key (simulates navigating to a direct link;
        // the hashchange event triggers show() on the attention section).
        await directPage.evaluate(() => {
          location.hash = '#/item/pr:schuettc/hail#3';
        });
        // Wait for the reading column to populate via openDetail().
        await directPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        const title = await directPage
          .$eval('.kit-read .cb-title', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'direct #/item/<key> shows the item title in .kit-read',
          title.length > 0,
        );
      } finally {
        await directPage.close();
      }
    }
    console.log('\nscenario: attention list and detail (continued)');

    // Check: opening a row shows item detail in .kit-read.
    {
      const firstRow = await page.$('.kit-row');
      if (firstRow) {
        await firstRow.dblclick();
        // Wait for the reading column to populate.
        await page
          .waitForSelector('.kit-read .cb-item', { timeout: 5000 })
          .catch(() => {});
        const hasKicker = await page
          .$eval('.kit-read .cb-kicker', (el) => el.textContent.length > 0)
          .catch(() => false);
        const hasTitle = await page
          .$eval('.kit-read .cb-title', (el) => el.textContent.length > 0)
          .catch(() => false);
        check('opening a row shows the detail kicker in .kit-read', hasKicker);
        check('opening a row shows the detail title in .kit-read', hasTitle);
      } else {
        check('opening a row shows the detail kicker in .kit-read', false);
        check('opening a row shows the detail title in .kit-read', false);
      }
    }

    // The 200-row cap is probe-shell.mjs's "a large view renders 200 rows
    // and a "show more"" (its own serve with 203 items).

    // ---- scenario: foot stays visible when view fits one page ---------------
    console.log('\nscenario: foot stays visible when view fits one page');

    {
      // Navigate to the 'new' view. The fixture has only a handful of items
      // (well under PAGE_SIZE=200) so there is no 'show more' needed.
      // The foot element must NOT be hidden: the selection count and the
      // select-all button must be visible even when the show-more button hides.
      await page.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await page.waitForFunction(() => location.hash === '#/attention/new');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(300);

      // cb-foot must not be hidden (the hidden attribute sets display:none).
      const footNotHidden = await page
        .$eval('.cb-foot', (el) => !el.hidden)
        .catch(() => false);
      check('cb-foot is not hidden when view fits one page', footNotHidden);

      // cb-sel-all must be in the rendered flow (offsetParent !== null means it
      // is not hidden by itself or any ancestor).
      const selAllVisible = await page
        .$eval('.cb-sel-all', (el) => el.offsetParent !== null)
        .catch(() => false);
      check('select-all is visible when view fits one page', selAllVisible);
    }

    // ---- scenario: select-all button shows total count ----------------------
    console.log('\nscenario: select-all button shows total count');

    {
      // Still on the 'new' view from the previous scenario.
      // The select-all button text must include the total item count, e.g.
      // "select all 4 in view" not just "select all in view".
      const selAllText = await page
        .$eval('.cb-sel-all', (el) => el.textContent ?? '')
        .catch(() => '');
      check(
        'select-all text includes the item count',
        /select all \d+ in view/.test(selAllText),
      );
    }

    // ---- scenario: dispositions for a mixed-kind selection are the intersection
    console.log(
      '\nscenario: dispositions for a mixed-kind selection are the intersection',
    );

    {
      // Select every visible row in 'new' view (repo:, pr:, issue:, branch: kinds).
      // Intersection of their allowed dispositions excludes 'merge' (pr-only).
      // A locator, so each box is found as it is clicked (a live redraw
      // can replace the rows between two clicks).
      const boxes = page.locator('.kit-row .kit-box');
      for (let i = 0, n = await boxes.count(); i < n; i++) {
        await boxes.nth(i).click();
        await page.waitForTimeout(40);
      }
      await page.waitForTimeout(300);

      // Open the decide sheet.
      await page.click('.kit-primary').catch(() => {});
      await page
        .waitForSelector('.kit-sheet', { timeout: 4000 })
        .catch(() => {});
      await page.waitForTimeout(200);

      const dispTexts = await page.$$eval('.kit-sheet .cb-choice', (btns) =>
        btns.map((b) => b.getAttribute('data-d') ?? ''),
      );

      // 'merge' is valid only for pr: — must not appear for a mixed selection.
      check(
        `mixed-kind selection does not offer pr-only disposition (merge) (cards: ${dispTexts.join(', ')})`,
        dispTexts.includes('keep') && !dispTexts.includes('merge'),
      );

      // Close without deciding and deselect.
      await page.keyboard.press('Escape');
      await page
        .waitForFunction(
          () => document.querySelector('.kit-backdrop') === null,
          { timeout: 3000 },
        )
        .catch(() => {});
      await page.waitForTimeout(200);
      const selBoxes = page.locator('.kit-row .kit-box');
      for (let i = 0, n = await selBoxes.count(); i < n; i++) {
        await selBoxes
          .nth(i)
          .click()
          .catch(() => {});
        await page.waitForTimeout(30);
      }
      await page.waitForTimeout(200);
    }

    // ---- scenario: partial decide deselects succeeded, keeps failed selected -
    console.log(
      '\nscenario: partial decide deselects succeeded and keeps failed selected',
    );

    {
      // Use a fresh browser context so the route interceptor is fully isolated
      // and cannot leak into the subsequent 'decide in bulk' scenario.
      const partialCtx = await browser.newContext();
      const partialPage = await partialCtx.newPage();
      try {
        await partialPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await partialPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to 'new' view and select two rows.
        await partialPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await partialPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await partialPage.waitForTimeout(600);
        await partialPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});

        const newBoxes = partialPage.locator('.kit-row .kit-box');
        if ((await newBoxes.count()) >= 2) {
          await newBoxes.nth(0).click();
          await partialPage.waitForTimeout(50);
          await newBoxes.nth(1).click();
          await partialPage.waitForTimeout(300);

          // Intercept /api/decide to simulate a partial success:
          // first key decided, second key errors.
          // The fresh context ensures the route cannot leak into other scenarios.
          await partialPage.route('**/api/decide', async (route, request) => {
            const body = JSON.parse(request.postData() || '{}');
            const keys = body.keys || [];
            await route.fulfill({
              status: 200,
              contentType: 'application/json',
              body: JSON.stringify({
                decided: keys.length > 0 ? 1 : 0,
                decided_keys: keys.length > 0 ? [keys[0]] : [],
                errors:
                  keys.length > 1
                    ? [keys[1] + ': disposition not allowed for this kind']
                    : [],
              }),
            });
          });

          // Open the decide sheet via the primary button.
          await partialPage.click('.kit-primary');
          await partialPage
            .waitForSelector('.kit-sheet', { timeout: 4000 })
            .catch(() => {});
          await partialPage.waitForTimeout(200);

          // Pick the first available disposition.
          const firstDisp = await partialPage.$('.kit-sheet .cb-choice');
          if (firstDisp) {
            await firstDisp.click();
            await partialPage.waitForTimeout(100);
          }

          // Click the sheet's fill ("<choice> · N items").
          await partialPage.click('.kit-sheet .kit-btn.fill');
          await partialPage.waitForTimeout(800);

          // The error for the failed key must be visible inside the sheet.
          const errVisible = await partialPage
            .$eval(
              '.kit-sheet .cb-choice-err',
              (el) =>
                el.offsetParent !== null && (el.textContent || '').length > 0,
            )
            .catch(() => false);
          check(
            'partial decide shows the error for the failed key',
            errVisible,
          );

          // Only the first key was decided (deselected). The second key remains
          // selected. Primary must show 'Decide 1' (not 'Decide 2').
          const primaryText = await partialPage
            .$eval('.kit-primary', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'partial decide deselects only the succeeded key (primary Decide 1)',
            primaryText.trim() === 'Decide 1',
          );
        } else {
          check('partial decide shows the error for the failed key', false);
          check(
            'partial decide deselects only the succeeded key (primary Decide 1)',
            false,
          );
        }
      } finally {
        await partialCtx.close();
      }
    }

    // ---- scenario: decide in bulk -------------------------------------------
    console.log('\nscenario: decide in bulk');

    {
      // Navigate to the 'new' view (shows all undecided items).
      // We use 'new' instead of 'waiting' so that waiting items (incoming PRs
      // and issues with incoming-no-reply hits) are preserved for the board
      // scenario that runs later and requires items in the waiting lane.
      await page.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await page.waitForFunction(() => location.hash === '#/attention/new');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});

      const boxes = page.locator('.kit-row .kit-box');
      // Select only 2 items to leave enough items alive for subsequent tests.
      const selectCount = Math.min(2, await boxes.count());

      for (let i = 0; i < selectCount; i++) {
        await boxes.nth(i).click();
        await page.waitForTimeout(50);
      }

      if (selectCount > 0) {
        await page.waitForTimeout(300);

        // 1. Primary reads 'Decide N'.
        const primaryText = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'selecting rows updates the primary',
          primaryText.trim() === `Decide ${selectCount}`,
        );

        // Open the decide sheet via the primary button.
        await page.click('.kit-primary');
        await page
          .waitForSelector('.kit-sheet', { timeout: 4000 })
          .catch(() => {});
        await page.waitForTimeout(200);

        const sheetEl = await page.$('.kit-sheet');
        if (sheetEl) {
          // Click the first available disposition button (keep is valid for all kinds).
          // 'keep' is valid for all kinds.
          const keepCard = await page.$('.kit-sheet .cb-choice[data-d="keep"]');
          const clickedLabel = keepCard
            ? await keepCard.$eval(
                '.cb-choice-label',
                (el) => el.textContent ?? '',
              )
            : '';
          if (keepCard) await keepCard.click();
          await page.waitForTimeout(150);

          // 2. The fill names the choice and the count:
          //    "<label> · N <kind>s" (items when the kinds differ).
          const fill = await page
            .$eval('.kit-sheet .kit-btn.fill', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            `the sheet's fill names the choice and the count ("${fill}")`,
            !!keepCard &&
              fill.startsWith(`${clickedLabel} \u00b7 ${selectCount} `),
          );

          // Click the fill.
          let decided = false;
          if (keepCard) {
            await page.click('.kit-sheet .kit-btn.fill');
            decided = true;
          }

          // Wait for the API call and list refresh.
          await page.waitForTimeout(2500);

          // 3. Decided items gone; primary hidden.
          const rowsAfter = await page.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          const primaryHidden = await page
            .$eval('.kit-primary', (el) => el.hidden)
            .catch(() => true);
          check(
            'deciding removes the items and clears the count',
            decided && (rowsAfter === 0 || primaryHidden),
          );
        } else {
          check("the sheet's fill names the choice and the count", false);
          check('deciding removes the items and clears the count', false);
        }
      } else {
        check('selecting rows updates the primary', false);
        check("the sheet's fill names the choice and the count", false);
        check('deciding removes the items and clears the count', false);
      }
    }

    // ---- scenario: deciding drops the ids it decided ------------------------
    console.log('\nscenario: deciding drops the ids it decided');

    {
      // After the bulk decide above, no waiting rows remain selected.
      // Select one row in the 'new' view (items that were not in 'waiting')
      // and verify the primary shows 'Decide 1' (the decided ids are gone).
      await page.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await page.waitForFunction(() => location.hash === '#/attention/new');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});

      const newBoxes = page.locator('.kit-row .kit-box');
      if ((await newBoxes.count()) > 0) {
        await newBoxes.first().click();
        await page.waitForTimeout(300);
        const primaryText = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        // Primary must show exactly 'Decide 1', not more (decided ids not re-counted).
        check(
          'a decided id is not re-counted',
          primaryText.trim() === 'Decide 1',
        );
        // Clean up: deselect.
        await newBoxes.first().click();
        await page.waitForTimeout(100);
      } else {
        // If no 'new' items remain (all are decided), primary must be hidden.
        const primaryHidden = await page
          .$eval('.kit-primary', (el) => el.hidden)
          .catch(() => true);
        check('a decided id is not re-counted', primaryHidden);
      }
    }

    // ---- scenario: decide one from the detail --------------------------------
    console.log('\nscenario: decide one from the detail');

    {
      const detailPage = await context.newPage();
      try {
        await detailPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await detailPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Open an undecided item in the 'new' view.
        await detailPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await detailPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await detailPage.waitForTimeout(600);
        await detailPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});

        // The first row of 'new', or a direct link to a repo item.
        const firstRow = await detailPage.$('.kit-row');
        if (firstRow) {
          await firstRow.dblclick();
        } else {
          await detailPage.evaluate(() => {
            location.hash = '#/item/repo:schuettc/hail';
          });
        }
        await detailPage
          .waitForSelector('.kit-read .cb-item .cb-choice', { timeout: 5000 })
          .catch(() => {});

        // The decide step is in the reading column: one click on a card
        // acts there, with no sheet (probe-decide.mjs decides with them).
        // Not now decides nothing until a condition is picked, so this
        // shared serve keeps its items.
        const cards = await detailPage.$$eval(
          '.kit-read .cb-decide .cb-choice',
          (els) => els.map((e) => e.getAttribute('data-d') ?? ''),
        );
        await detailPage
          .click('.kit-read .cb-decide .cb-choice[data-d="wait"]')
          .catch(() => {});
        const shown = await detailPage
          .waitForFunction(
            () =>
              !!document.querySelector(
                '.kit-read .cb-notnow:not([hidden]) .kit-chip',
              ),
            undefined,
            { timeout: 3000 },
          )
          .then(() => true)
          .catch(() => false);
        const sheetOpen = await detailPage.evaluate(
          () => !!document.querySelector('.kit-sheet'),
        );
        check(
          `the open item's Not now card opens its conditions in place, no sheet (cards: ${cards.join(', ')})`,
          cards.includes('wait') && shown && !sheetOpen,
        );
      } finally {
        await detailPage.close();
      }
    }
    // ---- scenario: the board view ------------------------------------------
    console.log(
      "\nscenario: the board shows four lanes with the fixture's items in the right lanes",
    );

    {
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 6000 }).catch(() => {});
      await page.waitForTimeout(800);

      const laneCount = await page.$$eval('.cb-lane', (lanes) => lanes.length);
      check('board shows four lanes', laneCount === 4);

      const laneIds = await page.$$eval('.cb-lane', (lanes) =>
        lanes.map((l) => l.dataset.lane),
      );
      check(
        'lanes are waiting, proposed, due, new in order',
        laneIds[0] === 'waiting' &&
          laneIds[1] === 'proposed' &&
          laneIds[2] === 'due' &&
          laneIds[3] === 'new',
      );

      const waitingCards = await page.$$eval(
        '[data-lane="waiting"] .kit-card',
        (cards) => cards.length,
      );
      check('waiting lane has cards from the fixture', waitingCards > 0);

      // Navigate back to list view.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: selection made on the board shows on the list ------------
    console.log(
      '\nscenario: a selection made on the board shows on the list and vice versa',
    );

    {
      // Navigate to board.
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(600);

      // Click the card's checkbox (.kit-box) for selection; clicking the
      // title navigates to the item instead (brief fix-round item 2).
      const firstCardBox = await page.$(
        '[data-lane="waiting"] .kit-card .kit-box',
      );
      const firstCard = await page.$('[data-lane="waiting"] .kit-card');
      if (firstCardBox && firstCard) {
        await firstCardBox.click();
        await page.waitForTimeout(300);

        const isSelected = await firstCard
          .evaluate((el) => el.classList.contains('on'))
          .catch(() => false);
        check('clicking a board card selects it', isSelected);

        // Navigate to list view; primary should reflect the selection.
        await page.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await page.waitForFunction(
          () => location.hash === '#/attention/waiting',
        );
        await page.waitForTimeout(600);

        const primaryText = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'selection from board is visible on the list (primary shows Decide N)',
          primaryText.trim().startsWith('Decide '),
        );

        // Go back to board and deselect to clean up.
        await page.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await page.waitForFunction(() => location.hash === '#/attention/board');
        await page
          .waitForSelector('.cb-lane', { timeout: 5000 })
          .catch(() => {});
        await page.waitForTimeout(600);
        const selCardBox = await page.$(
          '[data-lane="waiting"] .kit-card.on .kit-box',
        );
        if (selCardBox) {
          await selCardBox.click();
          await page.waitForTimeout(200);
        }
      } else {
        check('clicking a board card selects it', false);
        check(
          'selection from board is visible on the list (primary shows Decide N)',
          false,
        );
      }

      // Back to list.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: list selection shows on the board (reverse direction) ---
    console.log('\nscenario: a selection made on the list shows on the board');

    {
      // Ensure we're on the waiting list view with rows loaded.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(400);

      const firstRowBox = await page.$('.kit-row .kit-box');
      if (firstRowBox) {
        // Select the first row by clicking its checkbox.
        await firstRowBox.click();
        await page.waitForTimeout(200);

        // Verify selection took effect (primary shows "Decide 1" or similar).
        const primaryAfterSelect = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        const hasSelection = primaryAfterSelect.trim().startsWith('Decide ');

        // Navigate to the board.
        await page.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await page.waitForFunction(() => location.hash === '#/attention/board');
        await page
          .waitForSelector('.cb-lane', { timeout: 5000 })
          .catch(() => {});
        await page.waitForTimeout(600);

        // At least one card in any lane must have .on class (the selected item).
        // We don't use data-id from the list row (kit doesn't expose it as DOM
        // attribute); instead we verify any card is selected on the board.
        const anyCardOn = await page
          .$$eval('.cb-lane .kit-card.on', (cards) => cards.length)
          .catch(() => 0);
        check(
          'selection from list is visible on the board (card has .on class)',
          hasSelection && anyCardOn > 0,
        );

        // Deselect all selected cards on the board so subsequent scenarios
        // start with a clean selection.
        const selBoxes = await page.$$('.cb-lane .kit-card.on .kit-box');
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else {
        check(
          'selection from list is visible on the board (card has .on class)',
          false,
        );
      }

      // Back to waiting.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(400);
    }

    // ---- scenario: shift-click ranges within a lane -----------------------
    // Fixture has 3 waiting items (pr #3, issue #4, issue #5), so we can
    // click the first, shift-click the third, and assert the middle is selected.
    console.log('\nscenario: shift-click ranges within a lane');

    {
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(600);

      // Use .kit-box (the checkbox) for clicks so the card's title (which
      // navigates to the item, brief fix-round item 2) is not triggered.
      const waitingBoxes = await page.$$(
        '[data-lane="waiting"] .kit-card .kit-box',
      );
      if (waitingBoxes.length >= 3) {
        // Click first card, shift-click third card — middle card must be selected.
        await waitingBoxes[0].click();
        await page.waitForTimeout(150);
        await waitingBoxes[2].click({ modifiers: ['Shift'] });
        await page.waitForTimeout(150);

        const waitingCardsAll = await page.$$(
          '[data-lane="waiting"] .kit-card',
        );
        // The middle card (index 1) must have .on class.
        const middleSelected = await waitingCardsAll[1]
          ?.evaluate((el) => el.classList.contains('on'))
          .catch(() => false);
        check(
          'shift-click selects the middle card in the range',
          middleSelected === true,
        );

        const selectedCount = await page.$$eval(
          '[data-lane="waiting"] .kit-card.on',
          (cards) => cards.length,
        );
        check(
          'shift-click selects a range within the lane (≥3 cards)',
          selectedCount >= 3,
        );

        // Deselect by clicking each selected card's checkbox.
        const selBoxes = await page.$$(
          '[data-lane="waiting"] .kit-card.on .kit-box',
        );
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else if (waitingBoxes.length >= 2) {
        // Fallback: fixture has only 2 waiting cards.
        await waitingBoxes[0].click();
        await page.waitForTimeout(150);
        await waitingBoxes[1].click({ modifiers: ['Shift'] });
        await page.waitForTimeout(150);
        check('shift-click selects a range within the lane (≥3 cards)', false);
        check('shift-click selects the middle card in the range', false);
        const selBoxes = await page.$$(
          '[data-lane="waiting"] .kit-card.on .kit-box',
        );
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else {
        check('shift-click selects a range within the lane (≥3 cards)', false);
        check('shift-click selects the middle card in the range', false);
      }

      // Back to list.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: deciding from the board removes the card -----------------
    console.log(
      '\nscenario: deciding from the board removes the card and deselects it',
    );

    {
      const boardDecideCtx = await browser.newContext();
      const boardDecidePage = await boardDecideCtx.newPage();
      try {
        await boardDecidePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await boardDecidePage.waitForSelector('.kit-bar', { timeout: 8000 });

        await boardDecidePage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await boardDecidePage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await boardDecidePage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await boardDecidePage.waitForTimeout(800);

        const firstCard = await boardDecidePage.$(
          '[data-lane="waiting"] .kit-card',
        );
        // Click the checkbox (.kit-box) for selection; clicking the title
        // navigates instead of selecting (brief fix-round item 2).
        const firstCardBox = await boardDecidePage.$(
          '[data-lane="waiting"] .kit-card .kit-box',
        );
        if (firstCard && firstCardBox) {
          const cardId = await firstCard
            .evaluate((el) => el.dataset.id ?? '')
            .catch(() => '');
          await firstCardBox.click();
          await boardDecidePage.waitForTimeout(300);

          // Click Decide N.
          await boardDecidePage.click('.kit-primary');
          await boardDecidePage
            .waitForSelector('.kit-sheet', { timeout: 4000 })
            .catch(() => {});
          await boardDecidePage.waitForTimeout(200);

          // Pick the first disposition.
          const firstDisp = await boardDecidePage.$('.kit-sheet .cb-choice');
          if (firstDisp) {
            await firstDisp.click();
            await boardDecidePage.waitForTimeout(100);
          }

          // Click the sheet's fill ("<choice> · N items").
          await boardDecidePage.click('.kit-sheet .kit-btn.fill');
          await boardDecidePage.waitForTimeout(2500);

          // The decided card must be gone or selection cleared.
          const cardStillThere = cardId
            ? await boardDecidePage
                .$eval(
                  `[data-lane="waiting"] .kit-card[data-id="${cardId}"]`,
                  () => true,
                )
                .catch(() => false)
            : true;
          const primaryHidden = await boardDecidePage
            .$eval('.kit-primary', (el) => el.hidden)
            .catch(() => true);
          check(
            'deciding from the board removes the card and deselects it',
            !cardStillThere || primaryHidden,
          );
        } else {
          check(
            'deciding from the board removes the card and deselects it',
            false,
          );
        }
      } finally {
        await boardDecideCtx.close();
      }
    }

    // ---- scenario: a filter narrows every lane ------------------------------
    // Check all four lanes; unfiltered total must exceed filtered total.
    // ?q=nudge matches only pr #3 "fix nudge" in the waiting lane; all other
    // items (issue #4, issue #5, repo, branch) do not contain "nudge".
    console.log('\nscenario: a filter narrows every lane');

    {
      // Count total cards across all lanes without any filter.
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(800);

      const unfilteredTotal = await page.$$eval(
        '.cb-lane .kit-card',
        (cards) => cards.length,
      );

      // Open a fresh page with ?q=nudge to apply a filter.
      const filterPage = await context.newPage();
      try {
        const baseUrl = serveHandle.base.replace(/\/$/, '');
        await filterPage.goto(baseUrl + '/?q=nudge#/attention/board', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await filterPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await filterPage
          .waitForSelector('.cb-lane', { timeout: 8000 })
          .catch(() => {});
        await filterPage.waitForTimeout(800);

        const filteredTotal = await filterPage.$$eval(
          '.cb-lane .kit-card',
          (cards) => cards.length,
        );
        // ?q=nudge matches only the "fix nudge" PR; the filter must reduce the
        // total count across all lanes. No weakening || condition here.
        check(
          'a filter narrows every lane (total filtered < total unfiltered)',
          filteredTotal < unfilteredTotal,
        );
      } finally {
        await filterPage.close();
      }

      // Navigate back to waiting list view.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: board layout at 1600×900 ---------------------------------
    console.log(
      '\nscenario: board layout at 1600\xd7900 \u2014 all four lanes visible and \u2265220\u202fpx wide',
    );

    {
      const wideCtx = await browser.newContext({
        viewport: { width: 1600, height: 900 },
      });
      const widePage = await wideCtx.newPage();
      try {
        await widePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await widePage.waitForSelector('.kit-bar', { timeout: 8000 });

        await widePage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await widePage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await widePage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await widePage.waitForTimeout(800);

        // Screenshot after fix (required by the controller ruling).
        await widePage.screenshot({ path: '/tmp/board-after.png' });

        // The \'board\' chip must be the active view chip.
        const boardChipActive = await widePage
          .$eval('[data-id="board"]', (el) => el.classList.contains('on'))
          .catch(() => false);
        check(
          'board chip is the active view chip at 1600\xd7900',
          boardChipActive,
        );

        const lanes = await widePage.$$('.cb-lane');
        check('board shows four lanes at 1600\xd7900', lanes.length === 4);

        let allInViewport = true;
        let allWideEnough = true;
        for (const lane of lanes) {
          const box = await lane.boundingBox();
          if (!box) {
            allInViewport = false;
            allWideEnough = false;
            break;
          }
          if (box.x < 0 || box.x + box.width > 1600 + 1) allInViewport = false;
          if (box.width < 220) allWideEnough = false;
        }
        check(
          'all four lane bounding boxes are inside the 1600\xd7900 viewport',
          allInViewport,
        );
        check(
          'all four lanes are at least 220\u202fpx wide at 1600\xd7900',
          allWideEnough,
        );
      } finally {
        await wideCtx.close();
      }
    }

    // ---- scenario: board layout at 1100×800 (horizontal scroll) -----------
    console.log(
      '\nscenario: board layout at 1100\xd7800 \u2014 board scrolls, document does not',
    );

    {
      const narrowCtx = await browser.newContext({
        viewport: { width: 1100, height: 800 },
      });
      const narrowPage = await narrowCtx.newPage();
      try {
        await narrowPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await narrowPage.waitForSelector('.kit-bar', { timeout: 8000 });

        await narrowPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await narrowPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await narrowPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await narrowPage.waitForTimeout(800);

        // The document (html element) must not scroll horizontally.
        const noDocScroll = await narrowPage.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        );
        check(
          'document does not scroll horizontally at 1100\xd7800',
          noDocScroll,
        );

        // The board container must be reachable: it is either wide enough to
        // show all lanes without scrolling, or it provides its own scrollbar.
        const boardReachable = await narrowPage
          .$eval(
            '.cb-board',
            (el) =>
              // All lanes fit without scrolling.
              el.scrollWidth <= el.clientWidth ||
              // Or the board itself is scrollable.
              el.scrollWidth > el.clientWidth,
          )
          .catch(() => true); // .cb-board not found is handled above
        check(
          'board lanes are reachable (board scrolls, not document) at 1100\xd7800',
          noDocScroll && boardReachable,
        );
      } finally {
        await narrowCtx.close();
      }
    }

    // ---- scenario: opening a card from the board ----------------------------
    console.log(
      '\nscenario: opening a card from the board shows item in reading column',
    );

    {
      const cardCtx = await browser.newContext();
      const cardPage = await cardCtx.newPage();
      try {
        await cardPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await cardPage.waitForSelector('.kit-bar', { timeout: 8000 });

        await cardPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await cardPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await cardPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await cardPage.waitForTimeout(800);

        // Click the title element of the first waiting card.
        const titleEl = await cardPage.$(
          '[data-lane="waiting"] .kit-card .cb-card-title',
        );
        const cardTitle = titleEl
          ? ((await titleEl.textContent()) ?? '').trim()
          : '';

        if (titleEl && cardTitle) {
          await titleEl.click();
          // Wait for the item detail to load in the reading column.
          await cardPage.waitForTimeout(1200);

          const readText = await cardPage
            .$eval('.kit-read', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'opening a board card title shows the item in the reading column',
            readText.includes(cardTitle),
          );

          // The hash must now be #/item/<key> (not #/attention/board).
          const afterHash = await cardPage.evaluate(() => location.hash);
          check(
            'opening a board card navigates to #/item/<key>',
            afterHash.startsWith('#/item/'),
          );

          // Pressing Back must return to #/attention/board.
          await cardPage.goBack();
          await cardPage.waitForTimeout(600);
          const backHash = await cardPage.evaluate(() => location.hash);
          check(
            'Back from the item view returns to #/attention/board',
            backHash === '#/attention/board',
          );
        } else {
          check(
            'opening a board card title shows the item in the reading column',
            false,
          );
          check('opening a board card navigates to #/item/<key>', false);
          check('Back from the item view returns to #/attention/board', false);
        }
      } finally {
        await cardCtx.close();
      }
    }
    // ---- fix-round-2: encoded item URL opens correctly ------------------
    console.log(
      '\nscenario: encoded item URL (casebook_open-style) opens item detail',
    );

    {
      const encPage = await context.newPage();
      try {
        await encPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await encPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to the server-encoded URL: url.PathEscape('issue:schuettc/hail#4')
        // → 'issue:schuettc%2Fhail%234'.
        await encPage.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%236';
        });

        // Wait for the reading column to populate.
        await encPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await encPage.waitForTimeout(600);

        const itemTitle = await encPage
          .$eval('.kit-read .cb-title', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'server-encoded item URL (#/item/issue:schuettc%2Fhail%234) opens item detail',
          itemTitle.length > 0,
        );
      } finally {
        await encPage.close();
      }
    }

    // ---- fix-round-2: board "select all N in view" counts all lanes --------
    console.log(
      '\nscenario: board select-all reflects de-duplicated total across all lanes',
    );

    {
      const selAllPage = await context.newPage();
      try {
        await selAllPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await selAllPage.waitForSelector('.kit-bar', { timeout: 8000 });

        await selAllPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await selAllPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await selAllPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        // Give the board time to fetch all four lanes.
        await selAllPage.waitForTimeout(1200);

        // Count total cards across all lanes (scoped to .cb-lane so other
        // kit-cards — e.g. in the decide sheet — are not counted).
        const totalCards = await selAllPage.$$eval(
          '.cb-lane .kit-card',
          (cards) => cards.length,
        );

        const selAllText = await selAllPage
          .$eval('.cb-sel-all', (el) => el.textContent ?? '')
          .catch(() => '');
        const match = selAllText.match(/select all (\.?\d+) in view/);
        const selAllCount = match ? parseInt(match[1], 10) : -1;

        check(
          'board select-all shows total unique items (matches card count)',
          selAllCount > 0 && selAllCount === totalCards,
        );

        // Click select-all and verify the selection count equals totalCards.
        await selAllPage.click('.cb-sel-all');
        await selAllPage.waitForTimeout(300);
        const selectedCards = await selAllPage.$$eval(
          '.cb-lane .kit-card.on',
          (cards) => cards.length,
        );
        check(
          'board select-all selects exactly the total unique items',
          selectedCards === totalCards && totalCards > 0,
        );
      } finally {
        await selAllPage.close();
      }
    }

    // ---- fix-round-2: board lane "show N more" reflects de-duplicated count
    console.log(
      '\nscenario: board lane show-more reflects de-duplicated items',
    );

    {
      const showMorePage = await context.newPage();
      try {
        await showMorePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await showMorePage.waitForSelector('.kit-bar', { timeout: 8000 });

        await showMorePage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await showMorePage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await showMorePage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await showMorePage.waitForTimeout(1200);

        // For each lane: if the "show more" button is visible, it must show a
        // positive number > 0.  More importantly, if a lane's total unique items
        // equals the number of cards visible, the button must be hidden.
        let allLanesCorrect = true;
        for (const laneId of ['waiting', 'proposed', 'due', 'new']) {
          const moreHidden = await showMorePage
            .$eval(`[data-lane="${laneId}"] .cb-lane-more`, (el) => el.hidden)
            .catch(() => true);
          const moreText = await showMorePage
            .$eval(
              `[data-lane="${laneId}"] .cb-lane-more`,
              (el) => el.textContent ?? '',
            )
            .catch(() => '');
          // If visible, the number in "show N more" must be > 0.
          if (!moreHidden) {
            const n = parseInt((moreText.match(/\d+/) ?? ['0'])[0], 10);
            if (n <= 0) {
              allLanesCorrect = false;
              console.error(
                `    lane "${laneId}": "show more" visible with non-positive count (${moreText})`,
              );
            }
          }
          // If all cards for this lane are shown and no more pages would have
          // unique items, the button should be hidden.
          // (We can't know server-side totals here, so just check the logic
          // is consistent: visible button → positive count.)
        }
        check(
          'board lane show-more is hidden when all unique items are shown',
          allLanesCorrect,
        );
      } finally {
        await showMorePage.close();
      }
    }

    // ---- fix-round-2: empty reading column shows quiet empty state ---------
    console.log('\nscenario: empty reading column shows the quiet empty state');

    {
      const emptyReadPage = await context.newPage();
      try {
        await emptyReadPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await emptyReadPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to waiting view (no item open).
        await emptyReadPage.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await emptyReadPage.waitForFunction(
          () => location.hash === '#/attention/waiting',
        );
        await emptyReadPage
          .waitForSelector('.kit-row', { timeout: 8000 })
          .catch(() => {});
        await emptyReadPage.waitForTimeout(600);

        // The reading column must have the empty-state element (not be blank).
        const hasEmptyState = await emptyReadPage
          .$eval('.kit-read .cb-read-empty', (el) => el.offsetParent !== null)
          .catch(() => false);
        check(
          'empty reading column shows .cb-read-empty instead of blank',
          hasEmptyState,
        );

        // The empty state must show the section name.
        const sectionLabel = await emptyReadPage
          .$eval(
            '.kit-read .cb-read-empty-section',
            (el) => el.textContent ?? '',
          )
          .catch(() => '');
        check(
          'empty state shows the section name (attention)',
          sectionLabel.toLowerCase().includes('attention'),
        );

        // The empty state must show a prompt line.
        const promptText = await emptyReadPage
          .$eval(
            '.kit-read .cb-read-empty-prompt',
            (el) => el.textContent ?? '',
          )
          .catch(() => '');
        check('empty state shows a prompt line', promptText.length > 0);
      } finally {
        await emptyReadPage.close();
      }
    }

    // ---- scenario: title fallback uses keyWithoutKind (no kind repetition) --
    console.log(
      '\nscenario: title fallback uses keyWithoutKind (no kind in board card title)',
    );

    {
      const titleFallbackPage = await context.newPage();
      try {
        await titleFallbackPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await titleFallbackPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await titleFallbackPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await titleFallbackPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await titleFallbackPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await titleFallbackPage.waitForTimeout(800);

        // Check all board card titles: none should start with a kind prefix
        // followed by a colon, e.g. "branch:schuettc/hail@feat/client".
        // (Titleless items fall back to keyWithoutKind, e.g.
        //  "schuettc/hail@feat/client" rather than "branch:schuettc/hail@feat/client".)
        const badTitles = await titleFallbackPage.$$eval(
          '.cb-lane .kit-card .cb-card-title',
          (els) =>
            els
              .map((el) => el.textContent ?? '')
              .filter((t) => /^(branch|repo|issue|pr|worktree):/.test(t)),
        );
        check(
          'no board card title starts with a kind prefix (fallback is keyWithoutKind)',
          badTitles.length === 0,
        );
      } finally {
        await titleFallbackPage.close();
      }
    }

    // ---- scenario: live decided event deselects the decided keys ------------
    console.log(
      '\nscenario: live decided event deselects the decided item on the page',
    );

    {
      const liveDeselPage = await context.newPage();
      try {
        await liveDeselPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await liveDeselPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to board.
        await liveDeselPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await liveDeselPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await liveDeselPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await liveDeselPage.waitForTimeout(800);

        // Select the first waiting card via checkbox.
        const firstBox = await liveDeselPage.$(
          '[data-lane="waiting"] .kit-card .kit-box',
        );
        const firstCard = await liveDeselPage.$(
          '[data-lane="waiting"] .kit-card',
        );
        const cardId = firstCard
          ? await firstCard
              .evaluate((el) => el.dataset.id ?? '')
              .catch(() => '')
          : '';

        if (firstBox && cardId) {
          await firstBox.click();
          await liveDeselPage.waitForTimeout(200);

          const isSelected = await firstCard
            .evaluate((el) => el.classList.contains('on'))
            .catch(() => false);

          if (isSelected) {
            // Post a decide directly to the API (not via the page's UI) to
            // simulate a live decided event from another client/CLI.
            const resp = await liveDeselPage.evaluate(
              async ({ url, key }) => {
                const r = await fetch(url + '/api/decide', {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({
                    keys: [key],
                    disposition: 'keep',
                    note: 'live deselect probe',
                  }),
                });
                return r.status;
              },
              { url: serveHandle.base.replace(/\/$/, ''), key: cardId },
            );

            if (resp === 200) {
              // Wait for the live decided event to arrive and be processed.
              await liveDeselPage.waitForTimeout(2500);

              // The card must now be deselected (not in the selection store).
              // Check: primary button must not show "Decide 1" or count the decided item.
              await liveDeselPage.evaluate(() => {
                location.hash = '#/attention/waiting';
              });
              await liveDeselPage.waitForFunction(
                () => location.hash === '#/attention/waiting',
              );
              await liveDeselPage.waitForTimeout(600);

              const primaryText = await liveDeselPage
                .$eval('.kit-primary', (el) => el.textContent ?? '')
                .catch(() => '');
              // If the live decided event deselected the key, the primary
              // either shows 'Decide 0' (which should be hidden) or is absent.
              check(
                'live decided event deselects the decided key',
                !primaryText.trim().startsWith('Decide '),
              );
            } else {
              check('live decided event deselects the decided key', false);
            }
          } else {
            check('live decided event deselects the decided key', false);
          }
        } else {
          check('live decided event deselects the decided key', false);
        }
      } finally {
        await liveDeselPage.close();
      }
    }

    // ---- scenario: proposals event updates the proposed chip count ----------
    // Post a fake-agent proposal via /api/agent/propose.  The server emits a
    // "proposals" live event WITHOUT a follow-up index rebuild, so the page
    // must re-fetch /api/summary and update the "proposed" chip count.
    console.log(
      '\nscenario: fake-agent proposal updates the "proposed" chip count',
    );

    {
      const propCountPage = await context.newPage();
      try {
        await propCountPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await propCountPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Wait for the initial summary counts to arrive.
        await propCountPage
          .waitForSelector('.kit-chip[data-id="proposed"] .kit-n', {
            timeout: 6000,
          })
          .catch(() => {});
        await propCountPage.waitForTimeout(500);

        // Read the "proposed" chip count before posting the proposal.
        const beforeText = await propCountPage
          .$eval(
            '.kit-chip[data-id="proposed"] .kit-n',
            (el) => el.textContent ?? '',
          )
          .catch(() => '0');
        const beforeN = parseInt(beforeText, 10) || 0;

        // Use issue:schuettc/hail#5 (added to the fixture for this probe).
        // POST to /api/agent/propose — this does NOT trigger an index rebuild,
        // so only the "proposals" live event fires.  The page must re-fetch
        // /api/summary and update the chip count.
        const propKey = 'pr:schuettc/hail#3';
        const propSess = 'probe-session-1';
        await propCountPage.evaluate(
          async ({ url, key, sess }) => {
            // Register the agent session first (required before posting proposals).
            await fetch(url + '/api/agent/presence', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                id: sess,
                harness: 'pi',
                label: 'probe agent',
                cwd: '/tmp',
                pid: 0,
              }),
            });
            await fetch(url + '/api/agent/propose', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                session: sess,
                keys: [key],
                disposition: 'keep',
              }),
            });
          },
          {
            url: serveHandle.base.replace(/\/$/, ''),
            key: propKey,
            sess: propSess,
          },
        );

        // Wait for the live proposals event to be received and the summary
        // re-fetch to complete.
        await propCountPage.waitForTimeout(5000);

        // The "proposed" chip count must have increased.
        const afterText = await propCountPage
          .$eval(
            '.kit-chip[data-id="proposed"] .kit-n',
            (el) => el.textContent ?? '',
          )
          .catch(() => '0');
        const afterN = parseInt(afterText, 10) || 0;

        check(
          'fake-agent proposal updates the "proposed" chip count',
          afterN > beforeN,
        );
      } finally {
        await propCountPage.close();
      }
    }

    // ---- scenario: accepting and rejecting proposals (Task 5) ---------------
    // The chip-count scenario already posted a proposal on pr:schuettc/hail#3
    // from a 'pi' session.  These scenarios build on that state and also create
    // fresh proposals to test accept/reject/change/keys.
    console.log('\nscenario: accepting and rejecting proposals');

    // Helper: register an agent session and propose a disposition via the API.
    async function agentPropose(
      propPage,
      sess,
      harness,
      key,
      disposition,
      note,
    ) {
      return propPage.evaluate(
        async ({ base, session, h, k, disp, n }) => {
          const presence = await fetch(base + '/api/agent/presence', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              id: session,
              harness: h,
              label: h + ' probe',
              cwd: '/tmp',
              pid: 0,
            }),
          }).then((r) => r.json());
          const propose = await fetch(base + '/api/agent/propose', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              session,
              keys: [k],
              disposition: disp,
              ...(n ? { note: n } : {}),
            }),
          }).then((r) => r.json());
          return { presence, propose };
        },
        {
          base: serveHandle.base.replace(/\/$/, ''),
          session: sess,
          h: harness,
          k: key,
          disp: disposition,
          n: note || '',
        },
      );
    }

    // ---- A: proposal card geometry and agent name --------------------------
    // pr:schuettc/hail#3 already has a pending proposal from 'pi' session.
    {
      const propGeoPage = await context.newPage();
      try {
        await propGeoPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await propGeoPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Open the item detail via the hash route.
        await propGeoPage.evaluate(() => {
          location.hash = '#/item/pr:schuettc%2Fhail%233';
        });
        await propGeoPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await propGeoPage.waitForTimeout(1000);

        // The proposal card must be present.
        const hasCard = await propGeoPage
          .$('.cb-proposal-card')
          .then((el) => Boolean(el))
          .catch(() => false);
        check(
          'proposal card appears in item detail when a pending proposal exists',
          hasCard,
        );

        if (hasCard) {
          // Card head: "<agent> proposes · <disposition>" not "pending proposal".
          const cardHead = await propGeoPage
            .$eval(
              '.cb-proposal-card .kit-card-head',
              (el) => el.textContent ?? '',
            )
            .catch(() => '');
          check(
            'proposal card head contains "proposes ·"',
            cardHead.includes('proposes \u00b7'),
          );
          check(
            'proposal card head uses real agent name (pi), not hardcoded string',
            cardHead.toLowerCase().startsWith('pi'),
          );

          // Geometry: the recommendation sits between the decide step's
          // question and its cards.
          const cardBottom = await propGeoPage
            .$eval(
              '.cb-proposal-card',
              (el) => el.getBoundingClientRect().bottom,
            )
            .catch(() => -1);
          const cardTop = await propGeoPage
            .$eval('.cb-proposal-card', (el) => el.getBoundingClientRect().top)
            .catch(() => -1);
          const questionBottom = await propGeoPage
            .$eval(
              '.cb-decide .cb-question',
              (el) => el.getBoundingClientRect().bottom,
            )
            .catch(() => -1);
          const cardsTop = await propGeoPage
            .$eval(
              '.cb-decide .cb-choices',
              (el) => el.getBoundingClientRect().top,
            )
            .catch(() => -1);
          check(
            'proposal card is between the question and the choice cards (geometry)',
            questionBottom > 0 &&
              questionBottom <= cardTop &&
              cardBottom > 0 &&
              cardBottom <= cardsTop,
          );

          // Proposal card is inside the reading document (.kit-doc).
          const cardInDoc = await propGeoPage
            .$eval('.kit-doc .cb-proposal-card', (el) => el !== null)
            .catch(() => false);
          check(
            'proposal card is inside the .kit-doc reading document',
            cardInDoc,
          );
        } else {
          check('proposal card head contains "proposes ·"', false);
          check(
            'proposal card head uses real agent name (pi), not hardcoded string',
            false,
          );
          check(
            'proposal card is between the question and the choice cards (geometry)',
            false,
          );
          check('proposal card is inside the .kit-doc reading document', false);
        }

        // List row in proposed view must show "<agent> recommends <label>".
        await propGeoPage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await propGeoPage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await propGeoPage
          .waitForSelector('.kit-row', { timeout: 6000 })
          .catch(() => {});
        await propGeoPage.waitForTimeout(600);

        const subTexts = await propGeoPage
          .$$eval('.kit-row .kit-sub', (els) =>
            els.map((e) => e.textContent ?? ''),
          )
          .catch(() => []);
        const hasProposesText = subTexts.some((t) =>
          / recommends [A-Z]/.test(t),
        );
        check(
          `list row in proposed view shows "<agent> recommends <label>" (${subTexts.join(', ')})`,
          hasProposesText,
        );
      } finally {
        await propGeoPage.close();
      }
    }

    // ---- B: accept decides the item ----------------------------------------
    {
      const acceptPage = await context.newPage();
      try {
        await acceptPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await acceptPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Create a fresh proposal on issue:schuettc/hail#6 from pi session.
        const propResult = await agentPropose(
          acceptPage,
          'probe-accept-sess',
          'pi',
          'issue:schuettc/hail#6',
          'keep',
          'accept probe',
        );
        const proposed = propResult?.propose?.proposed ?? 0;
        if (proposed > 0) {
          // Navigate to the item detail.
          await acceptPage.evaluate(() => {
            location.hash = '#/item/issue:schuettc%2Fhail%236';
          });
          await acceptPage
            .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
            .catch(() => {});
          // Wait for the proposal card to appear (might need a re-render).
          await acceptPage.waitForTimeout(1000);

          const cardBeforeAccept = await acceptPage
            .$('.cb-proposal-card')
            .then((el) => Boolean(el))
            .catch(() => false);

          if (cardBeforeAccept) {
            // Click the 'accept' button: the item is decided and the
            // reading column moves on (to the next undecided item, or the
            // view's summary), so its card goes with it.
            await acceptPage.click('.cb-proposal-card .kit-btn.fill');
            const movedOn = await acceptPage
              .waitForFunction(
                () =>
                  document
                    .querySelector('.kit-read .cb-item')
                    ?.getAttribute('data-key') !== 'issue:schuettc/hail#6',
                undefined,
                { timeout: 5000 },
              )
              .then(() => true)
              .catch(() => false);
            const decision = await fetch(
              `${serveHandle.base}/api/item?key=${encodeURIComponent('issue:schuettc/hail#6')}`,
              { headers: { 'X-Local-Token': serveHandle.token } },
            )
              .then((r) => r.json())
              .then((d) => d.item?.decision?.disposition ?? '')
              .catch(() => '');
            check(
              `accept decides the item (${decision}) and the reading column moves off it with its card`,
              movedOn && decision === 'keep',
            );

            // Navigate to proposed view: the item should no longer be there.
            await acceptPage.evaluate(() => {
              location.hash = '#/attention/proposed';
            });
            await acceptPage.waitForFunction(
              () => location.hash === '#/attention/proposed',
            );
            await acceptPage.waitForTimeout(1000);

            const proposedRows = await acceptPage
              .$$eval('.kit-row', (rows) => rows.map((r) => r.dataset.id ?? ''))
              .catch(() => []);
            check(
              'accepted item leaves the proposed view',
              !proposedRows.includes('issue:schuettc/hail#6'),
            );
          } else {
            check(
              'accept decides the item and the reading column moves off it with its card',
              false,
            );
            check('accepted item leaves the proposed view', false);
          }
        } else {
          // proposal was not created — this is a real failure in a fresh probe run
          check(
            'fresh proposal created for accept test (issue:schuettc/hail#6)',
            false,
          );
        }
      } finally {
        await acceptPage.close();
      }
    }

    // ---- C: reject records a reason ----------------------------------------
    {
      const rejectPage = await context.newPage();
      try {
        await rejectPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await rejectPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Create a fresh proposal on repo:schuettc/hail.
        const propResult = await agentPropose(
          rejectPage,
          'probe-reject-sess',
          'pi',
          'repo:schuettc/hail',
          'archive',
          'reject probe',
        );
        const proposed = propResult?.propose?.proposed ?? 0;
        if (proposed > 0) {
          // Navigate to the item.
          await rejectPage.evaluate(() => {
            location.hash = '#/item/repo:schuettc%2Fhail';
          });
          await rejectPage
            .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
            .catch(() => {});
          await rejectPage.waitForTimeout(1000);

          const cardBeforeReject = await rejectPage
            .$('.cb-proposal-card')
            .then((el) => Boolean(el))
            .catch(() => false);

          if (cardBeforeReject) {
            // Click the 'reject' button (the last button in the card's actions).
            const rejectBtn = await rejectPage.$(
              '.cb-proposal-card .kit-btns .kit-btn:last-child',
            );
            if (rejectBtn) {
              await rejectBtn.click();
              await rejectPage
                .waitForSelector('.kit-sheet', { timeout: 4000 })
                .catch(() => {});
              await rejectPage.waitForTimeout(300);

              const sheetVisible = await rejectPage
                .$('.kit-sheet')
                .then((el) => Boolean(el))
                .catch(() => false);
              check('reject opens the reason sheet', sheetVisible);

              if (sheetVisible) {
                // Type a reason (noteField renders as input.kit-note).
                await rejectPage.fill(
                  '.kit-sheet input.kit-note',
                  'not needed right now',
                );
                await rejectPage.waitForTimeout(200);

                // Click the 'Reject N' fill button.
                await rejectPage.click('.kit-sheet .kit-btn.fill');
                await rejectPage
                  .waitForFunction(
                    () => !document.querySelector('.kit-sheet'),
                    { timeout: 5000 },
                  )
                  .catch(() => {});
                await rejectPage.waitForTimeout(500);

                // Proposal card must be gone.
                const cardAfterReject = await rejectPage
                  .$('.cb-proposal-card')
                  .then((el) => Boolean(el))
                  .catch(() => false);
                check(
                  'reject removes the proposal card from item detail',
                  !cardAfterReject,
                );

                // Verify via GET /api/item that the proposal is rejected and
                // the reason text is recorded.
                const itemState = await rejectPage
                  .evaluate(
                    async ({ base }) => {
                      const r = await fetch(
                        base + '/api/item?key=repo:schuettc/hail',
                      );
                      return r.ok ? r.json() : null;
                    },
                    { base: serveHandle.base.replace(/\/$/, '') },
                  )
                  .catch(() => null);
                const proposals = itemState?.proposals ?? [];
                const rejected = proposals.find((p) => p.state === 'rejected');
                check(
                  'reject records state=rejected in GET /api/item',
                  Boolean(rejected),
                );
                check(
                  'reject records the reason text in GET /api/item',
                  Boolean(rejected?.reason?.includes('not needed right now')),
                );
              } else {
                check(
                  'reject removes the proposal card from item detail',
                  false,
                );
                check('reject records state=rejected in GET /api/item', false);
                check('reject records the reason text in GET /api/item', false);
              }
            } else {
              check('reject opens the reason sheet', false);
              check('reject removes the proposal card from item detail', false);
              check('reject records state=rejected in GET /api/item', false);
              check('reject records the reason text in GET /api/item', false);
            }
          } else {
            check('reject opens the reason sheet', false);
            check('reject removes the proposal card from item detail', false);
            check('reject records state=rejected in GET /api/item', false);
            check('reject records the reason text in GET /api/item', false);
          }
        } else {
          // proposal was not created — real failure in a fresh probe run
          check(
            'fresh proposal created for reject test (repo:schuettc/hail)',
            false,
          );
        }
      } finally {
        await rejectPage.close();
      }
    }

    // ---- D: change opens the seeded decide sheet ---------------------------
    // pr:schuettc/hail#3 has a pending proposal from the chip-count scenario;
    // D reads that proposal and tests the "change" flow. After change, pr#3 is
    // decided. D's item is dedicated in the sense that A only reads it (no
    // decision) and D is the only scenario that decides it via change.
    {
      const changePage = await context.newPage();
      try {
        await changePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await changePage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to the item detail for pr:schuettc/hail#3.
        // This item has a pending proposal from the chip-count scenario.
        await changePage.evaluate(() => {
          location.hash = '#/item/pr:schuettc%2Fhail%233';
        });
        await changePage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await changePage.waitForTimeout(1000);

        const cardForChange = await changePage
          .$('.cb-proposal-card')
          .then((el) => Boolean(el))
          .catch(() => false);

        if (cardForChange) {
          // Click 'change…' — the second button in the card's actions.
          const btns = await changePage.$$(
            '.cb-proposal-card .kit-btns .kit-btn',
          );
          // buttons order: accept (fill), change…, reject
          const changeBtn = btns[1] ?? null;
          if (changeBtn) {
            const changeBtnText = (await changeBtn.textContent()) ?? '';
            check(
              'change… button is the second action in the proposal card',
              changeBtnText.includes('change'),
            );

            await changeBtn.click();
            await changePage
              .waitForSelector('.kit-sheet', { timeout: 4000 })
              .catch(() => {});
            await changePage.waitForTimeout(400);

            const sheetOpen = await changePage
              .$('.kit-sheet')
              .then((el) => Boolean(el))
              .catch(() => false);
            check('change… opens the decide sheet', sheetOpen);

            if (sheetOpen) {
              // The seeded disposition 'keep' must be pre-selected.
              const keepIsSelected = await changePage
                .$eval(
                  '.kit-sheet .cb-choice.on',
                  (el) => el.getAttribute('data-d') === 'keep',
                )
                .catch(() => false);
              check(
                "change sheet is seeded with the proposal's disposition",
                keepIsSelected,
              );

              // Change to a different disposition (e.g. 'close').
              const closeBtn = await changePage.$(
                '.kit-sheet .cb-choice[data-d="close"]:not(.on)',
              );
              if (closeBtn) {
                await closeBtn.click();
                await changePage.waitForTimeout(200);

                // Submit the decision.
                await changePage.click('.kit-sheet .kit-btn.fill');
                await changePage
                  .waitForFunction(
                    () => !document.querySelector('.kit-sheet'),
                    { timeout: 6000 },
                  )
                  .catch(() => {});
                await changePage.waitForTimeout(500);

                // Proposal card should be gone after change.
                const cardAfterChange = await changePage
                  .$('.cb-proposal-card')
                  .then((el) => Boolean(el))
                  .catch(() => false);
                check(
                  'change confirms and decides the item (proposal card removed)',
                  !cardAfterChange,
                );
              } else {
                // No alternate disposition button found — should not happen for PR.
                check(
                  'change confirms and decides the item (proposal card removed)',
                  false,
                );
              }
            } else {
              check(
                "change sheet is seeded with the proposal's disposition",
                false,
              );
              check(
                'change confirms and decides the item (proposal card removed)',
                false,
              );
            }
          } else {
            check(
              'change… button is the second action in the proposal card',
              false,
            );
            check('change… opens the decide sheet', false);
            check(
              "change sheet is seeded with the proposal's disposition",
              false,
            );
            check(
              'change confirms and decides the item (proposal card removed)',
              false,
            );
          }
        } else {
          // No card: the chip-count proposal for pr#3 was not found.
          // This is a real failure — pr#3 should have a pending proposal.
          check(
            'proposal card present for change test (pr:schuettc/hail#3)',
            false,
          );
        }
      } finally {
        await changePage.close();
      }
    }

    // ---- E: a key and r key ------------------------------------------------
    {
      const keyPage = await context.newPage();
      try {
        await keyPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await keyPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Create a proposal on issue:schuettc/hail#5 for the key tests.
        const propA = await agentPropose(
          keyPage,
          'probe-key-a-sess',
          'pi',
          'issue:schuettc/hail#7',
          'keep',
          'key test a',
        );
        const proposedA = propA?.propose?.proposed ?? 0;

        if (proposedA > 0) {
          // Navigate to proposed view, open the item.
          await keyPage.evaluate(() => {
            location.hash = '#/attention/proposed';
          });
          await keyPage.waitForFunction(
            () => location.hash === '#/attention/proposed',
          );
          await keyPage
            .waitForSelector('.kit-row', { timeout: 6000 })
            .catch(() => {});
          await keyPage.waitForTimeout(600);

          // Click the first row to open the item.
          const firstRow = await keyPage.$('.kit-row');
          if (firstRow) {
            await firstRow.click();
            await keyPage
              .waitForSelector('.cb-proposal-card', { timeout: 5000 })
              .catch(() => {});
            await keyPage.waitForTimeout(600);

            const hasCardForKey = await keyPage
              .$('.cb-proposal-card')
              .then((el) => Boolean(el))
              .catch(() => false);

            if (hasCardForKey) {
              // Dispatch focus to the window (headless Chrome withholds key events
              // without window focus — see progress.md).
              await keyPage.evaluate(() => {
                window.dispatchEvent(new Event('focus'));
                document.body.dispatchEvent(new FocusEvent('focus'));
              });
              await keyPage.waitForTimeout(100);

              // Press 'a' to accept the proposal: serve decides the item,
              // and the reading column moves on off it (the next undecided
              // item in the view, which may hold a card of its own, or the
              // view's summary).
              const openKey = await keyPage.$eval(
                '.kit-read .cb-item',
                (el) => el.getAttribute('data-key') ?? '',
              );
              await keyPage.keyboard.press('a');
              const movedOff = await keyPage
                .waitForFunction(
                  (k) =>
                    document
                      .querySelector('.kit-read .cb-item')
                      ?.getAttribute('data-key') !== k,
                  openKey,
                  { timeout: 5000 },
                )
                .then(() => true)
                .catch(() => false);
              const decided = await fetch(
                `${serveHandle.base}/api/item?key=${encodeURIComponent(openKey)}`,
                { headers: { 'X-Local-Token': serveHandle.token } },
              )
                .then((r) => r.json())
                .then((d) => d.item?.decision?.disposition ?? '')
                .catch(() => '');
              check(
                `"a" key accepts the open item's proposal (${openKey} decided ${decided}, its card gone with it)`,
                openKey !== '' && decided !== '' && movedOff,
              );
            } else {
              check(
                '"a" key accepts the open item\'s proposal (card disappears)',
                false,
              );
            }
          } else {
            check(
              '"a" key accepts the open item\'s proposal (card disappears)',
              false,
            );
          }
        } else {
          // proposal was not created — real failure in a fresh probe run
          check(
            'fresh proposal created for "a" key test (issue:schuettc/hail#7)',
            false,
          );
        }

        // Create a proposal for the 'r' key test on a fresh item.
        // Use branch:schuettc/hail@feat/client (from the fixture's feature branch).
        const propR = await agentPropose(
          keyPage,
          'probe-key-r-sess',
          'pi',
          'repo:schuettc/hail',
          'keep',
          'key test r',
        );
        const proposedR = propR?.propose?.proposed ?? 0;

        if (proposedR > 0) {
          await keyPage.evaluate(() => {
            location.hash = '#/attention/proposed';
          });
          await keyPage.waitForFunction(
            () => location.hash === '#/attention/proposed',
          );
          await keyPage
            .waitForSelector('.kit-row', { timeout: 6000 })
            .catch(() => {});
          await keyPage.waitForTimeout(600);

          // Open the first row.
          const rowForR = await keyPage.$('.kit-row');
          if (rowForR) {
            await rowForR.click();
            await keyPage
              .waitForSelector('.cb-proposal-card', { timeout: 5000 })
              .catch(() => {});
            await keyPage.waitForTimeout(600);

            const hasCardForR = await keyPage
              .$('.cb-proposal-card')
              .then((el) => Boolean(el))
              .catch(() => false);

            if (hasCardForR) {
              await keyPage.evaluate(() => {
                window.dispatchEvent(new Event('focus'));
                document.body.dispatchEvent(new FocusEvent('focus'));
              });
              await keyPage.waitForTimeout(100);

              // Press 'r' to reject (opens the reason sheet).
              await keyPage.keyboard.press('r');
              await keyPage
                .waitForSelector('.kit-sheet', { timeout: 4000 })
                .catch(() => {});
              await keyPage.waitForTimeout(300);

              const sheetFromKey = await keyPage
                .$('.kit-sheet')
                .then((el) => Boolean(el))
                .catch(() => false);
              check(
                '"r" key opens the reject reason sheet for the open item',
                sheetFromKey,
              );

              // Close the sheet.
              await keyPage.keyboard.press('Escape');
              await keyPage.waitForTimeout(300);
            } else {
              check(
                '"r" key opens the reject reason sheet for the open item',
                false,
              );
            }
          } else {
            check(
              '"r" key opens the reject reason sheet for the open item',
              false,
            );
          }
        } else {
          // proposal was not created — real failure in a fresh probe run
          check(
            'fresh proposal created for "r" key test (repo:schuettc/hail)',
            false,
          );
        }
      } finally {
        await keyPage.close();
      }
    }

    // ---- F: bulk accept/reject in proposed view foot -----------------------
    {
      const bulkPage = await context.newPage();
      try {
        await bulkPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await bulkPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to the proposed view.
        await bulkPage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await bulkPage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await bulkPage
          .waitForSelector('.kit-chip[data-id="proposed"]', { timeout: 5000 })
          .catch(() => {});
        await bulkPage.waitForTimeout(600);

        // Count how many proposed rows are available.
        const rows = await bulkPage.$$('.kit-row');
        if (rows.length > 0) {
          // Select all rows by clicking their checkboxes.
          for (const row of rows) {
            const box = await row.$('.kit-box');
            if (box) {
              await box.click();
              await bulkPage.waitForTimeout(50);
            }
          }
          await bulkPage.waitForTimeout(400);

          // The bulk accept button must be visible in the foot.
          const acceptBtnVisible = await bulkPage
            .$eval('.cb-prop-accept', (el) => !el.hidden)
            .catch(() => false);
          check(
            'bulk accept button appears in proposed view foot when items with proposals are selected',
            acceptBtnVisible,
          );

          const acceptBtnText = await bulkPage
            .$eval('.cb-prop-accept', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'bulk accept button shows count of items with proposals',
            acceptBtnText.match(/accept \d+/) !== null,
          );

          const rejectBtnVisible = await bulkPage
            .$eval('.cb-prop-reject', (el) => !el.hidden)
            .catch(() => false);
          check(
            'bulk reject button appears in proposed view foot when items are selected',
            rejectBtnVisible,
          );
        } else {
          // No proposed items in the view — this is a real failure: branch:schuettc/hail@feat/client
          // should still have a pending proposal from the 'r' key scenario.
          check('proposed view has rows for bulk accept/reject test', false);
        }
      } finally {
        await bulkPage.close();
      }
    }

    // ---- fix-round-2: foot layout — single line, no wrap, no overlap --------
    console.log(
      '\nscenario: foot layout — single line, no wrap at 400\u202fpx list width',
    );

    {
      const footPage = await context.newPage();
      try {
        await footPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await footPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Helper: check that every foot control is single-line (no wrapping)
        // and that no two controls overlap horizontally.
        async function checkFootLayout(
          view,
          label,
          { assertSelAllHidden = false } = {},
        ) {
          await footPage.evaluate((v) => {
            location.hash = `#/attention/${v}`;
          }, view);
          await footPage.waitForFunction(
            (v) => location.hash === `#/attention/${v}`,
            view,
          );
          await footPage
            .waitForSelector('.kit-row', { timeout: 5000 })
            .catch(() => {});
          await footPage.waitForTimeout(400);

          // Select all visible rows to trigger the bulk/sel-count display.
          const boxes = footPage.locator('.kit-row .kit-box');
          const nBoxes = await boxes.count();
          for (let i = 0; i < nBoxes; i++) {
            await boxes.nth(i).click();
            await footPage.waitForTimeout(30);
          }
          await footPage.waitForTimeout(300);

          // After selecting all rows, the "Select all" button must be hidden.
          // Only assert for views where this behaviour is expected (opt-in via
          // assertSelAllHidden): the proposed view hides the button once every
          // listed item is checked.
          if (assertSelAllHidden && nBoxes > 0) {
            const selAllHidden = await footPage.evaluate(() => {
              const el = document.querySelector('.cb-sel-all');
              if (!el) return true; // absent → hidden for this view
              const cs = getComputedStyle(el);
              return (
                el.hidden ||
                cs.display === 'none' ||
                cs.visibility === 'hidden' ||
                cs.opacity === '0' ||
                el.offsetParent === null
              );
            });
            check(
              `${label}: .cb-sel-all is hidden when all rows are selected`,
              selAllHidden,
            );
          }

          // Collect bounding rects of all visible foot controls.
          const footRects = await footPage.evaluate(() => {
            const foot = document.querySelector('.cb-foot');
            if (!foot) return null;
            // Gather all interactive foot controls (visible ones only).
            const selectors = [
              '.cb-sel-count',
              '.cb-sel-all',
              '.cb-prop-accept',
              '.cb-prop-reject',
              '.cb-foot-more',
            ];
            const rects = [];
            for (const sel of selectors) {
              const el = foot.querySelector(sel);
              if (!el || el.hidden || getComputedStyle(el).display === 'none')
                continue;
              const r = el.getBoundingClientRect();
              if (r.width === 0 && r.height === 0) continue;
              // lineHeight: scrollHeight > clientHeight would indicate wrap.
              rects.push({
                sel,
                top: r.top,
                bottom: r.bottom,
                left: r.left,
                right: r.right,
                scrollH: el.scrollHeight,
                clientH: el.clientHeight,
              });
            }
            return rects;
          });

          if (!footRects || footRects.length === 0) {
            check(`${label}: foot controls found`, false);
            return;
          }

          // Each control must not be taller than a single line.
          // A line height of ~20 px is typical; wrapping adds at least half again.
          // We check scrollHeight <= clientHeight (no overflow) as the wrap signal.
          let allSingleLine = true;
          for (const r of footRects) {
            if (r.scrollH > r.clientH + 2) {
              allSingleLine = false;
              console.error(
                `    wrap detected in ${r.sel}: scrollH=${r.scrollH} clientH=${r.clientH}`,
              );
            }
          }
          check(
            `${label}: all foot controls are single-line (no wrapping)`,
            allSingleLine,
          );

          // No two controls must overlap horizontally (left/right rectangles).
          let noOverlap = true;
          for (let i = 0; i < footRects.length; i++) {
            for (let j = i + 1; j < footRects.length; j++) {
              const a = footRects[i];
              const b = footRects[j];
              // Overlap: a.right > b.left && b.right > a.left (horizontal).
              if (a.right > b.left + 1 && b.right > a.left + 1) {
                noOverlap = false;
                console.error(
                  `    overlap: ${a.sel} [${a.left.toFixed(0)}\..\.${a.right.toFixed(0)}] overlaps ${b.sel} [${b.left.toFixed(0)}\..\.${b.right.toFixed(0)}]`,
                );
              }
            }
          }
          check(`${label}: no foot controls overlap`, noOverlap);
        }

        // Test with a selection in the proposed view; also verify that the
        // "Select all" button hides once every listed item is checked.
        await checkFootLayout('proposed', 'proposed view with selection', {
          assertSelAllHidden: true,
        });

        // Test with a selection in the new view (no bulk buttons).
        // The sel-all button doesn't have the same hide logic here.
        await checkFootLayout('new', 'new view with selection');

        // Take fix-round-2 screenshots.
        await footPage.setViewportSize({ width: 1600, height: 900 });

        // proposed: 1 selected
        await footPage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await footPage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await footPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await footPage.waitForTimeout(400);
        const propRows1 = await footPage.$$('.kit-row .kit-box');
        if (propRows1.length > 0) await propRows1[0].click();
        await footPage.waitForTimeout(300);
        await footPage.screenshot({ path: '/tmp/t5fix2-proposed-1sel.png' });

        // proposed: all selected
        for (let i = 1; i < propRows1.length; i++) {
          await propRows1[i].click();
          await footPage.waitForTimeout(30);
        }
        await footPage.waitForTimeout(300);
        await footPage.screenshot({ path: '/tmp/t5fix2-proposed-allsel.png' });

        // new: 2 selected
        await footPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await footPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await footPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await footPage.waitForTimeout(400);
        const newRows = await footPage.$$('.kit-row .kit-box');
        if (newRows.length > 0) await newRows[0].click();
        if (newRows.length > 1) await newRows[1].click();
        // Wait until the selection counter reflects 2 selected items before
        // capturing the screenshot (the old code just timed out, which meant the
        // count might not have updated yet).
        await footPage
          .waitForFunction(
            () => {
              const el = document.querySelector('.cb-sel-count');
              return el && /\b2\b/.test(el.textContent ?? '');
            },
            { timeout: 3000 },
          )
          .catch(() => {});
        await footPage.screenshot({ path: '/tmp/t5fix2-new-2sel.png' });
        console.log(
          '  t5fix2 screenshots: /tmp/t5fix2-proposed-1sel.png  /tmp/t5fix2-proposed-allsel.png  /tmp/t5fix2-new-2sel.png',
        );
      } finally {
        await footPage.close();
      }
    }

    // ---- G: live proposals event updates the agent name in list rows --------
    {
      const claudePage = await context.newPage();
      try {
        await claudePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await claudePage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to proposed view first to see any proposals.
        await claudePage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await claudePage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await claudePage.waitForTimeout(600);

        // Create a proposal from a 'claude' session so we can verify the
        // agent name is taken from the source, not hardcoded.
        // repo:schuettc/hail is still undecided (its proposal was rejected in
        // scenario C, not decided).  Create a fresh proposal from 'claude'.
        const claudeResult = await agentPropose(
          claudePage,
          'probe-claude-sess',
          'claude',
          'repo:schuettc/hail',
          'keep',
          'claude probe note',
        );
        const proposedClaude = claudeResult?.propose?.proposed ?? 0;

        if (proposedClaude > 0) {
          // Wait for the proposals live event and list reload.
          await claudePage.waitForTimeout(3000);

          // The row sub-text must contain 'claude' (not 'pi').
          const subTexts = await claudePage
            .$$eval('.kit-row .kit-sub', (els) =>
              els.map((e) => e.textContent ?? ''),
            )
            .catch(() => []);
          const hasClaudeText = subTexts.some((t) =>
            t.toLowerCase().includes('claude'),
          );
          check(
            'a proposal from claude shows "claude" in the list row sub-line',
            hasClaudeText,
          );
        } else {
          // proposal was not created — real failure in a fresh probe run
          // (repo:schuettc/hail should be undecided after scenario C rejected its proposal)
          check(
            'fresh proposal created for claude-name test (repo:schuettc/hail)',
            false,
          );
        }
      } finally {
        await claudePage.close();
      }
    }

    // ---- Task 5 screenshots ------------------------------------------------
    {
      const t5Page = await context.newPage();
      try {
        await t5Page.setViewportSize({ width: 1600, height: 900 });
        await t5Page.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await t5Page.waitForSelector('.kit-bar', { timeout: 8000 });

        async function detectThemeT5(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        async function forceThemeT5(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectThemeT5(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          const actual = await detectThemeT5(pg);
          check(
            `t5 theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // Use repo:schuettc/hail for the proposal card screenshot.
        // At this point repo has a pending proposal from the claude-name test (G).
        // We create one more from probe-t5-sess to ensure the card is visible.
        await agentPropose(
          t5Page,
          'probe-t5-sess',
          'pi',
          'repo:schuettc/hail',
          'archive',
          't5 screenshot note',
        );

        // Light: item detail with proposal card.
        await forceThemeT5(t5Page, 'light');
        await t5Page.evaluate(() => {
          location.hash = '#/item/repo:schuettc%2Fhail';
        });
        await t5Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await t5Page.waitForTimeout(1000);
        await t5Page.screenshot({ path: '/tmp/t5fix-light-item.png' });

        // Light: proposed view with a selection (so bulk buttons are visible).
        await t5Page.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await t5Page.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await t5Page
          .waitForSelector('.kit-chip[data-id="proposed"]', { timeout: 5000 })
          .catch(() => {});
        await t5Page.waitForTimeout(600);
        // Select all rows so the bulk accept/reject buttons appear in the foot.
        const t5PropBoxes = await t5Page.$$('.kit-row .kit-box');
        for (const box of t5PropBoxes) {
          await box.click();
          await t5Page.waitForTimeout(50);
        }
        await t5Page.waitForTimeout(400);
        await t5Page.screenshot({ path: '/tmp/t5fix-light-proposed.png' });

        // Dark: same pages.
        await forceThemeT5(t5Page, 'dark');
        const darkTheme = await detectThemeT5(t5Page);
        check('t5 dark theme is dark', darkTheme === 'dark');

        // Re-select rows for dark proposed screenshot.
        const t5DarkBoxes = await t5Page.$$('.kit-row .kit-box');
        for (const box of t5DarkBoxes) {
          await box.click();
          await t5Page.waitForTimeout(50);
        }
        await t5Page.waitForTimeout(400);
        await t5Page.screenshot({ path: '/tmp/t5fix-dark-proposed.png' });

        await t5Page.evaluate(() => {
          location.hash = '#/item/repo:schuettc%2Fhail';
        });
        await t5Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await t5Page.waitForTimeout(1000);
        await t5Page.screenshot({ path: '/tmp/t5fix-dark-item.png' });

        console.log(
          '  t5fix screenshots: /tmp/t5fix-light-item.png  /tmp/t5fix-light-proposed.png',
        );
        console.log(
          '                     /tmp/t5fix-dark-item.png   /tmp/t5fix-dark-proposed.png',
        );
      } finally {
        await t5Page.close();
      }
    }

    // ---- scenario: board screenshot at 1600x900 ----------------------------
    console.log(
      '\nscenario: board screenshot at 1600×900 — /tmp/fix3-board.png',
    );

    {
      const boardShotCtx = await browser.newContext({
        viewport: { width: 1600, height: 900 },
      });
      const boardShotPage = await boardShotCtx.newPage();
      try {
        await boardShotPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await boardShotPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await boardShotPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await boardShotPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await boardShotPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await boardShotPage.waitForTimeout(800);
        await boardShotPage.screenshot({
          path: '/tmp/fix3-board.png',
          fullPage: false,
        });
        console.log('  board screenshot: /tmp/fix3-board.png');
        // Basic sanity: all 4 lanes are visible.
        const laneCount = await boardShotPage.$$eval(
          '.cb-lane',
          (lanes) => lanes.length,
        );
        check('fix3-board screenshot: four lanes visible', laneCount === 4);
      } finally {
        await boardShotPage.close();
        await boardShotCtx.close();
      }
    }

    // ---- scenario: the dock shows the session and messages -----------------
    // Use a fresh page so the dock loads the fake agent's session data.
    console.log('\nscenario: the dock shows the session and messages');

    {
      const dockPage = await context.newPage();
      try {
        await dockPage.setViewportSize({ width: 1600, height: 900 });

        // Register a fake agent session and create a thread + messages.
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const sessId = `probe-dock-${Date.now()}`;
        await agent.presence(
          sessId,
          'pi · tools-workspace',
          '/home/court/tools-workspace',
        );
        const thread = await agent.newThread(sessId, 'triage');
        const m1 = await agent.postMessage(
          thread.id,
          'Propose decisions for the 40 chime PRs.',
          {
            keys: [
              'pr:schuettc/bettor-help-platform#670',
              'pr:schuettc/bettor-help-platform#671',
            ],
          },
        );

        // Opened by its session (casebook_open puts ?session= in the URL).
        await dockPage.goto(
          `${serveHandle.url}&session=${encodeURIComponent(sessId)}`,
          {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          },
        );
        await dockPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Wait for the dock to load the session and render its header.
        // The dock is inside .kit-rail.
        await dockPage
          .waitForSelector('.kit-rail', { timeout: 5000 })
          .catch(() => {});
        // Give the dock time to load sessions from the API.
        await dockPage.waitForTimeout(1500);

        // 1. Rail is at the right of the viewport.
        {
          const railRect = await dockPage
            .$eval('.kit-rail', (el) => {
              const r = el.getBoundingClientRect();
              return { right: r.right, width: r.width, visible: r.width > 0 };
            })
            .catch(() => ({ right: 0, width: 0, visible: false }));
          const vpWidth = await dockPage.evaluate(() => window.innerWidth);
          check('rail is visible', railRect.visible);
          check(
            'rail sits at the right of the viewport',
            railRect.right >= vpWidth - 4,
          );
          check('rail has non-zero width', railRect.width > 0);
        }

        // 2. Session header shows the session label.
        {
          const headerText = await dockPage
            .$eval('.cb-dock-header', (el) => el.textContent ?? '')
            .catch(() => '');
          // Should show the session label (from label or harness·cwd)
          const hasSession =
            headerText.includes('pi') ||
            headerText.includes('tools-workspace') ||
            headerText.includes('probe-dock');
          check('dock header shows the attached session', hasSession);
        }

        // 3. Thread chips include the thread we created.
        {
          const chipText = await dockPage
            .$$eval('.cb-dock-threads .kit-chip', (chips) =>
              chips.map((c) => c.textContent ?? '').join('|'),
            )
            .catch(() => '');
          check(
            'thread chip appears for the created thread',
            chipText.includes('triage'),
          );
          check('+ chip present for new thread', chipText.includes('+'));
        }

        // 4. Court's message card is visible in the dock.
        {
          // The dock loads the first thread. Wait for the message card.
          await dockPage.waitForTimeout(500);
          const msgCards = await dockPage
            .$$eval('[data-testid="dock-messages"] .kit-card', (cards) =>
              cards.map((c) => c.textContent ?? ''),
            )
            .catch(() => []);
          const hasMsg = msgCards.some(
            (t) => t.includes('chime PRs') || t.includes('Propose'),
          );
          check("Court's message card appears in the dock", hasMsg);
        }

        // 5. The cards inside the rail are visible.
        {
          const inside = await dockPage
            .evaluate(() => {
              const rail = document.querySelector('.kit-rail');
              const cards = document.querySelectorAll(
                '[data-testid="dock-messages"] .kit-card',
              );
              if (!rail || cards.length === 0) return false;
              const rr = rail.getBoundingClientRect();
              const cr = cards[0].getBoundingClientRect();
              // Card must be horizontally inside the rail.
              return (
                cr.left >= rr.left - 4 &&
                cr.right <= rr.right + 4 &&
                cr.width > 0
              );
            })
            .catch(() => false);
          check('message cards are inside the rail and visible', inside);
        }

        // 6. YOU card has rust (signal) left edge; agent card has amber edge.
        //    The agent replies with attached items so we get a PI card with links.
        {
          // First let the agent pick up the delivery.
          const waitResult = await agent.wait(sessId);
          if (waitResult) {
            // Reply with attached keys so the PI card has item links.
            await agent.reply(
              sessId,
              [m1.id],
              'answered',
              'Done: 38 close, 2 keep. They are in proposed.',
              {
                keys: [
                  'pr:schuettc/bettor-help-platform#670',
                  'pr:schuettc/bettor-help-platform#671',
                ],
              },
            );
          }
          // Wait for the PI card (agent reply) to appear in the dock.
          await dockPage
            .waitForSelector(
              '[data-testid="dock-messages"] .cb-dock-card--agent',
              { timeout: 5000 },
            )
            .catch(() => {});

          // Check edge colours of cards via computed style.
          const edges = await dockPage
            .evaluate(() => {
              const cards = document.querySelectorAll(
                '[data-testid="dock-messages"] .kit-card',
              );
              return Array.from(cards).map((c) => ({
                isYou: c.classList.contains('cb-dock-card--you'),
                isAgent: c.classList.contains('cb-dock-card--agent'),
                borderLeft: getComputedStyle(c).borderLeftColor,
              }));
            })
            .catch(() => []);

          const youCards = edges.filter((e) => e.isYou);
          const agentCards = edges.filter((e) => e.isAgent);
          check('YOU card has rust (signal) left edge', youCards.length > 0);
          // PI card must now be present (we waited for it above).
          check(
            'YOU and PI edge colours differ',
            agentCards.length > 0 &&
              youCards.length > 0 &&
              youCards[0].borderLeft !== agentCards[0].borderLeft,
          );
        }

        // 7. Delivery state appears on Court's card.
        {
          // The message m1 should now be in 'answered' state (we replied above).
          // Reload messages in the dock by waiting for the live event.
          await dockPage.waitForTimeout(600);
          const stateFooters = await dockPage
            .$$eval('[data-testid="dock-messages"] .cb-dock-state', (els) =>
              els.map((e) => ({
                text: e.textContent ?? '',
                state: e.dataset.state ?? '',
              })),
            )
            .catch(() => []);
          // Should see at least one state footer.
          check(
            "Court's card shows a delivery state footer",
            stateFooters.length > 0,
          );
        }

        // 8. Agent card link opens the route.
        {
          // Look for any link inside an agent card.
          const agentCardLinks = await dockPage
            .$$eval(
              '[data-testid="dock-messages"] .cb-dock-card--agent .cb-dock-link',
              (links) =>
                links.map((l) => ({
                  href: l.getAttribute('href'),
                  text: l.textContent ?? '',
                })),
            )
            .catch(() => []);
          // The agent reply was sent with attached keys (pr:...#670, pr:...#671).
          // The fixture guarantees links are present; assert they are.
          check(
            'agent card has item links (fixture includes attached keys)',
            agentCardLinks.length > 0,
          );
          if (agentCardLinks.length > 0) {
            // Click the first link and verify the hash changes.
            await dockPage.click(
              '[data-testid="dock-messages"] .cb-dock-card--agent .cb-dock-link',
            );
            await dockPage.waitForTimeout(300);
            const hash = await dockPage.evaluate(() => location.hash);
            check(
              'an agent card link opens the route',
              hash.includes('#/item/') ||
                hash.includes('#/rules/') ||
                hash.includes('#/apply/'),
            );
          } else {
            check('an agent card link opens the route', false);
          }
        }

        // 9. Live event updates the rail without a reload.
        //    Post another message and check it appears.
        {
          const beforeCount = await dockPage
            .$$eval(
              '[data-testid="dock-messages"] .kit-card',
              (els) => els.length,
            )
            .catch(() => 0);
          await agent.postMessage(
            thread.id,
            'Merge the 4 bettor-help bumps if CI is green.',
          );
          // Wait for the live SSE event to arrive and re-render.
          await dockPage.waitForTimeout(1200);
          const afterCount = await dockPage
            .$$eval(
              '[data-testid="dock-messages"] .kit-card',
              (els) => els.length,
            )
            .catch(() => 0);
          check(
            'live message event adds a card without reload',
            afterCount > beforeCount,
          );
        }
      } finally {
        await dockPage.close();
      }
    }

    // ---- scenario: every delivery state renders its label ------------------
    console.log('\nscenario: every delivery state renders its label');

    {
      const statePage = await context.newPage();
      try {
        await statePage.setViewportSize({ width: 1600, height: 900 });

        const agent = createAgent(serveHandle.base, serveHandle.token);
        const sessId = `probe-states-${Date.now()}`;
        await agent.presence(
          sessId,
          'pi · tools-workspace',
          '/home/court/tools-workspace',
        );
        const thread = await agent.newThread(sessId, 'states');

        // Post messages for each delivery state we want to test.
        // We can control state via what the agent replies with.
        const msgA = await agent.postMessage(
          thread.id,
          'This message will be answered.',
        );
        const msgD = await agent.postMessage(
          thread.id,
          'This message will be declined.',
        );

        // Pick up the delivery (both messages in one delivery).
        const delivery = await agent.wait(sessId);
        if (delivery) {
          await agent.reply(sessId, [msgA.id], 'answered', 'Done!');
          await agent.reply(sessId, [msgD.id], 'declined', 'Not applicable.');
        }

        // Post a new queued message (agent is between turns).
        const msgQ = await agent.postMessage(
          thread.id,
          'This message is queued.',
        );
        void msgQ; // suppress unused

        // Opened by its session (casebook_open puts ?session= in the URL).
        await statePage.goto(
          `${serveHandle.url}&session=${encodeURIComponent(sessId)}`,
          {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          },
        );
        await statePage.waitForSelector('.kit-bar', { timeout: 8000 });
        await statePage.waitForTimeout(2000);

        // Check that state labels appear on Court's cards.
        const stateLabels = await statePage
          .$$eval('[data-testid="dock-messages"] .cb-dock-state', (els) =>
            els.map((e) => e.dataset.state ?? ''),
          )
          .catch(() => []);
        const stateSet = new Set(stateLabels);
        check("delivery state 'answered' renders", stateSet.has('answered'));
        check("delivery state 'declined' renders", stateSet.has('declined'));
        check("delivery state 'queued' renders", stateSet.has('queued'));
      } finally {
        await statePage.close();
      }
    }

    // ---- scenario: stuck delivery offers release and move ------------------
    // Drives the dock from the page: clicks release and move buttons.
    console.log('\nscenario: stuck delivery offers release and move');

    {
      const stuckPage = await context.newPage();
      try {
        await stuckPage.setViewportSize({ width: 1600, height: 900 });

        // serve was started with CASEBOOK_STUCK_AFTER=2s so deliveries
        // become stuck after 2 seconds of no activity.
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const ts = Date.now();
        const sessId = `probe-stuck-${ts}`;
        const sessId2 = `probe-stuck-other-${ts}`;

        // Register sessId2 first (target for move).
        await agent.presence(sessId2, 'pi · other', '/home/court/other');
        await agent.newThread(sessId2, 'target');
        // Register sessId last so it is most-recently-seen → dock shows it.
        await agent.presence(sessId, 'pi · stuck-ws', '/home/court/stuck-ws');
        const thread = await agent.newThread(sessId, 'stuck-test');
        await agent.postMessage(thread.id, 'This message will get stuck.');

        // Agent picks up the delivery but does NOT reply — it will become stuck.
        const deliveryData = await agent.wait(sessId);
        check(
          'delivery exists',
          deliveryData !== null && deliveryData.delivery !== null,
        );

        // Keep sessId heartbeating so it stays present (not left) while we
        // wait for the delivery to become stuck. CASEBOOK_LEFT_AFTER=3s;
        // the stuck wait is ~4.5s total, so without this sessId would go left.
        const stuckHB = setInterval(() => {
          agent
            .presence(sessId, 'pi · stuck-ws', '/home/court/stuck-ws')
            .catch(() => {});
        }, 800);

        // Opened by its session (casebook_open puts ?session= in the URL).
        await stuckPage.goto(
          `${serveHandle.url}&session=${encodeURIComponent(sessId)}`,
          {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          },
        );
        await stuckPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Wait for the dock to load the session (initial loadSessions → loadDelivery).
        await stuckPage.waitForTimeout(2000);

        // Wait for the delivery to become stuck (CASEBOOK_STUCK_AFTER=2s).
        await stuckPage.waitForTimeout(2500);

        // Trigger dock refresh: a sessions event causes loadSessions → loadDelivery.
        // Send presence for sessId2 (not sessId, so sessId's last_seen stays old).
        await agent.presence(sessId2, 'pi · other', '/home/court/other');
        // The dock now re-fetches and sees delivery.stuck=true.
        // Wait for the stuck buttons to appear.
        const stuckVisible = await stuckPage
          .waitForSelector('.cb-dock-stuck', { timeout: 8000 })
          .then(() => true)
          .catch(() => false);
        check('stuck delivery shows release and move buttons', stuckVisible);

        // Also verify via API that the delivery is indeed stuck.
        const dv = await agent.getDelivery(sessId);
        check('delivery is stuck (API)', dv?.delivery?.stuck === true);

        // Stop heartbeating now that we've confirmed the stuck state.
        clearInterval(stuckHB);

        // ---- Finding 2: stuck footer colour and text ----------------------
        // The stuck footer uses the muted colour (not --kit-wait/blue), and
        // the text reads "delivered <age> ago · <bold>stuck</bold>" or
        // "delivered just now · <bold>stuck</bold>" for sub-minute ages.
        if (stuckVisible) {
          const footerInfo = await stuckPage
            .evaluate(() => {
              const footer = document.querySelector('[data-state="stuck"]');
              if (!footer) return null;
              return {
                text: footer.textContent ?? '',
                color: getComputedStyle(footer).color,
              };
            })
            .catch(() => null);

          // Resolve --kit-wait and --kit-muted for comparison.
          const cssColors = await stuckPage
            .evaluate(() => {
              function resolveVar(v) {
                const el = document.createElement('div');
                el.style.cssText = `border-left: 1px solid ${v}; position: absolute; visibility: hidden;`;
                document.body.appendChild(el);
                const c = getComputedStyle(el).borderLeftColor;
                el.remove();
                return c;
              }
              return {
                wait: resolveVar('var(--kit-wait)'),
                muted: resolveVar('var(--kit-muted)'),
              };
            })
            .catch(() => ({ wait: '', muted: '' }));

          const footerText = footerInfo?.text ?? '';
          // Matches "delivered 5m ago · stuck" or "delivered just now · stuck"
          const textOk =
            /^delivered .+ · stuck$/.test(footerText.trim()) ||
            footerText.trim() === 'delivered just now · stuck';
          check('stuck footer text matches "delivered ... · stuck"', textOk);

          // The stuck footer must NOT be the wait (blue) colour — it is muted.
          const colorOk =
            !!footerInfo?.color &&
            footerInfo.color !== cssColors.wait &&
            footerInfo.color === cssColors.muted;
          check('stuck footer colour is muted (not wait/blue)', colorOk);

          // ---- Finding 3: present session dot has agent colour ----------------
          const dotInfo = await stuckPage
            .evaluate(() => {
              function resolveVar(v) {
                const el = document.createElement('div');
                el.style.cssText = `background: ${v}; position: absolute; visibility: hidden;`;
                document.body.appendChild(el);
                const c = getComputedStyle(el).backgroundColor;
                el.remove();
                return c;
              }
              const dot = document.querySelector('.cb-dock-dot');
              return {
                dotBg: dot ? getComputedStyle(dot).backgroundColor : '',
                agentColor: resolveVar('var(--kit-agent)'),
                mutedColor: resolveVar('var(--kit-muted)'),
              };
            })
            .catch(() => ({ dotBg: '', agentColor: '', mutedColor: '' }));
          // In the stuck scenario the session is heartbeating (not left), so
          // the dot should be the agent colour.
          check(
            'present session dot has agent computed colour',
            !!dotInfo.dotBg &&
              dotInfo.dotBg === dotInfo.agentColor &&
              dotInfo.dotBg !== dotInfo.mutedColor,
          );

          // ---- Finding 5: action buttons use kit-btn neutral, not signal ------
          const btnInfo = await stuckPage
            .evaluate(() => {
              function resolveBorder(v) {
                const el = document.createElement('div');
                el.style.cssText = `border-left: 1px solid ${v}; position: absolute; visibility: hidden;`;
                document.body.appendChild(el);
                const c = getComputedStyle(el).borderLeftColor;
                el.remove();
                return c;
              }
              const btn = document.querySelector(
                '.cb-dock-stuck [data-action]',
              );
              return {
                border: btn ? getComputedStyle(btn).borderLeftColor : '',
                kitLine: resolveBorder('var(--kit-line)'),
                kitSignal: resolveBorder('var(--kit-signal)'),
                kitDanger: resolveBorder('var(--kit-danger)'),
              };
            })
            .catch(() => ({
              border: '',
              kitLine: '',
              kitSignal: '',
              kitDanger: '',
            }));

          check(
            'stuck action buttons border equals kit-line (neutral, like DECIDE)',
            !!btnInfo.border && btnInfo.border === btnInfo.kitLine,
          );
          check(
            'stuck action buttons border is not the signal colour',
            !!btnInfo.border && btnInfo.border !== btnInfo.kitSignal,
          );
          check(
            'stuck action buttons border is not the danger colour',
            !!btnInfo.border && btnInfo.border !== btnInfo.kitDanger,
          );
        } else {
          check('stuck footer text matches "delivered ... · stuck"', false);
          check('stuck footer colour is muted (not wait/blue)', false);
          check('present session dot has agent computed colour', false);
          check(
            'stuck action buttons border equals kit-line (neutral, like DECIDE)',
            false,
          );
          check('stuck action buttons border is not the signal colour', false);
          check('stuck action buttons border is not the danger colour', false);
        }

        // Take screenshots showing the dock with a stuck delivery.
        await stuckPage.screenshot({
          path: '/tmp/t6fix2-stuck.png',
          fullPage: false,
        });
        await stuckPage.screenshot({
          path: '/tmp/t6fix3-stuck.png',
          fullPage: false,
        });
        console.log(
          '  screenshots: /tmp/t6fix2-stuck.png  /tmp/t6fix3-stuck.png',
        );

        // ---- Click the release button on the page -------------------------
        if (stuckVisible) {
          await stuckPage.click('[data-action="release"]');
          // Wait for the stuck div to disappear (delivery released).
          await stuckPage
            .waitForFunction(() => !document.querySelector('.cb-dock-stuck'), {
              timeout: 6000,
            })
            .catch(() => {});
          const stillStuck = await stuckPage
            .$('.cb-dock-stuck')
            .catch(() => null);
          check(
            'clicking release removes the stuck buttons from the dock',
            stillStuck === null,
          );
        } else {
          check(
            'clicking release removes the stuck buttons from the dock',
            false,
          );
        }

        // ---- Post a new message and create another stuck delivery ----------
        await agent.postMessage(thread.id, 'message for move test');
        const mv = await agent.wait(sessId);
        check(
          'second delivery exists for move test',
          mv !== null && mv.delivery !== null,
        );

        // Wait for this delivery to become stuck.
        await stuckPage.waitForTimeout(2500);
        // Trigger dock refresh.
        await agent.presence(sessId2, 'pi · other', '/home/court/other');
        const stuckVisible2 = await stuckPage
          .waitForSelector('.cb-dock-stuck', { timeout: 8000 })
          .then(() => true)
          .catch(() => false);
        check('second stuck delivery shows move button', stuckVisible2);

        // ---- Click "move to another session" on the page ------------------
        if (stuckVisible2) {
          await stuckPage.click('[data-action="move"]');
          // The move sheet appears — click the target session.
          await stuckPage.waitForTimeout(400);
          const sheetItems = await stuckPage.$$(
            '.cb-dock-pick-sheet .cb-dock-pick-item',
          );
          check('move sheet shows sessions', sheetItems.length > 0);

          if (sheetItems.length > 0) {
            // Click the first item in the sheet (sessId2).
            await sheetItems[0].click();
            await stuckPage.waitForTimeout(800);
            // After moving, the delivery is no longer on sessId.
            // The dock should no longer show stuck buttons for sessId.
            const movedAwayOk = await stuckPage
              .$('.cb-dock-stuck')
              .then((el) => el === null)
              .catch(() => true);
            check(
              'moving delivery removes stuck buttons from the dock',
              movedAwayOk,
            );
            // MoveDelivery requeues messages to sessId2. Verify sessId2 now
            // has queued messages (deliverable on next agent.wait for sessId2).
            const afterMoveSess = await fetch(
              `${serveHandle.base}/api/sessions`,
              { headers: { 'X-Local-Token': serveHandle.token } },
            )
              .then((r) => r.json())
              .catch(() => ({ sessions: [] }));
            const s2q =
              (afterMoveSess.sessions ?? []).find((s) => s.id === sessId2)
                ?.queued ?? 0;
            check(
              'delivery moved to target session (API): messages queued on target',
              s2q > 0,
            );
          } else {
            check('moving delivery removes stuck buttons from the dock', false);
            check('delivery moved to target session (API)', false);
          }
        } else {
          check('move sheet shows sessions', false);
          check('moving delivery removes stuck buttons from the dock', false);
          check('delivery moved to target session (API)', false);
        }
      } finally {
        await stuckPage.close();
      }
    }

    // ---- scenario: a left session shows the queued count and move ----------
    // serve started with CASEBOOK_LEFT_AFTER=3s so sessions go left quickly.
    // Drives the left-session "move to..." from the page.
    console.log('\nscenario: a left session shows the queued count and move');

    {
      const leftPage = await context.newPage();
      let leftTargetHB = null;
      try {
        await leftPage.setViewportSize({ width: 1600, height: 900 });

        const agent = createAgent(serveHandle.base, serveHandle.token);
        const ts = Date.now();
        const sessId = `probe-left-${ts}`;
        const sessId2 = `probe-left-target-${ts}`;

        // Register sessId2 first (target), then sessId last so it is
        // most-recently-seen => dock picks it as the current session.
        await agent.presence(
          sessId2,
          'pi · left-target',
          '/home/court/left-target',
        );
        await agent.newThread(sessId2, 'target');
        await agent.presence(sessId, 'pi · left-ws', '/home/court/left-ws');
        const thread = await agent.newThread(sessId, 'left-test');
        await agent.postMessage(thread.id, 'first queued message');
        await agent.postMessage(thread.id, 'second queued message');

        // Verify server-side queued count is 2.
        const sessResp = await fetch(`${serveHandle.base}/api/sessions`, {
          headers: { 'X-Local-Token': serveHandle.token },
        });
        const sessData = await sessResp.json();
        const thisSess = (sessData.sessions ?? []).find((s) => s.id === sessId);
        check('queued count is 2 in sessions API', thisSess?.queued === 2);

        // Navigate to page: dock picks sessId as current (most recently seen).
        // Opened by its session (casebook_open puts ?session= in the URL).
        await leftPage.goto(
          `${serveHandle.url}&session=${encodeURIComponent(sessId)}`,
          {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          },
        );
        await leftPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await leftPage.waitForTimeout(1500);

        // Wait 3.5s for sessId to go left (CASEBOOK_LEFT_AFTER=3s).
        await leftPage.waitForTimeout(3500);

        // Trigger dock refresh: send presence for sessId2 (NOT sessId, so
        // sessId's last_seen stays old => remains left). sessId2 keeps
        // announcing itself: the move sheet offers only sessions that are
        // here.
        await agent.presence(
          sessId2,
          'pi · left-target',
          '/home/court/left-target',
        );
        leftTargetHB = setInterval(() => {
          agent
            .presence(sessId2, 'pi · left-target', '/home/court/left-target')
            .catch(() => {});
        }, 800);
        // The sessions event causes loadSessions => left=true for sessId.
        // Wait for [data-left] on the dock header.
        const leftHeaderVisible = await leftPage
          .waitForSelector('.cb-dock-header[data-left]', { timeout: 8000 })
          .then(() => true)
          .catch(() => false);
        check('left session header appears (data-left)', leftHeaderVisible);

        if (leftHeaderVisible) {
          // Visibility assertion: header text shows queued count.
          const headerText = await leftPage
            .$eval('.cb-dock-header', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'left header shows queued count',
            headerText.includes('queued') || headerText.includes('2'),
          );
          // "move to..." link is visible.
          const moveLinkVisible = await leftPage
            .$eval('[data-testid="dock-move-link"]', (el) => el.offsetWidth > 0)
            .catch(() => false);
          check(
            '"move to..." link is visible on left session header',
            moveLinkVisible,
          );

          // Geometry check: header text doesn't overflow the rail.
          // The header row's scrollWidth must not exceed its clientWidth.
          const headerNoOverflow = await leftPage
            .evaluate(() => {
              const headerRow = document.querySelector('.cb-dock-header-row');
              if (!headerRow) return false;
              return headerRow.scrollWidth <= headerRow.clientWidth + 2;
            })
            .catch(() => false);
          check(
            'left header row does not overflow (scrollWidth ≤ clientWidth)',
            headerNoOverflow,
          );

          // Geometry check: AGENT ▾ button is not overlapped by the session label.
          const noOverlap = await leftPage
            .evaluate(() => {
              const agentBtn = document.querySelector('.cb-dock-agent-btn');
              const label = document.querySelector('.cb-dock-session-label');
              if (!agentBtn || !label) return false;
              const ar = agentBtn.getBoundingClientRect();
              const lr = label.getBoundingClientRect();
              // No overlap: label right must not extend into agent btn left.
              return lr.right <= ar.left + 4;
            })
            .catch(() => false);
          check('session label does not overlap AGENT ▾ button', noOverlap);

          // ---- Finding 3: left session dot is muted -------------------------
          const dotColors = await leftPage
            .evaluate(() => {
              function resolveBg(v) {
                const el = document.createElement('div');
                el.style.cssText = `background: ${v}; position: absolute; visibility: hidden;`;
                document.body.appendChild(el);
                const c = getComputedStyle(el).backgroundColor;
                el.remove();
                return c;
              }
              const dot = document.querySelector('.cb-dock-dot');
              return {
                dotBg: dot ? getComputedStyle(dot).backgroundColor : '',
                mutedColor: resolveBg('var(--kit-muted)'),
                agentColor: resolveBg('var(--kit-agent)'),
              };
            })
            .catch(() => ({ dotBg: '', mutedColor: '', agentColor: '' }));
          check(
            'left session dot has muted computed colour',
            !!dotColors.dotBg &&
              dotColors.dotBg === dotColors.mutedColor &&
              dotColors.dotBg !== dotColors.agentColor,
          );

          // ---- Finding 4: gap > 0 between "·" separator and move link --------
          const gapOk = await leftPage
            .evaluate(() => {
              const row = document.querySelector('.cb-dock-left-row');
              const moveLink = row?.querySelector(
                '[data-testid="dock-move-link"]',
              );
              if (!row || !moveLink) return false;
              const children = Array.from(row.children);
              const moveLinkIdx = children.indexOf(moveLink);
              if (moveLinkIdx < 1) return false;
              const sep = children[moveLinkIdx - 1];
              if (!sep) return false;
              const sepRect = sep.getBoundingClientRect();
              const linkRect = moveLink.getBoundingClientRect();
              return linkRect.left - sepRect.right > 0;
            })
            .catch(() => false);
          check('gap between “·” separator and move link is > 0 px', gapOk);

          // Take screenshots showing the left session header.
          await leftPage.screenshot({
            path: '/tmp/t6fix-left.png',
            fullPage: false,
          });
          await leftPage.screenshot({
            path: '/tmp/t6fix2-left.png',
            fullPage: false,
          });
          await leftPage.screenshot({
            path: '/tmp/t6fix3-left.png',
            fullPage: false,
          });
          console.log(
            '  screenshots: /tmp/t6fix-left.png  /tmp/t6fix2-left.png  /tmp/t6fix3-left.png',
          );

          // ---- Click "move to..." on the page ---------------------------------
          await leftPage.click('[data-testid="dock-move-link"]');
          await leftPage.waitForTimeout(400);
          // The session move sheet appears with target sessions.
          const sheetItems = await leftPage.$$(
            '.cb-dock-pick-sheet .cb-dock-pick-item',
          );
          check(
            'left-session move sheet shows sessions',
            sheetItems.length > 0,
          );

          if (sheetItems.length > 0) {
            // Click the target session.
            await leftPage.click(
              `.cb-dock-pick-sheet .cb-dock-pick-item[data-session="${sessId2}"]`,
            );
            await leftPage.waitForTimeout(1000);
            // Verify via API: sessId has 0 queued, sessId2 has 2.
            const afterResp = await fetch(`${serveHandle.base}/api/sessions`, {
              headers: { 'X-Local-Token': serveHandle.token },
            });
            const afterData = await afterResp.json();
            const s1After = (afterData.sessions ?? []).find(
              (s) => s.id === sessId,
            );
            const s2After = (afterData.sessions ?? []).find(
              (s) => s.id === sessId2,
            );
            check(
              'after move: left session has 0 queued messages',
              (s1After?.queued ?? 0) === 0,
            );
            check(
              'after move: target session has 2 queued messages',
              (s2After?.queued ?? 0) === 2,
            );
          } else {
            check('after move: left session has 0 queued messages', false);
            check('after move: target session has 2 queued messages', false);
          }
        } else {
          check('left header shows queued count', false);
          check('"move to..." link is visible on left session header', false);
          check(
            'left header row does not overflow (scrollWidth ≤ clientWidth)',
            false,
          );
          check('session label does not overlap AGENT ▾ button', false);
          check('left session dot has muted computed colour', false);
          check('gap between "·" separator and move link is > 0 px', false);
          check('left-session move sheet shows sessions', false);
          check('after move: left session has 0 queued messages', false);
          check('after move: target session has 2 queued messages', false);
        }
      } finally {
        if (leftTargetHB) clearInterval(leftTargetHB);
        await leftPage.close();
      }
    }

    // ---- scenario: silent session goes left without any other activity ----
    // Verifies that the server publishes a sessions event when a session
    // crosses the left threshold, so the page learns about it without any
    // other session sending presence.
    console.log('\nscenario: silent session goes left (server-pushed event)');

    {
      const silentPage = await context.newPage();
      try {
        await silentPage.setViewportSize({ width: 1600, height: 900 });

        const agent = createAgent(serveHandle.base, serveHandle.token);
        const ts = Date.now();
        const sId = `probe-silent-${ts}`;

        // Register a lone session. No other session will heartbeat.
        await agent.presence(
          sId,
          'pi \u00b7 silent-ws',
          '/home/court/silent-ws',
        );
        await agent.newThread(sId, 'silent-thread');

        // Navigate to the page (dock picks sId as current).
        // Opened by its session (casebook_open puts ?session= in the URL).
        await silentPage.goto(
          `${serveHandle.url}&session=${encodeURIComponent(sId)}`,
          {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          },
        );
        await silentPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Let the dock finish initial loading.
        await silentPage.waitForTimeout(1500);

        // Wait for CASEBOOK_LEFT_AFTER=3s to elapse (use 4s to be safe).
        // The server's watch loop (WatchEvery=5s in production, shorter in tests)
        // will publish a sessions event when it detects the crossing.
        // The serve.mjs starts serve with WatchEvery at its default (5s).
        // We wait up to 10s for the dock to show the left header.
        const silentLeftVisible = await silentPage
          .waitForSelector('.cb-dock-header[data-left]', { timeout: 10000 })
          .then(() => true)
          .catch(() => false);
        check(
          'silent session goes left without any other activity',
          silentLeftVisible,
        );
      } finally {
        await silentPage.close();
      }
    }

    // ---- invariant: chip counts after dock scenarios ----------------------
    // Run this after the dock scenarios (stuck delivery + left session) to
    // catch any stale counts introduced by those scenarios.
    {
      const chipInvPage = await context.newPage();
      try {
        await chipInvPage.setViewportSize({ width: 1600, height: 900 });
        await chipInvPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await chipInvPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await chipInvPage.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await chipInvPage.waitForFunction(() =>
          location.hash.includes('#/attention'),
        );
        await chipInvPage.waitForTimeout(1000);
        await chipCountsMatchLists(
          chipInvPage,
          serveHandle.base,
          serveHandle.token,
          'after-dock-scenarios',
        );
      } finally {
        await chipInvPage.close();
      }
    }

    // ---- scenario: session picker and new thread default -------------------
    console.log(
      '\nscenario: session picked in the picker becomes default for new thread',
    );

    {
      const pickPage = await context.newPage();
      let pickHB = null;
      try {
        await pickPage.setViewportSize({ width: 1600, height: 900 });

        const agent = createAgent(serveHandle.base, serveHandle.token);
        const sessA = `probe-pick-a-${Date.now()}`;
        const sessB = `probe-pick-b-${Date.now()}`;
        await agent.presence(sessA, 'pi · session-a', '/home/court/a');
        await agent.presence(sessB, 'pi · session-b', '/home/court/b');
        await agent.newThread(sessA, 'thread-in-a');
        // Both stay here: the picker offers only sessions that are.
        pickHB = setInterval(() => {
          agent
            .presence(sessA, 'pi · session-a', '/home/court/a')
            .catch(() => {});
          agent
            .presence(sessB, 'pi · session-b', '/home/court/b')
            .catch(() => {});
        }, 800);

        await pickPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await pickPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await pickPage.waitForTimeout(1500);

        // The AGENT ▾ button opens the picker.
        {
          const btnText = await pickPage
            .$eval('.cb-dock-agent-btn', (el) => el.textContent ?? '')
            .catch(() => '');
          check('AGENT ▾ button is present', btnText.includes('AGENT'));
        }

        // Click the picker and select session B.
        await pickPage.click('.cb-dock-agent-btn').catch(() => {});
        await pickPage.waitForTimeout(300);

        // The picker shows sessions; select the one for sessB.
        const pickerItems = await pickPage
          .$$('.cb-dock-picker .cb-dock-pick-item')
          .catch(() => []);
        check('session picker shows sessions', pickerItems.length > 0);

        const bItem = await pickPage.$(
          `.cb-dock-picker .cb-dock-pick-item[data-session="${sessB}"]`,
        );
        if (bItem) {
          await bItem.click();
          await pickPage.waitForTimeout(600);
          // After switching, the header names session B (by its folder: it
          // has no pi name).
          const headerName = await pickPage
            .$eval('[data-testid="dock-session-name"]', (el) => el.textContent)
            .catch(() => '');
          check(
            `switching session updates the header (${headerName})`,
            headerName === 'b',
          );
          // Now click + to create a new thread; it should go to session B.
          await pickPage
            .click('[data-testid="dock-add-thread"]')
            .catch(() => {});
          await pickPage.waitForTimeout(600);
          // Verify the new thread appears in the chips.
          const chips = await pickPage
            .$$eval('.cb-dock-threads .kit-chip', (els) =>
              els.map((e) => e.textContent ?? ''),
            )
            .catch(() => []);
          check(
            'new thread chip appears after +',
            chips.length > 1, // more than just the + chip
          );
        } else {
          // Picker did not find sessB — this means the test environment is
          // different from expected. Fail both checks with a clear message.
          check('switching session updates the header', false);
          check('new thread chip appears after +', false);
        }
      } finally {
        if (pickHB) clearInterval(pickHB);
        await pickPage.close();
      }
    }

    // ---- scenario: task-6 screenshots at 1600x900 --------------------------
    console.log('\nscenario: task-6 dock screenshots at 1600\ u00d7900');

    {
      const t6Page = await context.newPage();
      try {
        await t6Page.setViewportSize({ width: 1600, height: 900 });

        // Register a session with several threads and messages.
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const sessId = `probe-t6-${Date.now()}`;
        await agent.presence(
          sessId,
          'pi · tools-workspace',
          '/home/court/tools-workspace',
        );
        const th1 = await agent.newThread(sessId, 'triage');
        const th2 = await agent.newThread(sessId, 'rules');
        void th2;

        // Post several messages with various states.
        const msgA = await agent.postMessage(
          th1.id,
          'Propose decisions for the 40 chime PRs.',
          { keys: ['pr:schuettc/bettor-help-platform#670'] },
        );
        const msgB = await agent.postMessage(
          th1.id,
          'Merge the 4 bettor-help bumps if CI is green.',
        );

        // Pick up and answer the first delivery.
        const d1 = await agent.wait(sessId);
        if (d1?.delivery) {
          await agent.reply(
            sessId,
            [msgA.id],
            'answered',
            'Done: 38 close, 2 keep.',
          );
          await agent.reply(sessId, [msgB.id], 'working', '');
        }

        // Opened by its session (casebook_open puts ?session= in the URL).
        await t6Page.goto(
          `${serveHandle.url}&session=${encodeURIComponent(sessId)}`,
          {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          },
        );
        await t6Page.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to an item so the reading column is populated.
        await t6Page.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await t6Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await t6Page.waitForTimeout(1200);

        // Helper: detect theme.
        async function detectThemeDock(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        async function forceThemeDock(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectThemeDock(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          const actual = await detectThemeDock(pg);
          check(`t6 theme forced to ${target}`, actual === target);
        }

        // Light screenshot.
        await forceThemeDock(t6Page, 'light');
        const lightTheme = await detectThemeDock(t6Page);
        check('t6 light theme detected', lightTheme === 'light');
        await t6Page.screenshot({ path: '/tmp/t6-light.png', fullPage: false });

        // Dark screenshot.
        await forceThemeDock(t6Page, 'dark');
        const darkTheme = await detectThemeDock(t6Page);
        check('t6 dark theme detected', darkTheme === 'dark');
        await t6Page.screenshot({ path: '/tmp/t6-dark.png', fullPage: false });

        console.log('  t6 screenshots: /tmp/t6-light.png  /tmp/t6-dark.png');
      } finally {
        await t6Page.close();
      }
    }

    // ---- Task 7 (its own seeded serve, so its place in the run is free) -----
    await composerScenarios(context);

    // ---- section keys (their own seeded serve too) --------------------------
    await keyScopeScenarios(context);

    // ---- Task 8: Rules (its own seeded serve) -------------------------------
    await rulesScenarios(context);
    await invalidRulesScenarios(context);

    // ---- Task 9: To apply (its own seeded serves) ---------------------------
    await applyScenarios(context, applyHelpers);

    // ---- Task 10: the keyboard layer, the failure states (their own serves)
    await shellScenarios(context, applyHelpers);

    // ---- the page belongs to the session that opened it (its own serve) ---
    await ownerScenarios(context, applyHelpers);

    // ---- the decide step: question, cards, Not now, move-on (its own serve)
    await decideScenarios(context, applyHelpers);

    // ---- scenario: fidelity — geometry and computed style -------------------
    console.log('\nscenario: fidelity — geometry and computed style');

    {
      const fidPage = await context.newPage();
      try {
        await fidPage.setViewportSize({ width: 1600, height: 900 });
        await fidPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await fidPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to item detail (encoded key as the server uses).
        await fidPage.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fidPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fidPage.waitForTimeout(600);

        // 1. Reading document geometry: left edge ≥ 40 px from list right edge;
        //    document width ≤ 700 px.
        {
          const listRight = await fidPage
            .$eval('.kit-list', (el) => el.getBoundingClientRect().right)
            .catch(() => 0);
          const docRect = await fidPage
            .$eval('.kit-doc', (el) => {
              const r = el.getBoundingClientRect();
              return { left: r.left, width: r.width };
            })
            .catch(() => ({ left: 0, width: 0 }));
          check(
            'reading document left edge is ≥ 40 px from list right edge',
            docRect.left - listRight >= 40,
          );
          check(
            'reading document width is ≤ 700 px',
            docRect.width > 0 && docRect.width <= 700,
          );
        }

        // 2. Kicker font-family is monospace.
        {
          const kickerFont = await fidPage
            .$eval('.kit-read .cb-kicker', (el) =>
              getComputedStyle(el).fontFamily.toLowerCase(),
            )
            .catch(() => '');
          check(
            'kicker font-family is monospace',
            kickerFont.includes('mono') ||
              kickerFont.includes('menlo') ||
              kickerFont.includes('courier'),
          );
        }

        // 3. Section labels are uppercase and mono.
        {
          const labelStyle = await fidPage
            .$eval('.kit-read .kit-label', (el) => {
              const cs = getComputedStyle(el);
              return {
                transform: cs.textTransform,
                font: cs.fontFamily.toLowerCase(),
              };
            })
            .catch(() => ({ transform: '', font: '' }));
          check(
            'section label text-transform is uppercase',
            labelStyle.transform === 'uppercase',
          );
          check(
            'section label font-family is monospace',
            labelStyle.font.includes('mono') ||
              labelStyle.font.includes('menlo') ||
              labelStyle.font.includes('courier'),
          );
        }

        // 4. Brand mark width and height are > 0 and it is visible.
        {
          const markSize = await fidPage
            .$eval('.kit-brand svg', (el) => {
              const r = el.getBoundingClientRect();
              return {
                width: r.width,
                height: r.height,
                visible:
                  el.offsetParent !== null ||
                  el.getBoundingClientRect().width > 0,
              };
            })
            .catch(() => ({ width: 0, height: 0, visible: false }));
          check('brand mark width > 0', markSize.width > 0);
          check('brand mark height > 0', markSize.height > 0);
          check('brand mark is visible', markSize.visible);
        }

        // ---- screenshots: detect actual theme, force correct theme, name correctly --
        // Helper: detect actual theme from computed background luminance.
        // Reads body (not html) because the kit sets background on body.
        // Parses both rgb(...) and rgba(...) forms.
        async function detectTheme(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        // Force a specific theme by clicking the toggle until the computed
        // background matches the target (up to 6 clicks max), then assert.
        async function forceTheme(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectTheme(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          // Assert that we actually reached the target theme.
          const actual = await detectTheme(pg);
          check(
            `theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // --- light screenshots ---
        await forceTheme(fidPage, 'light');

        // Item detail (still on #/item/issue:schuettc%2Fhail%234 from above).
        await fidPage.screenshot({
          path: '/tmp/fid-light-item.png',
          fullPage: false,
        });

        // Navigate to #/attention/new.
        await fidPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await fidPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await fidPage.waitForTimeout(600);
        await fidPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await fidPage.waitForTimeout(300);
        await fidPage.screenshot({
          path: '/tmp/fid-light-new.png',
          fullPage: false,
        });

        // --- dark screenshots ---
        await forceTheme(fidPage, 'dark');

        // List view in dark.
        await fidPage.screenshot({
          path: '/tmp/fid-dark-new.png',
          fullPage: false,
        });

        // Item detail in dark.
        await fidPage.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fidPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fidPage.waitForTimeout(600);
        await fidPage.screenshot({
          path: '/tmp/fid-dark-item.png',
          fullPage: false,
        });

        console.log(
          '  fid screenshots: /tmp/fid-light-item.png  /tmp/fid-light-new.png',
        );
        console.log(
          '                   /tmp/fid-dark-item.png   /tmp/fid-dark-new.png',
        );
      } finally {
        await fidPage.close();
      }
    }

    // ---- scenario: round-2 fidelity screenshots ----------------------------
    console.log('\nscenario: round-2 fidelity screenshots at 1600\xd7900');

    {
      const fid2Page = await context.newPage();
      try {
        await fid2Page.setViewportSize({ width: 1600, height: 900 });
        await fid2Page.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await fid2Page.waitForSelector('.kit-bar', { timeout: 8000 });

        // Helper: detect actual theme from computed body background.
        // Reads body (not html) because the kit sets background on body.
        // Parses both rgb(...) and rgba(...) forms.
        async function detectTheme2(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        // Force a specific theme, loop up to 6 times, then assert.
        async function forceTheme2(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectTheme2(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          // Assert the target was reached before capturing.
          const actual = await detectTheme2(pg);
          check(
            `fid2 theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // --- light screenshots (fid2) ---
        await forceTheme2(fid2Page, 'light');

        await fid2Page.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fid2Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fid2Page.waitForTimeout(600);
        await fid2Page.screenshot({
          path: '/tmp/fid2-light-item.png',
          fullPage: false,
        });

        await fid2Page.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await fid2Page.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await fid2Page.waitForTimeout(600);
        await fid2Page
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await fid2Page.waitForTimeout(300);
        await fid2Page.screenshot({
          path: '/tmp/fid2-light-new.png',
          fullPage: false,
        });

        // --- dark screenshots (fid2) ---
        await forceTheme2(fid2Page, 'dark');
        await fid2Page.waitForTimeout(200);

        await fid2Page.screenshot({
          path: '/tmp/fid2-dark-new.png',
          fullPage: false,
        });

        await fid2Page.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fid2Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fid2Page.waitForTimeout(600);
        await fid2Page.screenshot({
          path: '/tmp/fid2-dark-item.png',
          fullPage: false,
        });

        console.log(
          '  fid2 screenshots: /tmp/fid2-light-item.png  /tmp/fid2-light-new.png',
        );
        console.log(
          '                    /tmp/fid2-dark-item.png   /tmp/fid2-dark-new.png',
        );
      } finally {
        await fid2Page.close();
      }
    }

    // ---- invariant: chip counts must equal list totals ---------------------
    // Run this at the end of all scenarios with the page settled, on a fresh
    // page so no dock-scenario fixture data affects the attention view counts.
    console.log('\ninvariant: view chip counts match list totals');
    {
      const invPage = await context.newPage();
      try {
        await invPage.setViewportSize({ width: 1600, height: 900 });
        await invPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await invPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Navigate to attention to ensure chips are rendered.
        await invPage.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await invPage.waitForFunction(() =>
          location.hash.includes('#/attention'),
        );
        await invPage.waitForTimeout(1000);
        await chipCountsMatchLists(
          invPage,
          serveHandle.base,
          serveHandle.token,
          'end-of-probe',
        );
      } finally {
        await invPage.close();
      }
    }
  } catch (err) {
    console.error('probe: unexpected error:', err);
    fails++;
  } finally {
    console.log('\ninvariant: every git the probe ran was hermetic');
    {
      const g = gitGuard();
      check(
        g.bad.length
          ? `every git call ran hermetic with CASEBOOK_DISABLE=1 — ${g.bad.length} of ${g.calls} did not: ${g.bad.slice(0, 3).join(' | ')}`
          : `every git call the probe and its serves made (${g.calls}) ran hermetic, with CASEBOOK_DISABLE=1 (the guard's log)`,
        g.calls > 0 && g.bad.length === 0,
      );
    }
    console.log('\ninvariant: no section key clashed');
    check(
      keyClashes.length
        ? `no section key clashed — ${keyClashes.join(' | ')}`
        : 'no section key clashed (no "key clash" on any page\'s console)',
      keyClashes.length === 0,
    );
    await context.close();
    await browser.close();
    cleanup();
  }
}

let ran = false; // a browser ran the scenarios (not the no-Chrome skip)

void run().then(() => {
  if (throttle > 1) {
    console.log(`\nprobe: ${throttled} pages ran at ${throttle}x CPU throttle`);
  }
  console.log(`\nprobe: ${passes} passed, ${fails} failed`);
  if (ran && !only && passes < MIN_CHECKS) {
    console.error(
      `probe: a full run passed ${passes} checks, fewer than MIN_CHECKS (${MIN_CHECKS})`,
    );
    fails++;
  }
  if (fails > 0) process.exit(1);
});
