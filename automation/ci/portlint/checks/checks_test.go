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
			analysistest.Run(t, testdata, c.Analyzer, name)
		})
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
