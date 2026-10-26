# SPDX-License-Identifier: MIT
"""Tests for automation/ci/autofix.py: the producer's eligibility rules and the privileged side's validation of an untrusted artifact."""
from __future__ import annotations

import base64
import hashlib
import io
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import autofix  # noqa: E402

HEAD = "a" * 40
GATES = """# comment
divergences-drift | make divergences
coverage-drift | make coverage RESULTS=
"""
PATHS = """docs/parity/DIVERGENCES.md
test/parity/coverage.md
test/parity/interfaces/draft-v*.json
"""

FAKE_MAKE = r"""#!/bin/sh
# Stands in for make: a generator target writes a file; a gate target fails until its file has the regenerated content.
case "$1" in
  divergences) mkdir -p docs/parity; echo fresh > docs/parity/DIVERGENCES.md ;;
  coverage) echo fresh > test/parity/coverage.md ;;
  stray) echo x > outside.txt; echo fresh > test/parity/coverage.md ;;
  noop) : ;;
  -k)
    shift 3
    status=0
    for gate in "$@"; do
      case "$gate" in
        divergences-drift) grep -qx fresh docs/parity/DIVERGENCES.md 2>/dev/null || status=1 ;;
        coverage-drift) grep -qx fresh test/parity/coverage.md 2>/dev/null || status=1 ;;
        *) status=1 ;;
      esac
    done
    exit $status ;;
  *) exit 2 ;;
esac
"""


def git(root: Path, *args: str) -> None:
    subprocess.run(["git", "-C", str(root), *args], check=True, capture_output=True)


