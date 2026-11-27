#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""The upstream Pi pin tool. See pinlib.py for the model.

  pin.py show                 the effective pin and whether PI_UPSTREAM_REF overrides it
  pin.py get <key>            one field: ref commit short version package_version kind tag
                              build_metadata mirror_dir override committed_version
  pin.py env                  shell exports for the effective pin (eval it); a run that
                              overrides the pin also exports a GOFLAGS overlay for pigversion.go
  pin.py make-vars            one line of key=value words for Makefile
  pin.py tarball              path of the effective pin's source tarball (downloaded if absent)
  pin.py sync [--check]       regenerate (or verify) every committed file derived from the committed pin
  pin.py bump <ref>           write .upstream/pin for a tag or commit, then sync
  pin.py install-oracle       install the Pi oracle for the effective pin into extensions/sdk-ts/node_modules
  pin.py freeze-closure       freeze the pin's dependency closure (release: --from-published-lock <lock>; commit: derived from its packs)
  pin.py vendor               pack the pinned build's durable closure into third_party/pi-packs and point its consumers at it
  pin.py check-vendored [--rebuild] [--installed CONSUMER]  fail when the vendored packs, a consumer or a consumer's install are not the pin's (automation/pin/vendor.py)
  pin.py override-tree        under PI_UPSTREAM_REF, build the tree the oracle tests of that Pi commit read and print its path; the repository is not written
  pin.py check-oracle         verify the installed oracle is the effective pin's Pi
  pin.py link-mirror          point the effective pin's mirror (.upstream/<mirror>/node_modules) at its source build's
                              dependencies, so oracle scripts that import the mirrored source resolve its imports
  pin.py mirror-data --commit C --target DIR  make DIR (a source mirror's packages/ai/src/providers/data) the provider data
                              frozen for Pi commit C, checked against its recorded digest; fails when none is frozen
  pin.py ledgers [--from REF] generate the effective pin's ledgers that one file holds (async-contracts, behavior-contracts,
                              known-gaps, under test/parity/pin-ledgers/) from the earlier pin REF (default: the committed pin),
                              plus the interface delta manifest and the custom-factory draft; skips what exists
  pin.py oracle-node          the bin directory of the oracle Node (ai/testdata/node-microtasks/oracle-node-version), fetched from nodejs.org
                              and verified against its SHASUMS256.txt when the cache has none (automation/pin/nodefetch.py)
  pin.py retire <ref>         delete the post-release backports and the carried oracle that Pi <ref> already holds (automation/pin/carried.py);
                              the first step of `make pin-bump`, decided against the target before the pin moves
  pin.py gaps [--updates N]   the effective pin's gap list: `make ledger-update` N times (default 1), then `make ledger-check` (the pin-follow gaps step)
"""
from __future__ import annotations

import argparse
import contextlib
import json
import os
import re
import shlex
import subprocess
import sys
import tarfile
import tomllib
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402
from pinlib import ROOT, Pin, PinError  # noqa: E402


def field(pin: Pin, key: str, override: bool, base: Pin) -> str:
    table = {
        "ref": pin.ref, "commit": pin.commit, "short": pin.short, "version": pin.version,
        "package_version": pin.package_version, "kind": pin.kind, "tag": pin.tag,
        "build_metadata": pin.build_metadata, "mirror_dir": pin.mirror_dir,
        "override": "true" if override else "false", "committed_version": base.version,
    }
    if key not in table:
        raise PinError(f"unknown field {key!r}; known: {', '.join(table)}")
    return table[key]


KEYS = ["ref", "commit", "short", "version", "package_version", "kind", "tag", "build_metadata", "mirror_dir"]


def cmd_show(_: argparse.Namespace) -> None:
    pin, override = pinlib.effective()
    print(f"pin: Pi {pin.version} ({pin.kind} {pin.ref})")
    print(f"  commit {pin.commit}\n  package version {pin.package_version}")
    print(f"  source: {'PI_UPSTREAM_REF override (committed pin is ' + pinlib.committed().version + ')' if override else '.upstream/pin'}")


def cmd_get(args: argparse.Namespace) -> None:
    pin, override = pinlib.effective()
    print(field(pin, args.key, override, pinlib.committed()))


def cmd_env(_: argparse.Namespace) -> None:
    pin, override = pinlib.effective()
    for key in KEYS:
        print(f"export PIG_UPSTREAM_{key.upper()}={shlex.quote(field(pin, key, override, pin))}")
    print(f"export PIG_UPSTREAM_OVERRIDE={'true' if override else 'false'}")
    if override:
        flag = pinlib.overlay(pin)
        print(f'export GOFLAGS="${{GOFLAGS:+$GOFLAGS }}{flag}"')
        print(f"export {pinlib.OVERRIDE_ENV}={shlex.quote(os.environ[pinlib.OVERRIDE_ENV])}")


def cmd_make_vars(_: argparse.Namespace) -> None:
    pin, override = pinlib.effective()
    words = [f"{key}={field(pin, key, override, pin)}" for key in KEYS]
    words.append(f"override={'true' if override else 'false'}")
    if override:
        words.append("goflags=" + pinlib.overlay(pin))
    print(" ".join(words))


def cmd_tarball(_: argparse.Namespace) -> None:
    pin, _override = pinlib.effective()
    print(pinlib.tarball(pin.tag, True) if pin.kind == "tag" else pinlib.tarball(pin.commit, False))


def cmd_mirror_spec(args: argparse.Namespace) -> None:
    """`<commit> <package version> <tarball>` of a commit build named by a ledger version (X.Y.Z+<commit prefix>), for the mirror of an earlier pin."""
    package_version, _, short = args.version.partition("+")
    if not short or not pinlib.HEX_RE.match(short):
        raise PinError(f"{args.version!r} is not a commit build version (X.Y.Z+<commit>)")
    commit = pinlib.full_commit(short)
    path = pinlib.tarball(commit, False)
    identity_commit, identity_version = pinlib.tarball_identity(path)
    if identity_commit != commit or identity_version != package_version:
        raise PinError(f"{args.version}: the tarball of {commit} is Pi {identity_version} at {identity_commit}")
    print(commit, package_version, path)


def cmd_sync_from(_: argparse.Namespace) -> None:
    """The version the pin's upstream-sync manifest audits against."""
    manifest = ROOT / "test/parity/upstream-sync" / f"v{pinlib.effective()[0].version}.toml"
    if not manifest.exists():
        raise PinError(f"{manifest.relative_to(ROOT)} is missing")
    print(tomllib.loads(manifest.read_text(encoding="utf-8"))["from"])


