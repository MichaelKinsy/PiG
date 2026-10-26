#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Propose a change to the CI image pins on a new branch, as one GitHub-signed commit.

The CI images workflow (.github/workflows/ci-images.yml) runs this from main with a write token and no pull request code.

  lock --evidence DIR     DIR holds the refresh's artifacts, DIR/pig-ci-<image>-evidence/packages.lock: the apk lock resolved from
                          the package index, validated by a build and a scan. Proposes the locks that differ from the tree's.
  pin --publication DIR   DIR holds the publication artifacts, DIR/pig-ci-<image>-publication/image-ref.txt: the digest-pinned
                          reference of the image just published. Proposes automation/images/<image>/published.env for it.

Environment: GH_TOKEN, REPO (owner/name), BASE_SHA (the main commit the proposal builds on), RUN_ID.

The branch is created from BASE_SHA and the commit goes through createCommitOnBranch, so GitHub signs it and it carries the
bot's Signed-off-by (.github/AGENTS.md, "Derived-file autofix"). The repository setting that lets Actions open pull requests may
be off; then the branch stays and the log names it.
"""
from __future__ import annotations

import argparse
import base64
import datetime
import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BOT = "github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>"
IMAGES = {"go": "ci-go", "parity": "ci-parity"}
REF = re.compile(r"^ghcr\.io/michaelkinsy/pig-ci-[a-z]+@sha256:[0-9a-f]{64}$")


class ProposalError(Exception):
    pass


def gh(*args: str, stdin: str | None = None) -> str:
    result = subprocess.run(["gh", *args], input=stdin, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise ProposalError(f"gh {' '.join(args[:2])}: {result.stderr.strip() or result.stdout.strip()}")
    return result.stdout


def propose(files: dict[str, bytes], branch: str, headline: str, body: str, repo: str, base_sha: str) -> None:
    if not files:
        raise ProposalError("nothing to propose")
    gh("api", f"repos/{repo}/git/refs", "-f", f"ref=refs/heads/{branch}", "-f", f"sha={base_sha}")
    message = f"{body}\n\nSigned-off-by: {BOT}"
    request = {
        "query": "mutation($input: CreateCommitOnBranchInput!) { createCommitOnBranch(input: $input) { commit { oid } } }",
        "variables": {
            "input": {
                "branch": {"repositoryNameWithOwner": repo, "branchName": branch},
                "expectedHeadOid": base_sha,
                "message": {"headline": headline, "body": message},
                "fileChanges": {"additions": [{"path": p, "contents": base64.b64encode(c).decode("ascii")} for p, c in sorted(files.items())]},
            }
        },
    }
    oid = json.loads(gh("api", "graphql", "--input", "-", stdin=json.dumps(request)))["data"]["createCommitOnBranch"]["commit"]["oid"]
    print(f"proposal: commit {oid} on {branch}")
    try:
        gh("pr", "create", "--repo", repo, "--base", "main", "--head", branch, "--title", headline, "--body", body)
    except ProposalError as error:
        # Settings -> Actions -> General -> "Allow GitHub Actions to create and approve pull requests" is off by default.
        print(f"::warning::branch {branch} holds the proposal but no pull request was opened ({error}); open one from https://github.com/{repo}/compare/main...{branch}")


def stamp(run_id: str) -> str:
    return f"{datetime.datetime.now(datetime.timezone.utc):%Y%m%d}-{run_id}"


def cmd_lock(args: argparse.Namespace, repo: str, base_sha: str, run_id: str) -> int:
    changed: dict[str, bytes] = {}
    for image, directory in IMAGES.items():
        resolved = Path(args.evidence) / f"pig-ci-{image}-evidence" / "packages.lock"
        if not resolved.is_file():
            print(f"proposal: no resolved lock for {image} (skipped for a commit pin, or the build failed)")
            continue
        path = f"automation/images/{directory}/packages.lock"
        data = resolved.read_bytes()
        if not data or any(not re.fullmatch(r"[A-Za-z0-9._+-]+=[A-Za-z0-9._+~-]+", line) for line in data.decode().splitlines()):
            raise ProposalError(f"{resolved} is not an apk lock")
        if data != (ROOT / path).read_bytes():
            changed[path] = data
    if not changed:
        print("proposal: the apk locks are current")
        return 0
    names = ", ".join(sorted(changed))
    propose(
        changed,
        f"ci-images/lock-{stamp(run_id)}",
        "ci(images): refresh the apk locks from the current package index",
        f"The scheduled CI images refresh resolved the locked packages against the current package index, built the images from the\nresolved locks, and validated and scanned the result ({names}).\n\nAfter this merges, dispatch CI images from main with publish enabled; its pin proposal records the new image digest.",
        repo,
        base_sha,
    )
    return 0


def cmd_pin(args: argparse.Namespace, repo: str, base_sha: str, run_id: str) -> int:
    files: dict[str, bytes] = {}
    for image, directory in IMAGES.items():
        record = Path(args.publication) / f"pig-ci-{image}-publication" / "image-ref.txt"
        if not record.is_file():
            continue
        ref = record.read_text(encoding="utf-8").strip()
        if not REF.fullmatch(ref):
            raise ProposalError(f"{record}: {ref!r} is not a digest-pinned ghcr.io/michaelkinsy/pig-ci-* reference")
        inputs = subprocess.run([str(ROOT / "automation/ci/ci-image-inputs.sh"), image], capture_output=True, text=True, check=True).stdout.strip()
        # REUSE-IgnoreStart
        files[f"automation/images/{directory}/published.env"] = (
            "# SPDX-License-Identifier: MIT\n#\n"
            "# The published CI image this tree pins by digest, and the SHA-256 of the inputs it was built from\n"
            "# (automation/ci/ci-image-inputs.sh). Written by the CI images workflow's pin proposal; do not edit.\n\n"
            f"IMAGE_REF={ref}\nINPUTS_SHA256={inputs}\n"
        ).encode()
        # REUSE-IgnoreEnd
    if not files:
        raise ProposalError("no publication record found")
    propose(
        files,
        f"ci-images/pin-{stamp(run_id)}",
        "ci(images): pin the published CI images by digest",
        "The CI images workflow published these images from main after validation. Pull requests verify the pinned digests; the\nrecorded input hashes say which tree each image was built from.",
        repo,
        base_sha,
    )
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("lock").add_argument("--evidence", required=True)
    sub.add_parser("pin").add_argument("--publication", required=True)
    args = parser.parse_args(argv)
    try:
        repo, base_sha, run_id = (os.environ.get(k, "") for k in ("REPO", "BASE_SHA", "RUN_ID"))
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo) or not re.fullmatch(r"[0-9a-f]{40}", base_sha) or not re.fullmatch(r"[0-9]+", run_id):
            raise ProposalError("REPO, BASE_SHA and RUN_ID must be set to an owner/name, a commit id and a run id")
        return {"lock": cmd_lock, "pin": cmd_pin}[args.command](args, repo, base_sha, run_id)
    except ProposalError as error:
        print(f"proposal: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
