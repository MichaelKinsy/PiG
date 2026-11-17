package export

import (
	"os"
	"testing"
)

// The binary embeds each asset gzip-compressed (export.go); the uncompressed file (under assets/src, a data-only directory outside the root module zip) is the vendored source the pinned-hash test and the
// template probes read. A .gz that does not inflate to its source ships a different page than the one reviewed against Pi.
func TestEmbeddedAssetsMatchSources(t *testing.T) {
	// The sources sit flat in assets/src: a directory named vendor inside a module root makes Go read vendor/modules.txt.
	for name, source := range map[string]string{
		"assets/template.html":                     "assets/src/template.html",
		"assets/template.css":                      "assets/src/template.css",
		"assets/template.js":                       "assets/src/template.js",
		"assets/vendor/marked.min.js":              "assets/src/marked.min.js",
		"assets/vendor/marked-cjk-friendly.min.js": "assets/src/marked-cjk-friendly.min.js",
		"assets/vendor/highlight.min.js":           "assets/src/highlight.min.js",
	} {
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustReadAsset(name); got != string(want) {
				t.Fatalf("%s.gz does not inflate to %s; run go generate in internal/codingagent/export", name, source)
			}
		})
	}
}