# Derived files ------------------------------------------------------------------

def sub(pattern: str, replacement: str, text: str, path: str, count: int = 0, minimum: int = 1) -> str:
    result, n = re.subn(pattern, lambda m: m.expand(replacement) if "\\" in replacement else replacement, text, flags=re.M)
    if n < minimum or (count and n != count):
        raise PinError(f"{path}: pattern {pattern!r} matched {n} times")
    return result


def readme(pin: Pin, text: str) -> str:
    if pin.kind == "tag":
        link = f"https://github.com/earendil-works/pi/releases/tag/{pin.tag}"
    else:
        link = f"https://github.com/earendil-works/pi/commit/{pin.commit}"
    badge = pin.version.replace("+", "%2B")
    text = sub(r"^\[!\[Pi pin [^\]]*\]\(https://img\.shields\.io/badge/Pi%20pin-[^)]*\)\]\([^)]*\)",
               f"[![Pi pin {pin.version}](https://img.shields.io/badge/Pi%20pin-{badge}-8A2BE2)]({link})", text, "README.md", 1)
    text = sub(r"(checked against Pi )\S+( with paired parity scenarios)", rf"\g<1>{pin.version}\g<2>", text, "README.md", 1)
    text = sub(r"(prints? PiG's version as `<PiG release>\+<Pi release>`, for example `\d+\.\d+\.\d+\+)[^`]+(`)", rf"\g<1>{pin.build_metadata}\g<2>", text, "README.md", 1)
    sentence = (f"The pinned Pi release is {pin.version} at commit `{pin.commit}`." if pin.kind == "tag"
                else f"The pinned Pi build is {pin.version}, commit `{pin.commit}` of the main branch.")
    return sub(r"The pinned Pi (?:release|build) is [^`]*at commit `[0-9a-f]{40}`\.|The pinned Pi build is [^`]*, commit `[0-9a-f]{40}` of the main branch\.", sentence, text, "README.md", 1)


def provenance(pin: Pin, text: str) -> str:
    """The upstream pin and commit lines of docs/project/PROVENANCE.md."""
    kind = f"release tag `{pin.tag}`" if pin.kind == "tag" else "a commit of Pi's main branch"
    lines = f"- Upstream pin: `{pin.version}` ({kind})\n- Upstream commit: `{pin.commit}`"
    return sub(r"^- Upstream (?:release|pin): `[^`]*`[^\n]*\n- Upstream commit: `[0-9a-f]{40}`$", lines, text, "docs/project/PROVENANCE.md", 1)


def package_deps(names: list[str]):
    def apply(pin: Pin, text: str, path: str = "") -> str:
        pattern = r'("(?:%s)"\s*:\s*")[^"]+(")' % "|".join(re.escape(n) for n in names)
        return sub(pattern, rf"\g<1>{pin.package_version}\g<2>", text, path)
    return apply


def regex_rule(pattern: str, template: str, field_name: str = "version", count: int = 1):
    def apply(pin: Pin, text: str, path: str = "") -> str:
        value = pin.version if field_name == "version" else pin.package_version
        return sub(pattern, template.replace("{v}", value), text, path, count)
    return apply


def ci_tag(pin: Pin, text: str) -> str:
    return sub(r"(-pi)[0-9A-Za-z.]+(-)", rf"\g<1>{pin.build_metadata}\g<2>", text, "metadata.env", 1)


def ci_commit(pin: Pin, text: str) -> str:
    return sub(r"^(ARG PI_COMMIT=).*$", rf"\g<1>{pin.commit if pin.kind == 'commit' else ''}", text, "Dockerfile", 1)


