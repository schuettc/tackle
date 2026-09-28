# Casebook Page – Fidelity Report
**Branch:** feat/casebook-page  
**Date:** 2026-09-27  
**Spec:** §3.3–3.4 of the 2026-09-26 casebook-workbench spec

---

## Summary

All seven visual-fidelity issues are fixed. 67/67 probes pass (8 new fidelity probes added). No casebook serve processes left running.

---

## Issues Fixed

### 1. Reading column — `.kit-doc` wrapper
**Problem:** `renderItem()` returned the article directly into `.kit-read`, so the item detail stretched flush to the list border with no centering or padding.

**Fix:** `item.ts` — wrap the returned `.cb-item` article in `h('div', { class: 'kit-doc' })` before returning. The kit's `.kit-doc` rule (`max-width: 640px; margin: 0 auto; padding: 44px 32px 60px;`) now applies.

**Probe result:** "reading document left edge is ≥ 40 px from list right edge" ✓, "reading document width is ≤ 700 px" ✓.

---

### 2. Kicker — `.kit-kick` class
**Problem:** Kicker `<p>` used only `.cb-kicker` which had no casebook-specific style, so it rendered as default body text (16px sans-serif).

**Fix:** `item.ts` — added `kit-kick` to the kicker element's class list. The kit rule `.kit-kick { font: 12px var(--kit-mono); color: var(--kit-muted); }` now applies.

**Probe result:** "kicker font-family is monospace" ✓.

---

### 3. Title — `.kit-h1` class
**Problem:** Title `<h1>` used only `.cb-title` which had no casebook-specific style. Browser default h1 rendering applied (2em, no letter-spacing, wrong weight).

**Fix:** `item.ts` — added `kit-h1` to the title element's class list. The kit rule `.kit-h1 { font-size: 30px; line-height: 1.2; letter-spacing: -.02em; font-weight: 700; margin: 10px 0 18px; }` now applies.

---

### 4. Facts line (already correct)
`facts()` from the kit was already used; `.kit-facts` styling applies correctly. No change needed.

---

### 5. Section labels — `.kit-label` class
**Problem:** DECIDE, EVIDENCE, and HISTORY headers used `<h2 class="cb-section-label">` which had no casebook style. They rendered as large `<h2>` headings (~24px) instead of small uppercase mono labels.

**Fix:** `item.ts` — changed all three to `h('h3', { class: 'kit-label' })`. The kit rule `.kit-eyebrow, .kit-label { font: 10.5px var(--kit-mono); letter-spacing: .08em; text-transform: uppercase; color: var(--kit-muted); }` now applies. The `.kit-doc .kit-label { margin: 30px 0 10px; }` rule also fires because these labels are now inside `.kit-doc`.

**Probe result:** "section label text-transform is uppercase" ✓, "section label font-family is monospace" ✓.

---

### 6. Decide buttons — `.kit-btn` + `danger` class
**Problem:** Disposition buttons in the item-detail decide section used `.cb-sheet-disp` only, which drew a slightly heavier border (1.5px) and lacked semantic kit class. Danger dispositions (close/delete/archive) used `.cb-sheet-disp--danger` which applies `color: var(--kit-danger)` — this still works — but the kit's own `.kit-btn.danger` pattern wasn't used.

**Fix:** `item.ts` — added `kit-btn` to every disposition button's class, and for danger dispositions also added `danger` (the kit class). The class string is now `'kit-btn cb-sheet-disp'` (and `' cb-sheet-disp--danger danger'` for danger). The `.cb-sheet-disp` class name is retained so the existing probe check (`.cb-decide .cb-sheet-disp`) still passes.

---

### 7. Brand mark — SVG namespace
**Problem:** The mark SVG was created with `h()` which calls `document.createElement(tag)`. `document.createElement('svg')` and `document.createElement('path')` etc. create HTML namespace elements, not SVG namespace elements. These elements have no intrinsic size and the browser does not render them as vector graphics, so the mark had zero width/height in the bar.

**Root cause confirmed:** the kit's `h()` function is `const el = document.createElement(tag)`. This is correct for HTML but wrong for SVG subtrees, which require `document.createElementNS('http://www.w3.org/2000/svg', tag)`.

**Fix:** `app.ts` — added a local `svgEl(tag, attrs, ...children)` helper that uses `document.createElementNS` for the SVG namespace, and replaced all `h('svg'|'rect'|'path', ...)` calls in the mark with `svgEl(...)` calls. The mark is now a proper `SVGSVGElement` with `SVGPathElement` children.

