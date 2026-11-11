#!/usr/bin/env python3
"""Render the agent docs page additive-features.md.

Source: docs/additive-features.md, one `## D<N> <title>` record per feature. Each record
carries a one-sentence `Summary:` line and an `Audience: author|runtime` line. The page
lists the `author` records (how to extend or modify PiG) as ID, title, and summary.
--check fails when the page is stale.
"""
import pathlib, re, sys

root = pathlib.Path(__file__).resolve().parents[2]
ledger = (root / "docs/additive-features.md").read_text().split("\n")
page = root / "internal/pigdocs/content/additive-features.md"


def plain(text):
    # Plain text keeps flag-like words out of the code spans the docs gates check against pig help.
    return text.replace("`", "").replace("|", "\\|")


def field(ident, lines, name):
    values = [line[len(name) + 2:].strip() for line in lines if line.startswith(name + ": ")]
    if len(values) != 1:
        sys.exit(f"{ident}: docs/additive-features.md needs exactly one '{name}: ...' line, found {len(values)}")
    return values[0]


def records():
    heads = [i for i, line in enumerate(ledger) if re.match(r"## D\d+ ", line)]
    for start, end in zip(heads, heads[1:] + [len(ledger)]):
        ident, title = ledger[start][3:].split(" ", 1)
        lines = ledger[start + 1:end]
        audience = field(ident, lines, "Audience")
        if audience not in ("author", "runtime"):
            sys.exit(f"{ident}: Audience must be 'author' or 'runtime', not {audience!r}")
        yield ident, plain(title), plain(field(ident, lines, "Summary")), audience


all_records = list(records())
author = [r for r in all_records if r[3] == "author"]
runtime = len(all_records) - len(author)
rows = ["| ID | Title | Summary |", "|---|---|---|"]
rows += [f"| {ident} | {title} | {summary} |" for ident, title, summary, _ in author]
text = (
    "# Additive features\n\n"
    "These PiG-only features have no Pi equivalent. This page lists the ones that matter when you extend or modify PiG: "
    "extensions and SDKs, Piglets and Binaries, and diagnostic commands. "
    f"{runtime} more additive records cover performance, crash handling, security, and platform details; they do not change how you author. "
    "Search this page with `grep -n -i <word>`. "
    "[Divergences](divergences.md) lists the places where PiG behaves differently from Pi.\n\n"
    + "\n".join(rows) + "\n"
)
if "--check" in sys.argv:
    if not page.exists() or page.read_text() != text:
        sys.exit("stale internal/pigdocs/content/additive-features.md; run: make generate (or: make additive-list), then commit the result")
    sys.exit(0)
page.write_text(text)
print("wrote", page.relative_to(root))
