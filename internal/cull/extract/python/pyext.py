"""cull Python test extractor: pytest tests -> cull TestCase JSON, one line each.

Usage: python3 pyext.py <root> <relpath>...

<root> is the project root; each <relpath> is a test file path relative to
<root> ('/'-separated), and must already satisfy the Go side's Match
(test_*.py or *_test.py).

Prints one JSON object per line: either a TestCase-shaped object (id, lang,
framework, file, name, parent, body, context, callees, truncated, span:
{start,end} byte offsets into the file) for each leaf test found, or
{"skip": relpath, "reason": ...} for a relpath that failed to parse.

Ports the Phase 0 extractor's (cull-calibration/extractors/pyext.py) context
rules unchanged: fixtures from the conftest chain plus used same-file defs
become Context; imported-module callee resolution is unchanged. New here:
byte-exact spans (computed from UTF-8 line starts, since ast lineno/col are
per-line and col is already UTF-8 bytes), class-method Parent (the class
itself is never emitted), and the skip protocol for unparsable files.
"""
import ast
import io
import json
import os
import sys
import textwrap
import tokenize

MAX_CTX = int(os.environ.get("CULL_MAX_CONTEXT_BYTES") or 64000)


def _import_bound_name(a):
    """The identifier a plain `import` alias binds in the namespace: its
    asname, or (for a dotted `import a.b.c` with no asname) the first
    component -- that's the name that shows up as a Name node when the
    code does `a.b.something()`."""
    return a.asname if a.asname else a.name.split(".")[0]


def _collect_used_names(tree):
    """Every name that counts as "used" for tidy purposes: real Name
    references anywhere (import statements never introduce Name nodes for
    the names they bind, so no filtering is needed there), function/lambda
    parameter names (pytest fixtures are matched by parameter name), and
    anything listed in a module-level `__all__`."""
    used = set()
    for n in ast.walk(tree):
        if isinstance(n, ast.Name):
            used.add(n.id)
        elif isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            args = n.args
            for a in list(getattr(args, "posonlyargs", []) or []) + list(args.args) + list(args.kwonlyargs):
                used.add(a.arg)
            if args.vararg:
                used.add(args.vararg.arg)
            if args.kwarg:
                used.add(args.kwarg.arg)
        elif isinstance(n, ast.Assign):
            for t in n.targets:
                if isinstance(t, ast.Name) and t.id == "__all__" and isinstance(n.value, (ast.List, ast.Tuple, ast.Set)):
                    for elt in n.value.elts:
                        if isinstance(elt, ast.Constant) and isinstance(elt.value, str):
                            used.add(elt.value)
    return used


def _collect_comment_lines(src):
    """Line numbers (1-indexed) that contain a `#` comment anywhere --
    trailing on a statement's own line, or on its own line inside a
    parenthesized multi-line statement. Used so tidy leaves any import
    statement with a comment on one of its lines completely untouched
    (rewriting/deleting it would silently drop the comment)."""
    lines = set()
    try:
        for tok in tokenize.generate_tokens(io.StringIO(src).readline):
            if tok.type == tokenize.COMMENT:
                lines.add(tok.start[0])
    except Exception:
        pass
    return lines


