package boltruntime

import (
	"strings"
	"testing"
)

// The module under rewrite lives at /ext/p/dir/mod.<ext>. Where a test spells out the code the rewrite emits, it is the code Pi-Bolt's rewriteCopy emits for that file
// (packages/coding-agent/src/gen-plugin-manifest.mjs), so that a build here and a `pi-bolt compile` of the same package behave alike.
const (
	modDir = `"/ext/p/dir"`
	modTS  = `"/ext/p/dir/mod.ts"`
	modURL = `"file:///ext/p/dir/mod.ts"`
	// createRequire(file), as requireFrom writes it for /ext/p/dir/mod.ts.
	modRequire = `process.getBuiltinModule("node:module").createRequire("\u002fext\u002fp\u002fdir\u002fmod.ts")`
)

const same = "\x00same"

func TestRewriteModulePinsTheModulesOwnLocation(t *testing.T) {
	const sentinelPiNames = `/^(?:typebox|@sinclair\/typebox|@(?:earendil-works|mariozechner)\/pi-(?:agent-core|ai|coding-agent|tui))(?:\/|$)/`
	resolveFrom := `((s) => { if (typeof s !== "string" || ` + sentinelPiNames + `.test(s) || /^[a-z][a-z0-9+.-]*:/i.test(s)) return s; try { return Bun.resolveSync(s, "\u002fext\u002fp\u002fdir"); } catch { return s; } })`
	metaResolve := `((s) => { const p = Bun.resolveSync(s, "\u002fext\u002fp\u002fdir"); return p.startsWith("/") ? Bun.pathToFileURL(p).href : p; })`
	cases := []struct {
		name, file, in, want string
	}{
		// What the spike's two packages do, and what the refusal was for.
		{"sibling of import.meta.url", "/ext/p/dir/mod.ts",
			`const here = dirname(fileURLToPath(import.meta.url)); const data = join(here, "..", "skills", "a.md");`,
			`const here = dirname(fileURLToPath(` + modURL + `)); const data = join(here, "..", "skills", "a.md");`},
		{"new URL beside the module", "/ext/p/dir/mod.ts",
			`const locale = readFileSync(new URL("./locales/de.json", import.meta.url), "utf8");`,
			`const locale = readFileSync(new URL("./locales/de.json", ` + modURL + `), "utf8");`},
		{"__dirname and __filename in CommonJS", "/ext/p/dir/mod.cjs",
			`const fs = require("fs"); module.exports = [fs.readFileSync(__dirname + "/data.txt"), __filename];`,
			`const fs = require("fs"); module.exports = [fs.readFileSync("/ext/p/dir" + "/data.txt"), "/ext/p/dir/mod.cjs"];`},
		{"import.meta.dirname filename dir path file", "/ext/p/dir/mod.mjs",
			`[import.meta.dirname, import.meta.filename, import.meta.dir, import.meta.path, import.meta.file];`,
			`["/ext/p/dir", "/ext/p/dir/mod.mjs", "/ext/p/dir", "/ext/p/dir/mod.mjs", "mod.mjs"];`},
		{"import.meta.resolve", "/ext/p/dir/mod.ts", `const x = import.meta.resolve("./a.js");`, `const x = ` + metaResolve + `("./a.js");`},
		{"require.resolve", "/ext/p/dir/mod.ts", `const p = require.resolve("pkg/package.json");`, `const p = ` + modRequire + `.resolve("pkg/package.json");`},
		{"computed require", "/ext/p/dir/mod.ts", `const m = require(name); const n = require("./" + id + ".js");`,
			`const m = ` + modRequire + `(name); const n = ` + modRequire + `("./" + id + ".js");`},
		{"computed import()", "/ext/p/dir/mod.ts", "const m = await import(name); const n = await import(`./${id}.js`);",
			"const m = await ((s, o) => import(" + resolveFrom + "(s), o))(name); const n = await ((s, o) => import(" + resolveFrom + "(s), o))(`./${id}.js`);"},
		{"static require and import() are the bundler's", "/ext/p/dir/mod.ts", `const fs = require("fs"); const m = await import("./a.js"); const t = require(` + "`node:path`" + `); import("x", { with: { type: "json" } });`, same},
		{"line breaks inside the replaced span stay", "/ext/p/dir/mod.ts", "const u = import\n  .meta\n  .url;\nnext();", "const u = " + modURL + "\n\n;\nnext();"},
		{"a shebang is skipped", "/ext/p/dir/mod.mjs", "#!/usr/bin/env node\nconsole.log(import.meta.dir);", "#!/usr/bin/env node\nconsole.log(\"/ext/p/dir\");"},
		{"a spread is not a property access", "/ext/p/dir/mod.ts", `f(...__dirname, ...require);`, `f(..."/ext/p/dir", ...require);`},
		{"optional chaining is a property access", "/ext/p/dir/mod.ts", `x?.__dirname; x?.require("a" + b); x?.import(a);`, same},
		{"import.meta other members are left", "/ext/p/dir/mod.ts", `if (import.meta.main) { run(import.meta.env.X, import.meta.hot); }`, same},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteModule(c.in, c.file)
			want := c.want
			if want == same {
				want = c.in
			}
			if got.Source != want {
				t.Errorf("rewrite of\n  %s\ngot\n  %s\nwant\n  %s", c.in, got.Source, want)
			}
			if got.Changed != (want != c.in) {
				t.Errorf("Changed = %v for a source that is %s", got.Changed, map[bool]string{true: "unchanged", false: "changed"}[want == c.in])
			}
			if got.Unpinned != "" {
				t.Errorf("Unpinned = %q", got.Unpinned)
			}
			if strings.Count(got.Source, "\n") != strings.Count(c.in, "\n") {
				t.Errorf("line count changed: %d, was %d", strings.Count(got.Source, "\n"), strings.Count(c.in, "\n"))
			}
		})
	}
}

