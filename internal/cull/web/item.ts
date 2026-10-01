// item.ts — one test, open: facts, the recommendation, signals, source, note.

import {
  buttons,
  card,
  codeBlock,
  facts,
  fold,
  h,
  noteField,
  type Button,
} from '/_kit/kit.js';
import type { Ctx } from './app.ts';
import { actP, lean, recommendation, rvLevel, strength } from './model.ts';
import { assertLines } from './text.ts';

const FLAGS: [string, string][] = [
  ['tautological', 'restates the code'],
  ['incidental_detail', 'pins a detail'],
  ['framework_behavior', 'tests a library'],
  ['mock_only', 'mocks only'],
  ['not_missed', 'might not be missed'],
];

export function sigTable(rows: [string, number][]): HTMLElement {
  return h(
    'table',
    { class: 'sig' },
    ...rows.map(([k, v]) =>
      h(
        'tr',
        null,
        h('td', null, k),
        h(
          'td',
          null,
          h(
            'span',
            { class: 'bar' },
            h('i', { style: `width:${Math.round(v * 100)}%` }),
          ),
          h('span', { class: 'v' }, v.toFixed(2)),
        ),
      ),
    ),
  );
}

export function backLink(ctx: Ctx): HTMLElement {
  return h(
    'button',
    { class: 'back', type: 'button', onclick: () => ctx.back() },
    '← back to the overview (b)',
  );
}

export function noteBlock(ctx: Ctx, it: Item): HTMLElement[] {
  const field = noteField({
    value: ctx.noteOf(it),
    placeholder: 'why — n',
    onCommit: (v) => ctx.setNote(it, v),
  });
  // The kit's Enter handler (added first) commits; leave the field after it so
  // the next page key (1, b, …) isn't typed into the note.
  field.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.isComposing) field.blur();
  });
  return [h('div', { class: 'kit-label' }, 'note (optional)'), field];
}

export function testItem(ctx: Ctx, t: TestItem): HTMLElement {
  const a = ctx.answerOf(t.id);
  const l = lean(t);
  const p = actP(t);
  const kick = `test · ${ctx.rootName} · ${t.file}`;
  const head = [
    backLink(ctx),
    h('div', { class: 'kit-kick' }, ctx.blind ? `${kick} · blind` : kick),
    h('h1', { class: 'kit-h1' }, t.name),
  ];
  const answerBtns = (): Button[] => {
    const unanswer: Button[] = a
      ? [{ label: 'unanswer (u)', run: () => ctx.unanswer(t) }]
      : [];
    return [
      { label: '1 keep', run: () => ctx.answer([t], 'keep', 'item') },
      {
        label: '2 cut',
        danger: true,
        run: () => ctx.answer([t], 'cut', 'item'),
      },
      ...unanswer,
    ];
  };
  const src = t.state;
  const asserts = assertLines(src.language, src.test_source);
  const callees = src.code_under_test ?? [];
  const tail = [
    h('div', { class: 'kit-label' }, 'what it asserts'),
    asserts.length
      ? codeBlock(asserts.join('\n'))
      : h('p', { class: 'why' }, 'no assert lines found; read the whole test'),
    fold('the whole test', codeBlock(src.test_source)),
    src.setup_context
      ? fold('setup it uses', codeBlock(src.setup_context))
      : '',
    callees.length
      ? fold(
          `code under test · ${callees.map((c) => c.symbol).join(', ')}`,
          h(
            'div',
            null,
            ...callees.map((c) =>
              h(
                'div',
                null,
                h('div', { class: 'kit-label' }, c.file),
                codeBlock(c.source),
              ),
            ),
          ),
        )
      : h('p', { class: 'why' }, 'code under test: none found'),
    ...noteBlock(ctx, t),
  ];
  if (ctx.blind) {
    return h(
      'div',
      { class: 'kit-doc' },
      ...head,
      facts([['your answer', a?.value ?? 'open']]),
      h('div', { class: 'kit-label' }, 'your answer'),
      buttons(answerBtns()),
      ...tail,
    );
  }
  const rec = l === 'review' ? 'look' : l;
  const acts: Button[] =
    l === 'review'
      ? answerBtns()
      : [
          {
            label: `accept: ${l}`,
            fill: true,
            run: () => ctx.answer([t], l === 'cut' ? 'cut' : 'keep', 'item'),
          },
          {
            label: `${l === 'cut' ? 'keep' : 'cut'} instead`,
            danger: l !== 'cut',
            run: () => ctx.answer([t], l === 'cut' ? 'keep' : 'cut', 'item'),
          },
          ...(a ? [{ label: 'unanswer (u)', run: () => ctx.unanswer(t) }] : []),
        ];
  const flagRows: [string, number][] = FLAGS.map(([k, w]) => [
    w,
    (t.jev as unknown as Record<string, Noul>)[k]?.noul ?? 0,
  ]);
  flagRows.sort((x, y) => y[1] - x[1]);
  const pr = t.jev.verdict.probabilities;
  return h(
    'div',
    { class: 'kit-doc' },
    ...head,
    facts([
      ['cut probability', p.toFixed(2)],
      ['protects', rvLevel(t.jev.regression_value.score)],
      ['your answer', a?.value ?? 'open'],
    ]),
    card({
      edge: 'agent',
      head: `cull recommends · ${rec} · ${strength(p)}`,
      body: recommendation(t),
      actions: acts,
    }),
    h('div', { class: 'kit-label' }, 'jev’s signals'),
    sigTable([
      ['verdict: cut', pr.cut],
      ['verdict: keep', pr.keep],
      ['verdict: needs a look', pr.review],
      ...flagRows,
    ]),
    ...tail,
  );
}