def tidy_source(data, before=b""):
    """Drop the imports an edit orphaned from a Python source file.
    Only an alias whose bound name was used in `before` (the pre-edit
    file) and is unused in `data` is dropped: an import that was already
    unused (an autouse fixture, a usefixtures("x") string, a plugin) is
    never touched.
    Returns (new_source, removed) where removed is a list of
    "module.name" (or bare "name" for plain `import name`) strings for
    every alias dropped. Every byte outside the changed import statements
    is left exactly as it was, including each line's own line ending.
    A source that is not valid UTF-8 returns (None, []): no change.
    `from x import *` and `from __future__ import ...` are never touched;
    a statement left with no used aliases is deleted whole (with the
    blank lines after it, keeping the file's own separator); from a
    statement with some unused aliases only those aliases are cut, in
    place (never re-rendered). If the source fails to parse, it is
    returned unchanged.

    Only module-level (top-level) import statements are ever touched: one
    nested inside any block (if/try/with/def/class, including a
    `TYPE_CHECKING` guard) is left alone, since deleting the sole
    statement of an indented block would produce invalid Python. An
    import statement with a comment on any of its lines is also left
    alone: dropping/rewriting it would silently discard the comment. As a
    final safety net, the rewritten source is re-parsed before being
    returned; if that fails, the original source is returned unchanged
    with an empty removed list.
    """
    try:
        src = data.decode("utf-8")
    except UnicodeDecodeError:
        # Not UTF-8: refuse (None = "no change"); never write U+FFFD back.
        return None, []
    try:
        tree = ast.parse(src)
    except Exception:
        return src, []

    used = _collect_used_names(tree)
    try:
        used_before = _collect_used_names(ast.parse(before.decode("utf-8")))
    except Exception:
        used_before = set()

    def dropped(name):
        return name not in used and name in used_before

    comment_lines = _collect_comment_lines(src)
    # Lines split on "\n" only (ast counts lines the same way for "\n" and
    # "\r\n"); each keeps its own ending.
    lines = src.split("\n")
    lines = [l + "\n" for l in lines[:-1]] + ([lines[-1]] if lines[-1] else [])
    starts = [0]
    for l in lines:
        starts.append(starts[-1] + len(l))

    def off(lineno, col):
        """Char offset of (1-indexed line, UTF-8 byte column)."""
        line = lines[lineno - 1] if lineno - 1 < len(lines) else ""
        return starts[lineno - 1] + len(line.encode("utf-8")[:col].decode("utf-8", errors="ignore"))

    deleted = set()  # 1-indexed lines of statements deleted whole
    cuts = []  # (start, end) char ranges inside kept statements
    removed = []

    def plan(n, keep, names):
        """Delete n whole, or cut each run of dropped aliases out of it
        in place: a run followed by a kept alias goes up to that alias
        (its own line in a parenthesised list, else "name, "); a run at
        the end goes from the previous kept alias's end (", name")."""
        if not keep:
            deleted.update(range(n.lineno, n.end_lineno + 1))
            return True
        if any(getattr(a, "end_col_offset", None) is None for a in names):
            return False  # Python < 3.10: no alias positions; leave it
        i = 0
        while i < len(names):
            if names[i] in keep:
                i += 1
                continue
            j = i
            while j + 1 < len(names) and names[j + 1] not in keep:
                j += 1
            if j + 1 < len(names):
                cuts.append((off(names[i].lineno, names[i].col_offset), off(names[j + 1].lineno, names[j + 1].col_offset)))
            else:
                cuts.append((off(names[i - 1].end_lineno, names[i - 1].end_col_offset), off(names[j].end_lineno, names[j].end_col_offset)))
            i = j + 1
        return True

    # Lines holding more than one top-level statement (`import os; import
    # sys`): the line-based edit below would take the neighbour with it,
    # so an import on such a line is left alone, like a commented one.
    stmt_count = {}
    for n in tree.body:
        for l in range(n.lineno, n.end_lineno + 1):
            stmt_count[l] = stmt_count.get(l, 0) + 1
    shared_lines = {l for l, c in stmt_count.items() if c > 1}

    # Only tree.body (module-level statements), never ast.walk(tree): an
    # import nested inside any block is never touched.
    for n in tree.body:
        if not isinstance(n, (ast.Import, ast.ImportFrom)):
            continue
        if any(l in shared_lines or l in comment_lines for l in range(n.lineno, n.end_lineno + 1)):
            continue
        if isinstance(n, ast.ImportFrom):
            if n.module == "__future__" or any(a.name == "*" for a in n.names):
                continue
            keep = [a for a in n.names if not dropped(a.asname or a.name)]
            module = "." * n.level + (n.module or "")
            gone = [f"{module}.{a.name}" if module else a.name for a in n.names if a not in keep]
        else:
            keep = [a for a in n.names if not dropped(_import_bound_name(a))]
            gone = [a.name for a in n.names if a not in keep]
        if len(keep) == len(n.names):
            continue
        if plan(n, keep, n.names):
            removed.extend(gone)

    if not deleted and not cuts:
        return src, []

    # Settle the blank lines around each run of deleted statements: keep
    # the larger of the blank runs before and after it, or none at the
    # start or end of the file (the same rule apply uses for tests).
    def blank(i):
        return lines[i - 1].strip() == ""

    drop = set()
    n_lines = len(lines)
    i = 1
    while i <= n_lines:
        if i not in deleted:
            i += 1
            continue
        lo = i
        while lo > 1 and blank(lo - 1) and (lo - 1) not in deleted:
            lo -= 1
        hi = i
        while hi < n_lines and ((hi + 1) in deleted or blank(hi + 1)):
            hi += 1
        last_del = max(d for d in range(lo, hi + 1) if d in deleted)
        first_del = min(d for d in range(lo, hi + 1) if d in deleted)
        region = set(range(first_del, last_del + 1))
        n_before = first_del - lo
        n_after = hi - last_del
        if lo == 1 or hi == n_lines:
            region.update(range(lo, hi + 1))
        elif n_after > n_before:
            region.update(range(lo, first_del))
        else:
            region.update(range(last_del + 1, hi + 1))
        drop.update(region)
        i = hi + 1

    for l in drop:
        cuts.append((starts[l - 1], starts[l]))
    out_source = src
    for a, b in sorted(cuts, reverse=True):
        out_source = out_source[:a] + out_source[b:]

    # Test-only seam (see task-3-brief fix): forces the post-check below
    # to fail, so the revert-to-original path can be exercised without a
    # real bug. Never set outside tests.
    if os.environ.get("CULL_TIDY_TEST_FORCE_BROKEN"):
        out_source += "def (:\n"

    # Safety net: if the rewrite somehow produced invalid Python, return
    # the original source untouched rather than emit a broken file.
    try:
        ast.parse(out_source)
    except Exception:
        return src, []

    return out_source, removed


