# SPDX-License-Identifier: MIT
"""Work the Go port carries ahead of the pin, retired by the bump that reaches it.

Two kinds are carried, and `make pin-bump` decides each one against the target Pi before it moves the pin (`retire`):

* post-release backports: the entries of automation/gen/pi-post-release-backports.mjs, one block per Pi commit. Each entry is classified
  against the target's own installed package (the oracle built without backports): `carried` (the target already holds the change: the
  block is deleted), `pending` (the target is the build the entry was written for: it stays and must still apply), or `conflict` (neither:
  the bump stops naming the entry, the file and the first differing line).
* the carried oracle: tests of a Pi commit newer than the pin (Makefile CARRIED_ORACLE_REF) that run against that commit's build. When
  GitHub's compare says the target contains the commit, its make target, CI step, skip helpers, reviewed decisions and frozen files are
  deleted; otherwise it stays.

Every decision, with its evidence, is returned for the bump's state file and printed in its summary.
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parent))
import oracle  # noqa: E402
import pinlib  # noqa: E402
from pinlib import ROOT, Pin, PinError  # noqa: E402


# -- backports ----------------------------------------------------------------------------------------------------------

def decide_backports(results: list[dict], contains: Callable[[str], dict]) -> dict[str, dict]:
    """One decision per commit from the per-file classifications of the script: {commit: {state, files, detail, evidence}}.

    The target's own text decides when it can: every file of the entry holds the entry's after-text (carried) or its before-text (pending).
    Pi's version of a change is rarely byte for byte the backport's (the backport adds a note line), so a file that has lost the entry's
    before-text without holding its after-text is settled by `contains(commit)`: GitHub's compare of the commit with the target. A commit
    the target contains whose old text is gone from every such file makes the entry carried; anything else is a conflict."""
    by_id: dict[str, list[dict]] = {}
    for result in results:
        by_id.setdefault(result["id"], []).append(result)
    out = {}
    for ident, blocks in by_id.items():
        files = [b["key"] for b in blocks]
        states = {b["state"] for b in blocks}
        if states == {"carried"}:
            out[ident] = {"state": "carried", "files": files, "detail": "", "evidence": "the target holds the entry's text in every file"}
        elif states == {"pending"}:
            out[ident] = {"state": "pending", "files": files, "detail": "", "evidence": "the target holds the old text the entry changes in every file"}
        else:
            conflicts = [b for b in blocks if b["state"] == "conflict"]
            others = [b for b in blocks if b["state"] != "conflict"]
            first = conflicts[0] if conflicts else None
            detail = f"{first['key']}: {first['detail']}" if first else f"carried in {', '.join(b['key'] for b in blocks if b['state'] == 'carried')} but not in the other files of the entry"
            if conflicts and all(b["beforeGone"] for b in conflicts) and all(b["state"] == "carried" for b in others):
                answer = contains(ident)
                if answer["status"] in ("ahead", "identical"):
                    gone = ", ".join(b["key"] for b in conflicts)
                    out[ident] = {"state": "carried", "files": files, "detail": "", "evidence": f"GitHub compare {ident}...target: {answer['status']}; the old text is gone from {gone}, which holds Pi's own version of the change"}
                    continue
                detail += f" (GitHub compare {ident}...target: {answer['status']}: the target does not contain the commit)"
            out[ident] = {"state": "conflict", "files": files, "detail": detail, "evidence": ""}
    return out


def classify_backports(modules: Path, contains: Callable[[str], dict]) -> dict[str, dict]:
    """Classify every entry of the script against the Pi packages installed under `modules` (an oracle built without backports)."""
    done = oracle.backports_node("--classify", *(f"{name}={place}" for name, place in oracle.backport_places(modules).items()))
    if done.returncode != 0:
        raise PinError(f"the backports script cannot classify its entries:\n{done.stderr.strip()}")
    return decide_backports(json.loads(done.stdout), contains)


def retire_backport_blocks(text: str, carried: list[str]) -> str:
    for ident in carried:
        text, n = re.subn(rf"^// BEGIN backport {re.escape(ident)}\n.*?^// END backport {re.escape(ident)}\n\n?", "", text, flags=re.M | re.S)
        if not n:
            raise PinError(f"automation/gen/pi-post-release-backports.mjs has no block for the entry {ident}")
    return text


# -- the carried oracle -------------------------------------------------------------------------------------------------

CARRIED_REF = re.compile(r"^CARRIED_ORACLE_REF := ([0-9a-f]{40})$", re.M)


def carried_oracle_ref(root: Path = ROOT) -> str | None:
    found = CARRIED_REF.search((root / "Makefile").read_text(encoding="utf-8"))
    return found.group(1) if found else None


def github_compare(base: str, head: str) -> dict:
    """GitHub's comparison of base...head (status, ahead_by, behind_by), or a PinError in one line when it cannot be had."""
    url = f"https://api.github.com/repos/{pinlib.UPSTREAM_REPO}/compare/{base}...{head}?per_page=1"
    try:
        out = subprocess.run(["curl", "-sSfL", "-H", "Accept: application/vnd.github+json", *pinlib._auth_args(), url], check=True, capture_output=True, text=True, timeout=120).stdout
        data = json.loads(out)
        return {"status": data["status"], "ahead_by": data.get("ahead_by"), "behind_by": data.get("behind_by")}
    except (OSError, subprocess.SubprocessError, ValueError, KeyError) as error:
        reason = (getattr(error, "stderr", "") or str(error)).strip().splitlines()[-1:] or ["no answer"]
        raise PinError(f"cannot tell whether Pi {head[:8]} contains the carried commit {base[:9]} (CARRIED_ORACLE_REF): GitHub's compare failed ({reason[0]}); rerun with the network up, or set GH_PUBLIC_TOKEN when rate limited") from error


