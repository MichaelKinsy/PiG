# SPDX-License-Identifier: MIT
"""shard-balance.py packs a package's tests into shards by recorded duration and fails a shard that has no headroom."""
import importlib.util
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("shard_balance", HERE / "shard-balance.py")
sb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sb)

PACKAGE = "./pkg/conf"
MODEL = {"fixed": 50.0, "serial-factor": 1.0, "parallel-factor": 0.0, "default": 2.0}


def timings_text(tests, model=None):
    model = model or MODEL
    lines = [f"package {PACKAGE}"] + [f"{key} {value}" for key, value in model.items()]
    lines += [f"{kind} {seconds} {name}" for name, (kind, seconds) in tests.items()]
    return "\n".join(lines) + "\n"


class Fixture:
    """A repository holding one package with the given test names, a timings file and a weights file."""

    def __init__(self, tests, names, shards, model=None):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        package = self.root / "pkg" / "conf"
        package.mkdir(parents=True)
        body = "package conf\n\n" + "".join(f"func {name}(t *testing.T) {{}}\n" for name in names)
        (package / "conf_test.go").write_text(body + "func TestMain(m *testing.M) {}\nfunc helper() {}\n")
        self.timings = self.root / "timings.txt"
        self.timings.write_text(timings_text(tests, model))
        self.weights = self.root / "weights.txt"
        self.shards = shards
        self.pack()

    def pack(self):
        out = self.run("pack")
        self.weights.write_text(out.stdout)

    def run(self, command, shards=None):
        return subprocess.run(
            [sys.executable, str(HERE / "shard-balance.py"), command, "--package", PACKAGE, "--shards", str(shards or self.shards),
             "--timings", str(self.timings), "--weights", str(self.weights), "--root", str(self.root)],
            capture_output=True, text=True, check=False,
        )

    def close(self):
        self.dir.cleanup()


class PackTest(unittest.TestCase):
    def test_packs_longest_first_onto_the_lightest_shard_deterministically(self):
        tests = {f"TestA{i}": ("serial", s) for i, s in enumerate([90, 80, 70, 20, 20, 10])}
        placed = sb.pack(MODEL, tests, 3)
        loads = [sum(tests[n][1] for n, shard in placed.items() if shard == i + 1) for i in range(3)]
        self.assertEqual(loads, [100, 100, 90])
        self.assertEqual(placed, sb.pack(MODEL, dict(reversed(list(tests.items()))), 3))

    def test_parallel_tests_weigh_by_the_parallel_factor(self):
        model = dict(MODEL, **{"parallel-factor": 0.25})
        tests = {"TestSerial": ("serial", 40), "TestParallelA": ("parallel", 80), "TestParallelB": ("parallel", 80)}
        placed = sb.pack(model, tests, 2)
        # 20 s each against the 40 s serial test: the serial test alone, and the two parallel tests together, 40 s a shard.
        self.assertEqual(placed["TestParallelA"], placed["TestParallelB"])
        self.assertNotEqual(placed["TestSerial"], placed["TestParallelA"])

    def test_hash_matches_the_shell_placement(self):
        # test-shard-pattern.sh: FNV-1a over the name's bytes, modulo the shard count.
        self.assertEqual(sb.fnv1a32(""), 2166136261)
        self.assertEqual(sb.fnv1a32("a"), 0xE40C292C)


