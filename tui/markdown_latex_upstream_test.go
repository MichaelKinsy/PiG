package tui

import (
	"slices"
	"testing"
)

func TestMarkdownLatexUpstreamLayoutRegressions(t *testing.T) {
	cases := []struct {
		name, source string
		want         []string
	}{
		// .upstream/v0.87.1/packages/tui/test/latex.test.ts:323,412
		{"relation and join spacing", `$x=0$ $R\bowtie S$`, []string{"x = 0 R ⋈ S"}},
		// .upstream/v0.87.1/packages/tui/test/latex.test.ts:349
		{"font switches", `${\rm roman}+{\bf bold}$`, []string{"roman+bold"}},
		// .upstream/v0.87.1/packages/tui/test/latex.test.ts:437,469
		{"multiline fraction argument", "$$\n\\frac{1}\n{2}\n$$", []string{"1", "─", "2"}},
		// .upstream/v0.87.1/packages/tui/test/latex.test.ts:378
		{"adjacent matrix baseline", `$$A\mathbf e_1=\begin{pmatrix}\pi\\0\end{pmatrix},\qquad A\mathbf e_2=\begin{pmatrix}0\\\frac{1}{\pi}\end{pmatrix}.$$`, []string{"Ae₁ = ⎛ π ⎞, Ae₂ = ⎛ 0   ⎞", "      ⎝ 0 ⎠        ⎝ 1/π ⎠."}},
		// .upstream/v0.87.1/packages/tui/test/latex.test.ts:462
		{"centered cases", `$$f(x) = \begin{cases} x^{2} & x \geq 0 \\ -x & x < 0 \end{cases}$$`, []string{"       ⎧ x² if x ≥ 0", "f(x) = ⎨", "       ⎩ -x if x < 0"}},
		// .upstream/v0.87.1/packages/tui/test/latex.test.ts:494
		{"nested display script", `$$x^{n^2}$$`, []string{"  2", " n", "x"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := markdownPlainLines(NewMarkdown(tt.source).Render(100))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
