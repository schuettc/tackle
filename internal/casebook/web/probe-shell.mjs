// probe-shell.mjs — the keyboard layer and the failure states (Task 10), run
// by probe.mjs with its check helpers (t.check, t.checkList, t.until,
// t.eventually).
//
// Each scenario starts its own serve (serve.mjs: --no-open,
// CASEBOOK_NO_BROWSER=1, the hermetic git guard, the fake gh) with its own
// seeded repos, and opens its pages in a browser context of its own (a
// context's fake clock is shared by its pages).
//
//   keyboard navigation            spec §10 "keyboard navigation", "the theme"
//   the poll fallback …            spec §2.3: the stream drops, polling keeps state
//   serve isn't answering …        spec §2.4 "serve dies": the banner, retry
//   a restarted serve …            spec §2.4: the stale pill, the tab stops
//   decisions queued offline …     spec §2.4 "offline": offline · N queued
//   a slow push …                  the decide sheet closes on serve's answer,
//                                  not on the push after it

import { probeGit, startServe } from './serve.mjs';
import { createAgent } from './agent.mjs';

let serves = [];
/** stopShellServes stops every serve these scenarios started (probe.mjs cleanup). */
export function stopShellServes() {
  for (const s of serves) s.stop();
  serves = [];
}

// withServe runs fn with a serve of its own, and stops it after.
async function withServe(opts, fn) {
  const s = await startServe(opts);
  serves.push(s);
  try {
    return await fn(s);
  } finally {
    s.stop();
    serves = serves.filter((x) => x !== s);
  }
}

// withContext runs fn in a browser context of its own.
async function withContext(shared, fn) {
  const context = await shared.browser().newContext();
  try {
    return await fn(context);
  } finally {
    await context.close();
  }
}

// Repos for the GitHub cache (serve.mjs seedRepos): open PRs by people, with
// no reply from Court, so they wait on him.
function seedRepo(name, prs) {
  return {
    repo: `schuettc/${name}`,
    pushed_at: '2026-09-20T00:00:00Z',
    default_branch: 'main',
    prs: prs.map(([number, title, author], i) => ({
      repo: `schuettc/${name}`,
      number,
      title,
      author,
      state: 'OPEN',
      created_at: `2026-09-0${i + 1}T00:00:00Z`,
      updated_at: `2026-09-0${i + 1}T00:00:00Z`,
    })),
    issues: [],
  };
}
const dormant = (name) => ({
  repo: `schuettc/${name}`,
  pushed_at: '2023-01-10T00:00:00Z',
  default_branch: 'main',
  prs: [],
  issues: [],
});

