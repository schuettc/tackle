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
      await api.put("/decisions", { round, decisions });
    },
    async clear(round, id) {
      const q = new URLSearchParams({ round: String(round), id });
      await api.del(`/decisions?${q.toString()}`);
    },
    async decideFile(round, file, d, prints) {
      await api.put("/files", {
        round,
        file,
        action: d.action,
        content: d.content ?? "",
        note: d.note ?? "",
        prints
      });
    },
    async clearFile(round, file) {
      const q = new URLSearchParams({ round: String(round), file });
      await api.del(`/files?${q.toString()}`);
    },
    base: (round, file) => api.get("/base", { round: String(round), file }),
    async send(round) {
      const r = await api.post("/send", {
        round
      });
      return { sent: r?.sent ?? 0, to: r?.to ?? "" };
    },
    file: (round, id) => api.get("/file", { round: String(round), id })
  };
}
function newApi(opts) {
  return createApi(opts);
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
function markSent(rows) {
  for (const r of rows) if (r.decision) r.decision.sent = true;
}
function verdictOf(r) {
  return r.decision?.action === "edit" && r.decision.verdict || r.verdict || "";
}
function fieldOf(r, k) {
  const d = r.decision?.action === "edit" ? r.decision : void 0;
  if (d?.[k]) return d[k];
  if (d?.cleared?.includes(k)) return "";
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
  const d = { action: "edit" };
  if (verdict !== (r.verdict ?? "")) d.verdict = verdict;
  const cleared = [];
  for (const k of ["title", "text"]) {
    const now = f[k];
    if (now === (r[k] ?? "")) continue;
    if (now.trim() !== "") d[k] = now;
    else if (r[k]) cleared.push(k);
  }
  if (cleared.length) d.cleared = cleared;
  if (!d.verdict && !d.title && !d.text && !d.cleared)
    return { ok: false, error: "nothing changed: accept the proposal instead" };
  return { ok: true, decision: d };
}
function rowTitle(r) {
  if (r.title) return r.title;
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
  const shown = files.filter((f) => f.rows.length > 0 || f.rec);
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
var word = {
  accept: "accepted",
  edit: "edited",
  reject: "rejected"
};
function fileMeta(f) {
  const d = f.decision ? word[f.decision.action] : "·";
  return `${kb(f.size)} → ${kb(f.after)} KB · ${f.rows.length} · ${d}`.replace(
    / · ·$/,
    " ·"
  );
}
function printsFor(files, key) {
  const f = files.find((x) => x.key === key);
  const out = {};
  for (const k of f?.group ?? [key]) {
    const m = files.find((x) => x.key === k);
    if (m) out[k] = m.fingerprint;
  }
  return out;
}
function decideLocal(files, key, d) {
  const f = files.find((x) => x.key === key);
  if (!f) return;
  for (const k of f.group) {
    const m = files.find((x) => x.key === k);
    if (!m) continue;
    const cur = m.decision;
    if (d === null) m.decision = null;
    else if (k === key && d.action === "accept" && cur?.action === "edit")
      m.decision = { ...cur, note: d.note ?? "", sent: false };
    else if (k === key) m.decision = { ...d, sent: false };
    else if (d.action === "reject")
      m.decision = { action: "reject", note: cur?.note ?? "", sent: false };
    else if (cur && (cur.action === "edit" || cur.action === "accept"))
      continue;
    else m.decision = { action: "accept", note: cur?.note ?? "", sent: false };
  }
  if (f.decision?.action === "edit" && f.decision.content !== void 0)
    f.after = f.decision.content.length;
  else if (f.rec) f.after = f.rec.content.length;
}
function filesProgress(files) {
  const open = files.filter((f) => f.rec);
  return { decided: open.filter((f) => f.decision).length, total: open.length };
}
function unsentFiles(files) {
  return files.filter((f) => f.decision && !f.decision.sent).length;
}

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
    h2("h1", { class: "kit-h1" }, r.title || r.summary)
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
    const bs = [
      {
        label: "1 accept",
        fill: a === "accept",
        disabled: !r.verdict,
        run: () => ctx.accept([r])
      },
      { label: "2 edit", fill: a === "edit", run: () => ctx.startEdit(r) },
      {
        label: "3 reject",
        fill: a === "reject",
        danger: true,
        run: () => ctx.reject([r])
      }
    ];
    if (r.decision) bs.push({ label: "clear (u)", run: () => ctx.clear(r) });
    parts.push(buttons(bs));
    if (r.decision)
      parts.push(
        h2(
          "p",
          { class: "sift-why" },
          `your decision: ${rowMeta(r)}${r.decision.sent ? " · sent" : ""}`
        )
      );
    if (ctx.editing === r.id) parts.push(...editForm(ctx, r));
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
            disabled: !acc.length,
            run: () => ctx.accept(acc)
          },
          {
            label: `3 reject ${rej.length}`,
            danger: true,
            disabled: !rej.length,
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
var word2 = {
  accept: "accepted",
  edit: "edited",
  reject: "rejected"
};
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
  const d = f.decision?.action === "edit" ? f.decision : void 0;
  const text = h2("textarea", {
    class: "sift-field sift-text sift-whole",
    "aria-label": "the whole recommended file",
    spellcheck: false
  });
  text.value = d?.content ?? f.rec?.content ?? "";
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
      "edit · the whole file as it will be written"
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
  const did = new Map((f.rec?.findings ?? []).map((a) => [a.row, a]));
  return h2(
    "table",
    { class: "sift-rows sift-findings" },
    h2(
      "tr",
      null,
      h2("th", null, "line"),
      h2("th", null, "finding"),
      h2("th", null, "what the rewrite did")
    ),
    rows.map((r) => {
      const a = did.get(r.id);
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
        h2(
          "td",
          { class: "p" },
          a ? [h2("span", { class: `sift-did ${a.did}` }, a.did), " ", a.how] : "—"
        )
      );
    })
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
    h2("div", { class: "kit-kick" }, `${f.class} file · ${where2}`),
    h2("h1", { class: "kit-h1" }, where2),
    h2("p", { class: "sift-lead sift-summary" }, f.rec?.summary ?? ""),
    facts(ev)
  ];
  const edited = f.decision?.action === "edit";
  const after = edited ? f.decision?.content ?? "" : f.rec?.content ?? "";
  const base = ctx.baseOf(f);
  parts.push(
    h2(
      "div",
      { class: "kit-label" },
      edited ? "the change · your edit" : "the change"
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
  else parts.push(diffView(base, after, certainLines(rows)));
  const a = f.decision?.action;
  const bs = [
    {
      label: edited ? "1 accept your edit" : "1 accept",
      fill: a === "accept",
      run: () => ctx.accept(f)
    },
    { label: "2 edit", fill: a === "edit", run: () => ctx.startEdit(f) },
    {
      label: "3 reject",
      fill: a === "reject",
      danger: true,
      run: () => ctx.reject(f)
    }
  ];
  if (f.decision)
    bs.push({
      label: edited ? "revert to the recommendation (u)" : "clear (u)",
      run: () => ctx.clear(f)
    });
  const decide = buttons(bs);
  decide.classList.add("sift-decide");
  parts.push(decide);
  const said = [];
  if (f.decision)
    said.push(
      `your decision: ${word2[f.decision.action]}${f.decision.sent ? " · sent" : ""}`
    );
  if (others.length)
    said.push(
      `decided together with ${others.map((o) => displayPath(o.source, ctx.home)).join(", ")}: the recommendation moves text between them`
    );
  if (said.length)
    parts.push(h2("p", { class: "sift-why" }, said.join(". ") + "."));
  if (ctx.editing === f.key) parts.push(fileEdit(ctx, f));
  parts.push(
    h2("div", { class: "kit-label" }, "findings"),
    findingsTable(f, rows)
  );
  const field = noteField({
    value: ctx.noteOf(f),
    placeholder: "why (n)",
    onCommit: (v) => ctx.setNote(f, v)
  });
  field.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.isComposing) field.blur();
  });
  parts.push(h2("div", { class: "kit-label" }, "note"), field);
  return h2("div", { class: "kit-doc sift-file" }, ...parts);
}

