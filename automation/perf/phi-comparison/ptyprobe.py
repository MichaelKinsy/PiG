#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""PTY probe for the pig / phi comparison. See README.md for the method.

  ptyprobe.py first-frame --subject pig --mode cold --runs 30 --out first-frame.jsonl
  ptyprobe.py memory --subject pig --sessions 1 --out memory.jsonl
  ptyprobe.py summarize first-frame.jsonl memory.jsonl

Subjects are built binaries named by environment variables: PIG_BIN, PHI_BIN, FLOOR_BIN, and for scratch builds
PIG_SCRATCH_BIN / PHI_SCRATCH_BIN. Everything runs with a private HOME and an empty working directory; no model is ever
reached (pig: test-faux provider with --offline; phi: a config whose only model points at 127.0.0.1:9).
"""
import argparse
import gc
import json
import os
import re
import select
import shutil
import signal
import statistics
import struct
import subprocess
import sys
import tempfile
import time
import fcntl
import termios

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import vtscreen  # noqa: E402

ROWS, COLS = 40, 120
WORK = os.environ.get("PROBE_WORK", "/tmp/phi-cmp-work")
DA1_REPLY = b"\x1b[?62;22c"
OSC11_REPLY = b"\x1b]11;rgb:1c1c/1c1c/1c1c\x07"
PHI_CONFIG = "models:\n  - name: dummy\n    api: OpenAI\n    api_key: x\n    base_url: http://127.0.0.1:9/v1\n    default: true\n"
MODEL_MARK = {"pig": "faux-1", "phi": "dummy"}


def stub_dir():
    """Directory with no-op fd and rg on PATH: phi downloads both from github.com at every TUI start when they are missing, and pig
    announces the missing tools; with stubs neither does either, so no subject touches the network."""
    d = os.path.join(WORK, "stubbin")
    os.makedirs(d, exist_ok=True)
    for name in ("fd", "rg"):
        link = os.path.join(d, name)
        if not os.path.lexists(link):
            os.symlink("/usr/bin/true", link)
    return d


class Subject:
    def __init__(self, name, family, binary, extra_env=None, args=None, stubs=True):
        self.name, self.family, self.binary, self.stubs = name, family, binary, stubs
        self.extra_env = extra_env or {}
        self.args = args or []

    def prepare_home(self, home):
        os.makedirs(home, exist_ok=True)
        if self.family == "phi":
            os.makedirs(os.path.join(home, ".phi"), exist_ok=True)
            with open(os.path.join(home, ".phi", "config.yaml"), "w") as f:
                f.write(PHI_CONFIG)

    def argv(self):
        if self.family == "pig":
            return [self.binary, "--offline", "--model", "test-faux/faux-1"] + self.args
        return [self.binary] + self.args

    def env(self, home):
        path = "/usr/bin:/bin" + (":" + stub_dir() if self.stubs else "")
        env = {"PATH": path, "HOME": home, "TERM": "xterm-256color", "COLORTERM": "truecolor", "LANG": "C.UTF-8"}
        if self.family == "pig":
            env.update({"PIG_HOME": home + "/.pig", "PI_HOME": home + "/.pi", "PIG_CODING_AGENT_DIR": home + "/agent",
                        "PI_CODING_AGENT_DIR": home + "/agent", "PIG_TEST_FAUX": "1"})
        env.update(self.extra_env)
        if "PIG_PROFILE" in env:
            env["PIG_PROFILE_DIR"] = home + "/profile"
        return env


def subjects():
    e = os.environ.get
    out = {}
    if e("PIG_BIN"):
        out["pig"] = Subject("pig", "pig", e("PIG_BIN"))
        out["pig-trace"] = Subject("pig-trace", "pig", e("PIG_BIN"), {"PIG_STARTUP_TRACE": "1"})
        out["pig-metrics"] = Subject("pig-metrics", "pig", e("PIG_BIN"), {"PIG_PROFILE": "metrics"})
    if e("PHI_BIN"):
        # PHI_OFFLINE skips phi's startup version check (pig --offline does the same); phi-fresh is a first install as shipped: update check on and fd/rg missing, so it downloads them.
        out["phi"] = Subject("phi", "phi", e("PHI_BIN"), {"PHI_OFFLINE": "1"})
        out["phi-fresh"] = Subject("phi-fresh", "phi", e("PHI_BIN"), stubs=False)
    if e("PIG_SCRATCH_BIN"):
        out["pig-scratch"] = Subject("pig-scratch", "pig", e("PIG_SCRATCH_BIN"))
        out["pig-scratch-nohljs"] = Subject("pig-scratch-nohljs", "pig", e("PIG_SCRATCH_BIN"), {"HARNESS_NO_HLJS_LOAD": "1"})
    if e("PHI_SCRATCH_BIN"):
        out["phi-scratch"] = Subject("phi-scratch", "phi", e("PHI_SCRATCH_BIN"), {"PHI_OFFLINE": "1"})
    if e("FLOOR_BIN"):
        out["floor"] = Subject("floor", "floor", e("FLOOR_BIN"))
    return out


# ---------------------------------------------------------------- process / pty

def spawn(subject, home, cwd, stderr_path=None, extra_env=None):
    """Fork a child on a new pty (controlling terminal, ROWSxCOLS). Returns (pid, master_fd, t0_ns)."""
    argv, env = subject.argv(), subject.env(home)
    env.update(extra_env or {})
    errfd = os.open(stderr_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644) if stderr_path else os.open("/dev/null", os.O_WRONLY)
    t0 = time.perf_counter_ns()
    pid, master = os.forkpty()
    if pid == 0:
        try:
            os.dup2(errfd, 2)
            os.chdir(cwd)
            os.execve(argv[0], argv, env)
        finally:
            os._exit(127)
    os.close(errfd)
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    return pid, master, t0


def answer(master, data, tail):
    """Reply to DA1 and OSC 11 found in tail+data, in stream order. Returns the new tail (bytes that may begin a split query)."""
    buf = tail + data
    replies, end = [], 0
    for m in re.finditer(rb"\x1b\]11;\?(?:\x07|\x1b\\)|\x1b\[0?c", buf):
        replies.append(DA1_REPLY if m.group().endswith(b"c") else OSC11_REPLY)
        end = m.end()
    if replies:
        os.write(master, b"".join(replies))
    return buf[end:][-12:]


def reap(pid, master, kill=True):
    if kill:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    try:
        os.close(master)
    except OSError:
        pass
    try:
        os.waitpid(pid, 0)
    except ChildProcessError:
        pass


def evict(path):
    """Drop the file's clean page-cache pages (posix_fadvise DONTNEED) and return how many pages stay resident."""
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd) if False else None
        os.posix_fadvise(fd, 0, 0, os.POSIX_FADV_DONTNEED)
    finally:
        os.close(fd)
    out = subprocess.run(["fincore", "-n", "-b", "-o", "RES,SIZE", path], capture_output=True, text=True).stdout.split()
    page = os.sysconf("SC_PAGE_SIZE")
    return int(out[0]) // page if out else -1


