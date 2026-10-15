#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Render, measure and ratchet-check the performance scoreboard.

  scoreboard.py render [--check]     write docs/performance/scoreboard.md (--check: fail if it differs)
  scoreboard.py measure              run the gate tests, write scoreboard-measured.json, render
  scoreboard.py ratchet --base REF   fail if a ceiling rose or disappeared since REF, unless a commit in REF..HEAD says
                                     `Perf-Ratchet-Raise: <id> <reason>` or `Perf-Ratchet-Retire: <id> <reason>`

Every run validates the registry: each named test function exists, each ratchet gate has a ceiling, and each ceiling belongs to exactly one metric.
"""
import argparse
import json
import os
import pathlib
import re
import subprocess
import sys
import tomllib

ROOT = pathlib.Path(__file__).resolve().parents[2]
REGISTRY = ROOT / "automation/perf/scoreboard.toml"
RATCHET = ROOT / "automation/perf/ratchet.toml"
MEASURED = ROOT / "automation/perf/scoreboard-measured.json"
REPORT = ROOT / "docs/performance/scoreboard.md"
PERFGATE_LINE = re.compile(r"perfgate: (\S+)=(-?[0-9.]+) ceiling=")
GATE_KINDS = ("ratchet", "test", "branch", "missing", "measured")


class RegistryError(Exception):
    pass


def load_toml(path):
    with open(path, "rb") as handle:
        return tomllib.load(handle)


def test_exists(root, ref):
    """ref is '<package dir>:<TestName>'; the function must be declared in a _test.go file of that directory."""
    pkg, _, name = ref.partition(":")
    directory = root / pkg
    if not name or not directory.is_dir():
        return False
    declaration = re.compile(r"^func " + re.escape(name) + r"\(", re.M)
    return any(declaration.search(path.read_text(encoding="utf-8")) for path in directory.glob("*_test.go"))


def gate_kind(gate):
    kinds = [kind for kind in GATE_KINDS if kind in gate]
    if len(kinds) != 1:
        raise RegistryError(f"gate {gate} must have exactly one of {', '.join(GATE_KINDS)}")
    return kinds[0]


def validate(registry, ratchet, root=ROOT):
    """Return a list of problems; an empty list means the registry is consistent with the tree."""
    problems = []
    ceilings = ratchet.get("ceilings", {})
    owners = {}
    seen = set()
    for metric in registry.get("metric", []):
        for field in ("id", "area", "title", "impact", "owner", "target", "basis", "gates"):
            if field not in metric:
                problems.append(f"metric {metric.get('id', '?')}: missing {field}")
        if metric.get("id") in seen:
            problems.append(f"metric {metric['id']}: duplicate id")
        seen.add(metric.get("id"))
        for gate in metric.get("gates", []):
            try:
                kind = gate_kind(gate)
            except RegistryError as error:
                problems.append(f"metric {metric.get('id')}: {error}")
                continue
            if kind == "ratchet":
                if gate["ratchet"] not in ceilings:
                    problems.append(f"metric {metric['id']}: ratchet gate {gate['ratchet']} has no ceiling in {RATCHET.name}")
                owners.setdefault(gate["ratchet"], set()).add(metric["id"])
            elif kind == "test" and not test_exists(root, gate["test"]):
                problems.append(f"metric {metric['id']}: test {gate['test']} does not exist")
            elif kind == "branch" and not gate.get("path"):
                problems.append(f"metric {metric['id']}: branch gate {gate['branch']} names no path")
            elif kind == "missing" and not gate.get("route"):
                problems.append(f"metric {metric['id']}: missing gate '{gate['missing']}' is not routed to a lane")
    for gate_id in sorted(ceilings):
        if gate_id not in owners:
            problems.append(f"ceiling {gate_id} belongs to no metric")
    for resource in registry.get("resource", []):
        for ref in resource.get("tests", []):
            if not test_exists(root, ref):
                problems.append(f"resource {resource['name']}: test {ref} does not exist")
        for gate_id in resource.get("gates", []):
            if gate_id not in ceilings:
                problems.append(f"resource {resource['name']}: gate {gate_id} has no ceiling")
        if resource.get("missing") and not resource.get("route"):
            problems.append(f"resource {resource['name']}: missing gate is not routed")
    for practice in registry.get("practice", []):
        if practice.get("status") not in ("present", "partial", "missing"):
            problems.append(f"practice {practice.get('name')}: status must be present, partial or missing")
        if practice.get("status") == "present":
            for ref in re.findall(r"[\w./-]+:Test\w+", practice.get("where", "")):
                if not test_exists(root, ref):
                    problems.append(f"practice {practice['name']}: test {ref} does not exist")
    technique_ids = {technique["id"] for technique in registry.get("technique", [])}
    used = set()
    plans = registry.get("plan", [])
    if len(plans) > 10:
        problems.append(f"plan: {len(plans)} entries; the ranked plan holds the top 10")
    for rank, item in enumerate(plans, 1):
        for field in ("gap", "plan", "gain", "route"):
            if not item.get(field):
                problems.append(f"plan {rank}: missing {field}")
        for technique in item.get("techniques", []):
            if technique not in technique_ids:
                problems.append(f"plan {rank}: unknown technique {technique}")
            used.add(technique)
    for technique in registry.get("technique", []):
        placement = technique.get("placement", "")
        ranks = {int(rank) for rank in re.findall(r"plan (\d+)", placement)}
        if not placement:
            problems.append(f"technique {technique['id']}: no placement")
        for rank in ranks:
            if rank > len(plans) or technique["id"] not in plans[rank - 1].get("techniques", []):
                problems.append(f"technique {technique['id']}: placed in plan {rank}, which does not list it")
        if technique["id"] in used and not ranks:
            problems.append(f"technique {technique['id']}: listed by a plan but placed in none")
    for entry in registry.get("measure", []):
        if not (root / entry["pkg"]).is_dir():
            problems.append(f"measure: package {entry['pkg']} does not exist")
    return problems


def human(value, gate_id):
    """Format a measured count with the unit its id implies."""
    if value is None:
        return "not measured"
    if gate_id.endswith("_exponent"):
        return f"{value:.2f}"
    if gate_id.endswith("bytes") or "bytes_" in gate_id or gate_id.endswith(".bytes"):
        if abs(value) >= 1_000_000:
            return f"{value / 1_000_000:.2f} MB"
        if abs(value) >= 10_000:
            return f"{value / 1000:.1f} KB"
        return f"{value:.0f} B"
    if value == int(value):
        return f"{int(value):,}"
    return f"{value:,.1f}"


def gate_cell(gate, measured, ceilings):
    kind = gate_kind(gate)
    if kind == "ratchet":
        gate_id = gate["ratchet"]
        return f"`{gate_id}` {human(measured.get(gate_id), gate_id)} (ceiling {human(ceilings[gate_id], gate_id)})"
    if kind == "measured":
        gate_id = gate["measured"]
        return f"`{gate_id}` {human(measured.get(gate_id), gate_id)}"
    if kind == "test":
        return f"`{gate['test']}`"
    if kind == "branch":
        return f"BRANCH of lane {gate['branch']}: `{gate['path']}`"
    return f"**MISSING**: {gate['missing']} (route: {gate['route']})"


def status_of(metric):
    kinds = [gate_kind(gate) for gate in metric["gates"]]
    if "missing" in kinds:
        return "MISSING"
    if all(kind == "branch" for kind in kinds):
        return "BRANCH"
    return "GATED"


def render(registry, ratchet, measured_doc):
    ceilings = ratchet.get("ceilings", {})
    measured = measured_doc.get("values", {}) if measured_doc else {}
    metrics = registry.get("metric", [])
    lines = [
        "<!-- Generated by `make perf-scoreboard` from automation/perf/scoreboard.toml, ratchet.toml and scoreboard-measured.json. Do not edit. -->",
        "",
        "# Performance scoreboard",
        "",
        "Every hot path and resource metric of PiG, with its current count, the ratchet gate that guards it, a target and its basis, and the owning lane. Pi-identical output is the wall: a change that lowers a count must keep every parity test green.",
        "",
        "Counts do not depend on machine speed (allocations, bytes, GC cycles, goroutines, descriptors, children, sizes). Time is measured only on the benchmark cluster with the exact-CV gate and is not recorded here. Ceilings live in `automation/perf/ratchet.toml` and only move down (`make perf-ratchet-check`).",
        "",
    ]
    if measured_doc:
        lines += [f"Measured at `{measured_doc.get('rev', '?')}` with {measured_doc.get('go', '?')} on {measured_doc.get('platform', '?')} by `make perf-scoreboard-measure`.", ""]
    else:
        lines += ["No measurement recorded: run `make perf-scoreboard-measure`.", ""]
    gate_total = sum(len(metric["gates"]) for metric in metrics)
    missing_total = sum(1 for metric in metrics for gate in metric["gates"] if gate_kind(gate) == "missing")
    branch_total = sum(1 for metric in metrics for gate in metric["gates"] if gate_kind(gate) == "branch")
    lines += [
        f"{len(metrics)} metrics; {gate_total} gates: {gate_total - missing_total - branch_total} in this tree, {branch_total} on another lane's branch, {missing_total} missing.",
        "",
        "| Area | Metric | Status | Impact | Owner | Target | Gates and current counts |",
        "|---|---|---|---:|---|---|---|",
    ]
    for metric in metrics:
        cells = "<br>".join(gate_cell(gate, measured, ceilings) for gate in metric["gates"])
        target = f"{metric['target']}. *Basis:* {metric['basis']}"
        lines.append(f"| {metric['area']} | **{metric['id']}**: {metric['title']} | {status_of(metric)} | {metric['impact']} | {metric['owner']} | {target} | {cells} |")
    lines += ["", "## Missing gates", "", "| Metric | Missing gate | Route |", "|---|---|---|"]
    for metric in metrics:
        for gate in metric["gates"]:
            if gate_kind(gate) == "missing":
                lines.append(f"| {metric['id']} | {gate['missing']} | {gate['route']} |")
    lines += ["", "## Resources that can leak or grow", "", "| Resource | How it grows | Tests | Gates | Missing | Route |", "|---|---|---|---|---|---|"]
    for resource in registry.get("resource", []):
        tests = "<br>".join(f"`{ref}`" for ref in resource.get("tests", [])) or "none"
        gates = "<br>".join(f"`{gate_id}` {human(measured.get(gate_id), gate_id)}" for gate_id in resource.get("gates", [])) or "none"
        lines.append(f"| {resource['name']} | {resource['growth']} | {tests} | {gates} | {resource.get('missing') or 'none'} | {resource.get('route') or '-'} |")
    lines += ["", "## Enforcement practices", "", "| Practice | Status | Where |", "|---|---|---|"]
    for practice in registry.get("practice", []):
        lines.append(f"| {practice['name']} | {practice['status']} | {practice['where']} |")
    plan = registry.get("plan", [])
    if plan:
        lines += ["", "## Largest gaps to target, ranked by user impact", "", "| Rank | Gap | Plan | Expected gain | Techniques | Route |", "|---:|---|---|---|---|---|"]
        for rank, item in enumerate(plan, 1):
            techniques = ", ".join(item.get("techniques", [])) or "-"
            lines.append(f"| {rank} | {item['gap']} | {item['plan']} | {item['gain']} | {techniques} | {item['route']} |")
    techniques = registry.get("technique", [])
    if techniques:
        lines += ["", "## Techniques", "", "| ID | Technique | Placement |", "|---|---|---|"]
        for technique in techniques:
            lines.append(f"| {technique['id']} | {technique['name']} | {technique['placement']} |")
    protocol = registry.get("protocol")
    if protocol:
        lines += ["", "## Cross-language comparison method", "",
                  "Every comparison with another harness follows this protocol. Targets that cite another harness's self-reported numbers stand only until a same-machine run replaces them.", ""]
        lines += ["Harnesses: " + "; ".join(protocol["competitors"]) + ".", "", "Platforms: " + "; ".join(protocol["platforms"]) + ".", ""]
        lines += ["Endpoints:", ""] + [f"- {endpoint}" for endpoint in protocol["endpoints"]] + [""]
        lines += ["Scenarios:", ""] + [f"- {scenario}" for scenario in protocol["scenarios"]] + [""]
        lines += ["Rules:", ""] + [f"- {rule}" for rule in protocol["rules"]] + [""]
        lines += [f"Route: {protocol['route']}."]
    return "\n".join(lines) + "\n"


def inittrace(binary):
    """Sum this module's package initializers from GODEBUG=inittrace=1."""
    result = subprocess.run([binary, "--version"], env={"GODEBUG": "inittrace=1", "PATH": "/usr/bin:/bin", "HOME": str(pathlib.Path(binary).parent)},
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, timeout=60, check=True)
    line = re.compile(r"^init (\S+) @[\d.]+ ms, [\d.]+ ms clock, (\d+) bytes, (\d+) allocs$")
    total_bytes = total_allocs = 0
    for text in result.stderr.splitlines():
        match = line.match(text)
        if match and match.group(1).startswith("github.com/MichaelKinsy/PiG"):
            total_bytes += int(match.group(2))
            total_allocs += int(match.group(3))
    return {"startup.init.bytes": total_bytes, "startup.init.allocs": total_allocs}


