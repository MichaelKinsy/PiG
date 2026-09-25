#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Flag public claims that the repository's own evidence contradicts.

Scans the public prose (root Markdown files, docs/ including the user docs the
site renders, the knowledge graph JSON-LD, and the agent docs bundle), the
string literals in the Go sources that users see (help text, command output,
slash command descriptions), plus any extra paths given on the command line (the private hosting repository
passes its page sources and generated docs data), and fails on:

  phrase     a forbidden overclaim ("full parity", "byte for byte", "every Pi
             command", ...) outside the allowlist below
  governance sponsorship, OSPO approval, community ownership, endorsement, or
             signed releases stated without a negation on the same line
  version    a Pi version other than the pin in coding/pigversion/pigversion.go
             (UpstreamReviewedVersion, read from coding/upstream.go, is
             accepted only on a line about review)
  numbers    a porting or verification percentage or ratio that differs from
             the generated coverage block in AGENTS.md
  piglet    a ``pig piglet <verb>`` command in Markdown that is absent from
             ``pig piglet --help``, unless it appears under a Planned heading
  readme     a README porting block that disagrees with that coverage block

Every expected value is read from its single source, so the check follows the
pin and `make coverage` without edits here. Pass delivery or blog drafts as
extra paths to audit them the same way:

    automation/ci/check-public-claims.py ../delivery/*.md
"""
from __future__ import annotations

import argparse
import os
import pathlib
import re
import sys
from dataclasses import dataclass

# Overclaims that the evidence contradicts wherever they appear.
PHRASES = [
    r"\bfull(?:y)?[- ]parity\b",
    r"\bcomplete parity\b(?! suite)",
    r"\bparity[- ]complete\b",
    r"\b100\s?%\s+(?:parity|compatib|ported)",
    r"\bfeature[- ]complete\b",
    r"\bdrop-in replacement\b",
    r"\bevery pi command\b",
    r"\ball (?:of )?pi(?:'s)? (?:slash )?commands\b",
    r"\bsame commands\b",
    r"\bsame requests\b",
]

# Byte-level identity holds only for named outputs: tool definitions and their
# order match Pi byte for byte, whole requests do not. The engineering ledgers
# (DIVERGENCES.md, docs/*.md specs) use these words for single outputs pinned by
# named tests, so the check applies them only to user-facing text.
BYTE_PHRASES = [
    r"\bbyte[- ]for[- ]byte\b",
    r"\bbyte[- ]identical\b",
]
USER_FACING = ("README.md", "CHANGELOG.md", "docs/site/", "internal/pigdocs/content/")  # and Go strings
TOOL_DEFINITIONS = re.compile(r"\btool definitions\b", re.IGNORECASE)

# Claims that need a recorded, public decision (AGENTS.md "Public project
# writing") or a release that does not exist yet. A line that negates or
# conditions the claim ("does not", "until", "not yet") is not a claim.
GOVERNANCE = [
    r"\bsponsored by\b",
    r"\bopen source program office\b",
    r"\bOSPO\b",
    r"\bcommunity[- ]owned\b",
    r"\bcommunity governance\b",
    r"\bofficial(?:ly)? (?:pi|affiliated|endorsed|supported)\b",
    r"\bendorsed by\b",
    r"\b(?:binaries|releases|artifacts) are (?:signed|notarized)\b",
    r"\b(?:ran|run|runs|running|used|deployed) in production\b",
]
NEGATION = re.compile(
    r"\b(?:not|no|never|none|without|until|unless|nor|forbid(?:s|den)?|avoid|refuse[sd]?)\b|n't\b",
    re.IGNORECASE,
)

# (path, text on the line, reason). Each entry must still match a line, so the
# list cannot outlive the text it excuses.
ALLOWED: list[tuple[str, str, str]] = []

PI_VERSION = re.compile(r"\bPi v?(\d+\.\d+(?:\.\d+)?)\b")
TAG_VERSION = re.compile(r"(?:earendil-works/pi/releases/tag/v|pi-coding-agent@|Pi%20\w+-)(\d+\.\d+\.\d+)")
NUMBER_CONTEXT = re.compile(r"\b(?:port(?:ed|ing)?|parity|coverage|verif\w*|intended-portable)\b", re.IGNORECASE)
PERCENT = re.compile(r"(?<![\w.])(\d+(?:\.\d+)?)\s?%")
RATIO = re.compile(r"\b(\d+)\s*(?:/|of)\s*(\d+)\s+(?:intended-portable|upstream files|PORT_MAP|ported)")

SKIP_DIRS = {"node_modules", "vendor", "public", ".next", "out"}
SKIP_ROOT_FILES = {"AGENTS.md", "PORT_MAP.md", "THIRD_PARTY_NOTICES.md"}
# Generated text the site serves as is. The site's page sources and the docs
# data it generates from docs/site/docs live in the private hosting
# repository, whose CI passes them to this script as extra paths.
SITE_TEXT = ("docs/knowledge-graph/pig-knowledge-graph.jsonld",)
# Go packages whose string literals reach users: pig's CLI and help, the
# coding agent's commands and messages, and the SDK's errors.
GO_DIRS = ("cmd", "internal", "coding")
# Consume comments and rune literals as whole tokens before looking for strings.
GO_TOKEN = re.compile(r"//[^\n]*|/\*[\s\S]*?\*/|'(?:[^'\\\n]|\\.)*'|(?P<string>\"(?:[^\"\\\n]|\\.)*\"|`[^`]*`)")
PIGLET_COMMAND = re.compile(r"\bpig\s+piglet\s+([a-z][a-z0-9-]*)\b")
MARKDOWN_HEADING = re.compile(r"^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$")


