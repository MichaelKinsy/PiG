#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Rewrite build, release, and CI references for the internal Go layout."""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess
import sys

import plan


PACKAGE_MOVES = plan.package_moves()
# Keep this table equal to the governance moves in the docs section.
DOCUMENT_MOVES = {
    "CODE_OF_CONDUCT.md": ".github/CODE_OF_CONDUCT.md",
    "CONTRIBUTING.md": ".github/CONTRIBUTING.md",
    "SECURITY.md": ".github/SECURITY.md",
    "SUPPORT.md": ".github/SUPPORT.md",
    "GOVERNANCE.md": "docs/project/GOVERNANCE.md",
    "MAINTAINERS.md": "docs/project/MAINTAINERS.md",
    "QUICKSTART.md": "docs/project/QUICKSTART.md",
}
ROOT_FILES = {
    "Makefile", ".golangci.yml", ".gitattributes", "REUSE.toml", ".gitleaks.toml",
    ".gitignore", ".dockerignore", ".coderabbit.yaml", "docs/site/public/install.sh",
}
REQUIRED_FILES = {
    "Makefile", ".golangci.yml", ".gitattributes", "REUSE.toml", ".gitleaks.toml",
    "automation/make/ci.mk", "automation/make/parity.mk",
    "automation/ci/check-public-claims.py", "automation/ci/check-port-map-drift.py",
    "automation/ci/coverage_recipe_test.py", "automation/release/npm/pack_npm.py",
    ".github/CODEOWNERS", ".github/security-insights.yml",
    ".github/workflows/ci.yml", ".github/workflows/security.yml",
}
# Only these variables designate a checkout root. In particular, $home/agent,
# $PIG_HOME/agent, $agent/dist, and $ai/dist are not repository source paths.
ROOT_VARIABLES = ("root", "repo", "repo_root", "root_dir", "source_root", "ROOT", "REPO_ROOT", "PIG_ROOT", "SOURCE_ROOT", "PWD")
VARIABLE = r"\$(?:" + "|".join(ROOT_VARIABLES) + r")|\$\{(?:" + "|".join(ROOT_VARIABLES) + r")\}|\$\(CURDIR\)"
PATH_START = r"(?<![\w/.$-])(?P<prefix>\./|/|(?:" + VARIABLE + r")/)?"
DOCUMENT_PATH = re.compile(PATH_START + r"(?P<name>" + "|".join(re.escape(name) for name in DOCUMENT_MOVES) + r")(?=[\s\"'`)|,:#]|\.(?:\s|$)|$)", re.MULTILINE)


def owned(name: str) -> bool:
    """Go source and non-automation Markdown belong to the other sections."""
    path = Path(name)
    if path.suffix == ".go" or name.startswith("automation/layout/"):
        return False
    if path.suffix == ".md" and not name.startswith("automation/"):
        return False
    if "/testdata/" in name:
        return name.startswith("automation/ci/divguard/testdata/") and path.suffix == ".txt"
    return name in ROOT_FILES or name.startswith(("automation/", ".github/", ".devcontainer/"))


def replace_contract(text: str, old: str, new: str, name: str) -> str:
    """Accept the old or the applied form, but never silently lose a rule."""
    if old in text:
        return text.replace(old, new)
    if new not in text:
        raise ValueError(f"{name}: expected source fragment is missing: {old!r}")
    return text


