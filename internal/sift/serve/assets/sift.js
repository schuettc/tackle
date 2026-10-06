// app.ts
import {
  bar,
  createKeys,
  h as h3,
  initTheme,
  list,
  live
} from "/_kit/kit.js";

// api.ts
import { createApi, ApiError } from "/_kit/kit.js";
function client(api) {
  return {
    review: () => api.get("/review"),
    async decide(round, decisions) {
      const r = await api.put("/decisions", {
        round,
        decisions
      });
      return r?.rows ?? [];
    },
    async clear(round, id, fingerprint, decisionID) {
      const q = new URLSearchParams({
        round: String(round),
        id,
        fingerprint,
        decision_id: decisionID
      });
      const r = await api.del(
        `/decisions?${q.toString()}`
      );
      return r?.rows ?? [];
    },
    async decideFile(round, file, d, seen) {
      const r = await api.put("/files", {
        round,
        file,
        action: d.action,
        content: d.content ?? "",
        note: d.note ?? "",
        prints: seen.prints,
        decisions: seen.decisions
      });
      return r?.files ?? [];
    },
    async clearFile(round, file, seen) {
      const r = await api.post("/files/clear", {
        round,
        file,
        prints: seen.prints,
        decisions: seen.decisions
      });
      return r?.files ?? [];
    },
    async note(round, on, note) {
      await api.put("/notes", { round, ...on, note });
    },
    base: (round, file) => api.get("/base", { round: String(round), file }),
    async send(round, shown) {
      const r = await api.post("/send", { round, ...shown });
      return {
        sent: r?.sent ?? 0,
        to: r?.to ?? "",
        files: r?.files ?? [],
        rows: r?.rows ?? []
      };
    },
    file: (round, id) => api.get("/file", { round: String(round), id })
  };
}
function newApi(opts) {
  return createApi(opts);
}

// model.ts
var PLAIN_VERDICTS = [
  "keep",
  "delete",
  "rewrite",
  "move",
  "issue",
  "global",
  "private",
  "ask"
];
var TRACKED = /^tracked:[\w.-]+(?:\/[\w.-]+)?#\d+$/;
function validVerdict(v) {
  if (PLAIN_VERDICTS.includes(v)) return true;
  for (const p of ["merge:", "drop:"])
    if (v.startsWith(p) && v.slice(p.length).trim() !== "") return true;
  if (v.startsWith("close:")) {
    const r = v.slice("close:".length);
    return r === "done" || r === "obsolete" || TRACKED.test(r);
  }
  return false;
}
var CHECKS = [
  ["size", "size finding", "size findings"],
  ["load-limit", "load limit", "load limits"],
  ["duplicate", "duplicate", "duplicates"],
  ["dead-path", "dead path", "dead paths"],
  ["stale-status", "stale status", "stale statuses"],
  ["retired-store", "retired-store pointer", "retired-store pointers"],
  ["misplaced", "misplaced line", "misplaced lines"],
  ["secret", "secret", "secrets"],
  ["intake", "intake row", "intake rows"]
];
var checkOrder = (c) => {
  const i = CHECKS.findIndex(([k]) => k === c);
  return i < 0 ? CHECKS.length : i;
};
function checkCount(check, n) {
  const w = CHECKS.find(([k]) => k === check);
  return `${n} ${w ? n === 1 ? w[1] : w[2] : check}`;
}
function displayPath(src, home) {
  if (src.repo && src.path) {
    const name = src.repo.replace(/\/+$/, "").split("/").pop() ?? src.repo;
    return `${name}/${src.path}`;
  }
  if (!src.file) return src.entry ?? "";
  if (home && (src.file === home || src.file.startsWith(home + "/")))
    return "~" + src.file.slice(home.length);
  return src.file;
}
function editTarget(d) {
  if (d.action !== "edit" || !d.verdict?.startsWith("merge:")) return "";
  return d.verdict.slice("merge:".length);
}
function holdRows(rows, snap) {
  for (const r of snap) {
    const i = rows.findIndex((x) => x.id === r.id);
    if (i >= 0) rows[i] = r;
  }
}
function verdictOf(r) {
  return r.decision?.action === "edit" && r.decision.verdict || r.verdict || "";
}
function fieldOf(r, k) {
  const d = r.decision?.action === "edit" ? r.decision : void 0;
  if (d?.cleared?.includes(k)) return "";
  if (d?.[k]) return d[k];
  return r[k] ?? "";
}
var plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
function groupsOf(rows, backlog, home) {
  const by = /* @__PURE__ */ new Map();
  const order2 = [];
  const add = (key, kicker, r) => {
    let g = by.get(key);
    if (!g) {
      g = { key, kicker, title: "", rows: [] };
      by.set(key, g);
      order2.push(key);
    }
    g.rows.push(r);
  };
  if (backlog) {
    for (const r of rows) {
      const v = verdictOf(r);
      if (v === "issue")
        add(`issue:${r.destination ?? ""}`, "issues to file", r);
      else if (v.startsWith("close:")) add("closes", "closes", r);
      else add("decisions", "decisions", r);
    }
    const rank = (k) => k.startsWith("issue:") ? 0 : k === "decisions" ? 1 : 2;
    order2.sort((a, b) => rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0));
    return order2.map((k) => {
      const g = by.get(k);
      const n = g.rows.length;
      g.title = k.startsWith("issue:") ? `${k.slice("issue:".length) || "no repo"} · ${plural(n, "issue", "issues")}` : k === "closes" ? `${n} to close` : `${n} to decide`;
      return g;
    });
  }
  for (const r of rows) {
    const p = displayPath(r.source, home);
    add(`${p}|${r.check}`, p, r);
  }
  const groups = order2.map((k) => by.get(k));
  groups.sort(
    (a, b) => (a.kicker < b.kicker ? -1 : a.kicker > b.kicker ? 1 : 0) || checkOrder(a.rows[0].check) - checkOrder(b.rows[0].check)
  );
  for (const g of groups) {
    g.rows.sort((a, b) => (a.source.start ?? 0) - (b.source.start ?? 0));
    g.title = checkCount(g.rows[0].check, g.rows.length);
  }
  return groups;
}
function entries(groups, open) {
  const out = [];
  for (const g of groups) {
    if (g.rows.length === 1) {
      out.push({
        kind: "row",
        key: `r:${g.rows[0].id}`,
        row: g.rows[0],
        group: g,
        member: false
      });
      continue;
    }
    out.push({ kind: "group", key: `g:${g.key}`, group: g });
    const expanded = open === `g:${g.key}` || g.rows.some((r) => open === `r:${r.id}`);
    if (expanded)
      for (const r of g.rows)
        out.push({
          kind: "row",
          key: `r:${r.id}`,
          row: r,
          group: g,
          member: true
        });
  }
  return out;
}
function nextOpen(es, key) {
  const i = es.findIndex((e) => e.key === key);
  if (i < 0) return key;
  for (let j = i + 1; j < es.length; j++) {
    const e = es[j];
    if (e.kind === "row" && !e.row.decision) return e.key;
    if (e.kind === "group" && e.group.rows.some((r) => !r.decision))
      return e.key;
  }
  return es[i + 1]?.key ?? key;
}
function progress(rows) {
  return { decided: rows.filter((r) => r.decision).length, total: rows.length };
}
function unsent(rows) {
  return rows.filter((r) => r.decision && !r.decision.sent).length;
}
function groupTargets(rows, action) {
  return rows.filter(
    (r) => !r.decision && (action === "reject" || !!r.verdict)
  );
}
function editDecision(r, f) {
  const verdict = f.verdict.trim();
  if (!verdict) return { ok: false, error: "choose a verdict" };
  if (!validVerdict(verdict))
    return { ok: false, error: `“${verdict}” is not a verdict` };
  const d = { action: "edit", verdict };
  let changed = verdict !== (r.verdict ?? "");
  const cleared = [];
  for (const k of ["title", "text"]) {
    const now = f[k];
    if (now !== (r[k] ?? "")) changed = true;
    if (now.trim() !== "") d[k] = now;
    else if (r[k]) cleared.push(k);
  }
  if (cleared.length) d.cleared = cleared;
  if (!changed)
    return { ok: false, error: "nothing changed: accept the proposal instead" };
  return { ok: true, decision: d };
}
function rowTitle(r) {
  const title = fieldOf(r, "title");
  if (title) return title;
  const first = (r.passage ?? "").split("\n").map((l) => l.trim()).find((l) => l !== "");
  return first || r.summary;
}
function rowMeta(r) {
  if (!r.decision) return "·";
  if (r.decision.action === "edit") return `edited · ${verdictOf(r)}`;
  if (r.decision.action === "accept") return `accepted · ${r.verdict ?? ""}`;
  return "rejected";
}

