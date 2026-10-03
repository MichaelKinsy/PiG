#!/usr/bin/env python3
"""List baseline Node shim stand-ins against the pinned Pi source inventory."""
import argparse
import json
from pathlib import Path
import re
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("--baseline", required=True)
parser.add_argument("--corpus", type=Path, required=True)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[3]
shims = "coding/extension/host/subprocess/runtime-node/shims"
pi_version = re.search(r'^const UpstreamVersion = "([^"]+)"', (root / "internal/coding/pigversion/pigversion.go").read_text(), re.M).group(1)
inv = json.loads((root / f"test/parity/interfaces/upstream-v{pi_version}.json").read_text())["interfaces"]
extra = {
    "pi-coding-agent": ["createAgentSession", "compact", "copyToClipboard", "BorderedLoader", "DynamicBorder", "initTheme", "getPackageDir", "KeybindingsManager"],
    "pi-tui": ["Component", "Focusable", "SelectItem", "TUI", "OverlayHandle", "EditorTheme"],
    "pi-ai": ["cleanupSessionResources", "registerSessionResourceCleanup"],
}
rows = []
for module in extra:
    source = subprocess.check_output(["git", "show", f"{args.baseline}:{shims}/{module}.mjs"], cwd=root, text=True)
    names = re.findall(r"export (?:const|class) (\w+)\s*(?:= hostOnly\w*\(|\{\})", source)
    for name in sorted(set(names + extra[module])):
        entrypoint = "./compat" if module == "pi-ai" else "."
        entries = [i for i in inv if i["package"] == "@earendil-works/" + module and i["entrypoint"] == entrypoint and i["name"] == name]
        reference = f"No runtime export in pinned Pi {pi_version}; stale shim-only value"
        disposition = "removed stale value"
        if entries:
            entry = entries[0]
            package = "tui" if module == "pi-tui" else "ai" if module == "pi-ai" else "coding-agent"
            source_path = entry["source"]["path"].split("dist/", 1)[-1]
            path = f"packages/{package}/src/" + source_path.replace(".d.ts", ".ts")
            lines = (root / ".upstream/current" / path).read_text().splitlines()
            line = next((n for n, text in enumerate(lines, 1) if re.search(r"(?:class|function|interface|type|const)\s+" + re.escape(name) + r"\b", text)), entry["source"]["line"])
            reference = f"{path}:{line}"
            disposition = "exact Pi module"
            if entry["kind"] not in ("class", "function", "variable") or (module == "pi-coding-agent" and name == "KeybindingsManager"):
                disposition = "removed type-only stand-in; Pi has no runtime export"
        if module == "pi-tui":
            disposition = "exact Pi capability cache (required by Theme initialization)" if name == "getCapabilities" else "owned by fix-node-scrollview; unchanged in this slice"
        rows.append({"module": module, "name": name, "source": reference, "disposition": disposition, "corpus": []})

by_name = {row["name"]: row for row in rows}
for package_file in (args.corpus / "homes").glob("*/pi/.pi/agent/npm/node_modules/**/package.json"):
    # Only the selected package tree, not a nested dependency installation.
    if str(package_file).count("/node_modules/") != 1:
        continue
    manifest = json.loads(package_file.read_text())
    if not manifest.get("pi"):
        continue
    for file in package_file.parent.rglob("*"):
        rel = file.relative_to(package_file.parent)
        if file.suffix not in (".ts", ".tsx", ".js", ".mjs") or any(part in ("node_modules", "test", "tests") for part in rel.parts):
            continue
        text = file.read_text(errors="replace")
        for match in re.finditer(r'import\s*\{([^}]+)\}\s*from\s*[\'\"](@[^\'\"]+)[\'\"]', text, re.S):
            if "pi-" not in match[2]:
                continue
            for imported in match[1].split(","):
                name = imported.strip().split(" as ")[0]
                if name in by_name:
                    use = manifest["name"] + ":" + str(rel)
                    if use not in by_name[name]["corpus"]:
                        by_name[name]["corpus"].append(use)

out = ["# Node shim stand-in audit", "", f"Baseline: `{args.baseline}`. Reference: Pi {pi_version}. Regenerate with `python3 test/parity/unit-evidence/audit-node-shims.py --baseline {args.baseline} --corpus <locked-corpus-root> --output test/parity/unit-evidence/node-shim-audit.md`.", "", "The table lists every throwing or empty stand-in in the three package shims, plus the partial helpers identified by inspection. Corpus names are static production imports from the locked corpus, not feature-pass claims. Tests and nested dependency installations are excluded. Real package execution is recorded separately in `fix-node-createagentsession.md`.", "", "| Module / export | Pi source | Disposition | Published corpus imports |", "|---|---|---|---|"]
for row in rows:
    out.append(f"| `{row['module']}.{row['name']}` | `{row['source']}` | {row['disposition']} | " + "<br>".join(f"`{use}`" for use in sorted(row["corpus"])) + " |")
out += ["", "## Other shim modules", "", "- `builtin-tools.mjs`: all tool factories/definitions, truncation helpers and the file-mutation queue now re-export `packages/coding-agent/src/core/tools/index.ts` and `truncate.ts`. The baseline substituted text-only `renderCall`/`renderResult` components and partially reimplemented file/image/tool behavior. The actual tool modules replace that implementation, including PowerShell and the image worker.", "- `proper-lockfile.mjs`: the baseline supplied only a partial `lockSync`, removed regular files, and omitted async `lock`. It now uses the exact locked `proper-lockfile@4.1.2`, including its transitive dependency versions. Pi callers are `packages/coding-agent/src/core/auth-storage.ts:52-189` and `core/settings-manager.ts:214-299`. Go auth, model, settings and trust stores use the same directory-lock protocol.", "- `pi-ai-bridge.mjs`: missing-key and pre-abort error streams implement normal API failures. Built-in API execution remains D74's Go-host bridge. `bridgeImages` remains D74's explicit unsupported image-generation result; the corpus audit found no production import of that capability. Provider-specific option/result gaps in D74 are not retired here.", "- `pig-config.mjs`: D2's separate configuration root is intentional, not a stand-in. Independent stores use this root.", "- `pi-agent-core.mjs`: the complete Pi module graph and default stream function are real implementations. There is no throwing Agent stand-in.", "- `pi-ai-oauth.mjs`: Pi's OAuth entry is type-only; its empty runtime namespace is correct.", "- `typebox*.mjs` and `jiti/*.mjs`: these are locked dependency modules. Their validation/resolution exceptions are ordinary library errors, not fabricated exports.", "", "An imported independent UI class is not the live Go Main Screen. Prototype patches or arbitrary main-process component references remain D73's identity boundary. Providing real imported constructors does not claim that every private main-screen patch in `pi-cc-extensions` affects PiG's Go objects."]
args.output.write_text("\n".join(out) + "\n")
