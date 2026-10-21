// SPDX-License-Identifier: MIT

package checks

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// Each analyzer runs over a seeded package whose `// want` lines are the bad
// examples and whose unmarked functions are the near-miss good examples.
func TestAnalyzers(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range All() {
		name := c.Analyzer.Name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			analysistest.Run(t, testdata, c.Analyzer, append([]string{name}, scopePackages[name]...)...)
		})
	}
}

const fixtureModule = "github.com/MichaelKinsy/PiG/"

// scopePackages are fixtures that sit at a real package path, to pin which packages and files an ordering check reads. The shared ai and internal/jsloop fixtures hold the shapes of every ordering check and expect no finding: ai is out of scope and internal/jsloop is exempt.
var scopePackages = map[string][]string{
	"boundarygoroutine": {fixtureModule + "internal/codingagent", fixtureModule + "ai", fixtureModule + "internal/jsloop"},
	"unorderedfanin":    {fixtureModule + "chord/host", fixtureModule + "ai", fixtureModule + "internal/jsloop"},
}

// An ordering check needs a class on its marker: no reason, no class and a reason that names none each leave one finding at the marker and drop the one the marker covers.
func TestOrderingMarkerNeedsAClass(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var check *Check
	for _, c := range All() {
		if c.Analyzer.Name == "boundarygoroutine" {
			check = &c
		}
	}
	var reasons, classes int
	for _, r := range analysistest.Run(&recorder{TB: t}, testdata, check.Analyzer, "boundarymarker") {
		for _, d := range r.Diagnostics {
			switch {
			case strings.Contains(d.Message, "needs a reason"):
				reasons++
			case strings.Contains(d.Message, "needs a class"):
				classes++
			default:
				t.Errorf("unexpected finding: %s", d.Message)
			}
		}
	}
	if reasons != 1 || classes != 2 {
		t.Fatalf("needs-a-reason = %d, needs-a-class = %d; want 1 and 2 (noClass and wrongClass)", reasons, classes)
	}
}

// A marker without a reason does not suppress the finding; it is reported instead.
func TestAllowMarkerNeedsAReason(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var regexfold *Check
	for _, c := range All() {
		if c.Analyzer.Name == "regexfold" {
			regexfold = &c
		}
	}
	var reasons, findings int
	// The unmarked diagnostics have no `// want`, so analysistest reports them to a recorder instead of failing this test.
	for _, r := range analysistest.Run(&recorder{TB: t}, testdata, regexfold.Analyzer, "allow") {
		for _, d := range r.Diagnostics {
			if strings.Contains(d.Message, "needs a reason") {
				reasons++
			} else {
				findings++
			}
		}
	}
	if reasons != 1 || findings != 1 {
		t.Fatalf("needs-a-reason diagnostics = %d, findings = %d; want 1 and 1 (the wrong-check marker does not suppress)", reasons, findings)
	}
}

func TestEveryCheckIsDocumented(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All() {
		n := c.Analyzer.Name
		if seen[n] {
			t.Fatalf("duplicate check %s", n)
		}
		seen[n] = true
		if c.Analyzer.Doc == "" || c.Incident == "" || c.Severity == "" {
			t.Errorf("%s needs a doc, an incident and a severity", n)
		}
	}
}

// recorder swallows the errors analysistest raises for diagnostics with no `// want` line.
type recorder struct{ testing.TB }

func (recorder) Errorf(string, ...any) {}
