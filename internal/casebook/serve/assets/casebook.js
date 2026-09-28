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
function safeDecode(s) {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}
function pathEscape(s) {
  return encodeURIComponent(s).replace(/%3A/gi, ":").replace(/%40/gi, "@");
}
function parse(hash) {
  const path = hash.replace(/^#\//, "");
  const slash = path.indexOf("/");
  if (slash === -1) {
    return { section: path || "attention", sub: "" };
  }
  return {
    section: path.slice(0, slash),
    sub: safeDecode(path.slice(slash + 1))
  };
}
function go(section, sub) {
  location.hash = sub ? `#/${section}/${pathEscape(sub)}` : `#/${section}`;
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
  h as h5
} from "/_kit/kit.js";

// item.ts
import { h as h3, facts, card, fold } from "/_kit/kit.js";

// decide.ts
import {
  sheet,
  h as h2,
  noteField
} from "/_kit/kit.js";

// decide-math.ts
function kindFromKey(key) {
  const i = key.indexOf(":");
  return i > 0 ? key.slice(0, i) : "";
}
function allowedForKind(vocab, kind) {
  return (vocab.kinds ?? []).find((k) => k.kind === kind)?.allowed ?? [];
}
function allowedForKeys(vocab, keys) {
  if (keys.length === 0) return [];
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  if (kinds.length === 0) return [];
  const sets = kinds.map((k) => new Set(allowedForKind(vocab, k)));
  const first = [...sets[0] ?? /* @__PURE__ */ new Set()];
  return first.filter((d) => sets.every((s) => s.has(d)));
}

// decide.ts
var _vocab = null;
async function getVocab(ctx) {
  if (_vocab !== null) return _vocab;
  _vocab = await ctx.api.get("/decisions/vocabulary");
  return _vocab;
}
function dispositionNeedsUntil(vocab, keys, disp) {
  const selectedKinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  return (vocab.kinds ?? []).filter((k) => selectedKinds.includes(k.kind)).some((k) => (k.needs_until ?? []).includes(disp));
}
var DANGER_DISPS = /* @__PURE__ */ new Set(["close", "delete", "archive"]);
function openDecideSheet(ctx, keys, onDone) {
  void getVocab(ctx).then((vocab) => {
    openDecideSheetWithVocab(ctx, keys, vocab, onDone);
  });
}
function openDecideSheetWithVocab(ctx, keys, vocab, onDone) {
  const n = keys.length;
  const allowed = allowedForKeys(vocab, keys);
  let disposition = "";
  let until = "";
  let note = "";
  let submitting = false;
  let sh = null;
  let dryRunTimer = null;
  let lastDryRunError = null;
  const previewEl = h2("p", { class: "cb-sheet-preview" });
  previewEl.textContent = `${n} item${n === 1 ? "" : "s"}`;
  function updatePreview() {
    previewEl.textContent = `${disposition || "…"} ${n} item${n === 1 ? "" : "s"}`;
  }
  const untilInputEl = h2("input", {
    type: "text",
    class: "cb-sheet-input",
    placeholder: (vocab.until_forms ?? []).map((f) => f.syntax).join(", ") || "date(YYYY-MM-DD), inactive(90d) …"
  });
  const untilRow = h2(
    "div",
    { class: "cb-sheet-row" },
    h2("label", { class: "cb-sheet-label" }, "until"),
    untilInputEl
  );
  untilRow.hidden = true;
  const errEl = h2("p", { class: "cb-sheet-err" });
  errEl.hidden = true;
  async function runDryRun() {
    if (!disposition) return;
    if (!dispositionNeedsUntil(vocab, keys, disposition)) return;
    const currentUntil = untilInputEl.value.trim();
    if (!currentUntil) {
      lastDryRunError = `until is required for ${disposition}`;
      errEl.textContent = lastDryRunError;
      errEl.hidden = false;
      return;
    }
    try {
      const result = await ctx.api.post("/decide", {
        keys,
        disposition,
        until: currentUntil,
        dry_run: true
      });
      if (result.errors && result.errors.length > 0) {
        lastDryRunError = result.errors.join("; ");
        errEl.textContent = lastDryRunError;
        errEl.hidden = false;
      } else {
        lastDryRunError = null;
        errEl.hidden = true;
      }
    } catch {
    }
  }
  untilInputEl.addEventListener("input", () => {
    until = untilInputEl.value;
    lastDryRunError = null;
    errEl.hidden = true;
    if (dryRunTimer !== null) clearTimeout(dryRunTimer);
    dryRunTimer = setTimeout(() => void runDryRun(), 400);
  });
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
          const needsUntil = dispositionNeedsUntil(vocab, keys, d);
          untilRow.hidden = !needsUntil;
          errEl.hidden = true;
          lastDryRunError = null;
          if (dryRunTimer !== null) clearTimeout(dryRunTimer);
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
    if (dispositionNeedsUntil(vocab, keys, disposition)) {
      const currentUntil = untilInputEl.value.trim();
      if (!currentUntil) {
        errEl.textContent = `until is required for ${disposition}`;
        errEl.hidden = false;
        return;
      }
      if (dryRunTimer !== null) clearTimeout(dryRunTimer);
      await runDryRun();
      if (lastDryRunError) {
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
      const decidedKeys = result.decided_keys ?? [];
      if (decidedKeys.length > 0) {
        onDone(decidedKeys);
      }
      if (result.errors && result.errors.length > 0) {
        errEl.textContent = result.errors.join("; ");
        errEl.hidden = false;
        submitting = false;
      } else {
        sh?.close();
      }
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
      if (dryRunTimer !== null) clearTimeout(dryRunTimer);
    }
  });
}
function wireSelection(ctx, listHandle) {
  function openSheetForSelection() {
    const keys = listHandle.selectedIds();
    if (keys.length === 0) return;
    openDecideSheet(ctx, keys, (decided) => {
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
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!msg.startsWith("key clash:")) {
      throw err;
    }
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
function renderProposalCard(ctx, p, key) {
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
        openDecideSheet(ctx, [key], () => {
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
function renderDecideSection(ctx, key, kind) {
  const section = h3("section", { class: "cb-decide" });
  section.append(h3("h2", { class: "cb-section-label" }, "decide"));
  const dispRow = h3("div", { class: "cb-decide-btns" });
  section.append(dispRow);
  void getVocab(ctx).then((vocab) => {
    const vocabKind = (vocab.kinds ?? []).find((k) => k.kind === kind);
    const kindAllowed = allowedForKind(vocab, kind);
    const needsUntilSet = new Set(vocabKind?.needs_until ?? []);
    for (const d of kindAllowed) {
      const label = needsUntilSet.has(d) ? `${d}…` : d;
      dispRow.append(
        h3(
          "button",
          {
            type: "button",
            class: "cb-sheet-disp" + (DANGER_DISPS.has(d) ? " cb-sheet-disp--danger" : ""),
            onclick() {
              openDecideSheet(ctx, [key], () => {
              });
            }
          },
          label
        )
      );
    }
  });
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
    el.append(renderProposalCard(ctx, it.proposal, it.key));
  }
  el.append(renderDecideSection(ctx, it.key, it.kind));
  el.append(renderEvidence(detail.evidence ?? []));
  el.append(renderHistory(detail.history ?? [], detail.decisions ?? []));
  return el;
}

// board.ts
import { h as h4 } from "/_kit/kit.js";
var PAGE_SIZE = 200;
var LANES = [
  { id: "waiting", label: "waiting on you" },
  { id: "proposed", label: "proposed" },
  { id: "due", label: "due" },
  { id: "new", label: "new" }
];
function ageOf(it) {
  if (!it.created_at) return "";
  const ms = Date.now() - new Date(it.created_at).getTime();
  const days = Math.floor(ms / 864e5);
  if (days === 0) return "today";
  if (days === 1) return "1d";
  return `${days}d`;
}
function makeBoard(ctx, sel, filters, onOpen, onRefresh) {
  const el = h4("div", { class: "cb-board" });
  const laneState = new Map(
    LANES.map(({ id }) => [id, { items: [], total: 0 }])
  );
  const laneRowsEl = /* @__PURE__ */ new Map();
  const laneMoreEl = /* @__PURE__ */ new Map();
  for (const { id, label } of LANES) {
    const headEl = h4("div", { class: "cb-lane-head" });
    headEl.textContent = label;
    const rowsEl = h4("div", { class: "cb-lane-rows" });
    laneRowsEl.set(id, rowsEl);
    const moreEl = h4("button", { class: "cb-lane-more", hidden: true });
    moreEl.textContent = "show more";
    moreEl.addEventListener("click", () => {
      void loadMore(id);
    });
    laneMoreEl.set(id, moreEl);
    el.append(
      h4("div", { class: "cb-lane", "data-lane": id }, headEl, rowsEl, moreEl)
    );
  }
  function buildCard(it, laneId) {
    const selected = sel.has(it.key);
    const box = h4("span", { class: "kit-box" + (selected ? " on" : "") });
    const kk = h4("div", { class: "cb-card-kk" });
    kk.textContent = `${it.kind} · ${it.key}`;
    const titleEl = h4("div", { class: "cb-card-title" });
    titleEl.textContent = it.title ?? it.key;
    const card2 = h4(
      "div",
      {
        class: "cb-board-card" + (selected ? " on" : ""),
        tabindex: "0",
        "data-id": it.key
      },
      h4("div", { class: "cb-card-head" }, box, kk),
      titleEl
    );
    const age = ageOf(it);
    if (age) {
      const ageEl = h4("div", { class: "cb-card-age" });
      ageEl.textContent = age;
      card2.append(ageEl);
    }
    if (it.proposal) {
      const propEl = h4("div", { class: "cb-card-prop" });
      propEl.textContent = `${it.proposal.disposition} proposed`;
      card2.append(propEl);
    }
    titleEl.addEventListener("click", (e) => {
      e.stopPropagation();
      onOpen?.(it.key, laneId);
      ctx.route.go("item", it.key);
    });
    card2.addEventListener("click", (e) => {
      const state = laneState.get(laneId);
      const orderedIds = state?.items.map((i) => i.key) ?? [];
      if (e.shiftKey) {
        const anchor = sel.anchor();
        if (anchor !== null) {
          sel.range(anchor, it.key, orderedIds);
        } else {
          sel.toggle(it.key);
        }
      } else {
        sel.toggle(it.key);
      }
    });
    card2.addEventListener("keydown", (e) => {
      if (e.key === "o" || e.key === "Enter") {
        e.preventDefault();
        onOpen?.(it.key, laneId);
        ctx.route.go("item", it.key);
      }
    });
    return card2;
  }
  function repaintLane(laneId) {
    const rowsEl = laneRowsEl.get(laneId);
    const moreEl = laneMoreEl.get(laneId);
    if (!rowsEl || !moreEl) return;
    const state = laneState.get(laneId);
    rowsEl.replaceChildren(...state.items.map((it) => buildCard(it, laneId)));
    const remaining = state.total - state.items.length;
    if (remaining > 0) {
      moreEl.textContent = `show ${Math.min(PAGE_SIZE, remaining)} more`;
      moreEl.hidden = false;
    } else {
      moreEl.hidden = true;
    }
  }
  function repaintSelection() {
    for (const { id: laneId } of LANES) {
      const rowsEl = laneRowsEl.get(laneId);
      if (!rowsEl) continue;
      const state = laneState.get(laneId);
      const cards = rowsEl.querySelectorAll(".cb-board-card");
      cards.forEach((card2, i) => {
        const it = state.items[i];
        if (!it) return;
        const selected = sel.has(it.key);
        card2.classList.toggle("on", selected);
        card2.querySelector(".kit-box")?.classList.toggle("on", selected);
      });
    }
  }
  async function fetchLane(laneId) {
    const q = {
      ...filters(),
      view: laneId,
      offset: "0",
      limit: String(PAGE_SIZE)
    };
    const data = await ctx.api.get("/items", q);
    return { items: data.items ?? [], total: data.total };
  }
  let boardTotalUnique = 0;
  async function refresh() {
    const results = await Promise.allSettled(
      LANES.map(({ id }) => fetchLane(id))
    );
    const seenIds = /* @__PURE__ */ new Set();
    for (let i = 0; i < LANES.length; i++) {
      const { id } = LANES[i];
      const result = results[i];
      if (result.status === "fulfilled") {
        const { items, total } = result.value;
        const unique = items.filter((it) => !seenIds.has(it.key));
        unique.forEach((it) => seenIds.add(it.key));
        const adjustedTotal = items.length >= total ? unique.length : total;
        laneState.set(id, { items: unique, total: adjustedTotal });
      }
      repaintLane(id);
    }
    let uniqueCount = 0;
    for (const { id } of LANES) {
      uniqueCount += laneState.get(id)?.items.length ?? 0;
    }
    boardTotalUnique = uniqueCount;
    onRefresh?.(boardTotalUnique);
  }
  async function loadMore(laneId) {
    const state = laneState.get(laneId);
    if (state.items.length >= state.total) return;
    const q = {
      ...filters(),
      view: laneId,
      offset: String(state.items.length),
      limit: String(PAGE_SIZE)
    };
    try {
      const data = await ctx.api.get("/items", q);
      const existingKeys = new Set(
        LANES.flatMap(
          ({ id }) => id === laneId ? [] : laneState.get(id)?.items.map((it) => it.key) ?? []
        )
      );
      const more = (data.items ?? []).filter((it) => !existingKeys.has(it.key));
      laneState.set(laneId, {
        items: [...state.items, ...more],
        total: data.total
      });
      repaintLane(laneId);
    } catch {
    }
  }
  const unsubSel = sel.onChange(() => {
    repaintSelection();
  });
  void refresh();
  function destroy() {
    unsubSel();
  }
  function totalItems() {
    return boardTotalUnique;
  }
  function allKeys() {
    return LANES.flatMap(
      ({ id }) => laneState.get(id)?.items.map((it) => it.key) ?? []
    );
  }
  return { el, refresh, destroy, totalItems, allKeys };
}

// attention.ts
var PAGE_SIZE2 = 200;
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
function ageOf2(it) {
  if (!it.created_at) return "";
  const ms = Date.now() - new Date(it.created_at).getTime();
  const days = Math.floor(ms / 864e5);
  if (days === 0) return "today";
  if (days === 1) return "1d";
  return `${days}d`;
}
function makeAttention(ctx) {
  const readEl = h5("div", { class: "kit-read" });
  let offset = 0;
  let totalItems = 0;
  let loadedItems = [];
  const filters = emptyFilters();
  let loading = false;
  let footEl = null;
  let searchDebounceTimer = null;
  let totalItemsForView = 0;
  let boardHandle = null;
  function renderReadEmpty() {
    const nameEl = h5("p", { class: "cb-read-empty-section" }, "attention");
    const countEl = h5(
      "p",
      { class: "cb-read-empty-count" },
      `${totalItemsForView} items`
    );
    const promptEl = h5(
      "p",
      { class: "cb-read-empty-prompt" },
      "Select an item to see it here."
    );
    return h5("div", { class: "cb-read-empty" }, nameEl, countEl, promptEl);
  }
  function updateReadEmptyCount() {
    const countEl = readEl.querySelector(".cb-read-empty-count");
    if (countEl) {
      countEl.textContent = `${totalItemsForView} items`;
    }
  }
  function showReadEmpty() {
    readEl.replaceChildren(renderReadEmpty());
  }
  showReadEmpty();
  function buildFoot() {
    const selCount = h5(
      "span",
      { class: "cb-sel-count", hidden: true },
      "0 selected"
    );
    const selAllBtn = h5(
      "button",
      {
        class: "cb-sel-all",
        onclick() {
          void selectAllInView();
        }
      },
      "select all 0 in view"
    );
    footEl = h5(
      "div",
      { class: "cb-foot" },
      selCount,
      selAllBtn,
      h5(
        "button",
        {
          class: "cb-foot-more",
          hidden: true,
          // shown by updateFoot() when there are more items
          onclick() {
            void loadMore();
          }
        },
        `show ${PAGE_SIZE2} more`
      )
    );
    return footEl;
  }
  async function selectAllInView() {
    if (boardHandle) {
      handle.selectAll(boardHandle.allKeys());
      return;
    }
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
      const age = ageOf2(it);
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
      limit: String(PAGE_SIZE2)
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
      updateReadEmptyCount();
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
    const selAll = footEl.querySelector(".cb-sel-all");
    if (selAll instanceof HTMLElement) {
      selAll.textContent = `select all ${totalItemsForView} in view`;
    }
    const btn = footEl.querySelector(".cb-foot-more");
    if (btn) {
      const remaining = totalItems - offset;
      if (remaining > 0) {
        btn.textContent = `show ${Math.min(PAGE_SIZE2, remaining)} more`;
        btn.hidden = false;
      } else {
        btn.hidden = true;
      }
    }
    footEl.hidden = false;
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
  function getBoardFilters() {
    const p = {};
    if (filters.kind) p["kind"] = filters.kind;
    if (filters.repo) p["repo"] = filters.repo;
    if (filters.relation) p["relation"] = filters.relation;
    if (filters.bot) p["bot"] = filters.bot;
    if (filters.age) p["age"] = filters.age;
    if (filters.rule) p["rule"] = filters.rule;
    if (filters.q) p["q"] = filters.q;
    return p;
  }
  function mountBoard() {
    if (boardHandle) return;
    handle.el.closest(".kit-app")?.classList.add("cb-board-active");
    const kitRows = handle.el.querySelector(".kit-rows");
    const kitFoot = handle.el.querySelector(".kit-foot");
    if (kitRows) kitRows.hidden = true;
    if (kitFoot) kitFoot.hidden = true;
    boardHandle = makeBoard(
      ctx,
      selection,
      getBoardFilters,
      (key, laneId) => {
        filters.view = laneId;
      },
      (total) => {
        if (!footEl) return;
        const selAll = footEl.querySelector(".cb-sel-all");
        if (selAll instanceof HTMLElement) {
          selAll.textContent = `select all ${total} in view`;
        }
      }
    );
    handle.el.append(boardHandle.el);
  }
  function unmountBoard() {
    if (!boardHandle) return;
    handle.el.closest(".kit-app")?.classList.remove("cb-board-active");
    boardHandle.destroy();
    boardHandle.el.remove();
    boardHandle = null;
    const kitRows = handle.el.querySelector(".kit-rows");
    const kitFoot = handle.el.querySelector(".kit-foot");
    if (kitRows) kitRows.hidden = false;
    if (kitFoot) kitFoot.hidden = false;
  }
  function show(sub) {
    if (sub === "board") {
      const urlQ2 = getUrlQ();
      if (urlQ2 !== filters.q) {
        filters.q = urlQ2;
        handle.setSearch(urlQ2);
      }
      filters.view = "board";
      handle.setChips(
        "view",
        VIEWS.map((v) => ({
          ...v,
          on: v.id === "board"
        }))
      );
      mountBoard();
      return;
    }
    unmountBoard();
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
      showReadEmpty();
      void reload();
    } else if (sub) {
      handle.setChips(
        "view",
        VIEWS.map((v) => ({
          ...v,
          on: v.id === filters.view
        }))
      );
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
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      } else if (type === "decided" || type === "proposals") {
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
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
