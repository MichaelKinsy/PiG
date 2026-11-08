package surfaces

// pi: packages/chord/src/node/surfaces/bundle.ts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Tests of base bundles' resolution (p3 step 0a): packages resolve with the given conditions (a source checkout's source,
// not its `dist/`), by default the server process's (`--conditions`/`-C` of its flags and of a quoted `NODE_OPTIONS`), a
// browser build keeping its `module` condition and `browser` field, and every file read recorded with its bytes' hash. Ports
// packages/chord/test/node/surfaces/bundle.test.ts.

// project is a directory with files and the workspace packages under `workspace/` linked into its `node_modules`: its real path.
func project(t *testing.T, files map[string]string, linked ...string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range linked {
		if err := os.Symlink(filepath.Join(dir, "workspace", name), filepath.Join(dir, "node_modules", name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// chordish is a workspace package `chordish` exporting its source under the `source` condition and its build otherwise.
func chordish() map[string]string {
	return map[string]string{
		// Written as text: the order of an `exports` object's conditions matters.
		"workspace/chordish/package.json":  `{"name":"chordish","type":"module","exports":{".":{"source":"./src/index.ts","default":"./dist/index.js"}}}`,
		"workspace/chordish/src/index.ts":  "export const from: string = \"source\";\n",
		"workspace/chordish/dist/index.js": "export const from = \"dist\";\n",
		"main.ts":                          "import { from } from \"chordish\";\nconsole.log(from);\n",
	}
}

// scriptOf is the bundle's script text.
func scriptOf(bundle *BaseBundle) string { return string(bundle.Files[bundle.Script].Bytes) }

func sortedKeys(inputs plugins.BaseInputs) []string {
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestConditionsResolvesWithTheGivenConditionsTheSourceAsInputNotDistEachWithItsBytesHash(t *testing.T) {
	dir := project(t, chordish(), "chordish")
	for _, platform := range []BasePlatform{PlatformBrowser, PlatformSandbox} {
		bundle, err := BuildBase(filepath.Join(dir, "main.ts"), platform, "/p/", []string{"source"})
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(dir, "workspace", "chordish", "src", "index.ts")
		want := []string{filepath.Join(dir, "main.ts"), source}
		slices.Sort(want)
		if got := sortedKeys(bundle.Inputs); !slices.Equal(got, want) {
			t.Fatalf("%s: inputs = %v, want %v", platform, got, want)
		}
		bytes, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if got := bundle.Inputs[source]; got != plugins.ContentHash(bytes) {
			t.Fatalf("%s: hash = %s, want %s", platform, got, plugins.ContentHash(bytes))
		}
		if !strings.Contains(scriptOf(bundle), `"source"`) {
			t.Fatalf("%s: the script does not contain the source: %s", platform, scriptOf(bundle))
		}
	}
	plain, err := BuildBase(filepath.Join(dir, "main.ts"), PlatformBrowser, "/p/", []string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.Inputs[filepath.Join(dir, "workspace", "chordish", "dist", "index.js")]; !ok {
		t.Fatalf("without conditions the build did not read dist: %v", sortedKeys(plain.Inputs))
	}
}

func TestConditionsTakesTheServerProcessesConditionsByDefaultItsFlagsAndAQuotedNODEOPTIONS(t *testing.T) {
	got := ProcessConditions(
		[]string{"--no-warnings", "--conditions=source", "-C", "edge"},
		`--max-old-space-size=100 --conditions="dev ish" -C "a\"b" -C=c`,
	)
	if want := []string{"dev ish", `a"b`, "c", "source", "edge"}; !slices.Equal(got, want) {
		t.Fatalf("conditions = %q, want %q", got, want)
	}
	if got := ProcessConditions(nil, ""); len(got) != 0 {
		t.Fatalf("conditions = %q, want none", got)
	}
	dir := project(t, chordish(), "chordish")
	t.Setenv("NODE_OPTIONS", `--conditions "source"`)
	bundle, err := BuildBase(filepath.Join(dir, "main.ts"), PlatformBrowser, "/p/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scriptOf(bundle), `"source"`) {
		t.Fatalf("the script does not contain the source: %s", scriptOf(bundle))
	}
}

func TestConditionsKeepsABrowserBuildsModuleConditionAndBrowserFieldWithCustomConditions(t *testing.T) {
	dir := project(t, map[string]string{
		"workspace/dual/package.json":    `{"name":"dual","exports":{".":{"module":"./esm.js","default":"./cjs.js"}}}`,
		"workspace/dual/esm.js":          "export const dual = \"module\";\n",
		"workspace/dual/cjs.js":          "exports.dual = \"default\";\n",
		"workspace/fielded/package.json": `{"name":"fielded","main":"./node.js","browser":{"./node.js":"./browser.js"}}`,
		"workspace/fielded/node.js":      "exports.fielded = \"node\";\n",
		"workspace/fielded/browser.js":   "exports.fielded = \"browser\";\n",
		"main.ts":                        "import { dual } from \"dual\";\nimport { fielded } from \"fielded\";\nconsole.log(dual, fielded);\n",
	}, "dual", "fielded")
	bundle, err := BuildBase(filepath.Join(dir, "main.ts"), PlatformBrowser, "/p/", []string{"source"})
	if err != nil {
		t.Fatal(err)
	}
	script := scriptOf(bundle)
	if !strings.Contains(script, `"module"`) || !strings.Contains(script, `"browser"`) || strings.Contains(script, `"node"`) {
		t.Fatalf("script = %s", script)
	}
}
