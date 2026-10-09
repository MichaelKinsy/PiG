package coding

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports the themes, extensions and extendResources cases of packages/coding-agent/test/resource-loader.test.ts (upstream 1.0.4).

func writeTheme(t *testing.T, path, name string) {
	t.Helper()
	dark, err := os.ReadFile(filepath.Join("..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(dark, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = name
	encoded, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	writeResource(t, path, string(encoded))
}

func themeNamed(t *testing.T, loader *DefaultResourceLoader, name string) *tui.Theme {
	t.Helper()
	for _, theme := range loader.GetThemes().Themes {
		if theme.Name == name {
			return theme
		}
	}
	t.Fatalf("no theme %q in %+v", name, loader.GetThemes())
	return nil
}

// themeSourceInfo is theme.sourceInfo (resource-loader.test.ts:829,839), which the loader sets on every theme with a sourcePath.
func themeSourceInfo(t *testing.T, theme *tui.Theme) icodingagent.PiSourceInfo {
	t.Helper()
	if theme == nil || theme.SourceInfo == nil {
		t.Fatalf("theme %+v has no sourceInfo", theme)
	}
	return *theme.SourceInfo
}

func hasTheme(loader *DefaultResourceLoader, name string) bool {
	return slices.ContainsFunc(loader.GetThemes().Themes, func(theme *tui.Theme) bool { return theme.Name == name })
}

// resource-loader.test.ts:36 "should initialize with empty results before reload".
func TestDefaultResourceLoaderStartsWithoutThemesAndExtensions(t *testing.T) {
	f := newResourceLoaderFixture(t)
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir})
	if got := loader.GetExtensions(); len(got.Extensions) != 0 || len(got.Errors) != 0 || len(got.Warnings) != 0 {
		t.Errorf("extensions = %+v, want none", got)
	}
	if got := loader.GetThemes(); len(got.Themes) != 0 || len(got.Diagnostics) != 0 {
		t.Errorf("themes = %+v, want none", got)
	}
	if len(loader.GetSkills().Skills) != 0 || len(loader.GetPrompts().Prompts) != 0 {
		t.Error("skills or prompts before reload")
	}
}

// resource-loader.test.ts:225 "should prefer project resources": the project theme wins a name collision with the user theme and keeps its source path.
func TestDefaultResourceLoaderThemesPreferProjectOverUser(t *testing.T) {
	f := newResourceLoaderFixture(t)
	userTheme := filepath.Join(f.agentDir, "themes", "collision.json")
	projectTheme := filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "themes", "collision.json")
	writeTheme(t, userTheme, "collision-theme")
	writeTheme(t, projectTheme, "collision-theme")

	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	theme := themeNamed(t, loader, "collision-theme")
	if theme.SourcePath != projectTheme {
		t.Errorf("sourcePath = %q, want the project theme %q", theme.SourcePath, projectTheme)
	}
	if want := (icodingagent.PiSourceInfo{Path: projectTheme, Source: "auto", Scope: "project", Origin: "top-level", BaseDir: filepath.Dir(filepath.Dir(projectTheme))}); themeSourceInfo(t, theme) != want {
		t.Errorf("sourceInfo = %+v, want %+v", theme.SourceInfo, want)
	}
	var collisions int
	for _, diagnostic := range loader.GetThemes().Diagnostics {
		if diagnostic.Collision != nil && diagnostic.Collision.WinnerPath == projectTheme && diagnostic.Collision.LoserPath == userTheme {
			collisions++
		}
	}
	if collisions != 1 {
		t.Errorf("collision diagnostics = %+v, want the user theme to lose once", loader.GetThemes().Diagnostics)
	}
}