def cached_pages(path):
    out = subprocess.run(["fincore", "-n", "-b", "-o", "RES", path], capture_output=True, text=True).stdout.split()
    return int(out[0]) // os.sysconf("SC_PAGE_SIZE") if out else -1


# ---------------------------------------------------------------- first frame

def record(subject, home, cwd, window, stderr_path=None, extra_env=None, answers=True):
    """Spawn, answer DA1/OSC 11 immediately, and record (t_ns, bytes) for `window` seconds. No parsing while recording."""
    pid, master, t0 = spawn(subject, home, cwd, stderr_path, extra_env)
    chunks, tail = [], b""
    deadline = t0 + int(window * 1e9)
    while True:
        remaining = (deadline - time.perf_counter_ns()) / 1e9
        if remaining <= 0:
            break
        r, _, _ = select.select([master], [], [], remaining)
        if not r:
            break
        try:
            data = os.read(master, 65536)
        except OSError:
            break
        t = time.perf_counter_ns()
        if not data:
            break
        chunks.append((t - t0, data))
        if answers:
            tail = answer(master, data, tail)
    reap(pid, master)
    return chunks


def analyze(chunks, model_mark, cwd_mark):
    """First non-query output and first full frame, from the recorded chunks. Times in ms from just before fork."""
    scr = vtscreen.Screen(ROWS, COLS)
    res = {"first_byte_ms": None, "first_nonquery_ms": None, "first_glyph_ms": None, "first_full_ms": None, "bytes_to_full": None}
    total = 0
    for t, data in chunks:
        total += len(data)
        ms = t / 1e6
        scr.feed(data)
        if res["first_byte_ms"] is None:
            res["first_byte_ms"] = ms
        if res["first_nonquery_ms"] is None and scr.nonquery_bytes:
            res["first_nonquery_ms"] = ms
        if res["first_glyph_ms"] is None and scr.glyphs():
            res["first_glyph_ms"] = ms
        if res["first_full_ms"] is None and scr.glyphs() > 20:
            text = scr.text()
            if model_mark in text and cwd_mark in text:
                res["first_full_ms"] = ms
                res["bytes_to_full"] = total
                res["final_screen_glyphs_at_full"] = scr.glyphs()
                break
    return res


