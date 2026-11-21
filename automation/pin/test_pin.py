# SPDX-License-Identifier: MIT
"""Tests of the pin model and its derivations. They use no network: refs that
need resolution are exercised through the cached and committed paths."""
import argparse
import contextlib
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import tarfile
from pathlib import Path, PurePosixPath, PureWindowsPath
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402
import pin as pin_tool  # noqa: E402
import oracle  # noqa: E402
import backport_fixture  # noqa: E402

TAG = pinlib.Pin("v1.1.0", "a" * 40, "1.1.0", "1.1.0")
COMMIT = pinlib.Pin("b" * 40, "b" * 40, "1.1.0", "1.1.0+bbbbbbbb")


class ModelTests(unittest.TestCase):
    def test_kinds(self):
        self.assertEqual(TAG.kind, "tag")
        self.assertEqual(COMMIT.kind, "commit")
        self.assertEqual(TAG.tag, "v1.1.0")
        self.assertEqual(COMMIT.tag, "b" * 40)

    def test_display_and_metadata(self):
        self.assertEqual(TAG.mirror_dir, "v1.1.0")
        self.assertEqual(COMMIT.mirror_dir, "v1.1.0+bbbbbbbb")
        self.assertEqual(COMMIT.build_metadata, "1.1.0.bbbbbbbb")
        self.assertNotIn("+", COMMIT.build_metadata)

    def test_validate_rejects_inconsistent_pins(self):
        for bad in (
            pinlib.Pin("v1.1.0", "short", "1.1.0", "1.1.0"),
            pinlib.Pin("v1.1.0", "a" * 40, "1.1.0", "1.1.0+aaaaaaaa"),
            pinlib.Pin("v1.2.0", "a" * 40, "1.1.0", "1.1.0"),
            pinlib.Pin("b" * 40, "b" * 40, "1.1.0", "1.1.0"),
            pinlib.Pin("c" * 40, "b" * 40, "1.1.0", "1.1.0+bbbbbbbb"),
        ):
            with self.assertRaises(pinlib.PinError, msg=bad):
                pinlib.validate(bad, "test")

    def test_pin_file_round_trip(self):
        with tempfile.TemporaryDirectory() as directory:
            for pin in (TAG, COMMIT):
                path = Path(directory) / "pin"
                path.write_text(pinlib.render_pin_file(pin))
                self.assertEqual(pinlib.read_pin_file(path), pin)

    def test_pin_file_rejects_extra_duplicate_and_missing_keys(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "pin"
            for body in ("ref=v1.1.0\n", pinlib.render_pin_file(TAG) + "ref=v1.1.0\n", pinlib.render_pin_file(TAG) + "extra=1\n"):
                path.write_text(body)
                with self.assertRaises(pinlib.PinError):
                    pinlib.read_pin_file(path)


class PigVersionTests(unittest.TestCase):
    def test_render_carries_every_field_and_a_single_plus(self):
        text = pinlib.render_pigversion(COMMIT, "0.5.0")
        self.assertIn('const UpstreamVersion = "1.1.0+bbbbbbbb"', text)
        self.assertIn('const UpstreamPackageVersion = "1.1.0"', text)
        self.assertIn('const UpstreamCommit = "' + "b" * 40 + '"', text)
        self.assertIn('const Version = PigVersion + "+1.1.0.bbbbbbbb"', text)

    def test_pig_version_is_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "pigversion.go"
            path.write_text(pinlib.render_pigversion(TAG, "0.7.3"))
            self.assertEqual(pinlib.pig_version(path), "0.7.3")


class RepositoryTests(unittest.TestCase):
    def test_only_active_interface_snapshots_are_tracked(self):
        reviewed = re.search(r'^const UpstreamReviewedVersion = "([^"]+)"', (pinlib.ROOT / "coding/upstream.go").read_text(), re.M).group(1)
        suffixes = tuple(f"-v{version}.json" for version in {pinlib.committed().version, reviewed})
        paths = subprocess.check_output(["git", "ls-files", "test/parity/interfaces"], cwd=pinlib.ROOT, text=True).splitlines()
        obsolete = [path for path in paths if re.search(r"-v\d.*\.json$", path) and not path.endswith(suffixes)]
        self.assertEqual(obsolete, [], "Remove outgoing interface snapshots after carrying the new pin's reviewed ledgers; Git history keeps the earlier copies")

    def test_only_active_interface_snapshots_are_unignored(self):
        pin = pinlib.committed()
        current = f"test/parity/interfaces/mapping-v{pin.version}.json"
        old = "test/parity/interfaces/mapping-v0.0.0.json"
        result = subprocess.run(["git", "check-ignore", "--no-index", "--stdin"], input=f"{current}\n{old}\n", cwd=pinlib.ROOT, text=True, capture_output=True, check=False)
        self.assertEqual(result.stdout.splitlines(), [old])
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_committed_derivations_match_the_pin(self):
        stale = [rel for rel, text in pin_tool.derived(pinlib.committed()).items() if (pinlib.ROOT / rel).read_text(encoding="utf-8") != text]
        self.assertEqual(stale, [])

    def test_derivation_changes_every_copy_for_a_commit_pin(self):
        base = pin_tool.derived(pinlib.committed())
        moved = pin_tool.derived(COMMIT)
        changed = {rel for rel in base if base[rel] != moved[rel]}
        for rel in (
            "internal/coding/pigversion/pigversion.go",
            ".gitignore",
            "README.md",
            "docs/site/docs/index.md",
            "test/parity/known-gaps.toml",
            "test/parity/behavior-contracts.toml",
            "test/parity/async-contracts.toml",
            "automation/images/ci-parity/Dockerfile",
            "automation/images/ci-parity/metadata.env",
        ):
            self.assertIn(rel, changed)
        self.assertTrue("commit/" + "b" * 40 in moved["README.md"])
        self.assertTrue("1.1.0%2B" + "b" * 8 in moved["README.md"])
        self.assertTrue("The pinned Pi build is 1.1.0+bbbbbbbb, commit `" + "b" * 40 in moved["README.md"])
        # The README's example is the string the binary prints (pigversion.Version: PigVersion + "+" + the build metadata, one plus).
        binary = re.search(r'^const Version = PigVersion \+ "(\+[^"]+)"', moved["internal/coding/pigversion/pigversion.go"], re.M).group(1)
        self.assertEqual(binary, "+1.1.0.bbbbbbbb")
        example = re.search(r"for example `(\d+\.\d+\.\d+)(\+[^`]+)`", moved["README.md"])
        self.assertEqual(example.group(2), binary)
        self.assertTrue("ARG PI_COMMIT=" + "b" * 40 in moved["automation/images/ci-parity/Dockerfile"])

    def test_provenance_names_the_committed_pin(self):
        # docs/project/PROVENANCE.md once kept naming Pi v0.99.1 / d86654ab long after the pin moved.
        pin = pinlib.committed()
        text = (pinlib.ROOT / "docs/project/PROVENANCE.md").read_text(encoding="utf-8")
        self.assertIn(f"`{pin.version}`", text)
        self.assertIn(f"`{pin.commit}`", text)
        self.assertNotIn("v0.99.1", text)
        self.assertIn("docs/project/PROVENANCE.md", pin_tool.derived(pin))

    def test_provenance_follows_a_moved_pin_of_either_kind(self):
        for pin in (COMMIT, TAG):
            text = pin_tool.derived(pin)["docs/project/PROVENANCE.md"]
            self.assertIn(f"- Upstream pin: `{pin.version}`", text)
            self.assertIn(f"- Upstream commit: `{pin.commit}`", text)
        self.assertIn("Upstream pin: `1.1.0+bbbbbbbb` (a commit of Pi's main branch)", pin_tool.derived(COMMIT)["docs/project/PROVENANCE.md"])
        self.assertIn("Upstream pin: `1.1.0` (release tag `v1.1.0`)", pin_tool.derived(TAG)["docs/project/PROVENANCE.md"])

    def test_no_pin_literal_outside_the_pin_file_and_its_derivations(self):
        # Every pin consumer reads the pin through pigversion.go or pin.py: a hard-coded literal would survive a bump.
        pin = pinlib.committed()
        derived = set(pin_tool.derived(pin))
        offenders = []
        for rel in ("Makefile", "automation/make/parity.mk", "automation/ci/build-ci-image.sh", "automation/gen/mirror-upstream.sh", "automation/gen/generate-model-catalogs.sh"):
            # The benchmark arm `pi-<release>` names a published Pi release it compares against, whatever the pin is.
            text = re.sub(rf"\bpi-{re.escape(pin.package_version)}\b", "", (pinlib.ROOT / rel).read_text(encoding="utf-8"))
            if pin.package_version in text or pin.commit in text:
                offenders.append(rel)
        self.assertEqual(offenders, [])
        self.assertTrue(derived)


class OverrideTests(unittest.TestCase):
    def run_pin(self, ref, *args):
        env = dict(os.environ)
        env.pop(pinlib.OVERRIDE_ENV, None)
        if ref is not None:
            env[pinlib.OVERRIDE_ENV] = ref
        return subprocess.run([sys.executable, str(Path(__file__).with_name("pin.py")), *args], capture_output=True, text=True, env=env, cwd=pinlib.ROOT, timeout=60)

    def test_committed_pin_needs_no_override(self):
        out = self.run_pin(None, "get", "override")
        self.assertEqual(out.stdout.strip(), "false")

    def test_the_committed_ref_is_an_override_of_nothing(self):
        committed = pinlib.committed()
        out = self.run_pin(committed.ref, "make-vars")
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertIn("override=false", out.stdout)
        self.assertNotIn("goflags", out.stdout)

    def test_a_short_commit_of_the_committed_pin_is_not_an_override(self):
        out = self.run_pin(pinlib.committed().commit[:10], "get", "override")
        self.assertEqual(out.stdout.strip(), "false")

    def test_overlay_is_shared_by_concurrent_runs_so_it_is_never_rewritten_in_place(self):
        """Every run of one pin writes the same overlay files; a rewrite truncates them under a concurrent `go` that is reading them."""
        flag = pinlib.overlay(COMMIT)
        config = Path(flag.removeprefix("-overlay="))
        before = {p: (p.stat().st_ino, p.stat().st_mtime_ns) for p in (config, config.parent / "pigversion.go")}
        os.utime(config, ns=(1, 1))
        os.utime(config.parent / "pigversion.go", ns=(1, 1))
        self.assertEqual(pinlib.overlay(COMMIT), flag)
        for path in before:
            self.assertEqual(path.stat().st_mtime_ns, 1, f"{path.name} was rewritten although its content is unchanged")
            self.assertEqual(path.stat().st_ino, before[path][0])
        self.assertEqual(json.loads(config.read_text())["Replace"].popitem()[1], str(config.parent / "pigversion.go"))

    def test_overlay_replaces_pigversion_for_a_run(self):
        flag = pinlib.overlay(COMMIT)
        self.assertTrue(flag.startswith("-overlay="))
        config = json.loads(Path(flag.removeprefix("-overlay=")).read_text())
        (replacement,) = config["Replace"].values()
        self.assertEqual(config["Replace"][str(pinlib.PIGVERSION)], replacement)
        self.assertIn("1.1.0+bbbbbbbb", Path(replacement).read_text())



class FrozenDataTests(unittest.TestCase):
    def directory(self, files):
        root = Path(tempfile.mkdtemp())
        for name, body in files.items():
            (root / name).write_text(body)
        return root

    def test_snapshot_is_reproducible_and_round_trips(self):
        data = self.directory({"a.json": "{}", "b.json": "[1]"})
        first, second = Path(tempfile.mkdtemp()) / "x.tgz", Path(tempfile.mkdtemp()) / "x.tgz"
        oracle.write_snapshot(data, first)
        os.utime(data / "a.json", (1, 1))
        oracle.write_snapshot(data, second)
        self.assertEqual(first.read_bytes(), second.read_bytes())
        self.assertEqual((first.parent / "x.tgz.sha256").read_text().strip(), oracle.data_digest(data))

    def test_digest_changes_with_content_and_names(self):
        base = oracle.data_digest(self.directory({"a.json": "{}"}))
        self.assertNotEqual(base, oracle.data_digest(self.directory({"a.json": "{ }"})))
        self.assertNotEqual(base, oracle.data_digest(self.directory({"b.json": "{}"})))

    def test_unpack_rejects_a_snapshot_that_does_not_match_its_digest(self):
        data = self.directory({"a.json": "{}"})
        frozen = Path(tempfile.mkdtemp())
        pin = COMMIT
        saved = oracle.FROZEN
        oracle.FROZEN = frozen
        try:
            oracle.write_snapshot(data, oracle.frozen_path(pin))
            source = Path(tempfile.mkdtemp())
            self.assertEqual(oracle.unpack_snapshot(pin, source), oracle.data_digest(data))
            (frozen / (oracle.frozen_path(pin).name + ".sha256")).write_text("0" * 64 + "\n")
            with self.assertRaises(pinlib.PinError):
                oracle.unpack_snapshot(pin, Path(tempfile.mkdtemp()))
        finally:
            oracle.FROZEN = saved


class TarballTests(unittest.TestCase):
    def test_the_download_holds_no_descriptor_of_the_file_it_renames(self):
        # Windows refuses to rename a file the process holds open (WinError 32), so the descriptor mkstemp returns must be
        # closed before curl writes the file and os.replace moves it.
        cache = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, cache, True)
        made = []
        open_at_download = []
        real_mkstemp, real_close = tempfile.mkstemp, os.close
        closed = set()

        def mkstemp(*args, **kwargs):
            made.append(real_mkstemp(*args, **kwargs))
            return made[-1]

        def close(descriptor):
            closed.add(descriptor)
            real_close(descriptor)

        def curl(argv, **_):
            open_at_download.extend(descriptor for descriptor, _ in made if descriptor not in closed)
            Path(argv[argv.index("-o") + 1]).write_bytes(b"tarball")

        with (
            mock.patch.dict(os.environ, {"XDG_CACHE_HOME": cache}),
            mock.patch.object(pinlib.tempfile, "mkstemp", mkstemp),
            mock.patch.object(pinlib.os, "close", close),
            mock.patch.object(pinlib.subprocess, "run", curl),
        ):
            path = pinlib.tarball("c" * 40, False)
        for descriptor in open_at_download:
            real_close(descriptor)
        self.assertEqual(path.read_bytes(), b"tarball")
        self.assertEqual(len(made), 1)
        self.assertEqual(open_at_download, [], "the mkstemp descriptor is still open while curl writes and os.replace renames")


