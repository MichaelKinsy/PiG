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
import backport_fixture  # noqa: E402

TAG = pinlib.Pin("v1.1.0", "a" * 40, "1.1.0", "1.1.0")
COMMIT = pinlib.Pin("b" * 40, "b" * 40, "1.1.0", "1.1.0+bbbbbbbb")
SCRIPT = backport_fixture.script()
HAVE_NODE = shutil.which("node") is not None


class DecisionTests(unittest.TestCase):
    @staticmethod
    def never(commit):
        raise AssertionError(f"the ancestry of {commit} was asked although the files decide")

    def gone(self, state="conflict", key="pi-tui/x.js", before_gone=True):
        return {"id": "e", "key": key, "state": state, "beforeGone": before_gone, "detail": "entry text line 1 reads 'a', the file's line 2 reads 'b'"}

    def test_pi_own_version_of_a_change_is_carried_when_the_target_contains_the_commit(self):
        asked = []
        got = carried.decide_backports([self.gone(), self.gone(key="pi-ai/y.js", state="carried", before_gone=True)], lambda c: asked.append(c) or {"status": "ahead"})
        self.assertEqual(got["e"]["state"], "carried")
        self.assertEqual(asked, ["e"])
        self.assertIn("GitHub compare e...target: ahead", got["e"]["evidence"])
        self.assertIn("pi-tui/x.js", got["e"]["evidence"])

    def test_a_missing_before_text_is_a_conflict_when_the_target_does_not_contain_the_commit(self):
        got = carried.decide_backports([self.gone()], lambda c: {"status": "diverged"})
        self.assertEqual(got["e"]["state"], "conflict")
        self.assertIn("pi-tui/x.js: entry text line 1", got["e"]["detail"])
        self.assertIn("the target does not contain the commit", got["e"]["detail"])

    def test_a_conflict_with_the_before_text_still_in_the_file_never_asks_the_ancestry(self):
        got = carried.decide_backports([self.gone(before_gone=False)], self.never)
        self.assertEqual(got["e"]["state"], "conflict")

    def test_an_entry_is_carried_or_pending_only_when_every_file_of_it_is(self):
        results = [
            {"id": "a", "key": "pi-ai/x.js", "state": "carried", "detail": ""},
            {"id": "b", "key": "pi-ai/y.js", "state": "pending", "detail": ""},
            {"id": "c", "key": "pi-ai/z.js", "state": "carried", "detail": ""}, {"id": "c", "key": "pi-tui/z.js", "state": "pending", "detail": ""},
            {"id": "d", "key": "pi-ai/w.js", "state": "conflict", "beforeGone": False, "detail": "entry text line 2 reads 'a', the file's line 9 reads 'b'"},
        ]
        got = carried.decide_backports(results, self.never)
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
        text = carried.retire_backport_blocks(SCRIPT.read_text(encoding="utf-8"), oracle.backport_ids(SCRIPT.read_text(encoding="utf-8")))
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
            empty.write_text(carried.retire_backport_blocks(SCRIPT.read_text(encoding="utf-8"), oracle.backport_ids(SCRIPT.read_text(encoding="utf-8"))), encoding="utf-8")
            with mock.patch.object(oracle, "BACKPORTS", empty), mock.patch.object(pinlib, "committed", lambda: COMMIT):
                digest = oracle.backports_digest(COMMIT)
            self.assertEqual(digest, "")
            self.assertEqual(oracle.identity("data", "closure", digest), oracle.identity("data", "closure"))
        with mock.patch.object(oracle, "BACKPORTS", SCRIPT), mock.patch.object(pinlib, "committed", lambda: COMMIT):
            self.assertEqual(oracle.backports_digest(COMMIT), hashlib.sha256(SCRIPT.read_bytes()).hexdigest(), "with entries the digest is the script's")


NO_CARRIED_ORACLE = "the pin carries no oracle ahead of it (no CARRIED_ORACLE_REF in the Makefile): the retirement was done by the bump, and these tests run again when a build carries one"

# Every file a retirement of the carried oracle edits or deletes, but the commit's frozen files.
FILES = ["Makefile", ".github/workflows/ci.yml", "automation/pin/carried_guard.py", "automation/pin/test_carried_guard.py", "automation/pin/vendor.py",
         "docs/parity/upstream-pin.md", "durable/codemode/codemode_pi_oracle_test.go", "durable/codemode/describe_pi_oracle_test.go",
         "durable/harness/submissions_pi_oracle_test.go", "durable/harness/suspendable_oracle_test.go", "durable/harness/wake_memos_pi_oracle_test.go",
         "durable/interop/carried_oracle_test.go", "durable/interop/interop_test.go", "durable/interop/unawaited_job_test.go", "internal/pioracle/source.go",
         "test/parity/interface-closure/autobind/mirror_guard_test.go", "test/parity/interface-closure/autobind/reviewed-decisions.json"]