def parse_trace(path):
    marks = {}
    try:
        for line in open(path, errors="replace"):
            m = re.match(r"\[startup\] (\S+)\s+(\d+)ms", line)
            if m:
                marks.setdefault(m.group(1), int(m.group(2)))
    except OSError:
        pass
    return marks


def cmd_first_frame(args):
    """Runs the named subjects round-robin (order rotated each round) so slow drift of the host hits every subject alike."""
    gc.disable()
    subs = [subjects()[n] for n in args.subject.split(",")]
    os.makedirs(WORK, exist_ok=True)
    window = args.window or (3.0 if args.mode == "cold" else 2.0)
    shared = {}
    for subj in subs:
        if args.mode == "warmhome":
            shared[subj.name] = tempfile.mkdtemp(prefix="home-", dir=WORK)
            subj.prepare_home(shared[subj.name])
            for _ in range(3):  # untimed seeding launches
                cwd = tempfile.mkdtemp(prefix="cwd-", dir=WORK)
                record(subj, shared[subj.name], cwd, 1.2)
                shutil.rmtree(cwd, ignore_errors=True)
        elif args.mode == "warm":
            home, cwd = _fresh(subj)
            record(subj, home, cwd, 1.2)  # one untimed run to put the binary in the page cache
            shutil.rmtree(home, ignore_errors=True)
            shutil.rmtree(cwd, ignore_errors=True)
    out = open(args.out, "a")
    for i in range(args.warmup + args.runs):
        k = i % len(subs)
        for subj in subs[k:] + subs[:k]:
            if subj.name in shared:
                home, cwd = shared[subj.name], tempfile.mkdtemp(prefix="cwd-", dir=WORK)
            else:
                home, cwd = _fresh(subj)
            pages_before = None
            if args.mode == "cold":
                pages_before = evict(subj.binary)
            else:
                pages_before = cached_pages(subj.binary)
            stderr_path = os.path.join(home, "stderr.txt") if subj.name == "pig-trace" else None
            chunks = record(subj, home, cwd, window, stderr_path, answers=not args.silent)
            if subj.family == "floor":
                res = {"first_byte_ms": chunks[0][0] / 1e6 if chunks else None}
            else:
                res = analyze(chunks, MODEL_MARK[subj.family], os.path.basename(cwd))
            rec = {"subject": subj.name, "mode": args.mode + ("+silent" if args.silent else ""), "run": i - args.warmup, "pages_cached_before": pages_before,
                   "chunks": len(chunks), **res}
            if stderr_path:
                rec["trace"] = parse_trace(stderr_path)
            if i >= args.warmup:
                out.write(json.dumps(rec) + "\n")
                out.flush()
            shutil.rmtree(cwd, ignore_errors=True)
            if subj.name not in shared:
                shutil.rmtree(home, ignore_errors=True)
    for home in shared.values():
        shutil.rmtree(home, ignore_errors=True)


def _fresh(subj):
    home = tempfile.mkdtemp(prefix="home-", dir=WORK)
    subj.prepare_home(home)
    return home, tempfile.mkdtemp(prefix="cwd-", dir=WORK)


# ---------------------------------------------------------------- memory

SMAPS_HEAD = re.compile(r"^([0-9a-f]+)-([0-9a-f]+) (\S{4}) ([0-9a-f]+) (\S+) (\d+)\s*(.*)$")
KEYS = ("Rss", "Pss", "Referenced", "Private_Clean", "Private_Dirty", "Shared_Clean", "Shared_Dirty", "Anonymous")


