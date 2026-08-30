# tackle Monorepo Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert the standalone `schuettc/scratch` repo into the `schuettc/tackle` Go monorepo — a home for small workshop tools — with scratch as its first `cmd/` binary, shared CI, and a prefixed-tag release pipeline that publishes assets named to the locked family distribution contract.

**Architecture:** Rename the existing repo (preserving history natively, no subtree merge) and restructure it into a single Go module `github.com/schuettc/tackle` with each tool under `cmd/<tool>/` and shared code under `internal/`. CI is one whole-repo `verify.yml`. Releases fire on **prefixed per-tool tags** (`scratch/vX.Y.Z`, later `proj/vX.Y.Z`) so each tool releases independently without disturbing the others; each release cross-compiles one tool and attaches `<tool>_<os>_<arch>.tar.gz` + shasum sidecars as GitHub release assets (which double as the invisible durable backing store).

**Tech Stack:** Go 1.26, Bubble Tea/Bubbles/Lipgloss (existing scratch deps), GitHub Actions, `gh` CLI.

**Spec:** `tools-ops/docs/superpowers/specs/2026-08-27-tools-standardization.md` (family distribution standard — the URL scheme + wire-format contract this plan's release assets must satisfy). Tackle-specific decisions are captured in Global Constraints below and in the muster coordination threads #326–#329.

## Global Constraints

- **Module path:** single Go module `github.com/schuettc/tackle`. One `go.mod` at repo root. Shared code in `internal/`; each user-facing tool in `cmd/<tool>/`.
- **Asset naming contract (verbatim from the family standard):** release assets are `<tool>_<os>_<arch>.tar.gz` with `<os>` ∈ {`darwin`,`linux`}, `<arch>` ∈ {`arm64`,`amd64`} — Go `GOOS`/`GOARCH` verbatim. Each tarball has a sibling `<tool>_<os>_<arch>.tar.gz.sha256` in **shasum format** (`<hex>  <filename>`, two spaces).
- **Version token:** bare semver, **no leading `v`** (e.g. `0.15.1`). Git tags are `<tool>/v<semver>` (v-prefixed); the release workflow **strips the `v`** when composing versions/filenames.
- **Release trigger:** prefixed tags only — `<tool>/v*`. A push of `scratch/v1.2.3` builds and releases **only** scratch. There is no push-to-main auto-release (scratch's old model is removed).
- **`go install` is a dev/fallback path only.** Because the module uses prefixed tags (not plain `vX.Y.Z`), `@latest` will not resolve them; the supported dev install is `go install github.com/schuettc/tackle/cmd/<tool>@main`. Primary distribution is the GitHub release binaries now, and the domain `/dl/` path once tackle.tools is live (separate follow-up).
- **DEFERRED — not in this plan:** (1) the S3-upload step in `release.yml` (blocked on tools-ops shipping the Downloads bucket + `environment:release` role and muster proving the recipe on thread #328); (2) the `install.sh` served from tackle.tools (blocked on the domain going live — it is at stage `zone`); (3) the **proj Go rewrite** (separate larger plan — its per-project tmux-server model and `switch-client`/detach-exec reattach need their own design pass).
- **First-tool reality:** at the end of this plan the repo contains exactly one `cmd/` tool: `cmd/scratch`. `cmd/proj` arrives in a later plan. The release workflow is written tool-generic (parameterized on the tag prefix) so proj needs no workflow change.

---

## File Structure

Target layout after this plan:

```
tackle/                              (was scratch/; repo renamed schuettc/scratch -> schuettc/tackle)
  go.mod                             module github.com/schuettc/tackle
  go.sum
  cmd/
    scratch/
      main.go                        (moved from repo-root main.go)
      main_test.go                   (moved from repo-root main_test.go)
  internal/
    notes/                           (unchanged; import paths updated)
    tui/                             (unchanged; import paths updated)
  .github/workflows/
    verify.yml                       (new — whole-repo gate: fmt/vet/lint/test/build-all)
    release.yml                      (replaces scratch's push-to-main workflow)
  README.md                          (rewritten for the monorepo)
  CHANGELOG.md                       (kept as-is)
  docs/superpowers/plans/2026-08-29-tackle-monorepo-foundation.md   (this file)
```

Files deliberately **removed**: repo-root `main.go`/`main_test.go` (moved into `cmd/scratch/`), scratch's old `_config.yml` and stray `*.patch` files if present (housekeeping, Task 6).

---

### Task 1: Rename the repository (GitHub + local)

**Files:** none edited — this is a repo/remote operation. It is a shared-state, GitHub-visible action; run the `gh repo rename` step only after confirming with Court.

**Interfaces:**
- Produces: the repo is reachable as `github.com/schuettc/tackle`; the local working copy's `origin` points at the renamed repo; the local directory is `tackle/`. GitHub auto-redirects the old `schuettc/scratch` URL.

- [ ] **Step 1: Confirm working tree is clean and pushed**

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace/scratch
git status --porcelain && git log origin/main..HEAD --oneline
```
Expected: no output from either command (clean, nothing unpushed). If there is uncommitted or unpushed work, stop and resolve it before renaming.

- [ ] **Step 2: Rename the GitHub repo** (GitHub-visible — confirm with Court first)

Run:
```bash
gh repo rename tackle --repo schuettc/scratch
```
Expected: `✓ Renamed repository schuettc/tackle`. GitHub now redirects `schuettc/scratch` → `schuettc/tackle`.

- [ ] **Step 3: Repoint the local remote and rename the local dir**

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace
git -C scratch remote set-url origin git@github.com:schuettc/tackle.git
mv scratch tackle
git -C tackle remote -v
```
Expected: both fetch/push URLs read `git@github.com:schuettc/tackle.git`.

- [ ] **Step 4: Verify the rename end-to-end**

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace/tackle
git fetch origin && git status
```
Expected: `git fetch` succeeds against the new URL; branch is up to date with `origin/main`.

- [ ] **Step 5: Commit the plan doc location** (the plan rode along in the dir move; no code change yet)

No commit needed in this task — the rename is not a git commit. Proceed to Task 2.

---

### Task 2: Restructure into a single-module monorepo (`cmd/scratch`)

**Files:**
- Modify: `go.mod` (module path)
- Move: `main.go` → `cmd/scratch/main.go`
- Move: `main_test.go` → `cmd/scratch/main_test.go`
- Modify: import paths inside `cmd/scratch/main.go` and any file under `internal/` that self-imports the module path

**Interfaces:**
- Consumes: nothing (first structural task).
- Produces: `go build ./...` and `go test ./...` pass from repo root; the scratch binary builds as `go build ./cmd/scratch`. Module is `github.com/schuettc/tackle`; scratch's internal imports are `github.com/schuettc/tackle/internal/notes` and `.../internal/tui`.

- [ ] **Step 1: Confirm the current build is green (baseline)**

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace/tackle
go build ./... && go test ./...
```
Expected: PASS. This is the pre-restructure baseline — if it fails, fix or report before moving files.

- [ ] **Step 2: Change the module path**

Edit `go.mod` line 1:
```
module github.com/schuettc/tackle
```
(was `module github.com/schuettc/scratch`; leave the `go 1.26.4` line and all `require` blocks unchanged.)

- [ ] **Step 3: Move the command into `cmd/scratch/`**

Run:
```bash
mkdir -p cmd/scratch
git mv main.go cmd/scratch/main.go
git mv main_test.go cmd/scratch/main_test.go
```

- [ ] **Step 4: Rewrite the module self-imports**

Run:
```bash
grep -rl 'github.com/schuettc/scratch' . --include='*.go'
```
Then in every file listed (expected: `cmd/scratch/main.go`, and any `internal/**/*.go` that imports a sibling internal package), replace the import prefix:
```bash
grep -rl 'github.com/schuettc/scratch' . --include='*.go' \
  | xargs sed -i '' 's#github.com/schuettc/scratch#github.com/schuettc/tackle#g'
```
(`sed -i ''` is the BSD/macOS form — no backup file.)

- [ ] **Step 5: Verify build + tests from the new layout**

Run:
```bash
go build ./cmd/scratch && go test ./...
go vet ./...
gofmt -l .
```
Expected: build succeeds, tests PASS, `go vet` clean, `gofmt -l` prints nothing (no unformatted files).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: restructure into tackle monorepo, scratch -> cmd/scratch"
```

---

### Task 3: Add the whole-repo CI gate (`verify.yml`)

**Files:**
- Create: `.github/workflows/verify.yml`

**Interfaces:**
- Consumes: the `cmd/scratch` + `internal/` layout from Task 2.
- Produces: a `verify` workflow that runs on PRs and pushes to `main`, gating fmt/vet/lint/test/build-all. Later tools (`cmd/proj`) are covered automatically by `./...`.

- [ ] **Step 1: Write `verify.yml`**

Create `.github/workflows/verify.yml`:
```yaml
name: verify

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  verify:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: gofmt
        run: |
          unformatted="$(gofmt -l .)"
          if [ -n "$unformatted" ]; then
            echo "These files are not gofmt-clean:"; echo "$unformatted"; exit 1
          fi
      - name: vet
        run: go vet ./...
      - name: build all commands
        run: go build ./...
      - name: test
        run: go test -race ./...
```
(macOS runner because scratch is a terminal/TUI tool exercised on darwin; `-race` matches family practice. No third-party linter is added here — `gofmt` + `go vet` are the gate, consistent with keeping the toolchain minimal. If a golangci-lint step is wanted later it is an additive change.)

- [ ] **Step 2: Validate the workflow locally**

Run:
```bash
gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```
Expected: all clean/PASS — this mirrors exactly what the workflow runs.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/verify.yml
git commit -m "ci: add whole-repo verify gate (fmt/vet/build/test)"
```

---

### Task 4: Replace the release workflow with prefixed-tag, contract-named releases

**Files:**
- Create: `.github/workflows/release.yml`
- Delete: the old scratch push-to-main release workflow if its filename differs from `release.yml` (scratch's was `.github/workflows/release.yml`, so this is an overwrite).

**Interfaces:**
- Consumes: the `cmd/<tool>` layout; the asset-naming + version-token constraints from Global Constraints.
- Produces: on a pushed tag `<tool>/v<semver>`, a GitHub release named `<tool>/v<semver>` whose assets are `<tool>_<os>_<arch>.tar.gz` (4 targets) + matching shasum-format `.sha256` sidecars. Tool-generic — proj reuses it unchanged. **No S3 step yet** (deferred; see Global Constraints).

- [ ] **Step 1: Write `release.yml`**

Create `.github/workflows/release.yml` (overwriting scratch's old push-to-main workflow):
```yaml
name: release

# Releases fire on a PREFIXED tag: <tool>/v<semver> (e.g. scratch/v0.15.1).
# Pushing one tag builds and releases exactly one tool — the others are never
# touched. The tag prefix names the tool; the semver is stripped of its leading
# v for the asset filenames and the (future) /dl path + latest pointer.
#
# Assets follow the family distribution contract:
#   <tool>_<os>_<arch>.tar.gz  +  <tool>_<os>_<arch>.tar.gz.sha256 (shasum format)
#   <os> in {darwin,linux}, <arch> in {arm64,amd64}  (Go GOOS/GOARCH)
# The GitHub release is also the invisible durable backing store for the binaries.
#
# NOT YET HERE (deferred): uploading these same assets to the tools-ops Downloads
# S3 bucket under /dl/<tool>/<version>/ and updating /dl/<tool>/latest. That step
# is added once tools-ops reports the bucket + environment:release role and muster
# proves the recipe (muster thread #328). When added, the upload job declares
# `environment: release` to assume the PutObject role.

on:
  push:
    tags:
      - "*/v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Parse tool + version from the tag
        id: tag
        run: |
          set -euo pipefail
          ref="${GITHUB_REF#refs/tags/}"        # e.g. scratch/v0.15.1
          tool="${ref%%/*}"                      # scratch
          vertag="${ref#*/}"                     # v0.15.1
          version="${vertag#v}"                  # 0.15.1
          if [ ! -d "cmd/$tool" ]; then
            echo "tag names tool '$tool' but cmd/$tool does not exist" >&2
            exit 1
          fi
          echo "tool=$tool"       >> "$GITHUB_OUTPUT"
          echo "version=$version" >> "$GITHUB_OUTPUT"
          echo "Releasing $tool $version (tag $ref)"

      - name: Cross-compile + package the four targets
        id: build
        env:
          TOOL: ${{ steps.tag.outputs.tool }}
        run: |
          set -euo pipefail
          mkdir -p dist
          for os in darwin linux; do
            for arch in arm64 amd64; do
              out="dist/${TOOL}_${os}_${arch}"
              GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
                go build -trimpath -o "$out/$TOOL" "./cmd/$TOOL"
              tarball="dist/${TOOL}_${os}_${arch}.tar.gz"
              tar -C "$out" -czf "$tarball" "$TOOL"
              ( cd dist && shasum -a 256 "${TOOL}_${os}_${arch}.tar.gz" \
                  > "${TOOL}_${os}_${arch}.tar.gz.sha256" )
              rm -rf "$out"
            done
          done
          ls -1 dist

      - name: Create the GitHub release
        env:
          GH_TOKEN: ${{ github.token }}
          TOOL: ${{ steps.tag.outputs.tool }}
          VERSION: ${{ steps.tag.outputs.version }}
        run: |
          set -euo pipefail
          gh release create "${GITHUB_REF#refs/tags/}" \
            --repo "$GITHUB_REPOSITORY" \
            --target "$GITHUB_SHA" \
            --title "$TOOL $VERSION" \
            --generate-notes \
            dist/${TOOL}_*.tar.gz dist/${TOOL}_*.tar.gz.sha256
```

- [ ] **Step 2: Dry-run the build matrix locally** (no release, just prove packaging + naming)

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace/tackle
rm -rf /tmp/tackle-dist && mkdir -p /tmp/tackle-dist
TOOL=scratch
for os in darwin linux; do for arch in arm64 amd64; do
  out="/tmp/tackle-dist/${TOOL}_${os}_${arch}"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -o "$out/$TOOL" "./cmd/$TOOL"
  tar -C "$out" -czf "/tmp/tackle-dist/${TOOL}_${os}_${arch}.tar.gz" "$TOOL"
  ( cd /tmp/tackle-dist && shasum -a 256 "${TOOL}_${os}_${arch}.tar.gz" > "${TOOL}_${os}_${arch}.tar.gz.sha256" )
  rm -rf "$out"
done; done
ls -1 /tmp/tackle-dist
cat /tmp/tackle-dist/scratch_darwin_arm64.tar.gz.sha256
```
Expected: eight files — four `scratch_{darwin,linux}_{arm64,amd64}.tar.gz` and four `.sha256`. The sha256 file content is `<64-hex>  scratch_darwin_arm64.tar.gz` (two spaces). Confirm `sha256sum -c` acceptance:
```bash
cd /tmp/tackle-dist && shasum -a 256 -c scratch_darwin_arm64.tar.gz.sha256
```
Expected: `scratch_darwin_arm64.tar.gz: OK`.

- [ ] **Step 3: Verify a target binary actually runs** (darwin/arm64 = local arch on this machine)

Run:
```bash
mkdir -p /tmp/tackle-smoke && tar -C /tmp/tackle-smoke -xzf /tmp/tackle-dist/scratch_darwin_arm64.tar.gz
/tmp/tackle-smoke/scratch --help 2>&1 | head -3 || /tmp/tackle-smoke/scratch 2>&1 | head -3
```
Expected: scratch's usage/output prints (exact text per scratch's CLI) — the extracted binary is runnable.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: prefixed-tag release workflow with contract-named assets"
```

- [ ] **Step 5: Do NOT push a real tag yet**

Leave the first real `scratch/v*` tag until after Task 6 (README + housekeeping) and Court's go-ahead. Cutting a release is a GitHub-visible action — confirm before tagging. When ready the command is:
```bash
# (later, with confirmation) pick the next version, then:
git tag scratch/v0.16.0 && git push origin scratch/v0.16.0
```

---

### Task 5: Update the dotfiles install reference

**Files:**
- Modify: `~/dotfiles/packages/terminal/pkg.sh` (the `go install github.com/schuettc/scratch@latest` line + its local-build fallback path)

**Interfaces:**
- Consumes: the new module path `github.com/schuettc/tackle/cmd/scratch`.
- Produces: dotfiles installs scratch from the tackle module. This touches the **dotfiles** repo, which the `tools-workspace/dotfiles-migration` session is actively converting to kempt — coordinate over muster before/at commit so the two changes don't collide.

- [ ] **Step 1: Locate the current reference**

Run:
```bash
grep -rn 'schuettc/scratch' ~/dotfiles/packages/terminal/pkg.sh
```
Expected: the `go install github.com/schuettc/scratch@latest` invocation and any local-build fallback that `cd`s into a scratch checkout.

- [ ] **Step 2: Update the go install path**

In `~/dotfiles/packages/terminal/pkg.sh`, change the install line from:
```sh
go install github.com/schuettc/scratch@latest
```
to (note: `@main`, because prefixed release tags are not `go install @latest`-resolvable on the root module — see Global Constraints):
```sh
go install github.com/schuettc/tackle/cmd/scratch@main
```
If a local-build fallback references the old repo dir `~/GitHub/schuettc/scratch`, update it to `~/GitHub/schuettc/tackle` and build `./cmd/scratch`.

- [ ] **Step 3: Verify the new install path resolves**

Run:
```bash
go install github.com/schuettc/tackle/cmd/scratch@main && command -v scratch
```
Expected: install succeeds and `scratch` is on `PATH` (in `$(go env GOPATH)/bin`). If GitHub's rename redirect hasn't propagated to the Go module proxy yet, this may need a minute or `GOPROXY=direct`.

- [ ] **Step 4: Coordinate + commit** (dotfiles is another session's active repo)

Send a heads-up to `tools-workspace/dotfiles-migration` over muster that the scratch install line is moving to the tackle module path, then commit in dotfiles:
```bash
git -C ~/dotfiles add packages/terminal/pkg.sh
git -C ~/dotfiles commit -m "terminal: install scratch from tackle monorepo (github.com/schuettc/tackle/cmd/scratch)"
```

---

### Task 6: README, housekeeping, and workspace registration

**Files:**
- Rewrite: `README.md` (monorepo intro)
- Remove: stray non-source files if present (`_config.yml`, `*.patch`)
- Modify: `../CLAUDE.md` (workspace members table — add tackle)

**Interfaces:**
- Consumes: the finished monorepo layout.
- Produces: a monorepo README documenting install + the per-tool release convention; the workspace CLAUDE.md lists tackle as a member.

- [ ] **Step 1: Rewrite `README.md`**

Replace `README.md` with:
```markdown
# tackle

Small workshop tools — TUIs you run in your own terminal. A `.tools` family
member. One Go monorepo; each tool is an independent binary under `cmd/`.

## Tools

- **scratch** — per-directory scratch notes with a live TUI.
- *(proj — project/tmux launcher — arriving soon.)*

## Install

Prebuilt binaries are published per tool on each GitHub release
(`<tool>_<os>_<arch>.tar.gz`). Once tackle.tools is live, the supported install is:

    curl -fsSL https://tackle.tools/install.sh | sh -s scratch

Dev/fallback (requires a Go toolchain):

    go install github.com/schuettc/tackle/cmd/scratch@main

## Releasing

Each tool releases independently via a prefixed tag:

    git tag scratch/v0.16.0 && git push origin scratch/v0.16.0

That builds and publishes **only** scratch (darwin/linux × arm64/amd64), leaving
every other tool untouched.

## Develop

    go build ./cmd/scratch
    go test ./...
```

- [ ] **Step 2: Remove stray non-source files**

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace/tackle
ls _config.yml *.patch 2>/dev/null && git rm -f _config.yml *.patch 2>/dev/null || echo "nothing stray to remove"
```
(Only removes files that exist; the scratch checkout had a `_config.yml` and two `*.patch` files — confirm they are not wanted before removing.)

- [ ] **Step 3: Register tackle in the workspace members table**

Edit `/Users/courtschuett/GitHub/schuettc/tools-workspace/CLAUDE.md`, adding a row to the Members table:
```
| `tackle/` | monorepo home for small workshop TUIs (tackle.tools) — scratch + proj |
```

- [ ] **Step 4: Final verify + commit**

Run:
```bash
cd /Users/courtschuett/GitHub/schuettc/tools-workspace/tackle
gofmt -l . && go vet ./... && go build ./... && go test -race ./...
git add -A && git commit -m "docs: monorepo README + housekeeping"
git -C .. add CLAUDE.md && git -C .. commit -m "workspace: register tackle member repo"
```
Expected: all checks clean; two commits (one in tackle, one in the workspace root).

- [ ] **Step 5: Push tackle**

```bash
git -C /Users/courtschuett/GitHub/schuettc/tools-workspace/tackle push origin main
```
Expected: push succeeds to `schuettc/tackle`; the `verify` workflow runs green on GitHub.

---

## Deferred follow-ups (tracked, not in this plan)

1. **S3 upload step in `release.yml`** — add once tools-ops reports the Downloads bucket name + region + the `environment:release` OIDC subject and muster proves the recipe (thread #328). The upload job declares `environment: release`, writes the same `dist/` assets to `s3://<bucket>/dl/<tool>/<version>/`, updates `/dl/<tool>/latest` (bare semver, plain text, only on a real non-prerelease release), and invalidates the latest pointer only. Per-**tool** paths (not per-product) — see thread #328.
2. **`install.sh` served from tackle.tools** — lift muster's proven `install.sh` (thread #328), parameterized to take a tool name (`sh -s <tool>`); lands as `site/` content once tackle.tools flips `zone → staged → live`.
3. **proj Go rewrite (`cmd/proj`)** — separate plan. Must design first: per-project tmux servers (`tmux -L proj-<name>`), cross-server jumps via `tmux detach -E 'tmux -L <srv> attach ...'` vs same-server `switch-client`, the `switch-client`-from-a-child-process reattach approach, server-cwd-poisoning avoidance (create sessions from `$HOME`), agent auto-launch (`--claude`/`--cursor`/`--pi`), roots parsing (`~/.config/proj/roots`, `project:` prefix), two-screen picker (Bubble Tea), and the precmd autojoin hook. The shell surface shrinks to a thin `proj()` shim + the autojoin hook.

## Self-Review

- **Spec coverage:** family asset-naming + version-token + checksum-format contract → Task 4 (build/package steps) and Global Constraints. Prefixed-tag independent release → Task 4. Single-module monorepo → Task 2. Repo rename preserving history → Task 1. dotfiles reference → Task 5. Workspace registration → Task 6. The S3/install.sh/proj items are explicitly deferred with reasons.
- **Placeholder scan:** no TBD/TODO; all workflow YAML and shell are literal; the one intentionally-not-run action (first real tag push) is called out as a confirm-gated step with the exact command.
- **Type consistency:** module path `github.com/schuettc/tackle` and internal import prefixes `.../internal/notes`, `.../internal/tui` are used identically in Tasks 2 and 5; asset filename pattern `<tool>_<os>_<arch>.tar.gz` + `.sha256` shasum sidecar is identical in Global Constraints, Task 4 build step, and Task 4 dry-run verification.