def decide_carried_oracle(target: Pin, root: Path = ROOT, compare: Callable[[str, str], dict] = github_compare) -> dict | None:
    ref = carried_oracle_ref(root)
    if ref is None:
        return None
    answer = compare(ref, target.commit)
    contained = answer["status"] in ("ahead", "identical")
    return {"commit": ref, "state": "carried" if contained else "pending", "evidence": f"GitHub compare {ref[:9]}...{target.commit[:9]}: {answer['status']}, ahead_by {answer.get('ahead_by')}, behind_by {answer.get('behind_by')}"}


def edit(root: Path, rel: str, transform: Callable[[str], str]) -> None:
    path = root / rel
    text = path.read_text(encoding="utf-8")
    new = transform(text)
    if new == text:
        raise PinError(f"{rel}: nothing to retire (the carried-oracle text this tool deletes is not there)")
    path.write_text(new, encoding="utf-8")


def drop(pattern: str, flags: int = re.M | re.S) -> Callable[[str], str]:
    return lambda text: re.sub(pattern, "", text, count=1, flags=flags)


def retire_carried_oracle(root: Path = ROOT) -> list[str]:
    """Delete everything that exists only because the port carries Pi work ahead of the pin; return the paths changed."""
    ref = carried_oracle_ref(root)
    if ref is None:
        return []
    edit(root, "Makefile", drop(r"^# Oracle tests of Pi work the Go port carries ahead of the committed pin.*?^\.PHONY: test-carried-oracles carried-oracles-run\n\n?"))
    edit(root, ".github/workflows/ci.yml", drop(r"^      # test-carried-oracles:.*?make test-carried-oracles\n"))
    changed = ["Makefile", ".github/workflows/ci.yml"]
    interop = root / "durable" / "interop"
    (interop / "carried_oracle_test.go").unlink()
    changed.append("durable/interop/carried_oracle_test.go")
    for path in sorted(interop.glob("*_test.go")):
        text = path.read_text(encoding="utf-8")
        if "\tskipWithoutSuspendableWork(t)\n" in text:
            path.write_text(text.replace("\tskipWithoutSuspendableWork(t)\n", ""), encoding="utf-8")
            changed.append(str(path.relative_to(root)))
    harness = "durable/harness/suspendable_oracle_test.go"

    def harness_edit(text: str) -> str:
        text = re.sub(r"// suspendableOraclePackages is .*?\n}\n", (
            "// suspendableOraclePackages is the packages directory of the pinned Pi, the oracle of the tests of suspendable work (signals, tool parks,\n"
            "// hook tasks, the generation at task version 3).\n"
            "func suspendableOraclePackages(t testing.TB) string {\n\tt.Helper()\n"
            "\treturn filepath.Join(testenv.ModuleRoot(t), \".upstream\", \"current\", \"packages\")\n}\n"), text, count=1, flags=re.S)
        return text.replace('\t"os"\n', "") if "os." not in text else text
    edit(root, harness, harness_edit)
    changed.append(harness)

    def decisions_edit(text: str) -> str:
        lines = [line for line in text.split("\n") if '"rationale":"Carried ahead of the pin:' not in line]
        out = "\n".join(lines)
        out = re.sub(r",\n\}", "\n}", out)
        json.loads(out)
        return out
    edit(root, "test/parity/interface-closure/autobind/reviewed-decisions.json", decisions_edit)
    changed.append("test/parity/interface-closure/autobind/reviewed-decisions.json")
    for frozen in sorted((root / "automation" / "pin" / "frozen").glob(f"{ref}.*")):
        frozen.unlink()
        changed.append(str(frozen.relative_to(root)))
    return changed


