# SPDX-License-Identifier: MIT
"""Tests for the performance scoreboard generator: registry validation, rendering and the ratchet check."""
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import scoreboard  # noqa: E402


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


class ValidateTest(unittest.TestCase):
    def setUp(self):
        self.root = pathlib.Path(tempfile.mkdtemp())
        write(self.root / "pkg/a_test.go", "package pkg\n\nfunc TestPresent(t *testing.T) {}\n")

    def metric(self, gates):
        return {"metric": [{"id": "m", "area": "a", "title": "t", "impact": 1, "owner": "o", "target": "x", "basis": "y", "gates": gates}]}

    def test_the_committed_registry_is_consistent_with_the_tree(self):
        registry = scoreboard.load_toml(scoreboard.REGISTRY)
        ratchet = scoreboard.load_toml(scoreboard.RATCHET)
        self.assertEqual(scoreboard.validate(registry, ratchet), [])

    def test_a_named_test_must_exist(self):
        registry = self.metric([{"test": "pkg:TestPresent"}, {"test": "pkg:TestAbsent"}])
        problems = scoreboard.validate(registry, {"ceilings": {}}, self.root)
        self.assertEqual(problems, ["metric m: test pkg:TestAbsent does not exist"])

    def test_a_ratchet_gate_needs_a_ceiling_and_every_ceiling_a_metric(self):
        registry = self.metric([{"ratchet": "x.allocs"}])
        problems = scoreboard.validate(registry, {"ceilings": {"y.allocs": 1}}, self.root)
        self.assertIn("metric m: ratchet gate x.allocs has no ceiling in ratchet.toml", problems)
        self.assertIn("ceiling y.allocs belongs to no metric", problems)

    def test_a_missing_gate_must_be_routed(self):
        problems = scoreboard.validate(self.metric([{"missing": "a gate"}]), {"ceilings": {}}, self.root)
        self.assertEqual(problems, ["metric m: missing gate 'a gate' is not routed to a lane"])

    def test_a_gate_has_exactly_one_kind(self):
        problems = scoreboard.validate(self.metric([{"test": "pkg:TestPresent", "ratchet": "x"}]), {"ceilings": {}}, self.root)
        self.assertEqual(len(problems), 1)
        self.assertIn("exactly one of", problems[0])


class PlanTest(unittest.TestCase):
    def registry(self, placement, techniques):
        return {
            "plan": [{"gap": "g", "plan": "p", "gain": "x", "route": "r", "techniques": techniques}],
            "technique": [{"id": "T1", "name": "n", "placement": placement}],
        }

    def test_a_placement_must_match_the_plan_that_lists_it(self):
        problems = scoreboard.validate(self.registry("plan 1", []), {"ceilings": {}})
        self.assertEqual(problems, ["technique T1: placed in plan 1, which does not list it"])
        self.assertEqual(scoreboard.validate(self.registry("plan 1", ["T1"]), {"ceilings": {}}), [])

    def test_a_listed_technique_must_be_placed_and_known(self):
        problems = scoreboard.validate(self.registry("below the top ten", ["T1", "T9"]), {"ceilings": {}})
        self.assertIn("plan 1: unknown technique T9", problems)
        self.assertIn("technique T1: listed by a plan but placed in none", problems)

    def test_the_plan_holds_at_most_ten(self):
        registry = {"plan": [{"gap": "g", "plan": "p", "gain": "x", "route": "r"}] * 11}
        self.assertIn("plan: 11 entries; the ranked plan holds the top 10", scoreboard.validate(registry, {"ceilings": {}}))