// files.ts
function splitLines(s) {
  if (s === "") return [];
  const ls = s.split("\n");
  if (ls[ls.length - 1] === "") ls.pop();
  return ls;
}
var LIMIT = 4e3;
function diffLines(a, b) {
  const A = splitLines(a);
  const B = splitLines(b);
  let pre = 0;
  while (pre < A.length && pre < B.length && A[pre] === B[pre]) pre++;
  let suf = 0;
  while (suf < A.length - pre && suf < B.length - pre && A[A.length - 1 - suf] === B[B.length - 1 - suf])
    suf++;
  const script = myers(
    A.slice(pre, A.length - suf),
    B.slice(pre, B.length - suf)
  );
  const out = [];
  let i = 0;
  let j = 0;
  const keep = () => {
    out.push({ kind: " ", old: i + 1, new: j + 1, text: A[i] });
    i++;
    j++;
  };
  while (i < pre) keep();
  for (const s of script) {
    if (s === "=") keep();
    else if (s === "-") out.push({ kind: "-", old: ++i, text: A[i - 1] });
    else out.push({ kind: "+", new: ++j, text: B[j - 1] });
  }
  while (i < A.length) keep();
  return out;
}
function myers(a, b) {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  if (max === 0) return [];
  if (max > LIMIT)
    return [...a.map(() => "-"), ...b.map(() => "+")];
  const off = max;
  const v = new Int32Array(2 * max + 2);
  const trace = [];
  let done = false;
  for (let d = 0; d <= max && !done; d++) {
    for (let k = -d; k <= d; k += 2) {
      let x2 = k === -d || k !== d && v[off + k - 1] < v[off + k + 1] ? v[off + k + 1] : v[off + k - 1] + 1;
      let y2 = x2 - k;
      while (x2 < n && y2 < m && a[x2] === b[y2]) {
        x2++;
        y2++;
      }
      v[off + k] = x2;
      if (x2 >= n && y2 >= m) done = true;
    }
    trace.push(v.slice(off - d, off + d + 1));
  }
  const out = [];
  let x = n;
  let y = m;
  for (let d = trace.length - 1; d > 0; d--) {
    const prev = trace[d - 1];
    const at = (k2) => prev[k2 + d - 1];
    const k = x - y;
    const down = k === -d || k !== d && at(k - 1) < at(k + 1);
    const pk = down ? k + 1 : k - 1;
    const px = at(pk);
    const x0 = down ? px : px + 1;
    while (x > x0 && y > x0 - k) {
      out.push("=");
      x--;
      y--;
    }
    out.push(down ? "+" : "-");
    x = px;
    y = px - pk;
  }
  while (x > 0 && y > 0) {
    out.push("=");
    x--;
    y--;
  }
  return out.reverse();
}
function hunks(ops, ctx) {
  const changed = ops.flatMap((o, i) => o.kind === " " ? [] : [i]);
  const out = [];
  let k = 0;
  while (k < changed.length) {
    let last = k;
    while (last + 1 < changed.length && changed[last + 1] - changed[last] <= 2 * ctx + 1)
      last++;
    const from = Math.max(0, changed[k] - ctx);
    const to = Math.min(ops.length - 1, changed[last] + ctx);
    const lines = ops.slice(from, to + 1);
    const olds = lines.filter((l) => l.old !== void 0);
    const news = lines.filter((l) => l.new !== void 0);
    const start = (ls, f) => ls.length ? ls[0][f] : 0;
    out.push({
      header: `@@ -${start(olds, "old")},${olds.length} +${start(news, "new")},${news.length} @@`,
      lines
    });
    k = last + 1;
  }
  return out;
}
function certainLines(rows) {
  const out = /* @__PURE__ */ new Set();
  for (const r of rows) {
    if (!r.certain || !r.source.start) continue;
    for (let n = r.source.start; n <= (r.source.end || r.source.start); n++)
      out.add(n);
  }
  return out;
}
var classRank = { global: 0, repo: 1, skill: 2 };
var order = (f, home) => [
  classRank[f.class] ?? 3,
  displayPath(f.source, home)
];
var before = (a, b) => a[0] - b[0] || (a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0);
function fileEntries(files, home) {
  const shown = files.filter(
    (f) => (f.rows.length > 0 || f.rec) && !f.unchanged
  );
  const by = new Map(shown.map((f) => [f.key, f]));
  const groups = /* @__PURE__ */ new Map();
  for (const f of shown) {
    const members = f.group.filter((k) => by.has(k));
    const id = members.length ? [...members].sort()[0] : f.key;
    if (!groups.has(id))
      groups.set(
        id,
        members.map((k) => by.get(k))
      );
  }
  const sorted = [...groups.values()].map(
    (g) => [...g].sort((a, b) => before(order(a, home), order(b, home)))
  );
  sorted.sort((a, b) => before(order(a[0], home), order(b[0], home)));
  return sorted.flatMap(
    (g) => g.map((f) => ({ key: `f:${f.key}`, file: f, linked: g.length > 1 }))
  );
}
var kb = (n) => (n / 1e3).toFixed(1);
function noChangeFiles(files, home) {
  return files.filter((f) => f.rec && f.unchanged).sort((a, b) => before(order(a, home), order(b, home)));
}
var choiceOf = {
  reject: "current",
  accept: "recommended",
  edit: "yours"
};
function chosen(f) {
  return f.decision ? choiceOf[f.decision.action] : null;
}
function didFor(f, row) {
  const a = f.rec?.findings.find((x) => x.row === row);
  const rec = a ? `${a.did}: ${a.how}` : "—";
  switch (chosen(f)) {
    case "current":
      return "left as it is";
    case "yours":
      return `your version (the recommendation: ${rec})`;
    default:
      return rec;
  }
}
function snippet(base, after, kind, n) {
  const ls = diffLines(base, after).filter((o) => o.kind === kind).map((o) => o.text);
  return ls.length > n ? [...ls.slice(0, n), "…"] : ls;
}
function fileMeta(f) {
  return `${kb(f.size)} → ${kb(f.after)} KB · ${f.rows.length} · ${chosen(f) ?? "not chosen"}`;
}
function seenFor(files, key) {
  const f = files.find((x) => x.key === key);
  const out = {
    prints: {},
    decisions: {}
  };
  for (const k of f?.group ?? [key]) {
    const m = files.find((x) => x.key === k);
    if (!m) continue;
    out.prints[k] = m.fingerprint;
    if (m.decision?.id) out.decisions[k] = m.decision.id;
  }
  return out;
}
function holdFiles(files, snap) {
  for (const f of snap) {
    const i = files.findIndex((x) => x.key === f.key);
    if (i >= 0) files[i] = f;
  }
}
function filesProgress(files) {
  const open = files.filter((f) => f.rec && !f.unchanged);
  return { decided: open.filter((f) => f.decision).length, total: open.length };
}
function unsentFiles(files) {
  return files.filter((f) => f.decision && !f.decision.sent).length;
}