// resource-loader.test.ts:515 "should not load project resources when untrusted": the project theme stays out, the user theme loads.
func TestDefaultResourceLoaderThemesFollowProjectTrust(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeTheme(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "themes", "project.json"), "project-theme")
	writeTheme(t, filepath.Join(f.agentDir, "themes", "user.json"), "user-theme")

	untrusted := loaderFor(t, f, icodingagent.Settings{}, false, nil)
	if hasTheme(untrusted, "project-theme") || !hasTheme(untrusted, "user-theme") {
		t.Errorf("untrusted themes = %+v", untrusted.GetThemes().Themes)
	}
	trusted := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	if !hasTheme(trusted, "project-theme") || !hasTheme(trusted, "user-theme") {
		t.Errorf("trusted themes = %+v", trusted.GetThemes().Themes)
	}
}

// resource-loader.ts reload: noThemes keeps only the additional theme paths; a missing one reports a warning from the theme loader and no second diagnostic for the same path.
func TestDefaultResourceLoaderNoThemesKeepsAdditionalThemePaths(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeTheme(t, filepath.Join(f.agentDir, "themes", "user.json"), "user-theme")
	extra := filepath.Join(t.TempDir(), "extra.json")
	writeTheme(t, extra, "extra-theme")
	missing := filepath.Join(t.TempDir(), "missing.json")

	loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
		o.NoThemes = true
		o.AdditionalThemePaths = []string{extra, missing}
	})
	if hasTheme(loader, "user-theme") || !hasTheme(loader, "extra-theme") {
		t.Errorf("themes = %+v", loader.GetThemes().Themes)
	}
	var missingDiagnostics []extension.ResourceDiagnostic
	for _, diagnostic := range loader.GetThemes().Diagnostics {
		if diagnostic.Path == missing {
			missingDiagnostics = append(missingDiagnostics, diagnostic)
		}
	}
	if len(missingDiagnostics) != 1 || missingDiagnostics[0].Message != "theme path does not exist" {
		t.Errorf("missing-path diagnostics = %+v, want the theme loader's one warning", missingDiagnostics)
	}
}

// ThemesOverride transforms the loaded themes before the loader stamps their source info (resource-loader.ts themesOverride).
func TestDefaultResourceLoaderThemesOverride(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeTheme(t, filepath.Join(f.agentDir, "themes", "user.json"), "user-theme")
	loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
		o.ThemesOverride = func(result ThemesResult) ThemesResult {
			result.Themes = result.Themes[:0:0]
			return result
		}
	})
	if got := loader.GetThemes().Themes; len(got) != 0 {
		t.Errorf("themes = %+v, want the override's empty set", got)
	}
}

// resource-loader.test.ts:640 "should load skills and prompts with extension metadata".
func TestExtendResourcesLoadsSkillsAndPromptsWithExtensionMetadata(t *testing.T) {
	f := newResourceLoaderFixture(t)
	extra := t.TempDir()
	skillDir := filepath.Join(extra, "extra-skills", "extra-skill")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	writeResource(t, skillPath, "---\nname: extra-skill\ndescription: Extra skill\n---\nExtra content")
	promptDir := filepath.Join(extra, "extra-prompts")
	promptPath := filepath.Join(promptDir, "extra.md")
	writeResource(t, promptPath, "---\ndescription: Extra prompt\n---\nExtra prompt content")

	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	if err := loader.ExtendResources(ResourceExtensionPaths{
		SkillPaths:  []ExtensionResourcePath{{Path: skillDir, Metadata: icodingagent.PathMetadata{Source: "extension:extra", Scope: "temporary", Origin: "top-level", BaseDir: skillDir}}},
		PromptPaths: []ExtensionResourcePath{{Path: promptPath, Metadata: icodingagent.PathMetadata{Source: "extension:extra", Scope: "temporary", Origin: "top-level", BaseDir: promptDir}}},
	}); err != nil {
		t.Fatal(err)
	}
	skill := skillNamed(t, loader, "extra-skill")
	if skill.SourceInfo.Source != "extension:extra" || skill.SourceInfo.Path != skillPath {
		t.Errorf("skill sourceInfo = %+v, want source extension:extra at %s", skill.SourceInfo, skillPath)
	}
	prompt := promptNamed(t, loader, "extra")
	if prompt.SourceInfo.Source != "extension:extra" || prompt.SourceInfo.Path != promptPath {
		t.Errorf("prompt sourceInfo = %+v, want source extension:extra at %s", prompt.SourceInfo, promptPath)
	}
}

