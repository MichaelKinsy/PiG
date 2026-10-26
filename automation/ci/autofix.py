#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Regenerate committed derived files that CI found stale, and apply the result safely.

Two halves, with a trust boundary between them (see .github/AGENTS.md, "Derived-file autofix"):

  prepare   runs in the read-only Bookkeeping job, on the pull request's own code. It reads the
            bookkeeping log, decides whether every failed gate is one whose only remedy is a
            generator listed in automation/ci/autofix-gates.txt, runs those generators, re-runs
            the failed gates, and writes the changed files into an artifact directory. It never
            has a write token.
  validate  runs in the privileged workflow, on the default branch's copy of this script and of
            automation/ci/autofix-paths.txt. The artifact is untrusted input: it must carry only
            regular files at allowlisted paths within the size limits. validate turns it into the
            GraphQL createCommitOnBranch input, so the commit is created by GitHub and is Verified.
  comment   renders the pull request comment for a fork, where nothing is pushed.

Usage:
  autofix.py failed-gates LOG
  autofix.py prepare --log LOG --out DIR --head-sha SHA [--gates FILE] [--paths FILE]
  autofix.py validate --artifact DIR --paths FILE --repo OWNER/NAME --branch BRANCH --expected-head SHA --out FILE
  autofix.py comment --artifact DIR --gates FILE --fork
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import shlex
import stat
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GATES_FILE = ROOT / "automation/ci/autofix-gates.txt"
PATHS_FILE = ROOT / "automation/ci/autofix-paths.txt"

MAX_FILE_BYTES = 16 * 1024 * 1024
MAX_TOTAL_BYTES = 48 * 1024 * 1024
MAX_FILES = 64

# The bot's identity as GitHub records it for a commit created with a workflow's GITHUB_TOKEN.
BOT_NAME = "github-actions[bot]"
BOT_EMAIL = "41898282+github-actions[bot]@users.noreply.github.com"
# Marks the commit this tool creates, so a second failure on the same head is reported, not fixed again.
TRAILER = "Autofix: derived files"

FAILED_GATE = re.compile(r"\*\*\* \[[^\]:]+:\d+: ([A-Za-z0-9_.-]+)\] Error")
GATE_NAME = re.compile(r"[A-Za-z0-9_.-]+")


class AutofixError(Exception):
    pass


def read_table(path: Path) -> list[tuple[str, str]]:
    rows: list[tuple[str, str]] = []
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        gate, sep, command = (part.strip() for part in line.partition("|"))
        if not sep or not GATE_NAME.fullmatch(gate) or not command:
            raise AutofixError(f"{path}:{number}: want 'gate | command', got {line!r}")
        rows.append((gate, command))
    return rows


def read_paths(path: Path) -> list[str]:
    patterns = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            patterns.append(line)
    return patterns


def path_allowed(path: str, patterns: list[str]) -> bool:
    """A pattern is an exact repository path, or one whose last element holds a single * (it never crosses a slash)."""
    if path.startswith("/") or "\\" in path or any(part in ("", ".", "..") for part in path.split("/")):
        return False
    for pattern in patterns:
        if "*" not in pattern:
            if path == pattern:
                return True
            continue
        directory, _, name = pattern.rpartition("/")
        if "*" in directory or name.count("*") != 1:
            raise AutofixError(f"unsupported path pattern {pattern!r}")
        prefix, suffix = name.split("*")
        got_dir, _, got_name = path.rpartition("/")
        if got_dir == directory and len(got_name) >= len(prefix) + len(suffix) and got_name.startswith(prefix) and got_name.endswith(suffix):
            return True
    return False


def failed_gates(log: str) -> list[str]:
    seen: list[str] = []
    for match in FAILED_GATE.finditer(log):
        if match.group(1) not in seen:
            seen.append(match.group(1))
    return seen


def run(command: list[str], env: dict[str, str] | None = None) -> int:
    print("+", shlex.join(command), flush=True)
    return subprocess.run(command, cwd=ROOT, env=env, check=False).returncode


def changed_files() -> tuple[list[str], list[str]]:
    """Paths the generators changed or added, and paths they deleted, relative to the checked-out tree."""
    out = subprocess.run(["git", "status", "--porcelain", "-z", "--untracked-files=all"], cwd=ROOT, check=True, capture_output=True).stdout
    present: list[str] = []
    deleted: list[str] = []
    entries = out.decode("utf-8").split("\0")
    index = 0
    while index < len(entries):
        entry = entries[index]
        index += 1
        if not entry:
            continue
        code, path = entry[:2], entry[3:]
        if "R" in code or "C" in code:
            raise AutofixError(f"a generator renamed or copied a file ({path})")
        (deleted if "D" in code else present).append(path)
    return sorted(present), sorted(deleted)


