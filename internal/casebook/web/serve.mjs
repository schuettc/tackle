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

// ---- hermetic git, guarded ---------------------------------------------------
//
// Every git the probe runs (its fixtures, its checks, the serves it starts,
// the binary's build) runs with no global or system config (so none of
// Court's hooks) and with CASEBOOK_DISABLE=1, which casebook's own git hook
// shims (~/.config/casebook/hooks) honour: nothing the probe does records
// into Court's casebook. A guard enforces it: at load, a git shim goes first
// on this process's PATH (every child inherits it), logs each call with the
// environment it ran in, and refuses one without that environment. The probe
// checks the log (gitGuard()).
export const HERMETIC_GIT = {
  GIT_CONFIG_GLOBAL: '/dev/null',
  GIT_CONFIG_NOSYSTEM: '1',
  CASEBOOK_DISABLE: '1',
  GIT_AUTHOR_NAME: 'probe',
  GIT_AUTHOR_EMAIL: 'probe@example.com',
  GIT_COMMITTER_NAME: 'probe',
  GIT_COMMITTER_EMAIL: 'probe@example.com',
};
const guardDir = realpathSync(tmpdir()) + `/casebook-probe-git-${process.pid}`;
const guardLog = join(guardDir, 'git-calls.log');
{
  const realGit = execSync('command -v git', { shell: '/bin/sh' })
    .toString()
    .trim();
  if (!realGit || realGit.startsWith(guardDir))
    throw new Error('serve.mjs: no git to guard');
  mkdirSync(guardDir, { recursive: true });
  writeFileSync(
    join(guardDir, 'git'),
    `#!/bin/sh
printf '%s\\t%s\\t%s\\t%s\\n' "\${CASEBOOK_DISABLE:-unset}" "\${GIT_CONFIG_GLOBAL:-unset}" "\${GIT_CONFIG_NOSYSTEM:-unset}" "$*" >> '${guardLog}'
if [ "$CASEBOOK_DISABLE" != 1 ] || [ "$GIT_CONFIG_GLOBAL" != /dev/null ] || [ "$GIT_CONFIG_NOSYSTEM" != 1 ]; then
  echo "probe git guard: refused git without CASEBOOK_DISABLE=1 and hermetic config: $*" >&2
  exit 97
fi
exec '${realGit}' "$@"
`,
    { mode: 0o755 },
  );
  process.env.PATH = `${guardDir}:${process.env.PATH}`;
  process.on('exit', () => rmSync(guardDir, { recursive: true, force: true }));
}

/**
 * gitGuard reads the guard's log: every git call the probe and its serves
 * made, and those that ran without CASEBOOK_DISABLE=1 and hermetic config
 * (the shim refused them).
 */
export function gitGuard() {
  const lines = existsSync(guardLog)
    ? readFileSync(guardLog, 'utf8').split('\n').filter(Boolean)
    : [];
  const bad = lines.filter((l) => {
    const [disable, global, nosystem] = l.split('\t');
    return disable !== '1' || global !== '/dev/null' || nosystem !== '1';
  });
  return { calls: lines.length, bad };
}