// resource-loader.test.ts:702 "should load extension resources returned as file URLs".
func TestExtendResourcesResolvesFileURLs(t *testing.T) {
	f := newResourceLoaderFixture(t)
	skillDir := filepath.Join(t.TempDir(), "extra skills", "file-url-skill")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	writeResource(t, skillPath, "---\nname: file-url-skill\ndescription: File URL skill\n---\nExtra content")

	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	href := (&url.URL{Scheme: "file", Path: filepath.ToSlash(skillDir)}).String()
	if err := loader.ExtendResources(ResourceExtensionPaths{SkillPaths: []ExtensionResourcePath{{Path: href, Metadata: icodingagent.PathMetadata{Source: "extension:file-url", Scope: "temporary", Origin: "top-level", BaseDir: skillDir}}}}); err != nil {
		t.Fatal(err)
	}
	if diagnostics := loader.GetSkills().Diagnostics; len(diagnostics) != 0 {
		t.Errorf("diagnostics = %+v", diagnostics)
	}
	skill := skillNamed(t, loader, "file-url-skill")
	if skill.FilePath != skillPath || skill.SourceInfo.Source != "extension:file-url" {
		t.Errorf("skill = %s %+v", skill.FilePath, skill.SourceInfo)
	}
}

// resource-loader.test.ts:745 "should keep package metadata for skills, prompts, and themes": extending the collections keeps the Package metadata of what a reload found and stamps the extension's resources with its own.
func TestExtendResourcesKeepsPackageMetadata(t *testing.T) {
	f := newResourceLoaderFixture(t)
	root := filepath.Join(f.agentDir, "npm", "node_modules", "metadata-pkg")
	writeResource(t, filepath.Join(root, "package.json"), `{"name":"metadata-pkg","version":"1.0.0"}`)
	writeResource(t, filepath.Join(root, "skills", "package-skill", "SKILL.md"), "---\nname: package-skill\ndescription: Package skill\n---\nPackage skill content")
	writeResource(t, filepath.Join(root, "prompts", "package-prompt.md"), "---\ndescription: Package prompt\n---\nPackage prompt content")
	writeTheme(t, filepath.Join(root, "themes", "package-theme.json"), "package-theme")

	resources := t.TempDir()
	extensionSkill := filepath.Join(resources, "extension-skill")
	extensionPrompts := filepath.Join(resources, "prompts")
	extensionThemes := filepath.Join(resources, "themes")
	writeResource(t, filepath.Join(extensionSkill, "SKILL.md"), "---\nname: extension-skill\ndescription: Extension skill\n---\nExtension skill content")
	writeResource(t, filepath.Join(extensionPrompts, "extension-prompt.md"), "---\ndescription: Extension prompt\n---\nExtension prompt content")
	writeTheme(t, filepath.Join(extensionThemes, "extension.json"), "extension-theme")

	loader := loaderFor(t, f, icodingagent.Settings{Packages: []icodingagent.PackageSource{{Source: "npm:metadata-pkg"}}}, true, nil)
	metadata := icodingagent.PathMetadata{Source: "extension:discovery", Scope: "temporary", Origin: "top-level"}
	if err := loader.ExtendResources(ResourceExtensionPaths{
		SkillPaths:  []ExtensionResourcePath{{Path: extensionSkill, Metadata: metadata}},
		PromptPaths: []ExtensionResourcePath{{Path: extensionPrompts, Metadata: metadata}},
		ThemePaths:  []ExtensionResourcePath{{Path: extensionThemes, Metadata: metadata}},
	}); err != nil {
		t.Fatal(err)
	}

	pkg := func(info icodingagent.PiSourceInfo) bool {
		return info.Source == "npm:metadata-pkg" && info.Scope == "user" && info.Origin == "package"
	}
	ext := func(info icodingagent.PiSourceInfo) bool {
		return info.Source == "extension:discovery" && info.Scope == "temporary" && info.Origin == "top-level"
	}
	if info := skillNamed(t, loader, "package-skill").SourceInfo; !pkg(info) {
		t.Errorf("package skill sourceInfo = %+v", info)
	}
	if info := promptNamed(t, loader, "package-prompt").SourceInfo; !pkg(info) {
		t.Errorf("package prompt sourceInfo = %+v", info)
	}
	if info := themeSourceInfo(t, themeNamed(t, loader, "package-theme")); !pkg(info) {
		t.Errorf("package theme sourceInfo = %+v", info)
	}
	// resource-loader.test.ts:829,839 read theme.sourceInfo on the Theme itself.
	for name, matches := range map[string]func(icodingagent.PiSourceInfo) bool{"package-theme": pkg, "extension-theme": ext} {
		theme := themeNamed(t, loader, name)
		if theme.SourceInfo == nil || !matches(*theme.SourceInfo) {
			t.Errorf("%s Theme.SourceInfo = %+v", name, theme.SourceInfo)
		}
	}
	if info := skillNamed(t, loader, "extension-skill").SourceInfo; !ext(info) {
		t.Errorf("extension skill sourceInfo = %+v", info)
	}
	if info := promptNamed(t, loader, "extension-prompt").SourceInfo; !ext(info) {
		t.Errorf("extension prompt sourceInfo = %+v", info)
	}
	if info := themeSourceInfo(t, themeNamed(t, loader, "extension-theme")); !ext(info) {
		t.Errorf("extension theme sourceInfo = %+v", info)
	}
}

