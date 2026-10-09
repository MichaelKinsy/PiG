package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func fileErrorCode(t *testing.T, err error) durableenv.FileErrorCode {
	t.Helper()
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	if !ok {
		t.Fatalf("error = %v, want a FileError", err)
	}
	return fileErr.Code
}

// upstream: packages/durable/src/testing/env-conformance.ts "binary reader reads byte ranges of the opened file": Info describes the opened file, and it and Read are invalid after Close (which is idempotent); Info is the opened file's metadata even after its path is renamed.
// Pi source: packages/durable/src/testing/env-conformance.ts
// mutation-checked: stat-ing the path instead of the open file fails it
// mutation-checked: zeroing the results of BinaryReader.Info fails it
// Pi: packages/durable/src/env/index.ts:98 (info)
func TestBinaryReaderInfoDescribesTheOpenedFile(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "data.txt"), []byte("hello world"), 0o600))
	reader := must(env.OpenBinaryReader(background, "data.txt", nil))
	info := must(reader.Info(background))
	if info.Name != "data.txt" || info.Kind != durableenv.FileKindFile || info.Size != 11 {
		t.Fatalf("info = %+v, want data.txt, file, 11 bytes", info)
	}
	mustDo(t, os.Rename(filepath.Join(root, "data.txt"), filepath.Join(root, "moved.txt")))
	mustDo(t, os.WriteFile(filepath.Join(root, "data.txt"), []byte("a different, longer file"), 0o600))
	if renamed := must(reader.Info(background)); renamed.Size != 11 {
		t.Fatalf("info after the path was replaced = %+v, want the opened file's 11 bytes", renamed)
	}
	if _, err := reader.Info(abortedContext()); fileErrorCode(t, err) != durableenv.FileErrorAborted {
		t.Fatalf("Info under an aborted context: %v", err)
	}
	mustDo(t, reader.Close(background))
	mustDo(t, reader.Close(background))
	if _, err := reader.Info(background); fileErrorCode(t, err) != durableenv.FileErrorInvalid {
		t.Fatalf("Info after Close: %v", err)
	}
}

// upstream: packages/durable/src/testing/env-conformance.ts "directory reader …": OpenDirReader pages a directory's entries with metadata, refuses a missing path (not_found), a file (not_directory) and an aborted context.
// Pi source: packages/durable/src/testing/env-conformance.ts, packages/durable/src/env/node.ts (openDirReader)
// mutation-checked: skipping the not-a-directory check fails it
// mutation-checked: zeroing the results of NodeExecutionEnv.OpenDirReader fails it
// Pi: packages/durable/src/env/node.ts:1128 (openDirReader)
func TestOpenDirReaderPagesEntriesAndRefusesNonDirectories(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.MkdirAll(filepath.Join(root, "dir"), 0o700))
	for _, name := range []string{"x", "y", "z"} {
		mustDo(t, os.WriteFile(filepath.Join(root, "dir", name), []byte(name), 0o600))
	}
	mustDo(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600))
	reader := must(env.OpenDirReader(background, "dir"))
	var names []string
	for range 10 {
		page := must(reader.Next(background, 2))
		if len(page.Entries) > 2 {
			t.Fatalf("a page of at most 2 entries held %d", len(page.Entries))
		}
		for _, entry := range page.Entries {
			if entry.Kind != durableenv.FileKindFile || entry.Size != 1 {
				t.Fatalf("entry %+v, want a one-byte file", entry)
			}
			names = append(names, entry.Name)
		}
		if page.Done {
			break
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"x", "y", "z"}) {
		t.Fatalf("listed %q", names)
	}
	mustDo(t, reader.Close(background))
	if _, err := reader.Next(background, 1); fileErrorCode(t, err) != durableenv.FileErrorInvalid {
		t.Fatalf("Next after Close: %v", err)
	}
	for _, c := range []struct {
		path string
		ctx  context.Context
		want durableenv.FileErrorCode
	}{{"missing", background, durableenv.FileErrorNotFound}, {"file.txt", background, durableenv.FileErrorNotDirectory}, {".", abortedContext(), durableenv.FileErrorAborted}} {
		if _, err := env.OpenDirReader(c.ctx, c.path); fileErrorCode(t, err) != c.want {
			t.Fatalf("OpenDirReader(%q) = %v, want %s", c.path, err, c.want)
		}
	}
}
