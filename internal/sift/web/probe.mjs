// probe.mjs — the build-time browser check for the sift review page.
//
// Seeds a real sift serve (serve-fixture.mjs) from testdata/round.json, an
// audit round with a recommendation for each file, opens the page in
// headless Chromium (playwright-core) and checks what the page promises, by
// what is visible and where it is, not only by what exists: to change, one
// list entry per file with its sizes, findings and the version picked,
// linked files together, and one entry for the files with nothing to
// change; a file's summary, the versions side by side (1 current, 2
// recommended, and yours once e has written it), the findings with what
// the chosen version does, the note, then a wrapping diff with the certain
// fix marked; linked files picked together; a stale page refused; nothing
// to change: each file's findings with the agent's reasons, agree (one, or
// a for all) muting them, disagree needing a note and going back to the
// agent, whose rewrite moves the file to change; while a decision or Send
// is in flight, no key or Send does anything; Send; light and dark. Then a round still being recommended (the
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
// A decision key, once the page has no decision saving: a group's keys do
// nothing while its last decision saves (the app marks that on .kit-app).
async function settled(p) {
  await p.waitForFunction(
    () => !document.querySelector('.kit-app')?.dataset.saving,
  );
}

async function decideKey(p, key) {
  await settled(p);
  await p.keyboard.press(key);
}

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
  const same = byRel('tools/CLAUDE.md');
  const cli = byRel('tools/cli/AGENTS.md');
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
    'the list shows one entry per file to change (4), then one for nothing to change',
    rowsShown === 5 &&
      (await page
        .locator('.kit-row')
        .last()
        .evaluate((e) => e.classList.contains('sift-nochange-row'))),
    String(rowsShown),
  );
  check(
    'the list heading counts only the files to change',
    (await page.locator('.kit-list .kit-eyebrow').innerText()).toLowerCase() ===
      'to change · 4 files',
    await page.locator('.kit-list .kit-eyebrow').innerText(),
  );
  const ncRow = page.locator('.kit-row.sift-nochange-row');
  check(
    'the nothing-to-change entry heads its section, below the files to change',
    (await ncRow.isVisible()) &&
      /^nothing to change · 2 files/.test(
        await ncRow.locator('.kit-kicker').innerText(),
      ) &&
      (await ncRow.innerText()).includes('2 files, 3 findings kept') &&
      (await box(ncRow)).y > (await box(page.locator('.kit-row').nth(3))).y,
    await ncRow.innerText(),
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
    'a list entry shows the size before and after, the findings and the version picked',
    /^\d+\.\d → \d+\.\d KB · \d+ · not chosen$/.test(meta),
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
    'the bar shows progress 0/6',
    norm(await page.locator('.kit-ctl').first().innerText()) === 'review 0/6',
  );
  check(
    'the overview counts the files to decide',
    (await page.locator('.kit-doc h1').innerText()) ===
      '6 of 6 files to decide',
  );
  await shoot(page, 'list');

  // ---- 2. a file: summary, diff, decisions, findings, note, in that order
  console.log('file');
  // Once no decision is saving: a saved decision moves the page on.
  const openFile = async (f) => {
    await settled(page);
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
  check(
    'the question is which version the file should have',
    (await page.locator('.kit-doc h1').innerText()) ===
      'Which version should this file have?',
  );
  for (const [name, sel] of Object.entries({
    summary: '.sift-summary',
    pick: '.sift-pick',
    findings: 'table.sift-findings',
    note: '.kit-doc .kit-note',
    diff: '.sift-diff',
  })) {
    const loc = page.locator(sel).first();
    ys[name] = (await loc.isVisible()) ? (await box(loc)).y : -1;
  }
  check(
    'the reading column runs summary, the versions, findings, note, then the change line by line',
    ys.summary > 0 &&
      ys.summary < ys.pick &&
      ys.pick < ys.findings &&
      ys.findings < ys.note &&
      ys.note < ys.diff,
    JSON.stringify(ys),
  );
  const choices = page.locator('.sift-pick .sift-choice');
  const cb = await choices.evaluateAll((els) =>
    els.map((e) => {
      const r = e.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height, t: e.innerText };
    }),
  );
  const docBox0 = await box(page.locator('.kit-doc'));
  check(
    'two versions side by side: 1 current and 2 recommended, inside the column',
    cb.length === 2 &&
      /^1 · current/.test(cb[0].t) &&
      /^2 · recommended/.test(cb[1].t) &&
      Math.abs(cb[0].y - cb[1].y) < 1 &&
      cb[1].x >= cb[0].x + cb[0].w &&
      cb[0].x >= docBox0.x &&
      cb[1].x + cb[1].w <= docBox0.x + docBox0.width + 0.5 &&
      cb.every((c) => c.w > 150 && c.h > 40),
    JSON.stringify(cb),
  );
  check(
    'current shows lines it keeps that the recommendation removes; recommended the lines it adds',
    (await choices.nth(0).locator('.sift-snip.del').isVisible()) &&
      (await choices.nth(1).locator('.sift-snip.add').isVisible()),
  );
  check(
    'nothing is chosen yet',
    (await page.locator('.sift-choice.chosen').count()) === 0,
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
  check(
    '"e · write my own version" is visible',
    await page
      .locator('.sift-decide .kit-btn', { hasText: 'e · write my own version' })
      .isVisible(),
  );
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
    'the linked file is named, picked together',
    (await page.locator('.kit-doc').innerText()).includes(
      'picked together with',
    ),
  );
  await shoot(page, 'file');

  // ---- 3. yours: e opens the whole recommended file; ⌘↵ stores it, and it
  // is then a third version
  console.log('yours');
  await decideKey(page, 'e');
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

  // Revisited, yours is a third version beside the others, and chosen; 2,
  // 3 and 1 then give the recommended version, yours, and the current one,
  // each what its key names on every file.
  await openFile(docs);
  await openFile(shop);
  const three = await page
    .locator('.sift-pick .sift-choice')
    .evaluateAll((els) =>
      els.map((e) => [
        e.getBoundingClientRect().y,
        e.classList.contains('chosen'),
        e.innerText.split('\n')[0],
      ]),
    );
  check(
    'yours is a third version, side by side, and chosen',
    three.length === 3 &&
      three.every(([y]) => Math.abs(y - three[0][0]) < 1) &&
      /^3 · yours/.test(three[2][2]) &&
      three.map(([, c]) => c).join() === 'false,false,true',
    JSON.stringify(three),
  );
  await decideKey(page, '2');
  check(
    '2 gives the recommended version, not your edit',
    await until(async () => {
      const d = (await fileOf(shop)).decision;
      return d?.action === 'accept' && !d.content;
    }),
  );
  await openFile(shop);
  check(
    'recommended is chosen, and yours is still there to pick',
    await until(
      async () =>
        (await page.locator('.sift-choice.recommended.chosen').isVisible()) &&
        (await page.locator('.sift-choice.yours').isVisible()) &&
        (await page
          .locator('.sift-dl.add', { hasText: 'Ask the shop team.' })
          .count()) === 0,
    ),
  );
  await decideKey(page, '3');
  check(
    '3 gives yours again',
    await until(async () => {
      const d = (await fileOf(shop)).decision;
      return d?.action === 'edit' && d.content === mine;
    }),
  );
  await openFile(shop);
  await decideKey(page, '1');
  check(
    '1 gives the current version, for both linked files',
    await until(
      async () =>
        (await fileOf(shop)).decision?.action === 'reject' &&
        (await fileOf(global)).decision?.action === 'reject',
    ),
  );
  await openFile(shop);
  await decideKey(page, '3');
  await until(async () => (await fileOf(shop)).decision?.action === 'edit');

  // ---- 4. 2 recommended, 1 current, n notes; linked files picked together
  console.log('keys');
  await openFile(docs);
  await decideKey(page, '2');
  check(
    '2 picks recommended (an accept)',
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
  await decideKey(page, '1');
  check(
    '1 picks current (a reject)',
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
        '· current',
      ),
    ),
  );
  await openFile(global);
  await decideKey(page, '1');
  check(
    '1 on a linked file picks current for both',
    await until(
      async () =>
        (await fileOf(global)).decision?.action === 'reject' &&
        (await fileOf(shop)).decision?.action === 'reject',
    ),
  );
  await openFile(global);
  await decideKey(page, '2');
  check(
    '2 on a linked file picks recommended for both',
    await until(
      async () =>
        (await fileOf(global)).decision?.action === 'accept' &&
        (await fileOf(shop)).decision?.action === 'accept',
    ),
  );
  await openFile(global);
  await decideKey(page, 'u');
  check(
    'u clears both',
    await until(
      async () =>
        !(await fileOf(global)).decision && !(await fileOf(shop)).decision,
    ),
  );
  await decideKey(page, '2');
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
      ).endsWith(' · not chosen'),
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
  await decideKey(page, '2');
  await until(async () => (await fileOf(docs)).decision?.action === 'accept');

  // ---- 6. nothing to change: the agent's reasons, agree (muting) or
  // disagree (with a note)
  console.log('nothing to change');
  const ncWrites = [];
  const onWrite = (r) => {
    if (r.url().includes('/api/') && r.method() !== 'GET')
      ncWrites.push(`${r.method()} ${new URL(r.url()).pathname}`);
  };
  page.on('request', onWrite);
  await settled(page);
  await ncRow.click();
  await page.waitForSelector('.sift-nochange');
  check(
    'it says the agent recommends no change to these files',
    (await page.locator('.kit-doc h1').innerText()) ===
      'The agent recommends no change to these files',
  );
  const blocks = page.locator('.sift-nc');
  const ncDoc = await box(page.locator('.kit-doc'));
  const bb = await blocks.evaluateAll((els) =>
    els.map((e) => {
      const r = e.getBoundingClientRect();
      return [r.x, r.width, e.querySelectorAll('table tr').length - 1];
    }),
  );
  check(
    'each file is listed inside the column with its findings (2 and 1)',
    bb.length === 2 &&
      bb.every(
        ([x, w]) => x >= ncDoc.x && x + w <= ncDoc.x + ncDoc.width + 0.5,
      ) &&
      bb.map(([, , n]) => n).join() === '2,1',
    JSON.stringify(bb),
  );
  check(
    "each finding shows the agent's reason",
    (await blocks.first().innerText()).includes('#57 is still open') &&
      (await blocks.nth(1).innerText()).includes('#61 is still in review'),
  );
  check(
    'each file has agree and disagree, and a agrees with all',
    (await blocks.locator('.kit-btn', { hasText: /^agree$/ }).count()) === 2 &&
      (await blocks.locator('.kit-btn', { hasText: 'disagree…' }).count()) ===
        2 &&
      (await page
        .locator('.sift-agree-all .kit-btn', {
          hasText: 'a · agree with all 2',
        })
        .isVisible()),
  );
  check(
    'the to-change heading still counts 4',
    (await page.locator('.kit-list .kit-eyebrow').innerText()).toLowerCase() ===
      'to change · 4 files',
  );
  await shoot(page, 'nothing-to-change');
  await blocks.nth(1).locator('.kit-btn', { hasText: 'disagree…' }).click();
  const why = page.locator('.sift-disagree-note');
  await why.waitFor();
  check(
    'disagree opens a note field, focused',
    await why.evaluate((el) => document.activeElement === el),
  );
  ncWrites.length = 0;
  await page.keyboard.press('Enter');
  await sleep(300);
  check(
    'disagree with no note is refused on the page: nothing is sent or stored',
    ncWrites.length === 0 &&
      !(await fileOf(cli)).decision &&
      /say what should change/.test(
        await page.locator('.sift-disagree .sift-err').innerText(),
      ),
    ncWrites.join(', '),
  );
  const refusedNote = await api('/api/files', {
    method: 'PUT',
    body: JSON.stringify({
      round: serve.round,
      file: cli.key,
      action: 'reject',
      prints: { [cli.key]: (await fileOf(cli)).fingerprint },
    }),
  });
  check(
    'and the server refuses it too (400)',
    refusedNote.status === 400,
    String(refusedNote.status),
  );
  await why.fill('#61 merged last week: say to use long options');
  await page.keyboard.press('Enter');
  check(
    'disagree with a note stores a reject with that note',
    await until(async () => {
      const d = (await fileOf(cli)).decision;
      return (
        d?.action === 'reject' &&
        d.note === '#61 merged last week: say to use long options'
      );
    }),
  );
  await settled(page);
  await page.keyboard.press('a');
  check(
    'a agrees with the rest, and agreeing mutes their findings',
    await until(async () => {
      const f = await fileOf(same);
      return f.decision?.action === 'accept' && f.muted;
    }),
  );
  check(
    'a keeps the disagreement',
    (await fileOf(cli)).decision?.action === 'reject' &&
      !(await fileOf(cli)).muted,
  );
  check(
    'the list entry says what was decided',
    await until(async () =>
      (await ncRow.innerText()).includes('1 agreed · 1 disagreed'),
    ),
    await ncRow.innerText(),
  );
  page.off('request', onWrite);

  // ---- 7. busy: while a request is held in flight, 1, 2, 3, u and Send
  // do nothing (no request leaves the page, 2 opens no edit)
  console.log('busy');
  const writes = [];
  page.on('request', (r) => {
    if (r.url().includes('/api/') && r.method() !== 'GET')
      writes.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  // Holds the next request matching glob until the returned release.
  const hold = async (glob) => {
    let release;
    let done;
    const held = new Promise((r) => (release = r));
    const passed = new Promise((r) => (done = r));
    await page.route(
      glob,
      async (route) => {
        await held;
        await route.continue();
        done();
      },
      { times: 1 },
    );
    return async () => {
      release();
      await passed;
      await settled(page);
    };
  };
  const saving = () =>
    page.waitForFunction(
      () => !!document.querySelector('.kit-app')?.dataset.saving,
    );
  const pressAll = async () => {
    for (const k of ['1', '2', '3', 'e', 'u']) await page.keyboard.press(k);
    await send.click();
    await sleep(400);
  };
  await openFile(skill);
  let release = await hold('**/api/files');
  writes.length = 0;
  await page.keyboard.press('1');
  await saving();
  await pressAll();
  check(
    'while a decision is in flight, 1, 2, 3, e, u and Send send nothing',
    writes.length === 1 && writes[0] === 'PUT /api/files',
    writes.join(', '),
  );
  check(
    'and e opens no editor',
    (await page.locator('textarea.sift-whole').count()) === 0,
  );
  check('Send reads saving…', norm(await send.innerText()) === 'saving…');
  await release();
  check(
    'the held decision lands',
    await until(
      async () => (await fileOf(skill)).decision?.action === 'reject',
    ),
  );

  // ---- 8. Send
  console.log('send');
  check(
    'Send shows what it carries',
    await until(async () => norm(await send.innerText()) === 'Send 6'),
  );
  await openFile(skill);
  release = await hold('**/api/send');
  writes.length = 0;
  await send.click();
  await saving();
  check(
    'Send in flight reads sending…',
    norm(await send.innerText()) === 'sending…',
  );
  for (const k of ['1', '2', '3', 'e', 'u']) await page.keyboard.press(k);
  await sleep(400);
  check(
    'while Send is in flight, 1, 2, 3, e and u send nothing',
    writes.length === 1 && writes[0] === 'POST /api/send',
    writes.join(', '),
  );
  check(
    'and e opens no editor while Send is in flight',
    (await page.locator('textarea.sift-whole').count()) === 0,
  );
  await release();
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

  // The disagreement went back to the agent, which recommends the file
  // again; its rewrite moves the file to change.
  const told = serve.sift(['wait', '--timeout', '5s']);
  check(
    'the agent is asked to recommend the disagreed file again, with the note',
    /Recommend again/.test(told) &&
      told.includes(cli.path) &&
      told.includes('#61 merged last week'),
    told,
  );
  const cliNow = await fileOf(cli);
  serve.sift(
    ['propose'],
    JSON.stringify({
      file: cli.key,
      base: cli.base,
      content:
        '# cli\n\n- New commands take long options (--name).\n- Every command prints its usage on -h.\n',
      findings: cliNow.rec.findings.map((a) => ({
        ...a,
        did: 'fixed',
        how: '#61 merged: says to use long options',
      })),
      summary: 'Long options are in: the wait on #61 is gone.',
    }),
  );
  check(
    "the agent's rewrite moves the file to change",
    await until(
      async () =>
        (
          await page.locator('.kit-list .kit-eyebrow').innerText()
        ).toLowerCase() === 'to change · 5 files' &&
        /^nothing to change · 1 file/.test(
          await ncRow.locator('.kit-kicker').innerText(),
        ) &&
        (await page
          .locator('.kit-row', { hasText: 'tools/cli/AGENTS.md' })
          .count()) === 1,
    ),
    await page.locator('.kit-list .kit-eyebrow').innerText(),
  );

  // ---- 9. light and dark
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

  // ---- 9. a round still being recommended: the page waits
  console.log('recommending');
  const recServe = await startServe({ recommending: true });
  serves.push(recServe);
  const rp = await open(pageUrl(recServe), '.kit-doc h1');
  check(
    'the page says the agent is still recommending, and how far it got',
    (await rp.locator('.kit-doc h1').innerText()) ===
      'The agent is recommending: 5 of 6 files',
  );
  check(
    'no file is listed, and nothing can be sent',
    (await rp.locator('.kit-row').count()) === 0 &&
      !(await rp.locator('.kit-primary').isVisible()),
  );
  await shoot(rp, 'recommending');
  await rp.close();

  // ---- 10. a backlog round: decided per item
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
  await decideKey(bp0, '1');
  check(
    '1 accepts an item',
    await until(
      async () =>
        (await bl.review()).rows.find((r) => r.id === 'b1')?.decision
          ?.action === 'accept',
    ),
  );
  await shoot(bp0, 'backlog');

  // An open editor is bound to the item as it was when it opened: the agent
  // re-proposes b2 under it, and a save that changes only the verdict is
  // refused (409), stores nothing, and reopens on the new proposal.
  console.log('editor');
  const puts = [];
  bp0.on('response', (r) => {
    if (r.request().method() === 'PUT' && r.url().includes('/api/decisions'))
      puts.push(r.status());
  });
  await bp0.goto(pageUrl(blServe, {}, `#/open/${encodeURIComponent('r:b2')}`));
  await bp0.waitForSelector('.kit-card');
  await decideKey(bp0, '2');
  await bp0.waitForSelector('.sift-edit');
  const reworded = 'Reworded by the agent while the editor was open.';
  blServe.sift(
    ['rows', 'add'],
    JSON.stringify({
      id: 'b2',
      verdict: 'issue',
      title: 'probe: flaky under load',
      destination: 'acme/tools',
      text: reworded,
      reason: 'Three CI runs this week.',
    }) + '\n',
  );
  check(
    'the page shows the new proposal under the open editor',
    await until(async () =>
      (await bp0.locator('.kit-doc').innerText()).includes(reworded),
    ),
  );
  await bp0.locator('.sift-verdict').fill('delete');
  await bp0.locator('.sift-edit button', { hasText: 'save edit' }).click();
  check(
    'the save gets an answer',
    await until(async () => puts.length > 0),
    puts.join(','),
  );
  check('  with 409', puts[0] === 409, puts.join(','));
  await settled(bp0);
  check(
    'and nothing is stored',
    !(await bl.review()).rows.find((r) => r.id === 'b2')?.decision,
  );
  check(
    'the editor reopens on the new proposal',
    await until(
      async () =>
        (await bp0.locator('.sift-edit .sift-text').inputValue()) ===
          reworded &&
        (await bp0.locator('.sift-verdict').inputValue()) === 'issue',
    ),
  );
  check(
    'and the bar says it changed',
    /changed/i.test(await bp0.locator('.kit-status').innerText()),
    await bp0.locator('.kit-status').innerText(),
  );

  // Edited titles are shown, and cleared ones are not, once saved and
  // reopened.
  console.log('titles');
  await bp0.locator('.sift-edit .sift-title').fill('probe: my own title');
  await bp0.locator('.sift-edit button', { hasText: 'save edit' }).click();
  check(
    'an edited title saves',
    await until(
      async () =>
        (await bl.review()).rows.find((r) => r.id === 'b2')?.decision?.title ===
        'probe: my own title',
    ),
  );
  await settled(bp0);
  const reopen = async () => {
    await bp0.goto(pageUrl(blServe, {}, '#/open'));
    await bp0.waitForSelector('.kit-row');
    await bp0.goto(
      pageUrl(blServe, {}, `#/open/${encodeURIComponent('r:b2')}`),
    );
    await bp0.waitForSelector('.kit-card');
  };
  await reopen();
  check(
    'reopened, the item shows the edited title',
    (await bp0.locator('.kit-doc h1').innerText()) === 'probe: my own title',
    await bp0.locator('.kit-doc h1').innerText(),
  );
  check(
    'and so does the list',
    (await bp0.locator('.kit-row.open').innerText()).includes(
      'probe: my own title',
    ),
    await bp0.locator('.kit-row.open').innerText(),
  );
  await decideKey(bp0, 'u');
  await until(
    async () => !(await bl.review()).rows.find((r) => r.id === 'b2')?.decision,
  );
  await decideKey(bp0, '2');
  await bp0.waitForSelector('.sift-edit');
  await bp0.locator('.sift-edit .sift-title').fill('');
  await bp0.locator('.sift-edit button', { hasText: 'save edit' }).click();
  check(
    'a cleared title saves',
    await until(async () =>
      (
        (await bl.review()).rows.find((r) => r.id === 'b2')?.decision
          ?.cleared ?? []
      ).includes('title'),
    ),
  );
  await settled(bp0);
  await reopen();
  const h1 = await bp0.locator('.kit-doc h1').innerText();
  const listed = await bp0.locator('.kit-row.open').innerText();
  check(
    'reopened, a cleared title is not shown, in the item or the list',
    !h1.includes('probe: flaky under load') &&
      !listed.includes('probe: flaky under load'),
    `${h1} | ${listed}`,
  );
  await bp0.close();

  // A backlog round with an item the agent has not answered waits too: no
  // item list, no Send, and a decision is refused.
  const blRec = await startServe({ fixture: BACKLOG, recommending: true });
  serves.push(blRec);
  const bpr = await open(pageUrl(blRec), '.kit-doc h1');
  check(
    'a backlog round still being recommended says how far the agent got',
    (await bpr.locator('.kit-doc h1').innerText()) ===
      'The agent is recommending: 3 of 4 items',
    await bpr.locator('.kit-doc h1').innerText(),
  );
  check(
    'and lists no item and offers no Send',
    (await bpr.locator('.kit-row').count()) === 0 &&
      !(await bpr.locator('.kit-primary').isVisible()),
  );
  const blr = apiOf(blRec);
  const recRow = (await blr.review()).rows.find((r) => r.verdict);
  const refused = (
    await blr.api('/api/decisions', {
      method: 'PUT',
      body: JSON.stringify({
        round: blRec.round,
        decisions: [
          { id: recRow.id, action: 'accept', fingerprint: recRow.fingerprint },
        ],
      }),
    })
  ).status;
  check(
    'a decision on an answered item is refused until the round is ready',
    refused === 409,
    String(refused),
  );
  await bpr.close();

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
