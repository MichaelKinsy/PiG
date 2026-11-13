//go:build unix

package tui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// writerWatching answers its first prompt with yes and its second (where the file is a link) with no, and notes whether a
// writer waiting for a reader of the FIFO was released before that second prompt.
type writerWatching struct {
	released chan struct{}
	asked    int
	early    bool
}

func (w *writerWatching) Columns() int { return 80 }
func (w *writerWatching) Write(string) {}
func (w *writerWatching) Ask([]string) (bool, error) {
	w.asked++
	if w.asked == 1 {
		return true, nil
	}
	select {
	case <-w.released:
		w.early = true
	case <-time.After(300 * time.Millisecond):
	}
	return false, nil
}

// Nothing is opened before the yes to "It links here": a FIFO opened without blocking (even once, to read its identity) releases
// a writer that waits for a reader, which then gets EPIPE when the reader closes.
func TestAttachingAFileOpensNothingBeforeTheLinkIsConfirmed(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	prompt := &writerWatching{released: make(chan struct{})}
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		if writer, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			close(prompt.released)
			_ = writer.Close()
		}
	}()
	// The writer blocks in its open until something opens the FIFO for reading: it has to be waiting before Attach runs.
	time.Sleep(100 * time.Millisecond)
	if _, err := Attach(link, prompt, "linux"); err != nil && prompt.asked < 2 {
		t.Fatalf("error = %v before the second prompt", err)
	}
	if prompt.early {
		t.Fatal("the FIFO was opened before the link was confirmed")
	}
	// Let the waiting writer go.
	if reader, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		_ = reader.Close()
	}
	<-opened
}
