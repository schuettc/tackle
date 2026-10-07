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
//   Not now: a condition picked from chips; "when a PR merges…" searches
//     the tracked items (a title finds the PR) or takes a pasted URL (an
//     untracked PR is offered as not tracked); junk offers and decides nothing;
//     options shown for older text can't be chosen once the text changes
//   a Not now naming a PR a sync couldn't find comes back, saying why
//   the next undecided item opens; the last shows the view's summary
//   u undoes the last decision; a decision changed since is refused (409),
//     even in the same second; u after a re-decide puts the previous back,
//     and is refused too when the item changed since
//   a live decide elsewhere doesn't skip the item after it
//   the multi-select sheet asks with the same cards: "Close it · 2 issues"
//
// And move-on past the first page, on a serve and repo of their own: with
// more than a page of items, deciding one on page two opens the next one on
// page two.
//
// And the closing comment and the GitHub link (casebook 0.4.1), on a serve
// and repo of their own:
//
//   the kicker's key links to the item's GitHub page (an issue, a PR, a
//     repo) in a new tab; a branch's is plain text; the kicker stays mono
//     and muted
//   choosing Close opens a closing-comment field under the cards and
//     decides nothing; Esc cancels it; a number key opens it too
//   "close with this comment" (and ↵) records the text as the decision's
//     note; To apply's step posts exactly that text
//   "close without comment" records no note; the step has no comment
//
// And what Leave it open leaves behind (casebook 0.4.3), on a serve and repo
// of their own:
//
//   deciding Leave it open moves the row into the left-open group at the
//     bottom ("left open · N", rows muted, "left open · date"); the view's
//     count drops; move-on opens the next undecided item, never a
//     left-open row
//   opening a left-open row shows its decision (the chosen card) and the
//     cards as usual
//   someone else's newer comment (the GitHub cache, then a restart) moves
//     it back up to the top, flagged "new activity since you left it
//     open", counted again; Court's own newer comment doesn't
//
// And recommendations in the list (Task 4), on a serve and repo of their own:
//
//   recommended items first; each row reads "pi recommends ‹label›"
//   "n recommended · m not yet" with serve's counts
//   "ask ‹session› to recommend the rest": only with a session attached, and
//     its message reaches that session's thread and no other's
//   "agree with all 3" accepts exactly those three; the next item opens
//   an outward group: "send all 2 to To apply"; To apply lists them; no job
//
// And a new recommendation on the open item (casebook 0.4.5), on a serve
// and repo of their own:
//
//   the agent proposes for the open item: its recommendation card (with the
//     reason) and the "recommended" mark show, with no reload
//   a proposal for another item leaves the open item as it was
//   with the closing comment typed, or the Not now picker open, the open
//     item isn't re-rendered under Court: "pi recommends ‹label› · show"
//     shows instead, and "show" re-renders it
//   the card's head and the board's cards say "recommends", as the list's
//     rows do: "pi recommends · Close it without merging"
//
// And "ask ‹session› to look into it" (casebook 0.4.5), on a serve and repo
// of their own:
//
//   with no session attached there is no button under the cards
//   with one, the button sends exactly the ask, the item attached, to that
//     session's thread only
//   while the message is queued (and worked on), the item says "‹session›
//     is looking into it" under its question, and its row says so (muted,
//     agent colour)
//   the agent's evidence and recommendation show on the open item with no
//     reload; its reply clears the marker

