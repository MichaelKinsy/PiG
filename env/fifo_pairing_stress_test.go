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
)

// A read of a FIFO returns after its writer closed, however the read and the close interleave. On macOS a read that
// enters the kernel while the writer closes can sleep for good (see readFIFO); concurrent rounds with a writer that
// closes right after its write meet that within seconds. Each round is a fresh FIFO, an in-process writer and
// os.ReadFile, which reads until end of file as the environments' whole-file reads do. The end-of-file signal
// readFIFO sends after the writer exited must wake every such read with the writer's bytes.
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
				exited := make(chan struct{})
				go func() {
					defer close(exited)
					file, err := os.OpenFile(fifo, os.O_WRONLY, 0)
					if err != nil {
						return
					}
					_, _ = file.WriteString("fifo text\n")
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
					failures <- "read returned a wrong value"
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
