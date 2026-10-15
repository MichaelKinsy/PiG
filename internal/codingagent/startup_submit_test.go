package codingagent

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	m.uiTaskCh = make(chan func(), 8)
	m.backgroundCtx = ctx
	t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
	m.themeState.output = io.Discard
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	if err := m.waitForTerminalColors(ctx); err != nil {
		t.Fatal(err)
	}
	m.endStartupSubmitWindow()
	if got := m.editor.Text(); got != "What is 20+22?" {
		t.Fatalf("editor = %q, want the early text kept", got)
	}
	if m.lastStatusText == nil || !strings.Contains(lastStatusContent(m), "Startup is still in progress") {
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

// newColorReplyStartupMode is the startup wait of TestStartupSubmitKeepsTextAndReportsProgress with the given terminal input queued: the editor
// takes input under Pi's startup submit handler, and the startup color query is pending.
func newColorReplyStartupMode(t *testing.T, input ...string) (*InteractiveMode, context.Context) {
	t.Helper()
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "")
	m, ctx := newCustomEditorDispatchMode(t)
	m.opts.Settings.Theme = ""
	tui.SetThemeSettingPresence(nil)
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), t.TempDir())
	m.inputReadCh = make(chan inputChunk, len(input))
	m.inputErrCh = make(chan error, 1)
	for _, data := range input {
		m.inputReadCh <- inputChunk{data: []byte(data)}
	}
	m.beginStartupSubmitWindow()
	m.uiTaskCh = make(chan func(), 8)
	m.backgroundCtx = ctx
	t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
	m.themeState.output = io.Discard
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	return m, ctx
}

// Pi completes the color query from the terminal input handler (tui.ts consumeTerminalColorResponse settles its Promise on the DA1 reply), and
// requestTerminalColors' `.then(apply)` and init()'s `await waitForTerminalColors()` continuation are microtasks, which run before Node reads the
// next input chunk. The input that carries the DA1 reply therefore applies the colors and ends the wait before any later input is handled.
func TestColorReplyCompletesTheStartupWaitBeforeTheNextInput(t *testing.T) {
	m, _ := newColorReplyStartupMode(t)
	m.handleStartupInput(inputChunk{data: []byte("\x1b[?62;22c")}, false)
	select {
	case <-m.themeState.colorQuery:
	default:
		t.Fatal("the DA1 reply was handled, but the color query completes only after later owner-loop work; Pi completes it before the next input")
	}
}

// A client that types as soon as the first frame shows: the terminal's DA1 reply arrives, then the prompt and its Enter as separate input. In
// Pi the reply ends the startup color wait before the prompt is read (see TestColorReplyCompletesTheStartupWaitBeforeTheNextInput), so the
// prompt reaches the ordinary submit handler, never "Startup is still in progress".
func TestPromptAfterTheColorReplyReachesTheSubmitHandler(t *testing.T) {
	m, ctx := newColorReplyStartupMode(t, "\x1b[?62;22c", "hi", "\r")
	if err := m.waitForTerminalColors(ctx); err != nil {
		t.Fatal(err)
	}
	if m.lastStatusText != nil && strings.Contains(lastStatusContent(m), "Startup is still in progress") {
		t.Fatal("the startup color wait handled the prompt typed after the DA1 reply and rejected it")
	}
	if got := len(m.inputReadCh); got != 2 {
		t.Fatalf("the startup color wait handled %d chunks typed after the DA1 reply, want none", 2-got)
	}
	m.endStartupSubmitWindow()
	for len(m.inputReadCh) > 0 {
		m.editor.HandleInput(string((<-m.inputReadCh).data))
	}
	if !slices.Equal(m.pendingUserInputs, []string{"hi"}) {
		t.Fatalf("submitted prompts = %q, want the prompt typed after the DA1 reply", m.pendingUserInputs)
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
		if m.lastStatusText == nil || !strings.Contains(lastStatusContent(m), "Startup is still in progress") {
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

// Pi's ensureTool runs to completion synchronously when it needs no download: a present tool resolves through getToolPath, and offline mode
// reports its warning and returns (tools-manager.ts ensureTool). Promise.all over two such calls settles in microtasks, so init() installs
// the ordinary submit handler (setupEditorSubmitHandler) before it reads more input. Only a download in flight lets input reach the startup
// submit handler (TestStartupSubmitWindowCoversManagedToolSetup).
func TestManagedToolSetupWithoutADownloadReadsNoInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, tm *tools.ToolsManager)
	}{
		{"tools present", func(t *testing.T, tm *tools.ToolsManager) {
			if err := os.MkdirAll(tm.BinDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"fd", "rg"} {
				binary := name
				if runtime.GOOS == "windows" {
					binary += ".exe" // ToolsManager.GetToolPath looks for <binary>.exe in its bin directory on win32
				}
				if err := os.WriteFile(filepath.Join(tm.BinDir(), binary), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"offline", func(t *testing.T, _ *tools.ToolsManager) { t.Setenv("PIG_OFFLINE", "1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "")
			tm := tools.NewToolsManager(t.TempDir())
			tc.setup(t, tm)
			m := &InteractiveMode{chatContainer: tui.NewContainer(), editor: tui.NewEditor(), inputReadCh: make(chan inputChunk, 2)}
			m.beginStartupSubmitWindow()
			m.inputReadCh <- inputChunk{data: []byte("hi")}
			m.inputReadCh <- inputChunk{data: []byte("\r")}
			m.ensureManagedTools(t.Context(), tm)
			if m.lastStatusText != nil && strings.Contains(lastStatusContent(m), "Startup is still in progress") {
				t.Fatal("managed-tool setup without a download handled the prompt typed during it and rejected it")
			}
			if got := len(m.inputReadCh); got != 2 {
				t.Fatalf("managed-tool setup without a download handled %d input chunks, want none", 2-got)
			}
		})
	}
}

// /reload re-detects the theme without Pi's startup submit handler.
func TestReloadThemeDetectionHasNoStartupSubmitHandler(t *testing.T) {
	restoreStartupTheme(t)
	m, ctx := newCustomEditorDispatchMode(t)
	m.inputReadCh = make(chan inputChunk, 1)
	m.inputErrCh = make(chan error, 1)
	m.inputReadCh <- inputChunk{data: []byte("\x1b]11;#000000\x07")}
	m.uiTaskCh = make(chan func(), 8)
	m.backgroundCtx = ctx
	t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
	m.themeState.output = io.Discard
	m.initTheme()
	m.applyThemeFromSettings(context.Background())
	if m.editor.OnSubmit != nil {
		t.Fatal("theme re-detection installed a submit handler")
	}
}
