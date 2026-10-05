// prose.ts — a passage as prose: line-numbered like the kit's codeBlock, but
// each line wraps instead of scrolling sideways, so a long rule reads in
// the column. One element per line, so the gutter stays beside its line.

import { h } from '/_kit/kit.js';

export function prose(
  src: string,
  o: { start?: number; mark?: [number, number] } = {},
): HTMLElement {
  const start = o.start ?? 1;
  const lines = src.split(/\r?\n/);
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return h(
    'div',
    { class: 'sift-prose' },
    lines.map((l, i) => {
      const n = start + i;
      const marked = !!o.mark && n >= o.mark[0] && n <= o.mark[1];
      return h(
        'div',
        { class: 'sift-ln' + (marked ? ' mark' : '') },
        h('span', { class: 'sift-n', 'aria-hidden': 'true' }, String(n)),
        h('span', { class: 'sift-t' }, l),
      );
    }),
  );
}
