# SPDX-License-Identifier: MIT
import hashlib
import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import carried  # noqa: E402
import oracle  # noqa: E402
import pinlib  # noqa: E402

TAG = pinlib.Pin("v1.1.0", "a" * 40, "1.1.0", "1.1.0")
COMMIT = pinlib.Pin("b" * 40, "b" * 40, "1.1.0", "1.1.0+bbbbbbbb")
SCRIPT = oracle.BACKPORTS
HAVE_NODE = shutil.which("node") is not None


class DecisionTests(unittest.TestCase):
    def test_an_entry_is_carried_or_pending_only_when_every_file_of_it_is(self):
        results = [
            {"id": "a", "key": "pi-ai/x.js", "state": "carried", "detail": ""},
            {"id": "b", "key": "pi-ai/y.js", "state": "pending", "detail": ""},
            {"id": "c", "key": "pi-ai/z.js", "state": "carried", "detail": ""}, {"id": "c", "key": "pi-tui/z.js", "state": "pending", "detail": ""},
            {"id": "d", "key": "pi-ai/w.js", "state": "conflict", "detail": "entry text line 2 reads 'a', the file's line 9 reads 'b'"},
        ]
        got = carried.decide_backports(results)
        self.assertEqual({k: v["state"] for k, v in got.items()}, {"a": "carried", "b": "pending", "c": "conflict", "d": "conflict"})
        self.assertIn("carried in pi-ai/z.js but not in the other files", got["c"]["detail"])
        self.assertTrue(got["d"]["detail"].startswith("pi-ai/w.js: entry text line 2"))


@unittest.skipUnless(HAVE_NODE, "node is needed to run the backports script")
class ScriptTests(unittest.TestCase):
    """The real script classifies the real entries against package trees written from its own tables."""

    def packages(self, root: Path, side: int, change: dict | None = None) -> dict[str, Path]:
        """Every file of every entry, holding the entries' before texts (side 0) or after texts (side 1)."""
        done = subprocess.run(["node", "--input-type=module", "-e", f'''
import {{ backportPatches, packageFileBackports }} from {json.dumps(SCRIPT.as_uri())};
console.log(JSON.stringify({{ ...backportPatches, ...packageFileBackports }}));'''], capture_output=True, text=True, check=True)
        tables = json.loads(done.stdout)
        for key, patches in tables.items():
            name, *rest = key.split("/")
            where = rest if name == "pi-coding-agent" and rest[0] in ("docs", "schemas") else ["dist", *rest]
            path = root / name / "/".join(where)
            path.parent.mkdir(parents=True, exist_ok=True)
            text = "\n\n".join(p[side] for p in patches)
            for old, new in (change or {}).items():
                text = text.replace(old, new)
            path.write_text(text, encoding="utf-8")
        return {name: root / name for name in oracle.BACKPORT_PACKAGES}

    def classify(self, packages):
        done = subprocess.run(["node", str(SCRIPT), "--classify", *(f"{n}={p}" for n, p in packages.items())], capture_output=True, text=True, check=True)
        return {(r["id"], r["key"]): r for r in json.loads(done.stdout)}

    def test_a_package_that_lacks_an_entry_is_pending_and_one_that_has_it_is_carried(self):
        with tempfile.TemporaryDirectory() as directory:
            before = self.classify(self.packages(Path(directory) / "before", 0))
            after = self.classify(self.packages(Path(directory) / "after", 1))
        llama = ("b3c2e4a6bef8", "pi-coding-agent/extensions/llama/provider.js")
        self.assertEqual(before[llama]["state"], "pending")
        self.assertEqual(after[llama]["state"], "carried")

    def test_a_changed_source_is_a_conflict_that_shows_the_first_differing_line(self):
        with tempfile.TemporaryDirectory() as directory:
            got = self.classify(self.packages(Path(directory), 0, {"DEFAULT_LLAMA_SERVER_URL = \"http://127.0.0.1:8080\"": "DEFAULT_LLAMA_SERVER_URL = \"http://127.0.0.1:8081\""}))
        entry = got[("b3c2e4a6bef8", "pi-coding-agent/extensions/llama/provider.js")]
        self.assertEqual(entry["state"], "conflict")
        self.assertIn("8080", entry["detail"])
        self.assertIn("8081", entry["detail"])
        self.assertIn("line 1", entry["detail"])

    def test_the_script_without_blocks_is_valid_exports_nothing_and_applies_nothing(self):
        text = carried.retire_backport_blocks(SCRIPT.read_text(encoding="utf-8"), oracle.backport_ids())
        self.assertEqual(oracle.backport_ids(text), [])
        with tempfile.TemporaryDirectory() as directory:
            empty = Path(directory) / "empty.mjs"
            empty.write_text(text, encoding="utf-8")
            done = subprocess.run(["node", "--input-type=module", "-e", f'''
import {{ backportPatches, packageFileBackports, applyPackageBackports, verifyPackageBackports, classifyBackports }} from {json.dumps(empty.as_uri())};
applyPackageBackports({{ "pi-ai": "/nonexistent" }}); verifyPackageBackports({{ "pi-ai": "/nonexistent" }});
console.log(JSON.stringify([Object.keys(backportPatches).length, Object.keys(packageFileBackports).length, classifyBackports({{ "pi-ai": "/nonexistent" }}).length]));'''], capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(done.stdout), [0, 0, 0])


