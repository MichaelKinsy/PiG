# SPDX-License-Identifier: MIT
"""overnight.sh against a fake python3: the exit status of each bench is logged and a failed bench fails the script."""
import os
import subprocess
import tempfile
import unittest

SCRIPT = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "overnight.sh")


class OvernightTest(unittest.TestCase):
    def run_script(self, failing):
        with tempfile.TemporaryDirectory() as tmp:
            fake = os.path.join(tmp, "bin")
            os.makedirs(fake)
            with open(os.path.join(fake, "python3"), "w") as f:  # `python3 -m pigbench <bench> ...`: fail for the named bench
                f.write(f'#!/bin/sh\n[ "$3" = "{failing}" ] && exit 3\nexit 0\n')
            os.chmod(os.path.join(fake, "python3"), 0o755)
            env = dict(os.environ, PATH=fake + os.pathsep + os.environ["PATH"], OUT=os.path.join(tmp, "out"), STAMP="t")
            env.pop("CORES", None)
            env.pop("START_UTC", None)
            return subprocess.run(["bash", SCRIPT], env=env, capture_output=True, text=True, timeout=60)

    def test_a_failed_bench_is_logged_with_its_status_and_fails_the_script(self):
        done = self.run_script("overhead")
        self.assertIn("end overhead exit 3", done.stdout)
        self.assertIn("end startup exit 0", done.stdout)
        self.assertIn("end reliability exit 0", done.stdout)  # the later benches still run
        self.assertNotEqual(done.returncode, 0)

    def test_all_benches_passing_exit_zero(self):
        done = self.run_script("none")
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertEqual(done.stdout.count(" exit 0"), 4)


if __name__ == "__main__":
    unittest.main()
