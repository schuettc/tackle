// probe-decide.mjs — the decide step (recommend-and-decide Task 3), run by
// probe.mjs with its check helpers (t.check, t.checkList, t.until,
// t.eventually).
//
// One serve of its own (serve.mjs: --no-open, CASEBOOK_NO_BROWSER=1, the
// hermetic git guard, the fake gh) with a repo of its own: issues and a pull
// request by other people, so they wait on Court. The fixture's own waiting
// items are decided first, so "waiting on you" holds this repo's items only.
//
//   the question, from serve, per kind
//   the recommendation card above the choice cards; a accepts it
//   one click decides, with no sheet; a number key picks a card
//   Not now: a condition picked from chips; one serve refuses decides nothing
//   the next undecided item opens; the last shows the view's summary
//   u undoes the last decision; a decision changed since is refused (409)
//   a live decide elsewhere doesn't skip the item after it
//   the multi-select sheet asks with the same cards: "Close it · 2 issues"
//
// And recommendations in the list (Task 4), on a serve and repo of their own:
//
//   recommended items first; each row reads "pi recommends ‹label›"
//   "n recommended · m not yet" with serve's counts
//   "ask ‹session› to recommend the rest": only with a session attached, and
//     its message reaches that session's thread and no other's
//   "agree with all 3" accepts exactly those three; the next item opens
//   an outward group: "send all 2 to To apply"; To apply lists them; no job

import { startServe } from './serve.mjs';
import { createAgent } from './agent.mjs';

let serves = [];
/** stopDecideServes stops every serve these scenarios started (probe.mjs cleanup). */
export function stopDecideServes() {
  for (const s of serves) s.stop();
  serves = [];
}

const REPO = 'rd-decide';
const ISSUE = (n) => `issue:schuettc/${REPO}#${n}`;
const PR = (n) => `pr:schuettc/${REPO}#${n}`;

function seed() {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${REPO}`,
    number,
    title,
    author,
    state: 'OPEN',
    created_at: `2026-09-${String(i + 1).padStart(2, '0')}T00:00:00Z`,
    updated_at: `2026-09-${String(i + 1).padStart(2, '0')}T00:00:00Z`,
  });
  return {
    // A sync's lookup found no PR #404: GitHub answered "not found".
    seedRefs: { [PR(404)]: { exists: false } },
    seedRepos: [
      {
        repo: `schuettc/${REPO}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [[7, 'Add a retry to the uploader', 'bob']].map(item),
        issues: [
          [1, 'Cross-region permission boundary question', 'alice'],
          [2, 'Typo in the README', 'carol'],
          [3, 'Support for a second region', 'dave'],
          [4, 'Docs for the new flag', 'erin'],
          [5, 'Waiting on the uploader change', 'frank'],
          [6, 'Old question about tags', 'grace'],
          [8, 'Spam one', 'mallory'],
          [9, 'Spam two', 'mallory'],
        ].map(item),
      },
    ],
  };
}

const BG = { light: 'rgb(244, 245, 248)', dark: 'rgb(20, 22, 29)' };

// setTheme clicks the bar's theme control until the page is in theme.
async function setTheme(pg, theme) {
  for (let i = 0; i < 3; i++) {
    const th = await pg.evaluate(() => document.documentElement.dataset.theme);
    if (th === theme) return;
    await pg.click('button.kit-ctl:has-text("theme")');
  }
}

