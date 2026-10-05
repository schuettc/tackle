# ---- .tools family standard: identical in every family repo ----------------
# `just verify` is exactly what CI runs: this tool's `prepare` (files the build
# needs, e.g. an embedded asset), the family gate (tools-actions go-ci, at the
# version .github/workflows/ci.yml pins), then this tool's `verify-extra`.
# The pre-push hook (lefthook.yml) runs it too, so local and CI never differ.
set shell := ["bash", "-euo", "pipefail", "-c"]

default: verify

verify: prepare gate verify-extra

# Everything: verify plus this tool's slow checks (browser, containers), which
# CI runs as their own required jobs.
verify-all: verify verify-slow

# The family Go gate: gofmt, vet, golangci-lint (family config), race tests,
# cross-build. Fetched once per tools-actions version into ~/.cache.
gate:
    #!/usr/bin/env bash
    set -euo pipefail
    v="$(grep -oE 'go-ci@v[0-9]+\.[0-9]+\.[0-9]+' .github/workflows/ci.yml | head -1 | cut -d@ -f2)"
    f="${XDG_CACHE_HOME:-$HOME/.cache}/tools-actions/$v/go-ci/local.sh"
    [ -f "$f" ] || { mkdir -p "$(dirname "$f")"; curl -fsSL "https://raw.githubusercontent.com/schuettc/tools-actions/$v/go-ci/local.sh" -o "$f"; }
    bash "$f"

fmt:
    gofmt -w $(git ls-files '*.go')

# Install the lefthook hooks into this clone's own .git/hooks (once per
# clone). A global core.hooksPath (casebook's recorder) forwards to them;
# plain `lefthook install` refuses to run under one.
hooks:
    #!/usr/bin/env bash
    set -euo pipefail
    d="$(cd "$(git rev-parse --git-common-dir)" && pwd)/hooks"
    git config --local core.hooksPath "$d"
    trap 'git config --local --unset core.hooksPath' EXIT
    lefthook install --force >/dev/null
    echo "lefthook hooks installed in $d"

# ---- tackle -----------------------------------------------------------------
# Files the gate needs that are not committed (built before the gate, locally
# and in CI). tackle has none: the casebook, cull and sift page bundles are
# committed and embedded.
prepare:

# Slow checks (a browser, a container) that CI runs as their own jobs: the
# casebook, cull and sift pages' browser probes (CI job `probe`).
verify-slow: casebook-probe cull-probe sift-probe

# Tool-specific checks beyond the gate (CI runs this too): the casebook, cull
# and sift pages' TypeScript gates and their committed bundles matching web/.
verify-extra: casebook-web casebook-bundle-fresh cull-web cull-bundle-fresh sift-web sift-bundle-fresh

# casebook's page (internal/casebook/web) is TypeScript built by esbuild into
# internal/casebook/serve/assets, which is COMMITTED and embedded, so node is
# dev-time only. Its node_modules can hold Go source (an npm package ships
# some); the family gate already skips anything under node_modules.
casebook_web := "internal/casebook/web"

# Install the page's dependencies once (CI caches node_modules keyed on the
# lockfile, so a hit skips `npm ci`), then copy the page kit's types out of the
# tools-common module go.mod pins (kit.d.ts is generated, not committed).
_casebook-web-deps:
    cd {{ casebook_web }} && { [ -d node_modules ] || npm ci; } && npm run --silent kit-types

# The page's TypeScript gate: unit tests, tsc against wire.d.ts, eslint,
# prettier. Pass-throughs: the commands live in web/package.json.
casebook-web: _casebook-web-deps
    cd {{ casebook_web }} && npm test
    cd {{ casebook_web }} && npm run --silent typecheck
    cd {{ casebook_web }} && npm run --silent lint
    cd {{ casebook_web }} && npm run --silent fmt:check

# Rebuild the committed bundle (without the browser probe `npm run build`
# ends with; that is verify-slow's job).
casebook-assets: _casebook-web-deps
    cd {{ casebook_web }} && npm run --silent build:js && npm run --silent build:css

# The committed bundle matches web/: nothing else notices a stale one.
casebook-bundle-fresh: casebook-assets
    git diff --exit-code -- internal/casebook/serve/assets/casebook.js internal/casebook/serve/assets/casebook.css \
      || { echo "the committed casebook bundle does not match web/: run 'just casebook-assets' and commit the result"; exit 1; }

# The page in a real headless chromium against a seeded casebook serve
# (--no-open, CASEBOOK_NO_BROWSER=1; see web/serve.mjs). KIT_BROWSER=required:
# a probe that finds no chromium fails rather than skipping. Install one with
# `cd internal/casebook/web && node node_modules/playwright-core/cli.js install chromium chromium-headless-shell`.
casebook-probe: _casebook-web-deps
    go build -o bin/casebook ./cmd/casebook
    cd {{ casebook_web }} && KIT_BROWSER=required CASEBOOK_BIN="$PWD/../../../bin/casebook" node probe.mjs

