// app.ts
import {
  bar,
  buttons as buttons4,
  createKeys,
  h as h4,
  initTheme,
  list,
  live
} from "/_kit/kit.js";

// api.ts
import { createApi, ApiError } from "/_kit/kit.js";
function client(api) {
  return {
    review: (project) => api.get("/review", { project: String(project) }),
    async put(project, run, answers) {
      await api.put("/answers", { project, run, answers });
    },
    async del(project, id, hash) {
      const q = new URLSearchParams({ project: String(project), id, hash });
      await api.del(`/answers?${q.toString()}`);
    },
    async send(project) {
      const r = await api.post("/send", { project });
      return r?.sent ?? 0;
    }
  };
}
function newApi(opts) {
  return createApi(opts);
}

// model.ts
var CONCERNS = {
  tautological: {
    short: "restates the code",
    long: "it may only restate what the code does, or check values it set up itself"
  },
  incidental_detail: {
    short: "pins a detail",
    long: "it pins a detail that could change harmlessly (wording, formatting, structure)"
  },
  framework_behavior: {
    short: "tests a library",
    long: "it mostly checks a library or framework, not this project's code"
  },
  mock_only: {
    short: "mocks only",
    long: "it only checks that mocks were called"
  },
  not_missed: {
    short: "might not be missed",
    long: "a maintainer might not miss it if it were deleted"
  }
};
var FLAG_ORDER = [
  "tautological",
  "incidental_detail",
  "framework_behavior",
  "mock_only",
  "not_missed"
];
var PINS_SETTING = {
  flag: "pins_setting",
  value: 1,
  short: "checks a setting's value",
  long: "it only reads a project setting or class and compares it to fixed values"
};
var CONCERN_THRESHOLD = 0.35;
var TEST_LEANS = ["cut", "keep", "review"];
var GROUP_LEANS = ["consolidate", "keep_separate", "review"];
function lean(item) {
  const probs = item.jev.verdict.probabilities;
  const keys = item.kind === "group" ? GROUP_LEANS : TEST_LEANS;
  let best = keys[0];
  for (const k of keys) if (probs[k] > probs[best]) best = k;
  return best;
}
function actP(item) {
  const probs = item.jev.verdict.probabilities;
  return item.kind === "group" ? probs.consolidate : probs.cut;
}
function concern(item) {
  if (item.rule === "pins_setting") return PINS_SETTING;
  const jev = item.jev;
  let best = null;
  let bestV = CONCERN_THRESHOLD;
  for (const flag of FLAG_ORDER) {
    const v = jev[flag]?.noul;
    if (typeof v === "number" && v >= bestV && (best === null || v > bestV)) {
      best = flag;
      bestV = v;
    }
  }
  if (best === null) return null;
  return { flag: best, value: bestV, ...CONCERNS[best] };
}
var NO_CONCERN = "no clear reason";
function strength(p) {
  return p >= 0.5 ? "leaning" : "slightly";
}
function rvLevel(score) {
  const n = Math.max(0, Math.min(3, Math.round(score)));
  return ["nothing", "cosmetic", "real but minor", "important"][n];
}
function recommendation(item) {
  const p = actP(item).toFixed(2);
  if (item.kind === "group") {
    const j = item.jev;
    const l2 = lean(item);
    const verb = l2 === "consolidate" ? "merge" : l2 === "keep_separate" ? "separate" : null;
    const lead2 = verb ? `Jev leans ${verb} (merge ${p})` : `Jev can't decide (merge ${p})`;
    const exact = j.exact_duplicate.noul >= 0.7;
    return `${lead2}. Same behavior ${j.same_behavior.noul.toFixed(2)}; loss if merged ${j.loss_if_merged.noul.toFixed(2)}${exact ? "; one of these looks like an exact duplicate" : ""}.`;
  }
  const l = lean(item);
  const lead = l === "cut" ? `Jev leans cut (${p})` : l === "keep" ? `Jev leans keep (cut ${p})` : `Jev can't decide (cut ${p})`;
  const c = concern(item);
  if (c === PINS_SETTING) {
    return `${lead}. cull sends it to you because it only checks a setting's value; only you know whether that value is deliberate. Protects behavior rated ${rvLevel(item.jev.regression_value.score)}.`;
  }
  const why = c ? `Jev's main concern: ${c.long} (${c.value.toFixed(2)}).` : "No single concern stood out; Jev is split on whether it earns its place.";
  return `${lead}. ${why} Protects behavior rated ${rvLevel(item.jev.regression_value.score)}.`;
}

