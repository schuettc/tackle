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
  let list4 = liveListeners.get(type);
  if (!list4) {
    list4 = [];
    liveListeners.set(type, list4);
  }
  list4.push(cb);
}
function emitLive(type, data) {
  const list4 = liveListeners.get(type);
  if (list4) for (const cb of list4) cb(data);
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
  let lastTitle = "";
  let agent = "";
  const agentListeners = [];
  let dockSession = "";
  const dockSessionListeners = [];
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
    setAttached(a, jobTitle2 = "") {
      lastAttached = a;
      lastTitle = jobTitle2;
      for (const d of dockHandles) d.setAttached(a, jobTitle2);
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
    },
    dockSession: () => dockSession,
    setDockSession(id) {
      if (id === dockSession) return;
      dockSession = id;
      for (const cb of dockSessionListeners) cb(id);
    },
    onDockSession(cb) {
      dockSessionListeners.push(cb);
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
    const list4 = h("div", { class: "kit-list" });
    const read = h("div", { class: "kit-read" });
    app.append(list4, read);
  }
  const rail = h("div", { class: "kit-rail" });
  for (const make of dockMakers) {
    const d = make(ctx);
    d.setAttached(lastAttached, lastTitle);
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
    handle.setCount("apply", counts["to-apply"] ?? 0);
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
      handle.setCount("apply", counts["to-apply"] ?? 0);
    }).catch(() => {
    });
  });
  dispatchCurrent();
  void liveClient;
}

// apply.ts
import {
  ApiError,
  list,
  h as h3,
  facts,
  buttons,
  card
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
import {
  sheet,
  h as h2,
  noteField
} from "/_kit/kit.js";
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

// apply-text.ts
function viewOf(job) {
  switch (job.state) {
    case "planned":
      return "ready";
    case "done":
    case "failed":
    case "cancelled":
      return "done";
    default:
      return "running";
  }
}
var TITLES = {
  "branch-delete-local": "Delete local branches",
  "branch-delete-remote": "Delete remote branches",
  "worktree-remove": "Remove worktrees",
  "repo-archive": "Archive repos",
  "repo-delete": "Delete repos",
  "pr-close": "Close PRs",
  "pr-merge": "Merge PRs",
  "issue-close": "Close issues"
};
function actionTitle(action) {
  return TITLES[action] ?? action;
}
var PAST = {
  "branch-delete-local": "deleted",
  "branch-delete-remote": "deleted on the remote",
  "worktree-remove": "removed",
  "repo-archive": "archived",
  "repo-delete": "deleted",
  "pr-close": "closed",
  "pr-merge": "merged",
  "issue-close": "closed"
};
var VERB = {
  "branch-delete-local": "delete",
  "branch-delete-remote": "delete",
  "worktree-remove": "remove",
  "repo-archive": "archive",
  "repo-delete": "delete",
  "pr-close": "close",
  "pr-merge": "merge",
  "issue-close": "close"
};
function actionVerb(action) {
  return VERB[action] ?? "run";
}
function laneLabel(lane, agent) {
  return lane === "agent" ? `${agent || "agent"} · outward` : "casebook · local";
}
function groupSteps(steps) {
  const out = [];
  const at2 = /* @__PURE__ */ new Map();
  for (const s of steps) {
    const k = `${s.action}\0${s.lane}`;
    const i = at2.get(k);
    if (i === void 0) {
      at2.set(k, out.length);
      out.push({ action: s.action, lane: s.lane, steps: [s] });
    } else out[i].steps.push(s);
  }
  return out;
}
function jobTitle(job) {
  const titles = [
    ...new Set(groupSteps(job.steps ?? []).map((g) => actionTitle(g.action)))
  ];
  if (!titles.length) return `job #${job.id}`;
  return titles.map((t, i) => i ? t.charAt(0).toLowerCase() + t.slice(1) : t).join(", ");
}
function jobLanes(job) {
  const have = new Set((job.steps ?? []).map((s) => s.lane));
  return ["casebook", "agent"].filter((l) => have.has(l));
}
var FINISHED = /* @__PURE__ */ new Set(["verified", "reported", "skipped", "failed"]);
function progress(job) {
  const p = {
    total: 0,
    finished: 0,
    byLane: {},
    verified: 0,
    paused: 0,
    skipped: 0,
    failed: 0,
    started: job.state === "running"
  };
  for (const s of job.steps ?? []) {
    p.total++;
    if (s.state !== "pending") p.started = true;
    if (FINISHED.has(s.state)) {
      p.finished++;
      p.byLane[s.lane] = (p.byLane[s.lane] ?? 0) + 1;
    }
    if (s.state === "verified") p.verified++;
    if (s.state === "paused") p.paused++;
    if (s.state === "skipped") p.skipped++;
    if (s.state === "failed") p.failed++;
  }
  return p;
}
function needsLine(needsYou, paused) {
  const parts = [];
  if (needsYou)
    parts.push(`${needsYou} ${needsYou === 1 ? "needs" : "need"} you`);
  if (paused) parts.push(`${paused} paused`);
  return parts.join(" · ");
}
function openCardsByJob(cards) {
  const m = /* @__PURE__ */ new Map();
  for (const c of cards) {
    if (c.state !== "open") continue;
    m.set(c.job_id, (m.get(c.job_id) ?? 0) + 1);
  }
  return m;
}
function stepMark(s, next) {
  const past = PAST[s.action] ?? "done";
  const why = (w) => s.detail ? `${w} · ${s.detail}` : w;
  if (s.undone_at) return { glyph: "↺", text: "undone", tone: "muted" };
  switch (s.state) {
    case "verified":
      return { glyph: "✓", text: `${past} · verified`, tone: "ok" };
    case "reported":
      return {
        glyph: "✓",
        text: `${past} · reported`,
        tone: "muted"
      };
    case "running":
      return {
        glyph: "▸",
        text: "running",
        tone: s.lane === "agent" ? "agent" : "signal"
      };
    case "paused":
      return { glyph: "‖", text: why("paused"), tone: "agent" };
    case "needs_you":
      return { glyph: "!", text: "needs you", tone: "signal" };
    case "failed":
      return { glyph: "✗", text: why("failed"), tone: "signal" };
    case "skipped":
      return { glyph: "–", text: why("skipped"), tone: "muted" };
    default:
      return { glyph: "·", text: next ? "next" : "queued", tone: "wait" };
  }
}
function nextSteps(steps) {
  const out = /* @__PURE__ */ new Set();
  const seen = /* @__PURE__ */ new Set();
  for (const s of steps) {
    if (s.state !== "pending" || seen.has(s.lane)) continue;
    seen.add(s.lane);
    out.add(s.id);
  }
  return out;
}
function planSummary(steps) {
  let local = 0;
  let out = 0;
  for (const s of steps) {
    if (s.lane === "agent") out++;
    else local++;
  }
  const parts = [pluralize(steps.length, "step")];
  if (local) parts.push(`${local} local`);
  if (out) parts.push(`${out} outward`);
  return parts.join(" · ");
}
function needsSession(steps) {
  return steps.some((s) => s.lane === "agent");
}
function makeSeq() {
  let latest = 0;
  return {
    next: () => ++latest,
    isLatest: (n) => n === latest
  };
}
function outwardGo(n) {
  return n === 1 ? "The outward step goes" : `The ${n} outward steps go`;
}
function plannedKeys(jobs) {
  const m = /* @__PURE__ */ new Map();
  const rank = (h13) => h13.planned ? 0 : 1;
  for (const j of jobs) {
    if (!["planned", "approved", "running", "paused"].includes(j.state))
      continue;
    const h13 = { id: j.id, planned: j.state === "planned" };
    for (const s of j.steps ?? []) {
      const was = m.get(s.key);
      if (!was || rank(h13) > rank(was) || rank(h13) === rank(was) && h13.id > was.id)
        m.set(s.key, h13);
    }
  }
  return m;
}
function heldLabel(h13) {
  return `${h13.planned ? "in plan" : "in job"} #${h13.id}`;
}
function heldWhere(held) {
  const vs = [...held.values()];
  const plans = vs.some((h13) => h13.planned);
  const jobs = vs.some((h13) => !h13.planned);
  if (plans && jobs) return "in a plan or a job";
  return jobs ? "in a job" : "in a plan";
}
function plannableCount(total, loaded, planned, all) {
  const have = new Set(loaded);
  let inPlan = loaded.filter((k) => planned.has(k)).length;
  if (!all) {
    for (const k of planned.keys()) if (!have.has(k)) inPlan++;
  }
  return Math.max(0, total - inPlan);
}
function ageText(ms) {
  const s = Math.max(0, Math.floor(ms / 1e3));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h13 = Math.floor(m / 60);
  if (h13 < 24) return `${h13}h`;
  return `${Math.floor(h13 / 24)}d`;
}
function observations(builtAt, intervalMs, now) {
  const age = now - builtAt;
  if (!intervalMs || age <= intervalMs)
    return { text: "observations fresh", stale: false };
  return { text: `observations ${ageText(age)} old`, stale: true };
}

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
  const h13 = Math.floor(m / 60);
  if (h13 < 24) return `${h13}h`;
  const d = Math.floor(h13 / 24);
  if (d < 30) return `${d}d`;
  const mo = Math.floor(d / 30);
  if (mo < 12) return `${mo}mo`;
  return `${Math.floor(mo / 12)}y`;
}

