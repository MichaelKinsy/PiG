package codemode_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// The pinned Pi CodemodeSandbox and codemode.Sandbox run the same scripts: the returned value, the output items and the error kind, name and message must be equal.
func TestScriptOutcomesMatchPi(t *testing.T) {
	scripts := []string{
		`return undefined`, `return null`, `return NaN`, `return Infinity`, `return -0`, `return [NaN, -0, Infinity, undefined, () => 1]`,
		`return { a: undefined, b: () => 1, c: Symbol("s"), d: 1n === 1n }`, `return 10n`, `return [10n]`, `return Symbol("x")`, `return () => 1`,
		`return new Date(0)`, `return new Date(NaN)`, `return new Map([[1, 2]])`, `return new Set([1])`, `return /x/g`,
		`return { toJSON() { return 7 } }`, `return { toJSON() { throw new Error("tj") } }`, `const a = {}; a.self = a; return a`, `return "\ud800"`, `return "a\u2028b\u2029c"`,
		`return { "\ud800": 1 }`, `return [1, , 3]`, `return new Uint8Array([1, 2])`, `return new Number(3)`, `return Object.create(null)`, `return "é😀"`,
		`text("a"); return "b"`, `text({ a: 1 }); text([1, undefined]); text(undefined); text(null); text(1n)`, `text(Symbol("s"))`, `text(() => 1)`, `text("a", "b")`, `text()`,
		`console.log("a", { b: 1 }, [2], null, undefined)`, `console.log("%s %d %i %f %j %o %O %%", "s", 4.5, 4.5, "1.5", { a: 1 }, [1], { b: 2 }, "extra")`,
		`console.log("%c styled", "color: red")`, `console.error("e"); console.warn("w"); console.info("i"); console.debug("d")`, `console.log(new Error("x").message)`,
		`console.log(1n, Symbol("s"), () => 1, new Map([[1, 2]]), new Set([1]), new Date(0), /x/g)`, `console.log("%s", { toString() { return "custom" } })`, `console.log("%s", 5n, Symbol("q"), null, undefined)`,
		`console.table([{ a: 1 }])`, `console.group("g"); console.log("in"); console.groupEnd()`, `console.assert(false, "m"); console.assert(true, "n")`, `console.dir({ a: 1 })`, `console.count(); console.count("x"); console.countReset(); console.count()`,
		`console.log(NaN, -0, 1e21, 1e-7, 123456789012345680000)`, `console.log("\ud800")`, `console.log([["a"]], { a: { b: { c: { d: 1 } } } })`, `const a = {}; a.self = a; console.log(a)`,
		`throw new Error("bad")`, `throw new TypeError("tbad")`, `throw "str"`, `throw 5`, `throw null`, `throw undefined`, `throw { message: "obj" }`, `throw { name: "Custom", message: "m" }`,
		`const e = new Error("m"); e.name = "Odd"; throw e`, `class E extends Error { constructor(m) { super(m); this.name = "E" } } throw new E("sub")`, `throw new Error()`, `throw new Error("a\nb")`,
		`await Promise.reject(new Error("rej"))`, `await Promise.reject("rej")`, `return await Promise.resolve(3)`, `return await new Promise((r) => r(4))`, `return Promise.resolve(5)`,
		`null.x`, `undefined.x`, `x.y`, `let a = 1; let a = 2`, `return (`, `function f() { return f() } f()`, `return JSON.parse("{")`, `return JSON.parse("")`, `return JSON.parse("[1,]")`,
		`return typeof fetch + typeof process + typeof require + typeof setTimeout + typeof globalThis.console`, `return Object.keys(globalThis).sort()`, `return typeof tools + typeof store + typeof load + typeof text + typeof image + typeof exit`,
		`exit(3)`, `exit()`, `text("a"); exit("x"); text("b")`, `return exit(undefined)`, `store("k", 1); return load("k")`, `store("", 1); return load("")`, `store(1, 2)`, `store("k")`, `load(1)`, `load()`,
		`store("k", () => 1)`, `store("k", 1n)`, `store("k", undefined); return load("k")`, `store("k", { a: undefined }); return load("k")`, `store("k", NaN); return load("k")`, `store("k", "\ud800"); return load("k")`,
		`return [typeof globalThis.Date, new Date(0).toISOString()]`, `return [1 / 3, 0.1 + 0.2, 2 ** 53, 2 ** 70, -1e-7, 1e21]`, `return "x".repeat(70000).length`, `return [..."ab"].map((c) => c.charCodeAt(0))`,
		`return Intl.DateTimeFormat().resolvedOptions().timeZone`, `return new Date(2020, 0, 1).getTimezoneOffset()`, `return Math.max()`, `return [Math.round(-0.5), Math.round(2.5)]`, `return structuredClone({ a: 1 })`, `return typeof queueMicrotask`,
		`return [1, 2, 3].toSorted((a, b) => b - a)`, `return Object.groupBy([1, 2, 3], (n) => n % 2 ? "odd" : "even")`, `return "abc".at(-1)`, `return [..."a😀"].length`, `return "İ".toLowerCase().length`, `return "ß".toUpperCase()`,
		`image()`, `image(1)`, `image("x")`, `image({})`, `image({ type: "image", data: "AAAA", mimeType: "image/png" })`, `image({ type: "image", data: "iVBORw0KGgo=", mimeType: "image/png" })`,
		`image({ image_url: "data:image/png;base64,iVBORw0KGgo=" })`, `image({ image_url: "data:image/png;base64,AAAA" })`, `image({ image_url: "data:text/plain;base64,AAAA" })`, `image({ image_url: "http://x/y.png" })`,
		`image({ image_url: 5 })`, `image("data:image/png;base64,iVBORw0KGgo=")`, `image(new Uint8Array([137,80,78,71,13,10,26,10]))`, `image({ type: "image", data: "/9j/4AAQ", mimeType: "image/jpeg" })`, `image({ type: "image", data: "R0lGODlh", mimeType: "image/gif" })`,
		`image({ type: "text", text: "x" })`, `image({ type: "image", data: 5, mimeType: "image/png" })`, `image({ type: "image", data: "AAAA" })`,
		`store("k", "x".repeat(100000))`, `for (let i = 0; i < 400; i++) store("k" + i, "x".repeat(2000))`, `store("a".repeat(300), 1)`, `store("k", { a: "x".repeat(500000) })`,
		`let d = 1; for (let i = 0; i < 200; i++) d = [d]; return d`, `let d = {}; for (let i = 0; i < 2000; i++) d = { d }; return d`, `return "x".repeat(2000000).length`, `text("x".repeat(2000000))`,
		`for (let i = 0; i < 20000; i++) console.log(i)`, `console.log(Array.from({ length: 200 }, (_, i) => i))`, `console.log("x".repeat(100000))`, `console.log({ a: "x".repeat(5000) })`,
		`return new Array(5).fill().map((_, i) => ({ i }))`, `return JSON.stringify({ a: 1n })`, `return [Object.getOwnPropertyNames(globalThis).includes("tools")]`, `tools.nope()`, `tools`, `return typeof models`,
		`const o = { get a() { throw new Error("getter") } }; return o`, `const o = { get a() { return 1 } }; return o`, `return Object.defineProperty({}, "x", { value: 1, enumerable: false })`, `return new Proxy({}, { ownKeys() { throw new Error("pk") } })`,
		`return [Symbol.iterator.toString()]`, `return Array.from({ length: 3 }, (_, i) => i * 1.1)`, `return 0.000001234`, `return 1e300 * 1e10`, `return -1e-324`, `return 123456789.12345678`,
		`return await Promise.all([1, Promise.resolve(2)])`, `return await Promise.race([new Promise(() => {}), 1])`, `await new Promise(() => {})`, `return new Promise(() => {})`, `Promise.reject(new Error("unhandled")); return 1`,
		`(async () => { throw new Error("late") })(); return 1`, `let r; await new Promise((res) => { r = res }); `, `return await (async function* () { yield 1 })().next()`,
		`return ["a", "B", "c"].sort((x, y) => x.localeCompare(y))`, `return (1234567.891).toLocaleString()`, `return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(1234.5)`,
	}
	// Returning an Error is left out: D145 records that PiG's prelude serializes an Error's name and message where Pi's gives {}.
	wasmPath, err := filepath.Abs("assets/quickjs.wasm")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath, err := filepath.Abs("testdata/script_oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		OK     bool     `json:"ok"`
		Value  string   `json:"value"`
		Output []string `json:"output"`
		Error  string   `json:"error"`
	}
	var want []outcome
	pioracle.Run(t, `
const { spawnSync } = await import("node:child_process");
const child = spawnSync(process.execPath, [process.env.ORACLE_SCRIPT], { input: JSON.stringify(input), encoding: "utf8", maxBuffer: 1 << 28 });
if (child.status !== 0) throw new Error(child.stderr);
emit(JSON.parse(child.stdout));
`, scripts, &want, "QUICKJS_WASM="+wasmPath, "ORACLE_SCRIPT="+scriptPath)
	bad := 0
	for i, script := range scripts {
		sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{TimeoutMs: 10_000})
		if err != nil {
			t.Fatal(err)
		}
		result, err := sandbox.Execute(t.Context(), script, codemode.ExecuteOptions{})
		_ = sandbox.Close()
		if err != nil {
			t.Fatal(err)
		}
		got := outcome{OK: result.OK}
		if result.OK {
			got.Value = "<undefined>"
			if result.Value != nil {
				got.Value = string(result.Value)
			}
		} else {
			got.Error = string(result.Error.Kind) + "|" + result.Error.Name + "|" + plainText(result.Error.Message)
		}
		for _, item := range result.Output {
			switch {
			case item.Type == codemode.OutputItemImage:
				got.Output = append(got.Output, "image:"+item.MimeType+":"+item.Data)
			case item.Console:
				got.Output = append(got.Output, "console:"+plainText(item.Text))
			default:
				got.Output = append(got.Output, "text:"+plainText(item.Text))
			}
		}
		if got.OK != want[i].OK || got.Value != want[i].Value || got.Error != want[i].Error || len(got.Output) != len(want[i].Output) || func() bool {
			for j := range got.Output {
				if got.Output[j] != want[i].Output[j] {
					return true
				}
			}
			return false
		}() {
			if bad++; bad <= 12 {
				t.Errorf("%s\n  PiG %+v\n  Pi  %+v", script, got, want[i])
			}
		}
	}
	if bad > 12 {
		t.Errorf("%d of %d scripts differ", bad, len(scripts))
	}
}

// plainText names each unpaired surrogate of a WTF-8 string the way testdata/script_oracle.mjs does.
func plainText(text string) string {
	var out strings.Builder
	units := jsstring.ToUTF16(text)
	for i := 0; i < len(units); i++ {
		switch unit := units[i]; {
		case unit >= 0xd800 && unit <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff:
			out.WriteString(string(utf16.Decode(units[i : i+2])))
			i++
		case unit >= 0xd800 && unit <= 0xdfff:
			fmt.Fprintf(&out, "<U+%x>", unit)
		default:
			out.WriteString(string(rune(unit)))
		}
	}
	return out.String()
}