// Only code changes. Each case puts the text of a location where a scanner that does not know the syntax would read it as code.
func TestRewriteModuleChangesCodeNeverTextOrComments(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"line comment", "// import.meta.url and __dirname and require(x)\nconst a = 1;", same},
		{"block comment", "/* import.meta.url\n__dirname */ const a = 1;", same},
		{"comment between tokens", "const u = import /* c */ . /* d */ meta . url;", "const u = " + modURL + ";"},
		{"double quoted string", `const s = "import.meta.url __dirname require(x)";`, same},
		{"single quoted string", `const s = 'import.meta.url __dirname';`, same},
		{"escaped quote in a string", `const s = "a\" import.meta.url"; const t = __dirname;`, `const s = "a\" import.meta.url"; const t = ` + modDir + `;`},
		{"template text", "const s = `import.meta.url __dirname`;", same},
		{"template substitution is code", "const s = `dir ${__dirname} url ${import.meta.url} text __dirname`;", "const s = `dir ${" + modDir + "} url ${" + modURL + "} text __dirname`;"},
		{"nested templates", "const s = `a${`b${import.meta.url}c`}d import.meta.url`;", "const s = `a${`b${" + modURL + "}c`}d import.meta.url`;"},
		{"object literal in a substitution", "const s = `${ {a: 1}.a } __dirname ${__dirname}`;", "const s = `${ {a: 1}.a } __dirname ${" + modDir + "}`;"},
		{"regex literal", `const re = /import.meta.url|__dirname/g; const x = __dirname;`, `const re = /import.meta.url|__dirname/g; const x = ` + modDir + `;`},
		{"regex with a slash in a class", `const re = /[/]__dirname/; use(__dirname);`, `const re = /[/]__dirname/; use(` + modDir + `);`},
		{"regex after return", "function f() { return /__dirname/.test(x) }", same},
		{"regex after a condition", "if (ok) /__dirname/.test(x);", same},
		{"regex after a block", "function f() {}\n/__dirname/.test(x);", same},
		{"division is not a regex", "const r = a / 2 / __dirname;", "const r = a / 2 / " + modDir + ";"},
		{"division after an object literal", "const r = {a: 1} / __dirname;", "const r = {a: 1} / " + modDir + ";"},
		{"division after a call", "const r = f(a) / 2 + __dirname / 3;", "const r = f(a) / 2 + " + modDir + " / 3;"},
		{"string after a regex", "const r = /a/.test(\"__dirname\"); const q = __dirname;", "const r = /a/.test(\"__dirname\"); const q = " + modDir + ";"},
		{"identifier that contains the word", "const my__dirname = 1; const __dirnames = 2; const import_meta = 3; const x = a.import.meta;", same},
		{"property and object key named like it", "const o = { __dirname: 1, __filename() {}, require: 2 }; o.__dirname; o.__filename; o.require(x);", same},
		{"class methods named import and require", "class A { async import(url) { return 1 } require(id) { return 2 } static require(id) { return 3 } get __dirname() { return 4 } }", same},
		{"interface members", "interface M { require(id: string): unknown; import(path: string): Promise<unknown>; }", same},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteModule(c.in, "/ext/p/dir/mod.ts")
			want := c.want
			if want == same {
				want = c.in
			}
			if got.Source != want {
				t.Errorf("rewrite of\n  %s\ngot\n  %s\nwant\n  %s", c.in, got.Source, want)
			}
		})
	}
}

