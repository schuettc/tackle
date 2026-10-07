// probe-load.mjs — the page under load (casebook 0.4.2), run by probe.mjs
// with its check helpers (t.check, t.until, t.eventually).
//
// Measured on Court's machine: an item took 24.6 s to show after a click,
// because the page replayed the whole event log (206,439 events) on load
// and every agent heartbeat published a "sessions" event that made the
// page fetch /api/sessions again (14,870 fetches for one click).
//
// One serve of its own (serve.mjs: --no-open, CASEBOOK_NO_BROWSER=1, the
// hermetic git guard, the fake gh), seeded like that machine at a smaller
// scale: 50 live sessions heartbeating (each once a second here, so a
// heartbeat that publishes shows inside the window), and thousands of
// events of history written into serve's own database.
//
//   an Attention item shows within 1.5 s of its row's click
//   fewer than 5 /api/sessions requests in the 3 s after the click
//   a page whose cursor is older than the oldest kept event (serve pruned
//     what it missed) hears "gap" and reloads its views: a thread made while
//     it was away shows in the dock

import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { startServe } from './serve.mjs';
import { createAgent } from './agent.mjs';

let serves = [];
/** stopLoadServes stops every serve these scenarios started (probe.mjs cleanup). */
export function stopLoadServes() {
  for (const s of serves) s.stop();
  serves = [];
}

const SESSIONS = 50;
const HISTORY = 150000;
// Sessions that came and went (Court's table held 288, 39 live).
const GONE = 250;

// withDb opens this serve's own database (under its temp home only).
async function withDb(home, fn) {
  const { DatabaseSync } = await import('node:sqlite');
  const file = readdirSync(home, { recursive: true }).find(
    (f) => String(f).split('/').pop() === 'casebook.db',
  );
  if (!file) throw new Error(`no casebook.db under ${home}`);
  const db = new DatabaseSync(join(home, String(file)));
  try {
    db.exec('PRAGMA busy_timeout = 5000');
    return fn(db);
  } finally {
    db.close();
  }
}

export async function loadScenarios(shared, t) {
  const context = await shared.browser().newContext({
    viewport: { width: 1600, height: 900 },
  });
  const s = await startServe();
  serves.push(s);
  let beating = true;
  let beats = Promise.resolve();
  try {
    const agent = createAgent(s.base, s.token);
    const ids = Array.from({ length: SESSIONS }, (_, i) => `load-${i + 1}`);
    const beatAll = () =>
      Promise.all(
        ids.map((id, i) =>
          agent.presence(
            id,
            `pi \u00b7 ws-${i + 1}`,
            `/home/court/ws-${i + 1}`,
          ),
        ),
      );
    await beatAll();
    // History: what a week of heartbeats left in the log.
    await withDb(s.home, (db) => {
      const ins = db.prepare(
        `INSERT INTO events(kind, payload, created_at) VALUES ('sessions', ?, ?)`,
      );
      db.exec('BEGIN IMMEDIATE');
      const now = Date.now();
      const gone = db.prepare(
        `INSERT INTO sessions(id, harness, label, cwd, pid, first_seen, last_seen) VALUES (?, 'pi', ?, ?, 0, ?, ?)`,
      );
      for (let i = 0; i < GONE; i++) {
        const day = now - (i + 1) * 3600_000;
        gone.run(
          `gone-${i + 1}`,
          `pi \u00b7 old-${i + 1}`,
          `/home/court/old-${i + 1}`,
          day,
          day,
        );
      }
      for (let i = 0; i < HISTORY; i++) {
        ins.run(JSON.stringify({ id: ids[i % SESSIONS] }), now - 1000);
      }
      db.exec('COMMIT');
    });
    // The heartbeats keep coming, once a second per session.
    beats = (async () => {
      while (beating) {
        await beatAll().catch(() => {});
        await new Promise((r) => setTimeout(r, 1000));
      }
    })();
    await loadScenario(context, t, s, agent, ids);
  } catch (err) {
    t.check(
      `the page under load: the scenario ran to its end — ${err.message}`,
      false,
    );
  } finally {
    beating = false;
    await beats;
    await context.close();
    s.stop();
    serves = serves.filter((x) => x !== s);
  }
}

