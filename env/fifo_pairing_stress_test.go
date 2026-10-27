//go:build !windows

package env

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A read of a FIFO returns after its writer closed, however the read and the close interleave. On macOS a read that
// enters the kernel while the writer closes can sleep for good (see readFIFO); concurrent rounds with a writer that
// closes right after its write meet that within seconds. Each round is a fresh FIFO, an in-process writer and
// os.ReadFile, which reads until end of file as the environments' whole-file reads do. The end-of-file signal
// readFIFO sends after the writer exited must wake every such read with the writer's bytes.
//
// The rounds run with the Go runtime's asynchronous preemption off, because macOS pairs the two ends of a FIFO badly when a signal reaches a
// blocking open (testenv.RerunWithoutAsyncPreemption explains the kernel path). The runtime's preemption signals (SIGURG) reach an open that has
// just been entered when so many goroutines run, and the test then fails on a round that has nothing to do with the end-of-file flag: in
// eight test processes at a time on macOS 26, each running the 12,000 rounds four times, three failed with a read that returned nothing
// and a writer that got "broken pipe", and in a second batch two did, once with a writer whose open never finished. A C program with one thread
// opening a fresh FIFO for reading, a thread signaling it and a writer opening and writing measured 15-24 failed writes in 20,000 rounds; the
// same program without the signals, none in 160,000. The runtime sends no preemption signal with asyncpreemptoff=1, and 576,000 rounds under
// the same load then passed.
func TestReadFIFOReturnsAfterWriterClosesWhateverTheInterleaving(t *testing.T) {
	if testenv.RerunWithoutAsyncPreemption(t) {
		return
	}
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
				exited := make(chan struct{})
				var writeErr error
				go func() {
					defer close(exited)
					file, err := os.OpenFile(fifo, os.O_WRONLY, 0)
					if err != nil {
						writeErr = err
						return
					}
					_, writeErr = file.WriteString("fifo text\n")
					_ = file.Close()
				}()
				value, err := readFIFOWithin(fifo, exited, func() outcome {
					data, err := os.ReadFile(fifo)
					return result(string(data), err)
				}, fifoEOFRedeliveryInterval, 10*time.Second)
				if err != nil {
					failures <- fmt.Sprintf("round %d: %v", round, err)
					return
				}
				if value.err != nil || value.value != "fifo text\n" {
					writer := "the writer has not exited"
					select {
					case <-exited:
						writer = fmt.Sprintf("writer error %v", writeErr)
					default:
					}
					failures <- fmt.Sprintf("round %d: read returned %#v, err %v (%s)", round, value.value, value.err, writer)
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
