#!/usr/bin/env python3
"""Validate divergence/additive records as current enforceable contracts."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import unicodedata
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class Record:
    ledger: Path
    ident: str
    title: str
    body: str
    line: int
    core: bool = False


def records(path: Path, core: bool = False) -> list[Record]:
    text = path.read_text(encoding="utf-8")
    matches = list(re.finditer(r"(?m)^## (D\d+) (.+)$", text))
    result: list[Record] = []
    for match in matches:
        following = re.compile(r"(?m)^## ").search(text, match.end())
        end = following.start() if following else len(text)
        result.append(
            Record(
                ledger=path,
                ident=match.group(1),
                title=match.group(2).strip(),
                body=text[match.end() : end],
                line=text.count("\n", 0, match.start()) + 1,
                core=core,
            )
        )
    return result


def upstream_version(root: Path) -> str:
    text = (root / "internal" / "coding" / "pigversion" / "pigversion.go").read_text(encoding="utf-8")
    match = re.search(r'const UpstreamPackageVersion = "([^"]+)"', text)
    if not match:
        raise ValueError("internal/coding/pigversion/pigversion.go has no UpstreamPackageVersion")
    return match.group(1)


INTENTION_LABELS = ("Protects", "PiG-shaped test considered", "Why not")


def test_level_divergences(root: Path, version: str) -> set[str]:
    """D ids that a reviewed test-mapping entry names: the divergences that design out or change a test."""
    path = root / "test" / "parity" / "interfaces" / f"test-mapping-v{version}.json"
    if not path.exists():
        return set()
    entries = json.loads(path.read_text(encoding="utf-8")).get("entries", [])
    return {entry["divergence"] for entry in entries if entry.get("divergence")}


def validate(record: Record, version: str, root: Path, test_level: set[str] = frozenset()) -> list[str]:
    errors: list[str] = []
    location = f"{record.ledger}:{record.line}: {record.ident}"
    if record.ident in test_level and record.core:
        for label in INTENTION_LABELS:
            if not re.search(rf"(?mi)^{re.escape(label)}:[ \t]*\S", record.body):
                errors.append(f"{location}: test-level divergence missing non-empty '{label}:' (AGENTS.md Upstream test parity: property, PiG-shaped test considered, why impossible or worthless)")
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

    if record.core:
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
    repository_roots = ("agent/", "ai/", "cmd/", "coding/", "extensions/", "internal/", "test/parity/", "piglets/", "automation/", "test/", "tui/")
    for listed in sorted(listed_paths):
        if not listed.startswith(repository_roots) or any(token in listed for token in ("*", "<", ">", "{", "}")) or ":" in listed:
            continue
        candidate = root / listed
        if "/" in listed and not candidate.exists() and not listed.startswith("packages/"):
            errors.append(f"{location}: listed evidence/call-site path does not exist: {listed}")
    return errors


# The approval binds the record's normative text. `SCRUTINIZED:approved` carries `text-sha256:<16 hex>`, the first 16
# hex digits of the SHA-256 of normative_text(record). The text of an approved record cannot change without a new
# approval line: the hash on that line then differs, and this gate fails with the bound and the current hash.
# The hash detects a change; it is not a signature and proves nobody approved anything. The owner's approval is the
# diff line that carries the new hash.
APPROVAL = "SCRUTINIZED:approved"
TOKEN = re.compile(r"[ \t]text-sha256:([0-9A-Za-z]+)")
HEX16 = re.compile(r"[0-9a-f]{16}")
DATE_AFTER_APPROVAL = re.compile(re.escape(APPROVAL) + r"(?: \d{4}-\d\d-\d\d)?")
# Blocks that list evidence, not the difference: test names, scenario paths, source markers. Everything else is
# normative, so a field this gate has not met before is hashed rather than ignored.
EVIDENCE_LABEL = re.compile(
    r"(?:Locked by|Evidence|Tests?|Call[- ]sites?(?:[ -]markers?)?|Parity coverage|PORT_MAP paths"
    r"|[A-Z][A-Za-z ,'-]* evidence|What was run\b[^:\n]*):"
)
# A block that continues the block before it: a list, a table, a code fence, an indented line.
CONTINUATION = re.compile(r"[ \t]*(?:[-*+]|\d+[.)])[ \t]|[ \t]*\||[ \t]*```|[ \t]{2,}\S")
LINK_TARGET = re.compile(r"\[([^\]]*)\]\([^)]*\)")
# Inline code that only points at evidence: a path under a repository or Pi source directory, a Go test name.
# Numbers, durations, limits, flag and setting names, identifiers, provider and model names stay in the hash.
EVIDENCE_SPAN = re.compile(
    r"`(?:(?:\.upstream|\.github|packages|agent|ai|automation|bench|chord|cmd|codemode|coding|docs|durable|env|examples"
    r"|extensions|handoffs|internal|mcp|piglets|plans|research|schemas?|telemetry|test|third_party|tui)/[^\s`]*"
    r"|(?:Test|Benchmark|Fuzz)[A-Z_]\w*(?:/[^\s`]*)?)`"
)
# A Pi version literal or an upstream tag (the word Pi or upstream, then a dotted version): a pin refresh re-checks evidence against another release and must not need a new approval.
# The Remove when line keeps its versions, because a different release there is a different condition.
PIN_VERSION = re.compile(r"(?i)\b(pi|upstream)\s+v?\d+\.\d+\.\d+\b")
PENDING_FILE = Path("automation") / "ci" / "divergence-reapproval-pending.txt"
# What changed, in the order the pending file lists them and the owner reads them: the difference itself, where or when
# it applies, the condition that ends it, the approval fields, wording with the same meaning, a record nobody approved
# before this rule (`new`).
PENDING_CLASSES = ("behaviour", "scope", "remove-when", "owner-field", "editorial", "new")


def tidy(text: str) -> str:
    text = " ".join(unicodedata.normalize("NFC", text).split())
    # what removed evidence leaves behind: empty parentheses, doubled or dangling separators
    text = re.sub(r"\(\s*[,;]?\s*\)", "", text)
    text = re.sub(r"([,;])(?:\s*[,;])+", r"\1", text)
    text = re.sub(r"\s+([,;.:)])", r"\1", text)
    return " ".join(text.replace("( ", "(").split())


def normative_blocks(record: Record) -> list[str]:
    """The blocks an approval binds, each normalized: the title, then every block that states the difference, its
    scope, the approved behaviour or the remove-when condition, and the approval line without its date and hash (a new approval writes both). Evidence blocks
    (test names, scenario paths, call-site markers), inline paths and test names, link targets, pin versions and
    whitespace are left out, so a reflow or an evidence edit keeps the hash and a changed word does not."""
    blocks: list[tuple[bool, list[str]]] = []  # (is the approval line, lines)
    current: list[str] = []
    for line in record.body.splitlines():
        if APPROVAL in line:
            if current:
                blocks.append((False, current))
                current = []
            blocks.append((True, [DATE_AFTER_APPROVAL.sub(APPROVAL, TOKEN.sub("", line))]))
        elif line.strip():
            current.append(line)
        elif current:
            blocks.append((False, current))
            current = []
    if current:
        blocks.append((False, current))
    kept = [PIN_VERSION.sub(r"\1 <version>", f"{record.ident} {record.title}")]
    excluded = False
    for approval, lines in blocks:
        block = "\n".join(lines)
        if not approval:
            if EVIDENCE_LABEL.match(lines[0].lstrip()):
                excluded = True
                continue
            if CONTINUATION.match(lines[0]):
                if excluded:
                    continue
            else:
                excluded = False
            if not block.lstrip().startswith("Remove when:"):
                block = PIN_VERSION.sub(r"\1 <version>", block)
        kept.append(block)
    return [tidy(EVIDENCE_SPAN.sub("", LINK_TARGET.sub(r"\1", block))) for block in kept]


def normative_text(record: Record) -> str:
    return " ".join(normative_blocks(record))


def text_hash(record: Record) -> str:
    return hashlib.sha256(normative_text(record).encode("utf-8")).hexdigest()[:16]


def bound_hashes(record: Record) -> list[str]:
    """The hash tokens on the record's approval lines (more than one, or a malformed one, is an error)."""
    found: list[str] = []
    for line in record.body.splitlines():
        if APPROVAL in line:
            found.extend(match.group(1) for match in TOKEN.finditer(line))
    return found


