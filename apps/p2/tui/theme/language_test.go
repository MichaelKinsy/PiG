package theme

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Theme.language: only what lowlight's `common` bundle registers is highlighted (theme.ts `lowlight.registered`), with its aliases.
//
// pi: apps/p2/src/tui/theme/theme.ts (Theme.language, Theme.highlight)
func TestLanguageIsLowlightCommon(t *testing.T) {
	for hint, want := range map[string]string{
		"ts": "typescript", "TSX": "typescript", "mts": "typescript", "js": "javascript", "mjs": "javascript", "sh": "bash", "zsh": "bash",
		"kt": "kotlin", "txt": "plaintext", "text": "plaintext", "src/a/Main.go": "go", "go {title=x}": "go", "rs": "rust",
		// Known to the full highlight.js 10, not bundled in lowlight `common`.
		"haskell": "", "dockerfile": "", "nginx": "", "": "", "unknownlang": "",
	} {
		if got := Language(hint); got != want {
			t.Errorf("Language(%q) = %q, want %q", hint, got, want)
		}
	}
	// A bundled language the engine does not register (highlight.js 10 loads a subset) is left unhighlighted, not mislabeled.
	for _, hint := range []string{"html", "svg", "yml", "xml", "yaml"} {
		if got, want := Language(hint), lowlightLanguages[hint]; got != "" && got != want {
			t.Errorf("Language(%q) = %q, want %q or none", hint, got, want)
		}
	}
}

func TestHighlightPlaintextIsUnstyled(t *testing.T) {
	dark, _ := BuiltinTheme("dark")
	th, err := CreateTheme(dark, tui.TerminalColorModeTrueColor, TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	got := th.Highlight("one\n\nthree \x1b[1m", "txt")
	if want := []string{"one", "", "three \x1b[1m"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("plaintext highlight = %q, want %q", got, want)
	}
	if colored := th.Highlight("x", "haskell"); colored[0] != th.Fg("mdCodeBlock", "x") {
		t.Fatalf("a language outside lowlight common = %q, want the mdCodeBlock color", colored)
	}
}
