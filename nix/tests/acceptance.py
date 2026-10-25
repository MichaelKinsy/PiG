# SPDX-License-Identifier: MIT
"""Acceptance check of the Nix package: extensions in every language load from the installed SDKs and toolchains.

The check drives `pig --mode rpc` with the offline test provider. A directory of conflicting tools (go, cargo, rustc, python3, node, uv,
cc, ...) leads PATH; each records its use and fails, so a build or launch that reached PATH instead of the installation's toolchain fails
the check. It proves, for the Go, Rust and Python factories (packed cells), the Go, Rust and Python standalones (isolated cells) and the
TypeScript factory (packed Node cell):

- registration (each extension's session_start status) and host calls (append an entry, read the entries back, notify);
- reload (ctx.reload() from an extension command) restarts every extension, which then answers again;
- a second start reuses every cached build and builds nothing: every build keeps the ready record its cold start published;
- a Node standalone (an executable script whose shebang searches PATH) runs on the installation's node;
- an isolated Go module whose toolchain directive names another release builds with the installation's go, not that release;
- no SDK or runtime copy is written under the config root, and the installed SDK root is unchanged.

With --broken it checks an installation whose Rust SDK is missing and whose Python SDK marker names another SDK: both faults are
reported, the affected extensions fail to load with the installation named, and nothing is staged in their place.

With --without-python it checks an installation built without Python: its Python extensions fail to load instead of running a python3
or uv from PATH.
"""

import argparse
import hashlib
import json
import os
import queue
import shutil
import subprocess
import sys
import threading
import time
from pathlib import Path

LOADED = ["go-packed", "go-isolated", "rust-packed", "rust-isolated", "python-packed", "python-isolated", "typescript-packed"]
CONFLICTING = ["go", "go1.99.0", "gofmt", "cargo", "rustc", "rustup", "python3", "python", "node", "npm", "uv", "cc", "gcc", "c++", "ld"]
COLD_BUDGET = 900
WARM_BUDGET = 120


def fail(message):
    print(f"FAIL: {message}", file=sys.stderr)
    sys.exit(1)


class RPC:
    """One `pig --mode rpc` process and the records it writes."""

    def __init__(self, pig, cwd, env, extensions, log):
        args = [pig, "--mode", "rpc", "--model", "test-faux/faux-1", "--no-session"]
        for ext in extensions:
            args += ["-e", str(ext)]
        self.log = log
        self.stderr = open(log, "wb")
        self.proc = subprocess.Popen(args, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr)
        self.records = queue.Queue()
        self.seen = []
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        for line in self.proc.stdout:
            try:
                self.records.put(json.loads(line))
            except json.JSONDecodeError:
                self.records.put({"type": "unparsed", "line": line.decode(errors="replace")})
        self.records.put(None)

    def send(self, record):
        self.proc.stdin.write((json.dumps(record) + "\n").encode())
        self.proc.stdin.flush()

    def await_all(self, predicate, wanted, budget, what):
        """Read records until predicate has returned every value in wanted."""
        pending = set(wanted)
        deadline = time.monotonic() + budget
        while pending:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                self.dump()
                fail(f"timed out after {budget}s waiting for {what}: still missing {sorted(pending)}")
            try:
                record = self.records.get(timeout=remaining)
            except queue.Empty:
                continue
            if record is None:
                self.dump()
                fail(f"pig exited while waiting for {what}: still missing {sorted(pending)}")
            self.seen.append(record)
            found = predicate(record)
            if found in pending:
                pending.discard(found)

    def dump(self):
        print("--- rpc records ---", file=sys.stderr)
        for record in self.seen[-60:]:
            print(json.dumps(record)[:400], file=sys.stderr)
        self.stderr.flush()
        print("--- stderr ---", file=sys.stderr)
        print(Path(self.log).read_text(errors="replace")[-6000:], file=sys.stderr)

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=60)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait()
        self.stderr.close()


