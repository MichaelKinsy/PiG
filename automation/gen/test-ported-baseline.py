#!/usr/bin/env python3
"""Ratchet the reviewed ported-test baseline from an accepted committed mapping."""

import argparse
import json
import re
from pathlib import Path
import subprocess


# The release squashes onto public main, so the anchor must be a commit every clone of it can fetch; keep this equal to defaultPublicMainRef in test/parity/cmd/testinventorycheck/release_policy.go. A clone that names the public remote differently passes its own remote-tracking ref with --public-main-ref. The name is fully qualified because Git's short-name lookup prefers a local tag or branch named origin/main over the remote-tracking ref.
PUBLIC_MAIN_REF = "refs/remotes/origin/main"


def version_key(version):
    return tuple(int(part) for part in version.split("."))


def previous_mapping(commit, version):
    """Return the newest committed mapping of an upstream version older than version."""
    names = subprocess.check_output(["git", "ls-tree", "-r", "--name-only", commit], text=True).split("\n")
    found = []
    for name in names:
        match = re.fullmatch(r"test-mapping-v(\d+(?:\.\d+)*)\.json", Path(name).name)
        if match and version_key(match.group(1)) < version_key(version):
            found.append((version_key(match.group(1)), name))
    if not found:
        raise SystemExit(f"commit {commit} has no mapping for {version} or an older upstream version")
    newest = max(found)[0]
    candidates = [name for key, name in found if key == newest]
    if len(candidates) != 1:
        raise SystemExit(f"commit {commit} has {len(candidates)} mappings of the newest older upstream version; the carried floor is ambiguous")
    return json.loads(subprocess.check_output(["git", "show", f"{commit}:{candidates[0]}"]))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--policy", default="test/parity/interfaces/test-porting-policy-v0.87.1.json")
    parser.add_argument("--public-main-ref", default=PUBLIC_MAIN_REF, help="local ref holding public main; the commit must be its ancestor")
    args = parser.parse_args()
    path = Path(args.policy)
    policy = json.loads(path.read_text())
    commit = subprocess.check_output(["git", "rev-parse", "--verify", f"{args.commit}^{{commit}}"], text=True).strip()
    public_main = args.public_main_ref
    if not public_main or public_main.startswith("-"):
        raise SystemExit(f"public main ref {public_main!r} must name a Git revision")
    reachable = subprocess.run(["git", "merge-base", "--is-ancestor", commit, public_main], capture_output=True, text=True)
    if reachable.returncode == 1:
        raise SystemExit(f"commit {commit} is not reachable from {public_main}; anchor the baseline on a public main commit")
    if reachable.returncode != 0:
        raise SystemExit(f"cannot check {commit} against {public_main} (fetch public main into it, or pass --public-main-ref with the ref that holds it): {reachable.stderr.strip()}")
    mapping_path = f"test/parity/interfaces/test-mapping-v{policy['upstreamVersion']}.json"
    shown = subprocess.run(["git", "show", f"{commit}:{mapping_path}"], capture_output=True)
    if shown.returncode == 0:
        mapping = json.loads(shown.stdout)
        if mapping["upstreamVersion"] != policy["upstreamVersion"]:
            raise SystemExit("upstream version mismatch; review the new denominator")
        paths = sorted(entry["path"] for entry in mapping["entries"] if entry["disposition"] == "ported")
        if len(paths) < len(policy["baselinePorted"]):
            raise SystemExit("refusing to lower the committed ported-test baseline")
    else:
        # The commit predates this upstream version (the first release of a leap): keep the stored baseline and require it to carry the previous floor, as the release gate does.
        paths = policy["baselinePorted"]
        current = {entry["path"] for entry in json.loads(Path(mapping_path).read_text())["entries"]}
        previous = previous_mapping(commit, policy["upstreamVersion"])
        dropped = sorted(entry["path"] for entry in previous["entries"] if entry["disposition"] == "ported" and entry["path"] in current and entry["path"] not in paths)
        if dropped:
            raise SystemExit(f"refusing to lower the committed ported-test baseline: {len(dropped)} carried paths dropped, first {dropped[0]}")
    policy["baselineCommit"] = commit
    policy["baselinePorted"] = paths
    path.write_text(json.dumps(policy, indent=2, ensure_ascii=False) + "\n")
    print(f"ported-test baseline: {len(paths)} paths at {commit}")


if __name__ == "__main__":
    main()
