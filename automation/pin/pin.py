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
  pin.py check-oracle         verify the installed oracle is the effective pin's Pi
  pin.py link-mirror          point the effective pin's mirror (.upstream/<mirror>/node_modules) at its source build's
                              dependencies, so oracle scripts that import the mirrored source resolve its imports
  pin.py ledgers [--from REF] generate the effective pin's ledgers that one file holds (async-contracts, behavior-contracts,
                              known-gaps, under test/parity/pin-ledgers/) from the earlier pin REF (default: the committed pin),
                              plus the interface delta manifest and the custom-factory draft; skips what exists
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import subprocess
import sys
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
    text = sub(r"(prints PiG's version as `<PiG release>\+<Pi release>`, for example `\d+\.\d+\.\d+\+)[^`]+(`)", rf"\g<1>{pin.version}\g<2>", text, "README.md", 1)
    sentence = (f"The pinned Pi release is {pin.version} at commit `{pin.commit}`." if pin.kind == "tag"
                else f"The pinned Pi build is {pin.version}, commit `{pin.commit}` of the main branch.")
    return sub(r"The pinned Pi (?:release|build) is [^`]*at commit `[0-9a-f]{40}`\.|The pinned Pi build is [^`]*, commit `[0-9a-f]{40}` of the main branch\.", sentence, text, "README.md", 1)


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


# (path, transform(pin, text), command to refresh a lock when the file changed or None)
INTEROP = ['@earendil-works/chord', '@earendil-works/pi-ai', '@earendil-works/pi-durable']
ORACLE_PACKAGES = ['@earendil-works/pi-agent-core', '@earendil-works/pi-ai', '@earendil-works/pi-coding-agent', '@earendil-works/pi-tui']
RULES: list[tuple[str, object, str | None]] = [
    ("README.md", lambda p, t: readme(p, t), None),
    ("docs/site/docs/index.md", regex_rule(r"^(\| Pinned Pi release \(behavior oracle\) \| Pi )\S+( \|)", r"\g<1>{v}\g<2>"), None),
    ("extensions/sdk-ts/package.json", package_deps(["@earendil-works/pi-coding-agent"]), "extensions/sdk-ts"),
    ("extensions/sdk-ts/README.md", lambda p, t: sub(r"(pi-coding-agent@)\S+", rf"\g<1>{p.package_version}", sub(r"(API from Pi )\S+?(?=\. It contains)", rf"\g<1>{p.package_version}", t, "README"), "README"), None),
    ("test/parity/known-gaps.toml", regex_rule(r'^(pi_version = ")[^"]+(")', r'\g<1>{v}\g<2>'), None),
    ("test/parity/behavior-contracts.toml", regex_rule(r'^(upstream_version = ")[^"]+(")', r'\g<1>{v}\g<2>'), None),
    ("test/parity/async-contracts.toml", regex_rule(r'\A(version = ")[^"]+(")', r'\g<1>{v}\g<2>'), None),
    ("automation/images/ci-parity/Dockerfile", lambda p, t: ci_commit(p, regex_rule(r"^(ARG PI_VERSION=)\S+", r"\g<1>{v}", "package_version")(p, t)), None),
    ("automation/images/ci-parity/metadata.env", lambda p, t: ci_tag(p, t), None),
    ("automation/images/ci-parity/package.json", package_deps(ORACLE_PACKAGES), "automation/images/ci-parity"),
    ("durable/interop/package.json", package_deps(INTEROP), "durable/interop"),
    ("bench/k8s/image/pi-durable/package.json", package_deps(INTEROP), "bench/k8s/image/pi-durable"),
    ("coding/extension/host/subprocess/runtime-node/harness/package.json", regex_rule(r'^(  "version": ")[^"]+(")', r'\g<1>{v}\g<2>', "package_version"), None),
]


def derived(pin: Pin) -> dict[str, str]:
    out = {"internal/coding/pigversion/pigversion.go": pinlib.render_pigversion(pin, pinlib.pig_version())}
    for rel, transform, _lock in RULES:
        path = ROOT / rel
        text = path.read_text(encoding="utf-8")
        out[rel] = transform(pin, text)
    return out


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
    if args.check:
        if stale:
            raise PinError("derived from .upstream/pin but stale (run `make pin-sync`): " + ", ".join(stale))
        print(f"pin-check: {len(wanted)} derived files match Pi {pin.version}")
        return
    for directory in sorted(changed_locks):
        npm = "npm"
        print(f"+ npm install --package-lock-only in {directory}", file=sys.stderr, flush=True)
        subprocess.run([npm, "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=ROOT / directory, check=True, timeout=600)
    print(f"pin-sync: Pi {pin.version}: {len(stale)} file(s) updated" + (": " + ", ".join(stale) if stale else ""))


# Ledgers that one file holds for the committed pin only. A bump archives the outgoing pin's copy and restores
# the incoming pin's archived copy when it has one, so moving the pin back reproduces the earlier pin's ledgers.
PIN_LEDGERS = ["test/parity/known-gaps.toml", "test/parity/behavior-contracts.toml", "test/parity/async-contracts.toml"]
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
    "test/parity/behavior-contracts.toml": "upstream_version",
    "test/parity/async-contracts.toml": "version",
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
        reviewed = ROOT / "test/parity/interface-closure/autobind/reviewed-types.json"
        snapshot = reviewed.read_bytes() if reviewed.exists() else None
        for _ in range(2):
            subprocess.run(["make", "--no-print-directory", "ledger-update"], cwd=ROOT, check=True, timeout=7200)
        if pin != pinlib.committed() and snapshot is not None:
            reviewed.write_bytes(snapshot)  # the prune of an earlier pin's exceptions belongs to the pin that is committed
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
        earlier = ROOT / "test/parity/upstream-sync" / f"v{before}.toml"
        base = tomllib.loads(earlier.read_text(encoding="utf-8"))["from"] if earlier.exists() else before
        run_go(f"go run ./test/parity/cmd/upstreamdelta -generate -from {shlex.quote(base)} -to {shlex.quote(version)}" + (f" -previous {rel(earlier)}" if earlier.exists() else ""), sync)


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
    if pin.kind != "tag":
        durable = oracle.ensure_durable(pin, force=args.force)
        oracle.link_interop(durable)
        print(f"install-oracle: Pi Durable {pin.version}: durable/interop/node_modules -> {os.path.realpath(durable)}")
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


def cmd_link_mirror(_: argparse.Namespace) -> None:
    import oracle

    pin, _override = pinlib.effective()
    modules = oracle.link_mirror(pin)
    if modules is not None:
        print(f"link-mirror: .upstream/{pin.mirror_dir}/node_modules -> {modules}")


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
    closure = oracle.read_frozen_lock(pin)
    if closure is not None:
        if data.get("closure_sha256") != oracle.closure_digest(closure):
            raise PinError("the installed Pi was built with another dependency closure than the frozen one; rebuild with `pin.py install-oracle --force`")
        problems = oracle.tree_differences(oracle.SDK_TS / "node_modules", closure)
        if problems:
            raise PinError("the installed dependency tree differs from the frozen closure:\n  " + "\n  ".join(problems[:20]))
    if pin.kind != "tag":
        oracle.check_durable(pin)
    print(f"check-oracle: Pi {pin.version} (source build of {pin.commit[:8]})")


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
    sub_.add_parser("link-mirror").set_defaults(fn=cmd_link_mirror)
    ledgers = sub_.add_parser("ledgers")
    ledgers.add_argument("--from", dest="previous")
    ledgers.add_argument("--force", action="store_true")
    ledgers.set_defaults(fn=cmd_ledgers)
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
