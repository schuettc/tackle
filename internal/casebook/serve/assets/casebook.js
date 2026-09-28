// app.ts
import {
  bar,
  createApi,
  createKeys,
  live,
  initTheme,
  h
} from "/_kit/kit.js";

// router.ts
var listeners = [];
function parse(hash) {
  const path = hash.replace(/^#\//, "");
  const slash = path.indexOf("/");
  if (slash === -1) {
    return { section: path || "attention", sub: "" };
  }
  return { section: path.slice(0, slash), sub: path.slice(slash + 1) };
}
function go(section, sub) {
  location.hash = sub ? `#/${section}/${sub}` : `#/${section}`;
}
function onRoute(cb) {
  listeners.push(cb);
}
function fire() {
  const r = parse(location.hash);
  for (const cb of listeners) cb(r);
}
window.addEventListener("hashchange", fire);
function dispatchCurrent() {
  fire();
}

// app.ts
var sectionMakers = [];
var dockMakers = [];
function registerSection(make) {
  sectionMakers.push(make);
}
var liveListeners = /* @__PURE__ */ new Map();
function onLiveEvent(type, cb) {
  let list2 = liveListeners.get(type);
  if (!list2) {
    list2 = [];
    liveListeners.set(type, list2);
  }
  list2.push(cb);
}
function emitLive(type, data) {
  const list2 = liveListeners.get(type);
  if (list2) for (const cb of list2) cb(data);
}
var booted = false;
function boot() {
  if (booted) return;
  booted = true;
  const api = createApi({
    onStale(err) {
      void err;
      handle.setLive("stale");
    }
  });
  const mark = h(
    "svg",
    {
      viewBox: "0 0 64 64",
      fill: "none",
      xmlns: "http://www.w3.org/2000/svg",
      "aria-hidden": "true"
    },
    // ink tile
    h("rect", { width: "64", height: "64", rx: "14", fill: "#14161d" }),
    // open book outline
    h("path", {
      d: "M32 18C25 13 16 13 8 15V49C16 47 25 47 32 52C39 47 48 47 56 49V15C48 13 39 13 32 18Z",
      fill: "none",
      stroke: "#d98f66",
      "stroke-width": "5",
      "stroke-linejoin": "round"
    }),
    // spine
    h("path", { d: "M32 18V52", stroke: "#d98f66", "stroke-width": "4.4" }),
    // one neutral rule on the left page
    h("path", {
      d: "M15 29H25",
      stroke: "#9aa0ab",
      "stroke-width": "5",
      "stroke-linecap": "round"
    }),
    // tick on the right page (fully drawn — resting frame)
    h("path", {
      d: "M37 33L42 38L50 26",
      fill: "none",
      stroke: "#d98f66",
      "stroke-width": "6",
      "stroke-linecap": "round",
      "stroke-linejoin": "round"
    })
  );
  const handle = bar({
    brand: { name: "casebook", mark },
    sections: [
      { id: "attention", label: "attention" },
      { id: "rules", label: "rules" },
      { id: "apply", label: "to apply" }
    ],
    active: "attention",
    onSection(id) {
      go(id);
    },
    staleText: "restarted · continued in a new tab"
  });
  initTheme("casebook", handle.themeControl);
  const app = h("div", { class: "kit-app" });
  app.append(handle.el);
  let currentRoute = { section: "attention", sub: "" };
  const ctx = {
    api,
    bar: handle,
    keys: createKeys(),
    route: {
      current: () => currentRoute,
      go
    },
    on: onLiveEvent,
    setPrimary(p) {
      handle.setPrimary(p);
    }
  };
  const sections = /* @__PURE__ */ new Map();
  for (const make of sectionMakers) {
    const sec = make(ctx);
    sections.set(sec.id, sec);
    sec.list.hidden = true;
    sec.read.hidden = true;
    app.append(sec.list, sec.read);
  }
  if (sections.size === 0) {
    const list2 = h("div", { class: "kit-list" });
    const read = h("div", { class: "kit-read" });
    app.append(list2, read);
  }
  const rail = h("div", { class: "kit-rail" });
  const dockHandles = [];
  for (const make of dockMakers) {
    dockHandles.push(make(ctx));
  }
  if (dockHandles.length > 0) {
    for (const d of dockHandles) rail.append(d.el);
  }
  app.append(rail);
  document.body.append(app);
  const liveClient = live({
    events: "/api/events",
    poll: "/api/state",
    onEvent(e) {
      emitLive(e.type, e.data);
      const sec = sections.get(currentRoute.section);
      if (sec) sec.onLive(e.type, e.data);
    },
    onStatus(s) {
      handle.setLive(s);
    },
    fetch: (url, init) => fetch(url, init)
  });
  onRoute((r) => {
    currentRoute = r;
    const sectionId = r.section === "rules" ? "rules" : r.section === "apply" ? "apply" : "attention";
    handle.setSection(sectionId);
    for (const [id, sec] of sections) {
      const active = id === sectionId;
      sec.list.hidden = !active;
      sec.read.hidden = !active;
      if (active) sec.show(r.sub);
    }
  });
  void api.get("/summary").then((s) => {
    const counts = s.counts ?? {};
    handle.setCount("attention", counts["all"] ?? 0);
    handle.setCount("rules", counts["rules"] ?? 0);
    handle.setCount("apply", counts["apply"] ?? 0);
    const minsAgo = s.synced_at ? Math.round((Date.now() - new Date(s.synced_at).getTime()) / 6e4) : null;
    const statusText = minsAgo !== null ? `synced ${minsAgo}m ago · ${s.machine}` : s.machine;
    handle.setStatus(
      s.offline_queued > 0 ? `offline · ${s.offline_queued} queued` : statusText,
      { tone: s.offline_queued > 0 ? "danger" : "muted" }
    );
  }).catch(() => {
  });
  onLiveEvent("index", () => {
    void api.get("/summary").then((s) => {
      const counts = s.counts ?? {};
      handle.setCount("attention", counts["all"] ?? 0);
      handle.setCount("rules", counts["rules"] ?? 0);
      handle.setCount("apply", counts["apply"] ?? 0);
    }).catch(() => {
    });
  });
  dispatchCurrent();
  void liveClient;
}

// attention.ts
import {
  list,
  createSelection,
  h as h4
} from "/_kit/kit.js";

// item.ts
import { h as h3, facts, card, fold } from "/_kit/kit.js";

// decide.ts
import {
  sheet,
  h as h2,
  noteField
} from "/_kit/kit.js";
var KIND_ALLOWED = {
  repo: ["keep", "archive", "delete", "wait", "watch", "ignore"],
  pr: ["keep", "close", "merge", "wait", "watch", "ignore"],
  issue: ["keep", "close", "wait", "watch", "ignore"],
  branch: ["keep", "delete", "wait", "watch", "ignore"],
  worktree: ["keep", "delete", "wait", "ignore"]
};
var FALLBACK_ALLOWED = ["keep", "close", "wait", "watch", "ignore"];
var NEEDS_UNTIL = /* @__PURE__ */ new Set(["wait", "watch"]);
var DANGER_DISPS = /* @__PURE__ */ new Set(["close", "delete", "archive"]);
function allowedForKind(kind) {
  return KIND_ALLOWED[kind] ?? FALLBACK_ALLOWED;
}
function allowedForItems(items) {
  if (items.length === 0) return FALLBACK_ALLOWED;
  const sets = items.map((it) => new Set(allowedForKind(it.kind)));
  const first = [...sets[0] ?? new Set(FALLBACK_ALLOWED)];
  return first.filter((d) => sets.every((s) => s.has(d)));
}
function validateUntil(s) {
  const v = s.trim();
  if (!v) {
    return "until is required for wait/watch — e.g. date(2026-12-01) or inactive(90d)";
  }
  const open = v.indexOf("(");
  if (open <= 0 || !v.endsWith(")")) {
    return "until must be one of: date(YYYY-MM-DD), merged(<pr>), closed(<pr|issue>), inactive(90d), released(<repo>)";
  }
  const op = v.slice(0, open);
  const arg = v.slice(open + 1, v.length - 1).trim();
  if (!["date", "merged", "closed", "inactive", "released"].includes(op)) {
    return `unknown until op "${op}": use date, merged, closed, inactive, or released`;
  }
  if (op === "date" && !/^\d{4}-\d{2}-\d{2}$/.test(arg)) {
    return "date must be YYYY-MM-DD";
  }
  return null;
}
function openDecideSheet(ctx, keys, allowed, onDone) {
  const n = keys.length;
  let disposition = "";
  let until = "";
  let note = "";
  let submitting = false;
  let sh = null;
  const previewEl = h2("p", { class: "cb-sheet-preview" });
  previewEl.textContent = `${n} item${n === 1 ? "" : "s"}`;
  function updatePreview() {
    previewEl.textContent = `${disposition || "…"} ${n} item${n === 1 ? "" : "s"}`;
  }
  const untilInputEl = h2("input", {
    type: "text",
    class: "cb-sheet-input",
    placeholder: "date(YYYY-MM-DD), merged(<pr>), inactive(90d) …"
  });
  untilInputEl.addEventListener("input", () => {
    until = untilInputEl.value;
  });
  const untilRow = h2(
    "div",
    { class: "cb-sheet-row" },
    h2("label", { class: "cb-sheet-label" }, "until"),
    untilInputEl
  );
  untilRow.hidden = true;
  const noteInputEl = noteField({
    placeholder: "optional note",
    onCommit(v) {
      note = v;
    }
  });
  const noteRow = h2(
    "div",
    { class: "cb-sheet-row" },
    h2("label", { class: "cb-sheet-label" }, "note"),
    noteInputEl
  );
  const errEl = h2("p", { class: "cb-sheet-err" });
  errEl.hidden = true;
  const dispContainer = h2("div", { class: "cb-sheet-disps" });
  for (const d of allowed) {
    const btn = h2(
      "button",
      {
        type: "button",
        class: "cb-sheet-disp" + (DANGER_DISPS.has(d) ? " cb-sheet-disp--danger" : ""),
        onclick() {
          disposition = d;
          updatePreview();
          untilRow.hidden = !NEEDS_UNTIL.has(d);
          errEl.hidden = true;
          for (const el of dispContainer.querySelectorAll(".cb-sheet-disp")) {
            el.classList.toggle("on", el === btn);
          }
        }
      },
      d
    );
    dispContainer.append(btn);
  }
  const body = h2(
    "div",
    { class: "cb-sheet-body" },
    dispContainer,
    untilRow,
    noteRow,
    previewEl,
    errEl
  );
  async function doDecide() {
    if (submitting) return;
    if (!disposition) {
      errEl.textContent = "select a disposition";
      errEl.hidden = false;
      return;
    }
    if (NEEDS_UNTIL.has(disposition)) {
      const currentUntil = untilInputEl.value.trim();
      const err = validateUntil(currentUntil);
      if (err) {
        errEl.textContent = err;
        errEl.hidden = false;
        return;
      }
      until = currentUntil;
    } else {
      until = "";
    }
    submitting = true;
    errEl.hidden = true;
    try {
      const payload = { keys, disposition };
      if (until) payload["until"] = until;
      if (note) payload["note"] = note;
      const result = await ctx.api.post("/decide", payload);
      if (result.errors && result.errors.length > 0) {
        errEl.textContent = result.errors.join("; ");
        errEl.hidden = false;
        submitting = false;
        return;
      }
      sh?.close();
      onDone(keys);
    } catch (err) {
      errEl.textContent = err instanceof Error ? err.message : "decide failed — try again";
      errEl.hidden = false;
      submitting = false;
    }
  }
  sh = sheet({
    title: `decide ${n} item${n === 1 ? "" : "s"}`,
    body,
    actions: [
      {
        label: `Decide ${n}`,
        fill: true,
        run() {
          void doDecide();
        }
      }
    ],
    onClose() {
      sh = null;
    }
  });
}
function wireSelection(ctx, listHandle) {
  function openSheetForSelection() {
    const keys = listHandle.selectedIds();
    if (keys.length === 0) return;
    const selItems = listHandle.selected();
    const allowed = allowedForItems(selItems);
    openDecideSheet(ctx, keys, allowed, (decided) => {
      listHandle.deselect(decided);
    });
  }
  listHandle.selection.onChange((ids) => {
    const countEl = listHandle.el.querySelector(".cb-sel-count");
    if (countEl instanceof HTMLElement) {
      if (ids.length > 0) {
        countEl.textContent = `${ids.length} selected`;
        countEl.hidden = false;
      } else {
        countEl.hidden = true;
      }
    }
    if (ids.length === 0) {
      ctx.setPrimary(null);
    } else {
      ctx.setPrimary({
        label: `Decide ${ids.length}`,
        run: openSheetForSelection
      });
    }
  });
  try {
    ctx.keys.register({
      keys: "d",
      label: "decide selection",
      group: "page",
      run() {
        openSheetForSelection();
      }
    });
  } catch {
  }
}

// item.ts
var BODY_CAP = 600;
function fmtDate(s) {
  if (!s) return "—";
  const d = new Date(s);
  return d.toLocaleDateString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric"
  });
}
function fmtTime(s) {
  const d = new Date(s);
  return d.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit"
  });
}
function renderEvidence(evs) {
  const section = h3("section", { class: "cb-evidence" });
  section.append(h3("h2", { class: "cb-section-label" }, "evidence"));
  if (evs.length === 0) {
    section.append(h3("p", { class: "cb-empty" }, "no evidence"));
    return section;
  }
  for (const ev of evs) {
    const by = ev.author ? h3("span", { class: "cb-muted" }, ` by ${ev.author}`) : null;
    const time = h3(
      "span",
      { class: "cb-muted" },
      ` · ${fmtDate(ev.created_at)}`
    );
    const item = h3(
      "div",
      { class: "cb-evidence-item" },
      h3("p", { class: "cb-evidence-text" }, ev.text),
      h3("p", { class: "cb-evidence-meta" }, ...by ? [by] : [], time)
    );
    section.append(item);
  }
  return section;
}
function renderHistory(events, decisions) {
  const section = h3("section", { class: "cb-history" });
  section.append(h3("h2", { class: "cb-section-label" }, "history"));
  if (events.length === 0 && decisions.length === 0) {
    section.append(h3("p", { class: "cb-empty" }, "no history"));
    return section;
  }
  for (const entry of decisions) {
    section.append(
      h3(
        "div",
        { class: "cb-history-item cb-history-decision" },
        h3("span", { class: "cb-history-time" }, fmtDate(entry.Time)),
        h3("span", { class: "cb-history-msg" }, entry.Subject)
      )
    );
  }
  for (const ev of events) {
    const label = ev.actions && ev.actions.length > 0 ? ev.actions.map((a) => a.hook ?? "").filter(Boolean).join(", ") : ev.hook ?? ev.src;
    section.append(
      h3(
        "div",
        { class: "cb-history-item" },
        h3("span", { class: "cb-history-time" }, fmtTime(ev.ts)),
        h3("span", { class: "cb-history-msg" }, label)
      )
    );
  }
  return section;
}
function renderProposalCard(ctx, p, key, kind) {
  const stateLabel = p.state === "pending" ? "pending proposal" : `proposal · ${p.state}`;
  const lines = [
    h3(
      "div",
      { class: "cb-proposal-detail" },
      h3("span", { class: "cb-label" }, "disposition "),
      h3("strong", null, p.disposition),
      p.until ? h3("span", null, ` until ${fmtDate(p.until)}`) : null,
      p.note ? h3("span", null, ` · ${p.note}`) : null
    )
  ];
  const bodyEl = h3("div", { class: "cb-proposal-body" }, ...lines);
  const cardActions = [
    {
      label: "accept",
      fill: true,
      run() {
        void ctx.api.post("/proposals/accept", { ids: [p.id] }).catch(() => {
        });
      }
    },
    {
      label: "change…",
      run() {
        openDecideSheet(ctx, [key], allowedForKind(kind), () => {
        });
      }
    },
    {
      label: "reject",
      danger: true,
      run() {
        void ctx.api.post("/proposals/reject", { ids: [p.id] }).catch(() => {
        });
      }
    }
  ];
  return card({
    edge: "agent",
    head: stateLabel,
    body: bodyEl,
    actions: cardActions
  });
}
var DANGER_DISPS_ITEM = /* @__PURE__ */ new Set(["close", "delete", "archive"]);
var NEEDS_ELLIPSIS = /* @__PURE__ */ new Set(["wait", "watch"]);
function renderDecideSection(ctx, key, kind) {
  const section = h3("section", { class: "cb-decide" });
  section.append(h3("h2", { class: "cb-section-label" }, "decide"));
  const allowed = allowedForKind(kind);
  const dispRow = h3("div", { class: "cb-decide-btns" });
  for (const d of allowed) {
    const label = NEEDS_ELLIPSIS.has(d) ? `${d}…` : d;
    dispRow.append(
      h3(
        "button",
        {
          type: "button",
          class: "cb-sheet-disp" + (DANGER_DISPS_ITEM.has(d) ? " cb-sheet-disp--danger" : ""),
          onclick() {
            openDecideSheet(ctx, [key], allowed, () => {
            });
          }
        },
        label
      )
    );
  }
  section.append(dispRow);
  return section;
}
function renderItem(ctx, detail) {
  const it = detail.item;
  const el = h3("article", { class: "cb-item" });
  const kickerParts = [it.kind, it.key, it.relation].filter(Boolean).join(" · ");
  el.append(h3("p", { class: "cb-kicker" }, kickerParts));
  el.append(h3("h1", { class: "cb-title" }, it.title ?? it.key));
  const factPairs = [];
  if (it.repo) factPairs.push(["repo", it.repo]);
  if (it.status) factPairs.push(["status", it.status]);
  if (it.author)
    factPairs.push(["author", it.author + (it.author_is_bot ? " (bot)" : "")]);
  if (it.created_at) factPairs.push(["opened", fmtDate(it.created_at)]);
  if (it.updated_at) factPairs.push(["updated", fmtDate(it.updated_at)]);
  if (it.labels && it.labels.length > 0)
    factPairs.push(["labels", it.labels.join(", ")]);
  if (it.landed) factPairs.push(["landed", it.landed_how ?? it.landed]);
  el.append(facts(factPairs));
  if (it.body) {
    const excerpt = it.body.slice(0, BODY_CAP);
    const rest = it.body.slice(BODY_CAP);
    const bodyWrap = h3("div", { class: "cb-body" });
    bodyWrap.append(h3("p", null, excerpt));
    if (rest) {
      bodyWrap.append(fold("read more", h3("p", null, rest)));
    }
    el.append(bodyWrap);
  }
  if (it.proposal) {
    el.append(renderProposalCard(ctx, it.proposal, it.key, it.kind));
  }
  el.append(renderDecideSection(ctx, it.key, it.kind));
  el.append(renderEvidence(detail.evidence ?? []));
  el.append(renderHistory(detail.history ?? [], detail.decisions ?? []));
  return el;
}

