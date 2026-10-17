package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestConformance_LanguageFromPathAgreesAcrossSDKs: Pi exports getLanguageFromPath (coding-agent index.ts:474, modes/interactive/theme/theme.ts:1028) for extension tools that
// highlight code. The Node SDK runs Pi's own code, so its answer is the reference: every path below runs in each SDK and must equal it.
func TestConformance_LanguageFromPathAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	var paths []string
	for _, extension := range []string{"ts", "tsx", "js", "jsx", "mjs", "cjs", "py", "rb", "rs", "go", "java", "kt", "swift", "c", "h", "cpp", "cc", "cxx", "hpp", "cs", "php", "sh", "bash", "zsh", "fish", "ps1", "sql", "html", "htm", "css", "scss", "sass", "less", "json", "yaml", "yml", "toml", "xml", "md", "markdown", "dockerfile", "makefile", "cmake", "lua", "perl", "r", "scala", "clj", "ex", "exs", "erl", "hs", "ml", "vim", "graphql", "proto", "tf", "hcl"} {
		paths = append(paths, "file."+extension, "dir/FILE."+strings.ToUpper(extension), "a.b."+strings.ToUpper(extension[:1])+extension[1:], extension)
	}
	paths = append(paths, "", ".", "x.", ".ts", "file", "Dockerfile", "Makefile", "dir.d/Makefile", "a.b/c.ts", "a.ts/b", "noext", "x.unknown", "x.TSX", "x. ts", "x.ts ", "x.t\u015f", "x.\u0130", "x.\u212a", "x.v\u0130m", "x.\u212at", "日本語.py", "a\\b.go", "x.constructorless", "x..ts", "...", "x.tS")
	want := map[string]string{"file.ts": "typescript", "dir/FILE.PY": "python", "Dockerfile": "dockerfile", "dir.d/Makefile": "", "x.tf": "hcl", "a.b/c.ts": "typescript", "x.": "", "x.ts ": ""} //nolint:gocritic // mapKey: the trailing space is the case: "ts " is not ts, so Pi has no language for it

	run := func(t *testing.T, h *harness, path string) any {
		t.Helper()
		args, err := json.Marshal(map[string]any{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "language_probe", string(args)) {
			t.Fatal("fixture has no language_probe command")
		}
		var reported string
		pollUntilConformance(t, "language_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "language:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got["lang"]
	}

	var reference []any
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		for _, path := range paths {
			reference = append(reference, run(t, h, path))
		}
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) != len(paths) {
		t.Fatalf("the Node reference ran %d of %d paths", len(reference), len(paths))
	}
	// Values Pi's table implies, so a mistake shared by every SDK cannot pass.
	for i, path := range paths {
		if language, pinned := want[path]; pinned {
			var expected any = language
			if language == "" {
				expected = nil
			}
			if !reflect.DeepEqual(reference[i], expected) {
				t.Fatalf("reference getLanguageFromPath(%q) = %v, want %v", path, reference[i], expected)
			}
		}
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for i, path := range paths {
				if got := run(t, h, path); !reflect.DeepEqual(got, reference[i]) {
					t.Errorf("getLanguageFromPath(%q) = %v, Node (Pi) reports %v", path, got, reference[i])
				}
			}
		})
	}
}
