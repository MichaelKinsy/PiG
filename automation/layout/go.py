#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Move implementation trees and rewrite parsed Go literals, not arbitrary text."""
import argparse
import json
import posixpath
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys

import plan

HERE = Path(__file__).resolve().parent
MODULE = "github.com/MichaelKinsy/PiG"
MOVES = plan.package_moves()
# Keep fixture paths equal to the governance moves in docs.py and build.py.
DOC_MOVES = {
    "CODE_OF_CONDUCT.md": ".github/CODE_OF_CONDUCT.md",
    "CONTRIBUTING.md": ".github/CONTRIBUTING.md",
    "SECURITY.md": ".github/SECURITY.md",
    "SUPPORT.md": ".github/SUPPORT.md",
    "GOVERNANCE.md": "docs/project/GOVERNANCE.md",
    "MAINTAINERS.md": "docs/project/MAINTAINERS.md",
    "QUICKSTART.md": "docs/project/QUICKSTART.md",
}
IMPORT = re.compile(re.escape(MODULE) + r"/((?:agent|ai|coding|tui)(?:/[\w.-]+)*)(?=$|[/#.\s\"`])")
# These tools store repository paths, including synthetic repository fixtures.
PATH_OWNERS = ("parity/", "automation/ci/", "automation/release/", "tests/docs-drift/", "tests/upstream-parity/", "tests/ci-images/", "cmd/gen-models/", "cmd/gen-image-models/", "coding/pigletbuild/", "coding/extension/host/subprocess/", "ai/registry_test.go")
REPO_PATH = re.compile(r'(?<!pkg:)(?<![\w/.-])(\./|/)?(agent|ai|coding|tui)/(?!src/|test/)([\w.*-][\w./*-]*)')
# Directory-name literals in these files are repository scan roots, not Pi package names.
ROOT_LITERALS = {
    "parity/runner/freshness.go": {"agent", "ai", "coding"},
    "automation/ci/divguard/check_agent_run_outside_session.go": {"coding"},
}
# These assertions encode the old public layout rather than simple paths.
SOURCE_REPLACEMENTS = {
    "embed.go": [
        ('// This is the only file at module root; runtime code lives in cmd/, internal/,\n// coding/. Its sole purpose is to host `//go:embed` directives that need',
         '// This is the only Go file at the module root. Runtime code lives in cmd/ and\n// internal/. This package hosts `//go:embed` directives that need'),
    ],
    "coding/doc.go": [
        ('// Package coding is the public SDK for embedding the pig coding\n// agent as a Go library.',
         '// Package coding provides the in-tree coding-agent Session and Runtime APIs.\n// External Go extension authors use the separate extensions/sdk module.'),
        ('// # Quick start', '// # In-tree quick start'),
        ('// The package exposes three public types that compose into a working', '// The package exposes three types that compose into a working'),
        ('//   - examples/sdk/main.go: minimal embedding example', '//   - examples/sdk/main.go: minimal in-tree session example'),
    ],
    "cmd/pig/main.go": [
        ('// Construct the SDK Services container. Mirrors what library\n\t// consumers of github.com/MichaelKinsy/PiG/internal/coding will do;\n\t// the binary uses the same path so SDK + CLI share the same\n\t// startup semantics.', '// Construct the shared Services container for all execution modes.'),
    ],
    "coding/extension/doc.go": [
        ('// coding/extension/host/ and depend on this package, never the reverse.', '// internal/coding/extension/host/ and depend on this package, never the reverse.'),
    ],
    "tests/upstream-parity/port_map_drift_test.go": [
        ('filepath.Join(root, "ai"', 'filepath.Join(root, "internal", "ai"'),
    ],
    "tests/upstream-parity/public_package_layout_test.go": [
        ('func TestPublicPackageDocumentation(', 'func TestPackageDocumentation('),
        ('func TestPublicPackageLayout(', 'func TestImplementationPackageLayout('),
        ('[]string{"agentcore", "internal/tui", "pigproc", "resources", "coding/presentation", "schema/gui-extension"}',
         '[]string{"agent", "ai", "coding", "tui", "agentcore", "pigproc", "resources", "internal/coding/presentation", "schema/gui-extension"}'),
        ('[]string{"agent", "ai", "coding", "tui", "piglets/standard", "piglets/porter"}',
         '[]string{"internal/agent", "internal/ai", "internal/coding", "internal/tui", "extensions/sdk", "piglets/standard", "piglets/porter"}'),
        ('"public path %s is not a directory"', '"current path %s is not a directory"'),
    ],
}


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args])


def moved(path):
    return plan.move_path(path, MOVES)


def quote(value, original):
    if original.startswith(b"`") and "`" not in value and "\r" not in value:
        return ("`" + value + "`").encode()
    # JSON quoting is also valid Go quoting, except that Go has no \/ escape.
    return json.dumps(value, ensure_ascii=False).encode()


