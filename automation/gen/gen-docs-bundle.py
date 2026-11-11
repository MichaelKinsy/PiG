#!/usr/bin/env python3
"""Generate the shared pages of the agent docs bundle from the site pages.

The site (docs/site/docs/) is the one source of the pages both trees share. The
agent bundle (internal/pigdocs/content/) is flat and embedded in every binary, so
this script copies each shared page into it and rewrites what a flat bundle cannot
hold. The manifest internal/pigdocs/bundle-manifest.json lists:

  pages      site page -> bundle page, with optional heading blocks to drop
  slug_map   site page -> hand-written bundle page of the same subject
  anchor_map site page#anchor -> bundle page#anchor, for content the bundle keeps elsewhere
  site_only  site pages the bundle links by URL on purpose
  glossary   the glossary page, rendered from docs/project/CONTEXT.md into both trees
  blocks     fragments rendered into marked blocks of hand-written pages
  agent_only bundle pages written by hand that have no site page
  deferred_pairs hand-written bundle pages whose site page differs; they move to pages one by one
  other_generators  bundle pages another generator writes

Link rewrite for a bundle page: a link to a page that ships in the bundle becomes
`page.md#anchor`; a link to a site-only page becomes the site URL; a repository
path becomes a GitHub URL; a root-absolute site path becomes a site URL. Site-only
constructs (<video>, <iframe>, and <!-- site-only:begin --> ... <!-- site-only:end -->)
are stripped. Every generated page starts with a do-not-edit header.

--check fails when any output is stale or a bundle page has no classification.
--root <dir> runs on another checkout, for tests.
"""
import json, pathlib, re, sys

root = pathlib.Path(__file__).resolve().parents[2]
if "--root" in sys.argv:  # the drift test runs the generator on a scratch copy
    root = pathlib.Path(sys.argv[sys.argv.index("--root") + 1]).resolve()
manifest_path = root / "internal/pigdocs/bundle-manifest.json"
manifest = json.loads(manifest_path.read_text())
site_dir = root / "docs/site/docs"
bundle_dir = root / "internal/pigdocs/content"
site_url = manifest["site_url"].rstrip("/")
repo_url = manifest["repo_url"].rstrip("/")


def fail(msg):
    sys.exit(f"gen-docs-bundle: {msg}")


# The bundle pages a link can reach: every classified bundle page.
hand_written = set(manifest["agent_only"]) | set(manifest["deferred_pairs"])
bundle_pages = hand_written | set(manifest["other_generators"])
bundle_pages |= {p["bundle"] for p in manifest["pages"]}
bundle_pages |= {manifest["glossary"]["bundle"]}
site_pages = {p.name for p in site_dir.glob("*.md")} | {manifest["glossary"]["site"]}

FENCE = re.compile(r"^\s*(`{3,}|~{3,})")
LINK = re.compile(r"(\]\()([^)\s]+)((?:\s+\"[^\"]*\")?\))")
HTML_REF = re.compile(r"""(<(?:img|a|source)\s[^>]*?\b(?:src|href)=")([^"]+)(")""")
SITE_ONLY = re.compile(r"<!--\s*site-only:begin\s*-->.*?<!--\s*site-only:end\s*-->\n?", re.S)
MEDIA = re.compile(r"<(video|iframe)\b[^>]*>.*?</\1>\n?|<(?:video|iframe)\b[^>]*/>\n?", re.S)


def rewrite_target(target, src_name, to_bundle):
    """Return the bundle form of a link target found in a site page."""
    t = target.strip("<>")
    path, sep, anchor = t.partition("#")
    frag = sep + anchor
    m = re.fullmatch(r"(?:https://pi-in-go\.dev)?/docs/(?:latest/?)?([a-z0-9-]*)/?", path)
    if m:
        slug = m[1] or "index"
        return page_link(slug + ".md", frag, to_bundle)
    if path.startswith("https://pi-in-go.dev") or re.match(r"[a-z][a-z0-9+.-]*:", path):
        return target
    if path == "":
        return target
    if path.startswith("/"):
        return site_url.rsplit("/docs/", 1)[0] + path + frag
    if "/" not in path and path.endswith(".md"):
        return page_link(path, frag, to_bundle)
    # A repository-relative path: resolve it from docs/site/docs.
    resolved = pathlib.PurePosixPath("docs/site/docs") / path
    parts = []
    for part in resolved.parts:
        if part == "..":
            if not parts:
                fail(f"{src_name}: link {target} leaves the repository")
            parts.pop()
        elif part != ".":
            parts.append(part)
    rel = "/".join(parts)
    mapped = manifest["link_map"].get(rel)
    if mapped:
        return mapped + frag
    kind = "tree" if (root / rel).is_dir() else "blob"
    return f"{repo_url}/{kind}/main/{rel}{frag}"


