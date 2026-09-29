package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type trustCaptureRenderer struct {
	*tui.TUI
	capture func()
}

func (r *trustCaptureRenderer) Render() { r.capture() }

func TestTrustSelectorProductionSavesParentWithoutActivatingIt(t *testing.T) {
	// Pi interactive-mode.ts:5157-5182 uses saved/current decisions and persists a parent selection without activating it in this Session.
	parent, agentDir := t.TempDir(), t.TempDir()
	cwd := filepath.Join(parent, "project")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parent) // The Session cwd, not the process cwd, owns the decision.
	store := NewProjectTrustStore(agentDir)
	if err := store.SetMany([]ProjectTrustUpdate{{Path: parent, Decision: new(true)}, {Path: cwd, Decision: new(false)}}); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsManager(cwd, agentDir)
	settings.SetProjectTrusted(false)
	input := make(chan []byte, 2)
	input <- []byte("\x1b[A")
	input <- []byte("\n")
	close(input)
	session := NewSession("trust", cwd)
	m := &InteractiveMode{opts: InteractiveOptions{AgentDir: agentDir, CWD: cwd, SettingsManager: settings, SessionHandle: &recordingCompactHandle{inner: session}}, editor: tui.NewEditor(), editorContainer: tui.NewContainer(), chatContainer: tui.NewContainer(), modalInputCh: input}
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	var frames []string
	m.tuiInst = &trustCaptureRenderer{TUI: tui.NewWithOutput(io.Discard, 220, 40), capture: func() { frames = append(frames, stripANSITest(strings.Join(m.editorContainer.Render(220), "\n"))) }}
	if err := trustHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"Saved decision: untrusted", "Current session: untrusted", "✓ Do not trust"} {
		if !strings.Contains(strings.Join(frames, "\n"), marker) {
			t.Errorf("selector frames missing %q: %q", marker, frames)
		}
	}
	entry, err := store.GetEntry(cwd)
	// Pi trust-manager.ts:41 stores canonicalizePath (realpath) keys, so macOS /var reads back as /private/var.
	if err != nil || entry == nil || entry.Path != realPathForTest(t, parent) || !entry.Decision {
		t.Fatalf("parent decision=%+v error=%v", entry, err)
	}
	if settings.IsProjectTrusted() {
		t.Fatal("saving trust activated it without a restart")
	}
}