import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
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
    ['move-on past the first page', pagesScenario, pagesSeed()],
    [
      'the closing comment and the GitHub link',
      closeCommentScenario,
      closeSeed(),
    ],
    ['Leave it open stays in the list', leftOpenScenario, leftOpenSeed()],
    ['a new recommendation on the open item', openRecScenario, openRecSeed()],
    ['ask the session to look into it', lookIntoScenario, lookIntoSeed()],
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

  // Only this repo's items wait on Court: the fixture's stop being tracked
  // first (kept, they would stay in the list's left-open group).
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${REPO}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });
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

  // ---- a on a close recommendation asks for the closing comment ----------
  // The recommendation's reason is written to Court: it is never the
  // comment. The field opens empty; nothing is decided until a button.
  await press(pg, 'a');
  {
    const FIELD = '.kit-app > .kit-read:not([hidden]) .cb-close-comment';
    const shown = await until(
      pg,
      (sel) => !!document.querySelector(sel)?.checkVisibility(),
      FIELD,
    );
    const f = await pg.evaluate(
      (sel) => ({
        value: document.querySelector(`${sel} input.kit-note`)?.value ?? null,
        focused:
          document.activeElement ===
          document.querySelector(`${sel} input.kit-note`),
        closeOn: !!document
          .querySelector(
            '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="close"]',
          )
          ?.classList.contains('on'),
      }),
      FIELD,
    );
    await pg.waitForTimeout(400);
    check(
      `a on a close recommendation opens the closing-comment field, empty ("${f.value}"), focused, on "Close it"`,
      shown && f.value === '' && f.focused && f.closeOn,
    );
    check(
      'and accepts nothing yet: issue #1 is undecided, its recommendation pending',
      (await decisionOf(ISSUE(1))) === null &&
        (await api('GET', `/api/item?key=${encodeURIComponent(ISSUE(1))}`)).item
          .proposal?.state === 'pending',
    );
    await pg
      .locator(`${FIELD} .kit-btn`, { hasText: /^close without comment$/ })
      .click();
  }
  check(
    '"close without comment" accepts the recommendation: issue #1 is decided close, proposed by the session, with no note',
    await eventually(async () => {
      const d = await decisionOf(ISSUE(1));
      return (
        d?.disposition === 'close' &&
        (d.proposed_by ?? '').includes(sid) &&
        !d.note
      );
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

  // ---- "Close it" asks for a closing comment, with no sheet ---------------
  // (the comment itself: the closing-comment scenario).
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="close"]',
  );
  await until(
    pg,
    () =>
      !!document.querySelector(
        '.kit-app > .kit-read:not([hidden]) .cb-close-comment:not([hidden]) .kit-note',
      ),
  );
  await pg
    .locator('.kit-app > .kit-read:not([hidden]) .cb-close-comment .kit-btn', {
      hasText: /^close without comment$/,
    })
    .click();
  check(
    'clicking "Close it", then "close without comment", decides issue #2 close',
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
    // In the same second: serve compares the whole decision, not the second.
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
  {
    const READ = '.kit-app > .kit-read:not([hidden])';
    const FIELD = `${READ} .cb-notnow-field input`;
    const field = pg.locator(FIELD);
    // prMerges picks Not now's "when a PR merges…" on the open item.
    const prMerges = async () => {
      const chip = `${READ} .cb-notnow:not([hidden]) .kit-chip`;
      if (!(await pg.$(chip))) {
        await pg.click(`${READ} .cb-choice[data-d="wait"]`);
        await until(pg, (sel) => !!document.querySelector(sel), chip);
      }
      await pg.click(`${READ} .cb-notnow .kit-chip[data-id="pr-merges"]`);
    };
    // searchFor types text, waits for its search to be in, and returns what
    // the field offers: each option's key and text.
    const searchFor = async (text) => {
      await field.fill(text);
      await until(
        pg,
        ([sel, v]) => document.querySelector(sel)?.dataset.searched === v,
        [FIELD, text],
      );
      return pg.$$eval(
        `${READ} .cb-pick-opts:not([hidden]) .cb-pick-opt`,
        (els) =>
          els.map((e) => ({
            key: e.getAttribute('data-key') ?? '',
            says: e.textContent ?? '',
          })),
      );
    };

    await prMerges();
    check(
      '"when a PR merges…" asks for one value (one field shows)',
      (await field.count()) === 1 && (await field.isVisible()),
    );
    const eg = await pg.evaluate(
      (sel) =>
        document.querySelector(sel)?.checkVisibility() === true
          ? document.querySelector(sel).textContent
          : '',
      `${READ} .cb-pick-eg`,
    );
    check(
      `under the field, what it takes ("${eg}")`,
      eg === 'tackle#58, a title, or https://github.com/owner/repo/pull/58',
    );

    // Junk: no options, and Enter decides nothing.
    const junk = await searchFor('zzqx nothing like it');
    check(`junk offers nothing (${junk.length} options)`, junk.length === 0);
    await field.press('Enter');
    await pg.waitForTimeout(400);
    col = await readCol(pg);
    check(
      `and Enter decides nothing: issue #5 stays undecided and open (${col.key})`,
      (await decisionOf(ISSUE(5))) === null && col.key === ISSUE(5),
    );

    // New text makes the options shown for older text stale at once: they
    // can't be chosen while the search for the new text runs.
    {
      const old = await searchFor('retry');
      const stale = await pg.evaluate(
        ([sel, opts]) => {
          const input = document.querySelector(sel);
          input.value = 'retry the uploader zzqx';
          input.dispatchEvent(new Event('input', { bubbles: true }));
          const shown = [...document.querySelectorAll(opts)];
          shown[0]?.click();
          return {
            shown: shown.length,
            disabled: shown.filter((b) => b.disabled).length,
          };
        },
        [FIELD, `${READ} .cb-pick-opt`],
      );
      check(
        `typing more disables the ${old.length} option(s) shown for "retry" at once (${stale.disabled} of ${stale.shown} disabled)`,
        old.length === 1 && stale.shown === 1 && stale.disabled === 1,
      );
      await pg.waitForTimeout(400);
      check(
        'and clicking one decides nothing: issue #5 stays undecided and open',
        (await decisionOf(ISSUE(5))) === null &&
          (await readCol(pg)).key === ISSUE(5),
      );
    }

    // A title finds the tracked pull request; choosing it decides.
    const byTitle = await searchFor('retry');
    checkList(
      'typing "retry" finds the tracked pull request by its title',
      byTitle.map((o) => `${o.key} | ${o.says}`),
      [`${PR(7)} | ${PR(7)} \u00b7 Add a retry to the uploader`],
    );
    await shoot(t, pg, 'pick', ['light']);
    await pg.click(`${READ} .cb-pick-opt[data-key="${PR(7)}"]`);
    check(
      `choosing it decides issue #5 wait until merged(${PR(7)})`,
      await eventually(async () => {
        const d = await decisionOf(ISSUE(5));
        return d?.disposition === 'wait' && d.until === `merged(${PR(7)})`;
      }),
    );
    check('and issue #6 opens next', await opened(t, pg, ISSUE(6)));
    await press(pg, 'u');
    check(
      'u undoes it: issue #5 is undecided again and opens',
      (await eventually(async () => (await decisionOf(ISSUE(5))) === null)) &&
        (await opened(t, pg, ISSUE(5))),
    );

    // A pasted URL of a pull request casebook doesn't track.
    const UP = 'pr:up/stream#58';
    await prMerges();
    const byUrl = await searchFor(
      'https://github.com/Up/Stream/pull/58/files?w=1#diff',
    );
    checkList(
      'a pasted URL of an untracked pull request is offered as not tracked',
      byUrl.map((o) => `${o.key} | ${o.says}`),
      [`${UP} | ${UP} \u00b7 not tracked; checked on the next sync`],
    );
    await pg.click(`${READ} .cb-pick-opt[data-key="${UP}"]`);
    check(
      `choosing it decides issue #5 wait until merged(${UP})`,
      await eventually(async () => {
        const d = await decisionOf(ISSUE(5));
        return d?.disposition === 'wait' && d.until === `merged(${UP})`;
      }),
    );
    check('and issue #6 opens next again', await opened(t, pg, ISSUE(6)));
    await press(pg, 'u');
    check(
      'u undoes that too: issue #5 opens, undecided',
      (await eventually(async () => (await decisionOf(ISSUE(5))) === null)) &&
        (await opened(t, pg, ISSUE(5))),
    );
  }

  // ---- a live decide of the next item elsewhere doesn't skip the one after --
  // (Kept, it moves to the list's left-open group: it leaves the rows that
  // need a decision.)
  await api('POST', '/api/decide', { keys: [ISSUE(6)], disposition: 'keep' });
  await until(
    pg,
    (kick) =>
      ![
        ...document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .kit-row:not(.cb-left-open) .kit-kicker',
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

    // ---- u after a re-decide puts the previous decision back --------------
    const WAS = `merged(${GONE})`;
    await pg.click(
      '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="keep"]',
    );
    check(
      're-deciding issue #4 keep',
      await eventually(
        async () => (await decisionOf(ISSUE(4)))?.disposition === 'keep',
      ),
    );
    await press(pg, 'u');
    check(
      `u puts back issue #4's previous decision (wait until ${WAS}) and opens it`,
      (await eventually(async () => {
        const d = await decisionOf(ISSUE(4));
        return d?.disposition === 'wait' && d.until === WAS;
      })) && (await opened(t, pg, ISSUE(4))),
    );

    // ---- u after a re-decide refuses a change made since -------------------
    // Within the same second: serve compares the whole decision.
    await pg.click(
      '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="keep"]',
    );
    check(
      're-deciding issue #4 keep again',
      await eventually(
        async () => (await decisionOf(ISSUE(4)))?.disposition === 'keep',
      ),
    );
    await api('POST', '/api/decide', {
      keys: [ISSUE(4)],
      disposition: 'ignore',
      note: 'changed by the agent',
    });
    await press(pg, 'u');
    const ERR = '.kit-app > .kit-read:not([hidden]) .cb-choice-err';
    const refused = await until(
      pg,
      (sel) =>
        [...document.querySelectorAll(sel)].some(
          (e) => !e.hidden && (e.textContent ?? '').includes('changed since'),
        ),
      ERR,
      5000,
    );
    const errShown = await pg.$$eval(ERR, (els) =>
      els.map((e) => e.textContent ?? '').join(' | '),
    );
    check(
      `u after issue #4 changed elsewhere shows serve's refusal ("${errShown}")`,
      refused,
    );
    const after = await decisionOf(ISSUE(4));
    check(
      `and the change stands (issue #4 is ${after?.disposition}, "${after?.note ?? ''}")`,
      after?.disposition === 'ignore' && after.note === 'changed by the agent',
    );
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
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });

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

  // ---- the recommendation's reason is never the closing comment -----------
  {
    const ds = await Promise.all([RPR(9), RPR(10)].map(decisionOf));
    check(
      `agreeing with the closes records no note: the reason ("superseded") stays on the proposals (notes: ${ds.map((d) => JSON.stringify(d?.note ?? '')).join(', ')})`,
      ds.every((d) => d?.disposition === 'close' && !d.note),
    );
    const plan = await api('POST', '/api/apply/plan', {
      keys: [RPR(9), RPR(10)],
    });
    await pg.evaluate((j) => {
      location.hash = `#/apply/${j}`;
    }, plan.job?.id);
    await until(
      pg,
      () => document.querySelectorAll('.cb-plan .cb-plan-step').length === 2,
      undefined,
      8000,
    );
    const shown = await pg.$$eval('.cb-plan .cb-plan-step', (els) =>
      els.map(
        (e) =>
          `${e.dataset.key} :: ${e.querySelector('.cb-cmd')?.textContent ?? ''}`,
      ),
    );
    checkList(
      "To apply's steps for the agreed closes post no comment",
      shown.sort(),
      [9, 10]
        .map((n) => `${RPR(n)} :: gh pr close ${n} -R 'schuettc/${REC}'`)
        .sort(),
    );
    const steps = plan.job?.steps ?? [];
    check(
      `and serve's steps post nothing (${steps.map((st) => st.posts === true).join(', ')})`,
      steps.length === 2 && steps.every((st) => !st.posts),
    );
  }
  await pg.close();
}

// ---- move-on past the first page ---------------------------------------------

const PAGES = 'rd-pages';
const PAGES_N = 230;

function pagesSeed() {
  const issues = [];
  for (let n = 1; n <= PAGES_N; n++) {
    const at = `2026-09-01T0${Math.floor(n / 60) % 10}:${String(n % 60).padStart(2, '0')}:00Z`;
    issues.push({
      repo: `schuettc/${PAGES}`,
      number: n,
      title: `Question number ${n}`,
      author: 'alice',
      state: 'OPEN',
      created_at: at,
      updated_at: at,
    });
  }
  return {
    seedRepos: [
      {
        repo: `schuettc/${PAGES}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [],
        issues,
      },
    ],
  };
}

async function pagesScenario(context, t, s) {
  const { check, until, eventually } = t;
  console.log('\nscenario: move-on past the first page');
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const decisionOf = async (key) =>
    (await api('GET', `/api/item?key=${encodeURIComponent(key)}`)).item
      .decision ?? null;

  // Only this repo's items wait on Court.
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${PAGES}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });
  const view = await api('GET', '/api/items?view=waiting&limit=500');
  const keys = (view.items ?? []).map((it) => it.key);
  check(
    `waiting on you holds all ${PAGES_N} issues, more than a page (${view.total})`,
    view.total === PAGES_N && keys.length === PAGES_N,
  );
  // Page two starts at 200: decide its sixth row; its seventh opens.
  const target = keys[205];
  const expected = keys[206];

  const pg = await context.newPage();
  await pg.goto(`${s.url}#/attention/waiting`, {
    waitUntil: 'domcontentloaded',
  });
  await pg.waitForSelector('.kit-row', { timeout: 8000 });
  await pg.click('.cb-foot-more');
  check(
    'show more brings in page two',
    await until(
      pg,
      (n) =>
        document.querySelectorAll('.kit-app > .kit-list:not([hidden]) .kit-row')
          .length === n,
      PAGES_N,
      8000,
    ),
  );
  await row(pg, target).click();
  check(`${target} (page two) opens`, await opened(t, pg, target));
  await pg.click(
    '.kit-app > .kit-read:not([hidden]) .cb-choice[data-d="keep"]',
  );
  check(
    `deciding ${target} keep`,
    await eventually(
      async () => (await decisionOf(target))?.disposition === 'keep',
    ),
  );
  check(
    `the next item on page two, ${expected}, opens (not page one's first, ${keys[0]})`,
    await opened(t, pg, expected),
  );
  // Opening it reloaded the list's first page; the next decide still goes
  // on in page two.
  await press(pg, '1');
  check(
    `deciding ${expected} with 1 opens ${keys[207]}, still on page two`,
    (await eventually(
      async () => (await decisionOf(expected))?.disposition === 'keep',
    )) && (await opened(t, pg, keys[207])),
  );
  await pg.close();
}

// ---- the closing comment and the GitHub link ---------------------------------

const CC = 'lc-close';
const CISSUE = (n) => `issue:schuettc/${CC}#${n}`;
const CPR = (n) => `pr:schuettc/${CC}#${n}`;
// The closing comments typed here.
const SAID = 'Thanks, fixed in v2 (see the release notes).';
const SAID_ENTER = 'Answered in the README; closing.';
const TYPED_NOT_SENT = 'typed, then closed without it';

function closeSeed() {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${CC}`,
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
        repo: `schuettc/${CC}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [[4, 'Retry the uploader', 'bob']].map(item),
        issues: [
          [1, 'Region question', 'alice'],
          [2, 'README typo', 'carol'],
          [3, 'Tags question', 'dave'],
        ].map(item),
      },
    ],
  };
}

