//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// A probe that names the pinned Pi release as a literal passes on one pin and fails on the next:
// expectations spell it {{UPSTREAM_VERSION}} or {{VERSION}}, and fixture programs read it from internal/coding/pigversion.
func TestScenarioAssertionsNameThePinThroughTokens(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := DiscoverScenarios(filepath.Join(root, "test/parity", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	literals := []string{coding.UpstreamVersion, regexp.QuoteMeta(coding.UpstreamVersion), coding.Version}
	for _, sc := range scenarios {
		var expectations []string
		expectations = append(expectations, sc.Assert.BothContain...)
		expectations = append(expectations, sc.Assert.PigContains...)
		expectations = append(expectations, sc.Assert.PiContains...)
		expectations = append(expectations, sc.Assert.BothMatchRegex...)
		expectations = append(expectations, sc.Diverge.PigContains...)
		expectations = append(expectations, sc.Diverge.PiContains...)
		for _, expectation := range expectations {
			for _, literal := range literals {
				if strings.Contains(expectation, literal) {
					t.Errorf("%s: expectation %q hard-codes the pin %q; use {{UPSTREAM_VERSION}} or {{VERSION}}", sc.Name, expectation, literal)
				}
			}
		}
	}
}

var versionLiteralComparison = regexp.MustCompile(`--version[^\n]*(==|===|!=)\s*"?'?[0-9]+\.[0-9]+\.[0-9]+|expected pinned Pi [0-9]`)

func TestProbeFixturesReadThePinFromItsSource(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"*.py", "*.sh", "*.mjs"} {
		paths, err := filepath.Glob(filepath.Join(root, "test/parity/testdata", pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if match := versionLiteralComparison.Find(data); match != nil {
				t.Errorf("%s compares a version to a literal (%q); read UpstreamVersion from internal/coding/pigversion/pigversion.go", path, match)
			}
		}
	}
}
