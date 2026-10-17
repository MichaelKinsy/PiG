# SPDX-License-Identifier: MIT
"""bench/k8s workload: the long-history bench as one k8sbench seed and one k8sbench sample per process.

bench/k8s runs a profile's targets interleaved in one pod, each sample a fresh process, and reads one JSON result line per
sample (docs/site/docs/benchmarking.md, "Profiles"). This module adapts longhistory's measurement to that contract
without changing it: `generate` builds the case's session file once (longhistory.session_for, the same bytes for a seed
as `make bench-longhistory` but for the header's cwd, which is this module's PROJECT), and `sample` runs longhistory's phases for one agent on a copy of that file and prints the
line. Sizes are history tokens: 1000000 is case 1m, 3000000 is case 3m.

The line's durable-bench fields are the RPC phase's: `open` is the RPC open (spawn to the first get_state answer),
`turn[]` the wall time of each append turn on the loaded session (so k8sbench's cold is open plus the first turn and its
warm the median of the rest), `openCpu`/`turnCpu[]` their process CPU, and `peakRss` the open's peak RSS in MiB. The
other phases' measures are flat camelCase fields that the longhistory profile declares as metrics.

Nothing here reaches the network: the model is longhistory's local mock, and a phase whose processes opened a connection
to anything but the mock fails the sample.

`overhead-seed` and `overhead-sample` adapt the overhead bench (pigbench overhead) the same way, for comparing PiG with
other agents on one node. A size is a number of tool rounds N. One sample runs the profile's scenarios in order, each a
fresh process against the local mock: `version` (exec to exit), `print` (one prompt, one reply), `print-tools` (one
prompt, N rounds of one tool call, then the reply), and where the agent has them `rpc` (ready, then N prompts of one
tool call each) and `pty` (the interactive TUI: first frame, settled, one prompt). The line's durable-bench fields are
print-tools', which every agent has: `open` is spawn to the first model request, `turn[]` the N gaps between a reply's
end and the agent's next request (what the harness adds per turn, its tool run included), `openCpu`/`turnCpu[]` the
process tree's CPU to the first request and between requests, and `peakRss` its peak RSS in MiB. The other scenarios'
measures are flat camelCase fields that the overhead profiles declare as metrics. The seed writes no fixture: its
fingerprint is the digest of the workload's definition (scenarios, rounds, prompt, file, reply, model), so every
target of a session provably ran the same workload.
"""
import hashlib
import json
import os
import shutil
import sys
import tempfile
import time
import types

from . import egress, longhistory
from .mock import MODEL, REPLY, MockServer

SIZES = {1_000_000: "1m", 3_000_000: "3m"}

# The project directory both generate and sample use: the session header records it, and the agents open the session in it.
PROJECT = os.path.join(tempfile.gettempdir(), "pigbench-history-project")

# The project directory of `make bench-longhistory` on a host whose TMPDIR is /tmp. The fingerprint is the digest of the
# session with this cwd in its header, so it does not depend on where the session is written (a Job sets TMPDIR=/data/tmp)
# and equals that bench's digest. The only bytes that differ are the header's cwd; sample checks the file's own sha256.
FINGERPRINT_PROJECT = "/tmp/pigbench-history-project"

# longhistory field -> sample-line field, per phase. Only numbers are carried; a phase that did not measure one leaves it out.
FIELDS = {
    "rpc": {"open_rpc_steady_rss_mib": "steadyRss", "resume_rpc_ms": "resumeRpcMs", "resume_rpc_cpu_ms": "resumeRpcCpuMs",
            "resume_rpc_peak_rss_mib": "resumeRpcPeakRss", "append_harness_added_ms": "appendAddedMs", "append_file_growth_bytes": "appendGrowthBytes"},
    "tui": {"first_output_ms": "tuiFirstOutputMs", "open_settled_ms": "tuiSettledMs", "open_cpu_ms": "tuiOpenCpuMs", "open_peak_rss_mib": "tuiPeakRss",
            "open_bytes": "tuiOpenBytes", "key_idle_ms_p50": "keyIdleMs", "key_streaming_ms_p50": "keyStreamingMs",
            "frame_interval_ms_p50": "frameIntervalMs", "frame_cpu_ms": "frameCpuMs", "stream_cpu_ms": "streamCpuMs",
            "resize_w100_ms": "resizeWidthMs", "resize_w100_cpu_ms": "resizeWidthCpuMs", "resize_w100_bytes": "resizeWidthBytes",
            "resize_h30_ms": "resizeHeightMs", "resize_h30_cpu_ms": "resizeHeightCpuMs", "resize_h30_bytes": "resizeHeightBytes"},
    "resume-tui": {"resume_tui_ms": "resumeTuiMs", "resume_tui_settled_ms": "resumeTuiSettledMs", "resume_tui_cpu_ms": "resumeTuiCpuMs",
                   "resume_tui_peak_rss_mib": "resumeTuiPeakRss"},
}


