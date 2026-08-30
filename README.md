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