// cssColor resolves a CSS colour expression to its computed rgb() string.
function cssColor(pg, expr, prop = 'color') {
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

// press presses keys with nothing focused. Headless Chrome withholds focus
// events without window focus, so they are dispatched (progress.md).
async function press(pg, ...keys) {
  await pg.evaluate(() => {
    document.activeElement?.blur();
    window.dispatchEvent(new Event('focus'));
    document.body.dispatchEvent(new FocusEvent('focus'));
  });
  for (const k of keys) await pg.keyboard.press(k);
}

// setTheme clicks the bar's theme control until the page is in theme.
async function setTheme(pg, theme) {
  for (let i = 0; i < 3; i++) {
    const th = await pg.evaluate(() => document.documentElement.dataset.theme);
    if (th === theme) return;
    await pg.click('button.kit-ctl:has-text("theme")');
  }
}

const BG = { light: 'rgb(244, 245, 248)', dark: 'rgb(20, 22, 29)' };

// shoot takes /tmp/t10-<name>-<theme>.png for each theme, asserting the
// theme (html[data-theme] and the body's ground) and that nothing is
// focused (a focus ring would be the probe's, not the page's).
async function shoot(t, pg, name, themes = ['light', 'dark']) {
  for (const theme of themes) {
    await setTheme(pg, theme);
    await pg.evaluate(() => document.activeElement?.blur());
    const got = await pg.evaluate(() => ({
      theme: document.documentElement.dataset.theme,
      bg: getComputedStyle(document.body).backgroundColor,
      blurred: document.activeElement === document.body,
    }));
    const path = `/tmp/t10-${name}-${theme}.png`;
    t.check(
      `${path} is ${theme} (theme ${got.theme}, body ${got.bg}), nothing focused`,
      got.theme === theme && got.bg === BG[theme] && got.blurred,
    );
    await pg.screenshot({ path });
  }
}

// The shown section's list and the rows in it.
const SHOWN = '.kit-app > .kit-list:not([hidden])';

// rowsOf reads a list's rows: its kicker, title, whether it is the cursor,
// selected, and what it says under the title.
function rowsOf(pg, sel = SHOWN) {
  return pg.$$eval(`${sel} .kit-row`, (els) =>
    els.map((e) => ({
      kicker: e.querySelector('.kit-kicker')?.textContent ?? '',
      title: e.querySelector('.kit-title')?.textContent ?? '',
      sub: e.querySelector('.kit-sub')?.textContent ?? '',
      cur: e.classList.contains('cur'),
      sel: e.classList.contains('sel'),
      id: e.dataset.key ?? '',
    })),
  );
}
const curOf = async (pg, sel = SHOWN) =>
  (await rowsOf(pg, sel)).findIndex((r) => r.cur);

// overlayKeys opens the ? overlay, reads every key it lists, and closes it.
async function overlayKeys(t, pg) {
  await press(pg, '?');
  const open = await t.until(pg, () => !!document.querySelector('.kit-keys'));
  if (!open) return [];
  const keys = await pg.$$eval('.kit-keys kbd', (els) =>
    els.map((e) => e.textContent),
  );
  await pg.keyboard.press('Escape');
  await t.until(pg, () => !document.querySelector('.kit-keys'));
  return keys.sort();
}

// The keys every section lists: the switches and the dock's.
const PAGE_KEYS = ['g a', 'g r', 'g p', '.', '\u2318\u21b5'];
const MOVE_OPEN = ['j', '\u2193', 'k', '\u2191', 'o', '\u21b5'];
const SELECT = ['x', '\u21e7x'];
const FAMILY = ['?', 'Esc'];
const EXPECT = {
  attention: [
    ...MOVE_OPEN,
    ...SELECT,
    '/',
    ...FAMILY,
    ...PAGE_KEYS,
    'd',
    'a',
    'r',
    'u',
    ...'123456789',
  ],
  board: ['/', ...FAMILY, ...PAGE_KEYS, 'd'],
  rules: [...MOVE_OPEN, ...FAMILY, ...PAGE_KEYS, 'a'],
  apply: [...MOVE_OPEN, ...SELECT, ...FAMILY, ...PAGE_KEYS, 'a', 'p'],
};

export async function shellScenarios(shared, t) {
  // A scenario that throws fails, and the next still runs.
  for (const [name, run] of [
    ['keyboard navigation', keyboardScenario],
    ['the poll fallback', pollFallbackScenario],
    ['the disconnected banner', downScenario],
    ['the stale pill', staleScenario],
    ['offline · N queued', offlineQueuedScenario],
    ['a slow push', slowPushScenario],
    ['push failed', pushFailedScenario],
    ['the stale pill from an API call', staleApiScenario],
    ['200 rows and "show more"', pageCapScenario],
    ["the board's search", boardSearchScenario],
  ]) {
    try {
      await run(shared, t);
    } catch (err) {
      t.check(`${name}: the scenario ran to its end — ${err.message}`, false);
    }
  }
}

// ---- keyboard navigation -----------------------------------------------------

const KB = 'kb-nav';
const KB_KEY = (n) => `pr:schuettc/${KB}#${n}`;
const KB_SEED = {
  seedRepos: [
    seedRepo(KB, [
      [41, 'retry the upload on a 503', 'lena'],
      [42, 'drop the legacy flag', 'lena'],
      [43, 'split the settings page', 'omar'],
      [44, 'wire the retry budget', 'omar'],
      [45, 'bump the timeout to 30s', 'pia'],
      [46, 'name the worker pool', 'pia'],
    ]),
    dormant('kb-dormant-a'),
    dormant('kb-dormant-b'),
  ],
};

async function keyboardScenario(shared, t) {
  const { check, checkList, until, eventually } = t;
  console.log('\nscenario: keyboard navigation');
  await withServe(KB_SEED, (serveHandle) =>
    withContext(shared, async (context) => {
      const agent = createAgent(serveHandle.base, serveHandle.token);
      const sid = 'probe-t10-kb';
      const label = 'pi \u00b7 kb';
      await agent.presence(sid, label, '/home/court/kb', 'pi');
      // The session stays here (serve's left threshold is 3 s in the probe):
      // the dock adds to a batch only for a session that is.
      const beat = setInterval(() => {
        agent.presence(sid, label, '/home/court/kb', 'pi').catch(() => {});
      }, 800);
      beat.unref();
      const thread = await agent.newThread(sid, 'kb');
      await agent.propose(sid, [KB_KEY(41)], 'keep', 'kb');
      await agent.propose(sid, [KB_KEY(42)], 'close', 'kb');
      await agent.api('POST', '/api/decide', {
        keys: ['repo:schuettc/kb-dormant-a', 'repo:schuettc/kb-dormant-b'],
        disposition: 'archive',
      });
      for (const [id, name] of [
        ['kb-first', 'First keyboard rule'],
        ['kb-second', 'Second keyboard rule'],
      ]) {
        await agent.api('POST', '/api/rules/draft', {
          id,
          name,
          status: 'draft',
          match: [{ field: 'repo', op: 'is', value: `schuettc/${KB}` }],
          propose: { disposition: 'keep' },
        });
      }
      const item = (key) =>
        agent.api('GET', `/api/item?key=${encodeURIComponent(key)}`);

      const pg = await context.newPage();
      const errors = [];
      pg.on('pageerror', (e) => errors.push(String(e)));
      try {
        await pg.setViewportSize({ width: 1600, height: 900 });
        await pg.goto(`${serveHandle.url}&q=${KB}#/attention/waiting`, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        const ready = await until(
          pg,
          ([l, th]) =>
            document.querySelectorAll(
              '.kit-app > .kit-list:not([hidden]) .kit-row',
            ).length === 6 &&
            document.querySelector('.cb-dock-session-label')?.textContent ===
              l &&
            !!document.querySelector(
              `.cb-dock-threads [data-thread="${th}"].on`,
            ),
          // The only session here: the dock attaches to it, named by its
          // folder (it has no pi name).
          ['kb', thread.id],
          10000,
        );
        check(
          'Attention lists the six kb-nav PRs, and the dock its session',
          ready,
        );
        let rows = await rowsOf(pg);

        // j / k move the cursor; o and ↵ open the cursor's row, and the
        // reading column follows.
        await press(pg, 'j');
        const afterJ = await curOf(pg);
        const readBefore = await pg.$eval(
          '.kit-app > .kit-read:not([hidden])',
          (e) => e.textContent,
        );
        await press(pg, 'j', 'k');
        const afterJK = await curOf(pg);
        check(
          `j moves the cursor down a row and k back up (${afterJ}, then ${afterJK})`,
          afterJ === 1 && afterJK === 1,
        );
        check(
          'moving opens nothing: the reading column stays',
          (await pg.$eval(
            '.kit-app > .kit-read:not([hidden])',
            (e) => e.textContent,
          )) === readBefore,
        );
        await press(pg, 'o');
        const oOpened = await until(
          pg,
          (want) =>
            document.querySelector('.kit-app > .kit-read:not([hidden]) h1')
              ?.textContent === want,
          rows[1].title,
        );
        const hashO = await pg.evaluate(() =>
          decodeURIComponent(location.hash),
        );
        check(
          `o opens the cursor's row: the reading column shows "${rows[1].title}" (${hashO})`,
          oOpened &&
            hashO === `#/item/${rows[1].kicker.replace(/^pr · /, 'pr:')}`,
        );
        await press(pg, 'k', 'Enter');
        check(
          `↵ opens the row k moved to ("${rows[0].title}")`,
          await until(
            pg,
            (want) =>
              document.querySelector('.kit-app > .kit-read:not([hidden]) h1')
                ?.textContent === want,
            rows[0].title,
          ),
        );

        // x selects the cursor's row; ⇧x selects the range to it; d decides
        // the selection, and it is decided on serve.
        rows = await rowsOf(pg);
        const hasProposal = (r) => r.sub.includes('proposes');
        let from = -1;
        for (let i = 0; i + 2 < rows.length; i++) {
          if (![0, 1, 2].some((d) => hasProposal(rows[i + d]))) {
            from = i;
            break;
          }
        }
        check(
          `three rows in a row without a proposal to select (from row ${from})`,
          from >= 0,
        );
        const cur0 = await curOf(pg);
        const moveTo = (i) => {
          const d = i - cur0;
          return Array(Math.abs(d)).fill(d > 0 ? 'j' : 'k');
        };
        await press(pg, ...moveTo(from), 'x');
        check(
          'x selects the cursor\'s row: "Decide 1"',
          await until(
            pg,
            () =>
              document.querySelector('.kit-primary')?.textContent ===
              'Decide 1',
          ),
        );
        await press(pg, 'j', 'j', 'Shift+X');
        const picked = rows.slice(from, from + 3);
        const pickedKeys = picked.map((r) => r.kicker.replace(/^pr · /, 'pr:'));
        check(
          '⇧x selects the range to the cursor: "Decide 3"',
          await until(
            pg,
            () =>
              document.querySelector('.kit-primary')?.textContent ===
              'Decide 3',
          ),
        );
        checkList(
          'the selected rows are the three the cursor went over',
          (await rowsOf(pg)).filter((r) => r.sel).map((r) => r.title),
          picked.map((r) => r.title),
        );
        await press(pg, 'd');
        const sheetTitle = (await until(
          pg,
          () => !!document.querySelector('.kit-sheet'),
        ))
          ? await pg.$eval(
              '.kit-sheet',
              (e) =>
                e.querySelector('.kit-sheet-head')?.textContent ??
                e.textContent,
            )
          : '';
        check(
          `d opens decide for the selection ("${sheetTitle}")`,
          sheetTitle.includes('decide 3 items'),
        );
        await pg.click('.kit-sheet .cb-choice[data-d="keep"]');
        await pg.click('.kit-sheet .kit-btn.fill:has-text("· 3 ")');
        check(
          'and deciding there decides the three on serve (keep)',
          await eventually(async () => {
            const got = await Promise.all(pickedKeys.map(item));
            return got.every((v) => v?.item?.decision?.disposition === 'keep');
          }),
        );
        check(
          'the decided rows leave the list and the selection ("Decide N" goes)',
          await until(
            pg,
            () =>
              document.querySelectorAll(
                '.kit-app > .kit-list:not([hidden]) .kit-row',
              ).length === 3 &&
              !!document.querySelector('.kit-primary')?.hidden,
          ),
        );
        // serve announces the decision (and the rebuilt index) just before
        // it answers, so the rows can leave while the sheet, modal, is still
        // open. Keys wait while a sheet is open: the next key waits for it
        // to close.
        check(
          'the decide sheet closes when serve answers',
          await until(pg, () => !document.querySelector('.kit-sheet')),
        );

        // a accepts the open item's proposal; r rejects it (its sheet).
        // An item opening (#/item/<key>) reloads the list too. Here that
        // reload is held on its way (2.5 s), so a and r are pressed while it
        // is: they act on the proposal the reading column shows, whatever
        // the list is doing.
        let held = 0;
        let hold = false;
        await pg.route(/\/api\/items\?/, async (r) => {
          if (!hold) return r.continue();
          held++;
          await new Promise((res) => setTimeout(res, 2500));
          held--;
          return r.continue().catch(() => {});
        });
        const openItem = async (key) => {
          hold = true;
          await pg.evaluate((k) => {
            location.hash = '#/item/' + encodeURIComponent(k);
          }, key);
          const shown = await until(
            pg,
            (k) =>
              !!document.querySelector('.kit-read .cb-proposal-card') &&
              (
                document.querySelector('.kit-read .cb-kicker')?.textContent ??
                ''
              )
                .replace(/\s+/g, '')
                .includes(k.replace(/^pr:/, '')),
            key,
            8000,
          );
          return shown && held > 0;
        };
        const proposalState = async (key) =>
          ((await item(key))?.proposals ?? []).map((p) => p.state).join(',');
        check(
          'the open item shows its pending proposal (keep), its list reload still on its way',
          await openItem(KB_KEY(41)),
        );
        const accepts = [];
        const onAccept = async (r) => {
          if (!r.url().includes('/api/proposals/accept')) return;
          accepts.push(`${r.status()} ${await r.text().catch(() => '')}`);
        };
        pg.on('response', onAccept);
        const atPress = await pg.evaluate(() => ({
          active: `${document.activeElement?.tagName}.${document.activeElement?.className}`,
          sheet: !!document.querySelector('.kit-sheet'),
          hash: location.hash,
          kicker: document.querySelector('.kit-read .cb-kicker')?.textContent,
        }));
        await press(pg, 'a');
        hold = false;
        let seen41 = '';
        const accepted = await eventually(async () => {
          const v = await item(KB_KEY(41));
          const states = (v?.proposals ?? []).map((p) => p.state).join(',');
          seen41 = `proposals ${states || 'none'}, decision ${v?.item?.decision?.disposition ?? 'none'}`;
          return (
            states === 'accepted' && v?.item?.decision?.disposition === 'keep'
          );
        });
        pg.off('response', onAccept);
        check(
          `a accepts the open proposal: serve has it accepted, and the item decided keep (${seen41}; accept calls: ${accepts.join(' | ') || 'none'}; at the press: ${atPress.active}, sheet ${atPress.sheet}, ${atPress.hash}, "${atPress.kicker}")`,
          accepted,
        );
        check(
          'the next open item shows its pending proposal (close), its list reload still on its way',
          await openItem(KB_KEY(42)),
        );
        await press(pg, 'r');
        hold = false;
        const rejectSheet = (await until(
          pg,
          () => !!document.querySelector('.kit-sheet'),
        ))
          ? await pg.$eval('.kit-sheet .kit-sheet-head', (e) => e.textContent)
          : '';
        check(
          `r opens reject for the open proposal ("${rejectSheet}")`,
          rejectSheet === 'reject 1 proposal',
        );
        await pg.click('.kit-sheet .kit-btn:has-text("Reject 1")');
        check(
          'and rejecting there rejects it on serve',
          await eventually(
            async () => (await proposalState(KB_KEY(42))) === 'rejected',
          ),
        );
        await until(pg, () => !document.querySelector('.kit-sheet'));

        // . focuses the composer; typed there, keys are text (none fires);
        // ⌘↵ fires in the field (it is inField): the draft joins the batch.
        await pg.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await until(
          pg,
          () =>
            document.querySelectorAll(
              '.kit-app > .kit-list:not([hidden]) .kit-row',
            ).length === 2,
        );
        const curBefore = await curOf(pg);
        await press(pg, '.');
        check(
          '. focuses the composer',
          await until(
            pg,
            () =>
              document.activeElement?.getAttribute('data-testid') ===
              'composer-input',
          ),
        );
        const typed = 'g r j x d ?';
        await pg.keyboard.type(typed);
        await pg.waitForTimeout(1200);
        const inField = await pg.evaluate(() => ({
          value: document.querySelector('[data-testid="composer-input"]').value,
          hash: location.hash,
          sheet: !!document.querySelector('.kit-sheet'),
          sel: document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row.sel',
          ).length,
        }));
        check(
          `typed in the composer, g r j x d ? are text: nothing switches, moves, selects or opens ("${inField.value}", ${inField.hash}, sheet ${inField.sheet}, ${inField.sel} selected)`,
          inField.value === typed &&
            inField.hash === '#/attention/waiting' &&
            !inField.sheet &&
            inField.sel === 0 &&
            (await curOf(pg)) === curBefore,
        );
        await pg.keyboard.press('Meta+Enter');
        check(
          '⌘↵ in the composer adds the text to the batch (tray and serve)',
          (await until(
            pg,
            (w) =>
              [
                ...document.querySelectorAll(
                  '[data-testid="batch-tray"] .cb-batch-draft .cb-batch-text',
                ),
              ]
                .map((e) => e.textContent)
                .includes(w),
            typed,
          )) &&
            (await eventually(async () =>
              ((await agent.messages(thread.id)).drafts ?? [])
                .map((m) => m.body)
                .includes(typed),
            )),
        );
        await pg.keyboard.press('Escape');
        // The draft was the in-field fixture: remove it (the tray's ×), so
        // the tray is empty again, on the page and on serve.
        await pg.click(
          '[data-testid="batch-tray"] .cb-batch-act[data-action="remove"]',
        );
        check(
          'the fixture draft is removed: the tray hides, and serve has no drafts',
          (await until(
            pg,
            () =>
              !!document.querySelector('[data-testid="batch-tray"]')?.hidden,
          )) &&
            (await eventually(
              async () =>
                ((await agent.messages(thread.id)).drafts ?? []).length === 0,
            )),
        );

        // ? lists every key that works here, and none that doesn't.
        checkList(
          'on Attention the ? overlay lists exactly its keys',
          await overlayKeys(t, pg),
          [...EXPECT.attention].sort(),
        );
        // The overlay on Attention, light and dark: the theme is set with it
        // closed (its backdrop takes clicks), then it is opened.
        for (const theme of ['light', 'dark']) {
          await setTheme(pg, theme);
          await press(pg, '?');
          await until(pg, () => !!document.querySelector('.kit-keys'));
          await pg.evaluate(() => document.activeElement?.blur());
          const got = await pg.evaluate(() => ({
            theme: document.documentElement.dataset.theme,
            bg: getComputedStyle(document.body).backgroundColor,
            blurred: document.activeElement === document.body,
            shown: !!document.querySelector('.kit-keys'),
            tray: !document.querySelector('[data-testid="batch-tray"]')?.hidden,
          }));
          const path = `/tmp/t10-overlay-${theme}.png`;
          check(
            `${path} is ${theme} with the overlay open (theme ${got.theme}, body ${got.bg}), nothing focused, no batch tray (${got.tray})`,
            got.theme === theme &&
              got.bg === BG[theme] &&
              got.blurred &&
              got.shown &&
              !got.tray,
          );
          await pg.screenshot({ path });
          await pg.keyboard.press('Escape');
          await until(pg, () => !document.querySelector('.kit-keys'));
        }
        await setTheme(pg, 'light');

        // g r: Rules, without the mouse. Its list takes the list keys; the
        // hidden Attention list keeps its cursor.
        const attCur = await curOf(pg);
        await press(pg, 'g', 'r');
        check(
          'g r switches to Rules',
          await until(
            pg,
            () =>
              location.hash === '#/rules' &&
              document
                .querySelector('.kit-ctl[data-id="rules"]')
                ?.classList.contains('on') &&
              !!document.querySelector(
                '.kit-app > .kit-list.cb-rules-list:not([hidden])',
              ),
          ),
        );
        await until(
          pg,
          () =>
            document.querySelectorAll(
              '.kit-app > .kit-list:not([hidden]) .kit-row',
            ).length >= 2,
        );
        checkList(
          'on Rules the ? overlay lists exactly its keys (no x or ⇧x: rules are not selected)',
          await overlayKeys(t, pg),
          [...EXPECT.rules].sort(),
        );
        const ruleRows = await rowsOf(pg);
        const ruleCur = await curOf(pg);
        await press(pg, 'j');
        const ruleCur2 = await curOf(pg);
        check(
          `j moves the Rules list's cursor (${ruleCur} → ${ruleCur2})`,
          ruleCur2 === ruleCur + 1,
        );
        check(
          "and not the hidden Attention list's",
          (await curOf(
            pg,
            '.kit-app > .kit-list:not(.cb-rules-list):not(.cb-apply-list)',
          )) === attCur,
        );
        await press(pg, 'o');
        const ruleTitle = ruleRows[ruleCur2]?.title ?? '';
        check(
          `o opens the rule under the cursor ("${ruleTitle}")`,
          await until(
            pg,
            (want) =>
              document.querySelector('.kit-app > .kit-read:not([hidden]) h1')
                ?.textContent === want &&
              document.querySelector('.kit-primary')?.textContent ===
                'Activate',
            ruleTitle,
          ),
        );
        const ruleId = await pg.evaluate(
          () => document.querySelector('.cb-rule')?.dataset.rule,
        );
        await press(pg, 'a');
        check(
          `a activates the open draft (${ruleId}), the key Attention binds to accept`,
          await eventually(async () => {
            const r = await agent.api('GET', `/api/rule?id=${ruleId}`);
            return r.rule.status === 'active';
          }),
        );

        // g p: To apply. Its list selects (the decided items).
        await press(pg, 'g', 'p');
        check(
          'g p switches to To apply',
          await until(
            pg,
            () =>
              location.hash === '#/apply' &&
              document
                .querySelector('.kit-ctl[data-id="apply"]')
                ?.classList.contains('on'),
          ),
        );
        checkList(
          'on To apply the ? overlay lists exactly its keys',
          await overlayKeys(t, pg),
          [...EXPECT.apply].sort(),
        );
        await pg.click('.kit-chip[data-id="ready"]');
        await until(
          pg,
          () =>
            document.querySelectorAll('.cb-apply-list .kit-row[data-key]')
              .length >= 2,
        );
        const applyRows = await rowsOf(pg);
        const firstItem = applyRows.findIndex((r) => r.id);
        const applyCur = await curOf(pg);
        await press(
          pg,
          ...Array(Math.max(0, applyCur - firstItem)).fill('k'),
          ...Array(Math.max(0, firstItem - applyCur)).fill('j'),
          'x',
        );
        check(
          `x selects the To apply row under the cursor (${applyRows[firstItem]?.id}): "Plan 1"`,
          await until(
            pg,
            () =>
              document.querySelector('.kit-primary')?.textContent === 'Plan 1',
          ),
        );
        const applyCurNow = await curOf(pg);

        // g a: back to Attention; its list has the keys again.
        await press(pg, 'g', 'a');
        check(
          'g a switches back to Attention',
          await until(
            pg,
            () =>
              location.hash === '#/attention' &&
              document
                .querySelector('.kit-ctl[data-id="attention"]')
                ?.classList.contains('on'),
          ),
        );
        await until(
          pg,
          () =>
            document.querySelectorAll(
              '.kit-app > .kit-list:not([hidden]) .kit-row',
            ).length === 2,
        );
        await press(pg, 'k');
        const back0 = await curOf(pg);
        await press(pg, 'j');
        const back1 = await curOf(pg);
        check(
          `back on Attention, k and j move its cursor (${back0} → ${back1}) and not To apply's`,
          back0 === 0 &&
            back1 === 1 &&
            (await curOf(pg, '.kit-app > .kit-list.cb-apply-list')) ===
              applyCurNow,
        );

        // The board is not a list: only d (the shared selection) works there.
        await pg.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await until(pg, () => !!document.querySelector('.cb-board'));
        checkList(
          'on the board the ? overlay lists exactly its keys (its search, d; no list keys)',
          await overlayKeys(t, pg),
          [...EXPECT.board].sort(),
        );

        // The theme is remembered across a reload.
        for (const theme of ['light', 'dark']) {
          await setTheme(pg, theme);
          await pg.reload({ waitUntil: 'domcontentloaded' });
          await pg.waitForSelector('.kit-bar');
          const got = await pg.evaluate(() => ({
            theme: document.documentElement.dataset.theme,
            bg: getComputedStyle(document.body).backgroundColor,
          }));
          check(
            `the theme is remembered: set ${theme}, reload, still ${theme} (${got.theme}, body ${got.bg})`,
            got.theme === theme && got.bg === BG[theme],
          );
        }
        await setTheme(pg, 'light');
        check(`no page errors (${errors.join(' | ')})`, errors.length === 0);
      } finally {
        clearInterval(beat);
        await pg.close();
      }
    }),
  );
}

// ---- the poll fallback ---------------------------------------------------------

async function pollFallbackScenario(shared, t) {
  const { check, until } = t;
  console.log(
    '\nscenario: the poll fallback keeps state when the stream drops',
  );
  const FB = 'fb-poll';
  await withServe(
    {
      seedRepos: [
        seedRepo(FB, [
          [51, 'keep the cursor on reconnect', 'rhea'],
          [52, 'poll while the stream is down', 'rhea'],
        ]),
      ],
    },
    (serveHandle) =>
      withContext(shared, async (context) => {
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const pg = await context.newPage();
        try {
          await pg.setViewportSize({ width: 1600, height: 900 });
          await pg.goto(`${serveHandle.url}&q=${FB}#/attention/waiting`, {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          check(
            'the page is live with the two fb-poll PRs',
            await until(
              pg,
              () =>
                document.querySelectorAll(
                  '.kit-app > .kit-list:not([hidden]) .kit-row',
                ).length === 2 &&
                document.querySelector('.kit-live')?.dataset.state === 'live',
              undefined,
              10000,
            ),
          );
          const polls = [];
          // Poll requests as the page makes them (a response's body is read
          // later, so a poll asked before the stream came back can be
          // counted after it).
          let pollsAsked = 0;
          pg.on('request', (r) => {
            if (r.url().includes('/api/state?since=')) pollsAsked++;
          });
          pg.on('response', async (r) => {
            if (!r.url().includes('/api/state?since=')) return;
            const body = await r.json().catch(() => null);
            polls.push({
              status: r.status(),
              types: (body?.events ?? []).map((e) => e.type),
            });
          });
          // The stream drops (the browser stops it) and can't come back.
          await pg.route(/\/api\/events/, (r) => r.abort());
          await pg.evaluate(() => window.stop());
          const pill = () =>
            pg.$eval('.kit-live', (e) => ({
              state: e.dataset.state,
              text: e.textContent.trim(),
              shown: e.getBoundingClientRect().width > 0,
            }));
          check(
            'the stream drops: the pill reads "polling"',
            await until(pg, () => {
              const e = document.querySelector('.kit-live');
              return (
                e?.dataset.state === 'polling' &&
                e.textContent.trim() === 'polling'
              );
            }),
          );
          await agent.api('POST', '/api/decide', {
            keys: [`pr:schuettc/${FB}#51`],
            disposition: 'keep',
          });
          check(
            'a decision made meanwhile reaches the page: its row leaves the list',
            await until(
              pg,
              () =>
                document.querySelectorAll(
                  '.kit-app > .kit-list:not([hidden]) .kit-row',
                ).length === 1,
              undefined,
              8000,
            ),
          );
          check(
            `through /api/state?since=<cursor>: a poll carried the decided event (${polls.map((p) => `${p.status} [${p.types.join(' ')}]`).join(', ')})`,
            polls.some((p) => p.status === 200 && p.types.includes('decided')),
          );
          const during = await pill();
          check(
            `while polling, the pill still reads "polling" (${during.state} "${during.text}")`,
            during.shown &&
              during.state === 'polling' &&
              during.text === 'polling',
          );
          // The stream can come back: the page reconnects on its own.
          await pg.unroute(/\/api\/events/);
          check(
            'the stream comes back: the pill reads "live" again',
            await until(
              pg,
              () => {
                const e = document.querySelector('.kit-live');
                return (
                  e?.dataset.state === 'live' && e.textContent.trim() === 'live'
                );
              },
              undefined,
              8000,
            ),
          );
          // A poll already asked as the stream came back may still finish;
          // after it, no poll is asked again.
          await pg.waitForTimeout(1000);
          const pollsAt = pollsAsked;
          await agent.api('POST', '/api/decide', {
            keys: [`pr:schuettc/${FB}#52`],
            disposition: 'keep',
          });
          check(
            'live again, the stream delivers (the last row leaves)',
            await until(
              pg,
              () =>
                document.querySelectorAll(
                  '.kit-app > .kit-list:not([hidden]) .kit-row',
                ).length === 0,
            ),
          );
          // Polling runs every 2 s: longer than that without one, it stopped.
          await pg.waitForTimeout(2500);
          check(
            `and polling has stopped (${pollsAsked - pollsAt} polls asked since)`,
            pollsAsked === pollsAt,
          );
        } finally {
          await pg.close();
        }
      }),
  );
}

// ---- serve isn't answering: the disconnected banner ---------------------------

async function downScenario(shared, t) {
  const { check, until } = t;
  console.log(
    "\nscenario: serve isn't answering — the disconnected banner and retry",
  );
  await withServe({ seedRepos: [dormant('down-probe')] }, (serveHandle) =>
    withContext(shared, async (context) => {
      const pg = await context.newPage();
      try {
        await pg.setViewportSize({ width: 1600, height: 900 });
        // A fake clock: the page's 2 s poll runs only when the probe says,
        // so a reconnect is either the retry's or the poll's, never a race.
        await pg.clock.install();
        // The bar's counts and status are asked for as the page boots; here
        // that answer never comes (as when serve goes away mid-boot).
        const SUMMARY = /\/api\/summary/;
        let summaries = 0;
        pg.on('request', (q) => {
          if (SUMMARY.test(q.url())) summaries++;
        });
        await pg.route(SUMMARY, (r) => r.abort());
        await pg.goto(serveHandle.url + '#/attention/waiting', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await until(
          pg,
          () => document.querySelector('.kit-live')?.dataset.state === 'live',
          undefined,
          10000,
        );
        await pg.clock.pauseAt(Date.now() + 1000);
        // fills: the sections fill the page from the bar (or the banner,
        // when shown) to the bottom of the window.
        const fills = () =>
          pg.evaluate(() => {
            const r = (s) =>
              document.querySelector(s)?.getBoundingClientRect() ?? null;
            const bar = r('.kit-bar');
            const b = r('[data-testid="down-banner"]');
            const top = b && b.height > 0 ? b.bottom : bar.bottom;
            const vh = document.documentElement.clientHeight;
            const cols = [
              r('.kit-app > .kit-list:not([hidden])'),
              r('.kit-app > .kit-read:not([hidden])'),
              r('.kit-app > .kit-rail'),
            ];
            const ok = cols.every(
              (c) =>
                c &&
                Math.abs(c.top - top) < 0.5 &&
                Math.abs(c.bottom - vh) < 0.5,
            );
            return {
              ok,
              says: cols
                .map((c) => (c ? `${c.top}–${c.bottom}` : 'none'))
                .join(', ')
                .concat(` (from ${top} to ${vh})`),
            };
          });
        const f0 = await fills();
        check(
          `connected, there is no banner, and list, reading column and dock fill the page under the bar: ${f0.says}`,
          (await pg.$eval('[data-testid="down-banner"]', (e) => e.hidden)) &&
            f0.ok,
        );
        const barNow = () =>
          pg.evaluate(() => ({
            attention:
              document.querySelector('.kit-ctl[data-id="attention"] .kit-n')
                ?.textContent ?? '',
            apply:
              document.querySelector('.kit-ctl[data-id="apply"] .kit-n')
                ?.textContent ?? '',
            status: document.querySelector('.kit-status')?.textContent ?? '',
          }));
        const bar0 = await barNow();
        check(
          `the boot's summary failed (${summaries} asked): no counts, no status ("${bar0.attention}", "${bar0.apply}", "${bar0.status}")`,
          summaries > 0 && !bar0.attention && !bar0.apply && !bar0.status,
        );
        await pg.unroute(SUMMARY);
        const EVENTS = /\/api\/events/;
        const STATE = /\/api\/state/;
        const cut = async () => {
          await pg.route(EVENTS, (r) => r.abort());
          await pg.route(STATE, (r) => r.abort());
          await pg.evaluate(() => window.stop());
          await until(
            pg,
            () =>
              document.querySelector('.kit-live')?.dataset.state === 'polling',
          );
          await pg.clock.runFor(2100);
          return until(
            pg,
            () =>
              document.querySelector('.kit-live')?.dataset.state === 'down' &&
              !document.querySelector('[data-testid="down-banner"]').hidden,
          );
        };
        check(
          'serve stops answering (stream and poll): the page goes down',
          await cut(),
        );
        const danger = await cssColor(pg, 'var(--kit-danger)');
        const bgPage = await cssColor(pg, 'var(--kit-bg)', 'background-color');
        const look = await pg.evaluate(() => {
          const b = document.querySelector('[data-testid="down-banner"]');
          const bar = document
            .querySelector('.kit-bar')
            .getBoundingClientRect();
          const list = document
            .querySelector('.kit-app > .kit-list:not([hidden])')
            .getBoundingClientRect();
          const r = b.getBoundingClientRect();
          const cs = getComputedStyle(b);
          const retry = b.querySelector('[data-testid="down-retry"]');
          const pill = document.querySelector('.kit-live');
          return {
            top: r.top,
            left: r.left,
            width: r.width,
            height: r.height,
            barBottom: bar.bottom,
            listTop: list.top,
            vw: document.documentElement.clientWidth,
            color: cs.color,
            border: `${cs.borderBottomStyle} ${cs.borderBottomColor}`,
            bg: cs.backgroundColor,
            text: b.querySelector('.cb-down-text')?.textContent ?? '',
            retryTag: retry?.tagName,
            retryText: retry?.textContent,
            retryShown: retry ? retry.getBoundingClientRect().width > 0 : false,
            pill: `${pill.dataset.state} ${pill.textContent.trim()}`,
          };
        });
        check(
          `the pill reads "disconnected" (${look.pill})`,
          look.pill === 'down disconnected',
        );
        check(
          `the banner runs across the page under the bar (top ${look.top} = bar bottom ${look.barBottom}, left ${look.left}, ${look.width}×${look.height} of ${look.vw})`,
          Math.abs(look.top - look.barBottom) < 0.5 &&
            look.left === 0 &&
            Math.abs(look.width - look.vw) < 0.5 &&
            look.height >= 24 &&
            look.height <= 48,
        );
        check(
          `and the sections sit below it, not under it (list top ${look.listTop})`,
          Math.abs(look.listTop - (look.top + look.height)) < 0.5,
        );
        check(
          `it is in danger, the error colour (text ${look.color}, rule ${look.border}, ground ${look.bg})`,
          look.color === danger &&
            look.border === `solid ${danger}` &&
            look.bg !== bgPage &&
            look.bg !== 'rgba(0, 0, 0, 0)',
        );
        check(
          `it says "disconnected", names no command, and its retry is a button ("${look.text}"; ${look.retryTag} "${look.retryText}")`,
          look.text.startsWith('disconnected') &&
            !/`|\$|\bcasebook (serve|sync)\b|\bgit |\bgh /.test(look.text) &&
            look.retryTag === 'BUTTON' &&
            look.retryText === 'retry' &&
            look.retryShown,
        );
        // serve answers again; the clock stands still, so only the retry
        // can reconnect now.
        await pg.unroute(EVENTS);
        await pg.unroute(STATE);
        const asked = summaries;
        await pg.click('[data-testid="down-retry"]');
        check(
          'retry reconnects at once: the banner goes and the pill reads "live"',
          await until(
            pg,
            () =>
              document.querySelector('[data-testid="down-banner"]').hidden &&
              document.querySelector('.kit-live')?.dataset.state === 'live' &&
              document.querySelector('.kit-live')?.textContent.trim() ===
                'live',
          ),
        );

        // Back, the page asks for what it missed: the bar's counts and the
        // status, as serve has them.
        const sum = await createAgent(serveHandle.base, serveHandle.token).api(
          'GET',
          '/api/summary',
        );
        const want = {
          attention: String(sum.counts?.all ?? 0),
          apply: String(sum.counts?.['to-apply'] ?? 0),
        };
        const got = await until(
          pg,
          (w) =>
            document.querySelector('.kit-ctl[data-id="attention"] .kit-n')
              ?.textContent === w.attention &&
            document.querySelector('.kit-ctl[data-id="apply"] .kit-n')
              ?.textContent === w.apply &&
            /^synced \d+m ago · probe$/.test(
              document.querySelector('.kit-status')?.textContent ?? '',
            ),
          want,
        );
        const bar1 = await barNow();
        const f1 = await fills();
        check(
          `and the sections fill the page under the bar again: ${f1.says}`,
          f1.ok,
        );
        check(
          `reconnected, the page asks for the summary it missed (${summaries - asked} asked): attention ${bar1.attention}, to apply ${bar1.apply}, "${bar1.status}"`,
          got && summaries > asked,
        );
        // "synced Nm ago" follows the clock, with no request (the clock is
        // the page's: three minutes on, three more minutes ago).
        const mins = (st) => Number(/^synced (\d+)m ago/.exec(st)?.[1] ?? NaN);
        const m0 = mins(bar1.status);
        const askedBefore = summaries;
        await pg.clock.runFor(3 * 60000);
        const st3 = await until(
          pg,
          (w) =>
            document.querySelector('.kit-status')?.textContent ===
            `synced ${w}m ago · probe`,
          m0 + 3,
        );
        check(
          `three minutes on, the status reads "synced ${m0 + 3}m ago · probe" (was ${m0}m), asking serve nothing`,
          Number.isFinite(m0) && st3 && summaries === askedBefore,
        );

        // Down again; this time the page's own poll finds serve back.
        check('down again', await cut());
        // The retry is neutral (not danger: it destroys nothing) and stands
        // out from the banner's ground, in both themes: its border has at
        // least 3:1 contrast with the ground (WCAG non-text contrast), and
        // its own ground differs from the banner's.
        const dangerNow = async () => cssColor(pg, 'var(--kit-danger)');
        for (const theme of ['light', 'dark']) {
          await setTheme(pg, theme);
          const c = await pg.evaluate(() => {
            const px = (css) => {
              const cv = document.createElement('canvas');
              cv.width = cv.height = 1;
              const x = cv.getContext('2d');
              x.fillStyle = '#000';
              x.fillStyle = css;
              x.fillRect(0, 0, 1, 1);
              return [...x.getImageData(0, 0, 1, 1).data];
            };
            const lum = ([r, g, b]) => {
              const f = (v) => {
                v /= 255;
                return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
              };
              return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
            };
            const ratio = (a, b) => {
              const [hi, lo] = [lum(a), lum(b)].sort((m, n) => n - m);
              return (hi + 0.05) / (lo + 0.05);
            };
            const banner = document.querySelector(
              '[data-testid="down-banner"]',
            );
            const btn = banner.querySelector('[data-testid="down-retry"]');
            const bs = getComputedStyle(btn);
            const ground = px(getComputedStyle(banner).backgroundColor);
            const border = px(bs.borderTopColor);
            const fill = px(bs.backgroundColor);
            return {
              border: bs.borderTopColor,
              color: bs.color,
              borderRatio: ratio(border, ground),
              fillRatio: ratio(fill, ground),
              fillAlpha: fill[3],
            };
          });
          const danger = await dangerNow();
          check(
            `${theme}: the retry is neutral and clearly visible on the banner (border ${c.border} at ${c.borderRatio.toFixed(2)}:1 to the ground, its own ground ${c.fillRatio.toFixed(2)}:1, text ${c.color})`,
            c.borderRatio >= 3 &&
              c.fillAlpha === 255 &&
              c.fillRatio > 1.05 &&
              c.border !== danger &&
              c.color !== danger,
          );
        }
        // The page down with its counts and status: light and dark.
        await shoot(t, pg, 'down');
        // serve answers the poll, but the stream stays cut: serve is back,
        // the banner goes, and the pill says the page is polling.
        await pg.unroute(STATE);
        await pg.clock.runFor(2100);
        check(
          'a poll that answers clears the banner on its own; the stream still cut, the pill reads "polling"',
          await until(pg, () => {
            const e = document.querySelector('.kit-live');
            return (
              document.querySelector('[data-testid="down-banner"]').hidden &&
              e?.dataset.state === 'polling' &&
              e.textContent.trim() === 'polling'
            );
          }),
        );
        await pg.unroute(EVENTS);
        await pg.clock.runFor(2100);
        check(
          'and when the stream can connect again the pill reads "live"',
          await until(
            pg,
            () =>
              document.querySelector('[data-testid="down-banner"]').hidden &&
              document.querySelector('.kit-live')?.dataset.state === 'live',
          ),
        );
      } finally {
        await pg.close();
      }
    }),
  );
}

// ---- a restarted serve: the stale pill ---------------------------------------

const STALE_TEXT = 'restarted \u00b7 continued in a new tab';

async function staleScenario(shared, t) {
  const { check, until } = t;
  console.log('\nscenario: a restarted serve shows the stale pill and stops');
  await withServe({ seedRepos: [dormant('stale-probe')] }, (serveHandle) =>
    withContext(shared, async (context) => {
      const pg = await context.newPage();
      const errors = [];
      pg.on('pageerror', (e) => errors.push(String(e)));
      try {
        await pg.setViewportSize({ width: 1600, height: 900 });
        await pg.clock.install();
        await pg.goto(serveHandle.url + '#/attention/waiting', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await until(
          pg,
          () => document.querySelector('.kit-live')?.dataset.state === 'live',
          undefined,
          10000,
        );
        await setTheme(pg, 'light');
        await pg.clock.pauseAt(Date.now() + 1000);
        const reqs = [];
        let first401 = -1;
        pg.on('request', (q) => {
          if (new URL(q.url()).pathname.startsWith('/api/')) reqs.push(q.url());
        });
        let first401Url = '';
        pg.on('response', (r) => {
          if (r.status() === 401 && first401 < 0) {
            first401 = reqs.length;
            first401Url = new URL(r.url()).pathname;
          }
        });
        const pagesBefore = context.pages().length;

        // serve restarts on the same port: this tab's token died with it.
        const before = new URL(serveHandle.base).port;
        const adv = await serveHandle.restart();
        check(
          `serve restarts on its port (${before} → ${new URL(adv.base).port}), and knows a tab was open (reopened: ${adv.reopened})`,
          new URL(adv.base).port === before && adv.reopened === true,
        );
        check(
          'the stream drops: the pill reads "polling" first',
          await until(
            pg,
            () =>
              document.querySelector('.kit-live')?.dataset.state === 'polling',
          ),
        );
        await pg.clock.runFor(2100);
        const staleNow = await until(
          pg,
          (w) => {
            const e = document.querySelector('.kit-live');
            return e?.dataset.state === 'stale' && e.textContent.trim() === w;
          },
          STALE_TEXT,
        );
        const pill = await pg.$eval('.kit-live', (e) => ({
          text: e.textContent.trim(),
          shown: e.getBoundingClientRect().width > 0,
          color: getComputedStyle(e).color,
        }));
        const muted = await cssColor(pg, 'var(--kit-muted)');
        check(
          `a 401 flips the pill to stale: "${pill.text}", muted (${pill.color})`,
          staleNow && pill.shown && pill.color === muted,
        );
        check(
          `the 401 came, from the live poll (${first401 >= 0 ? `after ${first401} requests: ${first401Url}` : 'none'})`,
          first401 >= 0 && first401Url === '/api/state',
        );

        // The tab stops: over a minute and more, and with Court clicking a
        // view and switching sections, no request leaves it.
        await pg.clock.runFor(60000);
        await pg.click('.kit-chip[data-id="new"]');
        await press(pg, 'g', 'p');
        await pg.clock.runFor(60000);
        await pg.waitForTimeout(500);
        const after = first401 < 0 ? reqs : reqs.slice(first401);
        check(
          `no further calls fire after the 401 over two minutes, a click and a switch (${after.length}: ${after.slice(0, 4).join(' ')})`,
          first401 >= 0 && after.length === 0,
        );
        const still = await pg.$eval(
          '.kit-live',
          (e) => `${e.dataset.state} ${e.textContent.trim()}`,
        );
        check(
          `and the pill still says so (${still})`,
          still === `stale ${STALE_TEXT}`,
        );
        check(
          `the restarted serve opened no tab here (CASEBOOK_NO_BROWSER: ${context.pages().length} pages, was ${pagesBefore})`,
          context.pages().length === pagesBefore,
        );
        await press(pg, 'g', 'a');
        await pg.evaluate(() => document.activeElement?.blur());
        const shot = await pg.evaluate(() => ({
          theme: document.documentElement.dataset.theme,
          bg: getComputedStyle(document.body).backgroundColor,
          blurred: document.activeElement === document.body,
        }));
        check(
          `/tmp/t10-stale-light.png is light (theme ${shot.theme}, body ${shot.bg}), nothing focused`,
          shot.theme === 'light' && shot.bg === BG.light && shot.blurred,
        );
        await pg.screenshot({ path: '/tmp/t10-stale-light.png' });
        check(`no page errors (${errors.join(' | ')})`, errors.length === 0);
      } finally {
        await pg.close();
      }
    }),
  );
}

// ---- decisions queued offline ---------------------------------------------------

async function offlineQueuedScenario(shared, t) {
  const { check, until, eventually } = t;
  console.log(
    '\nscenario: decisions queued offline show as "offline · N queued"',
  );
  const OQ = 'oq-probe';
  await withServe(
    {
      seedRepos: [
        seedRepo(OQ, [
          [61, 'queue the first decision', 'sol'],
          [62, 'queue the second decision', 'sol'],
        ]),
      ],
    },
    (serveHandle) =>
      withContext(shared, async (context) => {
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const pg = await context.newPage();
        try {
          await pg.setViewportSize({ width: 1600, height: 900 });
          await pg.goto(serveHandle.url + '#/attention/waiting', {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          await until(
            pg,
            () =>
              document.querySelector('.kit-live')?.dataset.state === 'live' &&
              /^synced \d+m ago · probe$/.test(
                document.querySelector('.kit-status')?.textContent ?? '',
              ),
            undefined,
            10000,
          );
          const status = () =>
            pg.$eval('.kit-status', (e) => ({
              text: e.textContent,
              tone: e.dataset.tone,
              color: getComputedStyle(e).color,
            }));
          const s0 = await status();
          const muted = await cssColor(pg, 'var(--kit-muted)');
          check(
            `nothing queued: the status reads "${s0.text}", muted`,
            s0.color === muted && !s0.text.startsWith('offline'),
          );
          const danger = await cssColor(pg, 'var(--kit-danger)');
          for (const [n, pr] of [
            [1, 61],
            [2, 62],
          ]) {
            await agent.api('POST', '/api/decide', {
              keys: [`pr:schuettc/${OQ}#${pr}`],
              disposition: 'keep',
            });
            // serve answers before its push to the casebook remote; the
            // count is the push's to say once it has failed.
            let sum = {};
            await eventually(async () => {
              sum = await agent.api('GET', '/api/summary');
              return sum.offline_queued === n;
            });
            const shown = await until(
              pg,
              (w) => document.querySelector('.kit-status')?.textContent === w,
              `offline \u00b7 ${n} queued`,
            );
            const s = await status();
            check(
              `a decision serve can't push yet (offline_queued ${sum.offline_queued}) shows live, without a reload: "${s.text}" in danger (${s.color})`,
              sum.offline_queued === n &&
                shown &&
                s.tone === 'danger' &&
                s.color === danger,
            );
          }
          const strip = await pg.$eval('[data-testid="push-error"]', (e) => ({
            hidden: e.hidden,
            height: e.getBoundingClientRect().height,
          }));
          const sum = await agent.api('GET', '/api/summary');
          check(
            `plain offline is no push failure: no push_error (${JSON.stringify(sum.push_error ?? null)}) and no strip (hidden ${strip.hidden}, ${strip.height} px)`,
            !sum.push_error && strip.hidden && strip.height === 0,
          );
        } finally {
          await pg.close();
        }
      }),
  );
}

// ---- a slow push ------------------------------------------------------------------

// slowPushScenario: serve answers a decide once the decision is committed
// and the index rebuilt; the push to the casebook remote follows. Here the
// remote (a bare repo in the temp home) holds every push in its pre-receive
// hook until released: the decide sheet closes, and the keys come back,
// while the push is held, and the status doesn't call it offline.
async function slowPushScenario(shared, t) {
  const { check, until, eventually } = t;
  console.log("\nscenario: a slow push doesn't hold the decide sheet");
  const SP = 'sp-probe';
  const KEY = `pr:schuettc/${SP}#71`;
  await withServe(
    {
      slowRemote: true,
      seedRepos: [
        seedRepo(SP, [
          [71, 'decide while the push is held', 'uma'],
          [72, 'stay in the list', 'uma'],
        ]),
      ],
    },
    (serveHandle) =>
      withContext(shared, async (context) => {
        const remote = `${serveHandle.home}/remotes/casebook-data.git`;
        const remoteLog = () =>
          probeGit(remote, 'log --format=%s main', serveHandle.home);
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const sum0 = await agent.api('GET', '/api/summary');
        check(
          `the slow remote has everything at the start (offline_queued ${sum0.offline_queued})`,
          sum0.offline_queued === 0,
        );
        const pg = await context.newPage();
        try {
          await pg.setViewportSize({ width: 1600, height: 900 });
          await pg.goto(`${serveHandle.url}&q=${SP}#/attention/waiting`, {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          const two = await until(
            pg,
            (sel) => document.querySelectorAll(`${sel} .kit-row`).length === 2,
            SHOWN,
            15000,
          );
          const rows = await rowsOf(pg);
          const at = rows.findIndex((r) =>
            r.title.includes('decide while the push is held'),
          );
          check(
            `two rows, the one to decide among them (row ${at})`,
            two && at >= 0,
          );
          // Put the cursor on it and select it.
          for (let i = 0; i < 4 && (await curOf(pg)) !== at; i++) {
            const cur = await curOf(pg);
            await press(pg, cur < at ? 'j' : 'k');
          }
          await press(pg, 'x');
          check(
            'x selects it: "Decide 1"',
            await until(
              pg,
              () =>
                document.querySelector('.kit-primary')?.textContent ===
                'Decide 1',
            ),
          );
          await pg.click('.kit-primary');
          await until(pg, () => !!document.querySelector('.kit-sheet'));
          await pg.click('.kit-sheet .cb-choice[data-d="keep"]');
          const t0 = Date.now();
          await pg.click('.kit-sheet .kit-btn.fill:has-text("· 1 ")');
          const closed = await until(
            pg,
            () => !document.querySelector('.kit-sheet'),
            undefined,
            3000,
          );
          const ms = Date.now() - t0;
          const held = await eventually(async () =>
            serveHandle.pushGate.started(),
          );
          const onRemote = remoteLog().includes(`decide ${KEY}`);
          check(
            `the decide sheet closes on serve's answer (${ms} ms) while the push is held (held ${held}, on the remote ${onRemote})`,
            closed && held && !onRemote && ms < 3000,
          );
          const v = await agent.api(
            'GET',
            `/api/item?key=${encodeURIComponent(KEY)}`,
          );
          check(
            `serve has it decided (keep) while its push is held (${v?.item?.decision?.disposition})`,
            v?.item?.decision?.disposition === 'keep' &&
              serveHandle.pushGate.started() &&
              !remoteLog().includes(`decide ${KEY}`),
          );
          await pg.waitForTimeout(300);
          const during = await pg.$eval('.kit-status', (e) => e.textContent);
          check(
            `a push on its way isn't offline: the status reads "${during}"`,
            /^synced \d+m ago · probe$/.test(during ?? ''),
          );
          await press(pg, '?');
          const keysBack = await until(
            pg,
            () => !!document.querySelector('.kit-keys'),
          );
          await pg.keyboard.press('Escape');
          check(
            `the keys work again while the push is held (? opens the overlay: ${keysBack})`,
            keysBack && !remoteLog().includes(`decide ${KEY}`),
          );
          serveHandle.pushGate.release();
          check(
            'released, the push puts the decision on the remote',
            await eventually(
              async () => remoteLog().includes(`decide ${KEY}`),
              10000,
            ),
          );
          const after = await eventually(async () => {
            const sum = await agent.api('GET', '/api/summary');
            return sum.offline_queued === 0;
          });
          const status = await pg.$eval('.kit-status', (e) => e.textContent);
          check(
            `and nothing is queued after it (status "${status}")`,
            after && /^synced \d+m ago · probe$/.test(status ?? ''),
          );
        } finally {
          await pg.close();
        }
      }),
  );
}

// ---- push failed ---------------------------------------------------------------

// pushFailedScenario: serve's push fails for a reason other than the
// network (here a rebase conflict in a file casebook doesn't resolve, set up
// on a local bare remote by serve.mjs's conflictRemote). The status says
// "push failed · N queued" in danger, and a strip under the bar says why in
// serve's words, wrapping so the whole reason (and its path) shows. Plain
// queued commits (nothing has failed yet) read "offline · N queued" with no
// strip, and a push that succeeds takes the strip away.
async function pushFailedScenario(shared, t) {
  const { check, until, eventually } = t;
  console.log('\nscenario: a push that fails says so, and why');
  const PF = 'pf-probe';
  await withServe(
    {
      conflictRemote: true,
      seedRepos: [
        seedRepo(PF, [
          [81, 'decide into a conflict', 'vic'],
          [82, 'decide once it clears', 'vic'],
        ]),
      ],
    },
    (serveHandle) =>
      withContext(shared, async (context) => {
        const remote = `${serveHandle.home}/remotes/casebook-data.git`;
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const pg = await context.newPage();
        try {
          await pg.setViewportSize({ width: 1600, height: 900 });
          await pg.goto(`${serveHandle.url}&q=${PF}#/attention/waiting`, {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          const look = () =>
            pg.evaluate(() => {
              const st = document.querySelector('.kit-status');
              const strip = document.querySelector(
                '[data-testid="push-error"]',
              );
              const down = document.querySelector(
                '[data-testid="down-banner"]',
              );
              const r = strip.getBoundingClientRect();
              const cs = getComputedStyle(strip);
              const bar = document
                .querySelector('.kit-bar')
                .getBoundingClientRect();
              const list = document
                .querySelector('.kit-app > .kit-list:not([hidden])')
                ?.getBoundingClientRect();
              const text = strip.querySelector('.cb-down-text');
              const lh = parseFloat(getComputedStyle(text).lineHeight) || 20;
              // The disconnected banner's look, shown for a moment to read it.
              down.hidden = false;
              const dcs = getComputedStyle(down);
              const downLook = `${dcs.color} ${dcs.backgroundColor} ${dcs.borderBottomStyle} ${dcs.borderBottomColor} ${dcs.font}`;
              down.hidden = true;
              return {
                status: st?.textContent ?? '',
                tone: st?.dataset.tone,
                statusColor: st ? getComputedStyle(st).color : '',
                hidden: strip.hidden,
                role: strip.getAttribute('role'),
                text: text?.textContent ?? '',
                title: strip.title,
                top: r.top,
                height: r.height,
                // The whole text shows: nothing scrolls or clips sideways in
                // the strip or its text, and the text box sits inside it.
                fits:
                  strip.scrollWidth <= strip.clientWidth &&
                  text.scrollWidth <= text.clientWidth &&
                  text.getBoundingClientRect().right <= r.right + 0.5 &&
                  text.getBoundingClientRect().bottom <= r.bottom + 0.5,
                lines: Math.round(text.getBoundingClientRect().height / lh),
                barBottom: bar.bottom,
                listTop: list?.top ?? -1,
                look: `${cs.color} ${cs.backgroundColor} ${cs.borderBottomStyle} ${cs.borderBottomColor} ${cs.font}`,
                downLook,
              };
            });
          const q0 = await until(
            pg,
            () =>
              document.querySelector('.kit-status')?.textContent ===
              'offline \u00b7 1 queued',
            undefined,
            10000,
          );
          const l0 = await look();
          check(
            `a commit queued before any push failed reads "${l0.status}", with no strip (hidden ${l0.hidden})`,
            q0 && l0.hidden && l0.height === 0,
          );

          await agent.api('POST', '/api/decide', {
            keys: [`pr:schuettc/${PF}#81`],
            disposition: 'keep',
          });
          let sum = {};
          const failed = await eventually(async () => {
            sum = await agent.api('GET', '/api/summary');
            return !!sum.push_error && sum.offline_queued === 2;
          }, 10000);
          check(
            `serve's push fails on the conflict: push_error "${sum.push_error}", ${sum.offline_queued} queued`,
            failed && /conflict in notes\.txt/.test(sum.push_error ?? ''),
          );
          const shown = await until(
            pg,
            () =>
              document.querySelector('.kit-status')?.textContent ===
                'push failed \u00b7 2 queued' &&
              !document.querySelector('[data-testid="push-error"]').hidden,
            undefined,
            10000,
          );
          const danger = await cssColor(pg, 'var(--kit-danger)');
          const l1 = await look();
          check(
            `the status reads "${l1.status}" in danger (${l1.tone}, ${l1.statusColor}), live`,
            shown &&
              l1.status === 'push failed \u00b7 2 queued' &&
              l1.tone === 'danger' &&
              l1.statusColor === danger,
          );
          check(
            `an alert strip under the bar says why, in serve's words, all of it visible (fits ${l1.fits}, ${l1.lines} lines; list starts under it): "${l1.text}"`,
            !l1.hidden &&
              l1.role === 'alert' &&
              l1.text === `push failed \u00b7 ${sum.push_error}` &&
              l1.title === sum.push_error &&
              l1.fits &&
              l1.text.endsWith(sum.push_error) &&
              Math.abs(l1.top - l1.barBottom) < 0.5 &&
              Math.abs(l1.listTop - (l1.top + l1.height)) < 0.5,
          );
          check(
            `the strip looks like the disconnected banner (${l1.look} vs ${l1.downLook})`,
            l1.look === l1.downLook,
          );
          check(
            `the strip gives Court no command to run ("${l1.text}")`,
            !/`|\brun\b|casebook (sync|decide|push)|git /.test(l1.text),
          );
          await pg.evaluate(() => document.activeElement?.blur());
          await pg.screenshot({ path: '/tmp/fr1-push-failed.png' });
          console.log('  screenshot: /tmp/fr1-push-failed.png');

          // The remote moves back to the base both share: the next push
          // goes through, and the failure goes with it.
          probeGit(
            remote,
            'update-ref refs/heads/main main~1',
            serveHandle.home,
          );
          await agent.api('POST', '/api/decide', {
            keys: [`pr:schuettc/${PF}#82`],
            disposition: 'keep',
          });
          const cleared = await eventually(async () => {
            sum = await agent.api('GET', '/api/summary');
            return !sum.push_error && sum.offline_queued === 0;
          }, 10000);
          const gone = await until(
            pg,
            () =>
              document.querySelector('[data-testid="push-error"]').hidden &&
              /^synced \d+m ago · probe$/.test(
                document.querySelector('.kit-status')?.textContent ?? '',
              ),
            undefined,
            10000,
          );
          const l2 = await look();
          check(
            `a push that succeeds clears it: no push_error, nothing queued, no strip, status "${l2.status}"`,
            cleared && gone && l2.height === 0,
          );
        } finally {
          await pg.close();
        }
      }),
  );
}

// ---- a restarted serve: the first 401 from an API call ---------------------------

// staleApiScenario: Court clicks in the 2 s before the live client's next
// poll. The first 401 is then an API call's (createApi's onStale), not the
// poll's: the tab goes stale at once, and nothing leaves it after.
async function staleApiScenario(shared, t) {
  const { check, until } = t;
  console.log(
    '\nscenario: a restarted serve — the first 401 from an API call makes the tab stale',
  );
  await withServe({ seedRepos: [dormant('stale-api-probe')] }, (serveHandle) =>
    withContext(shared, async (context) => {
      const pg = await context.newPage();
      const errors = [];
      pg.on('pageerror', (e) => errors.push(String(e)));
      try {
        await pg.setViewportSize({ width: 1600, height: 900 });
        await pg.clock.install();
        await pg.goto(serveHandle.url + '#/attention/waiting', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await until(
          pg,
          () => document.querySelector('.kit-live')?.dataset.state === 'live',
          undefined,
          10000,
        );
        // The page's clock stands still: no poll runs until the probe says.
        await pg.clock.pauseAt(Date.now() + 1000);
        const reqs = [];
        const u401 = [];
        pg.on('request', (q) => {
          const path = new URL(q.url()).pathname;
          if (path.startsWith('/api/')) reqs.push(path);
        });
        pg.on('response', (r) => {
          if (r.status() === 401)
            u401.push({ at: reqs.length, path: new URL(r.url()).pathname });
        });
        await serveHandle.restart();
        await until(
          pg,
          () =>
            document.querySelector('.kit-live')?.dataset.state === 'polling',
        );
        const beforeClick = reqs.length;
        // Court clicks a view: the API call meets the restarted serve.
        await pg.click('.kit-chip[data-id="new"]');
        const staleNow = await until(
          pg,
          (w) => {
            const e = document.querySelector('.kit-live');
            return e?.dataset.state === 'stale' && e.textContent.trim() === w;
          },
          STALE_TEXT,
        );
        const first = u401[0];
        check(
          `the first 401 is the click's API call, not a poll (${first ? `${first.path} after ${first.at - beforeClick} requests` : 'none'}; requests since the restart: ${reqs.slice(beforeClick).join(' ')})`,
          !!first &&
            first.path === '/api/items' &&
            !reqs.includes('/api/state'),
        );
        check(
          `and at once, before any poll, the pill reads "${STALE_TEXT}"`,
          staleNow && !reqs.includes('/api/state'),
        );
        const at = reqs.length;
        await pg.clock.runFor(60000);
        await pg.click('.kit-chip[data-id="waiting"]');
        await press(pg, 'g', 'r');
        await pg.clock.runFor(60000);
        await pg.waitForTimeout(500);
        const after = reqs.slice(at);
        check(
          `no further calls fire over two minutes, a click and a switch (${after.length}: ${after.slice(0, 4).join(' ')})`,
          after.length === 0,
        );
        check(
          `and the pill still says so (${await pg.$eval('.kit-live', (e) => `${e.dataset.state} ${e.textContent.trim()}`)})`,
          (await pg.$eval(
            '.kit-live',
            (e) => `${e.dataset.state} ${e.textContent.trim()}`,
          )) === `stale ${STALE_TEXT}`,
        );
        check(`no page errors (${errors.join(' | ')})`, errors.length === 0);
      } finally {
        await pg.close();
      }
    }),
  );
}

// ---- a large view: 200 rows and "show more" -----------------------------------

// pageCapScenario: a view with 203 items renders 200 rows (the kit is tested
// to 500; the first triage was 1,567), and the list's own "show 3 more"
// brings the rest.
async function pageCapScenario(shared, t) {
  const { check, checkList, until } = t;
  console.log('\nscenario: a large view renders 200 rows and a "show more"');
  const BIG = 'cap-probe';
  const N = 203;
  const prs = [];
  for (let i = 0; i < N; i++) {
    const day = new Date(Date.UTC(2026, 3, 1) + i * 3600000).toISOString();
    prs.push({
      repo: `schuettc/${BIG}`,
      number: 1000 + i,
      title: `capped change ${1000 + i}`,
      author: 'ivo',
      state: 'OPEN',
      created_at: day,
      updated_at: day,
    });
  }
  await withServe(
    {
      seedRepos: [
        {
          repo: `schuettc/${BIG}`,
          pushed_at: '2026-09-20T00:00:00Z',
          default_branch: 'main',
          prs,
          issues: [],
        },
      ],
    },
    (serveHandle) =>
      withContext(shared, async (context) => {
        const agent = createAgent(serveHandle.base, serveHandle.token);
        const all = await agent.api(
          'GET',
          `/api/items?view=waiting&q=${BIG}&offset=0&limit=1000`,
        );
        check(
          `serve has ${N} items waiting in ${BIG} (${all.total})`,
          all.total === N,
        );
        const pg = await context.newPage();
        try {
          await pg.setViewportSize({ width: 1600, height: 900 });
          await pg.goto(`${serveHandle.url}&q=${BIG}#/attention/waiting`, {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          const rowKeys = () =>
            pg.$$eval(`${SHOWN} .kit-row .kit-title`, (els) =>
              els.map((e) => e.textContent ?? ''),
            );
          const more = () =>
            pg.$eval(`${SHOWN} .cb-foot-more`, (b) => ({
              text: b.textContent,
              shown: !b.hidden && b.getBoundingClientRect().width > 0,
            }));
          const first = await until(
            pg,
            (sel) => document.querySelectorAll(`${sel} .kit-row`).length > 0,
            SHOWN,
            15000,
          );
          await pg.waitForTimeout(500);
          const keys200 = await rowKeys();
          check(
            `the first page renders exactly 200 rows of ${N} (${keys200.length})`,
            first && keys200.length === 200 && new Set(keys200).size === 200,
          );
          const m = await more();
          check(
            `the list's foot offers "show 3 more" ("${m.text}", shown ${m.shown})`,
            m.text === 'show 3 more' && m.shown,
          );
          await pg.click(`${SHOWN} .cb-foot-more`);
          const allShown = await until(
            pg,
            ([sel, n]) =>
              document.querySelectorAll(`${sel} .kit-row`).length === n,
            [SHOWN, N],
          );
          const keysAll = await rowKeys();
          checkList(
            `"show 3 more" brings the rest: all ${N}, each once, the first 200 unchanged`,
            [
              String(allShown),
              String(new Set(keysAll).size),
              String(keys200.every((k, i) => keysAll[i] === k)),
            ],
            ['true', String(N), 'true'],
          );
          const m2 = await more();
          check(`and the "show more" goes (shown ${m2.shown})`, !m2.shown);
        } finally {
          await pg.close();
        }
      }),
  );
}

// ---- the board's search ----------------------------------------------------------

// boardSearchScenario: on the board, the search field filters the lanes as
// it filters the list: / focuses it, typing narrows every lane to what serve
// finds for the text, Esc clears it and the lanes fill again, and the list
// then shows the same search's rows.
async function boardSearchScenario(shared, t) {
  const { check, checkList, until } = t;
  console.log("\nscenario: the board's search searches the board");
  const BS = 'bs-probe';
  await withServe(
    {
      seedRepos: [
        seedRepo(BS, [
          [71, 'rename the ledger export', 'kai'],
          [72, 'ledger totals drift by a cent', 'kai'],
          [73, 'bump the chart library', 'noa'],
        ]),
        seedRepo(`${BS}-other`, [[81, 'the ledger page is slow', 'noa']]),
        dormant(`${BS}-dormant`),
      ],
    },
    (serveHandle) =>
      withContext(shared, async (context) => {
        const agent = createAgent(serveHandle.base, serveHandle.token);
        // What serve finds for a text across the board's lanes (each item
        // once, in lane order: waiting, proposed, due, new).
        const lanesFind = async (q) => {
          const seen = new Set();
          for (const v of ['waiting', 'proposed', 'due', 'new']) {
            const r = await agent.api(
              'GET',
              `/api/items?view=${v}&offset=0&limit=500${q ? `&q=${encodeURIComponent(q)}` : ''}`,
            );
            for (const it of r.items ?? []) seen.add(it.key);
          }
          return [...seen].sort();
        };
        const pg = await context.newPage();
        const errors = [];
        pg.on('pageerror', (e) => errors.push(String(e)));
        try {
          await pg.setViewportSize({ width: 1600, height: 900 });
          await pg.goto(serveHandle.url + '#/attention/board', {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          const cards = () =>
            pg.$$eval('.cb-board .cb-lane .kit-card[data-id]', (els) =>
              [...new Set(els.map((e) => e.dataset.id))].sort(),
            );
          const allKeys = await lanesFind('');
          const boardIs = (want) =>
            until(
              pg,
              (w) =>
                [
                  ...new Set(
                    [
                      ...document.querySelectorAll(
                        '.cb-board .cb-lane .kit-card[data-id]',
                      ),
                    ].map((e) => e.dataset.id),
                  ),
                ]
                  .sort()
                  .join('|') === w,
              want.join('|'),
              8000,
            );
          check(
            `the board shows every item in its lanes (${allKeys.length})`,
            allKeys.length > 4 && (await boardIs(allKeys)),
          );
          await press(pg, '/');
          check(
            '/ on the board focuses the search field',
            await until(
              pg,
              () =>
                document.activeElement?.classList.contains('kit-search') &&
                !!document.activeElement.closest('.kit-list:not([hidden])'),
            ),
          );
          await pg.keyboard.type('ledger');
          const want = await lanesFind('ledger');
          const narrowed = await boardIs(want);
          checkList(
            `typing "ledger" narrows the lanes to what serve finds for it`,
            await cards(),
            want,
          );
          check(
            `(${want.length} of ${allKeys.length}, in the URL as ?q=ledger)`,
            narrowed &&
              want.length === 3 &&
              want.length < allKeys.length &&
              (await pg.evaluate(() =>
                new URL(location.href).searchParams.get('q'),
              )) === 'ledger',
          );
          await pg.keyboard.press('Escape');
          check(
            'Esc clears the search, and the lanes fill again',
            await boardIs(allKeys),
          );
          // The same search on the list: its rows are the waiting lane's.
          await press(pg, '/');
          await pg.keyboard.type('ledger');
          await boardIs(want);
          await pg.evaluate(() => {
            location.hash = '#/attention/waiting';
          });
          const waiting = (
            await agent.api(
              'GET',
              '/api/items?view=waiting&offset=0&limit=500&q=ledger',
            )
          ).items.map((it) => it.title);
          const rowsNow = await until(
            pg,
            (w) =>
              [
                ...document.querySelectorAll(
                  '.kit-app > .kit-list:not([hidden]) .kit-row .kit-title',
                ),
              ]
                .map((e) => e.textContent)
                .sort()
                .join('|') === w,
            [...waiting].sort().join('|'),
          );
          check(
            `and the list, switched to, searches the same: ${waiting.join(', ')}`,
            rowsNow && waiting.length === 3,
          );
          check(`no page errors (${errors.join(' | ')})`, errors.length === 0);
        } finally {
          await pg.close();
        }
      }),
  );
}