# One group of the probe (composer, keys, rules, apply, shell), on purpose: a partial run,
# which the full probe above refuses (a leftover PROBE_ONLY must not turn
# verify-slow into a partial run). Not part of any gate.
casebook-probe-only group: _casebook-web-deps
    go build -o bin/casebook ./cmd/casebook
    cd {{ casebook_web }} && PROBE_ONLY={{ group }} PROBE_PARTIAL=1 KIT_BROWSER=required CASEBOOK_BIN="$PWD/../../../bin/casebook" node probe.mjs

# cull's page (internal/cull/web) is TypeScript built by esbuild into
# internal/cull/serve/assets, which is COMMITTED and embedded, so node is
# dev-time only. Its node_modules can hold Go source (an npm package ships
# some); the family gate already skips anything under node_modules.
cull_web := "internal/cull/web"

# Install the page's dependencies once (CI caches node_modules keyed on the
# lockfile, so a hit skips `npm ci`); the postinstall copies the page kit's
# types out of the tools-common module go.mod pins (kit.d.ts is generated).
_cull-web-deps:
    cd {{ cull_web }} && { [ -d node_modules ] || npm ci; } && npm run --silent kit-types

# The page's TypeScript gate: unit tests, tsc against wire.d.ts, eslint,
# prettier. Pass-throughs: the commands live in web/package.json.
cull-web: _cull-web-deps
    cd {{ cull_web }} && npm test
    cd {{ cull_web }} && npm run --silent typecheck
    cd {{ cull_web }} && npm run --silent lint
    cd {{ cull_web }} && npm run --silent fmt:check

# Rebuild the committed bundle (build:js + build:css only).
cull-assets: _cull-web-deps
    cd {{ cull_web }} && npm run --silent build:js && npm run --silent build:css

# The committed bundle matches web/: nothing else notices a stale one.
cull-bundle-fresh: cull-assets
    git diff --exit-code -- internal/cull/serve/assets/cull.js internal/cull/serve/assets/cull.css \
      || { echo "the committed cull bundle does not match web/: run 'just cull-assets' and commit the result"; exit 1; }

# The page in a real headless chromium against a seeded cull serve (--no-open;
# web/serve-fixture.mjs builds the cull binary and the seeder itself).
# KIT_BROWSER=required: a probe that finds no chromium fails rather than
# skipping. Install one with
# `cd internal/cull/web && node node_modules/playwright-core/cli.js install chromium chromium-headless-shell`.
cull-probe: _cull-web-deps
    cd {{ cull_web }} && KIT_BROWSER=required npm run --silent probe

# sift's page (internal/sift/web) is TypeScript built by esbuild into
# internal/sift/serve/assets, which is COMMITTED and embedded, so node is
# dev-time only. Its node_modules can hold Go source (an npm package ships
# some); the family gate already skips anything under node_modules.
sift_web := "internal/sift/web"

# Install the page's dependencies once (CI caches node_modules keyed on the
# lockfile, so a hit skips `npm ci`); the postinstall copies the page kit's
# types out of the tools-common module go.mod pins (kit.d.ts is generated).
_sift-web-deps:
    cd {{ sift_web }} && { [ -d node_modules ] || npm ci; } && npm run --silent kit-types

# The page's TypeScript gate: unit tests, tsc against wire.d.ts, eslint,
# prettier. Pass-throughs: the commands live in web/package.json.
sift-web: _sift-web-deps
    cd {{ sift_web }} && npm test
    cd {{ sift_web }} && npm run --silent typecheck
    cd {{ sift_web }} && npm run --silent lint
    cd {{ sift_web }} && npm run --silent fmt:check

# Rebuild the committed bundle (build:js + build:css only).
sift-assets: _sift-web-deps
    cd {{ sift_web }} && npm run --silent build:js && npm run --silent build:css

# The committed bundle matches web/: nothing else notices a stale one.
sift-bundle-fresh: sift-assets
    git diff --exit-code -- internal/sift/serve/assets/sift.js internal/sift/serve/assets/sift.css \
      || { echo "the committed sift bundle does not match web/: run 'just sift-assets' and commit the result"; exit 1; }

# The page in a real headless chromium against a seeded sift serve (--no-open;
# web/serve-fixture.mjs builds the sift binary and the seeder itself): the
# views, groups, the wrapping prose view, accept / edit / reject, undo, Send,
# and a 653-row round, asserted by visibility and geometry. KIT_BROWSER=required:
# a probe that finds no chromium fails rather than skipping. Install one with
# `cd internal/sift/web && node node_modules/playwright-core/cli.js install chromium chromium-headless-shell`.
sift-probe: _sift-web-deps
    cd {{ sift_web }} && KIT_BROWSER=required npm run --silent probe
