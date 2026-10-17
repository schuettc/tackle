// router.test.ts — unit tests for router parse() and pathEscape().
//
// Run with: node --test router.test.ts
// (Node 24 strips TypeScript natively; no compiler step needed.)
//
// Imports the real helpers from router-helpers.ts (no window access at module
// scope) so that breaking a helper makes these tests fail immediately.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  safeDecode,
  pathEscape,
  parse,
  decideQuery,
  flatFor,
  ruleHash,
} from './router-helpers.ts';

// Build an item hash the same way go('item', key) does.
function buildItemHash(key: string): string {
  return `#/item/${pathEscape(key)}`;
}

// ---- parse tests -----------------------------------------------------------

describe('parse', () => {
  test('empty hash → decide with empty sub', () => {
    assert.deepStrictEqual(parse(''), {
      section: 'decide',
      sub: '',
      query: '',
    });
  });

  test('#/ → decide with empty sub', () => {
    assert.deepStrictEqual(parse('#/'), {
      section: 'decide',
      sub: '',
      query: '',
    });
  });

  test('#/decide?flat → decide, the query apart', () => {
    assert.deepStrictEqual(parse('#/decide?filters=a&flat'), {
      section: 'decide',
      sub: '',
      query: 'filters=a&flat',
    });
  });

  // A worktree key's ? is encoded (%3F): the query split never cuts it.
  test('an item key with an encoded ? stays an item route', () => {
    assert.deepStrictEqual(parse('#/item/worktree:m:%2Fa%3Fb'), {
      section: 'item',
      sub: 'worktree:m:/a?b',
      query: '',
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

  test('raw form (repo key with path slash) is still parseable', () => {
    // parse splits at the FIRST slash in the path after #/:
    // '#/item/repo:schuettc/hail' → section='item', sub='repo:schuettc/hail'
    const r = parse('#/item/repo:schuettc/hail');
    assert.strictEqual(r.section, 'item');
    assert.strictEqual(r.sub, 'repo:schuettc/hail');
  });

  test('safeDecode: plain string unchanged', () => {
    assert.strictEqual(safeDecode('hello'), 'hello');
  });

  test('safeDecode: encoded → decoded', () => {
    assert.strictEqual(safeDecode('a%20b'), 'a b');
  });

  test('safeDecode: malformed → raw (no throw)', () => {
    assert.strictEqual(safeDecode('bad%GGkey'), 'bad%GGkey');
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

  test('% is encoded as %25', () => {
    assert.strictEqual(pathEscape('a%b'), 'a%25b');
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
    'issue:foo%bar/baz#6', // key containing a literal % character
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

describe('decideQuery and flatFor', () => {
  test('decideQuery: flat or nothing', () => {
    assert.strictEqual(decideQuery({ flat: true, filters: [] }), 'flat');
    assert.strictEqual(decideQuery({ flat: false, filters: [] }), '');
  });

  test('decideQuery: the rule, the selected filters, then flat', () => {
    assert.strictEqual(
      decideQuery({ flat: false, filters: ['a', 'b'] }),
      'filters=a,b',
    );
    assert.strictEqual(
      decideQuery({ flat: true, filters: ['a'], rule: 'r' }),
      'rule=r&filters=a&flat',
    );
  });

  test('flatFor: an explicit view reads the address, else the stored choice', () => {
    assert.strictEqual(flatFor(true, 'flat', false), true);
    assert.strictEqual(flatFor(true, '', true), false);
    assert.strictEqual(flatFor(false, '', true), true);
    assert.strictEqual(flatFor(true, 'filters=a&flat', false), true);
  });

  test('ruleHash: a rule opens on decide, narrowing the list to it', () => {
    assert.strictEqual(
      ruleHash('landed-branches'),
      '#/decide?rule=landed-branches',
    );
  });
});

// Addresses from before decide and apply still circulate (bookmarks,
// casebook_open links already sent): each lands on decide with the same
// filters, rule or item, and none is read as an item key.
describe('old addresses land on decide', () => {
  const decide = (query = '') => ({ section: 'decide', sub: '', query });
  for (const [hash, want] of [
    ['#/attention', decide()],
    ['#/attention/new', decide()],
    ['#/attention/due', decide()],
    ['#/attention/proposed', decide()],
    ['#/attention/all', decide()],
    ['#/attention/waiting', decide()],
    ['#/attention/board', decide()],
    ['#/attention/new?flat', decide('flat')],
    ['#/attention?filters=a,b&flat', decide('filters=a,b&flat')],
    ['#/attention?rule=landed-branches', decide('rule=landed-branches')],
    ['#/rules', decide()],
    ['#/rules/landed-branches', decide('rule=landed-branches')],
    [
      '#/item/pr:schuettc%2Fhail%233',
      { section: 'item', sub: 'pr:schuettc/hail#3', query: '' },
    ],
  ] as const) {
    test(hash, () => assert.deepStrictEqual(parse(hash), want));
  }
});
