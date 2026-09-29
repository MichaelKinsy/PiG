package codingagent

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi binds handleStartupSubmit from mounting the UI (interactive-mode.ts:944-947) until setupEditorSubmitHandler (1027-1028): an Enter in that window keeps the typed text in the editor and reports that startup is still in progress (3072-3075). This is the window's first edge, startup theme detection.
func TestStartupSubmitKeepsTextAndReportsProgress(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "")
	m, ctx := newCustomEditorDispatchMode(t)
	m.opts.Settings.Theme = ""
	tui.SetThemeSettingPresence(nil)
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), t.TempDir())
	m.inputReadCh = make(chan inputChunk, 3)
	m.inputErrCh = make(chan error, 1)
	// The input pump delivers the typed text and the Enter key as separate sequences.
	m.inputReadCh <- inputChunk{data: []byte("What is 20+22?")}
	m.inputReadCh <- inputChunk{data: []byte("\r")}
	m.inputReadCh <- inputChunk{data: []byte("\x1b]11;#000000\x07")}
	m.beginStartupSubmitWindow()
	if err := m.initializeTerminalTheme(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	m.endStartupSubmitWindow()
	if got := m.editor.Text(); got != "What is 20+22?" {
		t.Fatalf("editor = %q, want the early text kept", got)
	}
	if m.lastStatusText == nil || !strings.Contains(m.lastStatusText.Content, "Startup is still in progress") {
		t.Fatalf("status = %v, want Startup is still in progress", m.lastStatusText)
	}
	if m.editor.OnSubmit == nil {
		t.Fatal("ordinary submission was not installed after startup")
	}
	m.editor.OnSubmit("after setup")
	if !slices.Equal(m.pendingUserInputs, []string{"after setup"}) {
		t.Fatal("the startup submit handler outlived startup")
	}
}

// The window's last edge: Pi's managed-tool setup (ensureTool for fd and rg, interactive-mode.ts:1017-1022) still runs under handleStartupSubmit, so text typed while a download is in flight reaches the editor and its Enter keeps it. Ending the window restores ordinary submission.
func TestStartupSubmitWindowCoversManagedToolSetup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		http.DefaultTransport = managedToolTransport(func(*http.Request) (*http.Response, error) {
			<-release
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		})
		m := &InteractiveMode{chatContainer: tui.NewContainer(), editor: tui.NewEditor(), inputReadCh: make(chan inputChunk, 2)}
		m.beginStartupSubmitWindow()
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.ensureManagedTools(t.Context(), tools.NewToolsManager(dir))
		}()
		synctest.Wait()
		m.inputReadCh <- inputChunk{data: []byte("What is 20+22?")}
		m.inputReadCh <- inputChunk{data: []byte("\r")}
		synctest.Wait()
		if got := m.editor.Text(); got != "What is 20+22?" {
			t.Errorf("editor during managed-tool setup = %q, want the typed text kept", got)
		}
		if m.lastStatusText == nil || !strings.Contains(m.lastStatusText.Content, "Startup is still in progress") {
			t.Errorf("status = %v, want Startup is still in progress", m.lastStatusText)
		}
		close(release)
		<-done
		m.endStartupSubmitWindow()
		if m.editor.OnSubmit == nil {
			t.Fatal("ordinary submission was not installed after managed-tool setup")
		}
		m.editor.OnSubmit("after setup")
		if !slices.Equal(m.pendingUserInputs, []string{"after setup"}) {
			t.Fatal("the startup submit handler outlived managed-tool setup")
		}
	})
}

// /reload re-detects the theme without Pi's startup submit handler.
func TestReloadThemeDetectionHasNoStartupSubmitHandler(t *testing.T) {
	restoreStartupTheme(t)
	m, _ := newCustomEditorDispatchMode(t)
	m.inputReadCh = make(chan inputChunk, 1)
	m.inputErrCh = make(chan error, 1)
	m.inputReadCh <- inputChunk{data: []byte("\x1b]11;#000000\x07")}
	if err := m.initializeTerminalTheme(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if m.editor.OnSubmit != nil {
		t.Fatal("theme re-detection installed a submit handler")
	}
}
