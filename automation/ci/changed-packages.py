#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Select the Go test packages a change can affect.

Usage: changed-packages.py [--base REF] [--json] [--summary FILE] [--committed]

Compares HEAD, the index, the working tree and untracked files with the merge
base of HEAD and REF, and maps each changed file to Go packages of the main
module:

- direct: the package whose directory holds a changed Go file, the package
  that embeds a changed file, the package whose directory (or a testdata
  directory beneath it) holds a changed file, and a package whose Go source
  names the changed file's repository path, as one string or as the quoted
  components of a filepath.Join call, and, for a file in changelog.d (the
  fragments the changelog, release-notes and bug-ledger tools and the docs
  tests list by globbing the directory), a package whose Go source names
  changelog.d.
- affected: every main-module package whose tests import a direct package,
  directly or transitively (the reverse closure of `go list -test -deps`),
  plus the direct packages themselves.

A change to go.mod, go.sum, go.work or go.work.sum affects every package. A changed file
no rule maps is listed as unmapped: no package test reads it by any path this
script can see, so only the full suite in CI covers it.

REF defaults to $TEST_BASE, then the branch's upstream (@{upstream}), then
main. The plain output prints one `<kind> <import path or file>` line per
entry, where kind is direct, affected, tagged, unmapped or module-wide (a
changed go.mod, go.sum, go.work or go.work.sum); --json prints one object.
--committed leaves out the index, the working tree and untracked files, for a CI job whose build steps
leave files behind. --summary FILE also appends the counts and lists as Markdown to FILE (CI passes
$GITHUB_STEP_SUMMARY). A tagged package exists only under a build tag (parity,
integration) and runs through its own make target.
"""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath

MODULE_WIDE = {"go.mod", "go.sum", "go.work", "go.work.sum"}

# Top-level directories whose every file is read by listing the directory, so no Go source names a file in it by path. A changed file in one belongs to each package whose Go source names the directory (the tools and tests that glob it). Only a directory of such files may be listed: a directory like docs or test names a whole area, and its name would select most packages.
NAMED_DIRECTORIES = ("changelog.d",)


def git(*args: str) -> str:
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def resolve_base(explicit: str | None) -> str:
    candidates = [explicit] if explicit else [os.environ.get("TEST_BASE"), "@{upstream}", "main"]
    for ref in candidates:
        if not ref:
            continue
        probe = subprocess.run(["git", "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}"], capture_output=True, text=True)
        if probe.returncode == 0:
            return ref
        if explicit:
            break
    raise SystemExit(f"changed-packages: no base commit among {', '.join(c for c in candidates if c)}; set TEST_BASE")


def changed_files(base: str, committed_only: bool) -> list[str]:
    merge_base = git("merge-base", "HEAD", base).strip()
    names = set()
    diffs = (("diff", "--name-only", "--no-renames", f"{merge_base}...HEAD"),)
    if not committed_only:
        diffs += (("diff", "--name-only", "--no-renames"), ("diff", "--name-only", "--no-renames", "--cached"), ("ls-files", "--others", "--exclude-standard"))
    for args in diffs:
        names.update(line for line in git(*args).splitlines() if line)
    return sorted(names)


# Map files with every build tag a maintained test uses, so a change to a tagged-only package is reported instead of unmapped.
MAPPING_TAGS = "integration,live,parity"


def go_list(root: Path) -> list[dict]:
    fields = "ImportPath,ForTest,Dir,Module,Deps,EmbedFiles,TestEmbedFiles,XTestEmbedFiles,GoFiles,TestGoFiles,XTestGoFiles,IgnoredGoFiles,CgoFiles"
    out = subprocess.run(["go", "list", "-e", "-test", "-deps", f"-tags={MAPPING_TAGS}", f"-json={fields}", "./..."], cwd=root, check=True, capture_output=True, text=True).stdout
    decoder = json.JSONDecoder()
    packages, index = [], 0
    while index < len(out):
        while index < len(out) and out[index].isspace():
            index += 1
        if index >= len(out):
            break
        value, index = decoder.raw_decode(out, index)
        packages.append(value)
    return packages


def strip_variant(path: str) -> str:
    return path.split(" [", 1)[0]


def owner_of(pkg: dict) -> str:
    """Return the package a test variant or external test package belongs to."""
    return pkg.get("ForTest") or strip_variant(pkg["ImportPath"])


def select(root: Path, files: list[str], packages: list[dict], main_module: str, runnable: set[str]) -> dict:
    root = root.resolve()
    by_dir: dict[str, str] = {}
    embeds: dict[str, set[str]] = {}
    sources: dict[str, dict[Path, None]] = {}
    tested: set[str] = set()
    for pkg in packages:
        path = owner_of(pkg)
        directory = pkg.get("Dir")
        if not directory or path.endswith(".test"):
            continue
        try:
            rel = Path(directory).resolve().relative_to(root).as_posix()
        except ValueError:
            continue
        by_dir.setdefault(rel, path)
        if (pkg.get("Module") or {}).get("Path") == main_module:
            tested.add(path)
        for key in ("EmbedFiles", "TestEmbedFiles", "XTestEmbedFiles"):
            for name in pkg.get(key) or []:
                embeds.setdefault(f"{rel}/{name}" if rel != "." else name, set()).add(path)
        for key in ("GoFiles", "TestGoFiles", "XTestGoFiles", "IgnoredGoFiles", "CgoFiles"):
            for name in pkg.get(key) or []:
                sources.setdefault(path, {})[Path(directory) / name] = None

    direct: set[str] = set()
    unmapped: list[str] = []
    module_wide: list[str] = []
    for name in files:
        if name in MODULE_WIDE:
            module_wide.append(name)
            continue
        hits = set(embeds.get(name, ()))
        parent = Path(name).parent.as_posix()
        if name.endswith(".go"):
            if parent in by_dir:
                hits.add(by_dir[parent])
        # A non-Go file belongs to the nearest enclosing package directory other than the module root; the root package reaches files only through its embeds.
        probe = parent
        while probe not in ("", "."):
            if probe in by_dir:
                hits.add(by_dir[probe])
                break
            probe = Path(probe).parent.as_posix()
        if not name.endswith(".go"):
            hits.update(referencing(name, sources, tested))
        if hits:
            direct.update(hits)
        else:
            unmapped.append(name)

    everything = bool(module_wide)
    affected: set[str] = set()
    for pkg in packages:
        owner = owner_of(pkg)
        if owner not in tested or owner.endswith(".test"):
            continue
        if everything or owner in direct or strip_variant(pkg["ImportPath"]) in direct or any(strip_variant(dep) in direct for dep in pkg.get("Deps") or []):
            affected.add(owner)
    if everything:
        affected = set(tested)
    return {
        "direct": sorted(direct & tested & runnable),
        "affected": sorted(affected & runnable),
        # Packages whose every file needs a build tag run through their own targets (make parity-family, make test-integration), not go test without tags.
        "tagged": sorted((affected & tested) - runnable),
        "unmapped": sorted(unmapped),
        "module_wide": module_wide,
    }


_TEXT_CACHE: dict[Path, str] = {}


def source_text(source: Path) -> str | None:
    """Read a Go source once per run: the test variants of a package list the same file, and every changed non-Go file scans every source."""
    if source not in _TEXT_CACHE:
        try:
            _TEXT_CACHE[source] = source.read_text(errors="replace")
        except OSError:
            return None
    return _TEXT_CACHE[source]


def referencing(name: str, sources: dict[str, dict[Path, None]], tested: set[str]) -> set[str]:
    """Return main-module packages whose Go source spells the file's repository path, or its directory when that has three or more components (path_pattern)."""
    patterns = [path_pattern(PurePosixPath(name))]
    parent = PurePosixPath(name).parent
    if parent.parts[:1] and parent.parts[0] in NAMED_DIRECTORIES:
        patterns.append(re.escape(parent.parts[0]))
    # A directory of three or more components names one fixture tree (test/parity/scenarios/rpc); a shorter one names a whole area of the repository and would select most packages.
    if len(parent.parts) >= 3:
        patterns.append(path_pattern(parent))
    needle = re.compile("|".join(patterns))
    # Every pattern spells the file's last component, or its directory's last component, so a source naming neither cannot match; a substring test skips the regular expression for nearly every source.
    literals = [PurePosixPath(name).name] + ([parent.name] if len(parent.parts) >= 3 else []) + ([parent.parts[0]] if parent.parts[:1] and parent.parts[0] in NAMED_DIRECTORIES else [])
    hits = set()
    for path, files in sources.items():
        if path not in tested:
            continue
        for source in files:
            text = source_text(source)
            if text is not None and any(lit in text for lit in literals) and needle.search(text):
                hits.add(path)
                break
    return hits


