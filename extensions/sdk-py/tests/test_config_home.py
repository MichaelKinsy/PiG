"""Context.config_home answers the shared env matrix (test/extension-conformance/testdata/configroot-matrix.json).

The host's internal/configroot and the Go, Rust and Node SDKs answer the same matrix: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig; an empty
variable falls through; ``~`` and ``~/`` expand; any other value stays literal; an unavailable home directory is an error, never a relative path.
"""

from __future__ import annotations

import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import pig_sdk

MATRIX = Path(__file__).resolve().parents[3] / "test" / "extension-conformance" / "testdata" / "configroot-matrix.json"


def _config_home() -> str:
    return pig_sdk.Context(extension=None).config_home  # type: ignore[arg-type]


class ConfigHomeMatrix(unittest.TestCase):
    def test_matrix(self) -> None:
        cases = json.loads(MATRIX.read_text())["cases"]
        self.assertTrue(cases)
        for case in cases:
            with self.subTest(case["name"]), tempfile.TemporaryDirectory() as home:
                env = {k: v for k, v in os.environ.items() if k not in ("PIG_HOME", "XDG_CONFIG_HOME")}
                env["HOME"] = env["USERPROFILE"] = home
                for name, value in case["env"].items():
                    if value is None:
                        env.pop(name, None)
                    else:
                        env[name] = value
                        if name == "HOME":
                            env["USERPROFILE"] = value
                with mock.patch.dict(os.environ, env, clear=True):
                    if case["want"] is None:
                        with self.assertRaises(RuntimeError):
                            _config_home()
                    else:
                        self.assertEqual(_config_home(), case["want"].replace("<HOME>", home))


if __name__ == "__main__":
    unittest.main()
