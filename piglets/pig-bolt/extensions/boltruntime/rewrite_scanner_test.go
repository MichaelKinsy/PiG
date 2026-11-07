package boltruntime

import "testing"

// Guesses the scanner makes that the other tests do not reach: each case fails if the decision it names is made the other way.
func TestRewriteModuleScannerGuesses(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a slash after an object literal divides", `const r = {a: 1} / __dirname / 2;`, `const r = {a: 1} / ` + modDir + ` / 2;`},
		{"an export list names a binding, not a use", `export { __dirname };`, same},
		{"an export list with several names", `export { a, __filename } from "./x.js";`, same},
		{"the result of a call is no cast target", `x[0](import.meta as any).dirname;`, same},
		{"an overload signature after a modifier is a method", `class A { static require(id: string); static import(url: string); }`, same},
	}
	for _, c := range cases {
		got := rewriteModule(c.in, "/ext/p/dir/mod.ts")
		want := c.want
		if want == same {
			want = c.in
		}
		if got.Source != want {
			t.Errorf("%s: rewrite of\n  %s\ngot\n  %s\nwant\n  %s", c.name, c.in, got.Source, want)
		}
	}
}