def measure(registry, timeout):
    values = {}
    failures = []
    for entry in registry.get("measure", []):
        command = ["go", "test", "-count=1", "-v", "-timeout", f"{timeout}s", "-run", entry["run"]]
        if entry.get("tags"):
            command += ["-tags", entry["tags"]]
        command.append(entry["pkg"])
        print("+", " ".join(command), flush=True)
        result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=timeout + 120,
                                env={**os.environ, "PIG_KEEP_COUNT1": "1"})
        for match in PERFGATE_LINE.finditer(result.stdout):
            values[match.group(1)] = float(match.group(2))
        if result.returncode:
            failures.append(entry["pkg"])
            print(result.stdout[-4000:], file=sys.stderr)
    binary = ROOT / "tmp/perf-scoreboard/pig"
    binary.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/pig"], cwd=ROOT, check=True, timeout=timeout,
                   env={**os.environ, "CGO_ENABLED": "0"})
    values.update(inittrace(str(binary)))
    rev = subprocess.run(["git", "rev-parse", "--short=10", "HEAD"], cwd=ROOT, stdout=subprocess.PIPE, text=True).stdout.strip()
    # The measurement's own outputs do not make the measured tree dirty.
    dirty = subprocess.run(["git", "status", "--porcelain", "--untracked-files=no", "--", ".",
                            f":!{MEASURED.relative_to(ROOT)}", f":!{REPORT.relative_to(ROOT)}"],
                           cwd=ROOT, stdout=subprocess.PIPE, text=True).stdout.strip()
    goversion = subprocess.run(["go", "env", "GOVERSION", "GOOS", "GOARCH"], cwd=ROOT, stdout=subprocess.PIPE, text=True).stdout.split()
    document = {
        "rev": rev + ("+changes" if dirty else ""),
        "go": goversion[0] if goversion else "?",
        "platform": "/".join(goversion[1:3]) if len(goversion) >= 3 else "?",
        "values": dict(sorted(values.items())),
    }
    MEASURED.write_text(json.dumps(document, indent=1) + "\n")
    return failures


