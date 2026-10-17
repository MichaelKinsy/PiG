# SPDX-License-Identifier: MIT
"""The Porter launchers run in a hermetic agent directory: the operator's auth and model choice, never the operator's packages or extensions."""
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LAUNCHER = ROOT / "automation/porter/run-pig-porter.sh"

FAKE_PIG = """#!/usr/bin/env bash
if [ "$1" = piglet ]; then exit 0; fi
dir="$PIG_CODING_AGENT_DIR"
{
  echo "dir=$dir"
  echo "pi_dir=$PI_CODING_AGENT_DIR"
  echo "home=$PIG_HOME"
  echo "settings=$(tr -d "\n" < "$dir/settings.json")"
  echo "auth=$(cat "$dir/auth.json" 2>/dev/null)"
  ls "$dir" | tr '\\n' ' ' | sed 's/^/files=/'; echo
} > "$FAKE_OUT"
echo "$dir" > "$FAKE_DIR"
if [ -n "${FAKE_WRITE:-}" ]; then
  mkdir -p "$dir/sessions/--project--"
  echo '{"type":"session"}' > "$dir/sessions/--project--/s.jsonl"
  printf '%s' '{"anthropic":{"type":"oauth","refresh":"rotated"}}' > "$dir/auth.json"
fi
"""


class LauncherTests(unittest.TestCase):
    def run_launcher(self, settings: dict, extras: dict[str, str] | None = None, operator: Path | None = None):
        with tempfile.TemporaryDirectory() as scratch:
            scratch = Path(scratch)
            operator = operator or scratch / "operator-agent"
            operator.mkdir()
            (operator / "auth.json").write_text('{"github-copilot":{"type":"api_key","key":"k"}}')
            (operator / "models.json").write_text("{}")
            (operator / "settings.json").write_text(json.dumps(settings))
            (operator / "extensions").mkdir()
            (operator / "extensions" / "lane-compact").mkdir()
            fake = scratch / "pig"
            fake.write_text(FAKE_PIG)
            fake.chmod(0o755)
            out, dirfile = scratch / "out.txt", scratch / "dir.txt"
            env = {**os.environ, "PIG_BIN": str(fake), "PIG_CODING_AGENT_DIR": str(operator), "FAKE_OUT": str(out), "FAKE_DIR": str(dirfile), **(extras or {})}
            env.pop("PIG_HOME", None)
            subprocess.run([str(LAUNCHER), "--no-session", "-p", "x"], env=env, check=True, timeout=60)
            lines = dict(line.split("=", 1) for line in out.read_text().splitlines() if "=" in line)
            used = Path(dirfile.read_text().strip())
            lines.update(used_exists=str(used.exists()), operator=str(operator), operator_settings=(operator / "settings.json").read_text(),
                         operator_extensions=" ".join(sorted(p.name for p in (operator / "extensions").iterdir())))
            return lines

    def test_the_agent_directory_is_a_private_copy_without_packages(self):
        lines = self.run_launcher({"defaultModel": "m", "packages": ["../elsewhere/pkg"], "extensions": ["x"], "followUpMode": "bogus"})
        self.assertNotEqual(lines["dir"], lines["operator"])
        self.assertEqual(lines["pi_dir"], lines["dir"])
        settings = json.loads(lines["settings"])
        self.assertEqual(settings["defaultModel"], "m")
        for key in ("packages", "extensions"):
            self.assertNotIn(key, settings)
        self.assertEqual(lines["operator_extensions"], "lane-compact")
        self.assertNotIn("lane-compact", lines["files"])
        self.assertIn("auth.json", lines["files"])

    def test_sessions_and_refreshed_credentials_outlive_the_run(self):
        # `make porter PORTER_ARGS=--continue` needs the earlier session, and a rotated OAuth refresh token written by the
        # run must reach the operator's auth.json: the old refresh token is dead once the provider has rotated it.
        with tempfile.TemporaryDirectory() as scratch:
            operator = Path(scratch) / "operator-agent"
            lines = self.run_launcher({"packages": ["a"]}, {"FAKE_WRITE": "1"}, operator=operator)
            self.assertEqual(lines["used_exists"], "False")
            self.assertTrue((operator / "sessions/--project--/s.jsonl").is_file(), "the run's session must stay in the operator's session directory")
            self.assertEqual(json.loads((operator / "auth.json").read_text())["anthropic"]["refresh"], "rotated")
            self.assertEqual(json.loads((operator / "settings.json").read_text())["packages"], ["a"])

    def test_the_private_copy_is_removed_and_the_operators_files_are_untouched(self):
        lines = self.run_launcher({"packages": ["a"]})
        self.assertEqual(lines["used_exists"], "False", "the hermetic directory must not outlive the run")
        self.assertEqual(json.loads(lines["operator_settings"])["packages"], ["a"])


class SkillTests(unittest.TestCase):
    def test_every_task_kind_the_pipeline_emits_is_a_skill_mode_with_its_listing_command(self):
        skill = (ROOT / "piglets/porter/skills/pig-porter/SKILL.md").read_text()
        follow = (ROOT / "automation/pin/follow.py").read_text()
        for kind, flag in (("closure", "--closure"), ("delta", "--delta")):
            self.assertIn(f'Task("{kind}"', follow)
            self.assertIn(f"- `{kind} <", skill)
            self.assertIn(f"follow.py \"$(python3 automation/pin/pin.py get commit)\" {flag}", skill)
            self.assertIn(f'add_argument("{flag}"', follow)

    def test_every_make_target_the_skill_names_is_a_repository_target(self):
        # A headless Porter run has only the repository: a target that exists in one operator's shell wrapper fails there.
        import re
        skill = (ROOT / "piglets/porter/skills/pig-porter/SKILL.md").read_text()
        makefiles = [ROOT / "Makefile", *sorted((ROOT / "automation/make").glob("*.mk"))]
        defined = {m.group(1) for f in makefiles for m in re.finditer(r"^([A-Za-z0-9_.-]+):", f.read_text(), re.M)}
        named = set(re.findall(r"(?:`|^)make ([a-z][a-z0-9-]*)", skill, re.M))
        self.assertTrue(named)
        self.assertEqual(sorted(named - defined), [])


    def test_closure_mode_does_not_accept_the_detectors_evidence_unread(self):
        """Round 4 review R4-3: the detector listed three tests as evidence for CodemodeToolContext.signal and none of them cancels the context."""
        skill = (ROOT / "piglets/porter/skills/pig-porter/SKILL.md").read_text()
        self.assertIn("Read every test you cite", skill)
        self.assertIn("exercises that property", skill)

    def test_the_skill_forbids_copying_another_lanes_branch(self):
        """Review R10-1: a delta task copied dflip-rest's unmerged durable images work."""
        skill = (ROOT / "piglets/porter/skills/pig-porter/SKILL.md").read_text()
        self.assertIn("never from another lane's branch", skill)

    def test_a_pin_move_run_names_its_target_branch_scope(self):
        skill = (ROOT / "piglets/porter/skills/pig-porter/SKILL.md").read_text()
        self.assertIn("pin-move branch", skill)


if __name__ == "__main__":
    unittest.main()
