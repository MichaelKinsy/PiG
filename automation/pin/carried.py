# SPDX-License-Identifier: MIT
"""Work the Go port carries ahead of the pin, retired by the bump that reaches it.

Two kinds are carried, and `make pin-bump` decides each one against the target Pi before it moves the pin (`retire`):

* post-release backports: the entries of automation/gen/pi-post-release-backports.mjs, one block per Pi commit. Each entry is classified
  against the target's own installed package (the oracle built without backports): `carried` (the target already holds the change: the
  block is deleted), `pending` (the target is the build the entry was written for: it stays and must still apply), or `conflict` (neither:
  the bump stops naming the entry, the file and the first differing line).
* the carried oracle: tests of a Pi commit newer than the pin (Makefile CARRIED_ORACLE_REF) that run against that commit's build. When
  GitHub's compare says the target contains the commit, its make targets, CI step, guard, skips, reviewed decisions and frozen files are
  deleted (retire_carried_oracle lists what goes and what stays); otherwise it stays.

Every decision, with its evidence, is returned for the bump's state file and printed in its summary.

Between bumps the sync ledgers of the followed Pi commits must account for the oracle (`oracle_ledger_problems`): a ported change of a file
the oracle runs has a block, a changed manifest or lock says whether its dependency is carried, or the row says why not.
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


# -- the sync ledgers' account of the oracle ----------------------------------------------------------------------------

LEDGERS = "test/parity/upstream-sync"
ORACLE_SOURCES = {"agent": "pi-agent-core", "ai": "pi-ai", "coding-agent": "pi-coding-agent", "mcp": "pi-mcp", "tui": "pi-tui"}  # the source directory of each package a block can patch
ORACLE_LOCKS = ("package-lock.json", "packages/coding-agent/install-lock/package-lock.json")
ORACLE_MANIFEST = re.compile(r"packages/[a-z-]+/package\.json")


def ledger_block_key(path: str) -> str | None:
    """The backport key of the installed file a path of the Pi repository becomes (packages/ai/src/api/x.ts is pi-ai/api/x.js; the coding
    agent's docs and schemas keep their path), or None for a path no block can carry (tests, the packages the script does not patch)."""
    found = re.fullmatch(r"packages/([a-z-]+)/(.+)", path)
    if not found or found[1] not in ORACLE_SOURCES:
        return None
    package, rest = ORACLE_SOURCES[found[1]], found[2]
    if found[1] == "coding-agent" and rest.split("/")[0] in ("docs", "schemas"):
        return f"{package}/{rest}"
    if not rest.startswith("src/") or rest.endswith((".d.ts", ".test.ts")):
        return None
    return f"{package}/{rest[4:-3]}.js" if rest.endswith(".ts") else f"{package}/{rest[4:]}"


def followed_ledgers(pin: Pin, root: Path = ROOT) -> list[Path]:
    """The sync ledgers of the Pi commits the port follows past the pin: v<pin version>+<commit>.toml."""
    return sorted((root / LEDGERS).glob(f"v{pin.version}+*.toml"))


def oracle_ledger_problems(pin: Pin, root: Path = ROOT) -> list[str]:
    """The rows of the followed commits' ledgers that change what the parity oracle installs or runs and do not account for it.

    The port follows Pi commits past the pin, and the oracle answers as the pin unless the backports script carries the commit, so a
    ported change the oracle lacks is a parity red that is the pin's age, found only by a full parity pass. Each such row must say what
    the oracle does:
    * a `ported` row of a file a block can patch (the compiled output of pi-agent-core, pi-ai, pi-coding-agent, pi-mcp and pi-tui, the coding agent's docs and
      schemas) has a block for that file in the script;
    * a row of a package manifest or a lock (any disposition: `designed-out` there is about PiG's product, which installs no npm
      dependency of Pi) carries `oracle = "carried: <backport id> ..."`, naming a commit the script has an entry for;
    * or the row carries `oracle = "not carried: <reason>"`.
    A ledger of a pin the script carries nothing for is read the same way: its rows then all say `not carried` or have nothing to carry."""
    import tomllib

    script = (root / "automation/gen/pi-post-release-backports.mjs").read_text(encoding="utf-8")
    ids = oracle.backport_ids(script)
    keys = set(re.findall(r'^(?:patch|packageFile|mirrorSource)\("\w+", "([^"]+)"', script, re.M))
    problems = []
    for ledger in followed_ledgers(pin, root):
        for row in tomllib.loads(ledger.read_text(encoding="utf-8")).get("files", []):
            path, said = row.get("path", ""), row.get("oracle")
            where = f"{LEDGERS}/{ledger.name}: {path}"
            if said is not None:
                names = re.match(r"carried: (\w+)\b", said)
                if names and names[1] not in ids:
                    problems.append(f"{where}: oracle = {said!r} names the entry {names[1]}, which automation/gen/pi-post-release-backports.mjs does not carry")
                elif not names and not re.match(r"not carried: \S", said):
                    problems.append(f"{where}: oracle = {said!r} is neither 'carried: <backport id>' nor 'not carried: <reason>'")
                continue
            key = ledger_block_key(path)
            if ORACLE_MANIFEST.fullmatch(path) or path in ORACLE_LOCKS:
                problems.append(f'{where}: a followed Pi commit changes a package manifest or a lock, and the row does not say what the oracle installs: add oracle = "carried: <backport id>" (a dependency entry of automation/gen/pi-post-release-backports.mjs) or oracle = "not carried: <reason>"')
            elif key is not None and row.get("disposition") == "ported" and key not in keys:
                problems.append(f'{where}: ported, and the oracle\'s {key} has no backport block, so the parity oracle still answers as the pin: add the block, or oracle = "not carried: <reason>" to the row')
    return problems


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


# The names that exist only while an oracle is carried. A retirement leaves none of them in a file it writes.
RETIRED_NAMES = ("CARRIED_ORACLE", "CARRIED_INTEROP", "test-carried-oracles", "carried_guard", "skipWithoutSuspendableWork", "suspendableOraclePackages", "namespaceOracleSkip")
PIN_DOCUMENT = "docs/parity/upstream-pin.md"
PIORACLE_IMPORT = '"github.com/MichaelKinsy/PiG/internal/pioracle"'


def retired_names_in(rel: str, text: str) -> list[str]:
    """The retired names `text` holds. The pin document keeps them in one paragraph, Carried work, which says what a bump retires."""
    if rel == PIN_DOCUMENT:
        text = "\n".join(line for line in text.split("\n") if not line.startswith("**Carried work.**"))
    return [name for name in RETIRED_NAMES if name in text]


class Retirement:
    """The edits of one retirement, decided on the whole tree before any file is written: a piece that is not where this tool
    expects it stops the retirement naming the file and the piece, and the tree is left as it was."""

    def __init__(self, root: Path):
        self.root = root
        self.texts: dict[str, str] = {}
        self.deleted: list[str] = []

    def read(self, rel: str, piece: str) -> str:
        if rel in self.texts:
            return self.texts[rel]
        path = self.root / rel
        if rel in self.deleted or not path.is_file():
            raise PinError(f"{rel}: nothing to retire ({piece} is not there)")
        return path.read_text(encoding="utf-8")

    def edit(self, rel: str, piece: str, transform: Callable[[str], str]) -> None:
        text = self.read(rel, piece)
        new = transform(text)
        if new == text:
            raise PinError(f"{rel}: nothing to retire ({piece} is not there)")
        self.texts[rel] = new

    def delete(self, rel: str, piece: str) -> None:
        if rel in self.deleted or not (self.root / rel).is_file():
            raise PinError(f"{rel}: nothing to retire ({piece} is not there)")
        self.texts.pop(rel, None)
        self.deleted.append(rel)

    def tests_with(self, directory: str, needle: str, piece: str) -> list[str]:
        """The test files of `directory` that still hold `needle`; none at all means the piece is gone."""
        found = [rel for rel in sorted(str(path.relative_to(self.root)) for path in (self.root / directory).glob("*_test.go")) if rel not in self.deleted and needle in self.read(rel, piece)]
        if not found:
            raise PinError(f"{directory}: nothing to retire ({piece} is not there)")
        return found

    def apply(self) -> list[str]:
        for rel, text in self.texts.items():
            left = retired_names_in(rel, text)
            if left:
                raise PinError(f"{rel}: the retirement would leave {', '.join(left)} behind: the carried-oracle text there is not the text this tool deletes")
        for rel, text in self.texts.items():
            (self.root / rel).write_text(text, encoding="utf-8")
        for rel in self.deleted:
            (self.root / rel).unlink()
        return [*self.texts, *self.deleted]


def drop(pattern: str, flags: int = re.M | re.S) -> Callable[[str], str]:
    return lambda text: re.sub(pattern, "", text, count=1, flags=flags)


def swap(old: str, new: str) -> Callable[[str], str]:
    return lambda text: text.replace(old, new)


def drop_unused_imports(text: str, imports: dict[str, str]) -> str:
    """Remove the import lines of `imports` ({import path: package name}) that the Go code of `text` no longer uses."""
    code = "\n".join(line for line in text.split("\n") if not line.lstrip().startswith("//"))
    for path, name in imports.items():
        if not re.search(rf"\b{re.escape(name)}\.", code):
            text = text.replace(f'\t"{path}"\n', "", 1)
    return text


def retire_carried_oracle(root: Path = ROOT, *, keep_frozen: bool = False) -> list[str]:
    """Delete everything that exists only because the port carries Pi work ahead of the pin; return the paths changed.

    What goes: the Makefile block (CARRIED_ORACLE_REF, the test lists, `test-carried-oracles` and `test-carried-oracles-guard`), the
    CI step that runs the guard, the guard and its tests, the three skips of the oracle tests (durable/interop, durable/harness,
    durable/codemode: the pin's Pi now has the work, so they run in every test run), the reviewed decisions carried ahead of the pin,
    the documentation of the carried run, and the commit's frozen files unless the target is that commit (`keep_frozen`: they are the
    new pin's then). What stays is what a run at another Pi commit needs whether or not one is carried: `pin.py override-tree`
    (vendor.override_tree) and internal/pioracle (Packages, Project, OverrideTree, SkipAtPin); their comments stop naming the target.
    Nothing is written unless every piece is found; a tree without CARRIED_ORACLE_REF carries nothing and is left alone."""
    ref = carried_oracle_ref(root)
    if ref is None:
        return []
    plan = Retirement(root)
    plan.edit("Makefile", "the block of CARRIED_ORACLE_REF, test-carried-oracles and test-carried-oracles-guard",
              drop(r"^# Oracle tests of Pi work the Go port carries ahead of the committed pin.*?^\.PHONY: test-carried-oracles test-carried-oracles-guard\n\n?"))
    plan.edit(".github/workflows/ci.yml", "the step that runs make test-carried-oracles-guard", drop(r"^      # test-carried-oracles:.*?make test-carried-oracles-guard\n"))
    for rel in ("automation/pin/carried_guard.py", "automation/pin/test_carried_guard.py"):
        plan.delete(rel, "the guard of the carried run")

    skip = "the skip skipWithoutSuspendableWork"
    plan.delete("durable/interop/carried_oracle_test.go", skip)
    for rel in plan.tests_with("durable/interop", "\tskipWithoutSuspendableWork(t)\n", skip):
        plan.edit(rel, skip, swap("\tskipWithoutSuspendableWork(t)\n", ""))

    skip = "the skip of suspendableOraclePackages"
    plan.delete("durable/harness/suspendable_oracle_test.go", skip)
    for rel in plan.tests_with("durable/harness", "suspendableOraclePackages(t)", skip):
        if PIORACLE_IMPORT not in plan.read(rel, skip):
            raise PinError(f"{rel}: cannot retire {skip}: the file does not import internal/pioracle, whose Packages replaces it")
        plan.edit(rel, skip, swap("suspendableOraclePackages(t)", "pioracle.Packages(t)"))

    skip = "the skip namespaceOracleSkip"
    describe = "durable/codemode/describe_pi_oracle_test.go"
    plan.edit(describe, skip, drop(r"^// namespaceOracleSkip skips .*?^}\n\n"))
    for rel in plan.tests_with("durable/codemode", "\tnamespaceOracleSkip(t)\n", skip):
        plan.edit(rel, skip, swap("\tnamespaceOracleSkip(t)\n", ""))
    plan.texts[describe] = drop_unused_imports(plan.texts[describe], {"os": "os", "path/filepath": "filepath"})

    def decisions_edit(text: str) -> str:
        lines = [line for line in text.split("\n") if '"rationale":"Carried ahead of the pin:' not in line]
        out = "\n".join(lines)
        out = re.sub(r",\n\}", "\n}", out)
        json.loads(out)
        return out
    plan.edit("test/parity/interface-closure/autobind/reviewed/reviewed-decisions.json", "a reviewed decision whose rationale starts 'Carried ahead of the pin:'", decisions_edit)

    # The tooling of a run at another Pi commit stays; its comments and the documentation stop naming the retired target.
    named = "the mention of make test-carried-oracles"
    plan.edit("automation/pin/vendor.py", named, swap(" for a run that compares with it (make test-carried-oracles).\n", " for a run that compares with it.\n"))
    plan.edit("internal/pioracle/source.go", named, swap(" make test-carried-oracles sets it for the\n", " A run at another Pi commit sets it for the\n"))
    plan.edit("durable/interop/interop_test.go", named, swap(" (pioracle.OverrideTreeEnv,\n// `make test-carried-oracles`), a ", " (pioracle.OverrideTreeEnv),\n// a "))
    plan.edit("test/parity/interface-closure/autobind/mirror_guard_test.go", named, swap(": make test-carried-oracles left\n", ": a run at another Pi once left\n"))
    plan.edit(PIN_DOCUMENT, "the paragraph 'The carried run at this pin'", drop(r"^- The carried run at this pin: .*?\n"))

    if not keep_frozen:
        frozen = sorted(str(path.relative_to(root)) for path in (root / "automation" / "pin" / "frozen").glob(f"{ref}.*"))
        if not frozen:
            raise PinError(f"automation/pin/frozen: nothing to retire (the frozen files of the carried commit {ref} are not there)")
        for rel in frozen:
            plan.delete(rel, "a frozen file of the carried commit")
    return plan.apply()


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
    script = root / "automation/gen/pi-post-release-backports.mjs"
    text = retire_backport_blocks(script.read_text(encoding="utf-8"), carried)
    if carried_oracle and carried_oracle["state"] == "carried":
        # The oracle first: it writes nothing unless every piece is there, so a retirement that stops leaves the script as it was too.
        changed += retire_carried_oracle(root, keep_frozen=target.commit == carried_oracle["commit"])
    if carried:
        script.write_text(text, encoding="utf-8")
        changed.insert(0, "automation/gen/pi-post-release-backports.mjs")
    return {"backports": backports, "carried_oracle": carried_oracle, "changed": changed}


def summary(decisions: dict) -> list[str]:
    lines = []
    for ident, d in decisions["backports"].items():
        lines.append(f"backport {ident}: {d['state']} [{d['evidence']}] ({', '.join(d['files'])})" + (" -> entry deleted" if d["state"] == "carried" else " -> kept, must still apply"))
    carried = decisions["carried_oracle"]
    if carried:
        lines.append(f"carried oracle {carried['commit'][:9]}: {carried['state']} ({carried['evidence']})" + (" -> make targets, CI step, guard, skips, decisions, frozen files deleted" if carried["state"] == "carried" else " -> kept"))
    elif lines:
        lines.append("carried oracle: none (the Makefile names no CARRIED_ORACLE_REF) -> nothing to retire")
    return lines or ["nothing is carried ahead of the pin"]
