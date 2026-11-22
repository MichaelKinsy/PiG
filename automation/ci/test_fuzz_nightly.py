# SPDX-License-Identifier: MIT
"""Regression tests for automation/ci/fuzz-nightly.sh on synthetic git repositories."""
from __future__ import annotations

from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("fuzz-nightly.sh")


def init_fixture_repo(root: Path, files: dict[str, str], skip_list: str | None = None) -> None:
    (root / "automation/ci").mkdir(parents=True, exist_ok=True)
    shutil.copy(SCRIPT, root / "automation/ci/fuzz-nightly.sh")
    if skip_list is not None:
        (root / "automation/ci/fuzz-nightly-skip.txt").write_text(skip_list, encoding="utf-8")
    for rel, content in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
    subprocess.run(["git", "init", "-q"], cwd=root, check=True)
    subprocess.run(["git", "config", "user.name", "Fixture"], cwd=root, check=True)
    subprocess.run(["git", "config", "user.email", "fixture@example.invalid"], cwd=root, check=True)
    subprocess.run(["git", "add", "."], cwd=root, check=True)
    subprocess.run(["git", "commit", "-q", "--no-gpg-sign", "-m", "fixture"], cwd=root, check=True)


def run_fuzz_nightly(
    files: dict[str, str],
    *,
    args: list[str] | None = None,
    env_extra: dict[str, str] | None = None,
    skip_list: str | None = None,
) -> subprocess.CompletedProcess[str]:
    with tempfile.TemporaryDirectory(prefix="fuzz-nightly-test-") as directory:
        root = Path(directory)
        init_fixture_repo(root, files, skip_list=skip_list)
        cmd = ["bash", str(root / "automation/ci/fuzz-nightly.sh"), *(args or [])]
        import os

        env = os.environ.copy()
        env["PIG_FUZZ_TIME"] = "1x"
        env["PIG_FUZZ_PARALLEL"] = "1"
        if env_extra:
            env.update(env_extra)
        return subprocess.run(cmd, cwd=root, env=env, capture_output=True, text=True, check=False)


BASE_FILES = {
    "go.mod": "module example.com/fuzzfixture\n\ngo 1.24\n",
    "untagged/u_test.go": (
        "package untagged\n\n"
        "import \"testing\"\n\n"
        "func FuzzUntagged(f *testing.F) {\n"
        "\tf.Add([]byte(\"a\"))\n"
        "\tf.Fuzz(func(t *testing.T, _ []byte) {})\n"
        "}\n"
    ),
    "tagged/t_test.go": (
        "//go:build parity\n\n"
        "package tagged\n\n"
        "import \"testing\"\n\n"
        "func FuzzTagged(f *testing.F) {\n"
        "\tf.Add([]byte(\"b\"))\n"
        "\tf.Fuzz(func(t *testing.T, _ []byte) {})\n"
        "}\n"
    ),
    "mixed/plain_test.go": (
        "package mixed\n\n"
        "import \"testing\"\n\n"
        "func FuzzMixedPlain(f *testing.F) {\n"
        "\tf.Add([]byte(\"c\"))\n"
        "\tf.Fuzz(func(t *testing.T, _ []byte) {})\n"
        "}\n"
    ),
    "mixed/gated_test.go": (
        "//go:build parity\n\n"
        "package mixed\n\n"
        "import \"testing\"\n\n"
        "func FuzzMixedGated(f *testing.F) {\n"
        "\tf.Add([]byte(\"d\"))\n"
        "\tf.Fuzz(func(t *testing.T, _ []byte) {})\n"
        "}\n"
    ),
}


