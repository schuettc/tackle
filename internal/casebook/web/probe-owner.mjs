// probe-owner.mjs — the page belongs to the session that opened it.
//
// casebook_open (and `casebook serve` run by a session) opens the page with
// ?session=<id>: the dock is attached to that session, says so in its header
// (name, folder, harness), and a reload keeps it. A page opened with no
// session asks Court to choose, unless exactly one eligible session is here.
// The chooser offers only eligible sessions (live, not subagent workers),
// sorted by name. When the attached session leaves, the dock says so and asks
// again: it never sends anywhere else on its own.
//
// Its own seeded serve (serve.mjs: --no-open, CASEBOOK_NO_BROWSER=1, its own
// CASEBOOK_HOME), so its sessions are the only ones there.

import { startServe } from './serve.mjs';
import { createAgent } from './agent.mjs';

let serves = [];
/** stopOwnerServes stops every serve these scenarios started (probe.mjs cleanup). */
export function stopOwnerServes() {
  for (const s of serves) s.stop();
  serves = [];
}

// A fake pi-casebook: what `casebook session-info` posts. parent: the
// session it was started from ('' for none).
function sessionInfo(agent, id, name, cwd, parent = '') {
  return agent.api('POST', '/api/agent/session-info', {
    id,
    name,
    harness: 'pi',
    cwd,
    // The probe's sessions all announce process.pid (agent.mjs): one "pi
    // process", like a parent and its pi-subagents workers.
    pid: process.pid,
    parent,
  });
}

// keepHere re-announces sessions every 800 ms (serve's left threshold is 3 s
// in the probe); it returns the stop.
function keepHere(agent, list) {
  const beat = () => {
    for (const [id, cwd, harness] of list) {
      agent.presence(id, '', cwd, harness).catch(() => {});
    }
  };
  beat();
  const t = setInterval(beat, 800);
  t.unref();
  return () => clearInterval(t);
}

// header reads the dock header: the name, the muted rest, the dot's colour.
function header(pg) {
  return pg.evaluate(() => {
    const h = document.querySelector('.cb-dock-header');
    const dot = h?.querySelector('.cb-dock-dot');
    return {
      name:
        document.querySelector('[data-testid="dock-session-name"]')
          ?.textContent ?? '',
      meta:
        document.querySelector('[data-testid="dock-session-meta"]')
          ?.textContent ?? '',
      attach: h?.getAttribute('data-attach') ?? '',
      left: h?.hasAttribute('data-left') ?? false,
      dot: dot ? getComputedStyle(dot).backgroundColor : '',
    };
  });
}

// chooser reads the chooser: shown or not, its lead line, its sessions.
function chooser(pg) {
  return pg.evaluate(() => {
    const c = document.querySelector('[data-testid="dock-chooser"]');
    return {
      shown: !!c && !c.hidden && c.getBoundingClientRect().height > 0,
      lead:
        c?.querySelector('[data-testid="dock-chooser-lead"]')?.textContent ??
        '',
      items: [...(c?.querySelectorAll('.cb-dock-pick-item') ?? [])].map(
        (e) => ({
          id: e.getAttribute('data-session'),
          title: e.querySelector('.cb-dock-pick-title')?.textContent ?? '',
          meta: e.querySelector('.cb-dock-pick-meta')?.textContent ?? '',
        }),
      ),
    };
  });
}

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

async function lightPage(context) {
  const pg = await context.newPage();
  await pg.setViewportSize({ width: 1600, height: 900 });
  await pg.emulateMedia({ colorScheme: 'light' });
  return pg;
}

async function isLight(pg) {
  return pg.evaluate(() => {
    const m = getComputedStyle(document.body).backgroundColor.match(
      /(\d+),\s*(\d+),\s*(\d+)/,
    );
    return !!m && 0.299 * +m[1] + 0.587 * +m[2] + 0.114 * +m[3] >= 128;
  });
}