class RenderTest(unittest.TestCase):
    def test_status_counts_and_missing_section(self):
        registry = {"metric": [
            {"id": "a", "area": "s", "title": "A", "impact": 5, "owner": "o", "target": "t", "basis": "b",
             "gates": [{"ratchet": "a.allocs"}, {"missing": "time gate", "route": "k8s-bench"}]},
            {"id": "b", "area": "s", "title": "B", "impact": 1, "owner": "o", "target": "t", "basis": "b",
             "gates": [{"branch": "team/x", "path": "f"}]},
        ]}
        text = scoreboard.render(registry, {"ceilings": {"a.allocs": 10}}, {"rev": "r", "go": "g", "platform": "p", "values": {"a.allocs": 7}})
        self.assertIn("2 metrics; 3 gates: 1 in this tree, 1 on another lane's branch, 1 missing.", text)
        self.assertIn("| **a**: A | MISSING |", text)
        self.assertIn("| **b**: B | BRANCH |", text)
        self.assertIn("`a.allocs` 7 (ceiling 10)", text)
        self.assertIn("| a | time gate | k8s-bench |", text)

    def test_protocol_section_lists_the_rules(self):
        registry = {"metric": [], "protocol": {"competitors": ["A", "B"], "platforms": ["Linux"], "endpoints": ["e"], "scenarios": ["s"],
                                               "rules": ["Instruction counts never rank across languages."], "route": "lane"}}
        text = scoreboard.render(registry, {"ceilings": {}}, None)
        self.assertIn("## Cross-language comparison method", text)
        self.assertIn("Harnesses: A; B.", text)
        self.assertIn("- Instruction counts never rank across languages.", text)

    def test_byte_counts_carry_units(self):
        self.assertEqual(scoreboard.human(2_500_000, "x.bytes"), "2.50 MB")
        self.assertEqual(scoreboard.human(65.4, "x.bytes_per_history_message"), "65 B")
        self.assertEqual(scoreboard.human(12345.6, "x.allocs"), "12,345.6")
        self.assertEqual(scoreboard.human(None, "x.allocs"), "not measured")
        self.assertEqual(scoreboard.human(1.0937, "x.bytes_exponent"), "1.09")


class RatchetTest(unittest.TestCase):
    def setUp(self):
        self.root = pathlib.Path(tempfile.mkdtemp())
        self.git("init", "-q")
        self.commit({"x.allocs": 10, "y.allocs": 5}, "base")
        self.base = self.git("rev-parse", "HEAD").strip()

    def git(self, *args):
        env = {**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t",
               "GIT_COMMITTER_EMAIL": "t@example.com", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}
        return subprocess.run(["git", *args], cwd=self.root, env=env, check=True, stdout=subprocess.PIPE, text=True).stdout

    def commit(self, ceilings, message):
        body = "[ceilings]\n" + "".join(f'"{key}" = {value}\n' for key, value in ceilings.items())
        write(self.root / "automation/perf/ratchet.toml", body)
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", message)

    def test_lowering_passes(self):
        self.commit({"x.allocs": 9, "y.allocs": 5}, "lower x")
        self.assertEqual(scoreboard.ratchet_check(self.base, self.root), [])

    def test_raising_fails_without_a_trailer(self):
        self.commit({"x.allocs": 11, "y.allocs": 5}, "raise x")
        problems = scoreboard.ratchet_check(self.base, self.root)
        self.assertEqual(len(problems), 1)
        self.assertIn("x.allocs: ceiling raised 10 -> 11", problems[0])

    def test_raising_passes_with_a_trailer(self):
        self.commit({"x.allocs": 11, "y.allocs": 5}, "raise x\n\nPerf-Ratchet-Raise: x.allocs new parity work needs it")
        self.assertEqual(scoreboard.ratchet_check(self.base, self.root), [])

    def test_removing_fails_without_a_retire_trailer(self):
        self.commit({"x.allocs": 10}, "drop y")
        self.assertIn("y.allocs: ceiling removed", scoreboard.ratchet_check(self.base, self.root)[0])
        self.commit({"x.allocs": 10}, "retire\n\nPerf-Ratchet-Retire: y.allocs the path was deleted")
        self.assertEqual(scoreboard.ratchet_check(self.base, self.root), [])

    def test_an_unresolvable_base_fails(self):
        self.commit({"x.allocs": 11, "y.allocs": 5}, "raise x")
        problems = scoreboard.ratchet_check("no-such-ref", self.root)
        self.assertEqual(len(problems), 1)
        self.assertIn("base no-such-ref does not name a commit", problems[0])

    def test_a_base_before_the_ratchet_file_passes(self):
        write(self.root / "other.txt", "x")
        self.git("rm", "-q", "automation/perf/ratchet.toml")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "no ratchet file")
        before = self.git("rev-parse", "HEAD").strip()
        self.commit({"x.allocs": 99}, "add the file")
        self.assertEqual(scoreboard.ratchet_check(before, self.root), [])


if __name__ == "__main__":
    unittest.main()
