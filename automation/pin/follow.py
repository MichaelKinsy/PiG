#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Follow a new upstream Pi commit: oracle, mirror, inventories, ledgers, gap list, Porter tasks, READY lines.

    follow.py <ref> [--dry-run] [--run-porter] [--ready-log PATH]

The pipeline never changes the committed pin and never merges. It runs every command under the
PI_UPSTREAM_REF override, records each step's start, end and exit code in
tmp/pin-follow/<version>/state.json (a rerun resumes after the last passing step), and reports the minutes
from the upstream commit to the gap list (target 30) and to the Porter output (target 120). READY and FINDING lines go to
--ready-log, else PIG_PIN_FOLLOW_READY_LOG, else tmp/pin-follow/<version>/ready.log.
"""
from __future__ import annotations

import argparse
import dataclasses
import datetime as dt
import json
import os
import re
import signal
import subprocess
import sys
import tomllib
from collections import defaultdict
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402

TARGET_MINUTES = {"gaps": 30, "porter": 120}
MAX_ROWS = 25  # one Porter task closes at most this many rows; a 12-row group once ran past the one-hour task limit with no report
STATE_DIR = "tmp/pin-follow"
LANE = os.environ.get("PIG_LANE_SLICE", "pin-follow")
DECLARATION = r"export\s+(?:declare\s+)?(?:abstract\s+)?(?:const|let|var|type|interface|function\*?|async\s+function|class|enum)\s+%s\b"


@dataclasses.dataclass
class GapRow:
    id: str
    reason: str
    go: str = ""


@dataclasses.dataclass
class GapGroup:
    file: str
    rows: list[GapRow]


@dataclasses.dataclass
class DeltaFile:
    path: str
    change: str


@dataclasses.dataclass
class DeltaGroup:
    package: str
    files: list[DeltaFile]


@dataclasses.dataclass
class Task:
    kind: str
    scope: str
    rows: int

    @property
    def task(self) -> str:
        return f"{self.kind} {self.scope}"


def symbol_of(row_id: str) -> tuple[str, str, str]:
    """(package, module path or empty, top-level symbol) of an interface id such as pkg:ai/providers/compat-schema#Name::property:x."""
    head = row_id.removeprefix("pkg:").split("::", 1)[0]
    where, _, symbol = head.partition("#")
    package, _, module = where.partition("/")
    return package, module.rstrip("."), symbol


def locate(mirror: Path, package: str, module: str, symbol: str) -> str | None:
    base = mirror / "packages" / package / "src"
    if module and (base / f"{module}.ts").is_file():
        return f"packages/{package}/src/{module}.ts"
    if not base.is_dir():
        return None
    pattern = re.compile(DECLARATION % re.escape(symbol), re.M)
    for path in sorted(base.rglob("*.ts")):
        if pattern.search(path.read_text(encoding="utf-8", errors="replace")):
            return str(path.relative_to(mirror))
    return None


def group_gaps(tsv: str, mirror: Path) -> list[GapGroup]:
    """Gap rows grouped by the upstream file that declares their top-level symbol; rows no file declares go to unlocated:<package>."""
    groups: dict[str, list[GapRow]] = defaultdict(list)
    for line in tsv.splitlines():
        fields = line.split("\t")
        if len(fields) < 3 or not fields[0]:
            continue
        package, module, symbol = symbol_of(fields[0])
        file = locate(mirror, package, module, symbol) or f"unlocated:{package}"
        groups[file].append(GapRow(fields[0], fields[2], fields[3] if len(fields) > 3 else ""))
    return [GapGroup(file, rows) for file, rows in sorted(groups.items())]


def group_delta(text: str) -> list[DeltaGroup]:
    """Upstream-sync files whose disposition is still pending, grouped by package, in manifest order."""
    groups: dict[str, list[DeltaFile]] = {}
    for entry in tomllib.loads(text).get("files", []):
        if entry.get("disposition") != "pending":
            continue
        parts = entry["path"].split("/")
        package = parts[1] if parts[0] == "packages" and len(parts) > 1 else parts[0]
        groups.setdefault(package, []).append(DeltaFile(entry["path"], entry.get("change", "")))
    return [DeltaGroup(package, files) for package, files in groups.items()]