def repository_reference(match, tracked):
    path = match[2] + "/" + match[3]
    if tracked is not None:
        root = path.removesuffix("/...").rstrip("/")
        # Synthetic Go scanner fixtures still name source files. Other paths must belong to the checkout, not an extension's runtime storage.
        if match[1] != "./" and path not in tracked and not path.endswith(".go") and not any(name.startswith(root + "/") for name in tracked):
            return match[0]
    return (match[1] or "") + moved(path)


def rewrite_go(path, data, literals, relocating, comments=None, tracked=None, moving_layout=False):
    edits = []
    for comment in comments or []:
        changed = IMPORT.sub(lambda m: MODULE + "/" + moved(m[1]), comment["Value"])
        if tracked is not None and not path.startswith("extensions/sdk/"):
            changed = REPO_PATH.sub(lambda m: repository_reference(m, tracked), changed)
        if changed != comment["Value"]:
            edits.append((comment["Start"], comment["End"], changed.encode()))
    for lit in literals or []:
        value = lit["Value"]
        changed = IMPORT.sub(lambda m: MODULE + "/" + moved(m[1]), value)
        if not lit["Import"]:
            if path.startswith("tests/docs-drift/") and value in DOC_MOVES:
                changed = DOC_MOVES[value]
            if path.startswith(PATH_OWNERS):
                changed = REPO_PATH.sub(lambda m: repository_reference(m, tracked), changed)
                for old in MOVES:
                    if changed in ("./" + old, old + "/"):
                        changed = changed.replace(old, MOVES[old], 1)
            if value in ROOT_LITERALS.get(path, set()):
                changed = moved(value)
            if path == "ai/registry_test.go" and value in ("ai/models_generated.go", "ai/image_models_generated.go"):
                changed = moved(value)
            if relocating and value.startswith("../.upstream/"):
                changed = "../" + value
            if moving_layout and tracked is not None and value.startswith(("../", "./")):
                # Only repository-backed references qualify; relative user paths are not source paths.
                target = posixpath.normpath(posixpath.join(str(PurePosixPath(path).parent), value))
                if target in tracked and (moved(target) != target or moved(path) != path):
                    rebased = posixpath.relpath(moved(target), str(PurePosixPath(moved(path)).parent))
                    changed = rebased if rebased.startswith(".") else "./" + rebased
                    if value.endswith("/"):
                        changed += "/"
        if changed != value:
            edits.append((lit["Start"], lit["End"], quote(changed, data[lit["Start"]:lit["End"]])))
    for start, end, replacement in sorted(edits, reverse=True):
        data = data[:start] + replacement + data[end:]
    text = data.decode()
    # Explicit structural path forms: only package directories followed by a
    # repository child. Never rewrite ~/.pig/agent, upstream packages/ai, or IDs.
    def joined(match):
        parts = re.findall(r'"([^"\n]+)"', match[0])
        path = "/".join(parts)
        changed = moved(path)
        return ", ".join(json.dumps(p) for p in changed.split("/")) if path != changed else match[0]

    text = re.sub(r'(?<!"internal", )"(?:coding|tui)", "(?:extension|pigletbuild|pigversion|testdata|upstream\.go|runtime\.go|theme_dark\.json|theme_light\.json)"', joined, text)
    if path.startswith(("parity/", "automation/ci/")):
        text = re.sub(r'(?<!"internal", )"(?:ai|agent)", "(?:sse\.go|agent\.go)"', joined, text)
    if relocating:
        depth = len(PurePosixPath(path).parent.parts)
        parents = ", ".join(['".."'] * depth)
        # The complete source-directory-to-root traversal gains one component.
        text = re.sub(r'(filepath\.Join\()' + re.escape(parents) + r'(?=,|\))', lambda m: m[1] + '"..", ' + parents, text)
        if depth > 1:
            collapsed = "/".join([".."] * depth)
            text = text.replace('filepath.Join("' + collapsed + '",', 'filepath.Join("../' + collapsed + '",')
        if depth == 1:
            text = text.replace('filepath.Abs("..")', 'filepath.Abs("../..")')
        text = text.replace("//go:generate ../automation/", "//go:generate ../../automation/")
    for old, new in SOURCE_REPLACEMENTS.get(path, []):
        if old in text:
            text = text.replace(old, new)
        elif new not in text:
            raise ValueError(f"{path}: required source rewrite anchor is missing: {old}")
    return text.encode()


def rewrite_module(path, data):
    # Module identities do not change. Rebase local replacements against the
    # relocated module directory, including fixtures added after this codemod.
    old_dir = str(PurePosixPath(path).parent)
    new_dir = str(PurePosixPath(moved(path)).parent)

    def replacement(match):
        raw = match[2]
        value = json.loads(raw) if raw.startswith('"') else raw
        if not value.startswith(("./", "../")):
            return match[0]
        target = posixpath.normpath(posixpath.join(old_dir, value))
        rebased = posixpath.relpath(moved(target), new_dir)
        if not rebased.startswith("."):
            rebased = "./" + rebased
        if raw.startswith('"'):
            rebased = json.dumps(rebased)
        return match[1] + rebased

    return re.sub(r'(=>[ \t]+)("[^"\n]+"|[^ \t\r\n]+)', replacement, data.decode()).encode()


