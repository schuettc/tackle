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

const [tsDir, root, ...relpaths] = process.argv.slice(2);
const ts = require(tsDir);
const fs = require("fs");
const path = require("path");
const MAX_CTX = parseInt(process.env.CULL_MAX_CONTEXT_BYTES || "24000", 10);

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
  for (const st of sf.statements) {
    if (!ts.isImportDeclaration(st) || !st.importClause) continue;
    if (!ts.isStringLiteralLike(st.moduleSpecifier)) continue;
    const target = resolveImport(file, st.moduleSpecifier.text);
    if (!target) continue;
    const nb = st.importClause.namedBindings;
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
    const start = byteAt[node.getStart(sf)];
    const end = byteAt[node.getEnd()];
    const body = rawBytes.slice(start, end).toString("utf8");
    const used = identifiers(node);
    const ctx = [];
    const callees = [];
    let size = 0;
    let truncated = false;
    const add = (t) => {
      if (size + t.length > MAX_CTX) {
        truncated = true;
        return false;
      }
      size += t.length;
      return true;
    };
    for (const n of [...used].sort()) {
      if (local[n] && add(local[n])) ctx.push(local[n]);
      const imp = imports[n];
      if (imp) {
        const decls = declsOf(imp.file);
        const s = decls[imp.orig];
        if (s && !callees.some((c) => c.symbol === n) && add(s)) {
          callees.push({ symbol: n, file: path.relative(root, imp.file).split(path.sep).join("/"), source: s });
        }
      }
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
