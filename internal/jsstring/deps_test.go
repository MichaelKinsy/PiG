package jsstring_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The root-locale collation tables are 1.26 MB of data. Only the code that ports a Pi localeCompare call links them, through
// internal/jsstring/localecompare; the string helpers and the ai package, which every embedder links, must not reach them.
func TestStringHelpersAndAiLinkNoCollationTables(t *testing.T) {
	for _, pkg := range []string{"github.com/MichaelKinsy/PiG/internal/jsstring", "github.com/MichaelKinsy/PiG/ai"} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if dep == "golang.org/x/text/collate" || strings.HasSuffix(dep, "/internal/jsstring/localecompare") {
				t.Errorf("%s links %s", pkg, dep)
			}
		}
	}
}
