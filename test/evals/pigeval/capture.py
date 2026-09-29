# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Capture the exact request bodies harnesses send to the mock model, and diff two of them."""
import difflib
import json
import os
import shutil
import tempfile

from . import proc, registry
from .mockllm import MockServer, system_text
from .overhead import prepare

PROMPT_FIELDS = {"messages", "input", "system", "instructions", "tools"}


def tool_name(tool):
    return tool.get("function", tool).get("name") or tool.get("name") or "?"


def diff(a_id, a, b_id, b, protocol):
    """Return lines describing how request a differs from request b (b is the reference)."""
    out = []
    sa, sb = system_text(protocol, a), system_text(protocol, b)
    if sa != sb:
        out.append(f"system prompt: {b_id} {len(sb.encode())} bytes, {a_id} {len(sa.encode())} bytes")
        out += difflib.unified_diff(sb.splitlines(), sa.splitlines(), b_id, a_id, lineterm="", n=1)
    ta = {tool_name(t): t for t in a.get("tools") or []}
    tb = {tool_name(t): t for t in b.get("tools") or []}
    for name in list(tb) + [n for n in ta if n not in tb]:
        if name not in ta or name not in tb:
            out.append(f"tool {name}: only in {b_id if name in tb else a_id}")
            continue
        ja = json.dumps(ta[name], indent=1, sort_keys=True).splitlines()
        jb = json.dumps(tb[name], indent=1, sort_keys=True).splitlines()
        if ja != jb:
            out.append(f"tool {name}: differs")
            out += difflib.unified_diff(jb, ja, b_id, a_id, lineterm="", n=0)
    if set(ta) == set(tb) and list(ta) != list(tb):
        out.append(f"tool order: {b_id} {list(tb)}, {a_id} {list(ta)}")
    for key in sorted((set(a) | set(b)) - PROMPT_FIELDS):
        if a.get(key) != b.get(key):
            out.append(f"field {key}: {b_id}={json.dumps(b.get(key))} {a_id}={json.dumps(a.get(key))}")
    ma = [m for m in a.get("messages", []) if m.get("role") not in ("system", "developer")]
    mb = [m for m in b.get("messages", []) if m.get("role") not in ("system", "developer")]
    if ma != mb:
        out.append("messages after the system prompt differ")
        out += difflib.unified_diff(json.dumps(mb, indent=1).splitlines(), json.dumps(ma, indent=1).splitlines(), b_id, a_id, lineterm="", n=1)
    return out


def run(args):
    harnesses = registry.select(registry.load(args.registry), args.harnesses)
    os.makedirs(args.out, exist_ok=True)
    captured = {}
    work = tempfile.mkdtemp(prefix="pigeval-requests-")
    with MockServer() as server:
        for h in harnesses:
            binary = registry.resolve(h, args.pig)
            if binary is None or not h.mock:
                print(f"requests: {h.id:9} skipped ({'not installed' if binary is None else 'no custom endpoint'})")
                continue
            env, cwd, values = prepare(h, work, server.base_url)
            server.recorder.take()
            result = proc.run([binary, *registry.substitute(dict(values, prompt=args.prompt), h.print_args)], env, cwd, args.timeout)
            got = [r for r in server.recorder.take() if r["protocol"] != "other"]
            for i, r in enumerate(got, 1):
                with open(os.path.join(args.out, f"{h.id}-{i}.json"), "w") as f:
                    json.dump(r["body"], f, indent=2, sort_keys=True)
            if got:
                captured[h.id] = got
            first = got[0] if got else {}
            print(f"requests: {h.id:9} exit {result['code']:<3} requests {len(got):<2} first {first.get('bytes', 0):>6} B  system {first.get('system_bytes', 0):>6} B  tools {first.get('tools', 0)}")
            if not got:
                print("  " + (result["stderr"].strip()[-600:] or result["stdout"].strip()[-300:]).replace("\n", "\n  "))
    shutil.rmtree(work, ignore_errors=True)
    print(f"requests: bodies written to {args.out}")
    if not args.diff:
        return 0
    ids = [h.id for h in harnesses if h.id in captured]
    if len(ids) < 2:
        print("requests: --diff needs two harnesses that sent requests")
        return 1
    a, b = ids[0], ids[1]
    ra, rb = captured[a][0], captured[b][0]
    if ra["protocol"] != rb["protocol"]:
        print(f"requests: {a} and {b} speak different protocols; compare the saved bodies instead")
        return 1
    lines = diff(a, ra["body"], b, rb["body"], ra["protocol"])
    print(f"\nrequests: first request of {a} (+) against {b} (-): {ra['bytes'] - rb['bytes']:+d} bytes")
    print("\n".join(lines) if lines else "identical")
    return 0
