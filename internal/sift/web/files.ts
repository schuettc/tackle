// files.ts — an audit round's files on the page, no DOM: the list's
// entries (linked files together), each file's sizes and decision, the
// line diff between the audited file and its recommendation, and the
// server's snapshots of a group's files after a decision. Tested with node
// --test (files.test.ts).

import { displayPath } from './model.ts';

export interface Op {
  kind: ' ' | '-' | '+';
  /** The line's number in the audited file (kept and removed lines). */
  old?: number;
  /** The line's number in the recommendation (kept and added lines). */
  new?: number;
  text: string;
}

export interface Hunk {
  header: string;
  lines: Op[];
}

/** A file's lines, without the empty one after its last newline. */
export function splitLines(s: string): string[] {
  if (s === '') return [];
  const ls = s.split('\n');
  if (ls[ls.length - 1] === '') ls.pop();
  return ls;
}

// Past this many lines between the first and last change, a file is shown
// as removed and added whole rather than diffed line by line.
const LIMIT = 4000;

/** The line diff from a to b: kept, removed and added lines, numbered. */
export function diffLines(a: string, b: string): Op[] {
  const A = splitLines(a);
  const B = splitLines(b);
  let pre = 0;
  while (pre < A.length && pre < B.length && A[pre] === B[pre]) pre++;
  let suf = 0;
  while (
    suf < A.length - pre &&
    suf < B.length - pre &&
    A[A.length - 1 - suf] === B[B.length - 1 - suf]
  )
    suf++;
  const script = myers(
    A.slice(pre, A.length - suf),
    B.slice(pre, B.length - suf),
  );
  const out: Op[] = [];
  let i = 0;
  let j = 0;
  const keep = () => {
    out.push({ kind: ' ', old: i + 1, new: j + 1, text: A[i] });
    i++;
    j++;
  };
  while (i < pre) keep();
  for (const s of script) {
    if (s === '=') keep();
    else if (s === '-') out.push({ kind: '-', old: ++i, text: A[i - 1] });
    else out.push({ kind: '+', new: ++j, text: B[j - 1] });
  }
  while (i < A.length) keep();
  return out;
}

/** Myers' shortest edit script from a to b: '=' keep, '-' remove, '+' add. */
function myers(a: string[], b: string[]): ('=' | '-' | '+')[] {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  if (max === 0) return [];
  if (max > LIMIT)
    return [...a.map(() => '-' as const), ...b.map(() => '+' as const)];
  const off = max;
  const v = new Int32Array(2 * max + 2);
  const trace: Int32Array[] = []; // trace[d][k + d]: x after step d
  let done = false;
  for (let d = 0; d <= max && !done; d++) {
    for (let k = -d; k <= d; k += 2) {
      let x =
        k === -d || (k !== d && v[off + k - 1] < v[off + k + 1])
          ? v[off + k + 1]
          : v[off + k - 1] + 1;
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[off + k] = x;
      if (x >= n && y >= m) done = true;
    }
    trace.push(v.slice(off - d, off + d + 1));
  }
  const out: ('=' | '-' | '+')[] = [];
  let x = n;
  let y = m;
  for (let d = trace.length - 1; d > 0; d--) {
    const prev = trace[d - 1];
    const at = (k: number) => prev[k + d - 1];
    const k = x - y;
    const down = k === -d || (k !== d && at(k - 1) < at(k + 1));
    const pk = down ? k + 1 : k - 1;
    const px = at(pk);
    const x0 = down ? px : px + 1;
    while (x > x0 && y > x0 - k) {
      out.push('=');
      x--;
      y--;
    }
    out.push(down ? '+' : '-');
    x = px;
    y = px - pk;
  }
  while (x > 0 && y > 0) {
    out.push('=');
    x--;
    y--;
  }
  return out.reverse();
}

/** The changes with ctx lines of context; changes close together share a
 * hunk. */