@dataclass(frozen=True)
class Pending:
    kind: str
    reason: str


def read_pending(root: Path) -> tuple[dict[str, Pending], list[str]]:
    """The records whose approved text changed (or that were never approved under this rule) and that the owner has not
    approved again yet: automation/ci/divergence-reapproval-pending.txt, `D<N> <class> <reason>`, sorted by class
    (PENDING_CLASSES). Returns the entries and the problems with the file."""
    path = root / PENDING_FILE
    pending: dict[str, Pending] = {}
    problems: list[str] = []
    if not path.exists():
        return pending, problems
    order: list[int] = []
    for number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split(None, 2)
        where = f"{PENDING_FILE}:{number}"
        if len(parts) < 3 or not re.fullmatch(r"D\d+", parts[0]) or parts[1] not in PENDING_CLASSES:
            problems.append(f"{where}: want `D<N> <class> <reason>` with class one of {', '.join(PENDING_CLASSES)}, got {line!r}")
            continue
        if parts[0] in pending:
            problems.append(f"{where}: {parts[0]} is listed twice")
            continue
        pending[parts[0]] = Pending(parts[1], parts[2].strip())
        order.append(PENDING_CLASSES.index(parts[1]))
    if order != sorted(order):
        problems.append(f"{PENDING_FILE}: entries must be sorted by class ({', '.join(PENDING_CLASSES)})")
    return pending, problems


