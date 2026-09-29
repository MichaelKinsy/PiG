//go:build windows

package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestClipboardCommandHidesItsWindow(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "clipboard-helper")
	hideClipboardWindow(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("clipboard helper must not show a Windows console")
	}
}

// Pi clipboard-command.ts:24-28 destroys the parent's stream at abort. The overlapped parent must be registered with Go's poller for Close to cancel a pending operation instead of joining the descendant holding the other end.
func TestClipboardPipeParentSupportsCancellation(t *testing.T) {
	for _, reads := range []bool{false, true} {
		t.Run(map[bool]string{false: "writer", true: "reader"}[reads], func(t *testing.T) {
			parent, child, err := clipboardPipe(reads)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := parent.Close(); err != nil {
					t.Error(err)
				}
				if err := child.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := parent.SetDeadline(time.Now()); err != nil {
				t.Fatalf("parent is not pollable: %v", err)
			}
			if reads {
				_, err = parent.Read(make([]byte, 1))
			} else {
				_, err = parent.Write([]byte("blocked by expired deadline"))
			}
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("expired parent I/O error=%v; want deadline exceeded", err)
			}
			// Libuv's child stdio handles have both attribute rights even when data flows in only one direction (win/pipe.c:274-282).
			var mode uint32 = windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT
			if err := windows.GetNamedPipeHandleState(windows.Handle(child.Fd()), &mode, nil, nil, nil, nil, 0); err != nil {
				t.Fatalf("query child pipe mode: %v", err)
			}
			if err := windows.SetNamedPipeHandleState(windows.Handle(child.Fd()), &mode, nil, nil); err != nil {
				t.Fatalf("set child pipe mode: %v", err)
			}
		})
	}
}
