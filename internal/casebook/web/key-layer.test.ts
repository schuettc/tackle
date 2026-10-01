// key-layer.test.ts — unit tests for makeKeyLayer (app.ts's keyboard layer).
//
// Run with: node --test key-layer.test.ts

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  makeKeyLayer,
  type KeyLike,
  type KeysLike,
  type NavLike,
} from './key-layer.ts';

// A registry that behaves like the kit's createKeys: the family defaults
// (the list keys only with a list, / only when it searches), a clash (one
// sequence a prefix of another) throws, destroy drops everything.
function kit() {
  const made: Array<{ list?: NavLike; destroyed: boolean }> = [];
  const create = (list?: NavLike): KeysLike & { run(keys: string): void } => {
    const bound: Array<KeyLike & { group: string }> = [];
    const me = { list, destroyed: false };
    made.push(me);
    const add = (b: KeyLike) => {
      const seq = b.keys.split(' ');
      const c = bound.find((o) => {
        const s = o.keys.split(' ');
        const n = Math.min(s.length, seq.length);
        return s.slice(0, n).join(' ') === seq.slice(0, n).join(' ');
      });
      if (c) throw new Error(`key clash: "${b.keys}" is taken by "${c.keys}"`);
      const e = { ...b, group: b.group ?? 'page' };
      bound.push(e);
      return () => {
        const i = bound.indexOf(e);
        if (i >= 0) bound.splice(i, 1);
      };
    };
    const fam = (keys: string, label: string, run: () => void) =>
      add({ keys, label, run, group: 'family' });
    if (list) {
      fam('j', 'next', () => list.move(1));
      fam('↓', 'next', () => list.move(1));
      fam('k', 'previous', () => list.move(-1));
      fam('↑', 'previous', () => list.move(-1));
      fam('o', 'open', () => list.open());
      fam('↵', 'open', () => list.open());
      fam('x', 'select', () => list.toggle());
      fam('⇧x', 'select range', () => list.toggleRange());
      if (typeof list.focusSearch === 'function')
        fam('/', 'search', () => list.focusSearch!());
    }
    fam('?', 'show keys', () => {});
    fam('Esc', 'close / leave field', () => {});
    return {
      register: add,
      showHelp() {},
      bindings: () =>
        bound.map((b) => ({ keys: b.keys, label: b.label, group: b.group })),
      destroy() {
        me.destroyed = true;
        bound.length = 0;
      },
      run(keys: string) {
        const b = bound.find((o) => o.keys === keys);
        if (!b) throw new Error(`not bound: ${keys}`);
        b.run(new Event('keydown') as KeyboardEvent);
      },
    };
  };
  let last: ReturnType<typeof create> | null = null;
  return {
    made,
    create: (list?: NavLike) => (last = create(list)),
    press: (keys: string) => last!.run(keys),
  };
}

// A list that records what the keys asked of it.
function nav(name: string, search = false) {
  const log: string[] = [];
  const n: NavLike & { log: string[] } = {
    log,
    move: (d) => log.push(`${name} move ${d}`),
    open: () => log.push(`${name} open`),
    toggle: () => log.push(`${name} toggle`),
    toggleRange: () => log.push(`${name} range`),
  };
  if (search) n.focusSearch = () => log.push(`${name} search`);
  return n;
}

const key = (keys: string, label: string, log?: string[]): KeyLike => ({
  keys,
  label,
  run: () => void log?.push(label),
});

const keysOf = (l: { keys: KeysLike }) =>
  l.keys
    .bindings()
    .map((b) => b.keys)
    .sort();

