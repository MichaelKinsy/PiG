package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFactorySources lays out a one-package Pi checkout (.upstream/current/packages/fx/src) and returns its reader.
func writeFactorySources(t *testing.T, files map[string]string) *tsReader {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, "packages", "fx", "src", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return newTSReader(root, "fx")
}

const wrapperSource = `
export function wrapToolDefinition<TDetails = unknown>(
	definition: ToolDefinition<any, TDetails>,
	factory?: F,
): AgentTool<any, TDetails> {
	return {
		name: definition.name,
		prepareArguments: definition.prepareArguments,
		execute: (id, params, ctx?: Ctx) => definition.execute(id, params, ctx ?? (factory?.(id) as Ctx)),
	};
}
`

// TestFactoryNeverSetsFollowsTheFactoriesOfPi: rule T12u proves from the Pi source that a factory's value has no such member. The
// closing cases are the shapes of core/tools: a wrapper that copies the member of its argument, a definition literal that spreads a
// renderer constant of another file, a spread of a call, and Object.assign of a literal without the member. Every refusal is a way
// the member could reach the value.
func TestFactoryNeverSetsFollowsTheFactoriesOfPi(t *testing.T) {
	renderers := `export const findRenderers = { renderCall(args, theme) { return "}" + args; }, renderResult: () => /[}"]/.test("x") };
export function createShellRenderers(prompt: string) { return { renderCall: () => ` + "`${prompt}}`" + ` }; }
`
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"wrapper of a definition literal that spreads an imported constant", map[string]string{
			"w.ts": wrapperSource,
			"find.ts": `import { findRenderers } from "./r.ts";
import { wrapToolDefinition } from "./w.ts";
export function createFindToolDefinition(cwd: string, options?: X): ToolDefinition<typeof s, D | undefined> {
	return {
		name: "find",
		description: ` + "`a ${cwd} } b`" + `,
		async execute(id, params) { if (params) { return { a: 1 }; } return { b: 2 }; },
		...findRenderers,
	};
}
export function createFindTool(cwd: string, options?: X): TypeBoxAgentTool<typeof s> {
	return wrapToolDefinition(createFindToolDefinition(cwd, options));
}`,
			"r.ts": renderers,
		}, true},
		{"a definition built through a call, a local constant and Object.assign", map[string]string{
			"w.ts": wrapperSource,
			"r.ts": renderers,
			"bash.ts": `import { createShellRenderers } from "./r.ts";
import { wrapToolDefinition } from "./w.ts";
function createShellToolDefinition(cwd: string, config: C, options?: O): ToolDefinition {
	return { name: config.name, execute: async () => ({}), ...createShellRenderers(config.prompt) };
}
export function createBashTool(cwd: string, options?: O): TypeBoxAgentTool<typeof s> {
	const definition = createShellToolDefinition(cwd, bashToolConfig, options);
	const tool = wrapToolDefinition(definition);
	Object.assign(tool, { promptSnippet: definition.promptSnippet, promptGuidelines: definition.promptGuidelines });
	return tool;
}`,
		}, true},
		{"the factory's definition sets the member", map[string]string{
			"w.ts": wrapperSource,
			"edit.ts": `import { wrapToolDefinition } from "./w.ts";
export function createEditToolDefinition(cwd: string) { return { name: "edit", prepareArguments: prepareEditArguments, execute: async () => ({}) }; }
export function createEditTool(cwd: string) { return wrapToolDefinition(createEditToolDefinition(cwd)); }`,
		}, false},
		{"a spread the reader cannot read could carry the member", map[string]string{
			"w.ts": wrapperSource,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
export function createXToolDefinition(cwd: string, options?: O) { return { name: "x", ...options, execute: async () => ({}) }; }
export function createXTool(cwd: string, options?: O) { return wrapToolDefinition(createXToolDefinition(cwd, options)); }`,
		}, false},
		{"a spread constant that names the member", map[string]string{
			"w.ts": wrapperSource,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
const extras = { prepareArguments: fix };
export function createXToolDefinition(cwd: string) { return { name: "x", ...extras }; }
export function createXTool(cwd: string) { return wrapToolDefinition(createXToolDefinition(cwd)); }`,
		}, false},
		{"Object.assign sets the member after the wrapper", map[string]string{
			"w.ts": wrapperSource,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
export function createXToolDefinition(cwd: string) { return { name: "x" }; }
export function createXTool(cwd: string) {
	const tool = wrapToolDefinition(createXToolDefinition(cwd));
	Object.assign(tool, { prepareArguments: fix });
	return tool;
}`,
		}, false},
		{"the wrapped value is assigned the member", map[string]string{
			"w.ts": wrapperSource,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
export function createXToolDefinition(cwd: string) { return { name: "x" }; }
export function createXTool(cwd: string) {
	const tool = wrapToolDefinition(createXToolDefinition(cwd));
	tool.prepareArguments = fix;
	return tool;
}`,
		}, false},
		{"a wrapper that assigns the member to its argument", map[string]string{
			"w.ts": `export function wrapToolDefinition(definition: D) { definition.prepareArguments = normalize; return { name: definition.name, prepareArguments: definition.prepareArguments }; }`,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
export function createXToolDefinition(cwd: string) { return { name: "x" }; }
export function createXTool(cwd: string) { return wrapToolDefinition(createXToolDefinition(cwd)); }`,
		}, false},
		{"a callee that mutates the definition it is passed", map[string]string{
			"w.ts": `export function decorate(definition: D) { Object.assign(definition, { prepareArguments: normalize }); return { name: definition.name }; }`,
			"x.ts": `import { decorate } from "./w.ts";
export function createXToolDefinition(cwd: string) { return { name: "x" }; }
export function createXTool(cwd: string) {
	const definition = createXToolDefinition(cwd);
	const tool = decorate(definition);
	return tool;
}`,
		}, false},
		{"a wrapper that sets the member itself", map[string]string{
			"w.ts": `export function wrapToolDefinition(definition: D) { return { name: definition.name, prepareArguments: normalize }; }`,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
export function createXToolDefinition(cwd: string) { return { name: "x" }; }
export function createXTool(cwd: string) { return wrapToolDefinition(createXToolDefinition(cwd)); }`,
		}, false},
		{"a factory with a conditional return", map[string]string{
			"x.ts": `export function createXTool(cwd: string) { if (cwd) { return { name: "a" }; } return { name: "b" }; }`,
		}, false},
		{"a factory with a braceless conditional return that sets the member", map[string]string{
			"x.ts": `function other() { return { name: "a", prepareArguments: normalize }; }
export function createXTool(cwd: string) { if (cwd) return other(); return { name: "b" }; }`,
		}, false},
		{"a wrapper with a braceless conditional return that sets the member", map[string]string{
			"w.ts": `export function wrapToolDefinition(definition: D) { if (definition.name) return legacy(definition); return { name: definition.name }; }
function legacy(definition: D) { return { name: definition.name, prepareArguments: normalize }; }`,
			"x.ts": `import { wrapToolDefinition } from "./w.ts";
export function createXToolDefinition(cwd: string) { return { name: "x" }; }
export function createXTool(cwd: string) { return wrapToolDefinition(createXToolDefinition(cwd)); }`,
		}, false},
		{"a factory declared twice", map[string]string{
			"a.ts": `export function createXTool(cwd: string) { return { name: "a" }; }`,
			"b.ts": `export function createXTool(cwd: string) { return { name: "b" }; }`,
		}, false},
		{"a file whose brackets the scanner cannot balance", map[string]string{
			"x.ts": "export function createXTool(cwd: string) { return { name: \"a\" }; }\nconst broken = {{;",
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := writeFactorySources(t, tc.files)
			factory := "createFindTool"
			switch {
			case tc.files["edit.ts"] != "":
				factory = "createEditTool"
			case tc.files["bash.ts"] != "":
				factory = "createBashTool"
			case tc.files["a.ts"] != "" || tc.files["x.ts"] != "":
				factory = "createXTool"
			}
			if got := r.factoryNeverSets(factory, "prepareArguments"); got != tc.want {
				t.Fatalf("factoryNeverSets(%s, prepareArguments) = %v, want %v", factory, got, tc.want)
			}
		})
	}
}

