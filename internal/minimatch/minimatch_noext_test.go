package minimatch

import "testing"

// Every row is minimatch(name, pattern, {noext: true, dot: true}) as minimatch 11.0.3 answered it: with noext an extglob is literal text.
// Pi's find tool sets noext for its Windows path matcher (core/tools/find.ts), so `+(a|b)` stays literal there as it does in fd.
func TestMatchNoExtKeepsExtglobsLiteral(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		want          bool
	}{
		{"a", "+(a|b)", false},
		{"+(a|b)", "+(a|b)", true},
		{"a", "@(a|b)", false},
		{"@(a|b)", "@(a|b)", true},
		{"a|b", "a|b", true},
		{"a", "a|b", false},
		{"x/a.ts", "x/{a,b}.ts", true},
		{"+()", "+()", true},
		{"", "+()", false},
		{"a)", "a)", true},
		{"a", "!(b)", true},
		{"!(b)", "!(b)", true},
		{"a.ts", "*.@(ts|js)", false},
		{"a.@(ts|js)", "*.@(ts|js)", true},
		{"ab", "a@(b)", false},
		{"a@(b)", "a@(b)", true},
		{"src/a.ts", "src/@(a|b).ts", false},
		{"src/@(a|b).ts", "src/@(a|b).ts", true},
		{"a", "@()", false},
		{"@()", "@()", true},
		{"a/b", "a/**", true},
		{"a", "a/**", false},
	} {
		if got := Match(tc.name, tc.pattern, Options{NoExt: true, Dot: true}); got != tc.want {
			t.Errorf("Match(%q, %q, noext) = %v, want %v", tc.name, tc.pattern, got, tc.want)
		}
	}
}
