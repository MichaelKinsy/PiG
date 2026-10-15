package configschema

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// The $defs name pattern is created on first use, so coding/cli's TestLazyRegexpPatternsAllCompile, which compiles the patterns created
// at init, does not see it.
func TestDefinitionNamePatternCompiles(t *testing.T) {
	pattern := definitionNamePattern()
	if invalid := lazyregexp.CompileAll(); invalid != "" {
		t.Fatal(invalid)
	}
	for name, want := range map[string]bool{"Keybindings": true, "a-b_9": true, "": false, "9a": false, "a b": false, "a\n": false} {
		if got := pattern.MatchString(name); got != want {
			t.Errorf("definition name %q: %v, want %v", name, got, want)
		}
	}
	if definitionNamePattern() != pattern {
		t.Error("a second use made another pattern")
	}
}