// extendResources keeps the paths of the last reload, so extending twice accumulates, and an empty collection leaves the others as they were (resource-loader.ts:461-499).
func TestExtendResourcesAccumulatesAndKeepsUntouchedCollections(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	writeSkill(t, first, "first-skill")
	writeSkill(t, second, "second-skill")
	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	prompts := loader.GetPrompts()
	themesBefore := loader.GetThemes()
	extend := func(dir string) {
		t.Helper()
		if err := loader.ExtendResources(ResourceExtensionPaths{SkillPaths: []ExtensionResourcePath{{Path: dir, Metadata: icodingagent.PathMetadata{Source: "extension:acc", Scope: "temporary", Origin: "top-level"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	extend(filepath.Join(first, "first-skill"))
	extend(filepath.Join(second, "second-skill"))
	if got := skillNames(loader); !slices.Equal(got, []string{"first-skill", "second-skill", "user-skill"}) {
		t.Errorf("skills = %v, want the reload's skill and both extensions' skills", got)
	}
	if got := loader.GetPrompts(); len(got.Prompts) != len(prompts.Prompts) || len(got.Diagnostics) != len(prompts.Diagnostics) {
		t.Errorf("prompts changed: %+v -> %+v", prompts, got)
	}
	if got := loader.GetThemes(); len(got.Themes) != len(themesBefore.Themes) {
		t.Errorf("themes changed: %+v -> %+v", themesBefore, got)
	}
	// A reload drops the extensions' source infos and paths.
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := skillNames(loader); !slices.Equal(got, []string{"user-skill"}) {
		t.Errorf("skills after reload = %v, want only the discovered skill", got)
	}
}

// extendResources throws when fileURLToPath rejects a path (utils/paths.ts:95-106); the call reports it and changes nothing.
func TestExtendResourcesReportsAnUnresolvablePath(t *testing.T) {
	f := newResourceLoaderFixture(t)
	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	if err := loader.ExtendResources(ResourceExtensionPaths{SkillPaths: []ExtensionResourcePath{{Path: "file:///%ZZ"}}}); err == nil {
		t.Fatal("ExtendResources succeeded for a malformed file URL")
	}
}

// resource-loader.test.ts:290 "should reload extensions once after project trust resolves": the bootstrap pass sees the project untrusted and its result reaches the trust callback; the final pass gets that result and the loader applies the answer.
func TestReloadResolvesProjectTrustAfterABootstrapExtensionLoad(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeTheme(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "themes", "project.json"), "project-theme")
	manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{})
	manager.SetProjectTrusted(true)

	var requests []ExtensionLoadRequest
	var trustedDuringBootstrap, trustedAtFinal []bool
	bootstrap := LoadExtensionsResult{Extensions: []extension.Extension{{Path: filepath.Join(f.agentDir, "extensions", "user.ts")}}}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager,
		LoadExtensions: func(_ context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
			requests = append(requests, request)
			if request.Bootstrap {
				trustedDuringBootstrap = append(trustedDuringBootstrap, manager.IsProjectTrusted())
				return bootstrap, nil
			}
			trustedAtFinal = append(trustedAtFinal, manager.IsProjectTrusted())
			final := LoadExtensionsResult{Extensions: []extension.Extension{{Path: filepath.Join(f.cwd, ".pi", "extensions", "project.ts")}}}
			final.Extensions = append(final.Extensions, request.PreTrust.Extensions...)
			return final, nil
		},
	})
	var seen LoadExtensionsResult
	err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(_ context.Context, input ResolveProjectTrustInput) (bool, error) {
		seen = input.ExtensionsResult
		return true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen.Extensions) != 1 || seen.Extensions[0].Path != bootstrap.Extensions[0].Path {
		t.Errorf("trust callback saw %+v, want the bootstrap set", seen)
	}
	if len(requests) != 2 || !requests[0].Bootstrap || requests[0].PreTrust != nil || requests[1].Bootstrap || requests[1].PreTrust == nil {
		t.Fatalf("requests = %+v, want a bootstrap pass then a final pass carrying its result", requests)
	}
	if !slices.Equal(trustedDuringBootstrap, []bool{false}) || !slices.Equal(trustedAtFinal, []bool{true}) {
		t.Errorf("project trust during bootstrap %v and final %v, want false then true", trustedDuringBootstrap, trustedAtFinal)
	}
	var paths []string
	for _, ext := range loader.GetExtensions().Extensions {
		paths = append(paths, ext.Path)
	}
	want := []string{filepath.Join(f.cwd, ".pi", "extensions", "project.ts"), filepath.Join(f.agentDir, "extensions", "user.ts")}
	if !slices.Equal(paths, want) {
		t.Errorf("extensions = %v, want %v", paths, want)
	}
	if !hasTheme(loader, "project-theme") {
		t.Error("the project theme did not load after trust resolved")
	}

	// A denied project keeps its themes out.
	err = loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if hasTheme(loader, "project-theme") {
		t.Error("the project theme loaded although trust was denied")
	}
}

// A failed trust decision or bootstrap load fails the reload, keeps the previous results and leaves the project untrusted as the bootstrap pass left it (resource-loader.ts:517-523).
func TestReloadStopsWhenTheBootstrapLoadOrTheTrustDecisionFails(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{})
	manager.SetProjectTrusted(true)
	bootstrapErr := errors.New("bootstrap failed")
	failBootstrap := false
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager,
		LoadExtensions: func(_ context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
			if request.Bootstrap && failBootstrap {
				return LoadExtensionsResult{}, bootstrapErr
			}
			return LoadExtensionsResult{}, nil
		}})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	trust := ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) { return true, os.ErrPermission }}
	if err := loader.Reload(trust); !errors.Is(err, os.ErrPermission) {
		t.Errorf("err = %v, want the trust decision's error", err)
	}
	failBootstrap = true
	if err := loader.Reload(trust); !errors.Is(err, bootstrapErr) {
		t.Errorf("err = %v, want the bootstrap error", err)
	}
	if got := skillNames(loader); !slices.Equal(got, []string{"user-skill"}) {
		t.Errorf("skills = %v, want the previous results kept", got)
	}
	if manager.IsProjectTrusted() {
		t.Error("project still trusted after a bootstrap pass")
	}
}

