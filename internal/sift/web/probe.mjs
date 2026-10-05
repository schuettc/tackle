// probe.mjs — the build-time browser check for the sift review page.
//
// Seeds a real sift serve (serve-fixture.mjs) from testdata/round.json, an
// audit round with a recommendation for each file, opens the page in
// headless Chromium (playwright-core) and checks what the page promises, by
// what is visible and where it is, not only by what exists: one list entry
// per file with its sizes, findings and decision, linked files together; a
// file's summary, a wrapping diff with the certain fix marked, accept /
// edit / reject on 1–3 (edit opens the whole file), the findings with what
// the rewrite did, the note; linked files decided together; a stale page
// refused; Send; light and dark. Then a round still being recommended (the
// page waits), a backlog round (decided per item), and a round the size of
// a real one (653 rows). Screenshots go to $PROBE_OUT (default a temp dir).
//
// Chromium is required with KIT_BROWSER=required; otherwise a missing browser
// skips the probe. BROWSER SAFETY: the serve is started with --no-open by
// serve-fixture.mjs (asserted at its load) and pages are headless.

import { mkdtempSync, mkdirSync, readFileSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import pkg from 'playwright-core';
import {
  startServe,
  cleanupBuild,
  FIXTURE,
  BACKLOG,
} from './serve-fixture.mjs';

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
// The kit draws a count in its own span: "review" + "0/4".
const norm = (s) =>
  s
    .replace(/\s+/g, ' ')
    .replace(/([^\d\s/])([\d/]+)$/, '$1 $2')
    .trim();

/** A seeded serve's API, as the page calls it. */
function apiOf(serve) {
  const api = async (path, init = {}) => {
    const res = await fetch(serve.base + path, {
      ...init,
      headers: {
        'X-Local-Token': serve.token,
        'Content-Type': 'application/json',
      },
    });
    const body = res.status === 204 ? null : await res.json();
    return { status: res.status, body };
  };
  return {
    api,
    review: async () => (await api('/api/review')).body,
  };
}

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
  const { api, review } = apiOf(serve);
  const out =
    process.env.PROBE_OUT ||
    mkdtempSync(join(realpathSync(tmpdir()), 'sift-probe-shots-'));
  mkdirSync(out, { recursive: true });

  const pageUrl = (s, q = {}, frag = '#/open') => {
    const u = new URL(s.url);
    for (const [k, v] of Object.entries(q)) u.searchParams.set(k, v);
    u.hash = frag;
    return u.toString();
  };
  const ctx = await browser.newContext({
    viewport: { width: 1400, height: 900 },
  });
  const errors = [];
  const open = async (url, ready = '.kit-row') => {
    const p = await ctx.newPage();
    p.on('pageerror', (e) => errors.push(String(e)));
    await p.goto(url);
    await p.waitForSelector(ready, { timeout: 8000 });
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
  const byRel = (rel) => rv0.files.find((f) => f.path === join(serve.dir, rel));
  const shop = byRel('shop/CLAUDE.md');
  const global = byRel('home/.agent/AGENTS.md');
  const docs = byRel('shop/docs/AGENTS.md');
  const skill = byRel('tools/skills/release/SKILL.md');
  const fileOf = async (f) =>
    (await review()).files.find((x) => x.key === f.key);
  const certain = fixture.rows.filter(
    (r) => r.certain && r.source.rel === 'shop/CLAUDE.md',
  );

  // ---- 1. the list: one entry per file, linked files together
  console.log('list');
  const page = await open(pageUrl(serve));
  const rowsShown = await page.locator('.kit-row').count();
  check(
    'the list shows one entry per file (4)',
    rowsShown === 4,
    String(rowsShown),
  );
  const keys = await page
    .locator('.kit-row')
    .evaluateAll((els) => els.map((e) => e.dataset.id ?? ''));
  const linked = await page.locator('.kit-row.sift-linked').count();
  const firstTwo = await page
    .locator('.kit-row')
    .evaluateAll((els) =>
      els.slice(0, 2).map((e) => e.classList.contains('sift-linked')),
    );
  check(
    'the two linked files sit together, first (the global file leads)',
    linked === 2 && firstTwo.every(Boolean),
    `${linked} ${JSON.stringify(firstTwo)} ${keys.join(',')}`,
  );
  const meta = await page.locator('.kit-row .kit-meta').first().innerText();
  check(
    'a list entry shows the size before and after, the findings and the decision',
    /^\d+\.\d → \d+\.\d KB · \d+ ·$/.test(meta),
    meta,
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
    'every list entry is inside the list, with height',
    rowBoxes.every(
      ([l, r, h]) =>
        l >= listBox.x && r <= listBox.x + listBox.width + 0.5 && h > 30,
    ),
    JSON.stringify(rowBoxes),
  );
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
  check('nothing to send yet: no Send button', !(await send.isVisible()));
  check(
    'the bar shows progress 0/4',
    norm(await page.locator('.kit-ctl').first().innerText()) === 'review 0/4',
  );
  check(
    'the overview counts the files to decide',
    (await page.locator('.kit-doc h1').innerText()) ===
      '4 of 4 files to decide',
  );
  await shoot(page, 'list');

  // ---- 2. a file: summary, diff, decisions, findings, note, in that order
  console.log('file');
  const openFile = async (f) => {
    await page.evaluate((h) => (location.hash = h), `#/open/f:${f.key}`);
    await page.waitForFunction(
      (p) =>
        document.querySelector('.kit-doc .kit-kick')?.textContent?.endsWith(p),
      f.path.split('/').slice(-2).join('/'),
    );
    await page.waitForSelector('.sift-diff');
  };
  await openFile(shop);
  const ys = {};
  for (const [name, sel] of Object.entries({
    summary: '.sift-summary',
    diff: '.sift-diff',
    decide: '.sift-decide',
    findings: 'table.sift-findings',
    note: '.kit-doc .kit-note',
  })) {
    const loc = page.locator(sel).first();
    ys[name] = (await loc.isVisible()) ? (await box(loc)).y : -1;
  }
  check(
    'the reading column runs summary, diff, accept / edit / reject, findings, note',
    ys.summary > 0 &&
      ys.summary < ys.diff &&
      ys.diff < ys.decide &&
      ys.decide < ys.findings &&
      ys.findings < ys.note,
    JSON.stringify(ys),
  );
  check(
    "the summary is the recommendation's",
    (await page.locator('.sift-summary').innerText()) ===
      fixture.recs['shop/CLAUDE.md'].summary,
  );
  const diff = page.locator('.sift-diff');
  const dbx = await box(diff);
  const docBox = await box(page.locator('.kit-doc'));
  check(
    'the diff stays inside the column',
    dbx.x >= docBox.x && dbx.x + dbx.width <= docBox.x + docBox.width + 0.5,
    JSON.stringify([dbx, docBox]),
  );
  const wide = await diff
    .locator('.sift-t')
    .evaluateAll(
      (els) => els.filter((e) => e.scrollWidth > e.clientWidth + 1).length,
    );
  check('no diff line scrolls sideways', wide === 0, String(wide));
  const longLine = diff.locator('.sift-dl.add', {
    hasText: 'Run the slow suite before a release:',
  });
  const wrap = await longLine.locator('.sift-t').evaluate((el) => ({
    h: el.getBoundingClientRect().height,
    lh: parseFloat(getComputedStyle(el).lineHeight),
  }));
  check(
    'the long added line wraps, several lines tall',
    wrap.h >= 3 * wrap.lh,
    JSON.stringify(wrap),
  );
  const cert = diff.locator('.sift-dl.cert');
  check(
    `the certain fix is marked in the diff (${certain.length} line)`,
    (await cert.count()) === certain.length &&
      (await cert.locator('.sift-t').first().innerText()) ===
        certain[0].passage &&
      (await cert.locator('.sift-sign').first().innerText()) === '!',
  );
  const del = await diff.locator('.sift-dl.del').count();
  const add = await diff.locator('.sift-dl.add').count();
  check(
    'the diff shows removed and added lines',
    del > 10 && add >= 1,
    `${del} ${add}`,
  );
  const gut = await diff
    .locator('.sift-dl.cert .sift-n')
    .first()
    .evaluate((el) => [el.textContent, el.getBoundingClientRect().left]);
  check(
    'the gutter shows the audited line number at the left',
    gut[0] === String(certain[0].source.start) && gut[1] < dbx.x + 60,
    JSON.stringify(gut),
  );
  for (const label of ['1 accept', '2 edit', '3 reject']) {
    const btn = page.locator('.sift-decide .kit-btn', { hasText: label });
    check(`"${label}" is visible`, await btn.isVisible());
  }
  const findingRows = page.locator('table.sift-findings tr');
  check(
    `the findings list each of the file's ${shop.rows.length} findings with what was done`,
    (await findingRows.count()) === shop.rows.length + 1 &&
      (await page.locator('table.sift-findings .sift-did.kept').count()) ===
        1 &&
      (await page.locator('table.sift-findings tr.cert').count()) ===
        certain.length,
  );
  const tb = await box(page.locator('table.sift-findings'));
  check(
    'the findings table fits the column',
    tb.x >= docBox.x && tb.x + tb.width <= docBox.x + docBox.width + 0.5,
    JSON.stringify([tb, docBox]),
  );
  check(
    'the linked file is named',
    (await page.locator('.kit-doc').innerText()).includes(
      'decided together with',
    ),
  );
  await shoot(page, 'file');

  // ---- 3. edit: 2 opens the whole recommended file; ⌘↵ stores it
  console.log('edit');
  await page.keyboard.press('2');
  const ta = page.locator('textarea.sift-whole');
  await ta.waitFor();
  check(
    'edit opens the whole recommended file',
    (await ta.inputValue()) === fixture.recs['shop/CLAUDE.md'].content,
  );
  const tab = await box(ta);
  const db2 = await box(page.locator('.kit-doc'));
  check(
    'the edit field fits the column and is tall enough for a whole file',
    tab.x >= db2.x &&
      tab.x + tab.width <= db2.x + db2.width + 0.5 &&
      tab.height >= 300,
    JSON.stringify([tab, db2]),
  );
  check(
    'the edit field has focus',
    await ta.evaluate((el) => document.activeElement === el),
  );
  await shoot(page, 'edit');
  const mine =
    fixture.recs['shop/CLAUDE.md'].content +
    '\n## Owners\n\n- Ask the shop team.\n';
  await ta.fill(mine);
  await page.keyboard.press('Meta+Enter');
  check(
    'the edit is stored with the whole file',
    await until(async () => {
      const d = (await fileOf(shop)).decision;
      return d?.action === 'edit' && d.content === mine;
    }),
  );
  check(
    'an edit to one linked file accepts the other',
    await until(
      async () => (await fileOf(global)).decision?.action === 'accept',
    ),
  );
  check(
    'the diff now shows your edit',
    await until(
      async () =>
        (await page
          .locator('.sift-dl.add', { hasText: 'Ask the shop team.' })
          .count()) === 1,
    ),
  );

  // ---- 4. 1 accepts, 3 rejects, n notes; linked files decided together
  console.log('keys');
  await openFile(docs);
  await page.keyboard.press('1');
  check(
    '1 accepts',
    await until(async () => (await fileOf(docs)).decision?.action === 'accept'),
  );
  check(
    'the page moves on to the next undecided file',
    await until(
      async () =>
        decodeURIComponent(await page.evaluate(() => location.hash)) ===
        `#/open/f:${skill.key}`,
    ),
    await page.evaluate(() => location.hash),
  );
  await openFile(skill);
  await page.keyboard.press('3');
  check(
    '3 rejects',
    await until(
      async () => (await fileOf(skill)).decision?.action === 'reject',
    ),
  );
  await openFile(skill);
  await page.keyboard.press('n');
  await page.keyboard.type('the skill is going away');
  await page.keyboard.press('Enter');
  check(
    'n then Enter stores a note on the decision',
    await until(
      async () =>
        (await fileOf(skill)).decision?.note === 'the skill is going away',
    ),
  );
  check(
    'the list says what was decided',
    await until(async () =>
      (await page.locator('.kit-row.open .kit-meta').innerText()).endsWith(
        '· rejected',
      ),
    ),
  );
  await openFile(global);
  await page.keyboard.press('3');
  check(
    '3 on a linked file rejects both',
    await until(
      async () =>
        (await fileOf(global)).decision?.action === 'reject' &&
        (await fileOf(shop)).decision?.action === 'reject',
    ),
  );
  await openFile(global);
  await page.keyboard.press('1');
  check(
    '1 on a linked file accepts both',
    await until(
      async () =>
        (await fileOf(global)).decision?.action === 'accept' &&
        (await fileOf(shop)).decision?.action === 'accept',
    ),
  );
  await openFile(global);
  await page.keyboard.press('u');
  check(
    'u clears both',
    await until(
      async () =>
        !(await fileOf(global)).decision && !(await fileOf(shop)).decision,
    ),
  );
  await page.keyboard.press('1');
  await until(async () => (await fileOf(shop)).decision?.action === 'accept');

  // ---- 5. a recommendation changed under the page: an old page is refused
  console.log('stale');
  const old = await fileOf(docs);
  const again = {
    file: old.key,
    base: old.base,
    content:
      '# docs\n\n- Regenerate the reference with `make docs`.\n- Write in the second person.\n',
    findings: old.rec.findings,
    summary: 'Says how to regenerate the reference.',
  };
  serve.sift(['propose'], JSON.stringify(again));
  const put = await api('/api/files', {
    method: 'PUT',
    body: JSON.stringify({
      round: serve.round,
      file: old.key,
      action: 'accept',
      prints: { [old.key]: old.fingerprint },
    }),
  });
  check(
    'a decision against the old recommendation gets 409',
    put.status === 409,
    String(put.status),
  );
  check(
    'the new recommendation drops the old decision, and the page shows it',
    await until(async () =>
      (
        await page
          .locator('.kit-row', { hasText: 'shop/docs/AGENTS.md' })
          .locator('.kit-meta')
          .innerText()
      ).endsWith(' ·'),
    ),
  );
  await openFile(docs);
  check(
    'the open file shows the new recommendation',
    await until(
      async () =>
        (await page
          .locator('.sift-dl.add', { hasText: 'Regenerate the reference' })
          .count()) === 1,
    ),
  );
  await page.keyboard.press('1');
  await until(async () => (await fileOf(docs)).decision?.action === 'accept');

  // ---- 6. Send
  console.log('send');
  check(
    'Send shows what it carries',
    await until(async () => norm(await send.innerText()) === 'Send 4'),
  );
  await send.click();
  check(
    'Send says where the decisions went',
    await until(async () =>
      (await page.locator('.kit-status').innerText()).startsWith('Sent '),
    ),
  );
  check(
    'the decisions are marked sent',
    await until(async () =>
      (await review()).files
        .filter((f) => f.decision)
        .every((f) => f.decision.sent),
    ),
  );
  check(
    'nothing left to send: the button goes',
    await until(async () => !(await send.isVisible())),
  );

  // ---- 7. light and dark
  console.log('themes');
  for (const theme of ['light', 'dark']) {
    const p = await open(
      pageUrl(
        serve,
        { theme },
        `#/open/${encodeURIComponent(`f:${shop.key}`)}`,
      ),
    );
    await p.waitForSelector('.sift-diff');
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
    const tint = await p
      .locator('.sift-dl.add')
      .first()
      .evaluate((el) => getComputedStyle(el).backgroundColor);
    check(
      `${theme}: added lines are tinted`,
      tint !== 'rgba(0, 0, 0, 0)' && tint !== 'transparent',
      tint,
    );
    await shoot(p, `theme-${theme}-file`);
    await p.close();
  }

  // ---- 8. a round still being recommended: the page waits
  console.log('recommending');
  const recServe = await startServe({ recommending: true });
  serves.push(recServe);
  const rp = await open(pageUrl(recServe), '.kit-doc h1');
  check(
    'the page says the agent is still recommending, and how far it got',
    (await rp.locator('.kit-doc h1').innerText()) ===
      'The agent is recommending: 3 of 4 files',
  );
  check(
    'no file is listed, and nothing can be sent',
    (await rp.locator('.kit-row').count()) === 0 &&
      !(await rp.locator('.kit-primary').isVisible()),
  );
  await shoot(rp, 'recommending');
  await rp.close();

  // ---- 9. a backlog round: decided per item
  console.log('backlog');
  const blServe = await startServe({ fixture: BACKLOG });
  serves.push(blServe);
  const bl = apiOf(blServe);
  const bp0 = await open(pageUrl(blServe));
  const groupsShown = (await bp0.locator('.kit-row').allInnerTexts()).map(norm);
  // A group of one shows as its item: issues to file (acme/shop, then
  // acme/tools), then the decision, then the close.
  check(
    'issues to file (by repo), then decisions, then closes',
    groupsShown.length === 4 &&
      groupsShown[0].includes('release: the script skips the changelog') &&
      groupsShown[1].includes('probe: flaky under load') &&
      groupsShown[2].includes('decide on the new layout') &&
      groupsShown[3].includes('rename the config key'),
    groupsShown.join(' | '),
  );
  await bp0.locator('.kit-row').first().click();
  await bp0.waitForSelector('.kit-card');
  await bp0.keyboard.press('1');
  check(
    '1 accepts an item',
    await until(
      async () =>
        (await bl.review()).rows.find((r) => r.id === 'b1')?.decision
          ?.action === 'accept',
    ),
  );
  await shoot(bp0, 'backlog');
  await bp0.close();

  // ---- 10. a round the size of a real one
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
  check(`and list as ${n} files, not 653 rows`, n > 0 && n <= 60, String(n));
  const t1 = Date.now();
  for (let i = 0; i < 30; i++) await bp.keyboard.press('j');
  await bp.waitForTimeout(50);
  check(
    `30 j presses through the files take under 3 s (${Date.now() - t1} ms)`,
    Date.now() - t1 < 3000,
  );
  check(
    'the cursor row is on screen',
    await bp.locator('.kit-row.open').isVisible(),
  );
  check(
    'the open file shows its diff',
    await until(async () => (await bp.locator('.sift-diff').count()) === 1),
  );
  await shoot(bp, 'scale');
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
