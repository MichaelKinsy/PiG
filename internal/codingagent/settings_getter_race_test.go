package codingagent

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// packages/coding-agent/src/core/settings-manager.ts:782-790 treats every slash-containing string as non-fixed, while the controller consumes the raw setting.
func TestSettingsFixedThemeAndRawControllerSelection(t *testing.T) {
	for _, value := range []string{"dark", "light/dark", "/", "themes/dark", ""} {
		t.Run(value, func(t *testing.T) {
			sm := NewSettingsManager(t.TempDir(), t.TempDir())
			if err := sm.SetTheme(value); err != nil {
				t.Fatal(err)
			}
			want := value
			if strings.Contains(value, "/") {
				want = ""
			}
			if got := sm.GetTheme(); got != want {
				t.Fatalf("fixed theme=%q, want %q", got, want)
			}
			mode := &InteractiveMode{opts: InteractiveModeOptions{SettingsManager: sm, Settings: Settings{Theme: "stale"}}}
			got := mode.getThemeSelection()
			if got == nil {
				t.Fatalf("raw controller selection unset, want %q", value)
			}
			if *got != value {
				t.Fatalf("raw controller selection=%q, want %q", *got, value)
			}
			if got := sm.Get().Theme; got != value {
				t.Fatalf("stored theme=%q, want %q", got, value)
			}
		})
	}
}

// packages/coding-agent/src/core/settings-manager.ts:986-996 trims only the configured command for emptiness, preserving both selected text and truthy env whitespace.
func TestSettingsExternalEditorCommandPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, configured, visual, editor, want string }{
		{"configured bytes", " code --wait ", "vim", "nano", " code --wait "},
		{"configured BOM is blank", "\ufeff", "vim", "nano", "vim"},
		{"configured NEL is not blank", "\u0085", "vim", "nano", "\u0085"},
		{"visual whitespace is truthy", "\t ", "  ", "nano", "  "},
		{"editor fallback", "", "", "emacs", "emacs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VISUAL", tc.visual)
			t.Setenv("EDITOR", tc.editor)
			sm := &SettingsManager{merged: Settings{ExternalEditor: tc.configured}}
			if got := sm.GetExternalEditorCommand(); got != tc.want {
				t.Fatalf("editor=%q, want %q", got, tc.want)
			}
			if sm.Get().ExternalEditor != tc.configured {
				t.Fatal("getter mutated the configured command")
			}
		})
	}
	for _, tc := range []struct{ platform, want string }{{"windows", "notepad"}, {"linux", "nano"}, {"darwin", "nano"}} {
		t.Run(tc.platform, func(t *testing.T) {
			if got := resolveExternalEditorCommand("", "", "", tc.platform); got != tc.want {
				t.Fatalf("platform default=%q, want %q", got, tc.want)
			}
		})
	}
}

func BenchmarkSettingsThemeAndEditorGetters(b *testing.B) {
	sm := &SettingsManager{merged: Settings{Theme: "light/dark", ExternalEditor: "code --wait", Extensions: make([]string, 100)}}
	b.ReportAllocs()
	for b.Loop() {
		if sm.GetTheme() != "" || sm.GetExternalEditorCommand() != "code --wait" {
			b.Fatal("incorrect getter result")
		}
	}
}

// The launch path must use the same ECMAScript emptiness test as SettingsManager, not reclassify NEL as blank.
func TestExternalEditorSelectionKeepsJSWhitespaceMeaning(t *testing.T) {
	for _, tc := range []struct{ name, configured, executable string }{
		{"BOM fallback", "\ufeff", "missing-visual-fixture"},
		{"NEL command", "\u0085", "\u0085"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && tc.configured == "\u0085" {
				t.Skip("cmd.exe accepts a NEL-only command line as an empty command, so no launch failure occurs")
			}
			t.Setenv("PATH", t.TempDir())
			t.Setenv("VISUAL", "missing-visual-fixture")
			t.Setenv("EDITOR", "missing-editor-fixture")
			got, err := OpenExternalEditor(context.Background(), "unchanged", tc.configured)
			if err == nil || !strings.HasPrefix(err.Error(), "external editor: run "+tc.executable+":") || got != "unchanged" {
				t.Fatalf("result=%q error=%v; want launch failure for %q with initial text intact", got, err, tc.executable)
			}
		})
	}
}
