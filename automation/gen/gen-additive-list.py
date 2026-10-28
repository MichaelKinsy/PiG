#!/usr/bin/env python3
"""Render the Additive features table of the agent docs page divergences.md.

Source: docs/additive-features.md, one `## D<N> <title>` record per feature. Each row is
the ID, the heading, and the record's one-sentence `Summary:` line.
The table sits between the additive-list markers in internal/pigdocs/content/divergences.md.
--check fails when the table is stale.
"""
import pathlib, re, sys

root = pathlib.Path(__file__).resolve().parents[2]
ledger = (root / "docs/additive-features.md").read_text().split("\n")
page = root / "internal/pigdocs/content/divergences.md"
START, END = "<!-- additive-list:start -->", "<!-- additive-list:end -->"


def plain(text):
    # Plain text keeps flag-like words out of the code spans the docs gates check against pig help.
    return text.replace("`", "").replace("|", "\\|")


def records():
    heads = [i for i, line in enumerate(ledger) if re.match(r"## D\d+ ", line)]
    for start, end in zip(heads, heads[1:] + [len(ledger)]):
        ident, title = ledger[start][3:].split(" ", 1)
        summaries = [line[len("Summary: "):].strip() for line in ledger[start + 1:end] if line.startswith("Summary: ")]
        if len(summaries) != 1:
            sys.exit(f"{ident}: docs/additive-features.md needs exactly one 'Summary: <sentence>' line, found {len(summaries)}")
        yield ident, plain(title), plain(summaries[0])


rows = ["| ID | Title | Summary |", "|---|---|---|"]
rows += [f"| {ident} | {title} | {summary} |" for ident, title, summary in records()]
block = START + "\n\n" + "\n".join(rows) + "\n\n" + END

text = page.read_text()
if START not in text or END not in text:
    sys.exit(f"{page.relative_to(root)} has no additive-list markers")
head, rest = text.split(START, 1)
rest = rest.split(END, 1)[1]
new = head + block + rest
if "--check" in sys.argv:
    if new != text:
        sys.exit("stale additive list in internal/pigdocs/content/divergences.md; run: make generate (or: make additive-list), then commit the result")
    sys.exit(0)
page.write_text(new)
print("wrote", page.relative_to(root))
