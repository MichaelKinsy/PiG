# SPDX-License-Identifier: MIT
"""The gate on the vendored Pi packages (vendor.py check): each way a consumer or a pack can leave the pin is red."""
import copy
import hashlib
import io
import json
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402
import vendor  # noqa: E402
from vendor import PACKS, integrity, rel_spec  # noqa: E402

COMMIT = "ab" * 20
PIN = pinlib.Pin("v1.2.3", COMMIT, "1.2.3", "1.2.3")
NAMES = ["@earendil-works/chord", "@earendil-works/pi-ai", vendor.DURABLE, "@earendil-works/pi-codemode", "@earendil-works/pi-schema", "@earendil-works/pi-telemetry"]


def tgz(files: dict[str, bytes]) -> bytes:
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        for name, data in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


def write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


class Fixture(unittest.TestCase):
    """A tree whose vendored packs, consumers and telemetry copy are all the pin's."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        packs = {}
        for name in NAMES:
            files = {"package/package.json": json.dumps({"name": name, "version": "1.2.3", "license": "MIT"}).encode()}
            if name.endswith("pi-telemetry"):
                files["package/dist/index.js"] = b"export {}\n"
                files["package/dist/testing/index.js"] = b"testing\n"
            data = tgz(files)
            file = vendor.pack_file(name, "1.2.3")
            (self.root / PACKS).mkdir(parents=True, exist_ok=True)
            (self.root / PACKS / file).write_bytes(data)
            packs[file] = {"name": name, "version": "1.2.3", "license": "MIT", "bytes": len(data), "integrity": integrity(data)}
        licence = b"MIT License\n"
        (self.root / PACKS / vendor.LICENSE).write_bytes(licence)
        self.manifest = {"commit": COMMIT, "package_version": "1.2.3", "license": {"sha256": hashlib.sha256(licence).hexdigest()}, "packs": packs}
        self.write_manifest()
        self.by_name = {meta["name"]: file for file, meta in packs.items()}
        for consumer, direct in vendor.CONSUMERS.items():
            package = {"name": "x", "dependencies": {n: rel_spec(consumer, self.by_name[n]) for n in direct}, "overrides": {n: rel_spec(consumer, f) for n, f in self.by_name.items()}}
            write(self.root / consumer / "package.json", json.dumps(package))
            lock = {"packages": {"": {"name": "x"}, "node_modules/typebox": {"version": "1", "resolved": "https://registry.npmjs.org/typebox/-/typebox-1.tgz", "integrity": "sha512-x"}}}
            for name, file in self.by_name.items():
                lock["packages"]["node_modules/" + name] = {"version": "1.2.3", "resolved": rel_spec(consumer, file), "integrity": packs[file]["integrity"]}
            write(self.root / consumer / "package-lock.json", json.dumps(lock))
        for path in ("package/package.json", "package/dist/index.js"):
            source = vendor.members((self.root / PACKS / self.by_name["@earendil-works/pi-telemetry"]).read_bytes())[path][1]
            copy_path = self.root / next(iter(vendor.COPIES)) / path[len("package/"):]
            copy_path.parent.mkdir(parents=True, exist_ok=True)
            copy_path.write_bytes(source)

    def write_manifest(self):
        write(self.root / PACKS / vendor.MANIFEST, json.dumps(self.manifest))

    def problems(self):
        return vendor.check(PIN, self.root)

    def edit_json(self, rel: str, edit) -> None:
        path = self.root / rel
        data = json.loads(path.read_text())
        edit(data)
        path.write_text(json.dumps(data))


class GateTests(Fixture):
    def test_a_tree_at_the_pin_is_green(self):
        self.assertEqual(self.problems(), [])

    def test_a_changed_pack_byte_is_red(self):
        path = self.root / PACKS / self.by_name[vendor.DURABLE]
        path.write_bytes(path.read_bytes() + b"\0")
        self.assertTrue(any("differs from the manifest" in p and vendor.DURABLE.split("/")[1] in p for p in self.problems()), self.problems())

    def test_a_manifest_of_another_commit_is_red(self):
        self.manifest["commit"] = "cd" * 20
        self.write_manifest()
        self.assertTrue(any("is for Pi commit" in p for p in self.problems()), self.problems())

    def test_a_lock_entry_from_the_registry_is_red(self):
        def edit(lock):
            lock["packages"]["node_modules/@earendil-works/pi-durable"]["resolved"] = "https://registry.npmjs.org/@earendil-works/pi-durable/-/pi-durable-1.2.3.tgz"
        self.edit_json("durable/interop/package-lock.json", edit)
        self.assertTrue(any("durable/interop/package-lock.json" in p and "registry.npmjs.org" in p for p in self.problems()), self.problems())

    def test_a_lock_entry_with_another_integrity_is_red(self):
        self.edit_json("durable/core/host/cf/package-lock.json", lambda lock: lock["packages"]["node_modules/@earendil-works/chord"].update(integrity="sha512-other"))
        self.assertTrue(any("durable/core/host/cf/package-lock.json" in p and "chord" in p for p in self.problems()), self.problems())

    def test_a_package_json_naming_a_pi_package_by_version_is_red(self):
        self.edit_json("bench/k8s/image/pi-durable/package.json", lambda package: package["dependencies"].update({"@earendil-works/pi-ai": "1.2.3"}))
        self.assertTrue(any("bench/k8s/image/pi-durable/package.json" in p and "'1.2.3'" in p for p in self.problems()), self.problems())

    def test_an_override_by_version_is_red(self):
        self.edit_json("durable/interop/package.json", lambda package: package["overrides"].update({"@earendil-works/pi-schema": "1.2.3"}))
        self.assertTrue(any("overrides @earendil-works/pi-schema" in p for p in self.problems()), self.problems())

    def test_a_consumer_added_later_is_red_wherever_it_sits(self):
        write(self.root / "some" / "new" / "project" / "package.json", json.dumps({"devDependencies": {"@earendil-works/pi-durable": "1.5.2"}}))
        self.assertTrue(any(p.startswith("some/new/project/package.json") and "neither a vendored consumer" in p for p in self.problems()), self.problems())

    def test_an_exempt_directory_may_name_the_published_release(self):
        write(self.root / "extensions" / "sdk-ts" / "package.json", json.dumps({"peerDependencies": {"@earendil-works/pi-coding-agent": "1.5.2"}}))
        write(self.root / "node_modules" / "x" / "package.json", json.dumps({"dependencies": {"@earendil-works/pi-ai": "1.0.0"}}))
        self.assertEqual(self.problems(), [])

    def test_a_pack_missing_from_the_manifest_is_red(self):
        (self.root / PACKS / "earendil-works-extra-1.2.3.tgz").write_bytes(b"x")
        self.assertTrue(any("is not in the manifest" in p for p in self.problems()), self.problems())

    def test_a_telemetry_copy_that_differs_from_its_pack_is_red(self):
        (self.root / next(iter(vendor.COPIES)) / "dist" / "index.js").write_bytes(b"changed")
        self.assertTrue(any("differs from the vendored @earendil-works/pi-telemetry pack" in p for p in self.problems()), self.problems())

    def test_a_licence_that_differs_from_the_manifest_is_red(self):
        (self.root / PACKS / vendor.LICENSE).write_bytes(b"other")
        self.assertTrue(any("LICENSE" in p for p in self.problems()), self.problems())


class RebuildTests(Fixture):
    def rebuild(self, packs: dict[str, bytes]):
        fresh = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, fresh, ignore_errors=True)
        for file, data in packs.items():
            (fresh / file).write_bytes(data)
        original = vendor.pack_pinned
        vendor.pack_pinned = lambda pin, destination: {f: fresh / f for f in packs}
        try:
            return vendor.check_rebuild(PIN, self.root)
        finally:
            vendor.pack_pinned = original

    def test_a_pack_equal_to_the_rebuild_is_green(self):
        self.assertEqual(self.rebuild({f: (self.root / PACKS / f).read_bytes() for f in self.manifest["packs"]}), [])

    def test_a_pack_that_differs_from_the_rebuild_lists_the_members(self):
        packs = {f: (self.root / PACKS / f).read_bytes() for f in self.manifest["packs"]}
        file = self.by_name[vendor.DURABLE]
        packs[file] = tgz({"package/package.json": b'{"name": "x", "version": "9"}'})
        problems = self.rebuild(packs)
        self.assertEqual(len(problems), 1)
        self.assertIn("members package/package.json", problems[0])


class InstalledTests(Fixture):
    """vendor.check_installed: a consumer's node_modules is the pin's only as a real directory installed from the vendored packs. The carried
    oracle run once linked durable/interop/node_modules to another Pi commit's tree, and the gates after it read that Pi."""

    def install(self) -> Path:
        modules = self.root / vendor.INTEROP / "node_modules"
        packages = {}
        for file, meta in self.manifest["packs"].items():
            for member, (_mode, data) in vendor.members((self.root / PACKS / file).read_bytes()).items():
                target = modules / meta["name"] / member[len("package/"):]
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
            packages["node_modules/" + meta["name"]] = {"version": meta["version"], "resolved": rel_spec(vendor.INTEROP, file), "integrity": meta["integrity"]}
        self.hidden = modules / ".package-lock.json"
        write(self.hidden, json.dumps({"packages": packages}))
        return modules

    def problems(self) -> str:
        return "\n".join(vendor.check_installed(vendor.INTEROP, self.root))

    def test_an_install_of_the_vendored_packs_is_green(self):
        self.install()
        self.assertEqual(self.problems(), "")

    def test_a_link_to_another_tree_is_red_even_when_that_tree_is_an_install_of_the_packs(self):
        modules = self.install()
        elsewhere = self.root / "elsewhere"
        modules.rename(elsewhere)
        modules.symlink_to(elsewhere, target_is_directory=True)
        self.assertIn("is a link to", self.problems())

    def test_a_package_installed_from_other_bytes_is_red(self):
        self.install()
        lock = json.loads(self.hidden.read_text())
        lock["packages"]["node_modules/" + vendor.DURABLE]["integrity"] = "sha512-of-another-commit"
        write(self.hidden, json.dumps(lock))
        self.assertIn("was installed from", self.problems())

    def test_an_installed_file_that_differs_from_its_pack_is_red(self):
        modules = self.install()
        (modules / vendor.DURABLE / "package.json").write_text("{}")
        (modules / vendor.DURABLE / "extra.js").write_text("")
        self.assertIn("differs from", self.problems())
        self.assertIn("extra.js, package.json", self.problems())

    def test_a_missing_install_is_red(self):
        self.assertIn("holds no npm install", self.problems())


