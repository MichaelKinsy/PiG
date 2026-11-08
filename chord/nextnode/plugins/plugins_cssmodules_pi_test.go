package plugins

// pi: packages/chord/src/node/plugins/css-modules.ts

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
)

// cssModuleGolden is one CSS module with what Pi's own `scopeCssModule` (c23b9ec1, esbuild 0.28.2) gave for it, scoped as
// plugin "p" at `/d/x.module.css` (`/d` its directory): the stylesheet and the mapping as the facet's module embeds it
// (`JSON.stringify(scoped.classes)`), or the messages of the build's failure joined by newlines.
type cssModuleGolden struct {
	CSS    string `json:"css"`
	Out    string `json:"out"`
	Module string `json:"module"`
	Error  string `json:"error"`
}

// cssModuleGoldens reads testdata/css-modules-pi.jsonl.gz: the 23 hand cases of the review of this slice, then the 3000
// that cssModuleCases makes, each run through Pi.
func cssModuleGoldens(t *testing.T) []cssModuleGolden {
	t.Helper()
	file, err := os.Open("testdata/css-modules-pi.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var goldens []cssModuleGolden
	lines := bufio.NewScanner(unzipped)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		var golden cssModuleGolden
		if err := json.Unmarshal(lines.Bytes(), &golden); err != nil {
			t.Fatal(err)
		}
		goldens = append(goldens, golden)
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return goldens
}

// scopedOf is the CSS module css scoped as plugin "p" at `/d/x.module.css`: its stylesheet and its mapping as the facet's
// module embeds it, or the failure's text.
func scopedOf(css string) (sheet, mapping, failure string) {
	scoped, err := ScopeCSSModule("p", "/d", "/d/x.module.css", css)
	if err != nil {
		return "", "", err.Error()
	}
	return scoped.CSS, classMapObject(scoped.Classes), ""
}

// The scoping is Pi's, case by case: the stylesheet and the mapping Pi's `vm` evaluation gives, in its key order (JavaScript
// lists integer-like keys first, ascending), without a `__proto__` key (a literal's prototype setter, which the spread drops),
// for the names esbuild writes as a template literal, and as `JSON.stringify` writes them.
func TestScopeCSSModuleGivesWhatPisScopeCssModuleGivesForEveryGoldenCase(t *testing.T) {
	goldens := cssModuleGoldens(t)
	if len(goldens) < 3000 {
		t.Fatalf("%d golden cases, want 3000 or more", len(goldens))
	}
	differing := 0
	for i, golden := range goldens {
		sheet, mapping, failure := scopedOf(golden.CSS)
		if sheet == golden.Out && mapping == golden.Module && failure == golden.Error {
			continue
		}
		differing++
		if differing <= 8 {
			t.Errorf("case %d %q:\n  stylesheet %q, want %q\n  mapping    %s, want %s\n  failure    %q, want %q", i, golden.CSS, sheet, golden.Out, mapping, golden.Module, failure, golden.Error)
		}
	}
	if differing > 0 {
		t.Fatalf("%d of %d cases differ from Pi", differing, len(goldens))
	}
}

// The golden's modules after the 23 hand cases are the generator's: a differential over cssModuleCases is one over Pi's.
func TestCSSModuleGoldensAreTheGeneratorsCases(t *testing.T) {
	goldens := cssModuleGoldens(t)
	generated := cssModuleCases(3000)
	if len(goldens) != cssModuleHandCases+len(generated) {
		t.Fatalf("%d golden cases, want %d hand cases and %d generated", len(goldens), cssModuleHandCases, len(generated))
	}
	for i, css := range generated {
		if goldens[cssModuleHandCases+i].CSS != css {
			t.Fatalf("golden case %d is %q, the generator's is %q", cssModuleHandCases+i, goldens[cssModuleHandCases+i].CSS, css)
		}
	}
}

// cssModuleHandCases is how many cases of the golden the review of this slice wrote by hand, before the generated ones.
const cssModuleHandCases = 23