def interface_snapshot_ignore(pin: Pin, text: str) -> str:
    reviewed = re.search(r'^const UpstreamReviewedVersion = "([^"]+)"', (ROOT / "coding/upstream.go").read_text(encoding="utf-8"), re.M)
    if reviewed is None:
        raise PinError("coding/upstream.go: missing UpstreamReviewedVersion")
    lines = ["# BEGIN PINNED INTERFACE SNAPSHOTS", "/test/parity/interfaces/*-v*.json"]
    for version in dict.fromkeys((pin.version, reviewed.group(1))):
        lines.append(f"!/test/parity/interfaces/*-v{version}.json")
    lines.append("# END PINNED INTERFACE SNAPSHOTS")
    return sub(r"(?s)^# BEGIN PINNED INTERFACE SNAPSHOTS\n.*?^# END PINNED INTERFACE SNAPSHOTS$", "\n".join(lines), text, ".gitignore", 1)


# (path, transform(pin, text), command to refresh a lock when the file changed or None)
ORACLE_PACKAGES = ['@earendil-works/pi-agent-core', '@earendil-works/pi-ai', '@earendil-works/pi-coding-agent', '@earendil-works/pi-tui']
RULES: list[tuple[str, object, str | None]] = [
    (".gitignore", lambda p, t: interface_snapshot_ignore(p, t), None),
    ("README.md", lambda p, t: readme(p, t), None),
    ("docs/project/PROVENANCE.md", lambda p, t: provenance(p, t), None),
    ("docs/site/docs/index.md", regex_rule(r"^(\| Pinned Pi release \(behavior oracle\) \| Pi )\S+( \|)", r"\g<1>{v}\g<2>"), None),
    ("extensions/sdk-ts/package.json", package_deps(["@earendil-works/pi-coding-agent"]), "extensions/sdk-ts"),
    ("extensions/sdk-ts/README.md", lambda p, t: sub(r"(pi-coding-agent@)\S+", rf"\g<1>{p.package_version}", sub(r"(API from Pi )\S+?(?=\. It contains)", rf"\g<1>{p.package_version}", t, "README"), "README"), None),
    ("test/parity/known-gaps.toml", regex_rule(r'^(pi_version = ")[^"]+(")', r'\g<1>{v}\g<2>'), None),
    ("test/parity/ledgers/behavior-contracts.toml", regex_rule(r'^(upstream_version = ")[^"]+(")', r'\g<1>{v}\g<2>'), None),
    ("test/parity/ledgers/async-contracts.toml", regex_rule(r'\A(version = ")[^"]+(")', r'\g<1>{v}\g<2>'), None),
    ("automation/images/ci-parity/Dockerfile", lambda p, t: ci_commit(p, regex_rule(r"^(ARG PI_VERSION=)\S+", r"\g<1>{v}", "package_version")(p, t)), None),
    ("automation/images/ci-parity/metadata.env", lambda p, t: ci_tag(p, t), None),
    ("automation/images/ci-parity/package.json", package_deps(ORACLE_PACKAGES), "automation/images/ci-parity"),
    ("coding/extension/host/subprocess/runtime-node/harness/package.json", regex_rule(r'^(  "version": ")[^"]+(")', r'\g<1>{v}\g<2>', "package_version"), None),
]


def derived(pin: Pin) -> dict[str, str]:
    out = {"internal/coding/pigversion/pigversion.go": pinlib.render_pigversion(pin, pinlib.pig_version())}
    for rel, transform, _lock in RULES:
        path = ROOT / rel
        text = path.read_text(encoding="utf-8")
        out[rel] = transform(pin, text)
    return out


INSTALL_LOCK_DIR = "bench/k8s/image/pi-agent"


def install_lock(pin: Pin) -> dict[str, bytes]:
    """Pi's release-published install pair, as the pinned source tarball holds it (packages/coding-agent/install-lock).
    bench/k8s/image/pi-agent keeps it unmodified: npm would rewrite the lock, and k8sbench pins the published hashes. The
    mirror drops package lockfiles, so the tarball (cached, downloaded on a miss) is the source."""
    wanted = {f"packages/coding-agent/install-lock/{name}": name for name in ("package.json", "package-lock.json")}
    found: dict[str, bytes] = {}
    with tarfile.open(pinlib.tarball(pin.tag, True) if pin.kind == "tag" else pinlib.tarball(pin.commit, False)) as archive:
        for member in archive:
            name = wanted.get(member.name.split("/", 1)[-1])
            if name and member.isfile():
                found[name] = archive.extractfile(member).read()
                if len(found) == len(wanted):
                    break
    if len(found) != len(wanted):
        raise PinError(f"Pi {pin.version}'s tarball has no packages/coding-agent/install-lock pair")
    return found


