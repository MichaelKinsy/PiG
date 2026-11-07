package boltruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Cases of the second review: where a lexer that guesses at `/` and at what a name is would change text or break the code. file is the module's path; want is the rewrite
// of in ("same" for no change).
var scannerCases = []struct{ name, file, in, want string }{
	// `++`, `--` and member names.
	{"a postfix increment is a value, so a slash divides", "/ext/p/dir/mod.ts", `const q = i++ / 2, u = import.meta.url / 3;`, `const q = i++ / 2, u = ` + modURL + ` / 3;`},
	{"a postfix decrement is a value", "/ext/p/dir/mod.ts", `const q = i-- / 2, u = import.meta.url / 3;`, `const q = i-- / 2, u = ` + modURL + ` / 3;`},
	{"a member named return is a value", "/ext/p/dir/mod.ts", `o.return / 2; const u = import.meta.url / 3;`, `o.return / 2; const u = ` + modURL + ` / 3;`},
	{"a member named typeof is a value", "/ext/p/dir/mod.ts", `x.typeof / 2; const u = import.meta.url / 3;`, `x.typeof / 2; const u = ` + modURL + ` / 3;`},
	{"an optional member named in is a value", "/ext/p/dir/mod.ts", `a?.in / 2; const u = import.meta.url / 3;`, `a?.in / 2; const u = ` + modURL + ` / 3;`},
	{"a member named void is a value", "/ext/p/dir/mod.ts", `x.void / 2; const u = import.meta.url / 3;`, `x.void / 2; const u = ` + modURL + ` / 3;`},
	{"a member named if is no condition", "/ext/p/dir/mod.ts", `x.if (a) / 2; const u = import.meta.url / 3;`, `x.if (a) / 2; const u = ` + modURL + ` / 3;`},
	{"a prefix increment starts an operand", "/ext/p/dir/mod.ts", `const q = ++i / 2, u = import.meta.url / 3;`, `const q = ++i / 2, u = ` + modURL + ` / 3;`},
	{"a prefix decrement starts an operand", "/ext/p/dir/mod.ts", `const q = --i / 2, u = import.meta.url / 3;`, `const q = --i / 2, u = ` + modURL + ` / 3;`},
	{"a unary plus after a plus", "/ext/p/dir/mod.ts", `const q = a + +b / 2, u = import.meta.url / 3;`, `const q = a + +b / 2, u = ` + modURL + ` / 3;`},
	{"a regex after a postfix operator and a plus", "/ext/p/dir/mod.ts", `const t = a++ + /import.meta.url/.test(x); const u = import.meta.url;`, `const t = a++ + /import.meta.url/.test(x); const u = ` + modURL + `;`},
	{"a regex after a postfix decrement and a minus", "/ext/p/dir/mod.ts", `const t = a-- - /__dirname/.lastIndex, u = __dirname;`, `const t = a-- - /__dirname/.lastIndex, u = ` + modDir + `;`},
	{"a regex after a prefix operator", "/ext/p/dir/mod.ts", `const t = ++/__dirname/.lastIndex, u = __dirname;`, `const t = ++/__dirname/.lastIndex, u = ` + modDir + `;`},
	{"a non-null assertion is a value", "/ext/p/dir/mod.ts", `const q = x! / 2, u = import.meta.url / 3;`, `const q = x! / 2, u = ` + modURL + ` / 3;`},
	{"a negation starts an operand", "/ext/p/dir/mod.ts", `const t = !/__dirname/.test(x), u = __dirname;`, `const t = !/__dirname/.test(x), u = ` + modDir + `;`},
	{"a not-equal operator is no assertion", "/ext/p/dir/mod.ts", `const t = a !== /__dirname/.source, u = __dirname;`, `const t = a !== /__dirname/.source, u = ` + modDir + `;`},

	// Line terminators.
	{"a backslash before CR LF continues a string", "/ext/p/dir/mod.ts", "const s = \"a\\\r\nb import.meta.url\"; const u = import.meta.url;", "const s = \"a\\\r\nb import.meta.url\"; const u = " + modURL + ";"},
	{"a bare CR ends a line comment", "/ext/p/dir/mod.ts", "// import.meta.url\rconst u = import.meta.url;", "// import.meta.url\rconst u = " + modURL + ";"},
	{"U+2028 ends a line comment", "/ext/p/dir/mod.ts", "// import.meta.url\u2028const u = import.meta.url;", "// import.meta.url\u2028const u = " + modURL + ";"},
	{"U+2029 ends a line comment", "/ext/p/dir/mod.ts", "// import.meta.url\u2029const u = import.meta.url;", "// import.meta.url\u2029const u = " + modURL + ";"},
	{"U+2028 is text in a string", "/ext/p/dir/mod.ts", "const s = \"a\u2028import.meta.url\"; const u = import.meta.url;", "const s = \"a\u2028import.meta.url\"; const u = " + modURL + ";"},
	{"a bare CR ends a hashbang", "/ext/p/dir/mod.mjs", "#!/usr/bin/env node\rconst u = import.meta.url;", "#!/usr/bin/env node\rconst u = \"file:///ext/p/dir/mod.mjs\";"},
	{"a bare CR ends a regex's line", "/ext/p/dir/mod.ts", "const a = b / c;\rconst r = /__dirname/; use(__dirname);", "const a = b / c;\rconst r = /__dirname/; use(" + modDir + ");"},

	// JSX in a .js file.
	{"JSX text in a .js file", "/ext/p/dir/mod.js", `const a = <div title="__dirname">import.meta.url {import.meta.url}</div>;`, `const a = <div title="__dirname">import.meta.url {"file:///ext/p/dir/mod.js"}</div>;`},
	{"a comparison in a .js file is no element", "/ext/p/dir/mod.js", `const ok = a < b && c > d, u = import.meta.url;`, `const ok = a < b && c > d, u = "file:///ext/p/dir/mod.js";`},
	{"a comparison after a postfix increment in a .js file", "/ext/p/dir/mod.js", `if (i++ <b || j-- <c) use(import.meta.url);`, `if (i++ <b || j-- <c) use("file:///ext/p/dir/mod.js");`},

	// TypeScript-only positions.
	{"typeof in a satisfies type", "/ext/p/dir/mod.ts", `const v = x satisfies typeof __dirname;`, same},
	{"typeof in a return type", "/ext/p/dir/mod.ts", `function f(): typeof __filename { return ""; }`, same},
	{"typeof in a type argument", "/ext/p/dir/mod.ts", `type R = Record<string, typeof __dirname>;`, same},
	{"keyof typeof", "/ext/p/dir/mod.ts", `type K = keyof typeof __dirname;`, same},
	{"typeof of require.resolve in a type", "/ext/p/dir/mod.ts", `let f: typeof require.resolve;`, same},
	{"typeof as a guard", "/ext/p/dir/mod.ts", `if (typeof __dirname === "string") use(__dirname);`, `if (typeof __dirname === "string") use(` + modDir + `);`},
	{"enum members are names", "/ext/p/dir/mod.ts", "enum E { __dirname, __filename = 1 }\nuse(__dirname);", "enum E { __dirname, __filename = 1 }\nuse(" + modDir + ");"},
	{"a const enum", "/ext/p/dir/mod.ts", "const enum E { A, __dirname }\nuse(__dirname);", "const enum E { A, __dirname }\nuse(" + modDir + ");"},
	{"a parameter property", "/ext/p/dir/mod.ts", `class C { constructor(private __dirname: string) {} }`, same},
	{"parameter properties with modifiers", "/ext/p/dir/mod.ts", `class C { constructor(public readonly __dirname: string, protected __filename: string, a: number) {} }`, same},
	{"a class field without a value", "/ext/p/dir/mod.ts", "class C { __dirname; }\nuse(__dirname);", "class C { __dirname; }\nuse(" + modDir + ");"},
	{"a class field with a value", "/ext/p/dir/mod.ts", "class C { __dirname = 1 }\nuse(__dirname);", "class C { __dirname = 1 }\nuse(" + modDir + ");"},
	{"class fields with modifiers", "/ext/p/dir/mod.ts", "class C { static __filename = 2; readonly __dirname: string; declare __filename: string }\nuse(__filename);", "class C { static __filename = 2; readonly __dirname: string; declare __filename: string }\nuse(" + modTS + ");"},
	{"class fields after a method and without semicolons", "/ext/p/dir/mod.ts", "class C {\n  m() {}\n  __dirname\n  __filename\n}", same},
	{"a class with a generic head", "/ext/p/dir/mod.ts", "class C<T extends Base<string>> extends D<T> implements I, J { __dirname }\nuse(__dirname);", "class C<T extends Base<string>> extends D<T> implements I, J { __dirname }\nuse(" + modDir + ");"},
	{"a class expression", "/ext/p/dir/mod.ts", "const C = class extends B { __dirname = 1 };\nuse(__dirname);", "const C = class extends B { __dirname = 1 };\nuse(" + modDir + ");"},
	{"a class field named like a method of an object", "/ext/p/dir/mod.ts", "class C { a = { __dirname: 1 }; b = () => __dirname }", "class C { a = { __dirname: 1 }; b = () => " + modDir + " }"},
	{"an interface", "/ext/p/dir/mod.ts", "interface I { __dirname: string; readonly __filename?: string; require(id: string): void }\nuse(__dirname);", "interface I { __dirname: string; readonly __filename?: string; require(id: string): void }\nuse(" + modDir + ");"},
	{"a type literal", "/ext/p/dir/mod.ts", "type T = { __dirname?: string; __filename: string };\nuse(__dirname);", "type T = { __dirname?: string; __filename: string };\nuse(" + modDir + ");"},
	{"an export list renames", "/ext/p/dir/mod.ts", `export { x as __dirname };`, same},
	{"an export list of a module's name", "/ext/p/dir/mod.ts", `export { __dirname as d } from "./m";`, same},
	{"an export list of names", "/ext/p/dir/mod.ts", `export { a, __filename, b as __dirname } from "./m";`, same},
	{"a namespace re-export", "/ext/p/dir/mod.ts", `export * as __dirname from "./m";`, same},
	{"a type export list", "/ext/p/dir/mod.ts", `export type { __dirname as d } from "./m";`, same},
	{"an import of a name under another name", "/ext/p/dir/mod.ts", "import { __dirname as d } from \"./m\";\nuse(__dirname, d);", "import { __dirname as d } from \"./m\";\nuse(" + modDir + ", d);"},
	{"an import under the name", "/ext/p/dir/mod.ts", "import { x as __dirname } from \"./m\";\nuse(__dirname);", same},
	{"a type annotation with typeof is no assignment", "/ext/p/dir/mod.ts", "const x: typeof __dirname = \"a\";\nuse(__dirname);", "const x: typeof __dirname = \"a\";\nuse(" + modDir + ");"},
	{"a let annotation with typeof", "/ext/p/dir/mod.ts", "let x: typeof __filename = \"a\";\nuse(__filename);", "let x: typeof __filename = \"a\";\nuse(" + modTS + ");"},
	{"a var annotation with typeof", "/ext/p/dir/mod.ts", "var x: typeof __dirname = \"a\", y: typeof __filename = \"b\";\nuse(__dirname, __filename);", "var x: typeof __dirname = \"a\", y: typeof __filename = \"b\";\nuse(" + modDir + ", " + modTS + ");"},
	{"an exported const annotation with typeof", "/ext/p/dir/mod.ts", "export const x: typeof __dirname = \"a\";\nexport const y = __dirname;", "export const x: typeof __dirname = \"a\";\nexport const y = " + modDir + ";"},
	{"a parameter default with a typeof annotation", "/ext/p/dir/mod.ts", "function f(a: typeof __dirname = \"x\") { return a; }\nuse(__dirname);", "function f(a: typeof __dirname = \"x\") { return a; }\nuse(" + modDir + ");"},
	{"an arrow parameter default with a typeof annotation", "/ext/p/dir/mod.ts", "const g = (a: typeof __filename = \"x\") => a;\nuse(__filename);", "const g = (a: typeof __filename = \"x\") => a;\nuse(" + modTS + ");"},
	{"a class field with a typeof annotation and a value", "/ext/p/dir/mod.ts", "class C { x: typeof __filename = \"a\"; static y: typeof __dirname = \"b\" }\nuse(__filename, __dirname);", "class C { x: typeof __filename = \"a\"; static y: typeof __dirname = \"b\" }\nuse(" + modTS + ", " + modDir + ");"},
	{"a real declaration after a typeof annotation still counts", "/ext/p/dir/mod.ts", "const x: typeof __dirname = \"a\";\nconst __dirname = \"b\";\nuse(__dirname);", same},
	{"an assignment after a ternary colon still declares", "/ext/p/dir/mod.ts", "a ? b : __dirname = c;\nuse(__dirname);", same},
	{"a typeof guard before an assignment", "/ext/p/dir/mod.ts", "if (typeof __dirname === \"undefined\") __dirname = \"x\";\nuse(__dirname);", same},
	{"an interface member without a type", "/ext/p/dir/mod.ts", "interface I { __dirname; __filename\n  a: string }\nuse(__dirname);", "interface I { __dirname; __filename\n  a: string }\nuse(" + modDir + ");"},
	{"a class head with a default type argument", "/ext/p/dir/mod.ts", "class C<T = string> { __dirname = 1 }\nuse(__dirname);", "class C<T = string> { __dirname = 1 }\nuse(" + modDir + ");"},
}

