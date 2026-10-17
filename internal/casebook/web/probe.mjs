// probe.mjs — the build-time browser sanity check for the casebook bundle.
//
// Spawns a real seeded casebook serve (via serve.mjs), opens its page in
// headless Chrome (playwright-core), and checks the four shell invariants:
//
//   1. two places with counts               (decide / apply; no rules, no attention)
//   2. theme toggle works                   (html[data-theme] flips)
//   3. routing switches places              (#/rules/<id> → decide?rule=<id>, #/apply, back)
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
import { loadScenarios, stopLoadServes } from './probe-load.mjs';
import { filtersScenarios, stopFiltersServes } from './probe-filters.mjs';

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
  stopLoadServes();
  stopFiltersServes();
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

// settled says when a wait for the page to match serve ran out (the checks
// after it say what is wrong; this says where the time went).
function settled(ok, what) {
  if (!ok) console.log(`  (waited for ${what} to match serve's; it never did)`);
  return ok;
}

// decideCountMatches checks that the bar's decide count is serve's total of
// everything to decide (GET /api/items?view=all), once the page settles.
async function decideCountMatches(pg, label) {
  const { base, token } = _serveHandle;
  let got = '';
  let want = '';
  const ok = await eventually(async () => {
    const r = await fetch(`${base}/api/items?view=all&limit=1`, {
      headers: { 'X-Local-Token': token },
    }).catch(() => null);
    if (!r?.ok) return false;
    want = String((await r.json()).total ?? 0);
    got = await pg
      .$eval('.kit-ctl[data-id="decide"] .kit-n', (e) => e.textContent ?? '')
      .catch(() => '');
    return got === want;
  });
  check(`${label}: decide counts serve's total (${got}, serve ${want})`, ok);
}

// listShows waits until the page's shown list holds exactly the items serve
// lists for decide (view=all; q narrows it as the search does): the page has
// loaded it.
async function listShows(pg, q = '', timeout = 6000) {
  const { base, token } = _serveHandle;
  const qs = q ? `&q=${encodeURIComponent(q)}` : '';
  return eventually(async () => {
    const r = await fetch(`${base}/api/items?view=all&limit=200${qs}`, {
      headers: { 'X-Local-Token': token },
    }).catch(() => null);
    if (!r?.ok) return false;
    // A row names its item in its kicker: "<kind> · <key without kind>".
    // The items left open are a group after the rest, collapsed: its
    // header counts them.
    const v = await r.json();
    const leftN = await pg
      .$eval(
        '.kit-app > .kit-list:not([hidden]) .cb-group[data-group="left-open"] .cb-group-n',
        (e) => Number(e.textContent ?? ''),
      )
      .catch(() => 0);
    if (leftN !== (v.left_open_total ?? 0)) return false;
    const want = (v.items ?? [])
      .map((i) =>
        i.kind
          ? `${i.kind} \u00b7 ${i.key.slice(i.key.indexOf(':') + 1)}`
          : i.key,
      )
      .sort();
    const got = (
      await pg
        .$$eval(
          '.kit-app > .kit-list:not([hidden]) .kit-row:not(.cb-left-open) .kit-kicker',
          (els) => els.map((e) => e.textContent ?? ''),
        )
        .catch(() => [])
    ).sort();
    return want.length === got.length && want.every((k, i) => k === got[i]);
  }, timeout).then((ok) => settled(ok, 'the list'));
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
// decide items no other scenario touches. Human authors put them in new,
// like the fixture's hail items.
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
    // The seeded repos' own items are new too, and a search by a repo's
    // name finds them: they stop being tracked, so those lists hold the
    // seeded pull requests and issues only.
    await createAgent(serveHandle.base, serveHandle.token).api(
      'POST',
      '/api/decide',
      {
        keys: T7_SEED.map((r) => `repo:${r.repo}`),
        disposition: 'ignore',
      },
    );
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
      // Kept: the check is that the send's end does not touch the field; a
      // short window after the delivery for a late write to show.
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
      hash: '#/attention/new',
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
        `${serveHandle.base}/api/items?view=new&q=t7-attached&limit=200`,
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
      // Kept: the check is that the edit does not change with the
      // selection; a window for a redraw that would undo it.
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
      // The rows' nth, not the list's: group headers sit between them.
      await pg.locator('.kit-row').nth(pick[0]).locator('.kit-title').click();
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
  console.log('\nscenario: the rail at 1600×900, light and dark');
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
      hash: '#/attention/new',
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
          `the rail in ${theme} is ${theme} (body ${bg})`,
          bg === bgs[theme],
        );
      }
    } finally {
      stopPresent();
      await pg.close();
    }
  }

  // ---- scenario: only the active section sets the attached context --------
  // Last: it decides one of its seeded items, which leaves a decision queued
  // for sync (the bar's status), and the rail scenario comes before it.
  console.log(
    '\nscenario: a hidden Attention does not change the attached line',
  );
  hiddenScenario: {
    // Its own items: T7_SEED's schuettc/t7-hidden (2 PRs); one is decided.
    const fx = await dockFixture(serveHandle, 'hidden');
    const pg = await openDock(context, serveHandle, fx, {
      search: '&q=t7-hidden',
      hash: '#/attention/new',
    });
    const attached = () =>
      pg.$eval('[data-testid="composer-attached"]', (e) => e.textContent);
    try {
      const resp = await fetch(
        `${serveHandle.base}/api/items?view=new&q=t7-hidden&limit=200`,
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

      // The other place is shown: apply with nothing open attaches nothing.
      await pg.evaluate(() => {
        location.hash = '#/apply';
      });
      check(
        'showing apply with nothing open sets its context ("nothing")',
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
      check("and the bar's primary stays apply's (none)", primaryHidden);

      // Back to Attention: it re-feeds what is still selected.
      await pg.evaluate(() => {
        location.hash = '#/decide';
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
    } finally {
      await pg.close();
    }
  }
}

// ---- section keys: decide's a / r / d act only while decide is shown ------
//
// Its own serve (KEYS_SEED), so what a failing key does (accept a proposal,
// open a sheet) touches nothing another scenario reads. decide keeps its
// open item and its selection while hidden; on #/apply neither a, r nor d may
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
    check('decide shows the open item with its pending proposal', opened);

    // Show apply: decide's open item stays, hidden.
    await pg.evaluate(() => {
      location.hash = '#/apply';
    });
    await until(
      pg,
      () =>
        document
          .querySelector('.kit-ctl[data-id="apply"]')
          ?.classList.contains('on') ?? false,
    );

    // r first: it only opens a sheet, while a failing a settles the
    // proposal r would act on.
    await pressWithFocus(pg, 'r');
    // Kept (this one and a's): each check is that the key does nothing
    // on #/apply; a window long enough for a sheet or a POST to land.
    await pg.waitForTimeout(500);
    const rSheet = await sheetOpen(pg);
    check(
      'on #/apply, "r" does not open the reject sheet for the hidden item',
      !rSheet,
    );
    if (rSheet) {
      await pg.keyboard.press('Escape');
      await pg.waitForTimeout(300); // only after a failed check
    }

    // A wrong accept is a POST after ordering, not synchronous: give it the
    // full window.
    await pressWithFocus(pg, 'a');
    await pg.waitForTimeout(1500);
    check(
      'on #/apply, "a" does not accept decide\'s hidden open proposal',
      (await proposalState()) === 'pending',
    );

    // Back on decide the same keys work again.
    await pg.evaluate((k) => {
      location.hash = '#/item/' + encodeURIComponent(k);
    }, key);
    await until(
      pg,
      () => !!document.querySelector('.kit-read .cb-proposal-card'),
      undefined,
      8000,
    );
    await pressWithFocus(pg, 'a');
    check(
      'back on decide, "a" accepts the open item\'s proposal',
      await eventually(async () => (await proposalState()) === 'accepted'),
    );

    // The ? overlay lists what is bound: each place's own keys only while
    // it is shown ("/" is decide's search; "a" is decide's accept).
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
    const onDecide = await overlay();
    check(
      `on decide the overlay lists its keys ("search", "accept proposal"), not d's "decide selection" (${onDecide.join(', ')})`,
      onDecide.includes('search') &&
        onDecide.includes('accept proposal') &&
        !onDecide.includes('decide selection'),
    );
    await pg.evaluate(() => {
      location.hash = '#/apply';
    });
    await until(
      pg,
      () =>
        document
          .querySelector('.kit-ctl[data-id="apply"]')
          ?.classList.contains('on') ?? false,
    );
    const onApply = await overlay();
    check(
      `on apply the overlay lists neither decide's "search" nor "accept proposal" (${onApply.join(', ')})`,
      !onApply.includes('search') && !onApply.includes('accept proposal'),
    );
    await pressWithFocus(pg, '/');
    check(
      'on apply, "/" does not reach decide\'s hidden search',
      !(await pg.evaluate(() =>
        document.activeElement?.classList.contains('kit-search'),
      )),
    );
    await pg.evaluate(() => {
      location.hash = '#/decide';
    });
    await until(
      pg,
      () =>
        document
          .querySelector('.kit-ctl[data-id="decide"]')
          ?.classList.contains('on') ?? false,
    );
    await pressWithFocus(pg, '/');
    check(
      'back on decide, "/" focuses its search',
      await until(pg, () =>
        document.activeElement?.classList.contains('kit-search'),
      ),
    );
  } finally {
    await pg.close();
  }
}

// ---- rules as groups on decide ----------------------------------------------
//
// The rule-group scenarios start their own serve. Its local branches come
// from a seeded machine snapshot (serve.mjs seedMachines): machine probe-r8's
// clones under /home/probe-r8, each branch with the landed verdict a real
// sync would have written. Its rules are saved through serve's own route
// (POST /api/rules/save: saving turns a rule on).

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
  ],
};
// Court's rule: every landed branch of r8-landed and r8-other, undecided.
// 6 match: 4 in main (a1 a2 a3 o1), 2 via merged PR (m1 m2).
const R8_LANDED = {
  id: 'r8-landed',
  name: 'Landed branches',
  match: [
    { field: 'kind', op: 'is', value: 'branch' },
    { field: 'repo', op: 'in', value: 'schuettc/r8-landed,schuettc/r8-other' },
    { field: 'landed', op: 'is', value: 'all-machines' },
    { field: 'landed-how', op: 'in', value: 'default-branch,merged-pr' },
  ],
  propose: { disposition: 'delete', note: 'landed ({how})' },
};
// A second rule, turned off: "rules N" lists it.
const R8_AGENT = {
  id: 'r8-agent',
  name: 'Merged agent branches',
  match: [
    { field: 'kind', op: 'is', value: 'branch' },
    { field: 'repo', op: 'is', value: 'schuettc/r8-agent' },
  ],
  propose: { disposition: 'delete' },
};

