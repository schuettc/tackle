// decide.ts — the page's decisions, no DOM. Tested with node --test
// (decide.test.ts) against a fake server.
//
// A decision is not shown before the server has it. Each request returns
// the server's snapshot of every file (or row) it touched, with the print
// a next decision answers, and the page shows that snapshot: a print is
// never held apart from the content it came with. One request per group of
// linked files (or per row) at a time: while one is in flight the group's
// decisions do nothing, and neither does Send. Send is exclusive: while it
// is in flight nothing else runs, and it carries the decisions the page
// shows; the server sends only those still in force and names them, and
// only those are marked sent. Every decision, clear and Send names what
// the page showed of each item it covers: the id of the decision in force
// (new each time a decision is made, never reused) and the item's print. A
// 409 reloads the group from the server and shows that. An open editor is
// bound to the snapshot it opened on (snapRow, snapFile): its save names
// that snapshot, never a fresher one shown since, and a 409 says the item
// changed under it. Notes are the exception: they approve nothing, so they
// are shown at once and saved on their own (and wait while Send is in
// flight).

import type { DecisionIn } from './api.ts';
import { holdFiles, seenFor } from './files.ts';
import { editTarget, holdRows } from './model.ts';

/** What the page showed of a group's files: each one's print, and the id
 * of its decision (none when undecided). */
export interface GroupSeen {
  prints: Record<string, string>;
  decisions: Record<string, string>;
}

/** What the page showed of one item: the id of its decision ('' for none)
 * and its print (for an edited merge, the print covers the target's). */
export interface Seen {
  decision_id: string;
  fingerprint: string;
}

/** An open row editor's snapshot: the row as the editor opened on it,
 * content, print and decision, and every row's print then (the merge
 * target an edit picks is bound to the one the person saw). */
export interface RowSnap {
  row: Finding;
  prints: Record<string, string>;
}

/** An open file editor's snapshot: the file as the editor opened on it,
 * and its group as the page showed it then. */
export interface FileSnap {
  file: FileView;
  seen: GroupSeen;
}

/** How a save went: saved; busy (did nothing); changed (409: the item is
 * not as the page showed it, and the page now shows it as it is); failed. */
export type Saved = 'saved' | 'busy' | 'changed' | 'failed';

/** The calls the decisions make (api.ts's client, or a test's fake). */
export interface DecisionServer {
  review(): Promise<Review>;
  decideFile(
    round: number,
    file: string,
    d: FileDecision,
    seen: GroupSeen,
  ): Promise<FileView[]>;
  clearFile(round: number, file: string, seen: GroupSeen): Promise<FileView[]>;
  decide(round: number, ds: DecisionIn[]): Promise<Finding[]>;
  clear(
    round: number,
    id: string,
    fingerprint: string,
    decisionID: string,
  ): Promise<Finding[]>;
  note(
    round: number,
    on: { file?: string; id?: string },
    note: string,
  ): Promise<void>;
  send(round: number, shown: Shown): Promise<Sent>;
}

/** The decisions the page shows unsent, which a Send covers: by file key
 * and by row id, each as its decision id and its item's print. */
export interface Shown {
  files: Record<string, Seen>;
  rows: Record<string, Seen>;
}

/** What a Send sent: how many, to whom, and which (file keys, row ids). */
export interface Sent {
  sent: number;
  to: string;
  files: string[];
  rows: string[];
}

/** What the decisions need from the page. */
export interface Hooks {
  /** The review the page shows. */
  review(): Review | null;
  /** Shows another review whole (the round moved on under a reload). */
  replace(r: Review): void;
  /** Something shown changed: a snapshot, or a group going busy or idle. */
  changed(): void;
  /** A request failed; conflict when it was a 409 and the group was
   * reloaded. */
  failed(msg: string, conflict: boolean): void;
}

