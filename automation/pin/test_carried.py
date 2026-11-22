# SPDX-License-Identifier: MIT
import ast
import hashlib
import json
import re
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

    def blocks_script(self, directory: Path, entries: str, tail: str = "") -> Path:
        """The committed machinery with `entries` as its only blocks, and `tail` as its last statements."""
        committed = SCRIPT.read_text(encoding="utf-8")
        bare = carried.retire_backport_blocks(committed, oracle.backport_ids(committed))
        anchor = re.search(r"^const packageFile = .*\n", bare, re.M)
        path = directory / "blocks.mjs"
        path.write_text(bare[:anchor.end()] + entries + bare[anchor.end():] + tail, encoding="utf-8")
        return path

    def test_two_blocks_of_one_key_both_apply_in_the_order_written(self):
        # The later block's before text holds the earlier block's after text, so it applies only after it.
        entries = '''
// BEGIN backport aaaaaaaaa
packageFile("aaaaaaaaa", "pi-ai/api/x.js", [["one();\\n", "one(1);\\n"]]);
// END backport aaaaaaaaa
// BEGIN backport bbbbbbbbb
patch("bbbbbbbbb", "pi-ai/api/y.js", [["y", "Y"]]);
// END backport bbbbbbbbb
// BEGIN backport ccccccccc
packageFile("ccccccccc", "pi-ai/api/x.js", [["one(1);\\ntwo();\\n", "one(1);\\ntwo(2);\\n"], ["three", "3"]]);
// END backport ccccccccc
'''
        with tempfile.TemporaryDirectory() as directory:
            script = self.blocks_script(Path(directory), entries)
            api = Path(directory) / "pi-ai" / "dist" / "api"
            api.mkdir(parents=True)
            (api / "x.js").write_text("one();\ntwo();\nthree\n", encoding="utf-8")
            (api / "y.js").write_text("y\n", encoding="utf-8")
            package = f"pi-ai={Path(directory) / 'pi-ai'}"
            tables = subprocess.run(["node", "--input-type=module", "-e", f'''
import {{ backportPatches, packageFileBackports }} from {json.dumps(script.as_uri())};
console.log(JSON.stringify([backportPatches, packageFileBackports]));'''], capture_output=True, text=True, check=True)
            subprocess.run(["node", str(script), "--packages", package], capture_output=True, text=True, check=True)
            subprocess.run(["node", str(script), "--verify-packages", package], capture_output=True, text=True, check=True)
            classified = subprocess.run(["node", str(script), "--classify", package], capture_output=True, text=True, check=True)
            self.assertEqual((api / "x.js").read_text(encoding="utf-8"), "one(1);\ntwo(2);\n3\n")
            self.assertEqual((api / "y.js").read_text(encoding="utf-8"), "Y\n")
        self.assertEqual(json.loads(tables.stdout), [
            {"pi-ai/api/y.js": [["y", "Y"]]},
            {"pi-ai/api/x.js": [["one();\n", "one(1);\n"], ["one(1);\ntwo();\n", "one(1);\ntwo(2);\n"], ["three", "3"]]},
        ])
        self.assertEqual([(r["id"], r["key"], r["state"]) for r in json.loads(classified.stdout)], [
            ("aaaaaaaaa", "pi-ai/api/x.js", "carried"), ("bbbbbbbbb", "pi-ai/api/y.js", "carried"), ("ccccccccc", "pi-ai/api/x.js", "carried"),
        ])

    STACKED = '''
// BEGIN backport aaaaaaaaa
patch("aaaaaaaaa", "pi-ai/api/x.js", [["go();\\n", "go();\\nif (a) first();\\n"]]);
// END backport aaaaaaaaa
// BEGIN backport ccccccccc
patch("ccccccccc", "pi-ai/api/x.js", [["if (a) first();\\n", "if (b) second();\\n"]]%s);
// END backport ccccccccc
'''

    def verify_stacked(self, entries: str, written: str = "go();\n") -> subprocess.CompletedProcess:
        with tempfile.TemporaryDirectory() as directory:
            script = self.blocks_script(Path(directory), entries)
            api = Path(directory) / "pi-ai" / "dist" / "api"
            api.mkdir(parents=True)
            (api / "x.js").write_text(written, encoding="utf-8")
            package = f"pi-ai={Path(directory) / 'pi-ai'}"
            subprocess.run(["node", str(script), "--packages", package], capture_output=True, text=True, check=True)
            self.assertEqual((api / "x.js").read_text(encoding="utf-8"), "go();\nif (b) second();\n")
            return subprocess.run(["node", str(script), "--verify-packages", package], capture_output=True, text=True)

    def test_a_block_that_declares_it_supersedes_the_earlier_one_it_rewrites_verifies(self):
        self.assertEqual(self.verify_stacked(self.STACKED % ', "aaaaaaaaa"').returncode, 0)

    def test_a_rewritten_block_that_is_not_declared_superseded_fails_verification(self):
        done = self.verify_stacked(self.STACKED % "")
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("pi-ai/api/x.js holds 0 copies of a backport's added text, want 1: go();\nif (a) first();", done.stderr)

    def test_a_declared_supersede_of_a_block_it_does_not_rewrite_fails_when_the_script_loads(self):
        entries = self.STACKED % ', "aaaaaaaaa"'
        entries = entries.replace('[["if (a) first();\\n", "if (b) second();\\n"]]', '[["other();\\n", "other(2);\\n"]]')
        with tempfile.TemporaryDirectory() as directory:
            script = self.blocks_script(Path(directory), entries)
            done = subprocess.run(["node", str(script), "--dependencies"], capture_output=True, text=True)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("backport block ccccccccc declares it supersedes aaaaaaaaa for pi-ai/api/x.js and rewrites none of its added text", done.stderr)

    def test_two_blocks_that_replace_the_same_text_fail_naming_both(self):
        entries = '''
// BEGIN backport aaaaaaaaa
packageFile("aaaaaaaaa", "pi-ai/api/x.js", [["one();\\n", "one(1);\\n"]]);
// END backport aaaaaaaaa
// BEGIN backport ccccccccc
packageFile("ccccccccc", "pi-ai/api/x.js", [["two();\\n", "two(2);\\n"], ["one();\\n", "one(3);\\n"]]);
// END backport ccccccccc
'''
        with tempfile.TemporaryDirectory() as directory:
            script = self.blocks_script(Path(directory), entries)
            api = Path(directory) / "pi-ai" / "dist" / "api"
            api.mkdir(parents=True)
            (api / "x.js").write_text("one();\ntwo();\n", encoding="utf-8")
            with mock.patch.object(oracle, "BACKPORTS", script), self.assertRaises(pinlib.PinError) as caught:
                oracle.patch_packages({"pi-ai": Path(directory) / "pi-ai"}, "a test tree")
            self.assertEqual((api / "x.js").read_text(encoding="utf-8"), "one();\ntwo();\n")
        self.assertIn("backport blocks aaaaaaaaa and ccccccccc both replace the same text of pi-ai/api/x.js", str(caught.exception))

    def test_a_key_that_is_compiled_output_in_one_block_and_a_package_file_in_another_fails_naming_both(self):
        entries = '''
// BEGIN backport aaaaaaaaa
patch("aaaaaaaaa", "pi-ai/api/x.js", [["one();\\n", "one(1);\\n"]]);
// END backport aaaaaaaaa
// BEGIN backport ccccccccc
packageFile("ccccccccc", "pi-ai/api/x.js", [["two();\\n", "two(2);\\n"]]);
// END backport ccccccccc
'''
        with tempfile.TemporaryDirectory() as directory:
            done = subprocess.run(["node", str(self.blocks_script(Path(directory), entries)), "--package-files"], capture_output=True, text=True, check=False)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("backport blocks aaaaaaaaa and ccccccccc disagree on pi-ai/api/x.js", done.stderr)

    def test_a_table_is_built_without_reading_the_blocks_of_other_tables(self):
        # A block of a table nobody builds, and a block of another kind that has no patches at all, even for the same key.
        entries = '''
// BEGIN backport ddddddddd
blocks.push({ id: "ddddddddd", key: "pi-ai/api/x.js", table: "dependency" });
// END backport ddddddddd
// BEGIN backport eeeeeeeee
blocks.push({ id: "eeeeeeeee", table: "nobody", patches: 7 });
// END backport eeeeeeeee
// BEGIN backport aaaaaaaaa
packageFile("aaaaaaaaa", "pi-ai/api/x.js", [["one();\\n", "one(1);\\n"]]);
// END backport aaaaaaaaa
'''
        with tempfile.TemporaryDirectory() as directory:
            script = self.blocks_script(Path(directory), entries)
            api = Path(directory) / "pi-ai" / "dist" / "api"
            api.mkdir(parents=True)
            (api / "x.js").write_text("one();\n", encoding="utf-8")
            tables = subprocess.run(["node", "--input-type=module", "-e", f'''
import {{ backportPatches, packageFileBackports }} from {json.dumps(script.as_uri())};
console.log(JSON.stringify([backportPatches, packageFileBackports]));'''], capture_output=True, text=True, check=True)
            subprocess.run(["node", str(script), "--packages", f"pi-ai={Path(directory) / 'pi-ai'}"], capture_output=True, text=True, check=True)
            self.assertEqual((api / "x.js").read_text(encoding="utf-8"), "one(1);\n")
        self.assertEqual(json.loads(tables.stdout), [{}, {"pi-ai/api/x.js": [["one();\n", "one(1);\n"]]}])

    def test_a_table_of_any_name_joins_in_block_order_and_refuses_the_same_text_twice(self):
        block = '''
// BEGIN backport {id}
blocks.push({{ id: "{id}", key: "packages/x.ts", table: "made-up", patches: {patches} }});
// END backport {id}
'''
        first = block.format(id="aaaaaaaaa", patches='[["one", "1"]]')
        tail = '\nexport const madeUp = tableOf("made-up");\n'
        read = 'import {{ madeUp, backportPatches, packageFileBackports }} from {}; console.log(JSON.stringify([madeUp, backportPatches, packageFileBackports]));'
        with tempfile.TemporaryDirectory() as directory:
            joined = self.blocks_script(Path(directory), first + block.format(id="ccccccccc", patches='[["two", "2"], ["three", "3"]]'), tail)
            done = subprocess.run(["node", "--input-type=module", "-e", read.format(json.dumps(joined.as_uri()))], capture_output=True, text=True, check=True)
            self.assertEqual(json.loads(done.stdout), [{"packages/x.ts": [["one", "1"], ["two", "2"], ["three", "3"]]}, {}, {}])
            twice = self.blocks_script(Path(directory), first + block.format(id="ccccccccc", patches='[["two", "2"], ["one", "uno"]]'), tail)
            done = subprocess.run(["node", "--input-type=module", "-e", read.format(json.dumps(twice.as_uri()))], capture_output=True, text=True, check=False)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("backport blocks aaaaaaaaa and ccccccccc both replace the same text of packages/x.ts", done.stderr)

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
        self.assertEqual(len(ids), 8, ids)
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
         "test/parity/interface-closure/autobind/mirror_guard_test.go", "test/parity/interface-closure/autobind/reviewed/reviewed-decisions.json"]
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
        decisions = json.loads(left["test/parity/interface-closure/autobind/reviewed/reviewed-decisions.json"])
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

    def test_every_carried_swap_anchor_occurs_once_in_the_tree(self):
        """Every literal swap anchor in retire_carried_oracle matches its target file in the real tree exactly once."""
        tree = ast.parse((pinlib.ROOT / "automation/pin/carried.py").read_text(encoding="utf-8"))
        found = 0
        for node in ast.walk(tree):
            if (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr == "edit"
                    and len(node.args) >= 3 and isinstance(node.args[0], ast.Constant) and isinstance(node.args[0].value, str)
                    and isinstance(node.args[2], ast.Call) and isinstance(node.args[2].func, ast.Name) and node.args[2].func.id == "swap"
                    and len(node.args[2].args) >= 1 and isinstance(node.args[2].args[0], ast.Constant) and isinstance(node.args[2].args[0].value, str)):
                rel, old = node.args[0].value, node.args[2].args[0].value
                found += 1
                self.assertEqual((pinlib.ROOT / rel).read_text(encoding="utf-8").count(old), 1, f"{rel}: swap anchor {old!r}")
        self.assertGreater(found, 0)

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


