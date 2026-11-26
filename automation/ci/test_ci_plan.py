# SPDX-License-Identifier: MIT
"""Unit tests for automation/ci/ci-plan.sh, ci-tiers.txt, and light PR safety guards."""
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CI_PLAN = ROOT / "automation" / "ci" / "ci-plan.sh"
CI_TIERS = ROOT / "automation" / "ci" / "ci-tiers.txt"
CLASSIFY = ROOT / "automation" / "ci" / "classify-changes.sh"
TEST_GROUPED = ROOT / "automation" / "ci" / "test-grouped.sh"
WIN_STRESS = ROOT / "automation" / "ci" / "windows-concurrency-stress.sh"


def run_plan(event: str, mode: str = "", requested: str = "", table: Path | None = None) -> dict:
    cmd = ["bash", str(CI_PLAN), event, mode, requested]
    if table is not None:
        cmd.append(str(table))
    out = subprocess.check_output(cmd, cwd=ROOT, text=True)
    result: dict = {"run": {}}
    for line in out.strip().splitlines():
        key, _, value = line.partition("=")
        if key in ("full", "full-required"):
            result[key] = value == "true"
        elif key.startswith("run-"):
            result["run"][key.removeprefix("run-")] = value == "true"
        else:
            result[key] = json.loads(value)
    return result