def path_pattern(path: PurePosixPath) -> str:
    """Return a regular expression for the ways Go source names a repository path.

    A path of two or more components matches anywhere, comments included, as one slash-separated string or as filepath.Join's quoted components ("docs", "x.md"). A root file's bare name ("Makefile", "README.md") also names files of other directories and other things, so it matches only as a relative path ("../../Makefile") or a later filepath.Join argument (root, "Makefile").
    """
    joined = re.escape(", ".join(f'"{part}"' for part in path.parts))
    if len(path.parts) == 1:
        quoted = re.escape(path.name)
        return rf'"(?:\.\./)+{quoted}"|,\s*"{quoted}"'
    return rf"{re.escape(path.as_posix())}|{joined}"


def summary_markdown(result: dict) -> str:
    """Return the selection as Markdown: one count per kind, then each non-empty list."""
    kinds = ("direct", "affected", "tagged", "unmapped", "module_wide")
    lines = [f"### Changed-package selection against `{result['base']}`", "", "| Kind | Count |", "|---|---|"]
    lines += [f"| {kind.replace('_', '-')} | {len(result[kind])} |" for kind in kinds]
    for kind in kinds:
        if result[kind]:
            lines += ["", f"<details><summary>{kind.replace('_', '-')} ({len(result[kind])})</summary>", "", *[f"- `{entry}`" for entry in result[kind]], "", "</details>"]
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--base")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--summary", metavar="FILE")
    parser.add_argument("--committed", action="store_true", help="read only the commits since the merge base, not the index, working tree or untracked files")
    args = parser.parse_args()
    root = Path(git("rev-parse", "--show-toplevel").strip())
    os.chdir(root)
    base = resolve_base(args.base)
    files = changed_files(base, args.committed)
    # In workspace mode go list -m prints every workspace module; the main module is the one rooted here.
    modules = subprocess.run(["go", "list", "-m", "-f", "{{.Path}}\t{{.Dir}}"], cwd=root, check=True, capture_output=True, text=True).stdout.splitlines()
    main_module = next((path for path, _, directory in (line.partition("\t") for line in modules) if directory and Path(directory).resolve() == root.resolve()), "")
    if not main_module:
        raise SystemExit(f"changed-packages: no module is rooted at {root}")
    runnable = set(subprocess.run(["go", "list", "./..."], cwd=root, check=True, capture_output=True, text=True).stdout.split())
    result = select(root, files, go_list(root) if files else [], main_module, runnable)
    result["base"] = base
    result["files"] = files
    if args.summary:
        with open(args.summary, "a", encoding="utf-8") as out:
            out.write(summary_markdown(result))
    if args.json:
        json.dump(result, sys.stdout, indent=2)
        sys.stdout.write("\n")
        return 0
    for kind in ("direct", "affected", "tagged", "unmapped", "module_wide"):
        for entry in result[kind]:
            print(kind.replace("_", "-"), entry)
    return 0


if __name__ == "__main__":
    sys.exit(main())
