package node

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// upstream: packages/durable/src/env/node.ts renameFile calls fs.rename, which libuv answers with the error of rename(2):
// a file over a directory is EISDIR (is_directory) and a directory over a directory that holds files is ENOTEMPTY. The
// remote environment compares equal with it in packages/env/test/differential.test.ts.
// mutation-checked: os.Rename instead of syscall.Rename in nodeerrno.Rename fails it (os.Rename answers EEXIST first)
func TestRenameFileReportsTheErrorOfRename2ForADirectoryDestination(t *testing.T) {
	skipOnWindows(t)
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644))
	mustDo(t, os.MkdirAll(filepath.Join(root, "full", "inner"), 0o755))
	mustDo(t, os.MkdirAll(filepath.Join(root, "dir"), 0o755))

	err := env.RenameFile(background, "file", "dir")
	fileError, ok := errors.AsType[*durableenv.FileError](err)
	if !ok || fileError.Code != durableenv.FileErrorIsDirectory {
		t.Fatalf("a file renamed over a directory: %v, want is_directory", err)
	}
	err = env.RenameFile(background, "dir", "full")
	fileError, ok = errors.AsType[*durableenv.FileError](err)
	if !ok || fileError.Code != durableenv.FileErrorUnknown || fileError.Cause == nil || errnoCode(fileError.Cause) != "ENOTEMPTY" {
		t.Fatalf("a directory renamed over a directory with files: %v, want ENOTEMPTY", err)
	}
}