class CIPlanTest(unittest.TestCase):
    def test_label_mode_always_sets_full_required(self):
        light = run_plan("pull_request", "label", "false")
        self.assertFalse(light["full"])
        self.assertTrue(light["full-required"])

        labeled = run_plan("pull_request", "label", "true")
        self.assertTrue(labeled["full"])
        self.assertTrue(labeled["full-required"])

        # Non-PR events always run full and do not require a PR label.
        for event in ("merge_group", "schedule", "workflow_dispatch", "push"):
            plan = run_plan(event, "label", "")
            self.assertTrue(plan["full"], event)
            self.assertFalse(plan["full-required"], event)

    def test_portmap_shards_are_queue_only_in_light_pr_plan(self):
        light = run_plan("pull_request", "label", "false")
        full = run_plan("merge_group", "", "")
        light_shards = [entry["shard"] for entry in light["linux"]]
        full_portmap = [entry["shard"] for entry in full["linux"] if entry["shard"].startswith("portmap-")]
        self.assertEqual(full_portmap, [f"portmap-{i}" for i in range(1, 13)])
        for shard in full_portmap:
            self.assertNotIn(shard, light_shards, f"{shard} must stay in queue tier, not pr")

    def test_light_pr_plan_includes_required_fast_rows(self):
        light = run_plan("pull_request", "label", "false")
        targets = {entry["shard"]: entry["target"] for entry in light["linux"]}
        self.assertEqual(targets.get("test-fast"), "ci-test-changed")
        self.assertEqual(targets.get("build"), "ci-build-pr")
        self.assertEqual(targets.get("checks"), "ci-checks-pr")
        self.assertEqual(targets.get("contracts"), "ci-contracts")
        self.assertEqual(light["windows"], [])
        self.assertEqual(light["macos"], [])

    def test_portmap_inputs_promote_to_full_run_via_classify_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            git_base = [
                "git",
                "-c", "user.name=t",
                "-c", "user.email=t@example.invalid",
                "-c", "commit.gpgsign=false",
                "-c", f"core.hooksPath={os.devnull}",
            ]

            def git(*args: str) -> str:
                return subprocess.check_output([*git_base, *args], cwd=repo, text=True).strip()

            def commit_file(rel: str, content: str) -> str:
                target = repo / rel
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content, encoding="utf-8")
                git("add", "-A")
                git("commit", "-q", "-m", rel)
                return git("rev-parse", "HEAD")

            git("init", "-q")
            base = commit_file("README.md", "base\n")
            neutral = commit_file("coding/plain.go", "package coding\n")
            portmap_doc = commit_file("docs/parity/PORT_MAP.md", "| a | b |\n")
            portmap_tool = commit_file("test/parity/cmd/portmapcheck/main.go", "package main\n")

            def classify(frm: str, to: str) -> dict[str, str]:
                out = subprocess.check_output(["bash", str(CLASSIFY), "pull_request", frm, to], cwd=repo, text=True)
                return dict(line.split("=", 1) for line in out.strip().splitlines())

            self.assertEqual(classify(base, neutral)["os-specific"], "false")
            self.assertEqual(classify(neutral, portmap_doc)["os-specific"], "true")
            self.assertEqual(classify(portmap_doc, portmap_tool)["os-specific"], "true")

    def test_test_grouped_declares_tree_wide_guard_packages(self):
        text = TEST_GROUPED.read_text(encoding="utf-8")
        for pkg in ("./internal/testenv", "./test/ci-images", "./test/docs-drift"):
            self.assertIn(pkg, text, f"test-grouped.sh must include guard package {pkg}")
        self.assertIn("GUARD_PKGS", text)

    def test_windows_concurrency_stress_dry_run(self):
        out = subprocess.check_output(["bash", str(WIN_STRESS), "--dry-run"], cwd=ROOT, text=True)
        for pkg in (
            "./internal/atomicfile",
            "./internal/pilock",
            "./ai",
            "./internal/childwait",
            "./internal/chord/winpipe",
            "./coding/extension/host/subprocess",
            "./coding/cli",
        ):
            self.assertIn(pkg, out)


    def test_portmap_path_gating_fails_closed(self):
        env_false = {**os.environ, "PORTMAP_REQUESTED": "false"}
        out = subprocess.check_output(
            ["bash", str(CI_PLAN), "pull_request", "", ""],
            cwd=ROOT,
            env=env_false,
            text=True,
        )
        linux = json.loads(dict(line.split("=", 1) for line in out.strip().splitlines())["linux"])
        self.assertEqual([e["shard"] for e in linux if e["shard"].startswith("portmap-")], [])

        for event in ("merge_group", "schedule", "workflow_dispatch", "push"):
            out = subprocess.check_output(
                ["bash", str(CI_PLAN), event, "", ""],
                cwd=ROOT,
                env=env_false,
                text=True,
            )
            linux = json.loads(dict(line.split("=", 1) for line in out.strip().splitlines())["linux"])
            self.assertEqual(
                [e["shard"] for e in linux if e["shard"].startswith("portmap-")],
                [f"portmap-{i}" for i in range(1, 13)],
                event,
            )

        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            git_base = [
                "git",
                "-c", "user.name=t",
                "-c", "user.email=t@example.invalid",
                "-c", "commit.gpgsign=false",
                "-c", f"core.hooksPath={os.devnull}",
            ]

            def git(*args: str) -> str:
                return subprocess.check_output([*git_base, *args], cwd=repo, text=True).strip()

            def commit_file(rel: str, content: str) -> str:
                target = repo / rel
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content, encoding="utf-8")
                git("add", "-A")
                git("commit", "-q", "-m", rel)
                return git("rev-parse", "HEAD")

            git("init", "-q")
            base = commit_file("README.md", "base\n")
            ordinary = commit_file("coding/plain.go", "package coding\n")
            unknown = commit_file("brand_new_surface/file.txt", "hello\n")
            marker = commit_file("coding/plain_test.go", "package coding\n// pi: packages/ai/src/x.ts\n")

            def classify(frm: str, to: str) -> dict[str, str]:
                out = subprocess.check_output(["bash", str(CLASSIFY), "pull_request", frm, to], cwd=repo, text=True)
                return dict(line.split("=", 1) for line in out.strip().splitlines())

            self.assertEqual(classify(base, ordinary)["portmap"], "false")
            self.assertEqual(classify(ordinary, unknown)["portmap"], "true")
            self.assertEqual(classify(unknown, marker)["portmap"], "true")


if __name__ == "__main__":
    unittest.main()
