#!/usr/bin/env python3
"""Fail when PORT_MAP.md does not account for current upstream source files.

PORT_MAP.md is the denominator for parity coverage. If upstream adds a source
file and the map omits it, coverage can overstate faithfulness. This check makes
that impossible: every non-test upstream source file in the packages pig tracks
must be represented, and every row, whatever its status, must point at a file
that still exists in .upstream/current. A removed file has no behavior to port,
defer, or rule out, so an n/a or ⏸ row for it only inflates those counts; the
removal is recorded in parity/upstream-sync/v<version>.toml instead.

Explicit production comments claiming to port/mirror an upstream source also
require a live mapping (partial is acceptable). This catches stale negative
classifications without pretending code presence proves behavioral completeness.

--reconcile prepares an upstream leap: it appends a ⬜ row for each new file to
its package table and deletes each row whose file disappeared. It never marks
anything ported; review the result.
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys
from dataclasses import dataclass

TRACKED_ROOTS = (
    "packages/agent/src",
    "packages/ai/src",
    "packages/coding-agent/src",
    "packages/tui/src",
)

PORTABLE_STATUSES = {"✅", "🟡", "⬜", "🔴"}
NON_LIVE_STATUSES = {"n/a", "⏸"}
ROW_RE = re.compile(r"^\|\s+`(?P<up>packages/[^`]+)`\s+\|\s+`(?P<go>[^`]+)`\s+\|\s*(?P<status>[^|]+?)\s*\|")


@dataclass(frozen=True)
class Row:
    upstream: str
    go: str
    status: str
    line: int


def parse_rows(port_map: pathlib.Path) -> dict[str, Row]:
    rows: dict[str, Row] = {}
    for line_no, line in enumerate(port_map.read_text(encoding="utf-8").splitlines(), 1):
        match = ROW_RE.match(line)
        if not match:
            continue
        upstream = match.group("up")
        status = match.group("status").strip()
        if upstream in rows:
            raise SystemExit(f"{port_map}:{line_no}: duplicate PORT_MAP row for {upstream} (first at line {rows[upstream].line})")
        rows[upstream] = Row(upstream, match.group("go"), status, line_no)
    return rows


def should_ignore_source(rel: str) -> bool:
    name = pathlib.PurePosixPath(rel).name
    parts = pathlib.PurePosixPath(rel).parts
    if name.endswith((".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx")):
        return True
    if any(part in {"test", "tests", "__tests__"} for part in parts):
        return True
    # Generated source files are still tracked in PORT_MAP when they affect
    # runtime behavior (models.generated.ts, image-models.generated.ts). Do not
    # ignore them here; require explicit ✅/n/a/⏸ classification.
    return False


def discover_upstream(upstream_root: pathlib.Path) -> set[str]:
    out: set[str] = set()
    for root in TRACKED_ROOTS:
        base = upstream_root / root
        if not base.exists():
            continue
        for path in base.rglob("*"):
            if not path.is_file():
                continue
            if path.suffix not in {".ts", ".tsx"} and not path.name.endswith(".d.ts"):
                continue
            rel = path.relative_to(upstream_root).as_posix()
            if should_ignore_source(rel):
                continue
            out.add(rel)
    return out


def status_token(status: str) -> str:
    # Status cells sometimes include a note after the emoji/text. The first
    # token is the authoritative status marker.
    return status.split()[0] if status.split() else status


def pinned_version(port_map: pathlib.Path) -> str:
    source = (port_map.parent / "coding" / "pigversion" / "pigversion.go").read_text(encoding="utf-8")
    match = re.search(r'^const UpstreamVersion = "([^"]+)"', source, re.MULTILINE)
    if not match:
        raise SystemExit("port-map-drift: cannot read UpstreamVersion from coding/pigversion/pigversion.go")
    return match.group(1)


def reconcile(port_map: pathlib.Path, missing: list[str], stale: list["Row"]) -> None:
    """Append ⬜ rows for missing files and delete rows for removed files."""
    version = pinned_version(port_map)
    lines = port_map.read_text(encoding="utf-8").splitlines()
    for row in sorted(stale, key=lambda item: -item.line):
        del lines[row.line - 1]
    by_root: dict[str, list[str]] = {}
    for rel in missing:
        root = next(root for root in TRACKED_ROOTS if rel.startswith(root + "/"))
        by_root.setdefault(root, []).append(rel)
    for root in sorted(by_root, key=lambda item: -TRACKED_ROOTS.index(item)):
        heading = next((i for i, line in enumerate(lines) if line.strip() == f"## `{root}/`"), None)
        if heading is None:
            raise SystemExit(f"port-map-drift: PORT_MAP has no section for {root}/")
        end = next((i for i in range(heading + 1, len(lines)) if lines[i].startswith("## ")), len(lines))
        last_row = max(i for i in range(heading, end) if lines[i].startswith("| `packages/"))
        new_rows = [f"| `{rel}` | `(new upstream by {version}; not mapped)` | ⬜ |" for rel in by_root[root]]
        lines[last_row + 1 : last_row + 1] = new_rows
    port_map.write_text("\n".join(lines) + "\n", encoding="utf-8")


def implementation_conflicts(port_map: pathlib.Path, rows: dict[str, Row]) -> list[str]:
    """Reject explicit production port claims left unstarted/designed-out.

    This is a contradiction check, not a completeness detector: source presence
    never proves a complete port and never auto-promotes a row. Ordinary source
    references and test fixtures are not implementation claims.
    """
    claim = re.compile(r"\b(?:ports?|mirrors?)\s+`?(packages/[\w./-]+\.tsx?)\b", re.IGNORECASE)
    conflicts = []
    for directory in ("agent", "ai", "coding", "cmd", "internal", "tui"):
        for source in sorted((port_map.parent / directory).rglob("*.go")):
            if source.is_symlink() or source.name.endswith("_test.go") or "testdata" in source.parts:
                continue
            for line_no, line in enumerate(source.read_text(encoding="utf-8").splitlines(), 1):
                if not line.lstrip().startswith("//"):
                    continue
                for upstream in claim.findall(line):
                    row = rows.get(upstream)
                    if row is not None and status_token(row.status) in {"⬜", "n/a", "⏸"}:
                        relative = source.relative_to(port_map.parent)
                        conflicts.append(f"{relative}:{line_no}: implementation claim contradicts {upstream} [{row.status}]; review mapping/status (do not auto-promote)")
    return conflicts


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--upstream", default=".upstream/current", help="current upstream tree")
    parser.add_argument("--port-map", default="PORT_MAP.md", help="PORT_MAP.md path")
    parser.add_argument("--reconcile", action="store_true", help="add ⬜ rows for new files and retire rows for removed files, then recheck")
    args = parser.parse_args()

    upstream_root = pathlib.Path(args.upstream)
    port_map = pathlib.Path(args.port_map)
    if not upstream_root.exists():
        print(f"port-map-drift: upstream tree not found: {upstream_root}", file=sys.stderr)
        return 2
    if not port_map.exists():
        print(f"port-map-drift: PORT_MAP not found: {port_map}", file=sys.stderr)
        return 2

    upstream_files = discover_upstream(upstream_root)
    rows = parse_rows(port_map)

    missing = sorted(upstream_files - set(rows))
    stale: list[Row] = []
    unknown_status: list[Row] = []
    for row in rows.values():
        token = status_token(row.status)
        if token not in PORTABLE_STATUSES | NON_LIVE_STATUSES:
            unknown_status.append(row)
            continue
        if row.upstream not in upstream_files:
            stale.append(row)

    if args.reconcile and (missing or stale):
        reconcile(port_map, missing, stale)
        print(f"port-map-drift: reconciled {len(missing)} new and {len(stale)} removed upstream files; review PORT_MAP.md")
        rows = parse_rows(port_map)
        missing = sorted(upstream_files - set(rows))
        stale = [row for row in rows.values() if row.upstream not in upstream_files]

    conflicts = implementation_conflicts(port_map, rows)
    if not missing and not stale and not unknown_status and not conflicts:
        print(f"port-map-drift: clean ({len(upstream_files)} upstream source files accounted for)")
        return 0

    for conflict in conflicts:
        print(f"port-map-drift: {conflict}", file=sys.stderr)
    if missing:
        print("port-map-drift: upstream files missing from PORT_MAP.md:", file=sys.stderr)
        for rel in missing:
            print(f"  + {rel}", file=sys.stderr)
    if stale:
        print("port-map-drift: PORT_MAP rows whose upstream file no longer exists:", file=sys.stderr)
        for row in stale:
            print(f"  - line {row.line}: {row.upstream} [{row.status}]", file=sys.stderr)
        print("    Delete them; parity/upstream-sync/v<version>.toml records removed files.", file=sys.stderr)
    if unknown_status:
        print("port-map-drift: PORT_MAP rows with unknown status token:", file=sys.stderr)
        for row in unknown_status:
            print(f"  ? line {row.line}: {row.upstream} [{row.status}]", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
