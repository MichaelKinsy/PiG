// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter"
)

// setScreenWriterChoice makes choice what the process has been told about the screen writer until the test ends, when the process's
// terminal and the registered writers are again what they were.
func setScreenWriterChoice(t *testing.T, choice int32) {
	t.Helper()
	screenWriterMu.Lock()
	previous, writer, writers := screenWriterChoice, processTerminal.screen.Load(), screenWriters.Load()
	screenWriterChoice = choice
	screenWriterMu.Unlock()
	t.Cleanup(func() {
		screenWriterMu.Lock()
		screenWriterChoice = previous
		if given := processTerminal.screen.Swap(writer); given != nil && given != writer {
			given.Stop()
		}
		screenWriters.Store(writers)
		screenWriterMu.Unlock()
	})
}

// What the process's terminal had when the package was initialized, before any test ran.
var swAtInit = struct {
	writer     *screenwriter.Writer
	registered bool
}{processTerminal.screen.Load(), screenWriters.Load() != nil}

// The process's terminal is built when the package is initialized, without a screen writer, whatever PIG_TUI_SCREEN_WRITER says: the
// test asks the initialized package, in this process and in one started with the variable asking for the writer.
func TestNoScreenWriterBeforeAnInteractiveRun(t *testing.T) {
	if swAtInit.writer != nil || swAtInit.registered {
		t.Errorf("with %s=%q the process's terminal was initialized with a screen writer (%v) or one was registered (%v)", screenWriterEnv, os.Getenv(screenWriterEnv), swAtInit.writer != nil, swAtInit.registered)
	}
	const child = "PIG_TEST_SCREEN_WRITER_INIT_CHILD"
	if os.Getenv(child) != "" {
		return
	}
	for _, value := range []string{"1", "writer", ""} {
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNoScreenWriterBeforeAnInteractiveRun$", "-test.count=1")
		command.Env = append(os.Environ(), screenWriterEnv+"="+value, child+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("%s=%q: %v\n%s", screenWriterEnv, value, err, output)
		}
	}
}

// SetScreenWriter, which interactive mode calls when it starts, is what gives the process's terminal its screen writer: before it no
// terminal has or gets one, the variable asking for it included; a terminal built before it, other than the process's, stays without.
func TestSetScreenWriterGivesTheProcessTerminalItsWriter(t *testing.T) {
	for _, c := range []struct {
		value  string
		writer bool
		want   bool
	}{{"", true, true}, {"1", true, true}, {"1", false, true}, {"", false, false}, {"pi", true, false}, {"pi", false, false}} {
		t.Run(fmt.Sprintf("value=%s/setting=%v", c.value, c.writer), func(t *testing.T) {
			t.Setenv(screenWriterEnv, c.value)
			setScreenWriterChoice(t, screenWriterUnset)
			processTerminal.screen.Store(nil)
			before, _ := swFileTerminal(t)
			if before.screen.Load() != nil || processTerminal.screen.Load() != nil {
				t.Fatalf("a terminal has a screen writer before SetScreenWriter")
			}
			SetScreenWriter(c.writer)
			given := processTerminal.screen.Load()
			if (given != nil) != c.want || swRegistered(processTerminal) != c.want {
				t.Fatalf("after SetScreenWriter(%v) the process's terminal has a screen writer = %v, registered = %v, want %v", c.writer, given != nil, swRegistered(processTerminal), c.want)
			}
			if before.screen.Load() != nil {
				t.Errorf("a terminal built before SetScreenWriter was given a screen writer")
			}
			if later, _ := swFileTerminal(t); (later.screen.Load() != nil) != c.want {
				t.Errorf("a terminal built after SetScreenWriter(%v) has a screen writer = %v, want %v", c.writer, later.screen.Load() != nil, c.want)
			}
			if SetScreenWriter(c.writer); processTerminal.screen.Load() != given {
				t.Errorf("a second SetScreenWriter(%v) replaced the process terminal's screen writer", c.writer)
			}
		})
	}
}

// swFileTerminal is a terminal that writes to a file, as the process's terminal does, and what the file holds.
func swFileTerminal(t *testing.T) (*ProcessTerminal, func() string) {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "terminal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	terminal := NewProcessTerminalWithOutput(nil, nil, file)
	if writer := terminal.screen.Load(); writer != nil {
		t.Cleanup(writer.Stop)
	}
	return terminal, func() string {
		data, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func swRegistered(terminal *ProcessTerminal) bool {
	if writers := screenWriters.Load(); writers != nil {
		for _, entry := range *writers {
			if entry.writer == terminal.screen.Load() {
				return true
			}
		}
	}
	return false
}

// SetScreenWriter(false) reaches a terminal that exists already, as the process's terminal does when the setting is read: its
// writer is dropped before the alternate screen is entered, so its first frame and every later one are the renderers' bytes, and a
// terminal built afterwards has no writer.
func TestSetScreenWriterDropsTheWriterOfATerminalBuiltBefore(t *testing.T) {
	t.Setenv(screenWriterEnv, "")
	setScreenWriterChoice(t, screenWriterOn)
	terminal, sent := swFileTerminal(t)
	if terminal.screen.Load() == nil || !swRegistered(terminal) {
		t.Fatalf("the terminal has no registered screen writer before the setting is applied")
	}
	SetScreenWriter(false)
	if swRegistered(terminal) {
		t.Errorf("the dropped writer is still told about writes past the terminal")
	}
	var wrote strings.Builder
	for _, data := range append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "one")), swFrame(swLine(1, "one!"))) {
		terminal.Write(data)
		wrote.WriteString(data)
		if terminal.ScreenWriterActive() {
			t.Fatalf("the screen writer is active after %q", data)
		}
	}
	if got := sent(); got != wrote.String() {
		t.Errorf("after SetScreenWriter(false) the terminal was sent %q, want exactly %q", got, wrote.String())
	}
	if later, _ := swFileTerminal(t); later.screen.Load() != nil {
		t.Errorf("a terminal built after SetScreenWriter(false) has a screen writer")
	}
	SetScreenWriter(true)
	if later, _ := swFileTerminal(t); later.screen.Load() == nil {
		t.Errorf("a terminal built after SetScreenWriter(true) has no screen writer")
	}
	if terminal.Write(swFrame(swLine(1, "one!"))); !strings.HasSuffix(sent(), swFrame(swLine(1, "one!"))) {
		t.Errorf("a dropped writer translated again after SetScreenWriter(true)")
	}
}

