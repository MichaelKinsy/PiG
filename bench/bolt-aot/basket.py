#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Perf basket for PiG's TypeScript extension runtimes: Node, Bun and the compiled aot host, on the bench/bolt-aot sets, one command to a JSONL and a markdown report.

Sets: `five` (the five-extension set of bench.py, with the word-count hot loop) and `ten` (the ten trivial bench-ext-NN extensions of ext10_bench.py). `five+probe` and `ten+probe`
are the same sets plus exts/basket-probe.ts (used only by the heap pass, so the timing sets stay exactly the published ones).
Arms (--arm NAME:KIND[:pig=PATH][:slot=PATH][:path=DIR]; KIND is node, bun or aot): each arm is a pig binary plus a runtime. The default arms are
  node, node2 (A/A repeat of node), bun, aot, aot2 (A/A repeat of aot), plus node24 (the container's qualified Node, as the 2026-10 results used) when present.
Passes per set and round (arms interleaved round-robin, order rotated each round, after one warm-up run per arm):
  timing  pig --mode rpc with the set and a scripted model; launch to ready; one first turn and N-1 further turns, each timed; /proc CPU (utime/stime, 10 ms ticks) and
          memory (smaps_rollup kinds, minor/major faults) of the Go host and of the extension-host process tree at ready, after 0.5 s settled, after the first turn and
          after N turns; /words hot loop on `five`. Nothing is traced in this pass.
  strace  (--alloc-rounds R) the same run under `strace -f --seccomp-bpf -e trace=memory,process`: counts of mmap/munmap/brk/mprotect/madvise/mremap and anonymous-mmap bytes,
          split per process (Go host vs extension host) and per phase. Slow, so it is never mixed into a timing number.
  heap    (--alloc-rounds R) the same run on `set+probe`, with JSC's GC log (BUN_JSC_logGC, Bun and aot) on stderr and /basket-probe at ready, after the first turn and after N turns, each
          after a forced GC: cumulative JS allocation (Node: v8 total_allocated_bytes; Bun and aot: the GC log), live heap, JSC object counts.
An arm that runs another tier than it names is an error, not a number: each run records the extension host's /proc exe path, and aot must be <slot>/aot/images/*/pig-ts-host.
Arm names ending in 2 are A/A repeats of the arm without the 2 and get an A/A column. An arm that cannot run (e.g. no image) is reported n/a with its error.
Set `rewrite` is the three extensions the Piglet's import.meta rewrite compiles in (rpiv-todo, pi-mcp-adapter, the locating fixture of rewrite-exts/; npm ci --ignore-scripts there first).
A `cold` pass per set (--cold-rounds) removes the digest cache's records beside the Bun and aot executables before each start (ready only), so a first start is its own cell; --pair BEFORE=AFTER
adds a before/after delta table. Arms may use different pig binaries and slots (pig=, slot=), so one session interleaves BEFORE and AFTER arms.
one command, run inside the CI container (node, go and strace on PATH; strace may be bind-mounted from the host):
  basket.py run --out DIR --pig PIG --slot SLOT --bun STOCK_BUN --build [--piglet-dir DIR] [--arm ...] [--label K=V ...]     (--build installs Bun, the compiler and the images first)
  basket.py report DIR/runs.jsonl                                                                                       (regenerates DIR/report.md)
  basket.py build --pig PIG --slot SLOT --bun STOCK_BUN [--piglet-dir DIR] --sets five,ten,five+probe,ten+probe        (images only)"""
import argparse, glob, json, os, re, shutil, signal, socket, statistics, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import bench, equivalence  # noqa: E402

HZ = os.sysconf("SC_CLK_TCK")
ROLLUP = ("Rss", "Pss", "Pss_Dirty", "Pss_Anon", "Pss_File", "Pss_Shmem", "Shared_Clean", "Shared_Dirty", "Private_Clean", "Private_Dirty",
          "Referenced", "Anonymous", "AnonHugePages", "Swap")
SUM_KEYS = ("utime", "stime", "cutime", "cstime", "minflt", "majflt", "threads") + ROLLUP
EXE = {"node": "node", "bun": "bun", "aot": "pig-ts-host"}
PROBE = os.path.join(HERE, "exts", "basket-probe.ts")
BUN_PATHS = {"24": "/opt/node/24.22.1/bin"}


# ---------------------------------------------------------------- sets and arms
def set_exts(name):
    base, _, probe = name.partition("+")
    if base == "five":
        exts = bench.exts()
    elif base == "ten":
        exts = sorted(glob.glob(os.path.join(os.path.abspath(equivalence.DEFAULT_EXTS), "bench-ext-*.ts")))
        assert len(exts) == 10, "the ten bench extensions are missing"
    elif base == "rewrite":
        # the three extensions the Piglet's import.meta rewrite compiles in (equivalence_pkgs.py): rpiv-todo, pi-mcp-adapter and the locating fixture (npm ci --ignore-scripts in rewrite-exts first)
        rw = os.path.join(HERE, "rewrite-exts")
        exts = [f"{rw}/node_modules/@juicesharp/rpiv-todo", f"{rw}/node_modules/pi-mcp-adapter", f"{rw}/locating/index.ts"]
    else:
        raise SystemExit(f"unknown set {name}")
    return exts + ([PROBE] if probe == "probe" else [])


def parse_arm(spec):
    parts = spec.split(":")
    if len(parts) < 2 or parts[1] not in EXE:
        raise SystemExit(f"arm spec {spec!r}: NAME:KIND[:pig=PATH][:slot=PATH][:path=DIR] with KIND node, bun or aot")
    arm = {"name": parts[0], "kind": parts[1]}
    for kv in parts[2:]:
        k, _, v = kv.partition("=")
        if k not in ("pig", "slot", "path"):
            raise SystemExit(f"arm spec {spec!r}: unknown key {k}")
        arm[k] = os.path.abspath(v) if k != "path" else v
    return arm


def default_arms(args):
    specs = ["node:node", "node2:node", "bun:bun", "aot:aot", "aot2:aot"]
    if os.path.isdir("/opt/node/24.22.1/bin"):
        specs.insert(2, "node24:node:path=/opt/node/24.22.1/bin")
    return specs


def arm_env(arm, slot):
    env = {"PIG_TS_RUNTIME": arm["kind"]}
    if arm["kind"] != "node":
        env["PIG_TS_RUNTIMES_DIR"] = arm.get("slot", slot)
    return env


# ---------------------------------------------------------------- /proc
def read_proc(pid, full):
    try:
        st = open(f"/proc/{pid}/stat").read()
        f = st[st.rindex(")") + 2:].split()
        r = {"pid": pid, "ppid": int(f[1]), "minflt": int(f[7]), "majflt": int(f[9]), "utime": int(f[11]), "stime": int(f[12]), "threads": int(f[17]),
             "cutime": int(f[13]), "cstime": int(f[14])}
        r["comm"] = open(f"/proc/{pid}/comm").read().strip()
        r["exe"] = os.readlink(f"/proc/{pid}/exe")
        if full:
            for line in open(f"/proc/{pid}/smaps_rollup"):
                k, _, v = line.partition(":")
                if k in ROLLUP:
                    r[k] = int(v.split()[0])
    except Exception:
        return None
    return r


def snapshot(pig_pid, full=True):
    procs = [p for p in (read_proc(q, full) for q in bench.proc_tree(pig_pid)) if p]
    pig = next((p for p in procs if p["pid"] == pig_pid), None)
    ext = [p for p in procs if p["pid"] != pig_pid]
    total = {k: sum(p.get(k, 0) for p in ext) for k in SUM_KEYS if full or k in ("utime", "stime", "cutime", "cstime", "minflt", "majflt", "threads")}
    return {"t": time.time(), "pig": pig, "ext": total, "ext_procs": [{k: p[k] for k in ("pid", "comm", "exe", "threads")} for p in ext]}


# ---------------------------------------------------------------- scripted model
class Model:
    """OpenAI-compatible scripted model: "call NN" -> bench_tool_NN when declared; any other prompt -> word_count on the small fixture when declared; tool result -> text."""
    def __init__(self, fixture):
        outer = self
        self.fixture = fixture

        class H(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            def log_message(self, *a): pass
            def setup(self):
                super().setup(); self.connection.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            def do_GET(self):
                b = json.dumps({"object": "list", "data": [{"id": "fake-model", "object": "model"}]}).encode()
                self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                last = body["messages"][-1]
                tools = [t.get("function", {}).get("name") for t in body.get("tools") or []]
                text = last["content"] if isinstance(last.get("content"), str) else "".join(p.get("text", "") for p in last.get("content") or [])
                self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.send_header("Transfer-Encoding", "chunked"); self.end_headers()
                def w(b): self.wfile.write(f"{len(b):x}\r\n".encode() + b + b"\r\n"); self.wfile.flush()
                def chunk(delta, finish=None):
                    return ("data: " + json.dumps({"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "fake-model", "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}) + "\n\n").encode()
                call = None
                if last["role"] == "user":
                    if text.startswith("call ") and len(text.split()) > 2 and text.split()[1] in tools:
                        # "call <tool> <json arguments>": that tool call, as equivalence_pkgs.py scripts it
                        call = (text.split()[1], json.loads(text.split(None, 2)[2]))
                    elif text.startswith("call ") and f"bench_tool_{text.split()[1]}" in tools:
                        call = (f"bench_tool_{text.split()[1]}", {"value": "v" + text.split()[1]})
                    elif "word_count" in tools and not text.startswith("/"):
                        call = ("word_count", {"path": outer.fixture})
                if call:
                    w(chunk({"role": "assistant", "tool_calls": [{"index": 0, "id": f"call_{int(time.time() * 1e6)}", "type": "function", "function": {"name": call[0], "arguments": json.dumps(call[1])}}]})); w(chunk({}, "tool_calls"))
                else:
                    w(chunk({"role": "assistant", "content": "Done."})); w(chunk({}, "stop"))
                w(b"data: [DONE]\n\n"); self.wfile.write(b"0\r\n\r\n"); self.wfile.flush()

        class Quiet(ThreadingHTTPServer):
            def handle_error(self, request, client_address):  # pig closes its connections when it exits: not an error of the benchmark
                pass

        self.server = Quiet(("127.0.0.1", 0), H)
        self.port = self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()


# ---------------------------------------------------------------- one run
class Rpc(bench.Rpc):
    """bench.Rpc with stderr in a file (a pipe nobody reads blocks a chatty pig) and an optional wrapper (strace) in front of argv."""
    def __init__(self, argv, env, cwd, stderr_path):
        self.stderr_path = stderr_path
        self.errf = open(stderr_path, "wb")
        self.p = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.errf, env=env, cwd=cwd, start_new_session=True)
        self.n = 0
    def until(self, pred, timeout=180):
        end = time.time() + timeout
        while time.time() < end:
            line = self.p.stdout.readline()
            if not line:
                raise RuntimeError("pig exited: " + open(self.stderr_path, errors="replace").read()[-1500:])
            try:
                ev = json.loads(line)
            except Exception:
                continue
            if pred(ev):
                return ev
        raise TimeoutError("rpc timeout")
    def close(self):
        super().close()
        self.errf.close()


REWRITE_CALLS = ['call todo {"action":"list"}', "call locating_read {}", 'call locating_read {"name":"legacy.txt"}', "call mcp {}"]


def turn_prompt(set_name, i):
    if set_name.startswith("rewrite"):
        return REWRITE_CALLS[i % len(REWRITE_CALLS)]
    return f"call {i % 10 + 1:02d}" if set_name.startswith("ten") else f"prompt {i}"


def do_turn(rpc, set_name, i):
    t = time.time()
    rpc.send({"type": "prompt", "message": turn_prompt(set_name, i)})
    rpc.until(lambda e: e.get("type") == "agent_end")
    return (time.time() - t) * 1000


def do_probe(rpc, arg=""):
    rpc.send({"type": "prompt", "message": ("/basket-probe " + arg).strip()})
    ev = rpc.until(lambda e: e.get("type") == "extension_ui_request" and str(e.get("message", "")).startswith("PROBE:"))
    return json.loads(ev["message"][len("PROBE:"):])


def expect_tier(arm, slot, snap):
    paths = sorted({p["exe"] for p in snap["ext_procs"]})
    names = sorted({os.path.basename(p) for p in paths})
    if EXE[arm["kind"]] not in names:
        raise RuntimeError(f"arm {arm['name']} ran {names}, not {EXE[arm['kind']]}")
    if arm["kind"] == "aot":
        root = os.path.join(arm.get("slot", slot), "aot", "images")
        if not any(p.startswith(root + os.sep) for p in paths):
            raise RuntimeError(f"arm {arm['name']}: pig-ts-host {paths} is not an image under {root}")
    if arm["kind"] == "bun" and not any(p == os.path.join(arm.get("slot", slot), "bun", "bun") for p in paths):
        raise RuntimeError(f"arm {arm['name']}: bun {paths} is not the slot's bun")
    return paths


def drop_digest_records(slot, kind):
    """Remove the digest cache's records beside the slot executables of kind (bun: <slot>/bun/.verified-bun; aot: every image's .verified-pig-ts-host), so the next start hashes: the cold start."""
    pat = os.path.join(slot, "bun", ".verified-*") if kind == "bun" else os.path.join(slot, "aot", "images", "*", ".verified-*") if kind == "aot" else None
    gone = glob.glob(pat) if pat else []
    for f in gone:
        os.remove(f)
    return len(gone)


def run_once(ctx, arm, set_name, mode="timing"):
    """mode: timing, strace, heap or cold (a start with the digest records removed: ready only; see the module docstring). Returns the run record."""
    slot = ctx["slot"]
    home = f"{ctx['base']}/{arm['name']}"
    agent = f"{home}/agent"
    os.makedirs(f"{home}/tmp", exist_ok=True)
    pig = arm.get("pig", ctx["pig"])
    env = {"PATH": (arm["path"] + ":" if arm.get("path") else "") + os.environ["PATH"], "HOME": home, "PIG_HOME": f"{home}/.pig", "PIG_CODING_AGENT_DIR": agent, "PI_CODING_AGENT_DIR": agent,
           "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PIG_TELEMETRY": "0", "PIG_SKIP_VERSION_CHECK": "1", "TERM": "dumb", "TMPDIR": f"{home}/tmp", **arm_env(arm, slot)}
    if mode == "heap" and arm["kind"] != "node":
        env["BUN_JSC_logGC"] = "basic"  # Node's cumulative allocation is read in the probe (v8.getHeapStatistics().total_allocated_bytes); there is no log to switch on
    argv = [pig, "--mode", "rpc", "--no-session", "--offline", "-ne", "--provider", "fake", "--model", "fake-model"]
    exts = set_exts(set_name + ("+probe" if mode == "heap" and "+" not in set_name else ""))
    for e in exts:
        argv += ["-e", e]
    trace = f"{home}/strace.txt"
    if mode == "strace":
        argv = ["strace", "-f", "-q", "--seccomp-bpf", "-ttt", "-e", "trace=memory,process", "-o", trace] + argv
    out = {"type": "run", "pass": mode, "set": set_name, "arm": arm["name"], "kind": arm["kind"], "loadavg": open("/proc/loadavg").read().split()[:3]}
    turns = ctx["turns"]
    if mode == "cold":
        out["records_dropped"] = drop_digest_records(arm.get("slot", slot), arm["kind"])
    t0 = time.time()
    rpc = Rpc(argv, env, ctx["work"], f"{home}/stderr.log")
    try:
        rid = rpc.send({"type": "get_commands"})
        ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
        t_ready = time.time()
        root = rpc.p.pid
        pig_pid = root
        if mode == "strace":
            kids = [p for p in bench.proc_tree(root) if p != root]
            pig_pid = kids[0]
        ready = snapshot(pig_pid, False)
        out["ready_ms"] = (t_ready - t0) * 1000
        names = [c.get("name") for c in (ev.get("data") or {}).get("commands", [])]
        want = "basket-probe" if mode == "heap" else {"five": "words", "ten": "bench-10", "rewrite": "locating"}[set_name.split("+")[0]]
        if want not in names:
            raise RuntimeError(f"command /{want} missing: the extension set did not load")
        out["commands"] = len(names)
        time.sleep(0.5)
        s0 = snapshot(pig_pid)
        out["ext_exe"] = expect_tier(arm, slot, s0)
        out["ext_n"] = len(s0["ext_procs"])
        out["ext_exe_bytes"] = os.path.getsize(out["ext_exe"][0])
        if arm["kind"] != "node":  # the digest cache's record beside the executable (written by a pig that has it, once the file is 2 s old)
            out["digest_record"] = os.path.exists(os.path.join(os.path.dirname(out["ext_exe"][0]), ".verified-" + os.path.basename(out["ext_exe"][0])))
        if mode == "cold":
            out["ready_snap"] = ready
            return out
        if mode == "heap":
            out["probe_ready"] = do_probe(rpc, "gc ready")
        t_t1 = time.time()
        walls = [do_turn(rpc, set_name, 0)]
        t_t1_end = time.time()
        time.sleep(0.2)
        s1 = snapshot(pig_pid)
        if mode == "heap":
            out["probe_t1"] = do_probe(rpc, "gc turn1")
        t_rest = time.time()
        for i in range(1, turns):
            walls.append(do_turn(rpc, set_name, i))
        t_rest_end = time.time()
        time.sleep(0.2)
        s2 = snapshot(pig_pid)
        out.update(turn_wall_ms=walls, ready_snap=ready, s0=s0, s1=s1, s2=s2, t={"spawn": t0, "ready": t_ready, "turn1": t_t1, "turn1_end": t_t1_end, "rest": t_rest, "rest_end": t_rest_end})
        if mode == "heap":
            out["probe_tN"] = do_probe(rpc, "gc turnN")
        if mode == "timing" and set_name == "five":
            tw = time.time()
            rpc.send({"type": "prompt", "message": f"/words {ctx['big']}"})
            ev = rpc.until(lambda e: e.get("type") == "extension_ui_request" and "words (" in str(e.get("message", "")))
            out["words_wall_ms"] = (time.time() - tw) * 1000
            out["words_ext_ms"] = float(ev["message"].rsplit("(", 1)[1].split(" ms")[0])
        if mode == "timing":
            out["loadavg_end"] = open("/proc/loadavg").read().split()[:3]
    finally:
        rpc.close()
    if mode == "strace":
        out["strace"] = parse_strace(trace, pig_pid, out["t"])
        if not os.environ.get("BASKET_KEEP_TRACE"):
            os.remove(trace)
    if mode == "heap":
        out["gc_log"] = parse_gc_log(f"{home}/stderr.log", arm["kind"])
    return out


# ---------------------------------------------------------------- strace and GC logs
MEM_CALLS = ("mmap", "munmap", "brk", "mprotect", "madvise", "mremap")
LINE = re.compile(r"^(\d+)\s+(\d+\.\d+)\s+(.*)$")


def parse_strace(path, pig_pid, t):
    """Counts of memory syscalls per process class (pig, ext) and phase. A thread belongs to the process that CLONE_THREADed it; a clone without CLONE_THREAD starts a process."""
    parent, pending, exe, events = {}, {}, {}, []
    for raw in open(path, errors="replace"):
        m = LINE.match(raw)
        if not m:
            continue
        tid, ts, rest = int(m.group(1)), float(m.group(2)), m.group(3)
        if rest.startswith("<..."):
            r = re.match(r"<\.\.\. (\w+) resumed>.*= (-?\d+)", rest)
            if r and r.group(1) in ("clone", "clone3", "fork", "vfork") and int(r.group(2)) > 0 and tid in pending:
                parent[int(r.group(2))] = (tid, "CLONE_THREAD" in pending.pop(tid))
            continue
        name = rest.split("(", 1)[0]
        if name in ("clone", "clone3", "fork", "vfork"):
            r = re.search(r"= (\d+)$", rest)
            if r:
                parent[int(r.group(1))] = (tid, "CLONE_THREAD" in rest)
            elif "<unfinished" in rest:
                pending[tid] = rest
        elif name == "execve":
            # a vfork parent holds the child's execve line as "<unfinished ...>": the path is on the first line either way
            exe.setdefault(tid, []).append(rest.split('"')[1])
        elif name in MEM_CALLS:
            length = None
            if name in ("mmap", "munmap", "mremap", "mprotect", "madvise"):
                a = rest.split("(", 1)[1].split(",")
                m2 = re.match(r"\s*(\d+)", a[1]) if len(a) > 1 else None
                length = int(m2.group(1)) if m2 else None
            events.append((tid, ts, name, length, "MAP_ANONYMOUS" in rest if name == "mmap" else False))

    def proc_of(tid):
        seen = 0
        while tid in parent and parent[tid][1] and seen < 100000:
            tid = parent[tid][0]
            seen += 1
        return tid

    def cls(tid):
        p = proc_of(tid)
        return "pig" if p == pig_pid else "ext"

    wins = [("startup", t["spawn"], t["ready"]), ("settle", t["ready"], t["turn1"]), ("turn1", t["turn1"], t["turn1_end"]), ("rest", t["turn1_end"], t["rest_end"])]
    res = {c: {w: {n: 0 for n in MEM_CALLS} | {"mmap_anon_bytes": 0, "munmap_bytes": 0} for w, _, _ in wins} for c in ("pig", "ext")}
    exes = {}  # executable of each process (its last execve) -> how many processes ran it
    for tid, ts, name, length, anon in events:
        for w, a, b in wins:
            if a <= ts < b:
                d = res[cls(tid)][w]
                d[name] += 1
                if name == "mmap" and anon and length:
                    d["mmap_anon_bytes"] += length
                if name == "munmap" and length:
                    d["munmap_bytes"] += length
    for tid, paths in exe.items():
        if proc_of(tid) == tid:
            exes[os.path.basename(paths[-1])] = exes.get(os.path.basename(paths[-1]), 0) + 1
    return {"by_class": res, "exes": exes, "processes": len({proc_of(c) for c in parent}), "events": len(events)}


JSC_START = re.compile(r"\[GC<(0x[0-9a-f]+)>: START \w (\d+)kb => (\w+)")
JSC_END = re.compile(r"\[GC<(0x[0-9a-f]+)>: .*=> (\d+)kb, p=[\d.]+ms .*cycle ([\d.]+)ms END\]")
MARK = re.compile(r"BASKET-MARK (\S*)")


def parse_gc_log(path, kind):
    """The extension host's GC log, cut into phases by the probe's BASKET-MARK lines. Only Bun and the compiled host have one (JSC's logGC: START <before>kb => type ... => <after>kb END);
    Node's V8 writes --trace-gc to stdout, which is the wire, so Node's cumulative allocation comes from v8.getHeapStatistics().total_allocated_bytes in the probe instead.
    Allocated bytes of a collection = heap before it - heap after the previous one of the same JSC heap (the first one counts everything allocated since start): kb resolution."""
    if kind == "node":
        return None
    seg, after, marks, pending = {}, {}, [], False
    cur, gcs, pause, kinds = 0, 0, 0.0, {}
    for line in open(path, errors="replace"):
        m = MARK.search(line)
        if m:
            marks.append(m.group(1)); cur += 1; pending = True
            continue
        m = JSC_START.search(line)
        if m:
            heap, before = m.group(1), int(m.group(2))
            alloc = max(before - after.get(heap, 0), 0)
            idx = cur - 1 if pending else cur
            pending = False
            seg[idx] = seg.get(idx, 0) + alloc
            gcs += 1
            kinds[m.group(3)] = kinds.get(m.group(3), 0) + 1
            continue
        m = JSC_END.search(line)
        if m:
            after[m.group(1)] = int(m.group(2)); pause += float(m.group(3))
    # segment i is what was allocated before mark i (startup, turn 1, turns 2..N for the marks ready, turn1, turnN)
    return {"format": "jsc logGC", "marks": marks, "alloc_kb_before_mark": [seg.get(i, 0) for i in range(len(marks))], "gcs": gcs, "cycle_ms": pause, "kinds": kinds}


# ---------------------------------------------------------------- environment facts for the header
def sh(cmd, **kw):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=120, **kw).stdout.strip()
    except Exception as e:
        return f"unavailable: {e}"


def sha256_file(path):
    import hashlib
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def lock_digest(version="1.4.2", variant="bun-linux-x64"):
    """The SHA-256 PiG's runtime lock pins for stock Bun (coding/extension/host/tsruntime/lock.go)."""
    text = open(os.path.join(HERE, "..", "..", "coding", "extension", "host", "tsruntime", "lock.go")).read()
    m = re.search(r'Version: "%s", Platform: "linux-x64", Variant: "%s", SHA256: "([0-9a-f]{64})"' % (re.escape(version), re.escape(variant)), text)
    return m.group(1) if m else None


def install_bun(slot, bun):
    """Put stock Bun 1.4.2 into the slot as /bun-runtime install writes it, after checking its SHA-256 against PiG's lock."""
    digest = sha256_file(bun)
    want = lock_digest()
    if digest != want:
        raise SystemExit(f"{bun}: SHA-256 {digest} is not the lock's {want}")
    os.makedirs(os.path.join(slot, "bun"), exist_ok=True)
    shutil.copyfile(bun, os.path.join(slot, "bun", "bun"))
    os.chmod(os.path.join(slot, "bun", "bun"), 0o755)
    with open(os.path.join(slot, "bun", "runtime.json"), "w") as f:
        json.dump({"id": "bun", "kind": "bun", "version": "1.4.2", "platform": "linux-x64", "provider": "basket", "image": {"executable": "bun", "sha256": digest}}, f)


def aot_images(slot):
    """Every built image under <slot>/aot/images: path, size, SHA-256 and the time one hash of it takes (the Go host hashes it once per start)."""
    out = []
    for exe in sorted(glob.glob(os.path.join(slot, "aot", "images", "*", "pig-ts-host"))):
        t = time.time()
        digest = sha256_file(exe)
        out.append({"path": exe, "bytes": os.path.getsize(exe), "sha256": digest, "hash_ms_python": (time.time() - t) * 1000})
    return out


def aot_failures(slot):
    out = []
    for f in sorted(glob.glob(os.path.join(slot, "aot", "failures", "*.json"))):
        d = json.load(open(f))
        out.append({"file": os.path.basename(f), "reason": d.get("reason"), "detail": str(d.get("detail", ""))[:500], "when": d.get("when"), "members": [os.path.basename(i["path"]) for i in d.get("inputs", [])][:12]})
    return out


def runtime_status(pig, kind, slot, path=None):
    env = dict(os.environ, PIG_TS_RUNTIME=kind, PIG_OFFLINE="1", PIG_TELEMETRY="0", PIG_SKIP_VERSION_CHECK="1")
    if kind != "node":
        env["PIG_TS_RUNTIMES_DIR"] = slot
    if path:
        env["PATH"] = path + ":" + env["PATH"]
    home = tempfile.mkdtemp(prefix="status-", dir=os.environ.get("TMPDIR", "/tmp"))
    env.update(HOME=home, PIG_HOME=f"{home}/.pig", PIG_CODING_AGENT_DIR=f"{home}/agent")
    try:
        return sh([pig, "extensions", "runtime", "status", "--json"], env=env)
    finally:
        shutil.rmtree(home, ignore_errors=True)


def header(args, arms, slot):
    repo = os.path.abspath(os.path.join(HERE, "..", ".."))
    h = {"type": "header", "date_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "argv": sys.argv[1:], "host": sh(["hostname"]), "kernel": sh(["uname", "-r"]),
         "cpu": next((l.split(":", 1)[1].strip() for l in open("/proc/cpuinfo") if l.startswith("model name")), "?"), "cpus_visible": os.cpu_count(),
         "cgroup_cpu_max": sh(["cat", "/sys/fs/cgroup/cpu.max"]), "cgroup_memory_max": sh(["cat", "/sys/fs/cgroup/memory.max"]),
         "load_before": open("/proc/loadavg").read().split()[:3], "clk_tck": HZ,
         "repo_commit": sh(["git", "-C", repo, "rev-parse", "HEAD"]), "repo_dirty": bool(sh(["git", "-C", repo, "status", "--porcelain", "--untracked-files=no"])),
         "labels": dict(kv.split("=", 1) for kv in args.label), "strace": sh(["strace", "-V"]).splitlines()[:1], "python": sys.version.split()[0]}
    h["arms"] = []
    for arm in arms:
        pig = arm.get("pig", args.pig)
        entry = {"name": arm["name"], "kind": arm["kind"], "pig": pig, "pig_sha256": sha256_file(pig), "pig_version": sh([pig, "--version"]), "slot": arm.get("slot", slot) if arm["kind"] != "node" else None}
        penv = dict(os.environ, PATH=(arm["path"] + ":" if arm.get("path") else "") + os.environ["PATH"])
        if arm["kind"] == "node":
            entry["node"] = sh(["node", "--version"], env=penv)
            entry["node_exe"] = shutil.which("node", path=penv["PATH"])
        if arm["kind"] == "bun":
            b = os.path.join(entry["slot"], "bun", "bun")
            entry["bun"] = sh([b, "--version"]); entry["bun_sha256"] = sha256_file(b); entry["bun_lock_sha256"] = lock_digest(); entry["bun_bytes"] = os.path.getsize(b)
        if arm["kind"] == "aot":
            s = entry["slot"]
            entry["aot_manifest"] = json.load(open(os.path.join(s, "aot", "runtime.json"))) if os.path.exists(os.path.join(s, "aot", "runtime.json")) else None
            entry["aot_images"] = aot_images(s)
            entry["aot_failures"] = aot_failures(s)
            entry["toolchain_dirs"] = sorted(os.listdir(os.path.join(s, "aot", "toolchain"))) if os.path.isdir(os.path.join(s, "aot", "toolchain")) else []
        entry["runtime_status"] = runtime_status(pig, arm["kind"], entry["slot"], arm.get("path"))
        h["arms"].append(entry)
    return h


# ---------------------------------------------------------------- commands
def make_ctx(args, base):
    work = f"{base}/work"
    os.makedirs(work, exist_ok=True)
    small, big = f"{work}/small.txt", f"{base}/big.txt"
    open(small, "w").write("one two three\nfour five\n")
    import random
    rnd, words, n = random.Random(7), ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta\n", "iota\t"], 0
    with open(big, "w") as f:  # the 16.8 MB file of bench.py
        while n < 16_800_000:
            s = " ".join(rnd.choice(words) for _ in range(1000)) + "\n"
            f.write(s); n += len(s)
    model = Model(small)
    return {"slot": os.path.abspath(args.slot), "pig": os.path.abspath(args.pig), "base": base, "work": work, "turns": args.turns, "big": big, "model": model}


def prepare_homes(ctx, arms):
    for arm in arms:
        ad = f"{ctx['base']}/{arm['name']}/agent"
        os.makedirs(ad, exist_ok=True)
        json.dump({"providers": {"fake": {"baseUrl": f"http://127.0.0.1:{ctx['model'].port}/v1", "api": "openai-completions", "apiKey": "fake", "models": [{"id": "fake-model", "name": "fake", "contextWindow": 200000, "maxTokens": 8192, "input": ["text"], "reasoning": False, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}, open(f"{ad}/models.json", "w"))
        json.dump({"lastChangelogVersion": "9999.0.0"}, open(f"{ad}/settings.json", "w"))
        open(f"{ad}/auth.json", "w").write("{}")


def cmd_build(args):
    slot = os.path.abspath(args.slot)
    if args.bun:
        install_bun(slot, args.bun)
    if not os.path.exists(os.path.join(slot, "bun", "runtime.json")):
        raise SystemExit(f"{slot}/bun/runtime.json is missing: pass --bun PATH (stock Bun 1.4.2)")
    bench.CFG["exts"] = os.path.abspath(args.exts)
    for name in args.sets.split(","):
        argv = [sys.executable, os.path.join(HERE, "build_set.py"), "--pig", os.path.abspath(args.pig), "--slot", slot]
        if args.piglet_dir:
            argv += ["--piglet-dir", os.path.abspath(args.piglet_dir)]
        argv += set_exts(name)
        print("build", name, flush=True)
        r = subprocess.run(argv)
        print(f"build {name}: exit {r.returncode}", flush=True)


def cmd_run(args):
    bench.CFG["exts"] = os.path.abspath(args.exts)
    arms = [parse_arm(s) for s in (args.arm or default_arms(args))]
    slot = os.path.abspath(args.slot)
    os.makedirs(args.out, exist_ok=True)
    runs_path = os.path.join(args.out, "runs.jsonl")
    if args.build:
        cmd_build(argparse.Namespace(slot=args.slot, bun=args.bun, exts=args.exts, sets=",".join(args.sets.split(",") + [s + "+probe" for s in args.sets.split(",")]), pig=args.pig, piglet_dir=args.piglet_dir))
    elif args.bun:  # no image build (no aot arm): only install stock Bun, SHA-256 checked, into the slot and into the slot of every bun arm that names its own
        for s in {slot} | {os.path.abspath(a["slot"]) for a in arms if a["kind"] == "bun" and "slot" in a}:
            install_bun(s, args.bun)
    hdr = header(args, arms, slot)
    hdr["pairs"] = [p.split("=", 1) for p in args.pair]
    with open(runs_path, "a") as f:
        f.write(json.dumps(hdr) + "\n")
    base = tempfile.mkdtemp(prefix="basket-", dir=os.environ.get("TMPDIR", "/tmp"))
    ctx = make_ctx(args, base)
    prepare_homes(ctx, arms)
    alloc_names = args.alloc_arms.split(",") if args.alloc_arms else [a["name"] for a in arms if not a["name"].endswith("2")]
    dead = {}

    def emit(rec):
        with open(runs_path, "a") as f:
            f.write(json.dumps(rec) + "\n")

    def one(arm, set_name, mode, rnd):
        key = (arm["name"], set_name, mode)
        if key in dead:
            return
        try:
            res = run_once(ctx, arm, set_name, mode)
        except Exception as e:
            res = {"type": "run", "pass": mode, "set": set_name, "arm": arm["name"], "kind": arm["kind"], "error": str(e)[:1500]}
        res["round"] = rnd
        emit(res)
        print(json.dumps({k: res.get(k) for k in ("pass", "set", "arm", "round", "ready_ms", "error")})[:300], flush=True)
        return res

    try:
        for set_name in args.sets.split(","):
            for arm in arms:  # warm-up: caches, first-start effects; recorded as round -1; an arm that cannot run is dropped for this set with its reason on record
                res = one(arm, set_name, "timing", -1)
                if res and "error" in res:
                    dead[(arm["name"], set_name, "timing")] = res["error"]
                    for m in ("strace", "heap", "cold"):
                        dead[(arm["name"], set_name, m)] = res["error"]
            cold = [a for a in arms if a["kind"] != "node"]
            for r in range(args.cold_rounds):  # cold starts: the digest records are removed before each, so the Go host hashes the executable (and records it again)
                k = r % max(len(cold), 1)
                for arm in cold[k:] + cold[:k]:
                    one(arm, set_name, "cold", r)
            for r in range(args.rounds):
                k = r % len(arms)
                for arm in arms[k:] + arms[:k]:
                    one(arm, set_name, "timing", r)
            sel = [a for a in arms if a["name"] in alloc_names]
            for mode in ("strace", "heap"):
                if args.alloc_rounds:
                    for arm in sel:
                        one(arm, set_name, mode, -1)  # warm-up
                for r in range(args.alloc_rounds):
                    k = r % max(len(sel), 1)
                    for arm in sel[k:] + sel[:k]:
                        one(arm, set_name, mode, r)
    finally:
        ctx["model"].server.shutdown()
        shutil.rmtree(base, ignore_errors=True)
        emit({"type": "footer", "load_after": open("/proc/loadavg").read().split()[:3], "date_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())})
    md = report(runs_path)
    open(os.path.join(args.out, "report.md"), "w").write(md)
    print(md)


# ---------------------------------------------------------------- report
def pct(v, p):
    s = sorted(v)
    if len(s) == 1:
        return s[0]
    x = (len(s) - 1) * p
    lo = int(x)
    hi = min(lo + 1, len(s) - 1)
    return s[lo] + (s[hi] - s[lo]) * (x - lo)


def cell(vals, nd=0):
    vals = [v for v in vals if v is not None]
    if not vals:
        return "n/a"
    f = lambda x: f"{x:.{nd}f}"
    if len(vals) >= 7:
        return f"{f(pct(vals, .5))} [{f(pct(vals, .1))}-{f(pct(vals, .9))}]"
    return f"{f(pct(vals, .5))} [{f(min(vals))}-{f(max(vals))}]"


def ms(ticks):
    return ticks * 1000 / HZ


def cpu(p):
    return None if p is None else ms(p["utime"] + p["stime"])


def d(a, b, k):
    return b[k] - a[k]


def mb(kb):
    return kb / 1024


def sharedkb(m):
    return m["Shared_Clean"] + m["Shared_Dirty"]


def timing_rows(N):
    """(label, extractor(run) -> number or None, decimals). Sections are separated by ('#', title) entries."""
    R = []
    A = R.append
    A(("#", "Startup (launch to ready: get_commands answered with the set's commands)"))
    A(("Launch to ready (ms)", lambda r: r["ready_ms"], 0))
    A(("Go host CPU to ready, user+sys (ms)", lambda r: cpu(r["ready_snap"]["pig"]), 0))
    A(("  Go host user (ms)", lambda r: ms(r["ready_snap"]["pig"]["utime"]), 0))
    A(("  Go host sys (ms)", lambda r: ms(r["ready_snap"]["pig"]["stime"]), 0))
    A(("Go host: CPU of reaped children by ready, the `--version` start checks (ms)", lambda r: ms(r["ready_snap"]["pig"]["cutime"] + r["ready_snap"]["pig"]["cstime"]), 0))
    A(("Extension host CPU to ready, user+sys (ms)", lambda r: ms(r["ready_snap"]["ext"]["utime"] + r["ready_snap"]["ext"]["stime"]), 0))
    A(("  extension host user (ms)", lambda r: ms(r["ready_snap"]["ext"]["utime"]), 0))
    A(("  extension host sys (ms)", lambda r: ms(r["ready_snap"]["ext"]["stime"]), 0))
    A(("Digest record present beside the executable after ready (1 = yes; Node: n/a)", lambda r: None if "digest_record" not in r else int(r["digest_record"]), 0))
    A(("Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms)", lambda r: ms(r["ready_snap"]["ext"]["cutime"] + r["ready_snap"]["ext"]["cstime"]), 0))
    A(("Extension host CPU, ready to 0.5 s later (idle after ready) (ms)", lambda r: ms(r["s0"]["ext"]["utime"] + r["s0"]["ext"]["stime"] - r["ready_snap"]["ext"]["utime"] - r["ready_snap"]["ext"]["stime"]), 0))
    A(("#", f"Turns (a prompt, one model tool call, the tool, a final answer; {N} per run)"))
    A(("Turn 1 wall (ms)", lambda r: r["turn_wall_ms"][0], 0))
    A(("Turn 2 wall (ms)", lambda r: r["turn_wall_ms"][1], 0))
    A((f"Steady wall, median of turns 6-{N} (ms)", lambda r: pct(r["turn_wall_ms"][5:], .5), 1))
    A(("Turn 1 / steady wall (ratio)", lambda r: r["turn_wall_ms"][0] / pct(r["turn_wall_ms"][5:], .5), 2))
    A(("#", "CPU per phase (user+sys, 10 ms ticks; the first turn is measured with 0.5 s settled before and 0.2 s after)"))
    A(("Go host CPU, turn 1 (ms)", lambda r: cpu(r["s1"]["pig"]) - cpu(r["s0"]["pig"]), 0))
    A(("Extension host CPU, turn 1 (ms)", lambda r: ms(r["s1"]["ext"]["utime"] + r["s1"]["ext"]["stime"] - r["s0"]["ext"]["utime"] - r["s0"]["ext"]["stime"]), 0))
    A((f"Go host CPU per turn, turns 2-{N} (ms)", lambda r: (cpu(r["s2"]["pig"]) - cpu(r["s1"]["pig"])) / (N - 1), 1))
    A((f"Extension host CPU per turn, turns 2-{N} (ms)", lambda r: ms(r["s2"]["ext"]["utime"] + r["s2"]["ext"]["stime"] - r["s1"]["ext"]["utime"] - r["s1"]["ext"]["stime"]) / (N - 1), 1))
    A((f"  extension host sys per turn, turns 2-{N} (ms)", lambda r: ms(r["s2"]["ext"]["stime"] - r["s1"]["ext"]["stime"]) / (N - 1), 1))
    A(("#", "Page faults (cumulative counters of the process; extension host = its process tree)"))
    A(("Extension host minor faults to 0.5 s after ready", lambda r: r["s0"]["ext"]["minflt"], 0))
    A(("Extension host major faults to 0.5 s after ready", lambda r: r["s0"]["ext"]["majflt"], 0))
    A(("Extension host minor faults, turn 1", lambda r: d(r["s0"]["ext"], r["s1"]["ext"], "minflt"), 0))
    A((f"Extension host minor faults per turn, turns 2-{N}", lambda r: d(r["s1"]["ext"], r["s2"]["ext"], "minflt") / (N - 1), 1))
    A(("Extension host major faults, whole run to the last turn", lambda r: r["s2"]["ext"]["majflt"], 0))
    A(("Go host minor faults to 0.5 s after ready", lambda r: r["s0"]["pig"]["minflt"], 0))
    A((f"Go host minor faults per turn, turns 2-{N}", lambda r: d(r["s1"]["pig"], r["s2"]["pig"], "minflt") / (N - 1), 1))
    for key, label in (("s0", "0.5 s after ready"), ("s1", "after turn 1"), ("s2", f"after {N} turns")):
        A(("#", f"Memory {label} (MB; smaps_rollup summed over the extension host's process tree)"))
        A((f"Extension host RSS", lambda r, k=key: mb(r[k]["ext"]["Rss"]), 1))
        A((f"Extension host PSS", lambda r, k=key: mb(r[k]["ext"]["Pss"]), 1))
        A((f"  Pss_Anon", lambda r, k=key: mb(r[k]["ext"]["Pss_Anon"]), 1))
        A((f"  Pss_File", lambda r, k=key: mb(r[k]["ext"]["Pss_File"]), 1))
        A((f"  Pss_Shmem", lambda r, k=key: mb(r[k]["ext"]["Pss_Shmem"]), 1))
        A((f"  Private_Dirty", lambda r, k=key: mb(r[k]["ext"]["Private_Dirty"]), 1))
        A((f"  Private_Clean", lambda r, k=key: mb(r[k]["ext"]["Private_Clean"]), 1))
        A((f"  Shared_Clean+Shared_Dirty", lambda r, k=key: mb(sharedkb(r[k]["ext"])), 1))
        A((f"Go host RSS", lambda r, k=key: mb(r[k]["pig"]["Rss"]), 1))
        A((f"Go host PSS", lambda r, k=key: mb(r[k]["pig"]["Pss"]), 1))
    A(("#", "Process"))
    A(("Extension host threads after the last turn", lambda r: r["s2"]["ext"]["threads"], 0))
    A(("Extension host executable size (MB)", lambda r: r["ext_exe_bytes"] / 1e6, 1))
    return R


def alloc_rows(N):
    R = []
    A = R.append
    for cls, title in (("ext", "Extension host (and its start-check children)"), ("pig", "Go host")):
        for win, label, div in (("startup", "launch to ready", 1), ("turn1", "turn 1", 1), ("rest", f"per turn, turns 2-{N}", N - 1)):
            A(("#", f"strace: memory syscalls, {title}, {label}"))
            for call in ("mmap", "munmap", "brk", "mprotect", "madvise"):
                A((call, lambda r, c=cls, w=win, k=call, dv=div: r["strace"]["by_class"][c][w][k] / dv, 0 if div == 1 else 1))
            A(("anonymous mmap requested (MiB of address space: reservations count in full, touched or not)", lambda r, c=cls, w=win, dv=div: r["strace"]["by_class"][c][w]["mmap_anon_bytes"] / dv / 2**20, 1))
    return R


def probe_alloc(r, phase):
    """Allocated MB in a phase from a heap-pass run: Node from v8 total_allocated_bytes, Bun and aot from the JSC GC log (kb resolution); None where the runtime exposes neither."""
    if r["kind"] == "node":
        t = lambda k: r[k]["raw"]["v8"].get("total_allocated_bytes")
        a, b, c = t("probe_ready"), t("probe_t1"), t("probe_tN")
        if None in (a, b, c):
            return None
        return {"startup": a, "turn1": b - a, "rest": c - b}[phase] / 2**20
    g = r.get("gc_log") or {}
    kb = g.get("alloc_kb_before_mark")
    if not kb or len(kb) < 3:
        return None
    return {"startup": kb[0], "turn1": kb[1], "rest": kb[2]}[phase] / 1024


def heap_rows(N):
    R = []
    A = R.append
    A(("#", "JS heap allocation (probe pass; Node: v8 total_allocated_bytes, Bun and aot: JSC GC log; a forced GC at each probe point)"))
    A(("Allocated, launch to ready (MB)", lambda r: probe_alloc(r, "startup"), 1))
    A(("Allocated, turn 1 incl. the probe (MB)", lambda r: probe_alloc(r, "turn1"), 1))
    A((f"Allocated per turn, turns 2-{N} (MB)", lambda r: (lambda x: None if x is None else x / (N - 1))(probe_alloc(r, "rest")), 2))
    A(("GC cycles over the run (JSC log; Node: PerformanceObserver gc entries)", lambda r: (r["gc_log"] or {}).get("gcs") if r["kind"] != "node" else r["probe_tN"]["raw"]["gcEvents"]["count"], 0))
    for key, label in (("probe_ready", "at ready"), ("probe_t1", "after turn 1"), ("probe_tN", f"after {N} turns")):
        A(("#", f"JS heap live after a forced GC, {label}"))
        A(("process.memoryUsage().heapUsed (MB)", lambda r, k=key: r[k]["live"]["memoryUsage"]["heapUsed"] / 2**20, 1))
        A(("process.memoryUsage().external + arrayBuffers (MB)", lambda r, k=key: (r[k]["live"]["memoryUsage"]["external"] + r[k]["live"]["memoryUsage"]["arrayBuffers"]) / 2**20, 1))
        A(("JSC live objects (Node: n/a, V8 does not expose a count)", lambda r, k=key: r[k]["jsc"]["heapStats"]["objectCount"] if r["kind"] != "node" and "heapStats" in (r[k].get("jsc") or {}) else None, 0))
        A(("JSC heapSize (MB; Node: n/a)", lambda r, k=key: r[k]["jsc"]["heapStats"]["heapSize"] / 2**20 if r["kind"] != "node" and "heapStats" in (r[k].get("jsc") or {}) else None, 1))
    return R


DELTA_ROWS = ("Launch to ready", "Go host CPU to ready", "Extension host CPU to ready", "Extension host CPU of reaped", "Digest record present", "Turn 1 wall", "Turn 2 wall", "Steady wall", "Turn 1 / steady", "Go host CPU, turn 1", "Extension host CPU, turn 1",
              "Go host CPU per turn", "Extension host CPU per turn", "Extension host RSS", "Extension host PSS", "  Pss_Anon", "  Pss_File", "  Private_Dirty", "  Private_Clean", "Go host RSS", "Extension host minor faults", "Extension host executable size")


def delta_table(runs, pairs, rows):
    """Median before, median after and the change in percent, per pair of arms, for the headline rows (memory rows are tagged with the moment they were read)."""
    ok = lambda a: [r for r in runs if r["arm"] == a and "error" not in r]
    head = ["Metric"]
    for b, a in pairs:
        head += [b, a, "delta %"]
    lines = ["| " + " | ".join(head) + " |", "|---|" + "---:|" * (len(head) - 1)]
    tag = ""
    for row in rows:
        if row[0] == "#":
            tag = " @" + row[1].split("Memory ")[1].split(" (")[0] if row[1].startswith("Memory") else ""
            continue
        label, fn, nd = row
        if not label.startswith(DELTA_ROWS):
            continue
        cells = []
        for b, a in pairs:
            med = {}
            for arm in (b, a):
                vals = []
                for r in ok(arm):
                    try:
                        v = fn(r)
                    except Exception:
                        v = None
                    if v is not None:
                        vals.append(v)
                med[arm] = pct(vals, .5) if vals else None
            if med[b] is None or med[a] is None:
                cells += [f"{med[b]:.{nd}f}" if med[b] is not None else "n/a", f"{med[a]:.{nd}f}" if med[a] is not None else "n/a", "-"]
            else:
                cells += [f"{med[b]:.{nd}f}", f"{med[a]:.{nd}f}", f"{(med[a] - med[b]) / med[b] * 100:+.0f}" if med[b] else "-"]
        lines.append(f"| {label.strip()}{tag} | " + " | ".join(cells) + " |")
    return "\n".join(lines)


def table(runs, arm_names, rows, notes):
    ok = {a: [r for r in runs if r["arm"] == a and "error" not in r] for a in arm_names}
    errs = {a: [r for r in runs if r["arm"] == a and "error" in r] for a in arm_names}
    aa = [(a, a + "2") for a in arm_names if a + "2" in arm_names]
    head = ["Metric"] + arm_names + [f"A/A {a}-{b} (%)" for a, b in aa if a in arm_names and b in arm_names]
    lines = ["| " + " | ".join(head) + " |", "|---|" + "---:|" * (len(head) - 1)]
    lines.append("| Runs ok / total | " + " | ".join(f"{len(ok[a])}/{len(ok[a]) + len(errs[a])}" for a in arm_names) + " |" + " |" * (len(head) - 1 - len(arm_names)))
    for row in rows:
        if row[0] == "#":
            lines.append("| **" + row[1] + "** |" + " |" * (len(head) - 1))
            continue
        label, fn, nd = row
        cells, med = [], {}
        for a in arm_names:
            vals = []
            for r in ok[a]:
                try:
                    v = fn(r)
                except Exception:
                    v = None
                if v is not None:
                    vals.append(v)
            cells.append(cell(vals, nd))
            if vals:
                med[a] = pct(vals, .5)
        aacells = []
        for a, b in aa:
            if a in arm_names and b in arm_names:
                if a in med and b in med and (med[a] + med[b]) != 0:
                    aacells.append(f"{abs(med[a] - med[b]) / ((med[a] + med[b]) / 2) * 100:.0f}")
                else:
                    aacells.append("-")
        lines.append(f"| {label} | " + " | ".join(cells + aacells) + " |")
    for a in arm_names:
        if errs[a]:
            lines.append("")
            lines.append(f"`{a}`: {len(errs[a])} failed run(s); first error: `{errs[a][0]['error'][:300].strip()}`".replace("\n", " "))
    return "\n".join(lines)


def report(path):
    recs = [json.loads(l) for l in open(path)]
    hdr = next(r for r in recs if r["type"] == "header")
    foot = next((r for r in recs if r["type"] == "footer"), None)
    runs = [r for r in recs if r["type"] == "run" and r.get("round", -1) >= 0 or (r["type"] == "run" and "error" in r)]
    allruns = [r for r in recs if r["type"] == "run"]
    runs = [r for r in allruns if r.get("round", -1) >= 0 or "error" in r]
    arm_names = [a["name"] for a in hdr["arms"]]
    out = ["# Perf basket report", "", f"Generated by `bench/bolt-aot/basket.py report` from `{os.path.basename(path)}`. **PROVISIONAL: one run on a loaded shared host; do not publish a number from it.** Read the A/A columns first.", ""]
    out += ["## Header", "", f"- date (UTC): {hdr['date_utc']}; host `{hdr['host']}`, kernel {hdr['kernel']}", f"- CPU: {hdr['cpu']}; {hdr['cpus_visible']} CPUs visible; cgroup cpu.max `{hdr['cgroup_cpu_max']}`, memory.max `{hdr['cgroup_memory_max']}`",
            f"- bench commit {hdr['repo_commit']} (dirty tracked files: {hdr['repo_dirty']}); labels: " + ", ".join(f"{k}={v}" for k, v in hdr["labels"].items())]
    loads = [float(r["loadavg"][0]) for r in allruns if "loadavg" in r] + [float(r["loadavg_end"][0]) for r in allruns if "loadavg_end" in r]
    out.append(f"- host load average (1 min): before {hdr['load_before'][0]}, during runs median {statistics.median(loads):.0f} (min {min(loads):.0f}, max {max(loads):.0f})" + (f", after {foot['load_after'][0]}" if foot else "") if loads else "- host load: no runs")
    out.append(f"- strace: {', '.join(hdr['strace'])}; python {hdr['python']}; clock tick {hdr['clk_tck']}/s (CPU resolution 10 ms)")
    out += ["", "| arm | kind | pig (sha256, version) | runtime |", "|---|---|---|---|"]
    for a in hdr["arms"]:
        rt = a.get("node") and f"Node {a['node']} ({a['node_exe']})" or a.get("bun") and f"Bun {a['bun']} (sha256 {a['bun_sha256'][:12]}, lock {'match' if a['bun_sha256'] == a['bun_lock_sha256'] else 'MISMATCH'})" or (
            f"Pi-Bolt manifest `{(a.get('aot_manifest') or {}).get('version')}`, toolchain {a.get('toolchain_dirs')}, {len(a.get('aot_images') or [])} image(s)")
        out.append(f"| {a['name']} | {a['kind']} | {a['pig_sha256'][:12]}, {a['pig_version'][:60]} | {rt} |")
    seen = set()
    for a in hdr["arms"]:
        if a["kind"] == "aot" and (a["slot"], a["pig"]) not in seen:
            seen.add((a["slot"], a["pig"]))
            out.append("")
            out.append(f"aot slot of `{a['name']}` (`{a['slot']}`): images " + "; ".join(f"`{os.path.relpath(i['path'], a['slot'])}` {i['bytes'] / 1e6:.1f} MB sha256 {i['sha256'][:12]} (one Python SHA-256 of it: {i['hash_ms_python']:.0f} ms)" for i in a.get("aot_images", [])) if a.get("aot_images") else f"aot slot of `{a['name']}` (`{a['slot']}`): NO image built")
            for f in a.get("aot_failures", []):
                out.append(f"  - build failure `{f['file'][:12]}` ({f['when']}): {f['reason']}: {' '.join(f['detail'].split())} (members {f['members']})")
    seen = set()
    for a in hdr["arms"]:
        key = (a["kind"], a["slot"], a["pig"], a.get("node"))
        if key in seen:
            continue
        seen.add(key)
        try:
            st = json.loads(a["runtime_status"])
            brief = {"selection": st.get("selection"), "installed": [{"id": i["id"], "version": i["manifest"].get("version"), "provider": i["manifest"].get("provider"), "sets": len(i["manifest"].get("sets") or {})} for i in st.get("installed") or []]}
            text = json.dumps(brief, indent=1)
        except Exception:
            text = a["runtime_status"][:800]
        out += ["", f"`pig extensions runtime status --json` for `{a['name']}` (abridged to selection and installed manifests):", "", "```json", text, "```"]
    sets = []
    for r in allruns:
        if r["set"] not in sets:
            sets.append(r["set"])
    for s in sets:
        N = max((len(r["turn_wall_ms"]) for r in runs if r["set"] == s and "turn_wall_ms" in r), default=20)
        out += ["", f"## Set `{s}`", ""]
        rr = [r for r in runs if r["set"] == s and r["pass"] == "timing"]
        rounds = len({r["round"] for r in rr if r["round"] >= 0})
        out += [f"Timing pass: {rounds} interleaved rounds (order rotated each round, one recorded warm-up run per arm before them), cells are median [p10-p90] over rounds (fewer than 7 runs: median [min-max]), A/A columns are |median(a)-median(b)| / their mean.", "",
                "Proof each arm ran its tier: every run's extension-host `/proc/<pid>/exe` is checked (node: a node binary; bun: the slot's bun; aot: `<slot>/aot/images/<key>/pig-ts-host`) and a run that differs is an error, not a number. Executables seen: " + "; ".join(f"{a}: " + ", ".join(sorted({e for r in rr if r['arm'] == a and 'ext_exe' in r for e in r['ext_exe']})) for a in arm_names), ""]
        out.append(table(rr, arm_names, timing_rows(N), []))
        cr = [r for r in runs if r["set"] == s and r["pass"] == "cold"]
        if cr:
            carms = [a for a in arm_names if any(r["arm"] == a for r in cr)]
            out += ["", f"**Cold starts** ({len({r['round'] for r in cr if r['round'] >= 0})} rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.", "",
                    table(cr, carms, [("Launch to ready, cold (ms)", lambda r: r["ready_ms"], 0), ("Go host CPU to ready, cold, user+sys (ms)", lambda r: cpu(r["ready_snap"]["pig"]), 0),
                                      ("Digest records removed before the start", lambda r: r["records_dropped"], 0), ("Digest record present after the start (1 = yes)", lambda r: int(r["digest_record"]), 0)], [])]
        pairs = hdr.get("pairs") or []
        if pairs:
            out += ["", "**Before / after** (medians of the timing pass; delta = after against before)", "", delta_table(rr, pairs, timing_rows(N))]
        words = [r for r in rr if "words_ext_ms" in r]
        if words:
            out += ["", "**/words hot loop (the word-count extension, 16.8 MB file, once per run after the turns)**", "", table(words, arm_names, [("/words in the extension (ms)", lambda r: r["words_ext_ms"], 0), ("/words wall from prompt (ms)", lambda r: r["words_wall_ms"], 0)], [])]
        sr = [r for r in runs if r["set"] == s and r["pass"] == "strace"]
        if sr:
            out += ["", f"Allocation, strace pass ({len({r['round'] for r in sr if r['round'] >= 0})} rounds; the same run under `strace -f --seccomp-bpf -e trace=memory,process`; counts per process class, `ext` = every process the Go host started: the `--version` start checks and the extension host). Cells median [min-max].", "",
                    "Processes seen by strace (executable: count of processes, first run per arm): " + "; ".join(f"{a}: {next((r['strace']['exes'] for r in sr if r['arm'] == a and 'strace' in r), {})}" for a in sorted({r['arm'] for r in sr})), "",
                    table(sr, sorted({r["arm"] for r in sr}, key=arm_names.index), alloc_rows(N), [])]
        hr = [r for r in runs if r["set"] == s and r["pass"] == "heap"]
        if hr:
            out += ["", f"Allocation, heap pass ({len({r['round'] for r in hr if r['round'] >= 0})} rounds; the set plus `basket-probe.ts`, which reads the runtime's own heap numbers). Cells median [min-max].", "",
                    table(hr, sorted({r["arm"] for r in hr}, key=arm_names.index), heap_rows(N), [])]
            for a in sorted({r["arm"] for r in hr}, key=arm_names.index):
                first = next((r for r in hr if r["arm"] == a and r["kind"] == "node" and "probe_ready" in r), None)
                if first and "total_allocated_bytes" not in first["probe_ready"]["raw"]["v8"]:
                    out.append(f"\n`{a}`: allocation n/a: {first['probe_ready']['version']}'s v8.getHeapStatistics() has no total_allocated_bytes field, and no GC log is read for Node (in a trial a `--trace-gc` shim put no GC lines on the host's stderr; not investigated further).")
    return "\n".join(out) + "\n"


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("run", "build"):
        p = sub.add_parser(name)
        p.add_argument("--pig", required=True)
        p.add_argument("--slot", required=True, help="runtime slot: <slot>/bun/{bun,runtime.json} and the aot manifest and images")
        p.add_argument("--exts", default=os.path.join(HERE, "exts"), help="the five-extension set's directory (npm ci --ignore-scripts there first)")
        p.add_argument("--bun", help="stock Bun 1.4.2 (SHA-256 checked against coding/extension/host/tsruntime/lock.go) to install into the slot")
        p.add_argument("--piglet-dir", help="the Piglet checkout whose worker builds the images (default: piglets/pig-bolt of this checkout); its pin decides the Pi-Bolt version")
        p.add_argument("--sets", default="five,ten" if name == "run" else "five,ten,five+probe,ten+probe")
    r = sub.choices["run"]
    r.add_argument("--out", required=True)
    r.add_argument("--rounds", type=int, default=12)
    r.add_argument("--turns", type=int, default=20)
    r.add_argument("--alloc-rounds", type=int, default=3)
    r.add_argument("--alloc-arms", help="arms of the strace and heap passes (default: every arm not named *2)")
    r.add_argument("--arm", action="append", help="NAME:KIND[:pig=PATH][:slot=PATH][:path=DIR]; repeatable; default node,node2,node24,bun,aot,aot2")
    r.add_argument("--label", action="append", default=[], help="K=V recorded in the header (e.g. pin=pi-bolt-1.1.2, tree=...)")
    r.add_argument("--cold-rounds", type=int, default=5, help="cold starts per bun and aot arm: the digest records beside the slot executables are removed before each start")
    r.add_argument("--pair", action="append", default=[], help="BEFORE_ARM=AFTER_ARM: a column triple (before, after, delta %%) in the report's delta table; repeatable")
    r.add_argument("--build", action="store_true", help="build the aot images (all sets and their +probe variants) before measuring")
    rep = sub.add_parser("report")
    rep.add_argument("runs")
    args = ap.parse_args()
    if args.cmd == "report":
        print(report(args.runs))
    elif args.cmd == "build":
        cmd_build(args)
    else:
        cmd_run(args)


if __name__ == "__main__":
    main()
