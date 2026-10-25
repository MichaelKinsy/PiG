//go:build linux

package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// receiptStagingFDs counts this process's open descriptors that point at a
// staged install receipt, live or already unlinked.
func receiptStagingFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	n := 0
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && strings.Contains(target, ".install-receipt-") {
			n++
		}
	}
	return n
}

// A failed receipt write must neither keep the staged temp file open nor leave
// it behind. The CA sidecar is group-readable, so readUpdateTransportCA fails
// after the temp file was created. Self-update and its receipt are PiG
// additions (D39); Pi has no counterpart.
func TestWriteStandaloneReceiptFailureReleasesStagedFile(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", t.TempDir())
	root := ConfigRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(root, updateTransportCASidecarName)
	if err := os.WriteFile(ca, []byte("ca"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ca, 0o644); err != nil {
		t.Fatal(err)
	}
	before := receiptStagingFDs(t)
	for range 20 {
		err := WriteStandaloneReceipt(exe, "v", "https://updates.example/manifest")
		if err == nil || !strings.Contains(err.Error(), "owner-only") {
			t.Fatalf("WriteStandaloneReceipt error = %v, want the owner-only CA error", err)
		}
	}
	if after := receiptStagingFDs(t); after != before {
		t.Fatalf("open staged receipt descriptors = %d after 20 failed writes, want %d", after, before)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-receipt-") {
			t.Errorf("stray staged receipt %s", entry.Name())
		}
	}
}
