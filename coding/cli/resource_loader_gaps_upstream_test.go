package cli

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

const upstreamDarkTheme = "../../.upstream/current/packages/coding-agent/src/modes/interactive/theme/dark.json"

func resourceLoaderFixture(t *testing.T) (root, cwd, agentDir string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("PIG_HOME", filepath.Join(root, "config"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	cwd, agentDir = filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, cwd, agentDir
}

// Ports packages/coding-agent/test/resource-loader.test.ts:123 ("should prefer
// project resources over user on name collisions") for prompts, skills and the
// order theme paths are handed to the theme registry (the registry itself is
// covered by TestThemePathsFirstPathWinsCollisions in internal/codingagent).
func TestUpstreamResourceLoaderProjectOverUserCollisions(t *testing.T) {
	_, cwd, agentDir := resourceLoaderFixture(t)
	projectConfig := filepath.Join(cwd, codingagent.CONFIG_DIR_NAME)
	userPrompt, projectPrompt := filepath.Join(agentDir, "prompts", "commit.md"), filepath.Join(projectConfig, "prompts", "commit.md")
	userSkill, projectSkill := filepath.Join(agentDir, "skills", "collision-skill", "SKILL.md"), filepath.Join(projectConfig, "skills", "collision-skill", "SKILL.md")
	darkTheme, err := os.ReadFile(upstreamDarkTheme)
	if err != nil {
		t.Fatal(err)
	}
	userTheme, projectTheme := filepath.Join(agentDir, "themes", "collision.json"), filepath.Join(projectConfig, "themes", "collision.json")
	for path, content := range map[string]string{
		userPrompt:    "User prompt",
		projectPrompt: "Project prompt",
		userSkill:     "---\nname: collision-skill\ndescription: user\n---\nUser skill",
		projectSkill:  "---\nname: collision-skill\ndescription: project\n---\nProject skill",
		userTheme:     string(darkTheme),
		projectTheme:  string(darkTheme),
	} {
		writePackageResource(t, path, content)
	}
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)

	prompts := codingagent.LoadPromptTemplates("", "", collectPromptPaths(cwd, agentDir, sm, Args{}, true)...)
	if i := slices.IndexFunc(prompts.Templates, func(p codingagent.PromptTemplate) bool { return p.Name == "commit" }); i < 0 || prompts.Templates[i].FilePath != projectPrompt {
		t.Fatalf("prompts = %#v, want commit from %s", prompts.Templates, projectPrompt)
	}

	skills, err := codingagent.LoadSkills(codingagent.LoadSkillsOptions{CWD: cwd, AgentDir: agentDir, SkillPaths: collectSkillInputs(cwd, agentDir, sm, Args{}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(skills.Skills, func(s *codingagent.SkillDef) bool { return s.Name == "collision-skill" }); i < 0 || skills.Skills[i].FilePath != projectSkill {
		t.Fatalf("skills = %#v, want collision-skill from %s", skills.Skills, projectSkill)
	}

	themePaths := collectThemePaths(cwd, agentDir, sm, Args{}, true)
	iProject, iUser := slices.Index(themePaths, projectTheme), slices.Index(themePaths, userTheme)
	if iProject < 0 || iUser < 0 || iProject > iUser {
		t.Fatalf("theme paths = %q, want project %s before user %s", themePaths, projectTheme, userTheme)
	}
}

// Ports the package half of packages/coding-agent/test/resource-loader.test.ts:685
// (issue #6968): package skills, prompts and themes keep source, scope and
// origin metadata. The extension-discovered half runs in internal/codingagent
// (TestUpstreamExtendResourcesKeepPackageMetadata).
func TestUpstreamResourceLoaderPackageMetadata(t *testing.T) {
	_, cwd, agentDir := resourceLoaderFixture(t)
	packageRoot := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "metadata-pkg")
	darkTheme, err := os.ReadFile(upstreamDarkTheme)
	if err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(packageRoot, "skills", "package-skill", "SKILL.md")
	promptPath := filepath.Join(packageRoot, "prompts", "package-prompt.md")
	themePath := filepath.Join(packageRoot, "themes", "package-theme.json")
	writePackageResource(t, filepath.Join(packageRoot, "package.json"), `{"name":"metadata-pkg","version":"1.0.0"}`)
	writePackageResource(t, skillPath, "---\nname: package-skill\ndescription: Package skill\n---\nPackage skill content")
	writePackageResource(t, promptPath, "---\ndescription: Package prompt\n---\nPackage prompt content")
	writePackageResource(t, themePath, string(darkTheme))
	writePackageResource(t, filepath.Join(agentDir, "settings.json"), `{"packages":["npm:metadata-pkg"]}`)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	infos := resourceSourceInfoProvider(cwd, agentDir, sm, Args{})()
	for _, tc := range []struct {
		path string
		kind tui.ResourceType
	}{{skillPath, tui.ResourceSkills}, {promptPath, tui.ResourcePrompts}, {themePath, tui.ResourceThemes}} {
		info, ok := infos[tc.path]
		if !ok {
			t.Fatalf("no source info for %s: %#v", tc.path, infos)
		}
		if info.Source != "npm:metadata-pkg" || info.Scope != "user" || info.Origin != "package" {
			t.Errorf("%s source info = %+v, want npm:metadata-pkg/user/package", tc.kind, info)
		}
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:645 for print, JSON
// and RPC mode: a file: URL returned by resources_discover (with an encoded
// space) loads the skill at its file path and records the extension source.
func TestUpstreamHeadlessExtendResourcesLoadsFileURLs(t *testing.T) {
	root, cwd, agentDir := resourceLoaderFixture(t)
	skillDir := filepath.Join(root, "extra skills", "file-url-skill")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	writePackageResource(t, skillPath, "---\nname: file-url-skill\ndescription: File URL skill\n---\nExtra content")
	urlPath := filepath.ToSlash(skillDir)
	if runtime.GOOS == "windows" {
		urlPath = "/" + urlPath
	}
	fileURL := (&url.URL{Scheme: "file", Path: urlPath}).String()
	loader := newCLIResourceLoader(cwd, agentDir)
	if err := startSessionWithLoader(t, cwd, agentDir, loader, []extension.Extension{{
		Path: filepath.Join(root, "extensions", "file-url.ts"),
		Handlers: map[string][]extension.HandlerFn{
			codingagent.EventResourcesDiscover: {func(...any) (any, error) {
				return &extension.ResourcesDiscoverResult{SkillPaths: []string{fileURL}}, nil
			}},
		},
	}}); err != nil {
		t.Fatalf("session_start discovery = %v", err)
	}
	skills := loader.GetSkills().Skills
	i := slices.IndexFunc(skills, func(s *codingagent.SkillDef) bool { return s.Name == "file-url-skill" })
	if i < 0 || skills[i].FilePath != skillPath {
		t.Fatalf("skills = %#v, want file-url-skill at %s", skills, skillPath)
	}
	if info, ok := loader.sourceInfo[skillDir]; !ok || info.Source != "extension:file-url" {
		t.Fatalf("source info = %#v", loader.sourceInfo)
	}
}

// resource-loader.ts:341-343,658-670: an invalid file URL from resources_discover
// makes fileURLToPath throw, so the headless bind fails for skills, prompts and
// themes alike (the prompt case used to be dropped silently).
func TestUpstreamHeadlessExtendResourcesRejectsInvalidFileURLs(t *testing.T) {
	root, cwd, agentDir := resourceLoaderFixture(t)
	const invalid = "file:///a%2Fb"
	for name, result := range map[string]extension.ResourcesDiscoverResult{
		"skills":  {SkillPaths: []string{invalid}},
		"prompts": {PromptPaths: []string{invalid}},
		"themes":  {ThemePaths: []string{invalid}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := startSessionWithLoader(t, cwd, agentDir, newCLIResourceLoader(cwd, agentDir), []extension.Extension{{
				Path: filepath.Join(root, "extensions", "bad.ts"),
				Handlers: map[string][]extension.HandlerFn{
					codingagent.EventResourcesDiscover: {func(...any) (any, error) { return &result, nil }},
				},
			}}); err == nil {
				t.Fatal("invalid file URL was accepted")
			}
		})
	}
}

// print-mode.ts:158-160: a throw while binding extensions prints error.message
// and exits 1 without sending a prompt. The message is Node's bare fileURLToPath text.
func TestPrintModeInvalidDiscoveredFileURLFailsBind(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	host := printModeTestHost(t, provider)
	host.Extensions = []extension.Extension{{Path: "bad-discover", Handlers: map[string][]extension.HandlerFn{
		codingagent.EventResourcesDiscover: {func(...any) (any, error) {
			return &extension.ResourcesDiscoverResult{PromptPaths: []string{"file:///a%2Fb"}}, nil
		}},
	}}}
	result := runPrintModeForTest(t, host, printModeOptions{Mode: "text", InitialMessage: "hello", Messages: []string{"again"}})
	if !errors.Is(result.err, errPrintModeHandled) {
		t.Fatalf("err = %v, want the handled failure exit", result.err)
	}
	if want := invalidFileURLMessage() + "\n"; result.stderr != want {
		t.Fatalf("stderr = %q, want %q", result.stderr, want)
	}
	if calls := provider.CallCount(); calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

// agent-session.ts bindExtensions extends the resource loader with what resources_discover returns before print-mode.ts sends its first prompt, and AgentSession.prompt expands a template the extension contributed. Probed with Pi 1.1.0 in print mode: `-p /dp` sends the discovered template's body.
// mutation-checked: a print-mode command catalog left as it was before session_start's discovery sends "/dp main.go" unexpanded.
func TestPrintAndJSONModeExpandATemplateDiscoveredAtStartup(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			template := filepath.Join(t.TempDir(), "dp.md")
			if err := os.WriteFile(template, []byte("---\ndescription: Discovered\n---\nDiscovered body: $ARGUMENTS"), 0o600); err != nil {
				t.Fatal(err)
			}
			captured := make(chan []ai.Message, 1)
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(context ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				captured <- context.Messages()
				return fauxTextResponse("done").AssistantMessage(), nil
			})})
			host := printModeTestHost(t, provider)
			host.Extensions = []extension.Extension{{Path: "discover", Handlers: map[string][]extension.HandlerFn{
				codingagent.EventResourcesDiscover: {func(...any) (any, error) {
					return &extension.ResourcesDiscoverResult{PromptPaths: []string{template}}, nil
				}},
			}}}
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "/dp main.go"})
			if result.err != nil {
				t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
			}
			messages := <-captured
			user, ok := messages[len(messages)-1].(ai.UserMessage)
			if !ok {
				t.Fatalf("last provider message = %T", messages[len(messages)-1])
			}
			if text := userMessageText(user); text != "Discovered body: main.go" {
				t.Fatalf("prompt sent = %q, want the discovered template expanded", text)
			}
		})
	}
}

