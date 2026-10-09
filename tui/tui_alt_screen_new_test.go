package tui

import (
	"bytes"
	"strings"
	"testing"
)

// tui-alt-screen.ts:255-282 new TuiAltScreen(terminal, showHardwareCursor, logDirectory, options = {}): the screen draws on the terminal it is
// given and takes its size from it, shows the hardware cursor as asked (the PI_HARDWARE_CURSOR default when it is not), keeps the log
// directory and applies options (mouse, copyOnSelect).
func TestNewTuiAltScreenDrawsOnItsTerminalAndTakesItsSize(t *testing.T) {
	t.Setenv("PI_TUI_WRITE_LOG", "")
	t.Setenv("PI_HARDWARE_CURSOR", "")
	t.Setenv("COLUMNS", "41")
	t.Setenv("LINES", "13")
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	on, off := true, false
	screen := NewTuiAltScreen(terminal, &on, "/var/log/pi", TuiAltScreenOptions{Mouse: &off, CopyOnSelect: &off})
	if screen.Width() != 41 || screen.Height() != 13 {
		t.Fatalf("size = %dx%d, want the terminal's 41x13", screen.Width(), screen.Height())
	}
	if !screen.GetShowHardwareCursor() {
		t.Fatal("showHardwareCursor = true was not kept")
	}
	if screen.logDirectory != "/var/log/pi" {
		t.Fatalf("logDirectory = %q", screen.logDirectory)
	}
	if screen.Terminal() != Terminal(terminal) {
		t.Fatal("Terminal() is not the terminal the screen was built on")
	}
	if screen.mouseEnabled || screen.GetCopyOnSelect() {
		t.Fatalf("options not applied: mouse=%v copyOnSelect=%v", screen.mouseEnabled, screen.GetCopyOnSelect())
	}
	screen.Add(&fixedLinesComponent{lines: []string{"hello from the alt screen"}})
	screen.SetRenderDispatcher(func(func()) {})
	screen.Start()
	defer screen.StopWithOptions(StopOptions{PreserveScreen: true})
	if !strings.Contains(out.String(), "hello from the alt screen") {
		t.Fatalf("the terminal received %q, not the frame", out.String())
	}
	if NewTuiAltScreen(terminal, nil, "").GetShowHardwareCursor() {
		t.Fatal("the default shows the hardware cursor without PI_HARDWARE_CURSOR")
	}
	t.Setenv("PI_HARDWARE_CURSOR", "1")
	if !NewTuiAltScreen(terminal, nil, "").GetShowHardwareCursor() {
		t.Fatal("PI_HARDWARE_CURSOR=1 is not the default")
	}
	if NewTuiAltScreen(terminal, &off, "").GetShowHardwareCursor() {
		t.Fatal("an explicit false lost to the environment")
	}
	defaults := NewTuiAltScreen(terminal, nil, "")
	if !defaults.mouseEnabled || !defaults.GetCopyOnSelect() {
		t.Fatalf("no options: mouse=%v copyOnSelect=%v, want both on", defaults.mouseEnabled, defaults.GetCopyOnSelect())
	}
}
