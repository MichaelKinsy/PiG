//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

func TestUpstreamExtensionsDiscovery(t *testing.T) {
	t.Parallel()
	const command = `export default function(pi) { pi.registerCommand("test", { handler: async () => {} }); }`
	tool := func(name string) string {
		return `import { Type } from "typebox"; export default function(pi) { pi.registerTool({name: "` + name + `", label: "` + name + `", description: "Test tool", parameters: Type.Object({}), execute: async () => ({content:[{type:"text",text:"ok"}]})}); }`
	}
	for _, tc := range []struct {
		name                                                                            string
		files                                                                           map[string]string
		explicit                                                                        []string
		direct, deps, markdown                                                          bool
		paths, tools, absentTools, commands, absentCommands, handlers, shortcuts, flags []string
		messageRenderers, entryRenderers                                                []string
		errorPath, errorText                                                            string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:43
		{name: "discovers direct .ts files in extensions/", files: map[string]string{"extensions/foo.ts": command, "extensions/bar.ts": command}, paths: []string{"extensions/bar.ts", "extensions/foo.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:54
		{name: "loads the coding-agent entrypoint without rewriting pi-ai provider subpaths", files: map[string]string{"extensions/coding-agent-import.ts": `import { getAgentDir } from "@earendil-works/pi-coding-agent"; void getAgentDir; ` + command}, paths: []string{"extensions/coding-agent-import.ts"}},
		// .upstream/v0.99.1/packages/coding-agent/test/extensions-discovery.test.ts:72-105 (regression for #9863). Two per-case differences:
		// (1) Pi's loader returns `warnings: []` for every load and only the resource loader adds package warnings (loader.ts:665,693;
		// resource-loader.ts:53-93), so the no-warning half is cmd/pig TestUpstreamResourceLoaderProjectManifestDoesNotOwnProjectExtension
		// and TestExtensionPackageWarningManifestShapes; this Host returns no warnings list.
		// (2) The test expects the physical node_modules copy to win, which only happens in Pi's unbundled Node branch (jiti loads a
		// compiled .js natively, loader.ts:553-557). PiG loads every extension through Pi's embedded-module branch
		// (loader.ts:551-552: virtualModules, tryNative:false; runtime-node/jiti-loader.mjs importExtension), where the host's own
		// module answers the import and the physical copy is never read. So the case asserts what both branches share: the ancestor
		// package.json that lists a host package under `dependencies` neither fails nor changes the load, and the extension loads.
		{name: "does not infer package ownership from ancestor manifests", files: map[string]string{
			"package.json": `{"name":"application","type":"module","dependencies":{"@earendil-works/pi-coding-agent":"1.0.0"}}`,
			"node_modules/@earendil-works/pi-coding-agent/package.json": `{"name":"@earendil-works/pi-coding-agent","type":"module","exports":"./index.js"}`,
			"node_modules/@earendil-works/pi-coding-agent/index.js":     `export const physicalDependency = true;`,
			"extensions/compiled-esm-extension.js": `import { physicalDependency } from "@earendil-works/pi-coding-agent";
export default function(pi) {
	pi.registerCommand(physicalDependency ? "physical-dependency" : "host-dependency", { handler: async () => {} });
}`}, paths: []string{"extensions/compiled-esm-extension.js"}, commands: []string{"host-dependency"}, absentCommands: []string{"physical-dependency"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:72
		{name: "keeps the type-only pi-ai OAuth compatibility barrel resolvable", files: map[string]string{"extensions/oauth-import.ts": `import * as oauth from "@earendil-works/pi-ai/oauth"; void oauth; ` + command}, paths: []string{"extensions/oauth-import.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:90
		{name: "discovers direct .js files in extensions/", files: map[string]string{"extensions/foo.js": command}, paths: []string{"extensions/foo.js"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:100
		{name: "discovers subdirectory with index.ts", files: map[string]string{"extensions/my-extension/index.ts": command}, paths: []string{"extensions/my-extension/index.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:113
		{name: "discovers subdirectory with index.js", files: map[string]string{"extensions/my-extension/index.js": command}, paths: []string{"extensions/my-extension/index.js"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:125
		{name: "prefers index.ts over index.js", files: map[string]string{"extensions/my-extension/index.ts": command, "extensions/my-extension/index.js": command}, paths: []string{"extensions/my-extension/index.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:138
		{name: "discovers subdirectory with package.json pi field", files: map[string]string{"extensions/my-package/src/main.ts": command, "extensions/my-package/package.json": `{"name":"my-package","pi":{"extensions":["./src/main.ts"]}}`}, paths: []string{"extensions/my-package/src/main.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:162
		{name: "keeps package.json pi extension entries with leading tilde package-relative", files: map[string]string{"extensions/tilde-package/~entry.ts": command, "extensions/tilde-package/~/entry.ts": command, "extensions/tilde-package/package.json": `{"name":"tilde-package","pi":{"extensions":["~entry.ts","~/entry.ts"]}}`}, paths: []string{"extensions/tilde-package/~/entry.ts", "extensions/tilde-package/~entry.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:187
		{name: "package.json can declare multiple extensions", files: map[string]string{"extensions/my-package/ext1.ts": command, "extensions/my-package/ext2.ts": command, "extensions/my-package/package.json": `{"name":"my-package","pi":{"extensions":["./ext1.ts","./ext2.ts"]}}`}, paths: []string{"extensions/my-package/ext1.ts", "extensions/my-package/ext2.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:208
		{name: "package.json with pi field takes precedence over index.ts", files: map[string]string{"extensions/my-package/index.ts": tool("from-index"), "extensions/my-package/custom.ts": tool("from-custom"), "extensions/my-package/package.json": `{"name":"my-package","pi":{"extensions":["./custom.ts"]}}`}, paths: []string{"extensions/my-package/custom.ts"}, tools: []string{"from-custom"}, absentTools: []string{"from-index"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:233
		{name: "ignores package.json without pi field, falls back to index.ts", files: map[string]string{"extensions/my-package/index.ts": command, "extensions/my-package/package.json": `{"name":"my-package","version":"1.0.0"}`}, paths: []string{"extensions/my-package/index.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:252
		{name: "ignores subdirectory without index or package.json", files: map[string]string{"extensions/not-an-extension/helper.ts": command, "extensions/not-an-extension/utils.ts": command}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:264
		{name: "does not recurse beyond one level", files: map[string]string{"extensions/container/nested/index.ts": command}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:278
		{name: "handles mixed direct files and subdirectories", files: map[string]string{"extensions/direct.ts": command, "extensions/with-index/index.ts": command, "extensions/with-manifest/entry.ts": command, "extensions/with-manifest/package.json": `{"pi":{"extensions":["./entry.ts"]}}`}, paths: []string{"extensions/direct.ts", "extensions/with-index/index.ts", "extensions/with-manifest/entry.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:299
		{name: "skips non-existent paths declared in package.json", files: map[string]string{"extensions/my-package/exists.ts": command, "extensions/my-package/package.json": `{"pi":{"extensions":["./exists.ts","./missing.ts"]}}`}, paths: []string{"extensions/my-package/exists.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:319
		{name: "loads extensions and registers commands", files: map[string]string{"extensions/with-command.ts": command}, paths: []string{"extensions/with-command.ts"}, commands: []string{"test"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:329
		{name: "loads extensions and registers tools", files: map[string]string{"extensions/with-tool.ts": tool("my-tool")}, paths: []string{"extensions/with-tool.ts"}, tools: []string{"my-tool"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:339
		{name: "reports errors for invalid extension code", files: map[string]string{"extensions/invalid.ts": "this is not valid typescript export"}, errorPath: "invalid.ts"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:349
		{name: "handles explicitly configured paths", files: map[string]string{"custom-location/my-ext.ts": command}, explicit: []string{"custom-location/my-ext.ts"}, paths: []string{"custom-location/my-ext.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:361
		{name: "resolves dependencies from extension's own node_modules", deps: true, explicit: []string{"with-deps"}, paths: []string{"with-deps/index.ts"}, tools: []string{"parse_duration"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:374
		{name: "registers message and entry renderers", files: map[string]string{"extensions/with-renderer.ts": `
			export default function(pi) {
				pi.registerMarkdownTransformer((markdown) => {
					return markdown;
				});
				pi.registerMessageRenderer("my-custom-type", (message, options, theme) => {
					return null; // Use default rendering
				});
				pi.registerEntryRenderer("my-entry-type", (entry, options, theme) => {
					return null;
				});
			}
		`}, paths: []string{"extensions/with-renderer.ts"}, markdown: true, messageRenderers: []string{"my-custom-type"}, entryRenderers: []string{"my-entry-type"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:399
		{name: "reports error when extension throws during initialization", files: map[string]string{"extensions/throws.ts": `export default function(pi) {throw new Error("Initialization failed!");}`}, errorPath: "throws.ts", errorText: "Initialization failed!"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:414
		{name: "reports error when extension has no default export", files: map[string]string{"extensions/no-default.ts": `export function notDefault(pi) {pi.registerCommand("test",{handler:async()=>{}});}`}, errorPath: "no-default.ts", errorText: "does not export a valid factory function"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:429
		{name: "allows multiple extensions to register different tools", files: map[string]string{"extensions/tool-a.ts": tool("tool-a"), "extensions/tool-b.ts": tool("tool-b")}, paths: []string{"extensions/tool-a.ts", "extensions/tool-b.ts"}, tools: []string{"tool-a", "tool-b"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:448
		{name: "loads extension with event handlers", files: map[string]string{"extensions/with-handlers.ts": `export default function(pi) {pi.on("agent_start",async()=>{});pi.on("tool_call",async()=>undefined);pi.on("agent_end",async()=>{});}`}, paths: []string{"extensions/with-handlers.ts"}, handlers: []string{"agent_start", "tool_call", "agent_end"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:467
		{name: "loads extension with shortcuts", files: map[string]string{"extensions/with-shortcut.ts": `export default function(pi) {pi.registerShortcut("ctrl+t",{description:"Test shortcut",handler:async(ctx)=>{}});}`}, paths: []string{"extensions/with-shortcut.ts"}, shortcuts: []string{"ctrl+t"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:485
		{name: "loads extension with flags", files: map[string]string{"extensions/with-flag.ts": `export default function(pi) {pi.registerFlag("my-flag",{description:"My custom flag",handler:async(value)=>{}});}`}, paths: []string{"extensions/with-flag.ts"}, flags: []string{"my-flag"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:503
		{name: "loadExtensions only loads explicit paths without discovery", direct: true, files: map[string]string{"extensions/discovered.ts": tool("discovered"), "explicit.ts": tool("explicit")}, explicit: []string{"explicit.ts"}, paths: []string{"explicit.ts"}, tools: []string{"explicit"}, absentTools: []string{"discovered"}},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-discovery.test.ts:521
		{name: "loadExtensions with no paths loads nothing", direct: true, files: map[string]string{"extensions/discovered.ts": command}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Sources and copied dependencies are case-local; each Host owns its processes and socket directory.
			t.Parallel()
			root := t.TempDir()
			for path, content := range tc.files {
				write(t, filepath.Join(root, filepath.FromSlash(path)), content)
			}
			if tc.deps {
				copyUpstreamOwnDependencies(t, root)
			}
			var paths []string
			for _, path := range tc.explicit {
				paths = append(paths, packagecontent.Collect([]string{filepath.Join(root, filepath.FromSlash(path))}, packagecontent.Extensions)...)
			}
			if !tc.direct {
				paths = append(paths, packagecontent.DiscoverAutomatic(filepath.Join(root, "extensions"), packagecontent.Extensions)...)
			}
			configs := make([]ExtConfig, 0, len(paths))
			for _, path := range paths {
				configs = append(configs, ExtConfig{Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Source: path, Enabled: true})
			}
			host := NewHost(root)
			t.Cleanup(func() { host.Shutdown("test done") })
			loaded, errs := host.LoadAll(t.Context(), configs)
			if tc.errorPath != "" {
				if len(errs) != 1 {
					t.Fatalf("errors=%v; want one", errs)
				}
				loadErr, ok := errors.AsType[*ExtensionLoadError](errs[0])
				if !ok || !strings.Contains(loadErr.Path, tc.errorPath) || !strings.Contains(loadErr.Error(), tc.errorText) {
					t.Fatalf("error=%v; want path %q and message %q", errs[0], tc.errorPath, tc.errorText)
				}
			} else if len(errs) != 0 {
				t.Fatalf("load errors: %v", errs)
			}
			gotPaths := make([]string, 0, len(loaded))
			for _, ext := range loaded {
				path, err := filepath.Rel(root, ext.Path)
				if err != nil {
					t.Fatal(err)
				}
				gotPaths = append(gotPaths, filepath.ToSlash(path))
			}
			slices.Sort(gotPaths)
			wantPaths := slices.Clone(tc.paths)
			slices.Sort(wantPaths)
			if !slices.Equal(gotPaths, wantPaths) {
				t.Fatalf("extensions=%q; want %q", gotPaths, wantPaths)
			}
			for _, name := range tc.tools {
				if !slices.ContainsFunc(loaded, func(ext extension.Extension) bool { _, ok := ext.Tools[name]; return ok }) {
					t.Errorf("missing tool %s", name)
				}
			}
			for _, name := range tc.absentTools {
				if slices.ContainsFunc(loaded, func(ext extension.Extension) bool { _, ok := ext.Tools[name]; return ok }) {
					t.Errorf("unexpected tool %s", name)
				}
			}
			for _, ext := range loaded {
				if tc.markdown && ext.MarkdownTransformer == nil {
					t.Error("missing Markdown transformer")
				}
				for _, name := range tc.messageRenderers {
					if _, ok := ext.MessageRenderers[name]; !ok {
						t.Errorf("missing message renderer %s", name)
					}
				}
				for _, name := range tc.entryRenderers {
					if _, ok := ext.EntryRenderers[name]; !ok {
						t.Errorf("missing entry renderer %s", name)
					}
				}
				for _, name := range tc.commands {
					if _, ok := ext.Commands[name]; !ok {
						t.Errorf("missing command %s", name)
					}
				}
				for _, name := range tc.absentCommands {
					if _, ok := ext.Commands[name]; ok {
						t.Errorf("unexpected command %s", name)
					}
				}
				for _, name := range tc.handlers {
					if len(ext.EventHandlers(name)) == 0 {
						t.Errorf("missing handler %s", name)
					}
				}
				for _, name := range tc.shortcuts {
					if _, ok := ext.Shortcuts[name]; !ok {
						t.Errorf("missing shortcut %s", name)
					}
				}
				for _, name := range tc.flags {
					if _, ok := ext.Flags[name]; !ok {
						t.Errorf("missing flag %s", name)
					}
				}
			}
		})
	}
}

func copyUpstreamOwnDependencies(t *testing.T, root string) {
	t.Helper()
	repo := findModuleRoot(t)
	source := filepath.Join(repo, ".upstream", "current", "packages", "coding-agent", "examples", "extensions", "with-deps")
	for _, name := range []string{"index.ts", "package.json"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, "with-deps", name), string(data))
	}
	dependency := filepath.Join(repo, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "ms")
	if err := os.CopyFS(filepath.Join(root, "with-deps", "node_modules", "ms"), os.DirFS(dependency)); err != nil {
		t.Fatalf("copy pinned ms dependency (run npm ci --prefix extensions/sdk-ts): %v", err)
	}
}