export async function ownerScenarios(context, t) {
  const { check, checkList, until, eventually } = t;
  const s = await startServe();
  serves.push(s);
  const stops = [];
  try {
    const agent = createAgent(s.base, s.token);
    const ts = Date.now();
    const A = `probe-owner-casebook-${ts}`;
    const B = `probe-owner-site-${ts}`;
    const C = `probe-owner-coordinator-${ts}`;
    const CC = `probe-owner-claude-${ts}`;
    const W = `probe-owner-worker-${ts}`;
    const GONE = `probe-owner-gone-${ts}`;
    const cwdA = '/home/court/tools-workspace';
    const cwdB = '/home/court/luminary-meridian';
    const cwdC = '/home/court/bettor-help-workspace';
    const cwdCC = '/home/court/kempt';

    // An exited session: announced once, named, then silent.
    await agent.presence(GONE, '', cwdA, 'pi');
    await sessionInfo(agent, GONE, 'aaa/exited', cwdA);
    await new Promise((r) => setTimeout(r, 3500));

    // Live: three named pi sessions, a Claude Code session (no pi name), and
    // a pi-subagents worker of B's, in the same process (its parent is live
    // there).
    const stopLive = keepHere(agent, [
      [A, cwdA, 'pi'],
      [B, cwdB, 'pi'],
      [C, cwdC, 'pi'],
      [CC, cwdCC, 'claude'],
      [W, cwdB, 'pi'],
    ]);
    stops.push(stopLive);
    await sessionInfo(agent, A, 'tools-workspace/casebook', cwdA);
    await sessionInfo(agent, B, 'luminary-meridian/site', cwdB);
    await sessionInfo(agent, C, 'bettor-help-workspace/coordinator', cwdC);
    await sessionInfo(agent, W, 'worker#40c0f7e1', cwdB, B);
    const threadA = await agent.newThread(A, 'owner');

    // ---- opening attached to a given session ------------------------------
    console.log(
      '\nscenario: the page opened by a session is attached to it (?session=)',
    );
    const opened = await agent.api('POST', '/api/agent/open', { session: A });
    check(
      `casebook_open from a session opens the page attached to it (${JSON.stringify(opened)})`,
      opened?.session === A,
    );
    {
      const pg = await lightPage(context);
      try {
        await pg.goto(`${s.url}&session=${encodeURIComponent(A)}`, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        check(
          'the header names the attached session: name, then folder · harness',
          await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'tools-workspace/casebook' &&
              document.querySelector('[data-testid="dock-session-meta"]')
                ?.textContent === 'tools-workspace \u00b7 pi',
            undefined,
            10000,
          ),
        );
        const hd = await header(pg);
        check(
          `the attached session's dot is agent amber (${hd.dot})`,
          hd.dot === (await cssColor(pg, 'var(--kit-agent)')),
        );
        check(
          `its folder and harness are muted text`,
          (await pg.evaluate(() => {
            const e = document.querySelector(
              '[data-testid="dock-session-meta"]',
            );
            return e ? getComputedStyle(e).color : '';
          })) === (await cssColor(pg, 'var(--kit-muted)')),
        );
        check(
          'its thread is open and no chooser shows',
          !!(await pg.$(`.cb-dock-threads [data-thread="${threadA.id}"].on`)) &&
            !(await chooser(pg)).shown,
        );
        check('the screenshot is light', await isLight(pg));
        await pg.screenshot({ path: '/tmp/owner-attached.png' });
        console.log('  screenshot: /tmp/owner-attached.png');

        // A message goes to the attached session, and only there.
        await pg.click('[data-testid="composer-input"]');
        await pg.keyboard.type('to the session that opened me');
        await pg.keyboard.press('Enter');
        const d = await agent.wait(A);
        checkList(
          'a message sent from it reaches that session',
          (d?.delivery?.messages ?? []).map((m) => m.body),
          ['to the session that opened me'],
        );
        if (d?.delivery) {
          await agent.reply(
            A,
            d.delivery.messages.map((m) => m.id),
            'answered',
            'ok',
          );
        }

        // A reload keeps it: the URL carries the session.
        await pg.reload({ waitUntil: 'domcontentloaded' });
        check(
          'a reload keeps the attachment (the URL carries ?session=)',
          (await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'tools-workspace/casebook',
            undefined,
            10000,
          )) && new URL(pg.url()).searchParams.get('session') === A,
        );

        // ---- a name shown and updated after a rename ----------------------
        console.log('\nscenario: a renamed session shows its new name');
        await sessionInfo(agent, A, 'tools-workspace/owner', cwdA);
        check(
          'the header follows a rename, without a reload',
          await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'tools-workspace/owner',
            undefined,
            8000,
          ),
        );
      } finally {
        await pg.close();
      }
    }

    // ---- a move away takes the thread out of the dock ---------------------
    console.log(
      '\nscenario: after a stuck delivery moves to B, the page attached to A never sends to B',
    );
    {
      const pg = await lightPage(context);
      try {
        await pg.goto(`${s.url}&session=${encodeURIComponent(A)}`, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await until(
          pg,
          (id) =>
            !!document.querySelector(
              `.cb-dock-threads [data-thread="${id}"].on`,
            ),
          threadA.id,
          10000,
        );
        await pg.click('[data-testid="composer-input"]');
        await pg.keyboard.type('before the move');
        await pg.keyboard.press('Enter');
        const d = await agent.wait(A);
        // A doesn't answer: the delivery goes stuck (2 s in the probe) while
        // A is still here, and Court moves it to B from its card.
        const stuck = await until(
          pg,
          () => !!document.querySelector('.cb-dock-stuck [data-action="move"]'),
          undefined,
          12000,
        );
        if (stuck) {
          await pg.click('.cb-dock-stuck [data-action="move"]');
          await pg
            .click(
              `.cb-dock-pick-sheet .cb-dock-pick-item[data-session="${B}"]`,
              { timeout: 5000 },
            )
            .catch(() => {});
        }
        const moved = await eventually(async () => {
          const v = await agent.api(
            'GET',
            `/api/threads?session=${encodeURIComponent(B)}`,
          );
          return (v.threads ?? []).some((x) => x.id === threadA.id);
        }, 8000);
        check(
          `the stuck delivery moved to B, and its thread with it (stuck ${stuck})`,
          moved,
        );
        check(
          'the dock stops showing the moved thread as the attached session\u2019s',
          await until(
            pg,
            (id) =>
              !document.querySelector(`.cb-dock-threads [data-thread="${id}"]`),
            threadA.id,
            8000,
          ),
        );
        check(
          'the header still names A, and A is still here',
          (await header(pg)).name === 'tools-workspace/owner' &&
            (await header(pg)).attach === 'here',
        );
        // Court's next message: to A's own thread, or refused; never to B.
        await pg.click('[data-testid="composer-input"]');
        await pg.keyboard.type('after the move');
        await pg.keyboard.press('Enter');
        await pg.waitForTimeout(800);
        const inMoved = (
          (await agent.messages(threadA.id)).messages ?? []
        ).some((m) => m.body === 'after the move');
        const dA = await agent.wait(A);
        const toA = (dA?.delivery?.messages ?? []).some(
          (m) => m.body === 'after the move',
        );
        const refused = await pg.evaluate(
          () =>
            !!document.querySelector('.cb-comp-note')?.textContent &&
            document.querySelector('[data-testid="composer-input"]').value ===
              'after the move',
        );
        check(
          `the next send goes to A's own thread or is refused, never to B (in B's thread ${inMoved}, to A ${toA}, refused ${refused})`,
          !inMoved && (toA || refused),
        );
        // Settle what's out, so later scenarios start clean.
        if (dA?.delivery) {
          await agent.reply(
            A,
            dA.delivery.messages.map((m) => m.id),
            'answered',
            'ok',
          );
        }
        if (!moved && d?.delivery) {
          await agent.reply(
            A,
            d.delivery.messages.map((m) => m.id),
            'answered',
            'ok',
          );
        }
        const dB = await agent.wait(B);
        if (dB?.delivery) {
          await agent.reply(
            B,
            dB.delivery.messages.map((m) => m.id),
            'answered',
            'ok',
          );
        }
        await pg.evaluate(() => {
          const i = document.querySelector('[data-testid="composer-input"]');
          if (i) i.value = '';
        });
      } finally {
        await pg.close();
      }
    }

    // ---- no auto-pick with 2+ eligible; workers and exited hidden ---------
    console.log(
      '\nscenario: a page opened by no session asks Court to choose (2+ here)',
    );
    {
      const pg = await lightPage(context);
      try {
        await pg.goto(s.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        check(
          'with several sessions here and none in the URL, nothing is picked: "no session" and the chooser',
          await until(
            pg,
            () => {
              const c = document.querySelector('[data-testid="dock-chooser"]');
              return (
                document.querySelector('[data-testid="dock-session-name"]')
                  ?.textContent === 'no session' &&
                !!c &&
                !c.hidden &&
                c.querySelectorAll('.cb-dock-pick-item').length >= 4
              );
            },
            undefined,
            10000,
          ),
        );
        const ch = await chooser(pg);
        checkList(
          'the chooser lists the eligible sessions by name (a folder when there is none), no worker, no exited session',
          ch.items.map((i) => i.title),
          [
            'bettor-help-workspace/coordinator',
            'kempt',
            'luminary-meridian/site',
            'tools-workspace/owner',
          ],
        );
        checkList(
          'each entry says its folder and harness, muted',
          ch.items.map((i) => i.meta),
          [
            'bettor-help-workspace \u00b7 pi',
            'claude',
            'luminary-meridian \u00b7 pi',
            'tools-workspace \u00b7 pi',
          ],
        );
        check(
          `the chooser asks ("${ch.lead}")`,
          ch.lead === 'Choose the session your messages go to.',
        );
        check(
          'the URL carries no session (nothing was chosen)',
          !new URL(pg.url()).searchParams.has('session'),
        );
        // The AGENT ▾ picker offers the same sessions.
        await pg.click('.cb-dock-agent-btn');
        const picked = await pg.$$eval(
          '.cb-dock-picker .cb-dock-pick-item',
          (els) => els.map((e) => e.getAttribute('data-session')),
        );
        checkList('the AGENT ▾ picker offers the same sessions', picked, [
          C,
          CC,
          B,
          A,
        ]);
        await pg.keyboard.press('Escape');
        // Sending with no session is refused, and says why.
        await pg.click('[data-testid="composer-input"]');
        await pg.keyboard.type('to whom?');
        await pg.keyboard.press('Enter');
        check(
          'sending with no session chosen sends nothing, keeps the text and says why',
          await until(
            pg,
            () =>
              document.querySelector('.cb-comp-note')?.textContent ===
                'choose a session first' &&
              document.querySelector('[data-testid="composer-input"]').value ===
                'to whom?',
          ),
        );
        // The composer's foot at 1600×900: the key hint and the note each
        // stay on one line (the note drops whole to a second line rather
        // than breaking mid-phrase).
        const foot = await pg.evaluate(() =>
          [...document.querySelectorAll('.cb-comp-foot > span')].map((e) => {
            const r = e.getBoundingClientRect();
            const lh = parseFloat(getComputedStyle(e).lineHeight) || 0;
            const fs = parseFloat(getComputedStyle(e).fontSize) || 0;
            const line = lh || fs * 1.4;
            return { text: e.textContent, h: r.height, line };
          }),
        );
        check(
          `nothing in the composer's foot wraps mid-phrase (${foot.map((f) => `"${f.text}" ${f.h.toFixed(1)}/${f.line.toFixed(1)}px`).join(', ')})`,
          foot.length === 2 &&
            foot.every((f) => f.text && f.h > 0 && f.h < f.line * 1.5),
        );
        check('the screenshot is light', await isLight(pg));
        await pg.screenshot({ path: '/tmp/owner-chooser.png' });
        console.log('  screenshot: /tmp/owner-chooser.png');
        // Choosing attaches the page, and the URL keeps it.
        await pg
          .click(
            `[data-testid="dock-chooser"] .cb-dock-pick-item[data-session="${B}"]`,
            { timeout: 5000 },
          )
          .catch(() => {});
        check(
          'choosing a session attaches the page to it and puts it in the URL',
          (await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'luminary-meridian/site' &&
              document.querySelector('[data-testid="dock-chooser"]').hidden,
          )) && new URL(pg.url()).searchParams.get('session') === B,
        );
        await pg.click('[data-testid="composer-input"]');
        await pg.keyboard.press('Enter');
        const d = await agent.wait(B);
        checkList(
          'the kept text, sent now, goes to the chosen session',
          (d?.delivery?.messages ?? []).map((m) => m.body),
          ['to whom?'],
        );
        if (d?.delivery) {
          await agent.reply(
            B,
            d.delivery.messages.map((m) => m.id),
            'answered',
            'ok',
          );
        }
      } finally {
        await pg.close();
      }
    }

    // ---- the attached session leaving prompts a choice --------------------
    console.log('\nscenario: when the attached session leaves, the page asks');
    {
      // C stops announcing itself; the others stay.
      stopLive();
      const stopRest = keepHere(agent, [
        [A, cwdA, 'pi'],
        [B, cwdB, 'pi'],
        [CC, cwdCC, 'claude'],
        [W, cwdB, 'pi'],
      ]);
      stops.push(stopRest);
      const threadC = await agent.newThread(C, 'coordination');
      await agent.postMessage(threadC.id, 'an earlier note');
      // A draft batch Court started for it.
      await agent.postMessage(threadC.id, 'a drafted follow-up', {}, true);
      const pg = await lightPage(context);
      try {
        await pg.goto(`${s.url}&session=${encodeURIComponent(C)}`, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        check(
          'the page opens attached to the session',
          await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'bettor-help-workspace/coordinator',
            undefined,
            10000,
          ),
        );
        check(
          'when it leaves, the dock says so and offers the sessions here, without choosing',
          await until(
            pg,
            () => {
              const c = document.querySelector('[data-testid="dock-chooser"]');
              return (
                !!document.querySelector('.cb-dock-header[data-left]') &&
                !!c &&
                !c.hidden &&
                c.querySelector('[data-testid="dock-chooser-lead"]')
                  ?.textContent ===
                  'bettor-help-workspace/coordinator left. Messages wait for it; choose a session to keep talking.'
              );
            },
            undefined,
            12000,
          ),
        );
        const ch = await chooser(pg);
        checkList(
          'the chooser offers the eligible sessions still here',
          ch.items.map((i) => i.id),
          [CC, B, A],
        );
        check(
          'the header still names the session that left, and the URL still carries it',
          (await header(pg)).name === 'bettor-help-workspace/coordinator' &&
            new URL(pg.url()).searchParams.get('session') === C,
        );
        check(
          'its thread stays readable',
          (
            await pg.$$eval(
              '[data-testid="dock-messages"] .cb-dock-bodytext',
              (els) => els.map((e) => e.textContent),
            )
          ).includes('an earlier note'),
        );
        // Sending is refused: nothing goes to the session that left, nor
        // anywhere else.
        const queuedAll = async () =>
          // every session, the one that left too (?all=1)
          ((await agent.api('GET', '/api/sessions?all=1')).sessions ?? [])
            .map((x) => `${x.id}:${x.queued}`)
            .sort()
            .join(',');
        const before = await queuedAll();
        // "+ thread" opens nothing for a session that left, and says why.
        const nThreads = async () =>
          (
            (
              await agent.api(
                'GET',
                `/api/threads?session=${encodeURIComponent(C)}`,
              )
            ).threads ?? []
          ).length;
        const threadsBefore = await nThreads();
        await pg.click('[data-testid="dock-add-thread"]');
        check(
          '"+" opens no thread for a session that left, and says why',
          (await until(
            pg,
            () =>
              document.querySelector('.cb-comp-note')?.textContent ===
              'your session left',
          )) && (await nThreads()) === threadsBefore,
        );
        await pg.evaluate(() => {
          document.querySelector('.cb-comp-note').textContent = '';
        });
        await pg.click('[data-testid="composer-input"]');
        await pg.keyboard.type('are you there?');
        await pg.keyboard.press('Enter');
        check(
          'sending to a session that left is refused, and the dock says why',
          await until(
            pg,
            () =>
              document.querySelector('.cb-comp-note')?.textContent ===
                'your session left' &&
              document.querySelector('[data-testid="composer-input"]').value ===
                'are you there?',
          ),
        );
        check(
          'nothing was queued to any session',
          (await queuedAll()) === before,
        );
        // Its draft batch doesn't go out either: the tray's send is refused
        // the same way, and the drafts stay.
        await pg
          .click('[data-testid="batch-tray"] .cb-batch-send', { timeout: 5000 })
          .catch(() => {});
        await pg.waitForTimeout(500);
        check(
          "the batch tray's send is refused too: the draft stays a draft, nothing queued",
          (await queuedAll()) === before &&
            (await pg.$$eval(
              '[data-testid="batch-tray"] .cb-batch-draft',
              (els) => els.length,
            )) === 1 &&
            (await pg.evaluate(
              () => document.querySelector('.cb-comp-note')?.textContent,
            )) === 'your session left',
        );
        check('the screenshot is light', await isLight(pg));
        await pg.screenshot({ path: '/tmp/owner-left.png' });
        console.log('  screenshot: /tmp/owner-left.png');
        // Court chooses: the page is now A's.
        await pg
          .click(
            `[data-testid="dock-chooser"] .cb-dock-pick-item[data-session="${A}"]`,
            { timeout: 5000 },
          )
          .catch(() => {});
        check(
          "choosing re-attaches the page: A's name, the URL says A",
          (await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'tools-workspace/owner',
          )) && new URL(pg.url()).searchParams.get('session') === A,
        );
      } finally {
        await pg.close();
      }
    }

    // ---- auto-pick with exactly one eligible session ----------------------
    console.log(
      '\nscenario: a page opened by no session attaches to the only one here',
    );
    {
      for (const stop of stops) stop();
      // Only B stays (W, its worker, stays too: it doesn't count).
      const stopB = keepHere(agent, [
        [B, cwdB, 'pi'],
        [W, cwdB, 'pi'],
      ]);
      stops.push(stopB);
      check(
        'serve has one eligible session left (B)',
        await eventually(async () => {
          const v = await agent.api('GET', '/api/sessions');
          const el = (v.sessions ?? []).filter((x) => x.eligible);
          return el.length === 1 && el[0].id === B;
        }, 8000),
      );
      const pg = await lightPage(context);
      try {
        await pg.goto(s.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        check(
          'with exactly one eligible session here, the page attaches to it on its own',
          await until(
            pg,
            () =>
              document.querySelector('[data-testid="dock-session-name"]')
                ?.textContent === 'luminary-meridian/site' &&
              document.querySelector('[data-testid="dock-chooser"]').hidden,
            undefined,
            10000,
          ),
        );
        check(
          'the lone-session attachment is not written to the URL (not a choice)',
          !new URL(pg.url()).searchParams.has('session'),
        );
        // A second session arrives: the guess no longer holds, so the
        // page goes back to asking.
        const stopA = keepHere(agent, [[A, cwdA, 'pi']]);
        stops.push(stopA);
        check(
          'when a second session arrives, the lone-session attachment drops back to the chooser',
          await until(
            pg,
            () => {
              const c = document.querySelector('[data-testid="dock-chooser"]');
              return (
                document.querySelector('[data-testid="dock-session-name"]')
                  ?.textContent === 'no session' &&
                !!c &&
                !c.hidden &&
                c.querySelectorAll('.cb-dock-pick-item').length === 2
              );
            },
            undefined,
            10000,
          ),
        );
      } finally {
        await pg.close();
      }
    }
  } finally {
    for (const stop of stops) stop();
    s.stop();
    serves = serves.filter((x) => x !== s);
  }
}