describe('makeKeyLayer', () => {
  test('the list keys drive the shown section’s list, and follow it', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const a = nav('attention', true);
    const p = nav('apply');
    const attention = {
      id: 'attention',
      listKeys: () => ({ nav: a, selects: true }),
      keys: () => [key('d', 'decide')],
    };
    const apply = {
      id: 'apply',
      listKeys: () => ({ nav: p, selects: true }),
      keys: () => [key('p', 'pause')],
    };
    layer.bind(attention);
    k.press('j');
    k.press('x');
    k.press('/');
    assert.deepEqual(a.log, [
      'attention move 1',
      'attention toggle',
      'attention search',
    ]);
    layer.bind(apply);
    k.press('j');
    k.press('⇧x');
    assert.deepEqual(p.log, ['apply move 1', 'apply range']);
    // Nothing reached the hidden list, and its search key is gone.
    assert.equal(a.log.length, 3);
    assert.ok(!keysOf(layer).includes('/'));
    assert.ok(!keysOf(layer).includes('d'));
    // Every kit registry but the newest was destroyed.
    assert.deepEqual(
      k.made.map((m) => m.destroyed),
      [true, true, false],
    );
  });

  test('a list whose rows can’t be selected gets no x or ⇧x', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const r = nav('rules');
    layer.bind({ id: 'rules', listKeys: () => ({ nav: r, selects: false }) });
    assert.deepEqual(keysOf(layer), ['?', 'Esc', 'j', 'k', 'o', '↑', '↓', '↵']);
    k.press('j');
    k.press('k');
    k.press('↵');
    assert.deepEqual(r.log, ['rules move 1', 'rules move -1', 'rules open']);
    assert.ok(
      layer.keys.bindings().every((b) => b.group === 'family'),
      'the list keys are the family’s',
    );
  });

  test('a list that can’t select but searches keeps its /', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const r = nav('rules', true);
    layer.bind({ id: 'rules', listKeys: () => ({ nav: r, selects: false }) });
    assert.ok(keysOf(layer).includes('/'));
    assert.ok(!keysOf(layer).includes('x'));
    k.press('/');
    assert.deepEqual(r.log, ['rules search']);
  });

  test('no list shown: no list keys (Attention’s board)', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const a = nav('attention', true);
    let board = false;
    const decide = [key('d', 'decide')];
    const attention = {
      id: 'attention',
      listKeys: () => (board ? null : { nav: a, selects: true }),
      keys: () => decide,
    };
    layer.bind(attention);
    assert.ok(keysOf(layer).includes('j'));
    board = true;
    layer.bind(attention);
    assert.deepEqual(keysOf(layer), ['?', 'Esc', 'd']);
  });

  test('page-wide keys survive every rebind, and unregister for good', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const log: string[] = [];
    const off = layer.keys.register(key('.', 'focus the composer', log));
    layer.keys.register(key('g r', 'go to rules', log));
    layer.bind({
      id: 'attention',
      listKeys: () => ({ nav: nav('a'), selects: true }),
    });
    layer.bind({
      id: 'rules',
      listKeys: () => ({ nav: nav('r'), selects: false }),
    });
    k.press('.');
    k.press('g r');
    assert.deepEqual(log, ['focus the composer', 'go to rules']);
    off();
    assert.ok(!keysOf(layer).includes('.'));
    layer.bind({ id: 'apply' });
    assert.ok(!keysOf(layer).includes('.'));
    assert.ok(keysOf(layer).includes('g r'));
  });

  test('a page-wide key that clashes throws, as the kit’s register does', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    layer.keys.register(key('g a', 'go to attention'));
    assert.throws(() => layer.keys.register(key('g', 'something')), /clash/);
    assert.deepEqual(keysOf(layer), ['?', 'Esc', 'g a']);
  });

  test('two sections bind the same key, each its own', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const log: string[] = [];
    const attention = {
      id: 'attention',
      keys: () => [key('a', 'accept', log)],
    };
    const rules = { id: 'rules', keys: () => [key('a', 'activate', log)] };
    layer.bind(attention);
    k.press('a');
    layer.bind(rules);
    k.press('a');
    assert.deepEqual(log, ['accept', 'activate']);
  });

  test('the same section, list and keys again is a no-op', () => {
    const k = kit();
    const layer = makeKeyLayer(k.create, () => {});
    const n = nav('rules');
    const d = key('a', 'activate');
    const s = {
      id: 'rules',
      listKeys: () => ({ nav: n, selects: false }),
      keys: () => [d],
    };
    layer.bind(s);
    layer.bind(s);
    assert.equal(k.made.length, 2); // the first registry, and one bind
  });

  test('a clash is reported, and the section’s other keys still bind', () => {
    const k = kit();
    const errors: string[] = [];
    const layer = makeKeyLayer(k.create, (m) => errors.push(m));
    layer.keys.register(key('/', 'page-wide search'));
    assert.doesNotThrow(() =>
      layer.bind({
        id: 'rules',
        keys: () => [key('/', 'search'), key('a', 'activate')],
      }),
    );
    assert.equal(errors.length, 1);
    assert.match(errors[0], /key clash in section rules: "\/" \(search\)/);
    assert.ok(keysOf(layer).includes('a'));
  });

  test('a page key that clashes with a list’s defaults is reported on the rebind', () => {
    const k = kit();
    const errors: string[] = [];
    const layer = makeKeyLayer(k.create, (m) => errors.push(m));
    layer.keys.register(key('x', 'page x'));
    layer.bind({
      id: 'apply',
      listKeys: () => ({ nav: nav('p'), selects: true }),
    });
    assert.equal(errors.length, 1);
    assert.match(errors[0], /key clash in the page: "x" \(page x\)/);
  });
});