// shootLC takes /tmp/lc-<name>-light.png (light, nothing focused unless
// keepFocus: the field open under the Close card shows its caret).
async function shootLC(t, pg, name, keepFocus = false) {
  await setTheme(pg, 'light');
  if (!keepFocus) await pg.evaluate(() => document.activeElement?.blur());
  const got = await pg.evaluate(() => ({
    theme: document.documentElement.dataset.theme,
    bg: getComputedStyle(document.body).backgroundColor,
  }));
  const path = `/tmp/lc-${name}-light.png`;
  t.check(
    `${path} is light (theme ${got.theme}, body ${got.bg})`,
    got.theme === 'light' && got.bg === BG.light,
  );
  await pg.screenshot({ path });
}

async function closeCommentScenario(context, t, s) {
  const { check, until, eventually } = t;
  console.log('\nscenario: the closing comment and the GitHub link');
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const decisionOf = async (key) =>
    (await api('GET', `/api/item?key=${encodeURIComponent(key)}`)).item
      .decision ?? null;
  const READ = '.kit-app > .kit-read:not([hidden])';
  const FIELD = `${READ} .cb-close-comment`;

  // Only this repo's items wait on Court.
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${CC}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });

  const pg = await context.newPage();
  await pg.goto(`${s.url}#/attention/waiting`, {
    waitUntil: 'domcontentloaded',
  });
  await pg.waitForSelector('.kit-row', { timeout: 8000 });
  await setTheme(pg, 'light');

  // kickerOf reads the open item's kicker: its text, and its link (if any).
  const kickerOf = () =>
    pg.evaluate((read) => {
      const k = document.querySelector(`${read} .cb-kicker`);
      const a = k?.querySelector('a');
      return {
        text: k?.textContent ?? '',
        links: k?.querySelectorAll('a').length ?? 0,
        href: a?.getAttribute('href') ?? '',
        target: a?.getAttribute('target') ?? '',
        rel: a?.getAttribute('rel') ?? '',
        linkText: a?.textContent ?? '',
      };
    }, READ);
  const openByHash = async (key) => {
    await pg.evaluate((k) => {
      location.hash = '#/item/' + encodeURIComponent(k);
    }, key);
    return opened(t, pg, key);
  };

  // ---- the kicker links to GitHub ------------------------------------------
  await row(pg, CISSUE(1)).click();
  check('issue #1 opens', await opened(t, pg, CISSUE(1)));
  {
    const k = await kickerOf();
    check(
      `an issue's kicker links its key to its GitHub page, in a new tab (${k.links} link: "${k.linkText}" → ${k.href}, target ${k.target}, rel ${k.rel})`,
      k.links === 1 &&
        k.linkText === `schuettc/${CC}#1` &&
        k.href === `https://github.com/schuettc/${CC}/issues/1` &&
        k.target === '_blank' &&
        k.rel.split(' ').includes('noopener'),
    );
    check(
      `the kicker still reads kind · key · relation ("${k.text}")`,
      k.text === `issue \u00b7 schuettc/${CC}#1 \u00b7 incoming`,
    );
    const muted = await cssColor(pg, 'var(--kit-muted)');
    const signal = await cssColor(pg, 'var(--kit-signal)');
    const mono = await pg.evaluate(() => {
      const d = document.createElement('span');
      d.style.setProperty('font-family', 'var(--kit-mono)');
      document.body.append(d);
      const v = getComputedStyle(d).fontFamily;
      d.remove();
      return v;
    });
    const st = await pg.evaluate((read) => {
      const k = document.querySelector(`${read} .cb-kicker`);
      const a = k.querySelector('a');
      return {
        kFont: getComputedStyle(k).fontFamily,
        kColor: getComputedStyle(k).color,
        aFont: getComputedStyle(a).fontFamily,
        aColor: getComputedStyle(a).color,
      };
    }, READ);
    check(
      `the kicker stays mono and muted (${st.kFont}; ${st.kColor}), the link in its mono (${st.aFont})`,
      st.kFont === mono && st.kColor === muted && st.aFont === mono,
    );
    check(
      `the link has the kit's link colour (${st.aColor})`,
      st.aColor === signal && st.aColor !== muted,
    );
  }
  await shootLC(t, pg, 'kicker');

  // A pull request's and a repository's pages; a branch has none.
  {
    check('pr #4 opens', await openByHash(CPR(4)));
    const pr = await kickerOf();
    check(
      `a pull request's kicker links to its pull page (${pr.href})`,
      pr.links === 1 &&
        pr.href === `https://github.com/schuettc/${CC}/pull/4` &&
        pr.target === '_blank',
    );
    check('the repo opens', await openByHash(`repo:schuettc/${CC}`));
    const repo = await kickerOf();
    check(
      `a repository's kicker links to its page (${repo.href})`,
      repo.links === 1 &&
        repo.href === `https://github.com/schuettc/${CC}` &&
        repo.target === '_blank',
    );
    const branches =
      (await api('GET', '/api/items?view=tracked&kind=branch&limit=1')).items ??
      [];
    check(
      `the fixture tracks a branch (${branches[0]?.key ?? 'none'}), with no url from serve ("${branches[0]?.url ?? ''}")`,
      branches.length === 1 && !branches[0].url,
    );
    if (branches.length) {
      check('the branch opens', await openByHash(branches[0].key));
      const br = await kickerOf();
      check(
        `a branch's kicker has no link, its key plain text ("${br.text}", ${br.links} links)`,
        br.links === 0 && br.text.startsWith('branch \u00b7 '),
      );
    }
    await row(pg, CISSUE(1)).click();
    await opened(t, pg, CISSUE(1));
  }

  // ---- choosing Close opens the closing-comment field ---------------------
  const fieldState = () =>
    pg.evaluate((sel) => {
      const f = document.querySelector(sel);
      const input = f?.querySelector('input.kit-note');
      const cards = [
        ...document.querySelectorAll(
          '.kit-app > .kit-read:not([hidden]) .cb-choices .cb-choice',
        ),
      ];
      const close = cards.find((c) => c.dataset.d === 'close');
      const last = cards.at(-1);
      return {
        shown: !!f && f.checkVisibility(),
        placeholder: input?.getAttribute('placeholder') ?? '',
        focused: !!input && document.activeElement === input,
        buttons: [...(f?.querySelectorAll('.kit-btn') ?? [])].map(
          (b) =>
            `${b.textContent}${b.classList.contains('fill') ? ' (fill)' : ''}`,
        ),
        below:
          !!f &&
          !!last &&
          f.getBoundingClientRect().top >= last.getBoundingClientRect().bottom,
        closeOn: !!close?.classList.contains('on'),
        value: input?.value ?? '',
      };
    }, FIELD);
  await pg.click(`${READ} .cb-choice[data-d="close"]`);
  await until(
    pg,
    (sel) => !!document.querySelector(sel)?.checkVisibility(),
    FIELD,
  );
  {
    const f = await fieldState();
    check(
      `choosing "Close it" opens the closing-comment field under the cards, focused ("${f.placeholder}")`,
      f.shown &&
        f.below &&
        f.focused &&
        f.placeholder === 'closing comment, posted when you approve the plan',
    );
    check(
      `with two buttons: ${f.buttons.join(', ')}`,
      f.buttons.join('|') ===
        'close with this comment (fill)|close without comment',
    );
    await pg.waitForTimeout(400);
    check(
      `and decides nothing yet (issue #1 undecided, still open: ${(await readCol(pg)).key})`,
      (await decisionOf(CISSUE(1))) === null &&
        (await readCol(pg)).key === CISSUE(1),
    );
  }
  await pg.locator(`${FIELD} input`).fill(SAID);
  await shootLC(t, pg, 'comment', true);

  // ---- Esc cancels ---------------------------------------------------------
  await pg.keyboard.press('Escape');
  {
    const ok = await until(
      pg,
      (sel) => !document.querySelector(sel)?.checkVisibility(),
      FIELD,
    );
    const f = await fieldState();
    await pg.waitForTimeout(300);
    check(
      `Esc cancels: the field closes, "Close it" is no longer chosen, issue #1 stays undecided and open`,
      ok &&
        !f.shown &&
        !f.closeOn &&
        (await decisionOf(CISSUE(1))) === null &&
        (await readCol(pg)).key === CISSUE(1),
    );
  }

  // ---- a number key picks the card: the field opens, empty ----------------
  await press(pg, '2');
  {
    await until(
      pg,
      (sel) => !!document.querySelector(sel)?.checkVisibility(),
      FIELD,
    );
    const f = await fieldState();
    check(
      `the number key 2 picks "Close it": the field opens again, empty ("${f.value}"), deciding nothing`,
      f.shown && f.value === '' && (await decisionOf(CISSUE(1))) === null,
    );
  }

  // ---- "close with this comment" records the note -------------------------
  await pg.locator(`${FIELD} input`).fill(SAID);
  await pg
    .locator(`${FIELD} .kit-btn`, { hasText: /^close with this comment$/ })
    .click();
  check(
    `"close with this comment" decides issue #1 close with the note "${SAID}"`,
    await eventually(async () => {
      const d = await decisionOf(CISSUE(1));
      return d?.disposition === 'close' && d.note === SAID;
    }),
  );
  check('and issue #2 opens next', await opened(t, pg, CISSUE(2)));

  // ---- ↵ in the field closes with the comment -----------------------------
  await pg.click(`${READ} .cb-choice[data-d="close"]`);
  await until(
    pg,
    (sel) => !!document.querySelector(sel)?.checkVisibility(),
    FIELD,
  );
  await pg.locator(`${FIELD} input`).fill(SAID_ENTER);
  await pg.locator(`${FIELD} input`).press('Enter');
  check(
    `↵ in the field decides issue #2 close with the note "${SAID_ENTER}"`,
    await eventually(async () => {
      const d = await decisionOf(CISSUE(2));
      return d?.disposition === 'close' && d.note === SAID_ENTER;
    }),
  );
  check('and issue #3 opens next', await opened(t, pg, CISSUE(3)));

  // ---- "close without comment" records no note, even with text typed ------
  await pg.click(`${READ} .cb-choice[data-d="close"]`);
  await until(
    pg,
    (sel) => !!document.querySelector(sel)?.checkVisibility(),
    FIELD,
  );
  await pg.locator(`${FIELD} input`).fill(TYPED_NOT_SENT);
  await pg
    .locator(`${FIELD} .kit-btn`, { hasText: /^close without comment$/ })
    .click();
  check(
    'with text typed, "close without comment" decides issue #3 close with no note',
    await eventually(async () => {
      const d = await decisionOf(CISSUE(3));
      return d?.disposition === 'close' && !d.note;
    }),
  );

  // ---- To apply: the steps post exactly that text, or nothing -------------
  {
    const plan = await api('POST', '/api/apply/plan', {
      keys: [CISSUE(1), CISSUE(3)],
    });
    const id = plan.job?.id;
    await pg.evaluate((j) => {
      location.hash = `#/apply/${j}`;
    }, id);
    const cmdOf = (key) =>
      until(
        pg,
        (k) =>
          !!document.querySelector(
            `.cb-plan .cb-plan-step[data-key="${CSS.escape(k)}"] .cb-cmd`,
          ),
        key,
        8000,
      ).then(() =>
        pg.$eval(
          `.cb-plan .cb-plan-step[data-key="${key.replace(/"/g, '\\"')}"] .cb-cmd`,
          (e) => e.textContent ?? '',
        ),
      );
    const withComment = await cmdOf(CISSUE(1));
    check(
      `To apply's step for issue #1 posts exactly the comment ("${withComment}")`,
      withComment ===
        `gh issue close 1 -R 'schuettc/${CC}' --comment '${SAID}'`,
    );
    const without = await cmdOf(CISSUE(3));
    check(
      `To apply's step for issue #3 has no comment ("${without}")`,
      without === `gh issue close 3 -R 'schuettc/${CC}'` &&
        !without.includes('--comment'),
    );
    const steps = plan.job?.steps ?? [];
    const posts = (k) => steps.find((st) => st.key === k)?.posts === true;
    check(
      `serve's step for issue #1 posts (it shows the comment again before it's posted); issue #3's posts nothing`,
      posts(CISSUE(1)) && !posts(CISSUE(3)),
    );
  }

  // ---- the card's accept on a close recommendation asks too ---------------
  {
    const sid = 'probe-lc-close';
    const REASON = 'Superseded by the new uploader; the author agreed.';
    await agent.presence(sid, 'pi \u00b7 lc', '/home/court/lc', 'pi');
    await agent.propose(sid, [CPR(4)], 'close', REASON);
    check('pr #4 opens, recommended', await openByHash(CPR(4)));
    await until(
      pg,
      (read) =>
        !!document.querySelector(`${read} .cb-proposal-card .kit-btn.fill`),
      READ,
    );
    await pg.click(`${READ} .cb-proposal-card .kit-btn.fill`);
    await until(
      pg,
      (sel) => !!document.querySelector(sel)?.checkVisibility(),
      FIELD,
    );
    const f = await fieldState();
    await pg.waitForTimeout(400);
    check(
      `the card's accept opens the closing-comment field, empty ("${f.value}"), not the reason, and accepts nothing yet`,
      f.shown && f.value === '' && (await decisionOf(CPR(4))) === null,
    );
    await pg.locator(`${FIELD} input`).fill(SAID);
    await pg
      .locator(`${FIELD} .kit-btn`, { hasText: /^close with this comment$/ })
      .click();
    check(
      `"close with this comment" accepts it: pr #4 is decided close, proposed by the session, with Court's comment as its note`,
      await eventually(async () => {
        const d = await decisionOf(CPR(4));
        return (
          d?.disposition === 'close' &&
          (d.proposed_by ?? '').includes(sid) &&
          d.note === SAID
        );
      }),
    );
  }
  await pg.close();
}

