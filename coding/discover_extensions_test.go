package coding

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func writeExtensionTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const discoverCommandExtension = `export default function(pi) { pi.registerCommand("test", { handler: async () => {} }); }`

// upstream: packages/coding-agent/src/core/extensions/loader.ts:828-875 discoverAndLoadExtensions: project-local <cwd>/<config dir>/extensions, then <agentDir>/extensions, then the configured paths, deduplicated by absolute path; discoverExtensionsInDir (:760-797) and resolveExtensionEntries (:732-758).
func TestDiscoverExtensionPathsFollowsPisLoaderOrderAndRules(t *testing.T) {
	cwd, agentDir, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	writeExtensionTree(t, cwd, map[string]string{
		icodingagent.ConfigDirName() + "/extensions/local.ts": discoverCommandExtension,
	})
	writeExtensionTree(t, agentDir, map[string]string{
		"extensions/global.js":                  discoverCommandExtension,
		"extensions/dir/index.ts":               discoverCommandExtension,
		"extensions/dir/index.js":               discoverCommandExtension,
		"extensions/pkg/custom.ts":              discoverCommandExtension,
		"extensions/pkg/index.ts":               discoverCommandExtension,
		"extensions/pkg/package.json":           `{"name":"pkg","pi":{"extensions":["./custom.ts","./missing.ts"]}}`,
		"extensions/readme.md":                  "not an extension",
		"extensions/nested/deep/index.ts":       discoverCommandExtension,
		"extensions/notindexed/helper.ts":       discoverCommandExtension,
		"extensions/emptymanifest/index.js":     discoverCommandExtension,
		"extensions/emptymanifest/package.json": `{"name":"m","pi":{"extensions":[]}}`,
	})
	writeExtensionTree(t, elsewhere, map[string]string{
		"single.ts":          discoverCommandExtension,
		"bundle/index.ts":    discoverCommandExtension,
		"bundle/other.ts":    discoverCommandExtension,
		"loose/a.ts":         discoverCommandExtension,
		"loose/b.js":         discoverCommandExtension,
		"loose/sub/index.ts": discoverCommandExtension,
	})
	configured := []string{
		filepath.Join(elsewhere, "single.ts"),
		filepath.Join(elsewhere, "bundle"),                 // a directory with an index: its entry only
		filepath.Join(elsewhere, "loose"),                  // a directory without entries: what discovery finds in it
		filepath.Join(agentDir, "extensions", "global.js"), // already discovered: not added again
		filepath.Join(elsewhere, "absent.ts"),              // a path that does not exist is kept for the loader to report
	}
	got, resolvedCwd, err := discoverExtensionPaths(configured, cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedCwd != cwd {
		t.Fatalf("resolved cwd = %q, want %q", resolvedCwd, cwd)
	}
	rel := func(root, name string) string { return filepath.Join(root, filepath.FromSlash(name)) }
	// os.ReadDir lists a directory in file-name order.
	want := []string{
		rel(cwd, icodingagent.ConfigDirName()+"/extensions/local.ts"),
		rel(agentDir, "extensions/dir/index.ts"),
		rel(agentDir, "extensions/emptymanifest/index.js"),
		rel(agentDir, "extensions/global.js"),
		rel(agentDir, "extensions/pkg/custom.ts"),
		rel(elsewhere, "single.ts"),
		rel(elsewhere, "bundle/index.ts"),
		rel(elsewhere, "loose/a.ts"),
		rel(elsewhere, "loose/b.js"),
		rel(elsewhere, "loose/sub/index.ts"),
		rel(elsewhere, "absent.ts"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("discovered paths:\n got %q\nwant %q", got, want)
	}
}

// upstream: packages/coding-agent/test/extensions-discovery.test.ts:43 'discovers direct .ts files in extensions/' (discoverAndLoadExtensions([], tempDir, tempDir)), and loader.ts:696-703 a failing path is an entry of errors, not a failure of the call.
func TestDiscoverAndLoadExtensionsLoadsWhatItDiscoversAndReportsFailures(t *testing.T) {
	dir := t.TempDir()
	writeExtensionTree(t, dir, map[string]string{
		"extensions/foo.ts": discoverCommandExtension,
		"extensions/bar.ts": discoverCommandExtension,
	})
	missing := filepath.Join(dir, "missing.ts")
	load, err := DiscoverAndLoadExtensions(t.Context(), []string{missing}, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer load.Close()
	var loaded []string
	for _, e := range load.Extensions {
		loaded = append(loaded, filepath.Base(e.Path))
	}
	slices.Sort(loaded)
	if !slices.Equal(loaded, []string{"bar.ts", "foo.ts"}) {
		t.Fatalf("loaded extensions = %q, want bar.ts and foo.ts", loaded)
	}
	if len(load.Errors) != 1 || load.Errors[0].Path != missing {
		t.Fatalf("errors = %+v, want one entry for %s", load.Errors, missing)
	}
	if load.Runtime == nil {
		t.Fatal("the result carries no runtime")
	}
}

// upstream: packages/coding-agent/src/core/extensions/loader.ts:181-221,695-715: loadExtensionsInternal creates one runtime, every loaded extension's pi.registerProvider queues on runtime.pendingProviderRegistrations until a runner binds, and the result's runtime is that runtime (agent-session-services.ts:157-193 drains it). A fresh runtime would report no registration.
func TestDiscoverAndLoadExtensionsReturnsTheRuntimeTheExtensionsShare(t *testing.T) {
	dir := t.TempDir()
	writeExtensionTree(t, dir, map[string]string{
		"extensions/provider.ts": `export default function(pi) { pi.registerProvider("discovered-provider", { api: "openai-completions", baseUrl: "https://discovered.invalid/v1", apiKey: "discovered-key", models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }] }); }`,
	})
	load, err := DiscoverAndLoadExtensions(t.Context(), nil, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer load.Close()
	if len(load.Errors) != 0 {
		t.Fatalf("errors = %+v", load.Errors)
	}
	var names []string
	for _, pending := range load.Runtime.PendingProviderRegistrations() {
		names = append(names, pending.Name)
	}
	if !slices.Equal(names, []string{"discovered-provider"}) {
		t.Fatalf("pending provider registrations of the result's runtime = %q, want the one the loaded extension made", names)
	}
}

// The discovery cases of Pi's extensions-discovery.test.ts, driven through the discovery half of DiscoverAndLoadExtensions with Pi's call shape discoverAndLoadExtensions(configured, tempDir, tempDir). Pi asserts the loaded paths; the Host's load of the same layouts is TestUpstreamExtensionsDiscovery (coding/extension/host/subprocess).
func TestDiscoverExtensionPathsMatchesPisDiscoveryCases(t *testing.T) {
	const ext = discoverCommandExtension
	for _, tc := range []struct {
		name       string
		files      map[string]string
		configured []string
		want       []string
	}{
		// upstream: packages/coding-agent/test/extensions-discovery.test.ts:43
		{name: "discovers direct .ts files in extensions/", files: map[string]string{"extensions/foo.ts": ext, "extensions/bar.ts": ext}, want: []string{"extensions/bar.ts", "extensions/foo.ts"}},
		// upstream: extensions-discovery.test.ts:125
		{name: "discovers direct .js files in extensions/", files: map[string]string{"extensions/foo.js": ext}, want: []string{"extensions/foo.js"}},
		// upstream: extensions-discovery.test.ts:135
		{name: "discovers subdirectory with index.ts", files: map[string]string{"extensions/my-extension/index.ts": ext}, want: []string{"extensions/my-extension/index.ts"}},
		// upstream: extensions-discovery.test.ts:148
		{name: "discovers subdirectory with index.js", files: map[string]string{"extensions/my-extension/index.js": ext}, want: []string{"extensions/my-extension/index.js"}},
		// upstream: extensions-discovery.test.ts:160
		{name: "prefers index.ts over index.js", files: map[string]string{"extensions/my-extension/index.ts": ext, "extensions/my-extension/index.js": ext}, want: []string{"extensions/my-extension/index.ts"}},
		// upstream: extensions-discovery.test.ts:173
		{name: "discovers subdirectory with package.json pi field", files: map[string]string{"extensions/my-package/src/main.ts": ext, "extensions/my-package/package.json": `{"name":"my-package","pi":{"extensions":["./src/main.ts"]}}`}, want: []string{"extensions/my-package/src/main.ts"}},
		// upstream: extensions-discovery.test.ts:197
		{name: "keeps package.json pi extension entries with leading tilde package-relative", files: map[string]string{"extensions/tilde-package/~entry.ts": ext, "extensions/tilde-package/~/entry.ts": ext, "extensions/tilde-package/package.json": `{"name":"tilde-package","pi":{"extensions":["~entry.ts","~/entry.ts"]}}`}, want: []string{"extensions/tilde-package/~/entry.ts", "extensions/tilde-package/~entry.ts"}},
		// upstream: extensions-discovery.test.ts:222
		{name: "package.json can declare multiple extensions", files: map[string]string{"extensions/my-package/ext1.ts": ext, "extensions/my-package/ext2.ts": ext, "extensions/my-package/package.json": `{"name":"my-package","pi":{"extensions":["./ext1.ts","./ext2.ts"]}}`}, want: []string{"extensions/my-package/ext1.ts", "extensions/my-package/ext2.ts"}},
		// upstream: extensions-discovery.test.ts:243
		{name: "package.json with pi field takes precedence over index.ts", files: map[string]string{"extensions/my-package/index.ts": ext, "extensions/my-package/custom.ts": ext, "extensions/my-package/package.json": `{"name":"my-package","pi":{"extensions":["./custom.ts"]}}`}, want: []string{"extensions/my-package/custom.ts"}},
		// upstream: extensions-discovery.test.ts:268
		{name: "ignores package.json without pi field, falls back to index.ts", files: map[string]string{"extensions/my-package/index.ts": ext, "extensions/my-package/package.json": `{"name":"my-package","version":"1.0.0"}`}, want: []string{"extensions/my-package/index.ts"}},
		// upstream: extensions-discovery.test.ts:287
		{name: "ignores subdirectory without index or package.json", files: map[string]string{"extensions/not-an-extension/helper.ts": ext, "extensions/not-an-extension/utils.ts": ext}},
		// upstream: extensions-discovery.test.ts:299
		{name: "does not recurse beyond one level", files: map[string]string{"extensions/container/nested/index.ts": ext}},
		// upstream: extensions-discovery.test.ts:313
		{name: "handles mixed direct files and subdirectories", files: map[string]string{"extensions/direct.ts": ext, "extensions/with-index/index.ts": ext, "extensions/with-manifest/entry.ts": ext, "extensions/with-manifest/package.json": `{"pi":{"extensions":["./entry.ts"]}}`}, want: []string{"extensions/direct.ts", "extensions/with-index/index.ts", "extensions/with-manifest/entry.ts"}},
		// upstream: extensions-discovery.test.ts:334
		{name: "skips non-existent paths declared in package.json", files: map[string]string{"extensions/my-package/exists.ts": ext, "extensions/my-package/package.json": `{"pi":{"extensions":["./exists.ts","./missing.ts"]}}`}, want: []string{"extensions/my-package/exists.ts"}},
		// upstream: extensions-discovery.test.ts:384
		{name: "handles explicitly configured paths", files: map[string]string{"custom-location/my-ext.ts": ext}, configured: []string{"custom-location/my-ext.ts"}, want: []string{"custom-location/my-ext.ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(tempDir, "extensions"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeExtensionTree(t, tempDir, tc.files)
			var configured []string
			for _, p := range tc.configured {
				configured = append(configured, filepath.Join(tempDir, filepath.FromSlash(p)))
			}
			got, _, err := discoverExtensionPaths(configured, tempDir, tempDir)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, p := range tc.want {
				want = append(want, filepath.Join(tempDir, filepath.FromSlash(p)))
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("discovered paths:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// upstream: loader.ts:742-797 and pi-manifest.ts readPiManifest: the manifest loses its byte-order mark before parsing, a dangling symbolic link named *.ts is discovered by its directory entry type (its load then reports the error), and a configured path goes through resolvePath(p, cwd, { normalizeUnicodeSpaces: true }) (loader.ts:859).
func TestDiscoverExtensionPathsReadsManifestsLinksAndConfiguredPathsAsPi(t *testing.T) {
	tempDir := t.TempDir()
	writeExtensionTree(t, tempDir, map[string]string{
		"extensions/bom/package.json": "\ufeff" + `{"pi":{"extensions":["./main.ts"]}}`,
		"extensions/bom/main.ts":      discoverCommandExtension,
		"extensions/bom/index.ts":     discoverCommandExtension,
		"my ext.ts":                   discoverCommandExtension,
	})
	discover := func(t *testing.T, want []string) {
		t.Helper()
		got, _, err := discoverExtensionPaths([]string{filepath.Join(tempDir, "my\u00a0ext.ts")}, tempDir, tempDir)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("discovered paths:\n got %q\nwant %q", got, want)
		}
	}
	bom, configured := filepath.Join(tempDir, "extensions", "bom", "main.ts"), filepath.Join(tempDir, "my ext.ts")
	discover(t, []string{bom, configured})
	t.Run("a dangling link", func(t *testing.T) {
		testenv.Symlink(t, filepath.Join(tempDir, "missing-target.ts"), filepath.Join(tempDir, "extensions", "dangling.ts"))
		discover(t, []string{bom, filepath.Join(tempDir, "extensions", "dangling.ts"), configured})
	})
}

// loader.ts:828-833 discoverAndLoadExtensions(configuredPaths, cwd, agentDir = getAgentDir()): an omitted agent directory is the configured agent directory, whose extensions/ folder is the global location.
func TestDiscoverExtensionPathsDefaultsTheAgentDirectoryToTheConfiguredOne(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeExtensionTree(t, agentDir, map[string]string{"extensions/global.ts": discoverCommandExtension})
	got, _, err := discoverExtensionPaths(nil, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(agentDir, "extensions", "global.ts")}; !slices.Equal(got, want) {
		t.Errorf("discovered %v, want %v", got, want)
	}
}
