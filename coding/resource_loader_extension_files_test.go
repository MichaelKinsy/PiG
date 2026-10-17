package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// extensionFileLoaderFixture is a loader over a temporary project and agent directory whose user settings list one extension file that holds
// invalid code, so a load that reaches it ends in an error entry naming it.
func extensionFileLoaderFixture(t *testing.T, options DefaultResourceLoaderOptions) (*DefaultResourceLoader, string) {
	t.Helper()
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	broken := filepath.Join(root, "broken.ts")
	for path, content := range map[string]string{
		broken:                                   "this is not a module (\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(broken) + `]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	options.CWD, options.AgentDir = cwd, agentDir
	loader := NewDefaultResourceLoader(options)
	t.Cleanup(loader.Close)
	return loader, broken
}

func errorPaths(result LoadExtensionsResult) []string {
	var paths []string
	for _, e := range result.Errors {
		paths = append(paths, e.Path)
	}
	return paths
}

// resource-loader.ts:513-575: a loader without a LoadExtensions hook loads the enabled extensions of the settings and Packages.
func TestDefaultResourceLoaderLoadsSettingsExtensionsByDefault(t *testing.T) {
	loader, broken := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := errorPaths(loader.GetExtensions()); !slices.Contains(got, broken) {
		t.Fatalf("error paths = %q, want the settings extension %q to have been loaded", got, broken)
	}
}

// resource-loader.ts:566-568: noExtensions keeps only the additional (CLI) extension paths, so the settings extension does not load.
func TestDefaultResourceLoaderNoExtensionsSkipsSettingsExtensions(t *testing.T) {
	loader, broken := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := errorPaths(loader.GetExtensions()); slices.Contains(got, broken) {
		t.Fatalf("error paths = %q, NoExtensions still loaded %q", got, broken)
	}
}

// resource-loader.ts:545-551: a local additional extension path that does not exist is one error entry naming the resolved path.
func TestDefaultResourceLoaderReportsAMissingAdditionalExtensionPath(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "nowhere", "ext.ts")
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true, AdditionalExtensionPaths: []string{missing}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	var found *ExtensionLoadError
	for _, e := range loader.GetExtensions().Errors {
		if e.Path == missing {
			found = &e
		}
	}
	if found == nil || found.Error != "Extension path does not exist: "+missing {
		t.Fatalf("errors = %+v, want 'Extension path does not exist: %s'", loader.GetExtensions().Errors, missing)
	}
}