class DependencyEntryTests(unittest.TestCase):
    """A `dependency(...)` entry rides the block of its Pi commit: it is listed for the oracle build, patches no file, and is classified
    by the range the target's package pins, so it is retired with the commit's other blocks."""

    ENTRY = """// BEGIN backport f00d
// not carried: pi-ai/package.json: published ranges follow the pin.
dependency("f00d", "pi-ai", { name: "@google/genai", from: "2.21.0", to: "2.32.0", resolved: "https://registry.npmjs.org/@google/genai/-/genai-2.32.0.tgz", integrity: "sha512-new", dependencies: { ws: "^8.18.0" } });
// END backport f00d

"""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, True)
        self.script = self.root / "backports.mjs"
        self.script.write_text(backport_fixture.text(self.ENTRY), encoding="utf-8")

    def node(self, *arguments: str) -> str:
        return subprocess.run(["node", str(self.script), *arguments], capture_output=True, text=True, check=True).stdout

    def package(self, pinned: str) -> Path:
        package = self.root / pinned
        package.mkdir()
        (package / "package.json").write_text(json.dumps({"name": "@earendil-works/pi-ai", "dependencies": {"@google/genai": pinned}}))
        return package

    def test_the_entry_is_listed_with_its_commit_and_the_lock_entry_of_the_carried_version(self):
        self.assertEqual(json.loads(self.node("--dependencies")), [{"id": "f00d", "package": "pi-ai", "name": "@google/genai", "from": "2.21.0", "to": "2.32.0", "resolved": "https://registry.npmjs.org/@google/genai/-/genai-2.32.0.tgz", "integrity": "sha512-new", "dependencies": {"ws": "^8.18.0"}}])
        self.assertEqual(oracle.backport_ids(self.script.read_text(encoding="utf-8")), ["f00d"])

    def test_no_file_of_the_installed_package_is_rewritten(self):
        package = self.package("2.21.0")
        before = (package / "package.json").read_bytes()
        self.assertEqual(json.loads(self.node("--package-files", f"pi-ai={package}")), [])
        self.node("--packages", f"pi-ai={package}")
        self.node("--verify-packages", f"pi-ai={package}")
        self.assertEqual((package / "package.json").read_bytes(), before, "the manifest keeps the pin's range")

    def test_the_entry_is_pending_on_the_pinned_range_carried_on_its_own_and_settled_by_ancestry_on_a_later_one(self):
        states = {}
        for pinned in ("2.21.0", "2.32.0", "2.40.0"):
            (result,) = json.loads(self.node("--classify", f"pi-ai={self.package(pinned)}"))
            self.assertEqual((result["id"], result["key"]), ("f00d", "pi-ai/package.json"))
            states[pinned] = result
        self.assertEqual({pinned: (r["state"], r["beforeGone"]) for pinned, r in states.items()}, {"2.21.0": ("pending", False), "2.32.0": ("carried", True), "2.40.0": ("conflict", True)})
        self.assertIn("pins @google/genai 2.40.0, the entry moves 2.21.0 to 2.32.0", states["2.40.0"]["detail"])
        self.assertEqual(carried.decide_backports([states["2.32.0"]], lambda _commit: self.fail("the target's own range decides"))["f00d"]["state"], "carried")
        self.assertEqual(carried.decide_backports([states["2.40.0"]], lambda _commit: {"status": "ahead"})["f00d"]["state"], "carried")
        self.assertEqual(carried.decide_backports([states["2.40.0"]], lambda _commit: {"status": "diverged"})["f00d"]["state"], "conflict")

    def test_retiring_the_commit_deletes_the_entry_with_its_block(self):
        text = carried.retire_backport_blocks(self.script.read_text(encoding="utf-8"), ["f00d"])
        self.assertNotIn("f00d", text)
        self.assertNotIn("not carried: pi-ai/package.json", text)
        self.script.write_text(text, encoding="utf-8")
        self.assertEqual(json.loads(self.node("--dependencies")), [])


