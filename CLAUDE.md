# tackle

- Check casebook page changes after each task with a headless screenshot: serve the page with `startServe` from `internal/casebook/web/serve.mjs` and capture it with playwright-core. Never use the maintainer's own browser for this.
- While iterating, run only the affected test or probe scenario; run `just verify-slow` once before opening the PR, not after every fix (it takes about 10 minutes).