@dataclass(frozen=True)
class Coverage:
    ported: int
    intended: int
    porting_pct: str
    behavioral: int
    behavioral_pct: str


@dataclass
class Finding:
    path: str
    line: int
    kind: str
    detail: str
    text: str


def go_const(root: pathlib.Path, name: str, relpath: str = "coding/upstream.go") -> str:
    source = (root / relpath).read_text(encoding="utf-8")
    match = re.search(rf'^const {name} = "([^"]+)"$', source, re.MULTILINE)
    if not match:
        sys.exit(f"public-claims: {relpath} has no {name}")
    return match.group(1)


def coverage_block(root: pathlib.Path) -> Coverage:
    agents = (root / "AGENTS.md").read_text(encoding="utf-8")
    match = re.search(
        r"\*\*Porting:\*\* (\d+) / (\d+) intended-portable entries ✅ \((\d+\.\d)%\); "
        r"\*\*Verification:\*\* (\d+) behavioral \((\d+\.\d)%\)",
        agents,
    )
    if not match:
        sys.exit("public-claims: AGENTS.md has no generated coverage summary; run make coverage")
    ported, intended, porting_pct, behavioral, behavioral_pct = match.groups()
    return Coverage(int(ported), int(intended), porting_pct, int(behavioral), behavioral_pct)


def public_files(root: pathlib.Path) -> list[pathlib.Path]:
    files = [p for p in sorted(root.glob("*.md")) if p.name not in SKIP_ROOT_FILES]
    for base in ("docs", "internal/pigdocs/content"):
        for directory, subdirs, names in os.walk(root / base):
            subdirs[:] = sorted(d for d in subdirs if d not in SKIP_DIRS)
            files += [pathlib.Path(directory) / n for n in sorted(names) if n.endswith((".md", ".tsx", ".ts"))]
    return files + [root / name for name in SITE_TEXT if (root / name).exists()]


def go_files(root: pathlib.Path) -> list[pathlib.Path]:
    files = []
    for base in GO_DIRS:
        for directory, subdirs, names in os.walk(root / base):
            subdirs[:] = sorted(d for d in subdirs if d not in SKIP_DIRS and d != "testdata")
            files += [pathlib.Path(directory) / n for n in sorted(names) if n.endswith(".go") and not n.endswith("_test.go")]
    return files


def go_string_lines(path: pathlib.Path) -> list[tuple[int, str]]:
    """Each line of each Go string literal with its source line; comments are skipped."""
    source = path.read_text(encoding="utf-8")
    lines = []
    for match in GO_TOKEN.finditer(source):
        if match.group("string") is None:
            continue
        start = source.count("\n", 0, match.start()) + 1
        body = match.group(0)[1:-1]
        if match.group(0)[0] == '"':
            lines += [(start, part) for part in body.split("\\n")]
        else:
            lines += [(start + offset, part) for offset, part in enumerate(body.splitlines())]
    return lines