class CheckTest(unittest.TestCase):
    def test_passes_when_every_shard_has_headroom(self):
        tests = {f"TestT{i}": ("serial", 100.0) for i in range(6)}
        fixture = Fixture(tests, list(tests), 3)
        self.addCleanup(fixture.close)
        result = fixture.run("check")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("predicted seconds per shard", result.stdout)

    def test_fails_when_a_shard_leaves_less_than_the_headroom(self):
        # 50 s fixed + 320 s of tests is 370 s, over 60% of the 600 s timeout.
        tests = {"TestBig": ("serial", 320.0)}
        fixture = Fixture(tests, list(tests), 1)
        self.addCleanup(fixture.close)
        result = fixture.run("check")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exceed 360 s", result.stderr)

    def test_counts_a_test_without_a_timing_at_the_default_cost_where_it_hashes(self):
        tests = {"TestKnown": ("serial", 300.0)}
        names = ["TestKnown"] + [f"TestNew{i}" for i in range(40)]
        fixture = Fixture(tests, names, 1)
        self.addCleanup(fixture.close)
        # One shard: 50 + 300 + 40 * 2 = 430 s.
        result = fixture.run("check")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("430 s", result.stderr)
        self.assertIn("40 tests unrecorded", result.stdout)

    def test_fails_when_the_pins_are_not_what_the_timings_pack_to(self):
        tests = {f"TestT{i}": ("serial", 50.0) for i in range(4)}
        fixture = Fixture(tests, list(tests), 2)
        self.addCleanup(fixture.close)
        lines = fixture.weights.read_text().splitlines()
        moved = [("2" if line.startswith("1 ") else "1" if line.startswith("2 ") else "") + line[1:] if line[:2] in ("1 ", "2 ") else line for line in lines]
        fixture.weights.write_text("\n".join(moved) + "\n")
        result = fixture.run("check")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not what the timings", result.stderr)

    def test_fails_without_a_weights_section_for_the_shard_count(self):
        tests = {"TestA": ("serial", 10.0)}
        fixture = Fixture(tests, list(tests), 2)
        self.addCleanup(fixture.close)
        result = fixture.run("check", shards=3)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no section", result.stderr)

    def test_other_sections_of_the_weights_file_are_left_alone(self):
        tests = {f"TestT{i}": ("serial", 50.0) for i in range(4)}
        fixture = Fixture(tests, list(tests), 2)
        self.addCleanup(fixture.close)
        other = "package ./other\nshards 2\n1 TestElsewhere\n\npackage ./pkg/conf\nshards 4\n1 TestA\n\n"
        fixture.weights.write_text(other + fixture.weights.read_text())
        self.assertEqual(fixture.run("check").returncode, 0)


class RecordTest(unittest.TestCase):
    LOG = """[test-grouped] >>> sharded pkg shard {shard} of 2
=== RUN   TestSerial
--- PASS: TestSerial ({serial}s)
=== RUN   TestParallel
=== PAUSE TestParallel
=== CONT  TestParallel
=== RUN   TestParallel/sub
--- PASS: TestParallel/sub (1.00s)
--- PASS: TestParallel ({parallel}s)
PASS
ok  	pkg	{wall}s
"""

    def test_record_takes_the_median_and_fits_the_shard_times(self):
        with tempfile.TemporaryDirectory() as tmp:
            logs = []
            for index, (serial, parallel) in enumerate([(100, 40), (120, 40), (140, 80), (110, 10)]):
                path = Path(tmp) / f"{index}.log"
                path.write_text(self.LOG.format(shard=1 + index % 2, serial=serial, parallel=parallel, wall=50 + 1.1 * serial + 0.2 * parallel))
                logs.append(str(path))
            result = subprocess.run(
                [sys.executable, str(HERE / "shard-balance.py"), "record", "--package", PACKAGE, *logs],
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            lines = result.stdout.splitlines()
            self.assertIn("serial 115.00 TestSerial", lines)  # the median of 100, 120, 140, 110
            self.assertIn("parallel 40.00 TestParallel", lines)
            self.assertNotIn("TestParallel/sub", result.stdout)
            self.assertIn("fixed 50.0", lines)
            self.assertIn("serial-factor 1.100", lines)
            self.assertIn("parallel-factor 0.200", lines)


class RepositoryTest(unittest.TestCase):
    def test_the_conformance_shards_keep_their_headroom(self):
        # The gate itself, on the recorded timings and pins of this repository: the shard count is the one test-grouped.sh runs.
        root = HERE.parent.parent
        script = (root / "automation/ci/test-grouped.sh").read_text()
        count = next(line.split(":")[-1] for line in script.splitlines() if line.strip().startswith("./test/extension-conformance:"))
        result = subprocess.run(
            [sys.executable, str(HERE / "shard-balance.py"), "check", "--package", "./test/extension-conformance", "--shards", count.strip()],
            capture_output=True, text=True, check=False, cwd=root,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_the_two_conformance_jobs_run_every_shard_once(self):
        root = HERE.parent.parent
        script = (root / "automation/ci/test-grouped.sh").read_text()
        count = int(next(line.split(":")[-1] for line in script.splitlines() if line.strip().startswith("./test/extension-conformance:")))
        ranges = re.findall(r"^test-conformance-([12]):.*\n\t@\./automation/ci/test-grouped\.sh conformance (\d+) (\d+)$", (root / "automation/make/ci.mk").read_text(), re.MULTILINE)
        self.assertEqual([(job, int(first)) for job, first, _ in ranges][0], ("1", 1))
        self.assertEqual([(int(first), int(last)) for _, first, last in ranges][1][1], count)
        self.assertEqual(int(ranges[0][2]) + 1, int(ranges[1][1]), "the first job's last shard and the second job's first shard are not adjacent")


if __name__ == "__main__":
    unittest.main()
