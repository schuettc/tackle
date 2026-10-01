// group.ts — one group of similar tests, open.

import {
  buttons,
  card,
  codeBlock,
  facts,
  fold,
  h,
  type Button,
} from '/_kit/kit.js';
import type { Ctx } from './app.ts';
import { backLink, noteBlock, sigTable } from './item.ts';
import { actP, lean, recommendation, strength } from './model.ts';

export function groupItem(ctx: Ctx, g: GroupItem): HTMLElement {
  const a = ctx.answerOf(g.id);
  const l = lean(g);
  const p = actP(g);
  const members = g.state.tests;
  const kick = `group · ${ctx.rootName} · ${g.file}`;
  const head = [
    backLink(ctx),
    h('div', { class: 'kit-kick' }, ctx.blind ? `${kick} · blind` : kick),
    h('h1', { class: 'kit-h1' }, `${members.length} similar tests`),
  ];
  const unanswer: Button[] = a
    ? [{ label: 'unanswer (u)', run: () => ctx.unanswer(g) }]
    : [];
  const plain: Button[] = [
    { label: '1 separate', run: () => ctx.answer([g], 'separate', 'item') },
    { label: '2 merge', run: () => ctx.answer([g], 'merge', 'item') },
    ...unanswer,
  ];
  const maxRow = Math.max(0, ...(g.rows ?? []).map((r) => r.length));
  const rows = h(
    'div',
    { class: 'scroll' },
    h(
      'table',
      { class: 'ov rows' },
      ...members.map((m, i) =>
        h(
          'tr',
          null,
          h('td', null, m.name),
          ...Array.from({ length: maxRow }, (_, j) =>
            h('td', null, (g.rows?.[i] ?? [])[j] ?? ''),
          ),
        ),
      ),
    ),
  );
  const tail = [
    h(
      'div',
      { class: 'kit-label' },
      'the table test’s rows (values that differ)',
    ),
    rows,
    ...members.map((m) => fold(m.name, codeBlock(m.source))),
    ...noteBlock(ctx, g),
  ];
  if (ctx.blind) {
    return h(
      'div',
      { class: 'kit-doc' },
      ...head,
      facts([['your answer', a?.value ?? 'open']]),
      h('div', { class: 'kit-label' }, 'your answer'),
      buttons(plain),
      ...tail,
    );
  }
  const verb = l === 'consolidate' ? 'merge' : 'separate';
  const other = verb === 'merge' ? 'separate' : 'merge';
  const acts: Button[] =
    l === 'review'
      ? plain
      : [
          {
            label: `accept: ${verb}`,
            fill: true,
            run: () => ctx.answer([g], verb, 'item'),
          },
          {
            label: `${other} instead`,
            run: () => ctx.answer([g], other, 'item'),
          },
          ...unanswer,
        ];
  const j = g.jev;
  return h(
    'div',
    { class: 'kit-doc' },
    ...head,
    facts([
      ['merge probability', p.toFixed(2)],
      ['your answer', a?.value ?? 'open'],
    ]),
    card({
      edge: 'agent',
      head:
        l === 'review'
          ? `cull recommends · look · ${strength(p)}`
          : `cull recommends · ${verb} · ${strength(p)}`,
      body: recommendation(g),
      actions: acts,
    }),
    h('div', { class: 'kit-label' }, 'jev’s signals'),
    sigTable([
      ['merge', p],
      ['same behavior', j.same_behavior.noul],
      ['exact duplicate', j.exact_duplicate.noul],
      ['something lost if merged', j.loss_if_merged.noul],
    ]),
    ...tail,
  );
}
