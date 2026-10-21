package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// A start of an entry whose marker already holds the SDK fingerprint writes nothing, so no start rewrites a file the collector reads.
func TestRecordSDKFingerprintLeavesAMarkerThatAlreadyHoldsItAlone(t *testing.T) {
	entryDir := t.TempDir()
	recordSDKFingerprint(entryDir, "0123456789abcdef")
	marker := filepath.Join(entryDir, sdkFingerprintFile)
	first, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	recordSDKFingerprint(entryDir, "0123456789abcdef")
	second, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(first, second) {
		t.Fatal("a marker that already held the fingerprint was replaced")
	}
	recordSDKFingerprint(entryDir, "fedcba9876543210")
	if got, err := os.ReadFile(marker); err != nil || string(got) != "fedcba9876543210" {
		t.Fatalf("marker after a new fingerprint = %q, %v", got, err)
	}
	if entries, err := os.ReadDir(entryDir); err != nil || len(entries) != 1 {
		t.Fatalf("entry holds %v, %v; want only the marker, no temporary file", entries, err)
	}
}
