package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A Piglet that strips the changelog, at runtime or by compiling it out of its Binary, has no /changelog: the command
// leaves the built-in list, so it neither resolves nor autocompletes, and the startup What's New parses an empty bundle.
// Stock PiG shows the bundled changelog.
func TestStripChangelogRemovesTheCommand(t *testing.T) {
	if !pigstrip.Has(pigstrip.ListFeatures, pigstrip.Changelog) {
		sc, out := newFakeSlashCtx()
		if err := changelogHandler(sc); err != nil {
			t.Fatalf("stock /changelog: %v", err)
		}
		if bundledChangelog() == "" || !strings.Contains(out.String(), "**What's New**") {
			t.Fatalf("stock /changelog shows no entries: %q", out.String())
		}
		if !NewSlashRegistry().IsBuiltin("changelog") {
			t.Fatal("stock /changelog is not a built-in")
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Changelog))

	if registry := NewSlashRegistry(); registry.IsBuiltin("changelog") {
		t.Fatal("stripped changelog still registers /changelog")
	}
	if got := bundledChangelog(); got != "" {
		t.Fatalf("stripped bundle = %d bytes, want none", len(got))
	}
}
