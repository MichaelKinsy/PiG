//go:build linux

package codingagent

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

const exitDrainOrderChildEnv = "PIG_TEST_EXIT_DRAIN_ORDER_CHILD"

// Pi's shutdown() ends the theme's color scheme reports and drains the input BEFORE it stops: `this.themeController.disableAutoSync(); await
// this.ui.terminal.drainInput(1000); this.stop();` (interactive-mode.ts:4491-4493 on a signal, :4502-4503 on a quit by keys). drainInput pops the
// keyboard protocol first (terminal.ts:449), and stop() then disposes the active selector, clears the terminal progress, and stops the renderer
// (interactive-mode.ts:7283-7300): so on the terminal, color scheme reports off, keyboard protocol pop, progress clear, then the renderer's cursor show,
// bracketed paste off and, fullscreen, the alternate screen left. Pig wrote the progress clear first of all.
func TestStopInteractiveTuiWritesTheProgressClearAfterTheDrainAsPiDoes(t *testing.T) {
	for _, tuiMode := range []string{"regular", "fullscreen"} {
		t.Run(tuiMode, func(t *testing.T) {
			master, slave := openTestPTY(t)
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStopInteractiveTuiDrainOrderChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), exitDrainOrderChildEnv+"="+tuiMode, "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = slave.Close()
			var captured bytes.Buffer
			copied := make(chan struct{})
			go func() {
				defer close(copied)
				_, _ = io.Copy(&captured, master)
			}()
			waitErr := cmd.Wait()
			select {
			case <-copied:
			case <-time.After(5 * time.Second):
				_ = master.Close()
				<-copied
			}
			if waitErr != nil {
				t.Fatalf("child failed: %v\n%q", waitErr, captured.Bytes())
			}
			output := captured.String()
			from := strings.LastIndex(output, "\x1b]777;mark;stopping\x07")
			to := strings.LastIndex(output, "\x1b]777;mark;stopped\x07")
			if from < 0 || to < from {
				t.Fatalf("no marks around the stop in %q", output)
			}
			stop := output[from:to]
			at := func(sequence string) int {
				i := strings.Index(stop, sequence)
				if i < 0 {
					t.Fatalf("the stop wrote no %q: %q", sequence, stop)
				}
				return i
			}
			// Where the first of each sequence is, in the order Pi writes them.
			order := []string{"\x1b[?2031l", "\x1b[<u", "\x1b]9;4;0\x07", "\x1b[?25h", "\x1b[?2004l"}
			if tuiMode == "fullscreen" {
				order = append(order, "\x1b[?1049l")
			}
			previous := -1
			for _, sequence := range order {
				i := at(sequence)
				if i < previous {
					t.Fatalf("%q is at byte %d, before the byte %d of the sequence that Pi writes before it (%q)\nthe stop wrote %q", sequence, i, previous, order, stop)
				}
				previous = i
			}
		})
	}
}

// TestStopInteractiveTuiDrainOrderChild is the child half of TestStopInteractiveTuiWritesTheProgressClearAfterTheDrainAsPiDoes: a mode with the terminal
// progress shown and the color scheme reports on, in raw mode with the keyboard protocol pushed, stops once.
func TestStopInteractiveTuiDrainOrderChild(t *testing.T) {
	tuiMode := os.Getenv(exitDrainOrderChildEnv)
	if tuiMode == "" {
		t.Skip("runs only as the pty child of TestStopInteractiveTuiWritesTheProgressClearAfterTheDrainAsPiDoes")
	}
	mark := func(name string) { _, _ = os.Stdout.WriteString("\x1b]777;mark;" + name + "\x07") }
	progress := true
	opts := InteractiveModeOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(),
		Model:    &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}},
		Settings: Settings{TuiMode: tuiMode, FullscreenExitOutput: FullscreenExitOutput("resume-hint"), ShowTerminalProgress: &progress},
	}
	opts.TuiMode = tuiMode
	m := newUnmountedSwitchTuiProbe(t, opts, os.Stdout)
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	m.rawRestore, m.rawDrain = restore, drain
	m.mountInteractiveTui(true)
	m.theme().setAutoSync(true)
	m.setTerminalProgress(true)
	mark("stopping")
	m.stopInteractiveTui()
	mark("stopped")
	if !m.tuiStopped.Load() {
		t.Fatal("the mode did not stop")
	}
}