async function loadScenario(context, t, s, agent, ids) {
  const { check, until } = t;
  console.log(
    `\nscenario: the page under load (${SESSIONS} sessions heartbeating, ${GONE} gone, ${HISTORY} events of history)`,
  );
  const pg = await context.newPage();
  const sessionsAsked = [];
  pg.on('request', (q) => {
    if (
      /\/api\/sessions(\?|$)/.test(
        new URL(q.url()).pathname + new URL(q.url()).search,
      )
    )
      sessionsAsked.push(Date.now());
  });
  try {
    // Opened by its session (as casebook_open does): the dock attaches.
    const tLoad = Date.now();
    await pg.goto(`${s.url}&session=${encodeURIComponent(ids[0])}`, {
      waitUntil: 'load',
      timeout: 15000,
    });
    const rows = pg.locator(
      '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row',
    );
    await rows.nth(1).waitFor({ state: 'visible', timeout: 15000 });
    console.log(
      `  · the rows showed after ${Date.now() - tLoad} ms; ${sessionsAsked.length} sessions requests so far`,
    );
    const before = await pg.evaluate(
      () => document.querySelector('article.cb-item')?.dataset.key ?? '',
    );
    const t0 = Date.now();
    await rows.nth(1).click();
    let shownAt = 0;
    try {
      await pg.waitForFunction(
        (before) => {
          const a = document.querySelector('article.cb-item');
          const h1 = a?.querySelector('.kit-h1, h1');
          return (
            !!a &&
            a.dataset.key !== before &&
            !!h1 &&
            h1.getBoundingClientRect().height > 0
          );
        },
        before,
        { timeout: 30000, polling: 20 },
      );
      shownAt = Date.now();
    } catch {
      // never shown: the check below says so
    }
    const shown = shownAt ? shownAt - t0 : Infinity;
    check(
      `an Attention item shows within 1.5 s of its row's click (${shown === Infinity ? 'never, in 30 s' : `${shown} ms`})`,
      shown <= 1500,
    );
    const windowEnd = t0 + 3000;
    const wait = windowEnd - Date.now();
    // Kept: the check counts requests over the 3 s after the click.
    if (wait > 0) await pg.waitForTimeout(wait);
    const inWindow = sessionsAsked.filter((at) => at >= t0 && at <= windowEnd);
    check(
      `fewer than 5 /api/sessions requests in the 3 s after the click, with ${SESSIONS} sessions heartbeating (${inWindow.length})`,
      inWindow.length < 5,
    );

    // A gap: the page is cut off; meanwhile a thread is made for its
    // session, and serve's prune drops everything the page missed.
    const chips = () =>
      pg.$$eval('.kit-rail [data-thread]', (els) =>
        els.map((e) => e.textContent ?? ''),
      );
    check(
      `the dock is attached to ${ids[0]} and has no "after the gap" thread yet (${(await chips()).join(', ') || 'none'})`,
      !(await chips()).includes('after the gap'),
    );
    const LIVE = /\/api\/(events|state)/;
    await pg.route(LIVE, (r) => r.abort());
    await pg.evaluate(() => window.stop());
    const wentDown = await until(
      pg,
      () => document.querySelector('.kit-live')?.dataset.state !== 'live',
      undefined,
      8000,
    );
    check('cut off, the page is no longer live', wentDown);
    await agent.newThread(ids[0], 'after the gap');
    // One more event after it, so the prune can keep the newest only.
    await agent.presence('load-gap', 'pi \u00b7 gap', '/home/court/gap');
    const kept = await withDb(s.home, (db) => {
      db.prepare(
        'DELETE FROM events WHERE cursor < (SELECT MAX(cursor) FROM events)',
      ).run();
      return Number(db.prepare('SELECT count(*) AS n FROM events').get().n);
    });
    check(`serve's log keeps only the newest event (${kept})`, kept === 1);
    await pg.unroute(LIVE);
    const back = await until(
      pg,
      () => document.querySelector('.kit-live')?.dataset.state === 'live',
      undefined,
      10000,
    );
    check('reconnected, the page is live again', back);
    const reloaded = await until(
      pg,
      () =>
        [...document.querySelectorAll('.kit-rail [data-thread]')].some(
          (e) => e.textContent === 'after the gap',
        ),
      undefined,
      6000,
    );
    check(
      `after the gap the dock reloads: the thread made while the page was away shows (${(await chips()).join(', ')})`,
      reloaded,
    );
  } finally {
    await pg.close();
  }
}