def case_of(tokens):
    if tokens not in SIZES:
        raise SystemExit(f"pigbench k8s: size {tokens} is not one of {', '.join(str(n) for n in SIZES)} history tokens")
    return SIZES[tokens]


def session_fingerprint(path):
    """The first 16 hex digits of the session's sha256 with FINGERPRINT_PROJECT as the header's cwd (Writer.header's serialization)."""
    with open(path, "rb") as f:
        head, sep, rest = f.read().partition(b"\n")
    header = json.loads(head)
    header["cwd"] = FINGERPRINT_PROJECT
    return hashlib.sha256(json.dumps(header, separators=(",", ":")).encode() + sep + rest).hexdigest()[:16]


def generate(tokens, seed, directory):
    """Build the case's session in directory and write k8s-<tokens>.json with seedMs, fingerprint, the file's sha256 and bytes; return the meta."""
    os.makedirs(PROJECT, exist_ok=True)
    t0 = time.perf_counter()
    path, facts = longhistory.session_for(directory, case_of(tokens), seed, PROJECT)
    meta = {"seedMs": round((time.perf_counter() - t0) * 1000, 1), "fingerprint": session_fingerprint(path), "sha256": facts["sha256"], "bytes": facts["bytes"], "session": path,
            "entries": facts["entries"], "historyTokens": facts["history_tokens"], "contextTokens": facts["context_tokens"], "compactions": facts["compactions"]}
    with open(os.path.join(directory, f"k8s-{tokens}.json"), "w") as f:
        json.dump(meta, f)
    return meta


def sample_line(target, tokens, index, fingerprint, results):
    """The k8sbench result line from the phases' results ({phase: longhistory sample dict})."""
    rpc = results["rpc"]
    line = {"t": "sample", "target": target, "turns": tokens, "sample": index, "fingerprint": fingerprint,
            "open": rpc["open_rpc_ms"], "turn": rpc["append_turn_walls_ms"], "openCpu": rpc["open_rpc_cpu_ms"], "turnCpu": rpc["append_turn_cpus_ms"]}
    if isinstance(rpc.get("open_rpc_peak_rss_mib"), (int, float)):
        line["peakRss"] = rpc["open_rpc_peak_rss_mib"]
    for phase, fields in FIELDS.items():
        got = results.get(phase, {})
        for src, dst in fields.items():
            v = got.get(src)
            if isinstance(v, (int, float)) and not isinstance(v, bool):
                line[dst] = v
    return line


