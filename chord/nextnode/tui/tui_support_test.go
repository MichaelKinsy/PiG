package tui

import (
	"io"
	"os"
	"os/exec"
	"testing"
)

// tuiNode is a Node binary of 26.5.1 or later (`CHORD_TUI_NODE`, else `node26`, else `node` on the PATH); the test is skipped
// without one, as Pi's are.
func tuiNode(t testing.TB) string {
	t.Helper()
	candidates := []string{os.Getenv("CHORD_TUI_NODE"), "node26", "node"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil && CheckSandboxNode(path) == nil {
			return path
		}
	}
	// Pi skips these tests without such a Node; CI sets CHORD_REQUIRE_TUI_NODE (make ci-chord-tui) so that a missing Node fails them
	// instead of hiding them.
	if os.Getenv("CHORD_REQUIRE_TUI_NODE") != "" {
		t.Fatal("no Node 26.5.1 or later (CHORD_TUI_NODE, node26 or node), and CHORD_REQUIRE_TUI_NODE is set")
	}
	t.Skip("no Node 26.5.1 or later (CHORD_TUI_NODE, node26 or node)")
	return ""
}

// terminalInput and terminalOutput are a test Terminal as the Input and Output of TerminalOptions: the launcher finds the raw
// mode, the size and the resizes by their methods, as Pi finds `setRawMode`, `columns` and "resize" on its streams.
type terminalInput struct{ Terminal }

func (i terminalInput) Read(p []byte) (int, error) { return i.Input().Read(p) }
func (i terminalInput) SetRawMode(raw bool) error  { return i.SetRaw(raw) }

type terminalOutput struct{ Terminal }

func (o terminalOutput) Write(p []byte) (int, error) { return o.Output().Write(p) }

// streamsOf is the Input and Output that stand for terminal.
func streamsOf(terminal Terminal) (io.Reader, io.Writer) {
	return terminalInput{terminal}, terminalOutput{terminal}
}
