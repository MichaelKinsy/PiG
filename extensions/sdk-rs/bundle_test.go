package rssdk

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The bundle is the one file sdk.zst, and names what a stager needs. That the frame holds these files' text and StagedHash is their hash is checked where it can be decoded: TestEmbeddedBundlesHoldTheirSources in coding/extension/pigsdk.
func TestSourceHoldsTheFrame(t *testing.T) {
	entries, err := fs.ReadDir(Source, ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "sdk.zst" {
		t.Fatalf("Source holds %v (%v), want the one file sdk.zst", entries, err)
	}
	frame, err := Source.ReadFile("sdk.zst")
	if err != nil || len(frame) == 0 {
		t.Fatalf("sdk.zst: %d bytes, %v", len(frame), err)
	}
	for _, required := range []string{"Cargo.toml", "src/lib.rs", "src/login.rs"} {
		if !slices.Contains(BundledFiles(), required) {
			t.Fatalf("BundledFiles must include %s so the staged crate builds", required)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(StagedHash()) {
		t.Fatalf("StagedHash() = %q, want 16 hex digits", StagedHash())
	}
}

func isTestModule(path string) bool { return strings.HasSuffix(path, "tests.rs") }