// app.ts
var qs = new URLSearchParams(location.search);
var plural2 = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
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
  let filter = "";
  let search = "";
  let lastFrag = "";
  let lastView = "";
  let syncing = false;
  let stale = false;
  let inflight = 0;
  let loading = false;
  let dirty = false;
  let flashing = false;
  let liveHandle = null;
  let statusTimer;
  let prevLive = "live";
  let loadTimer;
  let shown = [];
  const pending = /* @__PURE__ */ new Map();
  const bases = /* @__PURE__ */ new Map();
  const gated = (input, init) => stale ? Promise.reject(new Error("sift serve restarted: this tab has stopped")) : fetch(input, init);
  const api = client(newApi({ fetch: gated, onStale: () => goStale() }));
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
    return fileEntries(files(), home()).filter(
      (e) => !q || displayPath(e.file.source, home()).toLowerCase().includes(q)
    ).map((e) => ({ ...e, kind: "file" }));
  };
  const rowById = (id) => review?.rows.find((r) => r.id === id);
  const fileByKey = (k) => files().find((f) => f.key === k);
  const targetPrint = (d) => {
    const id = editTarget(d);
    return id ? rowById(id)?.fingerprint : void 0;
  };
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
      n && review?.round && !recommending() ? { label: `Send ${n}`, run: () => void send() } : null
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
      if (e.kind === "file") {
        const f = e.file;
        return {
          id: e.key,
          key: displayPath(f.source, home()),
          title: f.rec?.summary.split(/(?<=\.)\s/)[0] || `${plural2(f.rows.length, "finding")}`,
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
    });
  }
  const ctx = {
    get home() {
      return home();
    },
    get editing() {
      return editing;
    },
    noteOf: (r) => pending.get(r.id) ?? r.decision?.note ?? "",
    setNote,
    accept: (rows) => decide(rows, "accept"),
    reject: (rows) => decide(rows, "reject"),
    startEdit(r) {
      editing = r.id;
      render();
    },
    cancelEdit() {
      editing = null;
      render();
    },
    saveEdit,
    clear,
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
    get files() {
      return files();
    },
    rowsOf: (f) => f.rows.map((id) => rowById(id)).filter((r) => !!r).sort((a, c) => (a.source.start ?? 0) - (c.source.start ?? 0)),
    baseOf(f) {
      const v = bases.get(baseKey(f));
      if (v === void 0) loadBase(f);
      return v;
    },
    noteOf: (f) => pending.get(`f:${f.key}`) ?? f.decision?.note ?? "",
    setNote: setFileNote,
    accept: (f) => putFile(f, { action: "accept" }),
    reject: (f) => putFile(f, { action: "reject" }),
    startEdit(f) {
      editing = f.key;
      render();
    },
    cancelEdit() {
      editing = null;
      render();
    },
    saveEdit(f, content) {
      if (!content.trim())
        return "the file is empty: reject it to leave it as it is";
      const was = f.decision?.action === "edit" ? f.decision.content : f.rec?.content;
      if (content === was)
        return "nothing changed: accept the recommendation instead";
      putFile(f, { action: "edit", content });
      return null;
    },
    clear: clearFile
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
          `The agent is recommending: ${p.recommended} of ${plural2(p.files, "item")}`,
          "Every item arrives with what the agent recommends doing about it. This page opens for review when the last one is in."
        ) : message(
          "recommending",
          `The agent is recommending: ${p.recommended} of ${plural2(p.files, "file")}`,
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
      const oldEdit = read.querySelector(".sift-edit");
      const focus = document.activeElement;
      const doc = e.kind === "file" ? fileDoc(fctx, e.file) : e.kind === "group" ? groupDoc(ctx, e.group) : rowDoc(ctx, e.row);
      const noteNow = e.kind === "file" ? fctx.noteOf(e.file) : e.kind === "row" ? ctx.noteOf(e.row) : "";
      if (oldNote && lastView === vkey && (focus === oldNote || oldNote.value !== noteNow))
        doc.querySelector(".kit-note")?.replaceWith(oldNote);
      if (oldEdit && lastView === vkey)
        doc.querySelector(".sift-edit")?.replaceWith(oldEdit);
      read.replaceChildren(doc);
      if (focus instanceof HTMLElement && read.contains(focus)) focus.focus();
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
        `${p2.total - p2.decided} of ${plural2(p2.total, "item")} to decide`,
        "Open a group (↵) to decide it whole, or an item to decide it alone: 1 accept, 2 edit, 3 reject. Send returns your decisions to the agent."
      );
    }
    const p = filesProgress(files());
    if (!p.total)
      return message(
        "needs you",
        "Nothing here",
        search ? "Nothing matches the search." : "This round found nothing to change."
      );
    const linked = shown.filter((e) => e.kind === "file" && e.linked).length;
    return message(
      "needs you",
      `${p.total - p.decided} of ${plural2(p.total, "file")} to decide`,
      `Each file has one recommendation covering all its findings. Open one (↵) to read its diff, then 1 accept, 2 edit (the whole file), 3 reject.${linked ? " Linked files move text between them and are decided together." : ""} Send returns your decisions to the agent.`
    );
  }
  function go(f) {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const key = parseRoute(f);
    if (key !== openKey) editing = null;
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
    if (inflight > 0 || loading) {
      dirty = true;
      return;
    }
    loading = true;
    try {
      do {
        dirty = false;
        const r = await api.review();
        if (review && signature(review) === signature(r)) {
          review.cursor = r.cursor;
          continue;
        }
        review = r;
        render();
      } while (dirty && inflight === 0);
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
  function persist(op, rollback) {
    inflight++;
    op().catch((err) => {
      if (err instanceof ApiError && err.isStale) return;
      rollback();
      if (err instanceof ApiError && err.status === 409) {
        flash(
          `The review changed; reloaded. ${err.message}`.trim(),
          "danger"
        );
        dirty = true;
        return;
      }
      render();
      flash(`Not saved: ${err.message ?? String(err)}`, "danger");
    }).finally(() => {
      inflight--;
      if (inflight === 0 && dirty) void reload();
    });
  }
  function nextFile(key) {
    const i = shown.findIndex((e) => e.key === key);
    for (let j = i + 1; j < shown.length; j++) {
      const e = shown[j];
      if (e.kind === "file" && !e.file.decision) return e.key;
    }
    return shown[i + 1]?.key ?? key;
  }
  function putFile(f, d) {
    const round = review?.round?.id;
    if (!round) return;
    const fs = files();
    const prints = printsFor(fs, f.key);
    const group2 = f.group.map((k) => fileByKey(k)).filter((x) => !!x);
    const prev = group2.map((m) => ({ m, d: m.decision, after: m.after }));
    const pendingNote = pending.get(`f:${f.key}`);
    const note = pendingNote ?? f.decision?.note ?? "";
    pending.delete(`f:${f.key}`);
    const full = { ...d, note };
    decideLocal(fs, f.key, full);
    editing = null;
    if (openKey === `f:${f.key}` && d.action !== "edit")
      go(frag(nextFile(openKey)));
    else render();
    persist(
      () => api.decideFile(round, f.key, full, prints),
      () => {
        for (const p of prev) {
          p.m.decision = p.d;
          p.m.after = p.after;
        }
        if (pendingNote !== void 0) pending.set(`f:${f.key}`, pendingNote);
      }
    );
  }
  function clearFile(f) {
    const round = review?.round?.id;
    if (!round || !f.decision) return;
    const group2 = f.group.map((k) => fileByKey(k)).filter((x) => !!x);
    const prev = group2.map((m) => ({ m, d: m.decision, after: m.after }));
    decideLocal(files(), f.key, null);
    render();
    persist(
      () => api.clearFile(round, f.key),
      () => {
        for (const p of prev) {
          p.m.decision = p.d;
          p.m.after = p.after;
        }
      }
    );
  }
  function setFileNote(f, text) {
    const d = f.decision;
    if (!d) {
      if (text) pending.set(`f:${f.key}`, text);
      else pending.delete(`f:${f.key}`);
      return;
    }
    if ((d.note ?? "") === text) return;
    const round = review?.round?.id;
    if (!round) return;
    const old = d.note;
    d.note = text;
    d.sent = false;
    persist(
      () => api.decideFile(
        round,
        f.key,
        { action: d.action, content: d.content, note: text },
        printsFor(files(), f.key)
      ),
      () => {
        d.note = old;
      }
    );
    refreshBar();
  }
  function put(rows, make) {
    const round = review?.round?.id;
    if (!round || !rows.length) return;
    const prev = rows.map((r) => r.decision);
    const notes = rows.map((r) => pending.get(r.id));
    const body = rows.map((r) => {
      const d = make(r);
      const note = pending.get(r.id) ?? r.decision?.note ?? "";
      pending.delete(r.id);
      r.decision = { ...d, note };
      return {
        id: r.id,
        action: d.action,
        verdict: d.verdict,
        title: d.title,
        text: d.text,
        cleared: d.cleared,
        note,
        fingerprint: r.fingerprint,
        target_fingerprint: targetPrint(d)
      };
    });
    const single = rows.length === 1 && openKey === `r:${rows[0].id}`;
    editing = null;
    if (single) go(frag(nextOpen(shown, openKey)));
    else render();
    persist(
      () => api.decide(round, body),
      () => rows.forEach((r, i) => {
        r.decision = prev[i];
        const n = notes[i];
        if (n !== void 0) pending.set(r.id, n);
      })
    );
  }
  function decide(rows, action) {
    const targets = action === "accept" ? rows.filter((r) => r.verdict) : rows;
    if (!targets.length) {
      flash(
        "Nothing to accept: the agent proposed nothing here. Edit to give it a verdict."
      );
      return;
    }
    put(targets, () => ({ action }));
  }
  function saveEdit(r, f) {
    const res = editDecision(r, f);
    if (!res.ok) return res.error;
    put([r], () => res.decision);
    return null;
  }
  function clear(r) {
    const round = review?.round?.id;
    const prev = r.decision;
    if (!round || !prev) return;
    delete r.decision;
    render();
    persist(
      () => api.clear(round, r.id),
      () => {
        r.decision = prev;
      }
    );
  }
  function setNote(shownRow, text) {
    const r = rowById(shownRow.id) ?? shownRow;
    const d = r.decision;
    const round = review?.round?.id;
    if (!d || !round) {
      if (text) pending.set(r.id, text);
      else pending.delete(r.id);
      return;
    }
    if ((d.note ?? "") === text) return;
    const old = d.note;
    d.note = text;
    d.sent = false;
    persist(
      () => api.decide(round, [
        {
          id: r.id,
          action: d.action,
          verdict: d.verdict,
          title: d.title,
          text: d.text,
          cleared: d.cleared,
          note: text,
          fingerprint: r.fingerprint,
          target_fingerprint: targetPrint(d)
        }
      ]),
      () => {
        d.note = old;
      }
    );
    refreshBar();
  }
  async function send() {
    const round = review?.round?.id;
    if (!round) return;
    try {
      const { sent, to } = await api.send(round);
      if (review) {
        markSent(review.rows);
        for (const f of review.files) if (f.decision) f.decision.sent = true;
        if (sent) review.sends++;
      }
      refreshBar();
      flash(
        sent === 0 ? "Nothing to send." : to ? `Sent ${plural2(sent, "decision")} to ${to}.` : `Sent ${plural2(sent, "decision")}. The next agent session that opens sift gets them.`
      );
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`Not sent: ${err.message}`, "danger");
    }
  }
  const keys = createKeys({ list: l });
  const group = "decide";
  const on = (onFile, onRow, onGroup) => () => {
    const e = current();
    if (!e) return;
    if (e.kind === "file") onFile(e.file);
    else if (e.kind === "row") onRow(e.row);
    else onGroup?.(e.group.rows);
  };
  keys.register({
    keys: "1",
    label: "accept (a group: every undecided item)",
    group,
    run: on(
      (f) => fctx.accept(f),
      (r) => decide([r], "accept"),
      (rows) => decide(groupTargets(rows, "accept"), "accept")
    )
  });
  keys.register({
    keys: "2",
    label: "edit (a file: the whole file)",
    group,
    run: on(
      (f) => fctx.startEdit(f),
      (r) => ctx.startEdit(r),
      (rows) => ctx.open(`r:${rows[0].id}`)
    )
  });
  keys.register({
    keys: "3",
    label: "reject (a group: every undecided item)",
    group,
    run: on(
      (f) => fctx.reject(f),
      (r) => decide([r], "reject"),
      (rows) => decide(groupTargets(rows, "reject"), "reject")
    )
  });
  keys.register({
    keys: "u",
    label: "clear a decision",
    group,
    run: on(
      (f) => clearFile(f),
      (r) => clear(r)
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