def reject_symlink(root, name):
    path = root
    for part in PurePosixPath(name).parts:
        path = path / part
        if path.is_symlink():
            raise ValueError(f"expected regular tracked path, not a symlink: {name}")


def apply(root, keep_public_libraries=False):
    replacements = SOURCE_REPLACEMENTS
    if keep_public_libraries:
        replacements = {
            "tests/upstream-parity/public_package_layout_test.go": [
                ('"internal/tui"', '"internal/tui/tui.go"'),
            ],
        }
    with plan.selected(globals(), MOVES=plan.package_moves(keep_public_libraries), SOURCE_REPLACEMENTS=replacements):
        return apply_selected(root, keep_public_libraries)


def apply_selected(root, keep_public_libraries):
    root = root.resolve()
    if Path(git(root, "rev-parse", "--show-toplevel").decode().strip()).resolve() != root:
        raise ValueError("repo-root must be the Git checkout root")
    paths = git(root, "ls-files", "-z").decode().rstrip("\0").split("\0")
    if keep_public_libraries:
        for name in plan.PUBLIC_ROOTS:
            reject_symlink(root, name)
            if not (root / name).is_dir():
                raise ValueError(f"public library root is missing: {name}")
    states = []
    for old, new in MOVES.items():
        reject_symlink(root, old)
        reject_symlink(root, new)
        source, target = root / old, root / new
        if source.exists() == target.exists():
            raise ValueError(f"expected exactly one of {old} and {new}; missing path or move collision")
        existing = old if source.exists() else new
        if not any(p.startswith(existing + "/") for p in paths):
            raise ValueError(f"{existing}: no tracked files")
        states.append(source.exists())
    if len(set(states)) != 1:
        raise ValueError("partially moved tree: all selected packages must be in the same layout")
    relocating = states[0]
    for required in ("go.mod", "go.work", "extensions/sdk/go.mod", "embed.go", "CHANGELOG.md"):
        reject_symlink(root, required)
        if required not in paths or not (root / required).is_file():
            raise ValueError(f"required tracked file is missing: {required}")
    # Do not silently leave newly added implementation packages public.
    for path in paths:
        if "/" not in path and path.endswith(".go") and path != "embed.go":
            raise ValueError(f"unclassified root Go source: {path}; extend the move plan")
    go_paths = [p for p in paths if p.endswith(".go") and not p.startswith("automation/layout/")]
    for path in paths:
        if path.endswith((".go", "go.mod", "go.work")) or moved(path) != path:
            reject_symlink(root, path)
    scanned = subprocess.run(["go", "run", str(HERE / "scan/main.go")], cwd=root,
                             input=json.dumps(go_paths), text=True, capture_output=True, check=True)
    changes = {}
    moved_roots = tuple(new + "/" for new in MOVES.values())
    tracked = {p.removeprefix("internal/") if p.startswith(moved_roots) else p for p in paths}
    for file in json.loads(scanned.stdout):
        path = file["Path"]
        original = (root / path).read_bytes()
        logical = path
        if not relocating:
            for old, new in MOVES.items():
                if path.startswith(new + "/"):
                    logical = old + path[len(new):]
        updated = rewrite_go(logical, original, file["Literals"], relocating and moved(path) != path, file["Comments"], tracked, relocating)
        if updated != original:
            if path.startswith("extensions/sdk/"):
                raise ValueError(f"public SDK would change: {path}")
            changes[path] = updated
    if relocating:
        for path in paths:
            if path.endswith(("go.mod", "go.work")):
                original = (root / path).read_bytes()
                updated = rewrite_module(path, original)
                if updated != original:
                    changes[path] = updated
    # Every parse and precondition check completes before any file changes.
    for path, content in sorted(changes.items()):
        (root / path).write_bytes(content)
    count = sum(moved(p) != p for p in paths) if relocating else 0
    if relocating:
        (root / "internal").mkdir(exist_ok=True)
        for old, new in MOVES.items():
            (root / new).parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(["git", "-C", str(root), "mv", "--", old, new], check=True)
    go_changed = [moved(p) if relocating else p for p in changes if p.endswith(".go")]
    if go_changed:
        subprocess.run(["gofmt", "-w", *go_changed], cwd=root, check=True)
    print(f"layout-go: moved {count} tracked files; rewrote {len(changes)} files")


if __name__ == "__main__":
    try:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument("--keep-public-libraries", action="store_true")
        parser.add_argument("root", type=Path)
        args = parser.parse_args()
        apply(args.root, args.keep_public_libraries)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f"layout-go: {error}", file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr, file=sys.stderr)
        sys.exit(1)
