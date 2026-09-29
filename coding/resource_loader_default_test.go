package coding

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// resourceLoaderFixture is a project nested in a git checkout, a separate agent directory and a separate home directory.
type resourceLoaderFixture struct {
	repo, cwd, agentDir, home string
}

func newResourceLoaderFixture(t *testing.T) resourceLoaderFixture {
	t.Helper()
	root := t.TempDir()
	f := resourceLoaderFixture{repo: filepath.Join(root, "repo"), agentDir: filepath.Join(root, "agent"), home: filepath.Join(root, "home")}
	f.cwd = filepath.Join(f.repo, "packages", "app")
	for _, dir := range []string{filepath.Join(f.repo, ".git"), f.cwd, f.agentDir, f.home} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", f.home)
	t.Setenv("USERPROFILE", f.home)
	t.Setenv("PIG_HOME", filepath.Join(root, "ambient"))
	return f
}

func writeResource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	writeResource(t, filepath.Join(dir, name, "SKILL.md"), "---\nname: "+name+"\ndescription: The "+name+" skill.\n---\n\n# "+name+"\n")
}

func skillNames(loader ResourceLoader) []string {
	var names []string
	for _, skill := range loader.GetSkills().Skills {
		names = append(names, skill.Name)
	}
	slices.Sort(names)
	return names
}

