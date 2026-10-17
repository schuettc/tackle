// probe-filters.mjs — saved filters on the Attention list (casebook 0.7.0),
// run by probe.mjs with its check helpers (t.check, t.checkList, t.until,
// t.eventually).
//
// Each scenario has a serve of its own (serve.mjs: --no-open,
// CASEBOOK_NO_BROWSER=1, the hermetic git guard, the fake gh).
//
//   saved filters: the five examples as chips under the views; selecting
//     lists serve's AND of them over every status, in the address, across
//     a reload; the not-recommended group asks nothing while filtered; the
//     switch and a view chip; + filter opens the editor in the middle pane,
//     the list beside it its faded live preview (its first row doesn't
//     move), the field chips shown at once (conditions grow below them;
//     a used field or one the type lacks greyed), a type condition with
//     nothing ticked, last activity as 3 weeks or before a date, a
//     repository picked from its list or as owner/* (each matching serve's
//     GET /api/items?match=), cancel puts the list back, save selects the
//     new filter; edit and delete;
//     a filter deleted elsewhere is named, the list empty.
//   a filter that does not validate: marked with serve's reason, not
//     selectable, editable into a valid one.

import { startServe } from './serve.mjs';
import { createAgent } from './agent.mjs';

let serves = [];
/** stopFiltersServes stops every serve these scenarios started (probe.mjs cleanup). */
export function stopFiltersServes() {
  for (const s of serves) s.stop();
  serves = [];
}

const LIST = '.kit-app > .kit-list:not([hidden])';
const REPO = 'filt';
const FISSUE = (n) => `issue:schuettc/${REPO}#${n}`;
const IDS = [
  'dormant-repos',
  'incoming',
  'outgoing-prs',
  'quiet-14d',
  'unpushed',
];

// The seed: issues by others, four quiet for weeks and one updated today
// (so "Not updated in 14 days" leaves it out).
function seed() {
  const today = new Date().toISOString();
  const issue = (number, title, at) => ({
    repo: `schuettc/${REPO}`,
    number,
    title,
    author: 'alice',
    state: 'OPEN',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: at,
  });
  // Several repos under one owner, and one name under two owners: the
  // repository field's suggestions and patterns.
  const bare = (repo) => ({
    repo,
    pushed_at: '2026-09-20T00:00:00Z',
    default_branch: 'main',
    prs: [],
    issues: [],
  });
  return {
    seedRepos: [
      bare(`schuettc/${REPO}-a`),
      bare(`schuettc/${REPO}-b`),
      bare(`other/${REPO}`),
      {
        repo: `schuettc/${REPO}`,
        pushed_at: '2026-09-20T00:00:00Z',
        default_branch: 'main',
        prs: [],
        issues: [
          issue(1, 'quiet one', '2026-08-02T00:00:00Z'),
          issue(2, 'quiet two', '2026-08-03T00:00:00Z'),
          issue(3, 'quiet three', '2026-08-04T00:00:00Z'),
          issue(4, 'quiet four', '2026-08-05T00:00:00Z'),
          issue(5, 'busy today', today),
        ],
      },
    ],
  };
}

const BAD =
  'id = "bad"\nname = "Bad"\n[[match]]\nfield = "colour"\nop = "is"\nvalue = "red"\n';