// TestNeverSetsClosesOnlyAnOptionalFunctionMember: T12u applies to an optional member of function type, and only when the source
// proof says the factory's value never has it. A required member, an optional data member and a factory that may set it stay gaps.
func TestNeverSetsClosesOnlyAnOptionalFunctionMember(t *testing.T) {
	gt := goTypes(t, `
type Tool struct{ Name string }
var T Tool`)
	for _, tc := range []struct {
		name, up  string
		neverSets func(string) bool
		want      tri
	}{
		{"the factory never sets the optional hook", "{ name: string; hook?: (args: unknown) => string }", func(m string) bool { return m == "hook" }, yes},
		{"the factory may set the optional hook", "{ name: string; hook?: (args: unknown) => string }", func(string) bool { return false }, no},
		{"no source proof is available", "{ name: string; hook?: (args: unknown) => string }", nil, no},
		{"the hook is required", "{ name: string; hook: (args: unknown) => string }", func(string) bool { return true }, no},
		{"a required method", "{ name: string; hook(args: unknown): string }", func(string) bool { return true }, no},
		{"an optional data member", "{ name: string; count?: number }", func(string) bool { return true }, no},
		{"another optional hook the proof does not cover", "{ name: string; hook?: (a: string) => void; other?: (a: string) => void }", func(m string) bool { return m == "hook" }, no},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", neverSets: tc.neverSets}
			if got := c.agree(tc.up, gt["T"]); got.ok != tc.want {
				t.Fatalf("agree(%q) = %v (%s), want %v", tc.up, got.ok, got.why, tc.want)
			}
		})
	}
}