# Where a retired name may stay: the release history, the sync ledger of the pin that carried the work, and the tool that retires the next one.
HISTORY = ("changelog.d/", "CHANGELOG.md", "docs/bug-ledger.json", "docs/site/docs/bug-ledger.md", "test/parity/upstream-sync/", "automation/pin/carried.py", "automation/pin/test_carried.py")
OTHER = "c" * 40


def carried_tree(case: unittest.TestCase) -> Path:
    """A copy of the files of the real tree a retirement touches, with stand-ins for the carried commit's frozen files and another commit's."""
    root = Path(tempfile.mkdtemp())
    case.addCleanup(shutil.rmtree, root, True)
    for rel in FILES:
        (root / rel).parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(pinlib.ROOT / rel, root / rel)
    ref = carried.carried_oracle_ref(root)
    frozen = root / "automation/pin/frozen"
    frozen.mkdir(parents=True)
    for suffix in ("closure.json.gz", "provider-data.tgz"):
        (frozen / f"{ref}.{suffix}").write_text("x")
    (frozen / f"{OTHER}.closure.json.gz").write_text("x")
    return root


def snapshot(root: Path) -> dict[str, bytes]:
    return {str(path.relative_to(root)): path.read_bytes() for path in sorted(root.rglob("*")) if path.is_file()}