// LoadProjectTrustExtensions forces the project untrusted and returns the bootstrap set (resource-loader.ts:501-507); ExtensionsOverride and the loader's source info apply to the final set (resource-loader.ts:578-579).
func TestLoadProjectTrustExtensionsAndExtensionsOverride(t *testing.T) {
	f := newResourceLoaderFixture(t)
	manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{})
	manager.SetProjectTrusted(true)
	userExt := filepath.Join(f.agentDir, "extensions", "user.ts")
	var bootstrapSeen bool
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager,
		LoadExtensions: func(_ context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
			bootstrapSeen = bootstrapSeen || (request.Bootstrap && !manager.IsProjectTrusted())
			return LoadExtensionsResult{Extensions: []extension.Extension{{Path: userExt, Commands: map[string]extension.RegisteredCommand{"hello": {}}}}}, nil
		},
		ExtensionsOverride: func(result LoadExtensionsResult) LoadExtensionsResult {
			result.Errors = append(result.Errors, ExtensionLoadError{Path: userExt, Error: "overridden"})
			return result
		}})
	got, err := loader.LoadProjectTrustExtensions(context.Background())
	if err != nil || len(got.Extensions) != 1 || !bootstrapSeen {
		t.Fatalf("LoadProjectTrustExtensions = %+v, %v (untrusted bootstrap %v)", got, err, bootstrapSeen)
	}
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	final := loader.GetExtensions()
	if len(final.Errors) != 1 || final.Errors[0].Error != "overridden" {
		t.Errorf("errors = %+v, want the override's", final.Errors)
	}
	ext := final.Extensions[0]
	want := icodingagent.PiSourceInfo{Path: userExt, Source: "local", Scope: "user", Origin: "top-level", BaseDir: filepath.Join(f.agentDir, "extensions")}
	if ext.SourceInfo != want || ext.Commands["hello"].SourceInfo != want {
		t.Errorf("source info = %+v, command %+v, want %+v on both", ext.SourceInfo, ext.Commands["hello"].SourceInfo, want)
	}
}