def status(record):
    if record.get("type") == "extension_ui_request" and record.get("method") == "setStatus":
        text = record.get("statusText") or ""
        if text.endswith(": ready"):
            return text.removesuffix(": ready")
    return None


def notified(record):
    if record.get("type") == "extension_ui_request" and record.get("method") == "notify":
        return record.get("message")
    return None


def tree_digest(root):
    digest = hashlib.sha256()
    for path in sorted(Path(root).rglob("*")):
        stat = path.lstat()
        digest.update(f"{path.relative_to(root)}\0{stat.st_mode}\0{stat.st_size}\0{stat.st_mtime_ns}\0".encode())
        if path.is_file() and not path.is_symlink():
            digest.update(path.read_bytes())
    return digest.hexdigest()


def build_stamps(cache):
    """Every published build under the cache, by directory, with the identity of its ready record. A rebuild under the same digest keeps
    the directory name but publishes a new record, so equal stamps mean nothing was built again."""
    stamps = {}
    for path in sorted(Path(cache).rglob("ready.json")):
        if path.is_file():
            stat = path.stat()
            stamps[str(path.parent.relative_to(cache))] = (stat.st_ino, stat.st_mtime_ns, path.read_bytes())
    return stamps


def check_placement(cache, entries):
    """Each factory ran in its language's packed cell and each compiled standalone in its own isolated build."""
    for lang in ("go", "rust", "python", "node"):
        cells = [entry for entry in entries if entry.startswith(f"cells/{lang}/")]
        if len(cells) != 1:
            fail(f"want one packed {lang} cell, found {cells}")
    for name in ("go-isolated", "rust-isolated"):
        builds = [entry for entry in entries if entry.startswith(f"ext/{name}-")]
        if len(builds) != 1:
            fail(f"want one isolated build of {name}, found {builds}")
    for lang, member, outsider in (("python", "python_packed", "python-isolated"), ("node", "typescript-packed", "python-isolated")):
        cell = Path(cache, next(entry for entry in entries if entry.startswith(f"cells/{lang}/")))
        text = "".join(path.read_text(errors="replace") for path in cell.rglob("*") if path.is_file() and path.stat().st_size < 1 << 20)
        if member not in text or outsider in text:
            fail(f"the packed {lang} cell does not host exactly its factory ({member})")
    if len(entries) != 6:
        fail(f"want 4 packed cells and 2 isolated builds, found {sorted(entries)}")


def environment(work, conflicts):
    home = work / "home"
    env = {
        "HOME": str(home),
        "PIG_HOME": str(home / ".pig"),
        "PI_HOME": str(home / ".pi"),
        "PIG_CODING_AGENT_DIR": str(home / ".pig" / "agent"),
        "PI_CODING_AGENT_DIR": str(home / ".pig" / "agent"),
        "PIG_TEST_FAUX": "1",
        "PIG_OFFLINE": "1",
        "PATH": str(conflicts) + os.pathsep + os.environ.get("PATH", ""),
        "TMPDIR": str(work / "tmp"),
        "LANG": "C.UTF-8",
    }
    for key in ("CARGO_HOME", "CARGO_NET_OFFLINE", "GOFLAGS", "GOPROXY", "SSL_CERT_FILE"):
        if key in os.environ:
            env[key] = os.environ[key]
    (work / "tmp").mkdir(parents=True, exist_ok=True)
    return env


def write_conflicts(directory, log):
    directory.mkdir(parents=True)
    for name in CONFLICTING:
        tool = directory / name
        tool.write_text(f'#!/bin/sh\necho "{name} $*" >> "{log}"\nexit 97\n')
        tool.chmod(0o755)