export interface Decider {
  /** Some of keys ('f:<file>' or 'r:<row>') has a request in flight, or a
   * Send is (which holds every key). */
  busy(keys: string[]): boolean;
  /** No request in flight at all: Send and a reload may go. */
  idle(): boolean;
  /** A Send is in flight. */
  readonly sending: boolean;
  /** Counts the snapshots shown; a reload that started before one landed
   * is older than the page. */
  readonly epoch: number;
  /** A note given and not saved yet (key 'f:<file>' or 'r:<row>'). */
  noteOf(key: string): string | undefined;
  /** Each resolves true once the server has it and the page shows it;
   * false when it did nothing (busy) or failed (said through failed). */
  file(key: string, d: FileDecision): Promise<boolean>;
  clearFile(key: string): Promise<boolean>;
  rows(ds: { id: string; d: Decision }[]): Promise<boolean>;
  clearRow(id: string): Promise<boolean>;
  /** An editor's snapshot of a row or file as the page shows it now; null
   * when there is none. */
  snapRow(id: string): RowSnap | null;
  snapFile(key: string): FileSnap | null;
  /** Picks the recommended version of a file: an accept. When the page
   * shows the person's own version (an edit), which an accept would keep,
   * it first takes the edit back (a clear, bound to what the page shows),
   * then accepts the recommendation the clear's snapshot shows, in one
   * turn: the group waits for both. */
  recommend(key: string): Promise<boolean>;
  /** Saves an edit against the snapshot its editor opened on. */
  editRow(snap: RowSnap, d: Decision): Promise<Saved>;
  editFile(snap: FileSnap, content: string): Promise<Saved>;
  noteFile(key: string, text: string): void;
  noteRow(id: string, text: string): void;
  /** Send what the page shows unsent, unless a request is in flight (null
   * then); marks sent what the server says it sent. A failure throws. */
  send(): Promise<Sent | null>;
}

const statusOf = (err: unknown) => (err as { status?: number }).status;
const staleErr = (err: unknown) =>
  (err as { isStale?: boolean }).isStale === true;
const msgOf = (err: unknown) => (err instanceof Error ? err.message : 'failed');