func (f resourceLoaderFixture) session(t *testing.T, trusted bool, loader ResourceLoader) *Session {
	t.Helper()
	services, err := NewServices(ServicesOptions{CWD: f.cwd, AgentDir: f.agentDir, ProjectTrusted: &trusted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	manager, err := NewInMemorySessionManager(f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{SessionManager: manager, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session
}

// A Session's default loader reads the Services' project trust: Pi's package-manager.ts:2398-2446 skips project skill and prompt paths, and resource-loader.ts discoverSystemPromptFile and discoverAppendSystemPromptFile skip project prompt files, while the project is untrusted (resource-loader.test.ts:440-482).
func TestDefaultSessionLoaderAppliesProjectTrust(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	writeSkill(t, filepath.Join(projectDir, "skills"), "project-skill")
	writeResource(t, filepath.Join(f.agentDir, "prompts", "user-prompt.md"), "user prompt")
	writeResource(t, filepath.Join(projectDir, "prompts", "project-prompt.md"), "project prompt")
	writeResource(t, filepath.Join(f.agentDir, "SYSTEM.md"), "user system")
	writeResource(t, filepath.Join(projectDir, "SYSTEM.md"), "project system")
	writeResource(t, filepath.Join(f.agentDir, "APPEND_SYSTEM.md"), "user append")
	writeResource(t, filepath.Join(projectDir, "APPEND_SYSTEM.md"), "project append")
	writeResource(t, filepath.Join(f.agentDir, "AGENTS.md"), "Global instructions")
	writeResource(t, filepath.Join(f.cwd, "AGENTS.md"), "Project instructions")

	promptNames := func(loader ResourceLoader) []string {
		var names []string
		for _, template := range loader.GetPrompts().Prompts {
			names = append(names, template.Name)
		}
		slices.Sort(names)
		return names
	}
	t.Run("untrusted", func(t *testing.T) {
		loader := f.session(t, false, nil).ResourceLoader()
		if got := skillNames(loader); !slices.Equal(got, []string{"user-skill"}) {
			t.Errorf("skills = %v, want only the user skill", got)
		}
		if got := promptNames(loader); !slices.Equal(got, []string{"user-prompt"}) {
			t.Errorf("prompts = %v, want only the user prompt", got)
		}
		if got, ok := loader.GetSystemPrompt(); !ok || got != "user system" {
			t.Errorf("system prompt = %q, %v", got, ok)
		}
		if got := loader.GetAppendSystemPrompt(); !slices.Equal(got, []string{"user append"}) {
			t.Errorf("append system prompt = %q", got)
		}
		// resource-loader.test.ts:476-479: context files, including the project's, load whatever the trust.
		var paths []string
		for _, file := range loader.GetAgentsFiles().AgentsFiles {
			paths = append(paths, file.Path)
		}
		for _, want := range []string{filepath.Join(f.agentDir, "AGENTS.md"), filepath.Join(f.cwd, "AGENTS.md")} {
			if !slices.Contains(paths, want) {
				t.Errorf("agents files = %v, want %s", paths, want)
			}
		}
	})
	t.Run("trusted", func(t *testing.T) {
		loader := f.session(t, true, nil).ResourceLoader()
		if got := skillNames(loader); !slices.Equal(got, []string{"project-skill", "user-skill"}) {
			t.Errorf("skills = %v", got)
		}
		if got := promptNames(loader); !slices.Equal(got, []string{"project-prompt", "user-prompt"}) {
			t.Errorf("prompts = %v", got)
		}
		if got, ok := loader.GetSystemPrompt(); !ok || got != "project system" {
			t.Errorf("system prompt = %q, %v", got, ok)
		}
		if got := loader.GetAppendSystemPrompt(); !slices.Equal(got, []string{"project append"}) {
			t.Errorf("append system prompt = %q", got)
		}
	})
}

// The default loader resolves the locations Pi's package manager resolves for skills: settings-configured paths, override patterns over auto-discovered skills, ancestor .agents/skills up to the git root, and ~/.agents/skills (package-manager.ts:2398-2495, resource-loader.ts reload).
func TestDefaultLoaderResolvesSettingsAndAgentsSkills(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "disabled-skill")
	writeSkill(t, filepath.Join(f.agentDir, "extra"), "configured-skill")
	writeSkill(t, filepath.Join(f.home, ".agents", "skills"), "home-agents-skill")
	writeSkill(t, filepath.Join(f.repo, ".agents", "skills"), "ancestor-agents-skill")
	writeSkill(t, filepath.Join(f.cwd, ".agents", "skills"), "cwd-agents-skill")
	writeResource(t, filepath.Join(f.agentDir, "settings.json"), `{"skills":["extra","-skills/disabled-skill"]}`)

	got := skillNames(f.session(t, true, nil).ResourceLoader())
	want := []string{"ancestor-agents-skill", "configured-skill", "cwd-agents-skill", "home-agents-skill", "user-skill"}
	if !slices.Equal(got, want) {
		t.Fatalf("skills = %v, want %v", got, want)
	}
	// An untrusted project keeps its own .agents/skills out and the user's in.
	got = skillNames(f.session(t, false, nil).ResourceLoader())
	want = []string{"configured-skill", "home-agents-skill", "user-skill"}
	if !slices.Equal(got, want) {
		t.Fatalf("untrusted skills = %v, want %v", got, want)
	}
}

// A Session builds its default system prompt from its loader on construction, as _rebuildSystemPrompt does (agent-session.ts:1379-1391): the loader's skills and context files, its system prompt text and its appended text reach both the model's prompt and the systemPromptOptions extensions read.
func TestSessionDefaultPromptComesFromItsResourceLoader(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	writeResource(t, filepath.Join(f.agentDir, "AGENTS.md"), "global context rule")
	writeResource(t, filepath.Join(f.agentDir, "APPEND_SYSTEM.md"), "appended words")

	t.Run("default loader", func(t *testing.T) {
		session := f.session(t, true, nil)
		prompt := session.SystemPrompt()
		for _, want := range []string{"user-skill", "global context rule", "appended words"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("system prompt lacks %q:\n%s", want, prompt)
			}
		}
		options := session.GetSystemPromptOptions()
		if len(options.Skills) != 1 || options.Skills[0].Name != "user-skill" {
			t.Errorf("options.Skills = %#v", options.Skills)
		}
		if len(options.ContextFiles) != 1 || options.ContextFiles[0].Content != "global context rule" {
			t.Errorf("options.ContextFiles = %#v", options.ContextFiles)
		}
		if options.AppendSystemPrompt != "appended words" {
			t.Errorf("options.AppendSystemPrompt = %q", options.AppendSystemPrompt)
		}
	})
	t.Run("supplied loader", func(t *testing.T) {
		loader := fixedResourceLoader{
			skills: []*Skill{{Name: "custom-skill", Description: "A custom skill", Path: "/fake/SKILL.md", Dir: "/fake"}},
			files:  []ContextFile{{Path: "/fake/AGENTS.md", Content: "custom context"}}, system: "custom system prompt", appended: []string{"first", "second"},
		}
		session := f.session(t, true, loader)
		prompt := session.SystemPrompt()
		if !strings.HasPrefix(prompt, "custom system prompt") {
			t.Errorf("system prompt does not start with the loader's system prompt:\n%s", prompt)
		}
		for _, want := range []string{"custom-skill", "custom context", "first\n\nsecond"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("system prompt lacks %q:\n%s", want, prompt)
			}
		}
		for _, unwanted := range []string{"user-skill", "global context rule", "appended words"} {
			if strings.Contains(prompt, unwanted) {
				t.Errorf("system prompt holds %q the supplied loader does not have", unwanted)
			}
		}
		options := session.GetSystemPromptOptions()
		if !options.CustomPromptSet || options.CustomPrompt != "custom system prompt" || options.AppendSystemPrompt != "first\n\nsecond" {
			t.Errorf("options = %#v", options)
		}
	})
	t.Run("NoResources leaves the prompt to the caller", func(t *testing.T) {
		session := f.session(t, true, NoResources)
		prompt := session.SystemPrompt()
		for _, unwanted := range []string{"user-skill", "global context rule", "appended words"} {
			if strings.Contains(prompt, unwanted) {
				t.Errorf("a Session bound to NoResources rendered %q", unwanted)
			}
		}
	})
	t.Run("an explicit prompt stays the caller's", func(t *testing.T) {
		services, err := NewServices(ServicesOptions{CWD: f.cwd, AgentDir: f.agentDir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		session, err := NewSession(services, SessionOptions{SystemPrompt: "caller prompt"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		})
		if got := session.SystemPrompt(); got != "caller prompt" {
			t.Errorf("system prompt = %q", got)
		}
	})
}

// SetPromptResources replaces the skills and prompt templates only; the loader keeps its context files and prompt text.
func TestSetPromptResourcesKeepsTheLoadersPromptInputs(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeResource(t, filepath.Join(f.agentDir, "AGENTS.md"), "global context rule")
	session := f.session(t, true, nil)
	session.SetPromptResources([]PromptTemplate{{Name: "plain", Content: "expanded"}}, nil)
	if got := session.expandPromptText("/plain"); got != "expanded" {
		t.Fatalf("expandPromptText = %q", got)
	}
	files := session.ResourceLoader().GetAgentsFiles().AgentsFiles
	if len(files) != 1 || files[0].Content != "global context rule" {
		t.Fatalf("agents files = %#v, want the default loader's", files)
	}
}

// fixedResourceLoader is a supplied loader with every resource fixed.
type fixedResourceLoader struct {
	skills   []*Skill
	files    []ContextFile
	system   string
	appended []string
}

func (l fixedResourceLoader) GetSkills() SkillsResult {
	return SkillsResult{Skills: l.skills}
}
func (fixedResourceLoader) GetPrompts() PromptsResult { return PromptsResult{} }
func (l fixedResourceLoader) GetAgentsFiles() AgentsFilesResult {
	return AgentsFilesResult{AgentsFiles: l.files}
}
func (l fixedResourceLoader) GetSystemPrompt() (string, bool) { return l.system, l.system != "" }
func (l fixedResourceLoader) GetAppendSystemPrompt() []string { return l.appended }

// Reload rediscovers on demand, following the settings' project trust at that moment (resource-loader.ts reload).
func TestDefaultLoaderReloadFollowsTrustAndDisk(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	writeSkill(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "skills"), "project-skill")
	settings := icodingagent.NewSettingsManagerWithProjectTrust(f.cwd, f.agentDir, false)
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: settings})
	if got := skillNames(loader); len(got) != 0 {
		t.Fatalf("skills before Reload = %v", got)
	}
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := skillNames(loader); !slices.Equal(got, []string{"user-skill"}) {
		t.Fatalf("untrusted skills = %v", got)
	}
	settings.SetProjectTrusted(true)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "late-skill")
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := skillNames(loader); !slices.Equal(got, []string{"late-skill", "project-skill", "user-skill"}) {
		t.Fatalf("trusted skills after Reload = %v", got)
	}
}

