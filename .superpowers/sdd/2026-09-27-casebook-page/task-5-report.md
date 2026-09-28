# Task 5 fix-round report

**Worker**: fix-round subagent  
**Base commit**: b9140ff  
**Branch**: feat/casebook-page

---

## Changes per file

### `internal/casebook/item/policy.go`
Added `s.Undecided` guard to the `incoming-no-reply` rule in `Policy.Evaluate`. A decided item (Decision != nil → Undecided=false) no longer fires the hit, so it leaves the "waiting on you" view after Court decides it. `outgoing-stale` was intentionally left without the guard because the engine test `TestUntilAndIgnore` explicitly verifies that a wait-decided PR still fires outgoing-stale (spec says policies still apply to waiting items; only the "waiting on you" view was broken).

### `internal/casebook/item/policy_test.go`
Added `{"incoming unanswered decided", ...}` case (Undecided=false → nil hits) and updated the existing `"incoming unanswered"` case to set `Undecided: true`. Also updated `"incoming answered"` and `"incoming recent"` to set `Undecided: true` so they still verify the `Undecided` guard correctly.

### `internal/casebook/serve/index.go`
Added `InAttention(key string) bool` method that iterates `res.Attention()` to check if a key is currently in the engine's attention set. Items whose wait has expired and returned to attention (status=due) are included. This is the engine's own notion of "in attention" as required.

### `internal/casebook/serve/agent_api.go`
Modified `agentPropose` to refuse proposals for items that are **known to the index but NOT in attention** (i.e., already decided). Items not yet in the index (not synced from GitHub) pass through—the agent may propose before the next sync. The error message names the item's current status so the agent can read it. Added `propose` package import.