// SetScreenWriter(true) leaves a terminal built before with its writer, and its frames are translated from the first one.
func TestSetScreenWriterKeepsTheWriter(t *testing.T) {
	t.Setenv(screenWriterEnv, "")
	setScreenWriterChoice(t, screenWriterOn)
	terminal, sent := swFileTerminal(t)
	SetScreenWriter(true)
	for _, data := range append(swStarted(), swFrame(swLine(1, "one"))) {
		terminal.Write(data)
	}
	if !terminal.ScreenWriterActive() {
		t.Fatalf("the screen writer is not active on the alternate screen")
	}
	before := len(sent())
	terminal.Write(swFrame(swLine(1, "one")))
	if got := sent()[before:]; got != "" {
		t.Errorf("a frame that changes nothing was sent as %q", got)
	}
}

// A set PIG_TUI_SCREEN_WRITER outranks the setting: with a writer value SetScreenWriter(false) drops nothing.
func TestSetScreenWriterYieldsToTheEnvironment(t *testing.T) {
	t.Setenv(screenWriterEnv, "1")
	setScreenWriterChoice(t, screenWriterOn)
	terminal, sent := swFileTerminal(t)
	SetScreenWriter(false)
	if !swRegistered(terminal) {
		t.Fatalf("SetScreenWriter(false) dropped a writer the environment asked for")
	}
	for _, data := range append(swStarted(), swFrame(swLine(1, "one"))) {
		terminal.Write(data)
	}
	before := len(sent())
	terminal.Write(swFrame(swLine(1, "one")))
	if got := sent()[before:]; got != "" || !terminal.ScreenWriterActive() {
		t.Errorf("a frame that changes nothing was sent as %q, active = %v", got, terminal.ScreenWriterActive())
	}
	if later, _ := swFileTerminal(t); later.screen.Load() == nil {
		t.Errorf("a terminal built afterwards has no screen writer")
	}
}

// ScreenWriterActive may be asked from another goroutine while frames are written and while the writer is dropped (the race
// detector checks the reads); once the drop has returned it is false and stays so.
func TestScreenWriterActiveWhileTheWriterIsDropped(t *testing.T) {
	t.Setenv(screenWriterEnv, "")
	setScreenWriterChoice(t, screenWriterOn)
	terminal, _ := swFileTerminal(t)
	for _, data := range swStarted() {
		terminal.Write(data)
	}
	if !terminal.ScreenWriterActive() {
		t.Fatalf("the screen writer is not active on the alternate screen")
	}
	var readers, writer sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					terminal.ScreenWriterActive()
				}
			}
		})
	}
	dropped := make(chan struct{})
	writer.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-dropped:
				return
			default:
				terminal.Write(swFrame(swLine(1, strings.Repeat("x", i%40))))
			}
		}
	})
	SetScreenWriter(false)
	if terminal.ScreenWriterActive() {
		t.Errorf("active after the writer was dropped")
	}
	close(dropped)
	writer.Wait()
	close(stop)
	readers.Wait()
	terminal.Write(swFrame(swLine(1, "after")))
	if terminal.ScreenWriterActive() {
		t.Errorf("active after the writer was dropped and a frame was written")
	}
}

// ScreenWriterSelected answers as the choice SetScreenWriter makes: a set PIG_TUI_SCREEN_WRITER decides, `pi` or `0` against the
// writer and any other value for it, and the setting decides when the variable is unset or empty.
func TestScreenWriterSelected(t *testing.T) {
	for _, tc := range []struct {
		writer bool
		env    string
		want   bool
	}{
		{true, "", true}, {false, "", false},
		{true, "pi", false}, {true, "0", false}, {false, "pi", false},
		{false, "writer", true}, {false, "1", true}, {true, "writer", true},
	} {
		getenv := func(name string) string {
			if name == screenWriterEnv {
				return tc.env
			}
			return ""
		}
		choice := screenWriterOff
		if tc.writer {
			choice = screenWriterOn
		}
		if got := ScreenWriterSelected(tc.writer, getenv); got != tc.want || got != screenWriterWanted(getenv, choice) {
			t.Errorf("setting selects the writer = %v, %s=%q: selected = %v, want %v (a terminal gets one: %v)", tc.writer, screenWriterEnv, tc.env, got, tc.want, screenWriterWanted(getenv, choice))
		}
	}
}