class BlockTests(unittest.TestCase):
    def test_retiring_an_entry_removes_every_block_of_it_and_no_other(self):
        text = SCRIPT.read_text(encoding="utf-8")
        ids = oracle.backport_ids(text)
        self.assertEqual(len(ids), 7, ids)
        out = carried.retire_backport_blocks(text, ["0e1e3fc81"])
        self.assertEqual(oracle.backport_ids(out), [i for i in ids if i != "0e1e3fc81"])
        self.assertNotIn("0e1e3fc81 (#12052)", out)
        self.assertEqual(out.count("// BEGIN backport"), out.count("// END backport"))
        with self.assertRaisesRegex(pinlib.PinError, "no block for the entry nosuch"):
            carried.retire_backport_blocks(text, ["nosuch"])

    def test_a_script_without_entries_leaves_the_oracle_key_of_a_build_without_the_hooks(self):
        with tempfile.TemporaryDirectory() as directory:
            empty = Path(directory) / "none.mjs"
            empty.write_text(carried.retire_backport_blocks(SCRIPT.read_text(encoding="utf-8"), oracle.backport_ids()), encoding="utf-8")
            with mock.patch.object(oracle, "BACKPORTS", empty), mock.patch.object(pinlib, "committed", lambda: COMMIT):
                digest = oracle.backports_digest(COMMIT)
            self.assertEqual(digest, "")
            self.assertEqual(oracle.identity("data", "closure", digest), oracle.identity("data", "closure"))
        with mock.patch.object(pinlib, "committed", lambda: COMMIT):
            self.assertEqual(oracle.backports_digest(COMMIT), hashlib.sha256(SCRIPT.read_bytes()).hexdigest(), "with entries the digest is the script's")


