//go:build unix

package node

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Pi: packages/durable/src/env/index.ts:159-168 (DirReader) and env/node.ts:543-597 (NodeDirReader). Entries come back with their kind (file, directory, symlink) from
// lstat, an entry of an unsupported kind (a FIFO here) is skipped (node.ts:583-584 pushes only an ok fileInfoFromStats), `done` marks the end and a call after the end returns an
// empty page that is still done, an aborted call fails with `aborted` before the closed check (node.ts:558-560) and consumes no entry, and close is idempotent (node.ts:592-596).
// mutation-checked: appending the entry when fileInfoFromStats fails (unsupported kind kept), checking closed before aborted, and having a refused (aborted) call consume the
// directory each fail it, as does reporting done only on an empty page.
func TestDirReaderSkipsUnsupportedKindsEndsDoneAndHonoursAbortBeforeClosed(t *testing.T) {
	env, root := newTestEnv(t)
	dir := filepath.Join(root, "dir")
	mustDo(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	mustDo(t, os.WriteFile(filepath.Join(dir, "file"), []byte("abc"), 0o600))
	mustDo(t, os.Symlink("file", filepath.Join(dir, "link")))
	mustDo(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600))

	reader := must(env.OpenDirReader(background, "dir"))
	if _, err := reader.Next(abortedContext(), 10); fileErrorCode(t, err) != durableenv.FileErrorAborted {
		t.Fatalf("an aborted Next = %v, want aborted", err)
	}
	kinds := map[string]durableenv.FileKind{}
	var done bool
	for range 10 {
		page := must(reader.Next(background, 2))
		for _, entry := range page.Entries {
			kinds[entry.Name] = entry.Kind
		}
		if done = page.Done; done {
			break
		}
	}
	want := map[string]durableenv.FileKind{"sub": durableenv.FileKindDirectory, "file": durableenv.FileKindFile, "link": durableenv.FileKindSymlink}
	if !done || len(kinds) != len(want) {
		t.Fatalf("listed %v (done %t), want exactly %v: the aborted call must consume nothing and the FIFO must be skipped", kinds, done, want)
	}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("%s has kind %q, want %q", name, kinds[name], kind)
		}
	}
	if page := must(reader.Next(background, 5)); len(page.Entries) != 0 || !page.Done {
		t.Errorf("a call after the end returned %+v, want an empty page that is still done", page)
	}

	// A page larger than the directory reaches the end in the same call: done comes with the last entries (index.ts:163, node.ts:566-571).
	whole := must(env.OpenDirReader(background, "dir"))
	if page := must(whole.Next(background, 100)); len(page.Entries) != len(want) || !page.Done {
		t.Errorf("a page larger than the directory returned %d entries, done %t; want %d entries with done", len(page.Entries), page.Done, len(want))
	}
	mustDo(t, whole.Close(background))

	mustDo(t, reader.Close(background))
	mustDo(t, reader.Close(background))
	if _, err := reader.Next(abortedContext(), 1); fileErrorCode(t, err) != durableenv.FileErrorAborted {
		t.Errorf("an aborted Next on a closed reader = %v, want aborted before closed", err)
	}
	if _, err := reader.Next(background, 1); fileErrorCode(t, err) != durableenv.FileErrorInvalid {
		t.Errorf("Next on a closed reader = %v, want an invalid FileError", err)
	}
}
