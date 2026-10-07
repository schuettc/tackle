// decide-math.ts — pure decision-vocabulary helpers with no browser or kit
// imports.  This module is safe to import in node --test unit tests.
//
// These functions are the selection math the decide sheet uses:
//   kindFromKey   — extract the kind prefix from a casebook key
//   allowedForKind — look up allowed dispositions for one kind from server vocab
//   allowedForKeys — intersect allowed sets for a mixed-kind selection

import type { ChoiceVocab, DecisionVocabView, NotNowForm } from './wire.d.ts';

/**
 * kindFromKey extracts the kind from a key by taking the prefix before the
 * first colon.  "pr:schuettc/hail#3" → "pr".
 */
export function kindFromKey(key: string): string {
  const i = key.indexOf(':');
  return i > 0 ? key.slice(0, i) : '';
}

/**
 * allowedForKind returns the valid dispositions for one item kind from the
 * server's vocabulary.  Falls back to an empty array for unknown kinds.
 */
export function allowedForKind(
  vocab: DecisionVocabView,
  kind: string,
): string[] {
  return (vocab.kinds ?? []).find((k) => k.kind === kind)?.allowed ?? [];
}

/**
 * keyWithoutKind strips the kind prefix from a casebook key, leaving only the
 * identifier part suitable for display beside a kind label.
 * "issue:schuettc/hail#4" → "schuettc/hail#4".
 * Returns the full key unchanged when there is no colon.
 */
export function keyWithoutKind(key: string): string {
  const i = key.indexOf(':');
  return i > 0 ? key.slice(i + 1) : key;
}

/**
 * allowedForKeys returns the intersection of allowed dispositions for every
 * key in the slice, derived from each key's kind prefix.  This is the correct
 * way to compute allowed for a selection: it works for keys on unrendered
 * pages (where no ItemView is available) as well as rendered ones.
 */
export function allowedForKeys(
  vocab: DecisionVocabView,
  keys: string[],
): string[] {
  if (keys.length === 0) return [];
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  if (kinds.length === 0) return [];
  const sets = kinds.map((k) => new Set(allowedForKind(vocab, k)));
  const first = [...(sets[0] ?? new Set())];
  return first.filter((d) => sets.every((s) => s.has(d)));
}

// ---- pluralize helper ------------------------------------------------------

/**
 * pluralize returns "N singular" or "N plural" depending on n.
 * If plural is omitted, it defaults to singular + "s".
 *
 * pluralize(1, 'item')  → '1 item'
 * pluralize(2, 'item')  → '2 items'
 * pluralize(0, 'item')  → '0 items'
 */
export function pluralize(
  n: number,
  singular: string,
  plural?: string,
): string {
  return `${n} ${n === 1 ? singular : (plural ?? singular + 's')}`;
}

// ---- history display helpers ------------------------------------------------

/**
 * stripOwnKey removes the item's own key from a history entry subject line,
 * in display only.
 * e.g. "decide pr:schuettc/hail#3 → close by schuettc"
 *   → "decide → close by schuettc"
 *
 * Only the exact key (and any immediately trailing space) is removed.
 * Entries that mention a different key are left unchanged.
 */
export function stripOwnKey(subject: string, key: string): string {
  if (!key) return subject;
  const withSpace = key + ' ';
  if (subject.includes(withSpace)) {
    return subject.replace(withSpace, '');
  }
  if (subject.includes(key)) {
    return subject.replace(key, '');
  }
  return subject;
}

// ---- the decide step ---------------------------------------------------------

/**
 * nextUndecided is the item to open after the current one is decided: the
 * first key after current in order (the view's order when it was decided)
 * that is still undecided, then the first before it, then any undecided key
 * the order doesn't hold (a reload added it). undecided is the view as serve
 * lists it after the decision, so an item decided elsewhere meanwhile (a live
 * event) is not in it and is passed over, never counted as a step. When
 * current is no longer in order, the search starts at the beginning. null
 * when nothing is left.
 */
export function nextUndecided(
  order: string[],
  current: string,
  undecided: Set<string>,
): string | null {
  const i = order.indexOf(current);
  const ring = i >= 0 ? [...order.slice(i + 1), ...order.slice(0, i)] : order;
  for (const k of ring) {
    if (k !== current && undecided.has(k)) return k;
  }
  for (const k of undecided) {
    if (k !== current && !order.includes(k)) return k;
  }
  return null;
}

