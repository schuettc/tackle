// section-keys.test.ts — unit tests for bindSectionKeys (app.ts's keys swap).
//
// Run with: node --test section-keys.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { makeKeyBinder, type KeyLike } from './section-keys.ts';

// A registry that behaves like the kit's: a clash throws, unbind removes.
function registry() {
  const bound = new Map<string, string>();
  return {
    bound,
    register: (b: KeyLike): (() => void) => {
      if (bound.has(b.keys)) throw new Error(`key clash: ${b.keys}`);
      bound.set(b.keys, b.label);
      return () => {
        if (bound.get(b.keys) === b.label) bound.delete(b.keys);
      };
    },
  };
}

const key = (keys: string, label: string): KeyLike => ({
  keys,
  label,
  run() {},
});

describe('makeKeyBinder', () => {
  test('binds the shown section’s keys and swaps them on a switch', () => {
    const r = registry();
    const errors: string[] = [];
    const bind = makeKeyBinder(r.register, (m) => errors.push(m));
    const attention = {
      id: 'attention',
      keys: [key('a', 'accept'), key('/', 'search')],
    };
    const rules = { id: 'rules', keys: [key('a', 'activate')] };
    bind(attention);
    assert.deepEqual(
      [...r.bound.entries()],
      [
        ['a', 'accept'],
        ['/', 'search'],
      ],
    );
    bind(rules);
    assert.deepEqual([...r.bound.entries()], [['a', 'activate']]);
    bind(attention);
    assert.deepEqual(
      [...r.bound.entries()],
      [
        ['a', 'accept'],
        ['/', 'search'],
      ],
    );
    assert.deepEqual(errors, []);
  });

  test('the same section again is a no-op', () => {
    const r = registry();
    let calls = 0;
    const bind = makeKeyBinder(
      (b) => {
        calls++;
        return r.register(b);
      },
      () => {},
    );
    const s = { id: 'rules', keys: [key('a', 'activate')] };
    bind(s);
    bind(s);
    assert.equal(calls, 1);
  });

  test('a clash is reported, and the section’s other keys still bind', () => {
    const r = registry();
    r.register(key('/', 'page-wide search'));
    const errors: string[] = [];
    const bind = makeKeyBinder(r.register, (m) => errors.push(m));
    const s = { id: 'rules', keys: [key('/', 'search'), key('a', 'activate')] };
    assert.doesNotThrow(() => bind(s));
    assert.equal(errors.length, 1);
    assert.match(errors[0], /key clash in section rules: "\/" \(search\)/);
    assert.equal(r.bound.get('a'), 'activate');
  });
});
