//go:build windows

package plugins

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Not a port of a case of upstream's tests, the fact the cases that move a watched tree skip on Windows rest on (see
// skipWhereAWatchedTreeCannotMove): Windows refuses to rename a directory that has a watched directory inside it, as it does for
// a handle that `fs.watch` holds, and allows it once the watcher has closed its handles.
func TestWindowsRefusesToRenameAWatchedTreeUntilTheWatcherCloses(t *testing.T) {
	parent := realOf(t, tempRoot(t))
	base := filepath.Join(parent, "app")
	writeFiles(t, base, map[string]string{"core.ts": "v1", "sub/inner.ts": "inner"})
	f := watcherOver(t, nil, base)
	f.settled()
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(base, moved); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("rename of a watched tree = %v, want access denied", err)
	}
	f.close()
	f.watcher.join()
	if err := os.Rename(base, moved); err != nil {
		t.Fatalf("rename after the watcher closed: %v", err)
	}
}
