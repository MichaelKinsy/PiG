package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// resource-loader.ts getExtensions returns the last load's extensions, errors, warnings and shared runtime; loadProjectTrustExtensions leaves the settings untrusted; an extension without source info gets the source info of its path on itself and its tools.
// An extension whose Extension Host already set source info is not asserted here: Pi's applyExtensionSourceInfo (resource-loader.ts:914-926) recomputes it from the path, while this loader keeps the host's.
// Pi: packages/coding-agent/src/core/resource-loader.ts getExtensions, loadProjectTrustExtensions, applyExtensionSourceInfo.
func TestResourceLoaderReportsTheLoadAndAppliesExtensionSourceInfo(t *testing.T) {
	f := newResourceLoaderFixture(t)
	manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{})
	manager.SetProjectTrusted(true)
	runtime := new(extension.ExtensionRuntime)
	userExt := filepath.Join(f.agentDir, "extensions", "user.ts")
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{
				Extensions: []extension.Extension{
					{Path: userExt, Tools: map[string]extension.RegisteredTool{"t": {}}},
				},
				Errors:   []ExtensionLoadError{{Path: "e", Error: "boom"}},
				Warnings: []ExtensionLoadWarning{{Path: "w", Warning: "careful"}},
				Runtime:  runtime,
			}, nil
		}})
	if _, err := loader.LoadProjectTrustExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.IsProjectTrusted() {
		t.Error("LoadProjectTrustExtensions left the project trusted; its bootstrap pass keeps the settings untrusted")
	}
	manager.SetProjectTrusted(true)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	got := loader.GetExtensions()
	if len(got.Errors) != 1 || got.Errors[0].Error != "boom" || len(got.Warnings) != 1 || got.Warnings[0].Warning != "careful" || got.Runtime != runtime {
		t.Fatalf("GetExtensions = errors %+v warnings %+v runtime %p, want the load's", got.Errors, got.Warnings, got.Runtime)
	}
	if len(got.Extensions) != 1 {
		t.Fatalf("extensions = %d, want 1", len(got.Extensions))
	}
	user := got.Extensions[0]
	if user.SourceInfo.Scope != "user" || user.SourceInfo.Path != userExt || user.Tools["t"].SourceInfo != user.SourceInfo {
		t.Errorf("user extension source info %+v, its tool %+v, want the path's source info on both", user.SourceInfo, user.Tools["t"].SourceInfo)
	}
}

