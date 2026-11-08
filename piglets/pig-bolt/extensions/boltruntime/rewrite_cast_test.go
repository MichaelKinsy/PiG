package boltruntime

import "testing"

func TestRewriteModuleReadsATypeScriptCastOfImportMeta(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a cast with any", `const d = (import.meta as any).dirname; const u = (import.meta as ImportMeta).url;`, `const d = "/ext/p/dir"; const u = ` + modURL + `;`},
		{"after return and in an argument", `function f() { return (import.meta as any).filename } g((import.meta as any).dir);`, `function f() { return ` + modTS + ` } g("/ext/p/dir");`},
		{"a call's argument is not a parenthesized cast", `g(import.meta as any).url;`, same},
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
