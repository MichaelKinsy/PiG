//go:build linux || darwin

// SPDX-License-Identifier: MIT

package daemon

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A background job that keeps the terminal open does not keep the request open: after the program exits, ptyExitGrace
// of silence ends the collection and the request answers with the program's exit code.
func TestPtyExitGraceEndsATerminalABackgroundJobHoldsOpen(t *testing.T) {
	p := startServe(t, Options{})
	// The job inherits an ignored hangup, set before it starts, so the hangup the session leader's exit sends cannot end
	// it and it holds the terminal for 30 s.
	p.request(1, Object{"op": "pty", "cwd": t.TempDir(), "argv": []any{"sh", "-c", "trap '' HUP; sleep 30 & echo \"job $!\"; exit 7"}, "cols": float64(80), "rows": float64(24)}, nil)
	start := time.Now()
	var output strings.Builder
	for {
		frame := p.next()
		if frame.ID != 1 {
			continue
		}
		if frame.Kind == FrameEvent {
			output.Write(frame.Payload)
			continue
		}
		if _, after, ok := strings.Cut(output.String(), "job "); ok {
			if pid, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])); err == nil {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			}
		}
		if frame.Kind != FrameResult || object(t, frame)["exitCode"] != float64(7) {
			t.Fatalf("the terminal replied %+v %v, want exitCode 7", frame, frame.JSON)
		}
		break
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("the terminal answered after %v: it waited for the background job instead of the exit grace", elapsed)
	}
}