def cmd_prepare(args: argparse.Namespace) -> int:
    log = Path(args.log).read_text(encoding="utf-8", errors="replace")
    gates = dict(read_table(Path(args.gates)))
    patterns = read_paths(Path(args.paths))
    failed = failed_gates(log)
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    def decline(reason: str) -> int:
        print(f"autofix: not applicable: {reason}")
        write_output("eligible", "false")
        return 0

    if not failed:
        return decline("no gate failed in the bookkeeping log")
    foreign = [gate for gate in failed if gate not in gates]
    if foreign:
        return decline(f"failed gates with no generator: {', '.join(foreign)}")
    if subprocess.run(["git", "status", "--porcelain", "--untracked-files=all"], cwd=ROOT, check=True, capture_output=True).stdout:
        return decline("the work tree was not clean before regeneration")

    env = dict(os.environ, LC_ALL="C", FORCE_COLOR="0")
    commands: list[str] = []
    for gate, command in read_table(Path(args.gates)):
        if gate in failed and command not in commands:
            commands.append(command)
    for command in commands:
        if run(shlex.split(command), env) != 0:
            return decline(f"the generator {command!r} failed")
    if run(["make", "-k", "-j4", "-O", *failed], env) != 0:
        return decline("the failed gates still fail after regeneration, so the cause is not stale derived files")

    present, deleted = changed_files()
    if not present and not deleted:
        return decline("regeneration changed nothing")
    stray = [path for path in present + deleted if not path_allowed(path, patterns)]
    if stray:
        return decline(f"regeneration changed files outside the derived-file paths: {', '.join(stray)}")
    if len(present) > MAX_FILES:
        return decline(f"regeneration changed {len(present)} files")

    files = []
    total = 0
    for path in present:
        source = ROOT / path
        info = source.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o111:
            return decline(f"{path} is not a plain non-executable file")
        data = source.read_bytes()
        total += len(data)
        files.append({"path": path, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
        target = out / "files" / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    manifest = {"head_sha": args.head_sha, "gates": failed, "files": files, "deleted": deleted}
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"autofix: {len(files)} file(s) regenerated for {', '.join(failed)} ({total} bytes)")
    write_output("eligible", "true")
    return 0


def write_output(name: str, value: str) -> None:
    target = os.environ.get("GITHUB_OUTPUT")
    if target:
        with open(target, "a", encoding="utf-8") as stream:
            stream.write(f"{name}={value}\n")


def load_manifest(artifact: Path, patterns: list[str], gates: dict[str, str]) -> dict:
    """Parse and validate an untrusted artifact. Every value is checked here; nothing downstream trusts it."""
    manifest_path = artifact / "manifest.json"
    if manifest_path.is_symlink() or not manifest_path.is_file() or manifest_path.stat().st_size > 1 << 20:
        raise AutofixError("manifest.json is missing, not a plain file, or too large")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise AutofixError(f"manifest.json is not JSON: {error}") from error
    if not isinstance(manifest, dict) or set(manifest) != {"head_sha", "gates", "files", "deleted"}:
        raise AutofixError("manifest.json has unexpected keys")
    if not isinstance(manifest["head_sha"], str) or not re.fullmatch(r"[0-9a-f]{40}", manifest["head_sha"]):
        raise AutofixError("manifest head_sha is not a commit id")
    if not isinstance(manifest["gates"], list) or not manifest["gates"] or not all(isinstance(g, str) and g in gates for g in manifest["gates"]):
        raise AutofixError("manifest names a gate that has no generator")
    if not isinstance(manifest["files"], list) or not isinstance(manifest["deleted"], list):
        raise AutofixError("manifest files and deleted must be lists")
    if len(manifest["files"]) > MAX_FILES or len(manifest["deleted"]) > MAX_FILES or not (manifest["files"] or manifest["deleted"]):
        raise AutofixError("manifest lists no file or too many")
    seen = set()
    total = 0
    for entry in manifest["files"]:
        if not isinstance(entry, dict) or set(entry) != {"path", "bytes", "sha256"} or not isinstance(entry["path"], str):
            raise AutofixError("manifest file entry is malformed")
        path = entry["path"]
        if path in seen or not path_allowed(path, patterns):
            raise AutofixError(f"{path!r} is not an allowed derived-file path")
        seen.add(path)
        source = artifact / "files" / path
        # Every parent component must be a real directory inside the artifact, and the file a regular, non-linked file.
        node = artifact
        for part in ("files", *path.split("/")[:-1]):
            node = node / part
            if node.is_symlink() or not node.is_dir():
                raise AutofixError(f"{path}: parent is not a plain directory")
        if source.is_symlink() or not source.is_file():
            raise AutofixError(f"{path}: not a plain file")
        size = source.stat().st_size
        if size > MAX_FILE_BYTES or size != entry["bytes"]:
            raise AutofixError(f"{path}: size {size} does not match the manifest or exceeds the limit")
        total += size
        if total > MAX_TOTAL_BYTES:
            raise AutofixError("artifact exceeds the total size limit")
        if hashlib.sha256(source.read_bytes()).hexdigest() != entry["sha256"]:
            raise AutofixError(f"{path}: digest does not match the manifest")
    for path in manifest["deleted"]:
        if not isinstance(path, str) or path in seen or not path_allowed(path, patterns):
            raise AutofixError(f"{path!r} is not an allowed derived-file path")
        seen.add(path)
    return manifest


def commit_message(gates: list[str]) -> tuple[str, str]:
    headline = "Regenerate stale derived files"
    body = (
        f"CI found committed derived files out of date ({', '.join(gates)}). This commit is the output of the generators\n"
        "that the failed gates name, applied by the autofix workflow after the same gates passed on the regenerated tree.\n"
        "\n"
        f"{TRAILER}\n"
        f"Signed-off-by: {BOT_NAME} <{BOT_EMAIL}>"
    )
    return headline, body


def cmd_validate(args: argparse.Namespace) -> int:
    artifact = Path(args.artifact)
    gates = dict(read_table(Path(args.gates))) if args.gates else dict(read_table(GATES_FILE))
    manifest = load_manifest(artifact, read_paths(Path(args.paths)), gates)
    if manifest["head_sha"] != args.expected_head:
        raise AutofixError(f"the artifact was generated for {manifest['head_sha']}, the branch head is {args.expected_head}")
    headline, body = commit_message(manifest["gates"])
    additions = [
        {"path": entry["path"], "contents": base64.b64encode((artifact / "files" / entry["path"]).read_bytes()).decode("ascii")}
        for entry in manifest["files"]
    ]
    request = {
        "query": (
            "mutation($input: CreateCommitOnBranchInput!) {"
            " createCommitOnBranch(input: $input) { commit { oid url } } }"
        ),
        "variables": {
            "input": {
                "branch": {"repositoryNameWithOwner": args.repo, "branchName": args.branch},
                "expectedHeadOid": args.expected_head,
                "message": {"headline": headline, "body": body},
                "fileChanges": {"additions": additions, "deletions": [{"path": path} for path in manifest["deleted"]]},
            }
        },
    }
    Path(args.out).write_text(json.dumps(request), encoding="utf-8")
    print(f"autofix: {len(additions)} addition(s), {len(manifest['deleted'])} deletion(s) for {', '.join(manifest['gates'])}")
    return 0


def cmd_comment(args: argparse.Namespace) -> int:
    gates = dict(read_table(Path(args.gates)))
    # Only gate names and generator commands from the trusted table reach the comment, never text from the artifact.
    manifest = json.loads((Path(args.artifact) / "manifest.json").read_text(encoding="utf-8"))
    named = [g for g in manifest.get("gates", []) if isinstance(g, str) and g in gates]
    if not named:
        raise AutofixError("the artifact names no known gate")
    commands: list[str] = []
    for gate, command in read_table(Path(args.gates)):
        if gate in named and command not in commands:
            commands.append(command)
    lines = [
        "CI's bookkeeping job found committed derived files out of date: " + ", ".join(f"`{g}`" for g in named) + ".",
        "",
        "A pull request from a fork cannot receive an automatic fix commit. Regenerate them with `make generate` (the full set), or only the ones that failed:",
        "",
        "```sh",
        *commands,
        "```",
        "",
        "Review and commit the changed files (`git commit --signoff -S`), then push.",
    ]
    print("\n".join(lines))
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("failed-gates")
    p.add_argument("log")
    p = sub.add_parser("prepare")
    p.add_argument("--log", required=True)
    p.add_argument("--out", required=True)
    p.add_argument("--head-sha", required=True)
    p.add_argument("--gates", default=str(GATES_FILE))
    p.add_argument("--paths", default=str(PATHS_FILE))
    p = sub.add_parser("validate")
    p.add_argument("--artifact", required=True)
    p.add_argument("--paths", required=True)
    p.add_argument("--gates")
    p.add_argument("--repo", required=True)
    p.add_argument("--branch", required=True)
    p.add_argument("--expected-head", required=True)
    p.add_argument("--out", required=True)
    p = sub.add_parser("comment")
    p.add_argument("--artifact", required=True)
    p.add_argument("--gates", default=str(GATES_FILE))
    args = parser.parse_args(argv)
    try:
        if args.command == "failed-gates":
            print("\n".join(failed_gates(Path(args.log).read_text(encoding="utf-8", errors="replace"))))
            return 0
        return {"prepare": cmd_prepare, "validate": cmd_validate, "comment": cmd_comment}[args.command](args)
    except AutofixError as error:
        print(f"autofix: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