// shoot takes /tmp/rd-<name>-<theme>.png, asserting the theme took (the
// body's ground) and nothing is focused.
async function shoot(t, pg, name, themes) {
  for (const theme of themes) {
    await setTheme(pg, theme);
    await pg.evaluate(() => document.activeElement?.blur());
    const got = await pg.evaluate(() => ({
      theme: document.documentElement.dataset.theme,
      bg: getComputedStyle(document.body).backgroundColor,
      blurred: document.activeElement === document.body,
    }));
    const path = `/tmp/rd-${name}-${theme}.png`;
    t.check(
      `${path} is ${theme} (theme ${got.theme}, body ${got.bg}), nothing focused`,
      got.theme === theme && got.bg === BG[theme] && got.blurred,
    );
    await pg.screenshot({ path });
  }
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

// cssColor resolves a CSS colour expression to its computed rgb() string.
function cssColor(pg, expr) {
  return pg.evaluate((e) => {
    const d = document.createElement('div');
    d.style.setProperty('color', e);
    document.body.append(d);
    const v = getComputedStyle(d).color;
    d.remove();
    return v;
  }, expr);
}

// readCol reads what the reading column shows: the open item's key (or ''),
// its question, the recommendation card's text, the cards (disposition,
// label, kicker, says, recommended, chosen) and the summary line.
function readCol(pg) {
  return pg.evaluate(() => {
    const read = document.querySelector('.kit-app > .kit-read:not([hidden])');
    const item = read?.querySelector('.cb-item');
    const rec = read?.querySelector('.cb-proposal-card');
    return {
      key: item?.getAttribute('data-key') ?? '',
      question: read?.querySelector('.cb-question')?.textContent ?? '',
      rec: rec?.textContent ?? '',
      cards: [...(read?.querySelectorAll('.cb-choices .cb-choice') ?? [])].map(
        (c) => ({
          d: c.getAttribute('data-d') ?? '',
          label: c.querySelector('.cb-choice-label')?.textContent ?? '',
          kick: c.querySelector('.cb-choice-k')?.textContent ?? '',
          says: c.querySelector('.cb-choice-says')?.textContent ?? '',
          rec: c.classList.contains('rec'),
          on: c.classList.contains('on'),
        }),
      ),
      err: (() => {
        const e = read?.querySelector('.cb-choice-err');
        return e && !e.hidden ? (e.textContent ?? '') : '';
      })(),
      done: read?.querySelector('.cb-read-done')?.textContent ?? '',
    };
  });
}

// kicker is a row's kicker line for key: "issue · schuettc/r#1".
const kicker = (key) => key.replace(':', ' \u00b7 ');

// row is the shown list's row for key (rows carry no id; the kicker is the
// key).
const escapeRe = (x) => x.replace(/[.*+?^$()|[\]\\{}]/g, '\\$&');
const row = (pg, key) =>
  pg.locator('.kit-app > .kit-list:not([hidden]) .kit-row').filter({
    has: pg.locator('.kit-kicker', {
      hasText: new RegExp(`^${escapeRe(kicker(key))}$`),
    }),
  });

// opened waits for the reading column to show key with its cards.
function opened(t, pg, key) {
  return t.until(
    pg,
    (k) => {
      const read = document.querySelector('.kit-app > .kit-read:not([hidden])');
      return (
        read?.querySelector('.cb-item')?.getAttribute('data-key') === k &&
        (read.querySelectorAll('.cb-choices .cb-choice').length ?? 0) > 0
      );
    },
    key,
    8000,
  );
}

export async function decideScenarios(shared, t) {
  for (const [name, scenario, seeded] of [
    ['the decide step', decideScenario, seed()],
    ['recommendations in the list', recommendScenario, recSeed()],
  ]) {
    const context = await shared.browser().newContext({
      viewport: { width: 1600, height: 900 },
    });
    const s = await startServe(seeded);
    serves.push(s);
    try {
      await scenario(context, t, s);
    } catch (err) {
      t.check(`${name}: the scenario ran to its end — ${err.message}`, false);
    } finally {
      await context.close();
      s.stop();
      serves = serves.filter((x) => x !== s);
    }
  }
}

async function decideScenario(context, t, s) {
  const { check, checkList, until, eventually } = t;
  console.log('\nscenario: the decide step');
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const decisionOf = async (key) =>
    (await api('GET', `/api/item?key=${encodeURIComponent(key)}`)).item
      .decision ?? null;
  const vocab = await api('GET', '/api/decisions/vocabulary');
  const kindVocab = (k) => vocab.kinds.find((v) => v.kind === k);

  // Only this repo's items wait on Court: the fixture's are decided first.
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${REPO}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'keep' });
  const waiting = (
    (await api('GET', '/api/items?view=waiting&limit=500')).items ?? []
  ).map((it) => it.key);
  checkList(
    'waiting on you holds the seeded issues and pull request, in key order',
    waiting,
    [1, 2, 3, 4, 5, 6, 8, 9].map(ISSUE).concat([PR(7)]),
  );

  // A recommendation for issue #1, with its reason.
  const sid = 'probe-rd-decide';
  await agent.presence(sid, 'pi \u00b7 rd', '/home/court/rd', 'pi');
  const REASON =
    'A usage question on an example repo no longer maintained. Answer briefly and close.';
  await agent.propose(sid, [ISSUE(1)], 'close', REASON);

  const pg = await context.newPage();
  // Count every sheet the page ever opens.
  await pg.addInitScript(() => {
    window.__sheets = 0;
    new MutationObserver((ms) => {
      for (const m of ms)
        for (const n of m.addedNodes)
          if (n instanceof HTMLElement && n.matches('.kit-sheet'))
            window.__sheets++;
    }).observe(document.documentElement, { childList: true, subtree: true });
  });
  const sheets = () => pg.evaluate(() => window.__sheets);
  await pg.goto(`${s.url}#/attention/waiting`, {
    waitUntil: 'domcontentloaded',
  });
  await pg.waitForSelector('.kit-row', { timeout: 8000 });

  // ---- the multi-select sheet: the same question, the same cards ----------
  {
    for (const k of [ISSUE(8), ISSUE(9)]) {
      await row(pg, k).locator('.kit-box').click();
    }
    await until(
      pg,
      () => document.querySelector('.kit-primary')?.textContent === 'Decide 2',
    );
    await pg.click('.kit-primary');
    await until(pg, () => !!document.querySelector('.kit-sheet .cb-choice'));
    const sheet = await pg.evaluate(() => {
      const sh = document.querySelector('.kit-sheet');
      return {
        question: sh?.querySelector('.cb-question')?.textContent ?? '',
        labels: [...(sh?.querySelectorAll('.cb-choice') ?? [])].map(
          (c) => c.querySelector('.cb-choice-label')?.textContent ?? '',
        ),
      };
    });
    const iv = kindVocab('issue');
    check(
      `the sheet for two issues asks serve's question ("${sheet.question}")`,
      sheet.question === iv.question,
    );
    checkList(
      "the sheet's cards are serve's choices for an issue",
      sheet.labels,
      iv.choices.map((c) => c.label),
    );
    await pg.click('.kit-sheet .cb-choice[data-d="close"]');
    const fill = await pg
      .locator('.kit-sheet .kit-btn.fill')
      .textContent({ timeout: 3000 });
    check(
      `picking "Close it" makes the fill read "Close it · 2 issues" ("${fill}")`,
      fill === 'Close it \u00b7 2 issues',
    );
    check(
      'picking a card in the sheet decides nothing yet',
      (await decisionOf(ISSUE(8))) === null,
    );
    await pg.click('.kit-sheet .kit-btn.fill');
    check(
      'the fill decides both issues close',
      await eventually(
        async () =>
          (await decisionOf(ISSUE(8)))?.disposition === 'close' &&
          (await decisionOf(ISSUE(9)))?.disposition === 'close',
      ),
    );
    check(
      'and the sheet closes',
      await until(pg, () => !document.querySelector('.kit-sheet')),
    );
  }
  const sheetsBefore = await sheets();

  // ---- the row's line names serve's label for the recommendation ----------
  {
    const sub = await row(pg, ISSUE(1))
      .locator('.kit-sub')
      .textContent({ timeout: 5000 });
    check(
      `issue #1's row reads "pi recommends Close it" ("${sub}")`,
      sub === 'pi recommends Close it',
    );
  }

  // ---- issue #1: the question, the recommendation, the cards --------------
  await row(pg, ISSUE(1)).click();
  check('issue #1 opens', await opened(t, pg, ISSUE(1)));
  let col = await readCol(pg);
  check(
    `the open issue asks serve's question for an issue ("${col.question}")`,
    col.question === kindVocab('issue').question,
  );
  checkList(
    "its cards are serve's choices for an issue, numbered",
    col.cards.map((c) => `${c.kick.split(' ')[0]} ${c.label} — ${c.says}`),
    kindVocab('issue').choices.map((c, i) => `${i + 1} ${c.label} — ${c.says}`),
  );
  check(
    `the recommendation card shows the reason and the recommended choice ("${col.rec}")`,
    col.rec.includes(REASON) && col.rec.includes('Close it'),
  );
  const closeCard = col.cards.find((c) => c.d === 'close');
  check(
    `the recommended card is marked recommended ("${closeCard?.kick}"), and only it`,
    !!closeCard?.rec &&
      closeCard.kick.includes('recommended') &&
      col.cards.filter((c) => c.rec).length === 1,
  );
  {
    // Geometry: question, then the recommendation, then a two-column grid.
    const g = await pg.evaluate(() => {
      const r = (sel) =>
        document
          .querySelector(`.kit-app > .kit-read:not([hidden]) ${sel}`)
          ?.getBoundingClientRect();
      const cards = [
        ...document.querySelectorAll(
          '.kit-app > .kit-read:not([hidden]) .cb-choices .cb-choice',
        ),
      ].map((c) => {
        const b = c.getBoundingClientRect();
        return { top: b.top, left: b.left, bottom: b.bottom, w: b.width };
      });
      const q = r('.cb-question');
      const rec = r('.cb-proposal-card');
      return { q, rec, cards, vh: innerHeight };
    });
    const [c1, c2, c3] = g.cards;
    check(
      'the question is above the recommendation card, which is above the cards',
      g.q.bottom <= g.rec.top && g.rec.bottom <= c1.top,
    );
    check(
      `the cards are a two-column grid (1 and 2 side by side, 3 below; widths ${c1.w}, ${c2.w})`,
      Math.abs(c1.top - c2.top) < 1 &&
        c2.left > c1.left + c1.w - 1 &&
        c3.top >= c1.bottom &&
        Math.abs(c3.left - c1.left) < 1 &&
        Math.abs(c1.w - c2.w) < 1,
    );
    check(
      `the cards are on screen at 1600×900 (last card bottom ${Math.round(g.cards.at(-1).bottom)})`,
      g.cards.at(-1).bottom <= g.vh,
    );
  }
  {
    // Colours: amber marks the recommendation; an issue's close isn't danger.
    const agent = await cssColor(pg, 'var(--kit-agent)');
    const danger = await cssColor(pg, 'var(--kit-danger)');
    const got = await pg.evaluate(() => {
      const card = document.querySelector(
        '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="close"]',
      );
      return {
        kick: getComputedStyle(card.querySelector('.cb-choice-k')).color,
        label: getComputedStyle(card.querySelector('.cb-choice-label')).color,
      };
    });
    check(
      `the recommended card's kicker is the agent's colour (${got.kick})`,
      got.kick === agent,
    );
    check(
      `"Close it" is not drawn in danger (${got.label})`,
      got.label !== danger,
    );
  }
  await shoot(t, pg, 'decide', ['light', 'dark']);
  await setTheme(pg, 'light');

  // ---- a accepts the recommendation; the next undecided opens -------------
  await press(pg, 'a');
  check(
    'a accepts the recommendation: issue #1 is decided close, proposed by the session',
    await eventually(async () => {
      const d = await decisionOf(ISSUE(1));
      return d?.disposition === 'close' && (d.proposed_by ?? '').includes(sid);
    }),
  );
  check(
    'after the accept, the next undecided item (issue #2) opens',
    await opened(t, pg, ISSUE(2)),
  );
  check(
    "and the list's open row follows it to issue #2",
    await until(
      pg,
      (kick) =>
        [
          ...document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row.open .kit-kicker',
          ),
        ].map((e) => e.textContent) +
          '' ===
        kick,
      kicker(ISSUE(2)),
    ),
  );

  // ---- one click on "Close it" decides, with no sheet ----------------------
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="close"]',
  );
  check(
    'one click on "Close it" decides issue #2 close',
    await eventually(
      async () => (await decisionOf(ISSUE(2)))?.disposition === 'close',
    ),
  );
  check('and issue #3 opens next', await opened(t, pg, ISSUE(3)));
  check(
    `no sheet opened for one item (sheets opened since the bulk one: ${(await sheets()) - sheetsBefore})`,
    (await sheets()) === sheetsBefore,
  );

  // ---- the number key 1 decides keep ---------------------------------------
  await press(pg, '1');
  check(
    'the number key 1 decides issue #3 keep',
    await eventually(
      async () => (await decisionOf(ISSUE(3)))?.disposition === 'keep',
    ),
  );
  check('and issue #4 opens next', await opened(t, pg, ISSUE(4)));

  // ---- u undoes it: issue #3 is undecided again and opens -----------------
  await press(pg, 'u');
  check(
    'u clears issue #3 (it had no decision before)',
    await eventually(async () => (await decisionOf(ISSUE(3))) === null),
  );
  check('and goes back to issue #3', await opened(t, pg, ISSUE(3)));
  col = await readCol(pg);
  check(
    'issue #3 shows no chosen card again',
    col.cards.length > 0 && col.cards.every((c) => !c.on),
  );
  await press(pg, '1');
  check(
    'deciding issue #3 keep again moves on to issue #4',
    (await eventually(
      async () => (await decisionOf(ISSUE(3)))?.disposition === 'keep',
    )) && (await opened(t, pg, ISSUE(4))),
  );

  // ---- u refuses a decision changed since: serve's 409 message -------------
  {
    // decided_at has second precision: the change must be a later second.
    await new Promise((r) => setTimeout(r, 1100));
    await api('POST', '/api/decide', {
      keys: [ISSUE(3)],
      disposition: 'ignore',
    });
    await press(pg, 'u');
    const errText = () =>
      pg.evaluate(
        () =>
          document.querySelector(
            '.kit-app > .kit-read:not([hidden]) .cb-choice-err:not([hidden])',
          )?.textContent ?? '',
      );
    const shown = await until(
      pg,
      () =>
        (
          document.querySelector(
            '.kit-app > .kit-read:not([hidden]) .cb-choice-err:not([hidden])',
          )?.textContent ?? ''
        ).includes('changed since'),
      undefined,
      5000,
    );
    check(
      `u after issue #3 changed meanwhile shows serve's refusal ("${await errText()}")`,
      shown,
    );
    check(
      'and the change stands (issue #3 stays ignore)',
      (await decisionOf(ISSUE(3)))?.disposition === 'ignore',
    );
  }

  // ---- Not now → in 1 week --------------------------------------------------
  await opened(t, pg, ISSUE(4));
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="wait"]',
  );
  check(
    'Not now opens its conditions below the cards, deciding nothing yet',
    (await until(
      pg,
      () =>
        !!document.querySelector(
          '.kit-app > .kit-read:not([hidden]) .cb-notnow:not([hidden]) .kit-chip',
        ),
    )) && (await decisionOf(ISSUE(4))) === null,
  );
  {
    const chips = await pg.$$eval(
      '.kit-app > .kit-read:not([hidden]) .cb-notnow .kit-chip',
      (els) => els.map((e) => e.textContent ?? ''),
    );
    checkList(
      "the conditions are serve's Not now forms",
      chips,
      vocab.not_now.map((f) => f.label),
    );
  }
  const week = await pg.evaluate(() => {
    const d = new Date();
    const t = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 7);
    return `date(${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')})`;
  });
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-notnow .kit-chip[data-id="in-1w"]',
  );
  check(
    `"in 1 week" decides issue #4 wait until ${week}`,
    await eventually(async () => {
      const d = await decisionOf(ISSUE(4));
      return d?.disposition === 'wait' && d.until === week;
    }),
  );
  check('and issue #5 opens next', await opened(t, pg, ISSUE(5)));

  // ---- Not now → a PR merges… ----------------------------------------------
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="wait"]',
  );
  await until(
    pg,
    () =>
      !!document.querySelector(
        '.kit-app > .kit-read:not([hidden]) .cb-notnow:not([hidden]) .kit-chip',
      ),
  );
  await shoot(t, pg, 'notnow', ['light']);
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-notnow .kit-chip[data-id="pr-merges"]',
  );
  const field = pg.locator(
    '.kit-app > .kit-read:not([hidden]) .cb-notnow-field input',
  );
  check(
    '"when a PR merges…" asks for one value (one field shows)',
    (await field.count()) === 1 && (await field.isVisible()),
  );

  // ---- a live decide of the next item elsewhere doesn't skip the one after --
  await api('POST', '/api/decide', { keys: [ISSUE(6)], disposition: 'keep' });
  await until(
    pg,
    (kick) =>
      ![
        ...document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .kit-row .kit-kicker',
        ),
      ].some((e) => e.textContent === kick),
    kicker(ISSUE(6)),
  );
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="ignore"]',
  );
  check(
    'deciding issue #5 after issue #6 was decided elsewhere opens the pull request, not skipping it',
    (await eventually(
      async () => (await decisionOf(ISSUE(5)))?.disposition === 'ignore',
    )) && (await opened(t, pg, PR(7))),
  );
  col = await readCol(pg);
  check(
    `the pull request asks serve's question for a pull request ("${col.question}")`,
    col.question === kindVocab('pr').question,
  );
  checkList(
    "its cards are serve's choices for a pull request",
    col.cards.map((c) => c.label),
    kindVocab('pr').choices.map((c) => c.label),
  );

  // ---- the last item: the view's summary ------------------------------------
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="keep"]',
  );
  check(
    'deciding the last item shows "Nothing left in waiting on you."',
    await until(
      pg,
      () =>
        document.querySelector(
          '.kit-app > .kit-read:not([hidden]) .cb-read-done',
        )?.textContent === 'Nothing left in waiting on you.',
      undefined,
      8000,
    ),
  );
  check(
    'and the pull request is decided keep',
    (await decisionOf(PR(7)))?.disposition === 'keep',
  );
  check(
    'and no sheet opened for any single item',
    (await sheets()) === sheetsBefore,
  );

  // ---- a Not now naming a PR GitHub can't find comes back, saying why -----
  {
    const GONE = PR(404);
    const WHY = `its condition names ${GONE}, which GitHub can't find`;
    const dec = await api('POST', '/api/decide', {
      keys: [ISSUE(4)],
      disposition: 'wait',
      until: `merged(${GONE})`,
    });
    check(
      `serve accepts a Not now naming a PR it doesn't track (decided ${dec.decided}, errors ${JSON.stringify(dec.errors)})`,
      dec.decided === 1 && (dec.errors ?? []).length === 0,
    );
    const it = (
      await api('GET', `/api/item?key=${encodeURIComponent(ISSUE(4))}`)
    ).item;
    check(
      `a sync found ${GONE} doesn't exist: issue #4 is due ("${it.status}", "${it.due_reason ?? ''}")`,
      it.status === 'due' && it.due_reason === WHY,
    );
    await row(pg, ISSUE(4)).waitFor({ timeout: 8000 });
    await row(pg, ISSUE(4)).click();
    await opened(t, pg, ISSUE(4));
    const shown = await pg.evaluate(
      () =>
        document.querySelector(
          '.kit-app > .kit-read:not([hidden]) .cb-due-reason',
        )?.textContent ?? '',
    );
    check(`the page shows why issue #4 came back ("${shown}")`, shown === WHY);
  }
  await pg.close();
}