// bulk.ts
var byActDesc = (a, b) => actP(b) - actP(a) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
function buckets(items, answers) {
  const isGroup = items.length > 0 && items[0].kind === "group";
  const out = {};
  for (const id of isGroup ? ["consolidate", "keep_separate"] : ["cut", "keep", "review"]) {
    out[id] = [];
  }
  out.answered = [];
  for (const it of items) {
    if (answers.has(it.id)) out.answered.push(it);
    else (out[lean(it)] ??= []).push(it);
  }
  for (const k of Object.keys(out)) out[k].sort(byActDesc);
  return out;
}
var CONCERN_ORDER = [
  PINS_SETTING.short,
  "restates the code",
  "pins a detail",
  "might not be missed",
  "tests a library",
  "mocks only",
  NO_CONCERN
];
function byConcern(items) {
  const m = /* @__PURE__ */ new Map();
  for (const it of items) {
    const key = concern(it)?.short ?? NO_CONCERN;
    m.set(key, [...m.get(key) ?? [], it]);
  }
  const rows = [];
  for (const c of CONCERN_ORDER) {
    const xs = m.get(c);
    if (!xs?.length) continue;
    xs.sort(byActDesc);
    rows.push({
      concern: c,
      items: xs,
      meanP: xs.reduce((s, x) => s + actP(x), 0) / xs.length
    });
  }
  return rows;
}
function bySize(groups) {
  const m = /* @__PURE__ */ new Map();
  for (const g of groups) {
    const n = g.state.tests.length;
    const key = n >= 5 ? "5+" : String(Math.max(n, 2));
    m.set(key, [...m.get(key) ?? [], g]);
  }
  const rows = [];
  for (const size of ["2", "3", "4", "5+"]) {
    const xs = m.get(size);
    if (!xs?.length) continue;
    xs.sort(byActDesc);
    rows.push({
      size,
      items: xs,
      meanP: xs.reduce((s, x) => s + actP(x), 0) / xs.length
    });
  }
  return rows;
}
function bulkTargets(items, answers) {
  return items.filter((i) => !answers.has(i.id));
}

// group.ts
import {
  buttons as buttons2,
  card as card2,
  codeBlock as codeBlock2,
  facts as facts2,
  fold as fold2,
  h as h2
} from "/_kit/kit.js";

// item.ts
import {
  buttons,
  card,
  codeBlock,
  facts,
  fold,
  h,
  noteField
} from "/_kit/kit.js";

// text.ts
var PY = [
  "assert ",
  "assert(",
  "with pytest.raises",
  "pytest.raises",
  "self.assert"
];
var GO = ["t.Error", "t.Fatal", "assert.", "require."];
var TS = ["expect(", "assert.", "assert("];
function assertLines(lang, body) {
  const lines = body.split("\n").map((l) => l.trim());
  switch (lang) {
    case "python":
      return lines.filter((l) => PY.some((p) => l.startsWith(p)));
    case "go":
      return lines.filter((l) => GO.some((p) => l.includes(p)));
    case "typescript":
    case "javascript":
      return lines.filter((l) => TS.some((p) => l.includes(p)));
    default:
      return [];
  }
}

// item.ts
var FLAGS = [
  ["tautological", "restates the code"],
  ["incidental_detail", "pins a detail"],
  ["framework_behavior", "tests a library"],
  ["mock_only", "mocks only"],
  ["not_missed", "might not be missed"]
];
function sigTable(rows) {
  return h(
    "table",
    { class: "sig" },
    ...rows.map(
      ([k, v]) => h(
        "tr",
        null,
        h("td", null, k),
        h(
          "td",
          null,
          h(
            "span",
            { class: "bar" },
            h("i", { style: `width:${Math.round(v * 100)}%` })
          ),
          h("span", { class: "v" }, v.toFixed(2))
        )
      )
    )
  );
}
function backLink(ctx) {
  return h(
    "button",
    { class: "back", type: "button", onclick: () => ctx.back() },
    "← back to the overview (b)"
  );
}
function noteBlock(ctx, it) {
  const field = noteField({
    value: ctx.noteOf(it),
    placeholder: "why — n",
    onCommit: (v) => ctx.setNote(it, v)
  });
  field.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.isComposing) field.blur();
  });
  return [h("div", { class: "kit-label" }, "note (optional)"), field];
}
function testItem(ctx, t) {
  const a = ctx.answerOf(t.id);
  const l = lean(t);
  const p = actP(t);
  const kick = `test · ${ctx.rootName} · ${t.file}`;
  const head = [
    backLink(ctx),
    h("div", { class: "kit-kick" }, ctx.blind ? `${kick} · blind` : kick),
    h("h1", { class: "kit-h1" }, t.name)
  ];
  const answerBtns = () => {
    const unanswer = a ? [{ label: "unanswer (u)", run: () => ctx.unanswer(t) }] : [];
    return [
      { label: "1 keep", run: () => ctx.answer([t], "keep", "item") },
      {
        label: "2 cut",
        danger: true,
        run: () => ctx.answer([t], "cut", "item")
      },
      ...unanswer
    ];
  };
  const src = t.state;
  const asserts = assertLines(src.language, src.test_source);
  const callees = src.code_under_test ?? [];
  const tail = [
    h("div", { class: "kit-label" }, "what it asserts"),
    asserts.length ? codeBlock(asserts.join("\n")) : h("p", { class: "why" }, "no assert lines found; read the whole test"),
    fold("the whole test", codeBlock(src.test_source)),
    src.setup_context ? fold("setup it uses", codeBlock(src.setup_context)) : "",
    callees.length ? fold(
      `code under test · ${callees.map((c) => c.symbol).join(", ")}`,
      h(
        "div",
        null,
        ...callees.map(
          (c) => h(
            "div",
            null,
            h("div", { class: "kit-label" }, c.file),
            codeBlock(c.source)
          )
        )
      )
    ) : h("p", { class: "why" }, "code under test: none found"),
    ...noteBlock(ctx, t)
  ];
  if (ctx.blind) {
    return h(
      "div",
      { class: "kit-doc" },
      ...head,
      facts([["your answer", a?.value ?? "open"]]),
      h("div", { class: "kit-label" }, "your answer"),
      buttons(answerBtns()),
      ...tail
    );
  }
  const rec = l === "review" ? "look" : l;
  const acts = l === "review" ? answerBtns() : [
    {
      label: `accept: ${l}`,
      fill: true,
      run: () => ctx.answer([t], l === "cut" ? "cut" : "keep", "item")
    },
    {
      label: `${l === "cut" ? "keep" : "cut"} instead`,
      danger: l !== "cut",
      run: () => ctx.answer([t], l === "cut" ? "keep" : "cut", "item")
    },
    ...a ? [{ label: "unanswer (u)", run: () => ctx.unanswer(t) }] : []
  ];
  const flagRows = FLAGS.map(([k, w]) => [
    w,
    t.jev[k]?.noul ?? 0
  ]);
  flagRows.sort((x, y) => y[1] - x[1]);
  const pr = t.jev.verdict.probabilities;
  return h(
    "div",
    { class: "kit-doc" },
    ...head,
    facts([
      ["cut probability", p.toFixed(2)],
      ["protects", rvLevel(t.jev.regression_value.score)],
      ["your answer", a?.value ?? "open"]
    ]),
    card({
      edge: "agent",
      head: `cull recommends · ${rec} · ${strength(p)}`,
      body: recommendation(t),
      actions: acts
    }),
    h("div", { class: "kit-label" }, "jev’s signals"),
    sigTable([
      ["verdict: cut", pr.cut],
      ["verdict: keep", pr.keep],
      ["verdict: needs a look", pr.review],
      ...flagRows
    ]),
    ...tail
  );
}

