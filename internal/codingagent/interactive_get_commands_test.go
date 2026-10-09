package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:3361-3384 (_bindExtensionCore getCommands) is bound to the runtime every extension context reads (runner.ts:433, types.ts:2170) in every mode. Interactive mode binds it to the published catalog: the runner's extension commands, then the prompt templates, then the skills, each with its own sourceInfo.
func TestInteractiveWiringBindsGetCommandsToTheRuntime(t *testing.T) {
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
	mode := &InteractiveMode{
		newRunner:       inproc.NewRunner([]extension.Extension{ext}, cwd, runtime),
		agent:           mustNewAgent(agent.AgentOptions{}),
		promptTemplates: []PromptTemplate{{Name: "review", Description: "review the diff", FilePath: "/prompts/review.md", SourceInfo: templateSource}},
		opts:            InteractiveModeOptions{CWD: cwd, Skills: []*SkillDef{{Name: "lint", Description: "run the linter", FilePath: "/skills/lint/SKILL.md", SourceInfo: skillSource}}},
	}
	mode.publishSlashCommandCatalog()
	mode.wireInprocContextActions()

	want := []extension.SlashCommandInfo{
		{Name: "deploy", Description: "ship it", Source: "extension", SourceInfo: commandSource},
		{Name: "review", Description: "review the diff", Source: "prompt", SourceInfo: templateSource},
		{Name: "skill:lint", Description: "run the linter", Source: "skill", SourceInfo: skillSource},
	}
	if got := runtime.GetCommands(); !slices.Equal(got, want) {
		t.Fatalf("runtime.GetCommands = %+v, want %+v", got, want)
	}
}