def sample(target, family, binary, tokens, seed, directory, index, phases, mode, timeout):
    """Run the phases for one agent on copies of the generated session and return the result line."""
    if family not in ("pig", "pi"):
        raise SystemExit(f"pigbench k8s: family {family!r} is not pig or pi")
    if "rpc" not in phases:
        raise SystemExit("pigbench k8s: the rpc phase gives the line's open and turn[]; it cannot be left out")
    with open(os.path.join(directory, f"k8s-{tokens}.json")) as f:
        meta = json.load(f)
    path = meta["session"]
    with open(path, "rb") as f:
        digest = hashlib.sha256(f.read()).hexdigest()
    if digest != meta["sha256"]:
        raise SystemExit(f"pigbench k8s: {path} changed since it was generated (sha256 {digest[:16]}, generated {meta['sha256'][:16]})")
    facts = {"case": case_of(tokens), "seed": seed, "cwd": PROJECT, "bytes": meta["bytes"], "entries": meta["entries"], "history_tokens": meta["historyTokens"],
             "context_tokens": meta["contextTokens"], "compactions": meta["compactions"], "sha256": digest}
    arm = types.SimpleNamespace(id=target, family=family, binary=binary, spec={})  # spec: arms.toml defaults (agent dir, env, rpc dialect)
    opts = types.SimpleNamespace(history_timeout=timeout, timeout=timeout)
    from .overhead import Ctx
    work = tempfile.mkdtemp(prefix="pigbench-k8s-")
    results = {}
    try:
        with MockServer() as server:
            ctx = Ctx(work, server, opts)
            ctx.project = PROJECT
            egress.take()
            for phase in phases:
                copy = os.path.join(work, f"{phase}.jsonl")
                shutil.copy(path, copy)  # appends must not change the generated file
                fn = longhistory.PHASES[phase]
                got = fn(ctx, arm, facts["case"], copy, facts, mode) if phase in longhistory.MODE_PHASES else fn(ctx, arm, facts["case"], copy, facts)
                os.unlink(copy)
                left = sorted(egress.take())
                if left:
                    raise SystemExit(f"pigbench k8s: {phase}: connections outside the mock: {', '.join(left)}")
                if "failure" in got:
                    raise SystemExit(f"pigbench k8s: {phase}: {got['failure']}")
                results[phase] = got
    finally:
        shutil.rmtree(work, ignore_errors=True)
    return sample_line(target, tokens, index, meta["fingerprint"], results)


# The overhead scenarios a profile may run, in the order a sample runs them; print-tools gives the line and always runs.
OVERHEAD_SCENARIOS = ["version", "print", "print-tools", "rpc", "pty"]

# Agent families the overhead sample can configure against the mock (pigbench.harness).
OVERHEAD_FAMILIES = ["pig", "pi", "codex"]

# overhead result field -> sample-line field, per scenario.
OVERHEAD_FIELDS = {
    "version": {"wall_ms": "versionMs", "cpu_ms": "versionCpuMs", "peak_rss_mib": "versionRss"},
    "print": {"wall_ms": "printMs", "cpu_ms": "printCpuMs", "first_request_ms": "printFirstRequestMs", "teardown_ms": "printTeardownMs", "peak_rss_mib": "printRss"},
    "print-tools": {"wall_ms": "toolsMs", "cpu_ms": "toolsCpuMs", "teardown_ms": "toolsTeardownMs", "turn_gap_ms": "toolsGapMs", "turn_cpu_ms": "toolsTurnCpuMs"},
    "rpc": {"ready_ms": "rpcReadyMs", "ready_cpu_ms": "rpcReadyCpuMs", "ready_rss_mib": "rpcReadyRss", "turn_wall_ms": "rpcTurnMs", "turn_cpu_ms": "rpcTurnCpuMs",
            "turn_harness_added_ms": "rpcTurnAddedMs", "teardown_ms": "rpcTeardownMs", "peak_rss_mib": "rpcPeakRss"},
    "pty": {"first_frame_ms": "ptyFirstFrameMs", "ready_ms": "ptyReadyMs", "ready_cpu_ms": "ptyReadyCpuMs", "ready_rss_mib": "ptyReadyRss",
            "input_to_request_ms": "ptyInputMs", "input_to_reply_visible_ms": "ptyReplyMs", "peak_rss_mib": "ptyPeakRss"},
}


def overhead_scenarios(names):
    """The scenarios to run, in OVERHEAD_SCENARIOS order; print-tools is always one."""
    unknown = sorted(set(names) - set(OVERHEAD_SCENARIOS))
    if unknown:
        raise SystemExit(f"pigbench k8s: unknown overhead scenarios {', '.join(unknown)}; use {', '.join(OVERHEAD_SCENARIOS)}")
    if "print-tools" not in names:
        raise SystemExit("pigbench k8s: the print-tools scenario gives the line's open and turn[]; it cannot be left out")
    return [n for n in OVERHEAD_SCENARIOS if n in names]


def overhead_fingerprint(rounds, scenarios):
    """The first 16 hex digits of the sha256 of the workload's definition."""
    from .overhead import PROMPT, README
    spec = {"scenarios": overhead_scenarios(scenarios), "rounds": rounds, "prompt": PROMPT, "readme": README, "reply": REPLY, "model": MODEL}
    return hashlib.sha256(json.dumps(spec, sort_keys=True, separators=(",", ":")).encode()).hexdigest()[:16]


