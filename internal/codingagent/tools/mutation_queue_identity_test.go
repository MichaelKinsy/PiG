package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// A queued mutation keeps the identity of the file it was queued for (D190): the queued path replaced by another file (an atomic write:
// a temporary file renamed over it) meanwhile leaves a later mutation, through a hard link of the file queued, waiting for it. On Windows
// an os.Stat result left unpinned names the file at the path when it is compared, so the later mutation ran at once. Linux reads the
// identity when it stats, so this is green there before and after: needs Windows CI to prove red on the old queue.
func TestMutationQueueKeepsTheIdentityOfAFileReplacedWhileItsMutationIsQueued(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "Target.txt")
	if err := os.WriteFile(first, []byte("queued"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := mutationAlias(t, first, "hardlink")
	queue := NewFileMutationQueue()
	held, err := queue.Reserve(first)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement.tmp")
	if err := os.WriteFile(replacement, []byte("written by another"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, first); err != nil {
		t.Fatal(err)
	}
	later, err := queue.Reserve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(later.prev) != 1 {
		t.Errorf("the mutation through the hard link waits for %d earlier ones, want the queued one", len(later.prev))
	}
	held.Release()
	later.Wait()
	later.Release()
}