// A module that declares __dirname, __filename or require keeps its own. Only a declaration counts, not a comment or a string that spells one.
func TestRewriteModuleKeepsWhatTheModuleDeclares(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"the ESM idiom", "const __dirname = dirname(fileURLToPath(import.meta.url));\nconst d = join(__dirname, \"data\");",
			"const __dirname = dirname(fileURLToPath(" + modURL + "));\nconst d = join(__dirname, \"data\");"},
		{"let and var and function and class", "var __filename = f(import.meta.url); let __dirname; function require(id) { return id } class Y {}", "var __filename = f(" + modURL + "); let __dirname; function require(id) { return id } class Y {}"},
		{"createRequire", "import { createRequire } from \"node:module\";\nconst require = createRequire(import.meta.url);\nrequire(name); require.resolve(\"x\");",
			"import { createRequire } from \"node:module\";\nconst require = createRequire(" + modURL + ");\nrequire(name); require.resolve(\"x\");"},
		{"declare in TypeScript", "declare const __dirname: string;\ndeclare function require(id: string): any;\nuse(__dirname, require(name));", same},
		{"an import binding", `import { __dirname } from "./paths.js"; use(__dirname);`, same},
		{"an import binding with a default", `import paths, { __filename } from "./paths.js"; use(__filename);`, same},
		{"a namespace import", `import * as require from "./r.js"; require(name);`, same},
		{"an assignment", "__dirname = \"/x\"; use(__dirname);", same},
		{"a function parameter", "function f(a, __dirname) { return __dirname }\nconst g = (require, b) => require(b);\nconst h = async (__filename = \"x\") => __filename;", same},
		{"a parameter with a type", "function f(a: string, __dirname: string): string { return __dirname }", same},
		{"a destructuring declaration", "const { __dirname } = ctx; const { a, __filename } = ctx; const [require] = list; use(__dirname, __filename, require(x));", same},
		{"a nested destructuring declaration", "const { a: { __dirname } } = ctx; use(__dirname);", same},
		{"a destructuring parameter", "function f({ __dirname, b }) { return __dirname }", same},
		{"a destructuring assignment", "({ __dirname } = ctx); use(__dirname);", same},
		{"a catch binding", "try {} catch (__dirname) { use(__dirname) }", same},

		// A comment or a string is not a declaration.
		{"a comment that spells a declaration", "// const __dirname = x\n/* function require() {} */\nuse(__dirname);", "// const __dirname = x\n/* function require() {} */\nuse(" + modDir + ");"},
		{"a string that spells a declaration", "const s = \"const __dirname = 1; function require(){}\"; use(__dirname, __filename);", "const s = \"const __dirname = 1; function require(){}\"; use(" + modDir + ", " + modTS + ");"},
		{"a template that spells a declaration", "const s = `let __filename = 1`; use(__filename);", "const s = `let __filename = 1`; use(" + modTS + ");"},

		// A use is not a declaration.
		{"a condition", "if (__dirname) { go() } while (__filename) {}", "if (" + modDir + ") { go() } while (" + modTS + ") {}"},
		{"arguments of a call", "use(a, __dirname, b); use(__filename); join(root, __dirname)", "use(a, " + modDir + ", b); use(" + modTS + "); join(root, " + modDir + ")"},
		{"an object literal shorthand", "run({ __dirname }); const o = { a, __filename };", "run({ __dirname: " + modDir + " }); const o = { a, __filename: " + modTS + " };"},
		{"array elements", "const a = [x, __dirname, y]; const [p, q] = a;", "const a = [x, " + modDir + ", y]; const [p, q] = a;"},
		{"comparison and arrow body", "const same = __dirname == other; const f = () => __filename;", "const same = " + modDir + " == other; const f = () => " + modTS + ";"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteModule(c.in, "/ext/p/dir/mod.ts")
			want := c.want
			if want == same {
				want = c.in
			}
			if got.Source != want {
				t.Errorf("rewrite of\n  %s\ngot\n  %s\nwant\n  %s", c.in, got.Source, want)
			}
		})
	}
}

