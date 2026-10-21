//go:build !windows

package env

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// pairFIFO opens both ends of the FIFO at path on the calling goroutine and returns them, the read end in blocking mode.
//
// No thread sleeps in the kernel's open. A blocking open of one end of a FIFO sleeps until the other end is opened, and on
// macOS a signal that reaches the process while a thread sleeps there, or just after the partner woke it, makes the sleep
// fail with EINTR although the partner arrived (XNU bsd/miscfs/fifofs/fifo_vnops.c: fifo_open sleeps with PCATCH and
// _sleep reports a pending signal even for a thread that was woken). fifo_open then takes its error path and closes
// that end again, which is not a retry of the open: when the other end has meanwhile written and closed, no reader and no
// writer is left, and the FIFO's sockets are destroyed with the bytes in them; when the interrupted end is the writer's, the
// reader that found a writer returns from its open and reads end of file before anything was written. Go retries the
// open, so the reader then sleeps in a FIFO that has no writer and nothing to read, until something opens and closes a
// write end, and the read returns no bytes. Measured on macOS 15 (arm64) with this test's rounds and any signal, SIGWINCH
// sent to the process every 200 us or 1 ms: every run lost a round, in the three forms above (the writer's write failing
// with EPIPE, the reader returning end of file before the writer's open returned, and the writer's bytes gone). The signals a
// test process meets are its own children's SIGCHLD and the signals of the tools around it; no round can be made
// immune to them except by never sleeping in open.
//
// Opening the read end with O_NONBLOCK returns at once whether or not a writer exists, and the write end opened after it finds
// the reader and returns at once as well. A read of a FIFO that has a writer then waits for bytes or for the writer to close,
// as after a blocking open, so the read end is returned to blocking mode here, after the writer opened and before the
// first read.
func pairFIFO(path string) (reader, writer *os.File, err error) {
	var fd int
	for {
		if fd, err = syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0); !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if writer, err = os.OpenFile(path, os.O_WRONLY, 0); err != nil {
		_ = syscall.Close(fd)
		return nil, nil, err
	}
	if err = syscall.SetNonblock(fd, false); err != nil {
		_ = syscall.Close(fd)
		_ = writer.Close()
		return nil, nil, &os.PathError{Op: "fcntl", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), writer, nil
}

// Both ends of a FIFO open on one goroutine, with no thread to pair with: pairFIFO never sleeps in open, so a signal cannot
// interrupt the pairing (see pairFIFO). A pairing that opened the read end with a blocking open would wait here for a
// writer that this goroutine has yet to open.
func TestPairFIFOOpensBothEndsOnOneGoroutine(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	type ends struct {
		reader, writer *os.File
		err            error
	}
	paired := make(chan ends, 1)
	go func() {
		reader, writer, err := pairFIFO(fifo)
		paired <- ends{reader, writer, err}
	}()
	var got ends
	select {
	case got = <-paired:
	case <-time.After(fifoReadBound):
		t.Fatal("pairFIFO did not return on one goroutine: it waits in open for an end that only this goroutine can open")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.reader.Close()
	// The writer closes before the first read: the bytes stay for the reader, then end of file.
	if _, err := got.writer.WriteString("fifo text\n"); err != nil {
		t.Fatal(err)
	}
	if err := got.writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(got.reader)
	if err != nil || string(data) != "fifo text\n" {
		t.Fatalf("read %q, %v, want the writer's bytes and end of file", data, err)
	}
}

// A read of a FIFO returns after its writer closed, however the read and the close interleave. On macOS a read that
// enters the kernel while the writer closes can sleep for good (see readFIFO); concurrent rounds with a writer that
// closes right after its write meet that within seconds. Each round is a fresh FIFO paired by pairFIFO, an in-process
// writer, and a read to end of file, as the environments' whole-file reads do. The end-of-file signal readFIFO sends after
// the writer exited must wake every such read with the writer's bytes.
func TestReadFIFOReturnsAfterWriterClosesWhateverTheInterleaving(t *testing.T) {
	const workers, rounds = 8, 1500
	var wg sync.WaitGroup
	failures := make(chan string, workers)
	for range workers {
		dir := t.TempDir()
		wg.Go(func() {
			for round := range rounds {
				fifo := filepath.Join(dir, fmt.Sprintf("fifo-%d", round))
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					failures <- err.Error()
					return
				}
				reader, writer, err := pairFIFO(fifo)
				if err != nil {
					failures <- fmt.Sprintf("round %d: %v", round, err)
					return
				}
				exited := make(chan struct{})
				var writeErr error
				go func() {
					defer close(exited)
					_, writeErr = writer.WriteString("fifo text\n")
					if err := writer.Close(); writeErr == nil {
						writeErr = err
					}
				}()
				value, err := readFIFOWithin(fifo, exited, func() outcome {
					defer reader.Close()
					data, err := io.ReadAll(reader)
					return result(string(data), err)
				}, fifoEOFRedeliveryInterval, 10*time.Second)
				if err != nil {
					failures <- fmt.Sprintf("round %d: %v", round, err)
					return
				}
				<-exited
				if writeErr != nil {
					failures <- fmt.Sprintf("round %d: the writer failed: %v", round, writeErr)
					return
				}
				if value.err != nil || value.value != "fifo text\n" {
					failures <- fmt.Sprintf("round %d: read %q, %v, want the writer's bytes", round, value.value, value.err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
