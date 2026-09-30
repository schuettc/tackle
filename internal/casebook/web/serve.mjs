// serve.mjs — spawn a seeded casebook serve for the probe.
//
// Sets up a fresh CASEBOOK_HOME from the committed testdata/home fixture,
// starts `casebook serve --foreground --no-open`, waits for its advert,
// and returns {url, base, token, stop}.
//
// BROWSER SAFETY: serve is always started with --no-open and with
// CASEBOOK_NO_BROWSER=1, which triggers the test seam in cli/workbench.go
// that replaces openBrowser with a silent no-op.  No test or probe path
// can reach localweb.OpenBrowser.

import { execSync, spawn } from 'node:child_process';
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '../../..');

// The args we always pass to casebook serve. Never remove --no-open.
const SERVE_ARGS = ['serve', '--foreground', '--no-open'];

// Hard invariant: verify at module load that the args include --no-open.
// This ensures serve.mjs can never be accidentally changed to open a browser.
if (!SERVE_ARGS.includes('--no-open')) {
  throw new Error(
    'serve.mjs invariant violated: SERVE_ARGS must include --no-open',
  );
}

// Find the casebook binary: CASEBOOK_BIN env var (CI pre-builds and sets
// this), or rebuild from source so the binary always embeds the latest assets.
// A probe that starts more than one serve builds the binary once.
let builtBinary = '';
function findBinary() {
  if (process.env.CASEBOOK_BIN) return process.env.CASEBOOK_BIN;
  if (builtBinary) return builtBinary;
  const built = join(repoRoot, 'bin', 'casebook');
  // Always rebuild: the probe tests the current source, and the binary embeds
  // internal/casebook/serve/assets/ which was just rewritten by build:js/css.
  console.error('[serve.mjs] building casebook binary…');
  execSync(`go build -o "${built}" ./cmd/casebook`, {
    cwd: repoRoot,
    stdio: 'inherit',
  });
  builtBinary = built;
  return built;
}

// Set up a fresh CASEBOOK_HOME from the committed testdata/home fixture.
// The fixture contains:
//   data/repo.bundle   – git bundle of the casebook-data repo
//   cache/github.json  – GitHub observation cache (may be empty)
// Config is written fresh so paths are correct for this machine.
//
// seedRepos adds repos (the cache's repo shape: {repo, prs, issues, …}) to the
// fixture's GitHub cache, so a scenario can own Attention items no other
// scenario touches. seedMachines adds other machines' snapshots (the
// machines/<machine>.json shape: {version, machine, roots, clones}) to the
// casebook-data repo, committed, so a scenario can own local branches (with
// their landed verdicts) no other scenario touches.
let homes = 0;
//
// seedFiles adds any other files to the casebook-data repo, committed with
// the machines ({'rules/x.toml': '…'}), e.g. a rule file as a person wrote it.
function setupHome(
  seedRepos = [],
  seedMachines = [],
  seedFiles = {},
  seedClones = [],
  syncInterval = '30m',
) {
  const fixture = join(here, 'testdata', 'home');
  const home =
    realpathSync(tmpdir()) + `/casebook-probe-${process.pid}-${homes++}`;

  mkdirSync(join(home, 'config'), { recursive: true });
  mkdirSync(join(home, 'data'), { recursive: true });
  mkdirSync(join(home, 'state', 'live'), { recursive: true });
  writeFakeGh(home);

  // Clone the bundle to data/repo.
  const bundlePath = join(fixture, 'data', 'repo.bundle');
  const repoPath = join(home, 'data', 'repo');
  execSync(`git clone -q "${bundlePath}" "${repoPath}"`, { stdio: 'pipe' });
  // Real clones on this machine ("probe"), with landed branches, under the
  // home: the casebook lane's git runs in them and nowhere else.
  if (seedClones.length) {
    const snap = join(repoPath, 'machines', 'probe.json');
    const probe = existsSync(snap)
      ? JSON.parse(readFileSync(snap, 'utf8'))
      : { version: 1, machine: 'probe', roots: [], clones: [] };
    probe.roots = [...(probe.roots ?? []), join(home, 'clones')];
    probe.clones = [
      ...(probe.clones ?? []),
      ...seedClones.map((c) => makeClone(home, c)),
    ];
    seedMachines = [...seedMachines, probe];
  }
  const files = Object.entries(seedFiles);
  if (seedMachines.length || files.length) {
    for (const snap of seedMachines) {
      writeFileSync(
        join(repoPath, 'machines', `${snap.machine}.json`),
        JSON.stringify(snap, null, 2) + '\n',
      );
    }
    for (const [rel, text] of files) {
      mkdirSync(dirname(join(repoPath, rel)), { recursive: true });
      writeFileSync(join(repoPath, rel), text);
    }
    const git = (args) =>
      execSync(`git ${args}`, {
        cwd: repoPath,
        stdio: 'pipe',
        env: {
          ...process.env,
          GIT_AUTHOR_NAME: 'probe',
          GIT_AUTHOR_EMAIL: 'probe@example.com',
          GIT_COMMITTER_NAME: 'probe',
          GIT_COMMITTER_EMAIL: 'probe@example.com',
          GIT_CONFIG_GLOBAL: '/dev/null',
          GIT_CONFIG_NOSYSTEM: '1',
        },
      });
    git('add -A');
    git('commit -q -m "probe: seed machine snapshots"');
  }

  // Copy the github cache to the state directory, which is where CachePath()
  // looks: $CASEBOOK_HOME/state/github.json (tools.StateDir("casebook") joins
  // $CASEBOOK_HOME with "state").
  const srcCache = join(fixture, 'cache', 'github.json');
  if (existsSync(srcCache)) {
    cpSync(srcCache, join(home, 'state', 'github.json'));
  }
  if (seedRepos.length) {
    const cachePath = join(home, 'state', 'github.json');
    const cache = JSON.parse(readFileSync(cachePath, 'utf8'));
    cache.owners.schuettc.repos.push(...seedRepos);
    for (const r of seedRepos) {
      cache.merged_prs[r.repo] = { fetched: true, at: cache.authored_at };
    }
    writeFileSync(cachePath, JSON.stringify(cache, null, 2));
  }

  // Write a minimal config.toml. casebook_repo is left blank so it defaults
  // to CASEBOOK_HOME/data/repo (the clone above).
  writeFileSync(
    join(home, 'config', 'config.toml'),
    [
      'machine = "probe"',
      'user = "schuettc"',
      'casebook_remote = ""',
      'roots = []',
      `sync_interval = "${syncInterval}"`,
    ].join('\n') + '\n',
    { mode: 0o600 },
  );

  return home;
}

