package latex

import (
	"bytes"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// A lone UTF-16 surrogate in a math expression renders as pinned pi-tui renders it: a code point of its own, never U+FFFD (inline and display, in a
// script, fraction, root, accent, \not, \mathbb, \text, a matrix, cases and aligned, and next to the other half of a pair that the text joins).
func TestRenderLatexLoneSurrogatesMatchPi(t *testing.T) {
	high, low := jsstring.FromUTF16([]uint16{0xd83d}), jsstring.FromUTF16([]uint16{0xde00})
	var sources []string
	for _, h := range []string{high, low} {
		sources = append(sources,
			h, h+h, "x"+h, h+"x", "a"+h+"b = c", "x "+h+" y", "a"+h+" < "+h+"b",
			"x^"+h, "x_"+h, "x^{"+h+"}", "x_{"+h+"}", "x^{a+"+h+"}", "x_{"+h+"=1}^{n"+h+"}", "x^{"+h+h+"}", "x_{ab}^{"+h+"}",
			"\\frac{"+h+"}{y}", "\\frac{x}{"+h+"}", "\\frac{"+h+h+"}{"+h+h+"}", "\\frac{a"+h+"}{b+"+h+"}", "\\frac"+h+"x", "\\frac{1}{2}"+h,
			"\\sqrt{"+h+"}", "\\sqrt{x"+h+"}", "\\sqrt["+h+"]{x}", "\\binom{"+h+"}{x}",
			"\\vec{"+h+"}", "\\hat{"+h+"x}", "\\bar"+h, "\\not{"+h+"}", "\\not{"+h+"x}", "\\mathbb{"+h+"R"+h+"}", "\\mathbb{R}"+h,
			"\\text{a"+h+"b}", "\\text{"+h+"}", "\\text{if } "+h, "\\operatorname{a"+h+"}x", "\\operatorname{sin}"+h,
			"\\"+h, "\\"+h+"x", "\\alpha"+h, "\\alpha "+h+" \\beta", "\\,"+h, "\\\\"+h, "{"+h+"}", "{"+h+"}{"+h+"}",
			"\\left( "+h+" \\right)", "\\left["+h+"\\middle|x\\right]", "\\sum_{"+h+"}^{n} "+h, "\\int_"+h+"^1 x", "\\lim_{x \\to "+h+"} f",
			"\\begin{matrix} "+h+" & b \\\\ c & "+h+"d \\end{matrix}", "\\begin{pmatrix} "+h+" \\\\ x"+h+" \\end{pmatrix}",
			"\\begin{cases} "+h+" & x>0 \\\\ y"+h+" & \\text{otherwise} \\end{cases}", "\\begin{aligned} "+h+" &= a \\\\ &= b"+h+" \\end{aligned}",
			"\\begin{"+h+"} x \\end{"+h+"}", "\\begin{matrix}"+h+"\\end{matrix}",
			"\\frac{\\begin{matrix} "+h+" \\end{matrix}}{y}", "x^{\\frac{"+h+"}{2}}", "\\frac{x}{y}"+h+"\\frac{a}{b}",
			// Valid text around the unit, and a pair.
			"é"+h+"日本", "😀"+h, h+"😀", "\\frac{😀"+h+"}{é}",
		)
	}
	sources = append(sources,
		// A pair is one character; the halves a command or an empty group separates are one character in JavaScript too.
		"😀", "\\frac{😀}{x}", "\\sqrt{😀}", "x^{😀}", high+"{}"+low, high+"\\,"+low, "x^{"+high+"}{}"+low, low+"{}"+high, low+high,
		"\\frac{"+high+"}{y}\\frac{"+low+"}{z}", "\\text{"+high+"}\\text{"+low+"}",
	)
	input, err := jsstring.MarshalKeepingSurrogates(sources)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/latex_live.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][2]*string
	if err := jsstring.UnmarshalKeepingSurrogates(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(sources) {
		t.Fatalf("Pi returned %d renders for %d sources", len(expected), len(sources))
	}
	supported := 0
	for i, source := range sources {
		for mode, display := range []bool{false, true} {
			got, ok := RenderLatex(source, RenderLatexOptions{Display: display})
			var gotValue *string
			if ok {
				gotValue = &got
				supported++
			}
			if !reflect.DeepEqual(gotValue, expected[i][mode]) {
				t.Errorf("display=%t %q\n  Pig %s\n  Pi  %s", display, source, quoteOptional(gotValue), quoteOptional(expected[i][mode]))
			}
		}
	}
	if supported < len(sources) {
		t.Errorf("only %d of %d renders are supported expressions; the sources no longer reach the layout code", supported, 2*len(sources))
	}
}

func quoteOptional(s *string) string {
	if s == nil {
		return "unsupported"
	}
	return string(jsstring.AppendString(nil, *s))
}