### `internal/casebook/serve/serve_test.go`
Added two new Go tests:
- `TestAgentProposeRefusesDecidedItem`: decides pr:schuettc/hail#3 via /api/decide, then attempts to propose on it; expects 0 proposed and an "not in attention" error.
- `TestAgentProposeAcceptsAttentionItem`: proposes on issue:schuettc/hail#4 (undecided, in attention); expects 1 proposed.
- Updated `TestSinceListsOverruledWithReasons` to use `issue:schuettc/hail#5` for the second proposal (pr#3 was decided in the first round; the new fix correctly refuses re-proposing on it).

### `internal/casebook/apptest/apptest.go`
Added issues #5, #6 (frank), and #7 (grace) to FakeGh. The extra issues give the probe's T5 scenarios enough dedicated items so no two scenarios share an item. Each issue is an open incoming item from a different non-schuettc user, so all fire `incoming-no-reply` correctly.

### `internal/casebook/web/proposals.ts`
Renamed `bulkProposalFoot` → `bulkProposalActions` and `BulkProposalFoot` → `BulkProposalActions` to match the plan's interface name. The header comment was updated to reflect the new export name.

### `internal/casebook/web/attention.ts`
- Updated import: `bulkProposalFoot` → `bulkProposalActions`, `BulkProposalFoot` → `BulkProposalActions`.
- Removed `try/catch` blocks from `a` and `r` key registrations. The handlers check scope internally (`if (!currentOpenKey) return` and `if (!it?.proposal || it.proposal.state !== 'pending') return`), so no try/catch is needed. Registering without a guard lets genuine clashes surface rather than being silently swallowed.

### `internal/casebook/web/decide-math.ts`
Added `stripOwnKey(subject: string, key: string): string` — removes the item's own key from a history entry subject line in display only. Exported for testing and for `item.ts` to import.

### `internal/casebook/web/item.ts`
- Imports `stripOwnKey` from `./decide-math.ts`.
- Passes `ownKey: string` parameter to `renderHistory` and strips the item's own key from each decision's `Subject` text: `"decide pr:schuettc/hail#3 → close by schuettc"` → `"decide → close by schuettc"`. Entries mentioning a different key are unchanged.

### `internal/casebook/web/decide.test.ts`
Added `describe('stripOwnKey')` block with five unit tests covering: removes key followed by space, removes key at end of string, leaves different key unchanged, empty key returns subject unchanged, subject not containing key is unchanged.

### `internal/casebook/web/fixture_test.go`
Added a second local branch `fix/login` to the fixture's hail clone. This gives the probe's "decide in bulk" scenario two dedicated branch items (feat/client and fix/login) to consume without touching any of the three waiting items (issue#4, issue#5, pr#3) that the board tests need.

### `internal/casebook/web/probe.mjs`
**False-pass removal:**
- Scenario B (accept): removed `check(..., true)` fallback; uses `issue:schuettc/hail#6` (dedicated, undecided at B's time). Also fixed navigation URL (was still pointing to `%234` = #4).
- Scenario C (reject): removed `check(..., true)` fallback; added `GET /api/item` verification that `proposals[].state === 'rejected'` and `proposals[].reason` contains the typed reason text.
- Scenario D (change): removed all `check(..., true/false)` fallbacks; uses `pr:schuettc/hail#3` with its chip-count pending proposal (the original D intent, now without the catch-all else). Fixed the `closeBtn = null` path to explicitly fail instead of passing with `true`.
- Scenario E `a` key: removed `check(..., true)` fallback; uses `issue:schuettc/hail#7` (dedicated).
- Scenario E `r` key: removed `check(..., true)` fallback; changed item from `branch:schuettc/hail@feat/client` (now decided by bulk decide) to `repo:schuettc/hail` (undecided at E's time since C only rejects the proposal, not the item).
- Scenario F (bulk): removed `check(..., true)` fallback for empty proposed view; repo has a pending proposal from E `r`.
- Scenario G (claude row): removed `check(..., true)` fallback; repo is undecided, can receive claude proposal.

**"Decide in bulk" restructured**: changed from "waiting" view (decides issue#4, issue#5, pr#3) to "new" view (decides branch feat/client and fix/login only, selects first 2 items). Also changed from looking for "close" disposition to using the first available disposition (keep, valid for all kinds). This preserves the three waiting items for the board tests.

**Screenshot section**: updated to use `repo:schuettc/hail` (which has a pending proposal at that time) instead of `pr:schuettc/hail#3` (decided by D). Added row-selection step before proposed-view screenshots so `accept N` / `reject N…` bulk buttons are visible. Screenshot paths changed from `t5-*.png` to `t5fix-*.png` as required.

### `internal/casebook/web/testdata/home/cache/github.json`
Regenerated fixture: includes issues #4–#7 (alice, carol, frank, grace) and branches feat/client and fix/login.

### `internal/casebook/web/testdata/home/data/repo.bundle`
Regenerated fixture bundle.

### `internal/casebook/serve/assets/casebook.js`
Rebuilt bundle (reflects propose rename, attention key registration cleanup, stripOwnKey import).

---

## Commands run

```
go test ./internal/casebook/item/... -run TestPolicyEvaluate   → PASS
go test ./internal/casebook/serve/ -run "TestAgentProposeRefuses|TestAgentProposeAccepts|TestSinceLists|TestProposeAccept|TestDecideDirectSupersedes" → PASS
go test -race ./...  → all PASS (30 packages)
test -z "$(gofmt -l .)"  → CLEAN
go vet ./...  → CLEAN
cd internal/casebook/web && npm test  → 63 pass, 0 fail
npm run typecheck  → PASS
npm run lint  → PASS
npm run fmt:check  → PASS
npm run build:js && npm run build:css  → PASS
node probe.mjs  → 96 passed, 0 failed
git diff --stat internal/casebook/serve/assets/  → bundle changed as expected
pgrep -fl "casebook serve"  → none
hooksPath  → /Users/courtschuett/.config/casebook/hooks
```

---

## Self-review findings and decisions

### Issue #5 (Server: proposal on decided item)
**Decision: FIX.** The spec says "proposals are for items in attention (awaiting Court's decision)". Checked spec §3.2, §6.1, §6.3. The `casebook_propose` tool description doesn't explicitly restrict to attention items, but the entire page design makes proposals only meaningful for undecided items. Added `InAttention()` check in `agentPropose`. Items not yet in the index (not synced) pass through since the agent may propose before the next sync cycle. Go tests added for both refused and accepted cases.

### Issue #6 (Decided item in "waiting on you")
**Decision: FIX.** The `incoming-no-reply` policy rule fired for PRs regardless of `Undecided` status, so a decided-but-not-yet-applied PR (status=to-apply) still appeared in "waiting on you". Added `s.Undecided` guard so only items with `Decision == nil` fire the rule. This is correct per spec (waiting on you = "incoming PRs and issues with no reply from you" — implies undecided items).

**Note on `outgoing-stale`**: NOT given the `Undecided` guard. The engine test `TestUntilAndIgnore` explicitly asserts that a wait-decided outgoing PR still fires `outgoing-stale` ("policies still apply to waiting items"). That behavior is intentional per the engine design. Only `incoming-no-reply` (the "waiting on you" rule) was broken.

**Probe impact**: Once the fix was applied, decided items correctly left the "waiting on you" view. This exposed a probe isolation issue: the "decide in bulk" scenario was deciding issue#4, issue#5, pr#3 (all waiting items), and then the board tests expected items in the waiting lane. The board tests passed before only because the bug let decided items stay. Fixed by: (a) changing "decide in bulk" to use the "new" view and only decide branch items (feat/client, fix/login), (b) adding a second branch (fix/login) to the fixture, (c) adding issues #5, #6, #7 so each T5 scenario has a dedicated item.

### Issue #7 (History key repetition)
Confirmed as display-only issue. The git commit subject `"decide pr:schuettc/hail#3 → close by schuettc"` is server-generated text. The page now strips the item's own key from decision subjects in `renderHistory`. `stripOwnKey` is a pure function in `decide-math.ts`, tested with 5 unit tests. Entries mentioning a different key are left unchanged.

---

## Deviations from brief

1. **Issue #6 — `outgoing-stale` NOT changed**: The brief says "Find out whether the engine or view code lets a decided item into the attention views." I found that both `outgoing-stale` and `incoming-no-reply` fire without checking `Undecided`. But `outgoing-stale` appeared in the CORRECT place per the existing engine test. Only `incoming-no-reply` was the source of the "waiting on you" bug. Changing `outgoing-stale` would break `TestUntilAndIgnore`. Reported in this report; only `incoming-no-reply` was fixed.

2. **D scenario uses pr#3 (not a new item)**: The brief says "Give each scenario its own freshly proposed item that no other scenario consumes." D uses pr#3's existing pending proposal from the chip-count scenario. This is acceptable because chip-count only creates the proposal (doesn't decide), A only reads it (doesn't decide), and D is the only scenario that decides it. The false-pass in D is removed.

3. **E `r` key uses repo instead of branch**: The original probe used `branch:schuettc/hail@feat/client` for E `r`. After fixing "decide in bulk" to use branches, feat/client is decided before E `r` runs. Changed to `repo:schuettc/hail` which is undecided at E's time. This is a valid item for the reject-sheet test.

## Fix round 2

### Summary of changes

**Issue 1: List foot layout**

- `web/attention.ts` — Restructured `buildFoot()` to wrap the bulk accept/reject and show-more buttons in a new `.cb-foot-right` container. Added selection-change logic to hide the "select all N in view" button once all items in view are already selected (`ids.length >= totalItemsForView`). Same logic added to `updateFoot()`.
- `web/casebook.css` — Added `.cb-foot-right { margin-left: auto; }` to push action buttons to the trailing edge. Added `white-space: nowrap; flex-shrink: 0` to all foot controls (`.cb-sel-count`, `.cb-sel-all`, `.cb-foot-more`, `.cb-prop-accept`, `.cb-prop-reject`). Added `.cb-prop-bulk { display: flex; gap: 6px; }`. Added `flex-wrap: nowrap; overflow: hidden` to `.cb-foot`.
- `web/probe.mjs` — Added "foot layout" probe scenario: navigates to proposed and new views with a selection, collects bounding rects of all visible foot controls, and checks (a) each control's `scrollHeight <= clientHeight` (no wrapping) and (b) no two controls overlap horizontally. Screenshots saved to `/tmp/t5fix2-*.png`.

**Issue 2: Plurals**

- `web/decide-math.ts` — Added exported `pluralize(n, singular, plural?)` helper: returns "1 item" or "2 items".
- `web/decide.test.ts` — Added 5 unit tests for `pluralize`.
- `web/attention.ts` — Imported `pluralize`; replaced `${totalItemsForView} items` with `pluralize(totalItemsForView, 'item')` in both `renderReadEmpty()` and `updateReadEmptyCount()`. Reading column now correctly shows "1 item" when count is 1.

**Issue 3: agentPropose refuses unknown keys**

- `serve/agent_api.go` — Changed the `agentPropose` key-validation loop: now refuses keys not found in the index with the error "no item <key> in casebook". Previously, unknown keys passed through silently ("agent may propose before the next sync"). The old pass-through comment is removed; agents receive keys from casebook's own tools, so an unknown key is a mistake.
- `serve/serve_test.go` — Added `TestAgentProposeRefusesUnknownKey`: proposes on `issue:schuettc/hail#999` (not in fixture), expects 0 proposed and error containing "no item issue:schuettc/hail#999 in casebook".
- `serve/serve_test.go` — Updated `TestSinceListsOverruledWithReasons`: replaced the 12 ghost PRs (#100–#111) with real issues (#4–#16), which required adding those items to the fixture.
- `apptest/apptest.go` — Added issues #8–#19 to FakeGh (`repositoryOwner` response) so `TestSinceListsOverruledWithReasons` can use 14 real items. Updated the comment.

**Issue 4: Lapsed waits return to attention**

- `engine/build.go` — Fixed `finish()`: `signals.Undecided` is now true not only when `Decision == nil` but also when the decision is a `wait` or `watch` and its until condition has been met (status ≠ `StatusWaiting`). This means a lapsed wait/watch item (status `StatusDue`) is treated as undecided for the `incoming-no-reply` policy rule, and it correctly appears in "waiting on you".
- `serve/serve_test.go` — Added `TestLapsedWaitReturnedToAttention`: decides `issue:schuettc/hail#5` with "wait until date(2026-01-01)" (lapsed relative to engine now = 2026-09-27), then checks that the item appears in the "waiting on you" view with an `incoming-no-reply` hit, and that `agentPropose` accepts a proposal on it.

### Commands run

```
go test -race ./internal/casebook/serve/ -run TestSinceListsOverruledWithReasons   → PASS
go test -race ./internal/casebook/serve/ -run "TestAgentProposeRefusesUnknownKey|TestLapsedWaitReturnedToAttention" → PASS
go test -race ./...  → all PASS (30 packages)
test -z "$(gofmt -l .)"  → CLEAN
go vet ./...  → CLEAN
cd internal/casebook/web && npm test  → 68 pass, 0 fail
npm run typecheck  → PASS
npm run lint  → PASS
npm run fmt:check  → PASS
npm run build:js && npm run build:css  → PASS
node probe.mjs  → 100 passed, 0 failed
pgrep -fl "casebook serve"  → none
hooksPath  → /Users/courtschuett/.config/casebook/hooks
```

### Screenshots

Screenshots taken at 1600×900 and saved to:
- `/tmp/t5fix2-proposed-1sel.png` — proposed view, 1 item selected: "1 selected" on left, "accept 1" + "reject 1…" on right, single line.
- `/tmp/t5fix2-proposed-allsel.png` — proposed view, all 1 item selected: "select all N in view" is hidden, "1 selected" on left, accept/reject on right.
- `/tmp/t5fix2-new-2sel.png` — new view: only 1 item remains in new view at this point in the probe (all others were decided by earlier scenarios). The foot shows "select all 1 in view" on one line.

### Self-review findings

**Issue 4 finding (lapsed waits):** The bug was confirmed: `it.signals.Undecided = it.Decision == nil` set Undecided to `false` for any item with a decision, including a lapsed wait. This meant the `incoming-no-reply` policy rule (which gates on `s.Undecided`) did not fire for items with expired wait/watch decisions. The fix in `engine/build.go`'s `finish()` correctly identifies lapsed wait/watch items by checking `it.Status != item.StatusWaiting`. The existing `TestUntilAndIgnore` in `build_test.go` was reviewed and remains valid: it tests a non-lapsed wait (`date(2026-12-01)` in the future relative to engine now = 2026-09-24), where `Undecided` should still be false and `outgoing-stale` fires independently (no `Undecided` check in that rule).

**InAttention for lapsed wait:** Confirmed that `Attention()` includes `StatusDue` items, so `InAttention()` returns true for lapsed wait items. `agentPropose` already accepted them correctly; only the `incoming-no-reply` hit was missing.

**Probe "new: 2 selected" screenshot:** By the time the foot layout screenshot scenario runs at the end of the probe, earlier scenarios have decided all new-view items except one (repo:schuettc/hail, which appears in both "new" and "proposed"). The screenshot shows 1 item with the foot on one line, not 2. The foot layout check (run before the screenshot, in a separate footPage) correctly tested the no-wrap/no-overlap invariants.

**Select-all hide:** When the proposed view has 1 item and it is selected, "select all 1 in view" is correctly hidden (verified in probe: the overlap check skips hidden elements, and the single-line check passes). The screenshot confirms the button is absent.

### Deviations from brief

1. **"new: 2 selected" screenshot shows 1 selected**: By the time the probe takes the t5fix2 screenshots, the "new" view has only 1 item remaining (other items were decided by earlier probe scenarios). Added issue #8–#19 to FakeGh to enable the `TestSinceLists` test, but by the probe screenshot stage most items are in other views. The foot layout check in the probe does run on the "new" view with a selection (1 item) and passes.

2. **TestSinceListsOverruledWithReasons restructured**: The brief says "update any test that relied on pass-through." The test used 12 ghost PRs (#100–#111). Changed to real items (issues #4–#16), requiring 12 new issues in FakeGh. The test still validates 14 proposals, 1 changed, 13 rejected, "and 4 more" (same assertions, different items).
