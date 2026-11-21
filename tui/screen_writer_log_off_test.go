package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PIG_TUI_SCREEN_WRITER_LOG names a file the screen writer records into, so a terminal with the writer off (the environment or the setting selects Pi's bytes, or no
// interactive run applied the setting) records nothing and creates no file, though the variable is set; every write is sent as it came.
func TestScreenWriterLogWritesNoFileWithTheWriterOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    string
		choice int32
	}{
		{"the environment selects Pi's bytes", "pi", screenWriterOn},
		{"the environment turns it off with 0", "0", screenWriterOn},
		{"the setting selects Pi's bytes", "", screenWriterOff},
		{"no run applied the setting", "", screenWriterUnset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "writes.bin")
			t.Setenv(screenWriterEnv, tc.env)
			t.Setenv(screenWriterLogEnv, path)
			setScreenWriterChoice(t, tc.choice)
			var out bytes.Buffer
			terminal := NewProcessTerminalWithOutput(nil, nil, &out)
			inputs := append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "one")), swFrame(swLine(1, "one!")), "\x1b[?25h")
			for _, data := range inputs {
				terminal.Write(data)
			}
			if terminal.ScreenWriterActive() {
				t.Fatal("the terminal has a screen writer")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the log file exists after the writes (stat error %v)", err)
			}
			if got := out.String(); got != strings.Join(inputs, "") {
				t.Errorf("the terminal was sent %q, want every write as it came", got)
			}
		})
	}
}