// group.ts
function groupItem(ctx, g) {
  const a = ctx.answerOf(g.id);
  const l = lean(g);
  const p = actP(g);
  const members = g.state.tests;
  const kick = `group · ${ctx.rootName} · ${g.file}`;
  const head = [
    backLink(ctx),
    h2("div", { class: "kit-kick" }, ctx.blind ? `${kick} · blind` : kick),
    h2("h1", { class: "kit-h1" }, `${members.length} similar tests`)
  ];
  const unanswer = a ? [{ label: "unanswer (u)", run: () => ctx.unanswer(g) }] : [];
  const plain = [
    { label: "1 separate", run: () => ctx.answer([g], "separate", "item") },
    { label: "2 merge", run: () => ctx.answer([g], "merge", "item") },
    ...unanswer
  ];
  const maxRow = Math.max(0, ...(g.rows ?? []).map((r) => r.length));
  const rows = h2(
    "div",
    { class: "scroll" },
    h2(
      "table",
      { class: "ov rows" },
      ...members.map(
        (m, i) => h2(
          "tr",
          null,
          h2("td", null, m.name),
          ...Array.from(
            { length: maxRow },
            (_, j2) => h2("td", null, (g.rows?.[i] ?? [])[j2] ?? "")
          )
        )
      )
    )
  );
  const tail = [
    h2(
      "div",
      { class: "kit-label" },
      "the table test’s rows (values that differ)"
    ),
    rows,
    ...members.map((m) => fold2(m.name, codeBlock2(m.source))),
    ...noteBlock(ctx, g)
  ];
  if (ctx.blind) {
    return h2(
      "div",
      { class: "kit-doc" },
      ...head,
      facts2([["your answer", a?.value ?? "open"]]),
      h2("div", { class: "kit-label" }, "your answer"),
      buttons2(plain),
      ...tail
    );
  }
  const verb = l === "consolidate" ? "merge" : "separate";
  const other = verb === "merge" ? "separate" : "merge";
  const acts = l === "review" ? plain : [
    {
      label: `accept: ${verb}`,
      fill: true,
      run: () => ctx.answer([g], verb, "item")
    },
    {
      label: `${other} instead`,
      run: () => ctx.answer([g], other, "item")
    },
    ...unanswer
  ];
  const j = g.jev;
  return h2(
    "div",
    { class: "kit-doc" },
    ...head,
    facts2([
      ["merge probability", p.toFixed(2)],
      ["your answer", a?.value ?? "open"]
    ]),
    card2({
      edge: "agent",
      head: l === "review" ? `cull recommends · look · ${strength(p)}` : `cull recommends · ${verb} · ${strength(p)}`,
      body: recommendation(g),
      actions: acts
    }),
    h2("div", { class: "kit-label" }, "jev’s signals"),
    sigTable([
      ["merge", p],
      ["same behavior", j.same_behavior.noul],
      ["exact duplicate", j.exact_duplicate.noul],
      ["something lost if merged", j.loss_if_merged.noul]
    ]),
    ...tail
  );
}

// retry.ts
var STEPS = [1e3, 2e3, 5e3];
function retryDelay(attempt) {
  return STEPS[attempt] ?? 1e4;
}

