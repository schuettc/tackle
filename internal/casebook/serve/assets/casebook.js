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

// section-keys.ts
function makeKeyBinder(register, report) {
  let bound;
  let unbinds = [];
  return (sec) => {
    if (sec === bound) return;
    for (const unbind of unbinds) unbind();
    unbinds = [];
    bound = sec;
    for (const b of sec?.keys ?? []) {
      try {
        unbinds.push(register(b));
      } catch (err) {
        const why = err instanceof Error ? err.message : String(err);
        report(
          `[casebook] key clash in section ${sec?.id}: "${b.keys}" (${b.label}): ${why}`
        );
      }
    }
  };
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
  let list3 = liveListeners.get(type);
  if (!list3) {
    list3 = [];
    liveListeners.set(type, list3);
  }
  list3.push(cb);
}
function emitLive(type, data) {
  const list3 = liveListeners.get(type);
  if (list3) for (const cb of list3) cb(data);
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
  let agent = "";
  const agentListeners = [];
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
    },
    focusComposer() {
      dockHandles[0]?.focusComposer();
    },
    agentName: () => agent,
    setAgentName(name) {
      if (name === agent) return;
      agent = name;
      for (const cb of agentListeners) cb(name);
    },
    onAgentName(cb) {
      agentListeners.push(cb);
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
    const list3 = h("div", { class: "kit-list" });
    const read = h("div", { class: "kit-read" });
    app.append(list3, read);
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
  const bindSectionKeys = makeKeyBinder(
    (b) => ctx.keys.register(b),
    (m) => console.error(m)
  );
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
    bindSectionKeys(activeSec);
    if (activeSec) {
      activeSec.show(r.sub);
      ctx.setPrimary(activeSec.primary());
    } else {
      ctx.setPrimary(null);
      ctx.setAttached({});
    }
  });
  void api.get("/summary").then((s) => {
    const counts = s.counts ?? {};
    handle.setCount("attention", counts["all"] ?? 0);
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
function allowedForKind(vocab2, kind) {
  return (vocab2.kinds ?? []).find((k) => k.kind === kind)?.allowed ?? [];
}
function keyWithoutKind(key) {
  const i = key.indexOf(":");
  return i > 0 ? key.slice(i + 1) : key;
}
function allowedForKeys(vocab2, keys) {
  if (keys.length === 0) return [];
  const kinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  if (kinds.length === 0) return [];
  const sets = kinds.map((k) => new Set(allowedForKind(vocab2, k)));
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
function dispositionNeedsUntil(vocab2, keys, disp) {
  const selectedKinds = [...new Set(keys.map(kindFromKey).filter(Boolean))];
  return (vocab2.kinds ?? []).filter((k) => selectedKinds.includes(k.kind)).some((k) => (k.needs_until ?? []).includes(disp));
}
var DANGER_DISPS = /* @__PURE__ */ new Set(["close", "delete", "archive"]);
function openDecideSheet(ctx, keys, onDone, seed, customPost) {
  void getVocab(ctx).then((vocab2) => {
    openDecideSheetWithVocab(ctx, keys, vocab2, onDone, seed, customPost);
  });
}
function openDecideSheetWithVocab(ctx, keys, vocab2, onDone, seed, customPost) {
  const n = keys.length;
  const allowed = allowedForKeys(vocab2, keys);
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
    placeholder: (vocab2.until_forms ?? []).map((f) => f.syntax).join(", ") || "date(YYYY-MM-DD), inactive(90d) …"
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
  untilRow.hidden = !seed?.until && !dispositionNeedsUntil(vocab2, keys, disposition);
  const errEl = h2("p", { class: "cb-sheet-err" });
  errEl.hidden = true;
  async function runDryRun() {
    if (!disposition) return;
    if (!dispositionNeedsUntil(vocab2, keys, disposition)) return;
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
          const needsUntil = dispositionNeedsUntil(vocab2, keys, d);
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
    if (dispositionNeedsUntil(vocab2, keys, disposition)) {
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
      let errors2 = [];
      if (customPost) {
        decidedKeys = await customPost(disposition, until, note);
      } else {
        const payload = { keys, disposition };
        if (until) payload["until"] = until;
        if (note) payload["note"] = note;
        const result = await ctx.api.post("/decide", payload);
        decidedKeys = result.decided_keys ?? [];
        errors2 = result.errors ?? [];
      }
      if (decidedKeys.length > 0) {
        onDone(decidedKeys);
      }
      if (errors2.length > 0) {
        errEl.textContent = errors2.join("; ");
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
  return {
    keys: "d",
    label: "decide selection",
    group: "page",
    run() {
      openSheetForSelection();
    }
  };
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
  void getVocab(ctx).then((vocab2) => {
    const vocabKind = (vocab2.kinds ?? []).find((k) => k.kind === kind);
    const kindAllowed = allowedForKind(vocab2, kind);
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
    const card5 = h5(
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
      card5.append(ageEl);
    }
    if (it.proposal) {
      const propEl = h5("div", { class: "cb-card-prop" });
      propEl.textContent = `${agentFromSource(it.proposal.source)} proposes ${it.proposal.disposition}`;
      card5.append(propEl);
    }
    titleEl.addEventListener("click", (e) => {
      e.stopPropagation();
      onOpen?.(it.key, laneId);
      ctx.route.go("item", it.key);
    });
    card5.addEventListener("click", (e) => {
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
    card5.addEventListener("keydown", (e) => {
      if (e.key === "o" || e.key === "Enter") {
        e.preventDefault();
        onOpen?.(it.key, laneId);
        ctx.route.go("item", it.key);
      }
    });
    return card5;
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
      cards.forEach((card5, i) => {
        const it = state.items[i];
        if (!it) return;
        const selected = sel.has(it.key);
        card5.classList.toggle("on", selected);
        card5.querySelector(".kit-box")?.classList.toggle("on", selected);
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
  const viewCounts2 = {};
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
      count: viewCounts2[v.id]
    }));
  }
  function applyCounts(counts) {
    if (!counts) return;
    for (const v of VIEWS) {
      if (v.id !== "board") viewCounts2[v.id] = 0;
    }
    Object.assign(viewCounts2, counts);
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
  let decidePrimary = null;
  const decideKey = wireSelection(
    {
      ...ctx,
      setPrimary(p) {
        decidePrimary = p;
        if (active) ctx.setPrimary(p);
      }
    },
    handle
  );
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
  const acceptKey = {
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
  };
  const rejectKey = {
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
  };
  const searchKey = {
    keys: "/",
    label: "search",
    group: "page",
    run() {
      handle.focusSearch?.();
    }
  };
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
    keys: [decideKey, acceptKey, rejectKey, searchKey],
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
      return decidePrimary;
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
  const h12 = Math.floor(m / 60);
  if (h12 < 24) return `${h12}h`;
  const d = Math.floor(h12 / 24);
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
  return !a.keys?.length && !a.open && !a.rule && !a.job && !a.section;
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
  if (a.section && parts.length === 0) parts.push(`section ${a.section}`);
  return parts.join(" · ");
}
function attachedText(a) {
  const words = [...a.keys ?? []];
  if (a.open && !(a.keys?.length === 1 && a.keys[0] === a.open)) {
    words.push(`open ${a.open}`);
  }
  if (a.rule) words.push(`rule ${a.rule}`);
  if (a.job) words.push(`job ${a.job}`);
  if (a.section) words.push(`section ${a.section}`);
  return words.join(" ");
}
function parseAttached(text) {
  const words = text.split(/[\s,]+/).map((w) => w.trim()).filter(Boolean);
  const out = {};
  const keys = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    const next = words[i + 1];
    if ((w === "rule" || w === "job" || w === "open" || w === "section") && next) {
      if (w === "rule") out.rule = next;
      else if (w === "section") out.section = next;
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
  function restoreSent(sent) {
    const typed = input.value.replace(/^\s+/, "");
    input.value = typed ? `${sent.replace(/\s+$/, "")} ${typed}` : sent;
    fit();
  }
  async function send(batch) {
    const sent = input.value;
    const body = sent.trim();
    if (!body || sending) return;
    sending = true;
    note.textContent = "";
    input.value = "";
    fit();
    let posted = false;
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
      posted = true;
      override = null;
      renderAttached();
    } catch (err) {
      note.textContent = "not sent";
      console.error("[composer] send:", err);
    } finally {
      if (!posted) restoreSent(sent);
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
  const list3 = h8("div", { class: "cb-batch-drafts" });
  const el = card2({
    body: h8(
      "div",
      {},
      h8("div", { class: "cb-batch-head" }, label, sendBtn),
      list3
    )
  });
  el.classList.add("cb-batch");
  el.setAttribute("data-testid", "batch-tray");
  el.hidden = true;
  function render() {
    el.hidden = drafts.length === 0;
    if (!drafts.length) {
      list3.replaceChildren();
      return;
    }
    label.textContent = `batch · ${pluralize(drafts.length, "draft")}`;
    sendBtn.textContent = `send ${drafts.length}`;
    list3.replaceChildren(...drafts.map((d, i) => draftRow(d, i)));
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
      sheet4.close();
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
  const sheet4 = {
    el: h9("div", { class: "cb-dock-pick-sheet" }, content),
    close() {
      this.el.remove();
    }
  };
  document.body.append(sheet4.el);
  function onKey(e) {
    if (e.key === "Escape") {
      sheet4.close();
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
      sheet4.close();
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
  const sheet4 = {
    el: h9("div", { class: "cb-dock-pick-sheet" }, content),
    close() {
      this.el.remove();
    }
  };
  document.body.append(sheet4.el);
  function onKey(e) {
    if (e.key === "Escape") {
      sheet4.close();
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
    ctx.setAgentName(sess?.harness ?? "");
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

// rules.ts
import {
  list as list2,
  h as h11,
  facts as facts2,
  buttons,
  card as card4,
  sheet as sheet3,
  ApiError
} from "/_kit/kit.js";

// conditions.ts
import { h as h10 } from "/_kit/kit.js";

// menu-nav.ts
function menuNav(menu, selector, opener, close) {
  const items = () => [...menu.querySelectorAll(selector)];
  const rove = (to) => {
    for (const it of items()) it.tabIndex = it === to ? 0 : -1;
  };
  menu.addEventListener("focusin", (e) => {
    const t = e.target;
    if (t.matches(selector)) rove(t);
  });
  menu.addEventListener("keydown", (e) => {
    if (e.isComposing || e.metaKey || e.ctrlKey || e.altKey) return;
    const list3 = items();
    if (!list3.length) return;
    const at2 = list3.indexOf(document.activeElement);
    let next = -1;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowRight":
        next = at2 < 0 ? 0 : (at2 + 1) % list3.length;
        break;
      case "ArrowUp":
      case "ArrowLeft":
        next = at2 < 0 ? list3.length - 1 : (at2 - 1 + list3.length) % list3.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = list3.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    e.stopPropagation();
    rove(list3[next]);
    list3[next].focus();
  });
  menu.addEventListener("focusout", (e) => {
    if (menu.hidden) return;
    const to = e.relatedTarget;
    if (to && (menu.contains(to) || to === opener)) return;
    close();
  });
}

// conditions.ts
var ENUM = 0;
var DURATION = 2;
var BOOL = 3;
var COUNT = 4;
var vocab = null;
var errors = /* @__PURE__ */ new WeakMap();
function ruleVocabularyView(ctx) {
  if (!vocab) {
    vocab = ctx.api.get("/rules/vocabulary");
    vocab.catch(() => {
      vocab = null;
    });
  }
  return vocab;
}
function ruleVocabulary(ctx) {
  return ruleVocabularyView(ctx).then((v) => v.fields ?? []);
}
function noteTokens(ctx) {
  return ruleVocabularyView(ctx).then((v) => v.note_tokens ?? []);
}
function picks(f, op) {
  if (f.type === BOOL) return true;
  return f.type === ENUM && (f.values?.length ?? 0) > 0 && /^is/.test(op);
}
function choices(f) {
  return f.type === BOOL ? ["true", "false"] : f.values ?? [];
}
function placeholder(f, op) {
  if (f.type === DURATION) return "<n>h, <n>d or <n>w";
  if (f.type === COUNT) return "a count, e.g. 0";
  if (op === "in" || op === "not-in") return "a, b, c";
  if (op === "matches") return "a regular expression";
  return "";
}
function firstValue(f, op) {
  return picks(f, op) ? choices(f)[0] ?? "" : "";
}
function select(cls, label, options, value, onPick) {
  const el = h10(
    "select",
    { class: cls, "aria-label": label },
    ...options.map((o) => h10("option", { value: o }, o))
  );
  if (!options.includes(value)) {
    el.prepend(h10("option", { value }, value));
  }
  el.value = value;
  el.addEventListener("change", () => onPick(el.value));
  return el;
}
function conditionEditor(ctx, rule, onChange) {
  const editable = rule.editable ?? rule.status === "draft";
  const conds = (rule.match ?? []).map((c) => ({ ...c }));
  let fields = [];
  const fieldOf = (name) => fields.find((f) => f.name === name);
  const el = h10("div", {
    class: "kit-table cb-conds",
    "data-testid": "conditions"
  });
  const rowsEl = h10("div", { class: "cb-cond-rows" });
  const general = h10("div", { class: "cb-cond-err", hidden: true });
  el.append(rowsEl, general);
  const changed = () => onChange(conds.map((c) => ({ ...c })));
  function valueControl(i, f) {
    const c = conds[i];
    if (!editable || !f) {
      return h10("span", { class: "cb-cond-v" }, c.value);
    }
    if (picks(f, c.op)) {
      return select(
        "cb-cond-v",
        `${c.field} value`,
        choices(f),
        c.value,
        (v) => {
          c.value = v;
          changed();
        }
      );
    }
    const input = h10("input", {
      class: "cb-cond-v",
      type: "text",
      value: c.value,
      placeholder: placeholder(f, c.op),
      spellcheck: false,
      "aria-label": `${c.field} value`
    });
    input.addEventListener("input", () => {
      c.value = input.value;
      changed();
    });
    return input;
  }
  function row(i) {
    const c = conds[i];
    const f = fieldOf(c.field);
    const op = editable && f ? select("cb-cond-o", `${c.field} operator`, f.ops ?? [], c.op, (v) => {
      const wasPick = picks(f, c.op);
      c.op = v;
      if (picks(f, v) !== wasPick) {
        c.value = firstValue(f, v);
        render();
      }
      changed();
    }) : h10("span", { class: "cb-cond-o" }, c.op);
    const remove = editable ? h10(
      "button",
      {
        type: "button",
        class: "cb-cond-rm",
        "aria-label": `remove ${c.field} ${c.op}`,
        onclick() {
          conds.splice(i, 1);
          render();
          changed();
        }
      },
      "×"
    ) : null;
    return h10(
      "div",
      { class: "cb-cond", "data-index": i },
      h10(
        "div",
        { class: "kit-tr cb-cond-row" + (editable ? " cb-cond-edit" : "") },
        h10("span", { class: "cb-cond-f" }, c.field),
        op,
        valueControl(i, f),
        remove
      ),
      h10("div", { class: "cb-cond-err", hidden: true })
    );
  }
  function render() {
    rowsEl.replaceChildren(...conds.map((_, i) => row(i)));
    const e = errors.get(el);
    if (e) setConditionError(el, e.index, e.message);
  }
  const menu = h10("div", {
    class: "cb-cond-menu",
    role: "menu",
    "aria-label": "add a condition",
    hidden: true
  });
  const add = h10(
    "button",
    {
      type: "button",
      class: "cb-cond-add",
      "aria-expanded": "false",
      onclick() {
        setMenu(menu.hidden);
      }
    },
    "+ condition"
  );
  function onEsc(e) {
    if (e.key !== "Escape" || e.isComposing) return;
    if (!el.isConnected) {
      document.removeEventListener("keydown", onEsc, true);
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    setMenu(false);
    add.focus();
  }
  function setMenu(open) {
    menu.hidden = !open;
    add.setAttribute("aria-expanded", String(open));
    if (open) {
      document.addEventListener("keydown", onEsc, true);
      menu.querySelector(".cb-cond-menu-op")?.focus();
    } else {
      document.removeEventListener("keydown", onEsc, true);
    }
  }
  menuNav(menu, ".cb-cond-menu-op", add, () => setMenu(false));
  function addCondition(f, op) {
    conds.push({ field: f.name, op, value: firstValue(f, op) });
    setMenu(false);
    render();
    changed();
    const last = rowsEl.lastElementChild;
    last?.querySelector(".cb-cond-v")?.focus();
  }
  function fillMenu() {
    menu.replaceChildren(
      ...fields.map(
        (f) => h10(
          "div",
          { class: "cb-cond-menu-row", "data-field": f.name },
          h10("span", { class: "cb-cond-menu-f" }, f.name),
          h10(
            "span",
            { class: "cb-cond-menu-ops" },
            ...(f.ops ?? []).map(
              (op) => h10(
                "button",
                {
                  type: "button",
                  role: "menuitem",
                  class: "kit-chip cb-cond-menu-op",
                  "data-op": op,
                  onclick() {
                    addCondition(f, op);
                  }
                },
                op
              )
            )
          )
        )
      )
    );
  }
  render();
  if (editable) {
    el.append(add, menu);
    void ruleVocabulary(ctx).then((fs) => {
      fields = fs;
      fillMenu();
      render();
      el.dataset.ready = "true";
    }).catch((err) => {
      general.textContent = err instanceof Error ? err.message : "the vocabulary did not load";
      general.hidden = false;
    });
  } else {
    el.dataset.ready = "true";
  }
  return el;
}
function setConditionError(editor, index, message2) {
  if (message2 === null) errors.delete(editor);
  else errors.set(editor, { index, message: message2 });
  for (const e of editor.querySelectorAll(".cb-cond-err")) {
    e.hidden = true;
    e.textContent = "";
  }
  for (const r2 of editor.querySelectorAll(".cb-cond[data-invalid]")) {
    r2.removeAttribute("data-invalid");
  }
  if (message2 === null) return;
  const r = editor.querySelector(
    `.cb-cond[data-index="${index}"]`
  );
  const slot = r ? r.querySelector(".cb-cond-err") : editor.querySelector(":scope > .cb-cond-err");
  r?.setAttribute("data-invalid", "");
  if (slot) {
    slot.textContent = message2;
    slot.hidden = false;
  }
}

// rules-text.ts
function trackRecord(r) {
  const parts = [];
  if (r.accepted) parts.push(`${r.accepted} accepted`);
  if (r.rejected) parts.push(`${r.rejected} rejected`);
  if (r.pending) parts.push(`${r.pending} pending`);
  return parts.join(" · ");
}
function rowKicker(row) {
  const rec = trackRecord(row.record);
  const head = row.invalid ? "not valid" : `${row.matches} match · ${row.excluded} excluded`;
  return rec ? `${head} · ${rec}` : head;
}
function sameAction(a, b) {
  return a.disposition === b.disposition && (a.until ?? "") === (b.until ?? "") && (a.note ?? "") === (b.note ?? "");
}
function authorOf(createdBy) {
  const i = createdBy.indexOf(":");
  if (i > 0) return { agent: true, name: createdBy.slice(0, i) };
  return { agent: false, name: "you" };
}
function reasonSummary(by) {
  const rs = by ?? [];
  if (rs.length === 1 && rs[0].reason === "matched") return "";
  return rs.map((r) => `${r.count} ${r.reason.toLowerCase()}`).join(" · ");
}
function matchesHeading(total, by) {
  const why = reasonSummary(by);
  return `matches now · ${total}` + (why ? ` · ${why}` : "");
}
function conditionErrorIndex(message2) {
  const m = /^condition (\d+): /.exec(message2);
  const n = m ? Number(m[1]) : 0;
  return n >= 1 ? n - 1 : -1;
}
function viewCounts(rules) {
  let active = 0;
  for (const r of rules) if (r.status === "active") active++;
  return { all: rules.length, active, drafts: rules.length - active };
}
function sameConditions(a, b) {
  if (a.length !== b.length) return false;
  return a.every(
    (c, i) => c.field === b[i].field && c.op === b[i].op && c.value === b[i].value
  );
}
function sameRule(a, b) {
  const ex = (r) => (r.exclude ?? []).map((x) => `${x.key}\0${x.reason ?? ""}`).join("\n");
  return a.id === b.id && a.name === b.name && a.status === b.status && a.created_by === b.created_by && a.edited_at === b.edited_at && sameConditions(a.match ?? [], b.match ?? []) && sameAction(a.propose, b.propose) && ex(a) === ex(b);
}
function hasKindCondition(conds) {
  return conds.some((c) => c.field === "kind");
}
function orList(words) {
  if (words.length <= 1) return words.join("");
  return `${words.slice(0, -1).join(", ")} or ${words[words.length - 1]}`;
}
function kindHint(conds, allowed, all) {
  if (hasKindCondition(conds) || !allowed.length) return "";
  const missing = [...new Set(all)].filter((d) => !allowed.includes(d)).sort();
  return missing.length ? `add a kind condition to propose ${orList(missing)}` : "";
}
function refusedDisposition(d, conds, allowed) {
  const what = hasKindCondition(conds) ? "what this rule matches" : "every kind of item (the rule has no kind condition)";
  return `${d} can’t be proposed for ${what} (allowed: ${allowed.join(", ")})`;
}

// rules.ts
var HINT_SEP = " · ";
var PREVIEW_DEBOUNCE_MS = 300;
var PAGE = 200;
var VIEWS2 = [
  { id: "all", label: "all" },
  { id: "active", label: "active" },
  { id: "drafts", label: "drafts" }
];
function message(err) {
  return err instanceof Error ? err.message : String(err);
}
var isConflict = (err) => err instanceof ApiError && err.status === 409;
function slug(name) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60);
}
function renderRule(ctx, detail, hooks) {
  const id = detail.rule.id;
  let saved = detail;
  let base = detail.rule;
  let work = [...base.match ?? []];
  let workPropose = { ...base.propose };
  let exclude = base.exclude ?? [];
  let preview = detail.matches;
  let dispositions = detail.matches.dispositions ?? [];
  let shown = preview.page ?? [];
  let invalid = null;
  let conflict = null;
  let grouped = false;
  let seq = 0;
  let timer = null;
  let busy = false;
  const doc = h11("article", { class: "cb-rule", "data-rule": id });
  const el = h11("div", { class: "kit-doc" }, doc);
  const dirty = () => !sameConditions(work, base.match ?? []) || !sameAction(workPropose, base.propose);
  const isDraft = () => base.status !== "active";
  const editable = () => isDraft() || !!saved.invalid;
  const body = () => ({
    ...base,
    match: work,
    propose: workPropose,
    exclude
  });
  let factsEl = h11("div");
  let editor = h11("div");
  const titleEl = h11("h1", { class: "kit-h1" });
  const kickEl = h11("p", { class: "kit-kick" });
  const proseEl = h11("p");
  const invalidEl = h11("div", { "data-testid": "rule-invalid" });
  const conflictEl = h11("div", { "data-testid": "rule-conflict" });
  const proposeEl = h11("div");
  const proposeErrEl = h11("p", {
    class: "cb-cond-err cb-propose-err",
    "data-testid": "propose-err",
    hidden: true
  });
  const heading = h11("h3", { class: "kit-label cb-matches-label" });
  const groupChips = h11("span", { class: "cb-matches-view" });
  const matchesEl = h11("div", {
    class: "kit-table cb-matches",
    "data-testid": "matches"
  });
  const actionsEl = h11("div", { class: "cb-rule-actions" });
  const note = h11("p", { class: "cb-rule-note", role: "status" });
  function drawFacts() {
    const who = authorOf(base.created_by);
    const byEl = h11("b", who.agent ? { class: "cb-by-agent" } : null, who.name);
    const next = facts2([
      ["status", saved.invalid ? `${base.status} · not valid` : base.status],
      ["matches", invalid ? "—" : String(preview.total)],
      ["excluded", String(exclude.length)],
      ["by", byEl]
    ]);
    factsEl.replaceWith(next);
    factsEl = next;
  }
  function drawProse() {
    const who = authorOf(base.created_by);
    const lifecycle2 = "Once active, casebook proposes new matches after every sync, and never overrides an item that’s already decided.";
    if (!isDraft()) {
      proseEl.replaceChildren(
        "Active: casebook proposes new matches after every sync, and never overrides an item that’s already decided."
      );
    } else if (who.agent) {
      proseEl.replaceChildren(
        h11("span", { class: "cb-by-agent" }, who.name),
        " drafted this rule. It proposes nothing until you activate it, and only you can. ",
        lifecycle2
      );
    } else {
      proseEl.replaceChildren(
        "A draft proposes nothing until you activate it. ",
        lifecycle2
      );
    }
  }
  function drawInvalid() {
    if (!saved.invalid) {
      invalidEl.replaceChildren();
      return;
    }
    const skipped = base.status === "active";
    invalidEl.replaceChildren(
      card4({
        edge: "signal",
        head: skipped ? "not valid · active, but skipped at every sync" : "not valid",
        body: h11(
          "div",
          null,
          h11("p", { class: "cb-invalid-msg" }, saved.invalid),
          h11(
            "p",
            { class: "cb-invalid-help" },
            skipped ? "casebook proposes nothing from this rule until it is valid. Fix it and save it (it becomes a draft), or deactivate it." : "It can’t be activated or propose anything until it is valid."
          )
        )
      })
    );
  }
  function drawPropose() {
    const p = workPropose;
    const tr = (label, value) => h11(
      "div",
      { class: "kit-tr" },
      h11("span", { class: "cb-cond-f" }, label),
      h11("span"),
      value
    );
    if (!editable()) {
      drawChoices = () => {
      };
      proposeEl.replaceChildren(
        h11(
          "div",
          { class: "kit-table cb-propose", "data-testid": "propose" },
          tr(
            "disposition",
            h11(
              "span",
              {
                class: "cb-cond-v" + (DANGER_DISPS.has(p.disposition) ? " cb-danger" : "")
              },
              p.disposition
            )
          ),
          p.until ? tr("until", h11("span", { class: "cb-cond-v" }, p.until)) : null,
          p.note ? tr("note", h11("span", { class: "cb-cond-v" }, p.note)) : null
        )
      );
      return;
    }
    const pick = h11("button", {
      type: "button",
      class: "cb-cond-v cb-disp",
      "aria-label": "disposition",
      "aria-haspopup": "menu",
      "aria-expanded": "false",
      onclick() {
        setMenu(menu.hidden);
      }
    });
    const menu = h11("div", {
      class: "cb-disp-menu",
      role: "menu",
      "aria-label": "dispositions",
      hidden: true
    });
    const menuChips = h11("span", { class: "cb-disp-opts" });
    const whyEl = h11("span", {
      class: "cb-disp-why",
      "data-testid": "disp-why"
    });
    menu.append(
      h11("span"),
      h11("span"),
      h11("span", { class: "cb-disp-col" }, menuChips, whyEl)
    );
    menuNav(menu, ".cb-disp-opt", pick, () => setMenu(false));
    const onEsc = (e) => {
      if (e.key !== "Escape" || e.isComposing) return;
      if (!menu.isConnected) {
        document.removeEventListener("keydown", onEsc, true);
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      setMenu(false);
      pick.focus();
    };
    function setMenu(open) {
      menu.hidden = !open;
      pick.setAttribute("aria-expanded", String(open));
      if (open) {
        document.addEventListener("keydown", onEsc, true);
        (menu.querySelector(".cb-disp-opt.on") ?? menu.querySelector(".cb-disp-opt"))?.focus();
      } else {
        document.removeEventListener("keydown", onEsc, true);
      }
    }
    const needsUntil = () => !!workPropose.until || /^(wait|watch)$/.test(workPropose.disposition);
    const choose = (d) => {
      workPropose = { ...workPropose, disposition: d };
      setMenu(false);
      drawChoices();
      untilRow.hidden = untilHint.hidden = !needsUntil();
      edited();
      pick.focus();
    };
    drawChoices = () => {
      const cur = workPropose.disposition;
      pick.textContent = cur || "choose…";
      pick.dataset.value = cur;
      pick.classList.toggle("cb-danger", DANGER_DISPS.has(cur));
      menuChips.replaceChildren(
        ...dispositions.map(
          (d) => h11(
            "button",
            {
              type: "button",
              role: "menuitemradio",
              "aria-checked": String(d === cur),
              class: "kit-chip cb-disp-opt" + (d === cur ? " on" : "") + (DANGER_DISPS.has(d) ? " cb-danger" : ""),
              "data-disp": d,
              onclick() {
                choose(d);
              }
            },
            d
          )
        )
      );
      whyEl.textContent = kindHint(work, dispositions, allDispositions);
      drawProposeErr();
    };
    const text = (field, placeholder2) => {
      const input = h11("input", {
        class: "cb-cond-v",
        type: "text",
        value: p[field] ?? "",
        placeholder: placeholder2,
        spellcheck: false,
        "aria-label": field
      });
      input.addEventListener("input", () => {
        const v = input.value;
        workPropose = { ...workPropose, [field]: v || void 0 };
        drawProposeErr();
        edited();
      });
      return input;
    };
    const hint = (testid) => h11(
      "div",
      { class: "cb-prop-hint", "data-testid": testid },
      h11("span"),
      h11("span"),
      h11("span", { class: "cb-prop-hint-t" })
    );
    const untilIn = text("until", "");
    const untilRow = tr("until", untilIn);
    const untilHint = hint("until-hint");
    untilRow.hidden = untilHint.hidden = !needsUntil();
    const noteIn = text("note", "optional");
    const noteHint = hint("note-hint");
    const items = (el2, lead, words) => {
      const out = [];
      words.forEach((w, i) => {
        if (i) out.push(HINT_SEP);
        out.push(h11("span", { class: "cb-prop-hint-i" }, w));
      });
      el2.lastElementChild.replaceChildren(
        ...words.length ? [lead, ...out] : []
      );
    };
    void getVocab(ctx).then((v) => {
      allDispositions = (v.kinds ?? []).flatMap((k) => k.allowed ?? []);
      drawChoices();
      const forms = v.until_forms ?? [];
      if (forms[0]) untilIn.placeholder = `e.g. ${forms[0].example}`;
      items(
        untilHint,
        "",
        forms.map((f) => f.syntax)
      );
    }).catch(() => {
    });
    void noteTokens(ctx).then((ts) => {
      items(
        noteHint,
        "may use ",
        ts.map((t) => `${t.token} ${t.meaning}`)
      );
    }).catch(() => {
    });
    proposeEl.replaceChildren(
      h11(
        "div",
        { class: "kit-table cb-propose", "data-testid": "propose" },
        tr("disposition", pick),
        menu,
        untilRow,
        untilHint,
        tr("note", noteIn),
        noteHint
      ),
      proposeErrEl
    );
    drawChoices();
  }
  let drawChoices = () => {
  };
  let allDispositions = [];
  function drawProposeErr() {
    let msg = "";
    const d = workPropose.disposition;
    if (saved.invalid?.startsWith("propose:") && sameAction(workPropose, base.propose) && sameConditions(work, base.match ?? [])) {
      msg = saved.invalid;
    } else if (d && dispositions.length && !dispositions.includes(d)) {
      msg = refusedDisposition(d, work, dispositions);
    }
    proposeErrEl.textContent = msg;
    proposeErrEl.hidden = !msg;
  }
  function box(on, label, run) {
    return h11("span", {
      class: "kit-box",
      role: "checkbox",
      tabindex: 0,
      "aria-checked": String(on),
      "aria-label": label,
      onclick(e) {
        e.stopPropagation();
        run();
      },
      onkeydown(e) {
        if (e.key === " " || e.key === "Enter") {
          e.preventDefault();
          run();
        }
      }
    });
  }
  function matchRow(m) {
    const why = m.reason === "matched" ? m.title || m.status : m.reason.toLowerCase();
    return h11(
      "div",
      { class: "cb-mr on", "data-key": m.key, title: m.title || m.key },
      box(true, `exclude ${m.key}`, () => untick(m.key)),
      h11("span", { class: "cb-mr-k" }, keyWithoutKind(m.key)),
      h11("span", { class: "cb-mr-w" }, why)
    );
  }
  function excludedRow(x) {
    return h11(
      "div",
      { class: "cb-mr x", "data-key": x.key },
      box(false, `include ${x.key}`, () => void include(x.key)),
      h11("span", { class: "cb-mr-k" }, keyWithoutKind(x.key)),
      h11(
        "span",
        { class: "cb-mr-w" },
        x.reason ? `excluded · ${x.reason}` : "excluded"
      )
    );
  }
  function waitRow(text) {
    return h11(
      "div",
      { class: "cb-mr cb-mr-wait" },
      h11("span"),
      h11("span", { class: "cb-mr-k" }, text),
      h11("span")
    );
  }
  function drawMatches() {
    matchesEl.removeAttribute("data-invalid");
    if (invalid) {
      heading.textContent = "matches now";
      matchesEl.setAttribute("data-invalid", "");
      matchesEl.replaceChildren(
        waitRow("not previewed: the conditions aren’t valid")
      );
      groupChips.hidden = true;
      return;
    }
    heading.textContent = matchesHeading(preview.total, preview.by_reason);
    groupChips.hidden = false;
    const out = [];
    if (grouped) {
      const rest = [...shown];
      for (const g of preview.groups ?? []) {
        const mine = rest.filter((m) => (m.repo || m.key) === g.repo);
        for (const m of mine) rest.splice(rest.indexOf(m), 1);
        const why = reasonSummary(g.reasons);
        out.push(
          h11(
            "div",
            { class: "cb-mgroup", "data-repo": g.repo },
            h11("span", { class: "cb-mgroup-repo" }, g.repo),
            h11(
              "span",
              { class: "cb-mr-w" },
              String(g.count) + (why ? ` · ${why}` : "")
            )
          ),
          ...mine.map(matchRow)
        );
      }
      out.push(...rest.map(matchRow));
    } else {
      out.push(...shown.map(matchRow));
    }
    out.push(...exclude.map(excludedRow));
    const more = preview.total - shown.length;
    if (more > 0) {
      out.push(
        h11(
          "div",
          { class: "cb-mr cb-mr-more" },
          h11("span"),
          h11("span", { class: "cb-mr-k" }, `… ${more} more`),
          h11(
            "button",
            {
              type: "button",
              class: "cb-link",
              onclick() {
                void loadMore();
              }
            },
            `show ${Math.min(PAGE, more)} more`
          )
        )
      );
    }
    if (out.length === 0) out.push(waitRow("nothing matches now"));
    matchesEl.replaceChildren(...out);
  }
  function drawGroupChips() {
    const chip = (label, on) => h11(
      "button",
      {
        type: "button",
        class: "kit-chip" + (on ? " on" : ""),
        "aria-pressed": String(on),
        onclick() {
          grouped = label === "by repo";
          drawGroupChips();
          drawMatches();
        }
      },
      label
    );
    groupChips.replaceChildren(
      chip("list", !grouped),
      chip("by repo", grouped)
    );
  }
  function setPreview(p) {
    invalid = null;
    setConditionError(editor, -1, null);
    preview = p;
    shown = p.page ?? [];
    if (p.dispositions) dispositions = p.dispositions;
    drawChoices();
    drawMatches();
    drawFacts();
  }
  function setInvalid(msg) {
    invalid = msg;
    setConditionError(editor, conditionErrorIndex(msg), msg);
    drawMatches();
    drawFacts();
  }
  async function previewNow(depth = PAGE) {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    const mine = ++seq;
    try {
      const b = body();
      const p = await ctx.api.post("/rules/preview", b);
      let rows = p.page ?? [];
      while (rows.length < Math.min(depth, p.total)) {
        const next = await ctx.api.post(
          `/rules/preview?offset=${rows.length}&limit=${PAGE}`,
          b
        );
        if (mine !== seq || !(next.page ?? []).length) break;
        rows = [...rows, ...next.page ?? []];
      }
      if (mine === seq) setPreview({ ...p, page: rows });
    } catch (err) {
      if (mine !== seq) return;
      if (err instanceof ApiError && err.status === 400)
        setInvalid(err.message);
      else note.textContent = `preview failed: ${message(err)}`;
    }
  }
  function schedule(keepDepth = false) {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      void previewNow(keepDepth ? Math.max(PAGE, shown.length) : PAGE);
    }, PREVIEW_DEBOUNCE_MS);
  }
  async function loadMore() {
    const q = `?offset=${shown.length}&limit=${PAGE}`;
    const mine = seq;
    try {
      const p = await ctx.api.post(`/rules/preview${q}`, body());
      if (mine !== seq) return;
      shown = [...shown, ...p.page ?? []];
      preview = { ...p, page: shown };
      drawMatches();
    } catch (err) {
      note.textContent = `more matches failed: ${message(err)}`;
    }
  }
  function applyExclusions(d) {
    exclude = d.rule.exclude ?? [];
    base = { ...base, exclude };
    if (dirty() || invalid) {
      void previewNow(Math.max(PAGE, shown.length));
    } else {
      seq++;
      setPreview(d.matches);
    }
    drawFacts();
  }
  function untick(key) {
    const reason = h11("input", {
      class: "kit-note",
      type: "text",
      placeholder: "reason (optional)",
      "aria-label": "reason"
    });
    const err = h11("p", { class: "cb-sheet-err", hidden: true });
    let sending = false;
    const go2 = async () => {
      if (sending) return;
      sending = true;
      try {
        const d = await ctx.api.post("/rules/exclude", {
          id,
          key,
          reason: reason.value.trim()
        });
        sh.close();
        applyExclusions(d);
        hooks.saved();
      } catch (e) {
        err.textContent = message(e);
        err.hidden = false;
        sending = false;
      }
    };
    reason.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.isComposing) {
        e.preventDefault();
        void go2();
      }
    });
    const sh = sheet3({
      title: `exclude ${keyWithoutKind(key)}`,
      body: h11(
        "div",
        { class: "cb-sheet-body" },
        h11(
          "p",
          { class: "cb-sheet-preview" },
          "This rule skips it from now on. It stays listed here, unticked."
        ),
        h11(
          "div",
          { class: "cb-sheet-row" },
          h11("label", { class: "cb-sheet-label" }, "reason"),
          reason
        ),
        err
      ),
      actions: [
        { label: "exclude", fill: true, run: () => void go2() },
        { label: "cancel", run: () => sh.close() }
      ]
    });
    sh.el.dataset.testid = "exclude-sheet";
    reason.focus();
  }
  async function include(key) {
    try {
      const d = await ctx.api.post("/rules/include", {
        id,
        key
      });
      applyExclusions(d);
      hooks.saved();
    } catch (err) {
      note.textContent = `not included: ${message(err)}`;
    }
  }
  function drawConflict() {
    if (!conflict) {
      conflictEl.replaceChildren();
      return;
    }
    const theirs = conflict.detail.rule;
    const who = conflict.by ? authorOf(conflict.by).name : "";
    const p = theirs.propose;
    const theirsBody = h11(
      "div",
      null,
      h11(
        "p",
        { class: "cb-conflict-help" },
        "Your edits are kept below and not saved. Serve’s copy now:"
      ),
      theirs.name !== base.name ? h11("p", { class: "cb-conflict-name" }, theirs.name) : null,
      conditionEditor(
        ctx,
        { match: theirs.match, status: "active", editable: false },
        () => {
        }
      ),
      h11(
        "div",
        { class: "kit-table cb-propose" },
        h11(
          "div",
          { class: "kit-tr" },
          h11("span", { class: "cb-cond-f" }, "disposition"),
          h11("span"),
          h11(
            "span",
            {
              class: "cb-cond-v" + (DANGER_DISPS.has(p.disposition) ? " cb-danger" : ""),
              "data-testid": "conflict-disposition"
            },
            p.disposition
          )
        ),
        p.until ? h11(
          "div",
          { class: "kit-tr" },
          h11("span", { class: "cb-cond-f" }, "until"),
          h11("span"),
          h11("span", { class: "cb-cond-v" }, p.until)
        ) : null,
        p.note ? h11(
          "div",
          { class: "kit-tr" },
          h11("span", { class: "cb-cond-f" }, "note"),
          h11("span"),
          h11("span", { class: "cb-cond-v" }, p.note)
        ) : null
      ),
      h11("p", { class: "cb-conflict-note", role: "status" })
    );
    conflictEl.replaceChildren(
      card4({
        edge: "signal",
        head: who ? `changed while you were editing · by ${who}` : "changed while you were editing",
        body: theirsBody,
        actions: [
          {
            label: "reload theirs",
            run() {
              const d = conflict.detail;
              conflict = null;
              draw(d);
            }
          },
          {
            label: "keep mine",
            run() {
              const d = conflict.detail;
              conflict = null;
              draw(d, { match: work, propose: workPropose });
            }
          }
        ]
      })
    );
  }
  function refuseActivate() {
    const n = conflictEl.querySelector(".cb-conflict-note");
    if (n) {
      n.textContent = "Activate waits: reload their copy, or keep yours over it, first.";
    }
    conflictEl.scrollIntoView?.({ block: "nearest" });
  }
  function refuseInvalid() {
    note.textContent = "not activated: fix what’s marked above";
    invalidEl.scrollIntoView?.({ block: "nearest" });
  }
  async function afterConflict(what) {
    try {
      const d = await ctx.api.get("/rule", { id });
      if (dirty()) {
        conflict = { detail: d, by: "" };
        drawConflict();
        drawActions();
        hooks.primaryChanged();
      } else {
        draw(d);
        note.textContent = `It changed before you ${what} it; nothing was ${what}. Here is serve’s copy.`;
      }
    } catch (err) {
      note.textContent = message(err);
    }
  }
  function saysWhatHeEdited(r) {
    return sameConditions(r.match ?? [], work) && sameAction(r.propose, workPropose);
  }
  function settle(d) {
    conflict = null;
    if (d) {
      draw(d);
      return;
    }
    drawConflict();
    drawActions();
    hooks.primaryChanged();
  }
  async function save() {
    if (conflict) {
      refuseActivate();
      return false;
    }
    try {
      const d = await ctx.api.post(
        `/rules/draft?version=${encodeURIComponent(saved.version)}`,
        body()
      );
      draw(d);
      hooks.saved();
      if (conflict) {
        const { detail: newer, by } = conflict;
        settle(newer);
        const who = by ? authorOf(by).name : "someone";
        note.textContent = `Saved; then ${who} changed it. Here is serve’s copy; nothing else was done.`;
        return false;
      }
      return true;
    } catch (err) {
      if (isConflict(err)) void afterConflict("saved");
      else if (err instanceof ApiError && err.status === 400) {
        if (conditionErrorIndex(err.message) >= 0) setInvalid(err.message);
        else note.textContent = err.message;
      } else note.textContent = `not saved: ${message(err)}`;
      return false;
    }
  }
  async function lifecycle(verb) {
    if (busy) return;
    if (verb === "activate" && conflict) {
      refuseActivate();
      return;
    }
    if (verb === "activate" && saved.invalid && !dirty()) {
      refuseInvalid();
      return;
    }
    busy = true;
    try {
      if (verb === "activate" && dirty() && !await save()) return;
      if (verb === "activate" && saved.invalid) {
        refuseInvalid();
        return;
      }
      const d = await ctx.api.post(
        `/rules/${verb}`,
        verb === "activate" ? { id, version: saved.version } : { id }
      );
      draw(d);
      hooks.saved();
    } catch (err) {
      if (isConflict(err)) void afterConflict(`${verb}d`);
      else note.textContent = `not ${verb}d: ${message(err)}`;
    } finally {
      busy = false;
    }
  }
  async function proposeOnce() {
    if (busy) return;
    busy = true;
    try {
      if (dirty() && !await save()) return;
      const r = await ctx.api.post("/rules/propose-once", {
        id
      });
      note.replaceChildren(
        `${pluralize(r.proposed, "match", "matches")} proposed · `,
        h11(
          "button",
          {
            type: "button",
            class: "cb-link",
            onclick() {
              ctx.route.go("attention", "proposed");
            }
          },
          "see them in attention"
        )
      );
      hooks.saved();
    } catch (err) {
      note.textContent = `not proposed: ${message(err)}`;
    } finally {
      busy = false;
    }
  }
  function drawActions() {
    const bs = [];
    if (editable() && dirty()) {
      bs.push({
        label: isDraft() ? "save draft" : "save as draft",
        run: () => void save()
      });
    }
    if (isDraft() && !saved.invalid) {
      bs.push({ label: "propose once", run: () => void proposeOnce() });
    }
    actionsEl.replaceChildren(bs.length ? buttons(bs) : "", note);
  }
  function edited() {
    note.textContent = "";
    if (conflict && (!dirty() || saysWhatHeEdited(conflict.detail.rule))) {
      settle(conflict.detail);
      return;
    }
    drawActions();
    hooks.primaryChanged();
  }
  function onEdit(m) {
    work = m;
    schedule();
    edited();
  }
  function draw(d, keep) {
    if (timer !== null) clearTimeout(timer);
    timer = null;
    seq++;
    saved = d;
    base = d.rule;
    if (conflict && conflict.detail.version === d.version) conflict = null;
    work = keep ? keep.match : [...base.match ?? []];
    workPropose = keep ? keep.propose : { ...base.propose };
    exclude = base.exclude ?? [];
    preview = d.matches;
    shown = preview.page ?? [];
    dispositions = d.matches.dispositions ?? [];
    invalid = d.invalid && !keep && (conditionErrorIndex(d.invalid) >= 0 || d.invalid.startsWith("rules/")) ? d.invalid : null;
    doc.dataset.status = base.status;
    doc.toggleAttribute("data-invalid", !!d.invalid);
    kickEl.textContent = `rule · ${base.status} · rules/${id}.toml`;
    titleEl.textContent = base.name || id;
    editor = conditionEditor(
      ctx,
      { match: work, status: base.status, editable: editable() },
      onEdit
    );
    factsEl = h11("div");
    doc.replaceChildren(
      kickEl,
      titleEl,
      factsEl,
      proseEl,
      invalidEl,
      conflictEl,
      h11("h3", { class: "kit-label" }, "when an undecided item matches all of"),
      editor,
      h11("h3", { class: "kit-label" }, "propose"),
      proposeEl,
      h11("div", { class: "cb-matches-head" }, heading, groupChips),
      matchesEl,
      actionsEl
    );
    note.textContent = "";
    drawProse();
    drawInvalid();
    drawConflict();
    drawPropose();
    drawFacts();
    drawGroupChips();
    drawMatches();
    if (invalid) setInvalid(invalid);
    drawActions();
    if (keep) schedule();
    hooks.primaryChanged();
  }
  draw(detail);
  return {
    el,
    id,
    dirty,
    primary() {
      if (!isDraft()) {
        return { label: "Deactivate", run: () => void lifecycle("deactivate") };
      }
      return { label: "Activate", run: () => void lifecycle("activate") };
    },
    update(d, by) {
      if (d.version === saved.version) {
        if (conflict) settle(null);
        const was = (saved.rule.exclude ?? []).map((x) => x.key).join("\n");
        const now = (d.rule.exclude ?? []).map((x) => x.key).join("\n");
        saved = { ...saved, record: d.record, invalid: d.invalid };
        if (was !== now || !sameRule(d.rule, base)) applyExclusions(d);
        return;
      }
      if (!dirty() || saysWhatHeEdited(d.rule)) {
        conflict = null;
        draw(d);
        return;
      }
      conflict = { detail: d, by };
      drawConflict();
      drawActions();
      hooks.primaryChanged();
    },
    refresh() {
      if (!invalid || dirty()) schedule(true);
    }
  };
}
function makeRules(ctx) {
  const readEl = h11("div", { class: "kit-read cb-rules-read" });
  let rows = [];
  let view = "all";
  let active = false;
  let openId = null;
  let doc = null;
  let asking = false;
  let opening = 0;
  let painting = false;
  let listSeq = 0;
  let liveSeq = 0;
  const statusOf = (r) => r.rule.status;
  const inView = (r) => view === "all" || (view === "active" ? statusOf(r) === "active" : statusOf(r) !== "active");
  const askEl = h11(
    "button",
    {
      type: "button",
      class: "cb-link cb-rules-ask",
      onclick() {
        asking = true;
        feed();
        ctx.focusComposer();
      }
    },
    ""
  );
  const nameAgent = () => {
    askEl.textContent = `or ask ${ctx.agentName() || "the agent"} to draft one`;
  };
  nameAgent();
  ctx.onAgentName(nameAgent);
  const foot = h11(
    "div",
    { class: "cb-rules-foot" },
    h11(
      "button",
      {
        type: "button",
        class: "kit-btn",
        "data-testid": "new-rule",
        onclick() {
          newRule();
        }
      },
      "new rule"
    ),
    askEl
  );
  function newRule() {
    const name = h11("input", {
      class: "kit-note",
      type: "text",
      placeholder: "e.g. Landed branches → delete",
      "aria-label": "name"
    });
    const idIn = h11("input", {
      class: "kit-note",
      type: "text",
      placeholder: "e.g. landed-branches",
      spellcheck: false,
      "aria-label": "id"
    });
    let idTyped = false;
    name.addEventListener("input", () => {
      if (!idTyped) idIn.value = slug(name.value);
    });
    idIn.addEventListener("input", () => {
      idTyped = true;
    });
    const err = h11("p", { class: "cb-sheet-err", hidden: true });
    let sending = false;
    const create = async () => {
      if (sending) return;
      const id = idIn.value.trim();
      const title = name.value.trim();
      if (!id || !title) {
        err.textContent = "A rule needs a name and an id.";
        err.hidden = false;
        return;
      }
      sending = true;
      try {
        await ctx.api.post("/rules/draft?create=1", {
          id,
          name: title,
          status: "draft",
          match: [],
          propose: { disposition: "" }
        });
        sh.close();
        await loadList();
        ctx.route.go("rules", id);
      } catch (e) {
        err.textContent = message(e);
        err.hidden = false;
        sending = false;
      }
    };
    for (const input of [name, idIn]) {
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter" && !e.isComposing) {
          e.preventDefault();
          void create();
        }
      });
    }
    const sh = sheet3({
      title: "new rule",
      body: h11(
        "div",
        { class: "cb-sheet-body" },
        h11(
          "div",
          { class: "cb-sheet-row" },
          h11("label", { class: "cb-sheet-label" }, "name"),
          name
        ),
        h11(
          "div",
          { class: "cb-sheet-row" },
          h11("label", { class: "cb-sheet-label" }, "id"),
          idIn
        ),
        h11(
          "p",
          { class: "cb-sheet-preview" },
          "A draft: it proposes nothing until you activate it."
        ),
        err
      ),
      actions: [
        { label: "create", fill: true, run: () => void create() },
        { label: "cancel", run: () => sh.close() }
      ]
    });
    sh.el.dataset.testid = "new-rule-sheet";
    name.focus();
  }
  const handle = list2({
    label: "rules",
    views: VIEWS2.map((v) => ({ ...v, on: v.id === view })),
    openOnMove: false,
    row(r) {
      const who = authorOf(r.rule.created_by);
      const draft = r.rule.status !== "active";
      return {
        id: r.rule.id,
        key: rowKicker(r),
        title: r.rule.name || r.rule.id,
        sub: who.agent && draft ? `drafted by ${who.name} · awaiting your review` : void 0,
        meta: r.invalid ? "not valid" : r.rule.status
      };
    },
    onChip(group, id) {
      if (group !== "view") return;
      view = id;
      paintList();
    },
    onOpen(r) {
      if (painting) return;
      ctx.route.go("rules", r.rule.id);
    },
    foot
  });
  handle.el.classList.add("cb-rules-list");
  function paintList() {
    const c = viewCounts(rows.map((r) => r.rule));
    const shown = rows.filter(inView);
    handle.setChips(
      "view",
      VIEWS2.map((v) => ({ ...v, on: v.id === view, count: c[v.id] }))
    );
    handle.setItems(shown);
    const els = handle.el.querySelectorAll(".kit-row");
    els.forEach((rowEl, i) => {
      const r = shown[i];
      if (!r) return;
      rowEl.dataset.rule = r.rule.id;
      if (r.invalid) rowEl.title = r.invalid;
      rowEl.querySelector(".kit-meta")?.classList.add(
        "kit-pill",
        r.invalid ? "cb-pill-invalid" : r.rule.status === "active" ? "ok" : "wait"
      );
    });
    const openAt = shown.findIndex((r) => r.rule.id === openId);
    if (openAt >= 0) {
      painting = true;
      try {
        handle.open(openAt);
      } finally {
        painting = false;
      }
    }
    ctx.bar.setCount("rules", c.all);
    nameAgent();
    if (!doc && !openId) drawEmpty();
  }
  async function loadList() {
    const mine = ++listSeq;
    try {
      const v = await ctx.api.get("/rules");
      if (mine !== listSeq) return;
      rows = v.rules ?? [];
      paintList();
    } catch {
    }
  }
  function drawEmpty() {
    readEl.replaceChildren(
      h11(
        "div",
        { class: "cb-read-empty" },
        h11("p", { class: "cb-read-empty-section kit-label" }, "rules"),
        h11(
          "p",
          { class: "cb-read-empty-count" },
          pluralize(rows.length, "rule")
        ),
        h11(
          "p",
          { class: "cb-read-empty-prompt" },
          "Select a rule to see it here."
        )
      )
    );
  }
  function feed() {
    if (!active) return;
    ctx.setAttached(
      doc ? { rule: doc.id } : asking ? { section: "rules" } : {}
    );
    ctx.setPrimary(doc ? doc.primary() : null);
  }
  const hooks = {
    primaryChanged() {
      if (active) ctx.setPrimary(doc ? doc.primary() : null);
    },
    saved() {
      void loadList();
    }
  };
  async function open(id) {
    const mine = ++opening;
    try {
      const d = await ctx.api.get("/rule", { id });
      if (mine !== opening || openId !== id) return;
      doc = renderRule(ctx, d, hooks);
      asking = false;
      readEl.replaceChildren(doc.el);
      readEl.scrollTop = 0;
      feed();
    } catch (err) {
      if (mine !== opening) return;
      doc = null;
      readEl.replaceChildren(
        h11(
          "div",
          { class: "cb-read-empty" },
          h11("p", { class: "cb-read-empty-section kit-label" }, "rules"),
          h11("p", { class: "cb-read-empty-prompt" }, message(err))
        )
      );
      feed();
    }
  }
  function close() {
    opening++;
    openId = null;
    doc = null;
    drawEmpty();
    feed();
  }
  const activateKey = {
    keys: "a",
    label: "activate the open draft",
    group: "page",
    run() {
      if (!doc) return;
      const p = doc.primary();
      if (p?.label === "Activate") p.run();
    }
  };
  void loadList();
  return {
    id: "rules",
    list: handle.el,
    read: readEl,
    keys: [activateKey],
    show(sub) {
      active = true;
      const id = sub;
      if (!id) {
        if (openId) close();
        else feed();
      } else if (id !== openId) {
        openId = id;
        doc = null;
        feed();
        void open(id);
      } else {
        feed();
      }
      paintList();
    },
    hide() {
      active = false;
      asking = false;
    },
    onLive(type, data) {
      if (type === "rules") {
        void loadList();
        const ev = data;
        if (doc && openId && (!ev?.id || ev.id === openId)) {
          const d = doc;
          const mine = ++liveSeq;
          void ctx.api.get("/rule", { id: openId }).then((detail) => {
            if (mine === liveSeq && doc === d) d.update(detail, ev?.by ?? "");
          }).catch(() => {
          });
        }
      } else if (type === "index" || type === "decided") {
        doc?.refresh();
        if (type === "index") void loadList();
      } else if (type === "proposals") {
        void loadList();
      }
    },
    primary() {
      return doc ? doc.primary() : null;
    }
  };
}

// entry.ts
registerSection(makeAttention);
registerSection(makeRules);
registerDock(makeDock);
boot();