def listing(root: Path) -> list[tuple[str, str]]:
    return sorted((p.relative_to(root).as_posix(), "link" if p.is_symlink() else "dir" if p.is_dir() else hashlib.sha256(p.read_bytes()).hexdigest()) for p in root.rglob("*"))


class OverrideTreeTests(Fixture):
    """vendor.override_tree: the tree of a run at another Pi commit is built in the build store, appears only when it is finished, and the
    repository is never written, so no run can leave the repository's own Pi pointing at the other commit."""

    def setUp(self):
        super().setUp()
        import oracle

        self.store = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.store, ignore_errors=True)
        self.source = self.store / COMMIT / "build-1" / "src"
        (self.source / "packages" / "durable" / "src").mkdir(parents=True)
        self.installs = 0
        self.fail = False

        def write_packs(pin, directory):
            shutil.copytree(self.root / PACKS, directory)
            return self.manifest, self.by_name

        def run(command, **options):
            self.installs += 1
            if self.fail:
                raise subprocess.CalledProcessError(1, command)
            (Path(options["cwd"]) / "node_modules").mkdir()

        for patch in (
            mock.patch.object(oracle, "entry", lambda pin: self.store / pin.commit),
            mock.patch.object(vendor, "source_of", lambda pin: self.source),
            mock.patch.object(vendor, "write_packs", write_packs),
            mock.patch.object(vendor, "rewrite_consumer", lambda *arguments: None),
            mock.patch.object(vendor.subprocess, "run", run),
        ):
            patch.start()
            self.addCleanup(patch.stop)

    def trees(self) -> list[str]:
        return sorted(p.name for p in (self.store / COMMIT).iterdir() if p.name.startswith("durable-"))

    def test_the_tree_holds_the_source_packages_the_packs_and_the_install(self):
        tree = vendor.override_tree(PIN, self.root)
        self.assertEqual(tree.parent, self.store / COMMIT)
        self.assertEqual((tree / "packages").resolve(), (self.source / "packages").resolve())
        self.assertEqual(json.loads((tree / PACKS / vendor.MANIFEST).read_text())["commit"], COMMIT)
        self.assertTrue((tree / vendor.INTEROP / "node_modules").is_dir())
        self.assertTrue((tree / vendor.INTEROP / "package-lock.json").is_file())
        self.assertEqual((tree / vendor.READY).read_text(), COMMIT + "\n")

    def test_the_repository_is_not_written(self):
        before = listing(self.root)
        vendor.override_tree(PIN, self.root)
        self.assertEqual(listing(self.root), before)

    def test_a_finished_tree_is_built_once(self):
        self.assertEqual(vendor.override_tree(PIN, self.root), vendor.override_tree(PIN, self.root))
        self.assertEqual(self.installs, 1)

    def test_a_build_that_stops_leaves_no_tree_and_the_repository_as_it_was(self):
        before = listing(self.root)
        self.fail = True
        with self.assertRaises(subprocess.CalledProcessError):
            vendor.override_tree(PIN, self.root)
        self.assertEqual(self.trees(), [])
        self.assertEqual(listing(self.root), before)
        self.fail = False
        self.assertTrue((vendor.override_tree(PIN, self.root) / vendor.READY).is_file())

    def test_another_interop_lock_or_source_build_is_another_tree(self):
        first = vendor.override_tree(PIN, self.root)
        lock = self.root / vendor.INTEROP / "package-lock.json"
        lock.write_text(lock.read_text() + "\n")
        second = vendor.override_tree(PIN, self.root)
        self.source = self.store / COMMIT / "build-2" / "src"
        (self.source / "packages").mkdir(parents=True)
        self.assertEqual(len({first, second, vendor.override_tree(PIN, self.root)}), 3)


if __name__ == "__main__":
    unittest.main()