// resource-loader.ts:153-165 ResourceLoader declares getExtensions, getThemes, extendResources and reload next to the skill, prompt and system-prompt getters: a consumer holding the interface extends and reloads the loader, and a loader that only holds resolved collections refuses both instead of ignoring them.
func TestResourceLoaderInterfaceExtendsAndReloads(t *testing.T) {
	f := newResourceLoaderFixture(t)
	skillDir := filepath.Join(t.TempDir(), "extra-skills", "iface-skill")
	writeResource(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: iface-skill\ndescription: Interface skill\n---\nContent")
	var loader ResourceLoader = loaderFor(t, f, icodingagent.Settings{}, true, nil)
	if err := loader.ExtendResources(ResourceExtensionPaths{SkillPaths: []ExtensionResourcePath{{Path: skillDir, Metadata: icodingagent.PathMetadata{Source: "extension:iface", Scope: "temporary", Origin: "top-level", BaseDir: skillDir}}}}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(loader.GetSkills().Skills, func(s *Skill) bool { return s.Name == "iface-skill" }) {
		t.Fatalf("skills after ExtendResources = %+v", loader.GetSkills().Skills)
	}
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(loader.GetSkills().Skills, func(s *Skill) bool { return s.Name == "iface-skill" }) {
		t.Fatal("a reload kept the temporary extension skill")
	}
	if loader.GetExtensions().Extensions == nil || loader.GetThemes().Themes == nil {
		t.Fatal("a reloaded loader reports nil extension or theme collections, which Pi never returns")
	}

	var static ResourceLoader = &staticResourceLoader{}
	if err := static.ExtendResources(ResourceExtensionPaths{}); !errors.Is(err, errStaticResourceLoader) {
		t.Errorf("static ExtendResources error = %v", err)
	}
	if err := static.Reload(); !errors.Is(err, errStaticResourceLoader) {
		t.Errorf("static Reload error = %v", err)
	}
}
