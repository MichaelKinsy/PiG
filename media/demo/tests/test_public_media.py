"""The publication subset excludes uncleared assets without weakening rehearsal checks."""
import importlib.util
import json
from pathlib import Path
import unittest

DEMO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("verify_media", DEMO / "verify-media.py")
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


class PublicMediaTests(unittest.TestCase):
    def test_public_manifest_covers_exactly_cleared_clips_and_posters(self):
        expected = {f"demo-{step}.{ext}" for step in [*"abcde", "hook"] for ext in ["mp4", "png"]}
        self.assertEqual(set(verify.deliverables()), expected)
        self.assertEqual({row["file"] for row in json.loads((DEMO / "media.json").read_text())}, expected)
        for name in expected:
            self.assertTrue((DEMO / name).is_file(), name)

    def test_full_rehearsal_still_requires_every_original_deliverable(self):
        expected = {f"demo-{step}.{ext}" for step in [*"abcdef", "full", "hook"] for ext in ["mp4", "png"]}
        expected.add("demo-full.gif")
        self.assertEqual(set(verify.deliverables(True)), expected)


if __name__ == "__main__":
    unittest.main()