def ratchet_check(base, root=ROOT):
    """Return problems: ceilings raised or removed since base without a Perf-Ratchet-Raise/Retire trailer in base..HEAD."""
    # An unresolvable base (no upstream configured, a shallow clone, a typo) must fail: reading it as "no ceilings yet" would pass every raise.
    resolved = subprocess.run(["git", "rev-parse", "--verify", "--quiet", f"{base}^{{commit}}"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    if resolved.returncode:
        return [f"base {base} does not name a commit; set PERF_RATCHET_BASE to the ref the change is compared with"]
    old_text = subprocess.run(["git", "show", f"{base}:automation/perf/ratchet.toml"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    if old_text.returncode:
        return []  # The base predates the ratchet file: everything is new.
    old = tomllib.loads(old_text.stdout).get("ceilings", {})
    new = load_toml(root / "automation/perf/ratchet.toml").get("ceilings", {})
    log = subprocess.run(["git", "log", "--format=%B", f"{base}..HEAD"], cwd=root, stdout=subprocess.PIPE, text=True).stdout
    raised = set(re.findall(r"^Perf-Ratchet-Raise: (\S+) \S", log, re.M))
    retired = set(re.findall(r"^Perf-Ratchet-Retire: (\S+) \S", log, re.M))
    problems = []
    for gate_id, ceiling in sorted(old.items()):
        if gate_id not in new:
            if gate_id not in retired:
                problems.append(f"{gate_id}: ceiling removed (add 'Perf-Ratchet-Retire: {gate_id} <reason>' to the commit message)")
        elif new[gate_id] > ceiling and gate_id not in raised:
            problems.append(f"{gate_id}: ceiling raised {ceiling} -> {new[gate_id]} (add 'Perf-Ratchet-Raise: {gate_id} <reason>' with the profile)")
    return problems


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    render_parser = sub.add_parser("render")
    render_parser.add_argument("--check", action="store_true")
    measure_parser = sub.add_parser("measure")
    measure_parser.add_argument("--timeout", type=int, default=1800)
    ratchet_parser = sub.add_parser("ratchet")
    ratchet_parser.add_argument("--base", required=True)
    args = parser.parse_args(argv)

    if args.command == "ratchet":
        problems = ratchet_check(args.base)
        for problem in problems:
            print(f"perf-ratchet: {problem}", file=sys.stderr)
        return 1 if problems else 0

    registry, ratchet = load_toml(REGISTRY), load_toml(RATCHET)
    problems = validate(registry, ratchet)
    if problems:
        for problem in problems:
            print(f"perf-scoreboard: {problem}", file=sys.stderr)
        return 1
    failures = []
    if args.command == "measure":
        failures = measure(registry, args.timeout)
    measured = json.loads(MEASURED.read_text()) if MEASURED.exists() else None
    text = render(registry, ratchet, measured)
    if args.command == "render" and args.check:
        if not REPORT.exists() or REPORT.read_text() != text:
            print(f"perf-scoreboard: {REPORT.relative_to(ROOT)} is stale; run make perf-scoreboard", file=sys.stderr)
            return 1
        return 0
    REPORT.write_text(text)
    if failures:
        print(f"perf-scoreboard: gate tests failed in {', '.join(failures)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
