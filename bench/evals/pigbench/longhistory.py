# SPDX-License-Identifier: MIT
"""Long-history bench: sessions with ~1M tokens of history, and ~3M tokens across 3 compactions.

What Pi 1.1.0 does (upstream pinned at v1.1.0, packages/):
  - The session file holds every entry, compacted or not (coding-agent/src/core/session-manager.ts: appendFileSync per entry at
    1187; loadEntriesFromFile at 627 reads the whole file when a session is opened or resumed).
  - The transcript the TUI builds is only the model context: `buildContextEntries` (session-manager.ts:476-510) returns the
    latest compaction entry plus the entries from `firstKeptEntryId` on. `renderInitialMessages` (interactive-mode.ts:4208) renders
    that on startup and, through `renderCurrentSessionState` (interactive-mode.ts:2212-2218), on /resume. After a compaction,
    `compaction_end` (interactive-mode.ts:3686-3711) clears the chat container and re-renders the context entries, so entries
    from before the compaction leave the transcript. They are no longer rendered, but lines already written to the terminal
    stay in its scrollback until a full redraw clears it.
  - Every frame renders all components (tui.ts:394-406, `Container.render`; tui-main-screen.ts:264 `this.render(width)`) and then
    diffs the lines against the previous frame, so per-frame work is linear in the rendered transcript, softened by per-component
    caches (components/markdown.ts:247-283 cache the lines per width).
  - A width change (tui-main-screen.ts:337-341) or a height change (344-350, except on Termux) is a full redraw:
    `fullRender(true)` writes ESC[2J ESC[H ESC[3J (clear screen and scrollback) and then every line of the rendered transcript.
    A first changed line above the viewport also forces a full redraw (tui-main-screen.ts:450-453).
  So a 3M-token session with 3 compactions shows Pi only the last compaction's summary, its kept entries and what came after.
  The generator therefore builds, per case, a session file with the full history and records how many tokens the context holds.

Cases: `1m` is ~1M tokens with no compaction (the TUI renders all of it); `3m` is three ~1M-token segments each followed by a
compaction, then a short tail (the TUI renders summary + kept + tail; open, resume and append still read the whole 3M file).

Measured per arm and case: open time by RPC (get_state) and by TUI (first frame, settled), /resume time (RPC switch_session and the
TUI `/resume` selector), peak and steady RSS, keystroke latency at the bottom, frame time and frames while a reply streams,
full redraw on width and height changes (time, bytes, frames, whether scrollback is cleared), and per-turn append cost on the loaded file.
"""
import hashlib
import json
import os
import random
import re
import signal
import struct
import tempfile
import time

from . import arms as armlib
from . import checkpoint
from . import cgroup, egress, envinfo, procio, stats
from .mock import MODEL, REPLY, MockServer, Step, now_ns, scripted_policy

CASES = {"1m": {"segments": 1, "tokens_per_segment": 1_000_000, "compactions": 0, "tail_tokens": 0},
         "3m": {"segments": 3, "tokens_per_segment": 1_000_000, "compactions": 3, "tail_tokens": 60_000}}
# A context window larger than any case and automatic compaction off, so only the scripted compactions exist.
BIG = {"context_window": 4_000_000, "compaction": {"enabled": False}}
KEEP_TOKENS = 20_000
BYTES_PER_TOKEN = 4
SESSION_VERSION = 3
VOCAB = ("session transcript entry render buffer stream token provider model harness compaction summary cursor viewport latency frame "
         "diff terminal width height scrollback markdown component cache append parse index branch leaf message tool result "
         "file read write edit search query reply request response header delta chunk window context budget reserve").split()
CODE = ["func {n}(ctx context.Context, in {t}) ({t}, error) {{", "\tif in == nil {{ return nil, errors.New(\"{n}: nil input\") }}",
        "\tfor i := range in.Items {{ total += in.Items[i].Size * {k} }}", "\treturn in, nil", "}}"]
