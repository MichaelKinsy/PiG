package codingagent

import (
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
	var titles []string
	previous := setTerminalTitle
	setTerminalTitle = func(title string) { titles = append(titles, title) }
	t.Cleanup(func() { setTerminalTitle = previous })

	cwd := filepath.Join(t.TempDir(), "project")
	session := NewSession("title", cwd)
	m := &InteractiveMode{
		chatContainer: tui.NewContainer(),
		opts:          InteractiveOptions{CWD: cwd, SessionHandle: &recordingCompactHandle{inner: session}},
	}

	m.finishPackageUpdateCheck("linux", nil)
	if len(titles) != 0 {
		t.Fatalf("a linux check set the title %q", titles)
	}
	m.finishPackageUpdateCheck("windows", nil)
	if want := []string{tui.BuildTerminalTitle("", cwd)}; !slices.Equal(titles, want) {
		t.Fatalf("a windows check set titles %q, want %q", titles, want)
	}
}