// mutableResourceLoader is a supplied loader whose skills its owner replaces between rebuilds.
type mutableResourceLoader struct {
	fixedResourceLoader
}

func (l *mutableResourceLoader) GetSkills() SkillsResult { return l.fixedResourceLoader.GetSkills() }

// A clone of a Session that builds its default prompt from its loader keeps doing so: Pi's replacement AgentSession rebuilds its prompt from the resource loader it is given (agent-session.ts:1379-1391), so the clone reports the loader's skills in systemPromptOptions and renders what the loader holds at its next rebuild. A clone of a Session bound to NoResources keeps leaving the loader out of its prompt.
func TestCloneKeepsBuildingTheDefaultPromptFromTheLoader(t *testing.T) {
	f := newResourceLoaderFixture(t)
	loader := &mutableResourceLoader{fixedResourceLoader{skills: []*Skill{{Name: "first-skill", Description: "The first skill.", Path: "/fake/first/SKILL.md", Dir: "/fake/first"}}}}
	session := f.session(t, true, loader)
	clone, err := session.Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clone.Close(); err != nil {
			t.Error(err)
		}
	})
	if got, want := clone.SystemPrompt(), session.SystemPrompt(); got != want {
		t.Fatalf("clone system prompt differs from the source's:\n%s\n---\n%s", got, want)
	}
	options := clone.GetSystemPromptOptions()
	if options.CustomPromptSet || len(options.Skills) != 1 || options.Skills[0].Name != "first-skill" {
		t.Fatalf("clone options = customPromptSet %v, customPrompt %q, skills %#v; want the loader's skills and no custom prompt", options.CustomPromptSet, options.CustomPrompt, options.Skills)
	}

	loader.skills = []*Skill{{Name: "second-skill", Description: "The second skill.", Path: "/fake/second/SKILL.md", Dir: "/fake/second"}}
	clone.SetActiveToolsByName(clone.ActiveToolNames())
	if prompt := clone.SystemPrompt(); !strings.Contains(prompt, "second-skill") || strings.Contains(prompt, "first-skill") {
		t.Fatalf("clone did not rebuild its prompt from the loader:\n%s", prompt)
	}

	t.Run("NoResources with prompt resources set", func(t *testing.T) {
		writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
		source := f.session(t, true, NoResources)
		source.SetPromptResources(nil, []*Skill{{Name: "overlay-skill", Description: "An overlay skill.", Path: "/fake/overlay/SKILL.md", Dir: "/fake/overlay"}})
		source.SetActiveToolsByName(source.ActiveToolNames())
		clone, err := source.Clone()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := clone.Close(); err != nil {
				t.Error(err)
			}
		})
		if got, want := clone.SystemPrompt(), source.SystemPrompt(); got != want {
			t.Fatalf("clone system prompt differs from the source's:\n%s\n---\n%s", got, want)
		}
		clone.SetActiveToolsByName(clone.ActiveToolNames())
		if prompt := clone.SystemPrompt(); strings.Contains(prompt, "overlay-skill") || strings.Contains(prompt, "user-skill") {
			t.Fatalf("clone of a NoResources Session rendered loader skills:\n%s", prompt)
		}
		if got := skillNames(clone.ResourceLoader()); !slices.Equal(got, []string{"overlay-skill"}) {
			t.Fatalf("clone skills = %v, want the source's overlay", got)
		}
	})
}

