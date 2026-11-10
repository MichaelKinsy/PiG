//go:build windows

package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A Windows terminal ends when its process exits, as a Unix pseudo-terminal's master does: the output is read to its end, then Read reports io.EOF.
// The pseudoconsole's own output pipe never ends while the host holds the pseudoconsole, so tests that wait for pig's output (waitUntil's
// errTerminalClosed, "pig exited while waiting") would otherwise wait out their whole deadline for a pig that has already exited.
func TestConPTYTerminalReadsToEOFAfterTheProcessExits(t *testing.T) {
	process := startOnConPTY(t, `cmd.exe /d /c echo exit-marker`, nil, t.TempDir())
	defer process.close()
	terminal := conPTYTerminal{process}
	type result struct {
		screen string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var screen strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			screen.Write(buf[:n])
			if err != nil {
				done <- result{screen.String(), err}
				return
			}
		}
	}()
	select {
	case got := <-done:
		if !errors.Is(got.err, io.EOF) {
			t.Errorf("terminal ended with %v, want io.EOF", got.err)
		}
		if !strings.Contains(got.screen, "exit-marker") {
			t.Errorf("screen %q lacks the process's output", got.screen)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the terminal did not end after its process exited")
	}
}