if len(sys.argv) > 1 and sys.argv[1] == "--tidy":
    data = sys.stdin.buffer.read()
    before = b""
    if len(sys.argv) > 3 and sys.argv[2] == "--before":
        with open(sys.argv[3], "rb") as f:
            before = f.read()
    new_source, removed = tidy_source(data, before)
    print(json.dumps({"source": new_source, "removed": removed}))
    sys.exit(0)

root = sys.argv[1]
relpaths = sys.argv[2:]
src_root = os.path.join(root, "src") if os.path.isdir(os.path.join(root, "src")) else root

id_count = {}


def line_starts(data):
    """Byte offset of the start of each 1-indexed physical line in data."""
    starts = [0]
    for i, b in enumerate(data):
        if b == 0x0A:  # '\n'
            starts.append(i + 1)
    return starts


def node_lines(node):
    """(start_lineno, end_lineno), 1-indexed, decorators through end_lineno."""
    start = node.lineno
    if getattr(node, "decorator_list", None):
        start = min(d.lineno for d in node.decorator_list)
    return start, node.end_lineno


def node_span(starts, data_len, node):
    """Byte offsets [start,end) of node's whole source lines (decorators
    through end_lineno), from line starts in the UTF-8 encoded source."""
    start_lineno, end_lineno = node_lines(node)
    start = starts[start_lineno - 1]
    end = starts[end_lineno] if end_lineno < len(starts) else data_len
    return start, end


def seg(src, node):
    """Text of node's source lines (decorators through end_lineno): used for
    context/callee snippets, not for the emitted case's own span/body."""
    lines = src.splitlines(keepends=True)
    start, end = node_lines(node)
    return "".join(lines[start - 1 : end])


