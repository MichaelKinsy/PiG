package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

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

// failingNpmCommand writes an npm command that exits non-zero, which stands in for an install that cannot reach the registry, and returns its settings.json form.
func failingNpmCommand(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the npm command")
	}
	command := filepath.Join(t.TempDir(), "npm")
	if err := os.WriteFile(command, []byte("#!/bin/sh\necho \"npm install failed\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return `"npmCommand": [` + strconv.Quote(command) + `]`
}

func installFailureLoader(t *testing.T, settings map[string]string, options DefaultResourceLoaderOptions) *DefaultResourceLoader {
	t.Helper()
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	root := t.TempDir()
	options.CWD, options.AgentDir = filepath.Join(root, "project"), filepath.Join(root, "agent")
	files := map[string]string{filepath.Join(options.CWD, "keep"): ""}
	for rel, content := range settings {
		files[filepath.Join(root, rel)] = content
	}
	writeExtensionFiles(t, files)
	loader := NewDefaultResourceLoader(options)
	t.Cleanup(loader.Close)
	return loader
}

// Pi 6dfb54678 resource-loader.test.ts "should warn and keep loading when a configured package cannot be installed" (#11088).
func TestDefaultResourceLoaderWarnsAndKeepsLoadingWhenAConfiguredPackageCannotBeInstalled(t *testing.T) {
	loader := installFailureLoader(t, map[string]string{"agent/settings.json": `{"packages": ["npm:broken-package"], ` + failingNpmCommand(t) + `}`}, DefaultResourceLoaderOptions{})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	warnings := loader.GetExtensions().Warnings
	if len(warnings) != 1 || warnings[0].Path != "npm:broken-package" || !strings.HasPrefix(warnings[0].Warning, "failed to install (") ||
		!strings.Contains(warnings[0].Warning, `Run "pig remove npm:broken-package" to remove it from settings.json.`) {
		t.Fatalf("warnings = %+v, want one failed-to-install warning naming pig remove", warnings)
	}
}

// D215: a configured local Package whose directory does not exist warns with the path it resolved to and the pig remove hint, as a failed npm
// install does, and the load goes on; Pi skips it without a word. The pig command reports it the same way (TestStartupWarnsAboutAConfiguredLocalPackageThatDoesNotExist).
func TestDefaultResourceLoaderWarnsAboutAConfiguredLocalPackageThatDoesNotExist(t *testing.T) {
	loader := installFailureLoader(t, map[string]string{"agent/settings.json": `{"packages": ["../gone-package"]}`}, DefaultResourceLoaderOptions{})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	resolved := filepath.Join(filepath.Dir(loader.agentDir), "gone-package")
	warnings := loader.GetExtensions().Warnings
	if len(warnings) != 1 || warnings[0].Path != "../gone-package" || warnings[0].Warning != `failed to install (Path does not exist: `+resolved+`). Run "pig remove ../gone-package" to remove it from settings.json.` {
		t.Fatalf("warnings = %+v, want one warning naming %s and pig remove", warnings, resolved)
	}
}

// Pi 6dfb54678 resource-loader.test.ts "should point temporary extension sources at the source instead of pi remove".
func TestDefaultResourceLoaderPointsTemporaryExtensionSourcesAtTheSource(t *testing.T) {
	loader := installFailureLoader(t, map[string]string{"agent/settings.json": `{` + failingNpmCommand(t) + `}`}, DefaultResourceLoaderOptions{AdditionalExtensionPaths: []string{"npm:broken-package"}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	warnings := loader.GetExtensions().Warnings
	if len(warnings) != 1 || warnings[0].Path != "npm:broken-package" || !strings.Contains(warnings[0].Warning, "Check the source and try again.") || strings.Contains(warnings[0].Warning, "remove") {
		t.Fatalf("warnings = %+v, want one warning that points at the source", warnings)
	}
}

// Pi 6dfb54678 resource-loader.test.ts "should point project extension packages at pi remove --local".
func TestDefaultResourceLoaderPointsProjectPackagesAtRemoveLocal(t *testing.T) {
	loader := installFailureLoader(t, map[string]string{
		"agent/settings.json":        `{` + failingNpmCommand(t) + `}`,
		"project/.pig/settings.json": `{"packages": ["npm:broken-package"]}`,
	}, DefaultResourceLoaderOptions{})
	trusted := func(context.Context, ResolveProjectTrustInput) (bool, error) { return true, nil }
	if err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: trusted}); err != nil {
		t.Fatal(err)
	}
	warnings := loader.GetExtensions().Warnings
	if len(warnings) != 1 || !strings.Contains(warnings[0].Warning, `Run "pig remove npm:broken-package --local"`) {
		t.Fatalf("warnings = %+v, want one warning naming pig remove --local", warnings)
	}
}

// Pi 6dfb54678 resource-loader.test.ts "should warn once about a failing install when project trust is resolved".
func TestDefaultResourceLoaderWarnsOnceAboutAFailingInstallWhenProjectTrustIsResolved(t *testing.T) {
	loader := installFailureLoader(t, map[string]string{"agent/settings.json": `{"packages": ["npm:broken-package"], ` + failingNpmCommand(t) + `}`}, DefaultResourceLoaderOptions{})
	untrusted := func(context.Context, ResolveProjectTrustInput) (bool, error) { return false, nil }
	if err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: untrusted}); err != nil {
		t.Fatal(err)
	}
	warnings := loader.GetExtensions().Warnings
	if len(warnings) != 1 || warnings[0].Path != "npm:broken-package" {
		t.Fatalf("warnings = %+v, want exactly one for npm:broken-package", warnings)
	}
}
