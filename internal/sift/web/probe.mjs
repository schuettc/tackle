// probe.mjs — the build-time browser check for the sift review page.
//
// Seeds a real sift serve (serve-fixture.mjs) from testdata/round.json, opens
// the page in headless Chromium (playwright-core) and checks what the page
// promises, by what is visible and where it is, not only by what exists:
// the views and groups, a group expanding under its header, a long passage
// wrapping inside the reading column, accept / edit / reject on 1–3, a group
// decided whole, notes, undo of an applied row, Send, the whole file, light
// and dark, and a round the size of a real one (653 rows). Screenshots go to
// $PROBE_OUT (default a temp dir).
//
// Chromium is required with KIT_BROWSER=required; otherwise a missing browser
// skips the probe. BROWSER SAFETY: the serve is started with --no-open by
// serve-fixture.mjs (asserted at its load) and pages are headless.

import { mkdtempSync, mkdirSync, readFileSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import pkg from 'playwright-core';
import { startServe, cleanupBuild, FIXTURE } from './serve-fixture.mjs';
import { entries, groupsOf } from './model.ts';

const { chromium } = pkg;
const required = process.env.KIT_BROWSER === 'required';

let passes = 0;
let fails = 0;
function check(label, ok, detail = '') {
  if (ok) {
    console.log(`  ✓ ${label}`);
    passes++;
  } else {
    console.error(`  ✗ ${label}${detail ? ` — ${detail}` : ''}`);
    fails++;
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, ms = 4000) {
  const end = Date.now() + ms;
  for (;;) {
    let v;
    try {
      v = await fn();
    } catch {
      v = false;
    }
    if (v) return v;
    if (Date.now() > end) return false;
    await sleep(50);
  }
}

const serves = [];
let browser = null;
async function cleanup() {
  try {
    await browser?.close();
  } catch {
    // already gone
  }
  for (const s of serves) {
    try {
      await s.stop();
    } catch {
      // already gone
    }
  }
  cleanupBuild();
}
process.on('SIGINT', () => cleanup().finally(() => process.exit(130)));
process.on('SIGTERM', () => cleanup().finally(() => process.exit(143)));

const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8'));
// The kit draws a count in its own span: "needs you" + "33".
const norm = (s) =>
  s
    .replace(/\s+/g, ' ')
    .replace(/([^\d\s/])([\d/]+)$/, '$1 $2')
    .trim();

async function main() {
  try {
    browser = await chromium.launch({ headless: true });
  } catch (err) {
    if (required) {
      console.error(`probe: no chromium (KIT_BROWSER=required): ${err}`);
      process.exit(1);
    }
    console.log('probe: no chromium; skipped');
    return;
  }
  const serve = await startServe();
  serves.push(serve);
  const out =
    process.env.PROBE_OUT ||
    mkdtempSync(join(realpathSync(tmpdir()), 'sift-probe-shots-'));
  mkdirSync(out, { recursive: true });

  const pageUrl = (q = {}, frag = '#/open') => {
    const u = new URL(serve.url);
    for (const [k, v] of Object.entries(q)) u.searchParams.set(k, v);
    u.hash = frag;
    return u.toString();
  };
  const api = async (path, init = {}) => {
    const res = await fetch(serve.base + path, {
      ...init,
      headers: {
        'X-Local-Token': serve.token,
        'Content-Type': 'application/json',
      },
    });
    return res.status === 204 ? null : res.json();
  };
  const review = () => api('/api/review');
  const rowOf = async (id) => (await review()).rows.find((r) => r.id === id);

  const ctx = await browser.newContext({
    viewport: { width: 1400, height: 900 },
  });
  const errors = [];
  const open = async (url) => {
    const p = await ctx.newPage();
    p.on('pageerror', (e) => errors.push(String(e)));
    await p.goto(url);
    await p.waitForSelector('.kit-row', { timeout: 8000 });
    return p;
  };
  const box = (loc) => loc.boundingBox();
  const shots = [];
  const shoot = async (p, name) => {
    const f = join(out, `${name}.png`);
    await p.screenshot({ path: f });
    shots.push(f);
  };

  const rv0 = await review();
  const groups = groupsOf(
    rv0.rows.filter((r) => !r.certain),
    false,
    rv0.home,
  );
  const big = groups.reduce((a, g) => (g.rows.length > a.rows.length ? g : a));
  const longRow = fixture.rows.find((r) =>
    r.passage.startsWith('- Never mock'),
  );
  const nextAfterLong =
    big.rows[big.rows.findIndex((r) => r.id === longRow.id) + 1];

  // ---- 1. the list: views, groups, geometry
  console.log('list');
  const page = await open(pageUrl());
  const chips = (
    await page
      .locator('.kit-chips[data-group="view"] .kit-chip')
      .allInnerTexts()
  ).map(norm);
  check(
    'view chips: needs you 33, applied 2',
    chips.join('|') === 'needs you 33|applied 2',
    chips.join('|'),
  );
  const rowsShown = await page.locator('.kit-row').count();
  check(
    `the list shows one entry per group (${entries(groups, null).length}), not 33 rows`,
    rowsShown === entries(groups, null).length,
    String(rowsShown),
  );
  const listBox = await box(page.locator('.kit-list'));
  const readBox = await box(page.locator('main.kit-read'));
  check(
    'list and reading column sit side by side, filling the width',
    listBox.x === 0 &&
      Math.abs(readBox.x - (listBox.x + listBox.width)) <= 1 &&
      readBox.x + readBox.width >= 1399,
    JSON.stringify([listBox, readBox]),
  );
  const rowBoxes = await page.locator('.kit-row').evaluateAll((els) =>
    els.map((e) => {
      const r = e.getBoundingClientRect();
      return [r.left, r.right, r.height];
    }),
  );
  check(
    'every list row is inside the list, with height',
    rowBoxes.every(
      ([l, r, h]) =>
        l >= listBox.x && r <= listBox.x + listBox.width + 0.5 && h > 30,
    ),
    JSON.stringify(rowBoxes),
  );
  // A kicker never ellipsizes, so text wider than its box spills out of
  // the row (a title ellipsizes by design).
  const spill = await page
    .locator('.kit-row .kit-kicker')
    .evaluateAll((els) =>
      els
        .filter((e) => e.scrollWidth > e.clientWidth + 1)
        .map((e) => e.textContent),
    );
  check(
    'no list kicker runs past its row',
    spill.length === 0,
    spill.join(' | '),
  );
  const send = page.locator('.kit-primary');
  check(
    'Send shows what it carries (the 2 applied fixes)',
    (await send.isVisible()) && norm(await send.innerText()) === 'Send 2',
  );
  check(
    'the bar shows progress 0/33',
    norm(await page.locator('.kit-ctl').first().innerText()) === 'review 0/33',
  );
  check(
    'the overview counts what needs you',
    (await page.locator('.kit-doc h1').innerText()).startsWith(
      '33 of 33 rows to decide',
    ),
  );
  await shoot(page, 'list-dark');

  // ---- 2. a group opens and expands under its header
  console.log('group');
  const header = page.locator('.kit-row.sift-group', { hasText: big.title });
  await header.click();
  await until(
    async () =>
      (await page.locator('.kit-row.sift-member').count()) === big.rows.length,
  );
  const members = page.locator('.kit-row.sift-member');
  check(
    `the group expands to its ${big.rows.length} rows`,
    (await members.count()) === big.rows.length,
  );
  const hb = await box(header);
  const mb = await box(members.first());
  check(
    'its rows sit below the header, indented',
    mb.y > hb.y && mb.x - hb.x >= 12,
    JSON.stringify([hb, mb]),
  );
  check(
    'the reading column shows the group',
    norm(await page.locator('.kit-doc h1').innerText()) === big.title,
  );
  const accept = page.locator('.kit-card .kit-btn', { hasText: /^1 accept/ });
  const proposed = big.rows.filter((r) => r.verdict).length;
  check(
    `group accept takes the ${proposed} rows with a proposal`,
    norm(await accept.innerText()) === `1 accept ${proposed}`,
  );
  const table = await box(page.locator('table.sift-rows'));
  const doc = await box(page.locator('.kit-doc'));
  check(
    'the group table fits the reading column',
    table.x >= doc.x && table.x + table.width <= doc.x + doc.width + 0.5,
    JSON.stringify([table, doc]),
  );
  await shoot(page, 'group-dark');

  // ---- 3. a long passage wraps in the prose view
  console.log('row');
  await page.evaluate((f) => (location.hash = f), `#/open/r:${longRow.id}`);
  await page.waitForSelector('.sift-prose');
  const prose = page.locator('.sift-prose').first();
  const pb = await box(prose);
  const db = await box(page.locator('.kit-doc'));
  check(
    'the passage box stays inside the column',
    pb.x >= db.x && pb.x + pb.width <= db.x + db.width + 0.5,
    JSON.stringify([pb, db]),
  );
  const wrap = await prose
    .locator('.sift-t')
    .first()
    .evaluate((el) => ({
      sw: el.scrollWidth,
      cw: el.clientWidth,
      h: el.getBoundingClientRect().height,
      lh: parseFloat(getComputedStyle(el).lineHeight),
    }));
  check(
    'the long line wraps (no sideways scroll, several lines tall)',
    wrap.sw <= wrap.cw && wrap.h >= 3 * wrap.lh,
    JSON.stringify(wrap),
  );
  const gut = await prose
    .locator('.sift-n')
    .first()
    .evaluate((el) => [el.textContent, el.getBoundingClientRect().left]);
  check(
    'the gutter shows the passage line number at the left',
    gut[0] === String(longRow.source.start) && gut[1] < pb.x + 60,
    JSON.stringify(gut),
  );
  check(
    'the card shows the proposal and its reason',
    /proposes · rewrite/i.test(
      await page.locator('.kit-card-head').first().innerText(),
    ) &&
      (await page.locator('.kit-card-body').first().innerText()).includes(
        'Keeps the reason',
      ),
  );
  for (const label of ['1 accept', '2 edit', '3 reject']) {
    const btn = page.locator('.kit-doc .kit-btn', { hasText: label });
    check(`"${label}" is visible`, await btn.isVisible());
  }
  await shoot(page, 'row-dark');

  // ---- 4. edit: 2 opens text, title and verdict; ⌘↵ saves only what changed
  console.log('edit');
  await page.keyboard.press('2');
  const ta = page.locator('textarea.sift-text');
  await ta.waitFor();
  check(
    'edit opens the proposed text',
    (await ta.inputValue()) === longRow.text,
  );
  check(
    'edit opens the verdict',
    (await page.locator('input.sift-verdict').inputValue()) === 'rewrite',
  );
  check(
    'edit opens the title',
    await page.locator('input.sift-title').isVisible(),
  );
  const tb = await box(ta);
  const db2 = await box(page.locator('.kit-doc'));
  check(
    'the text field fits the column and is tall enough to edit',
    tb.x >= db2.x &&
      tb.x + tb.width <= db2.x + db2.width + 0.5 &&
      tb.height >= 100,
    JSON.stringify([tb, db2]),
  );
  check(
    'the text field has focus',
    await ta.evaluate((el) => document.activeElement === el),
  );
  await shoot(page, 'edit-dark');
  await page.locator('input.sift-verdict').fill('cut');
  await page.locator('.sift-edit .kit-btn', { hasText: 'save edit' }).click();
  check(
    'a bad verdict is refused on the page',
    (await page.locator('.sift-err').innerText()).includes('not a verdict'),
  );
  await page.locator('input.sift-verdict').fill('rewrite');
  await ta.fill('- Test against the real database.');
  await page.keyboard.press('Meta+Enter');
  check(
    'the edit is stored with only the text changed',
    await until(async () => {
      const d = (await rowOf(longRow.id)).decision;
      return (
        d &&
        d.action === 'edit' &&
        d.text === '- Test against the real database.' &&
        !d.verdict
      );
    }),
  );
  check(
    'the page moves on to the next undecided row',
    await until(
      async () =>
        decodeURIComponent(await page.evaluate(() => location.hash)) ===
        `#/open/r:${nextAfterLong.id}`,
    ),
    await page.evaluate(() => location.hash),
  );

  // ---- 5. 1 accepts, 3 rejects, n notes
  console.log('keys');
  const onKey = async (key, id) => {
    await page.evaluate((f) => (location.hash = f), `#/open/r:${id}`);
    await page.waitForFunction(
      (t) => document.querySelector('.kit-kick')?.textContent?.includes(t),
      `:${fixture.rows.find((r) => r.id === id).source.start}`,
    );
    await page.keyboard.press(key);
  };
  const withVerdict = big.rows.filter((r) => r.verdict && r.id !== longRow.id);
  await onKey('1', withVerdict[0].id);
  check(
    '1 accepts',
    await until(
      async () =>
        (await rowOf(withVerdict[0].id)).decision?.action === 'accept',
    ),
  );
  await onKey('3', withVerdict[1].id);
  check(
    '3 rejects',
    await until(
      async () =>
        (await rowOf(withVerdict[1].id)).decision?.action === 'reject',
    ),
  );
  await page.evaluate(
    (f) => (location.hash = f),
    `#/open/r:${withVerdict[1].id}`,
  );
  await page.waitForSelector('.kit-note');
  await page.keyboard.press('n');
  await page.keyboard.type('the linter covers it');
  await page.keyboard.press('Enter');
  check(
    'n then Enter stores a note on the decision',
    await until(
      async () =>
        (await rowOf(withVerdict[1].id)).decision?.note ===
        'the linter covers it',
    ),
  );
  check(
    'the list says what was decided',
    await until(
      async () =>
        (await page.locator('.kit-row.open .kit-meta').innerText()) ===
        'rejected',
    ),
  );

  // ---- 6. the group decided whole leaves single decisions alone
  console.log('group decision');
  const bigKey = `g:${big.key}`;
  await page.evaluate(
    (f) => (location.hash = f),
    `#/open/${encodeURIComponent(bigKey)}`,
  );
  await page.waitForFunction(
    (t) => document.querySelector('.kit-doc h1')?.textContent === t,
    big.title,
  );
  await page.keyboard.press('1');
  const want = big.rows
    .filter(
      (r) =>
        r.verdict &&
        ![longRow.id, withVerdict[0].id, withVerdict[1].id].includes(r.id),
    )
    .map((r) => r.id);
  check(
    `1 on the group accepts its ${want.length} other proposals`,
    await until(async () => {
      const rv = await review();
      const by = new Map(rv.rows.map((r) => [r.id, r]));
      return want.every((id) => by.get(id).decision?.action === 'accept');
    }),
  );
  const after = new Map((await review()).rows.map((r) => [r.id, r]));
  check(
    'rows decided one at a time keep their decision',
    after.get(longRow.id).decision.action === 'edit' &&
      after.get(withVerdict[1].id).decision.action === 'reject',
  );
  check(
    'rows with no proposal stay undecided',
    big.rows.filter((r) => !r.verdict).every((r) => !after.get(r.id).decision),
  );
  check(
    "the header shows the group's progress",
    await until(async () =>
      /^\d+\/\d+$/.test(
        await page
          .locator('.kit-row.sift-group', { hasText: big.title })
          .locator('.kit-meta')
          .innerText(),
      ),
    ),
  );

  // ---- 7. applied rows: undo, redo
  console.log('applied');
  await page
    .locator('.kit-chips[data-group="view"] .kit-chip', { hasText: 'applied' })
    .click();
  await until(async () => (await page.locator('.kit-row').count()) === 2);
  const certain = fixture.rows.filter((r) => r.certain);
  await page.locator('.kit-row').first().click();
  await page.waitForSelector('.kit-doc .kit-btn');
  await page.keyboard.press('u');
  check(
    'u undoes an applied row',
    await until(
      async () => (await rowOf(certain[0].id)).decision?.action === 'reject',
    ),
  );
  check(
    'the list says undone',
    await until(
      async () =>
        (await page.locator('.kit-row.open .kit-meta').innerText()) ===
        'undone',
    ),
  );
  await page.keyboard.press('u');
  check(
    'u again redoes it',
    await until(async () => !(await rowOf(certain[0].id)).decision),
  );
  await page.keyboard.press('u');
  await until(
    async () => (await rowOf(certain[0].id)).decision?.action === 'reject',
  );
  await shoot(page, 'applied-dark');

  // ---- 8. the whole file, folded, with the passage marked
  console.log('whole file');
  await page.evaluate((f) => (location.hash = f), `#/open/r:${longRow.id}`);
  await page.waitForSelector('.kit-fold');
  await page.locator('.kit-fold summary').click();
  check(
    'the whole file loads with the passage marked',
    await until(
      async () => (await page.locator('.kit-fold .sift-ln.mark').count()) === 1,
    ),
  );
  check(
    'the marked line is the passage',
    (await page.locator('.kit-fold .sift-ln.mark .sift-t').innerText()) ===
      longRow.passage,
  );

  // ---- 9. Send
  console.log('send');
  const before = norm(await send.innerText());
  await send.click();
  check(
    'Send says where the decisions went',
    await until(async () =>
      (await page.locator('.kit-status').innerText()).startsWith('Sent '),
    ),
    before,
  );
  check(
    'the decisions are marked sent',
    await until(async () =>
      (await review()).rows
        .filter((r) => r.decision)
        .every((r) => r.decision.sent),
    ),
  );
  check(
    'nothing left to send: the button goes',
    await until(async () => !(await send.isVisible())),
  );

  // ---- 10. light and dark
  console.log('themes');
  for (const theme of ['light', 'dark']) {
    const p = await open(
      pageUrl({ theme }, `#/open/${encodeURIComponent(bigKey)}`),
    );
    await p.waitForSelector('.sift-rows');
    const bg = await p.evaluate(
      () => getComputedStyle(document.body).backgroundColor,
    );
    check(
      `${theme}: the page has its theme's ground`,
      theme === 'light'
        ? bg === 'rgb(244, 245, 248)'
        : bg === 'rgb(20, 22, 29)',
      bg,
    );
    await shoot(p, `theme-${theme}-group`);
    await p.evaluate((f) => (location.hash = f), `#/open/r:${longRow.id}`);
    await p.waitForSelector('.sift-prose');
    await shoot(p, `theme-${theme}-row`);
    await p.close();
  }

  // ---- 11. a round the size of a real one
  console.log('scale');
  const bigServe = await startServe({ scale: 653 });
  serves.push(bigServe);
  const bp = await ctx.newPage();
  bp.on('pageerror', (e) => errors.push(String(e)));
  const t0 = Date.now();
  await bp.goto(bigServe.url + '#/open');
  await bp.waitForSelector('.kit-row');
  const loadMs = Date.now() - t0;
  const n = await bp.locator('.kit-row').count();
  check(`653 rows load in under 3 s (${loadMs} ms)`, loadMs < 3000);
  check(
    `and list as ${n} groups and rows, not 653`,
    n > 0 && n <= 60,
    String(n),
  );
  const t1 = Date.now();
  for (let i = 0; i < 30; i++) await bp.keyboard.press('j');
  await bp.waitForTimeout(50);
  check(
    `30 j presses through expanding groups take under 3 s (${Date.now() - t1} ms)`,
    Date.now() - t1 < 3000,
  );
  check(
    'the cursor row is on screen',
    await bp.locator('.kit-row.open').isVisible(),
  );
  await shoot(bp, 'scale-dark');
  await bp.close();

  check('no page errors', errors.length === 0, errors.join('; '));
  console.log('  screenshots:', shots.join(' '));
}

try {
  await main();
} catch (err) {
  console.error(err);
  fails++;
} finally {
  await cleanup();
}
console.log(`\n${passes} passed, ${fails} failed`);
process.exit(fails ? 1 : 0);
