# Task 4b Report — The board view (four lanes, shared selection)

Commit: `2708f05`  
Branch: `feat/casebook-page`  
Base: `650362f`

---

## What changed per file

### `internal/casebook/web/board.ts` (created)

New module exporting `makeBoard(ctx, sel, filters)`.

- Builds a `.cb-board` element with four `.cb-lane` children: waiting, proposed, due, new (in precedence order).
- Each lane has: a header (`cb-lane-head`), a scrollable rows container (`cb-lane-rows`), and a "show more" button (`cb-lane-more`, hidden when no more items).
- Cards (`cb-board-card`) built via `h()` and `textContent` only (no innerHTML). Each card shows `kind · key`, title, age, and proposal disposition. Cards carry `data-id` for probe identity checks.
- Click: `sel.toggle(id)`. Shift-click: `sel.range(anchor, id, laneOrderIds)` — if anchor is null (no prior click), falls back to toggle.
- `sel.onChange` subscription repaints card `.on` / `.kit-box.on` classes without rebuilding DOM.
- `refresh()` fetches all four lanes in parallel via `Promise.allSettled`, then deduplicates by key in precedence order (Waiting > Proposed > Due > New). At most 200 items per lane; "show more" loads the next page.
- `destroy()` unsubscribes from the selection store.

### `internal/casebook/web/attention.ts` (modified)