**Probe result:** "brand mark width > 0" ✓, "brand mark height > 0" ✓, "brand mark is visible" ✓.

---

### 8. List rows (already correct)
The kit's `list()` renders `.kit-row { display: grid; grid-template-columns: minmax(0,1fr) auto; }` with `meta` in the `auto` column — ages are right-aligned by the kit. Selected checkboxes use `.kit-row.sel .kit-box { background: var(--kit-signal); }` — signal colour on selection is handled by the kit. No changes needed.

---

### 9. List foot `.cb-foot` layout
**Problem:** The `.cb-foot` div wrapping the selection count and buttons had no display style, so its children (a `<span>` and two `<button>`s) flowed as inline elements but had no flex alignment.

**Fix:** `casebook.css` — added `.cb-foot { display: flex; align-items: center; gap: 8px; flex: 1; }`. The selection count appears on the left in signal colour (via `.cb-sel-count { color: var(--kit-signal); }`) when a selection is active; buttons follow in the same row.

---

## Probe Checks Added (fidelity — geometry and computed style)

Added a new scenario **"fidelity — geometry and computed style"** to `probe.mjs`, which:

1. Navigates to `#/item/issue:schuettc%2Fhail%234` at 1600×900.
2. Checks `.kit-doc` left edge is ≥ 40 px from `.kit-list` right edge.
3. Checks `.kit-doc` width is ≤ 700 px.
4. Checks `.cb-kicker` computed `fontFamily` contains a monospace keyword.
5. Checks first `.kit-label` in `.kit-read`: `textTransform === 'uppercase'` and `fontFamily` is monospace.
6. Checks `.kit-brand svg` has `getBoundingClientRect().width > 0` and `height > 0`.
7. Takes screenshots → `/tmp/fid-light-item.png`, `/tmp/fid-light-new.png`, `/tmp/fid-dark-new.png`, `/tmp/fid-dark-item.png`.

---

## Screenshot Comparison (vs. mock)

After-screenshots taken at 1600×900 (`/tmp/fid-light-item.png`, `/tmp/fid-dark-item.png`, `/tmp/fid-light-new.png`, `/tmp/fid-dark-new.png`).

**What matches the mock now:**
- The open-book SVG mark is visible left of "casebook" in the bar.
- Item detail is centred in the reading column with generous whitespace.
- Kicker (`issue · schuettc/hail#4 · …`) is small, muted mono.
- Title is a large (30px) bold sans heading.
- Facts line is the kit's `.kit-facts` muted-label / bold-value layout.
- DECIDE, EVIDENCE, HISTORY are small uppercase mono labels, not big headings.
- Disposition buttons are the kit's outlined mono buttons; close/delete/archive have danger (red) text.
- Selected-row checkboxes render in signal colour; ages are right-aligned.
- "N selected" in signal colour appears in the list foot when rows are selected.

**What still differs from the mock (belongs to later tasks):**
- The "pi proposes keep" proposal card at the top of the detail is absent (Task 5 — intentionally omitted).
- The agent dock / rail is empty (Tasks 6–7 — intentionally omitted).
- "make rule…" button in the foot is absent (Task 8 — intentionally omitted).

---

## Files Changed

| File | Change |
|------|--------|
| `internal/casebook/web/item.ts` | `.kit-doc` wrapper; `.kit-kick` / `.kit-h1` on kicker/title; `h3.kit-label` for section heads; `kit-btn` + `danger` on decide buttons |
| `internal/casebook/web/app.ts` | `svgEl()` helper; brand mark created with SVG namespace |
| `internal/casebook/web/casebook.css` | `.cb-foot { display: flex; … }` |
| `internal/casebook/web/probe.mjs` | Fidelity scenario + 8 new checks + 4 screenshots |
| `internal/casebook/serve/assets/casebook.js` | Rebuilt |
| `internal/casebook/serve/assets/casebook.css` | Rebuilt |

---

## Verification Commands Run

```
npm test            → 47/47 pass
npm run typecheck   → clean
npm run lint        → clean (0 warnings)
npm run fmt:check   → clean
npm run build       → 67/67 probe checks pass (8 new)

gofmt -l .          → (empty)
go vet ./...        → clean
go test -race ./... → all ok (103s for serve package)

pgrep -fl "casebook serve"  → (none)
git config core.hooksPath   → /Users/courtschuett/.config/casebook/hooks
```

---

## Deviations from Brief

None. All Tasks 1–4b items are addressed. Tasks 5, 6–7, 8 items are not stubbed as instructed.
