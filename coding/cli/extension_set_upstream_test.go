package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// commandFactory is the Go counterpart of `(pi) => pi.registerCommand(name, { description, handler })`: the extension it returns
// registers one command. When onLoad is set it records the factory run, as the upstream tests' `loaded.push(name)` does.
func commandFactory(name, description string, onLoad func()) extension.ExtensionFactory {
	return func(pi extension.API) error {
		if onLoad != nil {
			onLoad()
		}
		if name != "" {
			pi.RegisterCommand(name, extension.CommandOptions{Description: description, Handler: func(context.Context, string) error { return nil }})
		}
		return nil
	}
}

func newExtensionSetTestLoader(t *testing.T, cwd, agentDir string, flags Args, inline ...extension.InlineExtension) *extensionSetLoader {
	t.Helper()
	return &extensionSetLoader{
		CWD: cwd, AgentDir: agentDir, Settings: codingagent.NewSettingsManager(cwd, agentDir),
		Flags: flags, Inline: inline, Mode: extension.ModeTUI,
	}
}

func reloadExtensionSet(t *testing.T, loader *extensionSetLoader, resolveTrust func(extensionSetResult) (bool, error)) *extensionSetResult {
	t.Helper()
	result, err := loader.Reload(context.Background(), resolveTrust)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	t.Cleanup(result.Close)
	return result
}

func (r extensionSetResult) paths() []string {
	paths := make([]string, 0, len(r.Extensions))
	for _, ext := range r.Extensions {
		paths = append(paths, ext.Path)
	}
	return paths
}

