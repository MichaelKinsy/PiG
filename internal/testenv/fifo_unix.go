//go:build unix

package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// fifoPayload is larger than any named pipe's buffer, so a Write of it returns only after the copy has consumed the rest.
const fifoPayload = 4 << 20

// CopyFromFIFO runs read, which copies the named pipe it is given to a file it has open for writing, and calls during while the copy is open and waiting for more data. It then ends the pipe, waits for read, and returns what read returned and the bytes it was fed. A test checks in during that the process starts no child while the destination is open (RequireForkLocked).
//
// The pipe holds less than the payload, so the write to it returns only after the copy has read most of the payload; the copy is then inside its read, with its destination open.
func CopyFromFIFO(t testing.TB, read func(fifo string) error, during func()) (payload []byte, err error) {
	t.Helper()
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	payload = make([]byte, fifoPayload)
	for i := range payload {
		payload[i] = byte(i)
	}

	done := make(chan error, 1)
	go func() { done <- read(fifo) }()
	// Opening the write end waits for the reader, and the reader never opens when read fails first.
	writer := make(chan *os.File, 1)
	go func() {
		file, err := os.OpenFile(fifo, os.O_WRONLY, 0) //nolint:gosec // G304: the path names an output or state file that the user or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so writing it grants nothing the user lacks
		if err != nil {
			t.Error(err)
		}
		writer <- file
	}()
	var out *os.File
	select {
	case out = <-writer:
		if out == nil {
			t.FailNow()
		}
	case err := <-done:
		t.Fatalf("the copy ended before it opened the pipe: %v", err)
	}
	if _, err := out.Write(payload); err != nil {
		_ = out.Close()
		t.Fatalf("feed the copy: %v", err)
	}
	during()
	if err := out.Close(); err != nil {
		t.Fatal(fmt.Errorf("end the pipe: %w", err))
	}
	return payload, <-done
}
