package execfile_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/execfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// readerFunc is a reader the test controls while the destination is open.
type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// A child forked while the file is open for writing holds the descriptor until it calls exec, and the exec of the file fails with ETXTBSY. Go forks under the write side of syscall.ForkLock, so Write holds the read side while the file is open.
func TestWriteStartsNoChildWhileTheFileIsOpen(t *testing.T) {
	name := filepath.Join(t.TempDir(), "runner")
	var chunks int
	content := readerFunc(func(p []byte) (int, error) {
		chunks++
		if chunks == 3 {
			return 0, io.EOF
		}
		// Every read of the content happens with the file open for writing.
		testenv.RequireForkLocked(t)
		return copy(p, "chunk\n"), nil
	})
	if err := execfile.Write(name, os.O_CREATE|os.O_EXCL, 0o755, content); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != "chunk\nchunk\n" {
		t.Fatalf("file = %q, %v", got, err)
	}
	if !syscall.ForkLock.TryLock() {
		t.Fatal("Write returned with the fork lock held")
	}
	syscall.ForkLock.Unlock()
}

func TestWriteReleasesTheForkLockWhenItFails(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := errors.New("content failed")
	cases := map[string]func() error{
		"open fails": func() error {
			return execfile.Write(existing, os.O_CREATE|os.O_EXCL, 0o755, strings.NewReader("new"))
		},
		"content fails": func() error {
			return execfile.Write(filepath.Join(dir, "partial"), os.O_CREATE|os.O_EXCL, 0o755, io.MultiReader(strings.NewReader("half"), readerFunc(func([]byte) (int, error) { return 0, broken })))
		},
	}
	for name, write := range cases {
		t.Run(name, func(t *testing.T) {
			err := write()
			if err == nil {
				t.Fatal("Write succeeded")
			}
			if name == "content fails" && !errors.Is(err, broken) {
				t.Fatalf("error = %v, want the content's", err)
			}
			if !syscall.ForkLock.TryLock() {
				t.Fatal("Write returned with the fork lock held")
			}
			syscall.ForkLock.Unlock()
		})
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "old" {
		t.Fatalf("an exclusive create replaced the file: %q, %v", got, err)
	}
}

func TestWriteTruncatesWhenAsked(t *testing.T) {
	name := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(name, bytes.Repeat([]byte("old"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := execfile.Write(name, os.O_CREATE|os.O_TRUNC, 0o755, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != "new" {
		t.Fatalf("file = %q, %v", got, err)
	}
}

// Write holds the fork lock for one chunk at a time: a process start that arrives during a chunk runs between two chunks, when the file is partly written and no descriptor of it is open, not after the whole copy.
func TestWriteReleasesTheForkLockBetweenChunks(t *testing.T) {
	defer execfile.SetChunkSize(8)()
	name := filepath.Join(t.TempDir(), "runner")

	type observation struct {
		size       int64
		openWriter bool
	}
	observed := make(chan observation, 1)
	var reads int
	content := readerFunc(func(p []byte) (int, error) {
		reads++
		switch reads {
		case 1:
			// A fork: it takes the write side of the lock, so it waits for the chunk in progress and runs before the next chunk.
			go func() {
				syscall.ForkLock.Lock()
				defer syscall.ForkLock.Unlock()
				info, err := os.Stat(name)
				if err != nil {
					t.Error(err)
				}
				observed <- observation{info.Size(), hasOpenDescriptor(name)}
			}()
		case 2:
			// Wait until that fork is queued: a read lock is refused while a writer waits.
			for syscall.ForkLock.TryRLock() {
				syscall.ForkLock.RUnlock()
				runtime.Gosched()
			}
		}
		if reads > 6 {
			return 0, io.EOF
		}
		return copy(p, "abcd"), nil
	})
	if err := execfile.Write(name, os.O_CREATE|os.O_EXCL, 0o755, content); err != nil {
		t.Fatal(err)
	}
	got := <-observed
	if got.size != 8 {
		t.Fatalf("the fork ran with %d bytes written, want the first chunk of 8: the lock was held across chunks", got.size)
	}
	if got.openWriter {
		t.Fatal("a descriptor of the file was open when the fork ran")
	}
	if data, err := os.ReadFile(name); err != nil || string(data) != strings.Repeat("abcd", 6) {
		t.Fatalf("file = %q, %v", data, err)
	}
}

// hasOpenDescriptor reports whether this process holds name open. It can tell only where /proc/self/fd exists.
func hasOpenDescriptor(name string) bool {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name())); err == nil && target == name {
			return true
		}
	}
	return false
}

// Truncation and exclusive creation apply to the first chunk only; the later chunks continue where the last one ended.
func TestWriteAcrossChunksKeepsAllContent(t *testing.T) {
	defer execfile.SetChunkSize(8)()
	name := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(name, bytes.Repeat([]byte("old"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("0123456789", 5)
	if err := execfile.Write(name, os.O_CREATE|os.O_TRUNC, 0o755, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != want {
		t.Fatalf("file = %q, %v", got, err)
	}
	// A length that is a multiple of the chunk ends with an empty chunk.
	exact := strings.Repeat("x", 16)
	if err := execfile.Write(filepath.Join(filepath.Dir(name), "exact"), os.O_CREATE|os.O_EXCL, 0o755, strings.NewReader(exact)); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(filepath.Dir(name), "exact")); err != nil || string(got) != exact {
		t.Fatalf("exact file = %q, %v", got, err)
	}
	empty := filepath.Join(filepath.Dir(name), "empty")
	if err := execfile.Write(empty, os.O_CREATE|os.O_EXCL, 0o755, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty); err != nil {
		t.Fatalf("empty content did not create the file: %v", err)
	}
}

// Bytes is written from the slice in chunks and ends exactly at its length.
func TestWriteBytesAcrossChunks(t *testing.T) {
	defer execfile.SetChunkSize(8)()
	dir := t.TempDir()
	for _, length := range []int{0, 1, 7, 8, 9, 16, 41} {
		want := strings.Repeat("z", length)
		name := filepath.Join(dir, "f"+strings.Repeat("x", length))
		if err := execfile.Write(name, os.O_CREATE|os.O_EXCL, 0o755, execfile.Bytes([]byte(want))); err != nil {
			t.Fatalf("length %d: %v", length, err)
		}
		if got, err := os.ReadFile(name); err != nil || string(got) != want {
			t.Fatalf("length %d: file = %q, %v", length, got, err)
		}
	}
}