def read_rollup(pid):
    out = {}
    try:
        for line in open(f"/proc/{pid}/smaps_rollup"):
            k, _, v = line.partition(":")
            if v.strip().endswith("kB"):
                out[k] = int(v.split()[0])
    except OSError:
        return None
    return out


def classify(path, perms, start, exe):
    if path == exe:
        return {"r-xp": "binary_text", "r--p": "binary_rodata", "rw-p": "binary_data"}.get(perms, "binary_other")
    if path.startswith("[anon:"):
        return "anon:" + path[6:].strip(" ]")
    if path in ("[stack]",):
        return "main_stack"
    if path in ("[vdso]", "[vvar]", "[vsyscall]", "[vvar_vclock]"):
        return "kernel_pages"
    if path == "" or path == "[heap]":
        return "go_heap_arena" if 0xC000000000 <= start < 0xC100000000 else "anon_other"
    return "file:" + path


def read_smaps_split(pid):
    try:
        exe = os.readlink(f"/proc/{pid}/exe")
        lines = open(f"/proc/{pid}/smaps").read().splitlines()
    except OSError:
        return None
    split, cur = {}, None
    for line in lines:
        m = SMAPS_HEAD.match(line)
        if m:
            cls = classify(m.group(7), m.group(3), int(m.group(1), 16), exe)
            cur = split.setdefault(cls, {k: 0 for k in KEYS} | {"mappings": 0, "vsize_kb": 0})
            cur["mappings"] += 1
            cur["vsize_kb"] += (int(m.group(2), 16) - int(m.group(1), 16)) // 1024
        elif cur is not None:
            k, _, v = line.partition(":")
            if k in KEYS:
                cur[k] += int(v.split()[0])
    return split


def descendants(pid):
    kids = {}
    for d in os.listdir("/proc"):
        if d.isdigit():
            try:
                stat = open(f"/proc/{d}/stat").read()
                kids.setdefault(int(stat[stat.rindex(")") + 2:].split()[1]), []).append(int(d))
            except OSError:
                pass
    out, stack = [], [pid]
    while stack:
        p = stack.pop()
        out.append(p)
        stack.extend(kids.get(p, []))
    return out


def cpu_ms(pids):
    """User+system CPU consumed so far by the processes (clock ticks of /proc/PID/stat)."""
    tck, total = os.sysconf("SC_CLK_TCK"), 0
    for p in pids:
        try:
            stat = open(f"/proc/{p}/stat").read()
            f = stat[stat.rindex(")") + 2:].split()
            total += int(f[11]) + int(f[12])
        except OSError:
            pass
    return total * 1000 // tck


def sample(pid):
    """RSS / PSS of the process and its descendants, the root's smaps split, and thread count."""
    pids = descendants(pid)
    roll = {p: read_rollup(p) for p in pids}
    roll = {p: r for p, r in roll.items() if r}
    tot = {k: sum(r.get(k, 0) for r in roll.values()) for k in ("Rss", "Pss", "Pss_Anon", "Pss_File", "Pss_Shmem", "Private_Dirty")}
    root = roll.get(pid) or {}
    try:
        threads = len(os.listdir(f"/proc/{pid}/task"))
    except OSError:
        threads = -1
    return {"processes": len(roll), "cpu_ms": cpu_ms(pids), "tree_kb": tot, "root_kb": {k: root.get(k) for k in ("Rss", "Pss", "Pss_Anon", "Pss_File", "Pss_Shmem", "Private_Dirty")},
            "threads": threads, "split": read_smaps_split(pid)}