// ---- Leave it open stays in the list (casebook 0.4.3) ----------------------

const LO = 'lo-left';
const LISSUE = (n) => `issue:schuettc/${LO}#${n}`;

function leftOpenSeed() {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${LO}`,
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
        repo: `schuettc/${LO}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        issues: [
          [1, 'Question about the region flag', 'alice'],
          [2, 'Typo in the install guide', 'carol'],
          [3, 'Support a second profile', 'dave'],
          [4, 'Docs for the new option', 'erin'],
          [5, 'Crash when the cache is empty', 'frank'],
        ].map(item),
      },
    ],
  };
}

// shootLO takes /tmp/lo-<name>-light.png, light, nothing focused.
async function shootLO(t, pg, name) {
  await setTheme(pg, 'light');
  await pg.evaluate(() => document.activeElement?.blur());
  const got = await pg.evaluate(() => ({
    theme: document.documentElement.dataset.theme,
    bg: getComputedStyle(document.body).backgroundColor,
  }));
  const path = `/tmp/lo-${name}-light.png`;
  t.check(
    `${path} is light (theme ${got.theme}, body ${got.bg})`,
    got.theme === 'light' && got.bg === BG.light,
  );
  await pg.screenshot({ path });
}

// listState reads the shown list: each row's key (its kicker), whether it is
// in the left-open group, its meta and sub; the group's divider (its text,
// and how many rows come before it); and the waiting chip's count.
function listState(pg) {
  return pg.evaluate(() => {
    const list = document.querySelector('.kit-app > .kit-list:not([hidden])');
    const body = list?.querySelector('.kit-rows');
    const kids = [...(body?.children ?? [])];
    const head = list?.querySelector('.cb-left-open-head');
    const chip = list?.querySelector('.kit-chip[data-id="waiting"] .kit-n');
    return {
      rows: kids
        .filter((el) => el.classList.contains('kit-row'))
        .map((el) => ({
          key: (el.querySelector('.kit-kicker')?.textContent ?? '').replace(
            ' \u00b7 ',
            ':',
          ),
          left: el.classList.contains('cb-left-open'),
          back: el.classList.contains('cb-new-activity'),
          meta: el.querySelector('.kit-meta')?.textContent ?? '',
          sub: el.querySelector('.kit-sub')?.textContent ?? '',
          color: getComputedStyle(el.querySelector('.kit-title')).color,
        })),
      head: head?.textContent ?? '',
      headAt: head
        ? kids.filter(
            (el) =>
              el.classList.contains('kit-row') &&
              el.compareDocumentPosition(head) &
                Node.DOCUMENT_POSITION_FOLLOWING,
          ).length
        : -1,
      count: chip ? parseInt(chip.textContent ?? '0', 10) : 0,
    };
  });
}

