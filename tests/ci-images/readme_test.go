package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadmeBadgesUseProjectOwnedEvidence(t *testing.T) {
	root := repoRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`<img src="docs/assets/pig-project-banner.png" alt="PiG project banner: There are many agent harnesses, but this one is yours. Meet Pi-in-Go.">`,
		"https://github.com/MichaelKinsy/PiG/actions/workflows/ci.yml/badge.svg",
		"[![Parity coverage](.github/badges/parity-coverage.svg)](parity/coverage.md)",
		"[![Follow PiG on X](https://img.shields.io/badge/X-%40PiGCodingAgent-000000?logo=x&logoColor=white)](https://x.com/PiGCodingAgent)",
		"[![Join r/PiGCodingAgent](https://img.shields.io/badge/Reddit-r%2FPiGCodingAgent-FF4500?logo=reddit&logoColor=white)](https://www.reddit.com/r/PiGCodingAgent/)",
	} {
		if !strings.Contains(string(readme), required) {
			t.Errorf("README is missing badge reference %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".github", "badges", "parity-coverage.svg")); err != nil {
		t.Fatalf("parity coverage badge is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "assets", "pig-project-banner.png")); err != nil {
		t.Fatalf("README project banner is missing: %v", err)
	}
}