// LIST and READ are decide's list and its middle pane.
const LIST = '.kit-app > .kit-list:not([hidden])';
const READ = '.kit-app > .kit-read:not([hidden])';

// attentionWords is every visible text in the bar, the list and the middle
// pane that says "attention" (none should: decide and apply are the two
// places).
async function attentionWords(pg) {
  return pg.evaluate(() => {
    const out = [];
    for (const sel of [
      '.kit-bar',
      '.kit-app > .kit-list:not([hidden])',
      '.kit-app > .kit-read:not([hidden])',
    ]) {
      const root = document.querySelector(sel);
      if (!root) continue;
      const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let n = w.nextNode(); n; n = w.nextNode()) {
        const el = n.parentElement;
        if (!el || !/attention/i.test(n.textContent ?? '')) continue;
        const r = el.getBoundingClientRect();
        if (r.width && r.height) out.push(n.textContent.trim());
      }
    }
    return out;
  });
}

let _rulesServe = null;
async function ruleGroupsScenarios(context) {
  const serveHandle = await startServe({ seedMachines: [R8_MACHINE] });
  _rulesServe = serveHandle;
  try {
    await ruleGroupsScenariosOn(context, serveHandle);
  } finally {
    serveHandle.stop();
    _rulesServe = null;
  }
}

