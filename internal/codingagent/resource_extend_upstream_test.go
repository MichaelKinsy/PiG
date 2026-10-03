package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func extensionResourceMode(t *testing.T, cwd, extensionPath string, result extension.ResourcesDiscoverResult) *InteractiveMode {
	t.Helper()
	return &InteractiveMode{
		opts:  InteractiveOptions{CWD: cwd, AgentDir: t.TempDir()},
		agent: agent.NewAgent(agent.AgentOptions{}),
		newRunner: inproc.NewRunner([]extension.Extension{{
			Path: extensionPath,
			Handlers: map[string][]extension.HandlerFn{
				EventResourcesDiscover: {func(...any) (any, error) { return &result, nil }},
			},
		}}, cwd),
	}
}

// upstreamThemeNamed returns upstream's dark.json under another name, as the upstream tests build their themes.
func upstreamThemeNamed(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".upstream", "current", "packages", "coding-agent", "src", "modes", "interactive", "theme", "dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = name
	out, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func writeResourceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:580 ("should load
// skills and prompts with extension metadata"). An extension's resources_discover
// paths are the production input of DefaultResourceLoader.extendResources; the
// extension path supplies the "extension:<name>" source that the test passes as
// explicit metadata.
func TestUpstreamExtendResourcesLoadsSkillsAndPromptsWithExtensionMetadata(t *testing.T) {
	root := t.TempDir()
	extraSkillDir := filepath.Join(root, "extra-skills", "extra-skill")
	skillPath := filepath.Join(extraSkillDir, "SKILL.md")
	extraPromptDir := filepath.Join(root, "extra-prompts")
	promptPath := filepath.Join(extraPromptDir, "extra.md")
	writeResourceFile(t, skillPath, "---\nname: extra-skill\ndescription: Extra skill\n---\nExtra content")
	writeResourceFile(t, promptPath, "---\ndescription: Extra prompt\n---\nExtra prompt content")

	m := extensionResourceMode(t, root, filepath.Join(root, "extensions", "extra.ts"), extension.ResourcesDiscoverResult{SkillPaths: []string{extraSkillDir}, PromptPaths: []string{promptPath}})
	if err := m.extendResourcesFromExtensions(t.Context(), "startup"); err != nil {
		t.Fatal(err)
	}

	i := slices.IndexFunc(m.opts.Skills, func(s *SkillDef) bool { return s.Name == "extra-skill" })
	if i < 0 {
		t.Fatalf("extra-skill not loaded: %#v", m.opts.Skills)
	}
	info := m.loadedResourceFor(m.opts.Skills[i].Path, "skills").info
	if info.Source != "extension:extra" || info.Path != skillPath || info.Scope != "temporary" || info.Origin != "top-level" {
		t.Fatalf("skill source info = %+v, want extension:extra at %s", info, skillPath)
	}
	j := slices.IndexFunc(m.promptTemplates, func(p PromptTemplate) bool { return p.Name == "extra" })
	if j < 0 {
		t.Fatalf("extra prompt not loaded: %#v", m.promptTemplates)
	}
	info = m.loadedResourceFor(m.promptTemplates[j].FilePath, "prompts").info
	if info.Source != "extension:extra" || info.Path != promptPath {
		t.Fatalf("prompt source info = %+v, want extension:extra at %s", info, promptPath)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:645 ("should load
// extension resources returned as file URLs"): a file: URL (with an encoded
// space) resolves to the local skill directory, loads without diagnostics and
// keeps the extension source.
func TestUpstreamExtendResourcesLoadsFileURLs(t *testing.T) {
	root := t.TempDir()
	extraSkillDir := filepath.Join(root, "extra skills", "file-url-skill")
	skillPath := filepath.Join(extraSkillDir, "SKILL.md")
	writeResourceFile(t, skillPath, "---\nname: file-url-skill\ndescription: File URL skill\n---\nExtra content")
	fileURL := fileURLForTest(extraSkillDir).String()

	m := extensionResourceMode(t, root, filepath.Join(root, "extensions", "file-url.ts"), extension.ResourcesDiscoverResult{SkillPaths: []string{fileURL}})
	if err := m.extendResourcesFromExtensions(t.Context(), "startup"); err != nil {
		t.Fatal(err)
	}
	if len(m.opts.SkillDiagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", m.opts.SkillDiagnostics)
	}
	i := slices.IndexFunc(m.opts.Skills, func(s *SkillDef) bool { return s.Name == "file-url-skill" })
	if i < 0 {
		t.Fatalf("file-url-skill not loaded: %#v (paths %q)", m.opts.Skills, m.opts.SkillPaths)
	}
	if got := m.opts.Skills[i].Path; got != skillPath {
		t.Fatalf("skill path = %q, want %q", got, skillPath)
	}
	if info := m.loadedResourceFor(skillPath, "skills").info; info.Source != "extension:file-url" {
		t.Fatalf("source info = %+v", info)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:685 (issue #6968:
// extension discovery used to drop package scope/source), the extension half:
// resources an extension adds keep the extension metadata while package
// resources already recorded keep npm source, user scope and package origin.
// The package half runs in cmd/pig (TestUpstreamResourceLoaderPackageMetadata).
func TestUpstreamExtendResourcesKeepPackageMetadata(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "npm", "node_modules", "metadata-pkg")
	packageSkill := filepath.Join(packageRoot, "skills", "package-skill", "SKILL.md")
	packagePrompt := filepath.Join(packageRoot, "prompts", "package-prompt.md")
	packageTheme := filepath.Join(packageRoot, "themes", "package-theme.json")
	writeResourceFile(t, packageSkill, "---\nname: package-skill\ndescription: Package skill\n---\nPackage skill content")
	writeResourceFile(t, packagePrompt, "---\ndescription: Package prompt\n---\nPackage prompt content")
	writeResourceFile(t, packageTheme, upstreamThemeNamed(t, "package-theme"))
	extensionDir := filepath.Join(root, "extension-resources")
	extensionSkillDir := filepath.Join(extensionDir, "extension-skill")
	extensionPrompts := filepath.Join(extensionDir, "prompts")
	extensionThemes := filepath.Join(extensionDir, "themes")
	writeResourceFile(t, filepath.Join(extensionSkillDir, "SKILL.md"), "---\nname: extension-skill\ndescription: Extension skill\n---\nExtension skill content")
	writeResourceFile(t, filepath.Join(extensionPrompts, "extension-prompt.md"), "---\ndescription: Extension prompt\n---\nExtension prompt content")
	writeResourceFile(t, filepath.Join(extensionThemes, "extension.json"), upstreamThemeNamed(t, "extension-theme"))

	m := extensionResourceMode(t, root, filepath.Join(root, "extensions", "discovery.ts"), extension.ResourcesDiscoverResult{SkillPaths: []string{extensionSkillDir}, PromptPaths: []string{extensionPrompts}, ThemePaths: []string{extensionThemes}})
	m.opts.SkillPaths = []string{filepath.Dir(packageSkill)}
	m.opts.PromptPaths = []string{packagePrompt}
	m.opts.ThemePaths = []string{packageTheme}
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		filepath.Dir(packageSkill): {Path: packageSkill, ResourceType: "skills", Enabled: true, Scope: "user", Origin: "package", Source: "npm:metadata-pkg", BaseDir: packageRoot},
		packagePrompt:              {Path: packagePrompt, ResourceType: "prompts", Enabled: true, Scope: "user", Origin: "package", Source: "npm:metadata-pkg", BaseDir: packageRoot},
		packageTheme:               {Path: packageTheme, ResourceType: "themes", Enabled: true, Scope: "user", Origin: "package", Source: "npm:metadata-pkg", BaseDir: packageRoot},
	}
	if err := m.extendResourcesFromExtensions(t.Context(), "startup"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ kind, path string }{{"skills", packageSkill}, {"prompts", packagePrompt}, {"themes", packageTheme}} {
		info := m.loadedResourceFor(tc.path, tc.kind).info
		if info.Source != "npm:metadata-pkg" || info.Scope != "user" || info.Origin != "package" {
			t.Errorf("%s package info = %+v", tc.kind, info)
		}
	}
	for _, tc := range []struct{ kind, path string }{{"skills", filepath.Join(extensionSkillDir, "SKILL.md")}, {"prompts", filepath.Join(extensionPrompts, "extension-prompt.md")}, {"themes", filepath.Join(extensionThemes, "extension.json")}} {
		info := m.loadedResourceFor(tc.path, tc.kind).info
		if info.Source != "extension:discovery" || info.Scope != "temporary" || info.Origin != "top-level" {
			t.Errorf("%s extension info = %+v", tc.kind, info)
		}
	}
	if !slices.ContainsFunc(m.opts.Skills, func(s *SkillDef) bool { return s.Name == "package-skill" }) || !slices.ContainsFunc(m.opts.Skills, func(s *SkillDef) bool { return s.Name == "extension-skill" }) {
		t.Fatalf("skills = %#v", m.opts.Skills)
	}
}

// Ports the theme half of packages/coding-agent/test/resource-loader.test.ts:123
// ("should prefer project resources over user on name collisions"): theme paths
// arrive project first, and the first path's theme of a name owns it. The cmd/pig
// test TestUpstreamResourceLoaderProjectOverUserCollisions checks that order.
func TestThemePathsFirstPathWinsCollisions(t *testing.T) {
	root := t.TempDir()
	dark := upstreamThemeNamed(t, "collision-theme")
	userTheme, projectTheme := filepath.Join(root, "user", "collision.json"), filepath.Join(root, "project", "collision.json")
	for _, path := range []string{userTheme, projectTheme} {
		writeResourceFile(t, path, dark)
	}
	registry := tui.NewThemeRegistry()
	_, diagnostics := loadThemeResources(registry, []string{projectTheme, userTheme}, tui.GetTerminalColorMode())
	if len(diagnostics) != 1 || diagnostics[0].Type != extension.DiagnosticCollision {
		t.Fatalf("theme diagnostics = %+v, want one name collision", diagnostics)
	}
	if got := registry.PathOf("collision-theme"); got != projectTheme {
		t.Fatalf("theme source = %q, want project %q", got, projectTheme)
	}
}

// resource-loader.ts:341-343,658-670,865-867: extendResources resolves every
// extension-returned path with resolvePath, whose fileURLToPath throws for an
// invalid file URL, so the bind fails instead of dropping the path.
func TestExtendResourcesFromExtensionsRejectsInvalidFileURLs(t *testing.T) {
	root := t.TempDir()
	const invalid = "file:///a%2Fb"
	for name, result := range map[string]extension.ResourcesDiscoverResult{
		"skills":  {SkillPaths: []string{invalid}},
		"prompts": {PromptPaths: []string{invalid}},
		"themes":  {ThemePaths: []string{invalid}},
	} {
		t.Run(name, func(t *testing.T) {
			m := extensionResourceMode(t, root, filepath.Join(root, "extensions", "bad.ts"), result)
			err := m.extendResourcesFromExtensions(t.Context(), "startup")
			if err == nil {
				t.Fatal("invalid file URL was accepted")
			}
			// Pi's bindExtensions throws fileURLToPath's own error, so the fatal
			// "Failed to create session: <message>" of a rebind carries it bare.
			if err.Error() != invalidFileURLMessage() {
				t.Fatalf("error = %q, want the bare %q", err.Error(), invalidFileURLMessage())
			}
		})
	}
}

// paths.ts:75-106 resolvePath(p, cwd, { trim: true }) as applied to
// extension-discovered paths (NormalizeExtensionPaths).
func TestNormalizeExtensionPathsResolvePathTable(t *testing.T) {
	cwd := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	spaced := filepath.Join(cwd, "extra dir", "skill")
	resolve := func(input string) (string, error) {
		got, err := NormalizeExtensionPaths(cwd, &extension.ResourcesDiscoverAggregateResult{SkillPaths: []extension.AttributedResourcePath{{Path: input, ExtensionPath: "x.ts"}}})
		if err != nil {
			return "", err
		}
		return got.SkillPaths[0].Path, nil
	}
	for _, tc := range []struct {
		name, input, want string
		wantErr           bool
	}{
		{"trims and resolves relative to cwd", " rel/dir \n", filepath.Join(cwd, "rel", "dir"), false},
		{"expands ~", "~/x", filepath.Join(home, "x"), false},
		{"absolute", spaced, spaced, false},
		{"percent-encoded file URL", fileURLForTest(spaced).String(), spaced, false},
		{"encoded separator", "file:///a%2Fb", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.input)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("resolve(%q) = %q, %v; want %q (error %v)", tc.input, got, err, tc.want, tc.wantErr)
			}
		})
	}
	if runtime.GOOS == "windows" {
		for input, want := range map[string]string{"file:///C:/x/y": `C:\x\y`, "file://server/share/x": `\\server\share\x`} {
			if got, err := resolve(input); err != nil || got != want {
				t.Errorf("resolve(%q) = %q, %v; want %q", input, got, err, want)
			}
		}
	} else if _, err := resolve("file://host/x"); err == nil {
		t.Error("file://host/x was accepted on POSIX")
	}
}

// invalidFileURLMessage is Node's fileURLToPath text for file:///a%2Fb.
func invalidFileURLMessage() string {
	if runtime.GOOS == "windows" {
		return `File URL path must not include encoded \ or / characters`
	}
	return "File URL path must not include encoded / characters"
}

// interactive-mode.ts:6222-6255: /reload catches the error and shows
// `Reload failed: <error.message>` with the bare message.
func TestReloadFailureShowsPiText(t *testing.T) {
	root := t.TempDir()
	m := reloadTestMode(InteractiveOptions{CWD: root})
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Path: filepath.Join(root, "bad.ts"),
		Handlers: map[string][]extension.HandlerFn{
			EventResourcesDiscover: {func(...any) (any, error) {
				return &extension.ResourcesDiscoverResult{SkillPaths: []string{"file:///a%2Fb"}}, nil
			}},
		},
	}}, root)
	err := reloadHandler(m.buildSlashContext(t.Context()))
	if want := "Reload failed: " + invalidFileURLMessage(); err == nil || err.Error() != want {
		t.Fatalf("reload error = %v, want %q", err, want)
	}
}

// A snapshot error (an invalid settings file: URL, package-manager.ts:2340)
// fails /reload with `Reload failed: <error.message>`.
func TestReloadFailureFromSettingsSnapshotShowsPiText(t *testing.T) {
	m := reloadTestMode(InteractiveOptions{CWD: t.TempDir()})
	m.opts.SkillPaths = []string{"existing-skill"}
	m.opts.PromptPaths = []string{"existing-prompt"}
	m.opts.ReloadResourceProvider = func() ReloadResourceSnapshot {
		return ReloadResourceSnapshot{Err: errors.New(invalidFileURLMessage()), SkillPaths: []string{"replacement-skill"}, PromptPaths: []string{"replacement-prompt"}}
	}
	err := reloadHandler(m.buildSlashContext(t.Context()))
	if want := "Reload failed: " + invalidFileURLMessage(); err == nil || err.Error() != want {
		t.Fatalf("reload error = %v, want %q", err, want)
	}
	if !slices.Equal(m.opts.SkillPaths, []string{"existing-skill"}) || !slices.Equal(m.opts.PromptPaths, []string{"existing-prompt"}) {
		t.Fatalf("failed reload published replacement resources: skills %q, prompts %q", m.opts.SkillPaths, m.opts.PromptPaths)
	}
}