def cmd_sync(args: argparse.Namespace) -> None:
    pin = pinlib.committed()
    wanted = derived(pin)
    stale = []
    changed_locks = set()
    for rel, text in wanted.items():
        path = ROOT / rel
        if path.read_text(encoding="utf-8") != text:
            stale.append(rel)
            if not args.check:
                path.write_text(text, encoding="utf-8")
                for r, _t, lock in RULES:
                    if r == rel and lock:
                        changed_locks.add(lock)
    lock_pair = install_lock(pin)
    stale_pair = [f"{INSTALL_LOCK_DIR}/{name}" for name, data in lock_pair.items() if (ROOT / INSTALL_LOCK_DIR / name).read_bytes() != data]
    if args.check:
        stale += stale_pair
        if stale:
            raise PinError("derived from .upstream/pin but stale (run `make pin-sync`): " + ", ".join(stale))
        print(f"pin-check: {len(wanted) + len(lock_pair)} derived files match Pi {pin.version}")
        return
    for name, data in lock_pair.items():
        (ROOT / INSTALL_LOCK_DIR / name).write_bytes(data)
    stale += stale_pair
    for directory in sorted(changed_locks):
        npm = "npm"
        print(f"+ npm install --package-lock-only in {directory}", file=sys.stderr, flush=True)
        subprocess.run([npm, "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=ROOT / directory, check=True, timeout=600)
    print(f"pin-sync: Pi {pin.version}: {len(stale)} file(s) updated" + (": " + ", ".join(stale) if stale else ""))


# Ledgers that one file holds for the committed pin only. A bump archives the outgoing pin's copy and restores
# the incoming pin's archived copy when it has one, so moving the pin back reproduces the earlier pin's ledgers.
PIN_LEDGERS = ["test/parity/known-gaps.toml", "test/parity/ledgers/behavior-contracts.toml", "test/parity/ledgers/async-contracts.toml"]
ARCHIVE = "test/parity/pin-ledgers"


def swap_ledgers(old: Pin, new: Pin) -> list[str]:
    if old.version == new.version:
        return []
    moved = []
    for rel in PIN_LEDGERS:
        current = ROOT / rel
        archived_out = ROOT / ARCHIVE / ("v" + old.version) / Path(rel).name
        archived_out.parent.mkdir(parents=True, exist_ok=True)
        archived_out.write_text(current.read_text(encoding="utf-8"), encoding="utf-8")
        archived_in = ROOT / ARCHIVE / ("v" + new.version) / Path(rel).name
        if archived_in.exists():
            current.write_text(archived_in.read_text(encoding="utf-8"), encoding="utf-8")
            moved.append(rel)
    return moved


def cmd_bump(args: argparse.Namespace) -> None:
    old = pinlib.committed()
    pin = pinlib.resolve(args.ref, None)
    restored = swap_ledgers(old, pin)
    pinlib.PIN_FILE.write_text(pinlib.render_pin_file(pin), encoding="utf-8")
    print(f"pin-bump: .upstream/pin = Pi {pin.version} ({pin.kind} {pin.ref}), was {old.version}")
    if restored:
        print("pin-bump: restored archived ledgers of this pin: " + ", ".join(restored))
    cmd_sync(argparse.Namespace(check=False))


VERSION_FIELDS = {
    "test/parity/known-gaps.toml": "pi_version",
    "test/parity/ledgers/behavior-contracts.toml": "upstream_version",
    "test/parity/ledgers/async-contracts.toml": "version",
}


def with_version(text: str, field_name: str, version: str) -> str:
    """The ledger text with its top-level version field set to version."""
    updated, count = re.subn(rf'(?m)^{field_name} = "[^"]*"', f'{field_name} = "{version}"', text, count=1)
    if count != 1:
        raise PinError(f"no top-level {field_name} in the ledger")
    return updated


def previous_copy(rel: str, previous: Pin) -> Path:
    archived = ROOT / ARCHIVE / ("v" + previous.version) / Path(rel).name
    return archived if archived.exists() else ROOT / rel


def go_env_script(command: str) -> list[str]:
    """Run a shell command under the effective pin's environment (the Go overlay included when the pin is overridden)."""
    return ["bash", "-c", f'set -euo pipefail; eval "$(python3 {shlex.quote(str(Path(__file__).resolve()))} env)"; {command}']


def cmd_ledgers(args: argparse.Namespace) -> None:
    pin, _override = pinlib.effective()
    previous = pinlib.resolve(args.previous, None) if args.previous else pinlib.committed()
    if previous.version == pin.version:
        print(f"pin-ledgers: Pi {pin.version} is the pin the committed ledgers are for; nothing to generate")
        return
    dest = ROOT / ARCHIVE / ("v" + pin.version)
    dest.mkdir(parents=True, exist_ok=True)
    written: list[str] = []
    mirror_before = ROOT / ".upstream" / previous.mirror_dir
    if not mirror_before.exists():
        raise PinError(f"the earlier pin's mirror {mirror_before} is missing; run the earlier pin's `make upstream-mirror` first")
    for rel, field_name in VERSION_FIELDS.items():
        out = dest / Path(rel).name
        if out.exists() and not args.force:
            print(f"pin-ledgers: {out.relative_to(ROOT)} exists, kept")
            continue
        source = previous_copy(rel, previous)
        if Path(rel).name == "async-contracts.toml":
            quoted = shlex.quote(str(source))
            result = subprocess.run(
                go_env_script(f"go run ./test/parity/cmd/asynccheck -generate -previous {quoted} -previous-upstream {shlex.quote(str(mirror_before))}"),
                cwd=ROOT, check=True, capture_output=True, text=True, timeout=1800,
            )
            out.write_text(result.stdout, encoding="utf-8")
        else:
            out.write_text(with_version(source.read_text(encoding="utf-8"), field_name, pin.version), encoding="utf-8")
        written.append(rel)
        print(f"pin-ledgers: wrote {out.relative_to(ROOT)} from {source.relative_to(ROOT)}")
    install_generated(pin, written, dest)
    carry_interface_ledgers(pin, previous, args.force)
    for target in ("interface-delta-generate", "interface-delta", "custom-factory-ledger"):
        subprocess.run(["make", "--no-print-directory", target], cwd=ROOT, check=True, timeout=3600)


INTERFACES = "test/parity/interfaces"


@contextlib.contextmanager
def committed_exceptions_kept(pin: Pin):
    """The prune of an earlier pin's reviewed exceptions (autobind/reviewed/reviewed-types.json) that `make ledger-update` writes belongs to the pin
    that is committed: under a PI_UPSTREAM_REF override the file is restored when the block ends."""
    reviewed = ROOT / "test/parity/interface-closure/autobind/reviewed/reviewed-types.json"
    snapshot = reviewed.read_bytes() if reviewed.exists() else None
    try:
        yield
    finally:
        if pin != pinlib.committed() and snapshot is not None:
            reviewed.write_bytes(snapshot)


def cmd_gaps(args: argparse.Namespace) -> None:
    """The gap list of the effective pin: bind its ledgers (`make ledger-update`), then check them (`make ledger-check`). The check alone
    stops on every reviewed exception the earlier pin's mapping made stale, which only the update prunes (the pin-follow pipeline once
    stopped there and went green only after a hand-run `make ledger-update`)."""
    pin, _override = pinlib.effective()
    with committed_exceptions_kept(pin):
        for target in ["ledger-update"] * args.updates + ["ledger-check"]:
            subprocess.run(["make", "--no-print-directory", target], cwd=ROOT, check=True, timeout=7200)
    print(f"gaps: the gap list of Pi {pin.version} is derived and ledger-check passes")


def carry_interface_ledgers(pin: Pin, previous: Pin, force: bool) -> None:
    """Generate the pin's reviewed interface ledgers from the earlier pin's, each only when it is absent (--force: again).

    Every row whose upstream inputs are unchanged keeps its reviewed disposition; a changed row is pending. Pi's inventories
    (`make interface-proposals`) and the upstream mirror must already exist for the pin. The gap baseline is then derived by
    `make ledger-update`, which needs two runs: the first binds, the second settles the pending rows that are not gaps.
    """
    version, before = pin.version, previous.version
    interfaces = ROOT / INTERFACES
    mirror_before = ROOT / ".upstream" / previous.mirror_dir

    def target(name: str) -> Path:
        return interfaces / name.replace("{v}", version)

    def source(name: str) -> Path:
        return interfaces / name.replace("{v}", before)

    def run_go(command: str, out: Path | None = None) -> None:
        result = subprocess.run(go_env_script(command), cwd=ROOT, check=True, capture_output=out is not None, text=True, timeout=3600)
        if out is not None:
            out.write_text(result.stdout, encoding="utf-8")

    def absent(path: Path) -> bool:
        if path.exists() and not force:
            print(f"pin-ledgers: {path.relative_to(ROOT)} exists, kept")
            return False
        return True

    rel = lambda path: shlex.quote(str(path.relative_to(ROOT)))  # noqa: E731
    mapping = target("mapping-v{v}.json")
    if absent(mapping):
        run_go(f"go run ./test/parity/cmd/interfaceinventory -inventory {rel(target('upstream-v{v}.json'))} -observable {rel(target('cli-v{v}.json'))} -generate-pending -previous-mapping {rel(source('mapping-v{v}.json'))} -previous-upstream-root {shlex.quote(str(mirror_before))}", mapping)
        with committed_exceptions_kept(pin):
            for _ in range(2):
                subprocess.run(["make", "--no-print-directory", "ledger-update"], cwd=ROOT, check=True, timeout=7200)
        print(f"pin-ledgers: {mapping.relative_to(ROOT)} and the gap baseline derived from {source('mapping-v{v}.json').name}")
    tests = target("test-mapping-v{v}.json")
    if absent(tests):
        run_go(f"go run ./test/parity/cmd/testinventorycheck -generate-pending -previous-mapping {rel(source('test-mapping-v{v}.json'))}", tests)
    out = target("behavior-owner-overrides-v{v}.json")
    if absent(out):
        data = json.loads(source("behavior-owner-overrides-v{v}.json").read_text(encoding="utf-8"))
        data["upstreamVersion"] = version
        out.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
    out = target("test-porting-policy-v{v}.json")
    if absent(out):
        # An entry is the reviewed tags of a test file; its hash follows the file in the new inventory, a removed file loses its
        # entry, and a new file has none, so the release gate lists it as untagged until it is reviewed.
        data = json.loads(source("test-porting-policy-v{v}.json").read_text(encoding="utf-8"))
        hashes = {f["path"]: f["sha256"] for f in json.loads(target("upstream-tests-v{v}.json").read_text(encoding="utf-8"))["files"]}
        data["upstreamVersion"] = version
        data["entries"] = [dict(entry, upstreamTestHash=hashes[entry["path"]]) for entry in data["entries"] if entry["path"] in hashes]
        out.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
    inputs = target("behavior-input-mapping-v{v}.json")
    if absent(inputs):
        run_go(
            f"go run ./test/parity/cmd/behaviorcheck -generate-input-mapping -input-inventory {rel(target('behavior-inputs-v{v}.json'))}"
            f" -previous-input-mapping {rel(source('behavior-input-mapping-v{v}.json'))} -previous-input-inventory {rel(source('behavior-inputs-v{v}.json'))}"
            f" -owner-overrides {rel(target('behavior-owner-overrides-v{v}.json'))} -families test/parity/families.toml -output-input-mapping {rel(inputs)}")
    sync = ROOT / "test/parity/upstream-sync" / f"v{version}.toml"
    if absent(sync):
        # The delta is against the exact earlier pin, whose mirror the audit needs (`make upstream-delta` builds it).
        run_go(f"go run ./test/parity/cmd/upstreamdelta -generate -from {shlex.quote(before)} -to {shlex.quote(version)}", sync)


def install_generated(pin: Pin, written: list[str], dest: Path) -> None:
    """Make the ledgers just generated for the committed pin the active ones.

    A bump to a pin with no archived ledgers leaves the earlier pin's ledgers in place with only their version field
    rewritten (`pin.py sync`). asynccheck does not hash sources, so the earlier pin's reviewed row of a changed file would
    stay green; the generated ledger, in which that row is pending, therefore replaces it. Under a PI_UPSTREAM_REF override
    the active ledgers belong to the committed pin and stay untouched.
    """
    if pin != pinlib.committed():
        return
    for rel in written:
        (ROOT / rel).write_text((dest / Path(rel).name).read_text(encoding="utf-8"), encoding="utf-8")
        print(f"pin-ledgers: {rel} is now the generated ledger of Pi {pin.version}")


PUBLISHED_ENV = "PIG_PARITY_PI_PUBLISHED"


def published_opt_in() -> bool:
    return os.environ.get(PUBLISHED_ENV) == "1"


def cmd_install_oracle(args: argparse.Namespace) -> None:
    """Build Pi from source at the pin and link it as the oracle; PIG_PARITY_PI_PUBLISHED=1 installs the published release instead."""
    import oracle

    pin, _override = pinlib.effective()
    if published_opt_in():
        sdk = json.loads((oracle.SDK_TS / "package.json").read_text())["devDependencies"]["@earendil-works/pi-coding-agent"]
        if pin.kind != "tag" or sdk != pin.package_version:
            raise PinError(f"{PUBLISHED_ENV}=1 installs the published release of a tag pin whose version sdk-ts declares; the pin is {pin.version} and sdk-ts declares {sdk}")
        print(f"install-oracle: {PUBLISHED_ENV}=1: installing the PUBLISHED Pi {pin.package_version}, not a build of {pin.ref}", file=sys.stderr)
        subprocess.run([sys.executable, str(ROOT / "automation/ci/npm-locked.py"), "extensions/sdk-ts"], cwd=ROOT, check=True, timeout=1800)
    else:
        oracle.link_sdk(oracle.ensure(pin, force=args.force))
    got = oracle.pi_version(oracle.SDK_TS / "node_modules")
    if got != pin.package_version:
        raise PinError(f"the installed Pi reports {got}, the pin needs {pin.package_version}")
    print(f"install-oracle: Pi {pin.version}: {oracle.SDK_TS / 'node_modules'} -> {os.path.realpath(oracle.SDK_TS / 'node_modules')}")
    point_current(pin)
    oracle.link_mirror(pin)


def point_current(pin: Pin) -> None:
    """Make .upstream/current the pin's mirror when it is present, so the source tree follows the oracle."""
    mirror = ROOT / ".upstream" / pin.mirror_dir
    current = ROOT / ".upstream" / "current"
    if not mirror.exists() or (current.is_symlink() and os.readlink(current) == pin.mirror_dir):
        return
    if current.is_symlink() or current.is_file():
        current.unlink()
    current.symlink_to(pin.mirror_dir)
    print(f"install-oracle: .upstream/current -> {pin.mirror_dir}")


def cmd_freeze_oracle(args: argparse.Namespace) -> None:
    import oracle

    pin, _override = pinlib.effective()
    source = Path(args.source) if args.source else None
    if pin.kind == "tag" and source is None:
        raise PinError("a release pin freezes the data its published package shipped: pass --from <pi-ai dist/providers/data>")
    print(f"freeze-oracle: wrote {oracle.freeze(pin, source)}")


def cmd_freeze_closure(args: argparse.Namespace) -> None:
    import oracle

    pin, _override = pinlib.effective()
    lock = Path(args.published_lock) if args.published_lock else None
    print(f"freeze-closure: wrote {oracle.freeze_closure(pin, lock)}")


def cmd_retire(args: argparse.Namespace) -> None:
    """Delete the carried work (post-release backports, the carried oracle) that Pi <ref> already holds (automation/pin/carried.py)."""
    import carried

    target = pinlib.resolve(args.ref, pinlib.committed())
    decisions = carried.retire(target)
    for line in carried.summary(decisions):
        print(f"retire: {line}")
    print(f"retire: {len(decisions['changed'])} file(s) changed" + (": " + ", ".join(decisions["changed"]) if decisions["changed"] else ""))


def cmd_oracle_node(_: argparse.Namespace) -> None:
    import nodefetch

    print(nodefetch.ensure())


def cmd_link_mirror(_: argparse.Namespace) -> None:
    import oracle

    pin, _override = pinlib.effective()
    modules = oracle.link_mirror(pin)
    if modules is not None:
        print(f"link-mirror: .upstream/{pin.mirror_dir}/node_modules -> {modules}")


def cmd_mirror_data(args: argparse.Namespace) -> None:
    import oracle

    oracle.mirror_data(args.commit, Path(args.target))


def cmd_vendor(args: argparse.Namespace) -> None:
    import vendor

    pin, override = pinlib.effective()
    if override:
        raise PinError("vendor writes the committed pin's packs: unset PI_UPSTREAM_REF")
    vendor.sync(pin)


def cmd_check_vendored(args: argparse.Namespace) -> None:
    import vendor

    pin, override = pinlib.effective()
    problems = vendor.check(pin) + (vendor.check_rebuild(pin) if args.rebuild else [])
    for consumer in args.installed:
        problems += vendor.check_installed(consumer)
    if problems:
        raise PinError("the vendored Pi packages are not the pin's:\n  " + "\n  ".join(problems))
    print(f"check-vendored: third_party/pi-packs holds the " + ("rebuilt " if args.rebuild else "") + f"durable closure of Pi {pin.version} and every consumer installs it")


def cmd_override_tree(_: argparse.Namespace) -> None:
    """Under PI_UPSTREAM_REF: build (once) the tree the oracle tests of that Pi commit read (vendor.override_tree) and print its
    path. The build reports on stderr, and the repository's own Pi (mirror, oracle link, installs) is not touched. The commit
    may be the pin's own: the tree is then the pin's Pi, read the way another commit's is."""
    import vendor

    if not os.environ.get(pinlib.OVERRIDE_ENV, "").strip():
        raise PinError("override-tree builds the tree of the Pi commit a run names: set PI_UPSTREAM_REF=<commit>")
    pin, _override = pinlib.effective()
    out = os.dup(1)
    os.dup2(2, 1)
    try:
        tree = vendor.override_tree(pin)
    finally:
        sys.stdout.flush()
        os.dup2(out, 1)
        os.close(out)
    print(tree)


def cmd_check_oracle(_: argparse.Namespace) -> None:
    import oracle

    pin, _override = pinlib.effective()
    got = oracle.pi_version(oracle.SDK_TS / "node_modules")
    if got != pin.package_version:
        raise PinError(f"the installed Pi reports {got}, the pin needs {pin.package_version}")
    stamp = Path(os.path.realpath(oracle.SDK_TS / "node_modules")).parent / oracle.STAMP
    data = json.loads(stamp.read_text()) if stamp.exists() else None
    if published_opt_in():
        if data is not None:
            raise PinError(f"{PUBLISHED_ENV}=1 but the installed Pi is the build of commit {data.get('commit')}; run `{PUBLISHED_ENV}=1 make parity-deps`")
        print(f"check-oracle: PUBLISHED Pi {pin.package_version} (opt-in), not a build of {pin.ref}")
        return
    if data is None:
        raise PinError(f"the installed Pi is a published package, not a build of {pin.ref}; run `make parity-deps` (or set {PUBLISHED_ENV}=1 to use the published release on purpose)")
    if data.get("commit") != pin.commit or data.get("source") != "build":
        raise PinError(f"the installed Pi is not the source build of commit {pin.commit} (stamp: {data})")
    frozen = oracle.frozen_digest(pin)
    if frozen is not None and data.get("provider_data_sha256") != frozen:
        raise PinError(f"the installed Pi was built with model data {data.get('provider_data_sha256')}, the frozen snapshot is {frozen}; rebuild with `pin.py install-oracle --force`")
    dependencies = oracle.carried_dependencies(pin)
    closure = oracle.oracle_closure(pin)  # the frozen closure with the carried dependencies in place
    with_carried = " with its carried dependencies" if dependencies else ""
    if closure is not None:
        if data.get("closure_sha256") != oracle.closure_digest(closure):
            raise PinError(f"the installed Pi was built with another dependency closure than the frozen one{with_carried}; rebuild with `pin.py install-oracle --force`")
        problems = oracle.tree_differences(oracle.SDK_TS / "node_modules", closure) + oracle.carried_problems(oracle.SDK_TS / "node_modules", dependencies)
        if problems:
            raise PinError(f"the installed dependency tree differs from the frozen closure{with_carried}:\n  " + "\n  ".join(problems[:20]))
    if data.get("backports", "") != oracle.backports_digest(pin):
        raise PinError("the installed Pi carries other post-release backports than automation/gen/pi-post-release-backports.mjs lists; rebuild with `pin.py install-oracle --force`")
    if data.get("catalog", "") != oracle.catalog_digest(pin):
        raise PinError("the installed Pi's catalog data was made from another recording or overlay than automation/gen/catalog-source holds; rebuild with `pin.py install-oracle --force`")
    if data.get("catalog"):
        installed_data = oracle.data_digest(oracle.backport_places(oracle.SDK_TS / "node_modules")["pi-ai"] / "dist" / "providers" / "data")
        if installed_data != data.get("catalog_data_sha256"):
            raise PinError(f"the installed Pi's provider data has the digest {installed_data}, its build stamped {data.get('catalog_data_sha256')}; rebuild with `pin.py install-oracle --force`")
    problem = oracle.bundle_problem(oracle.SDK_TS / "node_modules", data)
    if problem:
        raise PinError(f"{problem}; rebuild with `pin.py install-oracle --force`")
    catalog = f"; catalog data {data['catalog_data_sha256'][:12]} from the recording of {oracle.catalog_fetched_at()}" if data.get("catalog") else ""
    carried = f"; backports {data['backports'][:12]} in the compiled output and in the pi bundle {data.get('bundle_sha256', '')[:12] or '(none: the bin is compiled output)'}" if data.get("backports") else ""
    if pin == pinlib.committed():
        from carried import oracle_ledger_problems

        unaccounted = oracle_ledger_problems(pin)
        if unaccounted:
            raise PinError("the sync ledgers follow Pi commits whose changes the oracle does not account for (it would answer as the pin):\n  " + "\n  ".join(unaccounted))
    if dependencies and closure is not None:
        carried += f"; closure {data['closure_sha256'][:12]}: the frozen {oracle.frozen_closure_digest(pin)[:12]} with {len(dependencies)} carried " + ("dependency" if len(dependencies) == 1 else "dependencies") + " (" + ", ".join(f"{d['name']} {d['to']} of Pi {d['id']}" for d in dependencies) + ")"
    print(f"check-oracle: Pi {pin.version} (source build of {pin.commit[:8]}{carried}{catalog})")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub_ = parser.add_subparsers(dest="command", required=True)
    sub_.add_parser("show").set_defaults(fn=cmd_show)
    get = sub_.add_parser("get")
    get.add_argument("key")
    get.set_defaults(fn=cmd_get)
    sub_.add_parser("env").set_defaults(fn=cmd_env)
    sub_.add_parser("make-vars").set_defaults(fn=cmd_make_vars)
    sub_.add_parser("tarball").set_defaults(fn=cmd_tarball)
    spec = sub_.add_parser("mirror-spec")
    spec.add_argument("version")
    spec.set_defaults(fn=cmd_mirror_spec)
    sub_.add_parser("sync-from").set_defaults(fn=cmd_sync_from)
    sync = sub_.add_parser("sync")
    sync.add_argument("--check", action="store_true")
    sync.set_defaults(fn=cmd_sync)
    bump = sub_.add_parser("bump")
    bump.add_argument("ref")
    bump.set_defaults(fn=cmd_bump)
    inst = sub_.add_parser("install-oracle")
    inst.add_argument("--force", action="store_true")
    inst.set_defaults(fn=cmd_install_oracle)
    sub_.add_parser("check-oracle").set_defaults(fn=cmd_check_oracle)
    sub_.add_parser("vendor").set_defaults(fn=cmd_vendor)
    vendored = sub_.add_parser("check-vendored")
    vendored.add_argument("--rebuild", action="store_true", help="also pack the pinned source build again and compare the bytes")
    vendored.add_argument("--installed", action="append", default=[], metavar="CONSUMER", help="also require the node_modules of this consumer (durable/interop) to be an install of the vendored packs")
    vendored.set_defaults(fn=cmd_check_vendored)
    sub_.add_parser("link-mirror").set_defaults(fn=cmd_link_mirror)
    data = sub_.add_parser("mirror-data")
    data.add_argument("--commit", required=True)
    data.add_argument("--target", required=True)
    data.set_defaults(fn=cmd_mirror_data)
    sub_.add_parser("oracle-node").set_defaults(fn=cmd_oracle_node)
    retire = sub_.add_parser("retire")
    retire.add_argument("ref")
    retire.set_defaults(fn=cmd_retire)
    sub_.add_parser("override-tree").set_defaults(fn=cmd_override_tree)
    ledgers = sub_.add_parser("ledgers")
    ledgers.add_argument("--from", dest="previous")
    ledgers.add_argument("--force", action="store_true")
    ledgers.set_defaults(fn=cmd_ledgers)
    gaps = sub_.add_parser("gaps")
    gaps.add_argument("--updates", type=int, default=1, help="how many times to bind the ledgers before the check (a bump needs two: the first binds, the second settles the pending rows that are not gaps)")
    gaps.set_defaults(fn=cmd_gaps)
    closure = sub_.add_parser("freeze-closure")
    closure.add_argument("--from-published-lock", dest="published_lock")
    closure.set_defaults(fn=cmd_freeze_closure)
    freeze = sub_.add_parser("freeze-oracle")
    freeze.add_argument("--from", dest="source")
    freeze.set_defaults(fn=cmd_freeze_oracle)
    args = parser.parse_args()
    try:
        args.fn(args)
    except PinError as error:
        print(f"pin: {error}", file=sys.stderr)
        sys.exit(1)
    except subprocess.CalledProcessError as error:
        print(f"pin: {' '.join(map(str, error.cmd))} exited {error.returncode}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