async function ruleGroupsScenariosOn(context, serveHandle) {
  console.log('\nscenario: rules — a rule is the header of its group');
  const agent = createAgent(serveHandle.base, serveHandle.token);
  await agent.api('POST', '/api/rules/save?create=1', R8_LANDED);
  await agent.api('POST', '/api/rules/save?create=1', R8_AGENT);
  await agent.api('POST', '/api/rules/deactivate', { id: 'r8-agent' });
  const pending = async (id) =>
    (await agent.api('GET', '/api/items?view=all&limit=500')).items.filter(
      (it) =>
        it.proposal?.state === 'pending' && it.proposal.source === `rule:${id}`,
    ).length;
  check(
    'the saved rule recommends its 6 matches; the one turned off none',
    (await pending('r8-landed')) === 6 && (await pending('r8-agent')) === 0,
  );

  const pg = await context.newPage();
  const errors = [];
  pg.on('pageerror', (e) => errors.push(String(e)));
  try {
    await pg.setViewportSize({ width: 1440, height: 900 });
    await pg.goto(serveHandle.url + '#/decide', {
      waitUntil: 'domcontentloaded',
    });
    const HEAD = `${LIST} .cb-group[data-rule="r8-landed"]`;
    const shown = await until(
      pg,
      (sel) =>
        !!document.querySelector(`${sel} .cb-rule-more`) &&
        !!document.querySelector(`${sel} .cb-group-acts .fill`),
      HEAD,
      8000,
    );
    const head = shown
      ? await pg.$eval(HEAD, (e) => ({
          name: e.querySelector('.cb-group-label')?.textContent,
          tag: e.querySelector('.cb-group-tag')?.textContent,
          decision: e.querySelector('.cb-group-decision')?.textContent,
          n: e.querySelector('.cb-group-n')?.textContent,
          more: e.querySelector('.cb-rule-more')?.textContent,
          button: e.querySelector('.cb-group-acts .fill')?.textContent,
        }))
      : {};
    checkList(
      'the header reads the rule: name, "rule", its decision, its count and ⋯',
      [head.name, head.tag, head.decision, head.n, head.more],
      ['Landed branches', 'rule', 'Delete it', '6', '\u22ef'],
    );
    check(
      `its button names the decision for all of them: "Delete all 6" (${head.button})`,
      head.button === 'Delete all 6',
    );
    checkList('nothing visible says "attention"', await attentionWords(pg), []);
    // The starter rules (store/tidy.go seeds 3) and these 2.
    check(
      'the rule row under the filters: "+ rule" and "rules 5" (the one turned off too)',
      (await pg.$eval(`${LIST} .cb-rule-row`, (e) => e.textContent)) ===
        '+ rulerules 5',
    );

    // ⋯ edit: the editor in the middle pane, the list its preview.
    await pg.click(`${HEAD} .cb-rule-more`);
    await pg.click(`${HEAD} .cb-filter-menu-item[data-act="edit"]`);
    check(
      'edit opens the rule editor beside the list, and the list previews it',
      await until(
        pg,
        () =>
          !!document.querySelector(
            '.kit-app > .kit-read:not([hidden]) [data-testid="rule-editor"]',
          ) &&
          !!document.querySelector('.kit-list.cb-decide-list.cb-previewing') &&
          /previewing a draft rule · 6 items/.test(
            document.querySelector('.cb-preview-line')?.textContent ?? '',
          ),
      ),
    );
    await pg.click(`${READ} .cb-filter-cancel`);
    await until(
      pg,
      () => !document.querySelector('.kit-list.cb-decide-list.cb-previewing'),
    );

    // ⋯ turn off: its recommendations are withdrawn and the group leaves
    // the list, live.
    const reloads = [];
    pg.on('framenavigated', (f) => {
      if (f === pg.mainFrame()) reloads.push(f.url());
    });
    await pg.click(`${HEAD} .cb-rule-more`);
    await pg.click(`${HEAD} .cb-filter-menu-item[data-act="turn off"]`);
    check(
      'turn off removes the group, live (no reload)',
      (await until(pg, (sel) => !document.querySelector(sel), HEAD)) &&
        reloads.length === 0,
    );
    check(
      'and serve withdrew its recommendations',
      await eventually(async () => (await pending('r8-landed')) === 0),
    );

    // "rules N": every rule, off ones too, in the middle pane.
    await pg.click(`${LIST} [data-testid="rules-n"]`);
    await until(
      pg,
      () =>
        document.querySelectorAll(
          '.kit-app > .kit-read:not([hidden]) .cb-rules-row',
        ).length === 5,
    );
    checkList(
      "rules N lists both rules (serve's order, by id), each off now, with its decision",
      await pg.$$eval(`${READ} .cb-rules-row[data-id^="r8-"]`, (els) =>
        els.map(
          (e) =>
            `${e.querySelector('.cb-rules-row-name').textContent} | ${e.querySelector('.cb-rules-row-state').textContent} | ${e.querySelector('.cb-rules-row-d').textContent}`,
        ),
      ),
      [
        'Merged agent branches | off | Delete it',
        'Landed branches | off | Delete it',
      ],
    );
    // Turn it on again from the list: the group comes back.
    await pg.click(`${READ} .cb-rules-row[data-id="r8-landed"] .cb-rule-more`);
    await pg.click(
      `${READ} .cb-rules-row[data-id="r8-landed"] .cb-filter-menu-item[data-act="turn on"]`,
    );
    check(
      'turn on from the list brings its group back, 6 items',
      await until(
        pg,
        (sel) =>
          document.querySelector(`${sel} .cb-group-n`)?.textContent === '6',
        HEAD,
        8000,
      ),
    );
    // A row opens the rule, and the list narrows to it.
    await pg.click(
      `${READ} .cb-rules-row[data-id="r8-landed"] .cb-rules-row-open`,
    );
    check(
      'a row opens the rule in the middle pane and narrows the list (rule=r8-landed)',
      await until(
        pg,
        () =>
          location.hash === '#/decide?rule=r8-landed' &&
          document.querySelector('.kit-app > .kit-read:not([hidden]) .cb-rule')
            ?.dataset.rule === 'r8-landed' &&
          document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
          ).length === 6,
        undefined,
        8000,
      ),
    );
    check(
      'the rule reads on, with its record',
      (await pg.$eval(`${READ} .cb-rule .kit-kick`, (e) => e.textContent)) ===
        'rule · on',
    );

    // "Delete all 6" decides the 6, and they leave the list.
    await pg.goto(serveHandle.url + '#/decide', {
      waitUntil: 'domcontentloaded',
    });
    await until(pg, (sel) => !!document.querySelector(sel), `${HEAD} .fill`);
    const applyBefore = await pg.$eval(
      '.kit-ctl[data-id="apply"] .kit-n',
      (e) => Number(e.textContent),
    );
    await pg.click(`${HEAD} .cb-group-acts .fill`);
    check(
      'Delete all 6 decides them: the group leaves the list',
      await until(pg, (sel) => !document.querySelector(sel), HEAD, 8000),
    );
    check(
      'and apply counts 6 more',
      await until(
        pg,
        (n) =>
          Number(
            document.querySelector('.kit-ctl[data-id="apply"] .kit-n')
              ?.textContent,
          ) === n,
        applyBefore + 6,
        8000,
      ),
    );
    check('no page errors', errors.length === 0);
    if (errors.length) console.error(errors);
  } catch (err) {
    check(`rule groups: the scenario ran to its end — ${err.message}`, false);
  } finally {
    await pg.close();
  }
}

// ---- invalid rules ----------------------------------------------------------
//
// Its own serve, whose data repo holds rule files as a person (or an older
// casebook) wrote them: an active rule with a bool value serve refuses, and
// a file that isn't a rule.
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
  'rules/r8-broken.toml': 'this is [not a rule\n',
};