def piglet_help_commands(root: pathlib.Path) -> set[str]:
    path = root / "coding/piglet/main.go"
    if not path.exists():
        sys.exit("public-claims: coding/piglet/main.go is missing; cannot derive pig piglet --help")
    source = path.read_text(encoding="utf-8")
    help_function = re.search(r"func printHelp\(w io\.Writer\) \{(?P<body>.*?)\n\}", source, re.DOTALL)
    if not help_function:
        sys.exit("public-claims: coding/piglet/main.go has no parseable printHelp")
    commands = set()
    for token in GO_TOKEN.finditer(help_function.group("body")):
        literal = token.group("string")
        if literal is None:
            continue
        body = literal[1:-1]
        if literal.startswith('"'):
            body = body.replace(r"\n", "\n")
        commands.update(PIGLET_COMMAND.findall(body))
    if not commands:
        sys.exit("public-claims: pig piglet --help exposes no commands")
    return commands


def piglet_command_findings(path: pathlib.Path, name: str, known: set[str]) -> list[Finding]:
    if path.suffix != ".md" or not (name.startswith("docs/") or name.startswith("internal/pigdocs/")):
        return []
    planned_level: int | None = None
    findings = []
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        heading = MARKDOWN_HEADING.match(line)
        if heading:
            level = len(heading.group(1))
            if planned_level is not None and level <= planned_level:
                planned_level = None
            if re.search(r"\bPlanned\b", heading.group(2), re.IGNORECASE):
                planned_level = level
        if planned_level is not None:
            continue
        for command in PIGLET_COMMAND.finditer(line):
            verb = command.group(1)
            if verb not in known:
                findings.append(
                    Finding(
                        name,
                        number,
                        "piglet",
                        f"pig piglet {verb} is absent from pig piglet --help; move future syntax under a Planned heading",
                        line,
                    )
                )
    return findings


def display(path: pathlib.Path, root: pathlib.Path) -> str:
    try:
        return path.resolve().relative_to(root.resolve()).as_posix()
    except ValueError:
        return str(path)


def version_findings(line: str, pinned: str, reviewed: str) -> list[str]:
    problems = []
    for match in [*PI_VERSION.finditer(line), *TAG_VERSION.finditer(line)]:
        version = match.group(1)
        if version.count(".") == 1:
            if not pinned.startswith(version + "."):
                problems.append(f"Pi {version} is not the pinned Pi {pinned}")
            continue
        if version == pinned or (version == reviewed and "review" in line.lower()):
            continue
        problems.append(f"Pi {version} is not the pinned Pi {pinned}")
    return problems


def number_findings(line: str, cov: Coverage) -> list[str]:
    if not NUMBER_CONTEXT.search(line):
        return []
    problems = []
    current = {cov.porting_pct, cov.behavioral_pct}
    for match in PERCENT.finditer(line):
        value = match.group(1)
        if f"{float(value):.1f}" not in current:
            problems.append(f"{value}% is not a current porting ({cov.porting_pct}%) or verification ({cov.behavioral_pct}%) figure")
    for match in RATIO.finditer(line):
        pair = (int(match.group(1)), int(match.group(2)))
        if pair not in {(cov.ported, cov.intended), (cov.behavioral, cov.ported)}:
            problems.append(f"{pair[0]}/{pair[1]} is not the current {cov.ported}/{cov.intended} porting or {cov.behavioral}/{cov.ported} verification ratio")
    return problems


def quoted_and_negated(line: str, match: re.Match[str]) -> bool:
    """A negated line that quotes the phrase warns against it ("do not say ...")."""
    before, after = line[: match.start()], line[match.end():]
    return bool(NEGATION.search(line)) and before.count('"') % 2 == 1 and '"' in after


