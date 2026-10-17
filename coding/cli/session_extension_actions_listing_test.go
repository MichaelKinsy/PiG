package cli

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi: packages/coding-agent/src/core/agent-session.ts:3361-3384 (_bindExtensionCore getCommands) binds the commands every extension reads through runtime.getCommands (runner.ts:433, types.ts:2170): the runner's extension commands under their invocation names, then the prompt templates, then the skills (named `skill:<name>`) of the Session's resource loader, each with the sourceInfo the resource carries.
// Both bindings that reach the runtime list them: the Session's own (NewSession with a runner) and the one bindSessionExtensionActions rebinds in every mode; before a Session exists the latter lists nothing.
func TestSessionExtensionActionsGetCommandsListsExtensionPromptAndSkillCommands(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	commandSource := extension.SourceInfo{Path: "/ext/deploy.ts", Source: "extension:deploy", Scope: "project", Origin: "top-level"}
	templateSource := extension.SourceInfo{Path: "/prompts/review.md", Source: "local", Scope: "user", Origin: "top-level", BaseDir: "/prompts"}
	skillSource := extension.SourceInfo{Path: "/skills/lint/SKILL.md", Source: "local", Scope: "project", Origin: "top-level", BaseDir: "/skills/lint"}
	ext := extension.Extension{
		Path: "/ext/deploy.ts", ResolvedPath: "/ext/deploy.ts",
		Handlers: map[string][]extension.HandlerFn{}, Tools: map[string]extension.RegisteredTool{},
		MessageRenderers: map[string]extension.MessageRenderer{}, Flags: map[string]extension.ExtensionFlag{},
		Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		Commands: map[string]extension.RegisteredCommand{"deploy": {
			Name: "deploy", Description: "ship it", SourceInfo: commandSource,
			Handler: func(context.Context, string) error { return nil },
		}},
	}
	runtime := extension.CreateExtensionRuntime()
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd, runtime)

	var live *coding.Session
	current := func() *coding.Session { return live }
	earlyRuntime := extension.CreateExtensionRuntime()
	bindSessionExtensionActions(inproc.NewRunner([]extension.Extension{ext}, cwd, earlyRuntime), nil, current, extension.ContextActions{}, nil)
	if got := earlyRuntime.GetCommands(); got == nil || len(got) != 0 {
		t.Fatalf("GetCommands before the Session exists = %#v, want an empty list", got)
	}

	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	live, err = coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	live.SetPromptResources(
		[]coding.PromptTemplate{{Name: "review", Description: "review the diff", FilePath: "/prompts/review.md", SourceInfo: templateSource}},
		[]*coding.Skill{{Name: "lint", Description: "run the linter", FilePath: "/skills/lint/SKILL.md", SourceInfo: skillSource}},
	)

	want := []extension.SlashCommandInfo{
		{Name: "deploy", Description: "ship it", Source: "extension", SourceInfo: commandSource},
		{Name: "review", Description: "review the diff", Source: "prompt", SourceInfo: templateSource},
		{Name: "skill:lint", Description: "run the linter", Source: "skill", SourceInfo: skillSource},
	}
	if got := runtime.GetCommands(); !slices.Equal(got, want) {
		t.Fatalf("GetCommands bound by the Session = %+v, want %+v", got, want)
	}
	bindSessionExtensionActions(runner, nil, current, extension.ContextActions{}, nil)
	if got := runtime.GetCommands(); !slices.Equal(got, want) {
		t.Fatalf("GetCommands bound by the mode = %+v, want %+v", got, want)
	}
}

// A resource the loader gave no sourceInfo is placed by its path: under the Session's agent directory it is a user resource, under the project's config directory a project resource, anywhere else a local one (SlashCommandCatalog.defaultSourceInfo). The Session supplies both directories, so a catalog without them would call every such resource local.
func TestSessionSlashCommandsPlaceResourcesWithoutSourceInfoByTheSessionDirectories(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	live, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	userPrompt := filepath.Join(services.AgentDir(), "prompts", "mine.md")
	projectSkill := filepath.Join(cwd, ".pig", "skills", "lint", "SKILL.md")
	elsewhere := filepath.Join(t.TempDir(), "other.md")
	live.SetPromptResources(
		[]coding.PromptTemplate{{Name: "mine", FilePath: userPrompt}, {Name: "other", FilePath: elsewhere}},
		[]*coding.Skill{{Name: "lint", FilePath: projectSkill}},
	)
	got := map[string]extension.SourceInfo{}
	for _, command := range live.SlashCommands() {
		got[command.Name] = command.SourceInfo
	}
	if got["mine"].Scope != "user" || got["skill:lint"].Scope != "project" || got["other"].Scope == "user" || got["other"].Scope == "project" {
		t.Fatalf("scopes = user prompt %q, project skill %q, other prompt %q; want user, project and neither", got["mine"].Scope, got["skill:lint"].Scope, got["other"].Scope)
	}
}
