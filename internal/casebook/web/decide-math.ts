// decide-math.ts — pure decision-vocabulary helpers with no browser or kit
// imports.  This module is safe to import in node --test unit tests.
//
// These functions are the selection math the decide sheet uses:
//   kindFromKey   — extract the kind prefix from a casebook key
//   allowedForKind — look up allowed dispositions for one kind from server vocab
//   allowedForKeys — intersect allowed sets for a mixed-kind selection

import type {
  ChoiceVocab,
  CountWords,
  DecisionVocabView,
  GroupView,
  ItemView,
  LookIntoVocab,
  NotNowForm,
  RuleRow,
  Session,
} from './wire.d.ts';
import { sessionName } from './sessions.ts';

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
 * nextInView is the item to open after current is decided: the first key
 * after current in order (the view's rows as the page had them, every page
 * it loaded) that is still in the view, then the view's rows past the last
 * of those, then (wrapping) the view's first row. fresh is the view as serve
 * lists it after the decision, in its order, as far as it has been fetched;
 * complete says it holds the whole view. An item decided elsewhere meanwhile
 * is not in fresh and is passed over, never counted as a step. MORE when
 * the answer may be in a page of fresh not fetched yet; null when nothing is
 * left. When current is no longer in order, the search starts at order's
 * beginning.
 */
/** MORE is nextInView's answer when a page not fetched yet may hold it. */
export const MORE: unique symbol = Symbol('more');