@unittest.skipUnless(HAVE_NODE, "node is required")
class ProviderDataManifestTests(unittest.TestCase):
    """Pi writes pi-ai's provider data and its hidden manifest in one generation: applying the backports fails when a data file and its manifest entry differ
    afterwards (the Copilot data file changed by a backport while .manifest.json named the release's bytes was the red of TestPublishedModelDataValidates)."""

    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, True)
        self.script = self.root / "backports.mjs"
        self.script.write_text(backport_fixture.text(""), encoding="utf-8")
        self.package = self.root / "pi-ai"
        self.data = self.package / "dist" / "providers" / "data"
        self.data.mkdir(parents=True)
        self.files = {"a.json": b'{"a":1}\n', "b.json": b'{"b":2}\n'}
        for name, body in self.files.items():
            (self.data / name).write_bytes(body)
        self.write_manifest({name: hashlib.sha256(body).hexdigest() for name, body in self.files.items()})

    def write_manifest(self, files):
        (self.data / ".manifest.json").write_text(json.dumps({"schemaVersion": 6, "generatedAt": "2026-01-01T00:00:00.000Z", "structureHash": "0" * 64, "files": files}), encoding="utf-8")

    def apply(self) -> subprocess.CompletedProcess:
        return subprocess.run(["node", str(self.script), "--packages", f"pi-ai={self.package}"], capture_output=True, text=True)

    def test_data_that_matches_its_manifest_passes(self):
        self.assertEqual(self.apply().returncode, 0)

    def test_a_data_file_changed_without_its_manifest_entry_fails_naming_the_file(self):
        (self.data / "b.json").write_bytes(b'{"b":3}\n')
        done = self.apply()
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("b.json does not match its manifest hash", done.stderr)
        self.assertNotIn("a.json does not match", done.stderr)

    def test_a_data_file_the_manifest_does_not_name_and_a_named_file_that_is_gone_fail(self):
        (self.data / "c.json").write_bytes(b"{}\n")
        self.assertIn("c.json is not in the manifest", self.apply().stderr)
        (self.data / "c.json").unlink()
        (self.data / "a.json").unlink()
        self.assertIn("a.json is in the manifest and not in the directory", self.apply().stderr)

    def test_the_vendored_directory_is_checked_the_same_way(self):
        dist = self.root / "dist"
        (dist / "pi-ai" / "providers").mkdir(parents=True)
        shutil.copytree(self.data, dist / "pi-ai" / "providers" / "data")
        self.assertEqual(subprocess.run(["node", str(self.script), str(dist)], capture_output=True, text=True).returncode, 0)
        (dist / "pi-ai" / "providers" / "data" / "a.json").write_bytes(b"{}\n")
        done = subprocess.run(["node", str(self.script), str(dist)], capture_output=True, text=True)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("a.json does not match its manifest hash", done.stderr)


