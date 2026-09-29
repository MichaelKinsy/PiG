#!/usr/bin/env python3
"""Ratchet the reviewed ported-test baseline from an accepted committed mapping."""

import argparse
import json
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--policy", default="test/parity/interfaces/test-porting-policy-v0.87.1.json")
    args = parser.parse_args()
    path = Path(args.policy)
    policy = json.loads(path.read_text())
    commit = subprocess.check_output(["git", "rev-parse", "--verify", f"{args.commit}^{{commit}}"], text=True).strip()
    mapping_path = f"test/parity/interfaces/test-mapping-v{policy['upstreamVersion']}.json"
    mapping = json.loads(subprocess.check_output(["git", "show", f"{commit}:{mapping_path}"]))
    if mapping["upstreamVersion"] != policy["upstreamVersion"]:
        raise SystemExit("upstream version mismatch; review the new denominator")
    paths = sorted(entry["path"] for entry in mapping["entries"] if entry["disposition"] == "ported")
    if len(paths) < len(policy["baselinePorted"]):
        raise SystemExit("refusing to lower the committed ported-test baseline")
    policy["baselineCommit"] = commit
    policy["baselinePorted"] = paths
    path.write_text(json.dumps(policy, indent=2) + "\n")
    print(f"ported-test baseline: {len(paths)} paths at {commit}")


if __name__ == "__main__":
    main()