// decide.ts
var statusOf = (err) => err.status;
var staleErr = (err) => err.isStale === true;
var msgOf = (err) => err instanceof Error ? err.message : "failed";
function createDecider(server, hooks) {
  const locks = /* @__PURE__ */ new Set();
  const notes = /* @__PURE__ */ new Map();
  let noting = 0;
  let sending = false;
  let epoch = 0;
  const busy = (keys) => sending || keys.some((k) => locks.has(k));
  const idle = () => locks.size === 0 && noting === 0 && !sending;
  const fileOf = (key) => hooks.review()?.files.find((f) => f.key === key);
  const rowOf = (id) => hooks.review()?.rows.find((r) => r.id === id);
  const groupKeys = (f) => [
    ...new Set([f.key, ...f.group].map((k) => `f:${k}`))
  ];
  const shownRound = (round) => {
    const r = hooks.review();
    return r?.round?.id === round ? r : null;
  };
  async function reload(round, keys) {
    const r = await server.review();
    epoch++;
    const cur = shownRound(round);
    if (!cur || r.round?.id !== round) {
      hooks.replace(r);
      return;
    }
    const files = /* @__PURE__ */ new Set();
    for (const k of keys) {
      if (!k.startsWith("f:")) continue;
      files.add(k.slice(2));
      for (const g of r.files.find((f) => f.key === k.slice(2))?.group ?? [])
        files.add(g);
    }
    holdFiles(
      cur.files,
      r.files.filter((f) => files.has(f.key))
    );
    const rows = new Set(
      keys.filter((k) => k.startsWith("r:")).map((k) => k.slice(2))
    );
    holdRows(
      cur.rows,
      r.rows.filter((x) => rows.has(x.id))
    );
    cur.progress = r.progress;
  }
  async function run(round, keys, call) {
    if (busy(keys)) return "busy";
    keys.forEach((k) => locks.add(k));
    hooks.changed();
    try {
      await call();
      epoch++;
      return "saved";
    } catch (err) {
      if (staleErr(err)) return "failed";
      if (statusOf(err) === 409) {
        try {
          await reload(round, keys);
          hooks.failed(
            `The review changed; this shows it now. ${msgOf(err)}`.trim(),
            true
          );
        } catch (e) {
          if (!staleErr(e))
            hooks.failed(`The review changed; reload: ${msgOf(e)}`, true);
        }
        return "changed";
      }
      hooks.failed(`Not saved: ${msgOf(err)}`, false);
      return "failed";
    } finally {
      keys.forEach((k) => locks.delete(k));
      hooks.changed();
      flush(keys);
    }
  }
  function flush(keys) {
    for (const k of keys) {
      const text = notes.get(k);
      if (text === void 0) continue;
      if (k.startsWith("f:")) {
        if (fileOf(k.slice(2))?.decision) noteFile(k.slice(2), text);
      } else if (rowOf(k.slice(2))?.decision) noteRow(k.slice(2), text);
    }
  }
  function saveNote(key, d, text, on) {
    const round = hooks.review()?.round?.id;
    notes.delete(key);
    if (!round || (d.note ?? "") === text) return;
    const old = { note: d.note, sent: d.sent };
    d.note = text;
    d.sent = false;
    noting++;
    hooks.changed();
    server.note(round, on, text).catch(async (err) => {
      if (staleErr(err)) return;
      d.note = old.note;
      d.sent = old.sent;
      if (statusOf(err) === 409) {
        await reload(round, [key]).catch(() => {
        });
        hooks.failed(`Note not saved: ${msgOf(err)}`, true);
      } else hooks.failed(`Note not saved: ${msgOf(err)}`, false);
    }).finally(() => {
      noting--;
      hooks.changed();
    });
  }
  function noteFile(key, text) {
    const f = fileOf(key);
    if (!f) return;
    const k = `f:${key}`;
    if (!f.decision || busy(groupKeys(f))) {
      if (text || f.decision) notes.set(k, text);
      else notes.delete(k);
      return;
    }
    saveNote(k, f.decision, text, { file: key });
  }
  function noteRow(id, text) {
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
  const saved = (p) => p.then((s) => s === "saved");
  function decideFile(key, d, seen, f) {
    const round = hooks.review()?.round?.id;
    if (!round) return Promise.resolve("failed");
    const keys = groupKeys(f);
    if (busy(keys)) return Promise.resolve("busy");
    const k = `f:${key}`;
    const full = {
      ...d,
      note: d.note ?? notes.get(k) ?? fileOf(key)?.decision?.note ?? ""
    };
    return run(round, keys, async () => {
      const snap = await server.decideFile(round, key, full, seen);
      if (notes.get(k) === full.note) notes.delete(k);
      const cur = shownRound(round);
      if (cur) holdFiles(cur.files, snap);
    });
  }
  function decideRows(ds, seen) {
    const round = hooks.review()?.round?.id;
    if (!round || !ds.length) return Promise.resolve("failed");
    const keys = ds.map((x) => `r:${x.id}`);
    if (busy(keys)) return Promise.resolve("busy");
    const body = [];
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
        note: notes.get(`r:${id}`) ?? rowOf(id)?.decision?.note ?? "",
        fingerprint: row.fingerprint,
        decision_id: row.decision?.id ?? "",
        target_fingerprint: t ? target(t) : void 0
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
        })
      );
    },
    recommend(key) {
      const r = hooks.review();
      const round = r?.round?.id;
      const f = fileOf(key);
      if (!r || !round || !f) return Promise.resolve(false);
      const seen = seenFor(r.files, key);
      if (f.decision?.action !== "edit")
        return saved(decideFile(key, { action: "accept" }, seen, f));
      const k = `f:${key}`;
      const note = notes.get(k) ?? f.decision.note ?? "";
      return saved(
        run(round, groupKeys(f), async () => {
          const cleared = await server.clearFile(round, key, seen);
          let cur = shownRound(round);
          if (cur) holdFiles(cur.files, cleared);
          const snap = await server.decideFile(
            round,
            key,
            { action: "accept", note },
            seenFor(cleared, key)
          );
          if (notes.get(k) === note) notes.delete(k);
          cur = shownRound(round);
          if (cur) holdFiles(cur.files, snap);
        })
      );
    },
    rows(ds) {
      if (ds.some((x) => !rowOf(x.id))) return Promise.resolve(false);
      return saved(
        decideRows(ds, (id) => ({
          row: rowOf(id),
          target: (t) => rowOf(t)?.fingerprint ?? ""
        }))
      );
    },
    clearRow(id) {
      const round = hooks.review()?.round?.id;
      const row = rowOf(id);
      if (!round || !row?.decision) return Promise.resolve(false);
      const print = row.fingerprint;
      const decision = row.decision.id ?? "";
      return saved(
        run(round, [`r:${id}`], async () => {
          const snap = await server.clear(round, id, print, decision);
          const cur = shownRound(round);
          if (cur) holdRows(cur.rows, snap);
        })
      );
    },
    snapRow(id) {
      const r = hooks.review();
      const row = rowOf(id);
      if (!r || !row) return null;
      return {
        row: structuredClone(row),
        prints: Object.fromEntries(r.rows.map((x) => [x.id, x.fingerprint]))
      };
    },
    snapFile(key) {
      const r = hooks.review();
      const f = fileOf(key);
      if (!r || !f) return null;
      return { file: structuredClone(f), seen: seenFor(r.files, key) };
    },
    editRow(snap, d) {
      if (!rowOf(snap.row.id)) return Promise.resolve("changed");
      return decideRows([{ id: snap.row.id, d }], () => ({
        row: snap.row,
        target: (t) => snap.prints[t] ?? ""
      }));
    },
    editFile(snap, content) {
      const f = fileOf(snap.file.key);
      if (!f) return Promise.resolve("changed");
      return decideFile(
        snap.file.key,
        { action: "edit", content },
        snap.seen,
        f
      );
    },
    noteFile,
    noteRow,
    async send() {
      const r = hooks.review();
      const round = r?.round?.id;
      if (!r || !round || !idle()) return null;
      const shown = { files: {}, rows: {} };
      for (const f of r.files)
        if (f.decision && !f.decision.sent)
          shown.files[f.key] = {
            decision_id: f.decision.id ?? "",
            fingerprint: f.fingerprint
          };
      for (const x of r.rows)
        if (x.decision && !x.decision.sent)
          shown.rows[x.id] = {
            decision_id: x.decision.id ?? "",
            fingerprint: x.fingerprint
          };
      sending = true;
      hooks.changed();
      try {
        const out = await server.send(round, shown);
        epoch++;
        const cur = shownRound(round);
        if (cur) {
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
    }
  };
}

// doc.ts
import {
  buttons,
  card,
  facts,
  fold,
  h as h2,
  noteField
} from "/_kit/kit.js";

// prose.ts
import { h } from "/_kit/kit.js";
function prose(src, o = {}) {
  const start = o.start ?? 1;
  const lines = src.split(/\r?\n/);
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  return h(
    "div",
    { class: "sift-prose" },
    lines.map((l, i) => {
      const n = start + i;
      const marked = !!o.mark && n >= o.mark[0] && n <= o.mark[1];
      return h(
        "div",
        { class: "sift-ln" + (marked ? " mark" : "") },
        h("span", { class: "sift-n", "aria-hidden": "true" }, String(n)),
        h("span", { class: "sift-t" }, l)
      );
    })
  );
}

