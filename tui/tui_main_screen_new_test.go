package tui

import (
	"bytes"
	"strings"
	"testing"
)

// tui-main-screen.ts new TuiMainScreen(terminal, showHardwareCursor, logDirectory): the screen draws on the terminal it is given and takes its
// size from it, shows the hardware cursor as asked (the PI_HARDWARE_CURSOR default when it is not) and keeps the log directory.
func TestNewTuiMainScreenDrawsOnItsTerminalAndTakesItsSize(t *testing.T) {
	t.Setenv("PI_TUI_WRITE_LOG", "")
	t.Setenv("PI_HARDWARE_CURSOR", "")
	t.Setenv("COLUMNS", "37")
	t.Setenv("LINES", "11")
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	on := true
	screen := NewTuiMainScreen(terminal, &on, "/var/log/pi")
	if screen.Width() != 37 || screen.Height() != 11 {
		t.Fatalf("size = %dx%d, want the terminal's 37x11", screen.Width(), screen.Height())
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
	screen.Add(&fixedLinesComponent{lines: []string{"hello from the screen"}})
	screen.RenderNow()
	if !strings.Contains(out.String(), "hello from the screen") {
		t.Fatalf("the terminal received %q, not the frame", out.String())
	}
	if NewTuiMainScreen(terminal, nil, "").GetShowHardwareCursor() {
		t.Fatal("the default shows the hardware cursor without PI_HARDWARE_CURSOR")
	}
	t.Setenv("PI_HARDWARE_CURSOR", "1")
	if !NewTuiMainScreen(terminal, nil, "").GetShowHardwareCursor() {
		t.Fatal("PI_HARDWARE_CURSOR=1 is not the default")
	}
	off := false
	if NewTuiMainScreen(terminal, &off, "").GetShowHardwareCursor() {
		t.Fatal("an explicit false lost to the environment")
	}
}