class WorkTree(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name) / "repo"
        (self.root / "docs/parity").mkdir(parents=True)
        (self.root / "test/parity").mkdir(parents=True)
        (self.root / "docs/parity/DIVERGENCES.md").write_text("stale\n")
        (self.root / "test/parity/coverage.md").write_text("stale\n")
        git(self.root, "init", "-q")
        git(self.root, "-c", "user.name=t", "-c", "user.email=t@t", "add", "-A")
        git(self.root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
        bin_dir = Path(self.dir.name) / "bin"
        bin_dir.mkdir()
        make = bin_dir / "make"
        make.write_text(FAKE_MAKE)
        make.chmod(0o755)
        self.gates = Path(self.dir.name) / "gates.txt"
        self.gates.write_text(GATES)
        self.paths = Path(self.dir.name) / "paths.txt"
        self.paths.write_text(PATHS)
        self.old_root, self.old_path = autofix.ROOT, os.environ["PATH"]
        autofix.ROOT = self.root
        os.environ["PATH"] = f"{bin_dir}{os.pathsep}{self.old_path}"
        os.environ.pop("GITHUB_OUTPUT", None)
        self.addCleanup(self.restore)

    def restore(self) -> None:
        autofix.ROOT = self.old_root
        os.environ["PATH"] = self.old_path

    def prepare(self, log: str) -> tuple[int, str, Path]:
        log_file = Path(self.dir.name) / "bookkeeping.log"
        log_file.write_text(log)
        out = Path(self.dir.name) / "artifact"
        output = Path(self.dir.name) / "output"
        os.environ["GITHUB_OUTPUT"] = str(output)
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = autofix.main(["prepare", "--log", str(log_file), "--out", str(out), "--head-sha", HEAD, "--gates", str(self.gates), "--paths", str(self.paths)])
        return code, output.read_text() if output.exists() else "", out


class ParseTests(unittest.TestCase):
    def test_failed_gates_come_from_make_errors_once_each(self) -> None:
        log = "ok\nmake: *** [Makefile:480: divergence-guard] Error 1\nmake: *** [automation/make/parity.mk:658: coverage-drift] Error 1\nmake: *** [Makefile:480: divergence-guard] Error 1\n"
        self.assertEqual(autofix.failed_gates(log), ["divergence-guard", "coverage-drift"])
        self.assertEqual(autofix.failed_gates("all gates passed\n"), [])

    def test_path_patterns(self) -> None:
        patterns = PATHS.split()
        self.assertTrue(autofix.path_allowed("docs/parity/DIVERGENCES.md", patterns))
        self.assertTrue(autofix.path_allowed("test/parity/interfaces/draft-v1.4.0+74f46864.json", patterns))
        for bad in ["docs/parity/DIVERGENCES.md/x", "test/parity/interfaces/sub/draft-v1.json", "test/parity/interfaces/draft-v1.txt", "../docs/parity/DIVERGENCES.md", "/docs/parity/DIVERGENCES.md", "docs//parity/DIVERGENCES.md", ".github/workflows/ci.yml", "docs/parity/./DIVERGENCES.md"]:
            self.assertFalse(autofix.path_allowed(bad, patterns), bad)

    def test_the_committed_tables_are_well_formed(self) -> None:
        gates = autofix.read_table(autofix.GATES_FILE)
        self.assertTrue(gates)
        patterns = autofix.read_paths(autofix.PATHS_FILE)
        self.assertFalse([p for p in patterns if p.startswith(".github/workflows")])
        # Every gate is a bookkeeping gate, so its failure reaches this tool's log.
        makefile = (autofix.ROOT / "automation/make/ci.mk").read_text()
        line = next(l for l in makefile.splitlines() if l.startswith("ci-bookkeeping:"))
        bookkeeping = line.split(":", 1)[1].split()
        for gate, _ in gates:
            self.assertIn(gate, bookkeeping)


class PrepareTests(WorkTree):
    def test_a_stale_derived_file_becomes_an_artifact(self) -> None:
        code, output, out = self.prepare("make: *** [Makefile:1: divergences-drift] Error 1\n")
        self.assertEqual(code, 0)
        self.assertIn("eligible=true", output)
        manifest = json.loads((out / "manifest.json").read_text())
        self.assertEqual(manifest["gates"], ["divergences-drift"])
        self.assertEqual([f["path"] for f in manifest["files"]], ["docs/parity/DIVERGENCES.md"])
        self.assertEqual((out / "files/docs/parity/DIVERGENCES.md").read_text(), "fresh\n")

    def test_two_gates_run_their_generators_in_table_order(self) -> None:
        code, output, out = self.prepare("make: *** [a.mk:9: coverage-drift] Error 1\nmake: *** [Makefile:1: divergences-drift] Error 1\n")
        self.assertIn("eligible=true", output)
        manifest = json.loads((out / "manifest.json").read_text())
        self.assertEqual(sorted(f["path"] for f in manifest["files"]), ["docs/parity/DIVERGENCES.md", "test/parity/coverage.md"])

    def test_another_failure_stops_autofix_for_the_whole_run(self) -> None:
        for log in ["make: *** [Makefile:1: divergences-drift] Error 1\nmake: *** [Makefile:2: port-map-drift] Error 2\n", "", "setup failed\n"]:
            code, output, out = self.prepare(log)
            self.assertEqual(code, 0)
            self.assertIn("eligible=false", output)
            self.assertFalse((out / "manifest.json").exists())
        self.assertEqual((self.root / "docs/parity/DIVERGENCES.md").read_text(), "stale\n")

    def test_a_gate_that_still_fails_after_regeneration_is_not_fixed(self) -> None:
        self.gates.write_text("divergences-drift | make divergences\ncoverage-drift | make noop\n")
        code, output, out = self.prepare("make: *** [a.mk:9: coverage-drift] Error 1\n")
        self.assertIn("eligible=false", output)
        self.assertFalse((out / "manifest.json").exists())

    def test_a_generator_that_writes_outside_the_derived_paths_is_refused(self) -> None:
        self.gates.write_text("divergences-drift | make divergences\ncoverage-drift | make stray\n")
        code, output, out = self.prepare("make: *** [a.mk:9: coverage-drift] Error 1\n")
        self.assertIn("eligible=false", output)
        self.assertFalse((out / "manifest.json").exists())

    def test_an_unclean_tree_is_not_regenerated(self) -> None:
        (self.root / "leftover.txt").write_text("x")
        code, output, out = self.prepare("make: *** [Makefile:1: divergences-drift] Error 1\n")
        self.assertIn("eligible=false", output)


class ValidateTests(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.base = Path(self.dir.name)
        self.artifact = self.base / "artifact"
        self.gates = self.base / "gates.txt"
        self.gates.write_text(GATES)
        self.paths = self.base / "paths.txt"
        self.paths.write_text(PATHS)
        self.write_artifact({"docs/parity/DIVERGENCES.md": b"fresh\n"})

    def write_artifact(self, files: dict[str, bytes], deleted: list[str] | None = None, **override) -> None:
        for path, data in files.items():
            target = self.artifact / "files" / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
        manifest = {
            "head_sha": HEAD,
            "gates": ["divergences-drift"],
            "files": [{"path": p, "bytes": len(d), "sha256": hashlib.sha256(d).hexdigest()} for p, d in files.items()],
            "deleted": deleted or [],
        }
        manifest.update(override)
        (self.artifact / "manifest.json").write_text(json.dumps(manifest))

    def validate(self, head: str = HEAD) -> tuple[int, str]:
        out = self.base / "request.json"
        out.unlink(missing_ok=True)
        err = io.StringIO()
        with redirect_stdout(io.StringIO()), redirect_stderr(err):
            code = autofix.main(["validate", "--artifact", str(self.artifact), "--paths", str(self.paths), "--gates", str(self.gates), "--repo", "o/r", "--branch", "feature", "--expected-head", head, "--out", str(out)])
        return code, err.getvalue()

    def test_a_good_artifact_becomes_a_signed_off_commit_request(self) -> None:
        code, err = self.validate()
        self.assertEqual(code, 0, err)
        request = json.loads((self.base / "request.json").read_text())
        variables = request["variables"]["input"]
        self.assertEqual(variables["branch"], {"repositoryNameWithOwner": "o/r", "branchName": "feature"})
        self.assertEqual(variables["expectedHeadOid"], HEAD)
        self.assertIn("Signed-off-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>", variables["message"]["body"])
        self.assertIn(autofix.TRAILER, variables["message"]["body"])
        self.assertEqual(variables["fileChanges"]["additions"], [{"path": "docs/parity/DIVERGENCES.md", "contents": base64.b64encode(b"fresh\n").decode()}])

    def assert_refused(self, fragment: str, head: str = HEAD) -> None:
        code, err = self.validate(head)
        self.assertEqual(code, 1, err)
        self.assertIn(fragment, err)
        self.assertFalse((self.base / "request.json").exists())

    def test_a_branch_that_moved_is_refused(self) -> None:
        self.assert_refused("branch head", head="b" * 40)

    def test_a_path_outside_the_allowlist_is_refused(self) -> None:
        self.write_artifact({".github/workflows/ci.yml": b"on: push\n"})
        self.assert_refused("not an allowed derived-file path")

    def test_traversal_in_a_path_is_refused(self) -> None:
        self.write_artifact({"docs/parity/DIVERGENCES.md": b"x"}, deleted=["../x"])
        self.assert_refused("not an allowed derived-file path")

    def test_a_symlinked_file_is_refused(self) -> None:
        target = self.artifact / "files/docs/parity/DIVERGENCES.md"
        target.unlink()
        target.symlink_to("/etc/passwd")
        self.assert_refused("not a plain file")

    def test_a_symlinked_directory_is_refused(self) -> None:
        self.write_artifact({"test/parity/coverage.md": b"fresh\n"})
        parent = self.artifact / "files/test/parity"
        (self.artifact / "files/test/parity/coverage.md").rename(self.base / "coverage.md")
        parent.rmdir()
        parent.symlink_to(self.base)
        self.assert_refused("not a plain directory")

    def test_a_changed_file_digest_is_refused(self) -> None:
        (self.artifact / "files/docs/parity/DIVERGENCES.md").write_bytes(b"other content\n")
        self.assert_refused("size")
        (self.artifact / "files/docs/parity/DIVERGENCES.md").write_bytes(b"fresh!")
        self.assert_refused("digest")

    def test_a_gate_with_no_generator_is_refused(self) -> None:
        self.write_artifact({"docs/parity/DIVERGENCES.md": b"fresh\n"}, gates=["port-map-drift"])
        self.assert_refused("no generator")

    def test_an_oversized_file_is_refused(self) -> None:
        old = autofix.MAX_FILE_BYTES
        autofix.MAX_FILE_BYTES = 3
        self.addCleanup(setattr, autofix, "MAX_FILE_BYTES", old)
        self.assert_refused("exceeds the limit")

    def test_extra_manifest_keys_are_refused(self) -> None:
        self.write_artifact({"docs/parity/DIVERGENCES.md": b"fresh\n"}, extra="x")
        self.assert_refused("unexpected keys")

    def test_the_fork_comment_names_only_trusted_commands(self) -> None:
        self.write_artifact({"docs/parity/DIVERGENCES.md": b"fresh\n"}, gates=["divergences-drift", "rm -rf /"])
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = autofix.main(["comment", "--artifact", str(self.artifact), "--gates", str(self.gates)])
        self.assertEqual(code, 0)
        self.assertIn("make divergences", buffer.getvalue())
        self.assertNotIn("rm -rf", buffer.getvalue())

    def test_an_executable_bit_cannot_be_smuggled(self) -> None:
        # The artifact carries content only; the commit creates plain 100644 files.
        mode = (self.artifact / "files/docs/parity/DIVERGENCES.md").stat().st_mode
        self.assertFalse(mode & stat.S_IXUSR)


class DCOTests(unittest.TestCase):
    """automation/ci/check-dco.sh accepts the autofix commit's identity and sign-off, and nobody else's use of them."""

    def commit(self, root: Path, author: str, email: str, message: str) -> str:
        env = dict(os.environ, GIT_AUTHOR_NAME=author, GIT_AUTHOR_EMAIL=email, GIT_COMMITTER_NAME="GitHub", GIT_COMMITTER_EMAIL="noreply@github.com")
        (root / "f").write_text(message)
        subprocess.run(["git", "-C", str(root), "add", "f"], check=True, env=env)
        subprocess.run(["git", "-C", str(root), "commit", "-q", "-m", message], check=True, env=env)
        return subprocess.run(["git", "-C", str(root), "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()

    def check(self, author: str, email: str, message: str) -> int:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            git(root, "init", "-q")
            base = self.commit(root, "Human", "human@example.com", "base\n\nSigned-off-by: Human <human@example.com>")
            head = self.commit(root, author, email, message)
            script = Path(__file__).resolve().parent / "check-dco.sh"
            return subprocess.run([str(script), base, head], cwd=root, capture_output=True).returncode

    def test_the_autofix_commit_is_signed_off(self) -> None:
        _, body = autofix.commit_message(["divergences-drift"])
        self.assertEqual(self.check(autofix.BOT_NAME, autofix.BOT_EMAIL, f"Regenerate stale derived files\n\n{body}"), 0)

    def test_another_author_cannot_borrow_the_bots_sign_off(self) -> None:
        _, body = autofix.commit_message(["divergences-drift"])
        self.assertEqual(self.check("Mallory", "mallory@example.com", f"Change\n\n{body}"), 1)


if __name__ == "__main__":
    unittest.main()
