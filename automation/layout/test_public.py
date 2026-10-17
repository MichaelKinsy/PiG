#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Public-library layout regressions, including Go's external import boundary."""
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import build
import docs
import docs_prose
import plan
import test_go

layout = test_go.layout


class PublicLayoutTest(unittest.TestCase):
    def test_public_libraries_and_private_helpers(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            test_go.LayoutGoTest().fixture(root)
            files = {
                "coding/pigversion/pigversion.go": "package pigversion\nconst Version = \"test\"\n",
                "tui/termsim/termsim.go": "package termsim\n",
                "tui/parity/parity.go": 'package parity\nimport _ "github.com/MichaelKinsy/PiG/tui/termsim"\n',
                "coding/doc.go": '// Package coding is the public SDK for embedding the pig coding\n// agent as a Go library.\npackage coding\n',
                "coding/version.go": 'package coding\nimport "github.com/MichaelKinsy/PiG/coding/pigversion"\nconst Version = pigversion.Version\n',
                "parity/pin.go": 'package parity\nvar pin = filepath.Join(root, "coding", "pigversion", "pigversion.go")\n',
                "automation/release/set-version/main.go": 'package main\nconst pin = "coding/pigversion/pigversion.go"\n',
            }
            for name, text in files.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(text)
            subprocess.run(["git", "-C", str(root), "add", "--", *files], check=True)
            before = (root / "coding/doc.go").read_bytes()
            layout.apply(root, keep_public_libraries=True)
            for name in plan.PUBLIC_ROOTS:
                self.assertTrue((root / name).is_dir())
            for name in plan.PRIVATE_HELPERS:
                self.assertFalse((root / name).exists())
                self.assertTrue((root / "internal" / name).is_dir())
            self.assertEqual(before, (root / "coding/doc.go").read_bytes())
            self.assertIn('"internal", "coding", "pigversion"', (root / "parity/pin.go").read_text())
            self.assertIn('"internal/coding/pigversion/pigversion.go"', (root / "automation/release/set-version/main.go").read_text())
            with tempfile.TemporaryDirectory() as consumer:
                consumer = Path(consumer)
                (consumer / "go.mod").write_text(f'module example.org/consumer\n\ngo 1.26\nreplace github.com/MichaelKinsy/PiG => {root}\nrequire github.com/MichaelKinsy/PiG v0.0.0\n')
                (consumer / "main.go").write_text('package main\nimport (\n' + ''.join(f'_ "github.com/MichaelKinsy/PiG/{name}"\n' for name in plan.PUBLIC_ROOTS) + ')\nfunc main() {}\n')
                subprocess.run(["go", "build", "."], cwd=consumer, check=True)
            snapshot = {p: (root / p).read_bytes() for p in layout.git(root, "ls-files").decode().splitlines()}
            layout.apply(root, keep_public_libraries=True)
            self.assertEqual(snapshot, {p: (root / p).read_bytes() for p in snapshot})

    def test_private_package_comment_references(self):
        source = b'// Pins live in coding/pigversion/pigversion.go, not ~/.pig/agent/settings.json.\npackage coding\n'
        comment = source.splitlines()[0]
        with patch.object(layout, "MOVES", plan.package_moves(True)):
            actual = layout.rewrite_go("coding/upstream.go", source, [], False,
                                       [{"Start": 0, "End": len(comment), "Value": comment.decode()}],
                                       {"coding/pigversion/pigversion.go"})
        self.assertEqual(actual, source.replace(b"in coding/pigversion/", b"in internal/coding/pigversion/"))

    def test_private_package_boundaries(self):
        with patch.object(build, "PACKAGE_MOVES", plan.package_moves(True)), patch.object(docs, "GO_MOVES", plan.package_moves(True)):
            source = 'coding/pigversion coding/pigversionish/file.go internal/coding/pigversion/file.go packages/tui/termsim/test.ts ./tui/parity/... $PIG_HOME/agent/'
            expected = 'internal/coding/pigversion coding/pigversionish/file.go internal/coding/pigversion/file.go packages/tui/termsim/test.ts ./internal/tui/parity/... $PIG_HOME/agent/'
            self.assertEqual(build.rewrite("Makefile", source), expected)
            self.assertEqual(build.rewrite("Makefile", expected), expected)

    def test_selected_paths_and_public_docs(self):
        with patch.object(build, "PACKAGE_MOVES", plan.package_moves(True)), patch.object(docs, "GO_MOVES", plan.package_moves(True)):
            self.assertEqual(build.rewrite("Makefile", "./coding/... coding/pigversion/pigversion.go tui/widthx/"), "./coding/... internal/coding/pigversion/pigversion.go tui/widthx/")
            self.assertEqual(build.rewrite("automation/a.py", 'root / "coding" / "pigversion" / "pigversion.go"'), 'root / "internal" / "coding" / "pigversion" / "pigversion.go"')
            rewrite = docs.Rewriter(Path("."), [])
            self.assertEqual(rewrite.references("coding/pigversion/pigversion.go coding/runtime.go tui/termsim/termsim.go"), "internal/coding/pigversion/pigversion.go coding/runtime.go internal/tui/termsim/termsim.go")
        self.assertEqual(docs_prose.rewrite_prose(docs_prose.SDK_INTRO, "docs/site/docs/sdk.md", keep_public_libraries=True), docs_prose.SDK_INTRO)


if __name__ == "__main__":
    unittest.main()
