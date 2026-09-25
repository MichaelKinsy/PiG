#!/usr/bin/env python3
"""Fail when parity/coverage.md, the coverage badge, or the README porting block
no longer matches PORT_MAP.md and the scenarios.

coverage.md is generated, so a scenario added without regenerating it silently
understates coverage. The report mixes two kinds of data: facts derived from
PORT_MAP.md and parity/scenarios (scenario counts, which scenarios cover which
upstream file), and the "last run" column, which comes from a transient parity
results file. Only the derived facts can be checked here, so the run column is
excluded from the comparison on both sides.
"""
from __future__ import annotations

import argparse
import pathlib
import shutil
import subprocess
import sys
import tempfile

ROW_PREFIX = "| `"
COLUMNS = 5


def strip_run_column(text: str) -> list[str]:
    """Drop the trailing "last run" cell from report rows, keep everything else."""
    stripped: list[str] = []
    for line in text.splitlines():
        if line.startswith(ROW_PREFIX):
            cells = line.split("|")
            # "| a | b | c | d | e |" splits to ['', ' a ', ..., ' e ', '']
            if len(cells) == COLUMNS + 2:
                line = "|".join(cells[:-2]) + "|"
        stripped.append(line)
    return stripped


def generate(pig_root: pathlib.Path, readme_path: pathlib.Path) -> tuple[str, str, str]:
    with tempfile.TemporaryDirectory() as directory:
        badge = pathlib.Path(directory) / "parity-coverage.svg"
        readme = pathlib.Path(directory) / "README.md"
        shutil.copyfile(readme_path, readme)
        proc = subprocess.run(
            [
                "go", "run", "./parity/cmd/coverage",
                "-port-map", "PORT_MAP.md",
                "-scenarios", "parity/scenarios",
                "-badge", str(badge),
                "-readme", str(readme),
            ],
            cwd=pig_root,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        if proc.returncode != 0:
            sys.exit(f"coverage-drift: generator failed:\n{proc.stderr}")
        return proc.stdout, badge.read_text(encoding="utf-8"), readme.read_text(encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--coverage", default="parity/coverage.md")
    parser.add_argument("--badge", default=".github/badges/parity-coverage.svg")
    parser.add_argument("--readme", default="README.md")
    args = parser.parse_args()

    pig_root = pathlib.Path(__file__).resolve().parents[2]
    committed_path = pig_root / args.coverage
    badge_path = pig_root / args.badge
    readme_path = pig_root / args.readme
    if not committed_path.is_file():
        sys.exit(f"coverage-drift: {args.coverage} not found")
    if not badge_path.is_file():
        sys.exit(f"coverage-drift: {args.badge} not found")

    if not readme_path.is_file():
        sys.exit(f"coverage-drift: {args.readme} not found")
    generated_report, generated_badge, generated_readme = generate(pig_root, readme_path)
    committed = strip_run_column(committed_path.read_text(encoding="utf-8"))
    current = strip_run_column(generated_report)

    committed_badge = badge_path.read_text(encoding="utf-8")
    report_current = committed == current
    badge_current = committed_badge == generated_badge
    readme_current = readme_path.read_text(encoding="utf-8") == generated_readme
    if report_current and badge_current and readme_current:
        print(f"coverage-drift: {args.coverage}, {args.badge}, and the {args.readme} porting block are current")
        return 0

    print("coverage-drift: generated coverage evidence is stale; regenerate it with", file=sys.stderr)
    print("    make coverage RESULTS=<parity results json>", file=sys.stderr)
    print("(omitting RESULTS blanks the 'last run' column for every family)", file=sys.stderr)
    if not badge_current:
        print(f"\nbadge differs: {args.badge}", file=sys.stderr)
    if not readme_current:
        print(f"\nporting block differs: {args.readme}", file=sys.stderr)
    if not report_current:
        for line_no, (was, now) in enumerate(zip(committed, current), 1):
            if was != now:
                print(f"\nfirst difference at line {line_no}:", file=sys.stderr)
                print(f"  committed: {was}", file=sys.stderr)
                print(f"  current:   {now}", file=sys.stderr)
                break
        else:
            print(
                f"\nlength differs: committed {len(committed)} lines, current {len(current)}",
                file=sys.stderr,
            )
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