export function createDecider(server: DecisionServer, hooks: Hooks): Decider {
  const locks = new Set<string>();
  const notes = new Map<string, string>();
  let noting = 0;
  let sending = false;
  let epoch = 0;

  const busy = (keys: string[]) => sending || keys.some((k) => locks.has(k));
  const idle = () => locks.size === 0 && noting === 0 && !sending;
  const fileOf = (key: string) =>
    hooks.review()?.files.find((f) => f.key === key);
  const rowOf = (id: string) => hooks.review()?.rows.find((r) => r.id === id);
  const groupKeys = (f: FileView) => [
    ...new Set([f.key, ...f.group].map((k) => `f:${k}`)),
  ];
  /** The review the page shows, if it is still round's. */
  const shownRound = (round: number) => {
    const r = hooks.review();
    return r?.round?.id === round ? r : null;
  };

  /** Shows the server's state of keys (and the groups they are in now). */
  async function reload(round: number, keys: string[]): Promise<void> {
    const r = await server.review();
    epoch++;
    const cur = shownRound(round);
    if (!cur || r.round?.id !== round) {
      hooks.replace(r);
      return;
    }
    const files = new Set<string>();
    for (const k of keys) {
      if (!k.startsWith('f:')) continue;
      files.add(k.slice(2));
      for (const g of r.files.find((f) => f.key === k.slice(2))?.group ?? [])
        files.add(g);
    }
    holdFiles(
      cur.files,
      r.files.filter((f) => files.has(f.key)),
    );
    const rows = new Set(
      keys.filter((k) => k.startsWith('r:')).map((k) => k.slice(2)),
    );
    holdRows(
      cur.rows,
      r.rows.filter((x) => rows.has(x.id)),
    );
    cur.progress = r.progress;
  }

  /** Runs a request for keys, unless one is in flight for any of them. */
  async function run(
    round: number,
    keys: string[],
    call: () => Promise<void>,
  ): Promise<Saved> {
    if (busy(keys)) return 'busy';
    keys.forEach((k) => locks.add(k));
    hooks.changed();
    try {
      await call();
      epoch++;
      return 'saved';
    } catch (err) {
      if (staleErr(err)) return 'failed';
      if (statusOf(err) === 409) {
        try {
          await reload(round, keys);
          hooks.failed(
            `The review changed; this shows it now. ${msgOf(err)}`.trim(),
            true,
          );
        } catch (e) {
          if (!staleErr(e))
            hooks.failed(`The review changed; reload: ${msgOf(e)}`, true);
        }
        return 'changed';
      }
      hooks.failed(`Not saved: ${msgOf(err)}`, false);
      return 'failed';
    } finally {
      keys.forEach((k) => locks.delete(k));
      hooks.changed();
      flush(keys);
    }
  }

  /** Saves the notes given for keys while they were busy. */
  function flush(keys: string[]): void {
    for (const k of keys) {
      const text = notes.get(k);
      if (text === undefined) continue;
      if (k.startsWith('f:')) {
        if (fileOf(k.slice(2))?.decision) noteFile(k.slice(2), text);
      } else if (rowOf(k.slice(2))?.decision) noteRow(k.slice(2), text);
    }
  }

  /** A note on a decision the page shows: shown at once, saved alone. */
  function saveNote(
    key: string,
    d: { note?: string; sent?: boolean },
    text: string,
    on: { file?: string; id?: string },
  ): void {
    const round = hooks.review()?.round?.id;
    notes.delete(key);
    if (!round || (d.note ?? '') === text) return;
    const old = { note: d.note, sent: d.sent };
    d.note = text;
    d.sent = false;
    noting++;
    hooks.changed();
    server
      .note(round, on, text)
      .catch(async (err: unknown) => {
        if (staleErr(err)) return;
        d.note = old.note;
        d.sent = old.sent;
        if (statusOf(err) === 409) {
          await reload(round, [key]).catch(() => {});
          hooks.failed(`Note not saved: ${msgOf(err)}`, true);
        } else hooks.failed(`Note not saved: ${msgOf(err)}`, false);
      })
      .finally(() => {
        noting--;
        hooks.changed();
      });
  }

  function noteFile(key: string, text: string): void {
    const f = fileOf(key);
    if (!f) return;
    const k = `f:${key}`;
    if (!f.decision || busy(groupKeys(f))) {
      // Held for the decision that takes it, or saved after the one in
      // flight.
      if (text || f.decision) notes.set(k, text);
      else notes.delete(k);
      return;
    }
    saveNote(k, f.decision, text, { file: key });
  }

  function noteRow(id: string, text: string): void {
    const r = rowOf(id);
    if (!r) return;
    const k = `r:${id}`;
    if (!r.decision || busy([k])) {
      if (text || r.decision) notes.set(k, text);
      else notes.delete(k);
      return;
    }
    saveNote(k, r.decision, text, { id });
  }

  const saved = (p: Promise<Saved>) => p.then((s) => s === 'saved');

  /** A file decision on key against seen (its group as the page showed
   * it); the note is the one given, else the decision's. */
  function decideFile(
    key: string,
    d: FileDecision,
    seen: GroupSeen,
    f: FileView,
  ): Promise<Saved> {
    const round = hooks.review()?.round?.id;
    if (!round) return Promise.resolve('failed');
    const keys = groupKeys(f);
    if (busy(keys)) return Promise.resolve('busy');
    const k = `f:${key}`;
    // A disagreement brings its own note; any other decision the one given
    // in the note field, else the decision's.
    const full: FileDecision = {
      ...d,
      note: d.note ?? notes.get(k) ?? fileOf(key)?.decision?.note ?? '',
    };
    return run(round, keys, async () => {
      const snap = await server.decideFile(round, key, full, seen);
      if (notes.get(k) === full.note) notes.delete(k);
      const cur = shownRound(round);
      if (cur) holdFiles(cur.files, snap);
    });
  }

  /** Row decisions, each against what the page showed of its row (and,
   * for an edit to merge:C, of C): seen gives them. */
  function decideRows(
    ds: { id: string; d: Decision }[],
    seen: (id: string) => { row: Finding; target: (id: string) => string },
  ): Promise<Saved> {
    const round = hooks.review()?.round?.id;
    if (!round || !ds.length) return Promise.resolve('failed');
    const keys = ds.map((x) => `r:${x.id}`);
    if (busy(keys)) return Promise.resolve('busy');
    const body: DecisionIn[] = [];
    for (const { id, d } of ds) {
      const { row, target } = seen(id);
      const t = editTarget(d);
      body.push({
        id,
        action: d.action,
        verdict: d.verdict,
        title: d.title,
        text: d.text,
        cleared: d.cleared,
        note: notes.get(`r:${id}`) ?? rowOf(id)?.decision?.note ?? '',
        fingerprint: row.fingerprint,
        decision_id: row.decision?.id ?? '',
        target_fingerprint: t ? target(t) : undefined,
      });
    }
    return run(round, keys, async () => {
      const snap = await server.decide(round, body);
      for (const b of body)
        if (notes.get(`r:${b.id}`) === b.note) notes.delete(`r:${b.id}`);
      const cur = shownRound(round);
      if (cur) holdRows(cur.rows, snap);
    });
  }

  return {
    busy,
    idle,
    get sending() {
      return sending;
    },
    get epoch() {
      return epoch;
    },
    noteOf: (key) => notes.get(key),

    // A decision or clear from the page binds to the item as it shows it
    // now; an edit binds to its editor's snapshot (editRow, editFile).
    file(key, d) {
      const r = hooks.review();
      const f = fileOf(key);
      if (!r || !f) return Promise.resolve(false);
      return saved(decideFile(key, d, seenFor(r.files, key), f));
    },

    clearFile(key) {
      const r = hooks.review();
      const round = r?.round?.id;
      const f = fileOf(key);
      if (!r || !round || !f?.decision) return Promise.resolve(false);
      const seen = seenFor(r.files, key);
      return saved(
        run(round, groupKeys(f), async () => {
          const snap = await server.clearFile(round, key, seen);
          const cur = shownRound(round);
          if (cur) holdFiles(cur.files, snap);
        }),
      );
    },

    recommend(key) {
      const r = hooks.review();
      const round = r?.round?.id;
      const f = fileOf(key);
      if (!r || !round || !f) return Promise.resolve(false);
      const seen = seenFor(r.files, key);
      if (f.decision?.action !== 'edit')
        return saved(decideFile(key, { action: 'accept' }, seen, f));
      const k = `f:${key}`;
      const note = notes.get(k) ?? f.decision.note ?? '';
      return saved(
        run(round, groupKeys(f), async () => {
          const cleared = await server.clearFile(round, key, seen);
          let cur = shownRound(round);
          if (cur) holdFiles(cur.files, cleared);
          // The recommendation, as the clear's snapshot shows it.
          const snap = await server.decideFile(
            round,
            key,
            { action: 'accept', note },
            seenFor(cleared, key),
          );
          if (notes.get(k) === note) notes.delete(k);
          cur = shownRound(round);
          if (cur) holdFiles(cur.files, snap);
        }),
      );
    },

    rows(ds) {
      if (ds.some((x) => !rowOf(x.id))) return Promise.resolve(false);
      return saved(
        decideRows(ds, (id) => ({
          row: rowOf(id)!,
          target: (t) => rowOf(t)?.fingerprint ?? '',
        })),
      );
    },

    clearRow(id) {
      const round = hooks.review()?.round?.id;
      const row = rowOf(id);
      if (!round || !row?.decision) return Promise.resolve(false);
      const print = row.fingerprint;
      const decision = row.decision.id ?? '';
      return saved(
        run(round, [`r:${id}`], async () => {
          const snap = await server.clear(round, id, print, decision);
          const cur = shownRound(round);
          if (cur) holdRows(cur.rows, snap);
        }),
      );
    },

    snapRow(id) {
      const r = hooks.review();
      const row = rowOf(id);
      if (!r || !row) return null;
      return {
        row: structuredClone(row),
        prints: Object.fromEntries(r.rows.map((x) => [x.id, x.fingerprint])),
      };
    },

    snapFile(key) {
      const r = hooks.review();
      const f = fileOf(key);
      if (!r || !f) return null;
      return { file: structuredClone(f), seen: seenFor(r.files, key) };
    },

    editRow(snap, d) {
      if (!rowOf(snap.row.id)) return Promise.resolve('changed');
      return decideRows([{ id: snap.row.id, d }], () => ({
        row: snap.row,
        target: (t) => snap.prints[t] ?? '',
      }));
    },

    editFile(snap, content) {
      const f = fileOf(snap.file.key);
      if (!f) return Promise.resolve('changed');
      return decideFile(
        snap.file.key,
        { action: 'edit', content },
        snap.seen,
        f,
      );
    },

    noteFile,
    noteRow,

    async send() {
      const r = hooks.review();
      const round = r?.round?.id;
      if (!r || !round || !idle()) return null;
      const shown: Shown = { files: {}, rows: {} };
      for (const f of r.files)
        if (f.decision && !f.decision.sent)
          shown.files[f.key] = {
            decision_id: f.decision.id ?? '',
            fingerprint: f.fingerprint,
          };
      for (const x of r.rows)
        if (x.decision && !x.decision.sent)
          shown.rows[x.id] = {
            decision_id: x.decision.id ?? '',
            fingerprint: x.fingerprint,
          };
      sending = true;
      hooks.changed();
      try {
        const out = await server.send(round, shown);
        epoch++;
        const cur = shownRound(round);
        if (cur) {
          // Only what the server sent; nothing else changed on this page
          // while Send was in flight.
          for (const f of cur.files)
            if (f.decision && out.files.includes(f.key)) f.decision.sent = true;
          for (const x of cur.rows)
            if (x.decision && out.rows.includes(x.id)) x.decision.sent = true;
          if (out.sent) cur.sends++;
        }
        return out;
      } finally {
        sending = false;
        hooks.changed();
        flush([...notes.keys()]);
      }
    },
  };
}
