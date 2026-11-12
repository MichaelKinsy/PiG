// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// setScreenWriterChoice makes choice what the process has been told about the screen writer until the test ends.
func setScreenWriterChoice(t *testing.T, choice int32) {
	t.Helper()
	screenWriterMu.Lock()
	previous := screenWriterChoice
	screenWriterChoice = choice
	screenWriterMu.Unlock()
	t.Cleanup(func() {
		screenWriterMu.Lock()
		screenWriterChoice = previous
		screenWriterMu.Unlock()
	})
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
	if terminal.screen != nil {
		t.Cleanup(terminal.screen.Stop)
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
			if entry.writer == terminal.screen {
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
	if terminal.screen == nil || !swRegistered(terminal) {
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
	if later, _ := swFileTerminal(t); later.screen != nil {
		t.Errorf("a terminal built after SetScreenWriter(false) has a screen writer")
	}
	SetScreenWriter(true)
	if later, _ := swFileTerminal(t); later.screen == nil {
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
	setScreenWriterChoice(t, screenWriterUnset)
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
	if later, _ := swFileTerminal(t); later.screen == nil {
		t.Errorf("a terminal built afterwards has no screen writer")
	}
}

// NoScreenWriter leaves no terminal with a screen writer, the environment asking for one included.
func TestNoScreenWriter(t *testing.T) {
	for _, value := range []string{"", "1", "pi"} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv(screenWriterEnv, value)
			setScreenWriterChoice(t, screenWriterOn)
			terminal, sent := swFileTerminal(t)
			NoScreenWriter()
			if writers := screenWriters.Load(); writers != nil {
				t.Errorf("%d screen writers are registered after NoScreenWriter", len(*writers))
			}
			var wrote strings.Builder
			for _, data := range append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "one"))) {
				terminal.Write(data)
				wrote.WriteString(data)
			}
			if got := sent(); got != wrote.String() || terminal.ScreenWriterActive() {
				t.Errorf("the terminal was sent %q, want exactly %q; active = %v", got, wrote.String(), terminal.ScreenWriterActive())
			}
			if later, _ := swFileTerminal(t); later.screen != nil {
				t.Errorf("a terminal built after NoScreenWriter has a screen writer")
			}
		})
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
