// overview.ts — the reading column when no item is open: what the list means,
// the accept-the-lean card, and the by-concern / by-size tables.

import { buttons, card, facts, h } from '/_kit/kit.js';
import type { Ctx } from './app.ts';
import { byConcern, bySize, bulkTargets } from './bulk.ts';

export interface BucketWords {
  id: string;
  label: string;
  head(n: number): string;
  lead: string;
}

export const TEST_BUCKETS: BucketWords[] = [
  {
    id: 'cut',
    label: 'leans cut',
    head: (n) => `${n} tests Jev leans toward cutting`,
    lead: 'Jev thinks these probably don’t earn their place, but not strongly enough (cut 0.30–0.59) to cut them without you.',
  },
  {
    id: 'keep',
    label: 'leans keep',
    head: (n) => `${n} tests Jev leans toward keeping`,
    lead: 'Jev’s most likely answer is keep, but its cut probability is still 0.30 or more.',
  },
  {
    id: 'review',
    label: 'undecided',
    head: (n) => `${n} tests Jev can’t decide`,
    lead: 'Jev’s most likely answer is “needs a look”. These are the ones most worth reading.',
  },
];

export const GROUP_BUCKETS: BucketWords[] = [
  {
    id: 'consolidate',
    label: 'leans merge',
    head: (n) => `${n} groups Jev leans toward merging`,
    lead: 'Similar tests in one file that Jev thinks could become one table test (merge 0.30–0.59). A merge is written by the agent and checked by cull.',
  },
  {
    id: 'keep_separate',
    label: 'leans separate',
    head: (n) => `${n} groups Jev leans toward keeping separate`,
    lead: 'Similar-looking tests Jev thinks probably check different things.',
  },
  {
    id: 'review',
    label: 'undecided',
    head: (n) => `${n} groups Jev can’t decide`,
    lead: 'Jev’s most likely answer is “needs a look”. Read each group and decide.',
  },
];

export const BLIND_BUCKET = (section: string): BucketWords => ({
  id: 'open',
  label: 'to judge',
  head: (n) => `${n} ${section} to judge`,
  lead: `Read each ${section === 'tests' ? 'test' : 'group'} and answer ${section === 'tests' ? 'keep or cut' : 'separate or merge'}. Jev’s opinion is hidden.`,
});

const REASON_WHY: Record<string, string> = {
  'restates the code':
    'asserts values it set up, or re-computes the answer the way the code does',
  'pins a detail': 'exact wording, formatting or internal structure',
  'might not be missed': 'little protection would be lost',
  'tests a library': 'mostly exercises a library or framework',
  'mocks only': 'only checks that mocks were called',
  'no clear reason': 'no concern stood out; Jev is split overall',
};

export interface OverviewArgs {
  section: 'tests' | 'groups';
  words: BucketWords | null; // null: the answered bucket
  /** the list as shown (open items of the bucket, or the answered ones) */
  xs: Item[];
  /** every item of the section */
  all: Item[];
  answers: ReadonlyMap<string, unknown>;
  answeredOf(id: string): string | undefined;
  allOpen: number;
  notice: string;
}

function table(head: string[], rows: HTMLElement[]): HTMLElement {
  return h(
    'table',
    { class: 'ov' },
    h('tr', null, ...head.map((t) => h('th', null, t))),
    ...rows,
  );
}

export function overview(ctx: Ctx, a: OverviewArgs): HTMLElement {
  const note = a.notice ? [h('p', { class: 'notice' }, a.notice)] : [];
  if (!a.words) return answeredView(ctx, a, note);
  const bk = a.words;
  const xs = a.xs;
  const doc = h(
    'div',
    { class: 'kit-doc' },
    ...note,
    h(
      'div',
      { class: 'kit-kick' },
      `${a.section} · ${ctx.rootName} · ${bk.label}`,
    ),
    h(
      'h1',
      { class: 'kit-h1' },
      xs.length ? bk.head(xs.length) : `Nothing left that ${bk.label}`,
    ),
    h('p', { class: 'lead' }, bk.lead),
  );
  if (!xs.length) return doc;
  const first = () => ctx.openSubset(xs);
  if (ctx.blind) {
    doc.append(
      buttons([{ label: 'open the first', fill: true, run: first }]),
      h('p', { class: 'why' }, 'Open any one from the list (j/k, ↵).'),
    );
    return doc;
  }
  return a.section === 'tests'
    ? testOverview(ctx, doc, bk, xs as TestItem[], a)
    : groupOverview(ctx, doc, bk, xs as GroupItem[], a);
}