export async function filtersScenarios(shared, t) {
  for (const [name, scenario, opts] of [
    ['saved filters', savedFilters, seed()],
    [
      'a filter that does not validate',
      invalidFilter,
      { seedFiles: { 'filters/bad.toml': BAD } },
    ],
  ]) {
    console.log(`\nscenario: ${name}`);
    const context = await shared.browser().newContext({
      viewport: { width: 1600, height: 1000 },
    });
    const s = await startServe(opts);
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

// rowKeys is the list's rows top to bottom, as item keys.
function rowKeys(pg) {
  return pg.$$eval(`${LIST} .kit-rows > .kit-row .kit-kicker`, (els) =>
    els.map((e) => (e.textContent ?? '').replace(' \u00b7 ', ':')),
  );
}

// chipState is the filter chips as "id:name:on".
function chipState(pg) {
  return pg.$$eval('.cb-filters .cb-filter', (els) =>
    els.map(
      (e) =>
        `${e.getAttribute('data-id')}:${e.textContent}:${e.getAttribute('aria-pressed')}`,
    ),
  );
}

async function savedFilters(context, t, s) {
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const A = 'probe-filters-a';
  const beat = () =>
    agent.presence(A, 'pi \u00b7 filters', '/home/court/filters', 'pi');
  await beat();
  const beating = setInterval(() => void beat().catch(() => {}), 1000);
  try {
    // An item decided Not now beforehand: a filter lists it all the same.
    await api('POST', '/api/decide', {
      keys: [FISSUE(2)],
      disposition: 'wait',
      until: 'date(2030-01-01)',
    });
    const served = async (q) =>
      ((await api('GET', `/api/items?${q}&limit=500`)).items ?? []).map(
        (it) => it.key,
      );
    const rows = await api('GET', '/api/filters');
    const names = Object.fromEntries(
      (rows.filters ?? []).map((r) => [r.filter.id, r.filter.name]),
    );

    const pg = await context.newPage();
    await pg.goto(
      `${s.url}${s.url.includes('?') ? '&' : '?'}session=${A}#/attention/due`,
      {
        waitUntil: 'domcontentloaded',
      },
    );
    await pg.waitForSelector('.cb-filters .cb-filter', { timeout: 8000 });

    // 1: the chips, in id order, under the view chips.
    t.checkList(
      'saved filters: the five examples are chips, in id order, with their names, none on',
      await chipState(pg),
      IDS.map((id) => `${id}:${names[id]}:false`),
    );
    {
      const g = await pg.evaluate(() => {
        const v = document
          .querySelector(
            '.kit-app > .kit-list:not([hidden]) .kit-chips[data-group="view"]',
          )
          ?.getBoundingClientRect();
        const b = document
          .querySelector('.cb-filters')
          ?.getBoundingClientRect();
        return v && b ? { vb: v.bottom, bt: b.top, h: b.height } : null;
      });
      t.check(
        `the filter bar shows under the view chips (${JSON.stringify(g)})`,
        !!g && g.h > 0 && g.bt >= g.vb - 0.5,
      );
    }

    // 2: Incoming: the address, no view on, serve's rows.
    await pg.click('.cb-filter[data-id="incoming"]');
    const incoming = await served('filters=incoming');
    await t.until(
      pg,
      (want) =>
        location.hash === '#/attention?filters=incoming' &&
        JSON.stringify(
          [
            ...document.querySelectorAll(
              '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row .kit-kicker',
            ),
          ].map((e) => (e.textContent ?? '').replace(' \u00b7 ', ':')),
        ) === JSON.stringify(want),
      incoming,
      8000,
    );
    t.check(
      `Incoming: the address is #/attention?filters=incoming (${await pg.evaluate(() => location.hash)})`,
      (await pg.evaluate(() => location.hash)) ===
        '#/attention?filters=incoming',
    );
    t.check(
      'and no view chip is on',
      (await pg.$$(`${LIST} .kit-chips[data-group="view"] .kit-chip.on`))
        .length === 0,
    );
    t.checkList(
      "and the rows are serve's filters=incoming",
      await rowKeys(pg),
      incoming,
    );
    t.check(
      `and the item decided Not now is listed (${FISSUE(2)})`,
      (await rowKeys(pg)).includes(FISSUE(2)),
    );

    // 3: and Not updated in 14 days: the AND.
    await pg.click('.cb-filter[data-id="quiet-14d"]');
    const both = await served('filters=incoming,quiet-14d');
    await t.until(
      pg,
      (want) =>
        location.hash === '#/attention?filters=incoming,quiet-14d' &&
        JSON.stringify(
          [
            ...document.querySelectorAll(
              '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row .kit-kicker',
            ),
          ].map((e) => (e.textContent ?? '').replace(' \u00b7 ', ':')),
        ) === JSON.stringify(want),
      both,
      8000,
    );
    t.check(
      `+ Not updated in 14 days: the address is #/attention?filters=incoming,quiet-14d (${await pg.evaluate(() => location.hash)})`,
      (await pg.evaluate(() => location.hash)) ===
        '#/attention?filters=incoming,quiet-14d',
    );
    t.checkList("and the rows are serve's AND", await rowKeys(pg), both);
    t.check(
      `and today's issue is not listed (${FISSUE(5)})`,
      !(await rowKeys(pg)).includes(FISSUE(5)) && incoming.includes(FISSUE(5)),
    );

    // 4: a reload keeps it.
    await pg.reload({ waitUntil: 'domcontentloaded' });
    await pg.waitForSelector('.cb-filters .cb-filter', { timeout: 8000 });
    await t.until(
      pg,
      (n) =>
        document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row',
        ).length === n &&
        document.querySelectorAll('.cb-filter.on').length === 2,
      both.length,
      8000,
    );
    t.checkList(
      'after a reload, the same chips are on',
      await chipState(pg),
      IDS.map(
        (id) => `${id}:${names[id]}:${id === 'incoming' || id === 'quiet-14d'}`,
      ),
    );
    t.checkList('and the rows are the same', await rowKeys(pg), both);

    // 5: the not recommended ask nothing while filtered.
    const attached = await t.until(
      pg,
      () =>
        document
          .querySelector('.cb-dock-header')
          ?.getAttribute('data-attach') === 'here',
      undefined,
      4000,
    );
    if (!attached) {
      await pg.click(
        `.cb-dock-chooser .cb-dock-pick-item[data-session="${A}"]`,
      );
      await t.until(
        pg,
        () =>
          document
            .querySelector('.cb-dock-header')
            ?.getAttribute('data-attach') === 'here',
      );
    }
    await t.until(
      pg,
      () =>
        !!document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .cb-group[data-d=""] .cb-group-acts',
        ),
    );
    {
      const got = await pg.evaluate(() => ({
        here:
          document
            .querySelector('.cb-dock-header')
            ?.getAttribute('data-attach') ?? '',
        head: !!document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .cb-group[data-d=""]',
        ),
        ask: document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .cb-group-ask',
        ).length,
      }));
      t.check(
        `with a session attached, the filtered list's "Not recommended yet" offers no ask (${JSON.stringify(got)})`,
        got.here === 'here' && got.head && got.ask === 0,
      );
    }

    // 6: the switch: flat, in the address.
    await pg.click('.cb-group-switch');
    await t.until(
      pg,
      () =>
        location.hash === '#/attention?filters=incoming,quiet-14d&flat' &&
        document.querySelectorAll(
          '.kit-app > .kit-list:not([hidden]) .cb-group',
        ).length === 0,
    );
    t.check(
      `the switch: #/attention?filters=incoming,quiet-14d&flat and no group heads (${await pg.evaluate(() => location.hash)})`,
      (await pg.evaluate(() => location.hash)) ===
        '#/attention?filters=incoming,quiet-14d&flat' &&
        (await pg.$$(`${LIST} .cb-group`)).length === 0,
    );

    // 7: a view chip clears the filters.
    await pg.click(
      `${LIST} .kit-chips[data-group="view"] .kit-chip[data-id="new"]`,
    );
    await t.until(
      pg,
      () =>
        location.hash === '#/attention/new?flat' &&
        document.querySelectorAll('.cb-filter.on').length === 0,
    );
    t.check(
      `the view chip new turns every filter off: ${await pg.evaluate(() => location.hash)}`,
      (await pg.evaluate(() => location.hash)) === '#/attention/new?flat' &&
        (await pg.$$('.cb-filter.on')).length === 0,
    );

    // 8: + filter: the editor in the middle pane, the list beside it its
    // live preview (faded), one row per field, save.
    const firstTop = () =>
      pg.evaluate(
        () =>
          document
            .querySelector(
              '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row',
            )
            ?.getBoundingClientRect().top ?? null,
      );
    const listState = () =>
      pg.evaluate(() => ({
        hash: location.hash,
        first:
          document
            .querySelector(
              '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row .kit-kicker',
            )
            ?.textContent?.replace(' \u00b7 ', ':') ?? '',
        view:
          document.querySelector(
            '.kit-app > .kit-list:not([hidden]) .kit-chips[data-group="view"] .kit-chip.on',
          )?.dataset.id ?? '',
      }));
    const ED = '[data-testid="filter-editor"]';
    const line = () =>
      pg.evaluate(() => {
        const e = document.querySelector('.cb-preview-line');
        return e && !e.hidden ? (e.textContent ?? '') : '';
      });
    // The view's rows first (step 7's reload may still be on its way).
    const newRows = await served('view=new&group=false');
    await t.until(
      pg,
      (k) =>
        document
          .querySelector(
            '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row .kit-kicker',
          )
          ?.textContent?.replace(' · ', ':') === k,
      newRows[0],
      8000,
    );
    const topBefore = await firstTop();
    const listBefore = await listState();
    await pg.click('.cb-filter-add');
    await pg.waitForSelector(`${ED} .cb-conds[data-ready="true"]`, {
      timeout: 8000,
    });
    {
      const got = await pg.evaluate((ed) => {
        const e = document.querySelector(ed);
        const read = document.querySelector('.kit-app > .kit-read');
        const l = document.querySelector('.kit-app > .kit-list:not([hidden])');
        return {
          inRead: !!e && !!read && read.contains(e),
          inList: !!e && !!l && l.contains(e),
          sheet: !!document.querySelector('.kit-sheet'),
          focus: document.activeElement?.classList.contains('cb-filter-name'),
          title: e?.querySelector('.cb-filter-title')?.textContent ?? '',
        };
      }, ED);
      t.check(
        `+ filter opens the editor "new filter" in the middle pane, no sheet, focus on its name (${JSON.stringify(got)})`,
        got.inRead &&
          !got.inList &&
          !got.sheet &&
          got.focus &&
          got.title === 'new filter',
      );
    }
    await t.until(
      pg,
      () =>
        document.querySelector('.cb-preview-line')?.textContent ===
        'add a condition to preview',
    );
    // The field chips show at once, above the conditions (no + condition
    // to open, no search), and stay put as conditions are added below.
    const geo = () =>
      pg.evaluate((ed) => {
        const r = (sel) =>
          document.querySelector(`${ed} ${sel}`)?.getBoundingClientRect();
        const rows = r('.cb-cond-rows');
        return {
          name: r('.cb-filter-name')?.top,
          chips: r('.cb-cond-menu')?.top,
          chipsBottom: r('.cb-cond-menu')?.bottom,
          rowsTop: rows?.top,
          rowsH: rows?.height,
        };
      }, ED);
    const shut = await geo();
    {
      const got = await pg.evaluate((ed) => {
        const m = document.querySelector(`${ed} .cb-cond-menu`);
        const add = document.querySelector(`${ed} .cb-cond-add`);
        const search = document.querySelector(`${ed} .cb-cond-search`);
        return {
          chips: document.querySelectorAll(`${ed} .cb-cond-menu-row`).length,
          shown: !!m && !m.hidden && m.getBoundingClientRect().height > 0,
          add: !!add && add.hidden && add.getBoundingClientRect().height === 0,
          search: !!search && getComputedStyle(search).display === 'none',
          offered: document.querySelectorAll(
            `${ed} .cb-cond-menu-row[aria-disabled="false"]`,
          ).length,
        };
      }, ED);
      t.check(
        `+ filter shows the field chips at once, all offered, with no + condition button and no search (${JSON.stringify(got)})`,
        got.chips > 5 &&
          got.shown &&
          got.add &&
          got.search &&
          got.offered === got.chips,
      );
    }
    {
      const vocab = await api('GET', '/api/rules/vocabulary');
      const help = (n) =>
        (vocab.fields ?? []).find((f) => f.name === n)?.help ?? '?';
      const got = await pg.evaluate((ed) => {
        const chip = (n) =>
          document.querySelector(`${ed} .cb-cond-menu-row[data-field="${n}"]`);
        const group = (n) =>
          chip(n)?.closest('.cb-cond-group')?.querySelector('.cb-cond-group-l')
            ?.textContent ?? '';
        return {
          type: chip('kind')?.textContent ?? '',
          typeTitle: chip('kind')?.getAttribute('title') ?? '',
          typeGroup: group('kind'),
          unpushedTitle: chip('unpushed')?.getAttribute('title') ?? '',
          unpushedGroup: group('unpushed'),
        };
      }, ED);
      t.check(
        `a chip's title is its field's help, its group the types it applies to: type for any type, unpushed commits for branch, worktree (${JSON.stringify(got)})`,
        got.type === 'type' &&
          got.typeTitle === help('kind') &&
          got.typeGroup === 'any type' &&
          got.unpushedTitle === help('unpushed') &&
          got.unpushedGroup === 'branch, worktree',
      );
    }
    // A new type condition has nothing ticked: it isn't previewed.
    const c0 = `${ED} .cb-cond[data-index="0"]`;
    const c1 = `${ED} .cb-cond[data-index="1"]`;
    const draftLoad = () =>
      pg.waitForResponse(
        (r) => r.url().includes('/api/items?') && r.url().includes('match='),
        { timeout: 8000 },
      );
    {
      const loaded = draftLoad();
      await pg.click(`${ED} .cb-cond-menu-row[data-field="kind"]`);
      await loaded;
      const got = await pg.evaluate((c) => {
        const r = document.querySelector(c);
        return {
          f: r?.querySelector('.cb-cond-f')?.textContent ?? '',
          picks: r?.querySelectorAll('.cb-cond-pick').length ?? 0,
          on: r?.querySelectorAll('.cb-cond-pick.on').length ?? -1,
          value: r?.querySelector('.cb-cond-picks')?.dataset.value,
        };
      }, c0);
      t.check(
        `the type chip adds "type" below with no value ticked (${JSON.stringify(got)})`,
        got.f === 'type' && got.picks === 5 && got.on === 0 && got.value === '',
      );
      t.check(
        `and with only it, unfilled, the list reads "add a condition to preview" ("${await line()}")`,
        (await line()) === 'add a condition to preview',
      );
    }
    // Tick issue and pull request: one row, "is".
    await pg.click(`${c0} .cb-cond-pick[data-value="issue"]`);
    await pg.click(`${c0} .cb-cond-pick[data-value="pr"]`);
    {
      const got = await pg.evaluate((c) => {
        const r = document.querySelector(c);
        return {
          f: r?.querySelector('.cb-cond-f')?.textContent,
          o: r?.querySelector('select.cb-cond-o')?.selectedOptions[0]
            ?.textContent,
          ops: [...(r?.querySelectorAll('select.cb-cond-o option') ?? [])].map(
            (o) => o.textContent,
          ),
          on: [...(r?.querySelectorAll('.cb-cond-pick.on') ?? [])].map(
            (b) => b.textContent,
          ),
          text: r?.querySelector('input'),
          value: r?.querySelector('.cb-cond-picks')?.dataset.value,
        };
      }, c0);
      t.check(
        `ticking issue and pull request reads "type is issue or pull request", values ticked, never typed (${JSON.stringify(got)})`,
        got.f === 'type' &&
          got.o === 'is' &&
          JSON.stringify(got.ops) === JSON.stringify(['is', 'is not']) &&
          JSON.stringify(got.on) ===
            JSON.stringify(['pull request', 'issue']) &&
          got.text === null &&
          got.value === 'issue,pr',
      );
    }
    const draftKeys = (
      (
        await api('POST', '/api/rules/preview?limit=500', {
          match: [{ field: 'kind', op: 'in', value: 'issue,pr' }],
        })
      ).page ?? []
    ).map((r) => r.key);
    const want = `previewing a draft filter \u00b7 ${draftKeys.length} item${draftKeys.length === 1 ? '' : 's'}`;
    await t.until(
      pg,
      (w) => document.querySelector('.cb-preview-line')?.textContent === w,
      want,
      8000,
    );
    {
      const got = await pg.evaluate(() => {
        const l = document.querySelector('.kit-app > .kit-list:not([hidden])');
        const rows = l?.querySelector('.kit-rows');
        return {
          faded: rows ? Number(getComputedStyle(rows).opacity) : 1,
          chipsOn: l?.querySelectorAll('.kit-chip.on:not(.cb-group-switch)')
            .length,
        };
      });
      t.check(
        `the list fades and reads "${await line()}" (${JSON.stringify(got)})`,
        (await line()) === want && got.faded < 1 && got.chipsOn === 0,
      );
    }
    {
      const topAfter = await firstTop();
      t.check(
        `the list's first row, previewing, is at the same y as before the editor opened (${topBefore} → ${topAfter})`,
        topBefore !== null &&
          topAfter !== null &&
          Math.abs(topAfter - topBefore) <= 1,
      );
    }
    t.checkList(
      "and its rows are serve's matches for kind in issue,pr, newest first",
      await rowKeys(pg),
      draftKeys,
    );
    // rowsAre waits for the list's rows to be want.
    const rowsAre = (want) =>
      t.until(
        pg,
        (w) =>
          JSON.stringify(
            [
              ...document.querySelectorAll(
                '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row .kit-kicker',
              ),
            ].map((e) => (e.textContent ?? '').replace(' \u00b7 ', ':')),
          ) === JSON.stringify(w),
        want,
        8000,
      );
    const matching = (match) =>
      served(`match=${encodeURIComponent(JSON.stringify(match))}&group=false`);

    // A second condition, last activity: the name and the chips stay where
    // they were, the conditions grow below them.
    await pg.click(`${ED} .cb-cond-menu-row[data-field="updated"]`);
    {
      const two = await geo();
      t.check(
        `with two conditions the name and the field chips are where they were, the conditions below them (${JSON.stringify(shut)} → ${JSON.stringify(two)})`,
        Math.abs(two.name - shut.name) <= 1 &&
          Math.abs(two.chips - shut.chips) <= 1 &&
          two.rowsTop >= two.chipsBottom - 1 &&
          two.rowsH > shut.rowsH,
      );
    }
    {
      const got = await pg.evaluate((ed) => {
        const chip = (n) =>
          document.querySelector(`${ed} .cb-cond-menu-row[data-field="${n}"]`);
        const s = (n) => ({
          off: chip(n)?.getAttribute('aria-disabled'),
          title: chip(n)?.getAttribute('title') ?? '',
        });
        return {
          kind: s('kind'),
          unpushed: s('unpushed'),
          author: s('author'),
        };
      }, ED);
      t.check(
        `with type set, its chip is greyed "already used, change it below", unpushed commits greyed "doesn't apply to the chosen type", author offered (${JSON.stringify(got)})`,
        got.kind.off === 'true' &&
          got.kind.title === 'already used, change it below' &&
          got.unpushed.off === 'true' &&
          got.unpushed.title === "doesn't apply to the chosen type" &&
          got.author.off === 'false',
      );
    }
    // Last activity: a number and a unit, or a date.
    const dur = () =>
      pg.evaluate((c) => {
        const r = document.querySelector(c);
        const ago = r?.querySelector('.cb-cond-ago');
        return {
          ops: [...(r?.querySelectorAll('select.cb-cond-o option') ?? [])].map(
            (o) => o.textContent,
          ),
          value: r?.querySelector('.cb-cond-dur')?.dataset.value,
          num: !!r?.querySelector('input.cb-cond-num'),
          unit: r?.querySelector('select.cb-cond-unit')?.selectedOptions[0]
            ?.textContent,
          ago:
            ago && ago.getBoundingClientRect().width > 0 ? ago.textContent : '',
          date: !!r?.querySelector('input.cb-cond-date'),
        };
      }, c1);
    await pg.fill(`${c1} input.cb-cond-num`, '3');
    await pg.selectOption(`${c1} select.cb-cond-unit`, 'w');
    {
      const got = await dur();
      t.check(
        `last activity "more than" 3 weeks is stored 3w, "ago" after it; its operators are more than, within the last, before, on or after (${JSON.stringify(got)})`,
        JSON.stringify(got.ops) ===
          JSON.stringify([
            'more than',
            'within the last',
            'before',
            'on or after',
          ]) &&
          got.value === '3w' &&
          got.num &&
          got.unit === 'weeks' &&
          got.ago === 'ago' &&
          !got.date,
      );
    }
    await pg.selectOption(`${c1} select.cb-cond-o`, 'before');
    {
      const got = await dur();
      t.check(
        `switching to "before" clears the value and asks for a date (${JSON.stringify(got)})`,
        got.value === '' && got.date && !got.num && got.ago === '',
      );
    }
    await pg.fill(`${c1} input.cb-cond-date`, '2026-08-04');
    {
      const got = await dur();
      t.check(
        `a date picked is stored 2026-08-04 (${JSON.stringify(got)})`,
        got.value === '2026-08-04',
      );
      const want = await matching([
        { field: 'kind', op: 'in', value: 'issue,pr' },
        { field: 'updated', op: 'before', value: '2026-08-04' },
      ]);
      await rowsAre(want);
      t.check(
        `serve lists the issues quiet since before then, not those active after (${JSON.stringify(want)})`,
        want.includes(FISSUE(1)) &&
          want.includes(FISSUE(2)) &&
          !want.includes(FISSUE(4)) &&
          !want.includes(FISSUE(5)) &&
          want.length < draftKeys.length,
      );
      t.checkList(
        "and the list is serve's GET /api/items?match= for type is issue or pull request, last activity before 2026-08-04",
        await rowKeys(pg),
        want,
      );
    }
    // Repository: what casebook has seen, or a * pattern.
    await pg.click(`${c1} .cb-cond-rm`);
    await pg.click(`${c0} .cb-cond-rm`);
    await pg.click(`${ED} .cb-cond-menu-row[data-field="repo"]`);
    const typed = `${c0} .cb-cond-typed`;
    const seenRepos = (
      (await api('GET', '/api/rules/values?field=repo')).values ?? []
    ).map((v) => v.value);
    const sugg = () =>
      pg.evaluate(
        ([c, ed]) => {
          const l = document.querySelector(`${c} .cb-cond-sugg`);
          const lr = l?.getBoundingClientRect();
          const tr = document
            .querySelector(`${ed} .cb-conds`)
            ?.getBoundingClientRect();
          return {
            shown: !!l && !l.hidden && (lr?.height ?? 0) > 0,
            head: l?.querySelector('.cb-cond-sugg-head')?.textContent ?? '',
            pats: [...(l?.querySelectorAll('.cb-cond-sugg-pat') ?? [])].map(
              (e) => e.dataset.value,
            ),
            first: l?.querySelector('.cb-cond-sugg-row')?.dataset.value ?? '',
            firstPat: !!l
              ?.querySelector('.cb-cond-sugg-row')
              ?.classList.contains('cb-cond-sugg-pat'),
            rows: [
              ...(l?.querySelectorAll(
                '.cb-cond-sugg-row:not(.cb-cond-sugg-pat)',
              ) ?? []),
            ].map((e) => e.dataset.value),
            inside:
              !!lr &&
              !!tr &&
              lr.left >= tr.left - 0.5 &&
              lr.right <= tr.right + 0.5,
            box: lr && tr ? [lr.left, lr.right, tr.left, tr.right] : null,
          };
        },
        [c0, ED],
      );
    await pg.click(`${typed} input.cb-cond-typed-in`);
    await t.until(
      pg,
      (c) => !document.querySelector(`${c} .cb-cond-sugg`)?.hidden,
      c0,
    );
    {
      const got = await sugg();
      t.check(
        `focusing a repository shows its list: "${seenRepos.length} repositories" and every repo casebook has seen (${JSON.stringify(got)})`,
        got.shown &&
          got.head === `${seenRepos.length} repositories` &&
          JSON.stringify(got.rows) === JSON.stringify(seenRepos) &&
          [
            'schuettc/filt',
            'schuettc/filt-a',
            'schuettc/filt-b',
            'other/filt',
          ].every((r) => got.rows.includes(r)),
      );
      t.check(
        `the suggestion list stays inside the conditions table, side to side (${JSON.stringify(got.box)})`,
        got.inside,
      );
    }
    await pg.keyboard.type('schuettc');
    await t.until(
      pg,
      (c) =>
        document.querySelector(`${c} .cb-cond-sugg-row`)?.dataset.value ===
        'schuettc/*',
      c0,
    );
    {
      const got = await sugg();
      t.check(
        `typing the owner "schuettc" offers schuettc/* first, a pattern (${JSON.stringify(got)})`,
        got.shown && got.first === 'schuettc/*' && got.firstPat && got.inside,
      );
    }
    await pg.click(`${c0} .cb-cond-sugg-row[data-value="schuettc/*"]`);
    {
      const got = await pg.evaluate(
        (t) => ({
          value: document.querySelector(t)?.dataset.value,
          chips: [...document.querySelectorAll(`${t} .cb-cond-typed-chip`)].map(
            (e) => e.dataset.value,
          ),
          ph:
            document.querySelector(`${t} input.cb-cond-typed-in`)
              ?.placeholder ?? '',
          abc: document.body.innerHTML.includes('a, b, c'),
        }),
        typed,
      );
      t.check(
        `picking it stores the pattern schuettc/*, shown as a chip; the box reads "search or type", "a, b, c" nowhere (${JSON.stringify(got)})`,
        got.value === 'schuettc/*' &&
          JSON.stringify(got.chips) === JSON.stringify(['schuettc/*']) &&
          got.ph === 'search or type' &&
          !got.abc,
      );
      const want = await matching([
        { field: 'repo', op: 'is', value: 'schuettc/*' },
      ]);
      await rowsAre(want);
      t.checkList(
        "and the list is serve's GET /api/items?match= for repository is schuettc/*",
        await rowKeys(pg),
        want,
      );
      t.check(
        `which is every schuettc repo's items and none of other/filt (${want.length})`,
        want.length > 0 &&
          want.includes('repo:schuettc/filt-a') &&
          !want.some((k) => k.includes('other/filt')),
      );
    }
    await pg.click(
      `${typed} .cb-cond-typed-chip[data-value="schuettc/*"] .cb-cond-typed-x`,
    );
    await t.until(
      pg,
      () =>
        document.querySelector('.cb-preview-line')?.textContent ===
        'add a condition to preview',
    );
    {
      const got = await pg.evaluate(
        (t) => ({
          value: document.querySelector(t)?.dataset.value,
          chips: document.querySelectorAll(`${t} .cb-cond-typed-chip`).length,
        }),
        typed,
      );
      t.check(
        `× removes it: no chip, no value, and the list reads "add a condition to preview" (${JSON.stringify(got)})`,
        got.value === '' &&
          got.chips === 0 &&
          (await line()) === 'add a condition to preview',
      );
    }
    await pg.click(`${c0} .cb-cond-rm`);
    // Cancel: the list as it was.
    await pg.click(`${ED} .cb-filter-cancel`);
    await t.until(
      pg,
      (b) =>
        !document.querySelector('[data-testid="filter-editor"]') &&
        location.hash === b.hash &&
        document.querySelector(
          '.kit-app > .kit-list:not([hidden]) .kit-chips[data-group="view"] .kit-chip.on',
        )?.dataset.id === b.view,
      listBefore,
    );
    {
      const after = await listState();
      const faded = await pg.$(`${LIST}.cb-previewing`);
      t.check(
        `cancel restores the list: ${JSON.stringify(listBefore)} → ${JSON.stringify(after)}, unfaded, the middle pane empty`,
        JSON.stringify(after) === JSON.stringify(listBefore) &&
          !faded &&
          !!(await pg.$('.kit-app > .kit-read .cb-read-empty')),
      );
    }
    // Again, and save.
    await pg.click('.cb-filter-add');
    await pg.waitForSelector(`${ED} .cb-conds[data-ready="true"]`);
    await pg.click(`${ED} .cb-cond-menu-row[data-field="kind"]`);
    await pg.click(`${c0} .cb-cond-pick[data-value="issue"]`);
    await pg.click(`${c0} .cb-cond-pick[data-value="pr"]`);
    await pg.fill(`${ED} .cb-filter-name`, 'Open issues');
    await pg.click(`${ED} .cb-filter-save`);
    await t.until(
      pg,
      () =>
        location.hash.startsWith('#/attention?filters=open-issues') &&
        document
          .querySelector('.cb-filter[data-id="open-issues"]')
          ?.getAttribute('aria-pressed') === 'true' &&
        !document.querySelector('[data-testid="filter-editor"]'),
      undefined,
      8000,
    );
    t.check(
      `save: the editor closes, the chip Open issues is on, the address names it and the list is unfaded (${await pg.evaluate(() => location.hash)})`,
      (await pg
        .textContent('.cb-filter[data-id="open-issues"]')
        .catch(() => '')) === 'Open issues' &&
        (await pg.evaluate(() => location.hash)).startsWith(
          '#/attention?filters=open-issues',
        ) &&
        !(await pg.$(`${LIST}.cb-previewing`)) &&
        (await line()) === '',
    );
    const saved = async () =>
      ((await api('GET', '/api/filters')).filters ?? []).find(
        (r) => r.filter.id === 'open-issues',
      );
    {
      const c = (await saved())?.filter.match ?? [];
      t.check(
        `and serve has it: one condition, kind in issue,pr (${JSON.stringify(c)})`,
        c.length === 1 &&
          c[0].field === 'kind' &&
          c[0].op === 'in' &&
          c[0].value === 'issue,pr',
      );
    }

    // 9: edit: the editor prefilled, a change saved.
    await pg.click('.cb-filter-item[data-id="open-issues"] .cb-filter-more');
    await pg.click(
      '.cb-filter-item[data-id="open-issues"] .cb-filter-menu-item[data-act="edit"]',
    );
    await pg.waitForSelector(`${ED} .cb-conds[data-ready="true"]`, {
      timeout: 8000,
    });
    {
      const got = await pg.evaluate(
        (ed) => ({
          f: document.querySelector(`${ed} .cb-cond-f`)?.dataset.name,
          on: [...document.querySelectorAll(`${ed} .cb-cond-pick.on`)].map(
            (b) => b.dataset.value,
          ),
          name: document.querySelector(`${ed} .cb-filter-name`)?.value,
          title: document.querySelector(`${ed} .cb-filter-title`)?.textContent,
        }),
        ED,
      );
      t.check(
        `edit opens "edit Open issues" prefilled: type with pull request and issue ticked, named Open issues (${JSON.stringify(got)})`,
        got.f === 'kind' &&
          JSON.stringify(got.on) === JSON.stringify(['pr', 'issue']) &&
          got.name === 'Open issues' &&
          got.title === 'edit Open issues',
      );
    }
    await pg.click(`${c0} .cb-cond-pick[data-value="issue"]`);
    await pg.click(`${ED} .cb-filter-save`);
    t.check(
      'unticking issue and saving makes serve say kind is pr',
      await t.eventually(async () => {
        const c = (await saved())?.filter.match?.[0];
        return c?.field === 'kind' && c?.op === 'is' && c?.value === 'pr';
      }),
    );

    // 10: delete.
    await t.until(
      pg,
      () => !document.querySelector('[data-testid="filter-editor"]'),
    );
    await pg.click('.cb-filter-item[data-id="open-issues"] .cb-filter-more');
    await pg.click(
      '.cb-filter-item[data-id="open-issues"] .cb-filter-menu-item[data-act="delete"]',
    );
    await t.until(
      pg,
      () => !document.querySelector('.cb-filter[data-id="open-issues"]'),
    );
    t.check(
      'delete removes the chip',
      !(await pg.$('.cb-filter[data-id="open-issues"]')),
    );
    t.check('and serve lacks it', (await saved()) === undefined);

    // 11: a filter deleted elsewhere: named, the list empty.
    await pg.goto(`${s.url}#/attention?filters=nope`, {
      waitUntil: 'domcontentloaded',
    });
    await t.until(
      pg,
      () =>
        (document.querySelector('.cb-filter-err')?.textContent ?? '').includes(
          'unknown filter "nope"',
        ),
      8000,
    );
    const err = await pg.evaluate(() => ({
      text: document.querySelector('.cb-filter-err')?.textContent ?? '',
      shown: !document.querySelector('.cb-filter-err')?.hidden,
      rows: document.querySelectorAll(
        '.kit-app > .kit-list:not([hidden]) .kit-rows > .kit-row',
      ).length,
    }));
    t.check(
      `#/attention?filters=nope says unknown filter "nope" and lists nothing (${JSON.stringify(err)})`,
      err.shown && err.text.includes('unknown filter "nope"') && err.rows === 0,
    );
  } finally {
    clearInterval(beating);
  }
}

