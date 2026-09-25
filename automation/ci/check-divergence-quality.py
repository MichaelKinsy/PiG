#!/usr/bin/env python3
"""Validate divergence/additive records as current enforceable contracts."""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class Record:
    ledger: Path
    ident: str
    title: str
    body: str
    line: int


def records(path: Path) -> list[Record]:
    text = path.read_text(encoding="utf-8")
    matches = list(re.finditer(r"(?m)^## (D\d+) (.+)$", text))
    result: list[Record] = []
    for index, match in enumerate(matches):
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        result.append(
            Record(
                ledger=path,
                ident=match.group(1),
                title=match.group(2).strip(),
                body=text[match.end() : end],
                line=text.count("\n", 0, match.start()) + 1,
            )
        )
    return result


def upstream_version(root: Path) -> str:
    text = (root / "coding" / "pigversion" / "pigversion.go").read_text(encoding="utf-8")
    match = re.search(r'const UpstreamVersion = "([^"]+)"', text)
    if not match:
        raise ValueError("coding/pigversion/pigversion.go has no UpstreamVersion")
    return match.group(1)


def validate(record: Record, version: str, root: Path) -> list[str]:
    errors: list[str] = []
    location = f"{record.ledger}:{record.line}: {record.ident}"
    if "SCRUTINIZED:approved" not in record.body:
        errors.append(f"{location}: missing SCRUTINIZED:approved")
    if not re.search(r"(?mi)^Remove when:", record.body):
        errors.append(f"{location}: missing Remove when condition")
    if not re.search(r"(?mi)^(Locked by|Tests|Parity allowance|Evidence):", record.body):
        errors.append(f"{location}: missing durable evidence")

    for observed in re.findall(r"(?i)\bpi v(\d+\.\d+\.\d+)\b", record.body):
        if observed != version:
            errors.append(f"{location}: stale upstream evidence pi v{observed}; current pin is {version}")

    if re.search(r"(?i)no production caller|caller-free", record.body):
        errors.append(f"{location}: caller-free code must be deleted")

    if record.ledger.name == "DIVERGENCES.md":
        invalid_reasons = [
            (r"(?i)no observable (?:behavioral )?difference", "equal behavior is not a divergence"),
            (r"(?i)not assertable|cannot be asserted|test harness", "test limitations are not divergences"),
        ]
        for pattern, message in invalid_reasons:
            if re.search(pattern, record.body):
                errors.append(f"{location}: {message}")
    else:
        dispositions = re.findall(r"(?mi)^Stock disposition: ([^.]+)\.", record.body)
        allowed = {
            "required substrate",
            "product-neutral diagnostics",
            "inert capability",
            "explicit unsafe opt-in",
        }
        if len(dispositions) != 1:
            errors.append(f"{location}: additive record must have one Stock disposition")
        elif dispositions[0] not in allowed:
            errors.append(f"{location}: unsupported Stock disposition {dispositions[0]!r}")

    listed_paths = set(re.findall(r"`([^`]+\.(?:go|rs|py|mjs|toml))`", record.body))
    repository_roots = ("agent/", "ai/", "cmd/", "coding/", "extensions/", "internal/", "parity/", "piglets/", "automation/", "tests/", "tui/")
    for listed in sorted(listed_paths):
        if not listed.startswith(repository_roots) or any(token in listed for token in ("*", "<", ">", "{", "}")) or ":" in listed:
            continue
        candidate = root / listed
        if "/" in listed and not candidate.exists() and not listed.startswith("packages/"):
            errors.append(f"{location}: listed evidence/call-site path does not exist: {listed}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default=".")
    args = parser.parse_args()
    root = Path(args.root).resolve()
    version = upstream_version(root)
    ledgers = [root / "DIVERGENCES.md", root / "docs" / "additive-features.md"]
    errors: list[str] = []
    count = 0
    for ledger in ledgers:
        ledger_records = records(ledger)
        ids = [int(record.ident[1:]) for record in ledger_records]
        if ids != sorted(ids):
            errors.append(f"{ledger}: records must be in ascending numeric order: {ids}")
        for record in ledger_records:
            count += 1
            errors.extend(validate(record, version, root))
    if errors:
        print("divergence quality: FAIL", file=sys.stderr)
        for error in errors:
            print(f"  - {error}", file=sys.stderr)
        return 1
    print(f"divergence quality: OK ({count} current approved records)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