def binding_errors(record: Record, pending: dict[str, Pending]) -> list[str]:
    location = f"{record.ledger}:{record.line}: {record.ident}"
    if APPROVAL not in record.body:
        return []  # reported as a missing approval
    tokens = bound_hashes(record)
    owner = (
        f"Only the owner approves, with `make divergence-approve ID={record.ident}`, which writes the date and the hash on the "
        f"approval line. An agent or a lane never runs it: a lane that changes the text adds `{record.ident} <class> <reason>` to "
        f"{PENDING_FILE} (classes: {', '.join(PENDING_CLASSES)}) and says so in its pull request"
    )
    if len(tokens) > 1:
        return [f"{location}: {len(tokens)} text-sha256 tokens on the approval line(s); keep one"]
    if tokens and not HEX16.fullmatch(tokens[0]):
        return [f"{location}: malformed text-sha256:{tokens[0]} (want 16 lowercase hex digits)"]
    current = text_hash(record)
    if tokens and tokens[0] == current:
        if record.ident in pending:
            return [f"{location}: listed in {PENDING_FILE} but its approval binds the current text (text-sha256:{current}); delete the stale entry"]
        return []
    if record.ident in pending:
        return []
    if tokens:
        return [
            f"{location}: the approved text changed: the approval binds text-sha256:{tokens[0]} but the text now hashes to "
            f"{current}. Restore the text, or take the change to the owner. {owner}. "
            f"`check-divergence-quality.py --print-hash {record.ident} --text` shows the hashed text"
        ]
    return [f"{location}: SCRUTINIZED:approved binds no text hash (the text hashes to {current}). {owner}"]


def core_records(root: Path) -> list[Record]:
    """The core divergences: the one-file-per-record sources when they exist (an edit shows at once, before
    `make divergences` regenerates the ledger), else the ledger."""
    directory = root / "docs" / "parity" / "divergences"
    if not directory.is_dir():
        return records(root / "docs" / "parity" / "DIVERGENCES.md", core=True)
    files = sorted(directory.glob("D[0-9]*.md"), key=lambda path: int(re.sub(r"\D", "", path.stem)))
    return [record for path in files for record in records(path, core=True)]


def additive_records(root: Path) -> list[Record]:
    return records(root / "docs" / "additive-features.md")


def find_record(root: Path, ident: str) -> Record:
    for record in core_records(root) + additive_records(root):
        if record.ident == ident:
            return record
    raise SystemExit(f"{ident}: no such record in docs/parity/divergences or docs/additive-features.md")


def stamp(root: Path, record: Record, date: str | None) -> bool:
    """Write the record's current text hash on its approval line, with `date` when given (the one-off stamp has none:
    it records no approval). Returns whether the file changed."""
    path = record.ledger
    text = path.read_text(encoding="utf-8")
    heading = re.search(rf"(?m)^## {record.ident} .+$", text)
    following = re.compile(r"(?m)^## ").search(text, heading.end())
    start, end = heading.start(), following.start() if following else len(text)
    lines = text[start:end].splitlines(keepends=True)
    approvals = [index for index, line in enumerate(lines) if APPROVAL in line]
    if len(approvals) != 1:
        raise SystemExit(f"{record.ident}: want exactly one {APPROVAL} line to bind, found {len(approvals)}")
    digest = text_hash(Record(path, record.ident, record.title, "".join(lines[1:]), 0))
    line = TOKEN.sub("", lines[approvals[0]])
    place = DATE_AFTER_APPROVAL.search(line)
    head = place.group(0) if date is None else f"{APPROVAL} {date}"
    lines[approvals[0]] = f"{line[:place.start()]}{head} text-sha256:{digest}{line[place.end():]}"
    updated = text[:start] + "".join(lines) + text[end:]
    path.write_text(updated, encoding="utf-8")
    return updated != text