func TestRewriteModuleScannerCases(t *testing.T) {
	for _, c := range scannerCases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteModule(c.in, c.file)
			want := c.want
			if want == same {
				want = c.in
			}
			if got.Source != want {
				t.Errorf("rewrite of\n  %q\ngot\n  %q\nwant\n  %q", c.in, got.Source, want)
			}
			if got.Unpinned != "" {
				t.Errorf("Unpinned = %q", got.Unpinned)
			}
		})
	}
}

// The code a rewrite gives must parse: Bun's transpiler (stock Bun 1.4.2, PIG_AOT_TEST_BUN) reads each rewritten module without a syntax error. A name that is replaced where
// the grammar wants a name (`typeof "/p"` in a type, a class field `"/p"`, an enum member) is a syntax error or the wrong member, and no string comparison sees why.
func TestRewriteModuleOutputParses(t *testing.T) {
	bun := os.Getenv("PIG_AOT_TEST_BUN")
	if bun == "" {
		t.Skip("set PIG_AOT_TEST_BUN to a Bun executable to check that rewritten modules parse")
	}
	dir := t.TempDir()
	for i, c := range scannerCases {
		file := filepath.Join(dir, "mod"+string(rune('a'+i%26))+string(rune('a'+i/26))+filepath.Ext(c.file))
		// Parsing does not care where the module lives; the rewrite is made for the path the test names.
		out := rewriteModule(c.in, c.file).Source
		if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		if msg, err := exec.Command(bun, "build", "--no-bundle", "--target=bun", file).CombinedOutput(); err != nil {
			t.Errorf("%s: the rewrite of %q does not parse:\n  %q\n%s", c.name, c.in, out, msg)
		}
	}
}
