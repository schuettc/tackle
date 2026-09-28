// probe.mjs — the build-time browser sanity check for the casebook bundle.
//
// Spawns a real seeded casebook serve (via serve.mjs), opens its page in
// headless Chrome (playwright-core), and checks the four shell invariants:
//
//   1. three section controls with counts   (attention / rules / to apply)
//   2. theme toggle works                   (html[data-theme] flips)
//   3. routing switches sections            (#/rules/... → rules, back → attention)
//   4. the live pill reads live             (.kit-live is visible, data-state="live")
//
// Chrome is required in CI (KIT_BROWSER=required) and optional locally.
// Skips cleanly when Chrome is absent and KIT_BROWSER is not set.
//
// BROWSER SAFETY: all serve startups go through serve.mjs which enforces
// --no-open and CASEBOOK_NO_BROWSER=1. The check below verifies this at
// module load so no future edit can accidentally open Court's browser.

import pkg from 'playwright-core';
import { startServe } from './serve.mjs';

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

async function run() {
  const browser = await findChrome();
  if (!browser) {
    if (required) {
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

  const context = await browser.newContext();
  const page = await context.newPage();

  try {
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
    await page
      .waitForSelector('.kit-ctl[data-id] .kit-n', { timeout: 5000 })
      .catch(() => {
        // counts may all be 0; kit renders a .kit-n span for 0 too
      });

    console.log('\nscenario: the shell boots');

    // 1. Three section controls with counts.
    {
      const controls = await page.$$('.kit-ctl[data-id]');
      const ids = await Promise.all(
        controls.map((c) => c.getAttribute('data-id')),
      );
      check('attention control present', ids.includes('attention'));
      check('rules control present', ids.includes('rules'));
      check('apply control present', ids.includes('apply'));

      // Each section control must show a number (even "0").
      let allHaveCount = true;
      for (const ctrl of controls) {
        const id = await ctrl.getAttribute('data-id');
        if (!id || !['attention', 'rules', 'apply'].includes(id)) continue;
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

    // 3. Routing switches sections.
    {
      await page.evaluate(() => {
        location.hash = '#/rules/test';
      });
      await page.waitForFunction(() => location.hash === '#/rules/test');

      const rulesActive = await page.$eval(
        '.kit-ctl[data-id="rules"]',
        (el) =>
          el.classList.contains('on') ||
          el.getAttribute('aria-current') === 'page',
      );
      check('routing to #/rules/... activates rules section', rulesActive);

      await page.evaluate(() => {
        location.hash = '#/attention';
      });
      await page.waitForFunction(() => location.hash === '#/attention');
      const attentionActive = await page.$eval(
        '.kit-ctl[data-id="attention"]',
        (el) =>
          el.classList.contains('on') ||
          el.getAttribute('aria-current') === 'page',
      );
      check(
        'routing back to #/attention activates attention section',
        attentionActive,
      );
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
      try {
        await page.waitForSelector('.kit-live:not([hidden])', {
          timeout: 5000,
        });
        const state = await page.$eval(
          '.kit-live',
          (el) => el.dataset.state ?? '',
        );
        check('live pill is visible', true);
        check('live pill data-state is "live"', state === 'live');
      } catch {
        check('live pill is visible within 5s', false);
        check('live pill data-state is "live"', false);
      }
    }
  } catch (err) {
    console.error('probe: unexpected error:', err);
    fails++;
  } finally {
    await context.close();
    await browser.close();
    cleanup();
  }

  console.log(`\nprobe: ${passes} passed, ${fails} failed`);
  if (fails > 0) process.exit(1);
}

void run();
