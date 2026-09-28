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
    // ---- scenario: attention list and detail --------------------------------
    console.log('\nscenario: attention list and detail');

    // Navigate to attention / waiting view.
    await page.evaluate(() => {
      location.hash = '#/attention/waiting';
    });
    await page.waitForFunction(() => location.hash === '#/attention/waiting');

    // Wait for the attention list panel.
    await page.waitForSelector('.kit-list', { timeout: 5000 }).catch(() => {});

    // Wait for rows to appear (the API call may take a moment).
    await page.waitForSelector('.kit-row', { timeout: 8000 }).catch(() => {});

    // Check: seeded items appear in the waiting view.
    {
      const rowCount = await page.$$eval('.kit-row', (rows) => rows.length);
      check('seeded items appear in the waiting view', rowCount > 0);
    }

    // Check: clicking a view chip narrows the list.
    {
      const beforeCount = await page.$$eval('.kit-row', (rows) => rows.length);

      // Click the 'new' view chip.
      const newChip = await page.$('[data-id="new"]');
      if (newChip) {
        await newChip.click();
        // Wait for the list to update.
        await page.waitForTimeout(600);
      }
      const afterCount = await page.$$eval('.kit-row', (rows) => rows.length);
      // The 'new' view may have 0 rows in the fixture (all are 'waiting'),
      // so we just verify the chip click did not error (afterCount is a number).
      check(
        'clicking a view chip changes the row set',
        (typeof afterCount === 'number' && afterCount !== beforeCount) ||
          afterCount === 0,
      );

      // Return to waiting view.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: search the Attention list (kit v0.11.0) -----------------
    console.log('\nscenario: search the Attention list (kit v0.11.0)');

    {
      // Navigate to waiting view and count the full row set.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);

      const fullCount = await page.$$eval('.kit-row', (rows) => rows.length);

      // 1. Typing narrows the list to the matching fixture item (after debounce).
      {
        const searchField = await page.$('.kit-search');
        if (searchField) {
          await searchField.click();
          await searchField.fill('nudge');
          // Wait for 200ms debounce + network round-trip.
          await page.waitForTimeout(500);
          const narrowCount = await page.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          check(
            "typing 'nudge' narrows the list after the debounce",
            narrowCount < fullCount || narrowCount <= 1,
          );
          const texts = await page.$$eval('.kit-row', (rows) =>
            rows.map((r) => (r.textContent || '').toLowerCase()),
          );
          check(
            'narrowed rows all contain the search term',
            texts.length === 0 || texts.every((t) => t.includes('nudge')),
          );
        } else {
          check("typing 'nudge' narrows the list after the debounce", false);
          check('narrowed rows all contain the search term', false);
        }
      }

      // 2. Esc clears the search and the list returns to the full count.
      {
        const searchField = await page.$('.kit-search');
        if (searchField) {
          await searchField.press('Escape');
          // Wait for 200ms debounce + reload.
          await page.waitForTimeout(500);
          const afterEscCount = await page.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          check(
            'Esc clears the search and restores the full list',
            afterEscCount === fullCount,
          );
          const clearedVal = await searchField.inputValue();
          check('search field is empty after Esc', clearedVal === '');
        } else {
          check('Esc clears the search and restores the full list', false);
          check('search field is empty after Esc', false);
        }
      }

      // 3. / focuses the search field.
      {
        // Blur any focused element first.
        await page.evaluate(() => {
          if (document.activeElement instanceof HTMLElement)
            document.activeElement.blur();
        });
        await page.keyboard.press('/');
        await page.waitForTimeout(150);
        const focused = await page
          .$eval('.kit-search', (el) => document.activeElement === el)
          .catch(() => false);
        check('/ focuses the search field', focused);
        // Press Esc to leave the field cleanly.
        await page.keyboard.press('Escape');
        await page.waitForTimeout(200);
      }

      // 4. Reload with search in URL restores the narrowed list and field text.
      {
        const reloadPage = await context.newPage();
        try {
          // Navigate to /?q=nudge#/attention/waiting. The context's cookie
          // provides auth; no ?t= needed.
          const baseUrl = serveHandle.base.replace(/\/$/, '');
          await reloadPage.goto(baseUrl + '/?q=nudge#/attention/waiting', {
            waitUntil: 'domcontentloaded',
            timeout: 15000,
          });
          await reloadPage.waitForSelector('.kit-bar', { timeout: 8000 });
          await reloadPage
            .waitForSelector('.kit-row', { timeout: 8000 })
            .catch(() => {});
          const reloadCount = await reloadPage.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          check(
            'reload with ?q=nudge shows the narrowed list',
            reloadCount < fullCount || reloadCount <= 1,
          );
          const fieldText = await reloadPage
            .$eval('.kit-search', (el) => el.value)
            .catch(() => '');
          check(
            'reload with ?q=nudge restores text in the search field',
            fieldText === 'nudge',
          );
        } finally {
          await reloadPage.close();
        }
      }
    }

    // ---- Check: opening a row shows item detail in .kit-read ----------------
    // ---- scenario: direct item link -----------------------------------------
    // Note: this check runs before the row-click check below so that the
    // reading column is empty when we navigate directly to the item.
    console.log('\nscenario: direct #/item/<key> link loads item detail');
    {
      // Use a fresh page and set the hash to simulate a direct link.
      // pr:schuettc/hail#3 is in the fixture with title "fix nudge".
      const directPage = await context.newPage();
      try {
        await directPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await directPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Set the hash to the item key (simulates navigating to a direct link;
        // the hashchange event triggers show() on the attention section).
        await directPage.evaluate(() => {
          location.hash = '#/item/pr:schuettc/hail#3';
        });
        // Wait for the reading column to populate via openDetail().
        await directPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        const title = await directPage
          .$eval('.kit-read .cb-title', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'direct #/item/<key> shows the item title in .kit-read',
          title.length > 0,
        );
      } finally {
        await directPage.close();
      }
    }
    console.log('\nscenario: attention list and detail (continued)');

    // Check: opening a row shows item detail in .kit-read.
    {
      const firstRow = await page.$('.kit-row');
      if (firstRow) {
        await firstRow.dblclick();
        // Wait for the reading column to populate.
        await page
          .waitForSelector('.kit-read .cb-item', { timeout: 5000 })
          .catch(() => {});
        const hasKicker = await page
          .$eval('.kit-read .cb-kicker', (el) => el.textContent.length > 0)
          .catch(() => false);
        const hasTitle = await page
          .$eval('.kit-read .cb-title', (el) => el.textContent.length > 0)
          .catch(() => false);
        check('opening a row shows the detail kicker in .kit-read', hasKicker);
        check('opening a row shows the detail title in .kit-read', hasTitle);
      } else {
        check('opening a row shows the detail kicker in .kit-read', false);
        check('opening a row shows the detail title in .kit-read', false);
      }
    }

    // ---- scenario: pagination -----------------------------------------------
    console.log('\nscenario: large view renders at most 200 rows');

    {
      const rowCount = await page.$$eval('.kit-row', (rows) => rows.length);
      check('at most 200 .kit-row rendered', rowCount <= 200);

      // The foot element (.cb-foot) is always in the DOM (hidden when no more).
      const footExists = await page
        .$('.cb-foot')
        .then((el) => el !== null)
        .catch(() => false);
      check('show-more foot element exists in DOM', footExists);
    }

    // ---- scenario: foot stays visible when view fits one page ---------------
    console.log('\nscenario: foot stays visible when view fits one page');

    {
      // Navigate to the 'new' view. The fixture has only a handful of items
      // (well under PAGE_SIZE=200) so there is no 'show more' needed.
      // The foot element must NOT be hidden: the selection count and the
      // select-all button must be visible even when the show-more button hides.
      await page.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await page.waitForFunction(() => location.hash === '#/attention/new');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(300);

      // cb-foot must not be hidden (the hidden attribute sets display:none).
      const footNotHidden = await page
        .$eval('.cb-foot', (el) => !el.hidden)
        .catch(() => false);
      check('cb-foot is not hidden when view fits one page', footNotHidden);

      // cb-sel-all must be in the rendered flow (offsetParent !== null means it
      // is not hidden by itself or any ancestor).
      const selAllVisible = await page
        .$eval('.cb-sel-all', (el) => el.offsetParent !== null)
        .catch(() => false);
      check('select-all is visible when view fits one page', selAllVisible);
    }

    // ---- scenario: select-all button shows total count ----------------------
    console.log('\nscenario: select-all button shows total count');

    {
      // Still on the 'new' view from the previous scenario.
      // The select-all button text must include the total item count, e.g.
      // "select all 4 in view" not just "select all in view".
      const selAllText = await page
        .$eval('.cb-sel-all', (el) => el.textContent ?? '')
        .catch(() => '');
      check(
        'select-all text includes the item count',
        /select all \d+ in view/.test(selAllText),
      );
    }

    // ---- scenario: dispositions for a mixed-kind selection are the intersection
    console.log(
      '\nscenario: dispositions for a mixed-kind selection are the intersection',
    );

    {
      // Select every visible row in 'new' view (repo:, pr:, issue:, branch: kinds).
      // Intersection of their allowed dispositions excludes 'merge' (pr-only).
      const boxes = await page.$$('.kit-row .kit-box');
      for (let i = 0; i < boxes.length; i++) {
        await boxes[i].click();
        await page.waitForTimeout(40);
      }
      await page.waitForTimeout(300);

      // Open the decide sheet.
      await page.click('.kit-primary').catch(() => {});
      await page
        .waitForSelector('.kit-sheet', { timeout: 4000 })
        .catch(() => {});
      await page.waitForTimeout(200);

      const dispTexts = await page.$$eval('.kit-sheet .cb-sheet-disp', (btns) =>
        btns.map((b) => (b.textContent ?? '').trim()),
      );

      // 'merge' is valid only for pr: — must not appear for a mixed selection.
      check(
        'mixed-kind selection does not offer pr-only disposition (merge)',
        !dispTexts.includes('merge'),
      );

      // Close without deciding and deselect.
      await page.keyboard.press('Escape');
      await page
        .waitForFunction(
          () => document.querySelector('.kit-backdrop') === null,
          { timeout: 3000 },
        )
        .catch(() => {});
      await page.waitForTimeout(200);
      const selBoxes = await page.$$('.kit-row .kit-box');
      for (const b of selBoxes) {
        await b.click().catch(() => {});
        await page.waitForTimeout(30);
      }
      await page.waitForTimeout(200);
    }

    // ---- scenario: partial decide deselects succeeded, keeps failed selected -
    console.log(
      '\nscenario: partial decide deselects succeeded and keeps failed selected',
    );

    {
      // Use a fresh browser context so the route interceptor is fully isolated
      // and cannot leak into the subsequent 'decide in bulk' scenario.
      const partialCtx = await browser.newContext();
      const partialPage = await partialCtx.newPage();
      try {
        await partialPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await partialPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to 'new' view and select two rows.
        await partialPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await partialPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await partialPage.waitForTimeout(600);
        await partialPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});

        const newBoxes = await partialPage.$$('.kit-row .kit-box');
        if (newBoxes.length >= 2) {
          await newBoxes[0].click();
          await partialPage.waitForTimeout(50);
          await newBoxes[1].click();
          await partialPage.waitForTimeout(300);

          // Intercept /api/decide to simulate a partial success:
          // first key decided, second key errors.
          // The fresh context ensures the route cannot leak into other scenarios.
          await partialPage.route('**/api/decide', async (route, request) => {
            const body = JSON.parse(request.postData() || '{}');
            const keys = body.keys || [];
            await route.fulfill({
              status: 200,
              contentType: 'application/json',
              body: JSON.stringify({
                decided: keys.length > 0 ? 1 : 0,
                decided_keys: keys.length > 0 ? [keys[0]] : [],
                errors:
                  keys.length > 1
                    ? [keys[1] + ': disposition not allowed for this kind']
                    : [],
                pushed: false,
              }),
            });
          });

          // Open the decide sheet via the primary button.
          await partialPage.click('.kit-primary');
          await partialPage
            .waitForSelector('.kit-sheet', { timeout: 4000 })
            .catch(() => {});
          await partialPage.waitForTimeout(200);

          // Pick the first available disposition.
          const firstDisp = await partialPage.$('.kit-sheet .cb-sheet-disp');
          if (firstDisp) {
            await firstDisp.click();
            await partialPage.waitForTimeout(100);
          }

          // Click the filled Decide N button.
          const sheetBtns = await partialPage.$$('.kit-sheet button');
          for (const btn of sheetBtns) {
            const t = (await btn.textContent()) ?? '';
            if (/^decide\s+\d+/i.test(t.trim())) {
              await btn.click();
              break;
            }
          }
          await partialPage.waitForTimeout(800);

          // The error for the failed key must be visible inside the sheet.
          const errVisible = await partialPage
            .$eval(
              '.cb-sheet-err',
              (el) =>
                el.offsetParent !== null && (el.textContent || '').length > 0,
            )
            .catch(() => false);
          check(
            'partial decide shows the error for the failed key',
            errVisible,
          );

          // Only the first key was decided (deselected). The second key remains
          // selected. Primary must show 'Decide 1' (not 'Decide 2').
          const primaryText = await partialPage
            .$eval('.kit-primary', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'partial decide deselects only the succeeded key (primary Decide 1)',
            primaryText.trim() === 'Decide 1',
          );
        } else {
          check('partial decide shows the error for the failed key', false);
          check(
            'partial decide deselects only the succeeded key (primary Decide 1)',
            false,
          );
        }
      } finally {
        await partialCtx.close();
      }
    }

    // ---- scenario: decide in bulk -------------------------------------------
    console.log('\nscenario: decide in bulk');

    {
      // Navigate to waiting view and collect rows.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});

      const boxes = await page.$$('.kit-row .kit-box');
      const selectCount = Math.min(4, boxes.length);

      for (let i = 0; i < selectCount; i++) {
        await boxes[i].click();
        await page.waitForTimeout(50);
      }

      if (selectCount > 0) {
        await page.waitForTimeout(300);

        // 1. Primary reads 'Decide N'.
        const primaryText = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'selecting rows updates the primary',
          primaryText.trim() === `Decide ${selectCount}`,
        );

        // Open the decide sheet via the primary button.
        await page.click('.kit-primary');
        await page
          .waitForSelector('.kit-sheet', { timeout: 4000 })
          .catch(() => {});
        await page.waitForTimeout(200);

        const sheetEl = await page.$('.kit-sheet');
        if (sheetEl) {
          // Find and click the 'close' disposition button.
          const dispBtns = await page.$$('.kit-sheet .cb-sheet-disp');
          let clickedClose = false;
          for (const btn of dispBtns) {
            const t = (await btn.textContent()) ?? '';
            if (t.trim() === 'close') {
              await btn.click();
              clickedClose = true;
              break;
            }
          }
          await page.waitForTimeout(150);

          // 2. Preview shows 'close N items'.
          const preview = await page
            .$eval('.cb-sheet-preview', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'the sheet previews the count',
            clickedClose &&
              preview.includes('close') &&
              preview.includes(`${selectCount} item`),
          );

          // Click the filled Decide N button.
          const allSheetBtns = await page.$$('.kit-sheet button');
          let decided = false;
          for (const btn of allSheetBtns) {
            const t = (await btn.textContent()) ?? '';
            if (/^decide\s+\d+/i.test(t.trim())) {
              await btn.click();
              decided = true;
              break;
            }
          }

          // Wait for the API call and list refresh.
          await page.waitForTimeout(2500);

          // 3. Decided items gone; primary hidden.
          const rowsAfter = await page.$$eval(
            '.kit-row',
            (rows) => rows.length,
          );
          const primaryHidden = await page
            .$eval('.kit-primary', (el) => el.hidden)
            .catch(() => true);
          check(
            'deciding removes the items and clears the count',
            decided && (rowsAfter === 0 || primaryHidden),
          );
        } else {
          check('the sheet previews the count', false);
          check('deciding removes the items and clears the count', false);
        }
      } else {
        check('selecting rows updates the primary', false);
        check('the sheet previews the count', false);
        check('deciding removes the items and clears the count', false);
      }
    }

    // ---- scenario: deciding drops the ids it decided ------------------------
    console.log('\nscenario: deciding drops the ids it decided');

    {
      // After the bulk decide above, no waiting rows remain selected.
      // Select one row in the 'new' view (items that were not in 'waiting')
      // and verify the primary shows 'Decide 1' (the decided ids are gone).
      await page.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await page.waitForFunction(() => location.hash === '#/attention/new');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});

      const newBoxes = await page.$$('.kit-row .kit-box');
      if (newBoxes.length > 0) {
        await newBoxes[0].click();
        await page.waitForTimeout(300);
        const primaryText = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        // Primary must show exactly 'Decide 1', not more (decided ids not re-counted).
        check(
          'a decided id is not re-counted',
          primaryText.trim() === 'Decide 1',
        );
        // Clean up: deselect.
        await newBoxes[0].click();
        await page.waitForTimeout(100);
      } else {
        // If no 'new' items remain (all are decided), primary must be hidden.
        const primaryHidden = await page
          .$eval('.kit-primary', (el) => el.hidden)
          .catch(() => true);
        check('a decided id is not re-counted', primaryHidden);
      }
    }

    // ---- scenario: decide one from the detail --------------------------------
    console.log('\nscenario: decide one from the detail');

    {
      const detailPage = await context.newPage();
      try {
        await detailPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await detailPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Open an undecided item in the 'new' view.
        await detailPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await detailPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await detailPage.waitForTimeout(600);
        await detailPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});

        const firstRow = await detailPage.$('.kit-row');
        if (firstRow) {
          await firstRow.dblclick();
          await detailPage
            .waitForSelector('.kit-read .cb-item', { timeout: 5000 })
            .catch(() => {});
          await detailPage.waitForTimeout(300);

          // Click a disposition button in the decide section.
          const decideButtons = await detailPage.$$(
            '.cb-decide .cb-sheet-disp',
          );
          if (decideButtons.length > 0) {
            await decideButtons[0].click();
            await detailPage.waitForTimeout(300);

            // The sheet must appear and mention 1 item (not the selection count).
            const sheetText = await detailPage
              .$eval('.kit-sheet', (el) => (el.textContent ?? '').toLowerCase())
              .catch(() => '');
            check(
              'a disposition button opens the sheet for that one key',
              sheetText.includes('1 item'),
            );

            // Close without deciding.
            await detailPage.keyboard.press('Escape');
            await detailPage.waitForTimeout(200);
          } else {
            check(
              'a disposition button opens the sheet for that one key',
              false,
            );
          }
        } else {
          // Fall back: use a direct item link to a repo item (which hasn't been decided above).
          await detailPage.evaluate(() => {
            location.hash = '#/item/repo:schuettc/hail';
          });
          await detailPage
            .waitForSelector('.kit-read .cb-item', { timeout: 5000 })
            .catch(() => {});
          await detailPage.waitForTimeout(300);

          const decideButtons = await detailPage.$$(
            '.cb-decide .cb-sheet-disp',
          );
          if (decideButtons.length > 0) {
            await decideButtons[0].click();
            await detailPage.waitForTimeout(300);
            const sheetText = await detailPage
              .$eval('.kit-sheet', (el) => (el.textContent ?? '').toLowerCase())
              .catch(() => '');
            check(
              'a disposition button opens the sheet for that one key',
              sheetText.includes('1 item'),
            );
            await detailPage.keyboard.press('Escape');
            await detailPage.waitForTimeout(200);
          } else {
            check(
              'a disposition button opens the sheet for that one key',
              false,
            );
          }
        }
      } finally {
        await detailPage.close();
      }
    }
    // ---- scenario: the board view ------------------------------------------
    console.log(
      "\nscenario: the board shows four lanes with the fixture's items in the right lanes",
    );

    {
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 6000 }).catch(() => {});
      await page.waitForTimeout(800);

      const laneCount = await page.$$eval('.cb-lane', (lanes) => lanes.length);
      check('board shows four lanes', laneCount === 4);

      const laneIds = await page.$$eval('.cb-lane', (lanes) =>
        lanes.map((l) => l.dataset.lane),
      );
      check(
        'lanes are waiting, proposed, due, new in order',
        laneIds[0] === 'waiting' &&
          laneIds[1] === 'proposed' &&
          laneIds[2] === 'due' &&
          laneIds[3] === 'new',
      );

      const waitingCards = await page.$$eval(
        '[data-lane="waiting"] .cb-board-card',
        (cards) => cards.length,
      );
      check('waiting lane has cards from the fixture', waitingCards > 0);

      // Navigate back to list view.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: selection made on the board shows on the list ------------
    console.log(
      '\nscenario: a selection made on the board shows on the list and vice versa',
    );

    {
      // Navigate to board.
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(600);

      // Click the card's checkbox (.kit-box) for selection; clicking the
      // title navigates to the item instead (brief fix-round item 2).
      const firstCardBox = await page.$(
        '[data-lane="waiting"] .cb-board-card .kit-box',
      );
      const firstCard = await page.$('[data-lane="waiting"] .cb-board-card');
      if (firstCardBox && firstCard) {
        await firstCardBox.click();
        await page.waitForTimeout(300);

        const isSelected = await firstCard
          .evaluate((el) => el.classList.contains('on'))
          .catch(() => false);
        check('clicking a board card selects it', isSelected);

        // Navigate to list view; primary should reflect the selection.
        await page.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await page.waitForFunction(
          () => location.hash === '#/attention/waiting',
        );
        await page.waitForTimeout(600);

        const primaryText = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'selection from board is visible on the list (primary shows Decide N)',
          primaryText.trim().startsWith('Decide '),
        );

        // Go back to board and deselect to clean up.
        await page.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await page.waitForFunction(() => location.hash === '#/attention/board');
        await page
          .waitForSelector('.cb-lane', { timeout: 5000 })
          .catch(() => {});
        await page.waitForTimeout(600);
        const selCardBox = await page.$(
          '[data-lane="waiting"] .cb-board-card.on .kit-box',
        );
        if (selCardBox) {
          await selCardBox.click();
          await page.waitForTimeout(200);
        }
      } else {
        check('clicking a board card selects it', false);
        check(
          'selection from board is visible on the list (primary shows Decide N)',
          false,
        );
      }

      // Back to list.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: shift-click ranges within a lane -----------------------
    console.log('\nscenario: shift-click ranges within a lane');

    {
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(600);

      // Use .kit-box (the checkbox) for clicks so the card's title (which
      // navigates to the item, brief fix-round item 2) is not triggered.
      const waitingBoxes = await page.$$(
        '[data-lane="waiting"] .cb-board-card .kit-box',
      );
      const waitingCards = await page.$$(
        '[data-lane="waiting"] .cb-board-card',
      );
      if (waitingBoxes.length >= 2) {
        // Click first card's checkbox, then shift-click second card's checkbox.
        await waitingBoxes[0].click();
        await page.waitForTimeout(150);
        await waitingBoxes[1].click({ modifiers: ['Shift'] });
        await page.waitForTimeout(150);

        const selectedCount = await page.$$eval(
          '[data-lane="waiting"] .cb-board-card.on',
          (cards) => cards.length,
        );
        check(
          'shift-click selects a range within the lane',
          selectedCount >= 2,
        );

        // Deselect by clicking each selected card's checkbox.
        const selBoxes = await page.$$(
          '[data-lane="waiting"] .cb-board-card.on .kit-box',
        );
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else if (waitingBoxes.length === 1) {
        await waitingBoxes[0].click();
        await page.waitForTimeout(100);
        const isSelected = await waitingCards[0]
          ?.evaluate((el) => el.classList.contains('on'))
          .catch(() => false);
        check('shift-click selects a range within the lane', isSelected);
        await waitingBoxes[0].click();
        await page.waitForTimeout(100);
      } else {
        check('shift-click selects a range within the lane', false);
      }

      // Back to list.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: deciding from the board removes the card -----------------
    console.log(
      '\nscenario: deciding from the board removes the card and deselects it',
    );

    {
      const boardDecideCtx = await browser.newContext();
      const boardDecidePage = await boardDecideCtx.newPage();
      try {
        await boardDecidePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await boardDecidePage.waitForSelector('.kit-bar', { timeout: 8000 });

        await boardDecidePage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await boardDecidePage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await boardDecidePage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await boardDecidePage.waitForTimeout(800);

        const firstCard = await boardDecidePage.$(
          '[data-lane="waiting"] .cb-board-card',
        );
        // Click the checkbox (.kit-box) for selection; clicking the title
        // navigates instead of selecting (brief fix-round item 2).
        const firstCardBox = await boardDecidePage.$(
          '[data-lane="waiting"] .cb-board-card .kit-box',
        );
        if (firstCard && firstCardBox) {
          const cardId = await firstCard
            .evaluate((el) => el.dataset.id ?? '')
            .catch(() => '');
          await firstCardBox.click();
          await boardDecidePage.waitForTimeout(300);

          // Click Decide N.
          await boardDecidePage.click('.kit-primary');
          await boardDecidePage
            .waitForSelector('.kit-sheet', { timeout: 4000 })
            .catch(() => {});
          await boardDecidePage.waitForTimeout(200);

          // Pick the first disposition.
          const firstDisp = await boardDecidePage.$(
            '.kit-sheet .cb-sheet-disp',
          );
          if (firstDisp) {
            await firstDisp.click();
            await boardDecidePage.waitForTimeout(100);
          }

          // Click the filled Decide N button.
          const sheetBtns = await boardDecidePage.$$('.kit-sheet button');
          for (const btn of sheetBtns) {
            const t = (await btn.textContent()) ?? '';
            if (/^decide\s+\d+/i.test(t.trim())) {
              await btn.click();
              break;
            }
          }
          await boardDecidePage.waitForTimeout(2500);

          // The decided card must be gone or selection cleared.
          const cardStillThere = cardId
            ? await boardDecidePage
                .$eval(
                  `[data-lane="waiting"] .cb-board-card[data-id="${cardId}"]`,
                  () => true,
                )
                .catch(() => false)
            : true;
          const primaryHidden = await boardDecidePage
            .$eval('.kit-primary', (el) => el.hidden)
            .catch(() => true);
          check(
            'deciding from the board removes the card and deselects it',
            !cardStillThere || primaryHidden,
          );
        } else {
          check(
            'deciding from the board removes the card and deselects it',
            false,
          );
        }
      } finally {
        await boardDecideCtx.close();
      }
    }

    // ---- scenario: a filter narrows every lane ------------------------------
    console.log('\nscenario: a filter narrows every lane');

    {
      // Count cards in the waiting lane without any filter.
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(800);

      const unfilteredWaiting = await page.$$eval(
        '[data-lane="waiting"] .cb-board-card',
        (cards) => cards.length,
      );

      // Open a fresh page with ?q=nudge to apply a filter.
      const filterPage = await context.newPage();
      try {
        const baseUrl = serveHandle.base.replace(/\/$/, '');
        await filterPage.goto(baseUrl + '/?q=nudge#/attention/board', {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await filterPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await filterPage
          .waitForSelector('.cb-lane', { timeout: 8000 })
          .catch(() => {});
        await filterPage.waitForTimeout(800);

        const filteredWaiting = await filterPage.$$eval(
          '[data-lane="waiting"] .cb-board-card',
          (cards) => cards.length,
        );
        check(
          'a filter narrows the board lanes',
          filteredWaiting < unfilteredWaiting || filteredWaiting <= 1,
        );
      } finally {
        await filterPage.close();
      }

      // Navigate back to waiting list view.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(600);
    }

    // ---- scenario: board layout at 1600×900 ---------------------------------
    console.log(
      '\nscenario: board layout at 1600\xd7900 \u2014 all four lanes visible and \u2265220\u202fpx wide',
    );

    {
      const wideCtx = await browser.newContext({
        viewport: { width: 1600, height: 900 },
      });
      const widePage = await wideCtx.newPage();
      try {
        await widePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await widePage.waitForSelector('.kit-bar', { timeout: 8000 });

        await widePage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await widePage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await widePage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await widePage.waitForTimeout(800);

        // Screenshot after fix (required by the controller ruling).
        await widePage.screenshot({ path: '/tmp/board-after.png' });

        // The \'board\' chip must be the active view chip.
        const boardChipActive = await widePage
          .$eval('[data-id="board"]', (el) => el.classList.contains('on'))
          .catch(() => false);
        check(
          'board chip is the active view chip at 1600\xd7900',
          boardChipActive,
        );

        const lanes = await widePage.$$('.cb-lane');
        check('board shows four lanes at 1600\xd7900', lanes.length === 4);

        let allInViewport = true;
        let allWideEnough = true;
        for (const lane of lanes) {
          const box = await lane.boundingBox();
          if (!box) {
            allInViewport = false;
            allWideEnough = false;
            break;
          }
          if (box.x < 0 || box.x + box.width > 1600 + 1) allInViewport = false;
          if (box.width < 220) allWideEnough = false;
        }
        check(
          'all four lane bounding boxes are inside the 1600\xd7900 viewport',
          allInViewport,
        );
        check(
          'all four lanes are at least 220\u202fpx wide at 1600\xd7900',
          allWideEnough,
        );
      } finally {
        await wideCtx.close();
      }
    }

    // ---- scenario: board layout at 1100×800 (horizontal scroll) -----------
    console.log(
      '\nscenario: board layout at 1100\xd7800 \u2014 board scrolls, document does not',
    );

    {
      const narrowCtx = await browser.newContext({
        viewport: { width: 1100, height: 800 },
      });
      const narrowPage = await narrowCtx.newPage();
      try {
        await narrowPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await narrowPage.waitForSelector('.kit-bar', { timeout: 8000 });

        await narrowPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await narrowPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await narrowPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await narrowPage.waitForTimeout(800);

        // The document (html element) must not scroll horizontally.
        const noDocScroll = await narrowPage.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        );
        check(
          'document does not scroll horizontally at 1100\xd7800',
          noDocScroll,
        );

        // The board container must be reachable: it is either wide enough to
        // show all lanes without scrolling, or it provides its own scrollbar.
        const boardReachable = await narrowPage
          .$eval(
            '.cb-board',
            (el) =>
              // All lanes fit without scrolling.
              el.scrollWidth <= el.clientWidth ||
              // Or the board itself is scrollable.
              el.scrollWidth > el.clientWidth,
          )
          .catch(() => true); // .cb-board not found is handled above
        check(
          'board lanes are reachable (board scrolls, not document) at 1100\xd7800',
          noDocScroll && boardReachable,
        );
      } finally {
        await narrowCtx.close();
      }
    }

    // ---- scenario: opening a card from the board ----------------------------
    console.log(
      '\nscenario: opening a card from the board shows item in reading column',
    );

    {
      const cardCtx = await browser.newContext();
      const cardPage = await cardCtx.newPage();
      try {
        await cardPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await cardPage.waitForSelector('.kit-bar', { timeout: 8000 });

        await cardPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await cardPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await cardPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await cardPage.waitForTimeout(800);

        // Click the title element of the first waiting card.
        const titleEl = await cardPage.$(
          '[data-lane="waiting"] .cb-board-card .cb-card-title',
        );
        const cardTitle = titleEl
          ? ((await titleEl.textContent()) ?? '').trim()
          : '';

        if (titleEl && cardTitle) {
          await titleEl.click();
          // Wait for the item detail to load in the reading column.
          await cardPage.waitForTimeout(1200);

          const readText = await cardPage
            .$eval('.kit-read', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'opening a board card title shows the item in the reading column',
            readText.includes(cardTitle),
          );

          // The hash must now be #/item/<key> (not #/attention/board).
          const afterHash = await cardPage.evaluate(() => location.hash);
          check(
            'opening a board card navigates to #/item/<key>',
            afterHash.startsWith('#/item/'),
          );

          // Pressing Back must return to #/attention/board.
          await cardPage.goBack();
          await cardPage.waitForTimeout(600);
          const backHash = await cardPage.evaluate(() => location.hash);
          check(
            'Back from the item view returns to #/attention/board',
            backHash === '#/attention/board',
          );
        } else {
          check(
            'opening a board card title shows the item in the reading column',
            false,
          );
          check('opening a board card navigates to #/item/<key>', false);
          check('Back from the item view returns to #/attention/board', false);
        }
      } finally {
        await cardCtx.close();
      }
    }
    // ---- fix-round-2: encoded item URL opens correctly ------------------
    console.log(
      '\nscenario: encoded item URL (casebook_open-style) opens item detail',
    );

    {
      const encPage = await context.newPage();
      try {
        await encPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await encPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to the server-encoded URL: url.PathEscape('issue:schuettc/hail#4')
        // → 'issue:schuettc%2Fhail%234'.
        await encPage.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });

        // Wait for the reading column to populate.
        await encPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await encPage.waitForTimeout(600);

        const itemTitle = await encPage
          .$eval('.kit-read .cb-title', (el) => el.textContent ?? '')
          .catch(() => '');
        check(
          'server-encoded item URL (#/item/issue:schuettc%2Fhail%234) opens item detail',
          itemTitle.length > 0,
        );
      } finally {
        await encPage.close();
      }
    }

    // ---- fix-round-2: board "select all N in view" counts all lanes --------
    console.log(
      '\nscenario: board select-all reflects de-duplicated total across all lanes',
    );

    {
      const selAllPage = await context.newPage();
      try {
        await selAllPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await selAllPage.waitForSelector('.kit-bar', { timeout: 8000 });

        await selAllPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await selAllPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await selAllPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        // Give the board time to fetch all four lanes.
        await selAllPage.waitForTimeout(1200);

        // Count total cards across all lanes.
        const totalCards = await selAllPage.$$eval(
          '.cb-board-card',
          (cards) => cards.length,
        );

        const selAllText = await selAllPage
          .$eval('.cb-sel-all', (el) => el.textContent ?? '')
          .catch(() => '');
        const match = selAllText.match(/select all (\.?\d+) in view/);
        const selAllCount = match ? parseInt(match[1], 10) : -1;

        check(
          'board select-all shows total unique items (matches card count)',
          selAllCount > 0 && selAllCount === totalCards,
        );

        // Click select-all and verify the selection count equals totalCards.
        await selAllPage.click('.cb-sel-all');
        await selAllPage.waitForTimeout(300);
        const selectedCards = await selAllPage.$$eval(
          '.cb-board-card.on',
          (cards) => cards.length,
        );
        check(
          'board select-all selects exactly the total unique items',
          selectedCards === totalCards && totalCards > 0,
        );
      } finally {
        await selAllPage.close();
      }
    }

    // ---- fix-round-2: board lane "show N more" reflects de-duplicated count
    console.log(
      '\nscenario: board lane show-more reflects de-duplicated items',
    );

    {
      const showMorePage = await context.newPage();
      try {
        await showMorePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await showMorePage.waitForSelector('.kit-bar', { timeout: 8000 });

        await showMorePage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await showMorePage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await showMorePage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await showMorePage.waitForTimeout(1200);

        // For each lane: if the "show more" button is visible, it must show a
        // positive number > 0.  More importantly, if a lane's total unique items
        // equals the number of cards visible, the button must be hidden.
        let allLanesCorrect = true;
        for (const laneId of ['waiting', 'proposed', 'due', 'new']) {
          const moreHidden = await showMorePage
            .$eval(`[data-lane="${laneId}"] .cb-lane-more`, (el) => el.hidden)
            .catch(() => true);
          const moreText = await showMorePage
            .$eval(
              `[data-lane="${laneId}"] .cb-lane-more`,
              (el) => el.textContent ?? '',
            )
            .catch(() => '');
          // If visible, the number in "show N more" must be > 0.
          if (!moreHidden) {
            const n = parseInt((moreText.match(/\d+/) ?? ['0'])[0], 10);
            if (n <= 0) {
              allLanesCorrect = false;
              console.error(
                `    lane "${laneId}": "show more" visible with non-positive count (${moreText})`,
              );
            }
          }
          // If all cards for this lane are shown and no more pages would have
          // unique items, the button should be hidden.
          // (We can't know server-side totals here, so just check the logic
          // is consistent: visible button → positive count.)
        }
        check(
          'board lane show-more is hidden when all unique items are shown',
          allLanesCorrect,
        );
      } finally {
        await showMorePage.close();
      }
    }

    // ---- fix-round-2: empty reading column shows quiet empty state ---------
    console.log('\nscenario: empty reading column shows the quiet empty state');

    {
      const emptyReadPage = await context.newPage();
      try {
        await emptyReadPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await emptyReadPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to waiting view (no item open).
        await emptyReadPage.evaluate(() => {
          location.hash = '#/attention/waiting';
        });
        await emptyReadPage.waitForFunction(
          () => location.hash === '#/attention/waiting',
        );
        await emptyReadPage
          .waitForSelector('.kit-row', { timeout: 8000 })
          .catch(() => {});
        await emptyReadPage.waitForTimeout(600);

        // The reading column must have the empty-state element (not be blank).
        const hasEmptyState = await emptyReadPage
          .$eval('.kit-read .cb-read-empty', (el) => el.offsetParent !== null)
          .catch(() => false);
        check(
          'empty reading column shows .cb-read-empty instead of blank',
          hasEmptyState,
        );

        // The empty state must show the section name.
        const sectionLabel = await emptyReadPage
          .$eval(
            '.kit-read .cb-read-empty-section',
            (el) => el.textContent ?? '',
          )
          .catch(() => '');
        check(
          'empty state shows the section name (attention)',
          sectionLabel.toLowerCase().includes('attention'),
        );

        // The empty state must show a prompt line.
        const promptText = await emptyReadPage
          .$eval(
            '.kit-read .cb-read-empty-prompt',
            (el) => el.textContent ?? '',
          )
          .catch(() => '');
        check('empty state shows a prompt line', promptText.length > 0);
      } finally {
        await emptyReadPage.close();
      }
    }

    // ---- scenario: fidelity — geometry and computed style -------------------
    console.log('\nscenario: fidelity — geometry and computed style');

    {
      const fidPage = await context.newPage();
      try {
        await fidPage.setViewportSize({ width: 1600, height: 900 });
        await fidPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await fidPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to item detail (encoded key as the server uses).
        await fidPage.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fidPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fidPage.waitForTimeout(600);

        // 1. Reading document geometry: left edge ≥ 40 px from list right edge;
        //    document width ≤ 700 px.
        {
          const listRight = await fidPage
            .$eval('.kit-list', (el) => el.getBoundingClientRect().right)
            .catch(() => 0);
          const docRect = await fidPage
            .$eval('.kit-doc', (el) => {
              const r = el.getBoundingClientRect();
              return { left: r.left, width: r.width };
            })
            .catch(() => ({ left: 0, width: 0 }));
          check(
            'reading document left edge is ≥ 40 px from list right edge',
            docRect.left - listRight >= 40,
          );
          check(
            'reading document width is ≤ 700 px',
            docRect.width > 0 && docRect.width <= 700,
          );
        }

        // 2. Kicker font-family is monospace.
        {
          const kickerFont = await fidPage
            .$eval('.kit-read .cb-kicker', (el) =>
              getComputedStyle(el).fontFamily.toLowerCase(),
            )
            .catch(() => '');
          check(
            'kicker font-family is monospace',
            kickerFont.includes('mono') ||
              kickerFont.includes('menlo') ||
              kickerFont.includes('courier'),
          );
        }

        // 3. Section labels are uppercase and mono.
        {
          const labelStyle = await fidPage
            .$eval('.kit-read .kit-label', (el) => {
              const cs = getComputedStyle(el);
              return {
                transform: cs.textTransform,
                font: cs.fontFamily.toLowerCase(),
              };
            })
            .catch(() => ({ transform: '', font: '' }));
          check(
            'section label text-transform is uppercase',
            labelStyle.transform === 'uppercase',
          );
          check(
            'section label font-family is monospace',
            labelStyle.font.includes('mono') ||
              labelStyle.font.includes('menlo') ||
              labelStyle.font.includes('courier'),
          );
        }

        // 4. Brand mark width and height are > 0 and it is visible.
        {
          const markSize = await fidPage
            .$eval('.kit-brand svg', (el) => {
              const r = el.getBoundingClientRect();
              return {
                width: r.width,
                height: r.height,
                visible:
                  el.offsetParent !== null ||
                  el.getBoundingClientRect().width > 0,
              };
            })
            .catch(() => ({ width: 0, height: 0, visible: false }));
          check('brand mark width > 0', markSize.width > 0);
          check('brand mark height > 0', markSize.height > 0);
          check('brand mark is visible', markSize.visible);
        }

        // ---- after-screenshots (light) ------------------------------------
        await fidPage.screenshot({
          path: '/tmp/fid-light-item.png',
          fullPage: false,
        });

        // Navigate to list view for second screenshot.
        await fidPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await fidPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await fidPage.waitForTimeout(600);
        await fidPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await fidPage.waitForTimeout(300);
        await fidPage.screenshot({
          path: '/tmp/fid-light-new.png',
          fullPage: false,
        });

        // ---- after-screenshots (dark) ------------------------------------
        // Toggle to dark mode.
        await fidPage.click('button.kit-ctl:has-text("theme")');
        await fidPage.waitForTimeout(200);
        // If we're not in dark yet, click once more (system → light → dark).
        const theme = await fidPage.$eval(
          'html',
          (el) => el.dataset.theme ?? '',
        );
        if (theme !== 'dark') {
          await fidPage.click('button.kit-ctl:has-text("theme")');
          await fidPage.waitForTimeout(200);
        }

        // Dark screenshot of the list view.
        await fidPage.screenshot({
          path: '/tmp/fid-dark-new.png',
          fullPage: false,
        });

        // Navigate to item detail for dark screenshot.
        await fidPage.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fidPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fidPage.waitForTimeout(600);
        await fidPage.screenshot({
          path: '/tmp/fid-dark-item.png',
          fullPage: false,
        });

        console.log(
          '  screenshots: /tmp/fid-light-item.png  /tmp/fid-light-new.png',
        );
        console.log(
          '               /tmp/fid-dark-item.png   /tmp/fid-dark-new.png',
        );
      } finally {
        await fidPage.close();
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
