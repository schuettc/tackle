// cull TypeScript test extractor: test()/it()/describe() (and Playwright's
// test.describe/test.only/test.skip/it.only/it.skip aliases) in
// *.test.ts(x)/*.spec.ts(x) -> cull TestCase JSON, one line each.
//
// Usage: node tsext.cjs <typescript-package-dir> <root> <relpath>...
//
// <typescript-package-dir> is a directory containing an installed
// "typescript" package (its package.json's main resolves to the compiler);
// <root> is the project root; each <relpath> is a test file path relative
// to <root> ('/'-separated), and must already satisfy the Go side's Match.
//
// Prints one JSON object per line: either a TestCase-shaped object (id,
// lang, framework, file, name, parent, body, context, callees, truncated,
// span: {start,end} byte offsets into the file) for each leaf test found,
// or {"skip": relpath, "reason": ...} for a relpath that failed to parse.
//
// Ports the Phase 0 extractor's (cull-calibration/extractors/tsext.cjs)
// context/callee rules unchanged (Object.create(null) maps to dodge
// prototype-key bugs). New here: containers include Playwright's
// test.describe, tests include test.only/test.skip/it.only/it.skip,
// byte-exact spans (TypeScript positions are UTF-16 code units, converted
// to UTF-8 byte offsets via a precomputed map), and the skip protocol for
// unparsable files.
"use strict";

const fs = require("fs");
const path = require("path");
const MAX_CTX = parseInt(process.env.CULL_MAX_CONTEXT_BYTES || "64000", 10);

// ts is the required typescript package; both extractMain and tidyMain set
// it (from their own tsDir argument) before using any of the helpers
// below that reference it.
let ts;

// bytePos[i] is the UTF-8 byte offset corresponding to the i-th UTF-16 code
// unit of text; bytePos[text.length] is the file's total byte length. A
// supplementary-plane code point occupies two UTF-16 code units (a
// surrogate pair) but is one indivisible token as far as the TS scanner is
// concerned, so both units map to the same byte offset.
function utf16ToByteMap(text) {
  const bytePos = new Array(text.length + 1);
  let byte = 0;
  let i = 0;
  while (i < text.length) {
    const cp = text.codePointAt(i);
    const units = cp > 0xffff ? 2 : 1;
    const byteLen = Buffer.byteLength(String.fromCodePoint(cp), "utf8");
    bytePos[i] = byte;
    if (units === 2) bytePos[i + 1] = byte;
    byte += byteLen;
    i += units;
  }
  bytePos[text.length] = byte;
  return bytePos;
}

function scriptKindFor(p) {
  return p.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
}

function parseFile(p) {
  const src = fs.readFileSync(p, "utf8");
  const sf = ts.createSourceFile(p, src, ts.ScriptTarget.Latest, true, scriptKindFor(p));
  return { sf, src };
}

// Top-level declarations by name -> source text.
function topDecls(sf) {
  const out = Object.create(null);
  for (const st of sf.statements) {
    const text = st.getFullText(sf).trim();
    if (
      (ts.isFunctionDeclaration(st) ||
        ts.isClassDeclaration(st) ||
        ts.isInterfaceDeclaration(st) ||
        ts.isEnumDeclaration(st) ||
        ts.isTypeAliasDeclaration(st)) &&
      st.name
    ) {
      out[st.name.text] = text;
    } else if (ts.isVariableStatement(st)) {
      for (const d of st.declarationList.declarations) if (ts.isIdentifier(d.name)) out[d.name.text] = text;
    }
  }
  return out;
}

function resolveImport(fromFile, spec) {
  if (!spec.startsWith(".")) return null;
  const base = path.resolve(path.dirname(fromFile), spec);
  for (const c of [base, base.replace(/\.js$/, ".ts"), base + ".ts", path.join(base, "index.ts")]) {
    if (fs.existsSync(c) && fs.statSync(c).isFile() && c.endsWith(".ts")) return c;
  }
  return null;
}

const declCache = Object.create(null);
function declsOf(p) {
  if (!(p in declCache)) declCache[p] = topDecls(parseFile(p).sf);
  return declCache[p];
}

function identifiers(node) {
  const out = new Set();
  (function walk(n) {
    if (ts.isIdentifier(n)) out.add(n.text);
    ts.forEachChild(n, walk);
  })(node);
  return out;
}