// NewSession without a loader runs the DefaultResourceLoader's reload, which reloads the SettingsManager: sdk.ts:187-190 awaits loader.reload(), resource-loader.ts:395-406 awaits settingsManager.reload(), and settings-manager.ts:520-558 rereads stored settings and drops transient applyOverrides. A settings file written after the Services were created is therefore seen, and an override applied to them is discarded.
func TestNewSessionReloadsSettingsLikeCreateAgentSession(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	writeSkill(t, filepath.Join(f.agentDir, "extra"), "override-skill")
	writeSkill(t, filepath.Join(f.agentDir, "late"), "late-skill")
	trusted := true
	services, err := NewServices(ServicesOptions{CWD: f.cwd, AgentDir: f.agentDir, ProjectTrusted: &trusted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	services.SettingsManager().ApplyOverrides(icodingagent.Settings{Skills: []string{"extra"}})
	writeResource(t, filepath.Join(f.agentDir, "settings.json"), `{"skills":["late"]}`)

	manager, err := NewInMemorySessionManager(f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	got := skillNames(session.ResourceLoader())
	want := []string{"late-skill", "user-skill"}
	if !slices.Equal(got, want) {
		t.Fatalf("skills = %v, want %v (settings reloaded, transient override discarded)", got, want)
	}
	if overrides := services.SettingsManager().Get().Skills; !slices.Equal(overrides, []string{"late"}) {
		t.Fatalf("settings skills = %v, want the reloaded stored value", overrides)
	}
}
