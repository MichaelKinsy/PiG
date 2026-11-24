package turn

import "testing"

func TestRxNonSpaceClass(t *testing.T) {
	prog := compileRx(`^\S+$`)
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"a", true},
		{"https://api.example.com/v1/messages", true},
		{"é", true},
		{"日本語", true},
		{"😀", true},
		{"", false},
		{" ", false},
		{"\t", false},
		{"\n", false},
		{"\r", false},
		{"\v", false},
		{"\f", false},
		{"\u00a0", false},
		{"\u2000", false},
		{"\u2028", false},
		{"\u2029", false},
		{"\u3000", false},
		{"\ufeff", false},
		{"a b", false},
		{"a\tb", false},
		{"a\nb", false},
	} {
		if got := prog.matchRx(tc.text); got != tc.want {
			t.Errorf("compileRx(`^\\S+$`).matchRx(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestCompileRxRejectsUnsupportedAlphanumericEscapes(t *testing.T) {
	for _, pattern := range []string{
		`\W`,
		`\D`,
		`\b`,
		`\B`,
		`\w`,
		`\1`,
		`[\S]`,
		`[\W]`,
		`[\D]`,
		`[\s]`,
		`[\1]`,
	} {
		panicked := false
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			compileRx(pattern)
		}()
		if !panicked {
			t.Errorf("compileRx(%q) did not panic; unsupported alphanumeric escape must fail compilation", pattern)
		}
	}

	// Punctuation escapes remain literals.
	for _, tc := range []struct {
		pattern, match, nomatch string
	}{
		{`\(413\)`, "(413)", "413"},
		{`\.`, ".", "a"},
		{`\[a\]`, "[a]", "a"},
		{`[\-]`, "-", "a"},
	} {
		prog := compileRx(tc.pattern)
		if !prog.matchRx(tc.match) {
			t.Errorf("compileRx(%q).matchRx(%q) = false, want true", tc.pattern, tc.match)
		}
		if prog.matchRx(tc.nomatch) {
			t.Errorf("compileRx(%q).matchRx(%q) = true, want false", tc.pattern, tc.nomatch)
		}
	}
}
