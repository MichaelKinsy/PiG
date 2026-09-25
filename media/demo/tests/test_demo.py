"""Guards for the recording fixtures, not Pi/PiG parity claims."""
import importlib.util
import os
import pathlib
import subprocess
import tempfile
import unittest

DEMO = pathlib.Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("demo_model", DEMO / "model/demo-model.py")
model = importlib.util.module_from_spec(spec)
spec.loader.exec_module(model)


class DemoModelTests(unittest.TestCase):
    def test_failed_tool_is_not_narrated_as_success(self):
        for step in ("build", "test"):
            with self.subTest(step=step):
                reply = model.script([
                    {"role": "user", "content": f"[chain 4/4 · {step}] fix the failing tests"},
                    {"role": "tool", "content": "FAILED (failures=2)\nCommand exited with code 1"},
                ])
                self.assertIn("failed", reply.lower())
                self.assertNotIn("tests pass", reply)
                self.assertNotIn("Applied the fix", reply)

    def test_greeting_does_not_invent_packaging_guarantees(self):
        reply = model.script([{"role": "user", "content": "hello piglet"}])
        self.assertNotIn("no install", reply)
        self.assertNotIn("single-file", reply)
        self.assertIn("scripted", reply.lower())

    def test_reading_source_with_value_error_is_not_a_tool_failure(self):
        reply = model.script([
            {"role": "user", "content": "[chain 2/4 · plan] fix the failing tests"},
            {"role": "tool", "content": 'raise ValueError("window must be positive")'},
        ])
        self.assertEqual(reply, model.PLAN)

    def test_test_command_preserves_exit_status(self):
        reply = model.script([{"role": "user", "content": "[chain 4/4 · test] fix the failing tests"}])
        self.assertEqual(reply["args"]["command"], "python3 -m unittest -v test_stats")

    def test_edit_matches_pinned_pi_schema(self):
        reply = model.script([{"role": "user", "content": "[chain 3/4 · build] fix the failing tests"}])
        self.assertEqual(reply, {"tool": "edit", "args": {"path": "stats.py", "edits": [{
            "oldText": "range(len(values) - window)",
            "newText": "range(len(values) - window + 1)",
        }]}})


class ScriptTests(unittest.TestCase):
    def test_build_failure_streams_diagnostics_and_keeps_log(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "piglet").mkdir()
            (root / "logs").mkdir()
            (root / "bin").mkdir()
            pig = root / "bin/pig"
            pig.write_text("#!/bin/sh\necho 'Compiling Rust members: chain-rs' >&2\necho 'compiler diagnostic' >&2\nexit 7\n")
            pig.chmod(0o755)
            result = subprocess.run(["bash", str(DEMO / "build-piglets.sh")], capture_output=True, text=True,
                                    env={**os.environ, "HOME": str(root), "DEMO_ROOT": str(root),
                                         "PATH": str(root / "bin") + os.pathsep + os.environ["PATH"]})
            self.assertEqual(result.returncode, 1)
            self.assertIn("Compiling Rust members: chain-rs", result.stdout + result.stderr)
            self.assertIn("compiler diagnostic", result.stdout + result.stderr)
            self.assertIn("compiler diagnostic", (root / "logs/build-pig-go.log").read_text())

    def test_launchers_do_not_embed_operator_paths_or_remote_builds(self):
        for name in ("stage.sh", "build-piglets.sh", "remote-stage.sh", "demo-shell.sh", "demo-term.sh"):
            with self.subTest(name=name):
                text = (DEMO / name).read_text()
                self.assertNotIn("/Users/", text)
                self.assertNotIn("ssh ", text)
                self.assertNotIn("--real", text)
                self.assertNotIn("git add -A", text)


if __name__ == "__main__":
    unittest.main()
