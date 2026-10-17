"""Regression tests for the Go pin scan in check-compliance.py; no network or real home access."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("compliance", Path(__file__).with_name("check-compliance.py"))
compliance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compliance)

PIN = "1.27.2"


class GoPinStrayTests(unittest.TestCase):
    def scan(self, files: dict[str, str]) -> list[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, text in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(text, encoding="utf-8")
            return compliance.go_pin_strays(root, PIN, sorted(files))

    def test_pinned_release_is_clean(self):
        self.assertEqual([], self.scan({
            "go.mod": "go 1.26.0\ntoolchain go1.27.2\n",
            "build.sh": "export GOTOOLCHAIN=go1.27.2\n",
            "Dockerfile": "ARG GO_IMAGE=golang:1.27.2-bookworm@sha256:abc\n",
            "images/Dockerfile": "ARG GO_VERSION=1.27.2\nARG GO_VERSION\nRUN test \"$(go env GOVERSION)\" = \"go${GO_VERSION}\"\n",
            ".github/workflows/ci.yml": "      go-version-file: .go-version\n",
        }))

    def test_each_form_of_another_release_is_reported(self):
        problems = self.scan({
            "go.work": "toolchain go1.27.1\n",
            "durable/x/build.sh": "export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1}\n",
            "bench/Dockerfile": "ARG GO_IMAGE=golang:1.27.1-bookworm\n",
            ".github/workflows/ci.yml": "      go-version: '1.27.1'\n",
            "automation/dev/toolchains.lock": "go\t1.27.2\tlinux\thttps://dl.google.com/go/go1.26.7.linux-amd64.tar.gz\n",
            "internal/evals/docker/Dockerfile": "ARG GO_VERSION=1.27.1\nFROM golang:${GO_VERSION}-bookworm AS builder\n",
            "automation/x/pins.toml": 'go_version = "1.27.1"\n',
        })
        self.assertEqual(7, len(problems), problems)
        for name in ["go.work:1", "durable/x/build.sh:1", "bench/Dockerfile:1", ".github/workflows/ci.yml:1", "toolchains.lock:1", "internal/evals/docker/Dockerfile:1", "pins.toml:1"]:
            self.assertTrue(any(name in problem for problem in problems), (name, problems))

    def test_language_floor_and_recorded_evidence_are_not_pins(self):
        self.assertEqual([], self.scan({
            "go.mod": "go 1.26.0\ntoolchain go1.27.2\n",
            "docs/performance/note.md": "Go 1.27.1 measured 3 ns.\n",
            "bench/pod_test.go": 'x := "go1.27.1"\n',
            "bench/testdata/VERSIONS.sh": "go=go1.27.1\n",
            "test/parity/evidence/run.sh": "go1.27.1\n",
        }))


if __name__ == "__main__":
    unittest.main()