// doc.ts
var where = (r, home) => {
  const p = displayPath(r.source, home);
  return r.source.start ? `${p}:${r.source.start}` : p;
};
function noteBlock(ctx, r) {
  const field = noteField({
    value: ctx.noteOf(r),
    placeholder: "why (n)",
    onCommit: (v) => ctx.setNote(r, v)
  });
  field.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.isComposing) field.blur();
  });
  return [h2("div", { class: "kit-label" }, "note"), field];
}
function wholeFile(ctx, r) {
  const body = h2("div", { class: "kit-muted" }, "loading…");
  const f = fold(
    r.source.ref ? `the whole file · at ${r.source.ref}` : "the whole file",
    body
  );
  let loaded = false;
  f.addEventListener("toggle", () => {
    if (!f.open || loaded) return;
    loaded = true;
    ctx.file(r).then(
      (out) => {
        const mark = r.source.start ? [r.source.start, r.source.end || r.source.start] : void 0;
        body.replaceChildren(
          prose(out.content, { mark }),
          out.truncated ? h2("p", { class: "sift-why" }, "cut short: the file is over 2 MB") : ""
        );
        body.className = "";
        body.querySelector(".mark")?.scrollIntoView?.({ block: "nearest" });
      },
      (err) => {
        loaded = false;
        body.textContent = `could not load the file: ${err.message}`;
      }
    );
  });
  return f;
}
function editForm(ctx, r) {
  const d = r.decision?.action === "edit" ? r.decision : void 0;
  const verdict = h2("input", {
    class: "sift-field sift-verdict",
    type: "text",
    value: d?.verdict || r.verdict || "",
    placeholder: "verdict: delete, rewrite, merge:ID, close:done…",
    "aria-label": "verdict",
    spellcheck: false
  });
  const title = h2("input", {
    class: "sift-field sift-title",
    type: "text",
    value: fieldOf(r, "title"),
    placeholder: "title (issue rows)",
    "aria-label": "title"
  });
  const text = h2("textarea", {
    class: "sift-field sift-text",
    "aria-label": "proposed text",
    rows: 6
  });
  text.value = d?.cleared?.includes("text") ? "" : fieldOf(r, "text") || r.passage || "";
  const err = h2("p", { class: "sift-err", role: "alert" });
  const pick = buttons(
    PLAIN_VERDICTS.map((v) => ({
      label: v,
      run() {
        verdict.value = v;
        verdict.focus();
      }
    }))
  );
  pick.classList.add("sift-verdicts");
  const save = () => {
    const msg = ctx.saveEdit(r, {
      verdict: verdict.value,
      title: title.value,
      text: text.value
    });
    err.textContent = msg ?? "";
  };
  const form = h2(
    "form",
    {
      class: "sift-edit",
      onsubmit: (e) => {
        e.preventDefault();
        save();
      }
    },
    h2("div", { class: "kit-label" }, "edit the proposal · verdict"),
    pick,
    verdict,
    h2("div", { class: "kit-label" }, "title"),
    title,
    h2("div", { class: "kit-label" }, "text"),
    text,
    err,
    buttons([
      { label: "save edit (⌘↵)", fill: true, run: save },
      { label: "cancel", run: () => ctx.cancelEdit() }
    ])
  );
  queueMicrotask(() => text.focus());
  return [form];
}
function rowDoc(ctx, r) {
  const parts = [
    h2("div", { class: "kit-kick" }, `${r.check} · ${where(r, ctx.home)}`),
    h2("h1", { class: "kit-h1" }, fieldOf(r, "title") || r.summary)
  ];
  const ev = (r.evidence ?? []).map((f) => [
    f.name,
    f.value
  ]);
  if (ev.length) parts.push(facts(ev));
  if (r.passage)
    parts.push(
      h2("div", { class: "kit-label" }, "now"),
      prose(r.passage, { start: r.source.start || 1 })
    );
  const v = verdictOf(r);
  const text = fieldOf(r, "text");
  if (text && v !== "delete")
    parts.push(
      h2(
        "div",
        { class: "kit-label" },
        r.decision?.action === "edit" && r.decision.text ? "your text" : "proposed text"
      ),
      prose(text, { start: r.source.start || 1 })
    );
  {
    const head = r.verdict ? `proposes · ${r.verdict}${r.destination ? ` → ${r.destination}` : ""}` : "no proposal yet";
    parts.push(
      card({
        edge: r.verdict ? "agent" : "wait",
        head,
        body: r.reason || (r.verdict ? "No reason given." : "The agent has not proposed a verdict for this row. Edit to give it one, or reject it to leave the file as it is.")
      }),
      h2("div", { class: "kit-label" }, "the proposal")
    );
    const a = r.decision?.action;
    const edited = a === "edit";
    const busy = ctx.busy([r]);
    const bs = [
      {
        label: edited ? "1 accept your edit" : "1 accept",
        fill: a === "accept",
        disabled: !v || busy,
        run: () => ctx.accept([r])
      },
      {
        label: "2 edit",
        fill: a === "edit",
        disabled: busy,
        run: () => ctx.startEdit(r)
      },
      {
        label: "3 reject",
        fill: a === "reject",
        danger: true,
        disabled: busy,
        run: () => ctx.reject([r])
      }
    ];
    if (r.decision)
      bs.push({
        label: edited ? "revert to the proposal (u)" : "clear (u)",
        disabled: busy,
        run: () => ctx.clear(r)
      });
    parts.push(buttons(bs));
    if (r.decision)
      parts.push(
        h2(
          "p",
          { class: "sift-why" },
          `your decision: ${rowMeta(r)}${r.decision.sent ? " · sent" : ""}`
        )
      );
    if (ctx.editing === r.id) parts.push(...editForm(ctx, ctx.edited ?? r));
  }
  parts.push(...noteBlock(ctx, r));
  if (r.source.file || r.source.repo) parts.push(wholeFile(ctx, r));
  return h2("div", { class: "kit-doc" }, ...parts);
}
function groupDoc(ctx, g) {
  const rows = g.rows;
  const decided = rows.filter((r) => r.decision).length;
  const proposed = rows.filter((r) => r.verdict).length;
  const parts = [
    h2("div", { class: "kit-kick" }, g.kicker),
    h2("h1", { class: "kit-h1" }, g.title)
  ];
  {
    const acc = groupTargets(rows, "accept");
    const rej = groupTargets(rows, "reject");
    parts.push(
      facts([
        ["decided", `${decided} of ${rows.length}`],
        ["with a proposal", String(proposed)]
      ]),
      card({
        edge: "agent",
        head: "decide the group",
        body: "Accept takes the proposal of every undecided row that has one; reject leaves every undecided row as it is. A row you decided on its own keeps its decision.",
        actions: [
          {
            label: `1 accept ${acc.length}`,
            fill: true,
            disabled: !acc.length || ctx.busy(rows),
            run: () => ctx.accept(acc)
          },
          {
            label: `3 reject ${rej.length}`,
            danger: true,
            disabled: !rej.length || ctx.busy(rows),
            run: () => ctx.reject(rej)
          },
          { label: "open the first", run: () => ctx.open(`r:${rows[0].id}`) }
        ]
      })
    );
  }
  parts.push(
    h2("div", { class: "kit-label" }, "rows"),
    h2(
      "table",
      { class: "sift-rows" },
      h2(
        "tr",
        null,
        h2("th", null, "line"),
        h2("th", null, "passage"),
        h2("th", null, "proposal"),
        h2("th", null, "decision")
      ),
      rows.map(
        (r) => h2(
          "tr",
          {
            onclick: () => ctx.open(`r:${r.id}`),
            tabindex: 0,
            onkeydown: (e) => e.key === "Enter" && ctx.open(`r:${r.id}`)
          },
          h2(
            "td",
            { class: "n" },
            r.source.start ? String(r.source.start) : "—"
          ),
          h2("td", { class: "p" }, rowTitle(r)),
          h2("td", { class: "n" }, verdictOf(r) || "—"),
          h2("td", { class: "n" }, rowMeta(r).split(" · ")[0])
        )
      )
    )
  );
  return h2("div", { class: "kit-doc" }, ...parts);
}
function message(kick, title, body) {
  return h2(
    "div",
    { class: "kit-doc" },
    h2("div", { class: "kit-kick" }, kick),
    h2("h1", { class: "kit-h1" }, title),
    h2("p", { class: "sift-lead" }, body)
  );
}
var kb2 = (n) => `${(n / 1e3).toFixed(1)} KB`;
var plural2 = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
function diffView(base, after, certain) {
  const hs = hunks(diffLines(base, after), 3);
  if (!hs.length)
    return h2("p", { class: "sift-why" }, "No change: the file stays as it is.");
  return h2(
    "div",
    { class: "sift-diff" },
    hs.flatMap((hk) => [
      h2("div", { class: "sift-hunk" }, hk.header),
      ...hk.lines.map((l) => {
        const cert = l.kind === "-" && l.old !== void 0 && certain.has(l.old);
        const cls = l.kind === "-" ? "del" : l.kind === "+" ? "add" : "ctx";
        return h2(
          "div",
          {
            class: `sift-dl ${cls}${cert ? " cert" : ""}`,
            title: cert ? "a certain finding: the rewrite must fix it" : void 0
          },
          h2(
            "span",
            { class: "sift-n", "aria-hidden": "true" },
            l.old ? String(l.old) : ""
          ),
          h2(
            "span",
            { class: "sift-n", "aria-hidden": "true" },
            l.new ? String(l.new) : ""
          ),
          h2(
            "span",
            { class: "sift-sign", "aria-hidden": "true" },
            cert ? "!" : l.kind
          ),
          h2("span", { class: "sift-t" }, l.text)
        );
      })
    ])
  );
}
function fileEdit(ctx, f) {
  const text = h2("textarea", {
    class: "sift-field sift-text sift-whole",
    "aria-label": "your version of the whole file",
    spellcheck: false
  });
  text.value = ctx.yours(f) ?? f.rec?.content ?? "";
  text.rows = Math.min(40, Math.max(12, splitLines(text.value).length + 2));
  const err = h2("p", { class: "sift-err", role: "alert" });
  const save = () => {
    err.textContent = ctx.saveEdit(f, text.value) ?? "";
  };
  queueMicrotask(() => text.focus());
  return h2(
    "form",
    {
      class: "sift-edit",
      onsubmit: (e) => {
        e.preventDefault();
        save();
      }
    },
    h2(
      "div",
      { class: "kit-label" },
      "your version · the whole file as it will be written"
    ),
    text,
    err,
    buttons([
      { label: "save edit (⌘↵)", fill: true, run: save },
      { label: "cancel", run: () => ctx.cancelEdit() }
    ])
  );
}
function findingsTable(f, rows) {
  return h2(
    "table",
    { class: "sift-rows sift-findings" },
    h2(
      "tr",
      null,
      h2("th", null, "line"),
      h2("th", null, "finding"),
      h2("th", null, "what the chosen version does")
    ),
    rows.map((r) => {
      const a = f.rec?.findings.find((x) => x.row === r.id);
      const did = didFor(f, r.id);
      const mark = a && did.startsWith(`${a.did}: `) ? [
        h2("span", { class: `sift-did ${a.did}` }, a.did),
        " ",
        did.slice(a.did.length + 2)
      ] : did;
      return h2(
        "tr",
        { class: r.certain ? "cert" : "" },
        h2(
          "td",
          { class: "n" },
          r.source.start ? String(r.source.start) : "file"
        ),
        h2(
          "td",
          { class: "p" },
          h2(
            "div",
            { class: "sift-check" },
            r.certain ? `${r.check} · certain` : r.check
          ),
          rowTitle(r)
        ),
        h2("td", { class: "p" }, mark)
      );
    })
  );
}
function choice(ctx, f, c, key, what, lines, sign) {
  const on = chosen(f) === c;
  const busy = ctx.busy(f);
  return h2(
    "div",
    {
      class: `sift-choice ${c}${on ? " chosen" : ""}`,
      role: "button",
      tabindex: 0,
      "aria-pressed": on ? "true" : "false",
      "aria-disabled": busy ? "true" : void 0,
      onclick: () => !busy && ctx.pick(f, c),
      onkeydown: (e) => {
        if ((e.key === "Enter" || e.key === " ") && !busy) {
          e.preventDefault();
          e.stopPropagation();
          ctx.pick(f, c);
        }
      }
    },
    h2("div", { class: "sift-ck" }, `${key} · ${c}${on ? " · chosen" : ""}`),
    h2("b", null, what),
    lines === null ? h2("div", { class: "sift-snip kit-muted" }, "loading…") : lines.length ? h2(
      "div",
      { class: `sift-snip ${sign === "-" ? "del" : "add"}` },
      lines.map(
        (l) => h2("div", null, l === "…" ? l : `${sign} ${l || " "}`)
      )
    ) : h2("div", { class: "sift-snip kit-muted" }, "no change")
  );
}
function fileDoc(ctx, f) {
  const rows = ctx.rowsOf(f);
  const certain = rows.filter((r) => r.certain).length;
  const others = f.group.filter((k) => k !== f.key).map((k) => ctx.files.find((x) => x.key === k)).filter((x) => !!x);
  const where2 = displayPath(f.source, ctx.home);
  const ev = [
    ["size", `${kb2(f.size)} → ${kb2(f.after)} · budget ${kb2(f.budget)}`],
    [
      "findings",
      certain ? `${rows.length}, ${certain} certain` : String(rows.length)
    ]
  ];
  if (others.length)
    ev.push([
      "linked",
      others.map((o) => displayPath(o.source, ctx.home)).join(", ")
    ]);
  if (f.source.ref)
    ev.push([
      "read at",
      f.commit ? `${f.source.ref} (${f.commit.slice(0, 10)})` : f.source.ref
    ]);
  const parts = [
    h2(
      "div",
      { class: "kit-kick" },
      `${f.class} file · ${plural2(rows.length, "finding")} · ${where2}`
    ),
    h2("h1", { class: "kit-h1" }, "Which version should this file have?"),
    h2("p", { class: "sift-lead sift-summary" }, f.rec?.summary ?? ""),
    facts(ev)
  ];
  const base = ctx.baseOf(f);
  const rec = f.rec?.content ?? "";
  const mine = ctx.yours(f);
  const snip = (after, sign) => typeof base === "string" ? snippet(base, after, sign, 4) : null;
  const pick = h2(
    "div",
    { class: `sift-pick${mine !== void 0 ? " three" : ""}` },
    choice(ctx, f, "current", "1", "Keep it as it is", snip(rec, "-"), "-"),
    choice(
      ctx,
      f,
      "recommended",
      "2",
      "Use the agent's version",
      snip(rec, "+"),
      "+"
    ),
    mine !== void 0 ? choice(
      ctx,
      f,
      "yours",
      "3",
      "Use your own version",
      snip(mine, "+"),
      "+"
    ) : ""
  );
  parts.push(pick);
  const busy = ctx.busy(f);
  const bs = [
    {
      label: mine !== void 0 ? "e · edit your version" : "e · write my own version",
      disabled: busy,
      run: () => ctx.startEdit(f)
    }
  ];
  bs.push({ label: "clear (u)", disabled: busy, run: () => ctx.clear(f) });
  const decide = holdClear(buttons(bs), !f.decision);
  decide.classList.add("sift-decide");
  parts.push(decide);
  const said = [];
  if (f.decision?.sent) said.push("sent");
  if (others.length)
    said.push(
      `picked together with ${others.map((o) => displayPath(o.source, ctx.home)).join(", ")}: the recommendation moves text between them`
    );
  if (busy) said.push("saving");
  if (said.length)
    parts.push(h2("p", { class: "sift-why" }, said.join(". ") + "."));
  if (ctx.editing === f.key) parts.push(fileEdit(ctx, ctx.edited ?? f));
  parts.push(
    h2(
      "div",
      { class: "kit-label" },
      "findings, and what the chosen version does about them"
    ),
    findingsTable(f, rows)
  );
  const field = noteField({
    value: ctx.noteOf(f),
    placeholder: "note to the agent (n)",
    onCommit: (v) => ctx.setNote(f, v)
  });
  field.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.isComposing) field.blur();
  });
  parts.push(h2("div", { class: "kit-label" }, "note"), field);
  const showMine = chosen(f) === "yours" && mine !== void 0;
  parts.push(
    h2(
      "div",
      { class: "kit-label" },
      showMine ? "the change · your version" : chosen(f) === "current" ? "the change · recommended, not picked" : "the change · recommended"
    )
  );
  if (base instanceof Error)
    parts.push(
      h2(
        "p",
        { class: "sift-err" },
        `could not load the audited file: ${base.message}`
      )
    );
  else if (base === void 0)
    parts.push(h2("p", { class: "kit-muted sift-loading" }, "loading…"));
  else parts.push(diffView(base, showMine ? mine : rec, certainLines(rows)));
  return h2("div", { class: "kit-doc sift-file" }, ...parts);
}
function holdClear(bs, none) {
  const c = bs.lastElementChild;
  if (c && none) {
    c.classList.add("sift-held");
    c.disabled = true;
    c.tabIndex = -1;
    c.setAttribute("aria-hidden", "true");
  }
  return bs;
}
function noChangeDoc(ctx, files) {
  const open = files.filter((f) => !f.decision);
  const parts = [
    h2(
      "div",
      { class: "kit-kick" },
      `nothing to change · ${plural2(files.length, "file")}`
    ),
    h2(
      "h1",
      { class: "kit-h1" },
      "The agent recommends no change to these files"
    ),
    h2(
      "p",
      { class: "sift-lead" },
      'Each finding below has its reason. Agree, and they stop showing until the text changes. Disagree on any file and say why: it moves to "to change" once the agent has rewritten it.'
    )
  ];
  for (const f of files) {
    const rows = ctx.rowsOf(f);
    const busy = ctx.busy(f);
    const a = f.decision?.action;
    const state = a === "accept" ? `agreed${f.muted ? " · muted" : ""}` : a === "reject" ? "disagreed: goes back to the agent" : "";
    const bs = [
      {
        label: "agree",
        fill: a === "accept",
        disabled: busy,
        run: () => ctx.agree(f)
      },
      {
        label: "disagree…",
        fill: a === "reject",
        danger: true,
        disabled: busy,
        run: () => ctx.startDisagree(f)
      }
    ];
    bs.push({ label: "clear", disabled: busy, run: () => ctx.clear(f) });
    const head = h2(
      "div",
      { class: "sift-nc-head" },
      h2(
        "div",
        { class: "sift-nc-what" },
        h2("div", { class: "sift-nc-path" }, displayPath(f.source, ctx.home)),
        h2(
          "div",
          { class: "sift-check" },
          [
            plural2(rows.length, "finding"),
            state,
            f.decision?.sent ? "sent" : ""
          ].filter(Boolean).join(" · ")
        )
      ),
      holdClear(buttons(bs), !f.decision)
    );
    const block = h2(
      "section",
      { class: `sift-nc${a ? ` ${a}` : ""}`, dataset: { key: f.key } },
      head
    );
    if (a === "reject" && f.decision?.note && ctx.disagreeing !== f.key)
      block.append(
        h2("p", { class: "sift-why" }, `your note: ${f.decision.note}`)
      );
    if (ctx.disagreeing === f.key) block.append(disagreeForm(ctx, f));
    block.append(
      h2(
        "table",
        { class: "sift-rows sift-findings" },
        h2(
          "tr",
          null,
          h2("th", null, "line"),
          h2("th", null, "finding"),
          h2("th", null, "why the agent keeps it")
        ),
        rows.map(
          (r) => h2(
            "tr",
            null,
            h2(
              "td",
              { class: "n" },
              r.source.start ? String(r.source.start) : "file"
            ),
            h2(
              "td",
              { class: "p" },
              h2("div", { class: "sift-check" }, r.check),
              rowTitle(r)
            ),
            h2(
              "td",
              { class: "p" },
              f.rec?.findings.find((x) => x.row === r.id)?.how ?? "—"
            )
          )
        )
      )
    );
    parts.push(block);
  }
  const all = buttons([
    {
      label: `a · agree with all ${open.length}`,
      fill: true,
      disabled: !open.length,
      run: () => ctx.agreeAll()
    }
  ]);
  all.classList.add("sift-agree-all");
  parts.push(all);
  return h2("div", { class: "kit-doc sift-nochange" }, ...parts);
}
function disagreeForm(ctx, f) {
  const note = h2("input", {
    class: "sift-field sift-disagree-note",
    type: "text",
    value: f.decision?.action === "reject" ? f.decision.note ?? "" : "",
    placeholder: "what should change? (the agent rewrites the file from this)",
    "aria-label": "why you disagree"
  });
  const err = h2("p", { class: "sift-err", role: "alert" });
  const send = () => {
    err.textContent = ctx.disagree(f, note.value) ?? "";
  };
  queueMicrotask(() => note.focus());
  return h2(
    "form",
    {
      class: "sift-disagree",
      onsubmit: (e) => {
        e.preventDefault();
        send();
      }
    },
    note,
    err,
    buttons([
      { label: "disagree (↵)", danger: true, fill: true, run: send },
      { label: "cancel", run: () => ctx.cancelDisagree() }
    ])
  );
}

