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
function findBinary() {
  if (process.env.CASEBOOK_BIN) return process.env.CASEBOOK_BIN;
  const built = join(repoRoot, 'bin', 'casebook');
  // Always rebuild: the probe tests the current source, and the binary embeds
  // internal/casebook/serve/assets/ which was just rewritten by build:js/css.
  console.error('[serve.mjs] building casebook binary…');
  execSync(`go build -o "${built}" ./cmd/casebook`, {
    cwd: repoRoot,
    stdio: 'inherit',
  });
  return built;
}

// Set up a fresh CASEBOOK_HOME from the committed testdata/home fixture.
// The fixture contains:
//   data/repo.bundle   – git bundle of the casebook-data repo
//   cache/github.json  – GitHub observation cache (may be empty)
// Config is written fresh so paths are correct for this machine.
function setupHome() {
  const fixture = join(here, 'testdata', 'home');
  const home = join(tmpdir(), `casebook-probe-${process.pid}`);

  mkdirSync(join(home, 'config'), { recursive: true });
  mkdirSync(join(home, 'data'), { recursive: true });
  mkdirSync(join(home, 'state', 'live'), { recursive: true });

  // Clone the bundle to data/repo.
  const bundlePath = join(fixture, 'data', 'repo.bundle');
  const repoPath = join(home, 'data', 'repo');
  execSync(`git clone -q "${bundlePath}" "${repoPath}"`, { stdio: 'pipe' });

  // Copy the github cache to the state directory, which is where CachePath()
  // looks: $CASEBOOK_HOME/state/github.json (tools.StateDir("casebook") joins
  // $CASEBOOK_HOME with "state").
  const srcCache = join(fixture, 'cache', 'github.json');
  if (existsSync(srcCache)) {
    cpSync(srcCache, join(home, 'state', 'github.json'));
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
      'sync_interval = "30m"',
    ].join('\n') + '\n',
    { mode: 0o600 },
  );

  return home;
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
export async function startServe() {
  const bin = findBinary();
  const home = setupHome();
  const advertPath = join(home, 'state', 'live', 'serve.json');

  const proc = spawn(bin, SERVE_ARGS, {
    env: {
      ...process.env,
      CASEBOOK_HOME: home,
      HOME: home,
      // Disables browser opening via the test seam in cli/workbench.go.
      CASEBOOK_NO_BROWSER: '1',
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
    stop() {
      if (!exited) proc.kill();
      rmSync(home, { recursive: true, force: true });
    },
  };
}
