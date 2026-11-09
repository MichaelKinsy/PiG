package plugins

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi's own cases for the TypeScript the Node host runs. chord/src/contracts/snapshot.ts and chord/src/surfaces/language.ts are
// vendored verbatim in the Node host's tree (node/pi), so a plugin written in TypeScript meets exactly their behaviour; Go
// does not reimplement them there. The cases below are Pi's, over the extracted tree the subprocess runs (the way
// TestRuntimeLoadsNoEsbuild... runs Pi's script), so they guard that tree and not a Go port of it.

// runVendored runs script (an ES module) on Node with the extracted tree's pi-schema in place of the package of that name.
func runVendored(t *testing.T, script func(pi func(path string) string) string) string {
	t.Helper()
	tree, err := extractNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pi := func(path string) string {
		return pcJSON(t, fileURL(filepath.Join(tree, "pi", "chord", "src", filepath.FromSlash(path))))
	}
	body := strings.Join([]string{
		`import { registerHooks } from "node:module";`,
		`import assert from "node:assert/strict";`,
		"registerHooks({ resolve(specifier, context, next) {",
		fmt.Sprintf("  if (specifier === \"@earendil-works/pi-schema\") return { url: %s, shortCircuit: true };", pcJSON(t, "file://"+filepath.ToSlash(filepath.Join(tree, "pi", "schema", "src", "index.ts")))),
		"  return next(specifier, context);",
		"} });",
		script(pi),
		`console.log("ok");`,
	}, "\n")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on the PATH")
	}
	output, err := exec.Command(node, "--experimental-strip-types", "--no-warnings", "--input-type=module", "--eval", body).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "ok" {
		t.Fatalf("output = %q, want ok", got)
	}
	return string(output)
}

// snapshot.test.ts › vocabulary › "refuses symbol keys, accessors and cycles, naming the path". Intention: a schema a
// plugin declares in JavaScript is read once into a plain copy, and what cannot be copied (a Symbol key, a getter, a cycle,
// a non-vocabulary type) is refused naming where it sits, so a plugin never registers a contract that changes after the fact.
// A Go schema is JSON data and has neither Symbol keys nor getters, so only a TypeScript plugin can hit this path.
func TestNodeHostSnapshotSchemaRefusesSymbolKeysAccessorsAndCyclesNamingThePath(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return strings.Join([]string{
			fmt.Sprintf(`const { snapshotSchema } = await import(%s);`, pi("contracts/snapshot.ts")),
			// vitest's toThrow(string) is a substring match.
			`const refuses = (fn, text) => assert.throws(fn, (error) => error instanceof Error && error.message.includes(text));`,
			`refuses(() => snapshotSchema({ type: "string", [Symbol("x")]: 1 }, "s"), "s: symbol keys are not supported");`,
			`const accessor = Object.defineProperty({ type: "object" }, "properties", { get: () => ({}), enumerable: true });`,
			`refuses(() => snapshotSchema(accessor, "s"), "s.properties: accessors are not supported");`,
			`const cyclic = { type: "array" };`,
			`cyclic.items = cyclic;`,
			`refuses(() => snapshotSchema(cyclic, "s"), "s.items: cyclic schema");`,
			`refuses(() => snapshotSchema({ type: "object", properties: { a: { type: 1 } } }, "s"), "s.properties.a.type: unsupported type 1");`,
		}, "\n")
	})
}

// language.test.ts › the render scope › "is current only while a render runs, and a nested render restores the outer", on the
// vendored language.ts (the Go half is TestANestedRenderInAnotherLanguageLeavesTheOuterRenderInItsOwn... in chord/next/surfaces).
func TestNodeHostRenderScopeIsCurrentOnlyWhileARenderRunsAndANestedRenderRestoresTheOuter(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return strings.Join([]string{
			fmt.Sprintf(`const { createLanguage, currentLanguage, inLanguage } = await import(%s);`, pi("surfaces/language.ts")),
			fmt.Sprintf(`const { NO_TRANSLATIONS } = await import(%s);`, pi("surfaces/translations.ts")),
			`const language = (locale) => createLanguage(locale, NO_TRANSLATIONS, () => {});`,
			`const de = language("de");`,
			`const fr = language("fr");`,
			`assert.equal(currentLanguage(), undefined);`,
			`const seen = inLanguage(de, () => [currentLanguage(), inLanguage(fr, () => currentLanguage()), currentLanguage()]);`,
			`assert.deepEqual(seen, [de, fr, de]);`,
			`assert.equal(currentLanguage(), undefined);`,
			`assert.throws(() => inLanguage(de, () => { throw new Error("render failed"); }), { message: "render failed" });`,
			`assert.equal(currentLanguage(), undefined);`,
		}, "\n")
	})
}
