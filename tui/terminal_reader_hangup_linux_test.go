//go:build linux

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openHangupPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}

// terminal.ts reads process.stdin with a "data" listener and no "error" listener, and Node reports the end of input every time the master side of the pseudo-terminal closes. Linux answers a slave read that races the hang-up with either 0 or EIO, so the reader must turn the EIO of a hung-up terminal into the end of input; the interactive loop then keeps the session running when the output terminal is alive (#165).
func TestTerminalReaderReportsEndOfInputWhenThePseudoTerminalMasterCloses(t *testing.T) {
	for i := range 300 {
		master, slave := openHangupPTY(t)
		reader, err := newTerminalReader(context.Background(), slave)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := reader.read(-1); result <- err }()
		if i%2 == 1 {
			time.Sleep(time.Millisecond)
		}
		if err := master.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, io.EOF) {
				t.Fatalf("run %d: read after the master closed = %v, want the end of input", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("run %d: the reader never saw the master close", i)
		}
		reader.close()
		_ = slave.Close()
	}
}