/** localDate is d's calendar date where the page runs, YYYY-MM-DD. */
export function localDate(d: Date): string {
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}-${mm}-${dd}`;
}

/**
 * notNowUntil is the until condition a Not now form makes with value (what
 * the form asks for), or null while it is incomplete. A "days" form fills its
 * hole with today + the days serve sends; a fixed form (asks "") is complete
 * as it is; any other fills its hole with the trimmed value.
 */
export function notNowUntil(
  form: NotNowForm,
  value: string,
  now: Date,
): string | null {
  if (!form.asks) return form.template;
  let v = value.trim();
  if (form.asks === 'days') {
    const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    d.setDate(d.getDate() + (form.days ?? 0));
    v = localDate(d);
  }
  if (!v) return null;
  return form.template.replace('%s', v);
}

/**
 * choicesForKeys is the cards a selection shares: the dispositions every
 * selected kind allows, in the first kind's order, each with serve's label
 * and sentence. When the kinds word a choice differently, their labels are
 * joined ("Leave it open / Keep it") and the first kind's sentence is kept.
 */
export function choicesForKeys(
  vocab: DecisionVocabView,
  keys: string[],
): ChoiceVocab[] {
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  const lists = kinds.map(
    (k) => (vocab.kinds ?? []).find((v) => v.kind === k)?.choices ?? [],
  );
  if (lists.length === 0) return [];
  const out: ChoiceVocab[] = [];
  for (const first of lists[0]) {
    const same = lists.map((l) =>
      l.find((x) => x.disposition === first.disposition),
    );
    if (same.some((x) => !x)) continue;
    const labels = [...new Set(same.map((x) => x!.label))];
    out.push({
      ...first,
      label: labels.join(' / '),
      outward: same.some((x) => x!.outward),
    });
  }
  return out;
}

// The nouns the sheet counts a selection of one kind in.
const KIND_NOUN: Record<string, [string, string]> = {
  pr: ['pull request', 'pull requests'],
  issue: ['issue', 'issues'],
  branch: ['branch', 'branches'],
  worktree: ['worktree', 'worktrees'],
  repo: ['repository', 'repositories'],
};

/**
 * fillLabel is the decide sheet's fill button: the choice and how many it
 * covers, in the kind's noun when the selection is one kind
 * ("Close it · 4 issues"), else in items.
 */
export function fillLabel(label: string, keys: string[]): string {
  const kinds = [...new Set(keys.map(kindFromKey))];
  const noun = kinds.length === 1 ? KIND_NOUN[kinds[0]] : undefined;
  const count = noun
    ? pluralize(keys.length, noun[0], noun[1])
    : pluralize(keys.length, 'item');
  return `${label} \u00b7 ${count}`;
}

// ---- recommendations in the list --------------------------------------------

/**
 * agentFromSource is the name a proposal's source shows as: "pi:session-id"
 * → "pi", "rule:id" → "rule".
 */
export function agentFromSource(source: string): string {
  const i = source.indexOf(':');
  return i === -1 ? source : source.slice(0, i);
}

/**
 * choiceLabel is serve's label for disposition on key's kind ("Close it");
 * watch reads as Not now. The disposition itself before the vocabulary has
 * loaded, or when serve has no card for it.
 */
export function choiceLabel(
  vocab: DecisionVocabView | null,
  key: string,
  disposition: string,
): string {
  const d = disposition === 'watch' ? 'wait' : disposition;
  if (!vocab) return disposition;
  return (
    choicesForKeys(vocab, [key]).find((c) => c.disposition === d)?.label ??
    disposition
  );
}

/** recommendLine is a list row's line for its pending proposal. */
export function recommendLine(
  vocab: DecisionVocabView | null,
  key: string,
  source: string,
  disposition: string,
): string {
  return `${agentFromSource(source)} recommends ${choiceLabel(vocab, key, disposition)}`;
}

/** What agreeGroups reads from a list row. */
export interface Recommendable {
  key: string;
  proposal?: {
    id: number;
    disposition: string;
    source: string;
    state: string;
  } | null;
}

/** A group of rows whose pending proposals share an agent and a choice. */
export interface AgreeGroup {
  agent: string;
  disposition: string;
  /** serve's label, joined when the kinds word it differently. */
  label: string;
  /** The choice goes to To apply (close, merge, archive, delete). */
  outward: boolean;
  ids: number[];
  keys: string[];
}

/**
 * agreeGroups groups the rows shown whose pending proposals come from the
 * same agent and recommend the same choice, two or more to a group, in the
 * order each group first appears. "agree with all" accepts exactly a
 * group's ids.
 */
export function agreeGroups(
  vocab: DecisionVocabView,
  items: Recommendable[],
): AgreeGroup[] {
  const groups = new Map<
    string,
    { agent: string; d: string; rows: Recommendable[] }
  >();
  for (const it of items) {
    const p = it.proposal;
    if (!p || p.state !== 'pending') continue;
    const agent = agentFromSource(p.source);
    const d = p.disposition === 'watch' ? 'wait' : p.disposition;
    const id = `${agent}\u0000${d}`;
    const g = groups.get(id) ?? { agent, d, rows: [] };
    g.rows.push(it);
    groups.set(id, g);
  }
  const out: AgreeGroup[] = [];
  for (const g of groups.values()) {
    if (g.rows.length < 2) continue;
    const keys = g.rows.map((r) => r.key);
    const c = choicesForKeys(vocab, keys).find((x) => x.disposition === g.d);
    out.push({
      agent: g.agent,
      disposition: g.d,
      label: c?.label ?? g.d,
      outward: c?.outward ?? false,
      ids: g.rows.map((r) => r.proposal!.id),
      keys,
    });
  }
  return out;
}

/**
 * agreeText is a group's line in the list foot: what is recommended, and the
 * action ("agree with all 3"; an outward choice "send all 2 to To apply").
 */
export function agreeText(g: AgreeGroup): { says: string; action: string } {
  const n = g.ids.length;
  return {
    says: `${g.agent} recommends ${g.label} for ${n}`,
    action: g.outward ? `send all ${n} to To apply` : `agree with all ${n}`,
  };
}

/** recommendedLine is the line at the top of Attention. */
export function recommendedLine(recommended: number, notYet: number): string {
  return `${recommended} recommended \u00b7 ${notYet} not yet`;
}

// The kinds each Not now form that names something searches and accepts.
export const PICK_KINDS: Record<string, string[]> = {
  pr: ['pr'],
  'pr-or-issue': ['pr', 'issue'],
  repo: ['repo'],
};

const NAME = '[\\w.-]+';
const GH_URL = new RegExp(
  `^https?://(?:www\\.)?github\\.com/(${NAME})/(${NAME})(?:/(pull|issues)/(\\d+))?(?:[/?#].*)?$`,
  'i',
);
const KIND_KEY = new RegExp(`^(pr|issue):(${NAME})/(${NAME})#(\\d+)$`, 'i');
const REPO_KEY = new RegExp(`^(?:repo:)?(${NAME})/(${NAME})$`, 'i');
const BARE_KEY = new RegExp(`^(${NAME})/(${NAME})#(\\d+)$`);

