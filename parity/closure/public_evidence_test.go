package closure

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryDoesNotShipLocalEvidenceRuns(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(root, "parity", "closuredata", "evidence"))
	if err == nil {
		t.Fatal("local evidence runs must be generated outside the source tree")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect local evidence directory: %v", err)
	}
}
