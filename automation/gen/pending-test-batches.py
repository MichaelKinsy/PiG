#!/usr/bin/env python3
"""Render non-overlapping upstream test-file batches from a committed baseline and reviewed path policy."""

import argparse
import collections
import hashlib
import json
from pathlib import Path
import subprocess


def group_for(path, area):
    name = Path(path).name
    if name == "validation.test.ts":
        return "validation"
    if "/harness/runtime/" in path:
        return "harness-runtime"
    if "/harness/execution-" in path:
        return "harness-execution"
    if path.startswith("packages/agent/"):
        return "agent-loop" if "/harness/" not in path else "harness"
    if area == "providers":
        for token in ("anthropic", "openai-completions", "openai-responses", "openai-codex", "azure", "google", "bedrock", "mistral", "cloudflare"):
            if token in name:
                return token
        if any(token in name for token in ("oauth", "auth", "credential")):
            return "credentials"
        if any(token in name for token in ("model", "thinking", "catalog")):
            return "catalog-options"
        return "shared-streaming"
    if area == "sessions":
        if any(token in name for token in ("compact", "branch-summary", "branch-summar")):
            return "compaction"
        if "/session-manager/" in path or any(token in name for token in ("session-file", "migration", "session-cwd", "session-info")):
            return "persistence"
        return "session-runtime"
    if area == "extensions":
        if any(token in name for token in ("skills", "prompt-template", "resource", "discovery", "loader", "factory", "package")):
            return "loading-resources"
        return "callbacks"
    if area == "tui":
        if any(token in name for token in ("editor", "input", "keys", "keybinding", "word-navigation", "autocomplete", "stdin")):
            return "input"
        if any(token in name for token in ("clipboard", "image", "native", "terminal")):
            return "terminal"
        return "rendering"
    if area == "cli":
        if "rpc" in name:
            return "rpc"
        if "settings" in name or "config" in name:
            return "settings"
        if "package" in name or "git-" in name:
            return "packages"
        return "startup-modes"
    return "builtin-tools" if area == "tools" else "support"


def targets(entry, area):
    found = set()
    for evidence in entry.get("evidence", []):
        path = evidence.split("#", 1)[0]
        if path.endswith("_test.go") and path.startswith(("agent/", "ai/", "coding/", "internal/", "cmd/", "tui/")):
            found.add(str(Path(path).parent))
    path = entry["path"]
    if path.startswith("packages/ai/"):
        found.add("agent" if path.endswith("/validation.test.ts") else "ai")
    elif path.startswith("packages/agent/"):
        if "/harness/runtime/" in path:
            found.update(("agent/harness/runtime", "agent/harness/execution"))
        elif "/harness/execution-" in path:
            found.add("agent/harness/execution")
        elif path.endswith("/harness/context.test.ts"):
            found.add("agent/harness")
        else:
            found.add("agent")
    elif path.startswith("packages/tui/"):
        found.add("tui")
    else:
        found.update({
            "tools": ("internal/codingagent/tools", "coding"),
            "providers": ("ai", "coding", "internal/codingagent"),
            "sessions": ("coding", "internal/codingagent"),
            "extensions": ("coding", "coding/extension/host/inproc", "coding/extension/host/subprocess"),
            "tui": ("internal/codingagent", "tui"),
            "cli": ("cmd/pig", "internal/codingagent"),
            "utilities": ("internal/codingagent",),
        }[area])
        if "compact" in path or "branch-summar" in path:
            found.add("internal/codingagent/compaction")
    return ", ".join(f"`./{target}`" for target in sorted(found))