/** probeGit runs git in dir, hermetically, with HOME the probe's home. */
export function probeGit(dir, args, home) {
  return execSync(`git ${args}`, {
    cwd: dir,
    stdio: 'pipe',
    env: { ...process.env, ...HERMETIC_GIT, HOME: home },
  })
    .toString()
    .trim();
}

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
  // go build stamps the binary from git: hermetic too.
  execSync(`go build -o "${built}" ./cmd/casebook`, {
    cwd: repoRoot,
    stdio: 'inherit',
    env: { ...process.env, ...HERMETIC_GIT },
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
//
// seedRefs adds individual lookups to the GitHub cache, as a sync's lookups
// left them ({'pr:o/r#9': {exists: false}}: GitHub answered "not found").
function setupHome(
  seedRepos = [],
  seedMachines = [],
  seedFiles = {},
  seedClones = [],
  syncInterval = '30m',
  slowRemote = false,
  conflictRemote = false,
  seedRefs = {},
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
  probeGit(home, `clone -q "${bundlePath}" "${repoPath}"`, home);
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
    const git = (args) => probeGit(repoPath, args, home);
    git('add -A');
    git('commit -q -m "probe: seed machine snapshots"');
  }

  if (slowRemote) makeSlowRemote(home, repoPath);
  if (conflictRemote) makeConflictRemote(home, repoPath);

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
  if (Object.keys(seedRefs).length) {
    const cachePath = join(home, 'state', 'github.json');
    const cache = JSON.parse(readFileSync(cachePath, 'utf8'));
    cache.refs = { ...(cache.refs ?? {}), ...seedRefs };
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
// Every call is logged (ghCalls()), with the environment it saw (ghEnv()),
// so a probe can say none went elsewhere. Nothing the probe starts can reach
// GitHub: were the fake bypassed, the serve's environment sends a real gh
// to a host that doesn't exist (GH_HOST, a .invalid name) with a token
// that isn't one, and no config of Court's (GH_CONFIG_DIR): it fails closed.
//
// git is real, and hermetic (HERMETIC_GIT above), and every repository it
// touches is under the probe's temp home.
const GH_FAIL_CLOSED = {
  GH_HOST: 'github.invalid',
  GH_TOKEN: 'probe-not-a-token',
  GITHUB_TOKEN: 'probe-not-a-token',
  GH_ENTERPRISE_TOKEN: 'probe-not-a-token',
  GITHUB_ENTERPRISE_TOKEN: 'probe-not-a-token',
  GH_PROMPT_DISABLED: '1',
};
const FAKE_GH = `#!/bin/sh
printf '%s\\n' "$*" >> "$CASEBOOK_HOME/gh-calls.log"
printf '%s %s %s\\n' "$GH_HOST" "$GH_TOKEN" "$GH_CONFIG_DIR" >> "$CASEBOOK_HOME/gh-env.log"
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
  const git = (dir, args) => probeGit(dir, args, home);
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

// makeSlowRemote gives the casebook-data clone a bare "origin" under
// <home>/remotes (instead of the fixture bundle, which can't take a push),
// holding everything the clone has, so nothing is queued at the start. Its
// pre-receive hook holds every push after that: it touches
// <home>/push-gate/started, then waits (up to 60 s) for
// <home>/push-gate/release. The hook is local: the push never leaves the
// probe's temp home.
function makeSlowRemote(home, repoPath) {
  const remote = join(home, 'remotes', 'casebook-data.git');
  const gate = join(home, 'push-gate');
  mkdirSync(dirname(remote), { recursive: true });
  mkdirSync(gate, { recursive: true });
  const git = (dir, args) => probeGit(dir, args, home);
  git(home, `init -q --bare "${remote}"`);
  git(repoPath, `remote set-url origin "${remote}"`);
  git(repoPath, 'push -q origin HEAD:main');
  git(repoPath, 'fetch -q origin');
  writeFileSync(
    join(remote, 'hooks', 'pre-receive'),
    `#!/bin/sh
cat >/dev/null
touch '${gate}/started'
i=0
while [ ! -f '${gate}/release' ] && [ $i -lt 600 ]; do sleep 0.1; i=$((i+1)); done
exit 0
`,
    { mode: 0o755 },
  );
}

// makeConflictRemote gives the casebook-data clone a bare "origin" under
// <home>/remotes, as makeSlowRemote does (no hook), then puts two different
// notes.txt on it: one pushed from another clone (another machine) and one
// committed in this clone, queued. serve's push then fails for a reason
// other than the network: rebasing onto the remote meets a conflict in a
// file casebook doesn't resolve. Moving the remote's main back one commit
// (to the base both share) lets the next push through. Local only.
function makeConflictRemote(home, repoPath) {
  const remote = join(home, 'remotes', 'casebook-data.git');
  const other = join(home, 'remotes', 'other-machine');
  mkdirSync(dirname(remote), { recursive: true });
  const git = (dir, args) => probeGit(dir, args, home);
  git(home, `init -q --bare "${remote}"`);
  git(repoPath, `remote set-url origin "${remote}"`);
  git(repoPath, 'push -q origin HEAD:main');
  git(repoPath, 'fetch -q origin');
  git(home, `clone -q -b main "${remote}" "${other}"`);
  writeFileSync(join(other, 'notes.txt'), 'from another machine\n');
  git(other, 'add notes.txt');
  git(other, 'commit -q -m "another machine: a note"');
  git(other, 'push -q origin HEAD:main');
  writeFileSync(join(repoPath, 'notes.txt'), 'from this machine\n');
  git(repoPath, 'add notes.txt');
  git(repoPath, 'commit -q -m "probe: a note of its own"');
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
    opts.slowRemote,
    opts.conflictRemote,
    opts.seedRefs,
  );
  const advertPath = join(home, 'state', 'live', 'serve.json');

  // launch starts serve on the home (again, for restart: the same state
  // directory, so its database and its remembered port).
  let proc;
  let exited = false;
  let stderr = '';
  const launch = () => {
    exited = false;
    stderr = '';
    const p = spawn(bin, SERVE_ARGS, {
      env: {
        ...process.env,
        CASEBOOK_HOME: home,
        HOME: home,
        // The fake gh first on PATH (then the git guard); git without Court's
        // config; a real gh, were it reached, fails closed.
        PATH: `${join(home, 'bin')}:${process.env.PATH}`,
        ...HERMETIC_GIT,
        ...GH_FAIL_CLOSED,
        GH_CONFIG_DIR: join(home, 'gh'),
        // Disables browser opening via the test seam in cli/workbench.go:
        // with it, even a restarted serve that finds a tab was open (which
        // reopens the page whatever --no-open says) opens nothing.
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
    p.stderr.on('data', (d) => {
      stderr += String(d);
    });
    p.on('exit', () => {
      if (p === proc) exited = true;
    });
    proc = p;
  };
  const advertOf = async () => {
    try {
      return await waitForAdvert(advertPath);
    } catch (err) {
      if (!exited) proc.kill();
      throw new Error(`${String(err)}\nstderr: ${stderr}`);
    }
  };

  launch();
  let adv = await advertOf();

  return {
    url: adv.url,
    base: adv.base,
    token: adv.token,
    home,
    /**
     * The slow remote's gate (opts.slowRemote): started() says a push is
     * being held, release() lets every push through from now on.
     */
    pushGate: {
      started: () => existsSync(join(home, 'push-gate', 'started')),
      release: () => writeFileSync(join(home, 'push-gate', 'release'), ''),
    },
    /** Every gh call serve made (to the fake), one line each. */
    ghCalls() {
      const log = join(home, 'gh-calls.log');
      return existsSync(log)
        ? readFileSync(log, 'utf8').split('\n').filter(Boolean)
        : [];
    },
    /** The environment each gh call saw: "GH_HOST GH_TOKEN GH_CONFIG_DIR". */
    ghEnv() {
      const log = join(home, 'gh-env.log');
      return existsSync(log)
        ? readFileSync(log, 'utf8').split('\n').filter(Boolean)
        : [];
    },
    /**
     * restart stops serve and starts it again on the same home (its database
     * and its remembered port): the page's token dies with the old one. The
     * new one's advert is returned; handle.url/base/token follow it.
     * Reopened says the new serve found a tab had been open and would have
     * opened the page again (CASEBOOK_NO_BROWSER keeps it from doing so).
     */
    async restart() {
      if (!exited) {
        const gone = new Promise((r) => proc.once('exit', r));
        proc.kill();
        await gone;
      }
      rmSync(advertPath, { force: true });
      launch();
      adv = await advertOf();
      this.url = adv.url;
      this.base = adv.base;
      this.token = adv.token;
      return adv;
    },
    stop() {
      if (!exited) proc.kill();
      // serve shuts down gracefully and may still be finishing a background
      // push into home for a moment after the kill, so a single rm can meet
      // ENOTEMPTY. Retry with backoff; a home that still can't be removed is
      // a temp-dir leak, never a reason to fail the run.
      try {
        rmSync(home, {
          recursive: true,
          force: true,
          maxRetries: 20,
          retryDelay: 100,
        });
      } catch (err) {
        console.error(`probe: could not remove ${home}: ${err.code ?? err}`);
      }
    },
  };
}