async function invalidFilter(context, t, s) {
  const agent = createAgent(s.base, s.token);
  const api = (m, p, b) => agent.api(m, p, b);
  const pg = await context.newPage();
  await pg.goto(`${s.url}#/attention/due`, { waitUntil: 'domcontentloaded' });
  await pg.waitForSelector('.cb-filter[data-id="bad"]', { timeout: 8000 });

  // 12: marked, with serve's reason.
  {
    const got = await pg.evaluate(() => {
      const e = document.querySelector('.cb-filter[data-id="bad"]');
      return {
        invalid: e?.hasAttribute('data-invalid') ?? false,
        title: e?.getAttribute('title') ?? '',
        disabled: e?.getAttribute('aria-disabled') ?? '',
      };
    });
    t.check(
      `a filter that doesn't validate is marked, with serve's reason (${JSON.stringify(got)})`,
      got.invalid &&
        got.title.includes('unknown field "colour"') &&
        got.disabled === 'true',
    );
  }

  // 13: it can't be selected.
  const before = await pg.evaluate(() => location.hash);
  // force: Playwright waits for an aria-disabled element to be enabled; a
  // person's click lands all the same.
  await pg.click('.cb-filter[data-id="bad"]', { force: true });
  await pg.waitForTimeout(300);
  {
    const got = await pg.evaluate(() => ({
      hash: location.hash,
      on: document
        .querySelector('.cb-filter[data-id="bad"]')
        ?.getAttribute('aria-pressed'),
    }));
    t.check(
      `clicking it leaves the address and the chip off (${JSON.stringify(got)})`,
      got.hash === before && got.on === 'false',
    );
  }

  // 14: edit shows its condition with serve's message under it.
  await pg.click('.cb-filter-item[data-id="bad"] .cb-filter-more');
  await pg.click(
    '.cb-filter-item[data-id="bad"] .cb-filter-menu-item[data-act="edit"]',
  );
  await pg.waitForSelector('.cb-filter-edit .cb-conds[data-ready="true"]', {
    timeout: 8000,
  });
  await t.until(
    pg,
    () => !!document.querySelector('.cb-filter-edit .cb-cond[data-invalid]'),
  );
  {
    const got = await pg.evaluate(() => ({
      f:
        document.querySelector('.cb-filter-edit .cb-cond-f')?.dataset.name ??
        '',
      err:
        document.querySelector(
          '.cb-filter-edit .cb-cond[data-invalid] .cb-cond-err',
        )?.textContent ?? '',
    }));
    t.check(
      `edit shows its condition (colour) with serve's message under it (${JSON.stringify(got)})`,
      got.f === 'colour' && got.err.includes('unknown field "colour"'),
    );
  }

  // 15: replaced with kind is repo and saved, it validates.
  await pg.click('.cb-filter-edit .cb-cond .cb-cond-rm');
  await pg.click('.cb-filter-edit .cb-cond-menu-row[data-field="kind"]');
  // A new type condition has nothing ticked: tick repository.
  await pg.click('.cb-filter-edit .cb-cond .cb-cond-pick[data-value="repo"]');
  await pg.click('.cb-filter-save');
  await t.until(
    pg,
    () =>
      !!document.querySelector('.cb-filter[data-id="bad"]') &&
      !document.querySelector('.cb-filter[data-id="bad"][data-invalid]'),
    undefined,
    8000,
  );
  const row = ((await api('GET', '/api/filters')).filters ?? []).find(
    (r) => r.filter.id === 'bad',
  );
  t.check(
    `replaced with kind is repo and saved, it validates: no data-invalid, serve says valid (${JSON.stringify(row)})`,
    !(await pg.$('.cb-filter[data-id="bad"][data-invalid]')) &&
      !!row &&
      !row.invalid &&
      row.filter.match?.[0]?.value === 'repo',
  );
}
