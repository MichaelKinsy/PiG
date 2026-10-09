package experimental

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// packages/chord/src/node/bundle.ts:26-30,98-125: minify, define, platform and target reach esbuild; platform defaults to node and the
// target to node22.19 (es2022 off node). Each case builds one entry and checks the observable effect on the CommonJS bundle.
func TestBundleFacetsPassesMinifyDefinePlatformAndTargetToTheCompiler(t *testing.T) {
	isolateExperimentalTest(t)
	const source = "const readFile = require(\"fs\").readFileSync;\nconst describeValue = (valueName, fallbackValue) => {\n\treturn valueName ?? fallbackValue;\n};\nmodule.exports = { version: __FACET_VERSION__, describeValue, readFile };\n"
	build := func(t *testing.T, options BundleFacetsOptions) (BundleFacetsResult, string, error) {
		t.Helper()
		directory := t.TempDir()
		entry := filepath.Join(directory, "entry.js")
		if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		options.Plugin = FacetBundlePlugin{Id: "options-test"}
		options.Entries = []FacetEntrySource{{Name: "main", Source: entry}}
		options.Outdir = filepath.Join(directory, "out")
		result, err := BundleFacets(t.Context(), options)
		if err != nil {
			return result, "", err
		}
		contents, readErr := os.ReadFile(filepath.Join(options.Outdir, result.Manifest.Entries["main"].File))
		if readErr != nil {
			t.Fatal(readErr)
		}
		return result, string(contents), nil
	}
	define := map[string]string{"__FACET_VERSION__": `"1.2.3"`}
	_, plain, err := build(t, BundleFacetsOptions{Define: define})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		options BundleFacetsOptions
		check   func(t *testing.T, result BundleFacetsResult, text string)
	}{
		{"define replaces the identifier", BundleFacetsOptions{Define: define}, func(t *testing.T, _ BundleFacetsResult, text string) {
			if !strings.Contains(text, `"1.2.3"`) || strings.Contains(text, "__FACET_VERSION__") {
				t.Fatalf("define not applied:\n%s", text)
			}
		}},
		{"minify is off by default and shortens when set", BundleFacetsOptions{Define: define, Minify: true}, func(t *testing.T, _ BundleFacetsResult, text string) {
			if !strings.Contains(plain, "valueName") || !strings.Contains(plain, "\n  return") {
				t.Fatalf("the default build is minified:\n%s", plain)
			}
			if strings.Contains(text, "valueName") || strings.Contains(text, "\n  return") || len(text) >= len(plain) {
				t.Fatalf("minify not applied (%d bytes against %d):\n%s", len(text), len(plain), text)
			}
		}},
		{"node is the default platform: builtins stay external", BundleFacetsOptions{Define: define}, func(t *testing.T, result BundleFacetsResult, _ string) {
			if !slices.Contains(result.Manifest.Entries["main"].ExternalImports, "fs") {
				t.Fatalf("external imports = %v, want fs", result.Manifest.Entries["main"].ExternalImports)
			}
		}},
		{"the default node target keeps ??", BundleFacetsOptions{Define: define}, func(t *testing.T, _ BundleFacetsResult, text string) {
			if !strings.Contains(text, "??") {
				t.Fatalf("node22.19 lowered ??:\n%s", text)
			}
		}},
		{"an es2019 target lowers ??", BundleFacetsOptions{Define: define, Target: []string{"es2019"}}, func(t *testing.T, _ BundleFacetsResult, text string) {
			if strings.Contains(text, "??") {
				t.Fatalf("es2019 kept ??:\n%s", text)
			}
		}},
		{"an engine target lowers what it lacks", BundleFacetsOptions{Define: define, Target: []string{"node12"}}, func(t *testing.T, _ BundleFacetsResult, text string) {
			if strings.Contains(text, "??") {
				t.Fatalf("node12 kept ??:\n%s", text)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, text, err := build(t, c.options)
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, result, text)
		})
	}
	t.Run("the browser platform cannot resolve a node builtin", func(t *testing.T) {
		if _, _, err := build(t, BundleFacetsOptions{Define: define, Platform: FacetBundlePlatformBrowser}); err == nil || !strings.Contains(err.Error(), "Could not bundle facet entry main") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an invalid target or platform is a bundle error", func(t *testing.T) {
		for _, options := range []BundleFacetsOptions{{Target: []string{"nonsense"}}, {Target: []string{"node"}}, {Platform: "worker"}} {
			if _, _, err := build(t, options); err == nil || !strings.Contains(err.Error(), "Could not bundle facet entry main") {
				t.Fatalf("%+v: err = %v", options, err)
			}
		}
	})
}