MAX_DELTA_FILES = 8  # a 35-file delta (durable) ran past the one-hour task limit twice with no report; codemode's 8 files took 19 minutes


def parts_of(count: int, limit: int = MAX_ROWS) -> int:
    return max(1, -(-count // limit))


def part_slice(items: list, part: tuple[int, int]) -> list:
    """Part i of n of items: consecutive slices of ceil(len/n) items."""
    index, total = part
    size = -(-len(items) // total)
    return items[(index - 1) * size: index * size]


def make_tasks(gaps: list[GapGroup], deltas: list[DeltaGroup]) -> list[Task]:
    tasks: list[Task] = []
    for located in (True, False):
        for g in gaps:
            if g.file.startswith("unlocated:") == located:
                continue
            total = parts_of(len(g.rows))
            if total == 1:
                tasks.append(Task("closure", g.file, len(g.rows)))
                continue
            tasks += [Task("closure", f"{g.file} part {i}/{total}", len(part_slice(g.rows, (i, total)))) for i in range(1, total + 1)]
    for d in deltas:
        total = parts_of(len(d.files), MAX_DELTA_FILES)
        if total == 1:
            tasks.append(Task("delta", d.package, len(d.files)))
            continue
        tasks += [Task("delta", f"{d.package} part {i}/{total}", len(part_slice(d.files, (i, total)))) for i in range(1, total + 1)]
    return tasks


def rows_file(rows_dir: Path, file: str) -> Path:
    return rows_dir / (file.replace("/", "__").replace(":", "_") + ".tsv")


def delta_rows_file(rows_dir: Path, package: str) -> Path:
    return rows_dir / f"delta__{package}.tsv"


def freeze_rows(tsv: str, mirror: Path, rows_dir: Path, delta_text: str = "") -> None:
    """Write each file group's gap rows (id, reason, bound Go symbol) once. A closure task ends with `make ledger-update`, which removes
    the rows it closed from the baseline, so the parts of a group must slice this frozen list, never the shrinking baseline. A group that
    already has a frozen file is left alone."""
    rows_dir.mkdir(parents=True, exist_ok=True)
    for group in group_gaps(tsv, mirror):
        path = rows_file(rows_dir, group.file)
        if path.exists():
            continue
        staging = path.with_name(path.name + ".tmp")
        staging.write_text("".join(f"{row.id}\t{row.reason}\t{row.go}\n" for row in group.rows), encoding="utf-8")
        staging.replace(path)
    for delta in group_delta(delta_text) if delta_text else []:
        path = delta_rows_file(rows_dir, delta.package)
        if path.exists():
            continue
        staging = path.with_name(path.name + ".tmp")
        staging.write_text("".join(f"{f.change}\t{f.path}\n" for f in delta.files), encoding="utf-8")
        staging.replace(path)


def closure_brief(file: str, tsv: str, mirror: Path, part: tuple[int, int] | None = None, rows_dir: Path | None = None) -> str:
    """The gap rows of one upstream file, one tab-separated line each: id, reason, the Go symbol the detector bound. A part (i of n) lists
    its slice of the frozen group (frozen on first use when no task has frozen it); without a part the live baseline is listed."""
    if part is not None and rows_dir is not None:
        path = rows_file(rows_dir, file)
        if not path.exists():
            freeze_rows(tsv, mirror, rows_dir)
        rows = [line for line in path.read_text(encoding="utf-8").splitlines() if line] if path.exists() else []
        rows = part_slice(rows, part)
        return "\n".join([f"closure {file} part {part[0]}/{part[1]}: {len(rows)} rows", *rows])
    rows = []
    for line in tsv.splitlines():
        fields = line.split("\t")
        if len(fields) < 3 or not fields[0]:
            continue
        package, module, symbol = symbol_of(fields[0])
        if (locate(mirror, package, module, symbol) or f"unlocated:{package}") == file:
            rows.append(f"{fields[0]}\t{fields[2]}\t{fields[3] if len(fields) > 3 else ''}")
    if part is not None:
        rows = part_slice(rows, part)
        return "\n".join([f"closure {file} part {part[0]}/{part[1]}: {len(rows)} rows", *rows])
    return "\n".join([f"closure {file}: {len(rows)} rows", *rows])


def delta_brief(package: str, toml_text: str, part: tuple[int, int] | None = None, rows_dir: Path | None = None) -> str:
    """The pending upstream-sync files of one package, one line each: change, path. A part (i of n) lists its slice of the frozen list
    (frozen on first use when no task has frozen it), because a delta task marks files ported and so shrinks the pending list."""
    if part is not None and rows_dir is not None:
        path = delta_rows_file(rows_dir, package)
        if not path.exists():
            freeze_rows("", Path("/nonexistent"), rows_dir, toml_text)
        lines = [line for line in path.read_text(encoding="utf-8").splitlines() if line] if path.exists() else []
        lines = part_slice(lines, part)
        return "\n".join([f"delta {package} part {part[0]}/{part[1]}: {len(lines)} files", *lines])
    files = next((g.files for g in group_delta(toml_text) if g.package == package), [])
    lines = [f"{f.change}\t{f.path}" for f in files]
    if part is not None:
        lines = part_slice(lines, part)
        return "\n".join([f"delta {package} part {part[0]}/{part[1]}: {len(lines)} files", *lines])
    return "\n".join([f"delta {package}: {len(files)} files", *lines])


def latency_report(upstream: dt.datetime, reached: dict[str, dt.datetime]) -> dict[str, dict]:
    report = {}
    for name, target in TARGET_MINUTES.items():
        when = reached.get(name)
        minutes = None if when is None else round((when - upstream).total_seconds() / 60, 1)
        report[name] = {"minutes": minutes, "target": target, "met": minutes is not None and minutes <= target}
    return report


def run_command(command: list[str], env: dict[str, str], timeout: int, cwd: Path) -> int:
    """Run one step in its own process group with no stdin; a timeout kills the whole group, so no go, node or npm grandchild outlives it."""
    with subprocess.Popen(command, env=env, cwd=cwd, stdin=subprocess.DEVNULL, start_new_session=True) as process:
        try:
            return process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            kill_group(process)
            return 124
        except BaseException:
            kill_group(process)
            raise


def kill_group(process: subprocess.Popen) -> None:
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


@dataclasses.dataclass
class Result:
    failed: str | None
    plan: list[str]
    latency: dict


class Pipeline:
    def __init__(self, *, root: Path, ref: str, commit: str, version: str, upstream_time: dt.datetime,
                 runner: Callable = run_command, clock: Callable[[], dt.datetime] | None = None,
                 ready_log: Path | None = None, dry_run: bool = False, run_porter: bool = False,
                 porter_timeout: int = 5400, until: str | None = None):
        self.root, self.ref, self.commit, self.version = root, ref, commit, version
        self.upstream_time, self.runner = upstream_time, runner
        self.clock = clock or (lambda: dt.datetime.now(dt.timezone.utc))
        self.dry_run, self.run_porter, self.porter_timeout, self.until = dry_run, run_porter, porter_timeout, until
        self.out = root / STATE_DIR / version
        self.state_file = self.out / "state.json"
        named = os.environ.get("PIG_PIN_FOLLOW_READY_LOG")
        self.ready_log = ready_log or (Path(named) if named else self.out / "ready.log")
        self.env = dict(os.environ, PI_UPSTREAM_REF=ref)
        self.state = {"ref": ref, "commit": commit, "version": version, "upstream_time": upstream_time.isoformat(), "steps": []}
        self.plan: list[str] = []
        self.tasks: list[Task] = []

    # -- step table -------------------------------------------------------------------------------------------
    def frozen(self) -> Path:
        return self.root / "automation/pin/frozen" / f"{self.commit}.provider-data.tgz"

    def frozen_names(self) -> set[str]:
        return {p.name for p in self.frozen().parent.glob(f"{self.commit}.*")}

    INCREMENTAL = {"PIG_INTERFACE_EXTRACT_INCREMENTAL": "1"}

    def commands(self, name: str) -> list[tuple[list[str], int]]:
        pin = ["python3", "automation/pin/pin.py"]
        table = {
            "resolve": [([*pin, "show"], 300)],
            "oracle": [([*pin, "install-oracle"], 3600)],
            "mirror": [(["make", "upstream-mirror"], 1800)],
            "inventories": [(["make", "interface-proposals"], 3600), (["make", "test-inventory-generate"], 1800)],
            "ledgers": [([*pin, "ledgers"], 7200)],
            "gaps": [(["make", "ledger-check"], 3600)],
        }
        return table.get(name, [])

    ORDER = ["resolve", "oracle", "freeze", "mirror", "inventories", "ledgers", "gaps", "tasks", "porter", "ready"]

    # -- state --------------------------------------------------------------------------------------------------
    def load(self) -> None:
        if self.state_file.exists():
            self.state["steps"] = json.loads(self.state_file.read_text(encoding="utf-8"))["steps"]

    def save(self) -> None:
        self.out.mkdir(parents=True, exist_ok=True)
        self.state_file.write_text(json.dumps(self.state, indent=2) + "\n", encoding="utf-8")

    def passed(self, name: str) -> bool:
        return any(s["name"] == name and s["rc"] == 0 for s in self.state["steps"])

    def record(self, name: str, start: dt.datetime, rc: int) -> None:
        self.state["steps"] = [s for s in self.state["steps"] if s["name"] != name]
        self.state["steps"].append({"name": name, "start": start.isoformat(), "end": self.clock().isoformat(), "rc": rc})
        self.save()

    def end_of(self, name: str) -> dt.datetime | None:
        for step in self.state["steps"]:
            if step["name"] == name and step["rc"] == 0:
                return dt.datetime.fromisoformat(step["end"])
        return None

    def post(self, kind: str, text: str) -> None:
        stamp = self.clock().strftime("%Y-%m-%dT%H:%M:%SZ")
        self.ready_log.parent.mkdir(parents=True, exist_ok=True)
        with open(self.ready_log, "a", encoding="utf-8") as log:
            log.write(f"{stamp} {LANE} {kind} pin-follow {self.ref} {text}\n")

    # -- the steps --------------------------------------------------------------------------------------------------
    def external(self, name: str) -> int:
        for command, timeout in self.commands(name):
            self.plan.append(" ".join(command))
            if self.dry_run:
                continue
            extra = self.INCREMENTAL if command[:2] == ["make", "interface-proposals"] else {}
            rc = self.runner(command, {**self.env, **extra}, timeout, self.root)
            if rc != 0:
                return rc
        return 0

    def frozen_check(self) -> int:
        """install-oracle freezes the model data of a commit's first build; the pipeline fails when no snapshot exists and reports a new one."""
        new = sorted(self.frozen_names() - self.frozen_before)
        if new:
            self.post("FINDING", f"the oracle build froze {', '.join(new)} in automation/pin/frozen: commit them with the pin")
        return 0 if self.frozen().exists() else 1

    def make_task_list(self) -> int:
        gaps = self.root / "test/parity/interface-closure/gaps" / f"interface-gaps-v{self.version}.tsv"
        delta = self.root / "test/parity/upstream-sync" / f"v{self.version}.toml"
        mirror = self.root / ".upstream" / f"v{self.version}"
        groups = group_gaps(gaps.read_text(encoding="utf-8"), mirror) if gaps.exists() else []
        deltas = group_delta(delta.read_text(encoding="utf-8")) if delta.exists() else []
        freeze_rows(gaps.read_text(encoding="utf-8") if gaps.exists() else "", mirror, self.out / "rows", delta.read_text(encoding="utf-8") if delta.exists() else "")
        self.tasks = make_tasks(groups, deltas)
        lines = [f"{t.kind}\t{t.scope}\t{t.rows}" + f"\trows/{(rows_file(Path('.'), t.scope.split(' part ')[0]) if t.kind == 'closure' else delta_rows_file(Path('.'), t.scope.split(' part ')[0])).name}" for t in self.tasks]
        (self.out / "tasks.tsv").write_text("\n".join(lines) + ("\n" if lines else ""), encoding="utf-8")
        return 0

    def porter_tasks(self) -> int:
        failures = 0
        rows = []
        for task in self.tasks:
            started = self.clock()
            command = ["make", "porter-task", f"TASK={task.task}"]
            rc = self.runner(command, self.env, self.porter_timeout, self.root)
            rows.append(f"{task.kind}\t{task.scope}\t{task.rows}\t{rc}\t{(self.clock() - started).total_seconds():.0f}")
            failures += rc != 0
        (self.out / "porter.tsv").write_text("\n".join(rows) + "\n", encoding="utf-8")
        return 1 if failures else 0

    def write_record(self, latency: dict) -> None:
        """automation/pin/records/<version>.json: the pin this follow resolved, its step times and latency, and the frozen artifacts that
        belong with it. Commit it with them. It records a follow; it does not move the committed pin."""
        gaps = self.root / "test/parity/interface-closure/gaps" / f"interface-gaps-v{self.version}.tsv"
        record = {
            "ref": self.ref, "commit": self.commit, "version": self.version,
            "package_version": self.version.split("+")[0],
            "upstream_time": self.upstream_time.isoformat(),
            "steps": self.state["steps"], "latency": latency,
            "gap_rows": len([line for line in gaps.read_text(encoding="utf-8").splitlines() if line]) if gaps.exists() else None,
            "tasks": len(self.tasks),
            "frozen": sorted(self.frozen_names()),
        }
        path = self.root / "automation/pin/records" / f"{self.version}.json"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")

    def run(self) -> Result:
        if not self.dry_run:
            self.load()
        failed = None
        self.frozen_before = self.frozen_names()
        for name in self.ORDER:
            if name == "porter" and not self.run_porter:
                continue
            if name == "ready":
                break
            if not self.dry_run and self.passed(name):
                if name == "tasks":
                    self.make_task_list()
                if name == self.until:
                    return Result(None, self.plan, latency_report(self.upstream_time, {}))
                continue
            started = self.clock()
            if name == "freeze":
                self.plan.append("check automation/pin/frozen/<commit>.provider-data.tgz exists after the oracle build")
                rc = 0 if self.dry_run else self.frozen_check()
            elif name == "tasks":
                self.plan.append("group gap rows and pending delta files into Porter tasks")
                rc = 0 if self.dry_run else self.make_task_list()
            elif name == "porter":
                self.plan.append("make porter-task TASK=<each task>")
                rc = 0 if self.dry_run else self.porter_tasks()
            else:
                rc = self.external(name)
            if self.dry_run:
                if name == self.until:
                    break
                continue
            self.record(name, started, rc)
            if rc != 0:
                failed = name
                self.post("FINDING", f"step {name} failed rc={rc}; state {self.state_file}")
                break
            if name == self.until:
                return Result(None, self.plan, latency_report(self.upstream_time, {}))
        latency = latency_report(self.upstream_time, {k: v for k in TARGET_MINUTES if (v := self.end_of({"gaps": "gaps", "porter": "porter"}[k]))})
        if not self.dry_run and failed is None:
            self.write_record(latency)
            gaps, porter = latency["gaps"], latency["porter"]
            self.post("READY", f"{self.version}: gap list {gaps['minutes']} min after the upstream commit (target {gaps['target']}, met {gaps['met']}); "
                               f"{len(self.tasks)} Porter tasks in {self.out / 'tasks.tsv'}; porter {porter['minutes']} min (target {porter['target']}, met {porter['met']}); no pin flipped")
        return Result(failed, self.plan, latency)


def upstream_commit_time(pin: pinlib.Pin, commit_date: Callable[[pinlib.Pin], str] | None = None) -> dt.datetime:
    """The commit's time: the modification time `git archive` gives every file of the commit's source tarball, which the oracle
    build already downloaded. No API request, so no rate limit and no token."""
    if commit_date is None:
        import oracle

        commit_date = oracle.commit_date
    return dt.datetime.fromisoformat(commit_date(pin).replace("Z", "+00:00"))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("ref", help="the upstream commit (short or full) or tag to follow")
    parser.add_argument("--dry-run", action="store_true", help="print the plan; run nothing")
    parser.add_argument("--until", choices=Pipeline.ORDER, help="stop after this step; a rerun resumes after it")
    parser.add_argument("--run-porter", action="store_true", help="also run one Porter task per group (headless, serial)")
    parser.add_argument("--ready-log", help="append READY and FINDING lines here (default: PIG_PIN_FOLLOW_READY_LOG, else the run's state directory)")
    parser.add_argument("--closure", metavar="FILE", help="print the gap rows of one upstream file and exit")
    parser.add_argument("--freeze-rows", action="store_true", help="freeze every file group's gap rows (never overwrites) and exit")
    parser.add_argument("--part", metavar="I/N", help="with --closure or --delta: only part I of N of the frozen rows or files")
    parser.add_argument("--delta", metavar="PACKAGE", help="print the pending upstream-sync files of one package and exit")
    parser.add_argument("--upstream-time", help="ISO time of the upstream commit (default: the GitHub commit date)")
    args = parser.parse_args(argv)
    root = Path(__file__).resolve().parents[2]
    pin = pinlib.resolve(args.ref, None)
    rows_dir = root / STATE_DIR / pin.version / "rows"
    if args.freeze_rows:
        tsv = root / "test/parity/interface-closure/gaps" / f"interface-gaps-v{pin.version}.tsv"
        delta = root / "test/parity/upstream-sync" / f"v{pin.version}.toml"
        freeze_rows(tsv.read_text(encoding="utf-8") if tsv.exists() else "", root / ".upstream" / pin.mirror_dir, rows_dir,
                    delta.read_text(encoding="utf-8") if delta.exists() else "")
        return 0
    if args.closure:
        tsv = root / "test/parity/interface-closure/gaps" / f"interface-gaps-v{pin.version}.tsv"
        part = tuple(int(x) for x in args.part.split("/")) if args.part else None
        print(closure_brief(args.closure, tsv.read_text(encoding="utf-8"), root / ".upstream" / pin.mirror_dir, part, rows_dir))
        return 0
    if args.delta:
        part = tuple(int(x) for x in args.part.split("/")) if args.part else None
        print(delta_brief(args.delta, (root / "test/parity/upstream-sync" / f"v{pin.version}.toml").read_text(encoding="utf-8"), part, rows_dir))
        return 0
    if args.upstream_time:
        when = dt.datetime.fromisoformat(args.upstream_time.replace("Z", "+00:00"))
    elif args.dry_run:
        when = dt.datetime.now(dt.timezone.utc)
    else:
        when = upstream_commit_time(pin)
    result = Pipeline(root=root, ref=args.ref, commit=pin.commit, version=pin.version, upstream_time=when,
                      ready_log=Path(args.ready_log) if args.ready_log else None, dry_run=args.dry_run, run_porter=args.run_porter, until=args.until).run()
    if args.dry_run:
        print("\n".join(result.plan))
    print(json.dumps(result.latency))
    return 1 if result.failed else 0


if __name__ == "__main__":
    sys.exit(main())
