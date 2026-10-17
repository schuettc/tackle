// rules-text.ts — the Rules section's words, as pure functions.
//
// No kit or browser imports, so node --test can load it.

import type {
  Condition,
  Field,
  Operator,
  ReasonCount,
  Rule,
  RuleAction,
  TrackRecord,
} from './wire.d.ts';

/**
 * trackRecord is a rule's record (spec §4.3): "87 accepted · 4 rejected ·
 * 3 pending", leaving out the parts that are 0 ('' for none yet).
 */
export function trackRecord(r: TrackRecord): string {
  const parts: string[] = [];
  if (r.accepted) parts.push(`${r.accepted} accepted`);
  if (r.rejected) parts.push(`${r.rejected} rejected`);
  if (r.pending) parts.push(`${r.pending} pending`);
  return parts.join(' \u00b7 ');
}

/**
 * rowKicker is a rule's line in the list: what it matches now, what it
 * excludes, and its record ("849 match · 2 excluded · 3 pending"); an
 * invalid rule says so instead of counting.
 */
export function rowKicker(row: {
  matches: number;
  excluded: number;
  record: TrackRecord;
  invalid?: string;
}): string {
  const rec = trackRecord(row.record);
  const head = row.invalid
    ? 'not valid'
    : `${row.matches} match \u00b7 ${row.excluded} excluded`;
  return rec ? `${head} \u00b7 ${rec}` : head;
}

/** sameAction compares two [propose] blocks. */
export function sameAction(a: RuleAction, b: RuleAction): boolean {
  return (
    a.disposition === b.disposition &&
    (a.until ?? '') === (b.until ?? '') &&
    (a.note ?? '') === (b.note ?? '')
  );
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

/** viewCounts counts the list's views. */
export function viewCounts(rules: Array<{ status: string }>): {
  all: number;
  active: number;
  drafts: number;
} {
  let active = 0;
  for (const r of rules) if (r.status === 'active') active++;
  return { all: rules.length, active, drafts: rules.length - active };
}

/** sameConditions compares two condition lists, in order. */
export function sameConditions(a: Condition[], b: Condition[]): boolean {
  if (a.length !== b.length) return false;
  return a.every(
    (c, i) =>
      c.field === b[i].field && c.op === b[i].op && c.value === b[i].value,
  );
}

/**
 * sameRule reports whether two copies of a rule say the same thing: what a
 * live "rules" event is checked against before the open document is redrawn.
 */
export function sameRule(a: Rule, b: Rule): boolean {
  const ex = (r: Rule) =>
    (r.exclude ?? []).map((x) => `${x.key}\u0000${x.reason ?? ''}`).join('\n');
  return (
    a.id === b.id &&
    a.name === b.name &&
    a.status === b.status &&
    a.created_by === b.created_by &&
    a.edited_at === b.edited_at &&
    sameConditions(a.match ?? [], b.match ?? []) &&
    sameAction(a.propose, b.propose) &&
    ex(a) === ex(b)
  );
}

/** hasKindCondition: the rule says which kinds of item it matches. */
export function hasKindCondition(conds: Condition[]): boolean {
  return conds.some((c) => c.field === 'kind');
}

// "a, b or c"
function orList(words: string[]): string {
  if (words.length <= 1) return words.join('');
  return `${words.slice(0, -1).join(', ')} or ${words[words.length - 1]}`;
}

/**
 * kindHint is the disposition picker's line when the rule has no kind
 * condition: it may propose only what every kind of item allows, and a kind
 * condition would let it propose the rest ('' when nothing is missing).
 */
export function kindHint(
  conds: Condition[],
  allowed: string[],
  all: string[],
): string {
  if (hasKindCondition(conds) || !allowed.length) return '';
  const missing = [...new Set(all)].filter((d) => !allowed.includes(d)).sort();
  return missing.length
    ? `add a kind condition to propose ${orList(missing)}`
    : '';
}

/**
 * refusedDisposition says why this rule may not propose d, in serve's words
 * for a rule with no kind condition ("every kind of item (the rule has no
 * kind condition)").
 */
export function refusedDisposition(
  d: string,
  conds: Condition[],
  allowed: string[],
): string {
  const what = hasKindCondition(conds)
    ? 'what this rule matches'
    : 'every kind of item (the rule has no kind condition)';
  return `${d} can\u2019t be proposed for ${what} (allowed: ${allowed.join(', ')})`;
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
