#!/usr/bin/env python3
"""Fail when test/parity/coverage.md or the coverage badge no longer matches
docs/parity/PORT_MAP.md and the scenarios.

coverage.md is generated, so a scenario added without regenerating it silently
understates coverage. The report mixes two kinds of data: facts derived from
docs/parity/PORT_MAP.md and test/parity/scenarios (scenario counts, which scenarios cover which
upstream file), and the "last run" column, which comes from a transient parity
results file. Only the derived facts can be checked here, so the run column is
excluded from the comparison on both sides.
"""
from __future__ import annotations

import argparse
import pathlib
import subprocess
import sys
import tempfile

ROW_PREFIX = "| `"
REPAIR = "run: make generate (or: make coverage RESULTS=), then commit the result"


def strip_run_column(text: str) -> list[str]:
    """Drop only the upstream table's last-run cell, preserving unit evidence."""
    stripped: list[str] = []
    run_column = None
    column_count = None
    for line in text.splitlines():
        if line.startswith("| upstream |"):
            header = [cell.strip() for cell in line.split("|")]
            run_column = header.index("last run")
            column_count = len(header)
        elif line.startswith(ROW_PREFIX) and run_column is not None:
            cells = line.split("|")
            if len(cells) == column_count:
                del cells[run_column]
                line = "|".join(cells)
        stripped.append(line)
    return stripped


def generate(pig_root: pathlib.Path) -> tuple[str, str]:
    with tempfile.TemporaryDirectory() as directory:
        badge = pathlib.Path(directory) / "parity-coverage.svg"
        proc = subprocess.run(
            [
                "go", "run", "./test/parity/cmd/coverage", "-out", "-",
                "-port-map", "docs/parity/PORT_MAP.md",
                "-scenarios", "test/parity/scenarios",
                "-badge", str(badge),
            ],
            cwd=pig_root,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        if proc.returncode != 0:
            sys.exit(f"coverage-drift: generator failed:\n{proc.stderr}")
        return proc.stdout, badge.read_text(encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--coverage", default="test/parity/coverage.md")
    parser.add_argument("--badge", default=".github/badges/parity-coverage.svg")
    args = parser.parse_args()

    pig_root = pathlib.Path(__file__).resolve().parents[2]
    committed_path = pig_root / args.coverage
    badge_path = pig_root / args.badge
    if not committed_path.is_file():
        sys.exit(f"coverage-drift: {args.coverage} not found; {REPAIR}")
    if not badge_path.is_file():
        sys.exit(f"coverage-drift: {args.badge} not found; {REPAIR}")
    generated_report, generated_badge = generate(pig_root)
    committed = strip_run_column(committed_path.read_text(encoding="utf-8"))
    current = strip_run_column(generated_report)

    committed_badge = badge_path.read_text(encoding="utf-8")
    report_current = committed == current
    badge_current = committed_badge == generated_badge
    if report_current and badge_current:
        print(f"coverage-drift: {args.coverage} and {args.badge} are current")
        return 0

    print("coverage-drift: generated coverage evidence is stale", file=sys.stderr)
    print(REPAIR, file=sys.stderr)
    print("Use make coverage RESULTS=<path> to include a measured parity run.", file=sys.stderr)
    if not badge_current:
        print(f"\nbadge differs: {args.badge}", file=sys.stderr)
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