def overhead_seed(rounds, scenarios, directory):
    """Write overhead-<rounds>.json with the workload's fingerprint; there is no fixture to build."""
    if rounds < 1:
        raise SystemExit(f"pigbench k8s: {rounds} tool rounds; a size is at least one")
    os.makedirs(directory, exist_ok=True)
    meta = {"seedMs": 0.0, "fingerprint": overhead_fingerprint(rounds, scenarios), "bytes": 0, "rounds": rounds, "scenarios": overhead_scenarios(scenarios)}
    with open(os.path.join(directory, f"overhead-{rounds}.json"), "w") as f:
        json.dump(meta, f)
    return meta


def number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def overhead_line(target, rounds, index, fingerprint, results):
    """The k8sbench result line from the scenarios' results ({scenario: overhead sample dict})."""
    tools = results["print-tools"]
    series = tools.get("_series") or {}
    gaps, cpus = series.get("gap_ms") or [], series.get("cpu_turn_ms") or []
    if len(gaps) != rounds or len(cpus) != rounds or not number(tools.get("first_request_ms")) or not number(tools.get("first_request_cpu_ms")):
        raise SystemExit(f"pigbench k8s: print-tools measured {len(gaps)} turn gaps and {len(cpus)} turn CPU times for {rounds} tool rounds")
    line = {"t": "sample", "target": target, "turns": rounds, "sample": index, "fingerprint": fingerprint,
            "open": tools["first_request_ms"], "turn": gaps, "openCpu": tools["first_request_cpu_ms"], "turnCpu": cpus}
    if number(tools.get("peak_rss_mib")):
        line["peakRss"] = tools["peak_rss_mib"]
    for scenario, fields in OVERHEAD_FIELDS.items():
        got = results.get(scenario, {})
        for src, dst in fields.items():
            if number(got.get(src)):
                line[dst] = got[src]
    return line


def overhead_arm(target, family, binary, arm_id):
    """The agent to measure: an arms.toml arm when arm_id names one (its harness settings, such as another agent
    directory variable, come with it), else a plain agent of the family."""
    if family not in OVERHEAD_FAMILIES:
        raise SystemExit(f"pigbench k8s: family {family!r} is not one of {', '.join(OVERHEAD_FAMILIES)}")
    if not arm_id:
        return types.SimpleNamespace(id=target, family=family, binary=binary, no_extensions=False, spec={})
    from . import arms as armlib
    known = armlib.load()
    if arm_id not in known:
        raise SystemExit(f"pigbench k8s: arms.toml has no arm {arm_id!r}")
    arm = known[arm_id]
    if arm.family != family:
        raise SystemExit(f"pigbench k8s: arm {arm_id} is of family {arm.family}, not {family}")
    arm.binary = binary
    return arm


def overhead_sample(target, family, binary, rounds, scenarios, directory, index, timeout, arm_id=None):
    """Run the scenarios for one agent and return the result line."""
    from . import overhead
    arm = overhead_arm(target, family, binary, arm_id)
    scenarios = overhead_scenarios(scenarios)
    with open(os.path.join(directory, f"overhead-{rounds}.json")) as f:
        meta = json.load(f)
    fingerprint = overhead_fingerprint(rounds, scenarios)
    if meta.get("fingerprint") != fingerprint:
        raise SystemExit(f"pigbench k8s: the seed defined workload {meta.get('fingerprint')}, this sample's scenarios and rounds are {fingerprint}")
    opts = types.SimpleNamespace(timeout=timeout, rpc_turns=rounds)
    work = tempfile.mkdtemp(prefix="pigbench-k8s-")
    results = {}
    try:
        with MockServer() as server:
            ctx = overhead.Ctx(work, server, opts)
            for scenario in scenarios:
                if not ctx.supports(arm, scenario):
                    raise SystemExit(f"pigbench k8s: {family} has no {scenario} scenario; leave it out of the profile")
            egress.take()
            for scenario in scenarios:
                if scenario == "version":
                    got = overhead.one_version(ctx, arm)
                elif scenario == "print":
                    got = overhead.one_print(ctx, arm)
                elif scenario == "print-tools":
                    got = overhead.one_print(ctx, arm, tools=rounds, scenario="print-tools")
                elif scenario == "rpc":
                    got = overhead.one_rpc(ctx, arm)
                else:
                    got = overhead.one_pty(ctx, arm)
                left = sorted(egress.take())
                if left:
                    raise SystemExit(f"pigbench k8s: {scenario}: connections outside the mock: {', '.join(left)}")
                if "failure" in got:
                    raise SystemExit(f"pigbench k8s: {scenario}: {got['failure']}")
                results[scenario] = got
    finally:
        shutil.rmtree(work, ignore_errors=True)
    return overhead_line(target, rounds, index, fingerprint, results)


