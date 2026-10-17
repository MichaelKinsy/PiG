//go:build unix

package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Pi: packages/durable/src/storage/jsonl/node.ts opens storage over `new NodeExecutionEnv({ cwd })`, so NodeFileSystem must behave as that env's file calls do
// (packages/durable/src/env/node.ts:1005-1215). Each case below is the Pi behaviour the first version of NodeFileSystem lacked or no test pinned.

// afterCalls is a context that reports cancellation only from its n-th Err() call on: the abort arrives while the operation runs.
type afterCalls struct {
	context.Context
	left atomic.Int64
}

func abortingAfter(n int64) *afterCalls {
	c := &afterCalls{Context: context.Background()}
	c.left.Store(n)
	return c
}

func (c *afterCalls) Err() error {
	if c.left.Add(-1) >= 0 {
		return nil
	}
	return context.Canceled
}

func codeOf(t *testing.T, err error) durableenv.FileErrorCode {
	t.Helper()
	fileError, ok := errors.AsType[*durableenv.FileError](err)
	if !ok {
		t.Fatalf("error %v (%T) is not a *FileError", err, err)
	}
	return fileError.Code
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// node.ts:1005-1019 / :1021-1035: write and append create the parent directories, and an abort that lands after the mkdir stops the call before the file is written (append
// also reports an abort that lands after the append, :1030-1031). node.ts:1038-1056 truncate and :1059-1077 flush report an abort that lands after the operation.
// mutation-checked: dropping the after-mkdir check in WriteFile or AppendFile, or the after-operation check in AppendFile, TruncateFile or FlushFile, fails the matching case.
func TestNodeFileSystemAbortsBetweenItsStepsAsPisEnvDoes(t *testing.T) {
	cwd := t.TempDir()
	fileSystem := &NodeFileSystem{Cwd: cwd}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(cwd, "existing")
	must(os.WriteFile(existing, []byte("abc"), 0o600))

	for _, tc := range []struct {
		name string
		run  func(ctx context.Context) error
		// ran reports whether the effect of the operation reached the disk.
		ran func() bool
	}{
		{"write", func(ctx context.Context) error { return fileSystem.WriteFile(ctx, "w/f", "x") }, func() bool { return exists(filepath.Join(cwd, "w", "f")) }},
		{"append", func(ctx context.Context) error { return fileSystem.AppendFile(ctx, "a/f", "x") }, func() bool { return exists(filepath.Join(cwd, "a", "f")) }},
	} {
		// The first Err() call is the entry check; the second, after the mkdir, aborts: the parent exists, the file does not.
		if err := tc.run(abortingAfter(1)); codeOf(t, err) != durableenv.FileErrorAborted || tc.ran() {
			t.Errorf("%s aborted after the mkdir: error %v, file written %t; want aborted and no file", tc.name, err, tc.ran())
		}
	}
	// An abort after the operation completed: the operation ran, and the error still reports it.
	if err := fileSystem.AppendFile(abortingAfter(2), "existing", "d"); codeOf(t, err) != durableenv.FileErrorAborted {
		t.Errorf("append aborted afterwards = %v, want aborted", err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "abcd" {
		t.Errorf("append content %q, want abcd", got)
	}
	if err := fileSystem.TruncateFile(abortingAfter(1), "existing", 1); codeOf(t, err) != durableenv.FileErrorAborted {
		t.Errorf("truncate aborted afterwards = %v, want aborted", err)
	}
	if err := fileSystem.FlushFile(abortingAfter(1), "existing"); codeOf(t, err) != durableenv.FileErrorAborted {
		t.Errorf("flush aborted afterwards = %v, want aborted", err)
	}
	// node.ts:1079-1100 and :1163-1197 check the abort once, before the call: rename, createDir and remove ignore a later one.
	if err := fileSystem.RenameFile(abortingAfter(1), "existing", "moved"); err != nil {
		t.Errorf("rename with a late abort = %v, want success", err)
	}
	if err := fileSystem.CreateDir(abortingAfter(1), "made", nil); err != nil {
		t.Errorf("createDir with a late abort = %v, want success", err)
	}
	if err := fileSystem.Remove(abortingAfter(1), "moved", nil); err != nil {
		t.Errorf("remove with a late abort = %v, want success", err)
	}
}

// node.ts:1038-1046: truncateFile refuses a size that is not a non-negative safe integer as `invalid` before it opens the file (a missing file is not reported instead),
// and :1059-1067 flushFile refuses a directory as is_directory.
// mutation-checked: removing the size check, or loosening the upper bound from 2^53-1 to 2^62, fails it.
func TestNodeFileSystemRefusesUnsafeTruncateSizesAndFlushOfADirectory(t *testing.T) {
	cwd := t.TempDir()
	fileSystem := &NodeFileSystem{Cwd: cwd}
	for _, size := range []int64{-1, 1 << 53, 1 << 62} {
		if err := fileSystem.TruncateFile(context.Background(), "missing", size); codeOf(t, err) != durableenv.FileErrorInvalid {
			t.Errorf("TruncateFile(%d) on a missing file = %v, want invalid", size, err)
		}
	}
	if err := fileSystem.TruncateFile(context.Background(), "missing", 1<<53-1); codeOf(t, err) != durableenv.FileErrorNotFound {
		t.Errorf("the largest safe size must reach the open and report not_found, got %v", err)
	}
	if err := os.Mkdir(filepath.Join(cwd, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fileSystem.FlushFile(context.Background(), "dir"); codeOf(t, err) != durableenv.FileErrorIsDirectory {
		t.Errorf("FlushFile of a directory = %v, want is_directory", err)
	}
}

// node.ts:1179-1196 remove is rm(path, {recursive: false, force: false} by default): a directory needs recursive even when empty, a missing path is not_found unless force,
// and force ignores a missing path. node.ts:1163-1177 createDir defaults to recursive and a non-recursive call needs the parent.
// mutation-checked: force ignored, recursive ignored (an empty directory removed without it) and a recursive createDir regardless of the option each fail it.
func TestNodeFileSystemRemoveAndCreateDirOptionsFollowPisEnv(t *testing.T) {
	cwd := t.TempDir()
	fileSystem := &NodeFileSystem{Cwd: cwd}
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(cwd, "tree", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fileSystem.Remove(ctx, "tree/empty", nil); err == nil || !exists(filepath.Join(cwd, "tree", "empty")) {
		t.Errorf("removing an empty directory without recursive = %v (still there: %t), want an error and the directory kept", err, exists(filepath.Join(cwd, "tree", "empty")))
	}
	if err := fileSystem.Remove(ctx, "tree", &durableenv.RemoveOptions{Recursive: true}); err != nil || exists(filepath.Join(cwd, "tree")) {
		t.Errorf("recursive remove = %v (still there: %t), want the tree gone", err, exists(filepath.Join(cwd, "tree")))
	}
	if err := fileSystem.Remove(ctx, "gone", nil); codeOf(t, err) != durableenv.FileErrorNotFound {
		t.Errorf("remove of a missing path = %v, want not_found", err)
	}
	for _, options := range []*durableenv.RemoveOptions{{Force: true}, {Recursive: true, Force: true}} {
		if err := fileSystem.Remove(ctx, "gone", options); err != nil {
			t.Errorf("forced remove of a missing path with %+v = %v, want nil", options, err)
		}
	}
	no := false
	if err := fileSystem.CreateDir(ctx, "p/q", &durableenv.CreateDirOptions{Recursive: &no}); codeOf(t, err) != durableenv.FileErrorNotFound {
		t.Errorf("non-recursive createDir without its parent = %v, want not_found", err)
	}
	if err := fileSystem.CreateDir(ctx, "p/q", nil); err != nil || !exists(filepath.Join(cwd, "p", "q")) {
		t.Errorf("default createDir = %v, want the parents created", err)
	}
}

// node.ts:1103-1160 listDir reports a symbolic link as a symlink (lstat), skips an entry of an unsupported kind, and :1079-1100 renameFile reports a failure against the
// source path and an abort against the destination path.
// mutation-checked: following the link (stat), keeping the FIFO, reporting the destination for a failed rename, and checking the source for the abort each fail it.
func TestNodeFileSystemListsKindsAndReportsRenamePaths(t *testing.T) {
	cwd := t.TempDir()
	fileSystem := &NodeFileSystem{Cwd: cwd}
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(cwd, "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, step := range []error{
		os.WriteFile(filepath.Join(cwd, "d", "f"), []byte("abc"), 0o600),
		os.Symlink("f", filepath.Join(cwd, "d", "l")),
		os.Symlink("missing-target", filepath.Join(cwd, "d", "dangling")),
		syscall.Mkfifo(filepath.Join(cwd, "d", "fifo"), 0o600),
	} {
		if step != nil {
			t.Fatal(step)
		}
	}
	entries, err := fileSystem.ListDir(ctx, "d")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]durableenv.FileKind{}
	for _, entry := range entries {
		kinds[entry.Name] = entry.Kind
	}
	want := map[string]durableenv.FileKind{"f": durableenv.FileKindFile, "l": durableenv.FileKindSymlink, "dangling": durableenv.FileKindSymlink}
	if len(kinds) != len(want) {
		t.Fatalf("listed %v, want exactly %v (the FIFO is skipped)", kinds, want)
	}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("%s listed as %q, want %q", name, kinds[name], kind)
		}
	}

	err = fileSystem.RenameFile(ctx, "nope", "elsewhere")
	if fileError, ok := errors.AsType[*durableenv.FileError](err); !ok || fileError.Code != durableenv.FileErrorNotFound || fileError.Path != filepath.Join(cwd, "nope") {
		t.Errorf("failed rename = %v, want not_found against the source path", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = fileSystem.RenameFile(cancelled, "nope", "elsewhere")
	if fileError, ok := errors.AsType[*durableenv.FileError](err); !ok || fileError.Code != durableenv.FileErrorAborted || fileError.Path != filepath.Join(cwd, "elsewhere") {
		t.Errorf("aborted rename = %v, want aborted against the destination path", err)
	}
}