// calleePath returns ["test"] for `test(...)`, ["test","skip"] for
// `test.skip(...)`, or null for anything else.
function calleePath(expr) {
  if (ts.isIdentifier(expr)) return [expr.text];
  if (ts.isPropertyAccessExpression(expr) && ts.isIdentifier(expr.expression) && ts.isIdentifier(expr.name)) {
    return [expr.expression.text, expr.name.text];
  }
  return null;
}

function isContainerCall(fnPath) {
  if (!fnPath) return false;
  if (fnPath.length === 1) return fnPath[0] === "describe" || fnPath[0] === "suite";
  if (fnPath.length === 2) return fnPath[0] === "test" && fnPath[1] === "describe";
  return false;
}

function isTestCall(fnPath) {
  if (!fnPath) return false;
  if (fnPath.length === 1) return fnPath[0] === "test" || fnPath[0] === "it";
  if (fnPath.length === 2) return (fnPath[0] === "test" || fnPath[0] === "it") && (fnPath[1] === "only" || fnPath[1] === "skip");
  return false;
}

function frameworkFor(rel) {
  return /\.spec\.tsx?$/.test(rel) ? "playwright" : "node:test";
}

function extractMain(args) {
  const [tsDir, root, ...relpaths] = args;
  ts = require(tsDir);

  for (const rel of relpaths) {
  const file = path.join(root, rel);
  let src;
  try {
    src = fs.readFileSync(file, "utf8");
  } catch (e) {
    console.log(JSON.stringify({ skip: rel, reason: `read error: ${e.message}` }));
    continue;
  }

  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, scriptKindFor(file));
  // TypeScript's parser is error-tolerant: it always returns a SourceFile,
  // but records a diagnostic per syntax error on the internal
  // parseDiagnostics field. That is our "fails to parse" signal.
  if (sf.parseDiagnostics && sf.parseDiagnostics.length > 0) {
    const first = sf.parseDiagnostics[0];
    const msg =
      typeof first.messageText === "string" ? first.messageText : ts.flattenDiagnosticMessageText(first.messageText, " ");
    console.log(JSON.stringify({ skip: rel, reason: `parse error: ${msg}` }));
    continue;
  }

  const rawBytes = Buffer.from(src, "utf8");
  const byteAt = utf16ToByteMap(src);
  const local = topDecls(sf);
  const imports = Object.create(null);
  const nsImports = Object.create(null); // import * as ns -> file
  for (const st of sf.statements) {
    if (!ts.isImportDeclaration(st) || !st.importClause) continue;
    if (!ts.isStringLiteralLike(st.moduleSpecifier)) continue;
    const target = resolveImport(file, st.moduleSpecifier.text);
    if (!target) continue;
    const nb = st.importClause.namedBindings;
    if (nb && ts.isNamespaceImport(nb)) nsImports[nb.name.text] = target;
    if (nb && ts.isNamedImports(nb)) {
      for (const el of nb.elements) imports[el.name.text] = { file: target, orig: (el.propertyName ?? el.name).text };
    }
  }

  const idCounts = Object.create(null);
  const framework = frameworkFor(rel);

  function assignId(base) {
    idCounts[base] = (idCounts[base] ?? 0) + 1;
    const n = idCounts[base];
    return n === 1 ? base : `${base} #${n}`;
  }

  function emit(node, qual, parentId) {
    // Phase 0's body is node.getFullText(sf).trim(), which keeps leading
    // comments attached to the node; match that exactly while keeping the
    // span byte-exact by locating the trimmed text within the full text.
    const fullText = node.getFullText(sf);
    const trimmed = fullText.trim();
    const start16 = node.getFullStart() + fullText.indexOf(trimmed);
    const end16 = start16 + trimmed.length;
    const start = byteAt[start16];
    const end = byteAt[end16];
    const body = rawBytes.slice(start, end).toString("utf8");
    const used = identifiers(node);
    const ctx = [];
    const callees = [];
    let size = 0;
    let truncated = false;
    // soft: a referenced (not called) name that doesn't fit is left out
    // without counting as truncation.
    const add = (t, soft = false) => {
      if (size + t.length > MAX_CTX) {
        if (!soft) truncated = true;
        return false;
      }
      size += t.length;
      return true;
    };
    // Which identifiers / ns.NAME accesses are call targets (vs. merely
    // referenced).
    const calledIds = new Set();
    const calledNs = new Set();
    const nsRefs = [];
    (function walkCalls(n) {
      if (ts.isCallExpression(n)) {
        if (ts.isIdentifier(n.expression)) calledIds.add(n.expression.text);
        else if (
          ts.isPropertyAccessExpression(n.expression) &&
          ts.isIdentifier(n.expression.expression) &&
          ts.isIdentifier(n.expression.name)
        ) {
          calledNs.add(`${n.expression.expression.text}.${n.expression.name.text}`);
        }
      }
      // ns.NAME through a namespace import, in first-reference order.
      if (
        ts.isPropertyAccessExpression(n) &&
        ts.isIdentifier(n.expression) &&
        nsImports[n.expression.text] &&
        ts.isIdentifier(n.name)
      ) {
        nsRefs.push({ ns: n.expression.text, name: n.name.text });
      }
      ts.forEachChild(n, walkCalls);
    })(node);

    const addImported = (n, soft) => {
      const imp = imports[n];
      if (!imp) return;
      const s = declsOf(imp.file)[imp.orig];
      if (s && !callees.some((c) => c.symbol === n) && add(s, soft)) {
        callees.push({ symbol: n, file: path.relative(root, imp.file).split(path.sep).join("/"), source: s });
      }
    };
    const addNs = (ns, name, soft) => {
      const symbol = `${ns}.${name}`;
      const s = declsOf(nsImports[ns])[name];
      if (s && !callees.some((c) => c.symbol === symbol) && add(s, soft)) {
        callees.push({ symbol, file: path.relative(root, nsImports[ns]).split(path.sep).join("/"), source: s });
      }
    };

    // Setup/context and call targets first (overflow -> truncated), then
    // referenced names (overflow -> skipped).
    const sorted = [...used].sort();
    for (const n of sorted) {
      if (local[n] && add(local[n])) ctx.push(local[n]);
      if (calledIds.has(n)) addImported(n, false);
    }
    for (const { ns, name } of nsRefs) if (calledNs.has(`${ns}.${name}`)) addNs(ns, name, false);
    const nCallCallees = callees.length;
    for (const n of sorted) if (!calledIds.has(n)) addImported(n, true);
    for (const { ns, name } of nsRefs) if (!calledNs.has(`${ns}.${name}`)) addNs(ns, name, true);

    // The test only reads a project setting/class and compares it to fixed
    // values: code under test is non-empty and all merely referenced, no call
    // or `new` roots in an imported or same-file definition, and the test
    // callback takes no destructured fixtures. When unsure: false.
    function pinsSetting(testNode, nCall, cs) {
      if (nCall !== 0 || cs.length === 0) return false;
      const cb = testNode.arguments[testNode.arguments.length - 1];
      if (cb && (ts.isArrowFunction(cb) || ts.isFunctionExpression(cb))) {
        for (const p of cb.parameters) if (!ts.isIdentifier(p.name)) return false;
      }
      let ok = true;
      (function walk(n) {
        if (!ok) return;
        if (ts.isNewExpression(n) || ts.isCallExpression(n)) {
          let e = n.expression;
          while (
            ts.isPropertyAccessExpression(e) ||
            ts.isElementAccessExpression(e) ||
            ts.isNonNullExpression(e) ||
            ts.isParenthesizedExpression(e)
          ) {
            e = e.expression;
          }
          if (ts.isIdentifier(e) && (imports[e.text] || nsImports[e.text] || local[e.text])) ok = false;
          if (ts.isNewExpression(n) && !ts.isIdentifier(e)) ok = false;
        }
        ts.forEachChild(n, walk);
      })(testNode);
      return ok;
    }

    const id = assignId(`ts:${rel}:${qual}`);
    const obj = {
      id,
      lang: "typescript",
      framework,
      file: rel,
      name: qual,
      body,
      context: ctx.join("\n\n"),
      callees,
      truncated,
      span: { start, end },
    };
    if (parentId) obj.parent = parentId;
    if (pinsSetting(node, nCallCallees, callees)) obj.pins_setting = true;
    console.log(JSON.stringify(obj));
  }

  (function walk(node, describes) {
    if (ts.isCallExpression(node) && node.arguments.length >= 2) {
      const fnPath = calleePath(node.expression);
      const a0 = node.arguments[0];
      const title = ts.isStringLiteralLike(a0) ? a0.text : null;
      if (title && isContainerCall(fnPath)) {
        const nextDescribes = [...describes, title];
        ts.forEachChild(node, (c) => walk(c, nextDescribes));
        return;
      }
      if (title && isTestCall(fnPath)) {
        const qual = [...describes, title].join(" > ");
        const parentId = describes.length > 0 ? `ts:${rel}:${describes.join(" > ")}` : "";
        emit(node, qual, parentId);
        return;
      }
    }
    ts.forEachChild(node, (c) => walk(c, describes));
  })(sf, []);
  }
}