async function leftOpenScenario(context, t, s) {
  const { check, checkList, until, eventually } = t;
  console.log('\nscenario: Leave it open stays in the list');
  let agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const decisionOf = async (key) =>
    (await api('GET', `/api/item?key=${encodeURIComponent(key)}`)).item
      .decision ?? null;
  const READ = '.kit-app > .kit-read:not([hidden])';

  // Only this repo's items wait on Court: the fixture's stop being tracked
  // (kept, they would stay in the list, left open).
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${LO}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });

  const pg = await context.newPage();
  const load = async () => {
    await pg.goto(`${s.url}#/attention/waiting`, {
      waitUntil: 'domcontentloaded',
    });
    await pg.waitForSelector('.kit-row', { timeout: 8000 });
    await setTheme(pg, 'light');
  };
  await load();
  {
    const st = await listState(pg);
    checkList(
      'waiting on you holds the five seeded issues, with no left-open group',
      st.rows.map((r) => r.key + (r.left ? ' (left open)' : '')),
      [1, 2, 3, 4, 5].map(LISSUE),
    );
    check(
      `the waiting chip counts 5 (${st.count}), and there is no divider ("${st.head}")`,
      st.count === 5 && st.head === '',
    );
  }

  // ---- Leave it open: the row moves down, the count drops, move-on -------
  await row(pg, LISSUE(1)).click();
  check('issue #1 opens', await opened(t, pg, LISSUE(1)));
  await pg.click(`${READ} .cb-choice[data-d="keep"]`);
  check(
    'Leave it open decides issue #1 keep',
    await eventually(
      async () => (await decisionOf(LISSUE(1)))?.disposition === 'keep',
    ),
  );
  check(
    'move-on opens the next undecided item, issue #2',
    await opened(t, pg, LISSUE(2)),
  );
  check(
    'the list settles with the left-open divider',
    await until(
      pg,
      () =>
        document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .cb-left-open-head',
        )?.textContent === 'left open \u00b7 1',
    ),
  );
  {
    const st = await listState(pg);
    const today = new Date().toLocaleDateString('en-US', {
      month: 'short',
      day: 'numeric',
    });
    checkList(
      'issue #1 moved to the bottom, in the left-open group',
      st.rows.map((r) => r.key + (r.left ? ' (left open)' : '')),
      [2, 3, 4, 5].map(LISSUE).concat([`${LISSUE(1)} (left open)`]),
    );
    check(
      `the divider "${st.head}" comes after the 4 items that need a decision (at ${st.headAt})`,
      st.head === 'left open \u00b7 1' && st.headAt === 4,
    );
    const lo = st.rows.find((r) => r.key === LISSUE(1));
    check(
      `the left-open row reads "left open · ${today}" ("${lo?.meta}")`,
      lo?.meta === `left open \u00b7 ${today}`,
    );
    const muted = await cssColor(pg, 'var(--kit-muted)');
    const live = st.rows.find((r) => r.key === LISSUE(2));
    check(
      `the left-open row is muted (${lo?.color} = --kit-muted ${muted}; a live row ${live?.color})`,
      lo?.color === muted && live?.color !== muted,
    );
    check(`the waiting chip's count drops to 4 (${st.count})`, st.count === 4);
  }

  // Move-on never opens a left-open row: deciding the view's last item
  // wraps to its first undecided one (issue #2), not to issue #1.
  await row(pg, LISSUE(5)).click();
  check('issue #5 opens', await opened(t, pg, LISSUE(5)));
  await pg.click(`${READ} .cb-choice[data-d="keep"]`);
  check(
    'Leave it open decides issue #5 keep',
    await eventually(
      async () => (await decisionOf(LISSUE(5)))?.disposition === 'keep',
    ),
  );
  check(
    'move-on after the last item opens the first undecided one (issue #2), not a left-open row',
    await opened(t, pg, LISSUE(2)),
  );
  check(
    'the group counts 2',
    await until(
      pg,
      () =>
        document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .cb-left-open-head',
        )?.textContent === 'left open \u00b7 2',
    ),
  );

  // Opening a left-open row shows its decision and the cards as usual.
  await row(pg, LISSUE(1)).click();
  check('the left-open issue #1 opens', await opened(t, pg, LISSUE(1)));
  {
    const col = await readCol(pg);
    const iv = (await api('GET', '/api/decisions/vocabulary')).kinds.find(
      (k) => k.kind === 'issue',
    );
    checkList(
      "its cards are serve's choices for an issue",
      col.cards.map((c) => c.label),
      iv.choices.map((c) => c.label),
    );
    check(
      `its decision shows: Leave it open is the chosen card (${col.cards.filter((c) => c.on).map((c) => c.label)})`,
      col.cards
        .filter((c) => c.on)
        .map((c) => c.d)
        .join() === 'keep',
    );
    check(
      `the card says what Leave it open does now ("${col.cards[0]?.says}")`,
      col.cards[0]?.says ===
        'It stays open and stays in your list, at the bottom. It moves back up when someone replies or it changes.',
    );
  }
  await shootLO(t, pg, 'list');

  // ---- new activity brings it back ----------------------------------------
  // A sync records alice's comment on issue #1 after the decision, and
  // Court's own on issue #5: the GitHub cache, then serve restarts and
  // builds from it.
  const d1 = await decisionOf(LISSUE(1));
  const d5 = await decisionOf(LISSUE(5));
  const later = (d) =>
    new Date(new Date(d.decided_at).getTime() + 60000).toISOString();
  {
    const cachePath = join(s.home, 'state', 'github.json');
    const cache = JSON.parse(readFileSync(cachePath, 'utf8'));
    const repo = cache.owners.schuettc.repos.find(
      (r) => r.repo === `schuettc/${LO}`,
    );
    for (const [n, who, d] of [
      [1, 'alice', d1],
      [5, 'schuettc', d5],
    ]) {
      const is = repo.issues.find((i) => i.number === n);
      is.last_comment_author = who;
      is.last_comment_at = later(d);
      is.updated_at = later(d);
    }
    writeFileSync(cachePath, JSON.stringify(cache, null, 2));
  }
  await pg.goto('about:blank');
  await s.restart();
  agent = createAgent(s.base, s.token);
  await load();
  check(
    'after the restart, the list settles with issue #1 at the top',
    await until(
      pg,
      (k) =>
        document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .kit-row .kit-kicker',
        )?.textContent === k,
      kicker(LISSUE(1)),
      8000,
    ),
  );
  {
    const st = await listState(pg);
    checkList(
      "alice's newer comment moves issue #1 back to the top, needing a decision",
      st.rows.map((r) => r.key + (r.left ? ' (left open)' : '')),
      [1, 2, 3, 4].map(LISSUE),
    );
    const back = st.rows[0];
    check(
      `the brought-back row is flagged "${back?.sub}"`,
      back?.back && back.sub === 'new activity since you left it open',
    );
    check(`the waiting chip counts it again: 4 (${st.count})`, st.count === 4);
    // Court's own newer comment on issue #5 doesn't bring it back: it is
    // still left open (in new; with Court's reply it no longer waits on him).
    const nv = await api('GET', '/api/items?view=new&limit=500');
    check(
      "Court's own newer comment leaves issue #5 left open",
      (nv.left_open ?? []).some((it) => it.key === LISSUE(5)) &&
        !(nv.items ?? []).some((it) => it.key === LISSUE(5)) &&
        !st.rows.some((r) => r.key === LISSUE(5)),
    );
  }
  await row(pg, LISSUE(1)).click();
  check('the brought-back issue #1 opens', await opened(t, pg, LISSUE(1)));
  check(
    'its reading column says why it is back',
    (await pg.locator(`${READ} .cb-due-reason`).textContent()) ===
      'new activity since you left it open',
  );
  await shootLO(t, pg, 'back');
  await pg.close();
}

