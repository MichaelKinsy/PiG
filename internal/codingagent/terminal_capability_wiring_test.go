package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// isolateTerminalCapabilities pins a Kitty environment, which auto-detects
// images, truecolor, and hyperlinks, and restores the capability state.
func isolateTerminalCapabilities(t *testing.T) {
	t.Helper()
	previousCapabilities := tui.GetCapabilities()
	for _, name := range []string{"TMUX", "TERM", "HERDR_ENV", "PI_HYPERLINKS", "PI_IMAGE_PROTOCOL", "PI_TRUE_COLOR"} {
		t.Setenv(name, "")
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
	tui.ResetCapabilitiesCache()
	t.Cleanup(func() {
		tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
		tui.SetCapabilities(previousCapabilities)
		tui.RefreshActiveThemeColorMode()
	})
}

func settingsFromJSON(t *testing.T, body string) Settings {
	t.Helper()
	var s Settings
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// Upstream InteractiveMode's constructor calls
// setCapabilityOverrides(settingsManager.getTerminalCapabilityOverrides()).
func TestNewInteractiveModeAppliesTerminalCapabilitySettings(t *testing.T) {
	isolateTerminalCapabilities(t)
	NewInteractiveMode(InteractiveOptions{
		CWD:      t.TempDir(),
		AgentDir: t.TempDir(),
		Settings: settingsFromJSON(t, `{"terminal": {"hyperlinks": false, "images": false, "trueColor": false}}`),
	})
	if got := tui.GetCapabilities(); got.Hyperlinks || got.Images != "" || got.TrueColor {
		t.Fatalf("capabilities after construction = %+v, want the settings overrides", got)
	}
}

// Upstream /reload runs applyRuntimeSettings, which re-applies the terminal
// capability overrides from the reloaded settings.
func TestReloadAppliesTerminalCapabilitySettings(t *testing.T) {
	isolateTerminalCapabilities(t)
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sm := NewSettingsManager(cwd, agentDir)
	m := reloadTestMode(InteractiveOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: sm, Settings: sm.Get(), NoPromptTemplates: true, NoThemes: true})
	if got := tui.GetCapabilities(); !got.Hyperlinks {
		t.Fatalf("kitty without settings = %+v, want hyperlinks", got)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"terminal": {"hyperlinks": false, "images": "iterm2"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.buildSlashContext(t.Context()).Reload()
	if got := tui.GetCapabilities(); got.Hyperlinks || got.Images != tui.ImageProtocolITerm2 {
		t.Fatalf("capabilities after /reload = %+v, want hyperlinks off and iterm2 images", got)
	}
}

// Upstream createStartupTui applies the overrides before the first startup
// prompt renders.
func TestStartupPromptAppliesTerminalCapabilitySettings(t *testing.T) {
	isolateTerminalCapabilities(t)
	restoreStartupTheme(t)
	terminal := &fakeStartupTerminal{}
	selector := tui.NewExtensionSelector("Pick", []string{"a"})
	opts := StartupUIOptions{Settings: settingsFromJSON(t, `{"theme": "dark", "terminal": {"trueColor": false}}`)}
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, opts, false, tui.NewWithOutput(io.Discard, 80, 24), terminal, map[string]string{})
		done <- err
	}()
	terminal.send(t, "\r")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := tui.GetCapabilities(); got.TrueColor {
		t.Fatalf("capabilities after the startup prompt = %+v, want truecolor off", got)
	}
}

func recordInputNormalization(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	restore := normalizeInputSequence
	normalizeInputSequence = func(sequence string) string {
		mu.Lock()
		seen = append(seen, sequence)
		mu.Unlock()
		if sequence == "\r" {
			return "\x1b[13;2u"
		}
		return sequence
	}
	t.Cleanup(func() { normalizeInputSequence = restore })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// Upstream ProcessTerminal.forwardInputSequence normalizes each StdinBuffer
// sequence before the input handler sees it; Pig's interactive pump owns the
// StdinBuffer, so it must apply the same normalization.
func TestInteractiveInputPumpNormalizesNativeShiftEnter(t *testing.T) {
	seen := recordInputNormalization(t)
	m := &InteractiveMode{}
	readCh := make(chan inputChunk)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.pumpTerminalInput(ctx, &chunkReader{chunks: [][]byte{[]byte("a\r")}}, readCh, errCh)
	var got []string
	for chunk := range readCh {
		got = append(got, string(chunk.data))
		chunk.ticket.settle()
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "\x1b[13;2u" {
		t.Fatalf("routed input = %q, want a then the Shift+Enter sequence", got)
	}
	if calls := seen(); len(calls) != 2 || calls[1] != "\r" {
		t.Fatalf("normalized sequences = %q", calls)
	}
}

func TestStartupPromptNormalizesNativeShiftEnter(t *testing.T) {
	restoreStartupTheme(t)
	seen := recordInputNormalization(t)
	terminal := &fakeStartupTerminal{}
	input := tui.NewExtensionInputComponent("Name", "")
	opts := StartupUIOptions{Settings: settingsFromJSON(t, `{"theme": "dark"}`)}
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(input, opts, false, tui.NewWithOutput(io.Discard, 80, 24), terminal, map[string]string{})
		done <- err
	}()
	terminal.send(t, "x")
	terminal.send(t, "\x1b")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	calls := seen()
	if len(calls) == 0 || calls[0] != "x" {
		t.Fatalf("normalized sequences = %q, want the startup input", calls)
	}
}
