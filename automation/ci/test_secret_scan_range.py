# SPDX-License-Identifier: MIT
"""secret-scan-range.sh must scan a pull request over what it adds to its base, including the commits of merged member branches.

The batch shape: main has commit M (a file holding a secret-shaped test fixture), a member branch was cut BEFORE M and adds a
secret in its own commit, and a batch branch cut from main after M merges that member. gitleaks-action scanned the range
"<oldest pull request commit>^..<head>" with --first-parent: that range reaches back past M (main's own, already scanned
history), and --first-parent hides the member's commits behind the merge. It must also fail closed: a range it cannot compute exits 2 with one stderr line and nothing on stdout. Temporary git repositories only."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("secret-scan-range.sh")


class SecretScanRangeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        self.env = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull,
                    "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
                    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"}
        self.git("init", "-q", "-b", "main")
        self.git("config", "commit.gpgsign", "false")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, env=self.env, check=True, text=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.strip()

    def commit(self, name, text):
        (self.repo / name).write_text(text)
        self.git("add", name)
        self.git("commit", "-q", "-m", name)
        return self.git("rev-parse", "HEAD")

    def build_batch(self):
        a = self.commit("a.txt", "a\n")
        self.git("checkout", "-q", "-b", "member")
        first = self.commit("member-one.txt", "one\n")
        leak = self.commit("member-secret.txt", "a secret added on the member branch\n")
        self.git("checkout", "-q", "main")
        m = self.commit("main-fixture.txt", "a secret-shaped fixture already on main\n")
        self.git("checkout", "-q", "-b", "batch")
        self.git("merge", "-q", "--no-ff", "-m", "merge member", "member")
        return {"a": a, "first": first, "leak": leak, "m": m, "base": m, "head": self.git("rev-parse", "HEAD")}

    def scanned(self, log_opts):
        return set(self.git("log", "--format=%H", *log_opts).split())

    def test_range_is_what_the_pull_request_adds_over_its_base(self):
        c = self.build_batch()
        expr = subprocess.run([str(SCRIPT), c["base"], c["head"]], cwd=self.repo, env=self.env, check=True, text=True,
                              stdout=subprocess.PIPE).stdout.strip()
        self.assertEqual(f"{c['m']}..{c['head']}", expr)
        scanned = self.scanned(["--no-merges", expr])
        self.assertNotIn(c["m"], scanned, "main's own commit is rescanned")
        self.assertIn(c["first"], scanned)
        self.assertIn(c["leak"], scanned, "a secret added on the merged member branch is not scanned")

    def test_old_rule_rescans_main_and_hides_the_member(self):
        c = self.build_batch()
        old = ["--no-merges", "--first-parent", f"{c['first']}^..{c['head']}"]
        scanned = self.scanned(old)
        self.assertIn(c["m"], scanned)
        self.assertNotIn(c["leak"], scanned)

    def test_first_parent_alone_hides_member_commits_in_the_new_range(self):
        c = self.build_batch()
        expr = f"{c['m']}..{c['head']}"
        self.assertNotIn(c["leak"], self.scanned(["--no-merges", "--first-parent", expr]))
        self.assertIn(c["leak"], self.scanned(["--no-merges", expr]))

    def run_script(self, *args, cwd=None):
        return subprocess.run([str(SCRIPT), *args], cwd=cwd or self.repo, env=self.env, text=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def assert_fails_closed(self, result):
        self.assertEqual(2, result.returncode, result.stderr)
        self.assertEqual("", result.stdout, "a failure must print no range on stdout")
        self.assertEqual(1, len(result.stderr.strip().splitlines()), result.stderr)
        self.assertIn("secret-scan-range.sh:", result.stderr)

    def build_two_commits(self):
        first = self.commit("one.txt", "one\n")
        second = self.commit("two.txt", "two\n")
        return first, second

    def test_normal_range_is_two_full_shas(self):
        c = self.build_batch()
        result = self.run_script(c["base"], c["head"])
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertRegex(result.stdout, r"^[0-9a-f]{40}\.\.[0-9a-f]{40}\n$")

    def test_range_resolves_refs_to_full_shas(self):
        c = self.build_batch()
        result = self.run_script("main", "batch")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(f"{c['m']}..{c['head']}\n", result.stdout)

    def test_absent_base_fails_closed(self):
        _, second = self.build_two_commits()
        self.assert_fails_closed(self.run_script("0" * 40, second))

    def test_unknown_base_ref_fails_closed(self):
        _, second = self.build_two_commits()
        self.assert_fails_closed(self.run_script("no-such-ref", second))

    def test_absent_head_fails_closed(self):
        first, _ = self.build_two_commits()
        self.assert_fails_closed(self.run_script(first, "f" * 40))

    def test_non_commit_object_fails_closed(self):
        first, _ = self.build_two_commits()
        tree = self.git("rev-parse", "HEAD^{tree}")
        self.assert_fails_closed(self.run_script(tree, first))

    def test_option_shaped_base_fails_closed(self):
        _, second = self.build_two_commits()
        self.assert_fails_closed(self.run_script("--all", second))

    def test_unrelated_histories_fail_closed(self):
        _, second = self.build_two_commits()
        self.git("checkout", "-q", "--orphan", "other")
        other = self.commit("other.txt", "unrelated\n")
        self.assert_fails_closed(self.run_script(second, other))

    def test_shallow_clone_lacking_the_base_fails_closed(self):
        first, _ = self.build_two_commits()
        self.git("checkout", "-q", "-b", "pr")
        head = self.commit("three.txt", "three\n")
        shallow = Path(self.tmp.name) / "shallow"
        subprocess.run(["git", "clone", "-q", "--depth", "1", "--branch", "pr", f"file://{self.repo}", str(shallow)],
                       env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assert_fails_closed(self.run_script(first, head, cwd=shallow))

    def test_shallow_clone_lacking_the_fork_point_fails_closed(self):
        self.commit("root.txt", "root\n")
        self.git("checkout", "-q", "-b", "pr")
        self.commit("pr.txt", "pr\n")
        head = self.git("rev-parse", "HEAD")
        self.git("checkout", "-q", "main")
        self.commit("main-one.txt", "one\n")
        base = self.commit("main-two.txt", "two\n")
        shallow = Path(self.tmp.name) / "shallow"
        subprocess.run(["git", "clone", "-q", "--depth", "1", "--branch", "main", f"file://{self.repo}", str(shallow)],
                       env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        subprocess.run(["git", "fetch", "-q", "--depth", "1", "origin", head], cwd=shallow, env=self.env, check=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assert_fails_closed(self.run_script(base, head, cwd=shallow))

    def test_unset_and_empty_inputs_fail_closed(self):
        _, second = self.build_two_commits()
        for args in ([], [second], ["", second], [second, ""], ["", ""], [second, second, second]):
            with self.subTest(args=args):
                self.assert_fails_closed(self.run_script(*args))

    def test_base_equal_to_head_is_nothing_to_scan(self):
        _, second = self.build_two_commits()
        result = self.run_script(second, second)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(f"{second}..{second}\n", result.stdout)
        self.assertEqual([], self.git("log", "--format=%H", result.stdout.strip()).split())

    def test_head_already_in_base_is_nothing_to_scan(self):
        first, second = self.build_two_commits()
        result = self.run_script(second, first)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(f"{first}..{first}\n", result.stdout)


if __name__ == "__main__":
    unittest.main()