// ---- hermetic executors ------------------------------------------------------
//
// serve runs gh for one thing in these probes: verifying an agent-lane step
// the agent reported (a fresh `gh pr view`, `gh repo view`). The Go tests
// give serve apptest.FakeGh for it; the binary has no such seam, so every
// serve the probe starts finds a fake gh first on its PATH, answering the
// verification reads the way apptest.FakeGh does and refusing anything else.
// Every call is logged (ghCalls()), so a probe can say none went elsewhere.
// Nothing the probe starts can reach GitHub.
//
// git is real, and hermetic: no global or system config (so no hooks of
// Court's), and every repository it touches is under the probe's temp home.
const FAKE_GH = `#!/bin/sh
printf '%s\\n' "$*" >> "$CASEBOOK_HOME/gh-calls.log"
case "$*" in
  "pr view "*"--json state"*) echo '{"state":"CLOSED"}' ;;
  "issue view "*"--json state"*) echo '{"state":"CLOSED"}' ;;
  "repo view "*"--json isArchived"*) echo '{"isArchived":true}' ;;
  "repo view "*) echo '{}' ;;
  *) echo "probe fake gh: unexpected: $*" >&2; exit 1 ;;
esac
`;

function writeFakeGh(home) {
  mkdirSync(join(home, 'bin'), { recursive: true });
  writeFileSync(join(home, 'bin', 'gh'), FAKE_GH, { mode: 0o755 });
}

const HERMETIC_GIT = {
  GIT_CONFIG_GLOBAL: '/dev/null',
  GIT_CONFIG_NOSYSTEM: '1',
  GIT_AUTHOR_NAME: 'probe',
  GIT_AUTHOR_EMAIL: 'probe@example.com',
  GIT_COMMITTER_NAME: 'probe',
  GIT_COMMITTER_EMAIL: 'probe@example.com',
};

