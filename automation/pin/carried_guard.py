# SPDX-License-Identifier: MIT
"""Guard of `make test-carried-oracles`: the run compares with another Pi commit and must leave the tree's own Pi as it was.

The target runs three times: to its end (it must pass), failing (CARRIED_ORACLE_REF is the pin's own commit, whose Pi has none
of the carried work, so its tests fail) and killed (SIGKILL of every process of the run's session once a go test binary is
running; the session is read from /proc, so the guard runs on Linux). After each run the state must be the state before it:

  git status --porcelain            the same lines
  .upstream/current                 the link to the pin's mirror
  extensions/sdk-ts/node_modules    the link to the pin's oracle build
  durable/interop/node_modules      a directory that is an install of the vendored packs (vendor.check_installed)

A gate that runs after the carried oracles (make interface-gaps, the durable/interop tests) reads these, so a run that moved
one of them would make that gate read another Pi.
"""
from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from pathlib import Path

import pinlib
import vendor
from pinlib import ROOT, Pin

TARGET = "test-carried-oracles"
LINKS = (".upstream/current", "extensions/sdk-ts/node_modules", vendor.INTEROP + "/node_modules")


def git_status(root: Path) -> str:
    return subprocess.run(["git", "status", "--porcelain"], cwd=root, check=True, capture_output=True, text=True).stdout


def stands(path: Path) -> str:
    if path.is_symlink():
        return "link to " + os.path.realpath(path)
    return "directory" if path.is_dir() else "missing"


def state(root: Path, status=git_status) -> dict[str, str]:
    return {"git status --porcelain": status(root), **{rel: stands(root / rel) for rel in LINKS}}


def problems(root: Path, pin: Pin, before: dict[str, str], after: dict[str, str]) -> list[str]:
    """What a run changed, and what the tree's own Pi is not although it must be the pin's."""
    found = [f"{key} was {before[key]!r} and is {after[key]!r}" for key in before if before[key] != after[key]]
    mirror = os.path.realpath(root / ".upstream" / pin.mirror_dir)
    if after[".upstream/current"] != "link to " + mirror:
        found.append(f".upstream/current is {after['.upstream/current']}, not the link to the pin's mirror {mirror}")
    return found + vendor.check_installed(vendor.INTEROP, root)


def session(sid: int, proc: Path = Path("/proc")) -> dict[int, str]:
    """The live processes of session `sid` (Linux, read from /proc): pid -> the program it runs."""
    found = {}
    for entry in proc.iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fields = (entry / "stat").read_text().rpartition(")")[2].split()
            program = (entry / "cmdline").read_bytes().split(b"\0", 1)[0].decode(errors="replace")
        except OSError:
            continue  # the process ended while it was read
        if len(fields) > 3 and fields[0] != "Z" and fields[3] == str(sid):
            found[int(entry.name)] = program
    return found


def runs_a_test_binary(programs: dict[int, str]) -> bool:
    return any(program.endswith(".test") for program in programs.values())


def run(root: Path, arguments: list[str], kill: bool) -> int:
    """Run the target in its own session; with `kill`, end every process of it with SIGKILL once one is a go test binary."""
    process = subprocess.Popen(["make", "--no-print-directory", TARGET, *arguments], cwd=root, start_new_session=True)
    if not kill:
        return process.wait()
    deadline = time.monotonic() + 3600
    while process.poll() is None and not runs_a_test_binary(session(process.pid)):
        if time.monotonic() > deadline:
            break
        time.sleep(0.2)
    if process.poll() is not None:
        raise SystemExit(f"carried-guard: the run ended ({process.returncode}) before a test binary ran: nothing was killed")
    while members := session(process.pid):
        for pid in members:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        process.poll()
        time.sleep(0.2)
    return process.wait()


def main() -> int:
    pin, override = pinlib.effective()
    if override:
        raise SystemExit("carried-guard: unset PI_UPSTREAM_REF: the guard compares the tree with the committed pin")
    before = state(ROOT)
    wrong = problems(ROOT, pin, before, before)
    if wrong:
        raise SystemExit("carried-guard: the tree's own Pi is not the pin's before any run (make upstream-mirror installs it):\n  " + "\n  ".join(wrong))
    cases = (
        ("runs to its end", [], False, lambda rc: rc == 0, "exit 0"),
        ("fails", [f"CARRIED_ORACLE_REF={pin.commit}"], False, lambda rc: rc != 0, "a failure"),
        ("is killed", [], True, lambda rc: rc != 0, "a kill"),
    )
    failed = False
    for name, arguments, kill, expected, wanted in cases:
        print(f"carried-guard: {TARGET} {name}: starting", flush=True)
        rc = run(ROOT, arguments, kill)
        found = problems(ROOT, pin, before, state(ROOT))
        if not expected(rc):
            found.insert(0, f"the run returned {rc}, the case needs {wanted}")
        print(f"carried-guard: {TARGET} {name}: returned {rc}; " + ("the tree's own Pi is as before" if not found else "PROBLEMS:\n  " + "\n  ".join(found)), flush=True)
        failed = failed or bool(found)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