export function nextInView(
  order: string[],
  current: string,
  fresh: string[],
  complete: boolean,
): string | null | typeof MORE {
  const live = fresh.filter((k) => k !== current);
  const inView = new Set(live);
  const pos = new Map(order.map((k, i) => [k, i]));
  // The furthest row of order fresh has reached: a row before it that isn't
  // in fresh has left the view; a row after it may be on a later page.
  let reached = -1;
  for (const k of live) reached = Math.max(reached, pos.get(k) ?? -1);
  const i = order.indexOf(current);
  for (const k of i >= 0 ? order.slice(i + 1) : order) {
    if (inView.has(k)) return k;
    if (!complete && (pos.get(k) ?? 0) > reached) return MORE;
  }
  // Past order's rows: the first row of fresh after the last one order holds.
  let last = -1;
  live.forEach((k, j) => {
    if (pos.has(k)) last = j;
  });
  const past = live.slice(last + 1).find((k) => !pos.has(k));
  if (past) return past;
  if (!complete) return MORE;
  return live[0] ?? null;
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

/**
 * recommendLine is the line for an item's pending proposal, in serve's
 * words: the session that recommends (by, named as the rows name it; the
 * source's agent when serve knows none), then the choice ("‹session›
 * recommends Close it").
 */
export function recommendLine(
  vocab: DecisionVocabView,
  key: string,
  by: Session | undefined,
  source: string,
  disposition: string,
): string {
  const who = vocab.recommends.replaceAll('{session}', sessionName(by, source));
  return `${who} ${choiceLabel(vocab, key, disposition)}`;
}

/** CAME_BACK is the id of decide's "Came back" group (serve's). */
export const CAME_BACK = 'came-back';

/**
 * rowSub is a decide row's second line: why it came back (its own reason,
 * or serve's words for its status), then its pending recommendation's
 * reason. The flat list has no group heads, so there the reason follows
 * the choice ("Close it · stale"). A left-open row has none.
 */
export function rowSub(
  vocab: DecisionVocabView | null,
  it: ItemView,
  flat: boolean,
): string | undefined {
  const p =
    it.status !== 'left-open' && it.proposal?.state === 'pending'
      ? it.proposal
      : undefined;
  return (
    [
      it.group === CAME_BACK
        ? it.due_reason || vocab?.came_back?.[it.status]
        : it.new_activity
          ? it.due_reason
          : undefined,
      p && flat ? choiceLabel(vocab, it.key, p.disposition) : undefined,
      p?.note || undefined,
    ]
      .filter(Boolean)
      .join(' \u00b7 ') || undefined
  );
}

/**
 * decideSummary is the middle pane's summary with nothing open, one line
 * each (serve's words): how many to decide, how many came back, what each
 * rule (by its name) and agent recommends and for how many, and how many
 * are not recommended yet. A line with nothing to count is left out.
 */
export function decideSummary(
  groups: GroupView[],
  total: number,
  rules: RuleRow[],
  vocab: DecisionVocabView,
): string[] {
  const w = vocab.summary;
  const n = (t: string, k: number) => t.replaceAll('{n}', String(k));
  const lines = [n(w.to_decide, total)];
  const back = groups.find((g) => g.id === CAME_BACK);
  if (back?.total) lines.push(n(w.came_back, back.total));
  for (const g of groups) {
    if (!g.source || !g.total) continue;
    const rule = g.source.startsWith('rule:') ? g.source.slice(5) : '';
    const who = rule
      ? rules.find((r) => r.rule.id === rule)?.rule.name || rule
      : sessionName(g.by, g.source);
    lines.push(
      n(w.group, g.total)
        .replaceAll('{who}', who)
        .replaceAll('{decision}', g.label),
    );
  }
  const none = groups.find((g) => g.id === 'none');
  if (none?.total) lines.push(n(w.not_recommended, none.total));
  return lines;
}

// ---- the groups of decide's list --------------------------------------------

/**
 * countWord is the word for a group button's {n} (serve's count_words, the
 * rule serve's CountWord states): the ticked items when some of the group's
 * total are ticked ("3"), otherwise the whole group ("it", "both", "all
 * 27"). Ticking every item counts as the whole group.
 */
export function countWord(
  total: number,
  ticked: number,
  words: CountWords,
): string {
  if (ticked > 0 && ticked < total)
    return words.some.replaceAll('{n}', String(ticked));
  if (total === 1) return words.one;
  if (total === 2) return words.two;
  return words.all.replaceAll('{n}', String(total));
}

/**
 * groupButtons is a group's three buttons in serve's words, for the whole
 * group (ticked 0) or the ticked items of it: its choice for several items
 * ("Close all 27", "Close 3"), look into them, and discard their
 * recommendations.
 */
export function groupButtons(
  g: GroupView,
  ticked: number,
  vocab: Pick<DecisionVocabView, 'look_many' | 'discard_many' | 'count_words'>,
): { act: string; look: string; discard: string } {
  const n = countWord(g.total, ticked, vocab.count_words);
  const fill = (s: string): string => s.replaceAll('{n}', n);
  return {
    act: fill(g.many || g.label),
    look: fill(vocab.look_many),
    discard: fill(vocab.discard_many),
  };
}

/**
 * lookIntoCard is the open item's "Look into it" card, in serve's words
 * (the vocabulary's look_into): its label, its sentence with the session
 * named, and the message it sends about key.
 */
export function lookIntoCard(
  words: LookIntoVocab,
  session: string,
  key: string,
): { label: string; says: string; message: string } {
  return {
    label: words.label,
    says: words.says.replaceAll('{session}', session),
    message: words.message.replaceAll('{key}', key),
  };
}

/**
 * discardConfirm is the confirm Discard opens for n recommendations in
 * view, in serve's words: discarding withdraws them, with no judgement;
 * the items go back to the agent.
 */
export function discardConfirm(
  vocab: Pick<
    DecisionVocabView,
    'discard_confirm' | 'discard_one' | 'discard_some'
  >,
  n: number,
  view: string,
): string {
  const count =
    n === 1
      ? vocab.discard_one
      : vocab.discard_some.replaceAll('{n}', String(n));
  return vocab.discard_confirm
    .replaceAll('{count}', count)
    .replaceAll('{view}', view);
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

/** A LatestSearch runs a search per input, after a pause in typing. */
export interface LatestSearch {
  /** input is the field's text changing. */
  input(text: string): void;
  /** cancel drops any search under way or waiting. */
  cancel(): void;
}

/**
 * latestSearch searches with find delay ms after the last input and
 * publishes what it found for that text. Every input makes what was shown
 * stale at once (stale) and drops any answer still to come for older text,
 * so only the answer for the newest text is ever published.
 */
export function latestSearch<T>(
  find: (text: string) => Promise<T>,
  publish: (text: string, found: T) => void,
  delay: number,
  stale: () => void,
): LatestSearch {
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const run = async (my: number, text: string): Promise<void> => {
    const found = await find(text);
    if (my !== seq) return;
    publish(text, found);
  };
  return {
    input(text) {
      const my = ++seq;
      stale();
      clearTimeout(timer);
      timer = setTimeout(() => void run(my, text), delay);
    },
    cancel() {
      seq++;
      clearTimeout(timer);
    },
  };
}

// ---- the left-open group -------------------------------------------------------

/**
 * leftOpenMeta is a left-open row's meta: "left open · Oct 6", the date it
 * was left open (with its year when that isn't now's).
 */
export function leftOpenMeta(
  decidedAt: string | undefined,
  now: Date = new Date(),
): string {
  if (!decidedAt) return 'left open';
  const d = new Date(decidedAt);
  const opts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' };
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return `left open \u00b7 ${d.toLocaleDateString('en-US', opts)}`;
}

// ---- « ‹ › »: the open item's place in the list ----------------------------

/** NavTargets is where the open item's « ‹ › » buttons go (pos 0: the item
 * is not in the list's rows). */
export interface NavTargets {
  prev?: string;
  next?: string;
  prevGroup?: string;
  nextGroup?: string;
  pos: number;
  total: number;
  label: string;
}

/**
 * navTargets is key's place in the list: the items before and after it in
 * order (the list's loaded rows in group order, a collapsed group's rows
 * included), the first items of the groups before and after its own, its
 * 1-based position in its group and the group's total and label (serve's,
 * every page).
 */
export function navTargets(
  order: { key: string; group: string }[],
  groups: GroupView[],
  key: string,
): NavTargets {
  const i = order.findIndex((r) => r.key === key);
  if (i < 0) return { pos: 0, total: 0, label: '' };
  const group = order[i].group;
  // The first row of each group, in order.
  const firsts: { group: string; at: number }[] = [];
  order.forEach((r, j) => {
    if (j === 0 || order[j - 1].group !== r.group)
      firsts.push({ group: r.group, at: j });
  });
  const g = firsts.findIndex((f) => f.group === group && f.at <= i);
  const start = firsts[g].at;
  const gv = groups.find((x) => x.id === group);
  const out: NavTargets = {
    pos: i - start + 1,
    total: gv?.total ?? order.filter((r) => r.group === group).length,
    label: gv?.label ?? '',
  };
  if (i > 0) out.prev = order[i - 1].key;
  if (i + 1 < order.length) out.next = order[i + 1].key;
  if (g > 0) out.prevGroup = order[firsts[g - 1].at].key;
  if (g + 1 < firsts.length) out.nextGroup = order[firsts[g + 1].at].key;
  return out;
}

/**
 * shortKey is a key as a nav button's tooltip names it: without its kind
 * and owner. "pr:o/nfl-dk#467" → "nfl-dk#467".
 */
export function shortKey(key: string): string {
  const k = keyWithoutKind(key);
  const i = k.indexOf('/');
  return i >= 0 ? k.slice(i + 1) : k;
}
