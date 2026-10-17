// rules-text.ts — a rule's words (its record, conditions, matches), as pure
// functions.
//
// No kit or browser imports, so node --test can load it.

import type {
  Condition,
  Field,
  Operator,
  ReasonCount,
  TrackRecord,
} from './wire.d.ts';

/**
 * trackRecord is a rule's record: "87 accepted · 2 decided differently ·
 * 4 rejected · 3 pending", leaving out the parts that are 0 ('' for none
 * yet). Decided differently: the item was decided another way than the
 * rule recommended.
 */
export function trackRecord(r: TrackRecord): string {
  const parts: string[] = [];
  if (r.accepted) parts.push(`${r.accepted} accepted`);
  if (r.decided_differently)
    parts.push(`${r.decided_differently} decided differently`);
  if (r.rejected) parts.push(`${r.rejected} rejected`);
  if (r.pending) parts.push(`${r.pending} pending`);
  return parts.join(' \u00b7 ');
}

/**
 * authorOf names a rule's author. serve writes an agent's rules as
 * "<harness>:<session>" (the agent's source) and Court's as his user name.
 */
export function authorOf(createdBy: string): { agent: boolean; name: string } {
  const i = createdBy.indexOf(':');
  if (i > 0) return { agent: true, name: createdBy.slice(0, i) };
  // The starter rules casebook ships.
  if (createdBy === 'casebook') return { agent: false, name: 'casebook' };
  return { agent: false, name: 'you' };
}

/** reasonSummary is "394 in main · 455 via merged pr"; '' for a lone "matched". */
export function reasonSummary(by: ReasonCount[] | null): string {
  const rs = by ?? [];
  if (rs.length === 1 && rs[0].reason === 'matched') return '';
  return rs.map((r) => `${r.count} ${r.reason.toLowerCase()}`).join(' \u00b7 ');
}

/** matchesHeading is the matches section's label: "matches now · N · <by reason>". */
export function matchesHeading(
  total: number,
  by: ReasonCount[] | null,
): string {
  const why = reasonSummary(by);
  return `matches now \u00b7 ${total}` + (why ? ` \u00b7 ${why}` : '');
}

/**
 * conditionErrorIndex reads which condition serve's validation error names,
 * as the row's index (-1: none). serve counts conditions from 1, as Court
 * and the agent do ("condition 2:" is the second row, index 1).
 */
export function conditionErrorIndex(message: string): number {
  const m = /^condition (\d+): /.exec(message);
  const n = m ? Number(m[1]) : 0;
  return n >= 1 ? n - 1 : -1;
}

// "a, b or c"
function orList(words: string[]): string {
  if (words.length <= 1) return words.join('');
  return `${words.slice(0, -1).join(', ')} or ${words[words.length - 1]}`;
}

/** slug is a rule's or a filter's id from its name: lower case, a-z 0-9 and hyphens (serve's rule). */
export function slug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60);
}

/** fieldLabel is a field's plain label (its name when it has none). */
export function fieldLabel(f: { name: string; label?: string }): string {
  return f.label || f.name;
}

/** opLabel is an operator's plain label (its name when unknown). */
export function opLabel(ops: Operator[], op: string): string {
  return ops.find((o) => o.name === op)?.label || op;
}

/** valueLabel is a value's plain label on field f (the value itself when it has none). */
export function valueLabel(f: Field | undefined, v: string): string {
  return f?.value_labels?.[v] || v;
}

/**
 * conditionText reads a condition in labels: "type is pull request"; a list
 * of values reads with "or": "type is issue or pull request", "type is not
 * repository or worktree". Files and the wire keep the raw names.
 */
export function conditionText(
  c: Condition,
  fields: Field[],
  ops: Operator[],
): string {
  const f = fields.find((x) => x.name === c.field);
  const name = f ? fieldLabel(f) : c.field;
  if (c.op === 'in' || c.op === 'not-in') {
    const { values } = pickedValues(c);
    const words = orList(values.map((v) => valueLabel(f, v)));
    return `${name} ${opLabel(ops, c.op === 'in' ? 'is' : 'is-not')} ${words}`;
  }
  return `${name} ${opLabel(ops, c.op)} ${valueLabel(f, c.value)}`;
}

/**
 * pickedValues is a fixed-value condition as the editor's picker shows it:
 * the values ticked, and whether they are excluded (is-not, not-in).
 */
export function pickedValues(c: Condition): { not: boolean; values: string[] } {
  const not = c.op === 'is-not' || c.op === 'not-in';
  const values =
    c.op === 'in' || c.op === 'not-in'
      ? c.value
          .split(',')
          .map((v) => v.trim())
          .filter((v) => v !== '')
      : c.value === ''
        ? []
        : [c.value];
  return { not, values };
}

/**
 * pickedCondition is the condition the picker saves: one value is is (or
 * is-not), several in (or not-in) with the values comma-separated.
 */
export function pickedCondition(
  field: string,
  not: boolean,
  values: string[],
): Condition {
  if (values.length > 1) {
    return { field, op: not ? 'not-in' : 'in', value: values.join(',') };
  }
  return { field, op: not ? 'is-not' : 'is', value: values[0] ?? '' };
}

/**
 * typesOf is the item types a list's type condition leaves, of all (the
 * kind field's values, in order); null when it has none.
 */
export function typesOf(conds: Condition[], all: string[]): string[] | null {
  const c = conds.find((x) => x.field === 'kind');
  if (!c) return null;
  const { not, values } = pickedValues(c);
  return all.filter((k) => values.includes(k) !== not);
}

/** fieldApplies: f has a value on every type in types (null: any type). */
export function fieldApplies(f: Field, types: string[] | null): boolean {
  const ks = f.kinds ?? [];
  if (!types || ks.length === 0) return true;
  return types.every((t) => ks.includes(t));
}

/**
 * fieldTypes names the types f applies to, by the type field's labels
 * ("branch, worktree"); '' when it applies to every type.
 */
export function fieldTypes(f: Field, kind: Field | undefined): string {
  return (f.kinds ?? []).map((k) => valueLabel(kind, k)).join(', ');
}

/** fieldFound: the menu's search q finds f, by its label or its help. */
export function fieldFound(f: Field, q: string): boolean {
  const s = q.trim().toLowerCase();
  if (!s) return true;
  return (
    fieldLabel(f).toLowerCase().includes(s) ||
    (f.help ?? '').toLowerCase().includes(s)
  );
}