// ---- a new recommendation on the open item (casebook 0.4.5) ---------------

const OR = 'rd-openrec';
const OISSUE = (n) => `issue:schuettc/${OR}#${n}`;
const OPR = (n) => `pr:schuettc/${OR}#${n}`;

function openRecSeed() {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${OR}`,
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
        repo: `schuettc/${OR}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [[4, 'Bump the uploader dependency', 'renovate']].map(item),
        issues: [
          [1, 'Region question', 'alice'],
          [2, 'README typo', 'carol'],
          [3, 'Tags question', 'dave'],
          [5, 'Flag docs', 'erin'],
        ].map(item),
      },
    ],
  };
}

async function openRecScenario(context, t, s) {
  const { check, until } = t;
  console.log('\nscenario: a new recommendation on the open item');
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const READ = '.kit-app > .kit-read:not([hidden])';
  const FIELD = `${READ} .cb-close-comment`;
  const LINE = `${READ} .cb-rec-fresh`;

  // Only this repo's items wait on Court.
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${OR}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });
  const A = 'probe-openrec';
  await agent.presence(A, 'pi \u00b7 openrec', '/home/court/openrec', 'pi');

  const pg = await context.newPage();
  await pg.goto(`${s.url}#/attention/waiting`, {
    waitUntil: 'domcontentloaded',
  });
  await pg.waitForSelector('.kit-row', { timeout: 8000 });
  // A reload would lose this.
  await pg.evaluate(() => {
    window.__openRecNoReload = true;
  });
  const noReload = () => pg.evaluate(() => window.__openRecNoReload === true);
  // tagItem marks the open item's element; tagged says it is still shown
  // (not re-rendered).
  const tagItem = () =>
    pg.evaluate((r) => {
      document.querySelector(`${r} .cb-item`).dataset.probeTag = '1';
    }, READ);
  const tagged = () =>
    pg.evaluate(
      (r) => document.querySelector(`${r} .cb-item`)?.dataset.probeTag === '1',
      READ,
    );
  const recCard = (c) => c.cards.filter((x) => x.rec).map((x) => x.d);

  // ---- the agent proposes for the open item: it shows, no reload ---------
  await row(pg, OPR(4)).click();
  check('pr #4 opens, undecided', await opened(t, pg, OPR(4)));
  {
    const c = await readCol(pg);
    check(
      `before: no recommendation card, no card marked recommended (${recCard(c).join(',') || 'none'})`,
      c.rec === '' && recCard(c).length === 0,
    );
  }
  await agent.propose(A, [OPR(4)], 'close', 'superseded by the next bump');
  {
    const ok = await until(
      pg,
      (r) => !!document.querySelector(`${r} .cb-proposal-card`),
      READ,
      8000,
    );
    const c = await readCol(pg);
    const closeCard = c.cards.find((x) => x.d === 'close');
    check(
      `the recommendation card appears on the open item with its reason ("${c.rec}")`,
      ok &&
        c.key === OPR(4) &&
        c.rec.includes('Close it without merging') &&
        c.rec.includes('superseded by the next bump'),
    );
    check(
      `the proposed choice is marked recommended ("${closeCard?.kick}"), and only it`,
      !!closeCard &&
        closeCard.rec &&
        closeCard.kick.includes('recommended') &&
        recCard(c).join(',') === 'close',
    );
    check('with no reload', await noReload());
  }

  // ---- a proposal for another item leaves the open item as it was --------
  await row(pg, OISSUE(1)).click();
  check('issue #1 opens', await opened(t, pg, OISSUE(1)));
  await until(
    pg,
    (r) => !!document.querySelector(`${r} .cb-question`)?.textContent,
    READ,
  );
  await tagItem();
  const before = await readCol(pg);
  await agent.propose(A, [OISSUE(2)], 'keep', 'still being discussed');
  {
    // The list hears it: issue #2's row gets its line.
    const heard = await until(
      pg,
      (k) =>
        [
          ...document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
          ),
        ].some(
          (r) =>
            r.querySelector('.kit-kicker')?.textContent === k &&
            (r.querySelector('.kit-sub')?.textContent ?? '').includes(
              'recommends',
            ),
        ),
      kicker(OISSUE(2)),
      8000,
    );
    await pg.waitForTimeout(400);
    const after = await readCol(pg);
    check(
      `a proposal for issue #2 (the list heard it: ${heard}) leaves the open issue #1 as it was: not re-rendered, no card, nothing recommended`,
      heard &&
        (await tagged()) &&
        after.key === OISSUE(1) &&
        after.rec === '' &&
        recCard(after).length === 0 &&
        JSON.stringify(after) === JSON.stringify(before),
    );
    check(
      'and no "pi recommends · show" line on it',
      (await pg.locator(LINE).count()) === 0,
    );
  }

  // ---- mid-input: the closing comment typed --------------------------------
  const TYPED = 'Closing: answered in the docs.';
  await row(pg, OISSUE(3)).click();
  check('issue #3 opens', await opened(t, pg, OISSUE(3)));
  await pg.click(`${READ} .cb-choice[data-d="close"]`);
  await until(
    pg,
    (sel) => !!document.querySelector(sel)?.checkVisibility(),
    FIELD,
  );
  await pg.locator(`${FIELD} input`).fill(TYPED);
  await tagItem();
  await agent.propose(A, [OISSUE(3)], 'keep', 'a fix is in review');
  {
    const ok = await until(
      pg,
      (sel) => !!document.querySelector(sel)?.checkVisibility(),
      LINE,
      8000,
    );
    const line = ok ? ((await pg.textContent(LINE)) ?? '') : '';
    check(
      `with the closing comment typed, the line shows instead ("${line}")`,
      ok && line === 'pi recommends Leave it open \u00b7 show',
    );
    const st = await pg.evaluate((sel) => {
      const f = document.querySelector(sel);
      return {
        shown: !!f?.checkVisibility(),
        value: f?.querySelector('input')?.value ?? '',
      };
    }, FIELD);
    const c = await readCol(pg);
    check(
      `and the typed comment stays ("${st.value}", field ${st.shown ? 'open' : 'closed'}), not re-rendered: no card yet`,
      st.shown && st.value === TYPED && (await tagged()) && c.rec === '',
    );
  }
  await pg.click(`${LINE} button`);
  {
    const ok = await until(
      pg,
      (r) => !!document.querySelector(`${r} .cb-proposal-card`),
      READ,
      8000,
    );
    const c = await readCol(pg);
    check(
      `"show" re-renders it: the card ("${c.rec}") and "Leave it open" marked recommended`,
      ok &&
        c.key === OISSUE(3) &&
        c.rec.includes('a fix is in review') &&
        recCard(c).join(',') === 'keep' &&
        (await pg.locator(LINE).count()) === 0,
    );
  }

  // ---- mid-input: the Not now picker open ----------------------------------
  await row(pg, OISSUE(5)).click();
  check('issue #5 opens', await opened(t, pg, OISSUE(5)));
  await pg.click(`${READ} .cb-choice[data-d="wait"]`);
  await until(
    pg,
    (r) => !!document.querySelector(`${r} .cb-notnow`)?.checkVisibility(),
    READ,
  );
  await tagItem();
  await agent.propose(A, [OISSUE(5)], 'close', 'answered');
  {
    const ok = await until(
      pg,
      (sel) => !!document.querySelector(sel)?.checkVisibility(),
      LINE,
      8000,
    );
    const line = ok ? ((await pg.textContent(LINE)) ?? '') : '';
    const picker = await pg.evaluate(
      (r) => !!document.querySelector(`${r} .cb-notnow`)?.checkVisibility(),
      READ,
    );
    check(
      `with the Not now picker open, it stays open (${picker}) and the line shows ("${line}")`,
      ok &&
        picker &&
        (await tagged()) &&
        line === 'pi recommends Close it \u00b7 show',
    );
  }
  check('still no reload', await noReload());

  // ---- "recommends", as the list's rows say -------------------------------
  {
    await row(pg, OPR(4)).click();
    await opened(t, pg, OPR(4));
    const head = await pg
      .locator(`${READ} .cb-proposal-card .kit-card-head`)
      .textContent({ timeout: 8000 })
      .catch(() => '');
    check(
      `the recommendation card's head reads "pi recommends · Close it without merging" ("${head}")`,
      head === 'pi recommends \u00b7 Close it without merging',
    );
    await pg.evaluate(() => {
      location.hash = '#/attention/board';
    });
    const sel = `.cb-board .kit-card[data-id="${OPR(4)}"] .cb-card-prop`;
    const ok = await until(pg, (q) => !!document.querySelector(q), sel, 8000);
    const prop = ok ? ((await pg.textContent(sel)) ?? '') : '';
    check(
      `the board's card says "recommends" too ("${prop}")`,
      prop === 'pi recommends close',
    );
  }
  await pg.close();
}

