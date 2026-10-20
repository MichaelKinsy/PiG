# SPDX-License-Identifier: MIT
"""Tests of the pin model and its derivations. They use no network: refs that
need resolution are exercised through the cached and committed paths."""
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
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402
import pin as pin_tool  # noqa: E402
import oracle  # noqa: E402

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
    def test_committed_derivations_match_the_pin(self):
        stale = [rel for rel, text in pin_tool.derived(pinlib.committed()).items() if (pinlib.ROOT / rel).read_text(encoding="utf-8") != text]
        self.assertEqual(stale, [])

    def test_derivation_changes_every_copy_for_a_commit_pin(self):
        base = pin_tool.derived(pinlib.committed())
        moved = pin_tool.derived(COMMIT)
        changed = {rel for rel in base if base[rel] != moved[rel]}
        for rel in (
            "internal/coding/pigversion/pigversion.go",
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
        # The README's `pig --version` example shows the composite version, whose Pi part is the pin's display version.
        self.assertRegex(moved["README.md"], r"for example `\d+\.\d+\.\d+\+1\.1\.0\+bbbbbbbb`")
        self.assertTrue("ARG PI_COMMIT=" + "b" * 40 in moved["automation/images/ci-parity/Dockerfile"])

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
        self.builds = 0
        self.build_delay = 0.0
        self.probe = lambda: None
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
        (agent / "package.json").write_text(json.dumps({"name": oracle.AGENT, "version": "1.1.0", "bin": {"pi": "cli.js"}, "oracle": self.label}))
        (agent / "cli.js").write_text("")

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

    def test_ensure_durable_packs_the_published_build_not_the_entry(self):
        # ensure() builds into entry/build-<random>/src and publishes it through current.ref; it never creates entry/src.
        # ensure_durable must pack that published build's source, as freeze_closure resolves it.
        built = oracle.ensure(COMMIT).parent
        (built / "src" / "packages" / "durable" / "dist").mkdir(parents=True)
        self.assertFalse((oracle.entry(COMMIT) / "src").exists())
        packed_from = []

        def pack(source, _destination, *_roots):
            packed_from.append(source)
            raise pinlib.PinError("packed")

        with mock.patch.object(oracle, "pack", pack), mock.patch.object(oracle, "read_frozen_lock", lambda *_a: None):
            with self.assertRaisesRegex(pinlib.PinError, "packed"):
                oracle.ensure_durable(COMMIT)
        self.assertEqual(packed_from, [built / "src"])



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

    def test_a_commit_pin_freezes_a_durable_closure_whose_top_level_is_what_durable_interop_imports(self):
        pin = pinlib.committed()
        if pin.kind == "tag":
            self.skipTest("a release installs the published pi-durable (npm-locked.py durable/interop)")
        lock = oracle.read_frozen_lock(pin, oracle.DURABLE)
        self.assertIsNotNone(lock, f"no frozen durable closure for {pin.commit}: durable/interop would run the published pi-durable, not a build of the pin")
        top = lock["packages"][""]["devDependencies"]
        self.assertLessEqual(set(oracle.INTEROP), set(top))
        self.assertIn("typebox", top, "pi-durable's source imports typebox: the Go tests that import it resolve it from durable/interop/node_modules")
        for name in oracle.INTEROP:
            item = lock["packages"]["node_modules/" + name]
            self.assertTrue(item["resolved"].startswith(oracle.PACK_PREFIX), f"{name} must come from the pin's pack: {item['resolved']}")
        imported = json.loads((oracle.ROOT / "durable/interop/package.json").read_text())["dependencies"]
        photon = "@silvia-odwyer/photon"  # the image oracle's processor: durable/interop imports it beside the three packs
        self.assertIn(photon, top)
        self.assertEqual(sorted(imported), sorted([*oracle.INTEROP, photon]))

    def test_check_durable_rejects_an_interop_tree_that_is_not_the_pins_durable_oracle(self):
        pin = pinlib.committed()
        if pin.kind == "tag":
            self.skipTest("a release installs the published pi-durable")
        root = Path(tempfile.mkdtemp())
        (root / "durable" / "interop").mkdir(parents=True)
        stale = root / "stale"
        (stale / "@earendil-works" / "pi-durable").mkdir(parents=True)
        (stale / "@earendil-works" / "pi-durable" / "package.json").write_text(json.dumps({"name": oracle.DURABLE, "version": pin.package_version}))
        (root / "durable" / "interop" / "node_modules").symlink_to(stale)
        saved = oracle.ROOT
        oracle.ROOT = root
        try:
            with self.assertRaisesRegex(pinlib.PinError, "not the durable oracle of commit"):
                oracle.check_durable(pin)
        finally:
            oracle.ROOT = saved

    def test_the_durable_closure_has_its_own_frozen_file(self):
        pin = pinlib.committed()
        self.assertNotEqual(oracle.frozen_lock_path(pin), oracle.frozen_lock_path(pin, oracle.DURABLE))

    def test_the_workspace_closure_of_durable_is_its_own_root(self):
        source = Path(tempfile.mkdtemp())
        for name, ident, deps in (("durable", oracle.DURABLE, ["@earendil-works/pi-ai"]), ("ai", "@earendil-works/pi-ai", []), ("agent", oracle.AGENT, [oracle.DURABLE])):
            pkg = source / "packages" / name
            pkg.mkdir(parents=True)
            (pkg / "package.json").write_text(json.dumps({"name": ident, "dependencies": {d: "1" for d in deps}}))
        self.assertEqual(oracle.closure_workspaces(source, oracle.DURABLE), ["@earendil-works/pi-ai", oracle.DURABLE])

    def test_the_committed_pins_frozen_closure_matches_the_pin(self):
        pin = pinlib.committed()
        lock = oracle.read_frozen_lock(pin)
        self.assertIsNotNone(lock, f"no frozen closure for {pin.commit}")
        self.assertEqual(lock["packages"][""]["devDependencies"], {oracle.AGENT: pin.package_version})
        self.assertEqual(lock["packages"][oracle.LOCK_KEY]["version"], pin.package_version)


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

    def test_the_committed_pin_keeps_the_prune(self):
        self.assertEqual(self.carry(COMMIT).read_text(), "pruned")

    def test_an_existing_ledger_is_kept_unless_forced(self):
        self.carry(COMMIT)
        first = len(self.calls)
        self.carry(COMMIT)
        self.assertEqual(len(self.calls), first, self.calls[first:])
        self.carry(COMMIT, force=True)
        self.assertGreater(len(self.calls), first)


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


if __name__ == "__main__":
    unittest.main()