def run_session(pig, work, env, extensions, label, budget, reload):
    rpc = RPC(pig, work, env, extensions, work / f"{label}.stderr")
    started = time.monotonic()
    rpc.await_all(status, LOADED, budget, f"{label}: every extension's session_start status")
    loaded = time.monotonic() - started
    # One command at a time: each must see its own appends, and only its own extension's entries are counted.
    def probe(n, tag):
        for name in LOADED:
            rpc.send({"id": f"{label}-{name}-{tag}", "type": "prompt", "message": f"/{name}-probe"})
            rpc.await_all(notified, [f"{name}: entries={n}"], WARM_BUDGET, f"{label}: {name} host calls ({tag})")

    probe(1, "first")
    probe(2, "second")
    if reload:
        # The reload has finished when its command answers; statuses alone can arrive while it is still in progress.
        reload_id = f"{label}-reload"
        rpc.send({"id": reload_id, "type": "prompt", "message": "/nix-reload"})
        rpc.await_all(
            lambda record: status(record) or (reload_id if record.get("id") == reload_id and record.get("success") else None),
            LOADED + [reload_id],
            budget,
            f"{label}: every extension's status after reload and the reload command's answer",
        )
        probe(3, "after reload")
    rpc.close()
    print(f"{label}: {len(LOADED)} extensions registered in {loaded:.1f}s and answered host calls" + (" before and after reload" if reload else ""))


def check_no_copies(work):
    home = work / "home"
    for path in home.rglob("*"):
        if path.name in (".pig-sdk-version", ".pig-node-runtime", "runtime.mjs"):
            fail(f"an SDK or runtime copy was written under the config root: {path}")
    if (home / ".pig" / "state" / "pigsdk").exists():
        staged = [p.name for p in (home / ".pig" / "state" / "pigsdk").iterdir() if p.name.startswith("sdk")]
        if staged:
            fail(f"SDKs were staged under the config root: {staged}")


def check_conflicts(log):
    if log.exists():
        fail("a tool from PATH ran instead of the installation's toolchain:\n" + log.read_text())


def node_standalone(pig, work, env, script, conflict_log):
    """A Node standalone runs on the installation's node: the script records the interpreter that runs it and exits before it registers,
    so pig reports its load failure."""
    probe = work / "node-standalone.probe"
    run = subprocess.run(
        [pig, "--mode", "rpc", "--model", "test-faux/faux-1", "--no-session", "-e", str(script)],
        cwd=work, env={**env, "PIG_NIX_NODE_PROBE": str(probe)}, input=b"", capture_output=True, timeout=WARM_BUDGET,
    )
    check_conflicts(conflict_log)
    if not probe.exists():
        print((run.stdout + run.stderr).decode(errors="replace")[-4000:], file=sys.stderr)
        fail("the Node standalone did not run")
    print(f"the Node standalone ran on {probe.read_text()}, not on a node from PATH")


def without_python(args):
    work = Path(args.work)
    fixtures = work / "extensions"
    shutil.copytree(args.fixtures, fixtures)
    conflict_log = work / "conflicting-tools.log"
    write_conflicts(work / "conflicting-tools", conflict_log)
    env = environment(work, work / "conflicting-tools")
    for name in ("python-packed", "python-isolated"):
        run = subprocess.run(
            [args.pig, "--mode", "rpc", "--model", "test-faux/faux-1", "--no-session", "-e", str(fixtures / name)],
            cwd=work, env=env, input=b"", capture_output=True, timeout=COLD_BUDGET,
        )
        check_conflicts(conflict_log)
        if run.returncode == 0:
            fail(f"{name} loaded on an installation without Python")
        print(f"{name}: refused without running a Python from PATH")