// resource-loader.ts extendResources and findSourceInfoForPath: a path recorded by an extension names the source of everything under it (a directory, not a name prefix), a metadata baseDir resolves against the cwd, a later call adds to the earlier paths of every collection, and a theme keeps source info it already has.
// Pi: packages/coding-agent/src/core/resource-loader.ts extendResources, findSourceInfoForPath, updateThemesFromPaths.
func TestExtendResourcesRecordsDirectorySourcesAccumulatesAndKeepsThemeSourceInfo(t *testing.T) {
	f := newResourceLoaderFixture(t)
	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	root := t.TempDir()
	dirA := filepath.Join(root, "ext")
	dirB := filepath.Join(root, "ext-b")
	writeTheme(t, filepath.Join(dirA, "a.json"), "theme-a")
	writeTheme(t, filepath.Join(dirB, "b.json"), "theme-b")
	metaA := icodingagent.PathMetadata{Source: "extension:a", Scope: "temporary", Origin: "top-level", BaseDir: "relative-base"}
	metaB := icodingagent.PathMetadata{Source: "extension:b", Scope: "temporary", Origin: "top-level"}
	if err := loader.ExtendResources(ResourceExtensionPaths{ThemePaths: []ExtensionResourcePath{{Path: dirA, Metadata: metaA}}}); err != nil {
		t.Fatal(err)
	}
	if err := loader.ExtendResources(ResourceExtensionPaths{ThemePaths: []ExtensionResourcePath{{Path: filepath.Join(dirB, "b.json"), Metadata: metaB}}}); err != nil {
		t.Fatal(err)
	}
	a := themeSourceInfo(t, themeNamed(t, loader, "theme-a"))
	if a.Source != "extension:a" || a.BaseDir != filepath.Join(f.cwd, "relative-base") {
		t.Errorf("theme-a source info = %+v, want extension:a with its baseDir resolved against the cwd", a)
	}
	b := themeSourceInfo(t, themeNamed(t, loader, "theme-b"))
	if b.Source != "extension:b" {
		t.Errorf("theme-b source info = %+v, want its own recorded path, not the sibling directory whose name is its prefix", b)
	}
	if !hasTheme(loader, "theme-a") {
		t.Error("the second ExtendResources dropped the first call's theme paths")
	}

	promptsA, promptsB := filepath.Join(root, "pa"), filepath.Join(root, "pb")
	writePrompt(t, filepath.Join(promptsA, "first.md"), "first prompt")
	writePrompt(t, filepath.Join(promptsB, "second.md"), "second prompt")
	for _, dir := range []string{promptsA, promptsB} {
		if err := loader.ExtendResources(ResourceExtensionPaths{PromptPaths: []ExtensionResourcePath{{Path: dir, Metadata: metaB}}}); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, prompt := range loader.GetPrompts().Prompts {
		names = append(names, prompt.Name)
	}
	slices.Sort(names)
	if !slices.Contains(names, "first") || !slices.Contains(names, "second") {
		t.Errorf("prompts = %v, want both calls' prompts", names)
	}
}

// resource-loader.ts updateThemesFromPaths stamps a theme's sourcePath with the recorded source, a synthetic <...> path with its default source info, and leaves a theme that already has source info and no recorded source alone.
func TestThemesOverrideSourceInfoKeepsExistingAndStampsSyntheticPaths(t *testing.T) {
	f := newResourceLoaderFixture(t)
	recorded := t.TempDir()
	existing := &icodingagent.PiSourceInfo{Path: "/kept", Source: "kept", Scope: "temporary", Origin: "top-level"}
	loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
		o.ThemesOverride = func(result ThemesResult) ThemesResult {
			kept := &tui.Theme{Name: "kept", SourcePath: filepath.Join(os.TempDir(), "kept-theme.json"), SourceInfo: existing}
			synthetic := &tui.Theme{Name: "synthetic", SourcePath: "<inline:theme>"}
			weird := &tui.Theme{Name: "weird", SourcePath: "<not-synthetic"}
			closing := &tui.Theme{Name: "closing", SourcePath: filepath.Join(recorded, "theme>")}
			result.Themes = []*tui.Theme{kept, synthetic, weird, closing}
			return result
		}
	})
	if got := themeSourceInfo(t, themeNamed(t, loader, "kept")); got != *existing {
		t.Errorf("kept theme source info = %+v, want the existing one", got)
	}
	if got := themeSourceInfo(t, themeNamed(t, loader, "synthetic")); got != icodingagent.DefaultSourceInfoForPath(f.cwd, f.agentDir, "<inline:theme>") {
		t.Errorf("synthetic theme source info = %+v", got)
	}
	if got := themeSourceInfo(t, themeNamed(t, loader, "weird")); got.Path != "<not-synthetic" {
		t.Errorf("a path with only an opening bracket is a real path: %+v", got)
	}
	// A path that only ends in ">" is a real path: the source an extension recorded for its directory applies.
	if err := loader.ExtendResources(ResourceExtensionPaths{ThemePaths: []ExtensionResourcePath{{Path: recorded, Metadata: icodingagent.PathMetadata{Source: "extension:z", Scope: "temporary", Origin: "top-level"}}}}); err != nil {
		t.Fatal(err)
	}
	if got := themeSourceInfo(t, themeNamed(t, loader, "closing")); got.Source != "extension:z" {
		t.Errorf("closing-bracket theme source info = %+v, want the recorded extension source", got)
	}
}
