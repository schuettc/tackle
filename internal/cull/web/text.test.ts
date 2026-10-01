import { test } from 'node:test';
import assert from 'node:assert/strict';
import { assertLines } from './text.ts';

test('python assert lines', () => {
  const body = [
    'def test_x():',
    '    grid = make()',
    '    assert grid.total == 3',
    '    assert(grid.ok)',
    '    with pytest.raises(ValueError):',
    '        grid.bad()',
    '    self.assertEqual(a, b)',
    '    assertion = 1',
  ].join('\n');
  assert.deepEqual(assertLines('python', body), [
    'assert grid.total == 3',
    'assert(grid.ok)',
    'with pytest.raises(ValueError):',
    'self.assertEqual(a, b)',
  ]);
});

test('go assert lines', () => {
  const body = [
    'func TestX(t *testing.T) {',
    '\tgot := f()',
    '\tif got != 1 {',
    '\t\tt.Errorf("bad %d", got)',
    '\t}',
    '\trequire.NoError(t, err)',
    '\tassert.Equal(t, 1, got)',
    '\tt.Fatal("x")',
    '}',
  ].join('\n');
  assert.deepEqual(assertLines('go', body), [
    't.Errorf("bad %d", got)',
    'require.NoError(t, err)',
    'assert.Equal(t, 1, got)',
    't.Fatal("x")',
  ]);
});

test('typescript assert lines', () => {
  const body = [
    "it('x', () => {",
    '  const a = f();',
    '  expect(a).toBe(1);',
    '  assert.equal(a, 1);',
    '  assert(a);',
    '});',
  ].join('\n');
  for (const lang of ['typescript', 'javascript']) {
    assert.deepEqual(assertLines(lang, body), [
      'expect(a).toBe(1);',
      'assert.equal(a, 1);',
      'assert(a);',
    ]);
  }
});

test('unknown language and no asserts', () => {
  assert.deepEqual(assertLines('rust', 'assert_eq!(a, b);'), []);
  assert.deepEqual(assertLines('python', 'def test_x():\n    pass'), []);
});
