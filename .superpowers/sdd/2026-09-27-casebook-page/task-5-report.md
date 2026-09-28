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
