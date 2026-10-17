// filters-text.ts — the saved filters' pure helpers: the address's
// selection, toggling a chip, and a selection's name. No DOM, so
// filters-text.test.ts runs them under node --test.

import type { FilterRow } from './wire.d.ts';

/**
 * parseFilterIds is the selection an address's filters= names: its ids
 * split on commas, trimmed, empties and repeats dropped, in order.
 */
export function parseFilterIds(s: string | null): string[] {
  const out: string[] = [];
  for (const part of (s ?? '').split(',')) {
    const id = part.trim();
    if (id && !out.includes(id)) out.push(id);
  }
  return out;
}

/**
 * toggleFilter is selected with id added or removed. A filter that isn't
 * valid, or one rows doesn't know, can't be selected: selected is unchanged.
 */
export function toggleFilter(
  rows: FilterRow[],
  selected: string[],
  id: string,
): string[] {
  if (selected.includes(id)) return selected.filter((x) => x !== id);
  const row = rows.find((r) => r.filter.id === id);
  if (!row || row.invalid) return selected;
  return [...selected, id];
}

/** filterLabel names a selection: its filters' names (an unknown one by its id). */
export function filterLabel(rows: FilterRow[], ids: string[]): string {
  return ids
    .map((id) => rows.find((r) => r.filter.id === id)?.filter.name ?? id)
    .join(' + ');
}
