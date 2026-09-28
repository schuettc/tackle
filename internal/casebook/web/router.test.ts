// router.test.ts — unit tests for router parse() and pathEscape().
//
// Run with: node --test router.test.ts
// (Node 24 strips TypeScript natively; no compiler step needed.)
//
// These tests exercise the pure helper functions inline (the actual router.ts
// uses window.location and window.addEventListener which are not available in
// Node).  The implementations here must stay byte-for-byte identical to the
// ones in router.ts.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

// ---- inline copies of the pure helpers from router.ts ----------------------
// Keep these in sync with router.ts whenever the helpers change.

function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

function pathEscape(s: string): string {
  return encodeURIComponent(s).replace(/%3A/gi, ':').replace(/%40/gi, '@');
}

function parse(hash: string): { section: string; sub: string } {
  const path = hash.replace(/^#\//, '');
  const slash = path.indexOf('/');
  if (slash === -1) {
    return { section: path || 'attention', sub: '' };
  }
  return {
    section: path.slice(0, slash),
    sub: safeDecode(path.slice(slash + 1)),
  };
}

// Build an item hash the same way go('item', key) does.
function buildItemHash(key: string): string {
  return `#/item/${pathEscape(key)}`;
}

// ---- parse tests -----------------------------------------------------------

describe('parse', () => {
  test('empty hash → attention with empty sub', () => {
    assert.deepStrictEqual(parse(''), { section: 'attention', sub: '' });
  });

  test('#/ → attention with empty sub', () => {
    // path becomes '' after removing '#/', indexOf('/') = -1,
    // so section = '' || 'attention' = 'attention'.
    assert.deepStrictEqual(parse('#/'), { section: 'attention', sub: '' });
  });

  test('#/attention → attention with empty sub', () => {
    assert.deepStrictEqual(parse('#/attention'), {
      section: 'attention',
      sub: '',
    });
  });

  test('#/attention/waiting → attention / waiting', () => {
    assert.deepStrictEqual(parse('#/attention/waiting'), {
      section: 'attention',
      sub: 'waiting',
    });
  });

  test('#/attention/board → attention / board', () => {
    assert.deepStrictEqual(parse('#/attention/board'), {
      section: 'attention',
      sub: 'board',
    });
  });

  // Keys that the server encodes with url.PathEscape (/ → %2F, # → %23).
  // : is left unencoded by url.PathEscape.

  test('server-encoded issue key with / and # → decoded', () => {
    const r = parse('#/item/issue:schuettc%2Fhail%234');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'issue:schuettc/hail#4');
  });

  test('server-encoded pr key → decoded', () => {
    const r = parse('#/item/pr:schuettc%2Fhail%233');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'pr:schuettc/hail#3');
  });

  test('key with @ symbol → decoded', () => {
    // url.PathEscape keeps @ unencoded; encodeURIComponent encodes it.
    // parse() must decode either form.
    const r = parse('#/item/issue:user@org%2Frepo%231');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'issue:user@org/repo#1');
  });

  test('key with encoded @ → decoded', () => {
    const r = parse('#/item/issue:user%40org%2Frepo%231');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'issue:user@org/repo#1');
  });

  test('key with spaces → decoded', () => {
    const r = parse('#/item/worktree:machine%2Fpath%20to%2Fdir');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'worktree:machine/path to/dir');
  });

  test('key with colon kept unencoded (server-style) → raw colon returned', () => {
    // url.PathEscape keeps : unencoded; parse() must return it as-is.
    const r = parse('#/item/pr:schuettc%2Fhail%233');
    assert.ok(r.sub.includes(':'), `expected colon in sub, got: ${r.sub}`);
  });

  test('malformed percent-escape → raw string returned (no throw)', () => {
    // %GG is not a valid percent-encoded sequence.
    const r = parse('#/item/bad%GGkey');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'bad%GGkey');
  });

  test('raw form (unencoded / and #) is still parseable', () => {
    // The sub after the first slash is taken literally; only the sub is decoded.
    // A raw key passed through location.hash by older code still works.
    const r = parse('#/item/repo:schuettc/hail');
    assert.strictEqual(r.section, 'item');
    // 'repo:schuettc/hail' — the router takes the first slash as the
    // section/sub boundary, so sub = 'repo:schuettc' (only the part after
    // the first slash is the sub; the rest is not re-split).
    // Actually parse splits at the FIRST slash after #/: 'item' | 'repo:schuettc/hail'.
    // The slash indexOf finds the first slash in 'item/repo:schuettc/hail'.
    // section = 'item', sub = 'repo:schuettc/hail' (everything after first slash).
    // Hmm wait — let me re-check. hash = '#/item/repo:schuettc/hail'.
    // path = 'item/repo:schuettc/hail'
    // slash = path.indexOf('/') = 4  (after 'item')
    // section = 'item', sub = 'repo:schuettc/hail'
    // So sub IS 'repo:schuettc/hail'. The second slash is part of the sub, not
    // a path segment separator.
    assert.strictEqual(r.sub, 'repo:schuettc/hail');
  });
});