def claim_findings(line: str, user_facing: bool) -> list[tuple[str, str]]:
    found = []
    patterns = PHRASES[:]
    if user_facing and not TOOL_DEFINITIONS.search(line):
        patterns += BYTE_PHRASES
    for pattern in patterns:
        match = re.search(pattern, line, re.IGNORECASE)
        if match and not quoted_and_negated(line, match):
            found.append(("phrase", f"forbidden phrase {match.group(0)!r}"))
    if not NEGATION.search(line):
        for pattern in GOVERNANCE:
            match = re.search(pattern, line, re.IGNORECASE)
            if match:
                found.append(("governance", f"unrecorded claim {match.group(0)!r}"))
    return found


def scan(path: pathlib.Path, name: str, pinned: str, reviewed: str, cov: Coverage, user_facing: bool) -> list[Finding]:
    if path.suffix == ".go":
        lines = go_string_lines(path)
    else:
        lines = list(enumerate(path.read_text(encoding="utf-8").splitlines(), 1))
    findings = []
    for number, line in lines:
        for kind, detail in claim_findings(line, user_facing):
            findings.append(Finding(name, number, kind, detail, line))
        for detail in version_findings(line, pinned, reviewed):
            findings.append(Finding(name, number, "version", detail, line))
        for detail in number_findings(line, cov):
            findings.append(Finding(name, number, "numbers", detail, line))
    return findings


def readme_findings(root: pathlib.Path, cov: Coverage) -> list[Finding]:
    text = (root / "README.md").read_text(encoding="utf-8")
    block = re.search(r"<!-- BEGIN PORTING -->(.*?)<!-- END PORTING -->", text, re.DOTALL)
    if not block:
        return [Finding("README.md", 0, "readme", "no generated porting block; run make coverage", "")]
    required = [
        f"{cov.ported} of {cov.intended} intended-portable upstream files are ported ({cov.porting_pct}%)",
        f"{cov.behavioral} of the {cov.ported} ported files ({cov.behavioral_pct}%)",
    ]
    return [
        Finding("README.md", 0, "readme", f"porting block disagrees with the AGENTS.md coverage block: missing {want!r}; run make coverage", "")
        for want in required
        if want not in block.group(1)
    ]


def apply_allowlist(root: pathlib.Path, findings: list[Finding]) -> tuple[list[Finding], list[str]]:
    used = set()
    kept = []
    for finding in findings:
        excused = False
        for index, (path, needle, _) in enumerate(ALLOWED):
            if finding.kind == "phrase" and finding.path == path and needle in finding.text:
                used.add(index)
                excused = True
        if not excused:
            kept.append(finding)
    stale = [
        f"{path}: allowlist entry {needle!r} matches nothing"
        for index, (path, needle, _) in enumerate(ALLOWED)
        if index not in used and (root / path).exists()
    ]
    return kept, stale


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path(__file__).resolve().parents[2])
    parser.add_argument("extra", nargs="*", type=pathlib.Path, help="additional Markdown files to audit")
    args = parser.parse_args()
    root = args.root
    pinned = go_const(root, "UpstreamVersion", "coding/pigversion/pigversion.go")
    reviewed = go_const(root, "UpstreamReviewedVersion")
    cov = coverage_block(root)

    findings = readme_findings(root, cov)
    piglet_commands = piglet_help_commands(root)
    for path in public_files(root):
        name = display(path, root)
        findings += scan(path, name, pinned, reviewed, cov, name.startswith(USER_FACING))
        findings += piglet_command_findings(path, name, piglet_commands)
    for path in go_files(root):
        findings += scan(path, display(path, root), pinned, reviewed, cov, True)
    findings, stale = apply_allowlist(root, findings)
    for path in args.extra:
        findings += scan(path, display(path, root), pinned, reviewed, cov, True)

    for finding in findings:
        location = f"{finding.path}:{finding.line}" if finding.line else finding.path
        print(f"{location}: {finding.kind}: {finding.detail}")
        if finding.text:
            print(f"    {finding.text.strip()[:200]}")
    for message in stale:
        print(f"public-claims: {message}")
    if findings or stale:
        print(f"public-claims: {len(findings)} finding(s); fix the text or, for a true statement, extend ALLOWED with a reason", file=sys.stderr)
        return 1
    print(f"public-claims: no contradicted claims (Pi {pinned}; porting {cov.ported}/{cov.intended} = {cov.porting_pct}%)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