def top_defs(path):
    try:
        with open(path, encoding="utf-8") as f:
            src = f.read()
        tree = ast.parse(src)
    except Exception:
        return {}, None, ""
    plain = {}  # directly in the module body: last definition wins
    nested = {}  # inside top-level if/try/with: first wins, only fills gaps

    def visit(stmts, out, last):
        for n in stmts:
            put = (lambda k, v: out.__setitem__(k, v)) if last else out.setdefault
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                put(n.name, seg(src, n))
            elif isinstance(n, ast.Assign):
                for t in n.targets:
                    if isinstance(t, ast.Name):
                        put(t.id, seg(src, n))
            elif isinstance(n, ast.AnnAssign) and isinstance(n.target, ast.Name) and n.value is not None:
                put(n.target.id, seg(src, n))
            elif isinstance(n, (ast.If, ast.With, ast.AsyncWith)):
                visit(n.body, out, False)
                visit(n.orelse if isinstance(n, ast.If) else [], out, False)
            elif isinstance(n, (ast.Try, getattr(ast, "TryStar", ast.Try))):
                visit(n.body, out, False)
                for h in n.handlers:
                    visit(h.body, out, False)
                visit(n.orelse, out, False)
                visit(n.finalbody, out, False)

    visit(tree.body, nested, False)
    # plain pass: only direct definitions (blocks recurse into `nested` again, harmlessly)
    direct = [n for n in tree.body if not isinstance(n, (ast.If, ast.With, ast.AsyncWith, ast.Try, getattr(ast, "TryStar", ast.Try)))]
    visit(direct, plain, True)
    nested.update(plain)
    return nested, tree, src


def conftest_fixtures(test_path):
    fixtures = {}
    d = os.path.dirname(test_path)
    while True:
        c = os.path.join(d, "conftest.py")
        if os.path.exists(c):
            defs, tree, src = top_defs(c)
            for n in tree.body if tree else []:
                if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and any(
                    "fixture" in ast.unparse(dec) for dec in n.decorator_list
                ):
                    fixtures.setdefault(n.name, seg(src, n))
        if os.path.abspath(d) == os.path.abspath(root):
            break
        parent = os.path.dirname(d)
        if parent == d:
            break
        d = parent
    return fixtures


def module_file(mod):
    base = os.path.join(src_root, *mod.split("."))
    for p in (base + ".py", os.path.join(base, "__init__.py")):
        if os.path.exists(p):
            return p
    return None


mod_cache = {}


def mod_defs(mod):
    p = module_file(mod)
    if not p:
        return {}
    if p not in mod_cache:
        mod_cache[p] = top_defs(p)[0]
    return mod_cache[p]


def resolve(mod, name):
    p = module_file(mod)
    if not p:
        return None
    if p not in mod_cache:
        mod_cache[p] = top_defs(p)[0]
    src = mod_cache[p].get(name)
    return (os.path.relpath(p, root).replace(os.sep, "/"), src) if src else None


def names_in(node):
    return {n.id for n in ast.walk(node) if isinstance(n, ast.Name)} | {
        n.attr for n in ast.walk(node) if isinstance(n, ast.Attribute)
    }


PYTEST_BUILTIN_PARAMS = {"tmp_path", "monkeypatch", "capsys", "caplog", "request", "self", "cls"}


def _is_plain_value(src):
    """True if src (a definition's source) is an assignment whose value
    contains no call: data, so a method called on it is not project code."""
    try:
        tree = ast.parse(textwrap.dedent(src))
    except Exception:
        return False
    if len(tree.body) != 1:
        return False
    n = tree.body[0]
    if isinstance(n, ast.Assign) or (isinstance(n, ast.AnnAssign) and n.value is not None):
        return not any(isinstance(c, ast.Call) for c in ast.walk(n.value))
    return False


cur_file_names = set()