class CarriedOracleTests(unittest.TestCase):
    FILES = ["Makefile", ".github/workflows/ci.yml", "durable/harness/suspendable_oracle_test.go", "durable/interop/carried_oracle_test.go", "durable/interop/interop_test.go",
             "durable/interop/unawaited_job_test.go", "test/parity/interface-closure/autobind/reviewed-decisions.json"]

    def tree(self) -> Path:
        root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, root, True)
        for rel in self.FILES:
            (root / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(pinlib.ROOT / rel, root / rel)
        ref = carried.carried_oracle_ref(root)
        frozen = root / "automation/pin/frozen"
        frozen.mkdir(parents=True)
        for suffix in ("closure.json.gz", "provider-data.tgz"):
            (frozen / f"{ref}.{suffix}").write_text("x")
        (frozen / f"{'c' * 40}.closure.json.gz").write_text("x")
        return root

    def test_the_record_is_the_makefile_variable(self):
        self.assertRegex(carried.carried_oracle_ref(self.tree()) or "", r"^[0-9a-f]{40}$")

    def test_retiring_deletes_the_target_the_ci_step_the_skips_the_decisions_and_the_frozen_files(self):
        root = self.tree()
        ref = carried.carried_oracle_ref(root)
        changed = carried.retire_carried_oracle(root)
        make = (root / "Makefile").read_text()
        for gone in ("CARRIED_ORACLE", "test-carried-oracles", "carried-oracles-run"):
            self.assertNotIn(gone, make)
        self.assertIn("upstream-mirror: parity-deps", make)
        self.assertIn("pin-show:", make)
        ci = (root / ".github/workflows/ci.yml").read_text()
        self.assertNotIn("carried-oracles", ci)
        self.assertIn("Check the Durable core budget", ci)
        self.assertFalse((root / "durable/interop/carried_oracle_test.go").exists())
        for rel in ("durable/interop/interop_test.go", "durable/interop/unawaited_job_test.go", "durable/harness/suspendable_oracle_test.go"):
            self.assertNotIn("skipWithoutSuspendableWork", (root / rel).read_text(), rel)
            self.assertNotIn("Skip", (root / rel).read_text(), rel)
        harness = (root / "durable/harness/suspendable_oracle_test.go").read_text()
        self.assertNotIn('"os"', harness)
        self.assertIn("func suspendableOraclePackages(t testing.TB) string", harness)
        decisions = json.loads((root / "test/parity/interface-closure/autobind/reviewed-decisions.json").read_text())
        self.assertFalse([k for k, v in decisions.items() if "Carried ahead of the pin" in json.dumps(v)])
        self.assertEqual(sorted(p.name for p in (root / "automation/pin/frozen").iterdir()), [f"{'c' * 40}.closure.json.gz"])
        self.assertIn(f"automation/pin/frozen/{ref}.closure.json.gz", changed)

    def test_a_tree_without_the_text_to_delete_fails_naming_the_file(self):
        root = self.tree()
        carried.retire_carried_oracle(root)
        (root / "Makefile").write_text("CARRIED_ORACLE_REF := " + "d" * 40 + "\n")
        with self.assertRaisesRegex(pinlib.PinError, "Makefile: nothing to retire"):
            carried.retire_carried_oracle(root)

    def test_contained_means_ahead_or_identical(self):
        root = self.tree()
        for status, want in (("ahead", "carried"), ("identical", "carried"), ("behind", "pending"), ("diverged", "pending")):
            got = carried.decide_carried_oracle(COMMIT, root, lambda base, head, s=status: {"status": s, "ahead_by": 3, "behind_by": 0})
            self.assertEqual(got["state"], want)
            self.assertIn(status, got["evidence"])

    def test_no_answer_from_github_is_one_actionable_line_never_a_silent_decision(self):
        failure = subprocess.CalledProcessError(22, "curl", stderr="curl: (22) The requested URL returned error: 403")
        with mock.patch.object(carried.subprocess, "run", side_effect=failure):
            with self.assertRaises(pinlib.PinError) as caught:
                carried.github_compare("a" * 40, "b" * 40)
        message = str(caught.exception)
        self.assertNotIn("\n", message)
        self.assertIn("cannot tell whether Pi bbbbbbbb contains the carried commit aaaaaaaaa", message)
        self.assertIn("403", message)
        self.assertIn("GH_PUBLIC_TOKEN", message)


class RetireTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, True)
        for rel in CarriedOracleTests.FILES + ["automation/gen/pi-post-release-backports.mjs"]:
            (self.root / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(pinlib.ROOT / rel, self.root / rel)
        self.script = self.root / "automation/gen/pi-post-release-backports.mjs"
        self.ids = oracle.backport_ids(self.script.read_text())

    def retire(self, states, compare_status="ahead"):
        results = {i: {"state": states.get(i, "pending"), "files": ["f"], "detail": "f: entry text line 1 reads 'a', the file's line 2 reads 'b'" if states.get(i) == "conflict" else ""} for i in self.ids}
        with mock.patch.object(pinlib, "committed", lambda: TAG), mock.patch.object(carried, "classify_backports", lambda modules: results):
            return carried.retire(COMMIT, self.root, modules=Path("/unused"), compare=lambda base, head: {"status": compare_status, "ahead_by": 1, "behind_by": 0})

    def test_carried_entries_go_pending_ones_stay_and_the_decisions_are_returned(self):
        got = self.retire({"3345c2925": "carried", "bbfc6c7ce": "carried"})
        self.assertEqual(oracle.backport_ids(self.script.read_text()), [i for i in self.ids if i not in ("3345c2925", "bbfc6c7ce")])
        self.assertEqual(got["backports"]["3345c2925"]["state"], "carried")
        self.assertEqual(got["carried_oracle"]["state"], "carried")
        self.assertIn("automation/gen/pi-post-release-backports.mjs", got["changed"])
        self.assertNotIn("CARRIED_ORACLE", (self.root / "Makefile").read_text())
        lines = carried.summary(got)
        self.assertTrue(any(line.startswith("backport 3345c2925: carried") and "entry deleted" in line for line in lines))
        self.assertTrue(any(line.startswith("backport b3c2e4a6bef8: pending") and "kept" in line for line in lines))
        self.assertTrue(any("carried oracle" in line and "GitHub compare" in line for line in lines))

    def test_a_conflict_stops_with_the_entry_the_file_and_the_first_differing_line_and_edits_nothing(self):
        before = {p: (self.root / p).read_text() for p in CarriedOracleTests.FILES + ["automation/gen/pi-post-release-backports.mjs"]}
        with self.assertRaises(pinlib.PinError) as caught:
            self.retire({"bbfc6c7ce": "conflict", "3345c2925": "carried"})
        text = str(caught.exception)
        self.assertIn("backport bbfc6c7ce neither applies to nor is carried by Pi 1.1.0+bbbbbbbb: f: entry text line 1 reads 'a', the file's line 2 reads 'b'", text)
        self.assertEqual({p: (self.root / p).read_text() for p in before}, before)

    def test_a_carried_oracle_the_target_does_not_contain_stays(self):
        self.retire({}, compare_status="behind")
        self.assertIn("CARRIED_ORACLE_REF", (self.root / "Makefile").read_text())
        self.assertTrue((self.root / "durable/interop/carried_oracle_test.go").exists())

    def test_the_committed_pin_cannot_be_classified(self):
        with mock.patch.object(pinlib, "committed", lambda: COMMIT):
            with self.assertRaisesRegex(pinlib.PinError, "already the committed pin"):
                carried.retire(COMMIT, self.root, modules=Path("/unused"))


if __name__ == "__main__":
    unittest.main()
