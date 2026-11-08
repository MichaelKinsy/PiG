package boltruntime

import "testing"

// Inputs that are not valid code, because the guess the scanner makes about them is the point: a wrong guess must not hide the rest of the file, and a name that only
// looks like a keyword must not make a class of an object.
func TestRewriteModuleBoundsItsGuesses(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a string that is never closed ends at CR", "x = \"abc\rconst u = import.meta.url;", "x = \"abc\rconst u = " + modURL + ";"},
		{"a string that is never closed ends at LF", "x = 'abc\nconst u = import.meta.url;", "x = 'abc\nconst u = " + modURL + ";"},
		{"a regex that is never closed ends at CR", "z = /a\rimport.meta.url /;", "z = /a\r" + modURL + " /;"},
		{"a regex that is never closed ends at U+2028", "z = /a\u2028import.meta.url /;", "z = /a\u2028" + modURL + " /;"},
		{"a no-break space ends a word", "function f() { return\u00a0__dirname; }", "function f() { return\u00a0" + modDir + "; }"},
		{"U+2028 ends a word and a regex's flags", "x = /a/g\u2028import.meta.url;", "x = /a/g\u2028" + modURL + ";"},
		{"a BOM-like space after a word", "const a = b\ufeffc; use(__dirname);", "const a = b\ufeffc; use(" + modDir + ");"},
		{"a key named class does not make a class of an object", "const o = { class: 1, a: { __dirname } };", "const o = { class: 1, a: { __dirname: " + modDir + " } };"},
		{"a key named enum does not make an enum of an object", "const o = { enum: 1, a: { __filename } };", "const o = { enum: 1, a: { __filename: " + modTS + " } };"},
		{"a function body in a class is no class body", "class C { m() { use(__dirname); } }", "class C { m() { use(" + modDir + "); } }"},
		{"a static block is no class body", "class C { static { use(__dirname); } }", "class C { static { use(" + modDir + "); } }"},
	}
	for _, c := range cases {
		got := rewriteModule(c.in, "/ext/p/dir/mod.ts")
		if got.Source != c.want {
			t.Errorf("%s: rewrite of\n  %q\ngot\n  %q\nwant\n  %q", c.name, c.in, got.Source, c.want)
		}
	}
}