// A LoadExtensions hook replaces the default load, as the hook of the Extension Host always has.
func TestDefaultResourceLoaderHookReplacesTheDefaultExtensionLoad(t *testing.T) {
	called := 0
	loader, broken := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{LoadExtensions: func(_ context.Context, _ ExtensionLoadRequest) (LoadExtensionsResult, error) {
		called++
		return LoadExtensionsResult{}, nil
	}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if called == 0 || slices.Contains(errorPaths(loader.GetExtensions()), broken) {
		t.Fatalf("hook called %d times, errors %q: the default load ran beside the hook", called, errorPaths(loader.GetExtensions()))
	}
}

// resource-loader.ts:747-790 loadFinalExtensionSet: across a trust reload the extensions the bootstrap pass loaded stay loaded and are not
// loaded again, so an extension's factory runs once. The extension logs each run, and the settings extension is user-scope, so the bootstrap
// pass (project untrusted) already loads it.
func TestDefaultResourceLoaderTrustReloadKeepsTheBootstrapExtensions(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	logFile := filepath.Join(root, "factory.log")
	ext := filepath.Join(root, "ok.ts")
	for path, content := range map[string]string{
		ext:                                      "import { appendFileSync } from \"node:fs\";\nexport default function (pi) { appendFileSync(" + strconv.Quote(logFile) + ", \"run\\n\"); pi.registerCommand(\"zz\", { description: \"d\", handler: async () => {} }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	t.Cleanup(loader.Close)
	var bootstrap int
	err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(_ context.Context, input ResolveProjectTrustInput) (bool, error) {
		bootstrap = len(input.ExtensionsResult.Extensions)
		return true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	final := loader.GetExtensions()
	data, _ := os.ReadFile(logFile)
	if bootstrap != 1 || len(final.Extensions) != 1 || len(final.Errors) != 0 {
		t.Fatalf("bootstrap extensions = %d, final extensions = %d, errors = %+v; want 1, 1 and none", bootstrap, len(final.Extensions), final.Errors)
	}
	if runs := strings.Count(string(data), "run"); runs != 1 {
		t.Fatalf("the factory ran %d times across the trust reload, want once", runs)
	}
}

// resource-loader.ts:747-790: a path the bootstrap pass attempted and failed is not attempted again, and its error is kept once (failedPreloadPaths,
// errors: [...preTrust.errors, ...remaining.errors]).
func TestDefaultResourceLoaderTrustReloadDoesNotRetryAFailedBootstrapExtension(t *testing.T) {
	loader, broken := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{})
	err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range errorPaths(loader.GetExtensions()) {
		if path == broken {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the failed extension %q is in the errors %d times, want once", broken, count)
	}
}

func writeExtensionFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// resource-loader.ts:513-600, :714-745: a settings extension loads through the real Extension Host (it registers its command, the extension
// runs in a live process), and Close stops that host.
func TestDefaultResourceLoaderSuccessfulLoadRunsInALiveHostThatCloseStops(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	pidFile := filepath.Join(root, "pid")
	ext := filepath.Join(root, "ok.ts")
	writeExtensionFiles(t, map[string]string{
		ext:                                      "import { writeFileSync } from \"node:fs\";\nexport default function (pi) { writeFileSync(" + strconv.Quote(pidFile) + ", String(process.pid)); pi.registerCommand(\"zz\", { description: \"d\", handler: async () => {} }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
		filepath.Join(cwd, "keep"):               "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	t.Cleanup(loader.Close)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.GetExtensions()
	if len(loaded.Extensions) != 1 || len(loaded.Errors) != 0 || loaded.Runtime == nil {
		t.Fatalf("extensions = %d, errors = %+v, runtime = %v; want one extension, no error and a runtime", len(loaded.Extensions), loaded.Errors, loaded.Runtime)
	}
	if _, ok := loaded.Extensions[0].Commands["zz"]; !ok {
		t.Fatalf("commands of the loaded extension = %v, want zz registered by the factory", loaded.Extensions[0].Commands)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !extensionProcessAlive(pid) {
		t.Fatalf("extension process %q is not alive after the load: %v", data, err)
	}
	loader.Close()
	deadline := time.Now().Add(10 * time.Second)
	for extensionProcessAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("extension process %d still alive 10s after Close", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// resource-loader.ts:722-727, :576-579: a `builtin:<name>` path is an error when the loader has no such built-in extension, unless its
// name is in disabledBuiltinExtensions, where it is left out.
func TestDefaultResourceLoaderBuiltinPathsAreUnknownOrDisabled(t *testing.T) {
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true, AdditionalExtensionPaths: []string{"builtin:alpha", "builtin:beta"}, DisabledBuiltinExtensions: []string{"beta"}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	var got []ExtensionLoadError
	for _, e := range loader.GetExtensions().Errors {
		if strings.HasPrefix(e.Path, "builtin:") {
			got = append(got, e)
		}
	}
	if len(got) != 1 || got[0] != (ExtensionLoadError{Path: "builtin:alpha", Error: "Unknown built-in extension: builtin:alpha"}) {
		t.Fatalf("built-in errors = %+v, want only builtin:alpha unknown", got)
	}
}

// resource-loader.ts:66-93, :581: an extension Package whose package.json lists a host-provided package under dependencies adds one warning
// naming its package.json.
func TestDefaultResourceLoaderWarnsOfHostProvidedPackagesInDependencies(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	pkg := filepath.Join(root, "pkg")
	writeExtensionFiles(t, map[string]string{
		filepath.Join(pkg, "package.json"):       `{"name":"p","dependencies":{"typebox":"1","left-pad":"1"},"pi":{"extensions":["./ext.ts"]}}`,
		filepath.Join(pkg, "ext.ts"):             "export default function (pi) {}\n",
		filepath.Join(agentDir, "settings.json"): `{"packages": [` + strconv.Quote(pkg) + `]}`,
		filepath.Join(cwd, "keep"):               "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	t.Cleanup(loader.Close)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(pkg, "package.json")
	got := loader.GetExtensions().Warnings
	want := ExtensionLoadWarning{Path: manifest, Warning: `Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: typebox. Installed copies can bypass the extension loader and create duplicate runtime modules.`}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("warnings = %+v, want [%+v]", got, want)
	}
}

// resource-loader.ts:747-790: the unknown built-in error of the bootstrap pass is carried to the final set once, not added again.
func TestDefaultResourceLoaderTrustReloadReportsAnUnknownBuiltinOnce(t *testing.T) {
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{AdditionalExtensionPaths: []string{"builtin:alpha"}})
	err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range errorPaths(loader.GetExtensions()) {
		if path == "builtin:alpha" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("builtin:alpha is in the errors %d times, want once", count)
	}
}

func commandFactory(name string, runs *int) extension.ExtensionFactory {
	return func(pi extension.API) error {
		if runs != nil {
			*runs++
		}
		pi.RegisterCommand(name, extension.CommandOptions{Description: name, Handler: func(context.Context, string) error { return nil }})
		return nil
	}
}

// resource-loader.ts:376-383, :714-745, :765-775: ExtensionFactories load with the default extension load. A `builtin:<name>` path runs the
// Builtin entry of that name (hidden), an inline factory loads last as `<inline:name>`, and the extensions of the files, built-ins and inline
// factories share one runtime.
func TestDefaultResourceLoaderRunsTheExtensionFactories(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	ext := filepath.Join(root, "ok.ts")
	writeExtensionFiles(t, map[string]string{
		ext:                                      "export default function (pi) { pi.registerCommand(\"file\", { description: \"d\", handler: async () => {} }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
		filepath.Join(cwd, "keep"):               "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir, AdditionalExtensionPaths: []string{"builtin:alpha"}, ExtensionFactories: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: commandFactory("alpha", nil)},
		commandFactory("inline", nil),
	}})
	t.Cleanup(loader.Close)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.GetExtensions()
	var paths []string
	for _, e := range loaded.Extensions {
		paths = append(paths, e.Path)
	}
	if !slices.Equal(paths, []string{"builtin:alpha", ext, "<inline:1>"}) || len(loaded.Errors) != 0 {
		t.Fatalf("extension paths = %q, errors = %+v; want the built-in (a -e path comes first), the settings file, then the inline factory", paths, loaded.Errors)
	}
	if !loaded.Extensions[0].Hidden {
		t.Fatal("the built-in extension is not hidden")
	}
	if loaded.Runtime == nil || loader.files.host == nil || loaded.Runtime != loader.files.host.Runtime() {
		t.Fatalf("runtime %v is not the runtime of the Host (%v): the extensions do not share one runtime", loaded.Runtime, loader.files.host)
	}
}

// resource-loader.ts:747-790: the inline and built-in factories of the bootstrap pass are kept by the final pass, so each runs once across a trust
// reload.
func TestDefaultResourceLoaderTrustReloadRunsTheFactoriesOnce(t *testing.T) {
	var builtinRuns, inlineRuns int
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true, AdditionalExtensionPaths: []string{"builtin:alpha"}, ExtensionFactories: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: commandFactory("alpha", &builtinRuns)},
		commandFactory("inline", &inlineRuns),
	}})
	err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if got := loader.GetExtensions(); len(got.Extensions) != 2 || builtinRuns != 1 || inlineRuns != 1 {
		t.Fatalf("extensions = %d, built-in ran %d times, inline ran %d times; want 2, 1 and 1", len(got.Extensions), builtinRuns, inlineRuns)
	}
}

// resource-loader.ts:114-157, :765-775: a replaceable built-in whose command another extension also registers is left out, with a warning.
func TestDefaultResourceLoaderOmitsAReplacedBuiltin(t *testing.T) {
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true, AdditionalExtensionPaths: []string{"builtin:alpha"}, ExtensionFactories: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Replaceable: true, Factory: commandFactory("shared", nil)},
		commandFactory("shared", nil),
	}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.GetExtensions()
	if len(loaded.Extensions) != 1 || loaded.Extensions[0].Path != "<inline:1>" || len(loaded.Warnings) != 1 || loaded.Warnings[0].Path != "builtin:alpha" {
		t.Fatalf("extensions = %d, warnings = %+v; want only <inline:1> and a warning for builtin:alpha", len(loaded.Extensions), loaded.Warnings)
	}
}

// loader.ts:689 loadExtensionsInternal creates a runtime when none is passed, and a reload without a pre-trust pass passes none
// (resource-loader.ts:765, preTrustExtensions?.runtime): every reload starts from a new runtime, so a factory's registrations from an earlier
// reload are not carried into the next one.
// mutation-checked: keeping the previous reload's runtime fails it.
func TestDefaultResourceLoaderEachReloadStartsANewRuntime(t *testing.T) {
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true, ExtensionFactories: []extension.InlineExtension{commandFactory("inline", nil)}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	first := loader.GetExtensions().Runtime
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	second := loader.GetExtensions().Runtime
	if first == nil || second == nil || first == second {
		t.Fatalf("runtimes %p and %p; want a new runtime for the second reload", first, second)
	}
}

func TestDefaultResourceLoaderBuiltinErrorsFollowThePathOrder(t *testing.T) {
	loader, _ := extensionFileLoaderFixture(t, DefaultResourceLoaderOptions{NoExtensions: true, AdditionalExtensionPaths: []string{"builtin:one", "builtin:bad", "builtin:two"}, ExtensionFactories: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "bad", Builtin: true, Factory: func(extension.API) error { return errors.New("broken") }},
	}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	var got []ExtensionLoadError
	for _, e := range loader.GetExtensions().Errors {
		if strings.HasPrefix(e.Path, "builtin:") {
			got = append(got, e)
		}
	}
	want := []ExtensionLoadError{{Path: "builtin:one", Error: "Unknown built-in extension: builtin:one"}, {Path: "builtin:bad", Error: "broken"}, {Path: "builtin:two", Error: "Unknown built-in extension: builtin:two"}}
	if !slices.Equal(got, want) {
		t.Fatalf("built-in errors = %+v, want %+v", got, want)
	}
}
