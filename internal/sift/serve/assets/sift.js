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
    async undo(round, r, note) {
      await api.post("/undo", {
        round,
        id: r.id,
        note,
        fingerprint: r.fingerprint
      });
    },
    async redo(round, r, note) {
      await api.post("/redo", {
        round,
        id: r.id,
        note,
        fingerprint: r.fingerprint
      });
    },
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
  ["negative-rule", "negative rule", "negative rules"],
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
function fixOf(r) {
  return r.fix || "delete";
}
function isFix(r) {
  return r.certain && (!r.verdict || r.verdict === fixOf(r)) && !r.title && !r.destination && !r.text;
}
function fixDecision(op, note) {
  return { action: op === "undo" ? "reject" : "accept", note };
}
function markSent(rows) {
  for (const r of rows) if (r.decision) r.decision.sent = true;
}
function inView(r, v) {
  return v === "applied" ? isFix(r) : !isFix(r);
}
function isBacklog(round, rows) {
  if (round?.kind === "backlog") return true;
  return rows.length > 0 && rows.every((r) => r.check === "intake");
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
  const order = [];
  const add = (key, kicker, r) => {
    let g = by.get(key);
    if (!g) {
      g = { key, kicker, title: "", rows: [] };
      by.set(key, g);
      order.push(key);
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
    order.sort((a, b) => rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0));
    return order.map((k) => {
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
  const groups = order.map((k) => by.get(k));
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
  const open = rows.filter((r) => !isFix(r));
  return { decided: open.filter((r) => r.decision).length, total: open.length };
}
function unsent(rows, sends) {
  let n = rows.filter((r) => r.decision && !r.decision.sent).length;
  if (sends === 0) n += rows.filter((r) => isFix(r) && !r.decision).length;
  return n;
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
  if (isFix(r)) {
    if (r.decision?.action === "reject") return "undone";
    return r.decision && !r.decision.sent ? "redone" : "applied";
  }
  if (!r.decision) return "·";
  if (r.decision.action === "edit") return `edited · ${verdictOf(r)}`;
  if (r.decision.action === "accept") return `accepted · ${r.verdict ?? ""}`;
  return "rejected";
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
var cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);
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
      h2("div", { class: "kit-label" }, isFix(r) ? "the passage" : "now"),
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
  if (isFix(r)) {
    const done = ctx.appliedIn(r);
    const undone = r.decision?.action === "reject";
    parts.push(
      card({
        edge: "wait",
        head: undone ? "undone · stays as it is" : `applied · certain · ${fixOf(r)}`,
        body: undone ? "You took this fix out of the round: sift leaves the passage alone." : `${cap(r.summary)}. sift is certain of this one, so it ${fixOf(r) === "rewrite" ? "rewrites" : "removes"} the passage when it applies the round, unless you undo it.`
      }),
      h2("div", { class: "kit-label" }, "change"),
      done ? h2(
        "p",
        { class: "sift-why" },
        `already applied on ${done.branch}: change it on the branch`
      ) : buttons([
        undone ? { label: "redo (u)", run: () => ctx.redo([r]) } : { label: "undo (u)", danger: true, run: () => ctx.undo([r]) }
      ])
    );
  } else {
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
  if (ctx.view === "applied") {
    const live2 = rows.filter(
      (r) => isFix(r) && r.decision?.action !== "reject"
    );
    const undone = rows.filter(
      (r) => isFix(r) && r.decision?.action === "reject"
    );
    parts.push(
      facts([
        ["applied", String(live2.length)],
        ["undone", String(undone.length)]
      ]),
      card({
        edge: "wait",
        head: "certain fixes",
        body: "sift applies these itself. Undo any you want left as they are; it holds until you send.",
        actions: [
          {
            label: `undo ${live2.length}`,
            danger: true,
            disabled: !live2.length,
            run: () => ctx.undo(live2)
          },
          {
            label: `redo ${undone.length}`,
            disabled: !undone.length,
            run: () => ctx.redo(undone)
          }
        ]
      })
    );
  } else {
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

// app.ts
var qs = new URLSearchParams(location.search);
var plural2 = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
function frag(view, key) {
  return `#/${view}${key ? `/${encodeURIComponent(key)}` : ""}`;
}
function parseRoute(hash) {
  const m = /^#\/(open|applied)(?:\/(.+))?$/.exec(hash);
  if (!m) return { view: "open", key: null };
  let key = null;
  try {
    key = m[2] ? decodeURIComponent(m[2]) : null;
  } catch {
    key = null;
  }
  return { view: m[1], key };
}
function signature(r) {
  return `${r.round?.id ?? 0}|${r.sends}|${r.applies.map((a) => a.repo + a.state).join(",")}|` + r.rows.map((x) => {
    const d = x.decision;
    return `${x.id}:${x.verdict ?? ""}:${x.text ?? ""}:${d ? `${d.action}/${d.verdict ?? ""}/${d.text ?? ""}/${d.title ?? ""}/${d.note ?? ""}/${d.sent ? 1 : 0}` : ""}`;
  }).join(";");
}
function boot() {
  let review = null;
  let view = "open";
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
  const gated = (input, init) => stale ? Promise.reject(new Error("sift serve restarted: this tab has stopped")) : fetch(input, init);
  const api = client(newApi({ fetch: gated, onStale: () => goStale() }));
  const home = () => review?.home ?? "";
  const rowsOf = (v) => (review?.rows ?? []).filter((r) => inView(r, v));
  const matches = (r) => {
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
  const groups = () => groupsOf(
    rowsOf(view).filter(matches),
    isBacklog(review?.round ?? null, review?.rows ?? []),
    home()
  );
  const rowById = (id) => review?.rows.find((r) => r.id === id);
  const current = () => shown.find((e) => e.key === openKey);
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
    const kind = r.kind === "on-demand" ? "audit" : `${r.kind} audit`;
    const owner = r.owner ? ` · to ${r.owner}` : "";
    return `${kind} · round ${r.id} · ${day}${owner}`;
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
    const p = progress(review?.rows ?? []);
    b.setCount("review", review?.round ? `${p.decided}/${p.total}` : "");
    const n = unsent(review?.rows ?? [], review?.sends ?? 0);
    b.setPrimary(
      n && review?.round ? { label: `Send ${n}`, run: () => void send() } : null
    );
    if (!flashing) b.setStatus(baseStatus());
  }
  const read = h3("main", { class: "kit-read" });
  const l = list({
    label: "findings",
    views: [],
    onChip(group2, id) {
      if (group2 === "view") {
        editing = null;
        filter = "";
        go(frag(id === "applied" ? "applied" : "open"));
        return;
      }
      filter = filter === id ? "" : id;
      render();
    },
    search: {
      placeholder: "search files and passages",
      onInput(text) {
        search = text.trim();
        render();
      }
    },
    row(e) {
      if (e.kind === "group") {
        const g = e.group;
        const done = view === "applied" ? g.rows.filter((r2) => r2.decision?.action !== "reject").length : g.rows.filter((r2) => r2.decision).length;
        return {
          id: e.key,
          key: g.kicker,
          title: g.title,
          meta: done === g.rows.length && view === "open" ? "✓" : `${done}/${g.rows.length}`
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
      go(frag(view, e.key));
    }
  });
  function chips() {
    const all = review?.rows ?? [];
    l.setChips("view", [
      {
        id: "open",
        label: "needs you",
        count: all.filter((r) => inView(r, "open")).length,
        on: view === "open"
      },
      {
        id: "applied",
        label: "applied",
        count: all.filter((r) => inView(r, "applied")).length,
        on: view === "applied"
      }
    ]);
    const counts = /* @__PURE__ */ new Map();
    for (const r of rowsOf(view))
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
    });
  }
  const ctx = {
    get home() {
      return home();
    },
    get view() {
      return view;
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
    undo,
    redo,
    open: (key) => go(frag(view, key)),
    file: (r) => api.file(review?.round?.id ?? 0, r.id),
    appliedIn: (r) => review?.applies.find(
      (a) => a.repo === r.source.repo && (a.state === "pr" || a.state === "branch")
    )
  };
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
    const gs = groups();
    shown = entries(gs, openKey);
    if (openKey && !shown.some((e2) => e2.key === openKey)) {
      openKey = null;
      shown = entries(gs, null);
    }
    l.setItems(shown);
    decorate();
    const vkey = `${view}/${openKey ?? ""}/${editing ?? ""}`;
    const e = current();
    if (e) {
      const idx = shown.indexOf(e);
      syncing = true;
      l.open(idx);
      syncing = false;
      decorate();
      const oldNote = read.querySelector(".kit-note");
      const oldEdit = read.querySelector(".sift-edit");
      const focus = document.activeElement;
      const doc = e.kind === "group" ? groupDoc(ctx, e.group) : rowDoc(ctx, e.row);
      if (oldNote && lastView === vkey && (focus === oldNote || e.kind === "row" && oldNote.value !== ctx.noteOf(e.row)))
        doc.querySelector(".kit-note")?.replaceWith(oldNote);
      if (oldEdit && lastView === vkey)
        doc.querySelector(".sift-edit")?.replaceWith(oldEdit);
      read.replaceChildren(doc);
      if (focus instanceof HTMLElement && read.contains(focus)) focus.focus();
    } else {
      read.replaceChildren(overview(gs));
    }
    if (vkey !== lastView) read.scrollTop = 0;
    lastView = vkey;
  }
  function overview(gs) {
    const rows = gs.flatMap((g) => g.rows);
    if (view === "applied")
      return message(
        "applied",
        rows.length ? `${plural2(rows.length, "certain fix")}` : "No certain fixes",
        rows.length ? "sift applies these itself when the round is applied. Open one to read it, and undo any you want left as it is; it holds until you send." : "This round has nothing sift can fix on its own."
      );
    const p = progress(review?.rows ?? []);
    if (!rows.length)
      return message(
        "needs you",
        "Nothing here",
        filter || search ? "Nothing matches the filter." : "This round has nothing to judge."
      );
    const checks = [...new Set(rows.map((r) => r.check))].map((c) => checkCount(c, rows.filter((r) => r.check === c).length)).join(", ");
    return message(
      "needs you",
      `${p.total - p.decided} of ${plural2(p.total, "row")} to decide, in ${plural2(gs.length, "group")}`,
      `${checks}. Open a group (↵) to decide it whole, or a row to decide it alone: 1 accept, 2 edit, 3 reject. Send returns your decisions to the agent.`
    );
  }
  function go(f) {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const r = parseRoute(f);
    if (r.view !== view) {
      filter = "";
      editing = null;
    }
    if (r.key !== openKey) editing = null;
    view = r.view;
    openKey = r.key;
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
      const route = parseRoute(location.hash);
      view = route.view;
      openKey = route.key;
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
        fingerprint: r.fingerprint
      };
    });
    const single = rows.length === 1 && openKey === `r:${rows[0].id}`;
    editing = null;
    if (single) go(frag(view, nextOpen(shown, openKey)));
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
  function fixOp(rows, op) {
    const round = review?.round?.id;
    const live2 = rows.filter(
      (r) => isFix(r) && r.decision?.action === "reject" === (op === "redo")
    );
    if (!round || !live2.length) return;
    for (const r of live2) {
      const prev = r.decision;
      const note = pending.get(r.id) ?? "";
      pending.delete(r.id);
      r.decision = fixDecision(op, note);
      persist(
        () => op === "undo" ? api.undo(round, r, note) : api.redo(round, r, note),
        () => {
          if (prev) r.decision = prev;
          else delete r.decision;
        }
      );
    }
    render();
  }
  function undo(rows) {
    fixOp(rows, "undo");
  }
  function redo(rows) {
    fixOp(rows, "redo");
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
      () => isFix(r) && d.action === "reject" ? api.undo(round, r, text) : isFix(r) && d.action === "accept" ? api.redo(round, r, text) : api.decide(round, [
        {
          id: r.id,
          action: d.action,
          verdict: d.verdict,
          title: d.title,
          text: d.text,
          cleared: d.cleared,
          note: text,
          fingerprint: r.fingerprint
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
  const onRow = (fn, onGroup) => () => {
    const e = current();
    if (!e) return;
    if (e.kind === "row") fn(e.row);
    else onGroup?.(e.group.rows);
  };
  keys.register({
    keys: "1",
    label: "accept (a group: every undecided row)",
    group,
    run: onRow(
      (r) => !isFix(r) && decide([r], "accept"),
      (rows) => view === "open" && decide(groupTargets(rows, "accept"), "accept")
    )
  });
  keys.register({
    keys: "2",
    label: "edit the proposal",
    group,
    run: onRow(
      (r) => !isFix(r) && ctx.startEdit(r),
      (rows) => ctx.open(`r:${rows[0].id}`)
    )
  });
  keys.register({
    keys: "3",
    label: "reject (a group: every undecided row)",
    group,
    run: onRow(
      (r) => !isFix(r) && decide([r], "reject"),
      (rows) => view === "open" && decide(groupTargets(rows, "reject"), "reject")
    )
  });
  keys.register({
    keys: "u",
    label: "clear a decision; undo or redo a certain fix",
    group,
    run: onRow((r) => {
      if (!isFix(r)) clear(r);
      else if (r.decision?.action === "reject") redo([r]);
      else undo([r]);
    })
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
  if (!/^#\/(open|applied)/.test(location.hash))
    history.replaceState(null, "", frag("open"));
  void load();
}
boot();
