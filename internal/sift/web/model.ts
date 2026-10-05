// model.ts — the per-item page's logic (a backlog round's rows), no DOM:
// groups, the list's entries, progress, what Send carries, group decisions
// and edits. An audit round's files are in files.ts. Tested with
// node --test (model.test.ts).

/** A group of rows decided together: by kind in a backlog round (by file
 * and check for other rows). */
export interface Group {
  key: string;
  kicker: string;
  title: string;
  rows: Finding[];
}

export type Entry =
  | { kind: 'group'; key: string; group: Group }
  | { kind: 'row'; key: string; row: Finding; group: Group; member: boolean };

// The verdicts (internal/sift/row.ValidVerdict).
export const PLAIN_VERDICTS = [
  'keep',
  'delete',
  'rewrite',
  'move',
  'issue',
  'global',
  'private',
  'ask',
];
const TRACKED = /^tracked:[\w.-]+(?:\/[\w.-]+)?#\d+$/;

export function validVerdict(v: string): boolean {
  if (PLAIN_VERDICTS.includes(v)) return true;
  for (const p of ['merge:', 'drop:'])
    if (v.startsWith(p) && v.slice(p.length).trim() !== '') return true;
  if (v.startsWith('close:')) {
    const r = v.slice('close:'.length);
    return r === 'done' || r === 'obsolete' || TRACKED.test(r);
  }
  return false;
}

// The checks in the spec's order, with their words.
const CHECKS: [string, string, string][] = [
  ['size', 'size finding', 'size findings'],
  ['load-limit', 'load limit', 'load limits'],
  ['duplicate', 'duplicate', 'duplicates'],
  ['dead-path', 'dead path', 'dead paths'],
  ['stale-status', 'stale status', 'stale statuses'],
  ['retired-store', 'retired-store pointer', 'retired-store pointers'],
  ['misplaced', 'misplaced line', 'misplaced lines'],
  ['secret', 'secret', 'secrets'],
  ['intake', 'intake row', 'intake rows'],
];
const checkOrder = (c: string) => {
  const i = CHECKS.findIndex(([k]) => k === c);
  return i < 0 ? CHECKS.length : i;
};

/** "3 stale statuses", "1 dead path". */
export function checkCount(check: string, n: number): string {
  const w = CHECKS.find(([k]) => k === check);
  return `${n} ${w ? (n === 1 ? w[1] : w[2]) : check}`;
}

/** A row's file as the user knows it: repo/path, or ~/… under home. */
export function displayPath(src: Source, home: string): string {
  if (src.repo && src.path) {
    const name = src.repo.replace(/\/+$/, '').split('/').pop() ?? src.repo;
    return `${name}/${src.path}`;
  }
  if (!src.file) return src.entry ?? '';
  if (home && (src.file === home || src.file.startsWith(home + '/')))
    return '~' + src.file.slice(home.length);
  return src.file;
}

/** The row an edit chooses to merge into ('' for any other decision): the
 * page sends that row's fingerprint with the edit (target_fingerprint). */
export function editTarget(d: Decision): string {
  if (d.action !== 'edit' || !d.verdict?.startsWith('merge:')) return '';
  return d.verdict.slice('merge:'.length);
}

/** After a Send: every decision is sent. */
export function markSent(rows: Finding[]): void {
  for (const r of rows) if (r.decision) r.decision.sent = true;
}

/** A decision's snapshot, shown: each row the server returned replaces the
 * page's, its decision and fingerprint together. */
export function holdRows(rows: Finding[], snap: Finding[]): void {
  for (const r of snap) {
    const i = rows.findIndex((x) => x.id === r.id);
    if (i >= 0) rows[i] = r;
  }
}

/** A backlog round groups by kind: its kind says so, or every row is intake. */
export function isBacklog(round: ReviewRound | null, rows: Finding[]): boolean {
  if (round?.kind === 'backlog') return true;
  return rows.length > 0 && rows.every((r) => r.check === 'intake');
}

/** The verdict that counts now: an edit's, else the proposal's. */
export function verdictOf(r: Finding): string {
  return (
    (r.decision?.action === 'edit' && r.decision.verdict) || r.verdict || ''
  );
}

/** A title or text as it counts now (row.Effective): an edit's value, ''
 * when the edit cleared it, else the proposal's. */
export function fieldOf(r: Finding, k: 'title' | 'text'): string {
  const d = r.decision?.action === 'edit' ? r.decision : undefined;
  if (d?.[k]) return d[k];
  if (d?.cleared?.includes(k)) return '';
  return r[k] ?? '';
}

const plural = (n: number, one: string, many: string) =>
  `${n} ${n === 1 ? one : many}`;

