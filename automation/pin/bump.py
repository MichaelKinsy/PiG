#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Move the pin to a Pi tag or commit and regenerate everything that derives from it, in one resumable run.

    bump.py <ref>          (make pin-bump PI_UPSTREAM_REF=<ref> [PIN_FROM=<earlier ref>])

The run stops only on content work (ledger rows to close) or on a failure that names itself. Every step is recorded in
tmp/pin-bump/<commit[:8]>.json with its wall time and exit code; a rerun after a failure resumes at the step that failed, a rerun of a
finished bump is a no-op, and the earlier pin (the ledgers are carried from it) comes from that record, not from the pin file, which has
moved: `PIN_FROM` names it only when there is no record and the pin file already moved without one.

Steps: retire (what the port carried ahead of the pin and the target now holds: automation/pin/carried.py; decided against the target
before the pin moves), move (.upstream/pin and every file derived from it), mirror, inventories, ledgers, one step per vendor
target (the vendored Pi packages, the sdk-ts declarations, the runtime, chord copies, model catalogs, intl tables), observation (the observation oracles, under the oracle Node the tool
fetches itself; the Pi test sources; the alt-screen fixture), sdk-vectors (the vectors the SDK helper ports are tested against, run on the oracle: each carries the pin it was generated at, and the SDK surface page refuses stale ones), gaps (`pin.py gaps`: bind the ledgers twice, then check), generated (the
extension SDK surface page, the outgoing pin's inventories leave the index, `make generate`).
"""
from __future__ import annotations

import datetime as dt
import json
import os
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402
from pinlib import ROOT, Pin, PinError  # noqa: E402

STATE_DIR = "tmp/pin-bump"
PUBLIC_MAIN = "refs/remotes/origin/main"

VENDOR_TARGETS = ["vendor-pi-packs", "vendor-sdk-ts-declarations", "vendor-sdk-ts-pin-declarations", "vendor-pi-dist", "vendor-chord-plugins-node", "vendor-chord-surfaces-client", "model-catalogs", "intl-tables"]


def run_command(command: list[str], env: dict[str, str], cwd: Path) -> int:
    return subprocess.run(command, cwd=cwd, env=env, stdin=subprocess.DEVNULL).returncode


def git_text(root: Path, *args: str) -> str | None:
    done = subprocess.run(["git", *args], cwd=root, capture_output=True, text=True, check=False)
    return done.stdout if done.returncode == 0 else None


def head_pin(root: Path) -> Pin | None:
    """The pin file as HEAD holds it: the earlier pin while the bump is uncommitted."""
    text = git_text(root, "show", "HEAD:.upstream/pin")
    if text is None:
        return None
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "pin"
        path.write_text(text, encoding="utf-8")
        return pinlib.read_pin_file(path)


def obsolete_snapshots(root: Path, new: Pin) -> list[str]:
    """Tracked interface snapshots of a pin that is neither the new pin nor the reviewed baseline (coding.UpstreamReviewedVersion)."""
    reviewed = re.search(r'^const UpstreamReviewedVersion = "([^"]+)"', (root / "coding/upstream.go").read_text(encoding="utf-8"), re.M)
    if reviewed is None:
        raise PinError("coding/upstream.go: missing UpstreamReviewedVersion")
    suffixes = tuple(f"-v{version}.json" for version in {new.version, reviewed.group(1)})
    paths = (git_text(root, "ls-files", "test/parity/interfaces") or "").splitlines()
    return [path for path in paths if re.search(r"-v\d.*\.json$", path) and not path.endswith(suffixes)]


HYDRATE_HOSTS = "models.dev, openrouter.ai, ai-gateway.vercel.sh"


def frozen_provider_data(root: Path, target: Pin) -> str | None:
    """The recorded digest of the frozen provider data of a commit pin (automation/pin/frozen/<commit>.provider-data.tgz.sha256), None when there is none."""
    path = root / "automation/pin/frozen" / f"{target.commit}.provider-data.tgz.sha256"
    return path.read_text(encoding="utf-8").strip() if path.exists() else None


class Bump:
    ORDER = ["retire", "move", "mirror", "inventories", "ledgers", *VENDOR_TARGETS, "observation", "sdk-vectors", "gaps", "generated"]

    def __init__(self, root: Path, target: Pin, earlier: Pin | None, *, runner: Callable = run_command, retire_fn: Callable | None = None,
                 move_fn: Callable | None = None, summarize: Callable | None = None, env: dict[str, str] | None = None, out=sys.stdout):
        self.root, self.target, self.earlier, self.runner = root, target, earlier, runner
        self.retire_fn, self.move_fn, self.summarize = retire_fn, move_fn, summarize
        self.env = {k: v for k, v in (env if env is not None else os.environ).items() if k != pinlib.OVERRIDE_ENV}
        self.out = out
        self.state_file = root / STATE_DIR / f"{target.commit[:8]}.json"
        self.state: dict = {"to": target.fields(), "from": earlier.fields() if earlier else None, "steps": [], "decisions": None}

    # -- state -----------------------------------------------------------------------------------------------------------------
    def load(self) -> None:
        if self.state_file.exists():
            self.state = json.loads(self.state_file.read_text(encoding="utf-8"))

    def save(self) -> None:
        self.state_file.parent.mkdir(parents=True, exist_ok=True)
        self.state_file.write_text(json.dumps(self.state, indent=2) + "\n", encoding="utf-8")

    def passed(self, name: str) -> bool:
        return any(s["name"] == name and s["rc"] == 0 for s in self.state["steps"])

    def say(self, text: str) -> None:
        print(f"pin-bump: {text}", file=self.out, flush=True)

    # -- preflight -------------------------------------------------------------------------------------------------------------
    def preflight(self) -> None:
        """Fail in a second, not an hour in: `make generate` (known-gaps) anchors the committed test baseline on public main."""
        ref = self.env.get("PUBLIC_MAIN_REF") or PUBLIC_MAIN
        if self.runner is run_command and git_text(self.root, "rev-parse", "--verify", "--quiet", ref) is None:
            raise PinError(f"this clone has no {ref}, which known-gaps-drift needs: fetch public main into it, or set PUBLIC_MAIN_REF=<remote-tracking ref of public main> and rerun `make pin-bump`")

    # -- steps -----------------------------------------------------------------------------------------------------------------
    def make(self, *targets: str, **variables: str) -> list[tuple[list[str], dict]]:
        return [(["make", "--no-print-directory", target, *(f"{k}={v}" for k, v in variables.items())], {}) for target in targets]

    def commands(self, name: str) -> list[tuple[list[str], dict]]:
        pin = ["python3", "automation/pin/pin.py"]
        table = {
            "mirror": self.make("upstream-mirror"),
            "inventories": self.make("interface-proposals", "test-inventory-generate"),
            "ledgers": self.make("pin-ledgers", PIN_FROM=(self.state["from"] or {}).get("ref", "")),
            **{target: self.make(target) for target in VENDOR_TARGETS},
            "observation": [(["./automation/gen/regenerate-observation-oracles.sh"], {}), (["./automation/gen/refresh-pi-test-sources.sh"], {}), *self.make("regenerate-alt-screen-oracle")],
            "sdk-vectors": [(["node", "automation/gen/generate-sdk-vectors.mjs"], {})],
            "gaps": [([*pin, "gaps", "--updates", "2"], {})],
        }
        return table[name]

    def step_retire(self) -> int:
        decisions = self.retire_fn(self.target)
        self.state["decisions"] = decisions
        return 0

    def step_move(self) -> int:
        return self.move_fn(self.target)

    def step_generated(self) -> int:
        for command in ([["go", "run", "./test/parity/cmd/sdksurface"]]):
            rc = self.runner(command, self.env, self.root)
            if rc:
                return rc
        outgoing = obsolete_snapshots(self.root, self.target)
        if outgoing:
            self.say(f"{len(outgoing)} interface snapshot(s) of the outgoing pin leave the index (they stay on disk, ignored): " + ", ".join(Path(p).name for p in outgoing))
            rc = self.runner(["git", "rm", "-q", "--cached", "--", *outgoing], self.env, self.root)
            if rc:
                return rc
        return self.runner(["make", "--no-print-directory", "generate"], self.env, self.root)

    def run_step(self, name: str) -> int:
        if name == "retire":
            return self.step_retire()
        if name == "move":
            return self.step_move()
        if name == "generated":
            return self.step_generated()
        for command, extra in self.commands(name):
            self.say("+ " + " ".join(command))
            rc = self.runner(command, {**self.env, **extra}, self.root)
            if rc:
                return rc
        return 0

    def changed_files(self) -> int | None:
        text = git_text(self.root, "status", "--porcelain")
        return None if text is None else len(text.splitlines())

    def run(self) -> int:
        self.load()
        if self.state["from"] is None:
            raise PinError("no earlier pin is recorded for this bump")
        done = all(self.passed(name) for name in self.ORDER)
        if done:
            self.say(f"Pi {self.target.version} is already bumped (every step passed; record {self.state_file.relative_to(self.root)}). Delete the record to run it again.")
            return 0
        self.preflight()
        digest = frozen_provider_data(self.root, self.target)
        if self.state.get("provider_data") is None:
            if digest is not None:
                self.state["provider_data"] = {"source": "frozen", "digest": digest}
            elif self.target.kind == "commit":
                self.state["provider_data"] = {"source": "hydrated", "digest": None}
        provider = self.state.get("provider_data")
        if provider and provider["source"] == "frozen":
            self.say(f"provider data: the frozen snapshot of {self.target.short} (digest {provider['digest']}, automation/pin/frozen)")
        elif provider:
            self.say(f"provider data: none is frozen for {self.target.short}, so the FIRST oracle build hydrates it from live catalog services ({HYDRATE_HOSTS}) and freezes the result in automation/pin/frozen: that step needs that network and its output depends on the day it runs; commit the frozen files with the pin")
        self.say(f"Pi {self.state['from']['version']} -> {self.target.version} ({self.target.kind} {self.target.ref}); record {self.state_file.relative_to(self.root)}")
        for name in self.ORDER:
            if self.passed(name):
                self.say(f"{name}: passed earlier, skipped")
                continue
            started = dt.datetime.now(dt.timezone.utc)
            clock = time.monotonic()
            try:
                rc = self.run_step(name)
            except PinError as error:
                print(f"pin-bump: {name}: {error}", file=sys.stderr)
                rc = 1
            entry = {"name": name, "rc": rc, "start": started.isoformat(), "seconds": round(time.monotonic() - clock, 1), "files": self.changed_files()}
            self.state["steps"] = [s for s in self.state["steps"] if s["name"] != name] + [entry]
            self.save()
            if rc:
                self.say(f"{name} failed (rc {rc}); fix it and rerun the same command: it resumes at {name}")
                return rc
        self.summary()
        return 0

    def summary(self) -> None:
        provider = self.state.get("provider_data")
        if provider and provider["source"] == "hydrated":
            self.state["provider_data"]["digest"] = frozen_provider_data(self.root, self.target)
            self.save()
        self.say("done. Steps:")
        for s in self.state["steps"]:
            self.say(f"  {s['name']:<12} {s['seconds']:>8.1f} s  rc {s['rc']}  files changed {s['files']}")
        if provider:
            self.say(f"provider data: {provider['source']}, digest {provider['digest']}" + (" (hydrated from live catalog services on this run and frozen: commit automation/pin/frozen/" + self.target.commit + ".provider-data.tgz*)" if provider["source"] == "hydrated" else ""))
        if self.state["decisions"] and self.summarize:
            self.say("carried work decided against the target (review before committing):")
            for line in self.summarize(self.state["decisions"]):
                self.say("  " + line)


def earlier_pin(root: Path, target: Pin, explicit: str | None) -> Pin | None:
    """The pin this bump leaves: the record of an earlier run of it, else the explicit PIN_FROM, else the committed pin when it is not the target,
    else the pin HEAD holds (the bump is uncommitted and the pin file has moved); None when there is none."""
    record = root / STATE_DIR / f"{target.commit[:8]}.json"
    if record.exists():
        return None  # Bump.load takes `from` from the record
    if explicit:
        return pinlib.resolve(explicit, None)
    committed = pinlib.committed()
    if committed != target:
        return committed
    head = head_pin(root)
    return head if head is not None and head != target else None


def main(argv: list[str] | None = None) -> int:
    args = sys.argv[1:] if argv is None else argv
    if len(args) != 1:
        print("usage: bump.py <tag|commit>   (make pin-bump PI_UPSTREAM_REF=<tag|commit>)", file=sys.stderr)
        return 2
    import carried  # noqa: E402
    import pin as pin_tool  # noqa: E402

    try:
        target = pinlib.resolve(args[0], pinlib.committed())
        earlier = earlier_pin(ROOT, target, os.environ.get("PIN_FROM") or None)
        record = ROOT / STATE_DIR / f"{target.commit[:8]}.json"
        if earlier is None and not record.exists():
            print(f"pin-bump: Pi {target.version} is already the pin, HEAD holds the same pin and no record of a bump to it exists: nothing to do. "
                  f"To carry the ledgers from an earlier pin anyway, run `make pin-bump PI_UPSTREAM_REF={args[0]} PIN_FROM=<earlier ref>`.")
            return 0

        def retire(pin: Pin) -> dict:
            decisions = carried.retire(pin)
            for line in carried.summary(decisions):
                print(f"pin-bump: retire: {line}", flush=True)
            return decisions

        def move(pin: Pin) -> int:
            if pinlib.committed() != pin:
                pin_tool.cmd_bump(type("Args", (), {"ref": pin.ref})())
            else:
                pin_tool.cmd_sync(type("Args", (), {"check": False})())
            return 0

        return Bump(ROOT, target, earlier, retire_fn=retire, move_fn=move, summarize=carried.summary).run()
    except PinError as error:
        print(f"pin-bump: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
