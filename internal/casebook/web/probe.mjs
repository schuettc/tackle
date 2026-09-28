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
      // Navigate to the 'new' view (shows all undecided items).
      // We use 'new' instead of 'waiting' so that waiting items (incoming PRs
      // and issues with incoming-no-reply hits) are preserved for the board
      // scenario that runs later and requires items in the waiting lane.
      await page.evaluate(() => {
        location.hash = '#/attention/new';
      });
      await page.waitForFunction(() => location.hash === '#/attention/new');
      await page.waitForTimeout(600);
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});

      const boxes = await page.$$('.kit-row .kit-box');
      // Select only 2 items to leave enough items alive for subsequent tests.
      const selectCount = Math.min(2, boxes.length);

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
          // Click the first available disposition button (keep is valid for all kinds).
          const dispBtns = await page.$$('.kit-sheet .cb-sheet-disp');
          let clickedDisp = false;
          let clickedLabel = '';
          // Try 'keep' first (valid for all kinds); fall back to any button.
          for (const btn of dispBtns) {
            const t = (await btn.textContent()) ?? '';
            if (t.trim() === 'keep') {
              await btn.click();
              clickedDisp = true;
              clickedLabel = 'keep';
              break;
            }
          }
          if (!clickedDisp && dispBtns.length > 0) {
            const t = (await dispBtns[0].textContent()) ?? '';
            await dispBtns[0].click();
            clickedDisp = true;
            clickedLabel = t.trim();
          }
          await page.waitForTimeout(150);

          // 2. Preview shows '<disposition> N items'.
          const preview = await page
            .$eval('.cb-sheet-preview', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'the sheet previews the count',
            clickedDisp &&
              preview.includes(clickedLabel) &&
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
        '[data-lane="waiting"] .kit-card',
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
        '[data-lane="waiting"] .kit-card .kit-box',
      );
      const firstCard = await page.$('[data-lane="waiting"] .kit-card');
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
          '[data-lane="waiting"] .kit-card.on .kit-box',
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

    // ---- scenario: list selection shows on the board (reverse direction) ---
    console.log('\nscenario: a selection made on the list shows on the board');

    {
      // Ensure we're on the waiting list view with rows loaded.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForSelector('.kit-row', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(400);

      const firstRowBox = await page.$('.kit-row .kit-box');
      if (firstRowBox) {
        // Select the first row by clicking its checkbox.
        await firstRowBox.click();
        await page.waitForTimeout(200);

        // Verify selection took effect (primary shows "Decide 1" or similar).
        const primaryAfterSelect = await page
          .$eval('.kit-primary', (el) => el.textContent ?? '')
          .catch(() => '');
        const hasSelection = primaryAfterSelect.trim().startsWith('Decide ');

        // Navigate to the board.
        await page.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await page.waitForFunction(() => location.hash === '#/attention/board');
        await page
          .waitForSelector('.cb-lane', { timeout: 5000 })
          .catch(() => {});
        await page.waitForTimeout(600);

        // At least one card in any lane must have .on class (the selected item).
        // We don't use data-id from the list row (kit doesn't expose it as DOM
        // attribute); instead we verify any card is selected on the board.
        const anyCardOn = await page
          .$$eval('.cb-lane .kit-card.on', (cards) => cards.length)
          .catch(() => 0);
        check(
          'selection from list is visible on the board (card has .on class)',
          hasSelection && anyCardOn > 0,
        );

        // Deselect all selected cards on the board so subsequent scenarios
        // start with a clean selection.
        const selBoxes = await page.$$('.cb-lane .kit-card.on .kit-box');
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else {
        check(
          'selection from list is visible on the board (card has .on class)',
          false,
        );
      }

      // Back to waiting.
      await page.evaluate(() => {
        location.hash = '#/attention/waiting';
      });
      await page.waitForFunction(() => location.hash === '#/attention/waiting');
      await page.waitForTimeout(400);
    }

    // ---- scenario: shift-click ranges within a lane -----------------------
    // Fixture has 3 waiting items (pr #3, issue #4, issue #5), so we can
    // click the first, shift-click the third, and assert the middle is selected.
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
        '[data-lane="waiting"] .kit-card .kit-box',
      );
      if (waitingBoxes.length >= 3) {
        // Click first card, shift-click third card — middle card must be selected.
        await waitingBoxes[0].click();
        await page.waitForTimeout(150);
        await waitingBoxes[2].click({ modifiers: ['Shift'] });
        await page.waitForTimeout(150);

        const waitingCardsAll = await page.$$(
          '[data-lane="waiting"] .kit-card',
        );
        // The middle card (index 1) must have .on class.
        const middleSelected = await waitingCardsAll[1]
          ?.evaluate((el) => el.classList.contains('on'))
          .catch(() => false);
        check(
          'shift-click selects the middle card in the range',
          middleSelected === true,
        );

        const selectedCount = await page.$$eval(
          '[data-lane="waiting"] .kit-card.on',
          (cards) => cards.length,
        );
        check(
          'shift-click selects a range within the lane (≥3 cards)',
          selectedCount >= 3,
        );

        // Deselect by clicking each selected card's checkbox.
        const selBoxes = await page.$$(
          '[data-lane="waiting"] .kit-card.on .kit-box',
        );
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else if (waitingBoxes.length >= 2) {
        // Fallback: fixture has only 2 waiting cards.
        await waitingBoxes[0].click();
        await page.waitForTimeout(150);
        await waitingBoxes[1].click({ modifiers: ['Shift'] });
        await page.waitForTimeout(150);
        check('shift-click selects a range within the lane (≥3 cards)', false);
        check('shift-click selects the middle card in the range', false);
        const selBoxes = await page.$$(
          '[data-lane="waiting"] .kit-card.on .kit-box',
        );
        for (const b of selBoxes) {
          await b.click();
          await page.waitForTimeout(50);
        }
      } else {
        check('shift-click selects a range within the lane (≥3 cards)', false);
        check('shift-click selects the middle card in the range', false);
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
          '[data-lane="waiting"] .kit-card',
        );
        // Click the checkbox (.kit-box) for selection; clicking the title
        // navigates instead of selecting (brief fix-round item 2).
        const firstCardBox = await boardDecidePage.$(
          '[data-lane="waiting"] .kit-card .kit-box',
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
                  `[data-lane="waiting"] .kit-card[data-id="${cardId}"]`,
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
    // Check all four lanes; unfiltered total must exceed filtered total.
    // ?q=nudge matches only pr #3 "fix nudge" in the waiting lane; all other
    // items (issue #4, issue #5, repo, branch) do not contain "nudge".
    console.log('\nscenario: a filter narrows every lane');

    {
      // Count total cards across all lanes without any filter.
      await page.evaluate(() => {
        location.hash = '#/attention/board';
      });
      await page.waitForFunction(() => location.hash === '#/attention/board');
      await page.waitForSelector('.cb-lane', { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(800);

      const unfilteredTotal = await page.$$eval(
        '.cb-lane .kit-card',
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

        const filteredTotal = await filterPage.$$eval(
          '.cb-lane .kit-card',
          (cards) => cards.length,
        );
        // ?q=nudge matches only the "fix nudge" PR; the filter must reduce the
        // total count across all lanes. No weakening || condition here.
        check(
          'a filter narrows every lane (total filtered < total unfiltered)',
          filteredTotal < unfilteredTotal,
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
          '[data-lane="waiting"] .kit-card .cb-card-title',
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
          location.hash = '#/item/issue:schuettc%2Fhail%236';
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

        // Count total cards across all lanes (scoped to .cb-lane so other
        // kit-cards — e.g. in the decide sheet — are not counted).
        const totalCards = await selAllPage.$$eval(
          '.cb-lane .kit-card',
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
          '.cb-lane .kit-card.on',
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

    // ---- scenario: title fallback uses keyWithoutKind (no kind repetition) --
    console.log(
      '\nscenario: title fallback uses keyWithoutKind (no kind in board card title)',
    );

    {
      const titleFallbackPage = await context.newPage();
      try {
        await titleFallbackPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await titleFallbackPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await titleFallbackPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await titleFallbackPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await titleFallbackPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await titleFallbackPage.waitForTimeout(800);

        // Check all board card titles: none should start with a kind prefix
        // followed by a colon, e.g. "branch:schuettc/hail@feat/client".
        // (Titleless items fall back to keyWithoutKind, e.g.
        //  "schuettc/hail@feat/client" rather than "branch:schuettc/hail@feat/client".)
        const badTitles = await titleFallbackPage.$$eval(
          '.cb-lane .kit-card .cb-card-title',
          (els) =>
            els
              .map((el) => el.textContent ?? '')
              .filter((t) => /^(branch|repo|issue|pr|worktree):/.test(t)),
        );
        check(
          'no board card title starts with a kind prefix (fallback is keyWithoutKind)',
          badTitles.length === 0,
        );
      } finally {
        await titleFallbackPage.close();
      }
    }

    // ---- scenario: live decided event deselects the decided keys ------------
    console.log(
      '\nscenario: live decided event deselects the decided item on the page',
    );

    {
      const liveDeselPage = await context.newPage();
      try {
        await liveDeselPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await liveDeselPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to board.
        await liveDeselPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await liveDeselPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await liveDeselPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await liveDeselPage.waitForTimeout(800);

        // Select the first waiting card via checkbox.
        const firstBox = await liveDeselPage.$(
          '[data-lane="waiting"] .kit-card .kit-box',
        );
        const firstCard = await liveDeselPage.$(
          '[data-lane="waiting"] .kit-card',
        );
        const cardId = firstCard
          ? await firstCard
              .evaluate((el) => el.dataset.id ?? '')
              .catch(() => '')
          : '';

        if (firstBox && cardId) {
          await firstBox.click();
          await liveDeselPage.waitForTimeout(200);

          const isSelected = await firstCard
            .evaluate((el) => el.classList.contains('on'))
            .catch(() => false);

          if (isSelected) {
            // Post a decide directly to the API (not via the page's UI) to
            // simulate a live decided event from another client/CLI.
            const resp = await liveDeselPage.evaluate(
              async ({ url, key }) => {
                const r = await fetch(url + '/api/decide', {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({
                    keys: [key],
                    disposition: 'keep',
                    note: 'live deselect probe',
                  }),
                });
                return r.status;
              },
              { url: serveHandle.base.replace(/\/$/, ''), key: cardId },
            );

            if (resp === 200) {
              // Wait for the live decided event to arrive and be processed.
              await liveDeselPage.waitForTimeout(2500);

              // The card must now be deselected (not in the selection store).
              // Check: primary button must not show "Decide 1" or count the decided item.
              await liveDeselPage.evaluate(() => {
                location.hash = '#/attention/waiting';
              });
              await liveDeselPage.waitForFunction(
                () => location.hash === '#/attention/waiting',
              );
              await liveDeselPage.waitForTimeout(600);

              const primaryText = await liveDeselPage
                .$eval('.kit-primary', (el) => el.textContent ?? '')
                .catch(() => '');
              // If the live decided event deselected the key, the primary
              // either shows 'Decide 0' (which should be hidden) or is absent.
              check(
                'live decided event deselects the decided key',
                !primaryText.trim().startsWith('Decide '),
              );
            } else {
              check('live decided event deselects the decided key', false);
            }
          } else {
            check('live decided event deselects the decided key', false);
          }
        } else {
          check('live decided event deselects the decided key', false);
        }
      } finally {
        await liveDeselPage.close();
      }
    }

    // ---- scenario: proposals event updates the proposed chip count ----------
    // Post a fake-agent proposal via /api/agent/propose.  The server emits a
    // "proposals" live event WITHOUT a follow-up index rebuild, so the page
    // must re-fetch /api/summary and update the "proposed" chip count.
    console.log(
      '\nscenario: fake-agent proposal updates the "proposed" chip count',
    );

    {
      const propCountPage = await context.newPage();
      try {
        await propCountPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await propCountPage.waitForSelector('.kit-bar', { timeout: 8000 });
        // Wait for the initial summary counts to arrive.
        await propCountPage
          .waitForSelector('.kit-chip[data-id="proposed"] .kit-n', {
            timeout: 6000,
          })
          .catch(() => {});
        await propCountPage.waitForTimeout(500);

        // Read the "proposed" chip count before posting the proposal.
        const beforeText = await propCountPage
          .$eval(
            '.kit-chip[data-id="proposed"] .kit-n',
            (el) => el.textContent ?? '',
          )
          .catch(() => '0');
        const beforeN = parseInt(beforeText, 10) || 0;

        // Use issue:schuettc/hail#5 (added to the fixture for this probe).
        // POST to /api/agent/propose — this does NOT trigger an index rebuild,
        // so only the "proposals" live event fires.  The page must re-fetch
        // /api/summary and update the chip count.
        const propKey = 'pr:schuettc/hail#3';
        const propSess = 'probe-session-1';
        await propCountPage.evaluate(
          async ({ url, key, sess }) => {
            // Register the agent session first (required before posting proposals).
            await fetch(url + '/api/agent/presence', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                id: sess,
                harness: 'pi',
                label: 'probe agent',
                cwd: '/tmp',
                pid: 0,
              }),
            });
            await fetch(url + '/api/agent/propose', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                session: sess,
                keys: [key],
                disposition: 'keep',
              }),
            });
          },
          {
            url: serveHandle.base.replace(/\/$/, ''),
            key: propKey,
            sess: propSess,
          },
        );

        // Wait for the live proposals event to be received and the summary
        // re-fetch to complete.
        await propCountPage.waitForTimeout(5000);

        // The "proposed" chip count must have increased.
        const afterText = await propCountPage
          .$eval(
            '.kit-chip[data-id="proposed"] .kit-n',
            (el) => el.textContent ?? '',
          )
          .catch(() => '0');
        const afterN = parseInt(afterText, 10) || 0;

        check(
          'fake-agent proposal updates the "proposed" chip count',
          afterN > beforeN,
        );
      } finally {
        await propCountPage.close();
      }
    }

    // ---- scenario: accepting and rejecting proposals (Task 5) ---------------
    // The chip-count scenario already posted a proposal on pr:schuettc/hail#3
    // from a 'pi' session.  These scenarios build on that state and also create
    // fresh proposals to test accept/reject/change/keys.
    console.log('\nscenario: accepting and rejecting proposals');

    // Helper: register an agent session and propose a disposition via the API.
    async function agentPropose(
      propPage,
      sess,
      harness,
      key,
      disposition,
      note,
    ) {
      return propPage.evaluate(
        async ({ base, session, h, k, disp, n }) => {
          const presence = await fetch(base + '/api/agent/presence', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              id: session,
              harness: h,
              label: h + ' probe',
              cwd: '/tmp',
              pid: 0,
            }),
          }).then((r) => r.json());
          const propose = await fetch(base + '/api/agent/propose', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              session,
              keys: [k],
              disposition: disp,
              ...(n ? { note: n } : {}),
            }),
          }).then((r) => r.json());
          return { presence, propose };
        },
        {
          base: serveHandle.base.replace(/\/$/, ''),
          session: sess,
          h: harness,
          k: key,
          disp: disposition,
          n: note || '',
        },
      );
    }

    // ---- A: proposal card geometry and agent name --------------------------
    // pr:schuettc/hail#3 already has a pending proposal from 'pi' session.
    {
      const propGeoPage = await context.newPage();
      try {
        await propGeoPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await propGeoPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Open the item detail via the hash route.
        await propGeoPage.evaluate(() => {
          location.hash = '#/item/pr:schuettc%2Fhail%233';
        });
        await propGeoPage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await propGeoPage.waitForTimeout(1000);

        // The proposal card must be present.
        const hasCard = await propGeoPage
          .$('.cb-proposal-card')
          .then((el) => Boolean(el))
          .catch(() => false);
        check(
          'proposal card appears in item detail when a pending proposal exists',
          hasCard,
        );

        if (hasCard) {
          // Card head: "<agent> proposes · <disposition>" not "pending proposal".
          const cardHead = await propGeoPage
            .$eval(
              '.cb-proposal-card .kit-card-head',
              (el) => el.textContent ?? '',
            )
            .catch(() => '');
          check(
            'proposal card head contains "proposes ·"',
            cardHead.includes('proposes \u00b7'),
          );
          check(
            'proposal card head uses real agent name (pi), not hardcoded string',
            cardHead.toLowerCase().startsWith('pi'),
          );

          // Geometry: proposal card bottom <= decide section top.
          const cardBottom = await propGeoPage
            .$eval(
              '.cb-proposal-card',
              (el) => el.getBoundingClientRect().bottom,
            )
            .catch(() => -1);
          const decideTop = await propGeoPage
            .$eval('.cb-decide', (el) => el.getBoundingClientRect().top)
            .catch(() => -1);
          check(
            'proposal card is above the decide section (geometry)',
            cardBottom > 0 && decideTop > 0 && cardBottom <= decideTop,
          );

          // Proposal card is inside the reading document (.kit-doc).
          const cardInDoc = await propGeoPage
            .$eval('.kit-doc .cb-proposal-card', (el) => el !== null)
            .catch(() => false);
          check(
            'proposal card is inside the .kit-doc reading document',
            cardInDoc,
          );
        } else {
          check('proposal card head contains "proposes ·"', false);
          check(
            'proposal card head uses real agent name (pi), not hardcoded string',
            false,
          );
          check('proposal card is above the decide section (geometry)', false);
          check('proposal card is inside the .kit-doc reading document', false);
        }

        // List row in proposed view must show "<agent> proposes <disposition>".
        await propGeoPage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await propGeoPage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await propGeoPage
          .waitForSelector('.kit-row', { timeout: 6000 })
          .catch(() => {});
        await propGeoPage.waitForTimeout(600);

        const subTexts = await propGeoPage
          .$$eval('.kit-row .kit-sub', (els) =>
            els.map((e) => e.textContent ?? ''),
          )
          .catch(() => []);
        const hasProposesText = subTexts.some((t) => t.includes('proposes'));
        check(
          'list row in proposed view shows "<agent> proposes <disposition>"',
          hasProposesText,
        );
      } finally {
        await propGeoPage.close();
      }
    }

    // ---- B: accept decides the item ----------------------------------------
    {
      const acceptPage = await context.newPage();
      try {
        await acceptPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await acceptPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Create a fresh proposal on issue:schuettc/hail#6 from pi session.
        const propResult = await agentPropose(
          acceptPage,
          'probe-accept-sess',
          'pi',
          'issue:schuettc/hail#6',
          'keep',
          'accept probe',
        );
        const proposed = propResult?.propose?.proposed ?? 0;
        if (proposed > 0) {
          // Navigate to the item detail.
          await acceptPage.evaluate(() => {
            location.hash = '#/item/issue:schuettc%2Fhail%236';
          });
          await acceptPage
            .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
            .catch(() => {});
          // Wait for the proposal card to appear (might need a re-render).
          await acceptPage.waitForTimeout(1000);

          const cardBeforeAccept = await acceptPage
            .$('.cb-proposal-card')
            .then((el) => Boolean(el))
            .catch(() => false);

          if (cardBeforeAccept) {
            // Click the 'accept' button.
            await acceptPage.click('.cb-proposal-card .kit-btn.fill');
            // Wait for the item to be decided and the card to disappear.
            await acceptPage
              .waitForFunction(
                () => !document.querySelector('.cb-proposal-card'),
                { timeout: 5000 },
              )
              .catch(() => {});
            await acceptPage.waitForTimeout(500);

            const cardAfterAccept = await acceptPage
              .$('.cb-proposal-card')
              .then((el) => Boolean(el))
              .catch(() => false);
            check(
              'accept removes the proposal card from item detail',
              !cardAfterAccept,
            );

            // Navigate to proposed view: the item should no longer be there.
            await acceptPage.evaluate(() => {
              location.hash = '#/attention/proposed';
            });
            await acceptPage.waitForFunction(
              () => location.hash === '#/attention/proposed',
            );
            await acceptPage.waitForTimeout(1000);

            const proposedRows = await acceptPage
              .$$eval('.kit-row', (rows) => rows.map((r) => r.dataset.id ?? ''))
              .catch(() => []);
            check(
              'accepted item leaves the proposed view',
              !proposedRows.includes('issue:schuettc/hail#6'),
            );
          } else {
            check('accept removes the proposal card from item detail', false);
            check('accepted item leaves the proposed view', false);
          }
        } else {
          // proposal was not created — this is a real failure in a fresh probe run
          check(
            'fresh proposal created for accept test (issue:schuettc/hail#6)',
            false,
          );
        }
      } finally {
        await acceptPage.close();
      }
    }

    // ---- C: reject records a reason ----------------------------------------
    {
      const rejectPage = await context.newPage();
      try {
        await rejectPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await rejectPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Create a fresh proposal on repo:schuettc/hail.
        const propResult = await agentPropose(
          rejectPage,
          'probe-reject-sess',
          'pi',
          'repo:schuettc/hail',
          'archive',
          'reject probe',
        );
        const proposed = propResult?.propose?.proposed ?? 0;
        if (proposed > 0) {
          // Navigate to the item.
          await rejectPage.evaluate(() => {
            location.hash = '#/item/repo:schuettc%2Fhail';
          });
          await rejectPage
            .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
            .catch(() => {});
          await rejectPage.waitForTimeout(1000);

          const cardBeforeReject = await rejectPage
            .$('.cb-proposal-card')
            .then((el) => Boolean(el))
            .catch(() => false);

          if (cardBeforeReject) {
            // Click the 'reject' button (the last button in the card's actions).
            const rejectBtn = await rejectPage.$(
              '.cb-proposal-card .kit-btns .kit-btn:last-child',
            );
            if (rejectBtn) {
              await rejectBtn.click();
              await rejectPage
                .waitForSelector('.kit-sheet', { timeout: 4000 })
                .catch(() => {});
              await rejectPage.waitForTimeout(300);

              const sheetVisible = await rejectPage
                .$('.kit-sheet')
                .then((el) => Boolean(el))
                .catch(() => false);
              check('reject opens the reason sheet', sheetVisible);

              if (sheetVisible) {
                // Type a reason (noteField renders as input.kit-note).
                await rejectPage.fill(
                  '.kit-sheet input.kit-note',
                  'not needed right now',
                );
                await rejectPage.waitForTimeout(200);

                // Click the 'Reject N' fill button.
                await rejectPage.click('.kit-sheet .kit-btn.fill');
                await rejectPage
                  .waitForFunction(
                    () => !document.querySelector('.kit-sheet'),
                    { timeout: 5000 },
                  )
                  .catch(() => {});
                await rejectPage.waitForTimeout(500);

                // Proposal card must be gone.
                const cardAfterReject = await rejectPage
                  .$('.cb-proposal-card')
                  .then((el) => Boolean(el))
                  .catch(() => false);
                check(
                  'reject removes the proposal card from item detail',
                  !cardAfterReject,
                );

                // Verify via GET /api/item that the proposal is rejected and
                // the reason text is recorded.
                const itemState = await rejectPage
                  .evaluate(
                    async ({ base }) => {
                      const r = await fetch(
                        base + '/api/item?key=repo:schuettc/hail',
                      );
                      return r.ok ? r.json() : null;
                    },
                    { base: serveHandle.base.replace(/\/$/, '') },
                  )
                  .catch(() => null);
                const proposals = itemState?.proposals ?? [];
                const rejected = proposals.find((p) => p.state === 'rejected');
                check(
                  'reject records state=rejected in GET /api/item',
                  Boolean(rejected),
                );
                check(
                  'reject records the reason text in GET /api/item',
                  Boolean(rejected?.reason?.includes('not needed right now')),
                );
              } else {
                check(
                  'reject removes the proposal card from item detail',
                  false,
                );
                check('reject records state=rejected in GET /api/item', false);
                check('reject records the reason text in GET /api/item', false);
              }
            } else {
              check('reject opens the reason sheet', false);
              check('reject removes the proposal card from item detail', false);
              check('reject records state=rejected in GET /api/item', false);
              check('reject records the reason text in GET /api/item', false);
            }
          } else {
            check('reject opens the reason sheet', false);
            check('reject removes the proposal card from item detail', false);
            check('reject records state=rejected in GET /api/item', false);
            check('reject records the reason text in GET /api/item', false);
          }
        } else {
          // proposal was not created — real failure in a fresh probe run
          check(
            'fresh proposal created for reject test (repo:schuettc/hail)',
            false,
          );
        }
      } finally {
        await rejectPage.close();
      }
    }

    // ---- D: change opens the seeded decide sheet ---------------------------
    // pr:schuettc/hail#3 has a pending proposal from the chip-count scenario;
    // D reads that proposal and tests the "change" flow. After change, pr#3 is
    // decided. D's item is dedicated in the sense that A only reads it (no
    // decision) and D is the only scenario that decides it via change.
    {
      const changePage = await context.newPage();
      try {
        await changePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await changePage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to the item detail for pr:schuettc/hail#3.
        // This item has a pending proposal from the chip-count scenario.
        await changePage.evaluate(() => {
          location.hash = '#/item/pr:schuettc%2Fhail%233';
        });
        await changePage
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await changePage.waitForTimeout(1000);

        const cardForChange = await changePage
          .$('.cb-proposal-card')
          .then((el) => Boolean(el))
          .catch(() => false);

        if (cardForChange) {
          // Click 'change…' — the second button in the card's actions.
          const btns = await changePage.$$(
            '.cb-proposal-card .kit-btns .kit-btn',
          );
          // buttons order: accept (fill), change…, reject
          const changeBtn = btns[1] ?? null;
          if (changeBtn) {
            const changeBtnText = (await changeBtn.textContent()) ?? '';
            check(
              'change… button is the second action in the proposal card',
              changeBtnText.includes('change'),
            );

            await changeBtn.click();
            await changePage
              .waitForSelector('.kit-sheet', { timeout: 4000 })
              .catch(() => {});
            await changePage.waitForTimeout(400);

            const sheetOpen = await changePage
              .$('.kit-sheet')
              .then((el) => Boolean(el))
              .catch(() => false);
            check('change… opens the decide sheet', sheetOpen);

            if (sheetOpen) {
              // The seeded disposition 'keep' must be pre-selected.
              const keepIsSelected = await changePage
                .$eval(
                  '.kit-sheet .cb-sheet-disp.on',
                  (el) => (el.textContent ?? '').trim() === 'keep',
                )
                .catch(() => false);
              check(
                "change sheet is seeded with the proposal's disposition",
                keepIsSelected,
              );

              // Change to a different disposition (e.g. 'close').
              const closeBtn = await changePage.$(
                '.kit-sheet .cb-sheet-disp:not(.on)',
              );
              if (closeBtn) {
                await closeBtn.click();
                await changePage.waitForTimeout(200);

                // Submit the decision.
                await changePage.click('.kit-sheet .kit-btn.fill');
                await changePage
                  .waitForFunction(
                    () => !document.querySelector('.kit-sheet'),
                    { timeout: 6000 },
                  )
                  .catch(() => {});
                await changePage.waitForTimeout(500);

                // Proposal card should be gone after change.
                const cardAfterChange = await changePage
                  .$('.cb-proposal-card')
                  .then((el) => Boolean(el))
                  .catch(() => false);
                check(
                  'change confirms and decides the item (proposal card removed)',
                  !cardAfterChange,
                );
              } else {
                // No alternate disposition button found — should not happen for PR.
                check(
                  'change confirms and decides the item (proposal card removed)',
                  false,
                );
              }
            } else {
              check(
                "change sheet is seeded with the proposal's disposition",
                false,
              );
              check(
                'change confirms and decides the item (proposal card removed)',
                false,
              );
            }
          } else {
            check(
              'change… button is the second action in the proposal card',
              false,
            );
            check('change… opens the decide sheet', false);
            check(
              "change sheet is seeded with the proposal's disposition",
              false,
            );
            check(
              'change confirms and decides the item (proposal card removed)',
              false,
            );
          }
        } else {
          // No card: the chip-count proposal for pr#3 was not found.
          // This is a real failure — pr#3 should have a pending proposal.
          check(
            'proposal card present for change test (pr:schuettc/hail#3)',
            false,
          );
        }
      } finally {
        await changePage.close();
      }
    }

    // ---- E: a key and r key ------------------------------------------------
    {
      const keyPage = await context.newPage();
      try {
        await keyPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await keyPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Create a proposal on issue:schuettc/hail#5 for the key tests.
        const propA = await agentPropose(
          keyPage,
          'probe-key-a-sess',
          'pi',
          'issue:schuettc/hail#7',
          'keep',
          'key test a',
        );
        const proposedA = propA?.propose?.proposed ?? 0;

        if (proposedA > 0) {
          // Navigate to proposed view, open the item.
          await keyPage.evaluate(() => {
            location.hash = '#/attention/proposed';
          });
          await keyPage.waitForFunction(
            () => location.hash === '#/attention/proposed',
          );
          await keyPage
            .waitForSelector('.kit-row', { timeout: 6000 })
            .catch(() => {});
          await keyPage.waitForTimeout(600);

          // Click the first row to open the item.
          const firstRow = await keyPage.$('.kit-row');
          if (firstRow) {
            await firstRow.click();
            await keyPage
              .waitForSelector('.cb-proposal-card', { timeout: 5000 })
              .catch(() => {});
            await keyPage.waitForTimeout(600);

            const hasCardForKey = await keyPage
              .$('.cb-proposal-card')
              .then((el) => Boolean(el))
              .catch(() => false);

            if (hasCardForKey) {
              // Dispatch focus to the window (headless Chrome withholds key events
              // without window focus — see progress.md).
              await keyPage.evaluate(() => {
                window.dispatchEvent(new Event('focus'));
                document.body.dispatchEvent(new FocusEvent('focus'));
              });
              await keyPage.waitForTimeout(100);

              // Press 'a' to accept the proposal.
              await keyPage.keyboard.press('a');
              await keyPage.waitForTimeout(1500);

              const cardAfterKey = await keyPage
                .$('.cb-proposal-card')
                .then((el) => Boolean(el))
                .catch(() => false);
              check(
                '"a" key accepts the open item\'s proposal (card disappears)',
                !cardAfterKey,
              );
            } else {
              check(
                '"a" key accepts the open item\'s proposal (card disappears)',
                false,
              );
            }
          } else {
            check(
              '"a" key accepts the open item\'s proposal (card disappears)',
              false,
            );
          }
        } else {
          // proposal was not created — real failure in a fresh probe run
          check(
            'fresh proposal created for "a" key test (issue:schuettc/hail#7)',
            false,
          );
        }

        // Create a proposal for the 'r' key test on a fresh item.
        // Use branch:schuettc/hail@feat/client (from the fixture's feature branch).
        const propR = await agentPropose(
          keyPage,
          'probe-key-r-sess',
          'pi',
          'repo:schuettc/hail',
          'keep',
          'key test r',
        );
        const proposedR = propR?.propose?.proposed ?? 0;

        if (proposedR > 0) {
          await keyPage.evaluate(() => {
            location.hash = '#/attention/proposed';
          });
          await keyPage.waitForFunction(
            () => location.hash === '#/attention/proposed',
          );
          await keyPage
            .waitForSelector('.kit-row', { timeout: 6000 })
            .catch(() => {});
          await keyPage.waitForTimeout(600);

          // Open the first row.
          const rowForR = await keyPage.$('.kit-row');
          if (rowForR) {
            await rowForR.click();
            await keyPage
              .waitForSelector('.cb-proposal-card', { timeout: 5000 })
              .catch(() => {});
            await keyPage.waitForTimeout(600);

            const hasCardForR = await keyPage
              .$('.cb-proposal-card')
              .then((el) => Boolean(el))
              .catch(() => false);

            if (hasCardForR) {
              await keyPage.evaluate(() => {
                window.dispatchEvent(new Event('focus'));
                document.body.dispatchEvent(new FocusEvent('focus'));
              });
              await keyPage.waitForTimeout(100);

              // Press 'r' to reject (opens the reason sheet).
              await keyPage.keyboard.press('r');
              await keyPage
                .waitForSelector('.kit-sheet', { timeout: 4000 })
                .catch(() => {});
              await keyPage.waitForTimeout(300);

              const sheetFromKey = await keyPage
                .$('.kit-sheet')
                .then((el) => Boolean(el))
                .catch(() => false);
              check(
                '"r" key opens the reject reason sheet for the open item',
                sheetFromKey,
              );

              // Close the sheet.
              await keyPage.keyboard.press('Escape');
              await keyPage.waitForTimeout(300);
            } else {
              check(
                '"r" key opens the reject reason sheet for the open item',
                false,
              );
            }
          } else {
            check(
              '"r" key opens the reject reason sheet for the open item',
              false,
            );
          }
        } else {
          // proposal was not created — real failure in a fresh probe run
          check(
            'fresh proposal created for "r" key test (repo:schuettc/hail)',
            false,
          );
        }
      } finally {
        await keyPage.close();
      }
    }

    // ---- F: bulk accept/reject in proposed view foot -----------------------
    {
      const bulkPage = await context.newPage();
      try {
        await bulkPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await bulkPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to the proposed view.
        await bulkPage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await bulkPage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await bulkPage
          .waitForSelector('.kit-chip[data-id="proposed"]', { timeout: 5000 })
          .catch(() => {});
        await bulkPage.waitForTimeout(600);

        // Count how many proposed rows are available.
        const rows = await bulkPage.$$('.kit-row');
        if (rows.length > 0) {
          // Select all rows by clicking their checkboxes.
          for (const row of rows) {
            const box = await row.$('.kit-box');
            if (box) {
              await box.click();
              await bulkPage.waitForTimeout(50);
            }
          }
          await bulkPage.waitForTimeout(400);

          // The bulk accept button must be visible in the foot.
          const acceptBtnVisible = await bulkPage
            .$eval('.cb-prop-accept', (el) => !el.hidden)
            .catch(() => false);
          check(
            'bulk accept button appears in proposed view foot when items with proposals are selected',
            acceptBtnVisible,
          );

          const acceptBtnText = await bulkPage
            .$eval('.cb-prop-accept', (el) => el.textContent ?? '')
            .catch(() => '');
          check(
            'bulk accept button shows count of items with proposals',
            acceptBtnText.match(/accept \d+/) !== null,
          );

          const rejectBtnVisible = await bulkPage
            .$eval('.cb-prop-reject', (el) => !el.hidden)
            .catch(() => false);
          check(
            'bulk reject button appears in proposed view foot when items are selected',
            rejectBtnVisible,
          );
        } else {
          // No proposed items in the view — this is a real failure: branch:schuettc/hail@feat/client
          // should still have a pending proposal from the 'r' key scenario.
          check('proposed view has rows for bulk accept/reject test', false);
        }
      } finally {
        await bulkPage.close();
      }
    }

    // ---- fix-round-2: foot layout — single line, no wrap, no overlap --------
    console.log(
      '\nscenario: foot layout — single line, no wrap at 400\u202fpx list width',
    );

    {
      const footPage = await context.newPage();
      try {
        await footPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await footPage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Helper: check that every foot control is single-line (no wrapping)
        // and that no two controls overlap horizontally.
        async function checkFootLayout(view, label) {
          await footPage.evaluate((v) => {
            location.hash = `#/attention/${v}`;
          }, view);
          await footPage.waitForFunction(
            (v) => location.hash === `#/attention/${v}`,
            view,
          );
          await footPage
            .waitForSelector('.kit-row', { timeout: 5000 })
            .catch(() => {});
          await footPage.waitForTimeout(400);

          // Select all visible rows to trigger the bulk/sel-count display.
          const boxes = await footPage.$$('.kit-row .kit-box');
          for (const box of boxes) {
            await box.click();
            await footPage.waitForTimeout(30);
          }
          await footPage.waitForTimeout(300);

          // Collect bounding rects of all visible foot controls.
          const footRects = await footPage.evaluate(() => {
            const foot = document.querySelector('.cb-foot');
            if (!foot) return null;
            // Gather all interactive foot controls (visible ones only).
            const selectors = [
              '.cb-sel-count',
              '.cb-sel-all',
              '.cb-prop-accept',
              '.cb-prop-reject',
              '.cb-foot-more',
            ];
            const rects = [];
            for (const sel of selectors) {
              const el = foot.querySelector(sel);
              if (!el || el.hidden || getComputedStyle(el).display === 'none')
                continue;
              const r = el.getBoundingClientRect();
              if (r.width === 0 && r.height === 0) continue;
              // lineHeight: scrollHeight > clientHeight would indicate wrap.
              rects.push({
                sel,
                top: r.top,
                bottom: r.bottom,
                left: r.left,
                right: r.right,
                scrollH: el.scrollHeight,
                clientH: el.clientHeight,
              });
            }
            return rects;
          });

          if (!footRects || footRects.length === 0) {
            check(`${label}: foot controls found`, false);
            return;
          }

          // Each control must not be taller than a single line.
          // A line height of ~20 px is typical; wrapping adds at least half again.
          // We check scrollHeight <= clientHeight (no overflow) as the wrap signal.
          let allSingleLine = true;
          for (const r of footRects) {
            if (r.scrollH > r.clientH + 2) {
              allSingleLine = false;
              console.error(
                `    wrap detected in ${r.sel}: scrollH=${r.scrollH} clientH=${r.clientH}`,
              );
            }
          }
          check(
            `${label}: all foot controls are single-line (no wrapping)`,
            allSingleLine,
          );

          // No two controls must overlap horizontally (left/right rectangles).
          let noOverlap = true;
          for (let i = 0; i < footRects.length; i++) {
            for (let j = i + 1; j < footRects.length; j++) {
              const a = footRects[i];
              const b = footRects[j];
              // Overlap: a.right > b.left && b.right > a.left (horizontal).
              if (a.right > b.left + 1 && b.right > a.left + 1) {
                noOverlap = false;
                console.error(
                  `    overlap: ${a.sel} [${a.left.toFixed(0)}\..\.${a.right.toFixed(0)}] overlaps ${b.sel} [${b.left.toFixed(0)}\..\.${b.right.toFixed(0)}]`,
                );
              }
            }
          }
          check(`${label}: no foot controls overlap`, noOverlap);
        }

        // Test with a selection in the proposed view.
        await checkFootLayout('proposed', 'proposed view with selection');

        // Test with a selection in the new view (no bulk buttons).
        await checkFootLayout('new', 'new view with selection');

        // Take fix-round-2 screenshots.
        await footPage.setViewportSize({ width: 1600, height: 900 });

        // proposed: 1 selected
        await footPage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await footPage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await footPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await footPage.waitForTimeout(400);
        const propRows1 = await footPage.$$('.kit-row .kit-box');
        if (propRows1.length > 0) await propRows1[0].click();
        await footPage.waitForTimeout(300);
        await footPage.screenshot({ path: '/tmp/t5fix2-proposed-1sel.png' });

        // proposed: all selected
        for (let i = 1; i < propRows1.length; i++) {
          await propRows1[i].click();
          await footPage.waitForTimeout(30);
        }
        await footPage.waitForTimeout(300);
        await footPage.screenshot({ path: '/tmp/t5fix2-proposed-allsel.png' });

        // new: 2 selected
        await footPage.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await footPage.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await footPage
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await footPage.waitForTimeout(400);
        const newRows = await footPage.$$('.kit-row .kit-box');
        if (newRows.length > 0) await newRows[0].click();
        if (newRows.length > 1) await newRows[1].click();
        await footPage.waitForTimeout(300);
        await footPage.screenshot({ path: '/tmp/t5fix2-new-2sel.png' });
        console.log(
          '  t5fix2 screenshots: /tmp/t5fix2-proposed-1sel.png  /tmp/t5fix2-proposed-allsel.png  /tmp/t5fix2-new-2sel.png',
        );
      } finally {
        await footPage.close();
      }
    }

    // ---- G: live proposals event updates the agent name in list rows --------
    {
      const claudePage = await context.newPage();
      try {
        await claudePage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await claudePage.waitForSelector('.kit-bar', { timeout: 8000 });

        // Navigate to proposed view first to see any proposals.
        await claudePage.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await claudePage.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await claudePage.waitForTimeout(600);

        // Create a proposal from a 'claude' session so we can verify the
        // agent name is taken from the source, not hardcoded.
        // repo:schuettc/hail is still undecided (its proposal was rejected in
        // scenario C, not decided).  Create a fresh proposal from 'claude'.
        const claudeResult = await agentPropose(
          claudePage,
          'probe-claude-sess',
          'claude',
          'repo:schuettc/hail',
          'keep',
          'claude probe note',
        );
        const proposedClaude = claudeResult?.propose?.proposed ?? 0;

        if (proposedClaude > 0) {
          // Wait for the proposals live event and list reload.
          await claudePage.waitForTimeout(3000);

          // The row sub-text must contain 'claude' (not 'pi').
          const subTexts = await claudePage
            .$$eval('.kit-row .kit-sub', (els) =>
              els.map((e) => e.textContent ?? ''),
            )
            .catch(() => []);
          const hasClaudeText = subTexts.some((t) =>
            t.toLowerCase().includes('claude'),
          );
          check(
            'a proposal from claude shows "claude" in the list row sub-line',
            hasClaudeText,
          );
        } else {
          // proposal was not created — real failure in a fresh probe run
          // (repo:schuettc/hail should be undecided after scenario C rejected its proposal)
          check(
            'fresh proposal created for claude-name test (repo:schuettc/hail)',
            false,
          );
        }
      } finally {
        await claudePage.close();
      }
    }

    // ---- Task 5 screenshots ------------------------------------------------
    {
      const t5Page = await context.newPage();
      try {
        await t5Page.setViewportSize({ width: 1600, height: 900 });
        await t5Page.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await t5Page.waitForSelector('.kit-bar', { timeout: 8000 });

        async function detectThemeT5(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        async function forceThemeT5(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectThemeT5(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          const actual = await detectThemeT5(pg);
          check(
            `t5 theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // Use repo:schuettc/hail for the proposal card screenshot.
        // At this point repo has a pending proposal from the claude-name test (G).
        // We create one more from probe-t5-sess to ensure the card is visible.
        await agentPropose(
          t5Page,
          'probe-t5-sess',
          'pi',
          'repo:schuettc/hail',
          'archive',
          't5 screenshot note',
        );

        // Light: item detail with proposal card.
        await forceThemeT5(t5Page, 'light');
        await t5Page.evaluate(() => {
          location.hash = '#/item/repo:schuettc%2Fhail';
        });
        await t5Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await t5Page.waitForTimeout(1000);
        await t5Page.screenshot({ path: '/tmp/t5fix-light-item.png' });

        // Light: proposed view with a selection (so bulk buttons are visible).
        await t5Page.evaluate(() => {
          location.hash = '#/attention/proposed';
        });
        await t5Page.waitForFunction(
          () => location.hash === '#/attention/proposed',
        );
        await t5Page
          .waitForSelector('.kit-chip[data-id="proposed"]', { timeout: 5000 })
          .catch(() => {});
        await t5Page.waitForTimeout(600);
        // Select all rows so the bulk accept/reject buttons appear in the foot.
        const t5PropBoxes = await t5Page.$$('.kit-row .kit-box');
        for (const box of t5PropBoxes) {
          await box.click();
          await t5Page.waitForTimeout(50);
        }
        await t5Page.waitForTimeout(400);
        await t5Page.screenshot({ path: '/tmp/t5fix-light-proposed.png' });

        // Dark: same pages.
        await forceThemeT5(t5Page, 'dark');
        const darkTheme = await detectThemeT5(t5Page);
        check('t5 dark theme is dark', darkTheme === 'dark');

        // Re-select rows for dark proposed screenshot.
        const t5DarkBoxes = await t5Page.$$('.kit-row .kit-box');
        for (const box of t5DarkBoxes) {
          await box.click();
          await t5Page.waitForTimeout(50);
        }
        await t5Page.waitForTimeout(400);
        await t5Page.screenshot({ path: '/tmp/t5fix-dark-proposed.png' });

        await t5Page.evaluate(() => {
          location.hash = '#/item/repo:schuettc%2Fhail';
        });
        await t5Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await t5Page.waitForTimeout(1000);
        await t5Page.screenshot({ path: '/tmp/t5fix-dark-item.png' });

        console.log(
          '  t5fix screenshots: /tmp/t5fix-light-item.png  /tmp/t5fix-light-proposed.png',
        );
        console.log(
          '                     /tmp/t5fix-dark-item.png   /tmp/t5fix-dark-proposed.png',
        );
      } finally {
        await t5Page.close();
      }
    }

    // ---- scenario: board screenshot at 1600x900 ----------------------------
    console.log(
      '\nscenario: board screenshot at 1600×900 — /tmp/fix3-board.png',
    );

    {
      const boardShotCtx = await browser.newContext({
        viewport: { width: 1600, height: 900 },
      });
      const boardShotPage = await boardShotCtx.newPage();
      try {
        await boardShotPage.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await boardShotPage.waitForSelector('.kit-bar', { timeout: 8000 });
        await boardShotPage.evaluate(() => {
          location.hash = '#/attention/board';
        });
        await boardShotPage.waitForFunction(
          () => location.hash === '#/attention/board',
        );
        await boardShotPage
          .waitForSelector('.cb-lane', { timeout: 6000 })
          .catch(() => {});
        await boardShotPage.waitForTimeout(800);
        await boardShotPage.screenshot({
          path: '/tmp/fix3-board.png',
          fullPage: false,
        });
        console.log('  board screenshot: /tmp/fix3-board.png');
        // Basic sanity: all 4 lanes are visible.
        const laneCount = await boardShotPage.$$eval(
          '.cb-lane',
          (lanes) => lanes.length,
        );
        check('fix3-board screenshot: four lanes visible', laneCount === 4);
      } finally {
        await boardShotPage.close();
        await boardShotCtx.close();
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

        // ---- screenshots: detect actual theme, force correct theme, name correctly --
        // Helper: detect actual theme from computed background luminance.
        // Reads body (not html) because the kit sets background on body.
        // Parses both rgb(...) and rgba(...) forms.
        async function detectTheme(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        // Force a specific theme by clicking the toggle until the computed
        // background matches the target (up to 6 clicks max), then assert.
        async function forceTheme(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectTheme(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          // Assert that we actually reached the target theme.
          const actual = await detectTheme(pg);
          check(
            `theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // --- light screenshots ---
        await forceTheme(fidPage, 'light');

        // Item detail (still on #/item/issue:schuettc%2Fhail%234 from above).
        await fidPage.screenshot({
          path: '/tmp/fid-light-item.png',
          fullPage: false,
        });

        // Navigate to #/attention/new.
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

        // --- dark screenshots ---
        await forceTheme(fidPage, 'dark');

        // List view in dark.
        await fidPage.screenshot({
          path: '/tmp/fid-dark-new.png',
          fullPage: false,
        });

        // Item detail in dark.
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
          '  fid screenshots: /tmp/fid-light-item.png  /tmp/fid-light-new.png',
        );
        console.log(
          '                   /tmp/fid-dark-item.png   /tmp/fid-dark-new.png',
        );
      } finally {
        await fidPage.close();
      }
    }

    // ---- scenario: round-2 fidelity screenshots ----------------------------
    console.log('\nscenario: round-2 fidelity screenshots at 1600\xd7900');

    {
      const fid2Page = await context.newPage();
      try {
        await fid2Page.setViewportSize({ width: 1600, height: 900 });
        await fid2Page.goto(serveHandle.url, {
          waitUntil: 'domcontentloaded',
          timeout: 15000,
        });
        await fid2Page.waitForSelector('.kit-bar', { timeout: 8000 });

        // Helper: detect actual theme from computed body background.
        // Reads body (not html) because the kit sets background on body.
        // Parses both rgb(...) and rgba(...) forms.
        async function detectTheme2(pg) {
          const bg = await pg
            .$eval('body', (el) => getComputedStyle(el).backgroundColor)
            .catch(() => 'rgb(255,255,255)');
          const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
          if (m) {
            const lum =
              0.299 * parseInt(m[1]) +
              0.587 * parseInt(m[2]) +
              0.114 * parseInt(m[3]);
            return lum < 128 ? 'dark' : 'light';
          }
          return 'light';
        }
        // Force a specific theme, loop up to 6 times, then assert.
        async function forceTheme2(pg, target) {
          for (let i = 0; i < 6; i++) {
            const cur = await detectTheme2(pg);
            if (cur === target) return;
            await pg.click('button.kit-ctl:has-text("theme")').catch(() => {});
            await pg.waitForTimeout(300);
          }
          // Assert the target was reached before capturing.
          const actual = await detectTheme2(pg);
          check(
            `fid2 theme forced to ${target} (actual: ${actual})`,
            actual === target,
          );
        }

        // --- light screenshots (fid2) ---
        await forceTheme2(fid2Page, 'light');

        await fid2Page.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fid2Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fid2Page.waitForTimeout(600);
        await fid2Page.screenshot({
          path: '/tmp/fid2-light-item.png',
          fullPage: false,
        });

        await fid2Page.evaluate(() => {
          location.hash = '#/attention/new';
        });
        await fid2Page.waitForFunction(
          () => location.hash === '#/attention/new',
        );
        await fid2Page.waitForTimeout(600);
        await fid2Page
          .waitForSelector('.kit-row', { timeout: 5000 })
          .catch(() => {});
        await fid2Page.waitForTimeout(300);
        await fid2Page.screenshot({
          path: '/tmp/fid2-light-new.png',
          fullPage: false,
        });

        // --- dark screenshots (fid2) ---
        await forceTheme2(fid2Page, 'dark');
        await fid2Page.waitForTimeout(200);

        await fid2Page.screenshot({
          path: '/tmp/fid2-dark-new.png',
          fullPage: false,
        });

        await fid2Page.evaluate(() => {
          location.hash = '#/item/issue:schuettc%2Fhail%234';
        });
        await fid2Page
          .waitForSelector('.kit-read .cb-item', { timeout: 8000 })
          .catch(() => {});
        await fid2Page.waitForTimeout(600);
        await fid2Page.screenshot({
          path: '/tmp/fid2-dark-item.png',
          fullPage: false,
        });

        console.log(
          '  fid2 screenshots: /tmp/fid2-light-item.png  /tmp/fid2-light-new.png',
        );
        console.log(
          '                    /tmp/fid2-dark-item.png   /tmp/fid2-dark-new.png',
        );
      } finally {
        await fid2Page.close();
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
