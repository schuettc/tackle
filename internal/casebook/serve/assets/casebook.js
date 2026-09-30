// app.ts
import {
  bar,
  createApi,
  createKeys,
  live,
  initTheme,
  h
} from "/_kit/kit.js";

// router-helpers.ts
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

// router.ts
var listeners = [];
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
function registerDock(make) {
  dockMakers.push(make);
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
  function svgEl(tag, attrs, ...children) {
    const NS = "http://www.w3.org/2000/svg";
    const el = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
    for (const child of children) el.appendChild(child);
    return el;
  }
  const mark = svgEl(
    "svg",
    {
      viewBox: "0 0 64 64",
      fill: "none",
      xmlns: "http://www.w3.org/2000/svg",
      "aria-hidden": "true"
    },
    // ink tile
    svgEl("rect", { width: "64", height: "64", rx: "14", fill: "#14161d" }),
    // open book outline
    svgEl("path", {
      d: "M32 18C25 13 16 13 8 15V49C16 47 25 47 32 52C39 47 48 47 56 49V15C48 13 39 13 32 18Z",
      fill: "none",
      stroke: "#d98f66",
      "stroke-width": "5",
      "stroke-linejoin": "round"
    }),
    // spine
    svgEl("path", { d: "M32 18V52", stroke: "#d98f66", "stroke-width": "4.4" }),
    // one neutral rule on the left page
    svgEl("path", {
      d: "M15 29H25",
      stroke: "#9aa0ab",
      "stroke-width": "5",
      "stroke-linecap": "round"
    }),
    // tick on the right page (fully drawn — resting frame)
    svgEl("path", {
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
  const dockHandles = [];
  let lastAttached = {};
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
    },
    setAttached(a) {
      lastAttached = a;
      for (const d of dockHandles) d.setAttached(a);
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
  for (const make of dockMakers) {
    const d = make(ctx);
    d.setAttached(lastAttached);
    dockHandles.push(d);
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
      for (const sec of sections.values()) sec.onLive(e.type, e.data);
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
      if (!active) sec.hide();
    }
    const activeSec = sections.get(sectionId);
    if (activeSec) {
      activeSec.show(r.sub);
    } else {
      ctx.setAttached({});
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
  h as h6
} from "/_kit/kit.js";

// item.ts
import { h as h4, facts, fold } from "/_kit/kit.js";

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
function keyWithoutKind(key) {
  const i = key.indexOf(":");
  return i > 0 ? key.slice(i + 1) : key;
}
function allowedForKeys(vocab, keys) {
  if (keys.length === 0) return [];
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  if (kinds.length === 0) return [];
  const sets = kinds.map((k) => new Set(allowedForKind(vocab, k)));
  const first = [...sets[0] ?? /* @__PURE__ */ new Set()];
  return first.filter((d) => sets.every((s) => s.has(d)));
}
function pluralize(n, singular, plural) {
  return `${n} ${n === 1 ? singular : plural ?? singular + "s"}`;
}
function stripOwnKey(subject, key) {
  if (!key) return subject;
  const withSpace = key + " ";
  if (subject.includes(withSpace)) {
    return subject.replace(withSpace, "");
  }
  if (subject.includes(key)) {
    return subject.replace(key, "");
  }
  return subject;
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
function openDecideSheet(ctx, keys, onDone, seed, customPost) {
  void getVocab(ctx).then((vocab) => {
    openDecideSheetWithVocab(ctx, keys, vocab, onDone, seed, customPost);
  });
}
function openDecideSheetWithVocab(ctx, keys, vocab, onDone, seed, customPost) {
  const n = keys.length;
  const allowed = allowedForKeys(vocab, keys);
  let disposition = seed?.disposition ?? "";
  let until = seed?.until ?? "";
  let note = seed?.note ?? "";
  let submitting = false;
  let sh = null;
  let dryRunTimer = null;
  let lastDryRunError = null;
  const previewEl = h2("p", { class: "cb-sheet-preview" });
  previewEl.textContent = disposition ? `${disposition} ${pluralize(n, "item")}` : pluralize(n, "item");
  function updatePreview() {
    previewEl.textContent = `${disposition || "…"} ${pluralize(n, "item")}`;
  }
  const untilInputEl = h2("input", {
    type: "text",
    class: "cb-sheet-input",
    placeholder: (vocab.until_forms ?? []).map((f) => f.syntax).join(", ") || "date(YYYY-MM-DD), inactive(90d) …"
  });
  if (seed?.until) {
    untilInputEl.value = seed.until;
  }
  const untilRow = h2(
    "div",
    { class: "cb-sheet-row" },
    h2("label", { class: "cb-sheet-label" }, "until"),
    untilInputEl
  );
  untilRow.hidden = !seed?.until && !dispositionNeedsUntil(vocab, keys, disposition);
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
    value: seed?.note ?? "",
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
    const isSeeded = d === disposition;
    const btn = h2(
      "button",
      {
        type: "button",
        class: "cb-sheet-disp" + (DANGER_DISPS.has(d) ? " cb-sheet-disp--danger" : "") + (isSeeded ? " on" : ""),
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
      let decidedKeys;
      let errors = [];
      if (customPost) {
        decidedKeys = await customPost(disposition, until, note);
      } else {
        const payload = { keys, disposition };
        if (until) payload["until"] = until;
        if (note) payload["note"] = note;
        const result = await ctx.api.post("/decide", payload);
        decidedKeys = result.decided_keys ?? [];
        errors = result.errors ?? [];
      }
      if (decidedKeys.length > 0) {
        onDone(decidedKeys);
      }
      if (errors.length > 0) {
        errEl.textContent = errors.join("; ");
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
    title: `decide ${pluralize(n, "item")}`,
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

// proposals.ts
import { card, sheet as sheet2, noteField as noteField2, h as h3 } from "/_kit/kit.js";
function agentFromSource(source) {
  const i = source.indexOf(":");
  return i === -1 ? source : source.slice(0, i);
}
function openRejectSheet(ctx, ids, onDone) {
  let reason = "";
  let submitting = false;
  let sh = null;
  const reasonInput = noteField2({
    placeholder: "reason (optional)",
    onCommit(v) {
      reason = v;
    }
  });
  const errEl = h3("p", { class: "cb-sheet-err" });
  errEl.hidden = true;
  const body = h3(
    "div",
    { class: "cb-sheet-body" },
    h3(
      "div",
      { class: "cb-sheet-row" },
      h3("label", { class: "cb-sheet-label" }, "reason"),
      reasonInput
    ),
    errEl
  );
  async function doReject() {
    if (submitting) return;
    submitting = true;
    errEl.hidden = true;
    try {
      const payload = { ids };
      if (reason.trim()) payload["reason"] = reason.trim();
      await ctx.api.post("/proposals/reject", payload);
      onDone();
      sh?.close();
    } catch (err) {
      errEl.textContent = err instanceof Error ? err.message : "reject failed — try again";
      errEl.hidden = false;
      submitting = false;
    }
  }
  sh = sheet2({
    title: `reject ${pluralize(ids.length, "proposal")}`,
    body,
    actions: [
      {
        label: `Reject ${ids.length}`,
        fill: true,
        run() {
          void doReject();
        }
      }
    ],
    onClose() {
      sh = null;
    }
  });
}
function proposalCard(ctx, detail, onDone) {
  const proposal = detail.item.proposal;
  if (!proposal || proposal.state !== "pending") return null;
  const p = proposal;
  const agent = agentFromSource(p.source);
  const head = `${agent} proposes · ${p.disposition}`;
  const lines = [];
  if (p.note) {
    const noteEl = h3("p", { class: "cb-proposal-note" });
    noteEl.textContent = p.note;
    lines.push(noteEl);
  }
  const bodyEl = h3("div", { class: "cb-proposal-body" }, ...lines);
  function doAccept() {
    void ctx.api.post("/proposals/accept", { ids: [p.id] }).then(() => {
      onDone?.();
    }).catch(() => {
    });
  }
  function doReject() {
    openRejectSheet(ctx, [p.id], () => {
      onDone?.();
    });
  }
  const actions = [
    { label: "accept", fill: true, run: doAccept },
    {
      label: "change…",
      run() {
        openDecideSheet(
          ctx,
          [p.key],
          () => {
            onDone?.();
          },
          {
            disposition: p.disposition,
            until: p.until ?? "",
            note: p.note ?? ""
          },
          async (disp, until, note) => {
            const result = await ctx.api.post(
              "/proposals/change",
              { id: p.id, disposition: disp, until, note }
            );
            return result.decided_keys ?? [];
          }
        );
      }
    },
    { label: "reject", run: doReject }
  ];
  const el = card({ edge: "agent", head, body: bodyEl, actions });
  el.classList.add("cb-proposal-card");
  return el;
}
function bulkProposalActions(ctx, onDone) {
  let _ids = [];
  let _keys = [];
  const acceptBtn = h3("button", {
    class: "kit-btn fill cb-prop-accept",
    hidden: true,
    onclick() {
      if (_ids.length === 0) return;
      const ids = [..._ids];
      const keys = [..._keys];
      void ctx.api.post("/proposals/accept", { ids }).then(() => {
        onDone(keys);
      }).catch(() => {
      });
    }
  });
  acceptBtn.textContent = "accept 0";
  const rejectBtn = h3("button", {
    class: "kit-btn cb-prop-reject",
    hidden: true,
    onclick() {
      if (_ids.length === 0) return;
      const keys = [..._keys];
      openRejectSheet(ctx, [..._ids], () => {
        onDone(keys);
      });
    }
  });
  rejectBtn.textContent = "reject 0…";
  const el = h3("div", { class: "cb-prop-bulk" }, acceptBtn, rejectBtn);
  function update(proposalIds, itemKeys) {
    _ids = proposalIds;
    _keys = itemKeys;
    const n = proposalIds.length;
    if (n === 0) {
      acceptBtn.hidden = true;
      rejectBtn.hidden = true;
    } else {
      acceptBtn.textContent = `accept ${n}`;
      rejectBtn.textContent = `reject ${n}…`;
      acceptBtn.hidden = false;
      rejectBtn.hidden = false;
    }
  }
  return { el, update };
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
function fmtShortDate(s) {
  if (!s)
    return "def";
  const d = new Date(s);
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${mm}-${dd}`;
}
function renderEvidence(evs) {
  const section = h4("section", { class: "cb-evidence" });
  section.append(h4("h3", { class: "kit-label" }, "evidence"));
  if (evs.length === 0) {
    section.append(h4("p", { class: "cb-empty" }, "no evidence"));
    return section;
  }
  for (const ev of evs) {
    const authorEl = ev.author ? h4("span", { class: "cb-evidence-author" }, ev.author + " ") : null;
    const time = h4(
      "span",
      { class: "cb-muted" },
      ` · ${fmtShortDate(ev.created_at)}`
    );
    const item = h4(
      "div",
      { class: "cb-evidence-item" },
      h4(
        "p",
        { class: "cb-evidence-meta" },
        ...authorEl ? [authorEl] : [],
        ev.text,
        time
      )
    );
    section.append(item);
  }
  return section;
}
function renderHistory(events, decisions, ownKey) {
  const section = h4("section", { class: "cb-history" });
  section.append(h4("h3", { class: "kit-label" }, "history"));
  if (events.length === 0 && decisions.length === 0) {
    section.append(h4("p", { class: "cb-empty" }, "no history"));
    return section;
  }
  for (const entry of decisions) {
    const display = stripOwnKey(entry.Subject, ownKey);
    section.append(
      h4(
        "div",
        { class: "cb-history-item cb-history-decision" },
        h4("span", { class: "cb-history-time" }, fmtShortDate(entry.Time)),
        h4("span", { class: "cb-history-msg" }, display)
      )
    );
  }
  for (const ev of events) {
    const label = ev.actions && ev.actions.length > 0 ? ev.actions.map((a) => a.hook ?? "").filter(Boolean).join(", ") : ev.hook ?? ev.src;
    section.append(
      h4(
        "div",
        { class: "cb-history-item" },
        h4("span", { class: "cb-history-time" }, fmtShortDate(ev.ts)),
        h4("span", { class: "cb-history-msg" }, label)
      )
    );
  }
  return section;
}
function renderDecideSection(ctx, key, kind) {
  const section = h4("section", { class: "cb-decide" });
  section.append(h4("h3", { class: "kit-label" }, "decide"));
  const dispRow = h4("div", { class: "cb-decide-btns" });
  section.append(dispRow);
  void getVocab(ctx).then((vocab) => {
    const vocabKind = (vocab.kinds ?? []).find((k) => k.kind === kind);
    const kindAllowed = allowedForKind(vocab, kind);
    const needsUntilSet = new Set(vocabKind?.needs_until ?? []);
    for (const d of kindAllowed) {
      const label = needsUntilSet.has(d) ? `${d}…` : d;
      dispRow.append(
        h4(
          "button",
          {
            type: "button",
            class: "kit-btn cb-sheet-disp" + (DANGER_DISPS.has(d) ? " cb-sheet-disp--danger danger" : ""),
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
function renderItem(ctx, detail, onRefresh) {
  const it = detail.item;
  const el = h4("article", { class: "cb-item" });
  const displayKey = keyWithoutKind(it.key);
  const kickerParts = [it.kind, displayKey, it.relation].filter(Boolean).join(" · ");
  el.append(h4("p", { class: "cb-kicker kit-kick" }, kickerParts));
  el.append(
    h4("h1", { class: "cb-title kit-h1" }, it.title ?? keyWithoutKind(it.key))
  );
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
    const bodyWrap = h4("div", { class: "cb-body" });
    bodyWrap.append(h4("p", null, excerpt));
    if (rest) {
      bodyWrap.append(fold("read more", h4("p", null, rest)));
    }
    el.append(bodyWrap);
  }
  const propCard = proposalCard(ctx, detail, onRefresh);
  if (propCard) {
    el.append(propCard);
  }
  el.append(renderDecideSection(ctx, it.key, it.kind));
  el.append(renderEvidence(detail.evidence ?? []));
  el.append(
    renderHistory(detail.history ?? [], detail.decisions ?? [], it.key)
  );
  return h4("div", { class: "kit-doc" }, el);
}

// board.ts
import { h as h5 } from "/_kit/kit.js";
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
  const el = h5("div", { class: "cb-board" });
  const laneState = new Map(
    LANES.map(({ id }) => [id, { items: [], total: 0 }])
  );
  const laneRowsEl = /* @__PURE__ */ new Map();
  const laneMoreEl = /* @__PURE__ */ new Map();
  for (const { id, label } of LANES) {
    const headEl = h5("div", { class: "cb-lane-head" });
    headEl.textContent = label;
    const rowsEl = h5("div", { class: "cb-lane-rows" });
    laneRowsEl.set(id, rowsEl);
    const moreEl = h5("button", { class: "cb-lane-more", hidden: true });
    moreEl.textContent = "show more";
    moreEl.addEventListener("click", () => {
      void loadMore(id);
    });
    laneMoreEl.set(id, moreEl);
    el.append(
      h5("div", { class: "cb-lane", "data-lane": id }, headEl, rowsEl, moreEl)
    );
  }
  function buildCard(it, laneId) {
    const selected = sel.has(it.key);
    const box = h5("span", { class: "kit-box" + (selected ? " on" : "") });
    const kk = h5("span", { class: "cb-card-kk" });
    const displayKey = it.kind ? keyWithoutKind(it.key) : it.key;
    kk.textContent = it.kind ? `${it.kind} · ${displayKey}` : displayKey;
    const titleEl = h5("div", { class: "cb-card-title" });
    titleEl.textContent = it.title ?? displayKey;
    const card4 = h5(
      "div",
      {
        // Use .kit-card for background/border/radius from the kit;
        // .on marks the card as selected (adds signal-coloured left shadow).
        class: "kit-card" + (selected ? " on" : ""),
        tabindex: "0",
        "data-id": it.key
      },
      h5("div", { class: "kit-card-head" }, box, kk),
      titleEl
    );
    const age = ageOf(it);
    if (age) {
      const ageEl = h5("div", { class: "cb-card-age" });
      ageEl.textContent = age;
      card4.append(ageEl);
    }
    if (it.proposal) {
      const propEl = h5("div", { class: "cb-card-prop" });
      propEl.textContent = `${agentFromSource(it.proposal.source)} proposes ${it.proposal.disposition}`;
      card4.append(propEl);
    }
    titleEl.addEventListener("click", (e) => {
      e.stopPropagation();
      onOpen?.(it.key, laneId);
      ctx.route.go("item", it.key);
    });
    card4.addEventListener("click", (e) => {
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
    card4.addEventListener("keydown", (e) => {
      if (e.key === "o" || e.key === "Enter") {
        e.preventDefault();
        onOpen?.(it.key, laneId);
        ctx.route.go("item", it.key);
      }
    });
    return card4;
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
      const cards = rowsEl.querySelectorAll(".kit-card");
      cards.forEach((card4, i) => {
        const it = state.items[i];
        if (!it) return;
        const selected = sel.has(it.key);
        card4.classList.toggle("on", selected);
        card4.querySelector(".kit-box")?.classList.toggle("on", selected);
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
  const readEl = h6("div", { class: "kit-read" });
  let offset = 0;
  let totalItems = 0;
  let loadedItems = [];
  const filters = emptyFilters();
  let loading = false;
  let footEl = null;
  let propFoot = null;
  let searchDebounceTimer = null;
  let totalItemsForView = 0;
  let currentOpenKey = null;
  const viewCounts = {};
  let boardHandle = null;
  function renderReadEmpty() {
    const nameEl = h6(
      "p",
      { class: "cb-read-empty-section kit-label" },
      "attention"
    );
    const countEl = h6(
      "p",
      { class: "cb-read-empty-count" },
      pluralize(totalItemsForView, "item")
    );
    const promptEl = h6(
      "p",
      { class: "cb-read-empty-prompt" },
      "Select an item to see it here."
    );
    return h6("div", { class: "cb-read-empty" }, nameEl, countEl, promptEl);
  }
  function updateReadEmptyCount() {
    const countEl = readEl.querySelector(".cb-read-empty-count");
    if (countEl) {
      countEl.textContent = pluralize(totalItemsForView, "item");
    }
  }
  let active = false;
  function feedAttached() {
    if (!active) return;
    const ids = selection.ids();
    if (ids.length > 0) ctx.setAttached({ keys: ids });
    else if (currentOpenKey) ctx.setAttached({ open: currentOpenKey });
    else ctx.setAttached({});
  }
  function showReadEmpty() {
    currentOpenKey = null;
    readEl.replaceChildren(renderReadEmpty());
    feedAttached();
  }
  showReadEmpty();
  function buildFoot() {
    const selCount = h6(
      "span",
      { class: "cb-sel-count", hidden: true },
      "0 selected"
    );
    const selAllBtn = h6(
      "button",
      {
        class: "cb-sel-all",
        onclick() {
          void selectAllInView();
        }
      },
      "select all 0 in view"
    );
    propFoot = bulkProposalActions(ctx, (keys) => {
      selection.deselect(keys);
      void reload();
    });
    const footRight = h6(
      "div",
      { class: "cb-foot-right" },
      propFoot.el,
      h6(
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
    footEl = h6("div", { class: "cb-foot" }, selCount, selAllBtn, footRight);
    return footEl;
  }
  function updateProposalBulk(selectedIds) {
    if (!propFoot) return;
    if (filters.view !== "proposed") {
      propFoot.update([], []);
      return;
    }
    const withProps = loadedItems.filter(
      (it) => it.proposal && it.proposal.state === "pending" && selectedIds.includes(it.key)
    );
    propFoot.update(
      withProps.map((it) => it.proposal.id),
      withProps.map((it) => it.key)
    );
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
      const displayKey = it.kind ? keyWithoutKind(it.key) : it.key;
      const kindKey = it.kind ? `${it.kind} · ${displayKey}` : displayKey;
      const age = ageOf2(it);
      const proposal = it.proposal ? `${agentFromSource(it.proposal.source)} proposes ${it.proposal.disposition}` : void 0;
      return {
        id: it.key,
        key: kindKey,
        // Fallback: use display key (without kind prefix) so titleless items
        // like branch:schuettc/hail@feat/client show "schuettc/hail@feat/client".
        title: it.title ?? keyWithoutKind(it.key),
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
      updateProposalBulk(selection.ids());
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
      updateProposalBulk(selection.ids());
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
      const allSelected = totalItemsForView > 0 && selection.ids().length >= totalItemsForView;
      selAll.hidden = allSelected;
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
    currentOpenKey = key;
    feedAttached();
    try {
      const detail = await ctx.api.get("/item", { key });
      const el = renderItem(ctx, detail, () => {
        void openDetail(key);
      });
      readEl.replaceChildren(el);
    } catch {
    }
  }
  function viewChips(activeId) {
    return VIEWS.map((v) => ({
      ...v,
      on: v.id === activeId,
      count: viewCounts[v.id]
    }));
  }
  function applyCounts(counts) {
    if (!counts) return;
    for (const v of VIEWS) {
      if (v.id !== "board") viewCounts[v.id] = 0;
    }
    Object.assign(viewCounts, counts);
    handle.setChips("view", viewChips(filters.view));
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
  selection.onChange((ids) => {
    updateProposalBulk(ids);
    if (footEl) {
      const selAll = footEl.querySelector(".cb-sel-all");
      if (selAll) {
        const allSelected = totalItemsForView > 0 && ids.length >= totalItemsForView;
        selAll.hidden = allSelected;
      }
    }
    feedAttached();
  });
  ctx.keys.register({
    keys: "a",
    label: "accept proposal",
    group: "page",
    run() {
      if (!currentOpenKey) return;
      const it = loadedItems.find((x) => x.key === currentOpenKey);
      if (!it?.proposal || it.proposal.state !== "pending") return;
      void ctx.api.post("/proposals/accept", { ids: [it.proposal.id] }).then(() => {
        selection.deselect([currentOpenKey]);
        void openDetail(currentOpenKey);
        void reload();
      }).catch(() => {
      });
    }
  });
  ctx.keys.register({
    keys: "r",
    label: "reject proposal",
    group: "page",
    run() {
      if (!currentOpenKey) return;
      const it = loadedItems.find((x) => x.key === currentOpenKey);
      if (!it?.proposal || it.proposal.state !== "pending") return;
      openRejectSheet(ctx, [it.proposal.id], () => {
        selection.deselect([currentOpenKey]);
        void openDetail(currentOpenKey);
        void reload();
      });
    }
  });
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
    active = true;
    feedAttached();
    if (sub === "board") {
      const urlQ2 = getUrlQ();
      if (urlQ2 !== filters.q) {
        filters.q = urlQ2;
        handle.setSearch(urlQ2);
      }
      filters.view = "board";
      handle.setChips("view", viewChips("board"));
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
      handle.setChips("view", viewChips(filters.view));
      updateProposalBulk(selection.ids());
      showReadEmpty();
      void reload();
    } else if (sub) {
      handle.setChips("view", viewChips(filters.view));
      void openDetail(sub);
      void reload();
    }
  }
  return {
    id: "attention",
    list: handle.el,
    read: readEl,
    show,
    hide() {
      active = false;
    },
    onLive(type, data) {
      if (type === "index") {
        const s = data;
        applyCounts(s?.counts ?? null);
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      } else if (type === "decided") {
        const decidedPayload = data;
        const decidedKeys = decidedPayload?.keys ?? [];
        if (decidedKeys.length > 0) {
          selection.deselect(decidedKeys);
        }
        void ctx.api.get("/summary").then((s) => applyCounts(s.counts)).catch(() => {
        });
        if (boardHandle) {
          void boardHandle.refresh();
        } else {
          void reload();
        }
      } else if (type === "proposals") {
        const propPayload = data;
        const propIds = propPayload?.ids ?? [];
        const propState = propPayload?.state ?? "";
        if (propState === "accepted" || propState === "rejected" || propState === "changed") {
          const propIdSet = new Set(propIds);
          const affectedKeys = loadedItems.filter((it) => it.proposal && propIdSet.has(it.proposal.id)).map((it) => it.key);
          if (affectedKeys.length > 0) {
            selection.deselect(affectedKeys);
          }
          if (currentOpenKey && affectedKeys.includes(currentOpenKey)) {
            void openDetail(currentOpenKey);
          }
        }
        void ctx.api.get("/summary").then((s) => applyCounts(s.counts)).catch(() => {
        });
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

// dock.ts
import { h as h9, card as card3 } from "/_kit/kit.js";

// time-utils.ts
function ageMs(ts) {
  return Date.now() - new Date(ts).getTime();
}
function fmtAge(ts) {
  const ms = ageMs(ts);
  const s = Math.floor(ms / 1e3);
  if (s < 60) return "now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h10 = Math.floor(m / 60);
  if (h10 < 24) return `${h10}h`;
  const d = Math.floor(h10 / 24);
  if (d < 30) return `${d}d`;
  const mo = Math.floor(d / 30);
  if (mo < 12) return `${mo}mo`;
  return `${Math.floor(mo / 12)}y`;
}

// thread.ts
function at(m) {
  const ts = m.author === "court" ? m.queued_at ?? m.created_at : m.created_at;
  return Date.parse(ts);
}
function threadOrder(ms) {
  return [...ms].sort(
    (a, b) => at(a) - at(b) || (a.batch_id ?? 0) - (b.batch_id ?? 0) || (a.batch_pos ?? 0) - (b.batch_pos ?? 0) || a.id - b.id
  );
}

// composer.ts
import { h as h7 } from "/_kit/kit.js";

// attached.ts
var KIND_PLURAL = { branch: "branches" };
function isEmpty(a) {
  return !a.keys?.length && !a.open && !a.rule && !a.job;
}
function attachedLabel(a) {
  const parts = [];
  const keys = a.keys ?? [];
  if (keys.length === 1) {
    parts.push(keyWithoutKind(keys[0]));
  } else if (keys.length > 1) {
    const kinds = new Set(keys.map(kindFromKey));
    const kind = kinds.size === 1 ? [...kinds][0] : "";
    const noun = kind ? pluralize(keys.length, kind, KIND_PLURAL[kind]) : pluralize(keys.length, "item");
    parts.push(`${noun} selected`);
  }
  if (a.open && !(keys.length === 1 && keys[0] === a.open)) {
    parts.push(keyWithoutKind(a.open));
  }
  if (a.rule) parts.push(`rule ${a.rule}`);
  if (a.job) parts.push(`job #${a.job}`);
  return parts.join(" · ");
}
function attachedText(a) {
  const words = [...a.keys ?? []];
  if (a.open && !(a.keys?.length === 1 && a.keys[0] === a.open)) {
    words.push(`open ${a.open}`);
  }
  if (a.rule) words.push(`rule ${a.rule}`);
  if (a.job) words.push(`job ${a.job}`);
  return words.join(" ");
}
function parseAttached(text) {
  const words = text.split(/[\s,]+/).map((w) => w.trim()).filter(Boolean);
  const out = {};
  const keys = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    const next = words[i + 1];
    if ((w === "rule" || w === "job" || w === "open") && next) {
      if (w === "rule") out.rule = next;
      else if (w === "job") out.job = next.replace(/^#/, "");
      else if (isKey(next)) out.open = next;
      i++;
      continue;
    }
    if (isKey(w) && !keys.includes(w)) keys.push(w);
  }
  if (keys.length) out.keys = keys;
  return out;
}
function isKey(w) {
  return /^[a-z]+:\S+$/.test(w);
}
function sameAttached(a, b) {
  return attachedText(a) === attachedText(b);
}

// composer.ts
function threadName(body) {
  const words = body.split(/\s+/).filter(Boolean).slice(0, 5).join(" ");
  return words.length > 40 ? words.slice(0, 39) + "…" : words;
}
function makeComposer(ctx, dock) {
  let context = {};
  let override = null;
  let sending = false;
  const effective = () => override ?? context;
  const value = h7("span", {
    class: "cb-comp-attached-value",
    contenteditable: "true",
    role: "textbox",
    "aria-label": "attached",
    spellcheck: false,
    "data-testid": "composer-attached"
  });
  const line = h7(
    "div",
    { class: "cb-comp-attached" },
    h7("span", { class: "cb-comp-attached-prefix" }, "attached: "),
    value
  );
  let editing = false;
  function renderAttached() {
    if (editing) return;
    const a = effective();
    value.textContent = isEmpty(a) ? "nothing" : attachedLabel(a);
    value.toggleAttribute("data-empty", isEmpty(a));
    value.toggleAttribute("data-edited", override !== null);
  }
  value.addEventListener("focus", () => {
    if (editing) return;
    editing = true;
    value.textContent = attachedText(effective());
    value.removeAttribute("data-empty");
    const range = document.createRange();
    range.selectNodeContents(value);
    const sel = window.getSelection();
    sel?.removeAllRanges();
    sel?.addRange(range);
  });
  function commit() {
    if (!editing) return;
    editing = false;
    const parsed = parseAttached(value.textContent ?? "");
    override = sameAttached(parsed, context) ? null : parsed;
    renderAttached();
  }
  function revert() {
    editing = false;
    renderAttached();
  }
  value.addEventListener("blur", commit);
  value.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      commit();
      value.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      revert();
      value.blur();
    }
  });
  const input = h7("textarea", {
    class: "cb-comp-input",
    rows: 1,
    placeholder: "Message the agent…",
    "aria-label": "message",
    "data-testid": "composer-input"
  });
  function fit() {
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, 160)}px`;
  }
  input.addEventListener("input", fit);
  input.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" || e.isComposing) return;
    if (e.shiftKey || e.metaKey || e.ctrlKey || e.altKey) return;
    e.preventDefault();
    void send(false);
  });
  const note = h7("span", { class: "cb-comp-note", role: "status" });
  const footer = h7(
    "div",
    { class: "cb-comp-foot" },
    h7("span", {}, "↵ send · ⌘↵ add to batch"),
    note
  );
  const el = h7(
    "div",
    { class: "cb-comp", "data-testid": "composer" },
    line,
    input,
    footer
  );
  async function threadFor(body) {
    const current = dock.currentThread();
    if (current) return current;
    const session = dock.currentSession();
    if (!session) return 0;
    const t = await ctx.api.post("/threads", {
      session,
      name: threadName(body)
    });
    dock.threadCreated(t);
    return t.id;
  }
  async function send(batch) {
    const sent = input.value;
    const body = sent.trim();
    if (!body || sending) return;
    sending = true;
    note.textContent = "";
    try {
      const thread = await threadFor(body);
      if (!thread) {
        note.textContent = "no agent session";
        return;
      }
      await ctx.api.post("/messages", {
        thread,
        body,
        attached: effective(),
        batch
      });
      const now = input.value;
      if (now.startsWith(sent)) {
        input.value = now.slice(sent.length).replace(/^\s+/, "");
      }
      fit();
      override = null;
      renderAttached();
    } catch (err) {
      note.textContent = "not sent";
      console.error("[composer] send:", err);
    } finally {
      sending = false;
    }
  }
  renderAttached();
  return {
    el,
    input,
    setAttached(a) {
      context = a;
      renderAttached();
    },
    setAgent(name) {
      input.placeholder = `Message ${name || "the agent"}…`;
    },
    focus() {
      input.focus();
    },
    addToBatch() {
      void send(true);
    }
  };
}

// progress.ts
import { h as h8, card as card2, fold as fold2, noteField as noteField3 } from "/_kit/kit.js";
function makeBatchTray(ctx) {
  let thread = 0;
  let batch = 0;
  let drafts = [];
  let loadSeq = 0;
  const label = h8("span", { class: "kit-card-head cb-batch-label" });
  const sendBtn = h8("button", {
    class: "kit-btn fill cb-batch-send",
    "data-testid": "batch-send",
    onclick() {
      void sendBatch();
    }
  });
  const list2 = h8("div", { class: "cb-batch-drafts" });
  const el = card2({
    body: h8(
      "div",
      {},
      h8("div", { class: "cb-batch-head" }, label, sendBtn),
      list2
    )
  });
  el.classList.add("cb-batch");
  el.setAttribute("data-testid", "batch-tray");
  el.hidden = true;
  function render() {
    el.hidden = drafts.length === 0;
    if (!drafts.length) {
      list2.replaceChildren();
      return;
    }
    label.textContent = `batch · ${pluralize(drafts.length, "draft")}`;
    sendBtn.textContent = `send ${drafts.length}`;
    list2.replaceChildren(...drafts.map((d, i) => draftRow(d, i)));
  }
  function actionBtn(text, action, aria, run, disabled = false) {
    return h8(
      "button",
      {
        class: "cb-batch-act",
        "data-action": action,
        "aria-label": aria,
        disabled,
        onclick: run
      },
      text
    );
  }
  function draftRow(d, i) {
    const text = h8("span", { class: "cb-batch-text" }, d.body);
    const row = h8(
      "div",
      { class: "cb-batch-draft", "data-draft-id": String(d.id) },
      text,
      h8(
        "span",
        { class: "cb-batch-acts" },
        actionBtn(
          "edit",
          "edit",
          `edit draft ${i + 1}`,
          () => editDraft(d, text)
        ),
        actionBtn(
          "↑",
          "up",
          `move draft ${i + 1} up`,
          () => void move(i, -1),
          i === 0
        ),
        actionBtn(
          "↓",
          "down",
          `move draft ${i + 1} down`,
          () => void move(i, 1),
          i === drafts.length - 1
        ),
        actionBtn(
          "×",
          "remove",
          `remove draft ${i + 1}`,
          () => void remove(d.id)
        )
      )
    );
    return row;
  }
  function editDraft(d, text) {
    const field = noteField3({
      value: d.body,
      onCommit(v) {
        const body = v.trim();
        if (body && body !== d.body) {
          d.body = body;
          void ctx.api.post("/drafts/edit", { id: d.id, body }).catch((err) => {
            console.error("[batch] edit:", err);
            void reload();
          });
        }
        render();
      }
    });
    field.classList.add("cb-batch-edit");
    field.setAttribute("aria-label", "edit draft");
    field.addEventListener("keydown", (e) => {
      if (e.key === "Escape") render();
    });
    field.addEventListener("blur", () => {
      if (field.isConnected) render();
    });
    text.replaceWith(field);
    field.focus();
    field.select();
  }
  async function move(i, delta) {
    const j = i + delta;
    if (j < 0 || j >= drafts.length) return;
    const next = [...drafts];
    [next[i], next[j]] = [next[j], next[i]];
    drafts = next;
    render();
    try {
      await ctx.api.post("/batches/reorder", {
        batch,
        ids: drafts.map((d) => d.id)
      });
    } catch (err) {
      console.error("[batch] reorder:", err);
      await reload();
    }
  }
  async function remove(id) {
    drafts = drafts.filter((d) => d.id !== id);
    render();
    try {
      await ctx.api.post("/drafts/remove", { id });
    } catch (err) {
      console.error("[batch] remove:", err);
      await reload();
    }
  }
  async function sendBatch() {
    if (!batch || !drafts.length) return;
    const sent = batch;
    drafts = [];
    render();
    try {
      await ctx.api.post("/batches/send", { batch: sent });
    } catch (err) {
      console.error("[batch] send:", err);
      await reload();
    }
  }
  async function load(t) {
    thread = t;
    const seq = ++loadSeq;
    if (!t) {
      batch = 0;
      drafts = [];
      render();
      return;
    }
    try {
      const mv = await ctx.api.get("/messages", {
        thread: String(t)
      });
      if (seq !== loadSeq) return;
      batch = mv.batch;
      drafts = mv.drafts ?? [];
      render();
    } catch (err) {
      console.error("[batch] load:", err);
    }
  }
  function reload() {
    return load(thread);
  }
  return { el, load, reload };
}
var NO_PROGRESS_MS = 4 * 60 * 1e3;
function fmtAgo(ms) {
  const s = Math.max(0, Math.floor(ms / 1e3));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}
function countText(n, total) {
  return n && total ? ` · ${n} of ${total}` : "";
}
function makeProgressLine() {
  let prog = null;
  let at2 = 0;
  const text = h8("span", { class: "cb-prog-text" });
  const age = h8("span", { class: "cb-prog-age" });
  const fill = h8("span", { class: "cb-prog-fill" });
  const bar2 = h8("div", { class: "cb-prog-bar", role: "progressbar" }, fill);
  const el = h8(
    "div",
    { class: "cb-prog", "data-testid": "progress-line" },
    h8(
      "div",
      { class: "cb-prog-row" },
      h8("span", { class: "cb-prog-dot" }),
      text,
      age
    ),
    bar2
  );
  el.hidden = true;
  function render() {
    el.hidden = !prog;
    if (!prog) return;
    const since = Date.now() - at2;
    text.textContent = prog.text + countText(prog.n, prog.total);
    const quiet = since >= NO_PROGRESS_MS;
    el.toggleAttribute("data-quiet", quiet);
    age.textContent = quiet ? `no progress for ${Math.floor(since / 6e4)}m` : fmtAgo(since);
    const n = prog.n ?? 0;
    const total = prog.total ?? 0;
    bar2.hidden = !(n && total);
    if (n && total) {
      fill.style.width = `${Math.min(100, n / total * 100)}%`;
      bar2.setAttribute("aria-valuenow", String(n));
      bar2.setAttribute("aria-valuemax", String(total));
    }
  }
  return {
    el,
    set(p, when) {
      prog = p;
      at2 = when;
      render();
    },
    clear() {
      prog = null;
      render();
    },
    tick: render
  };
}
function makeWaitingStrip() {
  const count = h8("span", {});
  const el = h8(
    "div",
    { class: "cb-wait", "data-testid": "waiting-strip" },
    h8("span", { class: "cb-wait-word" }, "waiting"),
    count
  );
  el.hidden = true;
  return {
    el,
    setQueued(n) {
      el.hidden = n <= 0;
      count.textContent = ` · ${n} queued for the end of this turn`;
    }
  };
}
function clock(ts) {
  return new Date(ts).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false
  });
}
function workedFold(msg) {
  const lines = msg.worked?.lines ?? [];
  const history2 = h8(
    "ol",
    { class: "cb-worked-lines" },
    ...lines.map(
      (l) => h8(
        "li",
        { class: "cb-worked-line" },
        h8("span", { class: "cb-worked-at" }, clock(l.at)),
        h8("span", {}, l.text + countText(l.n, l.total))
      )
    )
  );
  const el = fold2(msg.body, history2);
  el.classList.add("cb-worked");
  el.setAttribute("data-testid", "worked");
  return el;
}

// dock.ts
function stateColor(state) {
  switch (state) {
    case "queued":
      return "var(--kit-wait)";
    case "working":
      return "var(--kit-agent)";
    case "answered":
      return "var(--kit-ok)";
    case "failed":
      return "var(--kit-danger)";
    default:
      return "var(--kit-muted)";
  }
}
function stateLabel(msg) {
  const base = msg.state;
  if (base === "answered") {
    const n = msg.attached?.keys?.length ?? 0;
    if (n > 0) return `${base} · ${pluralize(n, "item")}`;
  }
  return base;
}
function sessionLabel(s) {
  if (s.label) return s.label;
  const harness = s.harness || "agent";
  const cwd = s.cwd ? s.cwd.split("/").filter(Boolean).pop() ?? s.cwd : "";
  return cwd ? `${harness} · ${cwd}` : harness;
}
function renderAgentBody(msg) {
  const wrap = h9("div", { class: "cb-dock-body" });
  const bodyP = h9("p", { class: "cb-dock-bodytext" });
  bodyP.textContent = msg.body;
  wrap.append(bodyP);
  const { attached } = msg;
  if (attached) {
    const links = [];
    if (attached.keys && attached.keys.length > 0) {
      const shown = attached.keys.slice(0, 5);
      for (const k of shown) {
        const a = h9("a", {
          class: "cb-dock-link",
          href: "#",
          onclick(e) {
            e.preventDefault();
            go("item", encodeURIComponent(k));
          }
        });
        a.textContent = keyWithoutKind(k);
        links.push(a);
      }
      if (attached.keys.length > 5) {
        links.push(
          h9(
            "span",
            { class: "cb-dock-link-more" },
            `+${attached.keys.length - 5} more`
          )
        );
      }
    }
    if (attached.open) {
      const k = attached.open;
      const a = h9("a", {
        class: "cb-dock-link",
        href: "#",
        onclick(e) {
          e.preventDefault();
          go("item", encodeURIComponent(k));
        }
      });
      a.textContent = keyWithoutKind(k);
      links.push(a);
    }
    if (attached.rule) {
      const id = attached.rule;
      const a = h9("a", {
        class: "cb-dock-link",
        href: "#",
        onclick(e) {
          e.preventDefault();
          go("rules", encodeURIComponent(id));
        }
      });
      a.textContent = id;
      links.push(a);
    }
    if (attached.job) {
      const id = attached.job;
      const a = h9("a", {
        class: "cb-dock-link",
        href: "#",
        onclick(e) {
          e.preventDefault();
          go("apply", encodeURIComponent(id));
        }
      });
      a.textContent = `job ${id}`;
      links.push(a);
    }
    if (links.length > 0) {
      const linkRow = h9("div", { class: "cb-dock-links" }, ...links);
      wrap.append(linkRow);
    }
  }
  return wrap;
}
function renderMsgCard(msg, delivery, sessions, ctx, agentName) {
  if (msg.state === "worked") return workedFold(msg);
  const isAgent = msg.author !== "court";
  const authorLabel = isAgent ? agentName.toUpperCase() : "YOU";
  const age = fmtAge(msg.created_at);
  const headEl = h9(
    "div",
    { class: "cb-dock-ch" },
    h9(
      "span",
      {
        class: isAgent ? "cb-dock-ch-name cb-dock-ch-agent" : "cb-dock-ch-name"
      },
      authorLabel
    ),
    h9("span", { class: "cb-dock-ch-age" }, age)
  );
  let bodyContent;
  if (isAgent) {
    bodyContent = renderAgentBody(msg);
  } else {
    const bodyEl = h9("div", { class: "cb-dock-body" });
    const bodyP = h9("p", { class: "cb-dock-bodytext" });
    bodyP.textContent = msg.body;
    bodyEl.append(bodyP);
    bodyContent = bodyEl;
  }
  let stateEl = null;
  if (!isAgent && msg.state) {
    const validStates = [
      "queued",
      "delivered",
      "received",
      "working",
      "answered",
      "declined",
      "failed",
      "unanswered",
      "interrupted"
    ];
    if (validStates.includes(msg.state)) {
      stateEl = h9("div", {
        class: "cb-dock-state",
        "data-state": msg.state,
        style: `color:${stateColor(msg.state)}`
      });
      stateEl.textContent = stateLabel(msg);
    }
  }
  const fullBody = h9(
    "div",
    { class: "cb-dock-card-body" },
    headEl,
    bodyContent
  );
  if (stateEl) fullBody.append(stateEl);
  const isStuckDelivery = !isAgent && delivery?.stuck === true && msg.delivery_id === delivery.id;
  if (isStuckDelivery && delivery) {
    if (stateEl) {
      const ageText = fmtAge(delivery.touched_at);
      const ageStr = ageText === "now" ? "just now" : `${ageText} ago`;
      stateEl.textContent = "";
      stateEl.setAttribute("data-state", "stuck");
      const stuckBold = h9("strong", { style: "font-weight:600" });
      stuckBold.textContent = "stuck";
      stateEl.append(`delivered ${ageStr} · `, stuckBold);
    }
    const d = delivery;
    const releaseBtn = h9("button", {
      class: "kit-btn",
      "data-action": "release",
      onclick() {
        void releaseDelivery(ctx, d.id);
      }
    });
    releaseBtn.textContent = "release";
    const otherSessions = sessions.filter((s) => s.id !== d.session_id);
    const moveBtn = h9("button", {
      class: "kit-btn",
      "data-action": "move",
      onclick() {
        void openMoveSheet(ctx, d, otherSessions);
      }
    });
    moveBtn.textContent = "move to another session";
    fullBody.append(h9("div", { class: "cb-dock-stuck" }, releaseBtn, moveBtn));
  }
  const el = card3({
    edge: isAgent ? "agent" : "signal",
    body: fullBody
  });
  el.classList.add("cb-dock-card");
  if (isAgent) el.classList.add("cb-dock-card--agent");
  else el.classList.add("cb-dock-card--you");
  return el;
}
async function releaseDelivery(ctx, id) {
  try {
    await ctx.api.post("/deliveries/release", { id });
  } catch (err) {
    console.error("[dock] release delivery:", err);
  }
}
async function moveDelivery(ctx, id, sessionId) {
  try {
    await ctx.api.post("/deliveries/move", { id, session: sessionId });
  } catch (err) {
    console.error("[dock] move delivery:", err);
  }
}
function openMoveSheet(ctx, delivery, targets) {
  const items = targets.map((s) => {
    const el = h9("button", { class: "cb-dock-pick-item" });
    el.textContent = sessionLabel(s);
    el.onclick = () => {
      void moveDelivery(ctx, delivery.id, s.id);
      sheet3.close();
    };
    return el;
  });
  if (items.length === 0) {
    const noOther = h9(
      "p",
      { class: "cb-dock-pick-empty" },
      "no other sessions"
    );
    items.push(noOther);
  }
  const content = h9("div", { class: "cb-dock-pick-list" }, ...items);
  const sheet3 = {
    el: h9("div", { class: "cb-dock-pick-sheet" }, content),
    close() {
      this.el.remove();
    }
  };
  document.body.append(sheet3.el);
  function onKey(e) {
    if (e.key === "Escape") {
      sheet3.close();
      document.removeEventListener("keydown", onKey);
    }
  }
  document.addEventListener("keydown", onKey);
  return Promise.resolve();
}
async function moveSession(ctx, fromSession, toSession) {
  try {
    await ctx.api.post("/sessions/move", {
      session: fromSession,
      target: toSession
    });
  } catch (err) {
    console.error("[dock] move session:", err);
  }
}
function openSessionMoveSheet(ctx, fromSession, targets) {
  const items = targets.map((s) => {
    const el = h9("button", { class: "cb-dock-pick-item" });
    el.textContent = sessionLabel(s);
    el.onclick = () => {
      void moveSession(ctx, fromSession, s.id);
      sheet3.close();
    };
    return el;
  });
  if (items.length === 0) {
    const noOther = h9(
      "p",
      { class: "cb-dock-pick-empty" },
      "no other sessions"
    );
    items.push(noOther);
  }
  const content = h9("div", { class: "cb-dock-pick-list" }, ...items);
  const sheet3 = {
    el: h9("div", { class: "cb-dock-pick-sheet" }, content),
    close() {
      this.el.remove();
    }
  };
  document.body.append(sheet3.el);
  function onKey(e) {
    if (e.key === "Escape") {
      sheet3.close();
      document.removeEventListener("keydown", onKey);
    }
  }
  document.addEventListener("keydown", onKey);
  return Promise.resolve();
}
function buildSessionPicker(sessions, currentId, onSelect) {
  const items = sessions.map((s) => {
    const busy = s.busy ? " · busy" : " · idle";
    const stale = s.left ? " · left" : "";
    const label = sessionLabel(s) + busy + stale;
    const el = h9("button", {
      class: "cb-dock-pick-item" + (s.id === currentId ? " cb-dock-pick-item--on" : "")
    });
    el.textContent = label;
    el.onclick = () => {
      onSelect(s.id);
      picker.remove();
    };
    return el;
  });
  const picker = h9(
    "div",
    { class: "cb-dock-picker", role: "listbox" },
    ...items
  );
  function onKey(e) {
    if (e.key === "Escape") {
      picker.remove();
      document.removeEventListener("keydown", onKey);
    }
  }
  document.addEventListener("keydown", onKey);
  function onClick(e) {
    if (!picker.contains(e.target)) {
      picker.remove();
      document.removeEventListener("click", onClick);
    }
  }
  setTimeout(() => document.addEventListener("click", onClick), 0);
  return picker;
}
function makeDock(ctx) {
  let sessions = [];
  let currentSessionId = "";
  let threads = [];
  let currentThreadId = 0;
  let messages = [];
  let currentDelivery = null;
  let lastUsedSessionId = "";
  const sessionDot = h9("span", { class: "cb-dock-dot" });
  const sessionLabelEl = h9("span", { class: "cb-dock-session-label" });
  const sessionPickerBtn = h9("button", {
    class: "cb-dock-agent-btn",
    "aria-label": "pick agent session",
    onclick(e) {
      e.stopPropagation();
      if (!sessions.length) return;
      const p = buildSessionPicker(sessions, currentSessionId, (id) => {
        lastUsedSessionId = id;
        void switchSession(id);
      });
      const btn = e.currentTarget;
      const rect = btn.getBoundingClientRect();
      p.style.top = `${rect.bottom + 4}px`;
      p.style.right = `${window.innerWidth - rect.right}px`;
      document.body.append(p);
    }
  });
  sessionPickerBtn.textContent = "AGENT ▾";
  const leftStatusRow = h9("div", { class: "cb-dock-left-row" });
  leftStatusRow.hidden = true;
  const sessionHeader = h9(
    "div",
    { class: "cb-dock-header" },
    h9(
      "div",
      { class: "cb-dock-header-row" },
      h9("span", { class: "cb-dock-who" }, sessionDot, sessionLabelEl),
      sessionPickerBtn
    ),
    leftStatusRow
  );
  const threadChips = h9("div", { class: "cb-dock-threads" });
  const messageArea = h9("div", {
    class: "cb-dock-messages",
    "data-testid": "dock-messages"
  });
  const batchTray = makeBatchTray(ctx);
  const progLine = makeProgressLine();
  const waitStrip = makeWaitingStrip();
  const composer = makeComposer(ctx, {
    currentThread: () => currentThreadId,
    currentSession: () => currentSessionId,
    threadCreated(t) {
      threads = [...threads, t];
      currentThreadId = t.id;
      renderThreadChips();
      void batchTray.load(t.id);
    }
  });
  const rail = h9("div", { class: "cb-dock-inner" });
  rail.append(
    sessionHeader,
    threadChips,
    messageArea,
    progLine.el,
    waitStrip.el,
    composer.el
  );
  function agentName() {
    return sessions.find((s) => s.id === currentSessionId)?.harness || "agent";
  }
  function renderHeader() {
    const sess = sessions.find((s) => s.id === currentSessionId);
    if (!sess) {
      sessionDot.style.background = "var(--kit-muted)";
      sessionLabelEl.textContent = "no session";
      leftStatusRow.hidden = true;
      sessionHeader.removeAttribute("data-left");
      return;
    }
    const stale = !!sess.left;
    sessionDot.style.removeProperty("background");
    sessionLabelEl.textContent = sessionLabel(sess);
    if (stale) {
      leftStatusRow.textContent = "";
      const moveLink = h9("button", {
        class: "cb-dock-move-link",
        "data-testid": "dock-move-link",
        onclick(e) {
          e.stopPropagation();
          const others = sessions.filter((s) => s.id !== sess.id);
          void openSessionMoveSheet(ctx, sess.id, others);
        }
      });
      moveLink.textContent = "move to…";
      const parts = [h9("span", {}, "left")];
      if (sess.queued > 0) {
        parts.push(h9("span", { class: "cb-dock-sep" }, "·"));
        parts.push(h9("span", {}, `${sess.queued} queued`));
      }
      parts.push(h9("span", { class: "cb-dock-sep" }, "·"));
      parts.push(moveLink);
      leftStatusRow.append(...parts);
      leftStatusRow.hidden = false;
      sessionHeader.setAttribute("data-left", "1");
    } else {
      leftStatusRow.hidden = true;
      leftStatusRow.textContent = "";
      sessionHeader.removeAttribute("data-left");
    }
  }
  function renderThreadChips() {
    threadChips.innerHTML = "";
    for (const t of threads) {
      const chip = h9("button", {
        class: "kit-chip" + (t.id === currentThreadId ? " on" : ""),
        "data-thread": String(t.id),
        onclick() {
          void switchThread(t.id);
        }
      });
      chip.textContent = t.name;
      threadChips.append(chip);
    }
    const addChip = h9("button", {
      class: "kit-chip cb-dock-add",
      "data-testid": "dock-add-thread",
      onclick() {
        void newThread();
      }
    });
    addChip.textContent = "+";
    threadChips.append(addChip);
  }
  function renderMessages() {
    messageArea.replaceChildren();
    if (messages.length === 0) {
      const empty = h9("p", { class: "cb-dock-empty" }, "no messages");
      messageArea.append(empty);
    }
    const name = agentName();
    for (const msg of messages) {
      const el = renderMsgCard(msg, currentDelivery, sessions, ctx, name);
      messageArea.append(el);
    }
    messageArea.append(batchTray.el);
    messageArea.scrollTop = messageArea.scrollHeight;
  }
  async function loadSessions() {
    try {
      const sv = await ctx.api.get("/sessions");
      sessions = sv.sessions ?? [];
      if (!currentSessionId && sessions.length > 0) {
        currentSessionId = lastUsedSessionId ? sessions.find((s) => s.id === lastUsedSessionId)?.id ?? sessions[0].id : sessions[0].id;
        updateSessionParts();
        await loadThreads();
        await loadProgress();
      } else {
        renderHeader();
        await loadDelivery();
        updateSessionParts();
      }
      renderHeader();
      updateSessionParts();
    } catch (err) {
      console.error("[dock] loadSessions:", err);
    }
  }
  async function loadThreads() {
    if (!currentSessionId) return;
    try {
      const tv = await ctx.api.get("/threads", {
        session: currentSessionId
      });
      threads = tv.threads ?? [];
      if (!currentThreadId && threads.length > 0) {
        currentThreadId = threads[0].id;
      }
      renderThreadChips();
      await loadMessages();
      await loadDelivery();
      await batchTray.load(currentThreadId);
    } catch (err) {
      console.error("[dock] loadThreads:", err);
    }
  }
  async function loadMessages() {
    if (!currentThreadId) {
      messages = [];
      renderMessages();
      return;
    }
    try {
      const mv = await ctx.api.get("/messages", {
        thread: String(currentThreadId)
      });
      messages = threadOrder(mv.messages ?? []);
      renderMessages();
    } catch (err) {
      console.error("[dock] loadMessages:", err);
    }
  }
  async function loadDelivery() {
    if (!currentSessionId) return;
    try {
      const dv = await ctx.api.get("/session/delivery", {
        session: currentSessionId
      });
      currentDelivery = dv.delivery ?? null;
      renderMessages();
    } catch (err) {
      console.error("[dock] loadDelivery:", err);
    }
  }
  async function loadProgress() {
    const sid = currentSessionId;
    if (!sid) {
      progLine.clear();
      return;
    }
    try {
      const pv = await ctx.api.get("/session/progress", {
        session: sid
      });
      if (sid !== currentSessionId) return;
      if (pv.progress) {
        progLine.set(pv.progress, Date.parse(pv.progress.updated_at));
      } else {
        progLine.clear();
      }
    } catch (err) {
      console.error("[dock] loadProgress:", err);
    }
  }
  function updateSessionParts() {
    const sess = sessions.find((s) => s.id === currentSessionId);
    const turn = !!sess && sess.busy && !sess.left;
    waitStrip.setQueued(turn ? sess.queued : 0);
    composer.setAgent(sess?.harness ?? "");
  }
  async function switchSession(id) {
    currentSessionId = id;
    currentThreadId = 0;
    threads = [];
    messages = [];
    currentDelivery = null;
    progLine.clear();
    renderHeader();
    renderThreadChips();
    renderMessages();
    updateSessionParts();
    await Promise.all([loadThreads(), loadProgress()]);
  }
  async function switchThread(id) {
    currentThreadId = id;
    messages = [];
    currentDelivery = null;
    renderThreadChips();
    renderMessages();
    await loadMessages();
    await loadDelivery();
    await batchTray.load(id);
  }
  async function newThread() {
    const sessId = lastUsedSessionId || currentSessionId;
    if (!sessId) return;
    try {
      const t = await ctx.api.post("/threads", {
        session: sessId,
        name: "thread"
      });
      threads = [...threads, t];
      currentThreadId = t.id;
      messages = [];
      currentDelivery = null;
      renderThreadChips();
      renderMessages();
      await batchTray.load(t.id);
    } catch (err) {
      console.error("[dock] newThread:", err);
    }
  }
  ctx.on("sessions", () => {
    void loadSessions();
  });
  ctx.on("thread", (data) => {
    void loadThreads();
    void data;
  });
  ctx.on("messages", (data) => {
    const d = data;
    if (!d.ids) return;
    const msgIds = new Set(d.ids);
    if (messages.some((m) => msgIds.has(m.id))) {
      void loadMessages();
      void loadDelivery();
    }
  });
  ctx.on("delivery", (data) => {
    const d = data;
    const isOurs = !d.session || d.session === currentSessionId || d.from === currentSessionId || d.id !== void 0 && currentDelivery?.id === d.id;
    if (isOurs) {
      void loadMessages().then(() => loadDelivery());
      void loadSessions();
    }
  });
  ctx.on("progress", (data) => {
    const p = data;
    if (p.session_id === currentSessionId) progLine.set(p, Date.now());
  });
  ctx.on("settled", (data) => {
    const d = data;
    if (d.session !== currentSessionId) return;
    progLine.clear();
    void loadMessages();
    void loadSessions();
  });
  ctx.on("drafts", () => {
    void batchTray.reload();
  });
  ctx.on("batch", () => {
    void batchTray.reload();
    void loadMessages();
    void loadSessions();
  });
  ctx.on("message", (data) => {
    const m = data;
    if (m.thread_id === currentThreadId) {
      void loadMessages();
      if (m.state === "draft") void batchTray.reload();
    }
    void loadSessions();
  });
  setInterval(() => progLine.tick(), 1e3);
  ctx.keys.register({
    keys: ".",
    label: "focus the composer",
    group: "agent",
    run() {
      composer.focus();
    }
  });
  ctx.keys.register({
    keys: "⌘↵",
    label: "add to batch",
    group: "agent",
    inField: true,
    run(e) {
      if (e.target !== composer.input) return false;
      composer.addToBatch();
    }
  });
  void loadSessions();
  return {
    el: rail,
    setAttached(a) {
      composer.setAttached(a);
    },
    focusComposer() {
      composer.focus();
    },
    currentThread() {
      return currentThreadId;
    },
    currentSession() {
      return currentSessionId;
    }
  };
}

// entry.ts
registerSection(makeAttention);
registerDock(makeDock);
boot();
