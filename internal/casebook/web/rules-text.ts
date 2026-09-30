// rules-text.ts — the Rules section's words, as pure functions.
//
// No kit or browser imports, so node --test can load it.

import type { Condition, ReasonCount, Rule, TrackRecord } from './wire.d.ts';

/**
 * trackRecord is a rule's record as the list shows it (spec §4.3):
 * "87 accepted · 4 rejected · 3 pending", leaving out the parts that are 0.
 * A rule with no record yet reads "0 pending".
 */
export function trackRecord(r: TrackRecord): string {
  const parts: string[] = [];
  if (r.accepted) parts.push(`${r.accepted} accepted`);
  if (r.rejected) parts.push(`${r.rejected} rejected`);
  if (r.pending) parts.push(`${r.pending} pending`);
  return parts.length ? parts.join(' \u00b7 ') : '0 pending';
}

/**
 * authorOf names a rule's author. serve writes an agent's rules as
 * "<harness>:<session>" (the agent's source) and Court's as his user name.
 */
export function authorOf(createdBy: string): { agent: boolean; name: string } {
  const i = createdBy.indexOf(':');
  if (i > 0) return { agent: true, name: createdBy.slice(0, i) };
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

/** conditionErrorIndex reads which condition serve's validation error names (-1: none). */
export function conditionErrorIndex(message: string): number {
  const m = /^condition (\d+): /.exec(message);
  return m ? Number(m[1]) : -1;
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
    a.propose.disposition === b.propose.disposition &&
    (a.propose.until ?? '') === (b.propose.until ?? '') &&
    (a.propose.note ?? '') === (b.propose.note ?? '') &&
    ex(a) === ex(b)
  );
}