let _invalidServe = null;
async function invalidRuleScenarios(context) {
  const serveHandle = await startServe({ seedFiles: INVALID_SEED });
  _invalidServe = serveHandle;
  try {
    await invalidRuleScenariosOn(context, serveHandle);
  } finally {
    serveHandle.stop();
    _invalidServe = null;
  }
}

async function invalidRuleScenariosOn(context, serveHandle) {
  console.log('\nscenario: rules — an invalid rule says so and opens to fix');
  const agent = createAgent(serveHandle.base, serveHandle.token);
  const bad = await agent.api('GET', '/api/rule?id=r8-bad');
  check(
    `serve reads r8-bad as invalid, naming its second condition (${bad.invalid})`,
    bad.invalid.startsWith('condition 2: '),
  );
  const pg = await context.newPage();
  try {
    await pg.setViewportSize({ width: 1440, height: 900 });
    await pg.goto(serveHandle.url + '#/decide', {
      waitUntil: 'domcontentloaded',
    });
    await pg.click(`${LIST} [data-testid="rules-n"]`);
    await until(
      pg,
      () =>
        !!document.querySelector(
          '.kit-app > .kit-read:not([hidden]) .cb-rules-row[data-id="r8-bad"]',
        ),
    );
    const rows = await pg.$$eval(`${READ} .cb-rules-row`, (els) =>
      els.map(
        (e) =>
          `${e.dataset.id} | ${e.querySelector('.cb-rules-row-state').textContent}`,
      ),
    );
    check(
      `rules N says "not valid" for r8-bad (${rows.join('; ')})`,
      rows.includes('r8-bad | not valid'),
    );
    await pg.click(`${READ} .cb-rules-row[data-id="r8-bad"] .cb-rule-more`);
    await pg.click(
      `${READ} .cb-rules-row[data-id="r8-bad"] .cb-filter-menu-item[data-act="edit"]`,
    );
    const shownMsg = await until(
      pg,
      (msg) =>
        [
          ...document.querySelectorAll(
            '.kit-app > .kit-read:not([hidden]) [data-testid="rule-editor"] *',
          ),
        ].some((e) => !e.hidden && e.textContent === msg),
      bad.invalid,
    );
    check(
      `it opens in the editor with serve's message ("${bad.invalid}")`,
      shownMsg,
    );
  } catch (err) {
    check(`invalid rules: the scenario ran to its end — ${err.message}`, false);
  } finally {
    await pg.close();
  }
}

const applyHelpers = { check, checkList, until, eventually };

