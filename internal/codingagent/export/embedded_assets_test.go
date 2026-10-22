package export

import (
	"os"
	"testing"
)

// The binary embeds each asset gzip-compressed (export.go); the uncompressed file is the vendored source the pinned-hash test and the
// template probes read. A .gz that does not inflate to its source ships a different page than the one reviewed against Pi.
func TestEmbeddedAssetsMatchSources(t *testing.T) {
	for _, name := range []string{
		"assets/template.html",
		"assets/template.css",
		"assets/template.js",
		"assets/vendor/marked.min.js",
		"assets/vendor/marked-cjk-friendly.min.js",
		"assets/vendor/highlight.min.js",
	} {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustReadAsset(name); got != string(want) {
				t.Fatalf("%s.gz does not inflate to %s; run go generate in internal/codingagent/export", name, name)
			}
		})
	}
}
