# SPDX-License-Identifier: MIT
"""The dispatch generator follows the committed pin without obsolete snapshots."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402

SPEC = importlib.util.spec_from_file_location("pending_test_batches", pinlib.ROOT / "automation/gen/pending-test-batches.py")
batches = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(batches)


class PendingTestBatchesTests(unittest.TestCase):
    def test_committed_pin_default_and_explicit_version(self):
        pin = pinlib.Pin("b" * 40, "b" * 40, "1.1.0", "1.1.0+bbbbbbbb")
        for explicit_version in (None, "0.84.0"):
            for dispositions in ([], ["pending", "partial", "ported"]):
                with self.subTest(version=explicit_version, dispositions=dispositions), tempfile.TemporaryDirectory() as directory:
                    version = explicit_version or pin.version
                    entries = [{"path": f"packages/agent/test/case-{index}.test.ts", "disposition": disposition} for index, disposition in enumerate(dispositions)]
                    baseline = json.dumps({"entries": entries}).encode()
                    root = Path(directory)
                    interfaces = root / "test/parity/interfaces"
                    interfaces.mkdir(parents=True)
                    (interfaces / f"test-mapping-v{version}.json").write_bytes(baseline)
                    (interfaces / f"upstream-tests-v{version}.json").write_text(json.dumps({"files": [{"path": entry["path"], "caseCount": 1} for entry in entries]}))
                    (interfaces / f"test-porting-policy-v{version}.json").write_text(json.dumps({"entries": [{"path": entry["path"], "area": "tools", "tags": ["hot-path"]} for entry in entries]}))
                    out = root / "batches.md"
                    argv = ["pending-test-batches.py", "--baseline-commit", "baseline", "--out", str(out)]
                    if explicit_version:
                        argv.extend(("--version", explicit_version))
                    with contextlib.chdir(root), contextlib.redirect_stdout(io.StringIO()), mock.patch.object(pinlib, "committed", return_value=pin), mock.patch.object(batches, "baseline_mapping", return_value=baseline) as read_baseline, mock.patch.object(sys, "argv", argv):
                        batches.main()
                        first = out.read_bytes()
                        batches.main()
                        self.assertEqual(first, out.read_bytes())
                    read_baseline.assert_called_with("baseline", f"test/parity/interfaces/test-mapping-v{version}.json")
                    text = first.decode()
                    self.assertIn(f"--baseline-commit baseline --version {version}", text)
                    pending = [entry for entry in entries if entry["disposition"] in ("pending", "partial")]
                    self.assertIn(f"= {len(pending)} files", text)
                    for entry in entries:
                        self.assertEqual(text.count(f"`{entry['path']}`"), int(entry in pending))


if __name__ == "__main__":
    unittest.main()
