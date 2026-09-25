package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A full redraw discards terminal scrollback, so a reader loses their place.
// Every branch that decides on one emits identical bytes, so the reason is not
// recoverable from the output; upstream logs it behind PI_TUI_DEBUG_REDRAW.
func TestRedrawReasonIsLogged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PI_TUI_DEBUG_REDRAW", "1")
	redrawDebugOnce = sync.Once{}

	w := &countingWriter{}
	tui := NewWithOutput(w, 120, 40)
	tui.Add(NewText("hello"))
	tui.Render()
	// A width change is the least ambiguous full-redraw trigger.
	tui.width = 100
	tui.Render()

	data, err := os.ReadFile(filepath.Join(home, "agent", "pig-debug.log"))
	if err != nil {
		t.Fatalf("no redraw log was written: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "terminal width changed") {
		t.Errorf("the log does not name the branch that redrew:\n%s", log)
	}
	if !strings.Contains(log, "height=") || !strings.Contains(log, "prev=") {
		t.Errorf("the log omits the buffer measurements needed to read it:\n%s", log)
	}
}

// The switch must be off by default: this writes to disk on a render path.
func TestRedrawReasonIsSilentByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PI_TUI_DEBUG_REDRAW", "")
	redrawDebugOnce = sync.Once{}

	w := &countingWriter{}
	tui := NewWithOutput(w, 120, 40)
	tui.Add(NewText("hello"))
	tui.Render()
	tui.width = 100
	tui.Render()

	if _, err := os.Stat(filepath.Join(home, "agent", "pig-debug.log")); !os.IsNotExist(err) {
		t.Error("a redraw log was written with the switch off")
	}
}

func TestStaleRedrawDebugAliasesDoNotEnableLogging(t *testing.T) {
	for _, name := range []string{"PI_DEBUG_REDRAW", "PIG_DEBUG_REDRAW"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Setenv("PI_TUI_DEBUG_REDRAW", "")
			t.Setenv(name, "1")
			redrawDebugOnce = sync.Once{}

			w := &countingWriter{}
			tui := NewWithOutput(w, 120, 40)
			tui.Add(NewText("hello"))
			tui.Render()
			tui.width = 100
			tui.Render()

			if _, err := os.Stat(filepath.Join(home, "agent", "pig-debug.log")); !os.IsNotExist(err) {
				t.Errorf("stale %s alias enabled redraw logging", name)
			}
		})
	}
}