// ---- the scenario list ------------------------------------------------------
//
// run() below is the scenario list. A full run (no PROBE_ONLY) must pass at
// least MIN_CHECKS checks: a scenario that stops early, or is skipped, can't
// leave the probe green. Raise it whenever checks are added.
const MIN_CHECKS = 856;

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
  'load',
  'filters',
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
    // PROBE_ONLY=rules runs only the rule-group scenarios (their own serve).
    if (process.env.PROBE_ONLY === 'rules') {
      await ruleGroupsScenarios(context);
      await invalidRuleScenarios(context);
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
    // PROBE_ONLY=load runs only the page-under-load scenario
    // (probe-load.mjs, its own serve).
    if (process.env.PROBE_ONLY === 'load') {
      await loadScenarios(context, applyHelpers);
      return;
    }
    // PROBE_ONLY=filters runs only the saved filters' scenarios
    // (probe-filters.mjs, their own serves).
    if (process.env.PROBE_ONLY === 'filters') {
      await filtersScenarios(context, applyHelpers);
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
    // (decide's and apply's.)
    await page
      .waitForFunction(
        () =>
          !!document.querySelector('.kit-ctl[data-id="decide"] .kit-n') &&
          !!document.querySelector('.kit-ctl[data-id="apply"] .kit-n'),
        undefined,
        { timeout: 5000 },
      )
      .catch(() => {
        // counts may all be 0; kit renders a .kit-n span for 0 too
      });

    console.log('\nscenario: the shell boots');

    // 1. Two places, decide and apply, with counts; no rules control.
    {
      const controls = await page.$$('.kit-ctl[data-id]');
      const ids = await Promise.all(
        controls.map((c) => c.getAttribute('data-id')),
      );
      check('decide control present', ids.includes('decide'));
      check('apply control present', ids.includes('apply'));
      check(
        'no rules control, no attention control',
        !ids.includes('rules') && !ids.includes('attention'),
      );
      checkList(
        'the bar names the two places: decide and apply',
        await page.$$eval('.kit-ctl[data-id]', (els) =>
          els.map((e) => e.firstChild?.textContent?.trim() ?? ''),
        ),
        ['decide', 'apply'],
      );

      // Each section control must show a number (even "0").
      let allHaveCount = true;
      for (const ctrl of controls) {
        const id = await ctrl.getAttribute('data-id');
        if (!id || !['decide', 'apply'].includes(id)) continue;
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

    // 3. Routing: an old #/rules/<id> lands on decide, narrowed to the
    // rule; #/apply and #/decide switch places.
    {
      const on = (id) =>
        page.$eval(
          `.kit-ctl[data-id="${id}"]`,
          (el) =>
            el.classList.contains('on') ||
            el.getAttribute('aria-current') === 'page',
        );
      await page.evaluate(() => {
        location.hash = '#/rules/test';
      });
      check(
        '#/rules/test lands on decide with rule=test',
        (await until(page, () => location.hash === '#/decide?rule=test')) &&
          (await on('decide')),
      );
      await page.evaluate(() => {
        location.hash = '#/apply';
      });
      await page.waitForFunction(() => location.hash === '#/apply');
      check('routing to #/apply activates apply', await on('apply'));
      await page.evaluate(() => {
        location.hash = '#/decide';
      });
      await page.waitForFunction(() => location.hash === '#/decide');
      check('routing back to #/decide activates decide', await on('decide'));
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
    // ---- scenario: decide's list and detail ---------------------------------
    console.log("\nscenario: decide's list and detail");

    // Navigate to decide.
    await page.evaluate(() => {
      location.hash = '#/decide';
    });
    await page.waitForFunction(() => location.hash === '#/decide');

    // Wait for the attention list panel.
    await page.waitForSelector('.kit-list', { timeout: 5000 }).catch(() => {});

    // Wait for rows to appear (the API call may take a moment).
    await page.waitForSelector('.kit-row', { timeout: 8000 }).catch(() => {});

    // Check: seeded items appear in the list.
    {
      const rowCount = await page.$$eval('.kit-row', (rows) => rows.length);
      check("seeded items appear in decide's list", rowCount > 0);
    }

    // Check: decide is one list: no view chips.
    {
      await listShows(page);
      const views = await page.$$eval(
        '.kit-app > .kit-list:not([hidden]) .kit-chips[data-group="view"] .kit-chip',
        (els) => els.length,
      );
      check(`decide has no view chips (${views})`, views === 0);
      checkList(
        'nothing visible says "attention"',
        await attentionWords(page),
        [],
      );
    }

    // ---- scenario: search decide's list (kit v0.11.0) ----------------------
    console.log("\nscenario: search decide's list (kit v0.11.0)");

    {
      // Navigate to the new view and count the full row set.
      await page.evaluate(() => {
        location.hash = '#/decide';
      });
      await page.waitForFunction(() => location.hash === '#/decide');
      await listShows(page);

      const fullCount = await page.$$eval('.kit-row', (rows) => rows.length);

      // 1. Typing narrows the list to the matching fixture item (after debounce).
      {
        const searchField = await page.$('.kit-search');
        if (searchField) {
          await searchField.click();
          await searchField.fill('nudge');
          // Wait for 200ms debounce + network round-trip.
          await listShows(page, 'nudge');
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
          await listShows(page);
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
        await until(
          page,
          () =>
            document.activeElement === document.querySelector('.kit-search'),
        );
        const focused = await page
          .$eval('.kit-search', (el) => document.activeElement === el)
          .catch(() => false);
        check('/ focuses the search field', focused);
        // Press Esc to leave the field cleanly.
        await page.keyboard.press('Escape');
        await until(
          page,
          () =>
            document.activeElement !== document.querySelector('.kit-search'),
        );
      }

      // 4. Reload with search in URL restores the narrowed list and field text.
      {
        const reloadPage = await context.newPage();
        try {
          // Navigate to /?q=nudge#/decide. The context's cookie provides
          // auth; no ?t= needed.
          const baseUrl = serveHandle.base.replace(/\/$/, '');
          await reloadPage.goto(baseUrl + '/?q=nudge#/decide', {
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
    console.log("\nscenario: decide's list and detail (continued)");

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
      // summary must be visible even when the show-more button hides.
      await page.evaluate(() => {
        location.hash = '#/decide';
      });
      await page.waitForFunction(() => location.hash === '#/decide');
      await listShows(page);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});

      // cb-foot must not be hidden (the hidden attribute sets display:none).
      const footNotHidden = await page
        .$eval('.cb-foot', (el) => !el.hidden)
        .catch(() => false);
      check('cb-foot is not hidden when view fits one page', footNotHidden);
    }

    // Two of the 'new' view's items are decided here, as the bulk decide
    // that stood here did (ticked items now act through their group), so
    // the scenarios after it see the casebook they always saw.
    {
      const r = await fetch(`${serveHandle.base}/api/items?view=new&limit=2`, {
        headers: { 'X-Local-Token': serveHandle.token },
      });
      const keys = ((await r.json()).items ?? []).map((it) => it.key);
      const d = await fetch(`${serveHandle.base}/api/decide`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Local-Token': serveHandle.token,
        },
        body: JSON.stringify({ keys, disposition: 'keep' }),
      });
      check(
        `serve decides the two items the old bulk decide took (${keys.join(', ')}: ${d.status})`,
        d.ok && keys.length === 2,
      );
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
          location.hash = '#/decide';
        });
        await detailPage.waitForFunction(() => location.hash === '#/decide');
        await listShows(detailPage);
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
    // ---- scenario: no board ------------------------------------------------
    // The board is gone: no .cb-board, and an old link to it
    // (#/attention/board) lands on decide's one list.
    console.log('\nscenario: no board');

    {
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/decide');
      const landed = await listShows(page);
      const geo = await page.evaluate(() => {
        const l = document.querySelector('.kit-app > .kit-list:not([hidden])');
        const r = l?.getBoundingClientRect();
        const row = l?.querySelector('.kit-row');
        const rr = row?.getBoundingClientRect();
        return {
          listW: r?.width ?? 0,
          rowH: rr?.height ?? 0,
          rowShown: !!row && getComputedStyle(row).visibility === 'visible',
          board: !!document.querySelector('.cb-board'),
          on: !!document.querySelector('.kit-ctl[data-id="decide"].on'),
        };
      });
      check(
        `no board: #/attention/board lands on #/decide's list (list ${Math.round(geo.listW)}px, row ${Math.round(geo.rowH)}px)`,
        landed &&
          geo.on &&
          geo.listW >= 280 &&
          geo.rowH > 0 &&
          geo.rowShown &&
          !geo.board,
      );
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
        await until(
          encPage,
          () =>
            (document.querySelector('.kit-read .cb-title')?.textContent ?? '')
              .length > 0,
        );

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

        // Navigate to the new view (no item open).
        await emptyReadPage.evaluate(() => {
          location.hash = '#/decide';
        });
        await emptyReadPage.waitForFunction(() => location.hash === '#/decide');
        await emptyReadPage
          .waitForSelector('.kit-row', { timeout: 8000 })
          .catch(() => {});
        await until(
          emptyReadPage,
          () =>
            (
              document.querySelector('.kit-read .cb-read-empty-prompt')
                ?.textContent ?? ''
            ).length > 0,
        );

        // The reading column must have the empty-state element (not be blank).
        const hasEmptyState = await emptyReadPage
          .$eval('.kit-read .cb-read-empty', (el) => el.offsetParent !== null)
          .catch(() => false);
        check(
          'empty reading column shows .cb-read-empty instead of blank',
          hasEmptyState,
        );

        // The empty state is a summary: how many to decide, no section
        // name.
        const head = await emptyReadPage
          .$eval(
            '.kit-read:not([hidden]) .cb-read-empty-count',
            (el) => el.textContent ?? '',
          )
          .catch(() => '');
        check(
          `empty state is the summary ("${head}"), with no section name`,
          /^\d+ to decide$/.test(head) &&
            !(await emptyReadPage.$('.kit-read .cb-read-empty-section')),
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

    // ---- scenario: a proposal shows in the list live ------------------------
    // Post a fake-agent proposal via /api/agent/propose. The server emits a
    // "proposals" live event WITHOUT a follow-up index rebuild, so the page
    // must hear it and list the item in the agent's group.
    console.log(
      "\nscenario: fake-agent proposal shows in the list's groups live",
    );

    {
      const propCountPage = await context.newPage();
      try {
        await propCountPage.goto(serveHandle.url + '#/decide', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await propCountPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await listShows(propCountPage);

        // Use pr:schuettc/hail#3. POST to /api/agent/propose — this does NOT
        // trigger an index rebuild, so only the "proposals" live event fires.
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

        check(
          "fake-agent proposal shows live: the item is in the agent's group",
          await until(
            propCountPage,
            (key) =>
              [
                ...document.querySelectorAll(
                  '.kit-app > .kit-list:not([hidden]) .cb-group[data-group^="pi:probe-session-1|keep"]',
                ),
              ].length === 1 &&
              [
                ...document.querySelectorAll(
                  '.kit-app > .kit-list:not([hidden]) .kit-row .kit-kicker',
                ),
              ].some((e) => e.textContent === key),
            'pr \u00b7 schuettc/hail#3',
            8000,
          ),
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
        await until(
          propGeoPage,
          () => !!document.querySelector('.kit-read .cb-proposal-card'),
        );

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
          // Card head: "‹session› recommends" (serve's words), then the
          // choice's label, not "pending proposal".
          const cardHead = await propGeoPage
            .$eval(
              '.cb-proposal-card .kit-card-head',
              (el) => el.textContent ?? '',
            )
            .catch(() => '');
          const cardLabel = await propGeoPage
            .$eval('.cb-proposal-card .cb-proposal-label', (el) =>
              el.checkVisibility() ? (el.textContent ?? '') : '',
            )
            .catch(() => '');
          check(
            `proposal card head reads "‹session› recommends" ("${cardHead}"), the choice's label under it ("${cardLabel}")`,
            cardHead.endsWith(' recommends') &&
              !cardHead.includes('proposes') &&
              cardLabel !== '',
          );
          // The session serve names as the recommender: its name, else its
          // folder, else its harness, else its id (sessions.ts).
          const by =
            (
              await createAgent(serveHandle.base, serveHandle.token).api(
                'GET',
                '/api/item?key=' + encodeURIComponent('pr:schuettc/hail#3'),
              )
            ).item?.recommended_by ?? null;
          const who = by
            ? by.name ||
              (by.cwd ?? '').split('/').filter(Boolean).pop() ||
              by.harness ||
              by.id
            : '';
          check(
            `proposal card head names the recommending session serve names ("${who}"), not a hardcoded string`,
            who !== '' && cardHead === `${who} recommends`,
          );

          // Geometry: the recommendation sits above the description, and
          // both above the decide step's question and its cards.
          const bodyTop = await propGeoPage
            .$eval('.cb-lead .cb-body', (el) => el.getBoundingClientRect().top)
            .catch(() => -2);
          const cardBottom = await propGeoPage
            .$eval(
              '.cb-proposal-card',
              (el) => el.getBoundingClientRect().bottom,
            )
            .catch(() => -1);
          const cardTop = await propGeoPage
            .$eval('.cb-proposal-card', (el) => el.getBoundingClientRect().top)
            .catch(() => -1);
          const questionTop = await propGeoPage
            .$eval(
              '.cb-decide .cb-question',
              (el) => el.getBoundingClientRect().top,
            )
            .catch(() => -1);
          check(
            `proposal card is above the description (${cardTop}..${cardBottom} <= ${bodyTop}), which is above the question (${questionTop})`,
            cardTop > 0 &&
              cardBottom > cardTop &&
              cardBottom <= bodyTop &&
              bodyTop < questionTop,
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
          check(
            'proposal card head reads "‹session› recommends", the choice\'s label under it',
            false,
          );
          check(
            'proposal card head names the recommending session serve names, not a hardcoded string',
            false,
          );
          check(
            'proposal card is above the description, which is above the question',
            false,
          );
          check('proposal card is inside the .kit-doc reading document', false);
        }

        // Recommended rows sit under their recommendation's header.
        await propGeoPage.evaluate(() => {
          location.hash = '#/decide';
        });
        await propGeoPage.waitForFunction(() => location.hash === '#/decide');
        await propGeoPage
          .waitForSelector('.kit-row', { timeout: 6000 })
          .catch(() => {});
        await listShows(propGeoPage);

        await until(
          propGeoPage,
          () =>
            !!document.querySelector(
              '.kit-app > .kit-list:not([hidden]) .cb-group',
            ),
        );
        const heads = await propGeoPage
          .$$eval('.kit-app > .kit-list:not([hidden]) .cb-group', (els) =>
            els.map((e) => ({
              id: e.getAttribute('data-group') ?? '',
              label: e.querySelector('.cb-group-label')?.textContent ?? '',
              shown: e.checkVisibility(),
            })),
          )
          .catch(() => []);
        check(
          `recommended rows sit under their recommendation's header (${heads.map((g) => g.label).join(', ')})`,
          heads.some((g) => g.id !== 'none') &&
            heads.every((g) => g.label !== '' && g.shown),
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
          await until(
            acceptPage,
            () => !!document.querySelector('.kit-read .cb-proposal-card'),
          );

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
              location.hash = '#/decide';
            });
            await acceptPage.waitForFunction(
              () => location.hash === '#/decide',
            );
            await listShows(acceptPage);

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
          await until(
            rejectPage,
            () => !!document.querySelector('.kit-read .cb-proposal-card'),
          );

          const cardBeforeReject = await rejectPage
            .$('.cb-proposal-card')
            .then((el) => Boolean(el))
            .catch(() => false);

          if (cardBeforeReject) {
            // Click the 'reject' button (the last button in the card's
            // actions; the key hint follows it on the row).
            const rejectBtn = await rejectPage.$(
              '.cb-proposal-card .kit-btns .kit-btn:last-of-type',
            );
            if (rejectBtn) {
              // By selector: the item may paint again (the proposal's live
              // event) between finding the button and clicking it.
              await rejectPage.click(
                '.cb-proposal-card .kit-btns .kit-btn:last-of-type',
              );
              await rejectPage
                .waitForSelector('.kit-sheet', { timeout: 4000 })
                .catch(() => {});

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

                // Click the 'Reject N' fill button.
                await rejectPage.click('.kit-sheet .kit-btn.fill');
                await rejectPage
                  .waitForFunction(
                    () => !document.querySelector('.kit-sheet'),
                    { timeout: 5000 },
                  )
                  .catch(() => {});
                await until(
                  rejectPage,
                  () => !document.querySelector('.cb-proposal-card'),
                );

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
        await until(
          changePage,
          () => !!document.querySelector('.kit-read .cb-proposal-card'),
        );

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

                // Submit the decision.
                await changePage.click('.kit-sheet .kit-btn.fill');
                await changePage
                  .waitForFunction(
                    () => !document.querySelector('.kit-sheet'),
                    { timeout: 6000 },
                  )
                  .catch(() => {});
                await until(
                  changePage,
                  () => !document.querySelector('.cb-proposal-card'),
                );

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

        // Create a proposal on issue:schuettc/hail#7 for the key tests.
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
          // Navigate to decide, open the item.
          await keyPage.evaluate(() => {
            location.hash = '#/decide';
          });
          await keyPage.waitForFunction(() => location.hash === '#/decide');
          await keyPage
            .waitForSelector('.kit-row', { timeout: 6000 })
            .catch(() => {});
          // The recommended item's row is in the session's group.
          await until(
            keyPage,
            () =>
              !!document.querySelector(
                '.kit-app > .kit-list:not([hidden]) .cb-group[data-group^="pi:probe-key-a-sess|"]',
              ),
            undefined,
            8000,
          );

          // Click its row to open the item.
          const firstRow = keyPage.locator(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
            { hasText: 'schuettc/hail#7' },
          );
          if ((await firstRow.count()) === 1) {
            await firstRow.click();
            await keyPage
              .waitForSelector('.cb-proposal-card', { timeout: 5000 })
              .catch(() => {});

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
            location.hash = '#/decide';
          });
          await keyPage.waitForFunction(() => location.hash === '#/decide');
          await keyPage
            .waitForSelector('.kit-row', { timeout: 6000 })
            .catch(() => {});
          // The recommended repo's row is in the session's group.
          await until(
            keyPage,
            () =>
              !!document.querySelector(
                '.kit-app > .kit-list:not([hidden]) .cb-group[data-group^="pi:probe-key-r-sess|"]',
              ),
            undefined,
            8000,
          );

          // Open its row.
          const rowForR = keyPage.locator(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
            { hasText: 'repo \u00b7 schuettc/hail' },
          );
          if ((await rowForR.count()) === 1) {
            await rowForR.click();
            await keyPage
              .waitForSelector('.cb-proposal-card', { timeout: 5000 })
              .catch(() => {});

            const hasCardForR = await keyPage
              .$('.cb-proposal-card')
              .then((el) => Boolean(el))
              .catch(() => false);

            if (hasCardForR) {
              await keyPage.evaluate(() => {
                window.dispatchEvent(new Event('focus'));
                document.body.dispatchEvent(new FocusEvent('focus'));
              });

              // Press 'r' to reject (opens the reason sheet).
              await keyPage.keyboard.press('r');
              await keyPage
                .waitForSelector('.kit-sheet', { timeout: 4000 })
                .catch(() => {});

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

    // ---- the foot: only "show N more" --------------------------------------
    // The summary and "discard all N" are the middle pane's (decide's
    // summary); the list's foot keeps only "show N more".
    console.log('\nscenario: the foot keeps only "show N more"');

    {
      const footPage = await context.newPage();
      try {
        await footPage.goto(serveHandle.url + '#/decide', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await footPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await listShows(footPage);
        const foot = await footPage.evaluate(() => {
          const f = document.querySelector(
            '.kit-app > .kit-list:not([hidden]) .cb-foot',
          );
          return {
            summary: !!f?.querySelector('.cb-summary'),
            discard: !!f?.querySelector('.cb-discard-all'),
            more: !!f?.querySelector('.cb-foot-more'),
          };
        });
        check(
          `the foot has no summary and no discard all, only "show N more" (${JSON.stringify(foot)})`,
          !foot.summary && !foot.discard && foot.more,
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
          location.hash = '#/decide';
        });
        await claudePage.waitForFunction(() => location.hash === '#/decide');
        await listShows(claudePage);

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
          await until(claudePage, () =>
            [...document.querySelectorAll('.kit-row .kit-sub')].some(
              (e) => e.textContent === 'claude probe note',
            ),
          );

          // The row's sub line gives the claude proposal's reason.
          const subTexts = await claudePage
            .$$eval('.kit-row .kit-sub', (els) =>
              els.map((e) => e.textContent ?? ''),
            )
            .catch(() => []);
          check(
            'a proposal from claude shows live on its row: its reason in the sub line',
            subTexts.includes('claude probe note'),
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

    // ---- Task 5: the theme forces light and dark ---------------------------
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
          }
          const actual = await detectThemeT5(pg);
          check(
            `t5 theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // One more pending proposal on repo:schuettc/hail (after the
        // claude-name test's), from probe-t5-sess: the scenarios after this
        // one run on that state.
        await agentPropose(
          t5Page,
          'probe-t5-sess',
          'pi',
          'repo:schuettc/hail',
          'archive',
          't5 screenshot note',
        );

        await forceThemeT5(t5Page, 'light');
        await forceThemeT5(t5Page, 'dark');
        const darkTheme = await detectThemeT5(t5Page);
        check('t5 dark theme is dark', darkTheme === 'dark');
      } finally {
        await t5Page.close();
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
        await until(
          dockPage,
          () =>
            [...document.querySelectorAll('.cb-dock-threads .kit-chip')].some(
              (c) => (c.textContent ?? '').includes('triage'),
            ) &&
            !!document.querySelector('[data-testid="dock-messages"] .kit-card'),
        );

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

        // 6. YOU card has signal left edge; agent card has amber edge.
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
          check('YOU card has signal left edge', youCards.length > 0);
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
          await until(
            dockPage,
            () =>
              !!document.querySelector(
                '[data-testid="dock-messages"] .cb-dock-state',
              ),
          );
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
            await until(dockPage, () =>
              /#\/(item|rules|apply)\//.test(location.hash),
            );
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
          await until(
            dockPage,
            (n) =>
              document.querySelectorAll(
                '[data-testid="dock-messages"] .kit-card',
              ).length > n,
            beforeCount,
          );
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
        await until(statePage, () => {
          const states = new Set(
            [
              ...document.querySelectorAll(
                '[data-testid="dock-messages"] .cb-dock-state',
              ),
            ].map((e) => e.dataset.state),
          );
          return ['answered', 'declined', 'queued'].every((s) => states.has(s));
        });

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
        // Wait for the delivery to become stuck (CASEBOOK_STUCK_AFTER=2s).
        const isStuck = async () =>
          (await agent.getDelivery(sessId))?.delivery?.stuck === true;
        await eventually(isStuck, 8000);

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
        await eventually(isStuck, 8000);
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
          await until(
            stuckPage,
            () =>
              !!document.querySelector(
                '.cb-dock-pick-sheet .cb-dock-pick-item',
              ),
          );
          const sheetItems = await stuckPage.$$(
            '.cb-dock-pick-sheet .cb-dock-pick-item',
          );
          check('move sheet shows sessions', sheetItems.length > 0);

          if (sheetItems.length > 0) {
            // Click the first item in the sheet (sessId2).
            await sheetItems[0].click();
            await until(
              stuckPage,
              () => !document.querySelector('.cb-dock-stuck'),
            );
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

        // Wait for sessId to go left on serve (CASEBOOK_LEFT_AFTER=3s).
        await eventually(async () => {
          const r = await fetch(
            `${serveHandle.base}/api/sessions?session=${encodeURIComponent(sessId)}`,
            { headers: { 'X-Local-Token': serveHandle.token } },
          );
          const d = await r.json();
          return (d.sessions ?? []).find((s) => s.id === sessId)?.left === true;
        }, 8000);

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

          // ---- Click "move to..." on the page ---------------------------------
          await leftPage.click('[data-testid="dock-move-link"]');
          await until(
            leftPage,
            () =>
              !!document.querySelector(
                '.cb-dock-pick-sheet .cb-dock-pick-item',
              ),
          );
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
            // Verify via API: sessId has 0 queued, sessId2 has 2. sessId
            // has left: it is listed when named (as the page names its own).
            const queuedOf = async () => {
              const r = await fetch(
                `${serveHandle.base}/api/sessions?session=${encodeURIComponent(sessId)}`,
                { headers: { 'X-Local-Token': serveHandle.token } },
              );
              const d = await r.json();
              const q = (id) =>
                (d.sessions ?? []).find((s) => s.id === id)?.queued ?? 0;
              return [q(sessId), q(sessId2)];
            };
            await eventually(async () => {
              const [q1, q2] = await queuedOf();
              return q1 === 0 && q2 === 2;
            });
            const afterResp = await fetch(
              `${serveHandle.base}/api/sessions?session=${encodeURIComponent(sessId)}`,
              {
                headers: { 'X-Local-Token': serveHandle.token },
              },
            );
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
        // The dock has loaded this session (its header names it) while it is
        // still here, so only serve's push can tell it the session left. The
        // header exists, unnamed and without data-left, before the load.
        const loadedHere = await until(
          silentPage,
          (id) => {
            const hd = document.querySelector('.cb-dock-header');
            return hd?.title === id && !hd.hasAttribute('data-left');
          },
          sId,
        );

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
          loadedHere && silentLeftVisible,
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
          location.hash = '#/decide';
        });
        await chipInvPage.waitForFunction(() => location.hash === '#/decide');
        await listShows(chipInvPage);
        await decideCountMatches(chipInvPage, 'after-dock-scenarios');
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
        await until(pickPage, () =>
          (
            document.querySelector('.cb-dock-agent-btn')?.textContent ?? ''
          ).includes('AGENT'),
        );

        // The AGENT ▾ button opens the picker.
        {
          const btnText = await pickPage
            .$eval('.cb-dock-agent-btn', (el) => el.textContent ?? '')
            .catch(() => '');
          check('AGENT ▾ button is present', btnText.includes('AGENT'));
        }

        // Click the picker and select session B.
        await pickPage.click('.cb-dock-agent-btn').catch(() => {});
        await until(
          pickPage,
          (id) =>
            !!document.querySelector(
              `.cb-dock-picker .cb-dock-pick-item[data-session="${id}"]`,
            ),
          sessB,
        );

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
          await until(
            pickPage,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'b',
          );
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
          await until(
            pickPage,
            () =>
              document.querySelectorAll('.cb-dock-threads .kit-chip').length >
              1,
          );
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

    // ---- scenario: task-6 dock themes at 1600x900 -------------------------
    console.log('\nscenario: task-6 dock themes at 1600\u00d7900');

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
          }
          const actual = await detectThemeDock(pg);
          check(`t6 theme forced to ${target}`, actual === target);
        }

        await forceThemeDock(t6Page, 'light');
        const lightTheme = await detectThemeDock(t6Page);
        check('t6 light theme detected', lightTheme === 'light');
        await forceThemeDock(t6Page, 'dark');
        const darkTheme = await detectThemeDock(t6Page);
        check('t6 dark theme detected', darkTheme === 'dark');
      } finally {
        await t6Page.close();
      }
    }

    // ---- Task 7 (its own seeded serve, so its place in the run is free) -----
    await composerScenarios(context);

    // ---- section keys (their own seeded serve too) --------------------------
    await keyScopeScenarios(context);

    // ---- Task 8: Rules (its own seeded serve) -------------------------------
    await ruleGroupsScenarios(context);
    await invalidRuleScenarios(context);

    // ---- Task 9: To apply (its own seeded serves) ---------------------------
    await applyScenarios(context, applyHelpers);

    // ---- Task 10: the keyboard layer, the failure states (their own serves)
    await shellScenarios(context, applyHelpers);

    // ---- the page belongs to the session that opened it (its own serve) ---
    await ownerScenarios(context, applyHelpers);

    // ---- the decide step: question, cards, Not now, move-on (its own serve)
    await decideScenarios(context, applyHelpers);

    // ---- the page under load: no replay, no refetch per heartbeat (its own serve)
    await loadScenarios(context, applyHelpers);

    // ---- saved filters: chips, the editor, the address (their own serves)
    await filtersScenarios(context, applyHelpers);

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
        await until(
          fidPage,
          () =>
            !!document.querySelector('.kit-doc') &&
            !!document.querySelector('.kit-read .cb-kicker') &&
            !!document.querySelector('.kit-read .kit-label'),
        );

        // 1. Reading document geometry: its text starts ≥ 40 px from the
        //    list's right edge; an item's document is at most 1060 px wide.
        {
          const listRight = await fidPage
            .$eval('.kit-list', (el) => el.getBoundingClientRect().right)
            .catch(() => 0);
          const docRect = await fidPage
            .$eval('.kit-doc', (el) => {
              const r = el.getBoundingClientRect();
              return {
                left: r.left + parseFloat(getComputedStyle(el).paddingLeft),
                width: r.width,
              };
            })
            .catch(() => ({ left: 0, width: 0 }));
          check(
            `reading document's text starts ≥ 40 px from list right edge (${docRect.left} - ${listRight})`,
            docRect.left - listRight >= 40,
          );
          check(
            `reading document width is ≤ 1060 px (${docRect.width})`,
            docRect.width > 0 && docRect.width <= 1060,
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

        // ---- the theme forces light and dark --------------------------------
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
          }
          // Assert that we actually reached the target theme.
          const actual = await detectTheme(pg);
          check(
            `theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        await forceTheme(fidPage, 'light');
        await forceTheme(fidPage, 'dark');
      } finally {
        await fidPage.close();
      }
    }

    // ---- scenario: round-2 fidelity, the theme forces light and dark --------
    console.log('\nscenario: round-2 fidelity themes at 1600\xd7900');

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
          }
          // Assert the target was reached.
          const actual = await detectTheme2(pg);
          check(
            `fid2 theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        await forceTheme2(fid2Page, 'light');
        await forceTheme2(fid2Page, 'dark');
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
        // decide, so its count shows.
        await invPage.evaluate(() => {
          location.hash = '#/decide';
        });
        await invPage.waitForFunction(() => location.hash === '#/decide');
        await listShows(invPage);
        await decideCountMatches(invPage, 'end-of-probe');
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
