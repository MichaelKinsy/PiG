import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def run(*args):
    return subprocess.run([sys.executable, os.path.join(HERE, "report.py"), *args], capture_output=True, text=True, check=True).stdout


class ReportTest(unittest.TestCase):
    def setUp(self):
        fd, self.path = tempfile.mkstemp(suffix=".txt")
        with os.fdopen(fd, "w") as f:
            f.write("pig pi pig\n")

    def tearDown(self):
        os.remove(self.path)

    def test_text_output_unchanged(self):
        self.assertEqual(run(self.path), "pi: 1\npig: 2\n")

    def test_json_flag(self):
        self.assertEqual(json.loads(run("--json", self.path)), {"pi": 1, "pig": 2})

    def test_json_flag_after_paths(self):
        self.assertEqual(json.loads(run(self.path, "--json")), {"pi": 1, "pig": 2})
