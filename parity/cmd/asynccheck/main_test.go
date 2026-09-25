package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestDiscoverAsyncSources(t *testing.T) {
	root := asyncSourceFixture(t)
	writeAsyncTestFile(t, root, "packages/ai/src/awaited.ts", "export async function load() {}")
	writeAsyncTestFile(t, root, "packages/agent/src/promise.ts", "type Loader = () => Promise<void>;")
	writeAsyncTestFile(t, root, "packages/tui/src/sync.ts", "export function render() {}")
	writeAsyncTestFile(t, root, "packages/coding-agent/src/ignored.test.ts", "async function test() {}")

	got, err := discoverAsyncSources(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"packages/agent/src/promise.ts", "packages/ai/src/awaited.ts"}
	if !slices.Equal(got, want) {
		t.Fatalf("discoverAsyncSources() = %v, want %v", got, want)
	}
}

func TestDiscoverAsyncSourcesRejectsMisleadingMirror(t *testing.T) {
	for _, pkg := range []string{"agent", "ai", "coding-agent", "tui"} {
		t.Run(pkg, func(t *testing.T) {
			for _, test := range []struct {
				name  string
				field string
				value any
			}{
				{name: "wrong-name", field: "name", value: "@example/not-pi"},
				{name: "other-tracked-name", field: "name", value: "@earendil-works/pi-" + map[string]string{"agent": "ai", "ai": "tui", "coding-agent": "agent-core", "tui": "coding-agent"}[pkg]},
				{name: "missing-name", field: "name"},
				{name: "version-prefix", field: "version", value: "v" + coding.UpstreamVersion},
				{name: "version-range", field: "version", value: "^" + coding.UpstreamVersion},
				{name: "version-prerelease", field: "version", value: coding.UpstreamVersion + "-rc.1"},
				{name: "version-build", field: "version", value: coding.UpstreamVersion + "+local"},
				{name: "version-whitespace", field: "version", value: coding.UpstreamVersion + " "},
				{name: "missing-version", field: "version"},
				{name: "numeric-version", field: "version", value: 1},
			} {
				t.Run(test.name, func(t *testing.T) {
					root := asyncSourceFixture(t)
					manifestPath := filepath.Join(root, "packages", pkg, "package.json")
					body, err := os.ReadFile(manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					var manifest map[string]any
					if err := json.Unmarshal(body, &manifest); err != nil {
						t.Fatal(err)
					}
					manifest[test.field] = test.value
					body, err = json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					writeAsyncTestFile(t, root, "packages/"+pkg+"/package.json", string(body))
					if _, err := discoverAsyncSources(root); err == nil || !strings.Contains(err.Error(), manifestPath) {
						t.Fatalf("discoverAsyncSources() error = %v, want rejection naming %s", err, manifestPath)
					}
				})
			}
		})
	}
}

func asyncSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for pkg, name := range map[string]string{
		"agent":        "@earendil-works/pi-agent-core",
		"ai":           "@earendil-works/pi-ai",
		"coding-agent": "@earendil-works/pi-coding-agent",
		"tui":          "@earendil-works/pi-tui",
	} {
		body, err := json.Marshal(map[string]string{"name": name, "version": coding.UpstreamVersion})
		if err != nil {
			t.Fatal(err)
		}
		writeAsyncTestFile(t, root, "packages/"+pkg+"/package.json", string(body))
		writeAsyncTestFile(t, root, "packages/"+pkg+"/src/sync.ts", "export function sync() {}")
	}
	return root
}

func TestAC35RejectsMissingAndPendingAsyncContracts(t *testing.T) {
	paths := []string{"packages/ai/src/added.ts", "packages/ai/src/existing.ts"}
	manifest := asyncManifest{
		Version: coding.UpstreamVersion,
		Files: []asyncAudit{{
			Path:        "packages/ai/src/added.ts",
			Disposition: "pending",
		}},
	}
	problems := strings.Join(validateManifest(manifest, paths, t.TempDir()), "\n")
	for _, want := range []string{"packages/ai/src/added.ts remains pending", "missing file audit packages/ai/src/existing.ts"} {
		if !strings.Contains(problems, want) {
			t.Fatalf("problems %q do not contain %q", problems, want)
		}
	}
}

func TestValidateManifestAcceptsTranslatedAndExplicitNonRuntimeAsync(t *testing.T) {
	repo := t.TempDir()
	writeAsyncTestFile(t, repo, "ai/async_test.go", "package ai")
	paths := []string{"packages/ai/src/runtime.ts", "packages/ai/src/types.ts"}
	manifest := asyncManifest{
		Version: coding.UpstreamVersion,
		Files: []asyncAudit{
			{
				Path:        "packages/ai/src/runtime.ts",
				Disposition: "translated",
				Contracts:   []string{"awaited", "cancellable"},
				Evidence:    []string{"test:ai/async_test.go#TestAwaited"},
			},
			{
				Path:        "packages/ai/src/types.ts",
				Disposition: "no-runtime-async",
				Rationale:   "Promise appears only in a TypeScript declaration",
			},
		},
	}
	if problems := validateManifest(manifest, paths, repo); len(problems) != 0 {
		t.Fatalf("validateManifest() problems = %v", problems)
	}
}

func writeAsyncTestFile(t *testing.T, root, relativePath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