// overview.ts
import { buttons as buttons3, card as card3, facts as facts3, h as h3 } from "/_kit/kit.js";
var TEST_BUCKETS = [
  {
    id: "cut",
    label: "leans cut",
    head: (n) => `${n} tests Jev leans toward cutting`,
    lead: "Jev thinks these probably don’t earn their place, but not strongly enough (cut 0.30–0.59) to cut them without you."
  },
  {
    id: "keep",
    label: "leans keep",
    head: (n) => `${n} tests Jev leans toward keeping`,
    lead: "Jev’s most likely answer is keep, but its cut probability is still 0.30 or more."
  },
  {
    id: "review",
    label: "undecided",
    head: (n) => `${n} tests Jev can’t decide`,
    lead: "Jev’s most likely answer is “needs a look”. These are the ones most worth reading."
  }
];
var GROUP_BUCKETS = [
  {
    id: "consolidate",
    label: "leans merge",
    head: (n) => `${n} groups Jev leans toward merging`,
    lead: "Similar tests in one file that Jev thinks could become one table test (merge 0.30–0.59). A merge is written by the agent and checked by cull."
  },
  {
    id: "keep_separate",
    label: "leans separate",
    head: (n) => `${n} groups Jev leans toward keeping separate`,
    lead: "Similar-looking tests Jev thinks probably check different things."
  },
  {
    id: "review",
    label: "undecided",
    head: (n) => `${n} groups Jev can’t decide`,
    lead: "Jev’s most likely answer is “needs a look”. Read each group and decide."
  }
];
var BLIND_BUCKET = (section) => ({
  id: "open",
  label: "to judge",
  head: (n) => `${n} ${section} to judge`,
  lead: `Read each ${section === "tests" ? "test" : "group"} and answer ${section === "tests" ? "keep or cut" : "separate or merge"}. Jev’s opinion is hidden.`
});
var REASON_WHY = {
  [PINS_SETTING.short]: PINS_SETTING.long,
  "restates the code": "asserts values it set up, or re-computes the answer the way the code does",
  "pins a detail": "exact wording, formatting or internal structure",
  "might not be missed": "little protection would be lost",
  "tests a library": "mostly exercises a library or framework",
  "mocks only": "only checks that mocks were called",
  "no clear reason": "no concern stood out; Jev is split overall"
};
function table(head, rows) {
  return h3(
    "table",
    { class: "ov" },
    h3("tr", null, ...head.map((t) => h3("th", null, t))),
    ...rows
  );
}
function overview(ctx, a) {
  const note = a.notice ? [h3("p", { class: "notice" }, a.notice)] : [];
  if (!a.words) return answeredView(ctx, a, note);
  const bk = a.words;
  const xs = a.xs;
  const doc = h3(
    "div",
    { class: "kit-doc" },
    ...note,
    h3(
      "div",
      { class: "kit-kick" },
      `${a.section} · ${ctx.rootName} · ${bk.label}`
    ),
    h3(
      "h1",
      { class: "kit-h1" },
      xs.length ? bk.head(xs.length) : `Nothing left that ${bk.label}`
    ),
    h3("p", { class: "lead" }, bk.lead)
  );
  if (!xs.length) return doc;
  const first = () => ctx.openSubset(xs);
  if (ctx.blind) {
    doc.append(
      buttons3([{ label: "open the first", fill: true, run: first }]),
      h3("p", { class: "why" }, "Open any one from the list (j/k, ↵).")
    );
    return doc;
  }
  return a.section === "tests" ? testOverview(ctx, doc, bk, xs, a) : groupOverview(ctx, doc, bk, xs, a);
}
function testOverview(ctx, doc, bk, xs, a) {
  const rows = byConcern(xs).map((r) => {
    const open = bulkTargets(r.items, a.answers);
    return h3(
      "tr",
      null,
      h3(
        "td",
        null,
        h3("div", null, r.concern),
        h3("div", { class: "why" }, REASON_WHY[r.concern] ?? "")
      ),
      h3("td", { class: "n" }, String(r.items.length)),
      h3("td", { class: "n" }, r.meanP.toFixed(2)),
      h3(
        "td",
        null,
        buttons3([
          { label: "open", run: () => ctx.openSubset(r.items) },
          {
            label: `keep ${open.length}`,
            disabled: !open.length,
            run: () => ctx.answer(open, "keep", "group")
          },
          {
            label: `cut ${open.length}`,
            danger: true,
            disabled: !open.length,
            run: () => ctx.answer(open, "cut", "group")
          }
        ])
      )
    );
  });
  const out = [];
  if (bk.id === "cut" || bk.id === "keep") {
    const v = bk.id;
    out.push(
      card3({
        edge: "agent",
        head: `accept jev’s lean · ${v} all ${xs.length}`,
        body: v === "cut" ? "Answers cut for every test still open here. cull apply still runs the suite before and after and rolls back if it breaks; a cut test is gone from the file, not from git." : "Answers keep for every test still open here. Nothing changes in the code.",
        actions: [
          {
            label: `${v} all ${xs.length}`,
            fill: true,
            run: () => ctx.answer(bulkTargets(xs, a.answers), v, "group")
          },
          { label: "open the first", run: () => ctx.openSubset(xs) }
        ]
      })
    );
  }
  doc.append(
    ...out,
    h3("div", { class: "kit-label" }, "by concern"),
    table(["Jev’s main concern", "tests", "cut p", ""], rows),
    h3(
      "p",
      { class: "why" },
      "Open any test from the list (j/k, ↵) to read it and override. Answers you give one at a time are never changed by a group action."
    )
  );
  return doc;
}
function groupOverview(ctx, doc, bk, xs, a) {
  const rows = bySize(xs).map((r) => {
    const open = bulkTargets(r.items, a.answers);
    return h3(
      "tr",
      null,
      h3("td", null, `${r.size} tests`),
      h3("td", { class: "n" }, String(r.items.length)),
      h3("td", { class: "n" }, r.meanP.toFixed(2)),
      h3(
        "td",
        null,
        buttons3([
          { label: "open", run: () => ctx.openSubset(r.items) },
          {
            label: `separate ${open.length}`,
            disabled: !open.length,
            run: () => ctx.answer(open, "separate", "group")
          },
          {
            label: `merge ${open.length}`,
            disabled: !open.length,
            run: () => ctx.answer(open, "merge", "group")
          }
        ])
      )
    );
  });
  if (bk.id === "consolidate" || bk.id === "keep_separate") {
    const v = bk.id === "consolidate" ? "merge" : "separate";
    doc.append(
      card3({
        edge: "agent",
        head: `accept jev’s lean · ${v} all ${xs.length}`,
        body: v === "merge" ? "The agent rewrites each group as one table test; cull check --group confirms every case survived." : "Nothing changes in the code.",
        actions: [
          {
            label: `${v} all ${xs.length}`,
            fill: true,
            run: () => ctx.answer(bulkTargets(xs, a.answers), v, "group")
          },
          { label: "open the first", run: () => ctx.openSubset(xs) }
        ]
      })
    );
  }
  doc.append(
    h3("div", { class: "kit-label" }, "by size"),
    table(["group size", "groups", "merge p", ""], rows),
    h3(
      "p",
      { class: "why" },
      "Open any group from the list (j/k, ↵) to read it and override. Answers you give one at a time are never changed by a group action."
    )
  );
  return doc;
}
function answeredView(ctx, a, note) {
  const vs = a.section === "tests" ? ["cut", "keep"] : ["merge", "separate"];
  const count = (v) => a.xs.filter((x) => a.answeredOf(x.id) === v).length;
  return h3(
    "div",
    { class: "kit-doc" },
    ...note,
    h3(
      "div",
      { class: "kit-kick" },
      `${a.section} · ${ctx.rootName} · answered`
    ),
    h3("h1", { class: "kit-h1" }, `${a.xs.length} answered`),
    facts3(vs.map((v) => [v, String(count(v))])),
    h3(
      "p",
      null,
      `${a.allOpen} still open across tests and groups. Sending returns these answers; the rest stay on the page.`
    )
  );
}