def pins_setting(fn, n_call_callees, callees, file_defs, imports, attr_mods):
    """The test only reads a project setting/class and compares it to fixed
    values: its code under test is non-empty and all of it merely referenced
    (no call target), no call in its body resolves to project code or to a
    same-file definition, and it takes no fixture parameters beyond pytest's
    built-ins. When unsure: False."""
    local = {n.id for n in ast.walk(fn) if isinstance(n, ast.Name) and isinstance(n.ctx, ast.Store)}
    reads = False
    call_funcs = {id(c.func) for c in ast.walk(fn) if isinstance(c, ast.Call)}
    for r in ast.walk(fn):
        if id(r) in call_funcs:
            continue
        nm = None
        if isinstance(r, ast.Name) and isinstance(r.ctx, ast.Load):
            nm = r.id
        elif isinstance(r, ast.Attribute) and isinstance(r.ctx, ast.Load) and isinstance(r.value, ast.Name):
            nm = r.value.id
        if nm is None or nm in local or nm == fn.name or nm.startswith("test_"):
            continue
        if nm in cur_file_names and nm not in imports and nm not in attr_mods:
            reads = True
        elif nm in imports and resolve(imports[nm], nm):
            reads = True
        elif nm in attr_mods and module_file(attr_mods[nm]):
            reads = True
    if not reads:
        return False
    a = fn.args
    for p in list(getattr(a, "posonlyargs", []) or []) + list(a.args) + list(a.kwonlyargs) + [x for x in (a.vararg, a.kwarg) if x]:
        if p.arg not in PYTEST_BUILTIN_PARAMS:
            return False
    for call in [c for c in ast.walk(fn) if isinstance(c, ast.Call)]:
        node, attrs = call.func, []
        while True:
            if isinstance(node, ast.Attribute):
                attrs.append(node.attr)
                node = node.value
            elif isinstance(node, ast.Subscript):
                attrs.append("[]")
                node = node.value
            else:
                break
        attrs.reverse()
        if not isinstance(node, ast.Name):
            continue  # a call on a call result / literal: the inner call decides
        n = node.id
        src = None
        if n in file_defs:
            src = file_defs[n]
        elif n in imports and module_file(imports[n]):
            hit = resolve(imports[n], n)
            src = hit[1] if hit else ""
        elif n in attr_mods and module_file(attr_mods[n]):
            if len(attrs) < 2 or attrs[0] == "[]":
                return False
            hit = resolve(attr_mods[n], attrs[0])
            src = hit[1] if hit else ""
            attrs = attrs[1:]
        else:
            continue  # builtin, standard library, third party or a local name
        if not attrs or not _is_plain_value(src):
            return False
    return True


def assign_id(base):
    id_count[base] = id_count.get(base, 0) + 1
    n = id_count[base]
    return base if n == 1 else f"{base} #{n}"


def emit(rel, qual, name, parent_id, fn, data, starts, src, file_defs, fixtures, imports, attr_mods):
    start, end = node_span(starts, len(data), fn)
    body = data[start:end].decode("utf-8")
    used = names_in(fn)
    args = {a.arg for a in fn.args.args}
    ctx, callees, size, trunc = [], [], 0, False

    def add(text, soft=False):
        # soft: a referenced name that doesn't fit is left out without
        # counting as truncation.
        nonlocal size, trunc
        if size + len(text) > MAX_CTX:
            if not soft:
                trunc = True
            return False
        size += len(text)
        return True

    for a in sorted(args):
        if a in fixtures and add(fixtures[a]):
            ctx.append(fixtures[a])
    for n in sorted(used):
        if n in file_defs and n != fn.name and not n.startswith("test_") and add(file_defs[n]):
            ctx.append(file_defs[n])
    for call in [c for c in ast.walk(fn) if isinstance(c, ast.Call)]:
        f = call.func
        hit = None
        if isinstance(f, ast.Name) and f.id in imports:
            hit = (f.id, resolve(imports[f.id], f.id))
        elif isinstance(f, ast.Attribute) and isinstance(f.value, ast.Name) and f.value.id in attr_mods:
            hit = (f"{f.value.id}.{f.attr}", resolve(attr_mods[f.value.id], f.attr))
        if hit and hit[1] and hit[0] not in {c["symbol"] for c in callees} and add(hit[1][1]):
            callees.append({"symbol": hit[0], "file": hit[1][0], "source": hit[1][1]})
    n_call_callees = len(callees)
    # Referenced names (constants, classes, functions read but not called),
    # after the call targets, in first-reference order.
    call_funcs = {id(c.func) for c in ast.walk(fn) if isinstance(c, ast.Call)}
    refs = []
    for r in ast.walk(fn):
        if isinstance(r, ast.Name) and isinstance(r.ctx, ast.Load):
            refs.append(r)
        elif (
            isinstance(r, ast.Attribute)
            and isinstance(r.ctx, ast.Load)
            and isinstance(r.value, ast.Name)
        ):
            refs.append(r)
    refs.sort(key=lambda r: (r.lineno, r.col_offset))
    for r in refs:
        if id(r) in call_funcs:
            continue
        hit = None
        if isinstance(r, ast.Name):
            if r.id in imports:
                hit = (r.id, resolve(imports[r.id], r.id))
        elif r.value.id in attr_mods:
            hit = (f"{r.value.id}.{r.attr}", resolve(attr_mods[r.value.id], r.attr))
        if hit and hit[1] and hit[0] not in {c["symbol"] for c in callees} and add(hit[1][1], soft=True):
            callees.append({"symbol": hit[0], "file": hit[1][0], "source": hit[1][1]})

    obj = {
        "id": assign_id(f"py:{rel}:{qual}"),
        "lang": "python",
        "framework": "pytest",
        "file": rel,
        "name": name,
        "body": body,
        "context": "\n\n".join(ctx),
        "callees": callees,
        "truncated": trunc,
        "span": {"start": start, "end": end},
    }
    if parent_id:
        obj["parent"] = parent_id
    if pins_setting(fn, n_call_callees, callees, file_defs, imports, attr_mods):
        obj["pins_setting"] = True
    print(json.dumps(obj))


