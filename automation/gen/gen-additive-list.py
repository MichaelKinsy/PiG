#!/usr/bin/env python3
"""Render the Additive features table of the agent docs page divergences.md.

Source: docs/additive-features.md, one `## D<N> <title>` record per feature. Each row is
the ID, the heading, and the first sentence of the record's first prose paragraph.
The table sits between the additive-list markers in internal/pigdocs/content/divergences.md.
--check fails when the table is stale.
"""
import pathlib, re, sys

root = pathlib.Path(__file__).resolve().parents[2]
ledger = (root / "docs/additive-features.md").read_text().split("\n")
page = root / "internal/pigdocs/content/divergences.md"
START, END = "<!-- additive-list:start -->", "<!-- additive-list:end -->"
LIMIT = 220


def plain(text):
    # Plain text keeps flag-like words out of the code spans the docs gates check against pig help.
    return text.replace("`", "").replace("|", "\\|")


def first_sentence(paragraph):
    paragraph = re.sub(r"^What:\s*", "", paragraph)
    paragraph = paragraph[:1].upper() + paragraph[1:]
    match = re.match(r"(.+?[.!?])(?:\s|$)", paragraph)
    text = match.group(1) if match else paragraph
    if len(text) > LIMIT:
        text = text[:LIMIT].rsplit(" ", 1)[0].rstrip(",;:(") + " ..."
    return plain(text)


def records():
    heads = [i for i, line in enumerate(ledger) if re.match(r"## D\d+ ", line)]
    for start, end in zip(heads, heads[1:] + [len(ledger)]):
        ident, title = ledger[start][3:].split(" ", 1)
        paragraphs, current, fenced = [], [], False
        for line in ledger[start + 1:end]:
            if line.startswith("```"):
                fenced = not fenced
                continue
            if fenced:
                continue
            if line.strip():
                current.append(line.strip())
            elif current:
                paragraphs.append(" ".join(current))
                current = []
        if current:
            paragraphs.append(" ".join(current))
        prose = [p for p in paragraphs if not p.startswith(("Stock disposition", "#", "|", "-", "*", "1.", "<!--"))]
        if not prose:
            sys.exit(f"{ident}: no prose paragraph in docs/additive-features.md")
        yield ident, plain(title), first_sentence(prose[0])


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