# -- the retirement -------------------------------------------------------------------------------------------------

def retire(target: Pin, root: Path = ROOT, *, modules: Path | None = None, compare: Callable[[str, str], dict] = github_compare) -> dict:
    """Decide every carried item against `target`, stop on a conflict before anything is edited, then delete the carried ones.
    The target's oracle (built without backports: the committed pin is another one) is the evidence for the backports."""
    if target == pinlib.committed():
        raise PinError(f"Pi {target.version} is already the committed pin: its oracle carries the backports, so they cannot be classified against it")
    ids = oracle.backport_ids((root / "automation/gen/pi-post-release-backports.mjs").read_text(encoding="utf-8"))
    backports = classify_backports(modules or oracle.ensure(target), lambda commit: compare(commit, target.commit)) if ids else {}
    conflicts = {ident: d for ident, d in backports.items() if d["state"] == "conflict"}
    if conflicts:
        lines = [f"backport {ident} neither applies to nor is carried by Pi {target.version}: {d['detail']}" for ident, d in conflicts.items()]
        raise PinError("\n  ".join(lines) + "\n  update or drop the entry in automation/gen/pi-post-release-backports.mjs, then rerun `make pin-bump`")
    carried_oracle = decide_carried_oracle(target, root, compare)
    carried = [ident for ident, d in backports.items() if d["state"] == "carried"]
    changed: list[str] = []
    if carried:
        script = root / "automation/gen/pi-post-release-backports.mjs"
        script.write_text(retire_backport_blocks(script.read_text(encoding="utf-8"), carried), encoding="utf-8")
        changed.append("automation/gen/pi-post-release-backports.mjs")
    if carried_oracle and carried_oracle["state"] == "carried":
        changed += retire_carried_oracle(root)
    return {"backports": backports, "carried_oracle": carried_oracle, "changed": changed}


def summary(decisions: dict) -> list[str]:
    lines = []
    for ident, d in decisions["backports"].items():
        lines.append(f"backport {ident}: {d['state']} [{d['evidence']}] ({', '.join(d['files'])})" + (" -> entry deleted" if d["state"] == "carried" else " -> kept, must still apply"))
    carried = decisions["carried_oracle"]
    if carried:
        lines.append(f"carried oracle {carried['commit'][:9]}: {carried['state']} ({carried['evidence']})" + (" -> make target, CI step, skip helpers, decisions, frozen files deleted" if carried["state"] == "carried" else " -> kept"))
    return lines or ["nothing is carried ahead of the pin"]