SLUG_DROP = re.compile(r"[^\w\s-]", re.A)
HTML_ANCHOR = re.compile(r'<a\s+(?:id|name)="([^"]+)"')
_anchor_cache = {}


def anchors_of(path):
    """Heading anchors of a Markdown file, as the site slugs them."""
    if path not in _anchor_cache:
        found = set()
        fenced = False
        text = path.read_text()
        for line in text.split("\n"):
            if line.strip().startswith("```"):
                fenced = not fenced
            m = None if fenced else re.match(r"^#{1,6}\s+(.+?)\s*#*$", line)
            if m:
                slug = re.sub(r"<[^>]*>", "", m[1].lower())
                slug = SLUG_DROP.sub("", slug)
                found.add(re.sub(r"\s+", "-", slug.strip()))
        found |= {a.lower() for a in HTML_ANCHOR.findall(text)}
        _anchor_cache[path] = found
    return _anchor_cache[path]


def page_link(name, frag, to_bundle):
    if not to_bundle:
        return f"/docs/latest/{name[:-3]}{frag}" if name != "index.md" else f"/docs/latest/{frag}"
    # A site page or anchor whose content the bundle holds under another page or anchor.
    for key in (name + frag, name):
        if key in manifest["anchor_map"] and (key == name + frag or not frag):
            return manifest["anchor_map"][key]
    mapped = manifest["slug_map"].get(name, name)
    if mapped in bundle_pages:
        # A hand-written bundle page can lack the anchor the site page has; the site URL keeps the link valid.
        target = bundle_dir / mapped
        if frag and mapped in hand_written and target.exists() and frag[1:].lower() not in anchors_of(target) and name in site_pages:
            return f"{site_url}/{name[:-3]}{frag}"
        return mapped + frag
    if name not in site_pages:
        fail(f"a link names {name}, which is neither a bundle page nor a site page")
    return f"{site_url}/{name[:-3]}{frag}" if name != "index.md" else f"{site_url}/{frag}"


def rewrite_links(text, src_name, to_bundle):
    out = []
    fenced = None
    for line in text.split("\n"):
        m = FENCE.match(line)
        if m:
            marker = m[1][0] * 3
            if fenced is None:
                fenced = marker
            elif line.strip().startswith(fenced):
                fenced = None
            out.append(line)
            continue
        if fenced:
            out.append(line)
            continue
        # Skip inline code spans so an example link in backticks stays literal.
        pieces = re.split(r"(`[^`]*`)", line)
        for i in range(0, len(pieces), 2):
            seg = LINK.sub(lambda k: k[1] + rewrite_target(k[2], src_name, to_bundle) + k[3], pieces[i])
            seg = HTML_REF.sub(lambda k: k[1] + rewrite_target(k[2], src_name, to_bundle) + k[3], seg)
            pieces[i] = seg
        out.append("".join(pieces))
    return "\n".join(out)


def drop_heading_blocks(text, headings, src_name):
    lines = text.split("\n")
    for heading in headings:
        level = len(heading) - len(heading.lstrip("#"))
        start = next((i for i, l in enumerate(lines) if l == heading), None)
        if start is None:
            fail(f"{src_name}: drop heading {heading!r} not found")
        end = len(lines)
        fenced = False
        for j in range(start + 1, len(lines)):
            if FENCE.match(lines[j]):
                fenced = not fenced
            m = re.match(r"^(#+)\s", lines[j])
            if not fenced and m and len(m[1]) <= level:
                end = j
                break
        del lines[start:end]
    return "\n".join(lines)


