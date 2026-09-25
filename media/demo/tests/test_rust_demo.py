"""Compile the shipped Rust demo against the current SDK without changing source."""
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

DEMO = pathlib.Path(__file__).resolve().parents[1]


class RustDemoTests(unittest.TestCase):
    def test_rust_demo_builds_against_current_sdk(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            crate = root / "chain-rs"
            shutil.copytree(DEMO / "sources/chain-rs", crate)
            sdk = DEMO.parents[1] / "extensions/sdk-rs"
            result = subprocess.run(["cargo", "check", "--offline", "--manifest-path", str(crate / "Cargo.toml"),
                                     "--config", 'patch.crates-io.pig-sdk.path="' + sdk.as_posix() + '"'],
                                    capture_output=True, text=True,
                                    env={**os.environ, "CARGO_TARGET_DIR": str(root / "target")})
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