// splitLinesKeepEnds splits text into lines, each retaining its own
// original line ending ("\n", "\r\n", or none for a final line lacking
// one) -- the JS analogue of Python's str.splitlines(keepends=True), used
// so tidy's line-level rewrites don't disturb any other line's ending.
function splitLinesKeepEnds(text) {
  const lines = [];
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    if (text[i] === "\n") {
      lines.push(text.slice(start, i + 1));
      start = i + 1;
    }
  }
  if (start < text.length) lines.push(text.slice(start));
  return lines;
}

// collectUsedIdentifiers walks sf for every Identifier node outside of
// import declarations (which live only at the top level, so skipping
// ts.isImportDeclaration statements suffices): type positions are walked
// like anything else, so a name used only in a type annotation still
// counts as used.
function collectUsedIdentifiers(sf) {
  const used = new Set();
  (function walk(node) {
    if (ts.isImportDeclaration(node)) return;
    if (ts.isIdentifier(node)) used.add(node.text);
    ts.forEachChild(node, walk);
  })(sf);
  return used;
}

// collectCommentLines scans the full text for // and /* */ comments and
// returns the set of 0-indexed line numbers each one occupies (a
// multi-line /* */ comment occupies every line it spans). Used so tidy
// leaves any import statement with a comment on any of its lines
// completely untouched -- rewriting/deleting it would silently drop the
// comment, whether it's a same-line trailing comment (which lives in the
// trivia *after* the statement's own end, not inside it) or one on its
// own line inside a multi-line named-import list.
function collectCommentLines(sf, text) {
  const scanner = ts.createScanner(ts.ScriptTarget.Latest, false, sf.languageVariant, text);
  const lines = new Set();
  for (;;) {
    const kind = scanner.scan();
    if (kind === ts.SyntaxKind.EndOfFileToken) break;
    if (kind === ts.SyntaxKind.SingleLineCommentTrivia || kind === ts.SyntaxKind.MultiLineCommentTrivia) {
      const start = sf.getLineAndCharacterOfPosition(scanner.getTokenPos()).line;
      const end = sf.getLineAndCharacterOfPosition(scanner.getTextPos()).line;
      for (let l = start; l <= end; l++) lines.add(l);
    }
  }
  return lines;
}