def accept(args):
    work = Path(args.work)
    fixtures = work / "extensions"
    shutil.copytree(args.fixtures, fixtures)
    for path in fixtures.rglob("*"):
        path.chmod(0o755 if path.is_dir() or os.access(path, os.X_OK) else 0o644)
    conflict_log = work / "conflicting-tools.log"
    write_conflicts(work / "conflicting-tools", conflict_log)
    env = environment(work, work / "conflicting-tools")
    extensions = [fixtures / name for name in LOADED]
    installed_before = tree_digest(args.installed)

    run_session(args.pig, work, env, extensions, "cold", COLD_BUDGET, reload=True)
    check_conflicts(conflict_log)
    cache = Path(env["PIG_HOME"]) / "cache"
    cold = build_stamps(cache)
    if not cold:
        fail(f"no build was published under {cache}")
    check_placement(cache, cold)
    print(f"cold start published {len(cold)} builds: a packed cell per language and an isolated build per compiled standalone")

    started = time.monotonic()
    run_session(args.pig, work, env, extensions, "warm", WARM_BUDGET, reload=False)
    check_conflicts(conflict_log)
    warm = build_stamps(cache)
    if warm != cold:
        rebuilt = sorted(entry for entry in cold.keys() | warm.keys() if cold.get(entry) != warm.get(entry))
        fail(f"a warm start built again: {rebuilt}")
    print(f"warm start reused all {len(warm)} builds in {time.monotonic() - started:.1f}s")

    node_standalone(args.pig, work, env, fixtures / "node-standalone" / "standalone.mjs", conflict_log)

    check_no_copies(work)
    if tree_digest(args.installed) != installed_before:
        fail("the installed SDK root changed")
    print("no SDK or runtime copy under the config root; installed SDK root unchanged")


def broken(args):
    work = Path(args.work)
    fixtures = work / "extensions"
    shutil.copytree(args.fixtures, fixtures)
    conflict_log = work / "conflicting-tools.log"
    write_conflicts(work / "conflicting-tools", conflict_log)
    env = environment(work, work / "conflicting-tools")

    dry = subprocess.run([args.pig, "reload", "--dry-run"], env=env, capture_output=True, text=True, check=True).stdout
    print(dry)
    for want in ("installed rust extension SDK is missing at", "installed python extension SDK at", "is 0000000000000000 but this pig embeds"):
        if want not in dry:
            fail(f"`pig reload --dry-run` does not report {want!r}")
    if subprocess.run([args.pig, "reload"], env=env, capture_output=True).returncode == 0:
        fail("`pig reload` succeeded over a broken installation")

    # pig refuses to start when an extension it was asked to load fails, so each broken extension starts its own run.
    for name, fault in (
        ("rust-packed", "installed rust extension SDK is missing at"),
        ("rust-isolated", "installed rust extension SDK is missing at"),
        ("python-packed", "is 0000000000000000 but this pig embeds"),
        ("python-isolated", "is 0000000000000000 but this pig embeds"),
    ):
        run = subprocess.run(
            [args.pig, "--mode", "rpc", "--model", "test-faux/faux-1", "--no-session", "-e", str(fixtures / name)],
            cwd=work, env=env, input=b"", capture_output=True, timeout=COLD_BUDGET,
        )
        output = (run.stdout + run.stderr).decode(errors="replace")
        if run.returncode == 0 or fault not in output or "reinstall pig" not in output or "PIG_SDK_" in output:
            print(output[-4000:], file=sys.stderr)
            fail(f"{name} over a broken installation exited {run.returncode} without naming the installation ({fault!r})")
        print(f"{name}: refused with the installation's fault")

    rpc = RPC(args.pig, work, env, [fixtures / "go-packed"], work / "intact.stderr")
    rpc.await_all(status, ["go-packed"], COLD_BUDGET, "the extension whose installed SDK is intact")
    rpc.close()
    check_conflicts(conflict_log)
    check_no_copies(work)
    print("a missing and a mismatched installed SDK are reported, their extensions do not load, and nothing is staged instead")


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--pig", required=True)
    parser.add_argument("--installed", required=True, help="the package's installed SDK root")
    parser.add_argument("--fixtures", required=True)
    parser.add_argument("--work", required=True)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--broken", action="store_true")
    mode.add_argument("--without-python", action="store_true")
    args = parser.parse_args()
    (broken if args.broken else without_python if args.without_python else accept)(args)


if __name__ == "__main__":
    main()