// apply.ts
var PAGE = 200;
var STEPS_PAGE = 200;
var VIEWS = [
  { id: "running", label: "running" },
  { id: "ready", label: "ready" },
  { id: "done", label: "done" }
];
function message(err) {
  return err instanceof Error ? err.message : String(err);
}
function builtAgo(ts) {
  const a = fmtAge(ts);
  return a === "now" ? "just now" : `${a} ago`;
}
var dangerVerb = (verb) => DANGER_DISPS.has(verb) || verb === "remove";
var planner = null;
function buildPlan(ctx, what) {
  const body = what === "all" ? { all: true } : { keys: what };
  return ctx.api.post("/apply/plan", body).then((v) => planner?.planned(v)).catch(
    (err) => err instanceof ApiError && err.status === 422 ? planner?.nothing(message(err)) : planner?.refused(message(err))
  );
}
function focusKey(el) {
  const host = el.closest(
    "[data-card],[data-step],[data-session],[data-testid]"
  );
  const hostKey = host ? ["card", "step", "session", "testid"].map((a) => host.getAttribute(`data-${a}`) ?? "").join("/") : "";
  const own = el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement ? el.className : el.textContent ?? "";
  return `${hostKey}|${el.tagName}|${own}`;
}
function focusSnapshot(root) {
  const a = document.activeElement;
  if (!a || a === document.body || !root.contains(a)) return null;
  const field = a instanceof HTMLTextAreaElement || a instanceof HTMLInputElement ? a : null;
  return {
    key: focusKey(a),
    start: field ? field.selectionStart : null,
    end: field ? field.selectionEnd : null
  };
}
function focusRestore(root, snap) {
  if (!snap) return;
  const els = root.querySelectorAll(
    "button, textarea, input, a[href], [tabindex]"
  );
  for (const el of els) {
    if (focusKey(el) !== snap.key) continue;
    el.focus({ preventScroll: true });
    if ((el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement) && snap.start !== null) {
      el.setSelectionRange(snap.start, snap.end ?? snap.start);
    }
    return;
  }
}
function makeApply(ctx) {
  const readEl = h3("div", { class: "kit-read cb-apply-read" });
  let jobs = [];
  let cards = [];
  let sessions = [];
  let items = [];
  let itemsTotal = 0;
  let machine = "";
  let view = "running";
  let active = false;
  let openId = null;
  let open = null;
  let planGroups = null;
  let courtSession = "";
  let stepsShown = STEPS_PAGE;
  let refusal = "";
  let nothingNote = "";
  let note = "";
  let busy = false;
  let planning = false;
  let painting = false;
  let builtAt = 0;
  let intervalMs = 0;
  let syncing = false;
  let syncError = "";
  let afterSync = null;
  const editors = /* @__PURE__ */ new Map();
  const itemsSeq = makeSeq();
  const listSeq = makeSeq();
  const jobSeq = makeSeq();
  const present = () => sessions.filter((s) => !s.left);
  const planSession = () => {
    const ps = present();
    const here = (id) => !!id && ps.some((s) => s.id === id);
    if (here(courtSession)) return courtSession;
    if (here(ctx.dockSession())) return ctx.dockSession();
    return ps.length === 1 ? ps[0].id : "";
  };
  const agentOf = (job) => {
    const id = job.state === "planned" && !job.session ? planSession() : job.session;
    const s = sessions.find((x) => x.id === id);
    return s?.harness || ctx.agentName() || "agent";
  };
  const footCount = h3("span", { class: "cb-apply-foot-n" });
  const footBtns = h3("span", { class: "cb-apply-foot-btns" });
  const footSel = h3("div", { class: "cb-apply-foot-sel" }, footCount, footBtns);
  const obsText = h3("span", { "data-testid": "observations" });
  const obsEl = h3(
    "div",
    { class: "cb-apply-obs" },
    h3("i", { class: "cb-apply-obs-dot", "aria-hidden": "true" }),
    obsText
  );
  const foot = h3("div", { class: "cb-apply-foot" }, footSel, obsEl);
  const inPlan = () => plannedKeys(jobs);
  const plannable = () => plannableCount(
    itemsTotal,
    items.map((i) => i.key),
    inPlan(),
    itemsTotal <= items.length
  );
  const handle = list({
    label: "to apply",
    views: VIEWS.map((v) => ({ ...v, on: v.id === view })),
    openOnMove: false,
    row(r) {
      if (r.t === "more") {
        return {
          id: "more",
          title: `… ${pluralize(r.left, "more decided item")}`,
          meta: "show more"
        };
      }
      if (r.t === "item") {
        const it = r.item;
        const plan2 = inPlan().get(it.key);
        return {
          id: it.key,
          key: `${it.kind} · ${keyWithoutKind(it.key)}`,
          title: it.title ?? keyWithoutKind(it.key),
          meta: plan2 ? heldLabel(plan2) : it.decision?.disposition ?? "",
          selectable: !plan2
        };
      }
      const j = r.job;
      const p = progress(j);
      const lanes = jobLanes(j).map((l) => laneLabel(l, agentOf(j))).join(" + ");
      const planned = j.state === "planned";
      return {
        id: `job:${j.id}`,
        key: planned ? `plan · job #${j.id}` : lanes || `job #${j.id}`,
        title: jobTitle(j),
        meta: planned ? pluralize(p.total, "step") : p.started || viewOf(j) === "done" ? `${p.finished}/${p.total}` : "queued",
        sub: needsLine(cardsFor(j.id), p.paused) || void 0
      };
    },
    onChip(group, id) {
      if (group !== "view") return;
      view = id;
      paintList();
      feed();
    },
    onOpen(r, i) {
      if (painting) return;
      if (r.t === "job") ctx.route.go("apply", String(r.job.id));
      else if (r.t === "item") {
        const plan2 = inPlan().get(r.item.key);
        if (plan2) ctx.route.go("apply", String(plan2.id));
        else handle.toggle(i);
      } else void loadItems(items.length);
    },
    onSelect() {
      paintFoot();
      if (active) ctx.setPrimary(primary());
      if (openId === null) drawOverview();
    },
    foot
  });
  handle.el.classList.add("cb-apply-list");
  const cardsFor = (jobId) => openCardsByJob(cards.filter((c) => c.kind !== "paused")).get(jobId) ?? 0;
  const selectedKeys = () => {
    const planned = inPlan();
    return handle.selectedIds().filter((k) => !k.startsWith("job:") && k !== "more" && !planned.has(k));
  };
  const listed = () => jobs.filter((j) => j.state !== "cancelled");
  function rowsFor(v) {
    const js = listed().filter((j) => viewOf(j) === v).sort((a, b) => b.id - a.id).map((job) => ({ t: "job", job }));
    if (v !== "ready") return js;
    const its = items.map((item) => ({ t: "item", item }));
    const more = itemsTotal > items.length ? [{ t: "more", left: itemsTotal - items.length }] : [];
    return [...js, ...its, ...more];
  }
  function counts() {
    const c = { running: 0, ready: itemsTotal, done: 0 };
    for (const j of listed()) c[viewOf(j)]++;
    return c;
  }
  function paintList() {
    const c = counts();
    handle.setChips(
      "view",
      VIEWS.map((v) => ({
        ...v,
        on: v.id === view,
        count: c[v.id] || void 0
      }))
    );
    const shown = rowsFor(view);
    handle.setItems(shown);
    const planned = inPlan();
    const els = handle.el.querySelectorAll(".kit-row");
    els.forEach((rowEl, i) => {
      const r = shown[i];
      if (!r) return;
      if (r.t !== "job") {
        if (r.t === "item") {
          rowEl.dataset.key = r.item.key;
          const plan2 = planned.get(r.item.key);
          if (plan2) {
            rowEl.dataset.plan = String(plan2.id);
            rowEl.classList.add("cb-in-plan");
          }
        } else rowEl.classList.add("cb-apply-more");
        return;
      }
      decorateJobRow(rowEl, r.job);
    });
    const openAt = shown.findIndex((r) => r.t === "job" && r.job.id === openId);
    if (openAt >= 0) {
      painting = true;
      try {
        handle.open(openAt);
      } finally {
        painting = false;
      }
    }
    paintFoot();
  }
  function decorateJobRow(rowEl, j) {
    rowEl.dataset.job = String(j.id);
    rowEl.classList.add("cb-job-row");
    const p = progress(j);
    const kicker = rowEl.querySelector(".kit-kicker");
    if (kicker && j.state !== "planned") {
      const lanes = jobLanes(j);
      if (lanes.length) {
        kicker.replaceChildren(
          ...lanes.flatMap((l, i) => [
            i ? " + " : "",
            h3(
              "span",
              { class: `cb-lanename cb-lanename-${l}`, "data-lane": l },
              laneLabel(l, agentOf(j))
            )
          ])
        );
      }
    } else kicker?.classList.add("cb-plan-kicker");
    const meta = rowEl.querySelector(".kit-meta");
    if (meta?.textContent === "queued") meta.classList.add("cb-queued");
    rowEl.querySelector(".kit-sub")?.classList.add("cb-needs-line");
    if (j.state !== "planned" && (p.started || viewOf(j) === "done")) {
      const bar2 = h3("div", {
        class: "cb-pb",
        role: "progressbar",
        "aria-valuemin": "0",
        "aria-valuemax": String(p.total),
        "aria-valuenow": String(p.finished)
      });
      for (const l of jobLanes(j)) {
        const n = p.byLane[l] ?? 0;
        if (!n) continue;
        bar2.append(
          h3("span", {
            class: `cb-pb-${l}`,
            style: `width:${100 * n / Math.max(1, p.total)}%`
          })
        );
      }
      rowEl.querySelector(".kit-title")?.after(bar2);
    }
  }
  function paintFoot() {
    paintObs();
    if (view !== "ready" || !itemsTotal) {
      footCount.textContent = "";
      footBtns.replaceChildren();
      footSel.hidden = true;
      return;
    }
    footSel.hidden = false;
    const n = selectedKeys().length;
    const can = plannable();
    const held = itemsTotal - can;
    footCount.textContent = n ? `${n} selected` : can ? pluralize(can, "decided item") : `${pluralize(held, "decided item")} ${heldWhere(inPlan())}`;
    footCount.classList.toggle("cb-sel", n > 0);
    const bs = [];
    if (n && !refusal)
      bs.push({ label: `plan ${n}`, run: () => plan(selectedKeys()) });
    if (can && !refusal)
      bs.push({ label: `plan all ${can}`, run: () => plan("all") });
    footBtns.replaceChildren(bs.length ? buttons(bs) : "");
  }
  function paintObs() {
    const o = builtAt ? observations(builtAt, intervalMs, Date.now()) : { text: "", stale: false };
    obsText.textContent = syncing ? "syncing observations…" : o.text;
    obsEl.dataset.state = syncing ? "syncing" : o.stale ? "stale" : "fresh";
    obsEl.hidden = !builtAt && !syncing;
  }
  const obsTimer = setInterval(paintObs, 1e3);
  void obsTimer;
  const dirty = {
    all: false,
    items: false,
    sessions: false,
    summary: false,
    jobs: /* @__PURE__ */ new Set()
  };
  let flushing = false;
  let queued = false;
  function mark(f) {
    f(dirty);
    if (flushing) {
      queued = true;
      return;
    }
    flushing = true;
    setTimeout(() => void flush(), 40);
  }
  async function flush() {
    try {
      do {
        queued = false;
        const d = {
          all: dirty.all,
          items: dirty.items,
          sessions: dirty.sessions,
          summary: dirty.summary,
          jobs: [...dirty.jobs]
        };
        dirty.all = dirty.items = dirty.sessions = dirty.summary = false;
        dirty.jobs.clear();
        const some = d.all && openId !== null ? [openId] : d.all ? [] : d.jobs;
        await Promise.all([
          d.all ? loadJobs() : null,
          some.length ? loadSome(some) : null,
          d.items ? loadItems() : null,
          d.sessions ? loadSessions() : null,
          d.summary ? loadSummary() : null
        ]);
      } while (queued);
    } finally {
      flushing = false;
    }
  }
  function patchJob(v) {
    const i = jobs.findIndex((j) => j.id === v.job.id);
    if (i >= 0) jobs[i] = v.job;
    else jobs = [...jobs, v.job];
    cards = [
      ...cards.filter((c) => c.job_id !== v.job.id),
      ...(v.needs_you ?? []).filter((c) => c.state === "open")
    ];
  }
  async function loadJobs() {
    const mine = listSeq.next();
    try {
      const [j, c] = await Promise.all([
        ctx.api.get("/jobs"),
        ctx.api.get("/needs-you")
      ]);
      if (!listSeq.isLatest(mine)) return;
      jobs = j.jobs ?? [];
      cards = c.cards ?? [];
      if (open) patchJob(open);
      paintList();
    } catch {
    }
  }
  async function loadSome(ids) {
    const openMine = openId !== null && ids.includes(openId) ? jobSeq.next() : 0;
    const at2 = openId;
    const got = await Promise.all(
      ids.map(
        (id) => ctx.api.get("/job", { id: String(id) }).catch(() => null)
      )
    );
    let drawn = null;
    got.forEach((v, i) => {
      if (!v) return;
      if (ids[i] === at2) {
        if (!jobSeq.isLatest(openMine) || openId !== at2) return;
        drawn = v;
      }
      patchJob(v);
    });
    if (drawn) setOpen(drawn);
    else paintList();
  }
  async function loadItems(offset = 0) {
    const mine = itemsSeq.next();
    try {
      const v = await ctx.api.get("/items", {
        view: "to-apply",
        limit: String(PAGE),
        offset: String(offset)
      });
      if (!itemsSeq.isLatest(mine)) return;
      const got = v.items ?? [];
      items = offset ? [...items, ...got] : got;
      itemsTotal = v.total;
      const keep = new Set(items.map((i) => i.key));
      const gone = selectedKeys().filter((k) => !keep.has(k));
      if (gone.length && offset === 0 && itemsTotal <= items.length) {
        handle.deselect(gone);
      }
      paintList();
      if (active) ctx.setPrimary(primary());
      if (openId === null) drawOverview();
    } catch {
    }
  }
  async function loadSessions() {
    try {
      const v = await ctx.api.get("/sessions");
      sessions = v.sessions ?? [];
      paintList();
      if (open?.job.state === "planned") drawOpen();
      if (active) ctx.setPrimary(primary());
    } catch {
    }
  }
  async function loadSummary() {
    try {
      const s = await ctx.api.get("/summary");
      machine = s.machine;
      builtAt = Date.parse(s.built_at) || 0;
      intervalMs = s.sync_interval_ms;
      syncing = s.syncing;
      paintObs();
      if (openId === null) drawOverview();
    } catch {
    }
  }
  async function loadJob(id) {
    const mine = jobSeq.next();
    try {
      const v = await ctx.api.get("/job", { id: String(id) });
      if (!jobSeq.isLatest(mine) || openId !== id) return;
      setOpen(v);
    } catch (err) {
      if (!jobSeq.isLatest(mine) || openId !== id) return;
      open = null;
      readEl.replaceChildren(
        h3(
          "div",
          { class: "cb-read-empty" },
          h3("p", { class: "cb-read-empty-section kit-label" }, "to apply"),
          h3("p", { class: "cb-read-empty-prompt" }, message(err))
        )
      );
      feed();
    }
  }
  function setOpen(v) {
    const first = open?.job.id !== v.job.id;
    open = v;
    if (v.job.state !== "cancelled") view = viewOf(v.job);
    patchJob(v);
    for (const id of [...editors.keys()]) {
      if (!(v.needs_you ?? []).some((c) => c.id === id && c.state === "open"))
        editors.delete(id);
    }
    if (first) stepsShown = STEPS_PAGE;
    paintList();
    drawOpen();
    if (first) readEl.scrollTop = 0;
    feed();
  }
  async function act(path, body, ok) {
    if (busy) return;
    busy = true;
    note = "";
    const mine = jobSeq.next();
    try {
      const r = await ctx.api.post(path, body);
      ok?.();
      const jv = r;
      if (jv && typeof jv === "object" && "job" in jv && jv.job) {
        if (jobSeq.isLatest(mine) && openId === jv.job.id) setOpen(jv);
        else patchJob(jv);
      } else if (openId !== null) {
        const id = openId;
        mark((d) => d.jobs.add(id));
      }
    } catch (err) {
      note = message(err);
      drawOpen();
    } finally {
      busy = false;
    }
  }
  let asked = [];
  function plan(what) {
    if (planning) return;
    planning = true;
    refusal = "";
    nothingNote = "";
    asked = what;
    void buildPlan(ctx, what).finally(() => {
      planning = false;
    });
  }
  planner = {
    planned(v) {
      refusal = "";
      afterSync = null;
      syncError = "";
      if (asked === "all") handle.clearSelection();
      else handle.deselect(asked);
      openId = v.job.id;
      planGroups = {
        job: v.job.id,
        groups: (v.groups ?? []).map((g) => ({
          action: g.action,
          lane: g.lane,
          steps: g.steps ?? []
        }))
      };
      setOpen({ job: v.job, needs_you: [] });
      ctx.route.go("apply", String(v.job.id));
    },
    nothing(msg) {
      nothingNote = msg;
      if (openId === null) drawOverview();
      void loadJobs().then(() => {
        if (openId === null) drawOverview();
        if (active) ctx.setPrimary(primary());
      });
      void loadItems();
    },
    refused(msg) {
      refusal = msg;
      if (openId === null) {
        drawOverview();
        paintFoot();
        if (active) ctx.setPrimary(primary());
      } else ctx.route.go("apply");
    }
  };
  async function syncFirst() {
    syncError = "";
    afterSync = asked;
    try {
      const v = await ctx.api.post("/sync", {});
      syncing = v.running;
    } catch (err) {
      afterSync = null;
      syncError = message(err);
    }
    paintObs();
    if (openId === null) drawOverview();
    if (active) ctx.setPrimary(primary());
  }
  function onSync(e) {
    if (e.state === "running") {
      syncing = true;
      syncError = "";
    } else {
      syncing = false;
      syncError = e.state === "failed" ? e.error ?? "failed" : "";
      mark((d) => d.summary = true);
      if (e.state === "done" && afterSync !== null) {
        const what = afterSync;
        afterSync = null;
        refusal = "";
        plan(what);
      } else if (e.state === "done" && refusal) {
        refusal = "";
        paintFoot();
      }
    }
    paintObs();
    if (openId === null) drawOverview();
    if (active) ctx.setPrimary(primary());
  }
  function refusalCard() {
    const body = h3(
      "div",
      null,
      h3("p", { "data-testid": "plan-refused" }, refusal)
    );
    if (syncing) {
      body.append(
        h3(
          "p",
          { class: "cb-apply-note", role: "status", "data-testid": "syncing" },
          "syncing… the plan is built again when the sync is done"
        )
      );
    } else if (syncError) {
      body.append(
        h3(
          "p",
          {
            class: "cb-apply-note",
            role: "status",
            "data-testid": "sync-failed"
          },
          `not synced: ${syncError}`
        )
      );
    }
    const el = card({
      edge: "signal",
      head: "not planned",
      body,
      actions: syncing ? void 0 : [{ label: "sync first", fill: true, run: () => void syncFirst() }]
    });
    el.dataset.testid = "refusal";
    return el;
  }
  function drawOverview() {
    if (openId !== null) return;
    const n = selectedKeys().length;
    const can = plannable();
    const doc = h3(
      "article",
      { class: "cb-apply-doc", "data-testid": "apply-overview" },
      h3(
        "p",
        { class: "kit-kick" },
        `to apply${machine ? ` · ${machine}` : ""}`
      ),
      h3(
        "h1",
        { class: "kit-h1" },
        itemsTotal ? `${pluralize(itemsTotal, "decided item")} to apply` : "Nothing decided is waiting to be applied"
      ),
      facts([
        ["running", String(counts().running)],
        ["plans", String(jobs.filter((j) => j.state === "planned").length)],
        [heldWhere(inPlan()), String(itemsTotal - can)],
        ["selected", String(n)]
      ]),
      h3(
        "p",
        null,
        "A plan shows the exact command for every step before anything runs. This machine applies what is on it; GitHub steps run once, from whichever machine applies them."
      )
    );
    if (refusal) doc.append(refusalCard());
    else if (nothingNote && !can && !n)
      doc.append(
        h3(
          "p",
          {
            class: "cb-apply-note",
            role: "status",
            "data-testid": "plan-nothing"
          },
          nothingNote
        )
      );
    else if (itemsTotal && (n || can)) {
      const bs = [];
      if (n)
        bs.push({
          label: `plan ${n} selected`,
          run: () => plan(selectedKeys())
        });
      if (can)
        bs.push({
          label: `plan all ${can}`,
          fill: !n,
          run: () => plan("all")
        });
      doc.append(buttons(bs));
    }
    const keep = focusSnapshot(readEl);
    readEl.replaceChildren(h3("div", { class: "kit-doc" }, doc));
    focusRestore(readEl, keep);
  }
  function drawOpen() {
    if (!open) return;
    const keep = focusSnapshot(readEl);
    const j = open.job;
    const doc = j.state === "planned" || j.state === "cancelled" ? renderPlan(j) : renderJob(ctx, open, jobHooks);
    readEl.replaceChildren(h3("div", { class: "kit-doc" }, doc));
    focusRestore(readEl, keep);
  }
  function renderPlan(j) {
    const steps = j.steps ?? [];
    const groups = planGroups?.job === j.id ? planGroups.groups : groupSteps(steps);
    const agent = agentOf(j);
    const discarded = j.state === "cancelled";
    const doc = h3(
      "article",
      { class: "cb-apply-doc cb-plan", "data-job": String(j.id) },
      h3(
        "p",
        { class: "kit-kick" },
        discarded ? `plan · job #${j.id} · ${j.machine} · discarded` : `plan · job #${j.id} · ${j.machine} · built ${builtAgo(j.created_at)}`
      ),
      h3("h1", { class: "kit-h1" }, jobTitle(j)),
      facts([
        ["steps", String(steps.length)],
        ["local", String(steps.filter((s) => s.lane !== "agent").length)],
        ["outward", String(steps.filter((s) => s.lane === "agent").length)]
      ])
    );
    if (!steps.length && !discarded) {
      doc.append(
        h3(
          "p",
          { "data-testid": "plan-empty" },
          "Nothing in this plan runs from this machine: no step is on it, and nothing outward."
        ),
        discardButtons(j)
      );
      return doc;
    }
    for (const g of groups) {
      doc.append(
        h3(
          "h3",
          { class: "kit-label cb-group-label" },
          h3(
            "span",
            { class: `cb-lanename cb-lanename-${g.lane}` },
            laneLabel(g.lane, agent)
          ),
          ` · ${actionTitle(g.action)} · ${g.steps.length}`
        ),
        h3(
          "div",
          {
            class: "kit-table cb-plan-group",
            "data-action": g.action,
            "data-lane": g.lane
          },
          ...g.steps.map(
            (s) => h3(
              "div",
              { class: "kit-tr cb-plan-step", "data-key": s.key },
              h3("span", { class: "cb-step-k" }, keyWithoutKind(s.key)),
              h3("code", { class: "cb-cmd" }, s.command)
            )
          )
        )
      );
    }
    if (discarded) {
      doc.append(
        h3(
          "p",
          { class: "cb-approve-none", "data-testid": "plan-discarded" },
          "This plan was discarded before it was approved: nothing in it ran."
        )
      );
    } else doc.append(approveBlock(j));
    return doc;
  }
  function discardButtons(j) {
    return buttons([{ label: "discard", run: () => void discard(j.id) }]);
  }
  function approveBlock(j) {
    const steps = j.steps ?? [];
    const outward = steps.filter((s) => s.lane === "agent").length;
    const el = h3("div", { class: "cb-approve", "data-testid": "approve" });
    el.append(h3("h3", { class: "kit-label" }, "approve"));
    const bs = [];
    if (needsSession(steps)) {
      const ps = present();
      const chosen = planSession();
      if (!ps.length) {
        el.append(
          h3(
            "p",
            { class: "cb-approve-none", "data-testid": "no-session" },
            `${outwardGo(outward)} to an agent session, and none is here. When one attaches, it appears here to choose.`
          )
        );
      } else {
        el.append(
          h3(
            "p",
            { class: "cb-approve-what" },
            `${planSummary(steps)}. ${outwardGo(outward)} to:`
          ),
          h3(
            "div",
            {
              class: "cb-sessions",
              role: "radiogroup",
              "aria-label": "session",
              "data-testid": "session-picker"
            },
            ...ps.map(
              (s) => h3(
                "button",
                {
                  type: "button",
                  role: "radio",
                  class: "kit-chip cb-session" + (s.id === chosen ? " on" : ""),
                  "aria-checked": String(s.id === chosen),
                  "data-session": s.id,
                  onclick() {
                    courtSession = s.id;
                    note = "";
                    drawOpen();
                    if (active) ctx.setPrimary(primary());
                  }
                },
                s.label || s.id
              )
            )
          )
        );
        if (!chosen) {
          el.append(
            h3(
              "p",
              { class: "cb-approve-none", "data-testid": "pick-session" },
              `${ps.length} sessions are here and none is chosen yet.`
            )
          );
        } else bs.push({ label: "approve", fill: true, run: () => approve() });
      }
    } else {
      el.append(
        h3(
          "p",
          { class: "cb-approve-what" },
          `${planSummary(steps)}, all local.`
        )
      );
      bs.push({ label: "approve and run", fill: true, run: () => approve() });
    }
    bs.push({ label: "discard", run: () => void discard(j.id) });
    el.append(buttons(bs));
    if (note)
      el.append(h3("p", { class: "cb-apply-note", role: "status" }, note));
    return el;
  }
  function canApprove() {
    if (!open || open.job.state !== "planned") return false;
    const steps = open.job.steps ?? [];
    if (!steps.length) return false;
    if (!needsSession(steps)) return true;
    return !!planSession();
  }
  function approve() {
    if (!open || !canApprove()) return;
    const needs = needsSession(open.job.steps ?? []);
    void act("/apply/approve", {
      plan_id: open.job.id,
      session: needs ? planSession() : ""
    });
  }
  async function discard(id) {
    if (busy) return;
    busy = true;
    note = "";
    try {
      const v = await ctx.api.post("/apply/cancel", { plan_id: id });
      patchJob(v);
      view = "ready";
      if (openId === id) ctx.route.go("apply");
      else paintList();
    } catch (err) {
      note = message(err);
      drawOpen();
    } finally {
      busy = false;
    }
  }
  function pauseOrResume() {
    if (!open || viewOf(open.job) !== "running") return;
    void act(open.job.paused ? "/jobs/resume" : "/jobs/pause", {
      id: open.job.id
    });
  }
  const jobHooks = {
    agent: (j) => agentOf(j),
    stepsShown: () => stepsShown,
    showMore() {
      stepsShown += STEPS_PAGE;
      drawOpen();
    },
    note: () => note,
    answer(card6, action, text, ok) {
      void act(
        "/jobs/answer",
        { needs_you: card6.id, action, text },
        ok
      );
    },
    undo(step) {
      void act("/jobs/undo", { step: step.id });
    },
    editing: (id) => editors.get(id),
    edit(id, text) {
      if (text === null) editors.delete(id);
      else editors.set(id, text);
    }
  };
  function primary() {
    if (open && openId !== null) {
      const j = open.job;
      if (j.state === "planned") {
        return canApprove() ? { label: "Approve", run: approve } : null;
      }
      if (viewOf(j) === "running") {
        return j.paused ? { label: "Resume", run: pauseOrResume } : { label: "Pause job", run: pauseOrResume };
      }
      return null;
    }
    if (openId === null && refusal) {
      return syncing ? null : { label: "Sync first", run: () => void syncFirst() };
    }
    if (openId === null && view === "ready" && itemsTotal) {
      const n = selectedKeys().length;
      if (n) return { label: `Plan ${n}`, run: () => plan(selectedKeys()) };
      return plannable() ? { label: "Plan all", run: () => plan("all") } : null;
    }
    return null;
  }
  function feed() {
    if (!active) return;
    if (open && openId !== null)
      ctx.setAttached({ job: String(openId) }, jobTitle(open.job));
    else ctx.setAttached({});
    ctx.setPrimary(primary());
  }
  const approveKey = {
    keys: "a",
    label: "approve the open plan",
    group: "page",
    run() {
      if (canApprove()) approve();
      else if (open?.job.state === "planned" && needsSession(open.job.steps ?? []) && !planSession()) {
        note = "not approved: no session is chosen for the outward steps";
        drawOpen();
      }
    }
  };
  const pauseKey = {
    keys: "p",
    label: "pause or resume the open job",
    group: "page",
    run() {
      pauseOrResume();
    }
  };
  function close() {
    jobSeq.next();
    openId = null;
    open = null;
    note = "";
    drawOverview();
    paintList();
    feed();
  }
  ctx.onDockSession(() => {
    if (open?.job.state === "planned") drawOpen();
    if (active) ctx.setPrimary(primary());
  });
  void loadJobs();
  void loadItems();
  void loadSessions();
  void loadSummary();
  const jobOf = (type, data) => {
    const d = data ?? {};
    if (type === "job") return d.id ?? null;
    return d.job_id ?? null;
  };
  return {
    id: "apply",
    list: handle.el,
    read: readEl,
    keys: [approveKey, pauseKey],
    show(sub) {
      active = true;
      const id = sub ? Number(decodeURIComponent(sub).replace(/^#/, "")) : NaN;
      if (!sub || !Number.isFinite(id)) {
        if (openId !== null) close();
        else {
          drawOverview();
          feed();
        }
      } else if (id !== openId) {
        openId = id;
        open = null;
        note = "";
        feed();
        void loadJob(id);
      } else {
        if (open) drawOpen();
        else void loadJob(id);
        feed();
      }
      paintList();
    },
    hide() {
      active = false;
    },
    onLive(type, data) {
      if (type === "job" || type === "step" || type === "needs_you") {
        const about = jobOf(type, data);
        mark((d) => {
          if (about === null) d.all = true;
          else d.jobs.add(about);
        });
      } else if (type === "index" || type === "decided") {
        mark((d) => {
          d.items = true;
          if (type === "index") d.summary = true;
        });
      } else if (type === "sessions") {
        mark((d) => d.sessions = true);
      } else if (type === "sync") {
        onSync(data ?? {});
      }
    },
    primary
  };
}
function renderJob(_ctx, v, hooks) {
  const j = v.job;
  const steps = j.steps ?? [];
  const p = progress(j);
  const agent = hooks.agent(j);
  const open = (v.needs_you ?? []).filter((c) => c.state === "open");
  const stepOf = (id) => steps.find((s) => s.id === id);
  const lanes = jobLanes(j).map((l) => laneLabel(l, agent));
  const state = j.paused && viewOf(j) === "running" ? "paused" : j.state;
  const doc = h3(
    "article",
    { class: "cb-apply-doc cb-job", "data-job": String(j.id) },
    h3(
      "p",
      { class: "kit-kick" },
      [lanes.join(" + "), `job #${j.id}`, state].filter(Boolean).join(" · ")
    ),
    h3("h1", { class: "kit-h1" }, jobTitle(j))
  );
  const pairs = [
    ["done", `${p.verified} of ${p.total}`],
    ["needs you", String(open.filter((c) => c.kind !== "paused").length)],
    ["paused", String(p.paused)]
  ];
  if (p.skipped) pairs.push(["skipped", String(p.skipped)]);
  if (p.failed) pairs.push(["failed", String(p.failed)]);
  const restores = steps.filter((s) => s.restore).length;
  if (restores) pairs.push(["restore records", String(restores)]);
  doc.append(facts(pairs));
  const cardsEl = h3("div", { class: "cb-needs", "data-testid": "needs-you" });
  for (const c of open)
    cardsEl.append(needsCard(c, stepOf(c.step_id), j, agent, hooks));
  doc.append(cardsEl);
  const note = hooks.note();
  if (note)
    doc.append(h3("p", { class: "cb-apply-note", role: "status" }, note));
  const next = nextSteps(steps);
  const shown = steps.slice(0, hooks.stepsShown());
  doc.append(
    h3("h3", { class: "kit-label" }, "steps"),
    h3(
      "div",
      { class: "kit-table cb-steps", "data-testid": "steps" },
      ...shown.map((s) => {
        const m = stepMark(s, next.has(s.id));
        return h3(
          "div",
          {
            class: "kit-tr cb-step",
            "data-step": String(s.id),
            "data-state": s.state,
            "data-key": s.key
          },
          h3("span", { class: `cb-step-g cb-tone-${m.tone}` }, m.glyph),
          h3("span", { class: "cb-step-k" }, keyWithoutKind(s.key)),
          h3(
            "span",
            { class: "cb-step-w" },
            h3("span", { class: "cb-step-t" }, m.text),
            s.undoable ? h3(
              "button",
              {
                type: "button",
                class: "kit-btn cb-undo",
                onclick() {
                  hooks.undo(s);
                }
              },
              "undo"
            ) : null
          )
        );
      }),
      steps.length > shown.length ? h3(
        "div",
        { class: "kit-tr cb-steps-more" },
        h3("span"),
        h3(
          "span",
          { class: "cb-step-k" },
          `… ${pluralize(steps.length - shown.length, "more step")}`
        ),
        h3(
          "button",
          {
            type: "button",
            class: "cb-link",
            onclick() {
              hooks.showMore();
            }
          },
          "show more"
        )
      ) : null
    )
  );
  return doc;
}
function needsCard(c, step, j, agent, hooks) {
  const key = step ? keyWithoutKind(step.key) : "";
  const verb = step ? actionVerb(step.action) : "run";
  const answer = (action, text = "") => hooks.answer(c, action, text);
  let el;
  switch (c.kind) {
    case "text": {
      const quote = h3("blockquote", { class: "cb-needs-text" }, c.text);
      const body = h3(
        "div",
        null,
        h3(
          "p",
          { class: "cb-needs-what" },
          h3("code", null, key),
          ` · ${verb} with comment`
        ),
        quote
      );
      const four = [
        {
          label: "post and close",
          fill: true,
          run: () => answer("post-and-close", c.text)
        },
        {
          label: "edit text",
          run() {
            hooks.edit(c.id, c.text);
            startEdit(c.text, true);
          }
        },
        {
          label: "close without comment",
          run: () => answer("close-without-comment")
        },
        { label: "skip", run: () => answer("skip") }
      ];
      el = card({
        edge: "signal",
        head: c.question ? `needs you · ${c.question}` : "needs you",
        body,
        actions: four
      });
      const startEdit = (text, focus) => {
        const field = h3("textarea", {
          class: "cb-needs-edit",
          "aria-label": "comment",
          rows: 4
        });
        field.value = text;
        field.addEventListener("input", () => hooks.edit(c.id, field.value));
        quote.replaceWith(field);
        el.querySelector(".kit-btns")?.replaceWith(
          buttons([
            {
              label: "save text",
              fill: true,
              run: () => hooks.answer(
                c,
                "edit-text",
                field.value,
                () => hooks.edit(c.id, null)
              )
            },
            {
              label: "cancel",
              run() {
                hooks.edit(c.id, null);
                field.replaceWith(quote);
                el.querySelector(".kit-btns")?.replaceWith(buttons(four));
              }
            }
          ])
        );
        if (focus) field.focus();
      };
      const draft = hooks.editing(c.id);
      if (draft !== void 0) startEdit(draft, false);
      break;
    }
    case "batch":
      el = card({
        edge: "signal",
        head: "needs you · confirm the batch",
        body: h3("p", null, c.question),
        actions: [
          { label: "confirm", fill: true, run: () => answer("confirm") },
          { label: "skip batch", run: () => answer("skip-batch") }
        ]
      });
      break;
    case "failed": {
      const bs = [];
      if (j.session)
        bs.push({
          label: `hand to ${agent}`,
          run: () => answer("hand-to-agent")
        });
      bs.push({ label: "skip", run: () => answer("skip") });
      el = card({
        edge: "signal",
        head: `needs you · ${key || "a step"} failed`,
        body: h3(
          "div",
          null,
          h3("p", null, c.question),
          step ? h3("code", { class: "cb-cmd" }, step.command) : null
        ),
        actions: bs
      });
      break;
    }
    case "paused":
      el = card({
        edge: "agent",
        head: `${agent} paused`,
        body: h3(
          "p",
          null,
          key ? h3("code", null, key) : null,
          key ? ": " : "",
          c.question
        ),
        actions: [
          {
            label: `${verb} anyway`,
            danger: dangerVerb(verb),
            run: () => answer("resume")
          },
          { label: "skip", run: () => answer("skip") }
        ]
      });
      break;
    default:
      el = card({
        edge: "signal",
        head: `needs you · ${c.kind}`,
        body: c.question
      });
  }
  el.classList.add("cb-needs-card");
  el.dataset.card = String(c.id);
  el.dataset.kind = c.kind;
  return el;
}

// attention.ts
import {
  list as list2,
  createSelection,
  h as h7
} from "/_kit/kit.js";

// item.ts
import { h as h5, facts as facts2, fold } from "/_kit/kit.js";

// proposals.ts
import { card as card2, sheet as sheet2, noteField as noteField2, h as h4 } from "/_kit/kit.js";
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
  const errEl = h4("p", { class: "cb-sheet-err" });
  errEl.hidden = true;
  const body = h4(
    "div",
    { class: "cb-sheet-body" },
    h4(
      "div",
      { class: "cb-sheet-row" },
      h4("label", { class: "cb-sheet-label" }, "reason"),
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
    const noteEl = h4("p", { class: "cb-proposal-note" });
    noteEl.textContent = p.note;
    lines.push(noteEl);
  }
  const bodyEl = h4("div", { class: "cb-proposal-body" }, ...lines);
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
  const el = card2({ edge: "agent", head, body: bodyEl, actions });
  el.classList.add("cb-proposal-card");
  return el;
}
function bulkProposalActions(ctx, onDone) {
  let _ids = [];
  let _keys = [];
  const acceptBtn = h4("button", {
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
  const rejectBtn = h4("button", {
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
  const el = h4("div", { class: "cb-prop-bulk" }, acceptBtn, rejectBtn);
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
  const section = h5("section", { class: "cb-evidence" });
  section.append(h5("h3", { class: "kit-label" }, "evidence"));
  if (evs.length === 0) {
    section.append(h5("p", { class: "cb-empty" }, "no evidence"));
    return section;
  }
  for (const ev of evs) {
    const authorEl = ev.author ? h5("span", { class: "cb-evidence-author" }, ev.author + " ") : null;
    const time = h5(
      "span",
      { class: "cb-muted" },
      ` · ${fmtShortDate(ev.created_at)}`
    );
    const item = h5(
      "div",
      { class: "cb-evidence-item" },
      h5(
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
  const section = h5("section", { class: "cb-history" });
  section.append(h5("h3", { class: "kit-label" }, "history"));
  if (events.length === 0 && decisions.length === 0) {
    section.append(h5("p", { class: "cb-empty" }, "no history"));
    return section;
  }
  for (const entry of decisions) {
    const display = stripOwnKey(entry.Subject, ownKey);
    section.append(
      h5(
        "div",
        { class: "cb-history-item cb-history-decision" },
        h5("span", { class: "cb-history-time" }, fmtShortDate(entry.Time)),
        h5("span", { class: "cb-history-msg" }, display)
      )
    );
  }
  for (const ev of events) {
    const label = ev.actions && ev.actions.length > 0 ? ev.actions.map((a) => a.hook ?? "").filter(Boolean).join(", ") : ev.hook ?? ev.src;
    section.append(
      h5(
        "div",
        { class: "cb-history-item" },
        h5("span", { class: "cb-history-time" }, fmtShortDate(ev.ts)),
        h5("span", { class: "cb-history-msg" }, label)
      )
    );
  }
  return section;
}
function renderDecideSection(ctx, key, kind) {
  const section = h5("section", { class: "cb-decide" });
  section.append(h5("h3", { class: "kit-label" }, "decide"));
  const dispRow = h5("div", { class: "cb-decide-btns" });
  section.append(dispRow);
  void getVocab(ctx).then((vocab2) => {
    const vocabKind = (vocab2.kinds ?? []).find((k) => k.kind === kind);
    const kindAllowed = allowedForKind(vocab2, kind);
    const needsUntilSet = new Set(vocabKind?.needs_until ?? []);
    for (const d of kindAllowed) {
      const label = needsUntilSet.has(d) ? `${d}…` : d;
      dispRow.append(
        h5(
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
  const el = h5("article", { class: "cb-item" });
  const displayKey = keyWithoutKind(it.key);
  const kickerParts = [it.kind, displayKey, it.relation].filter(Boolean).join(" · ");
  el.append(h5("p", { class: "cb-kicker kit-kick" }, kickerParts));
  el.append(
    h5("h1", { class: "cb-title kit-h1" }, it.title ?? keyWithoutKind(it.key))
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
  el.append(facts2(factPairs));
  if (it.body) {
    const excerpt = it.body.slice(0, BODY_CAP);
    const rest = it.body.slice(BODY_CAP);
    const bodyWrap = h5("div", { class: "cb-body" });
    bodyWrap.append(h5("p", null, excerpt));
    if (rest) {
      bodyWrap.append(fold("read more", h5("p", null, rest)));
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
  return h5("div", { class: "kit-doc" }, el);
}

// board.ts
import { h as h6 } from "/_kit/kit.js";
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
  const el = h6("div", { class: "cb-board" });
  const laneState = new Map(
    LANES.map(({ id }) => [id, { items: [], total: 0 }])
  );
  const laneRowsEl = /* @__PURE__ */ new Map();
  const laneMoreEl = /* @__PURE__ */ new Map();
  for (const { id, label } of LANES) {
    const headEl = h6("div", { class: "cb-lane-head" });
    headEl.textContent = label;
    const rowsEl = h6("div", { class: "cb-lane-rows" });
    laneRowsEl.set(id, rowsEl);
    const moreEl = h6("button", { class: "cb-lane-more", hidden: true });
    moreEl.textContent = "show more";
    moreEl.addEventListener("click", () => {
      void loadMore(id);
    });
    laneMoreEl.set(id, moreEl);
    el.append(
      h6("div", { class: "cb-lane", "data-lane": id }, headEl, rowsEl, moreEl)
    );
  }
  function buildCard(it, laneId) {
    const selected = sel.has(it.key);
    const box = h6("span", { class: "kit-box" + (selected ? " on" : "") });
    const kk = h6("span", { class: "cb-card-kk" });
    const displayKey = it.kind ? keyWithoutKind(it.key) : it.key;
    kk.textContent = it.kind ? `${it.kind} · ${displayKey}` : displayKey;
    const titleEl = h6("div", { class: "cb-card-title" });
    titleEl.textContent = it.title ?? displayKey;
    const card6 = h6(
      "div",
      {
        // Use .kit-card for background/border/radius from the kit;
        // .on marks the card as selected (adds signal-coloured left shadow).
        class: "kit-card" + (selected ? " on" : ""),
        tabindex: "0",
        "data-id": it.key
      },
      h6("div", { class: "kit-card-head" }, box, kk),
      titleEl
    );
    const age = ageOf(it);
    if (age) {
      const ageEl = h6("div", { class: "cb-card-age" });
      ageEl.textContent = age;
      card6.append(ageEl);
    }
    if (it.proposal) {
      const propEl = h6("div", { class: "cb-card-prop" });
      propEl.textContent = `${agentFromSource(it.proposal.source)} proposes ${it.proposal.disposition}`;
      card6.append(propEl);
    }
    titleEl.addEventListener("click", (e) => {
      e.stopPropagation();
      onOpen?.(it.key, laneId);
      ctx.route.go("item", it.key);
    });
    card6.addEventListener("click", (e) => {
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
    card6.addEventListener("keydown", (e) => {
      if (e.key === "o" || e.key === "Enter") {
        e.preventDefault();
        onOpen?.(it.key, laneId);
        ctx.route.go("item", it.key);
      }
    });
    return card6;
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
      cards.forEach((card6, i) => {
        const it = state.items[i];
        if (!it) return;
        const selected = sel.has(it.key);
        card6.classList.toggle("on", selected);
        card6.querySelector(".kit-box")?.classList.toggle("on", selected);
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
var VIEWS2 = [
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
  const readEl = h7("div", { class: "kit-read" });
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
    const nameEl = h7(
      "p",
      { class: "cb-read-empty-section kit-label" },
      "attention"
    );
    const countEl = h7(
      "p",
      { class: "cb-read-empty-count" },
      pluralize(totalItemsForView, "item")
    );
    const promptEl = h7(
      "p",
      { class: "cb-read-empty-prompt" },
      "Select an item to see it here."
    );
    return h7("div", { class: "cb-read-empty" }, nameEl, countEl, promptEl);
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
    const selCount = h7(
      "span",
      { class: "cb-sel-count", hidden: true },
      "0 selected"
    );
    const selAllBtn = h7(
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
    const footRight = h7(
      "div",
      { class: "cb-foot-right" },
      propFoot.el,
      h7(
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
    footEl = h7("div", { class: "cb-foot" }, selCount, selAllBtn, footRight);
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
  const handle = list2({
    label: "attention",
    views: VIEWS2.map((v) => ({ ...v, on: v.id === filters.view })),
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
    return VIEWS2.map((v) => ({
      ...v,
      on: v.id === activeId,
      count: viewCounts2[v.id]
    }));
  }
  function applyCounts(counts) {
    if (!counts) return;
    for (const v of VIEWS2) {
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
    if (VIEWS2.some((v) => v.id === view) && view !== "board") {
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
import { h as h10, card as card4 } from "/_kit/kit.js";

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
import { h as h8 } from "/_kit/kit.js";

// attached.ts
var KIND_PLURAL = { branch: "branches" };
function isEmpty(a) {
  return !a.keys?.length && !a.open && !a.rule && !a.job && !a.section;
}
function attachedLabel(a, jobTitle2 = "") {
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
  if (a.job)
    parts.push(
      jobTitle2 ? `job #${a.job} · ${jobTitle2.toLowerCase()}` : `job #${a.job}`
    );
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
  let contextTitle = "";
  let override = null;
  let sending = false;
  const effective = () => override ?? context;
  const value = h8("span", {
    class: "cb-comp-attached-value",
    contenteditable: "true",
    role: "textbox",
    "aria-label": "attached",
    spellcheck: false,
    "data-testid": "composer-attached"
  });
  const line = h8(
    "div",
    { class: "cb-comp-attached" },
    h8("span", { class: "cb-comp-attached-prefix" }, "attached: "),
    value
  );
  let editing = false;
  function renderAttached() {
    if (editing) return;
    const a = effective();
    const title = a.job && a.job === context.job ? contextTitle : "";
    value.textContent = isEmpty(a) ? "nothing" : attachedLabel(a, title);
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
  const input = h8("textarea", {
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
  const note = h8("span", { class: "cb-comp-note", role: "status" });
  const footer = h8(
    "div",
    { class: "cb-comp-foot" },
    h8("span", {}, "↵ send · ⌘↵ add to batch"),
    note
  );
  const el = h8(
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
    setAttached(a, jobTitle2 = "") {
      context = a;
      contextTitle = jobTitle2;
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
import { h as h9, card as card3, fold as fold2, noteField as noteField3 } from "/_kit/kit.js";
function makeBatchTray(ctx) {
  let thread = 0;
  let batch = 0;
  let drafts = [];
  let loadSeq = 0;
  const label = h9("span", { class: "kit-card-head cb-batch-label" });
  const sendBtn = h9("button", {
    class: "kit-btn fill cb-batch-send",
    "data-testid": "batch-send",
    onclick() {
      void sendBatch();
    }
  });
  const list4 = h9("div", { class: "cb-batch-drafts" });
  const el = card3({
    body: h9(
      "div",
      {},
      h9("div", { class: "cb-batch-head" }, label, sendBtn),
      list4
    )
  });
  el.classList.add("cb-batch");
  el.setAttribute("data-testid", "batch-tray");
  el.hidden = true;
  function render() {
    el.hidden = drafts.length === 0;
    if (!drafts.length) {
      list4.replaceChildren();
      return;
    }
    label.textContent = `batch · ${pluralize(drafts.length, "draft")}`;
    sendBtn.textContent = `send ${drafts.length}`;
    list4.replaceChildren(...drafts.map((d, i) => draftRow(d, i)));
  }
  function actionBtn(text, action, aria, run, disabled = false) {
    return h9(
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
    const text = h9("span", { class: "cb-batch-text" }, d.body);
    const row = h9(
      "div",
      { class: "cb-batch-draft", "data-draft-id": String(d.id) },
      text,
      h9(
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
  const text = h9("span", { class: "cb-prog-text" });
  const age = h9("span", { class: "cb-prog-age" });
  const fill = h9("span", { class: "cb-prog-fill" });
  const bar2 = h9("div", { class: "cb-prog-bar", role: "progressbar" }, fill);
  const el = h9(
    "div",
    { class: "cb-prog", "data-testid": "progress-line" },
    h9(
      "div",
      { class: "cb-prog-row" },
      h9("span", { class: "cb-prog-dot" }),
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
  const count = h9("span", {});
  const el = h9(
    "div",
    { class: "cb-wait", "data-testid": "waiting-strip" },
    h9("span", { class: "cb-wait-word" }, "waiting"),
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
  const history2 = h9(
    "ol",
    { class: "cb-worked-lines" },
    ...lines.map(
      (l) => h9(
        "li",
        { class: "cb-worked-line" },
        h9("span", { class: "cb-worked-at" }, clock(l.at)),
        h9("span", {}, l.text + countText(l.n, l.total))
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
  const wrap = h10("div", { class: "cb-dock-body" });
  const bodyP = h10("p", { class: "cb-dock-bodytext" });
  bodyP.textContent = msg.body;
  wrap.append(bodyP);
  const { attached } = msg;
  if (attached) {
    const links = [];
    if (attached.keys && attached.keys.length > 0) {
      const shown = attached.keys.slice(0, 5);
      for (const k of shown) {
        const a = h10("a", {
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
          h10(
            "span",
            { class: "cb-dock-link-more" },
            `+${attached.keys.length - 5} more`
          )
        );
      }
    }
    if (attached.open) {
      const k = attached.open;
      const a = h10("a", {
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
      const a = h10("a", {
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
      const a = h10("a", {
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
      const linkRow = h10("div", { class: "cb-dock-links" }, ...links);
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
  const headEl = h10(
    "div",
    { class: "cb-dock-ch" },
    h10(
      "span",
      {
        class: isAgent ? "cb-dock-ch-name cb-dock-ch-agent" : "cb-dock-ch-name"
      },
      authorLabel
    ),
    h10("span", { class: "cb-dock-ch-age" }, age)
  );
  let bodyContent;
  if (isAgent) {
    bodyContent = renderAgentBody(msg);
  } else {
    const bodyEl = h10("div", { class: "cb-dock-body" });
    const bodyP = h10("p", { class: "cb-dock-bodytext" });
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
      stateEl = h10("div", {
        class: "cb-dock-state",
        "data-state": msg.state,
        style: `color:${stateColor(msg.state)}`
      });
      stateEl.textContent = stateLabel(msg);
    }
  }
  const fullBody = h10(
    "div",
    { class: "cb-dock-card-body" },
    headEl,
    bodyContent
  );
  if (stateEl) fullBody.append(stateEl);
  const isStuckDelivery = !isAgent && delivery?.stuck === true && msg.delivery_id === delivery.id;
  if (isStuckDelivery && delivery) {
    if (stateEl) {
      const ageText2 = fmtAge(delivery.touched_at);
      const ageStr = ageText2 === "now" ? "just now" : `${ageText2} ago`;
      stateEl.textContent = "";
      stateEl.setAttribute("data-state", "stuck");
      const stuckBold = h10("strong", { style: "font-weight:600" });
      stuckBold.textContent = "stuck";
      stateEl.append(`delivered ${ageStr} · `, stuckBold);
    }
    const d = delivery;
    const releaseBtn = h10("button", {
      class: "kit-btn",
      "data-action": "release",
      onclick() {
        void releaseDelivery(ctx, d.id);
      }
    });
    releaseBtn.textContent = "release";
    const otherSessions = sessions.filter((s) => s.id !== d.session_id);
    const moveBtn = h10("button", {
      class: "kit-btn",
      "data-action": "move",
      onclick() {
        void openMoveSheet(ctx, d, otherSessions);
      }
    });
    moveBtn.textContent = "move to another session";
    fullBody.append(h10("div", { class: "cb-dock-stuck" }, releaseBtn, moveBtn));
  }
  const el = card4({
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
    const el = h10("button", { class: "cb-dock-pick-item" });
    el.textContent = sessionLabel(s);
    el.onclick = () => {
      void moveDelivery(ctx, delivery.id, s.id);
      sheet4.close();
    };
    return el;
  });
  if (items.length === 0) {
    const noOther = h10(
      "p",
      { class: "cb-dock-pick-empty" },
      "no other sessions"
    );
    items.push(noOther);
  }
  const content = h10("div", { class: "cb-dock-pick-list" }, ...items);
  const sheet4 = {
    el: h10("div", { class: "cb-dock-pick-sheet" }, content),
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
    const el = h10("button", { class: "cb-dock-pick-item" });
    el.textContent = sessionLabel(s);
    el.onclick = () => {
      void moveSession(ctx, fromSession, s.id);
      sheet4.close();
    };
    return el;
  });
  if (items.length === 0) {
    const noOther = h10(
      "p",
      { class: "cb-dock-pick-empty" },
      "no other sessions"
    );
    items.push(noOther);
  }
  const content = h10("div", { class: "cb-dock-pick-list" }, ...items);
  const sheet4 = {
    el: h10("div", { class: "cb-dock-pick-sheet" }, content),
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
    const el = h10("button", {
      class: "cb-dock-pick-item" + (s.id === currentId ? " cb-dock-pick-item--on" : "")
    });
    el.textContent = label;
    el.onclick = () => {
      onSelect(s.id);
      picker.remove();
    };
    return el;
  });
  const picker = h10(
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
  const sessionDot = h10("span", { class: "cb-dock-dot" });
  const sessionLabelEl = h10("span", { class: "cb-dock-session-label" });
  const sessionPickerBtn = h10("button", {
    class: "cb-dock-agent-btn",
    "aria-label": "pick agent session",
    onclick(e) {
      e.stopPropagation();
      if (!sessions.length) return;
      const p = buildSessionPicker(sessions, currentSessionId, (id) => {
        lastUsedSessionId = id;
        ctx.setDockSession(id);
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
  const leftStatusRow = h10("div", { class: "cb-dock-left-row" });
  leftStatusRow.hidden = true;
  const sessionHeader = h10(
    "div",
    { class: "cb-dock-header" },
    h10(
      "div",
      { class: "cb-dock-header-row" },
      h10("span", { class: "cb-dock-who" }, sessionDot, sessionLabelEl),
      sessionPickerBtn
    ),
    leftStatusRow
  );
  const threadChips = h10("div", { class: "cb-dock-threads" });
  const messageArea = h10("div", {
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
  const rail = h10("div", { class: "cb-dock-inner" });
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
      const moveLink = h10("button", {
        class: "cb-dock-move-link",
        "data-testid": "dock-move-link",
        onclick(e) {
          e.stopPropagation();
          const others = sessions.filter((s) => s.id !== sess.id);
          void openSessionMoveSheet(ctx, sess.id, others);
        }
      });
      moveLink.textContent = "move to…";
      const parts = [h10("span", {}, "left")];
      if (sess.queued > 0) {
        parts.push(h10("span", { class: "cb-dock-sep" }, "·"));
        parts.push(h10("span", {}, `${sess.queued} queued`));
      }
      parts.push(h10("span", { class: "cb-dock-sep" }, "·"));
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
      const chip = h10("button", {
        class: "kit-chip" + (t.id === currentThreadId ? " on" : ""),
        "data-thread": String(t.id),
        onclick() {
          void switchThread(t.id);
        }
      });
      chip.textContent = t.name;
      threadChips.append(chip);
    }
    const addChip = h10("button", {
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
      const empty = h10("p", { class: "cb-dock-empty" }, "no messages");
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
    setAttached(a, jobTitle2) {
      composer.setAttached(a, jobTitle2);
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
  list as list3,
  h as h12,
  facts as facts3,
  buttons as buttons2,
  card as card5,
  sheet as sheet3,
  ApiError as ApiError2
} from "/_kit/kit.js";

// conditions.ts
import { h as h11 } from "/_kit/kit.js";

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
    const list4 = items();
    if (!list4.length) return;
    const at2 = list4.indexOf(document.activeElement);
    let next = -1;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowRight":
        next = at2 < 0 ? 0 : (at2 + 1) % list4.length;
        break;
      case "ArrowUp":
      case "ArrowLeft":
        next = at2 < 0 ? list4.length - 1 : (at2 - 1 + list4.length) % list4.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = list4.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    e.stopPropagation();
    rove(list4[next]);
    list4[next].focus();
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
  const el = h11(
    "select",
    { class: cls, "aria-label": label },
    ...options.map((o) => h11("option", { value: o }, o))
  );
  if (!options.includes(value)) {
    el.prepend(h11("option", { value }, value));
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
  const el = h11("div", {
    class: "kit-table cb-conds",
    "data-testid": "conditions"
  });
  const rowsEl = h11("div", { class: "cb-cond-rows" });
  const general = h11("div", { class: "cb-cond-err", hidden: true });
  el.append(rowsEl, general);
  const changed = () => onChange(conds.map((c) => ({ ...c })));
  function valueControl(i, f) {
    const c = conds[i];
    if (!editable || !f) {
      return h11("span", { class: "cb-cond-v" }, c.value);
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
    const input = h11("input", {
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
    }) : h11("span", { class: "cb-cond-o" }, c.op);
    const remove = editable ? h11(
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
    return h11(
      "div",
      { class: "cb-cond", "data-index": i },
      h11(
        "div",
        { class: "kit-tr cb-cond-row" + (editable ? " cb-cond-edit" : "") },
        h11("span", { class: "cb-cond-f" }, c.field),
        op,
        valueControl(i, f),
        remove
      ),
      h11("div", { class: "cb-cond-err", hidden: true })
    );
  }
  function render() {
    rowsEl.replaceChildren(...conds.map((_, i) => row(i)));
    const e = errors.get(el);
    if (e) setConditionError(el, e.index, e.message);
  }
  const menu = h11("div", {
    class: "cb-cond-menu",
    role: "menu",
    "aria-label": "add a condition",
    hidden: true
  });
  const add = h11(
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
        (f) => h11(
          "div",
          { class: "cb-cond-menu-row", "data-field": f.name },
          h11("span", { class: "cb-cond-menu-f" }, f.name),
          h11(
            "span",
            { class: "cb-cond-menu-ops" },
            ...(f.ops ?? []).map(
              (op) => h11(
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
function setConditionError(editor, index, message3) {
  if (message3 === null) errors.delete(editor);
  else errors.set(editor, { index, message: message3 });
  for (const e of editor.querySelectorAll(".cb-cond-err")) {
    e.hidden = true;
    e.textContent = "";
  }
  for (const r2 of editor.querySelectorAll(".cb-cond[data-invalid]")) {
    r2.removeAttribute("data-invalid");
  }
  if (message3 === null) return;
  const r = editor.querySelector(
    `.cb-cond[data-index="${index}"]`
  );
  const slot = r ? r.querySelector(".cb-cond-err") : editor.querySelector(":scope > .cb-cond-err");
  r?.setAttribute("data-invalid", "");
  if (slot) {
    slot.textContent = message3;
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
function conditionErrorIndex(message3) {
  const m = /^condition (\d+): /.exec(message3);
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
var PAGE2 = 200;
var VIEWS3 = [
  { id: "all", label: "all" },
  { id: "active", label: "active" },
  { id: "drafts", label: "drafts" }
];
function message2(err) {
  return err instanceof Error ? err.message : String(err);
}
var isConflict = (err) => err instanceof ApiError2 && err.status === 409;
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
  let queue = Promise.resolve();
  const inTurn = (task) => {
    const run = queue.then(task);
    queue = run.catch(() => {
    });
    return run;
  };
  const doc = h12("article", { class: "cb-rule", "data-rule": id });
  const el = h12("div", { class: "kit-doc" }, doc);
  const dirty = () => !sameConditions(work, base.match ?? []) || !sameAction(workPropose, base.propose);
  const isDraft = () => base.status !== "active";
  const editable = () => isDraft() || !!saved.invalid;
  const body = () => ({
    ...base,
    match: work,
    propose: workPropose,
    exclude
  });
  let factsEl = h12("div");
  let editor = h12("div");
  const titleEl = h12("h1", { class: "kit-h1" });
  const kickEl = h12("p", { class: "kit-kick" });
  const proseEl = h12("p");
  const invalidEl = h12("div", { "data-testid": "rule-invalid" });
  const conflictEl = h12("div", { "data-testid": "rule-conflict" });
  const proposeEl = h12("div");
  const proposeErrEl = h12("p", {
    class: "cb-cond-err cb-propose-err",
    "data-testid": "propose-err",
    hidden: true
  });
  const heading = h12("h3", { class: "kit-label cb-matches-label" });
  const groupChips = h12("span", { class: "cb-matches-view" });
  const matchesEl = h12("div", {
    class: "kit-table cb-matches",
    "data-testid": "matches"
  });
  const actionsEl = h12("div", { class: "cb-rule-actions" });
  const note = h12("p", { class: "cb-rule-note", role: "status" });
  function drawFacts() {
    const who = authorOf(base.created_by);
    const byEl = h12("b", who.agent ? { class: "cb-by-agent" } : null, who.name);
    const next = facts3([
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
        h12("span", { class: "cb-by-agent" }, who.name),
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
      card5({
        edge: "signal",
        head: skipped ? "not valid · active, but skipped at every sync" : "not valid",
        body: h12(
          "div",
          null,
          h12("p", { class: "cb-invalid-msg" }, saved.invalid),
          h12(
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
    const tr = (label, value) => h12(
      "div",
      { class: "kit-tr" },
      h12("span", { class: "cb-cond-f" }, label),
      h12("span"),
      value
    );
    if (!editable()) {
      drawChoices = () => {
      };
      proposeEl.replaceChildren(
        h12(
          "div",
          { class: "kit-table cb-propose", "data-testid": "propose" },
          tr(
            "disposition",
            h12(
              "span",
              {
                class: "cb-cond-v" + (DANGER_DISPS.has(p.disposition) ? " cb-danger" : "")
              },
              p.disposition
            )
          ),
          p.until ? tr("until", h12("span", { class: "cb-cond-v" }, p.until)) : null,
          p.note ? tr("note", h12("span", { class: "cb-cond-v" }, p.note)) : null
        )
      );
      return;
    }
    const pick = h12("button", {
      type: "button",
      class: "cb-cond-v cb-disp",
      "aria-label": "disposition",
      "aria-haspopup": "menu",
      "aria-expanded": "false",
      onclick() {
        setMenu(menu.hidden);
      }
    });
    const menu = h12("div", {
      class: "cb-disp-menu",
      role: "menu",
      "aria-label": "dispositions",
      hidden: true
    });
    const menuChips = h12("span", { class: "cb-disp-opts" });
    const whyEl = h12("span", {
      class: "cb-disp-why",
      "data-testid": "disp-why"
    });
    menu.append(
      h12("span"),
      h12("span"),
      h12("span", { class: "cb-disp-col" }, menuChips, whyEl)
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
    let chipsFor = "";
    drawChoices = () => {
      const cur = workPropose.disposition;
      pick.textContent = cur || "choose…";
      pick.dataset.value = cur;
      pick.classList.toggle("cb-danger", DANGER_DISPS.has(cur));
      whyEl.textContent = kindHint(work, dispositions, allDispositions);
      drawProposeErr();
      const sig = `${cur}
${dispositions.join(" ")}`;
      if (sig === chipsFor) return;
      chipsFor = sig;
      menuChips.replaceChildren(
        ...dispositions.map(
          (d) => h12(
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
    };
    const text = (field, placeholder2) => {
      const input = h12("input", {
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
    const hint = (testid) => h12(
      "div",
      { class: "cb-prop-hint", "data-testid": testid },
      h12("span"),
      h12("span"),
      h12("span", { class: "cb-prop-hint-t" })
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
        out.push(h12("span", { class: "cb-prop-hint-i" }, w));
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
      h12(
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
    return h12("span", {
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
    return h12(
      "div",
      { class: "cb-mr on", "data-key": m.key, title: m.title || m.key },
      box(true, `exclude ${m.key}`, () => untick(m.key)),
      h12("span", { class: "cb-mr-k" }, keyWithoutKind(m.key)),
      h12("span", { class: "cb-mr-w" }, why)
    );
  }
  function excludedRow(x) {
    return h12(
      "div",
      { class: "cb-mr x", "data-key": x.key },
      box(false, `include ${x.key}`, () => void include(x.key)),
      h12("span", { class: "cb-mr-k" }, keyWithoutKind(x.key)),
      h12(
        "span",
        { class: "cb-mr-w" },
        x.reason ? `excluded · ${x.reason}` : "excluded"
      )
    );
  }
  function waitRow(text) {
    return h12(
      "div",
      { class: "cb-mr cb-mr-wait" },
      h12("span"),
      h12("span", { class: "cb-mr-k" }, text),
      h12("span")
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
          h12(
            "div",
            { class: "cb-mgroup", "data-repo": g.repo },
            h12("span", { class: "cb-mgroup-repo" }, g.repo),
            h12(
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
        h12(
          "div",
          { class: "cb-mr cb-mr-more" },
          h12("span"),
          h12("span", { class: "cb-mr-k" }, `… ${more} more`),
          h12(
            "button",
            {
              type: "button",
              class: "cb-link",
              onclick() {
                void loadMore();
              }
            },
            `show ${Math.min(PAGE2, more)} more`
          )
        )
      );
    }
    if (out.length === 0) out.push(waitRow("nothing matches now"));
    matchesEl.replaceChildren(...out);
  }
  function drawGroupChips() {
    const chip = (label, on) => h12(
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
  async function previewNow(depth = PAGE2) {
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
          `/rules/preview?offset=${rows.length}&limit=${PAGE2}`,
          b
        );
        if (mine !== seq || !(next.page ?? []).length) break;
        rows = [...rows, ...next.page ?? []];
      }
      if (mine === seq) setPreview({ ...p, page: rows });
    } catch (err) {
      if (mine !== seq) return;
      if (err instanceof ApiError2 && err.status === 400)
        setInvalid(err.message);
      else note.textContent = `preview failed: ${message2(err)}`;
    }
  }
  function schedule(keepDepth = false) {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      void previewNow(keepDepth ? Math.max(PAGE2, shown.length) : PAGE2);
    }, PREVIEW_DEBOUNCE_MS);
  }
  async function loadMore() {
    const q = `?offset=${shown.length}&limit=${PAGE2}`;
    const mine = seq;
    try {
      const p = await ctx.api.post(`/rules/preview${q}`, body());
      if (mine !== seq) return;
      shown = [...shown, ...p.page ?? []];
      preview = { ...p, page: shown };
      drawMatches();
    } catch (err) {
      note.textContent = `more matches failed: ${message2(err)}`;
    }
  }
  function applyExclusions(d) {
    exclude = d.rule.exclude ?? [];
    base = { ...base, exclude };
    if (dirty() || invalid) {
      void previewNow(Math.max(PAGE2, shown.length));
    } else {
      seq++;
      setPreview(d.matches);
    }
    drawFacts();
  }
  function untick(key) {
    const reason = h12("input", {
      class: "kit-note",
      type: "text",
      placeholder: "reason (optional)",
      "aria-label": "reason"
    });
    const err = h12("p", { class: "cb-sheet-err", hidden: true });
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
        err.textContent = message2(e);
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
      body: h12(
        "div",
        { class: "cb-sheet-body" },
        h12(
          "p",
          { class: "cb-sheet-preview" },
          "This rule skips it from now on. It stays listed here, unticked."
        ),
        h12(
          "div",
          { class: "cb-sheet-row" },
          h12("label", { class: "cb-sheet-label" }, "reason"),
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
      note.textContent = `not included: ${message2(err)}`;
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
    const theirsBody = h12(
      "div",
      null,
      h12(
        "p",
        { class: "cb-conflict-help" },
        "Your edits are kept below and not saved. Serve’s copy now:"
      ),
      theirs.name !== base.name ? h12("p", { class: "cb-conflict-name" }, theirs.name) : null,
      conditionEditor(
        ctx,
        { match: theirs.match, status: "active", editable: false },
        () => {
        }
      ),
      h12(
        "div",
        { class: "kit-table cb-propose" },
        h12(
          "div",
          { class: "kit-tr" },
          h12("span", { class: "cb-cond-f" }, "disposition"),
          h12("span"),
          h12(
            "span",
            {
              class: "cb-cond-v" + (DANGER_DISPS.has(p.disposition) ? " cb-danger" : ""),
              "data-testid": "conflict-disposition"
            },
            p.disposition
          )
        ),
        p.until ? h12(
          "div",
          { class: "kit-tr" },
          h12("span", { class: "cb-cond-f" }, "until"),
          h12("span"),
          h12("span", { class: "cb-cond-v" }, p.until)
        ) : null,
        p.note ? h12(
          "div",
          { class: "kit-tr" },
          h12("span", { class: "cb-cond-f" }, "note"),
          h12("span"),
          h12("span", { class: "cb-cond-v" }, p.note)
        ) : null
      ),
      h12("p", { class: "cb-conflict-note", role: "status" })
    );
    conflictEl.replaceChildren(
      card5({
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
      note.textContent = message2(err);
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
      else if (err instanceof ApiError2 && err.status === 400) {
        if (conditionErrorIndex(err.message) >= 0) setInvalid(err.message);
        else note.textContent = err.message;
      } else note.textContent = `not saved: ${message2(err)}`;
      return false;
    }
  }
  function lifecycle(verb) {
    return inTurn(() => lifecycleNow(verb));
  }
  async function lifecycleNow(verb) {
    if (verb === "deactivate" && isDraft()) return;
    if (verb === "activate" && !isDraft() && !dirty()) return;
    if (verb === "activate" && conflict) {
      refuseActivate();
      return;
    }
    if (verb === "activate" && saved.invalid && !dirty()) {
      refuseInvalid();
      return;
    }
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
      else note.textContent = `not ${verb}d: ${message2(err)}`;
    }
  }
  function proposeOnce() {
    return inTurn(proposeOnceNow);
  }
  async function proposeOnceNow() {
    try {
      if (dirty() && !await save()) return;
      const r = await ctx.api.post("/rules/propose-once", {
        id
      });
      note.replaceChildren(
        `${pluralize(r.proposed, "match", "matches")} proposed · `,
        h12(
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
      note.textContent = `not proposed: ${message2(err)}`;
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
    actionsEl.replaceChildren(bs.length ? buttons2(bs) : "", note);
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
    factsEl = h12("div");
    doc.replaceChildren(
      kickEl,
      titleEl,
      factsEl,
      proseEl,
      invalidEl,
      conflictEl,
      h12("h3", { class: "kit-label" }, "when an undecided item matches all of"),
      editor,
      h12("h3", { class: "kit-label" }, "propose"),
      proposeEl,
      h12("div", { class: "cb-matches-head" }, heading, groupChips),
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
  const readEl = h12("div", { class: "kit-read cb-rules-read" });
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
  const askEl = h12(
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
  const foot = h12(
    "div",
    { class: "cb-rules-foot" },
    h12(
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
    const name = h12("input", {
      class: "kit-note",
      type: "text",
      placeholder: "e.g. Landed branches → delete",
      "aria-label": "name"
    });
    const idIn = h12("input", {
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
    const err = h12("p", { class: "cb-sheet-err", hidden: true });
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
        err.textContent = message2(e);
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
      body: h12(
        "div",
        { class: "cb-sheet-body" },
        h12(
          "div",
          { class: "cb-sheet-row" },
          h12("label", { class: "cb-sheet-label" }, "name"),
          name
        ),
        h12(
          "div",
          { class: "cb-sheet-row" },
          h12("label", { class: "cb-sheet-label" }, "id"),
          idIn
        ),
        h12(
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
  const handle = list3({
    label: "rules",
    views: VIEWS3.map((v) => ({ ...v, on: v.id === view })),
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
      VIEWS3.map((v) => ({ ...v, on: v.id === view, count: c[v.id] }))
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
      h12(
        "div",
        { class: "cb-read-empty" },
        h12("p", { class: "cb-read-empty-section kit-label" }, "rules"),
        h12(
          "p",
          { class: "cb-read-empty-count" },
          pluralize(rows.length, "rule")
        ),
        h12(
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
        h12(
          "div",
          { class: "cb-read-empty" },
          h12("p", { class: "cb-read-empty-section kit-label" }, "rules"),
          h12("p", { class: "cb-read-empty-prompt" }, message2(err))
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
registerSection(makeApply);
registerDock(makeDock);
boot();