/**
 * pastedKeys turns a pasted GitHub URL (…/pull/58, …/issues/58, a repo's
 * page) or a key (pr:owner/repo#58, owner/repo#58, repo:owner/repo,
 * owner/repo) into the keys it names that the form asks for (PICK_KINDS):
 * one, or two for owner/repo#58 under "pr-or-issue" (it may be either).
 * Owner and repo are lower-cased, as casebook's keys are. Anything else (a
 * title, a number, a URL of another kind) is null.
 */
export function pastedKeys(text: string, asks: string): string[] | null {
  const kinds = PICK_KINDS[asks] ?? [];
  const s = text.trim();
  const repo = (o: string, r: string): string =>
    `${o}/${r.replace(/\.git$/i, '')}`.toLowerCase();
  const num = (n: string): number | null => (Number(n) > 0 ? Number(n) : null);
  const url = GH_URL.exec(s);
  const kindKey = KIND_KEY.exec(s);
  const repoKey = REPO_KEY.exec(s);
  const bare = BARE_KEY.exec(s);
  let out: string[] = [];
  if (url) {
    const [, o, r, part, n] = url;
    if (!part) out = [`repo:${repo(o, r)}`];
    else if (num(n) !== null)
      out = [`${part === 'pull' ? 'pr' : 'issue'}:${repo(o, r)}#${num(n)}`];
  } else if (kindKey) {
    const [, k, o, r, n] = kindKey;
    if (num(n) !== null) out = [`${k.toLowerCase()}:${repo(o, r)}#${num(n)}`];
  } else if (repoKey) {
    out = [`repo:${repo(repoKey[1], repoKey[2])}`];
  } else if (bare) {
    const [, o, r, n] = bare;
    if (num(n) !== null)
      out = ['pr', 'issue'].map((k) => `${k}:${repo(o, r)}#${num(n)}`);
  }
  out = out.filter((k) => kinds.includes(kindFromKey(k)));
  return out.length ? out : null;
}