class UpstreamDeltaMirrorTests(unittest.TestCase):
    """`make upstream-delta` audits against the exact earlier pin its manifest names, whose mirror it builds."""

    def test_sync_from_is_the_earlier_pin_of_the_pins_manifest(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, root, True)
        (root / "test/parity/upstream-sync").mkdir(parents=True)
        (root / "test/parity/upstream-sync" / f"v{COMMIT.version}.toml").write_text(f'from = "1.0.0+aaaaaaaa"\nto = "{COMMIT.version}"\n')
        out = io.StringIO()
        with (
            mock.patch.object(pin_tool, "ROOT", root),
            mock.patch.object(pinlib, "effective", lambda: (COMMIT, False)),
            contextlib.redirect_stdout(out),
        ):
            pin_tool.cmd_sync_from(None)
        self.assertEqual(out.getvalue().strip(), "1.0.0+aaaaaaaa")

    def test_a_missing_manifest_is_an_error_not_an_empty_from(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, root, True)
        with mock.patch.object(pin_tool, "ROOT", root), mock.patch.object(pinlib, "effective", lambda: (COMMIT, False)):
            with self.assertRaises(pinlib.PinError):
                pin_tool.cmd_sync_from(None)

    def test_mirror_spec_names_a_commit_build_only(self):
        for version in ("1.4.0", "1.4.0+zzzz"):
            with self.subTest(version=version), self.assertRaises(pinlib.PinError):
                pin_tool.cmd_mirror_spec(mock.Mock(version=version))

    def test_mirror_spec_checks_the_tarball_is_the_named_build(self):
        commit = "b556cfe5" + "0" * 32
        with (
            mock.patch.object(pinlib, "full_commit", lambda short: commit),
            mock.patch.object(pinlib, "tarball", lambda ref, tag: Path("/cache/x.tar.gz")),
            mock.patch.object(pinlib, "tarball_identity", lambda path: (commit, "1.3.0")),
        ):
            with self.assertRaises(pinlib.PinError):
                pin_tool.cmd_mirror_spec(mock.Mock(version="1.4.0+b556cfe5"))
        out = io.StringIO()
        with (
            mock.patch.object(pinlib, "full_commit", lambda short: commit),
            mock.patch.object(pinlib, "tarball", lambda ref, tag: Path("/cache/x.tar.gz")),
            mock.patch.object(pinlib, "tarball_identity", lambda path: (commit, "1.4.0")),
            contextlib.redirect_stdout(out),
        ):
            pin_tool.cmd_mirror_spec(mock.Mock(version="1.4.0+b556cfe5"))
        self.assertEqual(out.getvalue().split(), [commit, "1.4.0", "/cache/x.tar.gz"])

    def test_the_make_target_builds_the_from_mirror_before_the_audit(self):
        recipe = (pinlib.ROOT / "automation/make/parity.mk").read_text(encoding="utf-8")
        target = recipe[recipe.index("\nupstream-delta:"):]
        target = target[: target.index("\n\n")]
        self.assertLess(target.index("mirror-upstream.sh --version"), target.index("upstreamdelta"))
        self.assertIn("sync-from", target)

    def test_a_cached_tarball_expands_a_short_commit_without_a_request(self):
        cache = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, cache, True)
        commit = "b556cfe5" + "0" * 32
        (cache / "checkouts/github.com" / pinlib.UPSTREAM_REPO / "tarballs").mkdir(parents=True)
        (cache / "checkouts/github.com" / pinlib.UPSTREAM_REPO / "tarballs" / f"{commit}.tar.gz").write_bytes(b"")
        with (
            mock.patch.dict(os.environ, {"XDG_CACHE_HOME": str(cache)}),
            mock.patch.object(pinlib.subprocess, "run", side_effect=AssertionError("asked the network")),
        ):
            self.assertEqual(pinlib.full_commit("b556cfe5"), commit)

    def test_an_extracted_from_mirror_is_reused_without_a_request(self):
        root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, root, True)
        for rel in ("automation/gen/mirror-upstream.sh", "automation/pin/pin.py", "automation/pin/pinlib.py", "automation/pin/oracle.py"):
            (root / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(pinlib.ROOT / rel, root / rel)
        commit = "b556cfe5" + "0" * 32
        mirror = root / ".upstream" / "v1.4.0+b556cfe5"
        (mirror / "packages/coding-agent/src/core/extensions").mkdir(parents=True)
        (mirror / "packages/coding-agent/src/core/extensions/types.ts").write_text("")
        for n in range(200):
            (mirror / f"packages/coding-agent/src/f{n}.ts").write_text("")
        (mirror / ".pig-upstream-source.json").write_text(json.dumps({"version": "1.4.0+b556cfe5", "tag": commit, "commit": commit}, indent=2) + "\n")
        shims = root / "shims"
        shims.mkdir()
        shim = shims / "curl"
        shim.write_text("#!/bin/sh\necho curl called >&2\nexit 22\n")
        shim.chmod(0o755)
        env = {**os.environ, "PATH": f"{shims}{os.pathsep}{os.environ['PATH']}", "XDG_CACHE_HOME": str(root / "cache"), "CI": "1"}
        done = subprocess.run(["bash", str(root / "automation/gen/mirror-upstream.sh"), "--version", "v1.4.0+b556cfe5", "--sources-only"], env=env, capture_output=True, text=True, timeout=120)
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertIn("already mirrored", done.stdout)
        self.assertNotIn("curl called", done.stderr)


class SourceBuildTests(unittest.TestCase):
    def source(self):
        root = Path(tempfile.mkdtemp())
        (root / "package.json").write_text(json.dumps({"scripts": {"hydrate:model-data": "x", "build:offline": "y"}}))
        return root

    def test_a_release_tag_without_frozen_data_fails_instead_of_hydrating(self):
        saved = (oracle.FROZEN, oracle.npm)
        oracle.FROZEN, oracle.npm = Path(tempfile.mkdtemp()), lambda *a, **k: None
        try:
            with self.assertRaisesRegex(pinlib.PinError, "no frozen model data for release v1.1.0"):
                oracle.build_source(TAG, self.source())
        finally:
            oracle.FROZEN, oracle.npm = saved

    def hydrating_npm(self, live: dict[str, str]):
        """An npm that writes `live` as the hydrated model data when asked to hydrate."""
        def npm(*args, cwd=None, **_kwargs):
            if args[:2] == ("run", "hydrate:model-data"):
                target = cwd / oracle.DATA_DIR
                target.mkdir(parents=True, exist_ok=True)
                for name, text in live.items():
                    (target / name).write_text(text)
        return npm

    def test_a_commit_without_frozen_data_freezes_its_first_build_and_a_rebuild_reproduces_it(self):
        saved = (oracle.FROZEN, oracle.npm)
        oracle.FROZEN = Path(tempfile.mkdtemp())
        try:
            oracle.npm = self.hydrating_npm({"openrouter.json": '{"day":"first"}'})
            first = oracle.build_source(COMMIT, self.source())
            self.assertTrue(oracle.frozen_path(COMMIT).exists(), "the first build must freeze the data it hydrated")
            self.assertEqual(oracle.frozen_digest(COMMIT), first)
            oracle.npm = self.hydrating_npm({"openrouter.json": '{"day":"second"}'})
            rebuilt_source = self.source()
            self.assertEqual(oracle.build_source(COMMIT, rebuilt_source), first, "a later build of the commit must use the frozen data, not live data")
            self.assertEqual((rebuilt_source / oracle.DATA_DIR / "openrouter.json").read_text(), '{"day":"first"}')
        finally:
            oracle.FROZEN, oracle.npm = saved

    def test_an_interrupted_first_build_freeze_leaves_nothing_a_later_build_trusts(self):
        # The archive is the switch every later build takes; an archive without its digest marker would wedge the commit
        # (unpack fails on a missing digest, and a frozen snapshot is never rewritten).
        saved = (oracle.FROZEN, oracle.npm, oracle.data_digest)
        oracle.FROZEN = Path(tempfile.mkdtemp())
        try:
            oracle.npm = self.hydrating_npm({"openrouter.json": '{"day":"first"}'})

            def interrupted(_directory):
                raise OSError("interrupted")
            oracle.data_digest = interrupted
            with self.assertRaises(OSError):
                oracle.build_source(COMMIT, self.source())
            self.assertFalse(oracle.frozen_path(COMMIT).exists(), "an interrupted freeze must not leave an archive")
            oracle.data_digest = saved[2]
            digest = oracle.build_source(COMMIT, self.source())
            self.assertEqual(oracle.frozen_digest(COMMIT), digest)
            self.assertEqual(oracle.build_source(COMMIT, self.source()), digest)
        finally:
            oracle.FROZEN, oracle.npm, oracle.data_digest = saved

    def test_a_failed_build_leaves_no_stamp_and_reuses_nothing(self):
        store = Path(tempfile.mkdtemp())
        saved = (os.environ.get("PIG_PI_BUILD_STORE"), oracle.extract_source, oracle.build_source)
        os.environ["PIG_PI_BUILD_STORE"] = str(store)

        def failing(*_a, **_k):
            raise pinlib.PinError("build broke")

        oracle.extract_source, oracle.build_source = lambda *a, **k: None, failing
        try:
            with self.assertRaisesRegex(pinlib.PinError, "build broke"):
                oracle.ensure(COMMIT)
            self.assertEqual(list(store.rglob(oracle.STAMP)), [], "a failed build must publish nothing")
        finally:
            if saved[0] is None:
                del os.environ["PIG_PI_BUILD_STORE"]
            else:
                os.environ["PIG_PI_BUILD_STORE"] = saved[0]
            oracle.extract_source, oracle.build_source = saved[1], saved[2]

    def test_published_oracle_is_an_explicit_opt_in(self):
        saved = os.environ.pop(pin_tool.PUBLISHED_ENV, None)
        try:
            self.assertFalse(pin_tool.published_opt_in())
            os.environ[pin_tool.PUBLISHED_ENV] = "1"
            self.assertTrue(pin_tool.published_opt_in())
            os.environ[pin_tool.PUBLISHED_ENV] = "true"
            self.assertFalse(pin_tool.published_opt_in())
        finally:
            os.environ.pop(pin_tool.PUBLISHED_ENV, None)
            if saved is not None:
                os.environ[pin_tool.PUBLISHED_ENV] = saved


class SharedStoreTests(unittest.TestCase):
    """ensure() against the shared build store with the npm-facing build steps faked: worktrees of one pin share the store
    and merge gates read a published oracle while another worktree builds."""

    def setUp(self):
        self.store = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: (oracle.thaw(self.store), shutil.rmtree(self.store, ignore_errors=True)))
        self.builds = 0
        self.build_delay = 0.0
        self.probe = lambda: None
        self.binary = "cli.js"
        self.counter = threading.Lock()
        self.identity("first")
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(mock.patch.dict(os.environ, {"PIG_PI_BUILD_STORE": str(self.store)}))
        fakes = {
            "extract_source": self.extract_source, "build_source": self.build_source, "pack": self.pack,
            "closure_for": self.closure_for, "install_closure": self.install_closure, "with_typescript": self.with_typescript,
            "typescript_version": lambda: "5", "frozen_digest": lambda _pin: self.data,
            "frozen_closure_digest": lambda _pin: oracle.closure_digest(self.lock()),
        }
        for name, fake in fakes.items():
            stack.enter_context(mock.patch.object(oracle, name, fake))
        # The committed recording belongs to the committed pin: these fake builds of another commit carry no catalog data (a test that wants one writes its own).
        stack.enter_context(mock.patch.object(oracle, "CATALOG_SOURCE", self.store / "no-recording"))
        stack.enter_context(mock.patch.object(oracle, "CATALOG_OVERLAY", self.store / "no-recording" / "pi-overlay"))

    def identity(self, label: str) -> None:
        """Switch to the frozen inputs of another worktree of the same pin commit."""
        self.label = label
        self.data = hashlib.sha256(label.encode()).hexdigest()

    def lock(self) -> dict:
        return {"name": "pi-oracle", "version": "0.0.0", "lockfileVersion": 3, "packages": {"": {"note": self.label}, oracle.LOCK_KEY: {"version": "1.1.0"}}}

    def extract_source(self, _pin, destination):
        self.probe()
        destination.mkdir(parents=True, exist_ok=True)

    def build_source(self, _pin, _source):
        self.probe()
        with self.counter:
            self.builds += 1
        time.sleep(self.build_delay)
        return self.data

    def pack(self, _source, destination):
        self.probe()
        destination.mkdir(parents=True, exist_ok=True)
        return {}

    def closure_for(self, _pin, _source, _packed, _work):
        self.probe()
        return self.lock()

    def install_closure(self, _closure, _packed, flat):
        self.probe()
        agent = flat / "node_modules" / "@earendil-works" / "pi-coding-agent"
        agent.mkdir(parents=True)
        (agent / "package.json").write_text(json.dumps({"name": oracle.AGENT, "version": "1.1.0", "bin": {"pi": self.binary}, "oracle": self.label}))
        (agent / self.binary).parent.mkdir(parents=True, exist_ok=True)
        (agent / self.binary).write_text("")

    def with_typescript(self, _flat, _destination, _version):
        self.probe()

    @staticmethod
    def label_of(modules: Path) -> str | None:
        manifest = modules / "@earendil-works" / "pi-coding-agent" / "package.json"
        return json.loads(manifest.read_text())["oracle"] if manifest.is_file() else None

    def test_two_identities_of_one_commit_build_two_directories_and_neither_touches_the_other(self):
        first = oracle.ensure(COMMIT)
        first_stamp = (first.parent / oracle.STAMP).read_text()
        self.identity("second")
        second = oracle.ensure(COMMIT)
        self.assertNotEqual(first.resolve(), second.resolve(), "two frozen inputs of one commit share one build directory")
        self.assertEqual(self.label_of(first), "first", "building the second identity rewrote the first")
        self.assertEqual((first.parent / oracle.STAMP).read_text(), first_stamp)
        self.assertEqual(self.label_of(second), "second")
        self.identity("first")
        self.assertEqual(oracle.ensure(COMMIT), first, "the first identity must be reused, not rebuilt")
        self.assertEqual(self.builds, 2)

    def test_a_reader_of_a_published_oracle_never_sees_it_change_during_a_rebuild_of_another_identity(self):
        held = oracle.ensure(COMMIT)
        stamp = held.parent / oracle.STAMP
        stamp_text = stamp.read_text()
        seen = []

        def read():
            seen.append((self.label_of(held), stamp.is_file() and stamp.read_text() == stamp_text))

        done = threading.Event()

        def reader():
            while not done.is_set():
                read()

        self.probe = read
        thread = threading.Thread(target=reader)
        thread.start()
        try:
            self.identity("second")
            oracle.ensure(COMMIT)
        finally:
            done.set()
            thread.join()
        read()
        self.assertTrue(seen)
        self.assertEqual([s for s in seen if s != ("first", True)], [], "the held oracle disappeared or changed while another identity built")

    def test_concurrent_ensures_of_one_identity_build_once(self):
        self.build_delay = 0.3
        start = threading.Barrier(2)
        results, errors = [], []

        def run():
            start.wait()
            try:
                results.append(oracle.ensure(COMMIT))
            except Exception as error:  # noqa: BLE001 - reported below
                errors.append(error)

        threads = [threading.Thread(target=run) for _ in range(2)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(errors, [])
        self.assertEqual(self.builds, 1, "two concurrent builders of one identity both built")
        self.assertEqual(len(set(results)), 1)
        self.assertEqual(self.label_of(results[0]), "first")

    def test_a_cached_build_without_the_oracle_package_is_rebuilt_not_reused(self):
        held = oracle.ensure(COMMIT)
        oracle.thaw(held.parent)
        shutil.rmtree(held)
        held.mkdir()
        rebuilt = oracle.ensure(COMMIT)
        self.assertNotEqual(rebuilt.resolve(), held.resolve())
        self.assertEqual(self.label_of(rebuilt), "first")
        self.assertTrue((rebuilt / "@earendil-works" / "pi-coding-agent" / "cli.js").is_file())
        self.assertEqual(self.builds, 2)

    def test_a_cached_build_of_another_package_version_is_rebuilt_not_reused(self):
        held = oracle.ensure(COMMIT)
        manifest = held / "@earendil-works" / "pi-coding-agent" / "package.json"
        manifest.write_text(json.dumps({**json.loads(manifest.read_text()), "version": "0.0.1"}))
        rebuilt = oracle.ensure(COMMIT)
        self.assertNotEqual(rebuilt.resolve(), held.resolve())
        self.assertEqual(json.loads((rebuilt / "@earendil-works" / "pi-coding-agent" / "package.json").read_text())["version"], "1.1.0")
        self.assertEqual(self.builds, 2)

    @unittest.skipIf(os.name == "nt", "a directory's mode is not enforced on Windows")
    def test_a_published_oracle_has_no_writable_directory(self):
        held = oracle.ensure(COMMIT)
        writable = [str(d) for d, _n, _f in os.walk(held) if os.stat(d).st_mode & 0o222]
        self.assertEqual(writable, [], "an npm ci through extensions/sdk-ts/node_modules could empty the shared build")

    @unittest.skipIf(os.name == "nt" or os.geteuid() == 0, "root ignores a directory's mode")
    def test_emptying_a_published_oracle_the_way_npm_ci_does_fails_and_leaves_it_whole(self):
        held = oracle.ensure(COMMIT)
        for name in os.listdir(held):  # npm ci removes each entry of node_modules, recursively
            with self.assertRaises(PermissionError):
                shutil.rmtree(held / name)
        self.assertEqual(self.label_of(held), "first")
        self.assertEqual(oracle.ensure(COMMIT), held, "the whole build must still be the one reused")
        self.assertEqual(self.builds, 1)

    def test_a_thawed_build_can_be_deleted(self):
        held = oracle.ensure(COMMIT)
        oracle.thaw(held.parent)
        shutil.rmtree(held.parent)
        self.assertFalse(held.parent.exists())

    def test_a_build_that_installs_no_oracle_package_fails_and_publishes_nothing(self):
        def empty_install(_closure, _packed, flat):
            (flat / "node_modules").mkdir(parents=True)

        with mock.patch.object(oracle, "install_closure", empty_install):
            with self.assertRaises(FileNotFoundError):
                oracle.ensure(COMMIT)
        self.assertEqual(list(self.store.rglob("*.ref")), [], "an empty build must not be published")
        self.assertEqual(list(self.store.rglob(oracle.STAMP)), [])

    def test_a_forced_rebuild_publishes_a_new_directory_and_keeps_the_held_one(self):
        held = oracle.ensure(COMMIT)
        rebuilt = oracle.ensure(COMMIT, force=True)
        self.assertNotEqual(held, rebuilt)
        self.assertEqual(self.label_of(held), "first")
        self.assertTrue((held.parent / oracle.STAMP).is_file())
        self.assertEqual(oracle.ensure(COMMIT), rebuilt, "the forced build must be the one reused")
        self.assertEqual(self.builds, 2)

    def test_a_failed_forced_rebuild_keeps_the_published_oracle_and_leaves_no_partial_build(self):
        held = oracle.ensure(COMMIT)
        entries = sorted(p.name for p in oracle.entry(COMMIT).iterdir())

        def failing(*_a, **_k):
            raise pinlib.PinError("build broke")

        with mock.patch.object(oracle, "build_source", failing):
            with self.assertRaisesRegex(pinlib.PinError, "build broke"):
                oracle.ensure(COMMIT, force=True)
        self.assertEqual(self.label_of(held), "first")
        self.assertTrue((held.parent / oracle.STAMP).is_file())
        self.assertEqual(sorted(p.name for p in oracle.entry(COMMIT).iterdir()), entries, "a failed build left a partial directory")
        self.assertEqual(oracle.ensure(COMMIT), held)
        self.assertEqual(self.builds, 1)

    def carries_backports(self, pin=COMMIT):
        """The committed pin is `pin`, whose oracle carries the post-release backports; apply_backports is recorded, not run."""
        applied = []
        stack = contextlib.ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(mock.patch.object(pinlib, "committed", lambda: pin))
        stack.enter_context(mock.patch.object(oracle, "apply_backports", applied.append))
        stack.enter_context(mock.patch.object(oracle, "carried_dependencies", lambda *_a: []))  # the fake closure installs no dependency to replace
        return applied

    def recording(self, pi_commit: str) -> Path:
        """A recording of the catalog source for the committed pin (the real file set, fake bytes): meta.json, the archive and one overlay file."""
        root = self.store / "recording"
        (root / "pi-overlay").mkdir(parents=True)
        (root / "meta.json").write_text(json.dumps({"fetchedAt": "2026-01-01T00:00:00.000Z", "piCommit": pi_commit}))
        (root / "responses.tar.gz").write_bytes(b"archive")
        (root / "pi-overlay" / "file.ts").write_text("overlay")
        return root

    def test_the_catalog_data_of_the_oracle_is_made_from_the_recording_of_the_committed_pin_only(self):
        self.carries_backports()
        root = self.recording(COMMIT.commit)
        with mock.patch.object(oracle, "CATALOG_SOURCE", root), mock.patch.object(oracle, "CATALOG_OVERLAY", root / "pi-overlay"):
            digest = oracle.catalog_digest(COMMIT)
            self.assertRegex(digest, "^[0-9a-f]{64}$")
            self.assertEqual(oracle.catalog_digest(pinlib.Pin("c" * 40, "c" * 40, "1.1.0", "1.1.0+cccccccc")), "", "another commit takes no recording")
            (root / "responses.tar.gz").write_bytes(b"archive!")
            self.assertNotEqual(oracle.catalog_digest(COMMIT), digest, "one changed byte of the recording is another oracle")
            (root / "responses.tar.gz").write_bytes(b"archive")
            (root / "pi-overlay" / "file.ts").write_text("overlay!")
            self.assertNotEqual(oracle.catalog_digest(COMMIT), digest, "a changed overlay file is another oracle")

    def test_catalog_generator_command_passes_replay_as_a_file_url(self):
        # On Windows Node rejects a bare drive-letter path (D:\...) passed to --import with
        # ERR_UNSUPPORTED_ESM_URL_SCHEME ("Received protocol 'd:'"); --import must receive a file:// URL.
        win_replay = PureWindowsPath(r"D:\a\PiG\PiG\x.mjs")
        self.assertEqual(oracle.node_import_specifier(win_replay), "file:///D:/a/PiG/PiG/x.mjs")
        self.assertEqual(
            oracle.node_import_specifier(PureWindowsPath(r"D:\a\PiG with spaces\café\x.mjs")),
            "file:///D:/a/PiG%20with%20spaces/caf%C3%A9/x.mjs",
        )
        self.assertEqual(
            oracle.node_import_specifier(PurePosixPath("/tmp/a b/café/x.mjs")),
            "file:///tmp/a%20b/caf%C3%A9/x.mjs",
        )

        # Exercise generate_catalog_data itself with a Windows-shaped CATALOG_REPLAY and verify
        # the argv passed to subprocess.run uses the file:// URL rather than D:\...
        root = self.store / "catalog-cmd-recording"
        (root / "pi-overlay").mkdir(parents=True)
        (root / "meta.json").write_text(json.dumps({"fetchedAt": "2026-01-01T00:00:00.000Z", "piCommit": COMMIT.commit}))
        with tarfile.open(root / "responses.tar.gz", "w:gz"):
            pass
        source = self.store / "catalog-cmd-src"
        ai = source / "packages" / "ai"
        (ai / "catalog").mkdir(parents=True)
        (ai / "scripts").mkdir(parents=True)
        (ai / "src" / "providers" / "data").mkdir(parents=True)
        (ai / "package.json").write_text("{}")

        commands = []

        def fake_run(cmd, *, cwd, **kwargs):
            commands.append(cmd)
            out = Path(cwd) / "src" / "providers" / "data"
            out.mkdir(parents=True, exist_ok=True)
            (out / "openai.json").write_text("{}")
            return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

        with (
            mock.patch.object(oracle, "CATALOG_SOURCE", root),
            mock.patch.object(oracle, "CATALOG_OVERLAY", root / "pi-overlay"),
            mock.patch.object(oracle, "CATALOG_REPLAY", win_replay),
            mock.patch.object(oracle.shutil, "which", lambda _name: "node"),
            mock.patch.object(oracle.subprocess, "run", fake_run),
        ):
            oracle.generate_catalog_data(source, self.store / "catalog-cmd-work")

        self.assertEqual(len(commands), 1)
        self.assertEqual(commands[0], ["node", "--import", "file:///D:/a/PiG/PiG/x.mjs", "catalog/generate.ts", "--strict"])

    def test_a_recording_made_for_another_pin_stops_the_build(self):
        self.carries_backports()
        root = self.recording("c" * 40)
        with mock.patch.object(oracle, "CATALOG_SOURCE", root), mock.patch.object(oracle, "CATALOG_OVERLAY", root / "pi-overlay"):
            with self.assertRaisesRegex(pinlib.PinError, "names the Pi generator of commit c+, the pin is b+"):
                oracle.ensure(COMMIT)

    def test_the_committed_pins_oracle_carries_the_backports_and_is_stamped_with_them(self):
        applied = self.carries_backports()
        built = oracle.ensure(COMMIT)
        self.assertEqual(applied, [built], "the installed tree must be patched once, before the build is published")
        digest = hashlib.sha256(oracle.BACKPORTS.read_bytes()).hexdigest()
        self.assertEqual(json.loads((built.parent / oracle.STAMP).read_text())["backports"], digest)
        self.assertEqual(oracle.ensure(COMMIT), built, "a build with the current backports must be reused, not rebuilt")
        self.assertEqual(self.builds, 1)

    def test_a_build_without_the_backports_is_not_the_oracle_of_the_pin_that_carries_them(self):
        plain = oracle.ensure(COMMIT)
        self.assertNotIn("backports", json.loads((plain.parent / oracle.STAMP).read_text()))
        applied = self.carries_backports()
        backported = oracle.ensure(COMMIT)
        self.assertNotEqual(plain.resolve(), backported.resolve(), "the unpatched build was reused as the backported oracle")
        self.assertEqual(applied, [backported])
        self.assertNotIn("backports", json.loads((plain.parent / oracle.STAMP).read_text()), "the new build rewrote the first")
        self.assertEqual(self.builds, 2)

    def test_a_pin_the_backports_were_not_recorded_for_gets_a_plain_oracle(self):
        applied = self.carries_backports(TAG)  # the committed pin is another one: PI_UPSTREAM_REF built COMMIT for one run
        built = oracle.ensure(COMMIT)
        self.assertEqual(applied, [])
        self.assertNotIn("backports", json.loads((built.parent / oracle.STAMP).read_text()))

    def test_the_backported_oracles_bundle_is_rebuilt_after_the_patch_and_stamped(self):
        applied = self.carries_backports()
        calls = []

        def rebuild(source, modules, catalog=None):
            calls.append((source, modules, list(applied)))
            return "f" * 64

        with mock.patch.object(oracle, "rebuild_bundle", rebuild):
            built = oracle.ensure(COMMIT)
        self.assertEqual(calls, [(built.parent / "src", built, [built])], "the bundle is rebuilt once, from the source build, after the installed tree is patched")
        self.assertEqual(json.loads((built.parent / oracle.STAMP).read_text())["bundle_sha256"], "f" * 64)

    def test_a_backported_build_whose_pi_bin_runs_another_bundle_than_it_stamped_is_not_the_oracle(self):
        self.carries_backports()
        self.binary = "dist/bundle/cli.js"
        with mock.patch.object(oracle, "rebuild_bundle", lambda _source, _modules, _catalog=None: ""):  # a build of the code that patched the compiled output only
            unbundled = oracle.ensure(COMMIT)
        self.assertNotIn("bundle_sha256", json.loads((unbundled.parent / oracle.STAMP).read_text()))
        with mock.patch.object(oracle, "rebuild_bundle", lambda _source, modules, _catalog=None: oracle.tree_digest(oracle.bundle_of(modules))):
            rebuilt = oracle.ensure(COMMIT)
            self.assertNotEqual(unbundled.resolve(), rebuilt.resolve(), "a build whose bundle is the pin's was reused as the backported oracle")
            self.assertEqual(oracle.ensure(COMMIT), rebuilt, "a build whose bundle is the stamped one must be reused")
            self.assertEqual(self.builds, 2)
            (rebuilt / "@earendil-works" / "pi-coding-agent" / self.binary).write_text("another bundle")
            self.assertNotEqual(oracle.ensure(COMMIT).resolve(), rebuilt.resolve(), "a build whose bundle changed after its stamp was reused")
        self.assertEqual(self.builds, 3)

    def test_the_key_of_a_backported_oracle_is_not_the_key_of_a_build_made_before_the_bundle_was_rebuilt(self):
        before = hashlib.sha256(b"data\nclosure\nbackports").hexdigest()[:12]
        self.assertNotEqual(oracle.identity("data", "closure", "backports"), before, "a tree on the earlier code would be handed this build, and this tree that one")
        self.assertEqual(oracle.identity("data", "closure"), hashlib.sha256(b"data\nclosure").hexdigest()[:12], "an oracle without backports keeps its key")

    def test_changing_a_backport_rebuilds_the_oracle(self):
        self.carries_backports()
        first = oracle.ensure(COMMIT)
        with mock.patch.object(oracle, "backports_digest", lambda _pin: "0" * 64):
            second = oracle.ensure(COMMIT)
        self.assertNotEqual(first.resolve(), second.resolve())
        self.assertEqual(self.builds, 2)


class PiVersionTests(unittest.TestCase):
    def test_a_missing_oracle_package_is_a_pin_error_that_names_the_remedy(self):
        with tempfile.TemporaryDirectory() as empty:
            with self.assertRaisesRegex(pinlib.PinError, "make parity-deps"):
                oracle.pi_version(Path(empty))


def make_tree(root: Path, packages: dict[str, str]) -> Path:
    """A node_modules tree from {lock key: name@version}."""
    for key, ident in packages.items():
        name, version = ident.rsplit("@", 1)
        directory = root / key.removeprefix("node_modules/")
        directory.mkdir(parents=True, exist_ok=True)
        (directory / "package.json").write_text(json.dumps({"name": name, "version": version}))
    return root


class ClosureTests(unittest.TestCase):
    AGENT = "node_modules/@earendil-works/pi-coding-agent"

    def published_lock(self):
        return {
            "name": "sdk", "lockfileVersion": 3,
            "packages": {
                "": {"name": "sdk", "devDependencies": {oracle.AGENT: "1.1.0", "typescript": "5"}},
                self.AGENT: {"version": "1.1.0", "resolved": "https://registry.npmjs.org/x.tgz", "integrity": "sha512-x", "dev": True},
                self.AGENT + "/node_modules/@earendil-works/pi-ai": {"version": "1.1.0", "resolved": "https://registry.npmjs.org/y.tgz", "integrity": "sha512-y"},
                self.AGENT + "/node_modules/openai": {"version": "7.19.0", "resolved": "https://registry.npmjs.org/openai.tgz"},
                "node_modules/typescript": {"version": "5.9.3"},
            },
        }

    def test_a_published_closure_is_the_packages_subtree_without_the_installing_project(self):
        closure = oracle.published_closure(self.published_lock(), "1.1.0")
        self.assertEqual(sorted(closure["packages"]), ["", self.AGENT, self.AGENT + "/node_modules/@earendil-works/pi-ai", self.AGENT + "/node_modules/openai"])
        self.assertEqual(closure["packages"][""]["devDependencies"], {oracle.AGENT: "1.1.0"})

    def test_freezing_a_closure_builds_unbuilt_source_before_it_packs(self):
        # A pack of source without dist has another integrity than the oracle's build, so a closure frozen from it fails `npm ci` with EINTEGRITY.
        calls = []

        def extract(_pin, destination):
            (destination / "packages" / "coding-agent").mkdir(parents=True)
            calls.append("extract")

        def build(_pin, source):
            (source / "packages" / "coding-agent" / "dist").mkdir()
            calls.append("build")

        def pack(*_a):
            calls.append("pack")
            raise pinlib.PinError("packed")

        with tempfile.TemporaryDirectory() as home:
            with mock.patch.object(oracle, "published", lambda *_a: None), mock.patch.object(oracle, "entry", lambda _pin: Path(home)), \
                    mock.patch.object(oracle, "extract_source", extract), mock.patch.object(oracle, "build_source", build), mock.patch.object(oracle, "pack", pack):
                with self.assertRaisesRegex(pinlib.PinError, "packed"):
                    oracle.freeze_closure(TAG, Path("published-lock.json"))
        self.assertEqual(calls, ["extract", "build", "pack"])

    def freezing_from(self, published_data: str, frozen_data: str):
        """freeze_closure over a published build that ships `published_data` as its model data, for a pin that froze `frozen_data`; return the source it packed and the steps it took."""
        steps = []
        packed_from = []

        def write_data(source, text):
            (source / "packages" / "coding-agent" / "dist").mkdir(parents=True, exist_ok=True)
            data = source / oracle.DIST_DATA_DIR
            data.mkdir(parents=True, exist_ok=True)
            (data / "openrouter.json").write_text(text)

        def extract(_pin, destination):
            steps.append("extract")
            (destination / "packages").mkdir(parents=True)

        def build(_pin, source):
            steps.append("build")
            write_data(source, frozen_data)

        def pack(source, *_a):
            steps.append("pack")
            packed_from.append(source)
            raise pinlib.PinError("packed")

        with tempfile.TemporaryDirectory() as home, tempfile.TemporaryDirectory() as frozen:
            built = Path(home) / "build"
            write_data(built / "src", published_data)
            frozen_source = Path(frozen) / "data"
            frozen_source.mkdir()
            (frozen_source / "openrouter.json").write_text(frozen_data)
            with mock.patch.object(oracle, "published", lambda *_a: built), mock.patch.object(oracle, "entry", lambda _pin: Path(home) / "entry"), \
                    mock.patch.object(oracle, "frozen_digest", lambda _pin: oracle.data_digest(frozen_source)), \
                    mock.patch.object(oracle, "extract_source", extract), mock.patch.object(oracle, "build_source", build), mock.patch.object(oracle, "pack", pack), \
                    mock.patch.object(oracle, "read_frozen_lock", lambda *_a: None):
                with self.assertRaisesRegex(pinlib.PinError, "packed"):
                    oracle.freeze_closure(COMMIT)
            return packed_from[0].relative_to(home), steps

    def test_freezing_a_closure_does_not_pack_a_build_made_with_other_model_data(self):
        # A pack ships dist/providers/data. A build made before the pin froze its data packs other bytes than every later build, so a closure
        # frozen from it fails `npm ci` with EINTEGRITY in a clean cache while the warm cache that still holds the old build passes.
        packed, steps = self.freezing_from(published_data='{"day":"before the freeze"}', frozen_data='{"day":"frozen"}')
        self.assertEqual(packed, Path("entry/src"))
        self.assertEqual(steps, ["extract", "build", "pack"])

    def test_freezing_a_closure_packs_the_published_build_that_ships_the_frozen_model_data(self):
        packed, steps = self.freezing_from(published_data='{"day":"frozen"}', frozen_data='{"day":"frozen"}')
        self.assertEqual(packed, Path("build/src"))
        self.assertEqual(steps, ["pack"])

    def test_a_lock_of_another_install_is_not_a_closure(self):
        with self.assertRaisesRegex(pinlib.PinError, "not the lock of an install"):
            oracle.published_closure({"lockfileVersion": 3, "packages": {"": {}, "node_modules/typescript": {"version": "5"}}}, "1.1.0")

    def test_workspace_entries_are_abstracted_and_made_concrete_again(self):
        workspaces = {oracle.AGENT: Path("a"), "@earendil-works/pi-ai": Path("b")}
        abstract = oracle.abstract_packs(oracle.published_closure(self.published_lock(), "1.1.0"), workspaces, {oracle.AGENT: "sha512-agent", "@earendil-works/pi-ai": "sha512-ai"})
        self.assertEqual(abstract["packages"][self.AGENT]["resolved"], "pig-pack:" + oracle.AGENT)
        self.assertTrue(abstract["packages"][self.AGENT + "/node_modules/openai"]["resolved"].startswith("https://"))
        concrete = oracle.concrete_packs(abstract, {oracle.AGENT: Path("/p/agent.tgz"), "@earendil-works/pi-ai": Path("/p/ai.tgz")})
        self.assertEqual(concrete["packages"][self.AGENT]["resolved"], "file:/p/agent.tgz")
        with self.assertRaisesRegex(pinlib.PinError, "does not pack"):
            oracle.concrete_packs(abstract, {oracle.AGENT: Path("/p/agent.tgz")})

    # A release's closure comes from the published install, whose lock records the registry tarballs' integrity. The packs
    # npm ci installs are source packs, which never have those bytes, so the closure must carry the packs' own integrity:
    # with the registry's, npm ci refused every build of the v1.3.0 oracle (EINTEGRITY).
    def test_a_pack_is_pinned_by_its_own_integrity_not_the_registrys(self):
        workspaces = {oracle.AGENT: Path("a"), "@earendil-works/pi-ai": Path("b")}
        closure = oracle.published_closure(self.published_lock(), "1.1.0")
        given = oracle.abstract_packs(closure, workspaces, {oracle.AGENT: "sha512-new", "@earendil-works/pi-ai": "sha512-ai"})
        self.assertEqual(given["packages"][self.AGENT]["integrity"], "sha512-new")
        self.assertEqual(given["packages"][self.AGENT + "/node_modules/@earendil-works/pi-ai"]["integrity"], "sha512-ai")
        self.assertNotIn("sha512-x", json.dumps(given))

    def test_tarball_integrity_is_the_npm_sha512(self):
        tgz = Path(tempfile.mkdtemp()) / "x.tgz"
        tgz.write_bytes(b"abc")
        self.assertEqual(oracle.tarball_integrity(tgz), "sha512-3a81oZNherrMQXNJriBBMRLm+k6JqX6iCp7u5ktV05ohkpkqJ0/BqDa6PCOj/uu9RU1EI2Q86A4qmslPpUyknw==")

    def test_the_tree_must_be_the_lock_tree_not_pis_own_layout(self):
        # The published layout hoists openai beside pi-ai; Pi's install-lock nests it inside pi-ai. Both resolve the same
        # version, but the tests (and the vendored runtime) read the published paths.
        lock = oracle.published_closure(self.published_lock(), "1.1.0")
        published = make_tree(Path(tempfile.mkdtemp()), {
            self.AGENT: "@earendil-works/pi-coding-agent@1.1.0",
            self.AGENT + "/node_modules/@earendil-works/pi-ai": "@earendil-works/pi-ai@1.1.0",
            self.AGENT + "/node_modules/openai": "openai@7.19.0",
            "node_modules/typescript": "typescript@5.9.3",
        })
        self.assertEqual(oracle.tree_differences(published, lock), [])
        pis = make_tree(Path(tempfile.mkdtemp()), {
            self.AGENT: "@earendil-works/pi-coding-agent@1.1.0",
            self.AGENT + "/node_modules/@earendil-works/pi-ai": "@earendil-works/pi-ai@1.1.0",
            self.AGENT + "/node_modules/@earendil-works/pi-ai/node_modules/openai": "openai@7.19.0",
        })
        problems = oracle.tree_differences(pis, lock)
        self.assertTrue(any("pi-coding-agent/node_modules/openai 7.19.0 is in the closure but not installed" in p for p in problems), problems)
        self.assertTrue(any("pi-ai/node_modules/openai openai@7.19.0 is installed but not in the closure" in p for p in problems), problems)

    def test_a_version_difference_and_a_platform_package_are_told_apart(self):
        lock = oracle.published_closure(self.published_lock(), "1.1.0")
        lock["packages"][self.AGENT + "/node_modules/@esbuild/darwin-arm64"] = {"version": "0.28.2", "os": ["darwin"], "cpu": ["arm64"]}
        tree = make_tree(Path(tempfile.mkdtemp()), {
            self.AGENT: "@earendil-works/pi-coding-agent@1.1.0",
            self.AGENT + "/node_modules/@earendil-works/pi-ai": "@earendil-works/pi-ai@1.1.0",
            self.AGENT + "/node_modules/openai": "openai@6.40.0",
        })
        problems = oracle.tree_differences(tree, lock)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("installed openai@6.40.0, closure 7.19.0", problems[0])

    def test_a_commit_without_a_frozen_closure_derives_it_once_and_freezes_it(self):
        saved = (oracle.FROZEN, oracle.derive_closure)
        oracle.FROZEN = Path(tempfile.mkdtemp())
        calls = []

        def derive(*_args):
            calls.append(1)
            return {"lockfileVersion": 3, "packages": {"": {"day": len(calls)}}}
        oracle.derive_closure = derive
        try:
            first = oracle.closure_for(COMMIT, Path("src"), {}, Path("work"))
            self.assertTrue(oracle.frozen_lock_path(COMMIT).exists(), "the derived closure must be frozen")
            self.assertEqual(oracle.closure_for(COMMIT, Path("src"), {}, Path("work")), first)
            self.assertEqual(len(calls), 1, "a later build must read the frozen closure, not derive a new one")
        finally:
            oracle.FROZEN, oracle.derive_closure = saved

    def test_a_release_without_a_frozen_closure_fails_and_a_frozen_one_is_used_as_is(self):
        saved = oracle.FROZEN
        oracle.FROZEN = Path(tempfile.mkdtemp())
        try:
            with self.assertRaisesRegex(pinlib.PinError, "no frozen dependency closure for release v1.1.0"):
                oracle.closure_for(TAG, Path("src"), {}, Path("work"))
            lock = oracle.published_closure(self.published_lock(), "1.1.0")
            oracle.write_frozen_lock(TAG, lock)
            self.assertEqual(oracle.closure_for(TAG, Path("src"), {}, Path("work")), lock)
            with self.assertRaisesRegex(pinlib.PinError, "already exists"):
                oracle.write_frozen_lock(TAG, lock)
        finally:
            oracle.FROZEN = saved

    def test_the_workspace_closure_of_the_agent_is_transitive(self):
        source = Path(tempfile.mkdtemp())
        for name, deps in {"coding-agent": [oracle.AGENT, "@earendil-works/pi-ai", "left-pad"], "ai": [], "other": []}.items():
            pkg = source / "packages" / name
            pkg.mkdir(parents=True)
            ident = {"coding-agent": oracle.AGENT, "ai": "@earendil-works/pi-ai", "other": "@earendil-works/unrelated"}[name]
            (pkg / "package.json").write_text(json.dumps({"name": ident, "dependencies": {d: "1" for d in deps if d != ident}}))
        self.assertEqual(oracle.closure_workspaces(source), ["@earendil-works/pi-ai", oracle.AGENT])

    def test_the_committed_pins_frozen_closure_matches_the_pin(self):
        pin = pinlib.committed()
        lock = oracle.read_frozen_lock(pin)
        self.assertIsNotNone(lock, f"no frozen closure for {pin.commit}")
        self.assertEqual(lock["packages"][""]["devDependencies"], {oracle.AGENT: pin.package_version})
        self.assertEqual(lock["packages"][oracle.LOCK_KEY]["version"], pin.package_version)


class BackportTests(unittest.TestCase):
    """apply_backports hands the installed Pi packages to the backports script; the script's own patching is tested here with the real entries."""

    def tree(self, root: Path) -> Path:
        return make_tree(root / "node_modules", {
            "node_modules/@earendil-works/pi-coding-agent": "@earendil-works/pi-coding-agent@1.5.2",
            "node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core": "@earendil-works/pi-agent-core@1.5.2",
            "node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai": "@earendil-works/pi-ai@1.5.2",
            "node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui": "@earendil-works/pi-tui@1.5.2",
            "node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-mcp": "@earendil-works/pi-mcp@1.5.2",
        })

    def test_each_installed_pi_package_is_named_to_the_script_once(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(oracle, "BACKPORTS", Path(root) / "args.mjs"):
            modules = self.tree(Path(root))
            oracle.BACKPORTS.write_text('import { writeFileSync } from "node:fs";\nwriteFileSync(process.argv[1] + ".out", JSON.stringify(process.argv.slice(2)));\n')
            oracle.apply_backports(modules)
            agent = modules / "@earendil-works" / "pi-coding-agent"
            self.assertEqual(json.loads((Path(root) / "args.mjs.out").read_text()), [
                "--packages", f"pi-agent-core={agent / 'node_modules/@earendil-works/pi-agent-core'}", f"pi-ai={agent / 'node_modules/@earendil-works/pi-ai'}", f"pi-coding-agent={agent}",
                f"pi-mcp={agent / 'node_modules/@earendil-works/pi-mcp'}", f"pi-tui={agent / 'node_modules/@earendil-works/pi-tui'}"])

    def test_a_tree_with_two_copies_of_a_patched_package_is_refused(self):
        with tempfile.TemporaryDirectory() as root:
            modules = self.tree(Path(root))
            make_tree(modules, {"node_modules/@earendil-works/pi-tui": "@earendil-works/pi-tui@1.5.2"})
            with self.assertRaisesRegex(pinlib.PinError, "2 copies of pi-tui"):
                oracle.apply_backports(modules)

    def test_backport_blocks_that_share_a_file_key_all_apply_in_file_order(self):
        # Two Pi commits can change one file. The tables were built with Object.fromEntries, so the later block replaced the earlier one silently.
        blocks = '''
patch("aaaa1111", "pi-ai/utils/shared-key.js", [["one\\n", "one, by the first block\\n"]]);
patch("bbbb2222", "pi-ai/utils/shared-key.js", [["two\\n", "two, by the second block\\n"]]);
patch("cccc3333", "pi-ai/utils/ordered-key.js", [["alpha\\n", "beta\\n"]]);
patch("dddd4444", "pi-ai/utils/ordered-key.js", [["beta\\n", "gamma\\n"]]);
'''
        text = backport_fixture.text()
        anchor = re.search(r"^const packageFile = .*\n", text, re.M)
        script = '''
import assert from "node:assert/strict";
const { backportPatches, patchBackports } = await import(process.env.BACKPORTS);
assert.equal(backportPatches["pi-ai/utils/shared-key.js"].length, 2, "both blocks of the key are in the table");
assert.equal(patchBackports("pi-ai/utils/shared-key.js", "one\\ntwo\\n"), "one, by the first block\\ntwo, by the second block\\n");
// File order: the second block's before-text is the first block's after-text.
assert.equal(patchBackports("pi-ai/utils/ordered-key.js", "alpha\\n"), "gamma\\n");
'''
        with tempfile.TemporaryDirectory() as root:
            shared = Path(root) / "pi-post-release-backports.mjs"
            shared.write_text(text[:anchor.end()] + blocks + text[anchor.end():], encoding="utf-8")
            done = subprocess.run(["node", "--input-type=module", "-e", script], env={**os.environ, "BACKPORTS": shared.as_uri()}, capture_output=True, text=True, timeout=60)
            self.assertEqual(done.returncode, 0, done.stderr)

    def test_backport_blocks_that_replace_the_same_text_of_a_file_key_fail_when_the_script_loads(self):
        # A block whose before-text an earlier block of the same key replaces too can never apply. The table builder refuses the pair when the
        # script loads and names both blocks and the file (the pair used to load, and the second block failed when it was applied).
        blocks = '''
patch("eeee5555", "pi-ai/utils/stale-key.js", [["alpha\\n", "beta\\n"]]);
patch("ffff6666", "pi-ai/utils/stale-key.js", [["alpha\\n", "delta\\n"]]);
'''
        text = backport_fixture.text()
        anchor = re.search(r"^const packageFile = .*\n", text, re.M)
        with tempfile.TemporaryDirectory() as root:
            stale = Path(root) / "pi-post-release-backports.mjs"
            stale.write_text(text[:anchor.end()] + blocks + text[anchor.end():], encoding="utf-8")
            done = subprocess.run(["node", "--input-type=module", "-e", "await import(process.env.BACKPORTS);"], env={**os.environ, "BACKPORTS": stale.as_uri()}, capture_output=True, text=True, timeout=60)
            self.assertNotEqual(done.returncode, 0, done.stdout)
            self.assertIn("backport blocks eeee5555 and ffff6666 both replace the same text of pi-ai/utils/stale-key.js", done.stderr)

    def test_a_backport_that_does_not_apply_fails_the_build_with_the_scripts_reason(self):
        with tempfile.TemporaryDirectory() as root, mock.patch.object(oracle, "BACKPORTS", backport_fixture.script()):
            modules = self.tree(Path(root))
            with self.assertRaisesRegex(pinlib.PinError, "do not apply to the installed oracle"):
                oracle.apply_backports(modules)  # the packages hold no dist/ files

    def test_the_scripts_entries_patch_a_package_at_its_dist_and_docs_paths(self):
        script = r'''
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import assert from "node:assert/strict";
const { applyPackageBackports, backportPatches, packageBackportFiles, packageFileBackports } = await import(process.env.BACKPORTS);
const root = process.env.ROOT;
const packages = {};
const entriesByFile = { ...backportPatches, ...packageFileBackports };
const where = (file) => {
  const [name, ...rest] = file.split("/");
  packages[name] = join(root, name);
  return join(root, name, name === "pi-coding-agent" && (rest[0] === "docs" || rest[0] === "schemas" || rest[0] === "examples") ? rest.join("/") : ["dist", ...rest].join("/"));
};
for (const [file, entries] of Object.entries(entriesByFile)) {
  const path = where(file);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, entries.map(([before]) => before).join("\n//--\n"));
}
assert.deepEqual(Object.keys(packages).sort(), ["pi-ai", "pi-coding-agent", "pi-mcp", "pi-tui"]);
assert.deepEqual(packageBackportFiles(packages).sort(), Object.keys(entriesByFile).map(where).sort(), "--package-files must name every file --packages rewrites");
applyPackageBackports(packages);
for (const [file, entries] of Object.entries(entriesByFile)) {
  const text = readFileSync(where(file), "utf8");
  for (const [, after] of entries) assert.ok(text.includes(after), `${file} lacks ${after.slice(0, 60)}`);
}
assert.throws(() => applyPackageBackports(packages), /no longer applies/, "a second application must fail, not pass silently");
'''
        with tempfile.TemporaryDirectory() as root:
            done = subprocess.run(["node", "--input-type=module", "-e", script], env={**os.environ, "BACKPORTS": backport_fixture.script().as_uri(), "ROOT": root}, capture_output=True, text=True, timeout=60)
            self.assertEqual(done.returncode, 0, done.stderr)


FAKE_BUNDLER = r'''
// Stands for Pi's scripts/build-coding-agent-bundle.mjs: one chunk made of the five packages' compiled output with the whitespace removed
// (so no backport's text survives in it as written), under the coding agent's dist/bundle.
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const bundle = join(root, "packages", "coding-agent", "dist", "bundle");
rmSync(bundle, { recursive: true, force: true });
mkdirSync(join(bundle, "chunks"), { recursive: true });
const code = ["agent", "ai", "coding-agent", "mcp", "tui"].map((name) => readFileSync(join(root, "packages", name, "dist", "code.js"), "utf8")).join("");
writeFileSync(join(bundle, "chunks", "chunk.js"), code.replace(/\s+/g, ""));
writeFileSync(join(bundle, "cli.js"), 'import "./chunks/chunk.js";\n');
'''

FAKE_BACKPORTS = r'''
// Stands for the backports script: one entry per package (dist/code.js: old -> new) and one for the coding agent's docs.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const [mode, ...args] = process.argv.slice(2);
const packages = Object.fromEntries(args.map((arg) => arg.split("=")));
const files = Object.values(packages).map((dir) => join(dir, "dist", "code.js"));
if (packages["pi-coding-agent"]) files.push(join(packages["pi-coding-agent"], "docs", "page.md"));
if (mode === "--package-files") process.stdout.write(JSON.stringify(files));
else if (mode === "--packages") {
  for (const file of files) {
    const text = readFileSync(file, "utf8");
    if (!text.includes("old")) throw new Error(`no longer applies: ${file}`);
    writeFileSync(file, text.replace("old", "new"));
  }
} else throw new Error(`unknown mode ${mode}`);
'''


class BundleTests(unittest.TestCase):
    """`pi` runs the package's bundle, not the compiled output the backports patch: the oracle build has Pi's bundler make the bundle again from
    the patched compiled output. A fake bundler and a fake backports script stand for Pi's and the committed one."""

    PACKAGES = {"pi-agent-core": "agent", "pi-ai": "ai", "pi-coding-agent": "coding-agent", "pi-mcp": "mcp", "pi-tui": "tui"}

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, True)
        script = self.root / "backports.mjs"
        script.write_text(FAKE_BACKPORTS)
        patched = mock.patch.object(oracle, "BACKPORTS", script)
        patched.start()
        self.addCleanup(patched.stop)
        self.source = self.root / "src"
        (self.source / "scripts").mkdir(parents=True)
        (self.source / oracle.BUNDLER).write_text(FAKE_BUNDLER)
        (self.source / "package.json").write_text(json.dumps({"workspaces": ["packages/*"]}))
        for name, directory in self.PACKAGES.items():
            package = self.source / "packages" / directory
            (package / "dist").mkdir(parents=True)
            (package / "package.json").write_text(json.dumps({"name": f"@earendil-works/{name}", "version": "1.5.2", "bin": {"pi": "dist/bundle/cli.js"}}))
            (package / "dist" / "code.js").write_text(f"export const {directory.replace('-', '_')} = 'old';\n")
        (self.source / "packages" / "coding-agent" / "docs").mkdir()
        (self.source / "packages" / "coding-agent" / "docs" / "page.md").write_text("the old page\n")
        oracle.run_bundler(self.source)
        # The installed tree, as `npm ci` of the packs lays it out: the coding agent with the three other packages nested under it.
        self.modules = self.root / "node_modules"
        self.agent = self.modules / "@earendil-works" / "pi-coding-agent"
        shutil.copytree(self.source / "packages" / "coding-agent", self.agent)
        for name in ("pi-agent-core", "pi-ai", "pi-mcp", "pi-tui"):
            shutil.copytree(self.source / "packages" / self.PACKAGES[name], self.agent / "node_modules" / "@earendil-works" / name)
        self.chunk = self.agent / "dist" / "bundle" / "chunks" / "chunk.js"

    @staticmethod
    def files(tree: Path) -> dict[str, bytes]:
        return {path.relative_to(tree).as_posix(): path.read_bytes() for path in sorted(tree.rglob("*")) if path.is_file()}

    def test_the_bundle_pi_runs_is_rebuilt_from_the_patched_compiled_output_and_the_source_build_stays_as_built(self):
        as_built = self.files(self.source)
        oracle.apply_backports(self.modules)
        self.assertNotIn("new", self.chunk.read_text(), "the fixture's bundle carried the backports before it was rebuilt")
        digest = oracle.rebuild_bundle(self.source, self.modules)
        self.assertEqual(self.chunk.read_text(), "exportconstagent='new';exportconstai='new';exportconstcoding_agent='new';exportconstmcp='new';exportconsttui='new';")
        self.assertEqual(digest, oracle.tree_digest(self.agent / "dist" / "bundle"))
        self.assertEqual(self.files(self.source), as_built, "the source build must be left as built: the packs, the mirror and the Durable oracle read it")
        self.assertEqual(sorted(path.name for path in self.root.iterdir()), ["backports.mjs", "node_modules", "src"], "the rebuild left scratch beside the source build")
        self.assertEqual(oracle.bundle_problem(self.modules, {"backports": "b", "bundle_sha256": digest}), "")

    def test_a_bundler_that_does_not_reproduce_the_installed_bundle_stops_the_build(self):
        as_built = self.files(self.source)
        oracle.apply_backports(self.modules)
        self.chunk.write_text("the bundle of another build")
        with self.assertRaisesRegex(pinlib.PinError, "does not reproduce the installed bundle"):
            oracle.rebuild_bundle(self.source, self.modules)
        self.assertEqual(self.chunk.read_text(), "the bundle of another build", "the installed bundle was replaced by one that is not provably Pi's plus the backports")
        self.assertEqual(self.files(self.source), as_built)

    def test_a_backport_that_does_not_apply_to_the_source_build_stops_the_build_and_leaves_the_source_as_built(self):
        oracle.apply_backports(self.modules)
        (self.source / "packages" / "coding-agent" / "docs" / "page.md").write_text("another page\n")
        as_built = self.files(self.source)
        with self.assertRaisesRegex(pinlib.PinError, "do not apply to the source build"):
            oracle.rebuild_bundle(self.source, self.modules)
        self.assertEqual(self.files(self.source), as_built, "the packages patched before the failure were not put back")
        self.assertNotIn("new", self.chunk.read_text())

    def carries_a_dependency(self, source_version: str = "1.0.0") -> None:
        """The backports script carries `sdk` 1.0.0 -> 2.0.0, and the bundler bundles the copy the source build resolves."""
        dep = {"id": "c0ffee", "package": "pi-ai", "name": "sdk", "from": "1.0.0", "to": "2.0.0", "resolved": "https://registry.npmjs.org/sdk/-/sdk-2.0.0.tgz", "integrity": "sha512-x", "dependencies": {}}
        listing = f'}} else if (mode === "--dependencies") process.stdout.write({json.dumps(json.dumps([dep]))});\nelse throw new Error'
        (self.root / "backports.mjs").write_text("// BEGIN backport c0ffee\n// END backport c0ffee\n" + FAKE_BACKPORTS.replace("} else throw new Error", listing))
        (self.source / oracle.BUNDLER).write_text(FAKE_BUNDLER.replace('.join("");', '.join("") + readFileSync(join(root, "node_modules", "sdk", "index.js"), "utf8");'))
        for place, version, code in ((self.source / "node_modules" / "sdk", source_version, "sdk='one';"), (self.agent / "node_modules" / "sdk", "2.0.0", "sdk='two';")):
            place.mkdir(parents=True)
            (place / "package.json").write_text(json.dumps({"name": "sdk", "version": version}))
            (place / "index.js").write_text(code)
        oracle.run_bundler(self.source)
        shutil.rmtree(self.agent / "dist" / "bundle")
        shutil.copytree(self.source / "packages" / "coding-agent" / "dist" / "bundle", self.agent / "dist" / "bundle")

    def test_a_carried_dependency_is_bundled_from_the_installed_copy_and_the_source_build_keeps_its_own(self):
        self.carries_a_dependency()
        as_built = self.files(self.source)
        self.assertIn("sdk='one';", self.chunk.read_text())
        oracle.apply_backports(self.modules)
        digest = oracle.rebuild_bundle(self.source, self.modules)
        self.assertEqual(self.chunk.read_text(), "exportconstagent='new';exportconstai='new';exportconstcoding_agent='new';exportconstmcp='new';exportconsttui='new';sdk='two';")
        self.assertEqual(digest, oracle.tree_digest(self.agent / "dist" / "bundle"))
        self.assertEqual(self.files(self.source), as_built, "the source build must keep the dependency it was built with")
        self.assertEqual(sorted(path.name for path in self.root.iterdir()), ["backports.mjs", "node_modules", "src"], "the rebuild left scratch beside the source build")

    def test_a_source_build_that_does_not_resolve_the_version_a_carried_dependency_replaces_stops_the_build(self):
        self.carries_a_dependency(source_version="1.5.0")
        as_built = self.files(self.source)
        oracle.apply_backports(self.modules)
        with self.assertRaisesRegex(pinlib.PinError, r"does not resolve sdk to one copy of 1\.0\.0 .*found 1\.5\.0"):
            oracle.rebuild_bundle(self.source, self.modules)
        self.assertEqual(self.files(self.source), as_built)
        self.assertIn("sdk='one';", self.chunk.read_text(), "the installed bundle was replaced although the build stopped")

    def test_a_pi_whose_bin_is_compiled_output_has_no_bundle_to_rebuild(self):
        (self.agent / "package.json").write_text(json.dumps({"name": oracle.AGENT, "version": "1.5.2", "bin": {"pi": "dist/cli.js"}}))
        (self.source / oracle.BUNDLER).unlink()
        self.assertEqual(oracle.rebuild_bundle(self.source, self.modules), "")
        self.assertEqual(oracle.bundle_problem(self.modules, {"backports": "b"}), "")

    def test_an_installed_bundle_is_the_oracles_only_when_its_stamp_names_it(self):
        digest = oracle.tree_digest(self.agent / "dist" / "bundle")
        self.assertEqual(oracle.bundle_problem(self.modules, {}), "", "an oracle without backports runs the pin's bundle")
        self.assertIn("compiled output only", oracle.bundle_problem(self.modules, {"backports": "b"}))
        self.assertEqual(oracle.bundle_problem(self.modules, {"backports": "b", "bundle_sha256": digest}), "")
        self.assertIn("compiled output only", oracle.bundle_problem(self.modules, {"catalog": "c"}), "catalog data in the compiled output only is not the bundle's data")
        self.assertEqual(oracle.bundle_problem(self.modules, {"catalog": "c", "bundle_sha256": digest}), "")
        self.chunk.write_text("changed")
        self.assertIn(f"its build stamped {digest}", oracle.bundle_problem(self.modules, {"backports": "b", "bundle_sha256": digest}))

    def test_the_identity_of_an_oracle_names_the_recording_its_catalog_data_was_made_from(self):
        plain = oracle.identity("d", "c", "b")
        self.assertEqual(oracle.identity("d", "c", "b", ""), plain, "an oracle without catalog data keeps its identity")
        self.assertNotEqual(oracle.identity("d", "c", "b", "one"), plain)
        self.assertNotEqual(oracle.identity("d", "c", "b", "one"), oracle.identity("d", "c", "b", "two"), "another recording is another oracle")

    def test_the_digest_of_a_tree_is_that_of_its_files_in_path_order(self):
        tree = self.root / "tree"
        (tree / "chunks").mkdir(parents=True)
        (tree / "chunks" / "a.js").write_text("a")
        (tree / "chunks-b.js").write_text("b")
        lines = f"{hashlib.sha256(b'b').hexdigest()}  chunks-b.js\n{hashlib.sha256(b'a').hexdigest()}  chunks/a.js\n"
        self.assertEqual(oracle.tree_digest(tree), hashlib.sha256(lines.encode()).hexdigest())


class LedgerVersionTests(unittest.TestCase):
    def test_with_version_rewrites_only_the_top_level_field(self):
        text = '# pi_version = "0"\nupstream_version = "1.1.0"\n[[x]]\nupstream_version = "kept"\n'
        out = pin_tool.with_version(text, "upstream_version", "1.1.0+42a3497d")
        self.assertIn('upstream_version = "1.1.0+42a3497d"\n[[x]]', out)
        self.assertIn('upstream_version = "kept"', out)
        self.assertEqual(out.count("1.1.0+42a3497d"), 1)

    def test_with_version_fails_without_the_field(self):
        with self.assertRaises(pinlib.PinError):
            pin_tool.with_version("x = 1\n", "version", "2")


class LedgerInstallTests(unittest.TestCase):
    """`make pin-bump` to a pin with no archived ledgers runs `pin.py ledgers` under the new committed pin. The generated
    async-contracts ledger (a changed source file's row pending) must replace the earlier pin's, which `pin.py sync` only
    re-versioned; under a PI_UPSTREAM_REF override the active ledgers stay the committed pin's."""

    def source_tree_state(self):
        """The bytes of every file the run could write in the source tree: the archive of per-pin ledgers and the active ledgers."""
        state = {}
        for base in (pinlib.ROOT / pin_tool.ARCHIVE, *(pinlib.ROOT / rel for rel in pin_tool.PIN_LEDGERS)):
            for path in ([base] if base.is_file() else sorted(base.rglob("*"))):
                state[path] = path.read_bytes() if path.is_file() else None
        return state

    def run_ledgers(self, committed):
        """Run `cmd_ledgers` against a throwaway root, so the fake test pin's ledgers land in a temporary directory and never in the source tree."""
        before = self.source_tree_state()
        self.addCleanup(lambda: self.assertEqual(self.source_tree_state(), before, "the run wrote into the source tree"))
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        (root / ".upstream" / TAG.mirror_dir).mkdir(parents=True)
        for rel, field_name in pin_tool.VERSION_FIELDS.items():
            (root / rel).parent.mkdir(parents=True, exist_ok=True)
            (root / rel).write_text(f'{field_name} = "{COMMIT.version}"\nreviewed = "earlier pin"\n')

        class Result:
            stdout = f'version = "{COMMIT.version}"\nreviewed = "pending"\n'

        saved = (pin_tool.ROOT, pinlib.effective, pinlib.committed, pinlib.resolve, pin_tool.subprocess.run, pin_tool.carry_interface_ledgers)
        pin_tool.carry_interface_ledgers = lambda *args: None
        pin_tool.ROOT = root
        pinlib.effective = lambda: (COMMIT, committed is not COMMIT)
        pinlib.committed = lambda: committed
        pinlib.resolve = lambda ref, base=None: TAG
        pin_tool.subprocess.run = lambda *a, **k: Result()
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                pin_tool.cmd_ledgers(type("Args", (), {"previous": TAG.ref, "force": False})())
        finally:
            pin_tool.ROOT, pinlib.effective, pinlib.committed, pinlib.resolve, pin_tool.subprocess.run, pin_tool.carry_interface_ledgers = saved
        return root

    def test_a_bump_to_a_new_pin_makes_the_generated_ledgers_active(self):
        root = self.run_ledgers(COMMIT)
        self.assertIn('reviewed = "pending"', (root / "test/parity/async-contracts.toml").read_text())
        archived = root / pin_tool.ARCHIVE / ("v" + COMMIT.version) / "async-contracts.toml"
        self.assertEqual(archived.read_text(), (root / "test/parity/async-contracts.toml").read_text())

    def test_the_fake_test_pin_leaves_the_source_tree_unchanged(self):
        root = self.run_ledgers(COMMIT)
        fake = pin_tool.ARCHIVE + "/v" + COMMIT.version
        self.assertTrue((root / fake / "async-contracts.toml").is_file(), "the run did not write the fake pin's ledger under its temporary root")
        self.assertFalse((pinlib.ROOT / fake).exists(), f"{fake} was written into the source tree")

    def test_an_override_leaves_the_committed_pins_ledgers_active(self):
        root = self.run_ledgers(TAG)
        self.assertIn('reviewed = "earlier pin"', (root / "test/parity/async-contracts.toml").read_text())


class CarryTests(unittest.TestCase):
    """The reviewed interface ledgers of a new pin are carried from the earlier pin's by one command."""

    def setUp(self):
        self.root = root = Path(tempfile.mkdtemp())
        interfaces = root / pin_tool.INTERFACES
        interfaces.mkdir(parents=True)
        self.old, self.new = TAG.version, COMMIT.version
        (root / "test/parity/upstream-sync").mkdir(parents=True)
        (root / ".upstream" / TAG.mirror_dir).mkdir(parents=True)
        (interfaces / f"upstream-tests-v{self.new}.json").write_text(json.dumps({"files": [{"path": "a.test.ts", "sha256": "sha256:new"}, {"path": "added.test.ts", "sha256": "sha256:add"}]}))
        (interfaces / f"test-porting-policy-v{self.old}.json").write_text(json.dumps({"upstreamVersion": self.old, "baselineCommit": "c", "entries": [
            {"path": "a.test.ts", "upstreamTestHash": "sha256:old", "tags": ["hot-path"]}, {"path": "gone.test.ts", "upstreamTestHash": "sha256:g", "tags": []}]}))
        (interfaces / f"behavior-owner-overrides-v{self.old}.json").write_text(json.dumps({"upstreamVersion": self.old, "owners": []}))
        self.calls = []

    def carry(self, committed, force=False):
        calls = self.calls
        reviewed = self.root / "test/parity/interface-closure/autobind/reviewed-types.json"
        reviewed.parent.mkdir(parents=True, exist_ok=True)
        reviewed.write_text("original")

        def fake_run(command, **kwargs):
            text = " ".join(command)
            calls.append(text)
            if "-output-input-mapping" in text:
                (self.root / text.split("-output-input-mapping ")[1].split()[0].rstrip("'")).write_text("{}")
            if command[:2] == ["make", "--no-print-directory"]:
                reviewed.write_text("pruned")

            class Result:
                stdout = "{}"
            return Result()

        saved = (pin_tool.ROOT, pinlib.committed, pin_tool.subprocess.run)
        pin_tool.ROOT, pinlib.committed, pin_tool.subprocess.run = self.root, (lambda: committed), fake_run
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                pin_tool.carry_interface_ledgers(COMMIT, TAG, force)
        finally:
            pin_tool.ROOT, pinlib.committed, pin_tool.subprocess.run = saved
        return reviewed

    def test_the_policy_follows_the_new_inventory_and_the_mapping_is_derived_with_two_ledger_updates(self):
        self.carry(COMMIT)
        policy = json.loads((self.root / pin_tool.INTERFACES / f"test-porting-policy-v{self.new}.json").read_text())
        self.assertEqual(policy["upstreamVersion"], self.new)
        self.assertEqual(policy["entries"], [{"path": "a.test.ts", "upstreamTestHash": "sha256:new", "tags": ["hot-path"]}])
        self.assertEqual(sum(1 for c in self.calls if c.endswith("ledger-update")), 2)
        self.assertTrue(any("-generate-pending" in c and "interfaceinventory" in c and f"-previous-mapping test/parity/interfaces/mapping-v{self.old}.json" in c for c in self.calls))
        # The pending mapping holds a row for every command-line flag of the new pin's cli inventory (the 42a flip lost 65 cli rows without it).
        self.assertTrue(any("-generate-pending" in c and f"-observable test/parity/interfaces/cli-v{self.new}.json" in c for c in self.calls))
        overrides = json.loads((self.root / pin_tool.INTERFACES / f"behavior-owner-overrides-v{self.new}.json").read_text())
        self.assertEqual(overrides["upstreamVersion"], self.new)

    def test_an_override_run_restores_the_reviewed_types_the_update_pruned(self):
        self.assertEqual(self.carry(TAG).read_text(), "original")

    def gaps(self, committed):
        calls = self.calls
        reviewed = self.root / "test/parity/interface-closure/autobind/reviewed-types.json"
        reviewed.parent.mkdir(parents=True, exist_ok=True)
        reviewed.write_text("stale")

        def fake_run(command, **kwargs):
            calls.append(command[-1])
            if command[-1] == "ledger-update":
                reviewed.write_text("pruned")
            elif reviewed.read_text() != "pruned":
                raise subprocess.CalledProcessError(2, command)  # ledger-check stops on a stale exception

        saved = (pin_tool.ROOT, pinlib.effective, pinlib.committed, pin_tool.subprocess.run)
        pin_tool.ROOT, pinlib.effective, pinlib.committed, pin_tool.subprocess.run = self.root, (lambda: (COMMIT, committed != COMMIT)), (lambda: committed), fake_run
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                pin_tool.cmd_gaps(argparse.Namespace(updates=1))
        finally:
            pin_tool.ROOT, pinlib.effective, pinlib.committed, pin_tool.subprocess.run = saved
        return reviewed

    def test_the_gaps_step_binds_the_ledgers_before_it_checks_them(self):
        self.gaps(COMMIT)
        self.assertEqual(self.calls, ["ledger-update", "ledger-check"])

    def test_the_gaps_step_of_an_override_run_leaves_the_committed_pins_reviewed_types(self):
        self.assertEqual(self.gaps(TAG).read_text(), "stale")

    def test_the_committed_pin_keeps_the_prune(self):
        self.assertEqual(self.carry(COMMIT).read_text(), "pruned")

    def test_an_existing_ledger_is_kept_unless_forced(self):
        self.carry(COMMIT)
        first = len(self.calls)
        self.carry(COMMIT)
        self.assertEqual(len(self.calls), first, self.calls[first:])
        self.carry(COMMIT, force=True)
        self.assertGreater(len(self.calls), first)


class OverrideTreeCommandTests(unittest.TestCase):
    """`pin.py override-tree` is read by make as $(...): its standard output is the tree's path and nothing else, whatever the build prints."""

    def run_command(self, ref: str) -> str:
        def build(pin):
            print("npm said something")
            os.write(1, b"a child process said something\n")
            return Path("/store") / pin.commit / "durable-x"

        import vendor

        with tempfile.TemporaryFile() as captured:
            saved = os.dup(1)
            os.dup2(captured.fileno(), 1)
            try:
                with mock.patch.dict(os.environ, {pinlib.OVERRIDE_ENV: ref}), mock.patch.object(pinlib, "effective", lambda: (COMMIT, False)), mock.patch.object(vendor, "override_tree", build), contextlib.redirect_stderr(io.StringIO()):
                    pin_tool.cmd_override_tree(None)
            finally:
                sys.stdout.flush()
                os.dup2(saved, 1)
                os.close(saved)
            captured.seek(0)
            return captured.read().decode()

    def test_standard_output_is_the_path_alone(self):
        with tempfile.TemporaryFile() as errors:
            saved = os.dup(2)
            os.dup2(errors.fileno(), 2)
            try:
                printed = self.run_command(COMMIT.commit)
            finally:
                os.dup2(saved, 2)
                os.close(saved)
            errors.seek(0)
            self.assertIn(b"a child process said something", errors.read())
        self.assertEqual(printed, str(Path("/store") / COMMIT.commit / "durable-x") + "\n")

    def test_a_run_that_names_no_commit_has_no_override_tree(self):
        with self.assertRaisesRegex(pinlib.PinError, "PI_UPSTREAM_REF"):
            self.run_command("")


class MirrorLinkTests(unittest.TestCase):
    """link-mirror gives the pin's read-only mirror the dependencies of the pinned source build, so a Node oracle script
    that imports the mirrored source (chord/src/schema.ts imports typebox) resolves them."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.saved = (oracle.ROOT, oracle.SDK_TS)
        oracle.ROOT, oracle.SDK_TS = self.root, self.root / "extensions" / "sdk-ts"
        oracle.SDK_TS.mkdir(parents=True)
        self.mirror = self.root / ".upstream" / TAG.mirror_dir
        self.mirror.mkdir(parents=True)
        self.mirror.chmod(0o555)

    def tearDown(self):
        self.mirror.chmod(0o755)
        oracle.ROOT, oracle.SDK_TS = self.saved

    def install(self, stamp: dict | None) -> Path:
        build = self.root / "store" / "build-x"
        (build / "node_modules").mkdir(parents=True)
        (build / "src" / "node_modules" / "typebox").mkdir(parents=True)
        (build / "src" / "package.json").write_text("{}")
        if stamp is not None:
            (build / oracle.STAMP).write_text(json.dumps(stamp))
        (oracle.SDK_TS / "node_modules").symlink_to(build / "node_modules")
        return build / "src" / "node_modules"

    def test_the_mirror_links_the_source_builds_node_modules_and_stays_read_only(self):
        modules = self.install({"commit": TAG.commit, "source": "build"})
        self.assertEqual(oracle.link_mirror(TAG).resolve(), modules.resolve())
        link = self.mirror / "node_modules"
        self.assertTrue(link.is_symlink())
        self.assertEqual(Path(os.path.realpath(link)), modules.resolve())
        self.assertTrue((link / "typebox").is_dir())
        self.assertEqual(self.mirror.stat().st_mode & 0o777, 0o555)
        self.assertEqual(oracle.link_mirror(TAG).resolve(), modules.resolve())  # idempotent

    def test_every_workspace_link_that_is_not_a_link_to_its_package_is_replaced_and_logged(self):
        """Pi's telemetry leap added packages/telemetry, which chord imports; a build restored from the CI cache resolved
        typebox (a real directory) but not @earendil-works/pi-telemetry. The link a restore leaves can be missing, stale
        (to a path that is gone or to another package that has a package.json), a copy of the package, or a plain file."""
        modules = self.install({"commit": TAG.commit, "source": "build"})
        source = modules.parent
        (source / "package.json").write_text(json.dumps({"workspaces": ["packages/*", "apps/*"]}))
        packages = {"intact": "@earendil-works/chord", "missing": "@earendil-works/pi-telemetry", "gone": "@earendil-works/pi-ai", "elsewhere": "@earendil-works/pi-tui", "copy": "@earendil-works/pi-mcp", "file": "@earendil-works/pi-env", "app": "pi-web"}
        for key, name in packages.items():
            directory = source / ("apps" if key == "app" else "packages") / key
            directory.mkdir(parents=True)
            (directory / "package.json").write_text(json.dumps({"name": name}))
        scope = modules / "@earendil-works"
        scope.mkdir()
        (scope / "chord").symlink_to("../../packages/intact", target_is_directory=True)
        (scope / "pi-ai").symlink_to("../../packages/removed", target_is_directory=True)
        (scope / "pi-tui").symlink_to("../../packages/intact", target_is_directory=True)  # resolves, to the wrong package
        shutil.copytree(source / "packages" / "copy", scope / "pi-mcp")
        (scope / "pi-env").write_text("!<symlink>../../packages/file")
        (modules / "pi-web").symlink_to("../apps/app", target_is_directory=True)
        intact = os.readlink(scope / "chord")
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            oracle.link_mirror(TAG)
        for key, name in packages.items():
            directory = source / ("apps" if key == "app" else "packages") / key
            link = self.mirror / "node_modules" / name
            self.assertEqual(json.loads((link / "package.json").read_text())["name"], name)
            self.assertEqual(Path(os.path.realpath(modules / name)), directory.resolve(), name)
            self.assertTrue((modules / name).is_symlink(), name)
        self.assertEqual(os.readlink(scope / "chord"), intact)  # a correct link is left as it is
        log = stderr.getvalue()
        self.assertIn("workspace @earendil-works/chord: ok", log)
        for name, state in (("pi-telemetry", "missing"), ("pi-ai", "stale"), ("pi-tui", "stale"), ("pi-mcp", "copy"), ("pi-env", "file")):
            self.assertRegex(log, rf"workspace @earendil-works/{name}: {state}.*relinked, now linked")
        self.assertTrue((self.mirror / "node_modules" / "typebox").is_dir())

    def test_a_published_install_or_another_commits_build_links_nothing(self):
        for stamp in (None, {"commit": "c" * 40, "source": "build"}):
            with self.subTest(stamp=stamp):
                self.install(stamp)
                with contextlib.redirect_stderr(io.StringIO()):
                    self.assertIsNone(oracle.link_mirror(TAG))
                self.assertFalse((self.mirror / "node_modules").exists())
                (oracle.SDK_TS / "node_modules").unlink()
                shutil.rmtree(self.root / "store")


GENAI = {"id": "f00d", "package": "pi-ai", "name": "@google/genai", "from": "2.21.0", "to": "2.32.0",
         "resolved": "https://registry.npmjs.org/@google/genai/-/genai-2.32.0.tgz", "integrity": "sha512-new", "dependencies": {"ws": "^8.18.0"}}


class CarriedDependencyTests(unittest.TestCase):
    """A Pi commit the release line carries can move a dependency a Pi package pins exactly: the oracle installs the frozen closure with
    that one lock entry replaced by Pi's own at the carried commit."""

    AI = oracle.LOCK_KEY + "/node_modules/@earendil-works/pi-ai"
    SDK = oracle.LOCK_KEY + "/node_modules/@google/genai"

    def lock(self, version: str = "2.21.0") -> dict:
        return {"name": "pi-oracle", "version": "0.0.0", "lockfileVersion": 3, "packages": {
            "": {"devDependencies": {oracle.AGENT: "1.5.2"}},
            oracle.LOCK_KEY: {"version": "1.5.2", "dependencies": {"@earendil-works/pi-ai": "1.5.2"}},
            self.AI: {"version": "1.5.2", "resolved": "pig-pack:@earendil-works/pi-ai", "integrity": "sha512-pack", "dependencies": {"@google/genai": version, "ws": "8.20.0"}},
            self.SDK: {"version": version, "resolved": f"https://registry.npmjs.org/@google/genai/-/genai-{version}.tgz", "integrity": "sha512-old", "dependencies": {"ws": "^8.18.0"}, "hasInstallScript": True},
            oracle.LOCK_KEY + "/node_modules/ws": {"version": "8.20.0", "resolved": "https://registry.npmjs.org/ws/-/ws-8.20.0.tgz", "integrity": "sha512-ws"},
        }}

    def test_the_closure_installs_the_carried_version_with_pis_integrity_and_the_frozen_lock_is_left_alone(self):
        frozen = self.lock()
        before = json.dumps(frozen, sort_keys=True)
        got = oracle.carried_closure(frozen, [GENAI])
        self.assertEqual(json.dumps(frozen, sort_keys=True), before, "the frozen closure is the single source for everything else and is never rewritten")
        self.assertEqual({key for key in frozen["packages"] if frozen["packages"][key] != got["packages"][key]}, {self.AI, self.SDK})
        self.assertEqual(got["packages"][self.SDK], {**frozen["packages"][self.SDK], "version": "2.32.0", "resolved": GENAI["resolved"], "integrity": "sha512-new"})
        self.assertEqual(got["packages"][self.AI]["dependencies"], {"@google/genai": "2.32.0", "ws": "8.20.0"})
        self.assertNotEqual(oracle.closure_digest(got), oracle.closure_digest(frozen), "the closure digest must cover the carried entry")
        self.assertIs(oracle.carried_closure(frozen, []), frozen)

    def test_a_pin_that_already_installs_the_carried_version_stops_the_build_and_says_to_delete_the_entry(self):
        with self.assertRaisesRegex(pinlib.PinError, r"Pi f00d .*installs @google/genai 2\.32\.0 at .*, not 2\.21\.0\. .*delete the entry"):
            oracle.carried_closure(self.lock("2.32.0"), [GENAI])

    def test_an_entry_the_closure_cannot_hold_as_one_replaced_lock_entry_is_refused(self):
        cases = {
            "its own dependencies changed": ({**GENAI, "dependencies": {"ws": "^9.0.0"}}, lambda lock: None),
            "installs no @google/genai": (GENAI, lambda lock: lock["packages"].pop(self.SDK)),
            "replaces the exact pin 2.21.0 of @earendil-works/pi-ai and nothing else": (GENAI, lambda lock: lock["packages"][oracle.LOCK_KEY]["dependencies"].update({"@google/genai": "^2.0.0"})),
            r"needs @google/genai \^2\.21\.0 \(dependencies\)": (GENAI, lambda lock: lock["packages"][self.AI]["dependencies"].update({"@google/genai": "^2.21.0"})),
            "no @earendil-works/pi-ai of the frozen closure pins": (GENAI, lambda lock: lock["packages"][self.AI]["dependencies"].pop("@google/genai")),
            "registry archive of that exact version": ({**GENAI, "resolved": "https://example.com/genai-2.32.0.tgz"}, lambda lock: None),
            "its sha512 integrity": ({**GENAI, "integrity": "sha1-x"}, lambda lock: None),
        }
        for message, (dep, change) in cases.items():
            with self.subTest(message=message):
                lock = self.lock()
                change(lock)
                with self.assertRaisesRegex(pinlib.PinError, message):
                    oracle.carried_closure(lock, [dep])

    def test_a_peer_range_that_admits_both_versions_stays_and_one_that_does_not_stops_the_build(self):
        for peer, ok in ((">=2 <3", True), (">=2.21.0 <3", True), (">=2.30.0 <3", False), (">=2.21.0 <2.32.0", False), ("^2.21.0", False), (">=2.0.0 || ^3", False)):
            with self.subTest(peer=peer):
                lock = self.lock()
                other = oracle.LOCK_KEY + "/node_modules/@earendil-works/pi-ai/node_modules/openai"
                lock["packages"][other] = {"version": "7.19.0", "peerDependencies": {"@google/genai": peer}}
                if ok:
                    self.assertEqual(oracle.carried_closure(lock, [GENAI])["packages"][other]["peerDependencies"], {"@google/genai": peer})
                else:
                    with self.assertRaisesRegex(pinlib.PinError, r"\(peerDependencies\)"):
                        oracle.carried_closure(lock, [GENAI])

    def test_the_installed_tree_must_hold_one_copy_of_the_carried_version_with_the_dependencies_pis_lock_records(self):
        with tempfile.TemporaryDirectory() as root:
            modules = make_tree(Path(root) / "node_modules", {"node_modules/@earendil-works/pi-coding-agent": "@earendil-works/pi-coding-agent@1.5.2", self.SDK: "@google/genai@2.21.0"})
            manifest = modules.parent / self.SDK / "package.json"
            self.assertRegex(oracle.carried_problems(modules, [GENAI])[0], r"0 copies of @google/genai 2\.32\.0")
            manifest.write_text(json.dumps({"name": "@google/genai", "version": "2.32.0", "dependencies": {"ws": "^9.0.0"}}))
            self.assertRegex(oracle.carried_problems(modules, [GENAI])[0], r"declares the dependencies \{'ws': '\^9\.0\.0'\}")
            manifest.write_text(json.dumps({"name": "@google/genai", "version": "2.32.0", "dependencies": {"ws": "^8.18.0"}}))
            self.assertEqual(oracle.carried_problems(modules, [GENAI]), [])
            self.assertEqual(oracle.carried_place(modules, GENAI), modules.parent / self.SDK)

    def test_the_committed_entries_replace_what_the_committed_pins_frozen_closure_installs(self):
        """`make pin-check` runs this: a re-pin that already holds a carried commit, or an entry that names another archive than a
        sha512-pinned registry archive, fails here naming the entry, before any oracle is built."""
        pin = pinlib.committed()
        carried = oracle.carried_dependencies()
        got = oracle.carried_closure(oracle.read_frozen_lock(pin), carried)
        for dep in carried:
            self.assertEqual({item["version"] for key, item in got["packages"].items() if key.endswith("node_modules/" + dep["name"])}, {dep["to"]})
            self.assertRegex(dep["integrity"], r"^sha512-[A-Za-z0-9+/]{86}==$")
            self.assertIn(dep["id"], oracle.backport_ids())
        self.assertEqual(oracle.oracle_closure_digest(pin), oracle.closure_digest(got))


class MirroredSdkTests(unittest.TestCase):
    def test_the_sdk_version_the_go_google_encoder_mirrors_is_the_one_the_oracle_installs(self):
        """ai/google_wire.go writes Google request bodies in the member order of @google/genai's converters and names the version it
        mirrors. PiG has no SDK dependency, so nothing else says that a re-pin or a carried Pi commit moved the SDK under the port."""
        (named,) = re.findall(r'^const geminiMirroredSDKVersion = "([^"]+)"$', (pinlib.ROOT / "ai" / "google_wire.go").read_text(encoding="utf-8"), re.M)
        closure = oracle.oracle_closure(pinlib.committed())
        installed = {item["version"] for key, item in closure["packages"].items() if key.endswith("node_modules/@google/genai")}
        self.assertEqual(installed, {named}, "the oracle installs another @google/genai than ai/google_wire.go mirrors: port the converter changes between the two versions (see the comment at the head of ai/google_wire.go) and update geminiMirroredSDKVersion")


if __name__ == "__main__":
    unittest.main()
