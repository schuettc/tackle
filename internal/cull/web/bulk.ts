// bulk.ts — buckets, groupings and what a bulk action may touch.

import { actP, concern, lean, NO_CONCERN } from './model.ts';

export type Answers = ReadonlyMap<string, unknown>;

const byActDesc = (a: Item, b: Item) =>
  actP(b) - actP(a) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

/** Items by Jev's lean (answered items only in "answered"), each ordered by act probability desc, then id. */
export function buckets(
  items: Item[],
  answers: Answers,
): Record<string, Item[]> {
  const isGroup = items.length > 0 && items[0].kind === 'group';
  const out: Record<string, Item[]> = {};
  for (const id of isGroup
    ? ['consolidate', 'keep_separate']
    : ['cut', 'keep', 'review']) {
    out[id] = [];
  }
  out.answered = [];
  for (const it of items) {
    if (answers.has(it.id)) out.answered.push(it);
    else (out[lean(it)] ??= []).push(it);
  }
  for (const k of Object.keys(out)) out[k].sort(byActDesc);
  return out;
}

export const CONCERN_ORDER = [
  'restates the code',
  'pins a detail',
  'might not be missed',
  'tests a library',
  'mocks only',
  NO_CONCERN,
];

export interface ConcernRow {
  concern: string;
  items: TestItem[];
  meanP: number;
}

export function byConcern(items: TestItem[]): ConcernRow[] {
  const m = new Map<string, TestItem[]>();
  for (const it of items) {
    const key = concern(it)?.short ?? NO_CONCERN;
    m.set(key, [...(m.get(key) ?? []), it]);
  }
  const rows: ConcernRow[] = [];
  for (const c of CONCERN_ORDER) {
    const xs = m.get(c);
    if (!xs?.length) continue;
    xs.sort(byActDesc);
    rows.push({
      concern: c,
      items: xs,
      meanP: xs.reduce((s, x) => s + actP(x), 0) / xs.length,
    });
  }
  return rows;
}

export interface SizeRow {
  size: string;
  items: GroupItem[];
  meanP: number;
}

export function bySize(groups: GroupItem[]): SizeRow[] {
  const m = new Map<string, GroupItem[]>();
  for (const g of groups) {
    const n = g.state.tests.length;
    const key = n >= 5 ? '5+' : String(Math.max(n, 2));
    m.set(key, [...(m.get(key) ?? []), g]);
  }
  const rows: SizeRow[] = [];
  for (const size of ['2', '3', '4', '5+']) {
    const xs = m.get(size);
    if (!xs?.length) continue;
    xs.sort(byActDesc);
    rows.push({
      size,
      items: xs,
      meanP: xs.reduce((s, x) => s + actP(x), 0) / xs.length,
    });
  }
  return rows;
}

/** What a group action may touch: items with no answer, however it was given. */
export function bulkTargets<T extends Item>(items: T[], answers: Answers): T[] {
  return items.filter((i) => !answers.has(i.id));
}
