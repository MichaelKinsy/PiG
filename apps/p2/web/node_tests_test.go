package web_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The browser client's pure modules are Pi's TypeScript, vendored verbatim (upstream/); their tests are Pi's, run unchanged under Node.

// Pi main a44127d9: home directories shown as `~` in the sidebar's paths (src/web/path-label.ts).
func TestPathLabelRunsPisTests(t *testing.T) {
	oracle.RunNodeTests(t, "test/p2/path-label.test.ts")
}

// Pi main ea153dd28: project glyphs (plugins/glyphs/glyph.ts): the prototype's glyphs for the same seeds, seed forms, colors,
// mirror symmetry, and how far apart similar seeds are. The Go port (plugins/glyphs) has the same test.
func TestGlyphsRunPisTests(t *testing.T) {
	oracle.RunNodeTests(t, "test/p2/glyphs.test.ts")
}