function testOverview(
  ctx: Ctx,
  doc: HTMLElement,
  bk: BucketWords,
  xs: TestItem[],
  a: OverviewArgs,
): HTMLElement {
  const rows = byConcern(xs).map((r) => {
    const open = bulkTargets(r.items, a.answers);
    return h(
      'tr',
      null,
      h(
        'td',
        null,
        h('div', null, r.concern),
        h('div', { class: 'why' }, REASON_WHY[r.concern] ?? ''),
      ),
      h('td', { class: 'n' }, String(r.items.length)),
      h('td', { class: 'n' }, r.meanP.toFixed(2)),
      h(
        'td',
        null,
        buttons([
          { label: 'open', run: () => ctx.openSubset(r.items) },
          {
            label: `keep ${open.length}`,
            disabled: !open.length,
            run: () => ctx.answer(open, 'keep', 'group'),
          },
          {
            label: `cut ${open.length}`,
            danger: true,
            disabled: !open.length,
            run: () => ctx.answer(open, 'cut', 'group'),
          },
        ]),
      ),
    );
  });
  const out: (HTMLElement | string)[] = [];
  if (bk.id === 'cut' || bk.id === 'keep') {
    const v = bk.id;
    out.push(
      card({
        edge: 'agent',
        head: `accept jev’s lean · ${v} all ${xs.length}`,
        body:
          v === 'cut'
            ? 'Answers cut for every test still open here. cull apply still runs the suite before and after and rolls back if it breaks; a cut test is gone from the file, not from git.'
            : 'Answers keep for every test still open here. Nothing changes in the code.',
        actions: [
          {
            label: `${v} all ${xs.length}`,
            fill: true,
            run: () => ctx.answer(bulkTargets(xs, a.answers), v, 'group'),
          },
          { label: 'open the first', run: () => ctx.openSubset(xs) },
        ],
      }),
    );
  }
  doc.append(
    ...out,
    h('div', { class: 'kit-label' }, 'by concern'),
    table(['Jev’s main concern', 'tests', 'cut p', ''], rows),
    h(
      'p',
      { class: 'why' },
      'Open any test from the list (j/k, ↵) to read it and override. Answers you give one at a time are never changed by a group action.',
    ),
  );
  return doc;
}

function groupOverview(
  ctx: Ctx,
  doc: HTMLElement,
  bk: BucketWords,
  xs: GroupItem[],
  a: OverviewArgs,
): HTMLElement {
  const rows = bySize(xs).map((r) => {
    const open = bulkTargets(r.items, a.answers);
    return h(
      'tr',
      null,
      h('td', null, `${r.size} tests`),
      h('td', { class: 'n' }, String(r.items.length)),
      h('td', { class: 'n' }, r.meanP.toFixed(2)),
      h(
        'td',
        null,
        buttons([
          { label: 'open', run: () => ctx.openSubset(r.items) },
          {
            label: `separate ${open.length}`,
            disabled: !open.length,
            run: () => ctx.answer(open, 'separate', 'group'),
          },
          {
            label: `merge ${open.length}`,
            disabled: !open.length,
            run: () => ctx.answer(open, 'merge', 'group'),
          },
        ]),
      ),
    );
  });
  if (bk.id === 'consolidate' || bk.id === 'keep_separate') {
    const v = bk.id === 'consolidate' ? 'merge' : 'separate';
    doc.append(
      card({
        edge: 'agent',
        head: `accept jev’s lean · ${v} all ${xs.length}`,
        body:
          v === 'merge'
            ? 'The agent rewrites each group as one table test; cull check --group confirms every case survived.'
            : 'Nothing changes in the code.',
        actions: [
          {
            label: `${v} all ${xs.length}`,
            fill: true,
            run: () => ctx.answer(bulkTargets(xs, a.answers), v, 'group'),
          },
          { label: 'open the first', run: () => ctx.openSubset(xs) },
        ],
      }),
    );
  }
  doc.append(
    h('div', { class: 'kit-label' }, 'by size'),
    table(['group size', 'groups', 'merge p', ''], rows),
    h(
      'p',
      { class: 'why' },
      'Open any group from the list (j/k, ↵) to read it and override. Answers you give one at a time are never changed by a group action.',
    ),
  );
  return doc;
}

function answeredView(
  ctx: Ctx,
  a: OverviewArgs,
  note: HTMLElement[],
): HTMLElement {
  const vs = a.section === 'tests' ? ['cut', 'keep'] : ['merge', 'separate'];
  const count = (v: string) =>
    a.xs.filter((x) => a.answeredOf(x.id) === v).length;
  return h(
    'div',
    { class: 'kit-doc' },
    ...note,
    h(
      'div',
      { class: 'kit-kick' },
      `${a.section} · ${ctx.rootName} · answered`,
    ),
    h('h1', { class: 'kit-h1' }, `${a.xs.length} answered`),
    facts(vs.map((v): [string, string] => [v, String(count(v))])),
    h(
      'p',
      null,
      `${a.allOpen} still open across tests and groups. Sending returns these answers; the rest stay on the page.`,
    ),
  );
}