ANSI = re.compile(rb"\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78]")


def text_block(rng, chars):
    """Deterministic prose and code of about `chars` characters."""
    out, n = [], 0
    while n < chars:
        if rng.random() < 0.35:
            name = rng.choice(VOCAB) + str(rng.randrange(1000))
            block = "```go\n" + "\n".join(line.format(n=name, t=rng.choice(VOCAB).title(), k=rng.randrange(9)) for line in CODE) + "\n```\n"
        else:
            block = " ".join(rng.choice(VOCAB) for _ in range(rng.randrange(20, 60))).capitalize() + ".\n\n"
        out.append(block)
        n += len(block)
    return "".join(out)[:chars]


class Writer:
    def __init__(self, path, seed):
        self.f = open(path, "w", newline="\n")  # JSONL as Pi writes it on every host, so a seed gives the same bytes on Windows
        self.rng = random.Random(seed)
        self.parent = None
        self.n = 0
        self.tokens = 0  # context tokens so far (since the last compaction, summary included)
        self.entries = []  # (id, role, tokens) of message entries since the last compaction
        self.clock = 1_790_000_000_000
        self.total_tokens = 0

    def _id(self):
        self.n += 1
        return f"{self.n:08x}"

    def _emit(self, entry):
        self.f.write(json.dumps(entry, separators=(",", ":")) + "\n")

    def header(self, cwd):
        self._emit({"type": "session", "version": SESSION_VERSION, "id": f"bench-{self.rng.randrange(10**9):09d}", "timestamp": "2026-10-01T00:00:00.000Z", "cwd": cwd})

    def message(self, message, tokens):
        eid = self._id()
        self.clock += 1500
        message["timestamp"] = self.clock
        self._emit({"type": "message", "id": eid, "parentId": self.parent, "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime(self.clock / 1000)), "message": message})
        self.parent = eid
        self.tokens += tokens
        self.total_tokens += tokens
        self.entries.append((eid, message["role"], tokens))
        return eid

    def usage(self):
        return {"input": self.tokens, "output": 300, "cacheRead": 0, "cacheWrite": 0, "totalTokens": self.tokens + 300,
                "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}

    def turn(self):
        """One user turn: a question, a read tool call and its result, and a text answer."""
        rng = self.rng
        question = text_block(rng, rng.randrange(120, 400))
        self.message({"role": "user", "content": [{"type": "text", "text": question}]}, len(question) // BYTES_PER_TOKEN)
        call_id = f"call_{self.n:08x}"
        path = f"src/{rng.choice(VOCAB)}/{rng.choice(VOCAB)}{rng.randrange(100)}.go"
        self.message({"role": "assistant", "content": [{"type": "toolCall", "id": call_id, "name": "read", "arguments": {"path": path}}], "api": "openai-completions",
                      "provider": "bench", "model": MODEL, "usage": self.usage(), "stopReason": "toolUse"}, 40)
        body = text_block(rng, rng.randrange(2500, 5500))
        self.message({"role": "toolResult", "toolCallId": call_id, "toolName": "read", "content": [{"type": "text", "text": body}], "isError": False}, len(body) // BYTES_PER_TOKEN)
        answer = text_block(rng, rng.randrange(600, 1600))
        self.message({"role": "assistant", "content": [{"type": "text", "text": answer}], "api": "openai-completions", "provider": "bench", "model": MODEL,
                      "usage": self.usage(), "stopReason": "stop"}, len(answer) // BYTES_PER_TOKEN)

    def segment(self, tokens):
        start = self.tokens
        while self.tokens - start < tokens:
            self.turn()

    def compaction(self):
        """Compact: keep the last KEEP_TOKENS of entries starting at a user message, summarise the rest."""
        kept, acc = None, 0
        for eid, role, tok in reversed(self.entries):
            acc += tok
            if role == "user" and acc >= KEEP_TOKENS:
                kept = eid
                break
        before = self.tokens
        summary = "## Summary\n" + text_block(self.rng, 6000)
        eid = self._id()
        self.clock += 1500
        self._emit({"type": "compaction", "id": eid, "parentId": self.parent, "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime(self.clock / 1000)),
                    "summary": summary, "firstKeptEntryId": kept, "tokensBefore": before})
        self.parent = eid
        keep_from = next(i for i, (e, _, _) in enumerate(self.entries) if e == kept)
        self.entries = self.entries[keep_from:]
        self.tokens = sum(t for _, _, t in self.entries) + len(summary) // BYTES_PER_TOKEN

    def close(self):
        self.f.close()


def generate(path, case, seed, cwd):
    """Write a deterministic session file for `case` and return its facts (entries, bytes, total tokens, tokens the TUI renders)."""
    spec = CASES[case]
    w = Writer(path, seed)
    w.header(cwd)
    for i in range(spec["segments"]):
        w.segment(spec["tokens_per_segment"])
        if i < spec["compactions"]:
            w.compaction()
    if spec["tail_tokens"]:
        w.segment(spec["tail_tokens"])
    w.close()
    with open(path, "rb") as f:
        digest = hashlib.sha256(f.read()).hexdigest()
    return {"case": case, "seed": seed, "cwd": cwd, "bytes": os.path.getsize(path), "entries": w.n, "compactions": spec["compactions"], "history_tokens": w.total_tokens,
            "context_tokens": w.tokens, "sha256": digest}


def session_for(cache_dir, case, seed, cwd):
    """Generate (or reuse) the session file for a case; the path embeds seed and case, and the digest is recorded in the report."""
    os.makedirs(cache_dir, exist_ok=True)
    path = os.path.join(cache_dir, f"history-{case}-seed{seed}.jsonl")
    if os.path.exists(path + ".json"):
        with open(path + ".json") as f:
            stale = json.load(f).get("cwd") != cwd
        if stale:
            os.unlink(path + ".json")
    facts_path = path + ".json"
    if os.path.exists(path) and os.path.exists(facts_path):
        with open(facts_path) as f:
            return path, json.load(f)
    facts = generate(path, case, seed, cwd)
    with open(facts_path, "w") as f:
        json.dump(facts, f)
    return path, facts


def ms(a, b):
    return (b - a) / 1e6


def strip(data):
    return ANSI.sub(b"", data)


def frames(chunks, since=0):
    """(t_ns, bytes) per rendered frame: synchronized-output blocks when the TUI uses them, else output chunks."""
    out = []
    for t, c in chunks[since:]:
        n = c.count(b"\x1b[?2026l")
        if n:
            out.extend([(t, len(c) // n)] * n)
    if out:
        return out
    return [(t, len(c)) for t, c in chunks[since:]]


def resize(term, rows, cols):
    # POSIX-only modules: importing them at module load would stop every pigbench command on Windows, where the PTY scenarios do not run.
    import fcntl
    import termios
    fcntl.ioctl(term.master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
    try:
        os.killpg(os.getpgid(term.proc.pid), signal.SIGWINCH)
    except OSError:
        pass


def settle(term, since, quiet=0.8, timeout=120):
    """Wait for output to go quiet; return (t_first_after, t_last) in ns for chunks after index `since`, or None."""
    deadline = time.monotonic() + timeout
    with term.cond:
        while True:
            fresh = term.chunks[since:]
            if fresh and (now_ns() - fresh[-1][0]) / 1e9 >= quiet:
                return fresh[0][0], fresh[-1][0]
            left = deadline - time.monotonic()
            if left <= 0:
                return None
            term.cond.wait(min(0.05, left))


def one_tui(ctx, arm, case, path, facts, mode="fullscreen"):
    """Open the session in the TUI, then keystrokes, streaming, and resizes, all on one process."""
    h = ctx.harness(arm, f"tui-{case}-{mode}", settings_extra={"tuiMode": mode}, **BIG)
    srv = ctx.server
    stream_text = " ".join(["streamed"] * 400)
    srv.reset(policy=scripted_policy([Step(text=stream_text, event_delay_ms=8)]))
    meter = cgroup.new_meter()
    argv = [arm.binary, "--provider", "bench", "--model", MODEL, "--session", path]
    term = procio.Pty(argv, h.env, ctx.project, meter)
    s = {}
    try:
        deadline = time.monotonic() + ctx.opts.history_timeout
        t_first = None
        while time.monotonic() < deadline and t_first is None:
            with term.cond:
                term.cond.wait(0.05)
                if term.chunks and len(strip(b"".join(c for _, c in term.chunks)).strip()) > 0:
                    t_first = term.chunks[0][0]
        if t_first is None:
            return {"failure": "no output"}
        s["first_output_ms"] = ms(term.t_spawn, t_first)
        got = settle(term, 0, 1.5, ctx.opts.history_timeout)
        if got is None:
            return {"failure": "the screen never settled"}
        s["open_settled_ms"] = ms(term.t_spawn, got[1])
        head = b"".join(c for _, c in term.chunks)
        s["alt_screen"] = b"\x1b[?1049h" in head
        s["open_bytes"] = sum(len(c) for _, c in term.chunks)
        s["open_lines"] = sum(c.count(b"\n") for _, c in term.chunks)
        s["open_cpu_ms"] = meter.cpu_ms()
        s["open_peak_rss_mib"] = meter.max_proc_rss_mib()
        time.sleep(2.0)
        s["steady_rss_mib"] = rss_now(meter)
        # Keystroke latency at the bottom of a settled screen.
        lat = []
        for k in "abcdefghijklmnopqrstuvwxyz"[:20]:
            mark = len(term.chunks)
            t0 = now_ns()
            term.type(k)
            t = term.wait_text(k, 5, since=mark)
            if t is not None:
                lat.append(ms(t0, t))
            time.sleep(0.12)
        term.type("\x15")  # clear the line
        time.sleep(0.3)
        if lat:
            s["key_idle_ms_p50"], s["key_idle_ms_p95"] = stats.median(lat), stats.percentile(lat, 95)
        # Stream a long reply at the bottom; count frames and CPU; type while it streams.
        mark = len(term.chunks)
        cpu0 = meter.cpu_ms()
        term.type("go")
        time.sleep(0.2)
        t_enter = now_ns()
        term.type("\r")
        if not srv.wait_requests(1, 60):
            return {"failure": "the prompt never reached the endpoint"}
        lat2 = []
        time.sleep(0.5)
        for k in "ABCDEFGHIJ":
            m2 = len(term.chunks)
            t0 = now_ns()
            term.type(k)
            t = term.wait_text(k, 5, since=m2)
            if t is not None:
                lat2.append(ms(t0, t))
            time.sleep(0.2)
        quiet = settle(term, mark, 1.5, 120)
        if quiet is None:
            return {"failure": "streaming never finished"}
        fr = frames(term.chunks, mark)
        window = ms(t_enter, quiet[1])
        s["stream_frames"] = len(fr)
        s["stream_window_ms"] = window
        s["stream_cpu_ms"] = meter.cpu_ms() - cpu0
        gaps = [ms(a[0], b[0]) for a, b in zip(fr, fr[1:])]
        if gaps:
            s["frame_interval_ms_p50"], s["frame_interval_ms_p95"] = stats.median(gaps), stats.percentile(gaps, 95)
        if fr:
            s["frame_cpu_ms"] = s["stream_cpu_ms"] / len(fr)
            s["frame_bytes_p50"] = stats.median([b for _, b in fr])
        if lat2:
            s["key_streaming_ms_p50"], s["key_streaming_ms_p95"] = stats.median(lat2), stats.percentile(lat2, 95)
        term.type("\x15")
        time.sleep(0.3)
        # Resizes: width 120->100->120 and height 40->30->40. Each is a full redraw in Pi.
        for label, rows, cols in (("w100", 40, 100), ("w120", 40, 120), ("h30", 30, 120), ("h40", 40, 120)):
            mark = len(term.chunks)
            cpu = meter.cpu_ms()
            t0 = now_ns()
            resize(term, rows, cols)
            got = settle(term, mark, 0.6, ctx.opts.history_timeout)
            if got is None:
                s[f"resize_{label}_ms"] = None
                continue
            fresh = term.chunks[mark:]
            s[f"resize_{label}_ms"] = ms(t0, got[1])
            s[f"resize_{label}_first_byte_ms"] = ms(t0, got[0])
            s[f"resize_{label}_bytes"] = sum(len(c) for _, c in fresh)
            s[f"resize_{label}_lines"] = sum(c.count(b"\n") for _, c in fresh)
            s[f"resize_{label}_cpu_ms"] = meter.cpu_ms() - cpu
            s[f"resize_{label}_clears_scrollback"] = any(b"\x1b[3J" in c for _, c in fresh)
        s["peak_rss_mib"] = meter.max_proc_rss_mib()
        s["cpu_ms"] = meter.cpu_ms()
        s["peak_mem_mib"] = meter.peak_mem_mib()
        s["context_tokens"] = facts["context_tokens"]
        return s
    finally:
        term.close()
        meter.close()


def rss_now(meter):
    """Current resident set of the tree (not the peak): sum of VmRSS over the meter's members (a cgroup's, or the rusage meter's tree)."""
    pids = meter.pids()
    if not pids:
        return None
    return cgroup.status_kib(pids, "VmRSS") / 1024


def one_rpc(ctx, arm, case, path, facts):
    """Open by --session, then resume by switch_session from a fresh process, then append turns on the loaded file."""
    h = ctx.harness(arm, f"rpc-{case}", **BIG)
    srv = ctx.server
    srv.reset(policy=scripted_policy([Step(text=REPLY)]))
    s = {}
    # 1. Open: spawn on the file; ready is the first get_state response.
    meter = cgroup.new_meter()
    rpc = procio.Rpc([arm.binary, "--mode", "rpc", "--provider", "bench", "--model", MODEL, "--session", path], h.env, ctx.project, meter)
    try:
        rpc.send({"type": "get_state"})
        got = rpc.wait_for(lambda r: r.get("type") == "response" and r.get("command") == "get_state", ctx.opts.history_timeout)
        if got is None:
            return {"failure": "no get_state response when opening the session"}
        s["open_rpc_ms"] = ms(rpc.t_spawn, got[0])
        s["open_rpc_cpu_ms"] = meter.cpu_ms()
        s["open_rpc_peak_rss_mib"] = meter.max_proc_rss_mib()
        time.sleep(1.0)
        s["open_rpc_steady_rss_mib"] = rss_now(meter)
        rpc.cursor = got[2]
        # 3. Append cost on the loaded file: a few turns, each one user, one assistant entry.
        walls, added, cpus = [], [], []
        size0 = os.path.getsize(path)
        last = meter.cpu_ms()
        for i in range(5):
            first = len(srv.requests)
            _, t_send = rpc.send({"type": "prompt", "message": f"Append turn {i}: say hello."})
            end = rpc.wait_for(lambda r: r.get("type") == "agent_end", ctx.opts.history_timeout, start=rpc.cursor)
            if end is None:
                return {"failure": f"append turn {i}: no agent_end"}
            rpc.cursor = end[2]
            cpu = meter.cpu_ms()
            walls.append(ms(t_send, end[0]))
            cpus.append(cpu - last)
            last = cpu
            mine = srv.requests[first:]
            if mine and mine[-1].t_end:
                added.append(ms(mine[-1].t_end, end[0]))
        s["append_turn_wall_ms"], s["append_turn_cpu_ms"] = stats.median(walls[1:]), stats.median(cpus[1:])
        # Every turn, for runners that judge the first (cold) turn apart from the rest (bench/k8s).
        s["append_turn_walls_ms"], s["append_turn_cpus_ms"] = walls, cpus
        if added:
            s["append_harness_added_ms"] = stats.median(added[1:])
        s["append_request_bytes"] = srv.requests[-1].bytes
        s["append_file_growth_bytes"] = os.path.getsize(path) - size0
        s["rpc_peak_rss_mib"] = meter.max_proc_rss_mib()
        rpc.close()
    finally:
        if rpc.proc.poll() is None:
            rpc.close(grace=1)
        meter.close()
    # 2. Resume: a fresh process in a new empty session, then switch_session to the big file.
    meter = cgroup.new_meter()
    rpc = procio.Rpc([arm.binary, "--mode", "rpc", "--provider", "bench", "--model", MODEL, "--session-dir", h.sessions], h.env, ctx.project, meter)
    try:
        rpc.send({"type": "get_state"})
        got = rpc.wait_for(lambda r: r.get("type") == "response" and r.get("command") == "get_state", 60)
        if got is None:
            return {"failure": "no get_state response before resume"}
        rpc.cursor = got[2]
        base_cpu = meter.cpu_ms()
        rid, t0 = rpc.send({"type": "switch_session", "sessionPath": path})
        done = rpc.wait_for(lambda r: r.get("id") == rid, ctx.opts.history_timeout, start=rpc.cursor)
        if done is None:
            return {"failure": "switch_session never answered"}
        if not done[1].get("success"):
            return {"failure": f"switch_session failed: {str(done[1].get('error'))[:200]}"}
        s["resume_rpc_ms"] = ms(t0, done[0])
        s["resume_rpc_cpu_ms"] = meter.cpu_ms() - base_cpu
        s["resume_rpc_peak_rss_mib"] = meter.max_proc_rss_mib()
        rpc.close()
    finally:
        if rpc.proc.poll() is None:
            rpc.close(grace=1)
        meter.close()
    return s


def one_resume_tui(ctx, arm, case, path, facts, mode="fullscreen"):
    """Start a fresh TUI whose session directory holds the big session, type /resume and pick it."""
    h = ctx.harness(arm, f"resume-{case}-{mode}", settings_extra={"tuiMode": mode}, **BIG)
    session_dir = os.path.join(h.sessions, "tui-resume")
    os.makedirs(session_dir, exist_ok=True)
    target = os.path.join(session_dir, os.path.basename(path))
    if not os.path.exists(target):
        os.link(path, target) if os.stat(path).st_dev == os.stat(session_dir).st_dev else __import__("shutil").copy(path, target)
    ctx.server.reset(policy=scripted_policy([Step(text=REPLY)]))
    meter = cgroup.new_meter()
    term = procio.Pty([arm.binary, "--provider", "bench", "--model", MODEL, "--session-dir", session_dir], h.env, ctx.project, meter)
    s = {}
    try:
        if settle(term, 0, 0.8, 60) is None:
            return {"failure": "empty session never settled"}
        mark = len(term.chunks)
        term.type("/resume")
        time.sleep(0.4)
        term.type("\r")
        if term.wait_text("Resume Session", 60, since=mark) is None:
            return {"failure": "/resume selector never appeared"}
        time.sleep(2.0)
        mark = len(term.chunks)
        cpu0 = meter.cpu_ms()
        t0 = now_ns()
        term.type("\r")
        t_done = term.wait_text("Resumed session", ctx.opts.history_timeout, since=mark)
        if t_done is None:
            return {"failure": "the status line `Resumed session` never appeared"}
        s["resume_tui_ms"] = ms(t0, t_done)
        got = settle(term, mark, 1.5, ctx.opts.history_timeout)
        s["resume_tui_settled_ms"] = ms(t0, got[1]) if got else None
        s["resume_tui_cpu_ms"] = meter.cpu_ms() - cpu0
        s["resume_tui_bytes"] = sum(len(c) for _, c in term.chunks[mark:])
        s["resume_tui_peak_rss_mib"] = meter.max_proc_rss_mib()
        return s
    finally:
        term.close()
        meter.close()


PHASES = {"tui": one_tui, "rpc": one_rpc, "resume-tui": one_resume_tui}
MODE_PHASES = ("tui", "resume-tui")  # phases whose result depends on the TUI mode


def run(opts):
    """Generate the sessions, run every phase for every arm and case, interleaving arms, and write raw.json."""
    arm_list = [a for a in armlib.select(armlib.load(), opts.arms) if a.family in ("pig", "pi")]
    result = {"kind": "long-history", "options": {"runs": opts.runs, "cases": opts.cases, "seed": opts.seed, "modes": opts.modes, "history_timeout": opts.history_timeout, "phases": opts.phases},
              "samples": {}, "failures": {}, "sessions": {}, "egress": dict(egress.describe(), violations=[])}
    with armlib.Workspace() as ws:
        ws.install_all(arm_list)
        result["env"] = envinfo.collect(arm_list, seed=opts.seed)
        runnable = [a for a in arm_list if a.binary]
        cache = os.environ.get("PIGBENCH_HISTORY_CACHE") or os.path.join(armlib.REPO, "tmp", "bench", "history")
        from .overhead import Ctx
        # Outside the checkout: project resources and an AGENTS.md above the working directory would raise a trust dialog and change the prompt.
        project = os.path.join(tempfile.gettempdir(), "pigbench-history-project")
        os.makedirs(project, exist_ok=True)
        with MockServer() as server:
            ctx = Ctx(ws.work, server, opts)
            ctx.project = project
            for case in opts.cases:
                spath, facts = session_for(cache, case, opts.seed, project)
                result["sessions"][case] = facts
                print(f"bench: session {case}: {facts['entries']} entries, {facts['bytes'] / 1e6:.1f} MB, {facts['history_tokens'] / 1e6:.2f}M tokens, context {facts['context_tokens'] / 1e6:.2f}M", flush=True)
                for phase, mode in [(p, m) for p in opts.phases for m in (opts.modes if p in MODE_PHASES else [None])]:
                    for rep in range(opts.runs):
                        shift = rep % len(runnable)
                        for a in runnable[shift:] + runnable[:shift]:
                            work_copy = os.path.join(ws.work, f"{a.id}-{case}-{phase}-{mode}-{rep}.jsonl")
                            import shutil
                            shutil.copy(spath, work_copy)  # appends must not change the cached file
                            t0 = time.monotonic()
                            sample = PHASES[phase](ctx, a, case, work_copy, facts, mode) if mode else PHASES[phase](ctx, a, case, work_copy, facts)
                            os.unlink(work_copy)
                            left = sorted(egress.take())
                            if left:
                                result["egress"]["violations"].append({"arm": a.id, "phase": phase, "remotes": left})
                                sample = {"failure": f"egress: {', '.join(left)}"}
                            key = f"{case}/{phase}" + (f"/{mode}" if mode else "")
                            label = f"{key:24} {a.id:10} rep {rep + 1}/{opts.runs}"
                            if "failure" in sample:
                                print(f"bench: {label} FAILED: {sample['failure']}", flush=True)
                                result["failures"].setdefault(key, {}).setdefault(a.id, []).append(sample["failure"])
                                continue
                            print(f"bench: {label} {time.monotonic() - t0:.1f}s", flush=True)
                            result["samples"].setdefault(key, {}).setdefault(a.id, []).append(sample)
                            checkpoint.save(result, opts.out)
    result["ok"] = not result["failures"] and not result["egress"]["violations"]
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    checkpoint.clear(opts.out)
    return result
