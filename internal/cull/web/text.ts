// text.ts — pull the assertion lines out of a test body.

const PY = [
  'assert ',
  'assert(',
  'with pytest.raises',
  'pytest.raises',
  'self.assert',
];
const GO = ['t.Error', 't.Fatal', 'assert.', 'require.'];
const TS = ['expect(', 'assert.', 'assert('];

export function assertLines(lang: string, body: string): string[] {
  const lines = body.split('\n').map((l) => l.trim());
  switch (lang) {
    case 'python':
      return lines.filter((l) => PY.some((p) => l.startsWith(p)));
    case 'go':
      return lines.filter((l) => GO.some((p) => l.includes(p)));
    case 'typescript':
    case 'javascript':
      return lines.filter((l) => TS.some((p) => l.includes(p)));
    default:
      return [];
  }
}

/** The bar's status after Send: where the answers went (to), if anywhere. */
export function sentText(n: number, to?: string): string {
  if (n === 0) return 'Nothing to send.';
  const what = `${n} ${n === 1 ? 'answer' : 'answers'}`;
  return to
    ? `Sent ${what} to ${to}.`
    : `Sent ${what}. The next agent that opens cull in this project gets them.`;
}
