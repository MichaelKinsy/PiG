"""Regression tests for vet-tags.sh on synthetic repositories: no Go toolchain, network or real home access."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("vet-tags.sh")
LIST = "# rows\nvet integration\nprefix pig_strip_ make piglet-strip-build\nother tinygo make durable-contract\nnative linux\nplatform windows amd64 GOFLAGS=-v\nos-other darwin the macOS job\n"


def run_gate(files: dict[str, str], listing: str = LIST, mode: str = "check") -> subprocess.CompletedProcess:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "automation/ci").mkdir(parents=True)
        shutil.copy(SCRIPT, root / "automation/ci/vet-tags.sh")
        (root / "automation/ci/vet-tags.txt").write_text(listing, encoding="utf-8")
        for name, text in files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding="utf-8")
        return subprocess.run(["bash", str(root / "automation/ci/vet-tags.sh"), mode], capture_output=True, text=True, check=False)


class VetTagsTests(unittest.TestCase):
    base = {
        "a/a_test.go": "//go:build integration\n\npackage a\n",
        "b/b.go": "//go:build (linux || darwin) && !windows && go1.21 && goexperiment.jsonv2 && !race\n\npackage b\n",
        "c/c.go": "//go:build pig_strip_mcp || !pig_strip_docs\n\npackage c\n",
        "d/d.go": "//go:build !tinygo\n\npackage d\n",
        "e/testdata/e.go": "//go:build fixture_only\n\npackage e\n",
    }

    def test_listed_tags_pass(self):
        result = run_gate(self.base)
        self.assertEqual(0, result.returncode, result.stderr)

    def test_new_tag_without_a_row_fails(self):
        result = run_gate({**self.base, "f/f_test.go": "//go:build integration && newtag\n\npackage f\n"})
        self.assertEqual(1, result.returncode)
        self.assertIn('"newtag" appears', result.stderr)

    def test_negated_new_tag_fails(self):
        result = run_gate({**self.base, "f/f.go": "//go:build !newtag\n\npackage f\n"})
        self.assertEqual(1, result.returncode)
        self.assertIn('"newtag" appears', result.stderr)

    def test_row_no_file_names_fails(self):
        result = run_gate(self.base, LIST + "vet gone\n")
        self.assertEqual(1, result.returncode)
        self.assertIn('lists "gone"', result.stderr)

    def test_tags_lists_the_vet_rows(self):
        result = run_gate(self.base, LIST + "vet live\n", mode="tags")
        self.assertEqual("integration,live\n", result.stdout)

    def test_operating_system_without_a_row_fails(self):
        result = run_gate({**self.base, "g/g_plan9.go": "//go:build plan9\n\npackage g\n"})
        self.assertEqual(1, result.returncode)
        self.assertIn("\"plan9\"", result.stderr)

    def test_platforms_lists_the_platform_rows(self):
        result = run_gate(self.base, mode="platforms")
        self.assertEqual("GOOS=windows GOARCH=amd64 GOFLAGS=-v\n", result.stdout)


if __name__ == "__main__":
    unittest.main()
