package correspondence

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/internal/gitsnapshot"
)

// repositorySnapshotCommit writes an unreachable commit for the current test
// tree without changing the worktree index or checked-out ref.
func repositorySnapshotCommit(t *testing.T, root string) string {
	t.Helper()
	commit, err := gitsnapshot.Create(t.Context(), root, filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	return commit
}
