package experimental

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type facetOptionsProbe struct {
	Name     string            `json:"name"`
	Source   string            `json:"source"`
	Minify   *bool             `json:"minify,omitempty"`
	Define   map[string]string `json:"define,omitempty"`
	Platform string            `json:"platform,omitempty"`
	Target   any               `json:"target,omitempty"`
}

// upstream: packages/chord/src/node/bundle.ts:15-31 and 87-125. minify, define, platform and target reach esbuild; the default
// is platform node and target node22.19, es2022 for another platform; an invalid platform or target fails the entry with no
// diagnostics. Each probe is built by Pi's installed bundleFacets and by Pig, and the bundle text or error must be identical.
func TestBundleFacetsOptionsMatchPi(t *testing.T) {
	const source = "module.exports = { f: (a) => a?.b ?? process.env.FLAG, c: class { x = 1; #y = 2 }, g: async () => { for await (const v of []) {} } };\n"
	truthy := true
	probes := []facetOptionsProbe{
		{Name: "defaults", Source: source},
		{Name: "minify", Source: source, Minify: &truthy},
		{Name: "define", Source: source, Define: map[string]string{"process.env.FLAG": `"on"`}},
		{Name: "browser", Source: source, Platform: "browser"},
		{Name: "neutral", Source: source, Platform: "neutral"},
		{Name: "invalid platform", Source: source, Platform: "bogus"},
		{Name: "es2015", Source: source, Target: "es2015"},
		{Name: "es2019 list", Source: source, Target: []string{"es2019"}},
		{Name: "engine", Source: source, Target: "chrome70"},
		{Name: "es and engine", Source: source, Target: []string{"es2022", "node12"}},
		{Name: "invalid target", Source: source, Target: "bogus"},
		{Name: "engine without version", Source: source, Target: "node"},
		{Name: "node builtin on the browser platform", Source: `module.exports = require("fs");` + "\n", Platform: "browser"},
		{Name: "node builtin on node", Source: `module.exports = require("fs");` + "\n"},
		{Name: "browser default target", Source: source, Platform: "browser", Minify: &truthy},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/facet_bundle_options.mjs", pigversion.UpstreamVersion)
	cmd.Env = append(os.Environ(), "PIG_TEST_ROOT="+root)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for index, probe := range probes {
		t.Run(probe.Name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "entry.js"), []byte(probe.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			options := BundleFacetsOptions{
				Plugin: FacetBundlePlugin{Id: "p"}, Entries: []FacetEntrySource{{Name: "e", Source: "entry.js"}},
				Outdir: "out", WorkingDirectory: &directory, Define: probe.Define, Platform: FacetBundlePlatform(probe.Platform),
			}
			if probe.Minify != nil {
				options.Minify = *probe.Minify
			}
			switch target := probe.Target.(type) {
			case string:
				options.Target = []string{target}
			case []string:
				options.Target = target
			}
			result, err := BundleFacets(t.Context(), options)
			if !expected[index].OK {
				// The diagnostic lines after the first are formatted by each compiler front end.
				if err == nil || firstLine(err.Error()) != firstLine(expected[index].Error) {
					t.Fatalf("error = %v, want %q", err, expected[index].Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pi built the entry, Pig failed: %v", err)
			}
			code, err := os.ReadFile(filepath.Join(directory, "out", result.Manifest.Entries["e"].File))
			if err != nil {
				t.Fatal(err)
			}
			if string(code) != expected[index].Code {
				t.Fatalf("bundle differs from Pi:\nPig:\n%s\nPi:\n%s", code, expected[index].Code)
			}
		})
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
