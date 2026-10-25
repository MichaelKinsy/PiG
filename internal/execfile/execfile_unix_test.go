//go:build unix

package execfile_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/execfile"
)

// newlines is n bytes of newline: padding that lengthens a copy without a buffer of n bytes.
type newlines struct{ n int }

var newlineBlock = bytes.Repeat([]byte{'\n'}, 32<<10)

func (r *newlines) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), r.n)], newlineBlock)
	r.n -= n
	return n, nil
}

// The failure the fix closes: one goroutine writes a program and runs it while others start processes. Before Write held the fork lock, a child forked while the file was open still held its descriptor when the program started, and the start failed with "text file busy" (golang.org/issue/22315).
func TestStartAfterWriteIsNeverBusy(t *testing.T) {
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no true(1) to fork: %v", err)
	}
	dir := t.TempDir()

	stop := make(chan struct{})
	var forkers sync.WaitGroup
	for range 32 {
		forkers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = exec.Command(trueBinary).Run()
				}
			}
		})
	}
	t.Cleanup(func() {
		close(stop)
		forkers.Wait()
	})

	const programs = 20
	var busy int
	for i := range programs {
		// A script the shell stops reading at its first command; the padding lengthens the write the way a compiled binary does.
		content := io.MultiReader(strings.NewReader("#!/bin/sh\nexit 0\n"), &newlines{n: 2 << 20})
		name := filepath.Join(dir, fmt.Sprintf("program-%d", i))
		if err := execfile.Write(name, os.O_CREATE|os.O_EXCL, 0o755, content); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(name, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command(name).Run(); err != nil {
			if !errors.Is(err, syscall.ETXTBSY) {
				t.Fatalf("start %s: %v", name, err)
			}
			busy++
		}
	}
	if busy != 0 {
		t.Fatalf("%d of %d programs failed to start with %v", busy, programs, syscall.ETXTBSY)
	}
}

// Write opens one descriptor and closes it on every path, so a build that writes many binaries leaks none.
func TestWriteLeavesNoDescriptorOpen(t *testing.T) {
	open := func() int {
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Skipf("cannot list open descriptors: %v", err)
		}
		return len(entries)
	}
	dir := t.TempDir()
	before := open()
	for i := range 100 {
		name := filepath.Join(dir, fmt.Sprintf("program-%d", i))
		if err := execfile.Write(name, os.O_CREATE|os.O_EXCL, 0o755, strings.NewReader("ok")); err != nil {
			t.Fatal(err)
		}
		if execfile.Write(name, os.O_CREATE|os.O_EXCL, 0o755, strings.NewReader("again")) == nil {
			t.Fatal("an exclusive create replaced a file")
		}
		failing := io.MultiReader(strings.NewReader("half"), &failingReader{})
		if execfile.Write(filepath.Join(dir, fmt.Sprintf("partial-%d", i)), os.O_CREATE|os.O_EXCL, 0o755, failing) == nil {
			t.Fatal("a failing content did not fail the write")
		}
	}
	if after := open(); after != before {
		t.Fatalf("%d descriptors open after the writes, %d before", after, before)
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errors.New("content failed") }