def header(source):
    return (f"<!-- Generated from {source} by automation/gen/gen-docs-bundle.py. "
            "Do not edit this file: edit the source and run `make generate`. -->\n\n")


def bundle_page(source_rel, text, drops, name):
    text = SITE_ONLY.sub("", text)
    text = MEDIA.sub("", text)
    if drops:
        text = drop_heading_blocks(text, drops, name)
    text = rewrite_links(text, name, True)
    return header(source_rel) + text


# --- glossary -------------------------------------------------------------
def glossary_site():
    g = manifest["glossary"]
    ctx = (root / g["source"]).read_text()
    sections = re.findall(r"^## ([^\n]+)\n\n(.*?)(?=^## |\Z)", ctx, re.S | re.M)
    if not sections:
        fail(f"{g['source']} has no terms")
    out = ["# Glossary", "",
           "This page defines the terms PiG documentation uses. Each term has its definition, where you meet it, and the page that explains it. "
           "The definitions come from `docs/project/CONTEXT.md`. Edit that file, then run `make generate`.", ""]
    for term, body in sections:
        if term not in g["where"]:
            fail(f"glossary: no 'where' entry for the term {term!r}; add it to the manifest")
        meet, link = g["where"][term]
        out += [f"## {term}", "", " ".join(body.split()), "", f"Where you meet it: {meet}", ""]
        if link:
            out += [f"Read more: {link}", ""]
    unknown = set(g["where"]) - {t for t, _ in sections}
    if unknown:
        fail(f"glossary: manifest names terms that CONTEXT.md lacks: {sorted(unknown)}")
    return "\n".join(out).rstrip("\n") + "\n"


# --- fragment blocks ------------------------------------------------------
def render_block(path, name, body):
    pat = re.compile(rf"(<!-- gen:begin {re.escape(name)} -->\n).*?(<!-- gen:end {re.escape(name)} -->)", re.S)
    text = path.read_text()
    if not pat.search(text):
        fail(f"{path.relative_to(root)} has no gen:begin/gen:end markers for {name}")
    return pat.sub(lambda m: m[1] + body.rstrip("\n") + "\n" + m[2], text, count=1)


outputs = {}
shared_source = {}
for page in manifest["pages"]:
    src = site_dir / page["site"]
    if not src.exists():
        fail(f"site page {page['site']} does not exist")
    outputs[bundle_dir / page["bundle"]] = bundle_page(f"docs/site/docs/{page['site']}", src.read_text(), page.get("drop_headings", []), page["site"])

g = manifest["glossary"]
gtext = glossary_site()
outputs[site_dir / g["site"]] = gtext
outputs[bundle_dir / g["bundle"]] = bundle_page(g["source"], gtext, [], g["site"])

for block in manifest["blocks"]:
    frag = (root / block["fragment"]).read_text()
    for target in block["targets"]:
        path = root / target["file"]
        body = rewrite_links(frag, block["fragment"], target["bundle"]) if target["bundle"] else frag
        outputs[path] = render_block(path, block["name"], body)

# Classification: every bundle page is generated, agent-only, or written by another generator.
classified = {p["bundle"] for p in manifest["pages"]} | {g["bundle"]} | bundle_pages
actual = {p.name for p in bundle_dir.glob("*.md")} | {o.name for o in outputs if o.parent == bundle_dir}
problems = []
if actual - classified:
    problems.append(f"unclassified bundle pages (add to agent_only, deferred_pairs or pages in {manifest_path.relative_to(root)}): {sorted(actual - classified)}")
if classified - actual:
    problems.append(f"manifest names missing bundle pages: {sorted(classified - actual)}")
for name in set(manifest["agent_only"]) & site_pages:
    problems.append(f"{name} is in agent_only but the site has a page of that name; list it in deferred_pairs")

stale = [str(p.relative_to(root)) for p, text in outputs.items() if not p.exists() or p.read_text() != text]
if "--check" in sys.argv:
    if problems:
        sys.exit("; ".join(problems))
    if stale:
        sys.exit("stale generated docs; run: make generate (or: make docs-bundle), then commit the result: " + ", ".join(stale))
    sys.exit(0)
if problems:
    sys.exit("; ".join(problems))
for path, text in outputs.items():
    path.write_text(text)
print("wrote", len(outputs), "docs pages")