// invalidFileURLMessage is Node's fileURLToPath text for file:///a%2Fb.
func invalidFileURLMessage() string {
	if runtime.GOOS == "windows" {
		return `File URL path must not include encoded \ or / characters`
	}
	return "File URL path must not include encoded / characters"
}

// resource-loader.ts extendResources keeps a missing skill path ("Skill path does not exist") and each skill's diagnostics in
// getSkills().diagnostics, which only interactive mode shows: print, JSON and RPC mode write nothing to stderr for them.
func TestUpstreamHeadlessExtendResourcesReportsNoSkillDiagnostics(t *testing.T) {
	root, cwd, agentDir := resourceLoaderFixture(t)
	mismatchDir := filepath.Join(root, "extra", "mismatch")
	writePackageResource(t, filepath.Join(mismatchDir, "SKILL.md"), "---\nname: Bad--Name\ndescription: Badly named skill\n---\nBody")
	missing := filepath.Join(root, "extra", "missing-skill")
	if len(codingagent.SkillDiagnostics(mustLoadSkill(t, mismatchDir))) == 0 {
		t.Fatal("the fixture skill has no diagnostic, so the test proves nothing")
	}
	loader := newCLIResourceLoader(cwd, agentDir)
	var err error
	stderr := captureStderr(t, func() {
		err = loader.ExtendResources(coding.ResourceExtensionPaths{SkillPaths: []coding.ExtensionResourcePath{{Path: missing}, {Path: mismatchDir}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if skills := loader.GetSkills().Skills; len(skills) != 1 {
		t.Fatalf("skills = %d; want the mismatched skill added", len(skills))
	}
	if stderr != "" {
		t.Fatalf("headless extendResources wrote skill diagnostics to stderr, which Pi keeps for interactive mode only:\n%s", stderr)
	}
}

func mustLoadSkill(t *testing.T, dir string) *codingagent.SkillDef {
	t.Helper()
	loaded, err := codingagent.LoadSkillsFromPath(dir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("LoadSkillsFromPath(%s) = %d skills, %v", dir, len(loaded), err)
	}
	return loaded[0]
}
