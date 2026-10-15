# SPDX-License-Identifier: MIT
"""run-porter-tasks.sh commits what a headless Porter task left, completely and honestly.

Round 6 review (porter-4ac-durable c8124930f0): the first driver committed with `git add -u`, so a file the Porter created stayed
untracked and the branch did not compile in a clean checkout; it committed a timed-out task as ordinary output; and it never
checked that the tree builds."""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "automation/porter/run-porter-tasks.sh"

FAKE_MAKE = """#!/usr/bin/env bash
# fake `make porter-task TASK=...`: the task text decides what the "Porter" does
cat > /dev/null   # a real pig reads stdin: an inherited task list would be consumed here
task="$2"
echo "$task" >> "$FAKE_TASKS_RUN"
case "$task" in
  *newfile*) echo 'package caused' > internal_caused.go ;;&
  *edit*) echo changed >> tracked.txt ;;&
  *slow*) sleep 20 ;;
esac
exit 0
"""


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True, text=True).stdout


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        base = Path(self.tmp.name)
        self.repo = base / "repo"
        self.repo.mkdir()
        git(self.repo, "init", "-q", "-b", "main")
        git(self.repo, "config", "user.name", "T")
        git(self.repo, "config", "user.email", "t@example.com")
        git(self.repo, "config", "commit.gpgsign", "false")
        (self.repo / ".gitignore").write_text("bin/\ntmp/\n")
        (self.repo / "tracked.txt").write_text("base\n")
        git(self.repo, "add", "-A")
        git(self.repo, "commit", "-q", "-m", "base")
        self.shims = base / "shims"
        self.shims.mkdir()
        (self.shims / "make").write_text(FAKE_MAKE)
        (self.shims / "make").chmod(0o755)
        self.logs = base / "logs"
        self.runlog = base / "runlog.tsv"

    def run_tasks(self, tasks, **extra):
        tsv = Path(self.tmp.name) / "tasks.tsv"
        tsv.write_text("".join(f"closure\t{scope}\t3\n" for scope in tasks))
        self.ran = Path(self.tmp.name) / "ran.txt"
        env = {**os.environ, "PATH": f"{self.shims}:{os.environ['PATH']}", "PIG_PORTER_RUNLOG": str(self.runlog), "FAKE_TASKS_RUN": str(self.ran),
               "PIG_PORTER_TASK_TIMEOUT": "2", "PIG_PORTER_BUILD_CMD": "true", **extra}
        return subprocess.run([str(RUNNER), str(tsv), str(self.logs), str(self.repo)], env=env, capture_output=True, text=True, timeout=120)

    def last(self, fmt):
        return git(self.repo, "log", "-1", f"--format={fmt}").strip()

    def test_a_file_the_porter_created_is_committed_with_the_edit(self):
        result = self.run_tasks(["edit-newfile"])
        self.assertEqual(result.returncode, 0, result.stderr)
        files = git(self.repo, "show", "--name-only", "--format=", "HEAD").split()
        self.assertEqual(sorted(files), ["internal_caused.go", "tracked.txt"])
        self.assertEqual(git(self.repo, "status", "--porcelain").strip(), "", "nothing may stay untracked")

    def test_a_timed_out_task_is_committed_as_partial_not_as_output(self):
        self.run_tasks(["edit-slow"])
        self.assertIn("PARTIAL", self.last("%s"))
        self.assertIn("rc=124", self.last("%s"))

    def test_a_task_that_leaves_a_tree_that_does_not_build_is_committed_as_partial(self):
        self.run_tasks(["edit"], PIG_PORTER_BUILD_CMD="false")
        self.assertIn("PARTIAL", self.last("%s"))
        self.assertIn("build", self.last("%s"))

    def test_a_complete_task_says_so_and_carries_a_sign_off(self):
        self.run_tasks(["edit"])
        self.assertIn("complete", self.last("%s"))
        self.assertNotIn("PARTIAL", self.last("%s"))
        self.assertIn("Signed-off-by:", self.last("%B"))

    def test_a_task_that_changes_nothing_makes_no_commit(self):
        before = self.last("%H")
        self.run_tasks(["noop"])
        self.assertEqual(self.last("%H"), before)

    def test_the_runlog_records_status_and_the_task_stdin_is_not_the_task_list(self):
        self.run_tasks(["edit", "noop"])
        lines = self.runlog.read_text().splitlines()
        self.assertEqual(sum("PORTER end" in line for line in lines), 2, "both tasks ran: stdin must not feed the task list to the Porter")

    def test_a_task_that_reads_stdin_does_not_consume_the_task_list(self):
        """Mutant: dropping `< /dev/null` made the task's pig read the loop's task file and end the run after one task."""
        self.run_tasks(["edit-a", "edit-b", "edit-c"])
        self.assertEqual(len(self.ran.read_text().splitlines()), 3)

    def test_a_timed_out_task_whose_tree_also_fails_to_build_says_both(self):
        self.run_tasks(["edit-slow"], PIG_PORTER_BUILD_CMD="false")
        self.assertIn("rc=124", self.last("%s"))
        self.assertIn("build fails", self.last("%s"))

    def test_a_failed_commit_fails_the_task_and_the_runner_and_is_not_marked_done(self):
        """Review F2: `git commit || true` then .done let the next task's commit sweep up the uncommitted work under its own name."""
        hook = self.repo / ".git/hooks/pre-commit"
        hook.write_text("#!/bin/sh\nexit 1\n")
        hook.chmod(0o755)
        result = self.run_tasks(["edit-a", "edit-b"])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.ran.read_text().splitlines()), 1, "the next task must not run on top of uncommitted work")
        self.assertEqual(list(self.logs.glob("*.done")), [])
        self.assertIn("commit failed", result.stderr + (self.runlog.read_text() if self.runlog.exists() else ""))

    def test_a_dirty_tree_stops_the_runner_before_the_first_task(self):
        """Review F3: a task's commit must contain only that task's work."""
        (self.repo / "tracked.txt").write_text("someone else's edit\n")
        result = self.run_tasks(["edit-a"])
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.ran.exists() and self.ran.read_text(), "no task may run on a dirty tree")
        self.assertIn("not clean", result.stderr)

    def test_untracked_files_also_make_the_tree_dirty(self):
        (self.repo / "stray.go").write_text("package x\n")
        self.assertNotEqual(self.run_tasks(["edit-a"]).returncode, 0)

    def test_the_gap_rows_of_every_group_are_frozen_before_the_first_task(self):
        """Review F1: parts of a group slice the rows frozen before any task ran, not the baseline a closed task shrinks."""
        (self.repo / "automation/pin").mkdir(parents=True)
        (self.repo / "automation/pin/pin.py").write_text("print('c' * 40)\n")
        (self.repo / "automation/pin/follow.py").write_text("import os, sys\nopen(os.environ['FAKE_FREEZE_OUT'], 'a').write(' '.join(sys.argv[1:]) + '\\n')\n")
        git(self.repo, "add", "-A")
        git(self.repo, "commit", "-q", "-m", "tooling")
        out = Path(self.tmp.name) / "freeze.txt"
        self.run_tasks(["edit-a", "edit-b"], FAKE_FREEZE_OUT=str(out))
        self.assertEqual(out.read_text().splitlines(), ["c" * 40 + " --freeze-rows"], "frozen once, before the first task")

    def foreign_branch(self, content="package caused\n"):
        """Another lane's unmerged branch that already holds internal_caused.go."""
        git(self.repo, "checkout", "-q", "-b", "other-lane")
        (self.repo / "internal_caused.go").write_text(content)
        git(self.repo, "add", "-A")
        git(self.repo, "commit", "-q", "-m", "other lane's work")
        git(self.repo, "update-ref", "refs/remotes/staging/team/other-lane", "HEAD")
        git(self.repo, "checkout", "-q", "-")
        git(self.repo, "branch", "-q", "-D", "other-lane")

    def test_a_new_file_byte_identical_to_another_lanes_unmerged_branch_is_not_committed(self):
        """Review R10-1: the durable delta task landed 7 files byte-identical to dflip-rest's unmerged work in a PARTIAL commit."""
        self.foreign_branch()
        result = self.run_tasks(["edit-newfile", "edit-b"], PIG_PORTER_FOREIGN_MIN_BYTES="1")
        self.assertEqual(result.returncode, 6, result.stderr)
        self.assertIn("internal_caused.go", result.stderr)
        self.assertIn("other-lane", result.stderr)
        self.assertEqual(git(self.repo, "log", "--format=%s", "-1").strip(), "base", "nothing was committed")
        self.assertEqual(list(self.logs.glob("*.done")), [])
        self.assertEqual(len(self.ran.read_text().splitlines()), 1)

    def test_a_new_file_that_differs_from_the_other_lanes_copy_is_committed(self):
        self.foreign_branch("package something_else\n")
        self.assertEqual(self.run_tasks(["edit-newfile"], PIG_PORTER_FOREIGN_MIN_BYTES="1").returncode, 0)

    def test_a_small_identical_file_is_committed_under_the_default_size_floor(self):
        """A file under PIG_PORTER_FOREIGN_MIN_BYTES (default 64) is too short to show copying: two lanes write the same `package caused` line independently."""
        self.foreign_branch()
        result = self.run_tasks(["edit-newfile"])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotEqual(git(self.repo, "log", "--format=%s", "-1").strip(), "base", "the task was committed")

    def test_a_host_with_a_signing_key_gets_signed_commits(self):
        """Mutant found live: the runner put -S before `commit` ("unknown option: -S"); `|| true` hid it and every task's work stayed staged.
        The fixture hosts no key, so the signing path was never run."""
        key = Path(self.tmp.name) / "key"
        keygen = subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], capture_output=True)
        if keygen.returncode != 0:
            self.skipTest("ssh-keygen is not available")
        git(self.repo, "config", "gpg.format", "ssh")
        git(self.repo, "config", "user.signingkey", str(key))
        git(self.repo, "config", "commit.gpgsign", "true")
        result = self.run_tasks(["edit-newfile"])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("gpgsig", git(self.repo, "cat-file", "commit", "HEAD"))
        self.assertEqual(git(self.repo, "status", "--porcelain").strip(), "")

    def test_a_finished_task_is_not_rerun(self):
        self.run_tasks(["edit"])
        count = len(git(self.repo, "log", "--format=%h").split())
        self.run_tasks(["edit"])
        self.assertEqual(len(git(self.repo, "log", "--format=%h").split()), count)

    def test_commit_only_commits_the_tree_an_orphaned_task_left(self):
        (self.repo / "tracked.txt").write_text("edited\n")
        (self.repo / "new_file.go").write_text("package x\n")
        env = {**os.environ, "PIG_PORTER_BUILD_CMD": "true"}
        subprocess.run([str(RUNNER), "--commit-only", str(self.logs), str(self.repo), "closure", "a/b.ts", "124"], env=env, check=True, capture_output=True, timeout=60)
        self.assertIn("PARTIAL (rc=124)", self.last("%s"))
        self.assertEqual(git(self.repo, "status", "--porcelain").strip(), "")


if __name__ == "__main__":
    unittest.main()
