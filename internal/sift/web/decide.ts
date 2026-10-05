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
// only those are marked sent. Every decision and clear carries the prints
// of what the page shows; a 409 reloads the group from the server and
// shows that. Notes are the exception: they approve nothing, so they are
// shown at once and saved on their own (and wait while Send is in flight).

import type { DecisionIn } from './api.ts';
import { holdFiles, printsFor } from './files.ts';
import { editTarget, holdRows } from './model.ts';

/** The calls the decisions make (api.ts's client, or a test's fake). */
export interface DecisionServer {
  review(): Promise<Review>;
  decideFile(
    round: number,
    file: string,
    d: FileDecision,
    prints: Record<string, string>,
  ): Promise<FileView[]>;
  clearFile(
    round: number,
    file: string,
    prints: Record<string, string>,
  ): Promise<FileView[]>;
  decide(round: number, ds: DecisionIn[]): Promise<Finding[]>;
  clear(round: number, id: string, fingerprint: string): Promise<Finding[]>;
  note(
    round: number,
    on: { file?: string; id?: string },
    note: string,
  ): Promise<void>;
  send(round: number, shown: Shown): Promise<Sent>;
}

/** The decisions the page shows unsent, which a Send covers: by file key
 * and by row id. */
export interface Shown {
  files: Record<string, FileDecision>;
  rows: Record<string, Decision>;
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
  ): Promise<boolean> {
    if (busy(keys)) return false;
    keys.forEach((k) => locks.add(k));
    hooks.changed();
    try {
      await call();
      epoch++;
      return true;
    } catch (err) {
      if (staleErr(err)) return false;
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
        return false;
      }
      hooks.failed(`Not saved: ${msgOf(err)}`, false);
      return false;
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

    file(key, d) {
      const r = hooks.review();
      const round = r?.round?.id;
      const f = fileOf(key);
      if (!r || !round || !f) return Promise.resolve(false);
      const keys = groupKeys(f);
      if (busy(keys)) return Promise.resolve(false);
      const k = `f:${key}`;
      const full: FileDecision = {
        ...d,
        note: notes.get(k) ?? f.decision?.note ?? '',
      };
      const prints = printsFor(r.files, key);
      return run(round, keys, async () => {
        const snap = await server.decideFile(round, key, full, prints);
        if (notes.get(k) === full.note) notes.delete(k);
        const cur = shownRound(round);
        if (cur) holdFiles(cur.files, snap);
      });
    },

    clearFile(key) {
      const r = hooks.review();
      const round = r?.round?.id;
      const f = fileOf(key);
      if (!r || !round || !f?.decision) return Promise.resolve(false);
      const prints = printsFor(r.files, key);
      return run(round, groupKeys(f), async () => {
        const snap = await server.clearFile(round, key, prints);
        const cur = shownRound(round);
        if (cur) holdFiles(cur.files, snap);
      });
    },

    rows(ds) {
      const r = hooks.review();
      const round = r?.round?.id;
      if (!r || !round || !ds.length) return Promise.resolve(false);
      const keys = ds.map((x) => `r:${x.id}`);
      if (busy(keys)) return Promise.resolve(false);
      const body: DecisionIn[] = [];
      for (const { id, d } of ds) {
        const row = rowOf(id);
        if (!row) return Promise.resolve(false);
        const target = editTarget(d);
        body.push({
          id,
          action: d.action,
          verdict: d.verdict,
          title: d.title,
          text: d.text,
          cleared: d.cleared,
          note: notes.get(`r:${id}`) ?? row.decision?.note ?? '',
          fingerprint: row.fingerprint,
          target_fingerprint: target ? rowOf(target)?.fingerprint : undefined,
        });
      }
      return run(round, keys, async () => {
        const snap = await server.decide(round, body);
        for (const b of body)
          if (notes.get(`r:${b.id}`) === b.note) notes.delete(`r:${b.id}`);
        const cur = shownRound(round);
        if (cur) holdRows(cur.rows, snap);
      });
    },

    clearRow(id) {
      const round = hooks.review()?.round?.id;
      const row = rowOf(id);
      if (!round || !row?.decision) return Promise.resolve(false);
      const print = row.fingerprint;
      return run(round, [`r:${id}`], async () => {
        const snap = await server.clear(round, id, print);
        const cur = shownRound(round);
        if (cur) holdRows(cur.rows, snap);
      });
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
          shown.files[f.key] = { ...f.decision };
      for (const x of r.rows)
        if (x.decision && !x.decision.sent)
          shown.rows[x.id] = { ...x.decision };
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