export function groupsOf(
  rows: Finding[],
  backlog: boolean,
  home: string,
): Group[] {
  const by = new Map<string, Group>();
  const order: string[] = [];
  const add = (key: string, kicker: string, r: Finding) => {
    let g = by.get(key);
    if (!g) {
      g = { key, kicker, title: '', rows: [] };
      by.set(key, g);
      order.push(key);
    }
    g.rows.push(r);
  };
  if (backlog) {
    for (const r of rows) {
      const v = verdictOf(r);
      if (v === 'issue')
        add(`issue:${r.destination ?? ''}`, 'issues to file', r);
      else if (v.startsWith('close:')) add('closes', 'closes', r);
      else add('decisions', 'decisions', r);
    }
    const rank = (k: string) =>
      k.startsWith('issue:') ? 0 : k === 'decisions' ? 1 : 2;
    order.sort((a, b) => rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0));
    return order.map((k) => {
      const g = by.get(k)!;
      const n = g.rows.length;
      g.title = k.startsWith('issue:')
        ? `${k.slice('issue:'.length) || 'no repo'} · ${plural(n, 'issue', 'issues')}`
        : k === 'closes'
          ? `${n} to close`
          : `${n} to decide`;
      return g;
    });
  }
  for (const r of rows) {
    const p = displayPath(r.source, home);
    add(`${p}|${r.check}`, p, r);
  }
  const groups = order.map((k) => by.get(k)!);
  groups.sort(
    (a, b) =>
      (a.kicker < b.kicker ? -1 : a.kicker > b.kicker ? 1 : 0) ||
      checkOrder(a.rows[0].check) - checkOrder(b.rows[0].check),
  );
  for (const g of groups) {
    g.rows.sort((a, b) => (a.source.start ?? 0) - (b.source.start ?? 0));
    g.title = checkCount(g.rows[0].check, g.rows.length);
  }
  return groups;
}

/** The list's entries: a group of one is its row; a group is expanded while
 * it or one of its rows is open. */
export function entries(groups: Group[], open: string | null): Entry[] {
  const out: Entry[] = [];
  for (const g of groups) {
    if (g.rows.length === 1) {
      out.push({
        kind: 'row',
        key: `r:${g.rows[0].id}`,
        row: g.rows[0],
        group: g,
        member: false,
      });
      continue;
    }
    out.push({ kind: 'group', key: `g:${g.key}`, group: g });
    const expanded =
      open === `g:${g.key}` || g.rows.some((r) => open === `r:${r.id}`);
    if (expanded)
      for (const r of g.rows)
        out.push({
          kind: 'row',
          key: `r:${r.id}`,
          row: r,
          group: g,
          member: true,
        });
  }
  return out;
}

/** Where to go after deciding `key`: the next undecided row, else the next
 * entry, else stay. */
export function nextOpen(es: Entry[], key: string): string {
  const i = es.findIndex((e) => e.key === key);
  if (i < 0) return key;
  for (let j = i + 1; j < es.length; j++) {
    const e = es[j];
    if (e.kind === 'row' && !e.row.decision) return e.key;
    if (e.kind === 'group' && e.group.rows.some((r) => !r.decision))
      return e.key;
  }
  return es[i + 1]?.key ?? key;
}

/** Decided of the rows. */
export function progress(rows: Finding[]): { decided: number; total: number } {
  return { decided: rows.filter((r) => r.decision).length, total: rows.length };
}

/** What Send carries: the unsent decisions. */
export function unsent(rows: Finding[]): number {
  return rows.filter((r) => r.decision && !r.decision.sent).length;
}

/** The rows a group decision touches: never one already decided, and
 * accept only where there is a proposal. */
export function groupTargets(
  rows: Finding[],
  action: 'accept' | 'reject',
): Finding[] {
  return rows.filter(
    (r) => !r.decision && (action === 'reject' || !!r.verdict),
  );
}

export interface EditForm {
  verdict: string;
  title: string;
  text: string;
}

export type EditDecision = {
  action: 'edit';
  verdict?: string;
  title?: string;
  text?: string;
  cleared?: ('title' | 'text')[];
};

export type EditResult =
  { ok: true; decision: EditDecision } | { ok: false; error: string };

/** The edit decision for a form: only the fields that differ from the
 * proposal. A field the user emptied is cleared explicitly: an empty value
 * would mean "keep the proposal's". */
export function editDecision(r: Finding, f: EditForm): EditResult {
  const verdict = f.verdict.trim();
  if (!verdict) return { ok: false, error: 'choose a verdict' };
  if (!validVerdict(verdict))
    return { ok: false, error: `“${verdict}” is not a verdict` };
  const d: EditDecision = { action: 'edit' };
  if (verdict !== (r.verdict ?? '')) d.verdict = verdict;
  const cleared: ('title' | 'text')[] = [];
  for (const k of ['title', 'text'] as const) {
    const now = f[k];
    if (now === (r[k] ?? '')) continue;
    if (now.trim() !== '') d[k] = now;
    else if (r[k]) cleared.push(k);
  }
  if (cleared.length) d.cleared = cleared;
  if (!d.verdict && !d.title && !d.text && !d.cleared)
    return { ok: false, error: 'nothing changed: accept the proposal instead' };
  return { ok: true, decision: d };
}

/** A row's title in the list: an issue's title, else its passage's first
 * line, else the check's summary. */
export function rowTitle(r: Finding): string {
  if (r.title) return r.title;
  const first = (r.passage ?? '')
    .split('\n')
    .map((l) => l.trim())
    .find((l) => l !== '');
  return first || r.summary;
}

/** The list's right-hand word for a row. */
export function rowMeta(r: Finding): string {
  if (!r.decision) return '·';
  if (r.decision.action === 'edit') return `edited · ${verdictOf(r)}`;
  if (r.decision.action === 'accept') return `accepted · ${r.verdict ?? ''}`;
  return 'rejected';
}