class OracleLedgerTests(unittest.TestCase):
    """A followed Pi commit that changes what the oracle installs or runs must be carried by the oracle or say why not."""

    PIN = pinlib.Pin("v1.5.2", "b" * 40, "1.5.2", "1.5.2")
    SCRIPT = """// BEGIN backport abc123
patch("abc123", "pi-ai/utils/retry.js", [["a", "b"]]);
// END backport abc123

// BEGIN backport abc123
packageFile("abc123", "pi-coding-agent/docs/models.md", [["a", "b"]]);
// END backport abc123
"""

    def problems(self, rows: str, name: str = "v1.5.2+abc12345.toml") -> list[str]:
        root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, root, True)
        (root / "automation/gen").mkdir(parents=True)
        (root / "automation/gen/pi-post-release-backports.mjs").write_text(self.SCRIPT, encoding="utf-8")
        (root / carried.LEDGERS).mkdir(parents=True)
        (root / carried.LEDGERS / name).write_text('from = "1.5.2"\nto = "1.5.2+abc12345"\n' + rows, encoding="utf-8")
        return carried.oracle_ledger_problems(self.PIN, root)

    @staticmethod
    def row(path: str, disposition: str = "ported", oracle: str | None = None) -> str:
        return f'\n[[files]]\npath = "{path}"\nchange = "modified"\ndisposition = "{disposition}"\nevidence = []\nrationale = "r"\n' + (f'oracle = "{oracle}"\n' if oracle is not None else "")

    def test_a_path_is_keyed_as_the_installed_file_a_block_patches(self):
        self.assertEqual(carried.ledger_block_key("packages/ai/src/api/google-shared.ts"), "pi-ai/api/google-shared.js")
        self.assertEqual(carried.ledger_block_key("packages/coding-agent/src/core/export-html/template.js"), "pi-coding-agent/core/export-html/template.js")
        self.assertEqual(carried.ledger_block_key("packages/coding-agent/docs/models.md"), "pi-coding-agent/docs/models.md")
        self.assertEqual(carried.ledger_block_key("packages/tui/src/tui.ts"), "pi-tui/tui.js")
        self.assertEqual(carried.ledger_block_key("packages/agent/src/agent-loop.ts"), "pi-agent-core/agent-loop.js")
        self.assertEqual(carried.ledger_block_key("packages/mcp/src/naming.ts"), "pi-mcp/naming.js")
        for path in ("packages/ai/test/fetch-option.test.ts", "packages/agent/test/agent-loop.test.ts", "packages/ai/CHANGELOG.md", "packages/chord/src/store/store.ts", "packages/ai/package.json", "package-lock.json"):
            self.assertIsNone(carried.ledger_block_key(path), path)

    def test_a_ported_change_of_a_file_the_oracle_runs_needs_a_block_or_a_reason(self):
        self.assertEqual(self.problems(self.row("packages/ai/src/utils/retry.ts") + self.row("packages/coding-agent/docs/models.md")), [])
        (problem,) = self.problems(self.row("packages/ai/src/api/google-vertex.ts"))
        self.assertIn("v1.5.2+abc12345.toml: packages/ai/src/api/google-vertex.ts: ported, and the oracle's pi-ai/api/google-vertex.js has no backport block", problem)
        self.assertEqual(self.problems(self.row("packages/ai/src/api/google-vertex.ts", oracle="not carried: no scenario reaches it")), [])
        self.assertEqual(self.problems(self.row("packages/ai/src/api/google-vertex.ts", "designed-out") + self.row("packages/ai/test/x.test.ts") + self.row("packages/chord/src/store/store.ts")), [],
                         "a change PiG does not port, a test and a package the oracle does not patch have nothing to carry")

    def test_a_changed_manifest_or_lock_must_say_what_the_oracle_installs_whatever_its_disposition(self):
        for path in ("packages/ai/package.json", "packages/chord/package.json", "package-lock.json", "packages/coding-agent/install-lock/package-lock.json"):
            with self.subTest(path=path):
                (problem,) = self.problems(self.row(path, "designed-out"))
                self.assertIn("does not say what the oracle installs", problem)
                self.assertEqual(self.problems(self.row(path, "designed-out", "not carried: the oracle does not install it")), [])
                self.assertEqual(self.problems(self.row(path, "designed-out", "carried: abc123 as a dependency entry")), [])

    def test_an_answer_must_name_a_carried_entry_or_give_a_reason(self):
        self.assertIn("names the entry def456, which", self.problems(self.row("package-lock.json", "designed-out", "carried: def456"))[0])
        for said in ("yes", "not carried:", "not carried: "):
            self.assertIn("is neither", self.problems(self.row("package-lock.json", "designed-out", said))[0], said)

    def test_only_the_ledgers_of_commits_past_the_pin_are_read(self):
        self.assertEqual(self.problems(self.row("package-lock.json"), name="v1.5.2.toml"), [])
        self.assertEqual(self.problems(self.row("package-lock.json"), name="v1.4.0+abc12345.toml"), [])

    def test_the_committed_ledgers_account_for_the_oracle(self):
        self.assertEqual(carried.oracle_ledger_problems(pinlib.committed()), [])


if __name__ == "__main__":
    unittest.main()
