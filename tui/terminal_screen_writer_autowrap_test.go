// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package tui

import (
	"strings"
	"testing"
	"time"
)

// The fullscreen renderer enters the alternate screen with autowrap turned off, so the screen writer translates the frames written
// after its start. Its stop turns autowrap on and, in its next write, leaves the alternate screen, whether or not it keeps the
// screen: frames written through the terminal after it reach the terminal as they came. The writer translates only while autowrap
// is known to be off (D624); were the stop to keep the alternate screen shown, that rule alone would send those frames as they came.
func TestScreenWriterTranslatesOnlyUnderTheRenderersAutowrap(t *testing.T) {
	t.Setenv("PI_TUI_WRITE_LOG", "")
	t.Setenv("PI_HARDWARE_CURSOR", "")
	s := newSWScreen(t, true, 40, 10, 4096, 0)
	writer := s.term.screen.Load()
	// No settle write: the counts below are of the frames alone.
	writer.Stop()
	off := false
	screen := NewTuiAltScreen(s.term, &off, "", TuiAltScreenOptions{Mouse: &off, CopyOnSelect: &off})
	screen.Add(&fixedLinesComponent{lines: []string{"hello from the alt screen"}})
	screen.SetRenderDispatcher(func(func()) {})
	screen.Start()
	if !writer.Active() {
		t.Fatal("the screen writer does not see the alternate screen the renderer entered")
	}
	probe := func(text string) string { return swFrame(swLine(3, text+strings.Repeat(" ", 40-len(text)))) }
	started := writer.Stats()
	s.writes(probe("probe 1"), probe("probe 2"), probe("probe 3"))
	running := writer.Stats()
	if running.Translated == started.Translated || running.Wrapping != 0 {
		t.Fatalf("after the renderer's start, of 3 frames %d were translated and %d sent as they came for autowrap",
			running.Translated-started.Translated, running.Wrapping)
	}
	screen.StopWithOptions(StopOptions{PreserveScreen: true})
	if writer.Active() {
		t.Fatal("after the renderer's stop the writer still sees the alternate screen: the stop no longer leaves it, and frames written now are sent as they came only because autowrap is not known to be off")
	}
	stopped := writer.Stats()
	four, five := probe("probe 4"), probe("probe 5")
	s.writes(four, five)
	received := ""
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		received = string(s.data)
		s.mu.Unlock()
		if strings.Contains(received, five) {
			break
		}
	}
	if !strings.Contains(received, four) || !strings.Contains(received, five) || writer.Stats().Translated != stopped.Translated {
		t.Errorf("after the renderer's stop the frames did not reach the terminal as they came (%d translated)", writer.Stats().Translated-stopped.Translated)
	}
}