// ---- ask the session to look into it (casebook 0.4.5) ---------------------

const LK = 'rd-look';
const KISSUE = (n) => `issue:schuettc/${LK}#${n}`;
const LOOK_INTO = (key) =>
  `Look into ${key}: check its CI, recent activity and anything blocking it. If a check is failing, find the cause and what would fix it. Add what you find as evidence (casebook_evidence) and recommend what to do with a one-line reason (casebook_propose).`;

function lookIntoSeed() {
  const item = ([number, title, author], i) => ({
    repo: `schuettc/${LK}`,
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
        repo: `schuettc/${LK}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [],
        issues: [
          [1, 'Uploads fail on the second region', 'alice'],
          [2, 'README typo', 'carol'],
        ].map(item),
      },
    ],
  };
}

async function lookIntoScenario(context, t, s) {
  const { check, until, eventually } = t;
  console.log('\nscenario: ask the session to look into it');
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const READ = '.kit-app > .kit-read:not([hidden])';
  const ASK = `${READ} .cb-look-ask`;
  const MARK = `${READ} .cb-looking`;
  const KEY = KISSUE(1);

  // Only this repo's items wait on Court.
  const waiting0 = await api('GET', '/api/items?view=waiting&limit=500');
  const others = (waiting0.items ?? [])
    .map((it) => it.key)
    .filter((k) => !k.includes(`/${LK}`));
  if (others.length)
    await api('POST', '/api/decide', { keys: others, disposition: 'ignore' });
  // Two sessions, so the dock attaches to neither on its own; B has a
  // thread of its own the ask must not reach.
  const A = 'probe-look-a';
  const B = 'probe-look-b';
  await agent.presence(A, 'pi \u00b7 look-a', '/home/court/look-a', 'pi');
  await agent.presence(B, 'pi \u00b7 look-b', '/home/court/look-b', 'pi');
  const bThread = await agent.newThread(B, 'b work');

  const pg = await context.newPage();
  await pg.goto(`${s.url}#/attention/waiting`, {
    waitUntil: 'domcontentloaded',
  });
  await pg.waitForSelector('.kit-row', { timeout: 8000 });
  await pg.evaluate(() => {
    window.__lookNoReload = true;
  });
  await row(pg, KEY).click();
  check('issue #1 opens', await opened(t, pg, KEY));

  // ---- no session attached: no button --------------------------------------
  {
    await pg.waitForTimeout(500);
    const attach = await pg.getAttribute('.cb-dock-header', 'data-attach');
    check(
      `with no session attached (dock: ${attach}) there is no "look into it" button`,
      attach === 'none' && (await pg.locator(ASK).count()) === 0,
    );
  }

  // ---- attach A: the button names it, under the cards ----------------------
  await pg.click(`.cb-dock-chooser .cb-dock-pick-item[data-session="${A}"]`);
  await until(
    pg,
    () =>
      document.querySelector('.cb-dock-header')?.getAttribute('data-attach') ===
      'here',
  );
  const aName = await pg.textContent('[data-testid="dock-session-name"]');
  {
    const ok = await until(
      pg,
      (sel) => !!document.querySelector(sel)?.checkVisibility(),
      ASK,
    );
    const got = await pg.evaluate((sel) => {
      const b = document.querySelector(sel);
      const cards = b?.closest('.cb-decide')?.querySelector('.cb-choices-wrap');
      return {
        text: b?.textContent ?? '',
        below:
          !!b &&
          !!cards &&
          b.getBoundingClientRect().top >= cards.getBoundingClientRect().bottom,
      };
    }, ASK);
    check(
      `with ${aName} attached, "${got.text}" shows under the cards`,
      ok && got.text === `ask ${aName} to look into it` && got.below,
    );
  }

  // ---- the ask: exactly the text, the item attached, A's thread only -------
  const msgsOf = async (session) => {
    const tv = await api(
      'GET',
      `/api/threads?session=${encodeURIComponent(session)}`,
    );
    const out = [];
    for (const th of tv.threads ?? []) {
      const mv = await agent.messages(th.id);
      for (const m of mv.messages ?? []) out.push(m);
    }
    return out;
  };
  await pg.click(ASK);
  let asked = null;
  check(
    "the button puts exactly the ask (serve's words), with the item attached and the look-into purpose, in the attached session's thread, once",
    await eventually(async () => {
      const mine = (await msgsOf(A)).filter((m) => m.body === LOOK_INTO(KEY));
      asked = mine[0] ?? null;
      return (
        mine.length === 1 &&
        JSON.stringify(asked.attached?.keys ?? []) === JSON.stringify([KEY]) &&
        asked.purpose === 'look-into'
      );
    }),
  );
  {
    const b = await msgsOf(B);
    const bt = (await agent.messages(bThread.id)).messages ?? [];
    check(
      `and nothing in any other session's thread (B has ${b.length} messages)`,
      b.every((m) => !m.body.startsWith('Look into')) &&
        bt.every((m) => !m.body.startsWith('Look into')),
    );
    check(
      `it decides nothing (issue #1 undecided: ${JSON.stringify((await api('GET', `/api/item?key=${encodeURIComponent(KEY)}`)).item.decision ?? null)})`,
      !(await api('GET', `/api/item?key=${encodeURIComponent(KEY)}`)).item
        .decision,
    );
  }

  // ---- the marker, while the message is queued -----------------------------
  const marks = () =>
    pg.evaluate(
      ([mark, k]) => {
        const m = document.querySelector(mark);
        const r = [
          ...document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
          ),
        ].find((x) => x.querySelector('.kit-kicker')?.textContent === k);
        const rl = r?.querySelector('.cb-row-looking');
        return {
          item: m && m.checkVisibility() ? (m.textContent ?? '') : '',
          row: rl ? (rl.textContent ?? '') : '',
          itemColor: m ? getComputedStyle(m).color : '',
          rowColor: rl ? getComputedStyle(rl).color : '',
          ask: !!document.querySelector(
            '.kit-app > .kit-read:not([hidden]) .cb-look-ask',
          ),
        };
      },
      [MARK, kicker(KEY)],
    );
  const LOOKING = `${aName} is looking into it`;
  // rowLooking measures the row's marker against its title and its
  // recommendation line (if any).
  const rowLooking = () =>
    pg.evaluate((k) => {
      const r = [
        ...document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .kit-row',
        ),
      ].find((x) => x.querySelector('.kit-kicker')?.textContent === k);
      const m = r?.querySelector('.cb-row-looking');
      const title = r?.querySelector('.kit-title');
      const sub = r?.querySelector('.kit-sub');
      if (!m || !title) return { found: false };
      const cs = getComputedStyle(m);
      const mr = m.getBoundingClientRect();
      const tr = title.getBoundingClientRect();
      const lh = parseFloat(cs.lineHeight);
      return {
        found: true,
        height: Math.round(mr.height),
        line: Math.round(
          Number.isFinite(lh) ? lh : parseFloat(cs.fontSize) * 1.5,
        ),
        left: Math.round(mr.left),
        top: Math.round(mr.top),
        titleLeft: Math.round(tr.left),
        titleBottom: Math.round(tr.bottom),
        ws: cs.whiteSpace,
        overflow: cs.overflowX,
        to: cs.textOverflow,
        color: cs.color,
        font: cs.fontSize,
        sub: sub
          ? {
              left: Math.round(sub.getBoundingClientRect().left),
              color: getComputedStyle(sub).color,
              font: getComputedStyle(sub).fontSize,
            }
          : null,
      };
    }, kicker(KEY));
  {
    const ok = await until(
      pg,
      ([mark, k, want]) => {
        const m = document.querySelector(mark);
        const r = [
          ...document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
          ),
        ].find((x) => x.querySelector('.kit-kicker')?.textContent === k);
        return (
          !!m?.checkVisibility() &&
          m.textContent === want &&
          r?.querySelector('.cb-row-looking')?.textContent === want
        );
      },
      [MARK, kicker(KEY), LOOKING],
      8000,
    );
    const st = (
      await api('GET', `/api/messages?thread=${asked?.thread_id}`)
    ).messages?.find((m) => m.id === asked?.id)?.state;
    const got = await marks();
    check(
      `while the message is ${st}, the item says "${got.item}" under its question, and its row "${got.row}"`,
      ok && st === 'queued' && got.item === LOOKING && got.row === LOOKING,
    );
    const agentC = await cssColor(pg, 'var(--kit-agent)');
    check(
      `the item's marker is the agent colour (${got.itemColor}); the row's is the recommendation line's, the kit's sub line (${got.rowColor})`,
      got.itemColor === agentC && got.rowColor === agentC,
    );
    // The row's marker reads on one line, where the row's sub line goes:
    // under the title, from its left edge, cut with an ellipsis.
    const geo = await rowLooking();
    check(
      `the row's marker is one line high (${geo.height}px, line ${geo.line}px) and starts at the title's left edge (${geo.left} vs ${geo.titleLeft}), under it`,
      geo.found &&
        geo.height > 0 &&
        geo.height <= geo.line * 1.2 &&
        Math.abs(geo.left - geo.titleLeft) < 1 &&
        geo.top >= geo.titleBottom - 1,
    );
    check(
      `and it is cut with an ellipsis, not wrapped (white-space ${geo.ws}, overflow ${geo.overflow}, text-overflow ${geo.to})`,
      geo.ws === 'nowrap' && geo.overflow === 'hidden' && geo.to === 'ellipsis',
    );
    check('and while it looks, the button gives way to the marker', !got.ask);
    const under = await pg.evaluate((mark) => {
      const m = document.querySelector(mark);
      const q = m?.closest('.cb-decide')?.querySelector('.cb-question');
      return (
        !!m &&
        !!q &&
        m.getBoundingClientRect().top >= q.getBoundingClientRect().bottom
      );
    }, MARK);
    check('the marker sits under the question', under);
    await shoot(t, pg, 'looking', ['light']);
  }

  // ---- the agent looks: evidence and a recommendation show live -----------
  const d = await agent.wait(A);
  const ids = (d?.delivery?.messages ?? []).map((m) => m.id);
  check(
    `the agent's turn gets the ask (${ids.length} message)`,
    ids.includes(asked?.id) &&
      (d?.text ?? '').includes(LOOK_INTO(KEY).slice(0, 40)),
  );
  const EVIDENCE =
    'CI: the upload check fails on us-west-2 since the bucket rename.';
  await api('POST', '/api/agent/evidence', {
    session: A,
    key: KEY,
    text: EVIDENCE,
  });
  await agent.propose(
    A,
    [KEY],
    'keep',
    'a fix is one config line; keep it open',
  );
  {
    const ok = await until(
      pg,
      ([r, ev]) =>
        !!document.querySelector(`${r} .cb-proposal-card`) &&
        (document.querySelector(`${r} .cb-item`)?.textContent ?? '').includes(
          ev,
        ),
      [READ, EVIDENCE],
      8000,
    );
    const c = await readCol(pg);
    const got = await marks();
    check(
      `the agent's evidence and recommendation show on the open item ("${c.rec}"), no reload`,
      ok &&
        c.key === KEY &&
        c.rec.includes('a fix is one config line') &&
        c.cards
          .filter((x) => x.rec)
          .map((x) => x.d)
          .join(',') === 'keep' &&
        (await pg.evaluate(() => window.__lookNoReload === true)),
    );
    check(
      `still looking while the message is worked on ("${got.item}")`,
      got.item === LOOKING,
    );
    // With the recommendation on the row too, the marker matches its line.
    const geo = await rowLooking();
    check(
      `on the row, the marker matches "pi recommends …": left ${geo.left} vs ${geo.sub?.left}, colour ${geo.color} vs ${geo.sub?.color}, size ${geo.font} vs ${geo.sub?.font}; one line (${geo.height}px)`,
      geo.found &&
        !!geo.sub &&
        geo.left === geo.sub.left &&
        geo.color === geo.sub.color &&
        geo.font === geo.sub.font &&
        geo.height <= geo.line * 1.2,
    );
  }

  // ---- the reply settles it: the marker goes -------------------------------
  // A live agent keeps its presence fresh (else, past serve's "left" cutoff,
  // the dock has no session to offer the button for).
  await agent.presence(A, 'pi \u00b7 look-a', '/home/court/look-a', 'pi');
  await agent.reply(
    A,
    ids,
    'answered',
    'Looked: evidence and a recommendation are on the item.',
  );
  {
    const ok = await until(
      pg,
      ([mark, k]) => {
        const m = document.querySelector(mark);
        const r = [
          ...document.querySelectorAll(
            '.kit-app > .kit-list:not([hidden]) .kit-row',
          ),
        ].find((x) => x.querySelector('.kit-kicker')?.textContent === k);
        return (
          !m?.checkVisibility() && !!r && !r.querySelector('.cb-row-looking')
        );
      },
      [MARK, kicker(KEY)],
      8000,
    );
    const got = await marks();
    check(
      `the agent's reply clears the marker on the item and its row (item "${got.item}", row "${got.row}"), and the button is back`,
      ok && got.item === '' && got.row === '' && got.ask,
    );
    check(
      'with no reload',
      await pg.evaluate(() => window.__lookNoReload === true),
    );
  }
  await shoot(t, pg, 'look-into', ['light']);
  await pg.close();
}