@unittest.skipUnless(carried.carried_oracle_ref(), NO_CARRIED_ORACLE)
class CarriedOracleTests(unittest.TestCase):
    def tree(self) -> Path:
        return carried_tree(self)

    def test_the_record_is_the_makefile_variable(self):
        self.assertRegex(carried.carried_oracle_ref(self.tree()) or "", r"^[0-9a-f]{40}$")

    def test_retiring_deletes_the_targets_the_ci_step_the_guard_the_skips_the_decisions_and_the_frozen_files(self):
        root = self.tree()
        ref = carried.carried_oracle_ref(root)
        changed = carried.retire_carried_oracle(root)
        make = (root / "Makefile").read_text()
        self.assertIn("upstream-mirror: parity-deps", make)
        self.assertIn("\n\npin-show:", make)
        ci = (root / ".github/workflows/ci.yml").read_text()
        self.assertNotIn("carried", ci)
        self.assertIn("make durable-core-budget\n      - name: Save the Go module cache\n", ci)
        gone = ["automation/pin/carried_guard.py", "automation/pin/test_carried_guard.py", "durable/interop/carried_oracle_test.go", "durable/harness/suspendable_oracle_test.go"]
        for rel in gone:
            self.assertFalse((root / rel).exists(), rel)
        left = snapshot(root)
        for rel in FILES:
            if rel not in gone:
                self.assertEqual(carried.retired_names_in(rel, left[rel].decode()), [], rel)
            self.assertIn(rel, changed)
        document = left["docs/parity/upstream-pin.md"].decode().split("\n")
        self.assertEqual([line[:17] for line in document if any(name in line for name in carried.RETIRED_NAMES)], ["**Carried work.**"])
        for rel in ("durable/harness/submissions_pi_oracle_test.go", "durable/harness/wake_memos_pi_oracle_test.go"):
            self.assertIn('"testdata/', left[rel].decode())
            self.assertRegex(left[rel].decode(), r'_oracle\.mjs", pioracle\.Packages\(t\)\)', rel)
        describe = left["durable/codemode/describe_pi_oracle_test.go"].decode()
        for unused in ('\t"os"\n', '\t"path/filepath"\n', "SkipAtPin"):
            self.assertNotIn(unused, describe)
        for kept in ('\t"os/exec"\n', '\t"strings"\n', "func askDescribePi(t *testing.T, ops []describeOp) []describeAnswer {\n\tt.Helper()\n\tinput, err :=", "func TestDescribeMatchesPiOnSeededToolSets(t *testing.T) {\n\trng :="):
            self.assertIn(kept, describe)
        self.assertIn("func TestScriptedSessionStoresMatchPiDurable(t *testing.T) {\n\tdir := t.TempDir()\n", left["durable/interop/interop_test.go"].decode())
        for rel in ("internal/pioracle/source.go", "automation/pin/vendor.py"):
            self.assertIn("override", left[rel].decode().lower(), f"{rel}: the tooling of a run at another Pi commit stays")
        self.assertIn("func SkipAtPin(", left["internal/pioracle/source.go"].decode())
        decisions = json.loads(left["test/parity/interface-closure/autobind/reviewed-decisions.json"])
        self.assertFalse([k for k, v in decisions.items() if "Carried ahead of the pin" in json.dumps(v)])
        self.assertEqual(sorted(p.name for p in (root / "automation/pin/frozen").iterdir()), [f"{OTHER}.closure.json.gz"])
        self.assertIn(f"automation/pin/frozen/{ref}.closure.json.gz", changed)

    def test_no_tracked_file_outside_the_retired_ones_and_the_history_names_a_retired_piece(self):
        """With the test above (every file a retirement writes is free of the names), `git grep` of them is empty after a retirement."""
        found = subprocess.run(["git", "-C", str(pinlib.ROOT), "grep", "-l", "-E", "|".join(carried.RETIRED_NAMES)], capture_output=True, text=True)
        if found.returncode != 0:
            self.skipTest(f"git grep cannot read the tree: {found.stderr.strip() or 'no match'}")
        stray = [rel for rel in found.stdout.split("\n") if rel and rel not in FILES and not rel.startswith(HISTORY)]
        self.assertEqual(stray, [], "these files name a piece of the carried oracle and retire_carried_oracle does not edit them")

    @unittest.skipUnless(shutil.which("make"), "make is not installed")
    def test_the_makefile_after_a_retirement_still_plans_make_test_and_has_no_carried_target(self):
        root = self.tree()
        carried.retire_carried_oracle(root)

        def plan(target):
            return subprocess.run(["make", "-n", "-C", str(pinlib.ROOT), "-f", str(root / "Makefile"), target], capture_output=True, text=True)
        tests = plan("test")
        self.assertEqual(tests.returncode, 0, tests.stderr[-2000:])
        self.assertNotIn("carried", tests.stdout + tests.stderr)
        for target in ("test-carried-oracles", "test-carried-oracles-guard"):
            done = plan(target)
            self.assertNotEqual(done.returncode, 0, target)
            self.assertIn(f"No rule to make target '{target}'", done.stderr)

    def test_a_second_retirement_changes_nothing(self):
        root = self.tree()
        carried.retire_carried_oracle(root)
        after = snapshot(root)
        self.assertEqual(carried.retire_carried_oracle(root), [])
        self.assertEqual(snapshot(root), after)

    def test_a_tree_where_one_piece_is_gone_fails_naming_it_and_writes_nothing(self):
        def without(rel, old, new=""):
            def change(root):
                text = (root / rel).read_text()
                self.assertIn(old, text, rel)
                (root / rel).write_text(text.replace(old, new))
            return change

        def removed(rel):
            return lambda root: (root / rel).unlink()
        ref = carried.carried_oracle_ref()
        cases = [
            (without("Makefile", "\npin-show:", "\ncarried-again: test-carried-oracles\n\npin-show:"), r"Makefile: the retirement would leave test-carried-oracles behind"),
            (without("Makefile", ".PHONY: test-carried-oracles test-carried-oracles-guard\n"), r"Makefile: nothing to retire \(the block of CARRIED_ORACLE_REF, test-carried-oracles and test-carried-oracles-guard is not there\)"),
            (without(".github/workflows/ci.yml", "make test-carried-oracles-guard\n", "make test\n"), r"\.github/workflows/ci\.yml: nothing to retire \(the step that runs make test-carried-oracles-guard is not there\)"),
            (removed("automation/pin/carried_guard.py"), r"automation/pin/carried_guard\.py: nothing to retire \(the guard of the carried run is not there\)"),
            (removed("durable/interop/carried_oracle_test.go"), r"durable/interop/carried_oracle_test\.go: nothing to retire \(the skip skipWithoutSuspendableWork is not there\)"),
            (removed("durable/harness/suspendable_oracle_test.go"), r"durable/harness/suspendable_oracle_test\.go: nothing to retire \(the skip of suspendableOraclePackages is not there\)"),
            (without("durable/codemode/describe_pi_oracle_test.go", "// namespaceOracleSkip skips ", "// namespaceOracleSkip passes "), r"durable/codemode/describe_pi_oracle_test\.go: nothing to retire \(the skip namespaceOracleSkip is not there\)"),
            (without("durable/harness/wake_memos_pi_oracle_test.go", '"github.com/MichaelKinsy/PiG/internal/pioracle"', '"example.com/other"'), r"durable/harness/wake_memos_pi_oracle_test\.go: cannot retire the skip of suspendableOraclePackages: the file does not import internal/pioracle"),
            (without("internal/pioracle/source.go", "make test-carried-oracles sets it", "A run sets it"), r"internal/pioracle/source\.go: nothing to retire \(the mention of make test-carried-oracles is not there\)"),
            (without("docs/parity/upstream-pin.md", "- The carried run at this pin: ", "- A carried run: "), r"docs/parity/upstream-pin\.md: nothing to retire \(the paragraph 'The carried run at this pin' is not there\)"),
            (removed(f"automation/pin/frozen/{ref}.closure.json.gz"), None),
            (lambda root: [path.unlink() for path in (root / "automation/pin/frozen").glob(f"{ref}.*")], rf"automation/pin/frozen: nothing to retire \(the frozen files of the carried commit {ref} are not there\)"),
        ]
        for number, (change, message) in enumerate(cases):
            with self.subTest(case=number, message=message):
                root = self.tree()
                change(root)
                before = snapshot(root)
                if message is None:
                    self.assertIn("Makefile", carried.retire_carried_oracle(root), "one frozen file is enough: a commit pin without a closure has only its provider data")
                    continue
                with self.assertRaisesRegex(pinlib.PinError, message):
                    carried.retire_carried_oracle(root)
                self.assertEqual(snapshot(root), before, "a retirement that stops writes nothing")

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