def add_parser(sub):
    p = sub.add_parser("k8s", help="the long-history bench as bench/k8s seed and sample commands (one line per process)")
    k = p.add_subparsers(dest="k8s_cmd", required=True)
    g = k.add_parser("generate", help="build the session for a size in DIR and write DIR/k8s-<tokens>.json")
    g.add_argument("--tokens", type=int, required=True)
    g.add_argument("--seed", type=int, required=True)
    g.add_argument("--dir", required=True)
    s = k.add_parser("sample", help="measure one agent on a copy of the generated session; print one k8sbench result line")
    s.add_argument("--target", required=True, help="the profile's target name, echoed in the line")
    s.add_argument("--family", required=True, choices=["pig", "pi"])
    s.add_argument("--binary", required=True, help="the agent's executable")
    s.add_argument("--tokens", type=int, required=True)
    s.add_argument("--seed", type=int, required=True)
    s.add_argument("--dir", required=True)
    s.add_argument("--sample", type=int, default=0)
    s.add_argument("--phases", nargs="*", default=["rpc", "tui", "resume-tui"])
    s.add_argument("--mode", default="fullscreen", help="tuiMode for the TUI phases")
    s.add_argument("--timeout", type=float, default=300, help="per-step timeout, seconds")
    og = k.add_parser("overhead-seed", help="write DIR/overhead-<rounds>.json with the fingerprint of the overhead workload")
    og.add_argument("--rounds", type=int, required=True, help="tool rounds of print-tools and turns of rpc (the size)")
    og.add_argument("--scenarios", default="version,print,print-tools", help="comma-separated, from " + ",".join(OVERHEAD_SCENARIOS))
    og.add_argument("--dir", required=True)
    os_ = k.add_parser("overhead-sample", help="run the overhead scenarios for one agent; print one k8sbench result line")
    os_.add_argument("--target", required=True, help="the profile's target name, echoed in the line")
    os_.add_argument("--family", required=True, choices=OVERHEAD_FAMILIES)
    os_.add_argument("--binary", required=True, help="the agent's executable")
    os_.add_argument("--arm", help="the arms.toml arm the agent is, for its harness settings (default: a plain agent of the family)")
    os_.add_argument("--rounds", type=int, required=True)
    os_.add_argument("--scenarios", default="version,print,print-tools")
    os_.add_argument("--dir", required=True)
    os_.add_argument("--sample", type=int, default=0)
    os_.add_argument("--timeout", type=float, default=120, help="per-scenario timeout, seconds")


def main(args):
    if args.k8s_cmd == "generate":
        print(json.dumps(generate(args.tokens, args.seed, args.dir)))
        return 0
    if args.k8s_cmd == "overhead-seed":
        print(json.dumps(overhead_seed(args.rounds, args.scenarios.split(","), args.dir)))
        return 0
    # Progress goes to stderr; stdout carries only the result line.
    real = sys.stdout
    sys.stdout = sys.stderr
    try:
        if args.k8s_cmd == "overhead-sample":
            line = overhead_sample(args.target, args.family, args.binary, args.rounds, args.scenarios.split(","), args.dir, args.sample, args.timeout, args.arm)
        else:
            line = sample(args.target, args.family, args.binary, args.tokens, args.seed, args.dir, args.sample, args.phases, args.mode, args.timeout)
    finally:
        sys.stdout = real
    print(json.dumps(line), flush=True)
    return 0