class FuzzNightlyTests(unittest.TestCase):
    def test_tagged_untagged_and_mixed_packages_all_run(self) -> None:
        result = run_fuzz_nightly(BASE_FILES)
        self.assertEqual(0, result.returncode, f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}")
        self.assertNotIn("build constraints exclude all Go files", result.stderr)
        self.assertIn("== untagged FuzzUntagged ", result.stdout)
        self.assertIn("== tagged FuzzTagged ", result.stdout)
        self.assertIn("== mixed FuzzMixedPlain ", result.stdout)
        self.assertIn("== mixed FuzzMixedGated ", result.stdout)
        self.assertEqual(
            "fuzz-nightly: ran 4 targets, skipped 0 (named above), failed 0",
            result.stdout.strip().splitlines()[-1],
        )

    def test_list_mode_enumerates_every_target_without_running(self) -> None:
        result = run_fuzz_nightly(BASE_FILES, args=["--list"])
        self.assertEqual(0, result.returncode, f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}")
        self.assertIn("mixed\tFuzzMixedGated\tparity", result.stdout)
        self.assertIn("mixed\tFuzzMixedPlain\t-", result.stdout)
        self.assertIn("tagged\tFuzzTagged\tparity", result.stdout)
        self.assertIn("untagged\tFuzzUntagged\t-", result.stdout)
        self.assertEqual(
            "fuzz-nightly: listed 4 targets, skipped 0 (named above), failed 0",
            result.stdout.strip().splitlines()[-1],
        )

    def test_unsupported_build_expression_fails_loudly_naming_file(self) -> None:
        files = {
            **BASE_FILES,
            "complex/expr_test.go": (
                "//go:build parity && integration\n\n"
                "package complexpkg\n\n"
                "import \"testing\"\n\n"
                "func FuzzComplex(f *testing.F) {\n"
                "\tf.Fuzz(func(t *testing.T, _ []byte) {})\n"
                "}\n"
            ),
        }
        result = run_fuzz_nightly(files)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("complex/expr_test.go", result.stderr)
        self.assertIn("unsupported build constraint", result.stderr)

    def test_skip_list_skips_by_name_with_reason_and_counts(self) -> None:
        skip = "# package:target reason\ntagged:FuzzTagged needs external service not in nightly\n"
        result = run_fuzz_nightly(BASE_FILES, skip_list=skip)
        self.assertEqual(0, result.returncode, f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}")
        self.assertIn(
            "fuzz-nightly: skipped tagged:FuzzTagged: needs external service not in nightly",
            result.stdout,
        )
        self.assertNotIn("== tagged FuzzTagged ", result.stdout)
        self.assertEqual(
            "fuzz-nightly: ran 3 targets, skipped 1 (named above), failed 0",
            result.stdout.strip().splitlines()[-1],
        )

    def test_unknown_tagged_target_without_skip_entry_is_run_not_skipped(self) -> None:
        files = {
            **BASE_FILES,
            "broken/b_test.go": (
                "//go:build customtag\n\n"
                "package broken\n\n"
                "import \"testing\"\n\n"
                "func FuzzBroken(f *testing.F) {\n"
                "\tf.Add([]byte(\"x\"))\n"
                "\tf.Fuzz(func(t *testing.T, _ []byte) { t.Fatal(\"boom\") })\n"
                "}\n"
            ),
        }
        result = run_fuzz_nightly(files)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("== broken FuzzBroken ", result.stdout)
        self.assertIn("fuzz-nightly: failing target: broken:FuzzBroken", result.stderr)
        self.assertIn("fuzz-nightly: ran 5 targets, skipped 0 (named above), failed 1", result.stdout)

    def test_skip_list_entry_without_reason_fails_loudly(self) -> None:
        result = run_fuzz_nightly(BASE_FILES, skip_list="tagged:FuzzTagged\n")
        self.assertNotEqual(0, result.returncode)
        self.assertIn("tagged:FuzzTagged", result.stderr)

    def test_checked_in_skip_list_has_valid_entries(self) -> None:
        skip_path = SCRIPT.with_name("fuzz-nightly-skip.txt")
        self.assertTrue(skip_path.is_file(), f"missing {skip_path}")
        for lineno, raw in enumerate(skip_path.read_text(encoding="utf-8").splitlines(), start=1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split(None, 1)
            self.assertEqual(2, len(parts), f"{skip_path}:{lineno}: missing reason")
            self.assertIn(":", parts[0], f"{skip_path}:{lineno}: want <package>:<target>")
            self.assertTrue(parts[1].strip(), f"{skip_path}:{lineno}: empty reason")


if __name__ == "__main__":
    unittest.main()