class Live:
    """One running session for the memory probe: answers queries, tracks the screen, detects the first full frame."""

    def __init__(self, subj, home, cwd, stderr_path=None, extra_env=None):
        self.subj, self.home, self.cwd = subj, home, cwd
        self.pid, self.fd, self.t0 = spawn(subj, home, cwd, stderr_path, extra_env)
        self.scr = vtscreen.Screen(ROWS, COLS)
        self.tail = b""
        self.full_at = None
        self.alive = True

    def pump(self, timeout):
        r, _, _ = select.select([self.fd], [], [], timeout)
        if not r:
            return
        try:
            data = os.read(self.fd, 65536)
        except OSError:
            self.alive = False
            return
        if not data:
            self.alive = False
            return
        self.tail = answer(self.fd, data, self.tail)
        if self.full_at is None:
            self.scr.feed(data)
            if self.scr.glyphs() > 20:
                text = self.scr.text()
                if MODEL_MARK[self.subj.family] in text and os.path.basename(self.cwd) in text:
                    self.full_at = time.perf_counter()


def pump_all(lives, seconds):
    end = time.perf_counter() + seconds
    while time.perf_counter() < end:
        fds = {l.fd: l for l in lives if l.alive}
        if not fds:
            time.sleep(max(0, end - time.perf_counter()))
            return
        r, _, _ = select.select(list(fds), [], [], min(0.05, max(0, end - time.perf_counter())))
        for fd in r:
            fds[fd].pump(0)


def cmd_memory(args):
    gc.disable()
    subj = subjects()[args.subject]
    os.makedirs(WORK, exist_ok=True)
    out = open(args.out, "a")
    for run in range(args.runs):
        lives = []
        for k in range(args.sessions):
            home, cwd = _fresh(subj)
            extra = {}
            if args.gctrace:
                extra["GODEBUG"] = "gctrace=1"
            if args.memclasses:
                extra["HARNESS_MEMCLASSES"] = os.path.join(home, "memclasses.jsonl")
            lives.append(Live(subj, home, cwd, os.path.join(home, "stderr.txt") if (args.gctrace or subj.name == "pig-trace") else None, extra))
        # wait for every session's first full frame
        t_end = time.perf_counter() + 20
        while any(l.full_at is None for l in lives) and time.perf_counter() < t_end:
            pump_all(lives, 0.02)
        stamps, classes = {}, {}

        def take(phase):
            stamps[phase] = [sample(l.pid) for l in lives]
            if args.memclasses:  # SIGUSR2 makes a scratch build append its runtime/metrics memory classes
                for l in lives:
                    os.kill(l.pid, signal.SIGUSR2)
                pump_all(lives, 0.3)
                classes[phase] = [_last_json(os.path.join(l.home, "memclasses.jsonl")) for l in lives]

        take("first_frame")
        pump_all(lives, args.ready_after)
        take("ready")
        pump_all(lives, args.idle)
        take("idle")
        if args.freeos:  # scratch builds: SIGUSR1 = debug.FreeOSMemory; what would returning the start-up garbage give back?
            for l in lives:
                os.kill(l.pid, signal.SIGUSR1)
            pump_all(lives, 1.0)
            take("after_freeos")
        rec = {"subject": subj.name, "sessions": args.sessions, "run": run, "ready_after_s": args.ready_after, "idle_s": args.idle,
               "all_framed": all(l.full_at for l in lives), "samples": stamps}
        if args.memclasses:
            rec["memclasses"] = classes
        # tear down; pig-metrics wants a clean exit
        if args.clean_exit:
            for l in lives:
                os.write(l.fd, b"\x04")
            pump_all(lives, 3.0)
        for l in lives:
            reap(l.pid, l.fd)
        if subj.name == "pig-metrics":
            rec["process_metrics"] = [_metrics_file(l.home) for l in lives]
        if args.gctrace:
            rec["gctrace_tail"] = [_tail(os.path.join(l.home, "stderr.txt")) for l in lives]
        out.write(json.dumps(rec) + "\n")
        out.flush()
        for l in lives:
            shutil.rmtree(l.home, ignore_errors=True)
            shutil.rmtree(l.cwd, ignore_errors=True)


def _metrics_file(home):
    d = os.path.join(home, "profile")
    for name in sorted(os.listdir(d)) if os.path.isdir(d) else []:
        if name.endswith("metrics.json") or "metrics" in name:
            try:
                return json.load(open(os.path.join(d, name)))
            except (OSError, ValueError):
                return None
    return None


def _last_json(path):
    try:
        return json.loads(open(path).read().splitlines()[-1])
    except (OSError, IndexError, ValueError):
        return None


def _tail(path, n=3):
    try:
        return open(path, errors="replace").read().splitlines()[-n:]
    except OSError:
        return []


