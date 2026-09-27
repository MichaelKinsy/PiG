package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// A failed removal must leave the log eligible for the deferred cleanup's
// retry; only a removal that succeeds, or finds nothing, is final.
func TestPackedStderrLogRemoveRetriesAfterFailure(t *testing.T) {
	// A non-empty directory makes os.Remove fail on every platform.
	path := filepath.Join(t.TempDir(), "pig-packed-cell.log")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(path, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := &processStderrLog{path: path}

	log.remove()
	if log.removed {
		t.Fatal("remove marked the log removed although os.Remove failed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log should still exist after the failed removal: %v", err)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	log.remove()
	if !log.removed {
		t.Fatal("remove did not mark the log removed after a successful retry")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("log still exists after the retry: %v", err)
	}
}

func TestPackedStderrLogRemoveTreatsMissingFileAsRemoved(t *testing.T) {
	log := &processStderrLog{path: filepath.Join(t.TempDir(), "absent.log")}
	log.remove()
	if !log.removed {
		t.Fatal("a missing log should count as removed")
	}
}