// app.ts
var qs = new URLSearchParams(location.search);
var plural3 = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
var NOCHANGE = "nochange";
function frag(key) {
  return `#/open${key ? `/${encodeURIComponent(key)}` : ""}`;
}
function parseRoute(hash) {
  const m = /^#\/open(?:\/(.+))?$/.exec(hash);
  if (!m?.[1]) return null;
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return null;
  }
}
function signature(r) {
  return `${r.round?.id ?? 0}|${r.progress?.state}|${r.sends}|${r.applies.map((a) => a.repo + a.state).join(",")}|` + r.files.map((f) => {
    const d = f.decision;
    return `${f.key}:${f.fingerprint}:${d ? `${d.action}/${d.content?.length ?? 0}/${d.note ?? ""}/${d.sent ? 1 : 0}` : ""}`;
  }).join(";") + "|" + r.rows.map((x) => {
    const d = x.decision;
    return `${x.id}:${x.verdict ?? ""}:${x.text ?? ""}:${d ? `${d.action}/${d.verdict ?? ""}/${d.text ?? ""}/${d.title ?? ""}/${d.note ?? ""}/${d.sent ? 1 : 0}` : ""}`;
  }).join(";");
}
var perItem = (r) => r?.round?.kind === "backlog" || r?.round?.kind === "intake";
function boot() {
  let review = null;
  let openKey = null;
  let editing = null;
  let rowSnap = null;
  let fileSnap = null;
  let editGen = 0;
  let disagreeing = null;
  let ncNext = null;
  const mine = /* @__PURE__ */ new Map();
  let filter = "";
  let search = "";
  let lastFrag = "";
  let lastView = "";
  let syncing = false;
  let stale = false;
  let loading = false;
  let dirty = false;
  let flashing = false;
  let liveHandle = null;
  let statusTimer;
  let prevLive = "live";
  let loadTimer;
  let shown = [];
  const bases = /* @__PURE__ */ new Map();
  const gated = (input, init) => stale ? Promise.reject(new Error("sift serve restarted: this tab has stopped")) : fetch(input, init);
  const api = client(newApi({ fetch: gated, onStale: () => goStale() }));
  const decider = createDecider(api, {
    review: () => review,
    replace(r) {
      review = r;
      render();
    },
    changed() {
      if (decider.idle()) delete app.dataset.saving;
      else app.dataset.saving = "1";
      render();
      if (dirty && decider.idle()) void reload();
    },
    failed: (msg) => flash(msg, "danger")
  });
  const home = () => review?.home ?? "";
  const files = () => review?.files ?? [];
  const recommending = () => review?.progress?.state === "recommending";
  const matchesRow = (r) => {
    if (filter && r.check !== filter) return false;
    if (!search) return true;
    const q = search.toLowerCase();
    return [
      displayPath(r.source, home()),
      r.passage ?? "",
      r.summary,
      r.title ?? "",
      r.check
    ].some((s) => s.toLowerCase().includes(q));
  };
  const items = () => {
    if (perItem(review))
      return entries(
        groupsOf((review?.rows ?? []).filter(matchesRow), true, home()),
        openKey
      );
    const q = search.toLowerCase();
    const hit = (f) => !q || displayPath(f.source, home()).toLowerCase().includes(q);
    const out = fileEntries(files(), home()).filter((e) => hit(e.file)).map((e) => ({ ...e, kind: "file" }));
    const same = noChangeFiles(files(), home()).filter(hit);
    if (same.length) out.push({ kind: "nochange", key: NOCHANGE, files: same });
    return out;
  };
  const rowById = (id) => review?.rows.find((r) => r.id === id);
  const current = () => shown.find((e) => e.key === openKey);
  const baseKey = (f) => `${review?.round?.id ?? 0}:${f.key}:${f.base}`;
  const b = bar({
    brand: { name: "sift" },
    sections: [{ id: "review", label: "review", count: "" }],
    active: "review",
    status: "",
    staleText: "restarted · continued in a new tab"
  });
  const theme = initTheme("sift", b.themeControl);
  const forced = qs.get("theme");
  if (forced === "light" || forced === "dark") theme.set(forced);
  function baseStatus() {
    if (!review) return "";
    if (!review.round) return "no round yet — run sift check";
    const r = review.round;
    const day = r.at ? r.at.slice(0, 10) : "";
    const kind = perItem(review) ? r.kind : r.kind === "on-demand" ? "audit" : `${r.kind} audit`;
    const owner = r.owner ? ` · to ${r.owner}` : "";
    const state = review.progress?.state ? ` · ${review.progress.state}` : "";
    return `${kind} · round ${r.id} · ${day}${state}${owner}`;
  }
  function flash(text, tone = "muted") {
    clearTimeout(statusTimer);
    flashing = true;
    b.setStatus(text, { tone });
    statusTimer = setTimeout(
      () => {
        flashing = false;
        b.setStatus(baseStatus());
      },
      tone === "danger" ? 15e3 : 1e4
    );
  }
  function refreshBar() {
    const p = perItem(review) ? progress(review?.rows ?? []) : filesProgress(files());
    b.setCount(
      "review",
      review?.round && !recommending() ? `${p.decided}/${p.total}` : ""
    );
    const n = unsent(review?.rows ?? []) + unsentFiles(files());
    b.setPrimary(
      n && review?.round && !recommending() ? decider.idle() ? { label: `Send ${n}`, run: () => void send() } : { label: decider.sending ? "sending…" : "saving…", run: () => {
      } } : null
    );
    if (!flashing) b.setStatus(baseStatus());
  }
  const read = h3("main", { class: "kit-read" });
  const l = list({
    label: "files",
    views: [],
    onChip(group2, id) {
      if (group2 === "filter") {
        filter = filter === id ? "" : id;
        render();
      }
    },
    search: {
      placeholder: "search files",
      onInput(text) {
        search = text.trim();
        render();
      }
    },
    row(e) {
      if (e.kind === "nochange") {
        const n = e.files.length;
        const kept = e.files.reduce((t, f) => t + f.rows.length, 0);
        const agreed2 = e.files.filter(
          (f) => f.decision?.action === "accept"
        ).length;
        const disagreed = e.files.filter(
          (f) => f.decision?.action === "reject"
        ).length;
        return {
          id: e.key,
          key: `nothing to change · ${plural3(n, "file")}`,
          title: `${plural3(n, "file")}, ${plural3(kept, "finding")} kept`,
          meta: agreed2 + disagreed ? [
            agreed2 ? `${agreed2} agreed` : "",
            disagreed ? `${disagreed} disagreed` : ""
          ].filter(Boolean).join(" · ") : "reasons given · not reviewed"
        };
      }
      if (e.kind === "file") {
        const f = e.file;
        return {
          id: e.key,
          key: displayPath(f.source, home()),
          title: f.rec?.summary.split(/(?<=\.)\s/)[0] || `${plural3(f.rows.length, "finding")}`,
          meta: fileMeta(f)
        };
      }
      if (e.kind === "group") {
        const g = e.group;
        const done = g.rows.filter((r2) => r2.decision).length;
        return {
          id: e.key,
          key: g.kicker,
          title: g.title,
          meta: done === g.rows.length ? "✓" : `${done}/${g.rows.length}`
        };
      }
      const r = e.row;
      const at = r.source.start ? `:${r.source.start}` : "";
      return {
        id: e.key,
        key: e.member ? r.source.start ? `line ${r.source.start}` : displayPath(r.source, home()) : `${r.check} · ${displayPath(r.source, home())}${at}`,
        title: rowTitle(r),
        meta: rowMeta(r)
      };
    },
    openOnMove: true,
    onOpen(e) {
      if (syncing) return;
      go(frag(e.key));
    }
  });
  function chips() {
    l.setChips("view", []);
    if (!perItem(review)) {
      l.setChips("filter", []);
      return;
    }
    const counts = /* @__PURE__ */ new Map();
    for (const r of review?.rows ?? [])
      counts.set(r.check, (counts.get(r.check) ?? 0) + 1);
    const fs = counts.size > 1 ? [...counts.entries()].map(([c, n]) => ({
      id: c,
      label: c,
      count: n,
      on: filter === c
    })) : [];
    l.setChips("filter", fs);
  }
  function decorate() {
    const els = l.el.querySelectorAll(".kit-row");
    shown.forEach((e, i) => {
      els[i]?.classList.toggle("sift-group", e.kind === "group");
      els[i]?.classList.toggle("sift-member", e.kind === "row" && e.member);
      els[i]?.classList.toggle("sift-linked", e.kind === "file" && e.linked);
      els[i]?.classList.toggle("sift-nochange-row", e.kind === "nochange");
    });
    const eyebrow = l.el.querySelector(".kit-eyebrow");
    if (eyebrow)
      eyebrow.textContent = perItem(review) || !review?.round ? "items" : `to change · ${plural3(fileEntries(files(), home()).length, "file")}`;
  }
  const ctx = {
    get home() {
      return home();
    },
    get editing() {
      return editing;
    },
    get edited() {
      return rowSnap?.row ?? null;
    },
    noteOf: (r) => decider.noteOf(`r:${r.id}`) ?? r.decision?.note ?? "",
    setNote: (r, text) => decider.noteRow(r.id, text),
    accept: (rows) => decide(rows, "accept"),
    reject: (rows) => decide(rows, "reject"),
    startEdit(r) {
      if (ctx.busy([r])) return;
      openEdit(r.id, false);
    },
    cancelEdit() {
      closeEdit();
      render();
    },
    saveEdit,
    clear: (r) => void decider.clearRow(r.id),
    busy: (rows) => decider.busy(rows.map((r) => `r:${r.id}`)),
    open: (key) => go(frag(key)),
    file: (r) => api.file(review?.round?.id ?? 0, r.id)
  };
  const fctx = {
    get home() {
      return home();
    },
    get editing() {
      return editing;
    },
    get edited() {
      return fileSnap?.file ?? null;
    },
    get files() {
      return files();
    },
    rowsOf: (f) => f.rows.map((id) => rowById(id)).filter((r) => !!r).sort((a, c) => (a.source.start ?? 0) - (c.source.start ?? 0)),
    baseOf(f) {
      const v = bases.get(baseKey(f));
      if (v === void 0) loadBase(f);
      return v;
    },
    yours(f) {
      if (f.decision?.action === "edit" && f.decision.content !== void 0)
        mine.set(f.key, f.decision.content);
      return mine.get(f.key);
    },
    noteOf: (f) => decider.noteOf(`f:${f.key}`) ?? f.decision?.note ?? "",
    setNote: (f, text) => decider.noteFile(f.key, text),
    pick: (f, c) => pick(f, c),
    startEdit(f) {
      if (fctx.busy(f)) return;
      openEdit(f.key, true);
    },
    cancelEdit() {
      closeEdit();
      render();
    },
    saveEdit(_f, content) {
      const snap = fileSnap;
      if (!snap) return "the editor is closed";
      const f = snap.file;
      if (!content.trim())
        return "the file is empty: reject it to leave it as it is";
      const was = f.decision?.action === "edit" ? f.decision.content : f.rec?.content;
      if (content === f.rec?.content)
        return "this is the recommended version: pick it with 2";
      if (content === was) return "nothing changed";
      if (fctx.busy(f)) return "the last decision on this file is saving";
      const at = openKey;
      void decider.editFile(snap, content).then((out) => {
        if (out === "saved") mine.set(f.key, content);
        edited(out, f.key, true, at);
      });
      return null;
    },
    clear: (f) => void decider.clearFile(f.key),
    busy: (f) => decider.busy(f.group.map((k) => `f:${k}`))
  };
  const nctx = {
    get home() {
      return home();
    },
    rowsOf: (f) => fctx.rowsOf(f),
    get disagreeing() {
      return disagreeing;
    },
    agree(f) {
      if (disagreeing === f.key) disagreeing = null;
      void decider.file(f.key, { action: "accept" }).then((ok) => ok ? agreed(f.key) : render());
    },
    agreeAll() {
      const todo = noChangeFiles(files(), home()).filter(
        (f) => !f.decision && !fctx.busy(f)
      );
      if (!todo.length) return;
      void Promise.all(
        todo.map((f) => decider.file(f.key, { action: "accept" }))
      ).then(
        (oks) => oks.every(Boolean) && openKey === NOCHANGE ? leaveNoChange() : render()
      );
    },
    startDisagree(f) {
      if (fctx.busy(f)) return;
      disagreeing = f.key;
      render();
    },
    cancelDisagree() {
      disagreeing = null;
      render();
    },
    disagree(f, note) {
      if (!note.trim())
        return "say what should change: the agent rewrites the file from your note";
      if (fctx.busy(f)) return "the last decision on this file is saving";
      void decider.file(f.key, { action: "reject", note: note.trim() }).then((ok) => {
        if (ok && disagreeing === f.key) disagreeing = null;
        render();
      });
      return null;
    },
    clear: (f) => void decider.clearFile(f.key),
    busy: (f) => fctx.busy(f)
  };
  const loadingBases = /* @__PURE__ */ new Set();
  function loadBase(f) {
    const k = baseKey(f);
    const round = review?.round?.id;
    if (!round || loadingBases.has(k)) return;
    loadingBases.add(k);
    api.base(round, f.key).then(
      (out) => {
        bases.set(k, out.content);
        loadingBases.delete(k);
        render();
      },
      (err) => {
        bases.set(k, err);
        loadingBases.delete(k);
        render();
      }
    );
  }
  function render() {
    refreshBar();
    chips();
    if (!review) return;
    if (!review.round) {
      shown = [];
      l.setItems([]);
      read.replaceChildren(
        message(
          "sift",
          "No round yet",
          "Run sift check; this page shows what it finds."
        )
      );
      return;
    }
    if (recommending()) {
      shown = [];
      l.setItems([]);
      const p = review.progress;
      read.replaceChildren(
        perItem(review) ? message(
          "recommending",
          `The agent is recommending: ${p.recommended} of ${plural3(p.files, "item")}`,
          "Every item arrives with what the agent recommends doing about it. This page opens for review when the last one is in."
        ) : message(
          "recommending",
          `The agent is recommending: ${p.recommended} of ${plural3(p.files, "file")}`,
          "Every file arrives with a recommendation: one revised version covering all its findings. This page opens for review when the last one is in."
        )
      );
      return;
    }
    shown = items();
    if (openKey && !shown.some((e2) => e2.key === openKey)) {
      openKey = null;
      shown = items();
    }
    l.setItems(shown);
    decorate();
    const vkey = `${openKey ?? ""}/${editing ?? ""}`;
    const e = current();
    if (e) {
      syncing = true;
      l.open(shown.indexOf(e));
      syncing = false;
      decorate();
      const oldNote = read.querySelector(".kit-note");
      const oldWhy = read.querySelector(".sift-disagree");
      const oldEdit = read.querySelector(".sift-edit");
      const focus = document.activeElement;
      const doc = e.kind === "nochange" ? noChangeDoc(nctx, e.files) : e.kind === "file" ? fileDoc(fctx, e.file) : e.kind === "group" ? groupDoc(ctx, e.group) : rowDoc(ctx, e.row);
      const noteNow = e.kind === "file" ? fctx.noteOf(e.file) : e.kind === "row" ? ctx.noteOf(e.row) : "";
      if (oldNote && lastView === vkey && (focus === oldNote || oldNote.value !== noteNow))
        doc.querySelector(".kit-note")?.replaceWith(oldNote);
      doc.querySelector(".sift-edit")?.setAttribute("data-gen", String(editGen));
      if (oldEdit && lastView === vkey && oldEdit.dataset.gen === String(editGen))
        doc.querySelector(".sift-edit")?.replaceWith(oldEdit);
      const why = doc.querySelector(".sift-disagree");
      if (oldWhy && why && lastView === vkey && oldWhy.closest(".sift-nc")?.dataset.key === why.closest(".sift-nc")?.dataset.key)
        why.replaceWith(oldWhy);
      read.replaceChildren(doc);
      if (focus instanceof HTMLElement && read.contains(focus)) focus.focus();
      if (ncNext && e.kind === "nochange") {
        const at = [...doc.querySelectorAll(".sift-nc")].find(
          (s) => s.dataset.key === ncNext
        );
        at?.scrollIntoView({ block: "nearest" });
        at?.querySelector(".kit-btn")?.focus({
          preventScroll: true
        });
      }
      ncNext = null;
    } else {
      read.replaceChildren(overview());
    }
    if (vkey !== lastView) read.scrollTop = 0;
    lastView = vkey;
  }
  function overview() {
    if (perItem(review)) {
      const p2 = progress(review?.rows ?? []);
      if (!review?.rows.length)
        return message(
          "needs you",
          "Nothing here",
          "This round has nothing to decide."
        );
      return message(
        "needs you",
        `${p2.total - p2.decided} of ${plural3(p2.total, "item")} to decide`,
        "Open a group (↵) to decide it whole, or an item to decide it alone: 1 accept, 2 edit, 3 reject. Send returns your decisions to the agent."
      );
    }
    const p = filesProgress(files());
    const same = noChangeFiles(files(), home());
    if (!p.total && !same.length)
      return message(
        "needs you",
        "Nothing here",
        search ? "Nothing matches the search." : "This round found nothing to change."
      );
    const linked = shown.filter((e) => e.kind === "file" && e.linked).length;
    const sameLeft = same.filter((f) => !f.decision).length;
    const left = [
      p.total ? `${p.total - p.decided} of ${p.total} to change` : "",
      same.length ? `${sameLeft} of ${same.length} with nothing to change` : ""
    ].filter(Boolean).join(" and ");
    return message(
      "needs you",
      [
        p.total ? `${plural3(p.total, "file")} to change` : "",
        same.length ? `${same.length} with nothing to change` : ""
      ].filter(Boolean).join(" · "),
      `Still to decide: ${left}. Each file to change has one recommendation covering all its findings. Open one (↵) and pick the version it should have: 1 current, 2 recommended, or e to write your own.${linked ? " Linked files move text between them and are picked together." : ""}${same.length ? ` The agent recommends no change to ${plural3(same.length, "file")}: agree, or disagree and say why.` : ""} Send returns your decisions to the agent.`
    );
  }
  function go(f) {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const key = parseRoute(f);
    if (key !== openKey) {
      closeEdit();
      disagreeing = null;
    }
    openKey = key;
    render();
  }
  function onHash() {
    if (location.hash === lastFrag) return;
    go(location.hash);
  }
  let loadAttempt = 0;
  async function load() {
    clearTimeout(loadTimer);
    try {
      const r = await api.review();
      loadAttempt = 0;
      review = r;
      openKey = parseRoute(location.hash);
      lastFrag = location.hash;
      render();
      startLive(r.cursor);
    } catch (err) {
      review = null;
      l.setItems([]);
      refreshBar();
      if (err instanceof ApiError && err.isStale) return;
      loadTimer = setTimeout(
        () => void load(),
        [1e3, 2e3, 5e3][loadAttempt++] ?? 1e4
      );
      read.replaceChildren(
        h3(
          "div",
          { class: "kit-doc" },
          h3(
            "p",
            { class: "sift-lead" },
            "sift serve is not answering; retrying."
          )
        )
      );
    }
  }
  async function reload() {
    if (!decider.idle() || loading) {
      dirty = true;
      return;
    }
    loading = true;
    try {
      do {
        dirty = false;
        const epoch = decider.epoch;
        const r = await api.review();
        if (epoch !== decider.epoch || !decider.idle()) {
          dirty = true;
          continue;
        }
        if (review && signature(review) === signature(r)) {
          review.cursor = r.cursor;
          continue;
        }
        review = r;
        render();
      } while (dirty && decider.idle());
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`could not reload: ${err.message}`, "danger");
    } finally {
      loading = false;
    }
  }
  function startLive(cursor) {
    if (liveHandle || stale) return;
    liveHandle = live({
      events: "/api/events",
      poll: "/api/poll",
      cursor,
      fetch: gated,
      onEvent: () => void reload(),
      onStatus(s) {
        if (stale) return;
        if (s === "stale") {
          goStale();
          return;
        }
        const was = prevLive;
        prevLive = s;
        if (s === "down") {
          b.setLive("down", "disconnected");
          return;
        }
        b.setLive(s);
        if (s === "live" && was !== "live") void reload();
      }
    });
  }
  function goStale() {
    if (stale) return;
    stale = true;
    liveHandle?.stop();
    b.setLive("stale");
  }
  function openEdit(key, file) {
    rowSnap = file ? null : decider.snapRow(key);
    fileSnap = file ? decider.snapFile(key) : null;
    editing = key;
    editGen++;
    render();
  }
  function closeEdit() {
    editing = null;
    rowSnap = null;
    fileSnap = null;
  }
  function edited(out, key, file, at) {
    if (editing !== key) return;
    if (out === "changed") {
      openEdit(key, file);
      flash(
        "This changed while the editor was open; the editor now shows it as it is. Redo your edit.",
        "danger"
      );
      return;
    }
    if (out !== "saved") return;
    closeEdit();
    if (!file && at && openKey === at && at === `r:${key}`)
      go(frag(nextOpen(shown, at)));
    else render();
  }
  function agreed(key) {
    const e = current();
    if (openKey !== NOCHANGE || e?.kind !== "nochange") return render();
    const i = e.files.findIndex((f) => f.key === key);
    const next = e.files.slice(i + 1).find((f) => !f.decision);
    if (!next) return leaveNoChange();
    ncNext = next.key;
    render();
  }
  function leaveNoChange() {
    const left = shown.find((e) => e.kind === "file" && !e.file.decision);
    go(frag(left?.key ?? null));
  }
  function nextFile(key) {
    const i = shown.findIndex((e) => e.key === key);
    for (let j = i + 1; j < shown.length; j++) {
      const e = shown[j];
      if (e.kind === "file" && !e.file.decision) return e.key;
    }
    return shown[i + 1]?.key ?? key;
  }
  function pick(f, c) {
    const at = `f:${f.key}`;
    const was = f.decision;
    if (was?.action === "edit" && was.content !== void 0)
      mine.set(f.key, was.content);
    let p;
    if (c === "current") p = decider.file(f.key, { action: "reject" });
    else if (c === "recommended") p = decider.recommend(f.key);
    else {
      const content = mine.get(f.key);
      const snap = decider.snapFile(f.key);
      if (content === void 0 || !snap || was?.action === "edit") return;
      p = decider.editFile(snap, content).then((s) => s === "saved");
    }
    void p.then((ok) => {
      if (!ok) return;
      if (editing === f.key) closeEdit();
      if (openKey === at && c !== "yours") go(frag(nextFile(at)));
      else render();
    });
  }
  function put(rows, make) {
    if (!rows.length) return;
    const single = rows.length === 1 && openKey === `r:${rows[0].id}`;
    const at = openKey;
    void decider.rows(rows.map((r) => ({ id: r.id, d: make(r) }))).then((ok) => {
      if (!ok) return;
      if (rows.some((r) => r.id === editing)) closeEdit();
      if (single && at && openKey === at)
        go(frag(nextOpen(shown, at)));
      else render();
    });
  }
  function decide(rows, action) {
    const targets = action === "accept" ? rows.filter((r) => verdictOf(r)) : rows;
    if (!targets.length) {
      flash(
        "Nothing to accept: the agent proposed nothing here. Edit to give it a verdict."
      );
      return;
    }
    put(targets, () => ({ action }));
  }
  function saveEdit(_r, f) {
    const snap = rowSnap;
    if (!snap) return "the editor is closed";
    const res = editDecision(snap.row, f);
    if (!res.ok) return res.error;
    if (ctx.busy([snap.row])) return "the last decision on this row is saving";
    const at = openKey;
    void decider.editRow(snap, res.decision).then((out) => edited(out, snap.row.id, false, at));
    return null;
  }
  async function send() {
    try {
      const out = await decider.send();
      if (!out) return;
      const { sent, to } = out;
      refreshBar();
      flash(
        sent === 0 ? "Nothing to send." : to ? `Sent ${plural3(sent, "decision")} to ${to}.` : `Sent ${plural3(sent, "decision")}. The next agent session that opens sift gets them.`
      );
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`Not sent: ${err.message}`, "danger");
    }
  }
  const keys = createKeys({ list: l });
  const group = "decide";
  const busyNow = (e) => e.kind === "nochange" ? e.files.some((f) => fctx.busy(f)) : e.kind === "file" ? fctx.busy(e.file) : ctx.busy(e.kind === "row" ? [e.row] : e.group.rows);
  const on = (onFile, onRow, onGroup) => () => {
    const e = current();
    if (!e || e.kind === "nochange" || busyNow(e)) return;
    if (e.kind === "file") onFile(e.file);
    else if (e.kind === "row") onRow(e.row);
    else onGroup?.(e.group.rows);
  };
  keys.register({
    keys: "1",
    label: "a file: current · an item: accept (a group: every undecided item)",
    group,
    run: on(
      (f) => fctx.pick(f, "current"),
      (r) => decide([r], "accept"),
      (rows) => decide(groupTargets(rows, "accept"), "accept")
    )
  });
  keys.register({
    keys: "2",
    label: "a file: recommended · an item: edit",
    group,
    run: on(
      (f) => fctx.pick(f, "recommended"),
      (r) => ctx.startEdit(r),
      (rows) => ctx.open(`r:${rows[0].id}`)
    )
  });
  keys.register({
    keys: "3",
    label: "a file: yours, once written · an item: reject (a group: every undecided item)",
    group,
    run: on(
      (f) => {
        if (fctx.yours(f) !== void 0) fctx.pick(f, "yours");
      },
      (r) => decide([r], "reject"),
      (rows) => decide(groupTargets(rows, "reject"), "reject")
    )
  });
  keys.register({
    keys: "e",
    label: "a file: write your own version (the whole file)",
    group,
    run: on(
      (f) => fctx.startEdit(f),
      () => {
      }
    )
  });
  keys.register({
    keys: "a",
    label: "nothing to change: agree with all",
    group,
    run() {
      const e = current();
      if (e?.kind === "nochange") nctx.agreeAll();
    }
  });
  keys.register({
    keys: "u",
    label: "clear a decision",
    group,
    run: on(
      (f) => fctx.clear(f),
      (r) => ctx.clear(r)
    )
  });
  keys.register({
    keys: "n",
    label: "note",
    group,
    run() {
      document.querySelector(".kit-note")?.focus();
    }
  });
  keys.register({
    keys: "⌘↵",
    label: "save the edit",
    group,
    inField: true,
    run() {
      const f = document.querySelector(".sift-edit");
      if (!f) return false;
      f.requestSubmit();
    }
  });
  const app = document.getElementById("app") ?? document.body;
  app.classList.add("kit-app");
  app.append(b.el, l.el, read);
  b.setLive("polling");
  window.addEventListener("hashchange", onHash);
  if (!/^#\/open/.test(location.hash)) history.replaceState(null, "", frag());
  void load();
}
boot();