// ---- pathEscape tests ------------------------------------------------------

describe('pathEscape', () => {
  test('plain ascii is unchanged', () => {
    assert.strictEqual(pathEscape('waiting'), 'waiting');
  });

  test('/ is encoded as %2F', () => {
    assert.strictEqual(pathEscape('a/b'), 'a%2Fb');
  });

  test('# is encoded as %23', () => {
    assert.strictEqual(pathEscape('a#4'), 'a%234');
  });

  test(': is NOT encoded (matches url.PathEscape)', () => {
    assert.strictEqual(pathEscape('issue:owner/repo'), 'issue:owner%2Frepo');
  });

  test('@ is NOT encoded (matches url.PathEscape)', () => {
    assert.strictEqual(
      pathEscape('issue:user@org/repo'),
      'issue:user@org%2Frepo',
    );
  });

  test('space is encoded as %20', () => {
    assert.strictEqual(pathEscape('a b'), 'a%20b');
  });

  test('full issue key round-trips', () => {
    const key = 'issue:schuettc/hail#4';
    assert.strictEqual(pathEscape(key), 'issue:schuettc%2Fhail%234');
  });

  test('full pr key round-trips', () => {
    const key = 'pr:schuettc/hail#3';
    assert.strictEqual(pathEscape(key), 'pr:schuettc%2Fhail%233');
  });
});

// ---- round-trip tests (pathEscape + parse) ---------------------------------

describe('pathEscape + parse round-trip', () => {
  const keys = [
    'issue:schuettc/hail#4',
    'pr:schuettc/hail#3',
    'repo:schuettc/hail',
    'branch:machine/path#with-hash',
    'worktree:machine/path to dir',
    'issue:user@org/repo#10',
    'issue:nested:colon/slash#5',
  ];

  for (const key of keys) {
    test(`round-trips "${key}"`, () => {
      const hash = buildItemHash(key);
      const { section, sub } = parse(hash);
      assert.strictEqual(section, 'item');
      assert.strictEqual(sub, key);
    });
  }

  test('server-built URL (url.PathEscape style) parses to the same key', () => {
    // Simulate what the server produces with url.PathEscape for 'issue:schuettc/hail#4':
    // url.PathEscape keeps : but encodes / and #.
    const serverUrl = '#/item/issue:schuettc%2Fhail%234';
    const { sub: fromServer } = parse(serverUrl);

    // What the client builds:
    const clientUrl = buildItemHash('issue:schuettc/hail#4');
    const { sub: fromClient } = parse(clientUrl);

    // Both must decode to the same key.
    assert.strictEqual(fromServer, fromClient);
    assert.strictEqual(fromServer, 'issue:schuettc/hail#4');
  });
});
