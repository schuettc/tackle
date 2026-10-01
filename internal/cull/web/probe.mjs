// probe.mjs — the build-time browser check for the cull review page.
//
// Seeds a real cull serve (serve-fixture.mjs) from testdata/review.json, opens
// the page in headless Chromium (playwright-core) and checks what the page
// promises: the buckets and their counts, group actions that never touch an
// answer given one at a time, keys, blind mode, a second tab seeing an answer,
// a stale answer reloading, Send, and the groups section. Screenshots (light
// and dark) go to $PROBE_OUT (default a temp dir).
//
// Chromium is required with KIT_BROWSER=required; otherwise a missing browser
// skips the probe. BROWSER SAFETY: the serve is started with --no-open by
// serve-fixture.mjs (asserted at its load) and pages are headless.

import { mkdtempSync, mkdirSync, readFileSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import pkg from 'playwright-core';
import { startServe, cleanupBuild, FIXTURE } from './serve-fixture.mjs';
import { buckets, byConcern } from './bulk.ts';
import { concern } from './model.ts';

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

let serve = null;
let browser = null;
async function cleanup() {
  try {
    await browser?.close();
  } catch {
    // already gone
  }
  try {
    await serve?.stop();
  } catch {
    // already gone
  }
  cleanupBuild();
}
process.on('SIGINT', () => cleanup().finally(() => process.exit(130)));
process.on('SIGTERM', () => cleanup().finally(() => process.exit(143)));

const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8'));
const tests = fixture.items.filter((i) => i.kind === 'test');
const groups = fixture.items.filter((i) => i.kind === 'group');
const expectBuckets = buckets(tests, new Map());
const expectGroups = buckets(groups, new Map());

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
  serve = await startServe();
  const pid = serve.projectId;
  const out =
    process.env.PROBE_OUT ||
    mkdtempSync(join(realpathSync(tmpdir()), 'cull-probe-shots-'));
  mkdirSync(out, { recursive: true });

  const pageUrl = (q = {}, frag = `#/p/${pid}`) => {
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
  const review = () => api(`/api/review?project=${pid}`);
  const answered = async () => (await review()).items.filter((i) => i.answer);

  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 1100 },
  });
  const errors = [];
  const open = async (url) => {
    const p = await ctx.newPage();
    p.on('pageerror', (e) => errors.push(String(e)));
    await p.goto(url);
    await p.waitForSelector('.kit-chip', { timeout: 8000 });
    return p;
  };
  const chipText = (p) => p.locator('.kit-chip').allInnerTexts();
  const norm = (s) =>
    s
      .replace(/\s+/g, ' ')
      .replace(/(\D)(\d+)$/, '$1 $2')
      .trim();

  // ---- 0. the page shell and screenshots (nothing answered yet)
  console.log('overview');
  const page = await open(pageUrl());
  const chips = (await chipText(page)).map(norm);
  check(
    'tests chips: leans cut 21, leans keep 12, undecided 8',
    ['leans cut 21', 'leans keep 12', 'undecided 8'].every((c) =>
      chips.includes(c),
    ),
    chips.join(' | '),
  );
  check(
    'the answered chip starts at 0',
    chips.includes('answered 0'),
    chips.join(' | '),
  );
  check(
    'the accept card offers cut all 21',
    (await page.locator('.kit-card').first().innerText())
      .toLowerCase()
      .includes('cut all 21'),
  );
  const status = norm(await page.locator('.kit-status').innerText());
  check(
    'status line names the project and what was settled without you',
    status.startsWith(
      'shop · whole suite · 1,200 tests · 12 cut, 30 merges and 1,100 keeps settled without you',
    ),
    status,
  );
  const ctls = (await page.locator('.kit-ctl').allInnerTexts())
    .map(norm)
    .join('|');
  check(
    'the sections show open counts',
    ctls.startsWith('tests 41|groups 8'),
    ctls,
  );
  check(
    'the reading column is a main region',
    (await page.locator('main.kit-read').count()) === 1,
  );
  check(
    'no Send button while nothing is answered',
    (await page.locator('.kit-primary:visible').count()) === 0,
  );

  const shots = [];
  const shoot = async (p, name) => {
    const f = join(out, `${name}.png`);
    await p.screenshot({ path: f });
    shots.push(f);
  };
  const firstTest = tests[0];
  const firstGroup = groups[0];
  for (const theme of ['light', 'dark']) {
    const p = await open(pageUrl({ theme }));
    await shoot(p, `overview-${theme}`);
    await p.goto(
      pageUrl({ theme }, `#/p/${pid}/${encodeURIComponent(firstTest.id)}`),
    );
    await p.waitForSelector('.kit-row.open');
    await p.waitForTimeout(150);
    await shoot(p, `test-${theme}`);
    await p.goto(
      pageUrl({ theme }, `#/p/${pid}/${encodeURIComponent(firstGroup.id)}`),
    );
    await p.waitForFunction(() =>
      document
        .querySelector('.kit-doc .kit-kick')
        ?.textContent?.startsWith('group'),
    );
    await p.waitForTimeout(150);
    await shoot(p, `group-${theme}`);
    await p.close();
  }
  console.log('  screenshots:', out);

  // ---- 2. by-concern table matches byConcern()
  console.log('by concern');
  const expectRows = byConcern(expectBuckets.cut);
  const rows = await page
    .locator('table.ov tr')
    .evaluateAll((trs) =>
      trs
        .slice(1)
        .map((tr) =>
          [...tr.querySelectorAll('td')].map((td) =>
            td.innerText.replace(/\s+/g, ' ').trim(),
          ),
        ),
    );
  check(
    'one row per concern with the right count and mean',
    rows.length === expectRows.length &&
      rows.every(
        (r, i) =>
          r[0].startsWith(expectRows[i].concern) &&
          r[1] === String(expectRows[i].items.length) &&
          r[2] === expectRows[i].meanP.toFixed(2),
      ),
    JSON.stringify(rows),
  );

  // ---- the pins_setting item: first row, its own words, its own sentence
  console.log('pins_setting');
  {
    const pin = tests.find((i) => i.rule === 'pins_setting');
    check(
      'the first by-concern row is "checks a setting\'s value"',
      rows[0]?.[0].startsWith("checks a setting's value") &&
        rows[0][0].includes(
          'it only reads a project setting or class and compares it to fixed values',
        ) &&
        rows[0][1] === '1',
      JSON.stringify(rows[0]),
    );
    await page.goto(pageUrl({}, `#/p/${pid}/${encodeURIComponent(pin.id)}`));
    await page.waitForSelector('.kit-doc h1');
    const text = norm(await page.locator('main.kit-read').innerText());
    check(
      'its recommendation names the reason',
      text.includes(
        "Jev leans cut (0.51). cull sends it to you because it only checks a setting's value; only you know whether that value is deliberate. Protects behavior rated cosmetic.",
      ),
      text.slice(0, 400),
    );
    await page.goto(pageUrl());
    await page.waitForSelector('table.ov');
  }

  // ---- a long unbroken name wraps inside the reading column
  console.log('long names');
  {
    const longName = 'test_' + 'a_very_long_unbroken_name_'.repeat(6);
    await page.goto(
      pageUrl({}, `#/p/${pid}/${encodeURIComponent(expectBuckets.cut[0].id)}`),
    );
    await page.waitForSelector('.kit-doc h1');
    const m = await page.evaluate((name) => {
      const h1 = document.querySelector('.kit-doc h1');
      h1.textContent = name;
      const read = document.querySelector('main.kit-read');
      return {
        h: [h1.scrollWidth, h1.clientWidth],
        r: [read.scrollWidth, read.clientWidth],
      };
    }, longName);
    check(
      'a 120+ character name wraps in the h1',
      longName.length >= 120 && m.h[0] <= m.h[1],
      JSON.stringify(m),
    );
    check(
      'the reading column does not scroll sideways',
      m.r[0] <= m.r[1],
      JSON.stringify(m),
    );
  }

  // ---- 5/4/3. keys, an item answered with 1, then cut all on its concern
  console.log('keys and group actions');
  const top = expectBuckets.cut[0];
  const topConcern = concern(top)?.short ?? 'no clear reason';
  const topRow = expectRows.find((r) => r.concern === topConcern);
  const cur = async () =>
    norm(await page.locator('.kit-row.cur .kit-title').first().innerText());
  await page.keyboard.press('j');
  const c1 = await cur();
  await page.keyboard.press('k');
  const c2 = await cur();
  check(
    'j and k move the cursor',
    c1 === expectBuckets.cut[1].name && c2 === expectBuckets.cut[0].name,
    [c1, c2].join(' | '),
  );
  await page.keyboard.press('Enter');
  check(
    'j k ↵ open the first row',
    await until(
      async () => (await page.locator('.kit-doc h1').innerText()) === top.name,
    ),
    await page.locator('.kit-doc h1').innerText(),
  );
  check(
    'the route carries the item',
    decodeURIComponent(page.url().split('#')[1]) === `/p/${pid}/${top.id}`,
    page.url(),
  );
  check(
    'the list marks the open row',
    (await page.locator('.kit-row.open .kit-title').allInnerTexts()).join(
      '|',
    ) === top.name,
  );
  await page.keyboard.press('n');
  await page.keyboard.type('pinned on purpose');
  await page.keyboard.press('Enter');
  check(
    'Enter leaves the note field',
    await page.evaluate(
      () => !document.activeElement?.classList.contains('kit-note'),
    ),
  );
  await page.keyboard.press('1');
  check(
    '1 keeps the test, with the waiting note',
    await until(async () => {
      const a = (await answered()).find((i) => i.id === top.id);
      return (
        a?.answer.value === 'keep' &&
        a.answer.via === 'item' &&
        a.answer.note === 'pinned on purpose' &&
        a.answer.blind === false
      );
    }),
  );
  check(
    'answering moves to the next open item',
    await until(
      async () =>
        (await page.locator('.kit-doc h1').innerText()) ===
        expectBuckets.cut[1].name,
    ),
  );
  check(
    'the note field never swallowed a page key',
    (await page.locator('.kit-note').count()) === 0 ||
      !(await page.locator('.kit-note').first().inputValue()).includes('1'),
  );
  await page.keyboard.press('b');
  check(
    'b returns to the overview',
    await until(async () =>
      (await page.locator('.kit-doc h1').innerText()).includes(
        'tests Jev leans toward cutting',
      ),
    ),
  );
  const overviewRow = page
    .locator('table.ov tr', { hasText: topConcern })
    .first();
  const wantOpen = topRow.items.filter((i) => i.id !== top.id);
  check(
    'the concern row now counts only open items',
    norm(await overviewRow.locator('td').nth(1).innerText()) ===
      String(wantOpen.length),
  );
  await overviewRow
    .getByRole('button', { name: `cut ${wantOpen.length}` })
    .click();
  check(
    'cut all answers exactly the open items of that concern',
    await until(async () => {
      const a = await answered();
      const cuts = a.filter((i) => i.answer.value === 'cut');
      return (
        cuts.length === wantOpen.length &&
        wantOpen.every((w) =>
          cuts.some((c) => c.id === w.id && c.answer.via === 'group'),
        )
      );
    }),
  );
  const a1 = (await answered()).find((i) => i.id === top.id);
  check(
    'the item answered one at a time keeps its answer',
    a1.answer.value === 'keep' && a1.answer.via === 'item',
  );
  check(
    'the answered chip counts all of them',
    await until(
      async () =>
        norm(
          (await chipText(page)).find((c) => c.startsWith('answered')) ?? '',
        ) === `answered ${wantOpen.length + 1}`,
    ),
  );

  // ---- the selection foot: in answered it only unanswers
  console.log('selection foot');
  const fp = await open(pageUrl());
  await fp.locator('.kit-chip', { hasText: 'answered' }).click();
  await fp.waitForSelector('.kit-row .kit-box');
  await fp.locator('.kit-row .kit-box').nth(0).click();
  await fp.locator('.kit-row .kit-box').nth(1).click();
  const footBtn = (await fp.locator('.kit-foot button').allInnerTexts()).map(
    (t) => t.replace(/\s+/g, ' ').trim(),
  );
  check(
    'two answered rows selected: the foot offers only unanswer 2',
    footBtn.length === 1 && footBtn[0] === 'unanswer 2',
    footBtn.join(' | '),
  );
  const answeredBefore = (await answered()).length;
  if (footBtn[0] === 'unanswer 2')
    await fp.locator('.kit-foot button', { hasText: 'unanswer 2' }).click();
  check(
    'unanswer 2 removes exactly those two answers',
    await until(async () => (await answered()).length === answeredBefore - 2),
    String((await answered()).length),
  );
  await fp.close();

  // ---- 7. a second page sees an answer within 3 s
  console.log('two tabs');
  const other = await open(pageUrl());
  const before = norm(
    (await chipText(other)).find((c) => c.startsWith('answered')),
  );
  const next = expectBuckets.keep[0];
  await page.goto(pageUrl({}, `#/p/${pid}/${encodeURIComponent(next.id)}`));
  await page.waitForSelector('.kit-doc h1');
  await page.keyboard.press('2');
  const t0 = Date.now();
  const seen = await until(
    async () =>
      norm((await chipText(other)).find((c) => c.startsWith('answered'))) !==
      before,
    3000,
  );
  check(
    'the other tab shows the new answer within 3 s',
    !!seen,
    `${Date.now() - t0} ms`,
  );
  await other.close();

  // ---- 6. blind mode
  console.log('blind');
  const blind = await open(pageUrl({ blind: '1' }));
  check(
    'blind hides the accept card',
    (await blind.locator('.kit-card').count()) === 0,
  );
  check(
    'blind shows no lean in the list',
    !(await blind.locator('.kit-list').innerText())
      .toLowerCase()
      .includes('leans'),
  );
  const metas = await blind.locator('.kit-row .kit-meta').allInnerTexts();
  const listText = (await blind.locator('.kit-list').innerText()).toLowerCase();
  check(
    'blind rows show "·" as meta for open rows and no leaning words',
    metas.length > 0 &&
      metas.every((m) => m.trim() === '·') &&
      !listText.includes('leaning') &&
      !listText.includes('slightly'),
    metas.slice(0, 3).join('|'),
  );
  const open1 = await blind.locator('.kit-row').first();
  const blindName = norm(await open1.locator('.kit-title').innerText());
  await open1.click();
  await blind.waitForSelector('.kit-doc h1');
  check(
    'blind item shows no recommendation or signals',
    (await blind.locator('.kit-card').count()) === 0 &&
      (await blind.locator('table.sig').count()) === 0,
  );
  check(
    'blind marks itself in the kicker',
    (await blind.locator('.kit-kick').innerText())
      .toLowerCase()
      .includes('blind'),
  );
  await blind.keyboard.press('a');
  await sleep(300);
  check(
    'a does nothing in blind mode',
    !(await answered()).some((i) => i.name === blindName && i.answer),
  );
  await blind.keyboard.press('2');
  check(
    'an answer given in blind mode is stored blind:true',
    await until(async () =>
      (await answered()).some(
        (i) => i.name === blindName && i.answer.blind === true,
      ),
    ),
  );
  await blind.close();

  // ---- 10. groups
  console.log('groups');
  await page.goto(pageUrl());
  await page.waitForSelector('.kit-chip');
  await page.locator('.kit-ctl', { hasText: 'groups' }).click();
  const gchips = (await chipText(page)).map(norm);
  check(
    'groups chips: leans merge 3, leans separate 5; the empty undecided chip is hidden',
    ['leans merge 3', 'leans separate 5'].every((c) => gchips.includes(c)) &&
      !gchips.some((c) => c.startsWith('undecided')),
    gchips.join(' | '),
  );
  check(
    'groups overview has a by size table',
    (await page.locator('table.ov tr').count()) > 1 &&
      (await page.locator('.kit-label', { hasText: 'by size' }).count()) === 1,
  );
  const g0 = expectGroups.consolidate[0];
  await page.locator('.kit-row').first().click();
  await page.waitForSelector('.kit-doc h1');
  check(
    'group item lists a fold per member',
    (await page.locator('details.kit-fold').count()) >= g0.state.tests.length,
  );
  check(
    'group item shows the table test rows',
    (await page.locator('table.rows tr').count()) === g0.state.tests.length,
  );
  await page.keyboard.press('2');
  check(
    '2 on a group stores merge',
    await until(async () =>
      (await answered()).some(
        (i) => i.id === g0.id && i.answer.value === 'merge',
      ),
    ),
  );

  // ---- 9. send
  console.log('send');
  const n = (await answered()).length;
  const send = page.locator('.kit-primary:visible');
  check(
    'the bar offers Send with the unsent count',
    norm(await send.innerText()) === `Send ${n} answers`,
    await send.innerText(),
  );
  await send.click();
  const msg = `Sent ${n} answers. The agent is told in a later release; cull check uses them now.`;
  check(
    'Send reports exactly what it did',
    await until(
      async () => norm(await page.locator('.kit-status').innerText()) === msg,
    ),
    await page.locator('.kit-status').innerText(),
  );
  const afterSend = await review();
  check(
    'every answer is marked sent',
    afterSend.answered.sent === n && afterSend.answered.total === n,
    JSON.stringify(afterSend.answered),
  );
  check(
    'the Send button is gone',
    await until(
      async () => (await page.locator('.kit-primary:visible').count()) === 0,
    ),
  );

  // ---- 8. stale answer
  console.log('stale answer');
  const stale = await ctx.newPage();
  stale.on('pageerror', (e) => errors.push(String(e)));
  await stale.route('**/api/events*', (r) => r.abort());
  await stale.route('**/api/poll*', (r) => r.abort());
  const victim = expectBuckets.review[0];
  await stale.goto(pageUrl({}, `#/p/${pid}/${encodeURIComponent(victim.id)}`));
  await stale.waitForSelector('.kit-doc h1');
  serve.reseed({ mutate: victim.id });
  await stale.keyboard.press('1');
  check(
    'a stale answer reloads and says so',
    await until(
      async () =>
        norm(await stale.locator('.kit-status').innerText()) ===
        'The review changed; reloaded.',
    ),
    await stale.locator('.kit-status').innerText(),
  );
  check(
    'nothing was stored for the changed test',
    !(await answered()).some((i) => i.id === victim.id),
  );
  // A new run arrives while an item is open: the page reloads on its own; the
  // open item changed, so it goes back to the overview with a note.
  const changed = expectBuckets.review[1];
  const live = await open(
    pageUrl({}, `#/p/${pid}/${encodeURIComponent(changed.id)}`),
  );
  await live.waitForSelector('.kit-doc h1');
  serve.reseed({ mutate: changed.id });
  check(
    'a new run with the open test changed returns to the overview with a note',
    await until(
      async () =>
        (await live.locator('.kit-doc').innerText()).includes(
          'That test changed; it was judged again.',
        ),
      6000,
    ),
    await live.locator('.kit-doc').innerText(),
  );
  const keepOpen = expectBuckets.review[2];
  await live.goto(pageUrl({}, `#/p/${pid}/${encodeURIComponent(keepOpen.id)}`));
  await live.waitForSelector('.kit-doc h1');
  const undecided = async () =>
    norm((await chipText(live)).find((c) => c.startsWith('undecided')) ?? '');
  const undBefore = await undecided();
  const dropped = expectBuckets.review[3];
  serve.reseed({ mutate: changed.id, drop: dropped.id });
  const wantUnd = `undecided ${Number(undBefore.split(' ')[1]) - 1}`;
  check(
    'the new run arrives (the undecided count drops by one)',
    await until(async () => (await undecided()) === wantUnd, 6000),
    `${undBefore} -> ${await undecided()}`,
  );
  check(
    'a new run that leaves the open test alone keeps it open',
    (await live.locator('.kit-doc h1').innerText()) === keepOpen.name,
  );

  // ---- a failure that is not a 409 rolls the answer back
  console.log('failed save');
  const failing = await ctx.newPage();
  failing.on('pageerror', (e) => errors.push(String(e)));
  const target = expectBuckets.review[4];
  const targetFrag = `#/p/${pid}/${encodeURIComponent(target.id)}`;
  await failing.goto(pageUrl({}, targetFrag));
  await failing.waitForSelector('.kit-doc h1');
  await failing.route('**/api/answers*', (r) =>
    r.request().method() === 'PUT'
      ? r.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'disk on fire' }),
        })
      : r.continue(),
  );
  await failing.keyboard.press('n');
  await failing.keyboard.type('keep this note');
  await failing.keyboard.press('Enter');
  await failing.keyboard.press('1');
  check(
    'a failed save says "Not saved: …" in the bar',
    await until(async () =>
      norm(await failing.locator('.kit-status').innerText()).startsWith(
        'Not saved:',
      ),
    ),
    await failing.locator('.kit-status').innerText(),
  );
  check(
    'the failed answer was rolled back (nothing stored)',
    !(await answered()).some((i) => i.id === target.id),
  );
  await failing.evaluate((f) => {
    location.hash = f;
  }, `#/p/${pid}`);
  await failing.evaluate((f) => {
    location.hash = f;
  }, targetFrag);
  check(
    'the item is open again, unanswered',
    await until(
      async () =>
        (await failing.locator('.kit-doc h1').innerText()) === target.name &&
        (await failing.getByText('unanswer (u)').count()) === 0,
    ),
  );
  check(
    'the note waits for the next answer (a rolled back note stays pending)',
    (await failing.locator('.kit-note').inputValue()) === 'keep this note',
    JSON.stringify(await failing.locator('.kit-note').inputValue()),
  );
  await failing.close();

  // ---- a half-typed note survives another tab's change
  console.log('half-typed note');
  const typing = await open(
    pageUrl({}, `#/p/${pid}/${encodeURIComponent(expectBuckets.review[5].id)}`),
  );
  await typing.waitForSelector('.kit-doc h1');
  await typing.keyboard.press('n');
  await typing.keyboard.type('half typed');
  const rv = await review();
  const mate = rv.items.find((i) => i.id === expectBuckets.review[6].id);
  await api('/api/answers', {
    method: 'PUT',
    body: JSON.stringify({
      project: pid,
      run: rv.run.id,
      answers: [
        {
          id: mate.id,
          hash: mate.hash,
          kind: mate.kind,
          value: 'keep',
          note: '',
          via: 'item',
          blind: false,
        },
      ],
    }),
  });
  await until(async () =>
    (await typing.locator('.kit-chip').allInnerTexts())
      .map(norm)
      .some((c) => c.startsWith('answered') && !c.endsWith(' 0')),
  );
  await sleep(1500);
  check(
    'the note field keeps its text and focus after a re-render',
    (await typing.locator('.kit-note').inputValue()) === 'half typed' &&
      (await typing.evaluate(() =>
        document.activeElement?.classList.contains('kit-note'),
      )),
    JSON.stringify([
      await typing.locator('.kit-note').inputValue(),
      await typing.evaluate(() => document.activeElement?.className),
    ]),
  );
  await typing.close();

  // ---- the first load fails, then the page retries
  console.log('first load retry');
  const retry = await ctx.newPage();
  retry.on('pageerror', (e) => errors.push(String(e)));
  let refused = 0;
  await retry.route('**/api/review*', (r) =>
    refused++ < 1 ? r.abort() : r.continue(),
  );
  await retry.goto(pageUrl());
  check(
    'a failed first load says cull serve is not answering',
    await until(async () =>
      (await retry.locator('main.kit-read').innerText()).includes(
        'cull serve is not answering; retrying.',
      ),
    ),
    await retry.locator('main.kit-read').innerText(),
  );
  check(
    'the page loads on its own once serve answers',
    !!(await until(
      async () => (await retry.locator('.kit-chip').count()) > 0,
      5000,
    )),
  );
  await retry.close();

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
