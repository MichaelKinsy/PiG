//go:build unix

package nodefs

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// fifoRounds is the number of FIFOs each reader opens under a flood of signals. On macOS an open that is not protected loses the
// bytes in about one round in seven (probed with 1000 rounds of 28 signals each: Go's os.OpenFile lost 15%, Node's fs.readFileSync
// 19%, Node's fs/promises.readFile none because libuv opens on a worker thread), so 200 rounds of two readers leave a run whose
// opens are not protected a chance of (6/7)^400 to pass.
const fifoRounds = 200

// settle is how long a reader gets to finish once its writer has finished. A reader that is not done by then is released, and
// what it read is checked all the same: on macOS a reader blocked in read now and then (about one round in a thousand, with or
// without signals, and with plain os.OpenFile as well) is not woken by its writer's close, although the pipe reports no writer; the
// bytes were delivered, and only the end of file is missing.
const settle = time.Second

// An open of a FIFO that a signal interrupts must not lose the other end's bytes. macOS (fifo_open in XNU's
// bsd/miscfs/fifofs/fifo_vnops.c) closes the FIFO's send or receive side when a signal ends a blocking open, as the opener on the
// other end is let through: the writer's write fails with EPIPE, or the reader sees end of file while the retried open waits for a
// counterpart that is gone. Node avoids it because libuv opens on a worker thread that no signal is delivered to (fs.readFileSync,
// on the main thread, loses the same bytes). SIGURG is the signal Go's runtime sends itself for preemption.
func TestFIFOOpenLosesNoBytesToSignals(t *testing.T) {
	stop := floodSignals(t, "URG")
	defer stop()
	payload := bytes.Repeat([]byte("fifo payload\n"), 5)
	pace := rand.New(rand.NewPCG(1, 2))
	dir := t.TempDir()

	for _, r := range []struct {
		name string
		read func(path string) ([]byte, error)
	}{
		{"ReadFile", ReadFile},
		{"OpenFile", func(path string) ([]byte, error) {
			file, err := OpenFile(path, os.O_RDONLY, 0)
			if err != nil {
				return nil, err
			}
			defer func() { _ = file.Close() }()
			return io.ReadAll(file)
		}},
	} {
		for round := range fifoRounds {
			where := fmt.Sprintf("%s round %d", r.name, round)
			path := filepath.Join(dir, fmt.Sprintf("%s-%d", r.name, round))
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			type result struct {
				data []byte
				err  error
			}
			read := make(chan result, 1)
			go func() {
				data, err := r.read(path)
				read <- result{data, err}
			}()
			// The writer arrives while the reader's open sleeps, as the signals do.
			time.Sleep(time.Duration(pace.IntN(3000)) * time.Microsecond)
			written := make(chan error, 1)
			go func() {
				file, err := OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					written <- err
					return
				}
				_, err = file.Write(payload)
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
				written <- err
			}()

			var got result
			select {
			case err := <-written:
				if err != nil {
					release(path)
					t.Fatalf("%s: the writer failed: %v", where, err)
				}
				select {
				case got = <-read:
				case <-time.After(settle):
					release(path)
					got = <-read
				}
			case got = <-read:
				if err := <-written; err != nil {
					t.Fatalf("%s: the writer failed: %v", where, err)
				}
			case <-time.After(testbudget.Wait(t)):
				release(path)
				t.Fatalf("%s: the open never completed", where)
			}
			if got.err != nil || !bytes.Equal(got.data, payload) {
				t.Fatalf("%s: read %d bytes, error %v; want the %d the writer wrote", where, len(got.data), got.err, len(payload))
			}
		}
	}
}

// release lets a reader that waits in its open or its read of path go: a descriptor open for both reading and writing counts as
// the writer it waits for, and closing it ends the read.
func release(path string) {
	if file, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
		_ = file.Close()
	}
}

// floodSignals has a shell send sig to this process in a tight loop until the returned function is called. Go installs a handler for
// SIGURG, so the signal interrupts the system call of whichever thread the kernel picks. The sender is another process, as the
// signals of a child that exits or of a terminal are; signals sent from inside the process mostly reach the sending thread and
// rarely an open.
func floodSignals(t *testing.T, sig string) (stop func()) {
	t.Helper()
	flood := exec.Command("sh", "-c", "while kill -"+sig+" "+strconv.Itoa(os.Getpid())+" 2>/dev/null; do :; done")
	if err := flood.Start(); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = flood.Process.Kill()
		_ = flood.Wait()
	}
}
