package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi tui-main-screen.ts logs redraw reasons to its configured log directory, not a home-directory default.
func TestRedrawReasonIsLogged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_TUI_DEBUG_REDRAW", "1")
	ui := NewWithOutput(&countingWriter{}, 120, 40)
	ui.SetLogDirectory(dir)
	ui.Add(NewText("hello"))
	ui.Render()
	ui.width = 100
	ui.Render()
	data, err := os.ReadFile(filepath.Join(dir, "pi-tui-debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, "terminal width changed") {
		t.Errorf("missing redraw branch: %s", log)
	}
	if !strings.Contains(log, "height=") || !strings.Contains(log, "prev=") {
		t.Errorf("missing buffer measurements: %s", log)
	}
}

func TestRedrawReasonIsSilentByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_TUI_DEBUG_REDRAW", "")
	ui := NewWithOutput(&countingWriter{}, 120, 40)
	ui.SetLogDirectory(dir)
	ui.Add(NewText("hello"))
	ui.Render()
	ui.width = 100
	ui.Render()
	if _, err := os.Stat(filepath.Join(dir, "pi-tui-debug.log")); !os.IsNotExist(err) {
		t.Error("redraw log written while switch off")
	}
}

func TestStaleRedrawDebugAliasesDoNotEnableLogging(t *testing.T) {
	for _, name := range []string{"PI_DEBUG_REDRAW", "PIG_DEBUG_REDRAW"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PI_TUI_DEBUG_REDRAW", "")
			t.Setenv(name, "1")
			ui := NewWithOutput(&countingWriter{}, 120, 40)
			ui.SetLogDirectory(dir)
			ui.Add(NewText("hello"))
			ui.Render()
			ui.width = 100
			ui.Render()
			if _, err := os.Stat(filepath.Join(dir, "pi-tui-debug.log")); !os.IsNotExist(err) {
				t.Errorf("stale %s alias enabled logging", name)
			}
		})
	}
}

func TestRedrawLoggingTracksEnvironmentAndPropagatesIOFailure(t *testing.T) {
	dir := t.TempDir()
	ui := NewWithOutput(&countingWriter{}, 40, 10)
	ui.SetLogDirectory(dir)
	ui.Add(NewText("hello"))
	t.Setenv("PI_TUI_DEBUG_REDRAW", "")
	ui.Render()
	t.Setenv("PI_TUI_DEBUG_REDRAW", "1")
	ui.width = 30
	ui.Render()
	data, err := os.ReadFile(filepath.Join(dir, "pi-tui-debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	upstreamContains(t, string(data), "terminal width changed")
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ui.SetLogDirectory(file)
	ui.width = 20
	defer func() {
		if err, ok := recover().(error); !ok {
			t.Fatalf("log write failure did not propagate as an error: %v", err)
		}
	}()
	ui.Render()
}