// makeClone creates a real clone under <home>/clones/<name>, with a bare
// "origin" under <home>/remotes (so a remote delete pushes there), and
// returns its snapshot record (the machines/<m>.json clone shape) with the
// landed verdicts a sync would have written: each branch in c.branches is
// merged into main, pushed, and landed in main.
function makeClone(home, c) {
  const path = join(home, 'clones', c.name);
  const remote = join(home, 'remotes', `${c.name}.git`);
  mkdirSync(path, { recursive: true });
  mkdirSync(dirname(remote), { recursive: true });
  const git = (dir, args) =>
    execSync(`git ${args}`, {
      cwd: dir,
      stdio: 'pipe',
      env: { ...process.env, ...HERMETIC_GIT, HOME: home },
    })
      .toString()
      .trim();
  git(home, `init -q --bare "${remote}"`);
  git(path, 'init -q -b main');
  git(path, `remote add origin "${remote}"`);
  writeFileSync(join(path, 'README'), `${c.name}\n`);
  git(path, 'add README');
  git(path, 'commit -q -m init');
  const branches = [];
  for (const b of c.branches) {
    git(path, `switch -q -c "${b}" main`);
    writeFileSync(join(path, `${b.replace(/\W/g, '_')}.txt`), `${b}\n`);
    git(path, 'add -A');
    git(path, `commit -q -m "${b}"`);
    const tip = git(path, 'rev-parse HEAD');
    git(path, 'switch -q main');
    git(path, `merge -q --ff-only "${b}"`);
    git(path, `push -q origin "${b}"`);
    git(path, `branch -q --set-upstream-to="origin/${b}" "${b}"`);
    branches.push({
      name: b,
      tip,
      tip_at: '2026-09-20T00:00:00Z',
      upstream: `origin/${b}`,
      remote_tip: tip,
      landed_state: 'yes',
      landed: 'in main',
      landed_tip: tip,
      landed_how: 'default-branch',
    });
  }
  git(path, 'push -q origin main');
  return {
    path,
    repo: `schuettc/${c.name}`,
    remotes: { origin: `schuettc/${c.name}` },
    branches: [
      { name: 'main', tip: git(path, 'rev-parse main'), landed_state: 'no' },
      ...branches,
    ],
  };
}

// Wait up to timeoutMs for the advert file to appear and return its contents.
function waitForAdvert(advertPath, timeoutMs = 10000) {
  return new Promise((resolve, reject) => {
    const start = Date.now();
    const poll = setInterval(() => {
      try {
        const text = readFileSync(advertPath, 'utf8');
        const adv = JSON.parse(text);
        if (adv.url && adv.token) {
          clearInterval(poll);
          resolve(adv);
        }
      } catch {
        // not yet — keep polling
      }
      if (Date.now() - start > timeoutMs) {
        clearInterval(poll);
        reject(new Error(`casebook serve did not start within ${timeoutMs}ms`));
      }
    }, 100);
  });
}

/**
 * Start a seeded casebook serve.
 * Always uses --no-open and CASEBOOK_NO_BROWSER=1 so no browser tab opens.
 * Returns { url, base, token, stop } where stop() kills the serve process.
 */
export async function startServe(opts = {}) {
  const bin = findBinary();
  const home = setupHome(
    opts.seedRepos,
    opts.seedMachines,
    opts.seedFiles,
    opts.seedClones,
    opts.syncInterval,
  );
  const advertPath = join(home, 'state', 'live', 'serve.json');

  const proc = spawn(bin, SERVE_ARGS, {
    env: {
      ...process.env,
      CASEBOOK_HOME: home,
      HOME: home,
      // The fake gh first on PATH; git without Court's config.
      PATH: `${join(home, 'bin')}:${process.env.PATH}`,
      ...HERMETIC_GIT,
      // Disables browser opening via the test seam in cli/workbench.go.
      CASEBOOK_NO_BROWSER: '1',
      // Short stuck threshold so probes can test stuck delivery UI without
      // waiting 10 minutes. Tests that need this wait 3+ seconds after
      // picking up a delivery.
      CASEBOOK_STUCK_AFTER: '2s',
      // Short left threshold so probes can test left-session UI without
      // waiting 60 seconds for a session to go left.
      CASEBOOK_LEFT_AFTER: '3s',
      // Short watch interval so the left-crossing check fires quickly.
      CASEBOOK_WATCH_EVERY: '1s',
    },
    stdio: 'pipe',
  });

  let stderr = '';
  proc.stderr.on('data', (d) => {
    stderr += String(d);
  });

  let exited = false;
  proc.on('exit', () => {
    exited = true;
  });

  let adv;
  try {
    adv = await waitForAdvert(advertPath);
  } catch (err) {
    if (!exited) proc.kill();
    throw new Error(`${String(err)}\nstderr: ${stderr}`);
  }

  return {
    url: adv.url,
    base: adv.base,
    token: adv.token,
    home,
    /** Every gh call serve made (to the fake), one line each. */
    ghCalls() {
      const log = join(home, 'gh-calls.log');
      return existsSync(log)
        ? readFileSync(log, 'utf8').split('\n').filter(Boolean)
        : [];
    },
    stop() {
      if (!exited) proc.kill();
      rmSync(home, { recursive: true, force: true });
    },
  };
}