func requirePaths(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("extension paths = %q, want %q", got, want)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:43-57 ("should not treat a project manifest as the owner
// of a project extension"): a project package.json that depends on a host-provided package is not an extension package.
func TestUpstreamResourceLoaderProjectManifestDoesNotOwnProjectExtension(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"project/package.json":                         `{"dependencies":{"@earendil-works/pi-coding-agent":"1.0.0"}}`,
		"project/.pig/extensions/project-extension.ts": "export default function() {}",
	})
	result := reloadExtensionSet(t, newExtensionSetTestLoader(t, cwd, agentDir, Args{}), nil)
	if len(result.Extensions) != 1 || len(result.Warnings) != 0 {
		t.Fatalf("extensions = %q, warnings = %+v; want one extension and no warnings", result.paths(), result.Warnings)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:59-86 ("should warn about host dependencies in an
// extension package manifest", regression for #9863).
func TestUpstreamResourceLoaderWarnsAboutHostDependenciesInPackageManifest(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	packageRoot := filepath.Join(root, "extension-package")
	writeResourceTestFiles(t, root, map[string]string{
		"extension-package/package.json":                    `{"dependencies":{"@earendil-works/pi-coding-agent":"1.0.0"}}`,
		"extension-package/extensions/package-extension.ts": "export default function() {}",
		"agent/settings.json":                               `{"packages":[` + jsonString(packageRoot) + `]}`,
	})
	result := reloadExtensionSet(t, newExtensionSetTestLoader(t, cwd, agentDir, Args{}), nil)
	want := []extensionSetWarning{{
		Path:    filepath.Join(packageRoot, "package.json"),
		Warning: `Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: @earendil-works/pi-coding-agent. Installed copies can bypass the extension loader and create duplicate runtime modules.`,
	}}
	if len(result.Extensions) != 1 || !slices.Equal(result.Warnings, want) {
		t.Fatalf("extensions = %q, warnings = %+v; want one extension and %+v", result.paths(), result.Warnings, want)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:88-102 ("should fail when an extension package manifest
// cannot be parsed"): reload rejects with the JSON syntax error, which Pi's `toThrow(SyntaxError)` matches by class.
func TestUpstreamResourceLoaderFailsWhenPackageManifestCannotBeParsed(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	packageRoot := filepath.Join(root, "invalid-extension-package")
	writeResourceTestFiles(t, root, map[string]string{
		"invalid-extension-package/package.json":                    "{",
		"invalid-extension-package/extensions/package-extension.ts": "export default function() {}",
		"agent/settings.json":                                       `{"packages":[` + jsonString(packageRoot) + `]}`,
	})
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{})
	result, err := loader.Reload(context.Background(), nil)
	if result != nil {
		result.Close()
	}
	if _, isSyntax := errors.AsType[*json.SyntaxError](err); !isSyntax {
		t.Fatalf("reload error = %v (%T), want a JSON syntax error", err, err)
	}
}

// Pi's collectExtensionPackageWarnings (resource-loader.ts:66-93) parses `stripBom(readFileSync(package.json))` and reads
// `manifest.dependencies` with no guard: a leading BOM is skipped, a non-object `dependencies`, an array or a scalar manifest
// yields no warning, `null` throws a TypeError, and a package with no package.json yields nothing. Listed names are sorted.
func TestExtensionPackageWarningManifestShapes(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		missing        bool
		warning        string
		wantErr        bool
	}{
		{name: "bom is skipped", manifest: "\ufeff" + `{"dependencies":{"typebox":"1"}}`, warning: "typebox"},
		{name: "names are sorted", manifest: `{"dependencies":{"typebox":"1","@earendil-works/pi-ai":"1","left-pad":"1"}}`, warning: "@earendil-works/pi-ai, typebox"},
		{name: "legacy scope is host provided", manifest: `{"dependencies":{"@mariozechner/pi-tui":"1"}}`, warning: "@mariozechner/pi-tui"},
		{name: "peer dependencies are fine", manifest: `{"peerDependencies":{"typebox":"*"}}`},
		{name: "dependencies array", manifest: `{"dependencies":["typebox"]}`},
		{name: "dependencies null", manifest: `{"dependencies":null}`},
		{name: "dependencies string", manifest: `{"dependencies":"typebox"}`},
		{name: "unrelated dependencies", manifest: `{"dependencies":{"left-pad":"1"}}`},
		{name: "array manifest", manifest: `[]`},
		{name: "number manifest", manifest: `5`},
		{name: "null manifest", manifest: `null`, wantErr: true},
		{name: "invalid manifest", manifest: `{`, wantErr: true},
		{name: "no manifest", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if !tc.missing {
				writeResourceTestFiles(t, root, map[string]string{"package.json": tc.manifest})
			}
			configs := packageExtensionConfigsFixture(root)
			got, err := extensionPackageWarnings(configs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %t", err, tc.wantErr)
			}
			var want []extensionSetWarning
			if tc.warning != "" {
				want = []extensionSetWarning{{Path: filepath.Join(root, "package.json"), Warning: `Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: ` + tc.warning + `. Installed copies can bypass the extension loader and create duplicate runtime modules.`}}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("warnings = %+v, want %+v", got, want)
			}
		})
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:1040-1083 ("should leave out replaceable extensions
// whose names another extension registers"): a third-party MCP extension registering /mcp replaces the built-in one.
func TestUpstreamResourceLoaderLeavesOutReplaceableExtensions(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"agent/extensions/other-mcp.ts": `export default function(pi) {
  pi.registerCommand("mcp", { description: "other mcp", handler: async () => {} });
}`,
	})
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{},
		extension.NamedInlineExtension{Name: "mcp", Replaceable: true, Factory: commandFactory("mcp", "built-in mcp", nil)},
		extension.NamedInlineExtension{Name: "llama", Replaceable: true, Factory: commandFactory("llama", "built-in llama", nil)},
	)
	result := reloadExtensionSet(t, loader, nil)
	requirePaths(t, result.paths(), []string{filepath.Join(agentDir, "extensions", "other-mcp.ts"), "<inline:llama>"})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %+v", result.Errors)
	}
	runner := inproc.NewRunner(result.Extensions, cwd, result.Host.Runtime())
	t.Cleanup(func() { runner.Invalidate("") })
	for name, want := range map[string]string{"mcp": "other mcp", "llama": "built-in llama"} {
		if got, ok := runner.Command(name); !ok || got.Description != want {
			t.Errorf("command %q = %+v found=%t; want %q", name, got, ok, want)
		}
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:131-157 (omitReplacedExtensions). Upstream has no test
// for the warning of a replaced built-in extension: it names the extension that took the tool, command or flag, the
// registered name in its user-facing spelling, and the config remedy. Only `builtin:` paths warn, and only replaceable
// extensions are dropped. The remedy names this binary's own command (D2): `pig config`, never `pi config`.
func TestOmitReplacedExtensionsWarnsAboutReplacedBuiltin(t *testing.T) {
	registers := func(path string, replaceable bool, tool, command, flag string) extension.Extension {
		ext := extension.Extension{Path: path, Replaceable: replaceable}
		if tool != "" {
			ext.Tools, ext.ToolOrder = map[string]extension.RegisteredTool{tool: {}}, []string{tool}
		}
		if command != "" {
			ext.Commands, ext.CommandOrder = map[string]extension.RegisteredCommand{command: {Name: command}}, []string{command}
		}
		if flag != "" {
			ext.Flags, ext.FlagOrder = map[string]extension.ExtensionFlag{flag: {}}, []string{flag}
		}
		return ext
	}
	remedy := func(kind, name, builtin, owner string) extensionSetWarning {
		return extensionSetWarning{Path: "builtin:" + builtin, Warning: "Extension " + owner + " registers " + kind + " `" + name + "`, so built-in extension `" + builtin + "` was not loaded. To use `" + builtin + "`, run `pig config` and make sure it is enabled under Built-in extensions, then disable or remove the existing extension. We recommend only having one or the other loaded at a time."}
	}
	for _, tc := range []struct {
		name         string
		extensions   []extension.Extension
		wantPaths    []string
		wantWarnings []extensionSetWarning
	}{
		{"command", []extension.Extension{registers("/x/a.ts", false, "", "mcp", ""), registers("builtin:mcp", true, "", "mcp", "")}, []string{"/x/a.ts"}, []extensionSetWarning{remedy("command", "/mcp", "mcp", "/x/a.ts")}},
		{"tool", []extension.Extension{registers("/x/a.ts", false, "codemode", "", ""), registers("builtin:codemode", true, "codemode", "", "")}, []string{"/x/a.ts"}, []extensionSetWarning{remedy("tool", "codemode", "codemode", "/x/a.ts")}},
		{"flag", []extension.Extension{registers("/x/a.ts", false, "", "", "mcp-debug"), registers("builtin:mcp", true, "", "", "mcp-debug")}, []string{"/x/a.ts"}, []extensionSetWarning{remedy("flag", "--mcp-debug", "mcp", "/x/a.ts")}},
		{"inline replaceable is dropped silently", []extension.Extension{registers("/x/a.ts", false, "", "mcp", ""), registers("<inline:mcp>", true, "", "mcp", "")}, []string{"/x/a.ts"}, nil},
		{"non replaceable is kept", []extension.Extension{registers("/x/a.ts", false, "", "mcp", ""), registers("builtin:mcp", false, "", "mcp", "")}, []string{"/x/a.ts", "builtin:mcp"}, nil},
		{"two replaceable extensions do not replace each other", []extension.Extension{registers("builtin:a", true, "", "mcp", ""), registers("builtin:b", true, "", "mcp", "")}, []string{"builtin:a", "builtin:b"}, nil},
		{"a colon in the name cuts the reported name (split(\":\", 2))", []extension.Extension{registers("/x/a.ts", false, "ns:tool", "", ""), registers("builtin:codemode", true, "ns:tool", "", "")}, []string{"/x/a.ts"}, []extensionSetWarning{remedy("tool", "ns", "codemode", "/x/a.ts")}},
		{"no shared name", []extension.Extension{registers("/x/a.ts", false, "", "other", ""), registers("builtin:mcp", true, "", "mcp", "")}, []string{"/x/a.ts", "builtin:mcp"}, nil},
		{"the last non-replaceable owner is reported (a Map keeps the later entry of a repeated name)", []extension.Extension{registers("/x/a.ts", false, "", "mcp", ""), registers("/x/b.ts", false, "", "mcp", ""), registers("builtin:mcp", true, "", "mcp", "")}, []string{"/x/a.ts", "/x/b.ts"}, []extensionSetWarning{remedy("command", "/mcp", "mcp", "/x/b.ts")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warnings []extensionSetWarning
			kept := omitReplacedExtensions(tc.extensions, &warnings)
			var paths []string
			for _, ext := range kept {
				paths = append(paths, ext.Path)
			}
			if !slices.Equal(paths, tc.wantPaths) || !slices.Equal(warnings, tc.wantWarnings) {
				t.Fatalf("kept = %q, warnings = %+v; want %q, %+v", paths, warnings, tc.wantPaths, tc.wantWarnings)
			}
		})
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:1085-1105 ("should skip built-in extensions disabled
// in settings"). Upstream's toMatchObject checks the path and source of the extension's sourceInfo.
func TestUpstreamResourceLoaderSkipsBuiltinExtensionsDisabledInSettings(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{"agent/settings.json": `{"extensions":["-builtin:mcp"]}`})
	var loaded []string
	record := func(name string) func() { return func() { loaded = append(loaded, name) } }
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{},
		extension.NamedInlineExtension{Name: "mcp", Builtin: true, Factory: commandFactory("", "", record("mcp"))},
		extension.NamedInlineExtension{Name: "llama", Builtin: true, Factory: commandFactory("", "", record("llama"))},
	)
	result := reloadExtensionSet(t, loader, nil)
	requirePaths(t, result.paths(), []string{"builtin:llama"})
	info := codingagent.PiSourceInfoValue(result.Extensions[0].SourceInfo)
	if info.Path != "builtin:llama" || info.Source != "builtin" {
		t.Errorf("sourceInfo = %+v, want path builtin:llama and source builtin", info)
	}
	if !result.Extensions[0].Hidden {
		t.Error("built-in extension is not hidden")
	}
	if !slices.Equal(loaded, []string{"llama"}) {
		t.Errorf("loaded factories = %q, want [llama]", loaded)
	}
}

func TestBuiltInExtensionNamesListsOnlyBuiltinEntries(t *testing.T) {
	got := builtInExtensionNames([]extension.InlineExtension{extension.NamedInlineExtension{Name: "llama.cpp", Builtin: true}, extension.NamedInlineExtension{Name: "plain"}, extension.NamedInlineExtension{Name: "mcp", Builtin: true, Replaceable: true}, extension.ExtensionFactory(nil)})
	if want := []string{"llama.cpp", "mcp"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// packageExtensionConfigsFixture is the config of one extension of the package rooted at root, as collectPackageExtensionConfigs
// stamps it: origin "package" with the package root as its base directory.
func packageExtensionConfigsFixture(root string) []subprocess.ExtConfig {
	return []subprocess.ExtConfig{{Source: filepath.Join(root, "extensions", "a.ts"), SourceInfo: codingagent.PiSourceInfo{Path: filepath.Join(root, "extensions", "a.ts"), Source: root, Scope: "user", Origin: "package", BaseDir: root}, PackageRoot: root}}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/main.ts:797-800: a package warning is a warning diagnostic that names the
// package.json path, with no escaping.
func TestExtensionWarningDiagnosticsFormatPackageWarnings(t *testing.T) {
	got := extensionWarningDiagnostics([]extensionSetWarning{{Path: `/x/a"b/package.json`, Warning: "w1"}, {Path: "/y/package.json", Warning: "w2"}})
	want := []codingagent.AgentSessionRuntimeDiagnostic{
		{Type: "warning", Message: `Extension package "/x/a"b/package.json": w1`},
		{Type: "warning", Message: `Extension package "/y/package.json": w2`},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostics = %+v, want %+v", got, want)
	}
	if got := extensionWarningDiagnostics(nil); len(got) != 0 {
		t.Fatalf("diagnostics without warnings = %+v", got)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:552-573 and package-manager.ts:970-1006: the
// `builtin:<name>` paths a reload loads. Explicit `-e builtin:<name>` paths come first and are the only ones under --no-extensions;
// settings enable every other built-in extension in name order unless the user excludes it and the project does not override.
func TestEnabledBuiltinExtensionPaths(t *testing.T) {
	names := []string{"llama.cpp", "codemode", "mcp"}
	for _, tc := range []struct {
		name    string
		global  string
		project string
		flags   Args
		want    []string
	}{
		{"defaults", "", "", Args{}, []string{"builtin:llama.cpp", "builtin:codemode", "builtin:mcp"}},
		{"user exclusion", `{"extensions":["-builtin:mcp"]}`, "", Args{}, []string{"builtin:llama.cpp", "builtin:codemode"}},
		{"project override re-enables", `{"extensions":["-builtin:mcp"]}`, `{"extensions":["+builtin:mcp"]}`, Args{}, []string{"builtin:llama.cpp", "builtin:codemode", "builtin:mcp"}},
		{"no extensions", "", "", Args{NoExtensions: true}, nil},
		{"no extensions keeps explicit paths", "", "", Args{NoExtensions: true, Extensions: []string{"builtin:mcp", "./local.ts"}}, []string{"builtin:mcp"}},
		{"explicit paths come first", "", "", Args{Extensions: []string{"builtin:mcp"}}, []string{"builtin:mcp", "builtin:llama.cpp", "builtin:codemode"}},
		{"explicit path loads an excluded built-in", `{"extensions":["-builtin:mcp"]}`, "", Args{Extensions: []string{"builtin:mcp"}}, []string{"builtin:mcp", "builtin:llama.cpp", "builtin:codemode"}},
		{"explicit unknown name is kept for the loader to report", "", "", Args{NoExtensions: true, Extensions: []string{"builtin:missing"}}, []string{"builtin:missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cwd, agentDir := resourceExtensionFixture(t)
			files := map[string]string{}
			if tc.global != "" {
				files["agent/settings.json"] = tc.global
			}
			if tc.project != "" {
				files["project/.pig/settings.json"] = tc.project
			}
			writeResourceTestFiles(t, root, files)
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			// pig additive (D92): a built-in this test binary was built without is filtered out.
			if got, want := enabledBuiltinExtensionPaths(sm, tc.flags, names), withoutCompiledOutBuiltins(tc.want); !slices.Equal(got, want) {
				t.Fatalf("paths = %q, want %q", got, want)
			}
		})
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:706-735 with extensions/index.ts:8: `-e
// builtin:llama.cpp` names a known built-in extension, so the production built-in pass reports no "Unknown built-in extension"
// error for it (the llama host loads it); an unknown name still fails.
func TestProductionBuiltinPassAcceptsExplicitLlama(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	loader := &extensionSetLoader{CWD: cwd, AgentDir: agentDir, Settings: codingagent.NewSettingsManager(cwd, agentDir), Inline: builtInExtensions,
		Flags: Args{NoExtensions: true, Extensions: []string{"builtin:llama.cpp", "builtin:missing"}}}
	_, errs, _ := loader.loadBuiltinsAfter(nil)
	if want := []extensionSetError{{Path: "builtin:missing", Error: "Unknown built-in extension: builtin:missing"}}; !slices.Equal(errs, want) {
		t.Fatalf("errors = %+v, want %+v", errs, want)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/main.ts:793-796 with resource-loader.ts:717-733: a built-in extension's load
// error is reported as `Failed to load extension "<path>": <error>` with the bare error text; only file extensions carry the
// loader's own "Failed to load extension: " prefix (loader.ts:635).
func TestExtensionErrorDiagnosticsFormatBuiltinErrors(t *testing.T) {
	got := extensionErrorDiagnostics([]extensionSetError{{Path: "builtin:missing", Error: "Unknown built-in extension: builtin:missing"}, {Path: "builtin:mcp", Error: "boom"}})
	want := []codingagent.AgentSessionRuntimeDiagnostic{
		{Type: "error", Message: `Failed to load extension "builtin:missing": Unknown built-in extension: builtin:missing`},
		{Type: "error", Message: `Failed to load extension "builtin:mcp": boom`},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diagnostics = %+v, want %+v", got, want)
	}
}

// upstream: packages/coding-agent/src/core/resource-loader.ts:66-93 collectExtensionPackageWarnings reads metadata.packageRoot, and
// package-manager.ts:1360-1376 gives a local file source only baseDir (the file's directory), no packageRoot: the package.json next
// to a single extension file is not a package manifest and raises no warning.
func TestExtensionPackageWarningsIgnoreALocalFileSource(t *testing.T) {
	root := t.TempDir()
	writeResourceTestFiles(t, root, map[string]string{"package.json": `{"dependencies":{"typebox":"1"}}`})
	file := filepath.Join(root, "single.ts")
	configs := []subprocess.ExtConfig{{Source: file, SourceInfo: codingagent.PiSourceInfo{Path: file, Source: file, Scope: "user", Origin: "package", BaseDir: root}}}
	got, err := extensionPackageWarnings(configs)
	if err != nil || len(got) != 0 {
		t.Fatalf("warnings = %+v, err = %v; a file source has no package root", got, err)
	}
	configs[0].PackageRoot = root
	if got, err = extensionPackageWarnings(configs); err != nil || len(got) != 1 {
		t.Fatalf("warnings = %+v, err = %v; the same extension from a package directory warns", got, err)
	}
}

// resource-loader.ts:376-377 drops the built-in entries from extensionFactories, and loadExtensionFactories (:1138-1141) names a bare factory `<inline:N>` by
// its 1-based position among the rest; a named entry is `<inline:name>`; only a named entry can be hidden or replaceable. Probed with Pi 1.1.0's
// DefaultResourceLoader over [builtin zzz, bare, named, bare]: ["builtin:zzz","<inline:1>","<inline:named>","<inline:3>"].
// mutation-checked: counting the built-in entries in the position, or letting a bare factory be hidden, fails.
func TestInlineExtensionBareFactoriesAreNamedByPosition(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{},
		extension.NamedInlineExtension{Name: "mcp", Builtin: true, Factory: commandFactory("", "", nil)},
		extension.ExtensionFactory(commandFactory("first", "bare", nil)),
		extension.NamedInlineExtension{Name: "named", Hidden: true, Factory: commandFactory("named", "named", nil)},
		extension.ExtensionFactory(commandFactory("third", "bare", nil)),
	)
	result := reloadExtensionSet(t, loader, nil)
	inline := map[string]extension.Extension{}
	for _, ext := range result.Extensions {
		inline[ext.Path] = ext
	}
	for _, path := range []string{"<inline:1>", "<inline:named>", "<inline:3>"} {
		if _, ok := inline[path]; !ok {
			t.Fatalf("paths = %q, want %s", result.paths(), path)
		}
	}
	if inline["<inline:1>"].Hidden || inline["<inline:1>"].Replaceable || !inline["<inline:named>"].Hidden {
		t.Fatalf("hidden/replaceable: bare %+v named %+v", inline["<inline:1>"], inline["<inline:named>"])
	}
}