def rewrite(name: str, text: str) -> str:
    if name.startswith("automation/ci/divguard/testdata/"):
        # These are Go snippets with upstream-relative provenance comments.
        # Only the scanner's repository-relative input path moves.
        return re.sub(r"(?m)^(// divguard:path )((?:agent|ai|coding|tui)/[^\n]+)",
                      lambda m: m[1] + plan.move_path(m[2], PACKAGE_MOVES), text)
    # Slash-separated paths are handled separately from Python Path operands
    # and directory inventories, where a bare package name is meaningful.
    boundary = r"(?=/|[\s\"'`),;]|$)"
    suffix = r"(?!/(?:src|test|dist)(?:/|$))"
    suffix += boundary if all("/" in name for name in PACKAGE_MOVES) else "(?(prefix)" + boundary + "|(?=/))"
    package_path = re.compile(PATH_START + r"(?P<name>" + "|".join(re.escape(p) for p in PACKAGE_MOVES) + ")" + suffix, re.MULTILINE)
    text = package_path.sub(lambda m: (m["prefix"] or "") + PACKAGE_MOVES[m["name"]], text)
    text = DOCUMENT_PATH.sub(lambda m: (m["prefix"] or "") + DOCUMENT_MOVES[m["name"]], text)
    for old, new in DOCUMENT_MOVES.items():
        text = text.replace("https://github.com/MichaelKinsy/PiG/blob/main/" + old,
                            "https://github.com/MichaelKinsy/PiG/blob/main/" + new)
    if name.endswith(".py"):
        # Match a Path division operand, not keywords such as npm's "ai".
        for old, new in PACKAGE_MOVES.items():
            source = " / ".join('"' + p + '"' for p in old.split("/"))
            target = " / ".join('"' + p + '"' for p in new.split("/"))
            text = re.sub(r'(?P<operand>\b(?:root|ROOT|port_map\.parent|HERE\.parents\[2\])) / ' + re.escape(source),
                          lambda m: m["operand"] + " / " + target, text)

    structural = {
        "automation/ci/check-port-map-drift.py": [
            ('("agent", "ai", "coding", "cmd", "internal", "tui")', '("cmd", "internal")'),
        ],
        "automation/ci/check-divergence-quality.py": [
            ('("internal/agent/", "internal/ai/", "cmd/", "internal/coding/", "extensions/", "internal/", "parity/", "piglets/", "automation/", "tests/", "internal/tui/")',
             '("cmd/", "extensions/", "internal/", "parity/", "piglets/", "automation/", "tests/")'),
        ],
        "automation/ci/coverage_recipe_test.py": [
            ('(root / "internal" / "coding").mkdir()', '(root / "internal" / "coding").mkdir(parents=True)'),
        ],
        "automation/ci/check-public-claims.py": [
            ('GO_DIRS = ("cmd", "internal", "coding")', 'GO_DIRS = ("cmd", "internal")'),
            # The original scanner includes coding and internal, but not the
            # standalone agent, ai, or tui trees. Preserve that exact scope.
            ('for base in GO_DIRS:\n        for directory, subdirs, names in os.walk(root / base):',
             'for base in GO_DIRS:\n        for directory, subdirs, names in os.walk(root / base):\n            if pathlib.Path(directory) == root / "internal":\n                subdirs[:] = [d for d in subdirs if d not in {"agent", "ai", "tui"}]'),
            ('files = [p for p in sorted(root.glob("*.md")) if p.name not in SKIP_ROOT_FILES]\n    for base in ("docs", "internal/pigdocs/content"):',
             'files = [p for p in sorted(root.glob("*.md")) if p.name not in SKIP_ROOT_FILES]\n    files += [root / name for name in (".github/CODE_OF_CONDUCT.md", ".github/CONTRIBUTING.md", ".github/SECURITY.md", ".github/SUPPORT.md") if (root / name).is_file()]\n    for base in ("docs", "internal/pigdocs/content"):'),
        ],
    }
    if "coding" not in PACKAGE_MOVES:
        structural = {"automation/ci/check-public-claims.py": structural["automation/ci/check-public-claims.py"][-1:]}
    for old, new in structural.get(name, []):
        # A multi-line replacement can contain the old text as a prefix.
        if new not in text:
            text = replace_contract(text, old, new, name)
    return text


def tracked_files(root: Path) -> list[str]:
    result = subprocess.run(["git", "-C", str(root), "ls-files", "-z"], check=True, capture_output=True)
    return sorted(set(result.stdout.decode("utf-8").rstrip("\0").split("\0")))


def apply(root: Path) -> list[str]:
    files = tracked_files(root)
    missing = REQUIRED_FILES - set(files)
    if missing:
        raise ValueError("required tracked paths are missing: " + ", ".join(sorted(missing)))
    changes = []
    # Preflight every owned file and every structural rewrite before writing.
    for name in files:
        if not owned(name):
            continue
        path = root / name
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"expected a regular tracked file: {name}")
        original = path.read_bytes()
        try:
            text = original.decode("utf-8")
        except UnicodeDecodeError as error:
            if path.suffix in {".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf", ".zip", ".gz"}:
                continue
            raise ValueError(f"expected UTF-8 text: {name}") from error
        updated = rewrite(name, text).encode("utf-8")
        if updated != original:
            changes.append((name, updated))
    for name, updated in changes:
        (root / name).write_bytes(updated)
    return [name for name, _ in changes]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--keep-public-libraries", action="store_true")
    parser.add_argument("root", nargs="?", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()
    top = subprocess.run(["git", "-C", str(root), "rev-parse", "--show-toplevel"], check=True, capture_output=True, text=True).stdout.strip()
    if Path(top).resolve() != root:
        parser.error("root must be the Git worktree root")
    try:
        with plan.selected(globals(), PACKAGE_MOVES=plan.package_moves(args.keep_public_libraries)):
            changed = apply(root)
    except (OSError, ValueError) as error:
        print(f"layout-build: {error}", file=sys.stderr)
        return 1
    for name in changed:
        print(f"layout-build: rewrite {name}")
    print(f"layout-build: moved 0 files; rewrote {len(changed)} files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