// ---- recommendations in the list ---------------------------------------------

const REC = 'rd-rec';
const RISSUE = (n) => `issue:schuettc/${REC}#${n}`;
const RPR = (n) => `pr:schuettc/${REC}#${n}`;
const RECOMMEND_THE_REST =
  'Please recommend the items casebook still needs a recommendation for: call casebook_next until it says done.';

function recSeed() {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${REC}`,
    number,
    title,
    author,
    state: 'OPEN',
    created_at: `2026-09-${String(i + 1).padStart(2, '0')}T00:00:00Z`,
    updated_at: `2026-09-${String(i + 1).padStart(2, '0')}T00:00:00Z`,
  });
  return {
    seedRepos: [
      {
        repo: `schuettc/${REC}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [
          [9, 'Bump the uploader dependency', 'renovate'],
          [10, 'Bump the parser dependency', 'renovate'],
        ].map(item),
        issues: [
          [1, 'How do I set the region?', 'alice'],
          [2, 'Works on my machine', 'carol'],
          [3, 'A second region', 'dave'],
          [4, 'Flag docs', 'erin'],
          [5, 'Tags question', 'frank'],
          [6, 'Thanks for the tool', 'grace'],
        ].map(item),
      },
    ],
  };
}

async function recommendScenario(context, t, s) {
  const { check, checkList, until, eventually } = t;
  console.log('\nscenario: recommendations in the list');
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const decisionOf = async (key) =>
    (await api('GET', `/api/item?key=${encodeURIComponent(key)}`)).item
      .decision ?? null;

  // Only this repo's items wait on Court.
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${REC}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'keep' });

  // Two sessions here, so the dock attaches to neither on its own.
  const A = 'probe-rec-a';
  const B = 'probe-rec-b';
  await agent.presence(A, 'pi \u00b7 rec-a', '/home/court/rec-a', 'pi');
  await agent.presence(B, 'pi \u00b7 rec-b', '/home/court/rec-b', 'pi');
  // B already has a thread of its own: the ask must not land there.
  const bThread = await agent.newThread(B, 'b work');
  // pi recommends leaving three issues open and closing both bot PRs.
  await agent.propose(A, [RISSUE(2), RISSUE(4), RISSUE(6)], 'keep', 'active');
  await agent.propose(A, [RPR(9), RPR(10)], 'close', 'superseded');

  const pg = await context.newPage();
  await pg.goto(`${s.url}#/attention/waiting`, {
    waitUntil: 'domcontentloaded',
  });
  await pg.waitForSelector('.kit-row', { timeout: 8000 });
  await setTheme(pg, 'light');

  const kickers = () =>
    pg.$$eval(
      '.kit-app > .kit-list:not([hidden]) .kit-row .kit-kicker',
      (els) => els.map((e) => e.textContent ?? ''),
    );
  const recommended = [RISSUE(2), RISSUE(4), RISSUE(6), RPR(10), RPR(9)];
  const notYet = [RISSUE(1), RISSUE(3), RISSUE(5)];
  await until(
    pg,
    (want) =>
      [
        ...document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .kit-row .kit-kicker',
        ),
      ].length === want,
    recommended.length + notYet.length,
  );
  checkList(
    'the recommended items come first, each group in key order',
    await kickers(),
    [...recommended, ...notYet].map(kicker),
  );
  {
    const subs = {};
    for (const k of [RISSUE(2), RPR(9), RISSUE(1)]) {
      subs[k] =
        (await row(pg, k).locator('.kit-sub').count()) > 0
          ? await row(pg, k).locator('.kit-sub').textContent()
          : '';
    }
    check(
      `the rows name serve's label ("${subs[RISSUE(2)]}", "${subs[RPR(9)]}"), and an item with none has no line`,
      subs[RISSUE(2)] === 'pi recommends Leave it open' &&
        subs[RPR(9)] === 'pi recommends Close it without merging' &&
        subs[RISSUE(1)] === '',
    );
  }

  // ---- the line: serve's counts; no session attached, no offer -------------
  const lineText = () =>
    pg.evaluate(() => {
      const l = document.querySelector(
        '.kit-app > .kit-list:not([hidden]) .cb-rec-line',
      );
      return {
        shown: !!l && !l.hidden,
        counts: l?.querySelector('.cb-rec-counts')?.textContent ?? '',
        ask: (() => {
          const b = l?.querySelector('.cb-rec-ask');
          return b && !b.hidden ? (b.textContent ?? '') : '';
        })(),
        note: l?.querySelector('.cb-rec-note')?.textContent ?? '',
      };
    });
  const summaryLine = async () => {
    const sv = await api('GET', '/api/summary');
    return `${sv.recommended} recommended \u00b7 ${sv.not_recommended} not yet`;
  };
  {
    const want = await summaryLine();
    const ok = await until(
      pg,
      (w) =>
        document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .cb-rec-counts',
        )?.textContent === w,
      want,
    );
    const got = await lineText();
    check(
      `the line reads serve's counts ("${got.counts}", serve: "${want}")`,
      ok && got.shown && want.startsWith('5 recommended'),
    );
    const attach = await pg.getAttribute('.cb-dock-header', 'data-attach');
    check(
      `with no session attached (dock: ${attach}) the offer is absent`,
      attach === 'none' && got.ask === '',
    );
  }

  // ---- the foot: agree with all ---------------------------------------------
  const agreeLines = () =>
    pg.$$eval(
      '.kit-app > .kit-list:not([hidden]) .cb-agree:not([hidden]) .cb-agree-line',
      (els) =>
        els.map(
          (e) =>
            `${e.querySelector('.cb-agree-says')?.textContent} | ${e.querySelector('.cb-agree-btn')?.textContent}`,
        ),
    );
  checkList(
    'the foot offers agree with all per agreeing group',
    await agreeLines(),
    [
      'pi recommends Leave it open for 3 | agree with all 3',
      'pi recommends Close it without merging for 2 | send all 2 to To apply',
    ],
  );
  {
    const agentC = await cssColor(pg, 'var(--kit-agent)');
    const dangerC = await cssColor(pg, 'var(--kit-danger)');
    const got = await pg.evaluate(() => {
      const ls = document.querySelectorAll(
        '.kit-app > .kit-list:not([hidden]) .cb-agree-line',
      );
      return [...ls].map((l) => ({
        says: getComputedStyle(l.querySelector('.cb-agree-says')).color,
        btn: getComputedStyle(l.querySelector('.cb-agree-btn')).color,
      }));
    });
    check(
      `what is recommended is the agent's colour, and no action is danger (${got.map((g) => `${g.says}/${g.btn}`).join(', ')})`,
      got.length === 2 &&
        got.every((g) => g.says === agentC && g.btn !== dangerC),
    );
  }

  // ---- attach session A: the offer names it ---------------------------------
  await pg.click(`.cb-dock-chooser .cb-dock-pick-item[data-session="${A}"]`);
  await until(
    pg,
    () =>
      document.querySelector('.cb-dock-header')?.getAttribute('data-attach') ===
      'here',
  );
  const aName = await pg.textContent('[data-testid="dock-session-name"]');
  check(
    `with ${aName} attached the line offers "ask ${aName} to recommend the rest"`,
    !!aName &&
      (await until(
        pg,
        (w) => {
          const b = document.querySelector(
            '.kit-app > .kit-list:not([hidden]) .cb-rec-ask',
          );
          return !!b && !b.hidden && b.textContent === w;
        },
        `ask ${aName} to recommend the rest`,
      )),
  );
  await shoot(t, pg, 'list', ['light']);

  // ---- ask: the message reaches A's thread only -----------------------------
  const bodiesOf = async (session) => {
    const tv = await api(
      'GET',
      `/api/threads?session=${encodeURIComponent(session)}`,
    );
    const out = [];
    for (const th of tv.threads ?? []) {
      const mv = await agent.messages(th.id);
      for (const m of mv.messages ?? []) out.push(m.body);
    }
    return out;
  };
  await pg.click('.kit-app > .kit-list:not([hidden]) .cb-rec-ask');
  check(
    "asking puts the exact message in the attached session's thread, once",
    await eventually(async () => {
      const a = await bodiesOf(A);
      return a.filter((b) => b === RECOMMEND_THE_REST).length === 1;
    }),
  );
  {
    const b = await bodiesOf(B);
    const bt = (await agent.messages(bThread.id)).messages ?? [];
    check(
      `and nothing in any other session's thread (B has ${b.length} messages)`,
      !b.includes(RECOMMEND_THE_REST) &&
        bt.every((m) => m.body !== RECOMMEND_THE_REST),
    );
  }
  {
    const got = await lineText();
    check(
      `the offer gives way to "${got.note}"`,
      got.ask === '' && got.note === `asked ${aName}`,
    );
  }

  // ---- agree with all 3: exactly those three; the next item opens ----------
  await row(pg, RISSUE(2)).click();
  await opened(t, pg, RISSUE(2));
  await pg.click(
    '.kit-app > .kit-list:not([hidden]) .cb-agree-line[data-d="keep"] .cb-agree-btn',
  );
  check(
    'agree with all 3 decides exactly issues #2, #4 and #6 keep, from the recommendation',
    await eventually(async () => {
      const ds = await Promise.all(
        [RISSUE(2), RISSUE(4), RISSUE(6)].map(decisionOf),
      );
      return ds.every(
        (d) => d?.disposition === 'keep' && (d.proposed_by ?? '').includes(A),
      );
    }),
  );
  {
    const rest = await Promise.all(
      [RISSUE(1), RISSUE(3), RISSUE(5), RPR(9), RPR(10)].map(decisionOf),
    );
    const proposed = await api('GET', '/api/items?view=proposed&limit=500');
    checkList(
      'and nothing else: the rest are undecided, the PRs still recommended',
      [
        ...rest.map((d) => (d ? d.disposition : 'undecided')),
        ...(proposed.items ?? []).map((it) => it.key).sort(),
      ],
      [
        'undecided',
        'undecided',
        'undecided',
        'undecided',
        'undecided',
        RPR(10),
        RPR(9),
      ],
    );
  }
  check(
    'and the next undecided item (pull request #10) opens',
    await opened(t, pg, RPR(10)),
  );
  check(
    "and the list's open row follows it",
    await until(
      pg,
      (kick) =>
        [
          ...document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row.open .kit-kicker',
          ),
        ].map((e) => e.textContent) +
          '' ===
        kick,
      kicker(RPR(10)),
    ),
  );
  checkList(
    'the foot now offers only the outward group',
    await (async () => {
      await until(
        pg,
        () =>
          document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .cb-agree-line',
          ).length === 1,
      );
      return agreeLines();
    })(),
    ['pi recommends Close it without merging for 2 | send all 2 to To apply'],
  );

  // ---- an outward group goes to To apply, and runs nothing -----------------
  await pg.click(
    '.kit-app > .kit-list:not([hidden]) .cb-agree-line[data-d="close"] .cb-agree-btn',
  );
  check(
    'send all 2 to To apply decides both pull requests close',
    await eventually(async () => {
      const ds = await Promise.all([RPR(9), RPR(10)].map(decisionOf));
      return ds.every((d) => d?.disposition === 'close');
    }),
  );
  check(
    'and issue #1, the next undecided item, opens',
    await opened(t, pg, RISSUE(1)),
  );
  {
    const jobs = await api('GET', '/api/jobs');
    check(
      `no job was started (${(jobs.jobs ?? []).length} jobs)`,
      (jobs.jobs ?? []).length === 0,
    );
  }
  {
    const want = await summaryLine();
    check(
      `the line follows serve ("${want}")`,
      want.startsWith('0 recommended') &&
        (await until(
          pg,
          (w) =>
            document.querySelector(
              '.kit-app > .kit-list:not([hidden]) .cb-rec-counts',
            )?.textContent === w,
          want,
        )),
    );
  }
  await pg.goto(`${s.url}#/apply`, { waitUntil: 'domcontentloaded' });
  await pg.click('.kit-chip[data-id="ready"]');
  check(
    "To apply's ready list holds both pull requests",
    await until(
      pg,
      (want) => {
        const ks = [
          ...document.querySelectorAll('.cb-apply-list .kit-row'),
        ].map((e) => e.dataset.key);
        return want.every((k) => ks.includes(k));
      },
      [RPR(9), RPR(10)],
      8000,
    ),
  );
  check(
    'and still no job',
    ((await api('GET', '/api/jobs')).jobs ?? []).length === 0,
  );
  await pg.close();
}