// attention.ts
var PAGE_SIZE = 200;
var selection = createSelection();
var VIEWS = [
  { id: "waiting", label: "waiting on you" },
  { id: "new", label: "new" },
  { id: "due", label: "due" },
  { id: "proposed", label: "proposed" },
  { id: "board", label: "▦ board" }
];
var FILTER_CHIPS = [
  { id: "kind", label: "kind" },
  { id: "repo", label: "repo" },
  { id: "relation", label: "relation" },
  { id: "bot", label: "bot/human" },
  { id: "age", label: "age" },
  { id: "rule", label: "rule" }
];
function emptyFilters() {
  return {
    view: "waiting",
    kind: "",
    repo: "",
    relation: "",
    bot: "",
    age: "",
    rule: "",
    q: ""
  };
}
function getUrlQ() {
  return new URLSearchParams(location.search).get("q") ?? "";
}
function setUrlQ(q) {
  const p = new URLSearchParams(location.search);
  if (q) {
    p.set("q", q);
  } else {
    p.delete("q");
  }
  const qs = p.toString();
  const newUrl = location.pathname + (qs ? "?" + qs : "") + location.hash;
  history.replaceState(null, "", newUrl);
}
function ageOf(it) {
  if (!it.created_at) return "";
  const ms = Date.now() - new Date(it.created_at).getTime();
  const days = Math.floor(ms / 864e5);
  if (days === 0) return "today";
  if (days === 1) return "1d";
  return `${days}d`;
}
function makeAttention(ctx) {
  const readEl = h4("div", { class: "kit-read" });
  let offset = 0;
  let totalItems = 0;
  let loadedItems = [];
  const filters = emptyFilters();
  let loading = false;
  let footEl = null;
  let searchDebounceTimer = null;
  let totalItemsForView = 0;
  function buildFoot() {
    const selCount = h4(
      "span",
      { class: "cb-sel-count", hidden: true },
      "0 selected"
    );
    const selAllBtn = h4(
      "button",
      {
        class: "cb-sel-all",
        onclick() {
          void selectAllInView();
        }
      },
      "select all in view"
    );
    footEl = h4(
      "div",
      { class: "cb-foot" },
      selCount,
      selAllBtn,
      h4(
        "button",
        {
          class: "cb-foot-more",
          onclick() {
            void loadMore();
          }
        },
        `show ${PAGE_SIZE} more`
      )
    );
    return footEl;
  }
  async function selectAllInView() {
    let allIds = loadedItems.map((it) => it.key);
    if (totalItemsForView > loadedItems.length) {
      try {
        const data = await ctx.api.get("/items", {
          ...buildQuery(0),
          limit: String(totalItemsForView)
        });
        allIds = (data.items ?? []).map((it) => it.key);
      } catch {
      }
    }
    handle.selectAll(allIds);
  }
  const handle = list({
    label: "attention",
    views: VIEWS.map((v) => ({ ...v, on: v.id === filters.view })),
    filters: FILTER_CHIPS,
    selection,
    openOnMove: false,
    search: {
      placeholder: "search by key or title",
      onInput(text) {
        if (searchDebounceTimer !== null) clearTimeout(searchDebounceTimer);
        searchDebounceTimer = setTimeout(() => {
          searchDebounceTimer = null;
          filters.q = text;
          setUrlQ(text);
          void reload();
        }, 200);
      }
    },
    row(it) {
      const kindKey = `${it.kind} · ${it.key}`;
      const age = ageOf(it);
      const proposal = it.proposal ? `${it.proposal.disposition} proposed` : void 0;
      return {
        id: it.key,
        key: kindKey,
        title: it.title ?? it.key,
        meta: age,
        sub: proposal,
        selectable: true
      };
    },
    onChip(group, id) {
      if (group === "view") {
        if (id === "board") {
          ctx.route.go("attention", "board");
          return;
        }
        filters.view = id;
        ctx.route.go("attention", id);
        void reload();
      } else if (group === "filter") {
        cycleFilter(id);
        void reload();
      }
    },
    onOpen(it) {
      ctx.route.go("item", it.key);
      void openDetail(it.key);
    },
    // onSelect: wireSelection handles the primary button via selection.onChange().
    foot: buildFoot()
  });
  const filterCycles = {
    relation: ["", "incoming", "outgoing", "own"],
    bot: ["", "bot", "human"]
  };
  function cycleFilter(id) {
    const cycle = filterCycles[id];
    if (cycle) {
      const cur = filters[id] ?? "";
      const i = cycle.indexOf(cur);
      filters[id] = cycle[(i + 1) % cycle.length];
    }
  }
  function updateFilterChips() {
    handle.setChips(
      "filter",
      FILTER_CHIPS.map((c) => ({
        ...c,
        on: Boolean(filters[c.id]),
        label: filters[c.id] ? `${c.label}: ${filters[c.id]}` : c.label
      }))
    );
  }
  function buildQuery(pageOffset) {
    const p = {
      view: filters.view,
      offset: String(pageOffset),
      limit: String(PAGE_SIZE)
    };
    if (filters.kind) p["kind"] = filters.kind;
    if (filters.repo) p["repo"] = filters.repo;
    if (filters.relation) p["relation"] = filters.relation;
    if (filters.bot) p["bot"] = filters.bot;
    if (filters.age) p["age"] = filters.age;
    if (filters.rule) p["rule"] = filters.rule;
    if (filters.q) p["q"] = filters.q;
    return p;
  }
  async function reload() {
    if (loading) return;
    loading = true;
    try {
      offset = 0;
      loadedItems = [];
      updateFilterChips();
      const data = await ctx.api.get("/items", buildQuery(0));
      totalItems = data.total;
      totalItemsForView = data.total;
      loadedItems = data.items ?? [];
      offset = loadedItems.length;
      handle.setItems(loadedItems);
      updateFoot();
    } catch {
    } finally {
      loading = false;
    }
  }
  async function loadMore() {
    if (loading || offset >= totalItems) return;
    loading = true;
    try {
      const data = await ctx.api.get("/items", buildQuery(offset));
      const next = data.items ?? [];
      loadedItems = [...loadedItems, ...next];
      offset = loadedItems.length;
      totalItems = data.total;
      totalItemsForView = data.total;
      handle.setItems(loadedItems);
      updateFoot();
    } catch {
    } finally {
      loading = false;
    }
  }
  function updateFoot() {
    if (!footEl) return;
    const btn = footEl.querySelector(".cb-foot-more");
    if (!btn) return;
    const remaining = totalItems - offset;
    if (remaining > 0) {
      btn.textContent = `show ${Math.min(PAGE_SIZE, remaining)} more`;
      footEl.hidden = false;
    } else {
      footEl.hidden = true;
    }
  }
  async function openDetail(key) {
    try {
      const detail = await ctx.api.get("/item", { key });
      const el = renderItem(ctx, detail);
      readEl.replaceChildren(el);
    } catch {
    }
  }
  function applyCounts(counts) {
    if (!counts) return;
    handle.setChips(
      "view",
      VIEWS.map((v) => ({
        ...v,
        on: v.id === filters.view,
        count: counts[v.id] ?? void 0
      }))
    );
  }
  {
    const urlQ = getUrlQ();
    if (urlQ) {
      filters.q = urlQ;
      handle.setSearch(urlQ);
    }
  }
  void reload();
  wireSelection(ctx, handle);
  if (typeof handle.focusSearch === "function") {
    const focusFn = handle.focusSearch.bind(handle);
    try {
      ctx.keys.register({
        keys: "/",
        label: "search",
        group: "family",
        run() {
          focusFn();
        }
      });
    } catch {
    }
  }
  void ctx.api.get("/summary").then((s) => {
    applyCounts(s.counts);
  }).catch(() => {
  });
  function show(sub) {
    if (sub === "board") {
      return;
    }
    const urlQ = getUrlQ();
    if (urlQ !== filters.q) {
      filters.q = urlQ;
      handle.setSearch(urlQ);
    }
    const view = sub || "waiting";
    if (VIEWS.some((v) => v.id === view) && view !== "board") {
      filters.view = view;
      handle.setChips(
        "view",
        VIEWS.map((v) => ({
          ...v,
          on: v.id === filters.view
        }))
      );
      void reload();
    } else if (sub) {
      void openDetail(sub);
      void reload();
    }
  }
  return {
    id: "attention",
    list: handle.el,
    read: readEl,
    show,
    onLive(type, data) {
      if (type === "index") {
        const s = data;
        applyCounts(s?.counts ?? null);
        void reload();
      } else if (type === "decided" || type === "proposals") {
        void reload();
      }
    },
    primary() {
      return null;
    }
  };
}

// entry.ts
registerSection(makeAttention);
boot();