for rel in relpaths:
    path = os.path.join(root, rel)
    try:
        with open(path, "rb") as f:
            data = f.read()
        src = data.decode("utf-8")
        tree = ast.parse(src)
    except Exception as e:
        print(json.dumps({"skip": rel, "reason": f"parse error: {e}"}))
        continue

    starts = line_starts(data)
    file_defs = {}
    for n in tree.body:
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            file_defs[n.name] = seg(src, n)
        elif isinstance(n, ast.Assign):
            for t in n.targets:
                if isinstance(t, ast.Name):
                    file_defs[t.id] = seg(src, n)

    # Module-level names for the setting-only check only (not code context).
    file_names = set(file_defs)
    for n in tree.body:
        if isinstance(n, ast.AnnAssign) and isinstance(n.target, ast.Name) and n.value is not None:
            file_names.add(n.target.id)
    cur_file_names.clear()
    cur_file_names.update(file_names)

    fixtures = conftest_fixtures(path)
    for n in tree.body:
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and any(
            "fixture" in ast.unparse(d) for d in n.decorator_list
        ):
            fixtures[n.name] = seg(src, n)

    imports, attr_mods = {}, {}
    for n in ast.walk(tree):
        if isinstance(n, ast.ImportFrom) and n.module and n.level == 0:
            for a in n.names:
                bound = a.asname or a.name
                sub = f"{n.module}.{a.name}"
                # `from pkg import module`: a submodule file (and not a name
                # the package's __init__ defines) acts like `import pkg.module`.
                if a.name != "*" and module_file(sub) and a.name not in mod_defs(n.module):
                    attr_mods[bound] = sub
                else:
                    imports[bound] = n.module
        elif isinstance(n, ast.Import):
            for a in n.names:
                attr_mods[a.asname or a.name.split(".")[0]] = a.name if a.asname else a.name.split(".")[0]

    for n in tree.body:
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name.startswith("test_"):
            emit(rel, n.name, n.name, "", n, data, starts, src, file_defs, fixtures, imports, attr_mods)
        elif isinstance(n, ast.ClassDef) and n.name.startswith("Test"):
            parent_id = f"py:{rel}:{n.name}"
            for m in n.body:
                if isinstance(m, (ast.FunctionDef, ast.AsyncFunctionDef)) and m.name.startswith("test_"):
                    emit(
                        rel,
                        f"{n.name}::{m.name}",
                        m.name,
                        parent_id,
                        m,
                        data,
                        starts,
                        src,
                        file_defs,
                        fixtures,
                        imports,
                        attr_mods,
                    )