@unittest.skipUnless(carried.carried_oracle_ref(), NO_CARRIED_ORACLE)
class RetireTests(unittest.TestCase):
    def setUp(self):
        self.root = carried_tree(self)
        self.script = self.root / "automation/gen/pi-post-release-backports.mjs"
        self.script.parent.mkdir(parents=True, exist_ok=True)
        self.script.write_text(SCRIPT.read_text(encoding="utf-8"), encoding="utf-8")
        self.ids = oracle.backport_ids(self.script.read_text())

    def retire(self, states, compare_status="ahead", target=COMMIT):
        results = {i: {"state": states.get(i, "pending"), "files": ["f"], "detail": "f: entry text line 1 reads 'a', the file's line 2 reads 'b'" if states.get(i) == "conflict" else "", "evidence": "test"} for i in self.ids}
        with mock.patch.object(pinlib, "committed", lambda: TAG), mock.patch.object(carried, "classify_backports", lambda modules, contains: results):
            return carried.retire(target, self.root, modules=Path("/unused"), compare=lambda base, head: {"status": compare_status, "ahead_by": 1, "behind_by": 0})

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
        before = snapshot(self.root)
        with self.assertRaises(pinlib.PinError) as caught:
            self.retire({"bbfc6c7ce": "conflict", "3345c2925": "carried"})
        text = str(caught.exception)
        self.assertIn("backport bbfc6c7ce neither applies to nor is carried by Pi 1.1.0+bbbbbbbb: f: entry text line 1 reads 'a', the file's line 2 reads 'b'", text)
        self.assertEqual(snapshot(self.root), before)

    def test_a_carried_oracle_the_target_does_not_contain_stays(self):
        self.retire({}, compare_status="behind")
        self.assertIn("CARRIED_ORACLE_REF", (self.root / "Makefile").read_text())
        self.assertTrue((self.root / "durable/interop/carried_oracle_test.go").exists())

    def test_a_carried_piece_that_is_gone_stops_the_retirement_before_the_backports_are_edited(self):
        (self.root / "automation/pin/carried_guard.py").unlink()
        before = snapshot(self.root)
        with self.assertRaisesRegex(pinlib.PinError, r"carried_guard\.py: nothing to retire"):
            self.retire({"3345c2925": "carried"})
        self.assertEqual(snapshot(self.root), before)

    def test_a_second_retirement_says_that_no_oracle_is_carried(self):
        self.retire({"3345c2925": "carried"})
        after = snapshot(self.root)
        self.ids = oracle.backport_ids(self.script.read_text())
        got = self.retire({})
        self.assertEqual((got["carried_oracle"], got["changed"]), (None, []))
        self.assertEqual(snapshot(self.root), after)
        self.assertIn("carried oracle: none (the Makefile names no CARRIED_ORACLE_REF) -> nothing to retire", carried.summary(got))
        self.assertEqual(carried.summary({"backports": {}, "carried_oracle": None, "changed": []}), ["nothing is carried ahead of the pin"])

    def test_a_target_that_is_the_carried_commit_keeps_its_frozen_files(self):
        ref = carried.carried_oracle_ref(self.root)
        got = self.retire({}, compare_status="identical", target=pinlib.Pin(ref, ref, "1.1.0", "1.1.0+" + ref[:8]))
        self.assertEqual(got["carried_oracle"]["state"], "carried")
        self.assertNotIn("CARRIED_ORACLE", (self.root / "Makefile").read_text())
        self.assertEqual(sorted(p.name for p in (self.root / "automation/pin/frozen").iterdir()), sorted([f"{OTHER}.closure.json.gz", f"{ref}.closure.json.gz", f"{ref}.provider-data.tgz"]))
        self.assertFalse([rel for rel in got["changed"] if "frozen" in rel])

    def test_the_committed_pin_cannot_be_classified(self):
        with mock.patch.object(pinlib, "committed", lambda: COMMIT):
            with self.assertRaisesRegex(pinlib.PinError, "already the committed pin"):
                carried.retire(COMMIT, self.root, modules=Path("/unused"))


if __name__ == "__main__":
    unittest.main()
