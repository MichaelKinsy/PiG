package env

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// fifoReadBound is how long a read of a FIFO may take before the test fails with the step and the writer's state.
const fifoReadBound = 20 * time.Second

// fifoEOFRedeliveryInterval is how long after its writer exited a read that has not returned waits before the test
// signals end of file again.
const fifoEOFRedeliveryInterval = 50 * time.Millisecond

// startFIFOWriter runs a shell that writes text to the FIFO, as upstream's differential test does (a writer for each
// blocking open of the FIFO). The returned channel is closed once the shell has exited, so its end of the FIFO is closed.
func startFIFOWriter(t *testing.T, fifo, text string) <-chan struct{} {
	t.Helper()
	command := exec.Command("sh", "-c", `printf '%s' "$1" > "$2"`, "sh", text, fifo)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-exited })
	return exited
}

// readFIFO is read(), which opens and reads the FIFO whose only writer is the one that closes exited, and returns its
// outcome, or fails the test with the step if it does not return.
//
// macOS can leave a read of a FIFO asleep for good after the writer is gone. Measured on macOS 26 (arm64) with a plain
// syscall.Open and syscall.Read loop and an in-process writer: a read that returned the writer's bytes is
// followed by one that enters the kernel while the writer is closing, never returns although the writer has exited
// and the FIFO has no writer (a second open with O_NONBLOCK reads 0 at once), and returns 0 as soon as any program
// opens and closes a write end. So the end-of-file flag of the FIFO's receive side was lost (XNU
// bsd/miscfs/fifofs/fifo_vnops.c: fifo_close_internal sets SS_CANTRCVMORE with the last writer; fifo_read clears it
// after a read that returned no bytes). A 1 ms pause between the writer's write and its close left no hang in
// 22659 rounds; with none it hangs a few hundred rounds apart. Node's libuv reads the same way and is as
// exposed. Neither the product nor the writer can order the read after the close, because the read starts the moment
// the open completes. So once the writer has exited and the read still has not returned, this signals end of file
// again by opening and closing a write end. It never writes, so the bytes the read returns are the writer's.
func readFIFO(t *testing.T, step, fifo string, exited <-chan struct{}, read func() outcome) outcome {
	t.Helper()
	value, err := readFIFOWithin(fifo, exited, read, fifoEOFRedeliveryInterval, fifoReadBound)
	if err != nil {
		t.Fatalf("%s of the FIFO: %v", step, err)
	}
	return value
}

func readFIFOWithin(fifo string, exited <-chan struct{}, read func() outcome, interval, bound time.Duration) (outcome, error) {
	done := make(chan outcome, 1)
	go func() { done <- read() }()
	deadline := time.After(bound)
	select {
	case value := <-done:
		return value, nil
	case <-exited:
	case <-deadline:
		return outcome{}, fmt.Errorf("did not return within %s and its writer had not exited", bound)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case value := <-done:
			return value, nil
		case <-ticker.C:
			redeliverFIFOEOF(fifo)
		case <-deadline:
			return outcome{}, fmt.Errorf("did not return within %s although its writer had exited and end of file was signaled again", bound)
		}
	}
}

// redeliverFIFOEOF opens and closes a write end of the FIFO at path without blocking, which is what a writer that
// closes does for a reader: the last close of a write end signals end of file. With no reader open the open fails
// with ENXIO and there is nothing to signal. Windows has no FIFOs and the tests that use them skip there; the check
// is made at run time so env carries no Windows-only test file (automation/ci/windows-test-packages.sh selects those).
func redeliverFIFOEOF(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	file, err := os.OpenFile(path, os.O_WRONLY|nonBlock, 0)
	if err != nil {
		return
	}
	_ = file.Close()
}
