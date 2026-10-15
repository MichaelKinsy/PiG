# SPDX-License-Identifier: MIT
"""referencing() must select the same packages as the unoptimised scan of every source with the full pattern."""
import importlib.util
import re
import tempfile
import unittest
from pathlib import Path, PurePosixPath

spec = importlib.util.spec_from_file_location("changed_packages", Path(__file__).with_name("changed-packages.py"))
cp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cp)


class ReferencingTest(unittest.TestCase):
    def test_matches_unoptimised_scan(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            texts = {
                "a.go": 'x := filepath.Join(root, "docs", "guide.md")',
                "b.go": '// see test/parity/scenarios/rpc/one.json',
                "c.go": 'p := "../../Makefile"',
                "d.go": "package d // nothing",
                "e.go": 'filepath.Join(root, "Makefile")',
            }
            sources = {}
            for i, (n, t) in enumerate(texts.items()):
                (tmp / n).write_text(t)
                sources[f"m/{n[0]}"] = {tmp / n: None, tmp / "missing.go": None}
            tested = set(sources) - {"m/e"}
            for name in ("docs/guide.md", "test/parity/scenarios/rpc/two.json", "Makefile", "docs/none.md"):
                cp._TEXT_CACHE.clear()
                self.assertEqual(cp.referencing(name, sources, tested), reference_scan_safe(name, sources, tested), name)


def reference_scan_safe(name, sources, tested):
    patterns = [cp.path_pattern(PurePosixPath(name))]
    parent = PurePosixPath(name).parent
    if len(parent.parts) >= 3:
        patterns.append(cp.path_pattern(parent))
    needle = re.compile("|".join(patterns))
    hits = set()
    for p, files in sources.items():
        if p not in tested:
            continue
        for f in files:
            try:
                text = Path(f).read_text(errors="replace")
            except OSError:
                continue
            if needle.search(text):
                hits.add(p)
                break
    return hits


if __name__ == "__main__":
    unittest.main()
