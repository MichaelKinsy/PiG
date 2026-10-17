//go:build !windows

package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Ports packages/durable/src/env/node.ts NodeDirReader.next (:545-585) and openDirReader (:1128): the pinned upstream NodeExecutionEnv pages the same
// directories under Node, and the Go DirReader returns the same page sizes, done flags, entries (name, path, kind, size) and error codes: files, empty
// files, directories, symbolic links to files and directories and a dangling one (lstat kinds), a FIFO (an unsupported kind, skipped and not counted
// against maxEntries), non-ASCII names, an empty directory, a page size above, at and below the entry count, a read after the end, and a count that
// is not a positive safe integer.
// mutation-checked: lstat replaced by stat, a page of more entries than asked, ending the listing one page early, accepting a zero count, keeping an
// unsupported entry and a blocking open of the FIFO path each fail it. Skipping an entry that vanishes between readdir and lstat (ENOENT) is not
// reachable from a quiescent tree and has no case here.
func TestDirReaderPagesLikePi(t *testing.T) {
	env, root := newTestEnv(t)
	write := func(path, content string) {
		full := filepath.Join(root, path)
		mustDo(t, os.MkdirAll(filepath.Dir(full), 0o755))
		mustDo(t, os.WriteFile(full, []byte(content), 0o600))
	}
	write("mixed/file.txt", "hello")
	write("mixed/empty", "")
	write("mixed/é ü.txt", "x")
	write("mixed/日本語", "123456789")
	write("mixed/sub/inner", "i")
	mustDo(t, os.Symlink("file.txt", filepath.Join(root, "mixed", "link-to-file")))
	mustDo(t, os.Symlink("sub", filepath.Join(root, "mixed", "link-to-dir")))
	mustDo(t, os.Symlink("missing-target", filepath.Join(root, "mixed", "dangling")))
	mustDo(t, syscall.Mkfifo(filepath.Join(root, "mixed", "pipe"), 0o600))
	mustDo(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	for i := range 25 {
		write(fmt.Sprintf("many/f%02d", i), strings.Repeat("a", i))
	}
	write("fifos/a", "a")
	for _, name := range []string{"p1", "p2", "p3"} {
		mustDo(t, syscall.Mkfifo(filepath.Join(root, "fifos", name), 0o600))
	}
	write("fifos/b", "bb")

	paths := []string{"mixed", "empty", "many", "fifos", "."}
	sizes := []int{1, 2, 3, 4, 7, 100}
	bad := []float64{0, -1, 1.5, 9007199254740992}
	open := []string{"missing", "mixed/file.txt", "mixed/pipe", "mixed/dangling"}
	input, err := json.Marshal(map[string]any{"dir": root, "paths": paths, "sizes": sizes, "bad": bad, "open": open})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/dir_reader.mjs", repo)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct {
		Listings map[string]struct {
			Pages []struct {
				N    int  `json:"n"`
				Done bool `json:"done"`
			} `json:"pages"`
			Entries []struct {
				Name string `json:"name"`
				Path string `json:"path"`
				Kind string `json:"kind"`
				Size int64  `json:"size"`
			} `json:"entries"`
		} `json:"listings"`
		Bad  map[string]string `json:"bad"`
		Open map[string]string `json:"open"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		for _, size := range sizes {
			key := fmt.Sprintf("%s %d", path, size)
			expected, ok := want.Listings[key]
			if !ok {
				t.Fatalf("Pi gave no listing for %s", key)
			}
			reader := must(env.OpenDirReader(background, path))
			type page struct {
				N    int
				Done bool
			}
			var pages []page
			var entries []string
			for {
				next := must(reader.Next(background, size))
				pages = append(pages, page{len(next.Entries), next.Done})
				for _, e := range next.Entries {
					entries = append(entries, fmt.Sprintf("%s|%s|%s|%d", e.Name, e.Path, e.Kind, e.Size))
				}
				if next.Done {
					break
				}
			}
			after := must(reader.Next(background, size))
			pages = append(pages, page{len(after.Entries), after.Done})
			mustDo(t, reader.Close(background))
			slices.Sort(entries)
			var wantPages []page
			for _, p := range expected.Pages {
				wantPages = append(wantPages, page{p.N, p.Done})
			}
			var wantEntries []string
			for _, e := range expected.Entries {
				wantEntries = append(wantEntries, fmt.Sprintf("%s|%s|%s|%d", e.Name, e.Path, e.Kind, e.Size))
			}
			slices.Sort(wantEntries)
			if !slices.Equal(pages, wantPages) {
				t.Errorf("%s: pages %v, Pi %v", key, pages, wantPages)
			}
			if !slices.Equal(entries, wantEntries) {
				t.Errorf("%s: entries %q, Pi %q", key, entries, wantEntries)
			}
		}
	}
	for _, size := range bad {
		reader := must(env.OpenDirReader(background, paths[0]))
		_, err := reader.Next(background, int(size))
		got := "ok"
		if err != nil {
			got = string(fileErrorCode(t, err))
		}
		if size == 1.5 {
			// Go's count is an int: a fractional count cannot be expressed, so only Pi's answer is pinned.
			if want.Bad["1.5"] != "invalid" {
				t.Errorf("Pi accepts a count of 1.5: %q", want.Bad["1.5"])
			}
			continue
		}
		if got != want.Bad[strconv.FormatFloat(size, 'f', -1, 64)] {
			t.Errorf("Next(%v) = %s, Pi %s", size, got, want.Bad[strconv.FormatFloat(size, 'f', -1, 64)])
		}
		mustDo(t, reader.Close(background))
	}
	for _, path := range open {
		_, err := env.OpenDirReader(background, path)
		got := "ok"
		if err != nil {
			got = string(fileErrorCode(t, err))
		}
		if got != want.Open[path] {
			t.Errorf("OpenDirReader(%q) = %s, Pi %s", path, got, want.Open[path])
		}
	}
}

// packages/durable/src/env/node.ts:1128 openDirReader calls opendir, which refuses a FIFO with ENOTDIR at once. Opening it read-only and blocking waited
// for a writer, so the call never returned and ignored its context.
// mutation-checked: os.Open in place of the nonblocking open leaves the call waiting and fails this test by its deadline.
func TestOpenDirReaderOnAFIFOReturnsNotDirectory(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600))
	done := make(chan error, 1)
	go func() {
		_, err := env.OpenDirReader(background, "pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if code := fileErrorCode(t, err); code != "not_directory" {
			t.Fatalf("OpenDirReader(FIFO) = %s, want not_directory", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OpenDirReader on a FIFO is waiting for a writer")
	}
}
