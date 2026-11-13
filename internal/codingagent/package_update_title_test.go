package codingagent

import (
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream run() restores the terminal title in the finally of
// checkForPackageUpdates on win32, because npm can overwrite the shared console
// title while it checks package versions. It does so whether or not updates
// were found, and only on win32.
func TestFinishPackageUpdateCheckRestoresTheTitleOnWindows(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "project")
	session := NewSession("title", cwd)
	m := &InteractiveMode{
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 100, 30),
		opts:          InteractiveModeOptions{CWD: cwd, SessionHandle: &recordingCompactHandle{inner: session}},
	}
	terminal := recordTerminalCalls(t, m)

	m.finishPackageUpdateCheck("linux", nil)
	if titles := terminal.takeTitles(); len(titles) != 0 {
		t.Fatalf("a linux check set the title %q", titles)
	}
	m.finishPackageUpdateCheck("windows", nil)
	if titles, want := terminal.takeTitles(), []string{tui.BuildTerminalTitle("", cwd)}; !slices.Equal(titles, want) {
		t.Fatalf("a windows check set titles %q, want %q", titles, want)
	}
}
