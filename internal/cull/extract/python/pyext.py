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
import json
import os
import sys

MAX_CTX = 24000

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
    out = {}
    for n in tree.body:
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            out[n.name] = seg(src, n)
        elif isinstance(n, ast.Assign):
            for t in n.targets:
                if isinstance(t, ast.Name):
                    out[t.id] = seg(src, n)
    return out, tree, src


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

    def add(text):
        nonlocal size, trunc
        if size + len(text) > MAX_CTX:
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
                imports[a.asname or a.name] = n.module
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
