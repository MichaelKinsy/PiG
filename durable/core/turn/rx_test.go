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
