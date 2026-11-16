# SPDX-License-Identifier: MIT
"""secret-scan-range.sh must scan a pull request over what it adds to its base, including the commits of merged member branches.

The batch shape: main has commit M (a file holding a secret-shaped test fixture), a member branch was cut BEFORE M and adds a
secret in its own commit, and a batch branch cut from main after M merges that member. gitleaks-action scanned the range
"<oldest pull request commit>^..<head>" with --first-parent: that range reaches back past M (main's own, already scanned
history), and --first-parent hides the member's commits behind the merge. Temporary git repositories only."""
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


if __name__ == "__main__":
    unittest.main()