export function hunks(ops: Op[], ctx: number): Hunk[] {
  const changed = ops.flatMap((o, i) => (o.kind === ' ' ? [] : [i]));
  const out: Hunk[] = [];
  let k = 0;
  while (k < changed.length) {
    let last = k;
    while (
      last + 1 < changed.length &&
      changed[last + 1] - changed[last] <= 2 * ctx + 1
    )
      last++;
    const from = Math.max(0, changed[k] - ctx);
    const to = Math.min(ops.length - 1, changed[last] + ctx);
    const lines = ops.slice(from, to + 1);
    const olds = lines.filter((l) => l.old !== undefined);
    const news = lines.filter((l) => l.new !== undefined);
    const start = (ls: Op[], f: 'old' | 'new') => (ls.length ? ls[0][f]! : 0);
    out.push({
      header: `@@ -${start(olds, 'old')},${olds.length} +${start(news, 'new')},${news.length} @@`,
      lines,
    });
    k = last + 1;
  }
  return out;
}

/** The audited lines the file's certain findings are about. */
export function certainLines(rows: Finding[]): Set<number> {
  const out = new Set<number>();
  for (const r of rows) {
    if (!r.certain || !r.source.start) continue;
    for (let n = r.source.start; n <= (r.source.end || r.source.start); n++)
      out.add(n);
  }
  return out;
}

export interface FileEntry {
  key: string;
  file: FileView;
  /** Part of a group of linked files, which sit together in the list. */
  linked: boolean;
}

const classRank: Record<string, number> = { global: 0, repo: 1, skill: 2 };
const order = (f: FileView, home: string): [number, string] => [
  classRank[f.class] ?? 3,
  displayPath(f.source, home),
];
const before = (a: [number, string], b: [number, string]) =>
  a[0] - b[0] || (a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0);

/** The list: every file with findings or a recommendation, global files
 * first, then repo files and skills by path; linked files sit together at
 * the place of the first of them. */
export function fileEntries(files: FileView[], home: string): FileEntry[] {
  const shown = files.filter((f) => f.rows.length > 0 || f.rec);
  const by = new Map(shown.map((f) => [f.key, f]));
  const groups = new Map<string, FileView[]>();
  for (const f of shown) {
    const members = f.group.filter((k) => by.has(k));
    const id = members.length ? [...members].sort()[0] : f.key;
    if (!groups.has(id))
      groups.set(
        id,
        members.map((k) => by.get(k)!),
      );
  }
  const sorted = [...groups.values()].map((g) =>
    [...g].sort((a, b) => before(order(a, home), order(b, home))),
  );
  sorted.sort((a, b) => before(order(a[0], home), order(b[0], home)));
  return sorted.flatMap((g) =>
    g.map((f) => ({ key: `f:${f.key}`, file: f, linked: g.length > 1 })),
  );
}

const kb = (n: number) => (n / 1000).toFixed(1);

const word: Record<string, string> = {
  accept: 'accepted',
  edit: 'edited',
  reject: 'rejected',
};

/** The list's right-hand words: sizes before and after, findings, the
 * decision. */
export function fileMeta(f: FileView): string {
  const d = f.decision ? word[f.decision.action] : '·';
  return `${kb(f.size)} → ${kb(f.after)} KB · ${f.rows.length} · ${d}`.replace(
    / · ·$/,
    ' ·',
  );
}

/** The prints a decision on key carries: each file of its group's. */
export function printsFor(
  files: FileView[],
  key: string,
): Record<string, string> {
  const f = files.find((x) => x.key === key);
  const out: Record<string, string> = {};
  for (const k of f?.group ?? [key]) {
    const m = files.find((x) => x.key === k);
    if (m) out[k] = m.fingerprint;
  }
  return out;
}

/** A decision's snapshot, shown: each file the server returned replaces
 * the page's, content and print together. */
export function holdFiles(files: FileView[], snap: FileView[]): void {
  for (const f of snap) {
    const i = files.findIndex((x) => x.key === f.key);
    if (i >= 0) files[i] = f;
  }
}

/** Decided of the files with a recommendation. */
export function filesProgress(files: FileView[]): {
  decided: number;
  total: number;
} {
  const open = files.filter((f) => f.rec);
  return { decided: open.filter((f) => f.decision).length, total: open.length };
}

/** The file decisions the next Send carries. */
export function unsentFiles(files: FileView[]): number {
  return files.filter((f) => f.decision && !f.decision.sent).length;
}