def unpend(root: Path, idents: set[str]) -> None:
    """Drop the idents from the pending list: the owner's approval ends the pending state."""
    path = root / PENDING_FILE
    if not path.exists():
        return
    kept = [line for line in path.read_text(encoding="utf-8").splitlines(keepends=True) if line.split(None, 1)[:1] not in [[ident] for ident in idents]]
    path.write_text("".join(kept), encoding="utf-8")


# A parity row that says an extension capability exists in the Node runtime alone hides a native-SDK gap in prose.
# The per-runtime disposition lives in test/parity/ledgers/sdk-exports.toml (docs/extension-sdk-exports.md), where a gap
# names its lane and owner; the parity row points there instead.
NODE_ONLY = re.compile(r"(?i)\bnode(?:\s+runtime)?\s+only\b|\bgo/rust/python\s+(?:have|has)\s+no\b")


def parity_prose_errors(root: Path) -> list[str]:
    path = root / "docs" / "extension-api-parity.md"
    if not path.exists():
        return []
    errors = []
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        match = NODE_ONLY.search(line)
        if match:
            errors.append(
                f"{path.relative_to(root)}:{number}: {match.group(0)!r}: say which SDK cells are `gap:<lane>` in "
                "docs/extension-sdk-exports.md (test/parity/ledgers/sdk-exports.toml) instead of calling the capability Node-only"
            )
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".")
    parser.add_argument("--print-hash", metavar="D<N>", help="print the text hash an approval of this record binds")
    parser.add_argument("--text", action="store_true", help="with --print-hash: print the normalized text that is hashed")
    parser.add_argument("--stamp", nargs="+", metavar="D<N>", help="OWNER ONLY (make divergence-approve): approve these records as they are now, with today's date; drops them from the pending list")
    parser.add_argument("--date", metavar="YYYY-MM-DD", help="with --stamp: the approval date (default today)")
    parser.add_argument("--initial-stamp", action="store_true", help="the one-off stamp of a ledger that binds nothing yet: write the text hash on every approved record that is not pending; refuses to run when any record is already bound")
    args = parser.parse_args()
    root = Path(args.root).resolve()
    everything = core_records(root) + additive_records(root)
    if args.print_hash:
        record = find_record(root, args.print_hash)
        print(" ".join(normative_blocks(record)) if args.text else text_hash(record))
        return 0
    if args.initial_stamp:
        already = [record.ident for record in everything if bound_hashes(record)]
        if already:
            print(f"--initial-stamp refuses to run: {len(already)} record(s) are already bound (first {already[0]}); it runs once per ledger", file=sys.stderr)
            return 2
        pending, problems = read_pending(root)
        idents = [record.ident for record in everything if APPROVAL in record.body and record.ident not in pending]
        for ident in idents:
            stamp(root, find_record(root, ident), None)
        print(f"bound {len(idents)} record(s) to their current text, {len(pending)} left pending; this records no approval. Run `make divergences`.")
        return 0
    if args.stamp:
        import datetime

        date = args.date or datetime.date.today().isoformat()
        for ident in args.stamp:
            stamp(root, find_record(root, ident), date)
        unpend(root, set(args.stamp))
        print(f"approved {len(args.stamp)} record(s) on {date}; run `make divergences` to regenerate docs/parity/DIVERGENCES.md")
        return 0
    version = upstream_version(root)
    test_level = test_level_divergences(root, version)
    ledgers = [("docs/parity/divergences", core_records(root)), ("docs/additive-features.md", additive_records(root))]
    errors: list[str] = []
    count = bound = 0
    pending, problems = read_pending(root)
    errors.extend(problems)
    seen: set[str] = set()
    for ledger, ledger_records in ledgers:
        ids = [int(record.ident[1:]) for record in ledger_records]
        if ids != sorted(ids):
            errors.append(f"{ledger}: records must be in ascending numeric order: {ids}")
        for record in ledger_records:
            count += 1
            seen.add(record.ident)
            errors.extend(validate(record, version, root, test_level))
            errors.extend(binding_errors(record, pending))
            bound += bool(bound_hashes(record))
    for ident in sorted(set(pending) - seen, key=lambda value: int(value[1:])):
        errors.append(f"{PENDING_FILE}: {ident} is not a record in either ledger; delete the entry")
    errors.extend(parity_prose_errors(root))
    if errors:
        print("divergence quality: FAIL", file=sys.stderr)
        for error in errors:
            print(f"  - {error}", file=sys.stderr)
        return 1
    print(f"divergence quality: OK ({count} current approved records, {bound} bound to their text, {len(pending)} pending re-approval)")
    for ident, entry in pending.items():
        print(f"  pending re-approval: {ident} [{entry.kind}]: {entry.reason}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