# ---------------------------------------------------------------- summary

def stats(xs):
    xs = sorted(x for x in xs if x is not None)
    if not xs:
        return None
    q = statistics.quantiles(xs, n=4, method="inclusive") if len(xs) > 1 else [xs[0]] * 3
    p90 = xs[min(len(xs) - 1, max(0, -(-len(xs) * 9 // 10) - 1))]
    return {"n": len(xs), "median": statistics.median(xs), "min": xs[0], "max": xs[-1], "p25": q[0], "p75": q[2], "p90": p90}


def cmd_summarize(args):
    rows = [json.loads(l) for f in args.files for l in open(f) if l.strip()]
    ff = [r for r in rows if "first_byte_ms" in r]
    mem = [r for r in rows if "samples" in r]
    groups = {}
    for r in ff:
        groups.setdefault((r["subject"], r["mode"]), []).append(r)
    if groups:
        print("| subject | mode | n | first byte | first non-query | first glyph | first full frame (min-max) | p90 full |")
        print("|---|---|---:|---:|---:|---:|---:|---:|")
        for (s, m), rs in sorted(groups.items()):
            st = {k: stats([r.get(k) for r in rs]) for k in ("first_byte_ms", "first_nonquery_ms", "first_glyph_ms", "first_full_ms")}
            full = st["first_full_ms"]
            cell = lambda k: f"{st[k]['median']:.1f}" if st[k] else "-"  # noqa: E731
            print(f"| {s} | {m} | {len(rs)} | {cell('first_byte_ms')} | {cell('first_nonquery_ms')} | {cell('first_glyph_ms')} | "
                  + (f"{full['median']:.1f} ({full['min']:.1f}-{full['max']:.1f}) | {full['p90']:.1f} |" if full else "- | - |"))
        marks = {}
        for (s, m), rs in groups.items():
            if s == "pig-trace":
                for r in rs:
                    for k, v in r.get("trace", {}).items():
                        marks.setdefault((m, k), []).append(v)
        if marks:
            print("\n| trace mode | mark | n | median ms | min-max |")
            print("|---|---|---:|---:|---:|")
            for (m, k), v in sorted(marks.items(), key=lambda kv: (kv[0][0], statistics.median(kv[1]))):
                print(f"| {m} | {k} | {len(v)} | {statistics.median(v):.0f} | {min(v)}-{max(v)} |")
    mg = {}
    for r in mem:
        mg.setdefault((r["subject"], r["sessions"]), []).append(r)
    if mg:
        print("\n| subject | sessions | phase | n | total RSS MB | total PSS MB | root RSS MB | root PSS MB | procs | threads |")
        print("|---|---:|---|---:|---:|---:|---:|---:|---:|---:|")
        for (s, k), rs in sorted(mg.items()):
            for phase in ("first_frame", "ready", "idle", "after_freeos"):
                if phase not in rs[0]["samples"]:
                    continue

                def col(f):
                    return stats([f(r["samples"][phase]) for r in rs])
                trss = col(lambda ss: sum(x["tree_kb"]["Rss"] for x in ss) / 1024)
                tpss = col(lambda ss: sum(x["tree_kb"]["Pss"] for x in ss) / 1024)
                rrss = col(lambda ss: statistics.median(x["root_kb"]["Rss"] for x in ss) / 1024)
                rpss = col(lambda ss: statistics.median(x["root_kb"]["Pss"] for x in ss) / 1024)
                procs = col(lambda ss: sum(x["processes"] for x in ss) / len(ss))
                thr = col(lambda ss: statistics.median(x["threads"] for x in ss))
                f = lambda x, d=1: f"{x['median']:.{d}f} ({x['min']:.{d}f}-{x['max']:.{d}f})"  # noqa: E731
                print(f"| {s} | {k} | {phase} | {len(rs)} | {f(trss)} | {f(tpss)} | {f(rrss)} | {f(rpss)} | {procs['median']:.1f} | {thr['median']:.0f} |")
        print("\n| subject | sessions | CPU ms to first frame (all procs) | CPU ms over idle window (all procs) | idle CPU % of one core |")
        print("|---|---:|---:|---:|---:|")
        for (s, k), rs in sorted(mg.items()):
            ff = stats([sum(x["cpu_ms"] for x in r["samples"]["first_frame"]) for r in rs])
            idle = stats([sum(b["cpu_ms"] - a["cpu_ms"] for a, b in zip(r["samples"]["ready"], r["samples"]["idle"])) for r in rs])
            pct = stats([sum(b["cpu_ms"] - a["cpu_ms"] for a, b in zip(r["samples"]["ready"], r["samples"]["idle"])) / (r["idle_s"] * 10.0) for r in rs])
            print(f"| {s} | {k} | {ff['median']:.0f} ({ff['min']:.0f}-{ff['max']:.0f}) | {idle['median']:.0f} ({idle['min']:.0f}-{idle['max']:.0f}) | {pct['median']:.1f} |")
        mc = [r for r in mem if r.get("memclasses")]
        if mc:
            print("\nGo runtime memory classes (scratch builds, SIGUSR2), phase=idle, root process, median MB:")
            names = sorted({k for r in mc for k in (r["memclasses"]["idle"][0] or {}) if k.startswith("/memory/classes/") or k.startswith("/gc/heap")})
            subs = sorted({r["subject"] for r in mc})
            print("| metric | " + " | ".join(subs) + " |")
            print("|---|" + "---:|" * len(subs))
            for nme in names:
                cells = []
                for sb in subs:
                    vals = [r["memclasses"]["idle"][0].get(nme) for r in mc if r["subject"] == sb and r["memclasses"]["idle"][0]]
                    vals = [v for v in vals if v is not None]
                    cells.append(f"{statistics.median(vals) / 1e6:.2f}" if vals else "-")
                print(f"| {nme} | " + " | ".join(cells) + " |")
        print("\nper-mapping split, root process, phase=idle, median MB across runs (Referenced = pages the CPU accessed since mapping, so it excludes pages the kernel's fault-around mapped but nothing used):")
        for (s, k), rs in sorted(mg.items()):
            if k != 1:
                continue
            classes = sorted({c for r in rs for c in (r["samples"]["idle"][0]["split"] or {})})
            parts = []
            for c in classes:
                pss = [r["samples"]["idle"][0]["split"].get(c, {}).get("Pss", 0) / 1024 for r in rs]
                rss = [r["samples"]["idle"][0]["split"].get(c, {}).get("Rss", 0) / 1024 for r in rs]
                ref = [r["samples"]["idle"][0]["split"].get(c, {}).get("Referenced", 0) / 1024 for r in rs]
                parts.append((statistics.median(pss), statistics.median(rss), c, statistics.median(ref)))
            print(f"\n{s}:")
            for pss, rss, c, ref in sorted(parts, reverse=True):
                if rss >= 0.05:
                    print(f"  {c:28s} PSS {pss:7.2f}  RSS {rss:7.2f}  Referenced {ref:7.2f}")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("first-frame")
    p.add_argument("--subject", required=True, help="one subject or a comma list, run round-robin")
    p.add_argument("--mode", choices=("cold", "warm", "warmhome"), required=True)
    p.add_argument("--runs", type=int, default=30)
    p.add_argument("--warmup", type=int, default=0)
    p.add_argument("--window", type=float)
    p.add_argument("--silent", action="store_true", help="a terminal that answers no query at all (DA1 and OSC 11 included)")
    p.add_argument("--out", required=True)
    p.set_defaults(fn=cmd_first_frame)
    p = sub.add_parser("memory")
    p.add_argument("--subject", required=True)
    p.add_argument("--sessions", type=int, default=1)
    p.add_argument("--runs", type=int, default=7)
    p.add_argument("--ready-after", type=float, default=1.0)
    p.add_argument("--idle", type=float, default=10.0)
    p.add_argument("--gctrace", action="store_true")
    p.add_argument("--memclasses", action="store_true")
    p.add_argument("--freeos", action="store_true", help="scratch builds: after idle, SIGUSR1 (debug.FreeOSMemory) and sample again")
    p.add_argument("--clean-exit", action="store_true")
    p.add_argument("--out", required=True)
    p.set_defaults(fn=cmd_memory)
    p = sub.add_parser("summarize")
    p.add_argument("files", nargs="+")
    p.set_defaults(fn=cmd_summarize)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