def baseline_mapping(commit, mapping_path):
    """Read the mapping at the baseline commit, which may predate the move of parity/ under test/."""
    legacy = mapping_path.removeprefix("test/")
    for candidate in (mapping_path, legacy):
        result = subprocess.run(["git", "show", f"{commit}:{candidate}"], capture_output=True)
        if result.returncode == 0:
            return result.stdout
    raise RuntimeError(f"{commit} has neither {mapping_path} nor {legacy}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline-commit", required=True)
    parser.add_argument("--version", default="0.87.1")
    parser.add_argument("--out", default="docs/parity/pending-tests-batches.md")
    args = parser.parse_args()
    mapping_path = f"test/parity/interfaces/test-mapping-v{args.version}.json"
    baseline = json.loads(baseline_mapping(args.baseline_commit, mapping_path))
    current_bytes = Path(mapping_path).read_bytes()
    current = {entry["path"]: entry for entry in json.loads(current_bytes)["entries"]}
    inventory = json.loads(Path(f"test/parity/interfaces/upstream-tests-v{args.version}.json").read_text())
    files = {entry["path"]: entry for entry in inventory["files"]}
    policy = json.loads(Path(f"test/parity/interfaces/test-porting-policy-v{args.version}.json").read_text())
    reviews = {entry["path"]: entry for entry in policy["entries"]}
    obligations = [entry for entry in baseline["entries"] if entry["disposition"] in ("pending", "partial")]
    groups = collections.defaultdict(list)
    area_order = ["tools", "providers", "sessions", "extensions", "tui", "cli", "utilities"]
    for entry in obligations:
        review = reviews[entry["path"]]
        hot = "hot-path" in review["tags"]
        area = review["area"]
        groups[(not hot, area_order.index(area), group_for(entry["path"], area))].append(entry)
    batches = []
    for key in sorted(groups):
        chunk, cases = [], 0
        for entry in sorted(groups[key], key=lambda entry: entry["path"]):
            count = files[entry["path"]]["caseCount"]
            if chunk and (len(chunk) >= 8 or cases + count > 120):
                batches.append((key, chunk))
                chunk, cases = [], 0
            chunk.append(entry)
            cases += count
        if chunk:
            batches.append((key, chunk))
    assigned = [entry["path"] for _, batch in batches for entry in batch]
    expected = {entry["path"] for entry in obligations}
    if len(assigned) != len(set(assigned)) or set(assigned) != expected:
        raise RuntimeError("batches do not partition the complete baseline exactly once")
    disposition_counts = collections.Counter(entry["disposition"] for entry in obligations)
    out = [
        "# Upstream test porting batches", "", "## Dispatch contract", "",
        f"Baseline: `{args.baseline_commit}`, Pi {args.version}. This document partitions every baseline pending and partial test file exactly once: **{disposition_counts['pending']} pending + {disposition_counts['partial']} partial = {len(obligations)} files**, across **{len(batches)} batches**. Counts come from `upstream-tests-v{args.version}.json`; they are case sites, not expanded `it.each` matrices or a count of cases still missing in partial files.", "",
        "Hot-path batches come first. Target Go packages combine existing evidence locations with the owning production area; they are navigation targets, not proof of closure. New harness execution/runtime packages need their separate API and production callers, not substitutions from the older agent loop. Read the mapping rationale to enumerate missing cases before changing a partial entry.", "",
        "Assign one owner per upstream test file. Do not dispatch the validation batch to this slice: `fix-tool-arg-coercion` owns it. The six tool-regression files already closed here remain in their original batches with current disposition `ported`, so no baseline obligation disappears. Dispatch only rows still pending or partial. Test-file assignments do not authorize simultaneous edits to shared Go packages, SDK protocols, or generated ledgers; the lead coordinates those collisions and serializes integration and shared inventory updates.", "",
        f"Current mapping SHA-256: `{hashlib.sha256(current_bytes).hexdigest()}`. Current dispositions below are a generated snapshot; the JSON mapping remains authoritative.", "",
        "Regenerate after integration:", "", "```bash",
        f"python3 automation/gen/pending-test-batches.py --baseline-commit {args.baseline_commit} --version {args.version}",
        "```", "", "## Batch index", "",
        "| Batch | Priority | Area | Files | Upstream case sites | Still pending/partial |",
        "|---|---|---|---:|---:|---:|",
    ]
    named = []
    for number, (key, batch) in enumerate(batches, 1):
        cold, area_index, group = key
        area = area_order[area_index]
        batch_id = f"PT-{number:03d}-{area}-{group}"
        named.append((batch_id, key, batch))
        count = sum(files[entry["path"]]["caseCount"] for entry in batch)
        remaining = sum(current[entry["path"]]["disposition"] in ("pending", "partial") for entry in batch)
        out.append(f"| {batch_id} | {'reviewed non-hot' if cold else 'hot-path'} | {area} | {len(batch)} | {count} | {remaining} |")
    for batch_id, key, batch in named:
        area = area_order[key[1]]
        out.extend(("", f"## {batch_id}", "", "| Upstream test file | Case sites | Baseline | Current | Target Go packages |", "|---|---:|---|---|---|"))
        for entry in batch:
            path = entry["path"]
            out.append(f"| `{path}` | {files[path]['caseCount']} | {entry['disposition']} | {current[path]['disposition']} | {targets(current[path], area)} |")
    Path(args.out).write_text("\n".join(out) + "\n")
    print(f"partitioned {len(obligations)} files into {len(batches)} non-overlapping batches")


if __name__ == "__main__":
    main()