// app.ts
var NOTICE_CHANGED = "That test changed; it was judged again.";
var qs = new URLSearchParams(location.search);
var BLIND = qs.get("blind") === "1";
var frag = (project, item) => `#/p/${project}${item ? `/${encodeURIComponent(item)}` : ""}`;
function parseRoute(hash) {
  const m = /^#\/p\/(\d+)(?:\/(.+))?$/.exec(hash);
  if (!m) return null;
  let item = "";
  try {
    item = m[2] ? decodeURIComponent(m[2]) : "";
  } catch {
    item = "";
  }
  return { project: Number(m[1]), item };
}
var itemSort = (a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
var basename = (p) => p.replace(/\/+$/, "").split("/").pop() || p;
var plural = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
function signature(r) {
  return `${r.run?.id ?? 0}|` + r.items.map(
    (i) => `${i.id}@${i.hash}:${i.answer ? `${i.answer.value}/${i.answer.note}/${i.answer.via}/${i.answer.sent_at ? 1 : 0}` : ""}`
  ).join(";");
}
function boot() {
  let review = null;
  let project = 0;
  let section = "tests";
  let bucket = "cut";
  let openId = null;
  let subset = null;
  let notice = "";
  let lastFrag = "";
  let lastView = "";
  let syncing = false;
  let stale = false;
  let inflight = 0;
  let loading = false;
  let dirty = false;
  let liveHandle = null;
  let statusTimer;
  let prevLive = "live";
  let loadAttempt = 0;
  let loadTimer;
  const conflicted = [];
  let flashing = false;
  let reloadNote = "";
  const pending = /* @__PURE__ */ new Map();
  const gated = (input, init) => stale ? Promise.reject(new Error("cull serve restarted: this tab has stopped")) : fetch(input, init);
  const api = client(
    newApi({
      fetch: gated,
      onStale() {
        goStale();
      }
    })
  );
  const rootName = () => review ? basename(review.project.root) : "";
  const sectionKind = (s) => s === "tests" ? "test" : "group";
  const itemsOf = (s) => (review?.items ?? []).filter((i) => i.kind === sectionKind(s));
  const answerOf = (id) => review?.items.find((i) => i.id === id)?.answer;
  const answerMap = () => new Map(
    (review?.items ?? []).filter((i) => i.answer).map((i) => [i.id, i.answer])
  );
  const wordsOf = (s) => BLIND ? [BLIND_BUCKET(s)] : s === "tests" ? TEST_BUCKETS : GROUP_BUCKETS;
  const bucketIds = (s) => [
    ...wordsOf(s).map((w) => w.id),
    "answered"
  ];
  function bucketsNow(s) {
    const xs = itemsOf(s);
    const am = answerMap();
    if (BLIND) {
      return {
        open: xs.filter((x) => !am.has(x.id)).sort(itemSort),
        answered: xs.filter((x) => am.has(x.id)).sort(itemSort)
      };
    }
    const b2 = buckets(xs, am);
    for (const w of wordsOf(s)) b2[w.id] ??= [];
    return b2;
  }
  const firstNonEmpty = (s) => {
    const b2 = bucketsNow(s);
    return wordsOf(s).find((w) => b2[w.id].length)?.id ?? wordsOf(s)[0].id;
  };
  const naturalBucket = (it) => it.answer ? "answered" : BLIND ? "open" : lean(it);
  const listItems = () => {
    const xs = bucketsNow(section)[bucket] ?? [];
    return subset ? xs.filter((x) => subset.has(x.id)) : xs;
  };
  const unsent = () => (review?.items ?? []).filter((i) => i.answer && !i.answer.sent_at).length;
  const openCount = (s) => itemsOf(s).filter((i) => !i.answer).length;
  const b = bar({
    brand: { name: "cull" },
    sections: [
      { id: "tests", label: "tests", count: 0 },
      { id: "groups", label: "groups", count: 0 }
    ],
    active: "tests",
    onSection(id) {
      section = id === "groups" ? "groups" : "tests";
      bucket = firstNonEmpty(section);
      subset = null;
      openId = null;
      notice = "";
      go(frag(project));
    },
    status: "",
    staleText: "restarted · continued in a new tab"
  });
  const theme = initTheme("cull", b.themeControl);
  const forced = qs.get("theme");
  if (forced === "light" || forced === "dark") theme.set(forced);
  function baseStatus() {
    if (!review) return "";
    if (!review.run) return "no review yet — run cull check";
    const r = review.run;
    const s = r.summary ?? {};
    const mode = r.mode === "suite" ? "whole suite" : r.mode;
    return `${basename(review.project.root)} · ${mode} · ${r.total.toLocaleString("en-US")} tests · ${(s.cut ?? 0).toLocaleString("en-US")} cut, ${(s.consolidate ?? 0).toLocaleString("en-US")} merges and ${(s.keep ?? 0).toLocaleString("en-US")} keeps settled without you`;
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
    if (!stale) b.setSection(section);
    for (const s of ["tests", "groups"]) {
      const n2 = openCount(s);
      b.setCount(s, n2 || itemsOf(s).length === 0 ? n2 : "✓");
    }
    const n = unsent();
    b.setPrimary(
      n ? { label: `Send ${plural(n, "answer")}`, run: () => void send() } : null
    );
  }
  const read = h4("main", { class: "kit-read" });
  const selCount = h4("span", { class: "kit-n" }, "");
  const footBtns = h4("span", { style: "display:contents" });
  const l = list({
    label: "",
    views: [],
    onChip(_, id) {
      bucket = id;
      subset = null;
      openId = null;
      notice = "";
      go(frag(project));
    },
    row(x) {
      const a = answerOf(x.id)?.value;
      const meta = (p2) => a ?? (BLIND ? "·" : p2.toFixed(2));
      const file = x.file.replace(/^tests\//, "");
      if (x.kind === "test") {
        const p2 = actP(x);
        const key = BLIND ? file : `${concern(x)?.short ?? "no clear reason"} · ${file}`;
        const lw = lean(x) === "review" ? "undecided" : lean(x);
        return {
          id: x.id,
          key,
          title: x.name,
          meta: meta(p2),
          sub: a ? `answered ${a}` : BLIND ? "" : `${strength(p2)} ${lw}`,
          selectable: true
        };
      }
      const p = actP(x);
      return {
        id: x.id,
        key: `${x.state.tests.length} tests · ${file}`,
        title: x.state.tests.map((m) => m.name).join(", "),
        meta: meta(p),
        sub: a ? `answered ${a}` : BLIND ? "" : `merge ${p.toFixed(2)} · same behavior ${x.jev.same_behavior.noul.toFixed(2)}`,
        selectable: true
      };
    },
    onOpen(x) {
      if (syncing) return;
      go(frag(project, x.id));
    },
    onSelect(sel) {
      selCount.textContent = sel.length ? `${sel.length} selected` : "";
      footBtns.replaceChildren(footButtons(sel));
    },
    foot: h4(
      "span",
      { style: "display:contents" },
      selCount,
      h4("span", { style: "margin-left:auto" }),
      footBtns
    )
  });
  function footButtons(sel) {
    if (!sel.length) return "";
    if (bucket === "answered") {
      return buttons4([
        {
          label: `unanswer ${sel.length}`,
          run() {
            l.clearSelection();
            sel.forEach(unanswer);
          }
        }
      ]);
    }
    const targets = bulkTargets(sel, answerMap());
    if (!targets.length) return "";
    const [no, yes] = section === "tests" ? ["keep", "cut"] : ["separate", "merge"];
    return buttons4([
      {
        label: `${no} ${targets.length}`,
        run: () => ctx.answer(bulkTargets(sel, answerMap()), no, "group")
      },
      {
        label: `${yes} ${targets.length}`,
        danger: yes === "cut",
        run: () => ctx.answer(bulkTargets(sel, answerMap()), yes, "group")
      }
    ]);
  }
  function chips() {
    const bk = bucketsNow(section);
    const out = [
      ...wordsOf(section).map((w) => ({ id: w.id, label: w.label })),
      { id: "answered", label: "answered" }
    ].map((w) => ({ ...w, count: bk[w.id].length, on: bucket === w.id })).filter((c) => c.count > 0 || c.on || c.id === "answered");
    l.setChips("view", out);
  }
  const ctx = {
    blind: BLIND,
    get rootName() {
      return rootName();
    },
    answerOf,
    noteOf: (it) => pending.get(it.id) ?? it.answer?.note ?? "",
    answer,
    unanswer,
    setNote,
    open: (it) => go(frag(project, it.id)),
    openSubset(items) {
      if (!items.length) return;
      subset = new Set(items.map((i) => i.id));
      go(frag(project, items[0].id));
    },
    back() {
      go(frag(project));
    }
  };
  function message(kick, title, body) {
    return h4(
      "div",
      { class: "kit-doc" },
      h4("div", { class: "kit-kick" }, kick),
      h4("h1", { class: "kit-h1" }, title),
      h4("p", { class: "lead" }, body)
    );
  }
  function render() {
    refreshBar();
    if (!review) return;
    if (!flashing) b.setStatus(baseStatus());
    if (!review.run) {
      l.setItems([]);
      chips();
      read.replaceChildren(
        message(
          `cull · ${rootName()}`,
          "No review yet",
          `Run cull check in ${review.project.root}; this page shows what it could not settle.`
        )
      );
      return;
    }
    if (!review.items.length) {
      l.setItems([]);
      chips();
      read.replaceChildren(
        message(
          `cull · ${rootName()}`,
          `nothing to review in ${review.project.root}`,
          "Everything in the last run was settled without you."
        )
      );
      return;
    }
    const it = openId ? review.items.find((i) => i.id === openId) : void 0;
    if (it) {
      section = it.kind === "test" ? "tests" : "groups";
      if (!(bucketsNow(section)[bucket] ?? []).some((x) => x.id === it.id)) {
        bucket = naturalBucket(it);
        subset = null;
      } else if (subset && !subset.has(it.id)) subset = null;
    } else openId = null;
    if (!bucketIds(section).includes(bucket)) bucket = firstNonEmpty(section);
    refreshBar();
    chips();
    const shown = listItems();
    l.setItems(shown);
    footBtns.replaceChildren(footButtons(l.selected()));
    const view = `${section}/${bucket}/${openId ?? ""}`;
    if (it) {
      const idx = shown.findIndex((x) => x.id === it.id);
      if (idx >= 0) {
        syncing = true;
        l.open(idx);
        syncing = false;
      }
      const oldNote = read.querySelector(".kit-note");
      const hadFocus = !!oldNote && document.activeElement === oldNote;
      const keepNote = oldNote && lastView === view && (document.activeElement === oldNote || oldNote.value !== ctx.noteOf(it));
      const doc = it.kind === "test" ? testItem(ctx, it) : groupItem(ctx, it);
      if (keepNote) doc.querySelector(".kit-note")?.replaceWith(oldNote);
      read.replaceChildren(doc);
      if (keepNote && hadFocus) oldNote.focus();
    } else {
      const words = wordsOf(section).find((w) => w.id === bucket) ?? null;
      read.replaceChildren(
        overview(ctx, {
          section,
          words,
          xs: bucketsNow(section)[bucket] ?? [],
          all: itemsOf(section),
          answers: answerMap(),
          answeredOf: (id) => answerOf(id)?.value,
          allOpen: openCount("tests") + openCount("groups"),
          notice
        })
      );
    }
    if (view !== lastView) read.scrollTop = 0;
    lastView = view;
  }
  function go(f) {
    lastFrag = f;
    if (location.hash !== f) location.hash = f;
    const r = parseRoute(f);
    openId = r?.item || null;
    if (openId) notice = "";
    render();
  }
  function onHash() {
    if (location.hash === lastFrag) return;
    lastFrag = location.hash;
    route();
  }
  function route() {
    const r = parseRoute(location.hash);
    if (!r) {
      review = null;
      project = 0;
      l.setItems([]);
      refreshBar();
      read.replaceChildren(
        h4(
          "div",
          { class: "kit-doc" },
          h4("p", { class: "lead" }, "open this page with cull serve <path>")
        )
      );
      return;
    }
    if (r.project !== project || !review) {
      project = r.project;
      loadAttempt = 0;
      openId = r.item || null;
      void load();
      return;
    }
    openId = r.item || null;
    if (openId) notice = "";
    render();
  }
  async function load() {
    clearTimeout(loadTimer);
    try {
      const r = await api.review(project);
      loadAttempt = 0;
      review = r;
      const known = openId && r.items.some((i) => i.id === openId);
      if (!known) openId = null;
      section = "tests";
      bucket = firstNonEmpty("tests");
      if (!itemsOf("tests").some((i) => !i.answer) && itemsOf("groups").some((i) => !i.answer)) {
        section = "groups";
        bucket = firstNonEmpty("groups");
      }
      if (!openId && lastFrag !== frag(project)) {
        lastFrag = frag(project);
        history.replaceState(null, "", frag(project));
      }
      render();
      startLive(r.cursor);
    } catch (err) {
      review = null;
      l.setItems([]);
      refreshBar();
      if (err instanceof ApiError && err.isStale) return;
      const notFound = err instanceof ApiError && err.status === 404;
      if (!notFound) {
        loadTimer = setTimeout(() => void load(), retryDelay(loadAttempt++));
      }
      const msg = notFound ? "cull does not know that project; open this page with cull serve <path>" : "cull serve is not answering; retrying.";
      read.replaceChildren(
        h4("div", { class: "kit-doc" }, h4("p", { class: "lead" }, msg))
      );
    }
  }
  function applyReview(r) {
    const was = review;
    if (was && signature(was) === signature(r)) {
      was.cursor = r.cursor;
      return;
    }
    if (openId) {
      const shownHash = was?.items.find((i) => i.id === openId)?.hash;
      if (!r.items.some((i) => i.id === openId && i.hash === shownHash)) {
        openId = null;
        notice = NOTICE_CHANGED;
        lastFrag = frag(project);
        history.replaceState(null, "", lastFrag);
      }
    }
    review = r;
    render();
  }
  async function reload() {
    if (!project) return true;
    if (inflight > 0 || loading) {
      dirty = true;
      return true;
    }
    loading = true;
    try {
      do {
        dirty = false;
        applyReview(await api.review(project));
        conflicted.length = 0;
      } while (dirty && inflight === 0);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.isStale) return false;
      if (conflicted.length) {
        conflicted.splice(0).forEach((u) => u());
        render();
        flash("Could not reload; refresh the page.", "danger");
      } else {
        flash(`could not reload: ${err.message}`, "danger");
      }
      return false;
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
      onEvent(e) {
        const d = e.data ?? {};
        if (e.type === "reset" || d.project === project) void reload();
      },
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
  function persist(op, undo) {
    inflight++;
    op().catch((err) => {
      if (err instanceof ApiError && err.status === 409) {
        reloadNote = "The review changed; reloaded.";
        conflicted.push(undo);
        dirty = true;
        return;
      }
      if (err instanceof ApiError && err.isStale) return;
      undo();
      render();
      flash(`Not saved: ${err.message ?? String(err)}`, "danger");
    }).finally(() => {
      inflight--;
      if (inflight === 0 && dirty) {
        void reload().then((ok) => {
          if (ok && reloadNote) flash(reloadNote);
          reloadNote = "";
        });
      }
    });
  }
  function answer(items, value, via) {
    if (!review?.run || !items.length) return;
    const run = review.run.id;
    const prev = items.map((it) => it.answer);
    const waiting = items.map((it) => pending.get(it.id));
    const viewing = openId;
    const idx = viewing ? listItems().findIndex((x) => x.id === viewing) : -1;
    const body = items.map((it) => {
      const note = pending.get(it.id) ?? it.answer?.note ?? "";
      pending.delete(it.id);
      it.answer = { value, note, via, blind: BLIND };
      return {
        id: it.id,
        hash: it.hash,
        kind: it.kind,
        value,
        note,
        via,
        blind: BLIND
      };
    });
    if (via === "group") l.clearSelection();
    let next = viewing;
    if (via === "item" && viewing && items.some((i) => i.id === viewing) && bucket !== "answered") {
      const rest = listItems();
      next = rest.length ? rest[Math.min(Math.max(idx, 0), rest.length - 1)].id : null;
    }
    if (next !== viewing) go(frag(project, next ?? void 0));
    else render();
    const p = project;
    persist(
      () => api.put(p, run, body),
      () => items.forEach((it, i) => {
        it.answer = prev[i];
        const w = waiting[i];
        if (w !== void 0) pending.set(it.id, w);
      })
    );
  }
  function unanswer(it) {
    const prev = it.answer;
    if (!prev) return;
    delete it.answer;
    render();
    const p = project;
    persist(
      () => api.del(p, it.id, it.hash),
      () => {
        it.answer = prev;
      }
    );
  }
  function setNote(shown, text) {
    const it = review?.items.find((i) => i.id === shown.id) ?? shown;
    const a = it.answer;
    if (!a || !review?.run) {
      if (text) pending.set(it.id, text);
      else pending.delete(it.id);
      return;
    }
    if (a.note === text) return;
    const old = a.note;
    a.note = text;
    const p = project;
    const run = review.run.id;
    persist(
      () => api.put(p, run, [
        {
          id: it.id,
          hash: it.hash,
          kind: it.kind,
          value: a.value,
          note: text,
          via: a.via,
          blind: a.blind
        }
      ]),
      () => {
        a.note = old;
      }
    );
  }
  async function send() {
    try {
      const n = await api.send(project);
      const now = (/* @__PURE__ */ new Date()).toISOString();
      for (const it of review?.items ?? [])
        if (it.answer && !it.answer.sent_at) it.answer.sent_at = now;
      refreshBar();
      flash(
        n ? `Sent ${plural(n, "answer")}. The agent is told in a later release; cull check uses them now.` : "Nothing to send."
      );
    } catch (err) {
      if (!(err instanceof ApiError && err.isStale))
        flash(`Not sent: ${err.message}`, "danger");
    }
  }
  const keys = createKeys({ list: l });
  const current = () => openId ? review?.items.find((i) => i.id === openId) : void 0;
  const group = "answer";
  keys.register({
    keys: "1",
    label: "keep / separate",
    group,
    run() {
      const it = current();
      if (it) answer([it], it.kind === "test" ? "keep" : "separate", "item");
    }
  });
  keys.register({
    keys: "2",
    label: "cut / merge",
    group,
    run() {
      const it = current();
      if (it) answer([it], it.kind === "test" ? "cut" : "merge", "item");
    }
  });
  if (!BLIND) {
    keys.register({
      keys: "a",
      label: "accept cull’s recommendation",
      group,
      run() {
        const it = current();
        if (!it) return;
        const lv = lean(it);
        if (lv === "review") return;
        answer(
          [it],
          it.kind === "test" ? lv : lv === "consolidate" ? "merge" : "separate",
          "item"
        );
      }
    });
  }
  keys.register({
    keys: "u",
    label: "unanswer",
    group,
    run() {
      const it = current();
      if (it) unanswer(it);
    }
  });
  keys.register({
    keys: "b",
    label: "back to the overview",
    group,
    run: () => ctx.back()
  });
  keys.register({
    keys: "n",
    label: "note",
    group,
    run() {
      document.querySelector(".kit-note")?.focus();
    }
  });
  const app = document.getElementById("app") ?? document.body;
  app.classList.add("kit-app");
  app.append(b.el, l.el, read);
  b.setLive("polling");
  window.addEventListener("hashchange", onHash);
  lastFrag = location.hash;
  route();
}
boot();