func TestRewriteModuleReadsJSXAsText(t *testing.T) {
	const tsx = "/ext/p/dir/mod.tsx"
	cases := []struct{ name, file, in, want string }{
		{"text and attribute strings of an element", tsx, `const a = <div title="import.meta.url">import.meta.url __dirname {import.meta.url}</div>;`,
			`const a = <div title="import.meta.url">import.meta.url __dirname {"file:///ext/p/dir/mod.tsx"}</div>;`},
		{"a fragment and nested elements", tsx, `const a = <><b>__dirname</b>{__dirname}</>;`, `const a = <><b>__dirname</b>{"/ext/p/dir"}</>;`},
		{"a generic arrow function is not an element", tsx, `const f = <T,>(x: T) => x; use(import.meta.url);`, `const f = <T,>(x: T) => x; use("file:///ext/p/dir/mod.tsx");`},
		{"a comparison in a .ts file is not an element", "/ext/p/dir/mod.ts", `const ok = a < b && c > d; use(import.meta.url, "<div>import.meta.url</div>");`, `const ok = a < b && c > d; use("file:///ext/p/dir/mod.ts", "<div>import.meta.url</div>");`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rewriteModule(c.in, c.file).Source; got != c.want {
				t.Errorf("rewrite of\n  %s\ngot\n  %s\nwant\n  %s", c.in, got, c.want)
			}
		})
	}
}

// What the rewrite cannot pin is reported, not guessed at.
func TestRewriteModuleReportsWhatItCannotPin(t *testing.T) {
	for name, in := range map[string]string{
		"import.meta as a value":        `const meta = import.meta; read(meta.url);`,
		"import.meta spread":            `const copy = { ...import.meta };`,
		"import.meta computed member":   `const u = import.meta["url"];`,
		"import.meta optional chaining": `const u = import.meta?.url;`,
		"import.meta passed on":         `load(import.meta);`,
	} {
		got := rewriteModule(in, "/ext/p/dir/mod.ts")
		if got.Unpinned != "import.meta" {
			t.Errorf("%s: Unpinned = %q for %s", name, got.Unpinned, in)
		}
	}
	for name, in := range map[string]string{
		"a named member": `use(import.meta.url, import.meta.dirname, import.meta.main);`,
		"in a comment":   "// import.meta\nconst a = 1;",
		"in a string":    `const s = "import.meta";`,
		"a property":     `use(x.import.meta);`,
	} {
		if got := rewriteModule(in, "/ext/p/dir/mod.ts"); got.Unpinned != "" {
			t.Errorf("%s: Unpinned = %q for %s", name, got.Unpinned, in)
		}
	}
	// A path that is not UTF-8 has no string literal that names it.
	got := rewriteModule("use(__dirname);", "/ext/p/d\xffr/mod.ts")
	if got.Unpinned == "" || got.Changed {
		t.Errorf("a path that is not UTF-8: %+v", got)
	}
}