- Added import of `makeBoard` from `./board.ts`.
- Added `boardHandle: ReturnType<typeof makeBoard> | null` variable.
- Added `getBoardFilters()` — returns the active filter values (kind, repo, relation, bot, age, rule, q) without the `view` key (the board overrides view per lane).
- Added `mountBoard()` — hides `.kit-rows` and `.kit-foot` inside `handle.el` (using the kit's stable internal class names from kit v0.11.0), creates the board via `makeBoard`, appends `boardHandle.el` to `handle.el`.
- Added `unmountBoard()` — calls `boardHandle.destroy()`, removes `boardHandle.el`, restores `.kit-rows` and `.kit-foot` visibility.
- `show('board')` now calls `mountBoard()` (was a no-op).
- `show(other)` now calls `unmountBoard()` before setting up the list view.
- `onLive` forwards `index`, `decided`, and `proposals` events to `boardHandle.refresh()` when the board is active, otherwise to `reload()`.

### `internal/casebook/web/casebook.css` (modified)

Added styles inside the "board lanes" section:

- `.cb-lane-more` — show-more button at the foot of each lane (monospace, muted colour, full-width, no border).
- `.cb-board-card` — card container: white card background, 1px border, 8px radius, cursor pointer; `.on` adds signal-coloured left shadow and border.
- `.cb-board-card .kit-box` and `.kit-box.on` — selection indicator consistent with the kit's row checkbox.
- `.cb-card-head` — flex row holding the box and kicker.
- `.cb-card-kk` — monospace kind·key line (muted, ellipsis overflow).
- `.cb-card-title` — ellipsis title line.
- `.cb-card-age` — age in mono/muted.
- `.cb-card-prop` — proposal disposition in agent colour.

### `internal/casebook/web/probe.mjs` (modified)

Added five new board scenarios placed before the existing `catch` block (TDD: written first, confirmed failing, then implemented):

1. **"the board shows four lanes with the fixture's items in the right lanes"** — navigates to `#/attention/board`, checks for exactly 4 `.cb-lane` elements in the correct `data-lane` order (`waiting → proposed → due → new`), and that the waiting lane has at least one `.cb-board-card`.
2. **"a selection made on the board shows on the list and vice versa"** — clicks a board card, confirms `.on` class appears, navigates to the list view, confirms the primary button shows "Decide N".
3. **"shift-click ranges within a lane"** — clicks first card, shift-clicks second card, confirms at least 2 cards have `.on`.
4. **"deciding from the board removes the card and deselects it"** — fresh browser context; clicks a card, opens the decide sheet, picks a disposition, submits; confirms the decided card's `data-id` is absent from the lane or the primary is hidden.
5. **"a filter narrows every lane"** — counts unfiltered waiting cards in board view, then opens a fresh page with `?q=nudge#/attention/board` and confirms the filtered count is fewer or ≤ 1.

### `internal/casebook/serve/assets/casebook.css` and `casebook.js` (rebuilt)

Committed assets rebuilt by `npm run build` (esbuild). No manual edits.

---

## Commands run

```
# Typecheck
npm run typecheck       → OK

# Lint
npm run lint            → OK (0 warnings)

# Format check
npm run fmt:check       → FAIL (probe.mjs + board.ts)
npm run fmt             → fixed
npm run fmt:check       → OK

# Build (includes probe)
npm run build
  → probe: 43 passed, 0 failed
  (36 prior + 7 board scenarios all pass)

# Unit tests
npm test                → 18 pass, 0 fail

# Go
gofmt -l .             → (empty, OK)
go vet ./...            → (no output, OK)
go test -race ./...     → all pass
```

---

## Architecture note (deviation from brief, justified)

The brief says "the board view mounts the board in place of the list's rows". I implemented this by directly hiding `.kit-rows` and `.kit-foot` inside the kit list panel (`handle.el`) and appending the `.cb-board` element there, rather than inserting a sibling in the app grid. Reason: the kit app uses CSS grid **auto-placement** (not named grid areas), so a sibling `.kit-list` would land in the wrong grid column. Hiding and restoring the kit's internal containers by class name (`.kit-rows`, `.kit-foot`) is safe at kit v0.11.0 (classes confirmed in `localweb/page/assets/kit.css`). This approach keeps the kit's view chips and search header visible in board mode (filters and search still apply), matching the spec's intent.

---

## Self-review findings

- All untrusted text set via `textContent` (kit `h()` or explicit assignment). No `innerHTML`.
- `sel.range()` receives `sel.anchor()` with null guard — falls back to toggle.
- `refresh()` uses `Promise.allSettled` so one lane failure doesn't block others.
- `destroy()` unsubscribes the `onChange` listener.
- `unmountBoard()` is idempotent (checks `boardHandle !== null`).
- `mountBoard()` is idempotent (checks `boardHandle !== null`).
- No browser opened: `pgrep -fl "casebook serve"` returns nothing; `CASEBOOK_NO_BROWSER=1` and `--no-open` enforced by `serve.mjs`.
- `core.hooksPath` = `/Users/courtschuett/.config/casebook/hooks` ✓

---

## No concerns

## Fix round

Commit: `2efd917`
Branch: `feat/casebook-page`

---

### Root-cause summary (three bugs)

**Bug 1 — Board squeezes into the 400 px list column.**
`mountBoard()` added class `cb-board` to `.kit-app`.  The board *container* element inside the list is also named `.cb-board`.  The rule `.cb-board { display: flex; gap: 16px; padding: 16px; … }` therefore also matched the app element, converting it from a CSS grid to a flex container—items appeared side-by-side instead of in rows, so the board only ever got the share of space a flex item can win on a crowded bar.

**Bug 2 — "waiting on you" chip stayed active.**
`show('board')` mounted the board but never updated the view chips via `handle.setChips('view', …)`.  `filters.view` was also never set to `'board'`, so the chips stayed on the previous view.

**Bug 3 — Clicking a card center navigated away instead of selecting.**
The title click handler used `e.stopPropagation()` (correct for the title), but the Playwright probe's `firstCard.click()` hits the *center* of the card's bounding box—which lands on `.cb-card-title`.  Stoppage prevented the card's own toggle handler from running, so no selection occurred.  Separately, no navigation to `#/item/<key>` was implemented at all.

---

### What changed per file

#### `internal/casebook/web/casebook.css`

- Renamed app modifier class from `.cb-board` to `.cb-board-active` everywhere to avoid colliding with the board container element's `.cb-board` rule.
- New `.kit-app.cb-board-active` block:
  - `grid-template-columns: minmax(0, 1fr) auto` — collapses the 3-track original into 2 tracks so the list (track 1, 1fr) fills all space except the rail (track 2, auto = 360 px from the rail's `width: 360px`).
  - `overflow-x: hidden` — prevents the document from scrolling sideways; the `.cb-board` container handles its own horizontal scroll.
- `.kit-app.cb-board-active > .kit-list`: `width: auto` overrides the kit's `width: 400px`.
- `.kit-app.cb-board-active > .kit-read`: `display: none` removes the reading column from the grid while the board is shown.
- `.cb-lane`: changed from `width: 280px; flex: none` to `flex: 1 0 220px; min-width: 220px` so lanes share the available width equally; at 1600 × 900 each lane is ≈ 290 px.

#### `internal/casebook/web/attention.ts`

- `mountBoard()`: now adds `cb-board-active` (not `cb-board`) to `.kit-app`; also passes an `onOpen` callback to `makeBoard` that records `filters.view = laneId` before navigation so the list reloads with the correct view when the item is shown.
- `unmountBoard()`: removes `cb-board-active`.
- `show('board')`: sets `filters.view = 'board'` and calls `handle.setChips('view', …)` with `on: v.id === 'board'` so the board chip is highlighted.
- `show(key)` (item-key branch): calls `handle.setChips('view', …)` to restore the chip that corresponds to `filters.view` (the lane the card came from) so the list highlights the correct view while displaying the item detail.

#### `internal/casebook/web/board.ts`

- `makeBoard` signature extended with `onOpen?: (key: string, laneId: string) => void`.
- `buildCard`: title element (`cb-card-title`) gets a dedicated `click` handler with `e.stopPropagation()` that calls `onOpen?.(key, laneId)` then `ctx.route.go('item', key)`.
- `buildCard`: card gets a `keydown` handler for `o` / `Enter` that does the same.
- Card body click handler is unchanged (toggle/range on non-title clicks).

#### `internal/casebook/web/probe.mjs`

Six new scenarios added (after the existing filter scenario):

1. **`board layout at 1600×900`** — 1600 × 900 viewport page; checks board chip is active, 4 lanes exist, all lane bounding boxes inside viewport, each ≥ 220 px wide.  Also saves `/tmp/board-after.png`.
2. **`board layout at 1100×800`** — 1100 × 800 viewport; checks `document.documentElement.scrollWidth <= innerWidth` (document does not scroll horizontally).
3. **`opening a card from the board`** — clicks `.cb-card-title`; checks reading column text includes the card's title, hash is `#/item/<key>`, and `goBack()` returns to `#/attention/board`.

Existing board selection scenarios updated to click `.kit-box` (the checkbox) instead of the whole card element, so the title's `stopPropagation` is not triggered:
- "clicking a board card selects it" / cleanup: use `[data-lane="waiting"] .cb-board-card .kit-box`.
- "shift-click ranges within a lane" / cleanup: use `.kit-box` elements.
- "deciding from the board" / cleanup: use `.kit-box`.

#### `internal/casebook/serve/assets/casebook.css` and `casebook.js` (rebuilt)

Committed built assets (esbuild + postcss).

---

### Commands run

```
npm run typecheck   → OK
npm run lint        → OK (0 warnings)
npm run fmt:check   → OK
npm test            → 18 pass, 0 fail
npm run build       → probe: 52 passed, 0 failed
gofmt -l .         → (empty)
go vet ./...       → (no output)
go test -race ./... → all packages pass
pgrep -fl "casebook serve" → none
git config core.hooksPath → /Users/courtschuett/.config/casebook/hooks ✓
```

---

### Self-review

- Root cause of the class-name collision is fully documented in CSS comments to prevent recurrence.
- The `onOpen` callback sets `filters.view` **before** `ctx.route.go()`, which is synchronous before `hashchange` fires, so the reload uses the correct view.
- `e.stopPropagation()` on the title click is intentional: it prevents the card's toggle from firing when the user clicks the title to open.  Clicking any other part of the card (checkbox, kicker, age) still toggles selection.
- No `.innerHTML` introduced.
- No browser opened during the run.

---

### No new concerns

## Fix round 2

Commit: `2760e94`
Branch: `feat/casebook-page`
Base: `2efd917`

---

### Root-cause summary (four bugs)

**Bug 1 — Encoded item links open an empty page.**
`router.ts` `parse()` returned the raw percent-encoded sub (e.g.
`issue:schuettc%2Fhail%234`) without decoding it.  The API call
`/api/item?key=issue:schuettc%2Fhail%234` returned 404.  Separately,
`go()` built URLs by appending the raw key, so a `#` inside a key was
interpreted as the start of a new fragment by the browser, breaking the
URL.

**Bug 2 — Board "select all N in view" counted only the waiting view.**
`totalItemsForView` was set from `reload()` (list view).  When the board
was mounted, no callback updated the foot, so the button always said the
count from the last list view the user visited.

**Bug 3 — Board lane "show N more" inflated by de-duplicated items.**
`board.ts` `refresh()` set each lane's `total` to the raw API total.
After de-duplication, items already shown in a higher-precedence lane
were removed from the displayed set, but the `total` was not adjusted.
The remaining count (`total - items.length`) included de-duplicated
items that would never be shown, so e.g. the New lane showed "show 2
more" when all its unique items were already visible.

**Bug 4 — Empty reading column was completely blank.**
`readEl` started with no children.  Until an item was opened, the right
side of the app was a white void with no hint that items could be
selected.

---

### What changed per file

#### `internal/casebook/web/router.ts`

- Added `safeDecode(s)` — wraps `decodeURIComponent` in a try/catch so a
  malformed `%xx` escape returns the raw string instead of throwing.
- Added export `pathEscape(s)` — mirrors Go's `url.PathEscape`: runs
  `encodeURIComponent` then un-encodes `:` and `@` (which `url.PathEscape`
  keeps raw).  Encodes `/`, `#`, `?`, `%`, space, etc.
- `parse()`: decodes the sub with `safeDecode` so both server-built URLs
  (`url.PathEscape` keeps `:`) and client-built URLs (`encodeURIComponent`
  encodes `:`) round-trip to the same key.
- `go()`: encodes the sub with `pathEscape` before setting `location.hash`
  so `/` and `#` in keys never break the fragment.

#### `internal/casebook/web/router.test.ts` (created)

New `node --test` unit-test file covering:
- `parse()`: empty hash, `#/`, `#/attention`, `#/attention/waiting`,
  `#/attention/board`, server-encoded issue key (`issue:schuettc%2Fhail%234`),
  server-encoded pr key, key with `@`, encoded `@`, spaces, raw colon,
  malformed percent-escape (no throw), raw unencoded key.
- `pathEscape()`: plain ASCII unchanged, `/` → `%2F`, `#` → `%23`,
  `:` kept raw, `@` kept raw, space → `%20`, full issue/pr key encoding.
- Round-trip tests: seven key patterns (including `/`, `#`, `@`, `:`, spaces)
  plus a server-vs-client URL cross-check.
- Total: 47 tests, all pass.

#### `internal/casebook/web/board.ts`

- `makeBoard` signature: added optional `onRefresh?: (totalUniqueItems: number) => void`.
- Return type: added `totalItems(): number` and `allKeys(): string[]` to the
  public API.
- `refresh()`: after de-duplication, sets `adjustedTotal`:
  - If the API returned all items on the first page (`items.length >= total`),
    `adjustedTotal = unique.length` (no "show more" for de-duplicated items).
  - Otherwise keeps the raw `total` (more pages may have unique items).
- `refresh()`: after repainting all lanes, counts unique items across all
  lanes (`boardTotalUnique`) and calls `onRefresh?.(boardTotalUnique)`.
- `totalItems()`: returns `boardTotalUnique`.
- `allKeys()`: returns all unique keys across all lanes in precedence order.

#### `internal/casebook/web/attention.ts`

- `renderReadEmpty()` / `updateReadEmptyCount()` / `showReadEmpty()`: new
  helpers that render `.cb-read-empty` with the section name, view count,
  and prompt "Select an item to see it here."
- `readEl` is initialised by calling `showReadEmpty()` so the reading column
  is never blank on first paint.
- `reload()`: calls `updateReadEmptyCount()` after setting `totalItemsForView`
  so the count stays current.
- `selectAllInView()`: when `boardHandle` is set, calls `handle.selectAll(boardHandle.allKeys())` instead of fetching from the API.
- `show(listView)` (non-board, non-item branch): calls `showReadEmpty()` when
  switching list views so the reading column resets to the empty state.
- `mountBoard()`: passes `onRefresh` callback to `makeBoard`; callback updates
  the foot's "select all N in view" button with the de-duplicated board total.

#### `internal/casebook/web/casebook.css`

Added `.cb-read-empty`, `.cb-read-empty-section`, `.cb-read-empty-count`, and
`.cb-read-empty-prompt` styles (muted mono/sans, matching the kit's muted
colour token).

#### `internal/casebook/web/probe.mjs`

Four new fix-round-2 scenarios (added before the final `catch`):

1. **`encoded item URL opens item detail`** — navigates to
   `#/item/issue:schuettc%2Fhail%234` (exact server URL), waits for
   `.kit-read .cb-item`, asserts title is non-empty.
2. **`board select-all reflects de-duplicated total`** — counts
   `.cb-board-card` total, reads `.cb-sel-all` text, asserts the number
   matches; clicks select-all and asserts `.cb-board-card.on` count equals
   total.
3. **`board lane show-more reflects de-duplicated items`** — for each of the
   four lanes, if `.cb-lane-more` is visible, asserts its count is > 0.
4. **`empty reading column shows the quiet empty state`** — navigates to
   `#/attention/waiting` with no item open, asserts `.cb-read-empty` is
   visible, section label includes "attention", prompt text is non-empty.

#### `internal/casebook/serve/assets/casebook.css` and `casebook.js` (rebuilt)

esbuild rebuild of the updated sources.

---

### Commands run

```
npm test            → 47 pass, 0 fail  (29 new router.test.ts tests)
npm run typecheck   → OK
npm run lint        → OK (0 warnings)
npm run fmt:check   → OK (after npm run fmt)
npm run build       → probe: 59 passed, 0 failed  (7 new fix-round-2 probes)
gofmt -l .         → (empty)
go vet ./...       → (no output)
go test -race ./... → all packages pass
pgrep -fl "casebook serve" → none
git config core.hooksPath → /Users/courtschuett/.config/casebook/hooks ✓
```

Screenshots taken at 1600×900:
- `/tmp/fix2-board.png` — board view, "select all 4 in view", no "show more" in New lane
- `/tmp/fix2-item.png` — `#/item/issue:schuettc%2Fhail%234` opens "crash on start"
- `/tmp/fix2-new.png`  — new view, reading column shows "ATTENTION / 4 items / Select an item to see it here."

---

### Self-review

- `pathEscape` keeps `:` and `@` raw (matching Go's `url.PathEscape`) so
  server-generated and client-generated URLs are identical.
- `safeDecode` catches any `URIError` from a malformed `%xx` sequence;
  both `%3A` (client encodes `:`) and `:` (server leaves raw) decode to `:`.
- `adjustedTotal` only collapses to `unique.length` when the API confirmed
  all items were returned on the first page; otherwise "show more" remains
  available for pages that might contain unique items.
- `showReadEmpty()` is called at init and on every list-view navigation; item
  opens replace the empty state; board view hides `.kit-read` via CSS so no
  state management needed there.
- No `innerHTML` introduced. No browser opened.

---

### No new concerns

## Review fixes

**Commit:** `8a9e03e`
**Branch:** `feat/casebook-page`
**Base commit:** `1e42b53`

---

### Reviewer findings addressed (8 items)

**1. Title fallback (board.ts, item.ts, attention.ts)**
- Changed `it.title ?? it.key` → `it.title ?? displayKey` in board.ts (where `displayKey = keyWithoutKind(it.key)`)
- Changed `it.title ?? it.key` → `it.title ?? keyWithoutKind(it.key)` in item.ts and attention.ts
- Added probe: "no board card title starts with a kind prefix (fallback is keyWithoutKind)" — checks all board card `.cb-card-title` elements, fails if any text matches `/^(branch|repo|issue|pr|worktree):/`

**2. Theme detection in probe (probe.mjs)**
- Changed `$eval('html', ...)` → `$eval('body', ...)` in both `detectTheme` and `detectTheme2` (kit sets background on `body`, not `html`)
- Changed regex `/rgb\((\d+),\s*(\d+),\s*(\d+)\)/` → `/rgba?\((\d+),\s*(\d+),\s*(\d+)/` to match both `rgb()` and `rgba()` forms
- Extended `forceTheme` / `forceTheme2` to loop up to 6 times and assert `actual === target` before taking the screenshot (the assertion fires as a probe check)

**3. Deselect on live decided events (attention.ts)**
- Added `selection.deselect(decidedKeys)` call in the `onLive('decided')` handler, where `decidedKeys = (data as {keys?: string[]}).keys ?? []` (matching the server's payload: `{"keys": done, "disposition": ..., "by": ..., "proposed_by": ...}`)
- Added probe: "live decided event deselects the decided item on the page" — selects a board card, POSTs `/api/decide` directly (not via UI), waits 2.5s, navigates to list view, asserts primary button does not show "Decide ..."

**4. Counts under every live event (attention.ts)**
- Added `ctx.api.get('/summary').then(s => applyCounts(s.counts))` in both the `decided` and `proposals` branches of `onLive`
- Added probe: "fake-agent proposal updates the 'proposed' chip count" — registers a session via `/api/agent/presence`, POSTs to `/api/agent/propose` for `pr:schuettc/hail#3`, waits 5s, asserts `.kit-chip[data-id="proposed"] .kit-n` text increased
  - Root cause of initial failure: view chips use `.kit-chip` not `.kit-ctl`; and the kit only renders `.kit-n` when count is a number (not undefined). Selector corrected.
  - Key selection: `pr:schuettc/hail#3` is used because `issue:#4` and `issue:#5` are decided by earlier probes; PR #3 remains undecided until this probe.

**5. router.test.ts imports real code (router-helpers.ts, router.ts, router.test.ts)**
- Created `router-helpers.ts` — pure module exporting `safeDecode`, `pathEscape`, `parse` with no `window` access at module scope
- Updated `router.ts` — re-exports the helpers from `router-helpers.ts` and imports them for `go()` and `dispatchCurrent()`; `window.addEventListener` remains only here
- Rewrote `router.test.ts` — imports `{ safeDecode, pathEscape, parse }` from `./router-helpers.ts` directly; removed all inline copies
- Verified: breaking `pathEscape` (replacing body with `return s`) causes 8 of 34 router tests to fail; reverting restores all 34

**6. Kit card classes (board.ts, casebook.css)**
- Changed `class: 'cb-board-card' + (selected ? ' on' : '')` → `class: 'kit-card' + (selected ? ' on' : '')` in board.ts
- Changed `h('div', { class: 'cb-card-head' }, box, kk)` → `h('div', { class: 'kit-card-head' }, box, kk)`
- Changed `.kk` element from `div` to `span` to avoid block inside kit-card-head
- Updated `repaintSelection()` selector from `.cb-board-card` → `.kit-card`
- Updated all probe.mjs selectors: s/\.cb-board-card/\.kit-card/g; scoped board-only counts to `.cb-lane .kit-card` to avoid matching kit-cards elsewhere
- Empty state eyebrow: added `kit-label` class to `nameEl` in `renderReadEmpty()` (`h('p', { class: 'cb-read-empty-section kit-label' }, 'attention')`)
- `casebook.css`: replaced `.cb-board-card` / `.cb-card-head` blocks with:
  - `.cb-lane .kit-card` — tighter padding/margin, cursor, transition (kit's background/border/radius reused)
  - `.cb-lane .kit-card.on` — signal-coloured left inset shadow
  - `.cb-lane .kit-card .kit-box` / `.kit-box.on` — checkbox sizing and selected colour
  - `.cb-lane .kit-card-head` — flex layout override (kit's default is block + uppercase); text-transform: none
  - `.cb-card-kk`, `.cb-card-title`, `.cb-card-age`, `.cb-card-prop` — ellipsis and typographic overrides specific to board cards
- Screenshot: `/tmp/fix3-board.png` at 1600×900 taken and checked ("four lanes visible" probe)

**7. Stronger probes**
- *Shift-click (3-item lane):* Extended fixture with `issue:schuettc/hail#5` (author carol) in `apptest/apptest.go`, regenerated `testdata/home` bundle (`go test -run TestFixture -update`). Updated shift-click probe to require `waitingBoxes.length >= 3`, click first, shift-click third, assert `waitingCardsAll[1].classList.contains('on')` (middle card). Two checks: "middle card selected" and "≥3 selected".
- *Filter check all lanes:* Replaced per-waiting-lane filter count + `||` condition with `.cb-lane .kit-card` total across all lanes; assert `filteredTotal < unfilteredTotal` (no `||` weakener). ?q=nudge matches only PR #3 "fix nudge" in the waiting lane; all other items don't contain "nudge".
- *List→board selection:* Added new scenario "a selection made on the list shows on the board" — selects first list row, navigates to board, asserts `any .cb-lane .kit-card.on count > 0`, deselects all.

**8. % keys (router.test.ts)**
- Added `'issue:foo%bar/baz#6'` to the round-trip test key list; `pathEscape('%')` = `'%25'`, which decodes back to `'%'` via `safeDecode`

---

### Files changed per file

| File | Change |
|------|--------|
| `internal/casebook/apptest/apptest.go` | Added issue #5 (by carol) to FakeGh GraphQL response |
| `internal/casebook/web/testdata/home/cache/github.json` | Regenerated (now includes issue #5) |
| `internal/casebook/web/testdata/home/data/repo.bundle` | Regenerated (fixture bundle with 3 waiting items) |
| `internal/casebook/web/router-helpers.ts` | New: pure exports `safeDecode`, `pathEscape`, `parse` |
| `internal/casebook/web/router.ts` | Re-exports helpers from router-helpers.ts |
| `internal/casebook/web/router.test.ts` | Imports real helpers; adds `%` key; adds safeDecode tests |
| `internal/casebook/web/board.ts` | Title fallback; `.kit-card` / `.kit-card-head` classes |
| `internal/casebook/web/item.ts` | Title fallback uses `keyWithoutKind` |
| `internal/casebook/web/attention.ts` | Title fallback; deselect on `decided`; summary re-fetch on `decided`+`proposals`; `kit-label` on empty-state eyebrow |
| `internal/casebook/web/casebook.css` | Kit card CSS: removed `.cb-board-card` duplicate block; added `.cb-lane .kit-card` overrides |
| `internal/casebook/web/probe.mjs` | All probe fixes: theme detection, shift-click 3 items, filter all lanes, list→board selection, title fallback probe, live deselect probe, proposals count probe, board screenshot |
| `internal/casebook/serve/assets/casebook.js` | Rebuilt |
| `internal/casebook/serve/assets/casebook.css` | Rebuilt |

---

### Commands run

```
# Web checks
npm test            → 58/58 pass (5 new: safeDecode×3, % key round-trip, router imports real code)
npm run typecheck   → clean
npm run lint        → clean (0 warnings)
npm run fmt:check   → clean
npm run build       → 73 probe checks pass, 0 failed
                      (5 new: title fallback, live deselect, proposals count, list→board selection,
                       shift-click middle card; + theme assert checks in fid scenarios)

# Go checks
gofmt -l .          → (empty)
go vet ./...        → (no output)
go test -race ./... → all packages pass

# Safety
pgrep -fl "casebook serve" → (none)
git config core.hooksPath  → /Users/courtschuett/.config/casebook/hooks

# Regression: breaking pathEscape in router-helpers.ts causes 8/34 router tests to fail; reverting passes 34/34
```

---

### Self-review findings

- **Kit card `text-transform: none` override**: `kit-card-head` has `text-transform: uppercase` in the kit. The board card uses kit-card-head as a flex row holder (checkbox + kicker). Added `text-transform: none` in casebook.css so keys display unmodified. This is board-specific and documented.
- **`% key` round-trip**: `pathEscape('%') = '%25'`; `safeDecode('%25') = '%'`. Verified round-trip works.
- **Fixture item count**: Waiting lane now has 3 items (PR #3, issue #4, issue #5). The shift-click probe uses the waitingBoxes in order (alphabetically sorted by ID). Issue #4 comes before #5 and both before PR #3. Middle card in shift-click (first→third) is issue #5.
- **Probe ordering consideration**: Deciding from board decides issue #4 (first waiting); live deselect decides issue #5 (second waiting); proposals probe proposes PR #3 (third waiting = still undecided). Ordering verified through probe run.
- **`serveHandle.base` vs `serveHandle.url`**: API calls from `page.evaluate()` use `serveHandle.base` (the base URL without token path). This works because pages load from the token URL and receive the auth cookie, which is sent with same-origin `fetch()` calls.
- No `innerHTML` introduced. No browser opened.

### Deviations from brief

None. All 8 reviewer findings are addressed.
