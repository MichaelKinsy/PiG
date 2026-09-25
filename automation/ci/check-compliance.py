#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Check the repository's compliance evidence locally. Behind `make compliance`.

Every check runs, every failure is printed, and the exit status is 1 if any
check fails. docs/project/compliance.md maps each check to the badge or
OpenSSF criterion it supports.

Checks:
  files        required community, security, and metadata files exist
  citation     CITATION.cff has the required CFF 1.2.0 fields and names this repository
  actions      every workflow action is pinned to a full commit SHA
  permissions  every workflow's top-level token permissions are read-only
  pins         every copy of the Go, Node, Rust, and Pi pins matches its one source
  reuse        `reuse lint` passes (version pinned in security.yml)
  govulncheck  `go tool govulncheck ./...` reports no reachable vulnerability
               (GOVULNDB=file:///path selects a local vulndb.zip copy)

Options:
  --skip NAME  skip one check (repeatable); skipped checks are reported as such
"""
from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]

REQUIRED_FILES = [
    "LICENSE",
    "SECURITY.md",
    "CONTRIBUTING.md",
    "CODE_OF_CONDUCT.md",
    "GOVERNANCE.md",
    "MAINTAINERS.md",
    "CHANGELOG.md",
    "CITATION.cff",
    ".github/dependabot.yml",
    ".github/security-insights.yml",
]

SHA_PIN = re.compile(r"^[^@\s]+@[0-9a-f]{40}$")
USES = re.compile(r"^\s*(?:-\s+)?uses:\s*['\"]?([^'\"\s#]+)")


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def search(pattern: str, path: str, label: str) -> str:
    match = re.search(pattern, read(path), re.MULTILINE)
    if not match:
        raise LookupError(f"{path}: cannot find {label}")
    return match.group(1)


def check_files() -> list[str]:
    return [f"missing required file {name}" for name in REQUIRED_FILES if not (ROOT / name).is_file()]


def top_level_scalars(text: str) -> dict[str, str]:
    values = {}
    for line in text.splitlines():
        match = re.match(r"^([A-Za-z][\w-]*):\s*(.*?)\s*$", line)
        if match:
            values[match.group(1)] = match.group(2).strip("'\"")
    return values


def check_citation() -> list[str]:
    text = read("CITATION.cff")
    values = top_level_scalars(text)
    problems = []
    for key in ["cff-version", "message", "title", "authors", "license", "repository-code"]:
        if key not in values:
            problems.append(f"CITATION.cff: missing {key}")
    if values.get("cff-version") != "1.2.0":
        problems.append(f"CITATION.cff: cff-version = {values.get('cff-version')!r}, want '1.2.0'")
    if values.get("type", "software") not in {"software", "dataset"}:
        problems.append(f"CITATION.cff: type = {values.get('type')!r}, want software or dataset")
    authors = re.search(r"^authors:\s*\n((?:[ \t]+.*\n?)+)", text, re.MULTILINE)
    if not authors or not re.search(r"^\s+- (family-names|name):\s*\S", authors.group(1), re.MULTILINE):
        problems.append("CITATION.cff: authors must list at least one person or entity")
    module = search(r"^module (\S+)$", "go.mod", "module path")
    repository = "https://" + module
    for key in ["repository-code", "url"]:
        if key in values and values[key] != repository:
            problems.append(f"CITATION.cff: {key} = {values[key]!r}, want {repository!r} (go.mod module)")
    license_titles = {"MIT": "MIT License"}
    title = license_titles.get(values.get("license", ""))
    if title is None:
        problems.append(f"CITATION.cff: license {values.get('license')!r} is not the repository license (MIT)")
    elif title not in read("LICENSE"):
        problems.append(f"CITATION.cff: LICENSE is not the {title}")
    return problems


def workflows() -> list[pathlib.Path]:
    return sorted((ROOT / ".github/workflows").glob("*.y*ml"))


def check_actions() -> list[str]:
    problems = []
    for path in workflows() + sorted((ROOT / ".github/actions").rglob("action.y*ml")):
        for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            match = USES.match(line)
            if not match:
                continue
            ref = match.group(1)
            if ref.startswith("./") or (ref.startswith("docker://") and "@sha256:" in ref):
                continue
            if not SHA_PIN.match(ref):
                problems.append(f"{path.relative_to(ROOT)}:{number}: {ref} is not pinned to a full commit SHA")
    return problems


def check_permissions() -> list[str]:
    problems = []
    for path in workflows():
        lines = path.read_text(encoding="utf-8").splitlines()
        name = path.relative_to(ROOT)
        index = next((i for i, line in enumerate(lines) if re.match(r"^permissions:", line)), None)
        if index is None:
            problems.append(f"{name}: no top-level permissions (the default token may be read-write)")
            continue
        inline = lines[index].split(":", 1)[1].split("#", 1)[0].strip()
        if inline:
            if inline not in {"read-all", "{}"}:
                problems.append(f"{name}: top-level permissions: {inline} is not read-only")
            continue
        for line in lines[index + 1 :]:
            if line and not line.startswith((" ", "\t", "#")):
                break
            match = re.match(r"^\s+([\w-]+):\s*(\S+)", line)
            if match and match.group(2) not in {"read", "none"}:
                problems.append(f"{name}: top-level permission {match.group(1)}: {match.group(2)} is not read-only")
    return problems


def check_pins() -> list[str]:
    go = search(r"^toolchain go(\S+)$", "go.mod", "toolchain directive")
    node = read(".node-version").strip()
    node_runtime = search(
        r'^const minimumNodeRuntimeVersion = "v([^"]+)"',
        "coding/extension/host/subprocess/builder_node.go",
        "minimumNodeRuntimeVersion",
    ).removesuffix(".0")
    pi = search(r'^const UpstreamVersion = "([^"]+)"', "coding/pigversion/pigversion.go", "UpstreamVersion")
    rust = search(r"^\s+RUST_VERSION:\s*(\S+)", ".github/workflows/ci.yml", "RUST_VERSION")

    def minor(version: str) -> str:
        return ".".join(version.split(".")[:2])

    devcontainer = json.loads(read(".devcontainer/devcontainer.json"))["features"]
    sdk = json.loads(read("extensions/sdk-ts/package.json"))
    oracle = json.loads(read("automation/images/ci-parity/package.json"))["dependencies"]
    parity_tag = search(r"^IMAGE_TAG=(\S+)$", "automation/images/ci-parity/metadata.env", "IMAGE_TAG")
    go_tag = search(r"^IMAGE_TAG=(\S+)$", "automation/images/ci-go/metadata.env", "IMAGE_TAG")
    copies = [
        ("Go", go, ".devcontainer/devcontainer.json go feature", devcontainer["ghcr.io/devcontainers/features/go:1"]["version"]),
        ("Go", go, "ci-parity Dockerfile ARG GO_VERSION", search(r"^ARG GO_VERSION=(\S+)$", "automation/images/ci-parity/Dockerfile", "GO_VERSION")),
        ("Go", go, "ci-parity image tag", re.match(r"go([\d.]+)-", parity_tag).group(1)),
        ("Go", go, "ci-go GO_IMAGE", search(r"^ARG GO_IMAGE=golang:([\d.]+)-", "automation/images/ci-go/Dockerfile", "GO_IMAGE")),
        ("Go", go, "ci-go image tag", re.match(r"go([\d.]+)-", go_tag).group(1)),
        ("Node", node, ".devcontainer/devcontainer.json node feature", devcontainer["ghcr.io/devcontainers/features/node:1"]["version"]),
        ("Node", minor(node), "ci-parity image tag", re.search(r"-node([\d.]+)-", parity_tag).group(1)),
        ("Node extension runtime", node_runtime, "automation/dev/setup.sh extension runtime floor", search(r"Node >= ([\d.]+) is required", "automation/dev/setup.sh", "Node extension runtime floor")),
        ("Node extension runtime", node_runtime, "cmd/pig/setup_command.go extension runtime floor", search(r"install Node.js ([\d.]+) or newer", "cmd/pig/setup_command.go", "Node extension runtime floor")),
        ("Node extension runtime", node_runtime, "internal troubleshooting extension runtime floor", search(r"Node and TypeScript extensions \| Node.js ([\d.]+) or newer", "internal/pigdocs/content/troubleshooting.md", "Node extension runtime floor")),
        ("Node extension runtime", node_runtime, "site troubleshooting extension runtime floor", search(r"Node and TypeScript extensions \| Node.js ([\d.]+) or newer", "docs/site/docs/install-troubleshooting.md", "Node extension runtime floor")),
        ("Rust", rust, ".devcontainer/devcontainer.json rust feature", devcontainer["ghcr.io/devcontainers/features/rust:1"]["version"]),
        ("Rust", minor(rust), "ci-parity image tag", re.search(r"-rust([\d.]+)-", parity_tag).group(1)),
        ("Pi", pi, "extensions/sdk-ts devDependency", sdk.get("devDependencies", {}).get("@earendil-works/pi-coding-agent", "")),
        ("Pi", pi, "extensions/sdk-ts peerDependency", sdk.get("peerDependencies", {}).get("@earendil-works/pi-coding-agent", "")),
        ("Pi", pi, "ci-parity Dockerfile ARG PI_VERSION", search(r"^ARG PI_VERSION=(\S+)$", "automation/images/ci-parity/Dockerfile", "PI_VERSION")),
        ("Pi", pi, "ci-parity image tag", re.search(r"-pi([\d.]+)-", parity_tag).group(1)),
        ("Pi", pi, "parity/known-gaps.toml pi_version", search(r'^pi_version = "([^"]+)"', "parity/known-gaps.toml", "pi_version")),
        ("Pi", pi, "parity/behavior-contracts.toml upstream_version", search(r'^upstream_version = "([^"]+)"', "parity/behavior-contracts.toml", "upstream_version")),
        ("Pi", pi, "README Pi pin badge", search(r"img\.shields\.io/badge/Pi%20pin-([\d.]+)-", "README.md", "Pi pin badge")),
        ("Pi", pi, "README Pi pin badge text", search(r"\[!\[Pi pin ([\d.]+)\]", "README.md", "Pi pin badge text")),
        ("Pi", pi, "README Pi pin badge link", search(r"earendil-works/pi/releases/tag/v([\d.]+)\)", "README.md", "Pi pin badge link")),
    ]
    for package in ["pi-agent-core", "pi-ai", "pi-coding-agent", "pi-tui"]:
        copies.append(("Pi", pi, f"ci-parity oracle {package}", oracle.get(f"@earendil-works/{package}", "")))
    version = r"(\d+(?:\.\d+)*)"
    for path, pattern in [
        ("docs/site/docs/containerization.md", rf"^FROM golang:{version} AS build"),
        ("docs/site/docs/termux.md", rf"Use Go {version} for this build\."),
        ("docs/site/docs/windows.md", rf"Install Git and Go {version}\."),
        ("internal/pigdocs/content/extension-api.md", rf"Build PiG and Go extensions with Go {version}\."),
    ]:
        copies.append(("Go", go, path, search(pattern, path, "Go version")))
    source = {
        "Go": "go.mod toolchain",
        "Node": ".node-version",
        "Node extension runtime": "coding/extension/host/subprocess/builder_node.go minimumNodeRuntimeVersion",
        "Rust": ".github/workflows/ci.yml RUST_VERSION",
        "Pi": "coding/pigversion/pigversion.go UpstreamVersion",
    }
    return [
        f"{kind} pin drift: {where} = {value!r}, want {want!r} from {source[kind]}"
        for kind, want, where, value in copies
        if value != want
    ] + check_workflow_version_files()


# Workflows never pin Go or Node by literal: each setup step reads the source.
SETUP_VERSION_FILES = {"setup-go": ("go-version", "go.mod"), "setup-node": ("node-version", ".node-version")}


def workflow_step(lines: list[str], index: int) -> list[str]:
    """Return the stripped lines of the workflow step that contains lines[index]."""
    def indent(line: str) -> int:
        return len(line) - len(line.lstrip(" "))

    start = index
    while start > 0 and not lines[start].lstrip().startswith("- "):
        start -= 1
    step = [lines[start].strip().removeprefix("- ").strip()]
    for line in lines[start + 1:]:
        if line.strip() and indent(line) <= indent(lines[start]):
            break
        step.append(line.strip())
    return step


def check_workflow_version_files() -> list[str]:
    problems = []
    setup = re.compile(r"^\s*(?:-\s+)?uses:\s*actions/(setup-go|setup-node)@")
    literal = re.compile(r"^\s*(go-version|node-version|GO_VERSION|NODE_VERSION):")
    for path in workflows():
        name = path.relative_to(ROOT)
        lines = path.read_text(encoding="utf-8").splitlines()
        for number, line in enumerate(lines, 1):
            if match := literal.match(line):
                problems.append(f"{name}:{number}: {match.group(1)} is a literal pin; read the one source with a *-version-file input")
            match = setup.match(line)
            if not match:
                continue
            key, want = SETUP_VERSION_FILES[match.group(1)]
            if f"{key}-file: {want}" not in workflow_step(lines, number - 1):
                problems.append(f"{name}:{number}: actions/{match.group(1)} does not set {key}-file: {want}")
    return problems


def run(command: list[str], label: str) -> list[str]:
    result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True)
    if result.returncode == 0:
        return []
    output = (result.stdout + result.stderr).strip().splitlines()
    return [f"{label} failed (exit {result.returncode}):"] + ["    " + line for line in output[-40:]]


def check_reuse() -> list[str]:
    requirement = search(r'"?(reuse(?:\[[\w,-]+\])?==[\d.]+)"?', ".github/workflows/security.yml", "reuse requirement")
    # Single-process linting also works in containers and sandboxes that
    # restrict POSIX semaphores; the repository lints in seconds either way.
    lint = ["reuse", "--no-multiprocessing", "lint"]
    if shutil.which("reuse"):
        return run(lint, " ".join(lint))
    if shutil.which("uvx"):
        return run(["uvx", "--from", requirement, *lint], f"uvx --from {requirement} {' '.join(lint)}")
    return [f"reuse is not installed: run `python3 -m pip install '{requirement}'` or install uv, then rerun"]


def check_govulncheck() -> list[str]:
    # GOVULNDB selects a local copy of https://vuln.go.dev/vulndb.zip for
    # offline or TLS-intercepted machines; the default is the public database.
    database = os.environ.get("GOVULNDB", "")
    command = ["go", "tool", "govulncheck"] + (["-db", database] if database else []) + ["./..."]
    return run(command, " ".join(command))


CHECKS = {
    "files": check_files,
    "citation": check_citation,
    "actions": check_actions,
    "permissions": check_permissions,
    "pins": check_pins,
    "reuse": check_reuse,
    "govulncheck": check_govulncheck,
}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--skip", action="append", default=[], choices=sorted(CHECKS))
    args = parser.parse_args()
    failed = False
    for name, check in CHECKS.items():
        if name in args.skip:
            print(f"compliance: {name:<12} SKIPPED")
            continue
        try:
            problems = check()
        except (LookupError, KeyError, AttributeError, OSError, json.JSONDecodeError) as error:
            problems = [f"check could not run: {error}"]
        if problems:
            failed = True
            print(f"compliance: {name:<12} FAIL")
            for problem in problems:
                print(f"  - {problem}")
        else:
            print(f"compliance: {name:<12} ok")
    if failed:
        print("compliance: FAILED", file=sys.stderr)
        return 1
    print("compliance: all checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