// tidySource rewrites src (one file's full text) dropping import
// specifiers whose bound identifier is never used elsewhere in the file.
// A side-effect import (`import "x"`, no importClause) is always kept.
// An import declaration left with nothing used is deleted whole (with the
// blank lines after it, keeping the file's own separator); from one with
// some specifiers unused only those are cut, in place (never re-rendered). Every other byte of
// src is returned unchanged, including each line's own line ending. If
// src fails to parse, it is returned unchanged.
//
// Only sf.statements (top-level statements) are ever considered, so an
// import can never be "nested" here (ES import declarations are only
// legal at the top level of a module). An import declaration with a
// comment on any of its lines is left alone entirely. As a final safety
// net, the rewritten source is re-parsed before being returned; if that
// introduces new parse errors, the original source is returned unchanged
// with an empty removed list.
function tidySource(src, relpath, before) {
  let sf;
  try {
    sf = ts.createSourceFile(relpath || "input.ts", src, ts.ScriptTarget.Latest, true, scriptKindFor(relpath || ""));
  } catch (e) {
    return { source: src, removed: [] };
  }
  if (sf.parseDiagnostics && sf.parseDiagnostics.length > 0) {
    return { source: src, removed: [] };
  }

  const used = collectUsedIdentifiers(sf);
  // Only what the edit orphaned: a name used before (the pre-edit file)
  // and unused now. An import already unused before (React for classic
  // JSX, a plugin, a type-only side effect) is never touched.
  let usedBefore = new Set();
  try {
    const bsf = ts.createSourceFile(relpath || "input.ts", before || "", ts.ScriptTarget.Latest, true, scriptKindFor(relpath || ""));
    if (!(bsf.parseDiagnostics && bsf.parseDiagnostics.length > 0)) usedBefore = collectUsedIdentifiers(bsf);
  } catch (e) {
    usedBefore = new Set();
  }
  const dropped = (name) => !used.has(name) && usedBefore.has(name);
  const commentLines = collectCommentLines(sf, src);
  const lines = splitLinesKeepEnds(src);
  const deleted = new Set(); // 0-indexed lines of statements deleted whole
  const cuts = []; // [start, end) ranges cut in place inside kept statements
  const removed = [];

  // Lines holding more than one top-level statement (`import {a} from
  // "a"; import {b} from "b";`): the line-based edit below would take the
  // neighbour with it, so an import on such a line is left alone.
  const stmtCount = new Map();
  for (const st of sf.statements) {
    const s = sf.getLineAndCharacterOfPosition(st.getStart(sf, false)).line;
    const e = sf.getLineAndCharacterOfPosition(st.getEnd()).line;
    for (let l = s; l <= e; l++) stmtCount.set(l, (stmtCount.get(l) || 0) + 1);
  }

  for (const st of sf.statements) {
    if (!ts.isImportDeclaration(st)) continue;
    if (!st.importClause) continue; // side-effect import: always kept

    const startLine = sf.getLineAndCharacterOfPosition(st.getStart(sf, false)).line;
    const endLine = sf.getLineAndCharacterOfPosition(st.getEnd()).line;
    let sharesLine = false;
    for (let l = startLine; l <= endLine; l++) {
      if (stmtCount.get(l) > 1) sharesLine = true;
    }
    if (sharesLine) continue;
    let hasComment = false;
    for (let l = startLine; l <= endLine; l++) {
      if (commentLines.has(l)) {
        hasComment = true;
        break;
      }
    }
    if (hasComment) continue; // leave any import with a comment on one of its lines untouched

    const clause = st.importClause;
    const moduleText = st.moduleSpecifier.getText(sf);

    const hadDefault = !!clause.name;
    let keepDefault = null;
    if (hadDefault) {
      if (!dropped(clause.name.text)) keepDefault = clause.name.text;
      else removed.push(`${moduleText}.default`);
    }

    const hadNamespace = !!(clause.namedBindings && ts.isNamespaceImport(clause.namedBindings));
    let keepNamespace = null;
    if (hadNamespace) {
      if (!dropped(clause.namedBindings.name.text)) keepNamespace = clause.namedBindings.name.text;
      else removed.push(`${moduleText}.*`);
    }

    const hadNamed = !!(clause.namedBindings && ts.isNamedImports(clause.namedBindings));
    let keepNamed = null;
    if (hadNamed) {
      keepNamed = [];
      for (const el of clause.namedBindings.elements) {
        if (!dropped(el.name.text)) {
          keepNamed.push(el);
        } else {
          const orig = (el.propertyName ?? el.name).text;
          removed.push(`${moduleText}.${orig}`);
        }
      }
    }

    const nothingDropped =
      (!hadDefault || keepDefault) &&
      (!hadNamespace || keepNamespace) &&
      (!hadNamed || keepNamed.length === clause.namedBindings.elements.length);
    if (nothingDropped) continue;

    const keepAnything = keepDefault || keepNamespace || (keepNamed && keepNamed.length > 0);
    if (!keepAnything) {
      for (let l = startLine; l <= endLine; l++) deleted.add(l);
      continue;
    }

    // Cut the dropped parts in place; the statement is never re-rendered.
    const nb = clause.namedBindings;
    if (hadDefault && !keepDefault) {
      cuts.push([clause.name.getStart(sf), nb.getStart(sf)]); // "D, "
    } else if (hadDefault && nb && (hadNamespace ? !keepNamespace : keepNamed.length === 0)) {
      cuts.push([clause.name.getEnd(), nb.getEnd()]); // ", { b }"
    }
    if (hadNamed && keepNamed.length > 0 && keepNamed.length < nb.elements.length) {
      // A run of dropped names followed by a kept one goes up to it (its
      // own line in a multi-line list, else "b, "); a run at the end
      // goes from the previous kept name's end (", b").
      const els = nb.elements;
      const kept = new Set(keepNamed);
      let i = 0;
      while (i < els.length) {
        if (kept.has(els[i])) {
          i++;
          continue;
        }
        let j = i;
        while (j + 1 < els.length && !kept.has(els[j + 1])) j++;
        if (j + 1 < els.length) cuts.push([els[i].getStart(sf), els[j + 1].getStart(sf)]);
        else cuts.push([els[i - 1].getEnd(), els[j].getEnd()]);
        i = j + 1;
      }
    }
  }

  if (deleted.size === 0 && cuts.length === 0) return { source: src, removed: [] };

  // Settle the blank lines around each run of deleted statements: keep
  // the larger of the blank runs before and after it, or none at the
  // start or end of the file (the same rule apply uses for tests).
  const lineStarts = [0];
  for (const l of lines) lineStarts.push(lineStarts[lineStarts.length - 1] + l.length);
  const blank = (i) => lines[i].trim() === "";
  const n = lines.length;
  let i = 0;
  while (i < n) {
    if (!deleted.has(i)) {
      i++;
      continue;
    }
    let lo = i;
    while (lo > 0 && blank(lo - 1) && !deleted.has(lo - 1)) lo--;
    let hi = i;
    while (hi + 1 < n && (deleted.has(hi + 1) || blank(hi + 1))) hi++;
    let firstDel = i;
    let lastDel = i;
    for (let l = i; l <= hi; l++) if (deleted.has(l)) lastDel = l;
    let from = firstDel;
    let to = lastDel;
    const nBefore = firstDel - lo;
    const nAfter = hi - lastDel;
    if (lo === 0 || hi === n - 1) {
      from = lo;
      to = hi;
    } else if (nAfter > nBefore) {
      from = lo;
    } else {
      to = hi;
    }
    cuts.push([lineStarts[from], lineStarts[to + 1]]);
    i = hi + 1;
  }

  cuts.sort((x, y) => y[0] - x[0]);
  let outSource = src;
  for (const [a, b] of cuts) outSource = outSource.slice(0, a) + outSource.slice(b);

  // Test-only seam (see task-3-brief fix): forces the post-check below
  // to fail, so the revert-to-original path can be exercised without a
  // real bug. Never set outside tests.
  if (process.env.CULL_TIDY_TEST_FORCE_BROKEN) {
    outSource += "import {\n";
  }

  // Safety net: if the rewrite somehow produced a source with new parse
  // errors, return the original source untouched rather than emit a
  // broken file.
  let outDiagnostics = 0;
  try {
    const outSf = ts.createSourceFile(relpath || "input.ts", outSource, ts.ScriptTarget.Latest, true, scriptKindFor(relpath || ""));
    outDiagnostics = (outSf.parseDiagnostics && outSf.parseDiagnostics.length) || 0;
  } catch (e) {
    outDiagnostics = Infinity;
  }
  if (outDiagnostics > 0) {
    return { source: src, removed: [] };
  }

  return { source: outSource, removed };
}

function tidyMain(args) {
  const [tsDir, relpath, beforePath] = args;
  ts = require(tsDir);

  const chunks = [];
  process.stdin.on("data", (c) => chunks.push(c));
  process.stdin.on("end", () => {
    const buf = Buffer.concat(chunks);
    const src = buf.toString("utf8");
    // Not valid UTF-8: refuse (null = "no change"); never write U+FFFD back.
    if (!Buffer.from(src, "utf8").equals(buf)) {
      console.log(JSON.stringify({ source: null, removed: [] }));
      return;
    }
    const before = beforePath ? fs.readFileSync(beforePath, "utf8") : "";
    const { source, removed } = tidySource(src, relpath, before);
    console.log(JSON.stringify({ source, removed }));
  });
}

// Dispatch last, once every helper above is defined: extractMain and
// tidyMain both call functions/read consts defined later in this file
// (in source order) than this call site would otherwise be if it ran at
// the top -- const/let bindings are not initialized until their own
// statement runs, so calling into them too early is a ReferenceError.
const cliArgs = process.argv.slice(2);
if (cliArgs[0] === "--tidy") {
  tidyMain(cliArgs.slice(1));
} else {
  extractMain(cliArgs);
}
