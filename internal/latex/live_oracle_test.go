package latex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func liveOracleCorpus(t *testing.T) []string {
	t.Helper()
	var base []string
	for _, name := range []string{"testdata/corpus.txt", "testdata/upstream-corpus.txt"} {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			var source string
			if err := json.Unmarshal([]byte(line), &source); err != nil {
				t.Fatalf("%s: %q is not a JSON string: %v", name, line, err)
			}
			base = append(base, source)
		}
		_ = f.Close()
	}
	// Short atoms keep composed expressions well-formed often enough to reach the layout code.
	atoms := []string{"x", "y", "a_1", "x^2", "\\alpha", "\\beta", "1", "2", "n", "\\pi", "\\infty", "\\sum_{i=1}^{n} i", "\\int_0^1 f(x)\\,dx", "\\frac{1}{2}", "\\sqrt{x}", "a+b", "\\mathbb{R}", "\\text{if } x>0", "\\vec{v}", "\\hat{x}", "😀", "𝒜", "é", "e\u0301", "日本"}
	rng := rand.New(rand.NewSource(20261016))
	pick := func(list []string) string { return list[rng.Intn(len(list))] }
	any := func() string {
		if rng.Intn(3) == 0 {
			return pick(base)
		}
		return pick(atoms)
	}
	templates := []func() string{
		func() string { return "\\frac{" + any() + "}{" + any() + "}" },
		func() string { return any() + "^{" + any() + "}" },
		func() string { return any() + "_{" + any() + "}" },
		func() string { return any() + "_{" + any() + "}^{" + any() + "}" },
		func() string { return "\\sqrt{" + any() + "}" },
		func() string { return "\\sqrt[" + pick(atoms) + "]{" + any() + "}" },
		func() string { return "\\left( " + any() + " \\right)" },
		func() string { return "\\left[ " + any() + " \\middle| " + any() + " \\right]" },
		func() string {
			return "\\begin{matrix} " + any() + " & " + any() + " \\\\ " + any() + " & " + any() + " \\end{matrix}"
		},
		func() string {
			return "\\begin{pmatrix} " + any() + " & " + any() + " \\\\ " + any() + " & " + any() + " \\end{pmatrix}"
		},
		func() string { return "\\begin{bmatrix} " + any() + " \\\\ " + any() + " \\end{bmatrix}" },
		func() string {
			return "\\begin{cases} " + any() + " & " + any() + " \\\\ " + any() + " & " + any() + " \\end{cases}"
		},
		func() string { return any() + " + " + any() + " = " + any() },
		func() string { return "\\lim_{x \\to 0} " + any() },
		func() string { return "\\prod_{" + any() + "}^{" + any() + "} " + any() },
		func() string { return "\\overline{" + any() + "}" },
		func() string { return "\\binom{" + any() + "}{" + any() + "}" },
		func() string { return "\\frac{" + any() + "}{" + any() + " + \\frac{" + any() + "}{" + any() + "}}" },
		func() string {
			return "\\begin{aligned} " + any() + " &= " + any() + " \\\\ &= " + any() + " \\end{aligned}"
		},
		func() string { return "\\operatorname{" + pick([]string{"sin", "argmax", "Tr"}) + "}" + any() },
	}
	seen := map[string]bool{}
	var corpus []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			corpus = append(corpus, s)
		}
	}
	for _, s := range base {
		add(s)
	}
	for range 3000 {
		add(templates[rng.Intn(len(templates))]())
	}
	// Two levels of nesting reach stacked layouts inside layouts.
	for range 1500 {
		inner := templates[rng.Intn(len(templates))]()
		add(strings.Replace(templates[rng.Intn(len(templates))](), pick(atoms), inner, 1))
		add("\\frac{" + inner + "}{" + templates[rng.Intn(len(templates))]() + "}")
	}
	return corpus
}

// renderLatex against pinned pi-tui (latex.ts renderLatex, LatexParser.render, renderLayout and the matrix, cases and nested renderers): the recorded corpus
// plus 6000 seeded compositions (fractions, scripts, roots, delimiters, matrices, cases, aligned, nested stacks) in inline and display mode; the text and the
// supported-or-undefined decision must be equal.
func TestRenderLatexMatchesPiLive(t *testing.T) {
	corpus := liveOracleCorpus(t)
	input, err := jsstring.MarshalKeepingSurrogates(corpus)
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
	failures, supported := 0, 0
	for i, source := range corpus {
		for mode, display := range []bool{false, true} {
			got, ok := RenderLatex(source, RenderLatexOptions{Display: display})
			var gotValue *string
			if ok {
				gotValue = &got
				supported++
			}
			if !reflect.DeepEqual(gotValue, expected[i][mode]) {
				if failures++; failures <= 8 {
					t.Errorf("display=%t %q\n  Pig %s\n  Pi  %s", display, source, quoteOptional(gotValue), quoteOptional(expected[i][mode]))
				}
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d renders differ from Pi", failures, 2*len(corpus))
	}
	t.Logf("%d of %d renders supported", supported, 2*len(corpus))
	if supported < len(corpus) {
		t.Errorf("only %d of %d renders are supported expressions; the corpus no longer reaches the layout code", supported, 2*len(corpus))
	}
}
