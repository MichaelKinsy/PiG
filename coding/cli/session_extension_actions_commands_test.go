package cli

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:3361-3390 (_bindExtensionCore getCommands) lists the extension commands, then the prompt templates, then the skills, each with its own sourceInfo, and runner.ts:433 copies it into the shared runtime. The value comes from the real Session's resource loader and the real Runner's command registry, through bindSessionExtensionActions.
func TestSessionBindsGetCommandsToItsRunnerRuntime(t *testing.T) {
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	templateInfo := extension.SourceInfo{Path: "/p/review.md", Source: "local", Scope: "project", Origin: "top-level"}
	skillInfo := extension.SourceInfo{Path: "/s/lint/SKILL.md", Source: "local", Scope: "user", Origin: "top-level"}
	session.SetPromptResources(
		[]coding.PromptTemplate{{Name: "review", Description: "Review a diff", FilePath: "/p/review.md", SourceInfo: templateInfo}},
		[]*coding.Skill{{Name: "lint", Description: "Lint code", FilePath: "/s/lint/SKILL.md", SourceInfo: skillInfo}},
	)
	extInfo := extension.SourceInfo{Path: "/ext/deploy.ts", Source: "local", Scope: "project", Origin: "top-level"}
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "deploy", Path: "/ext/deploy.ts", SourceInfo: extInfo, CommandOrder: []string{"deploy"},
		Commands: map[string]extension.RegisteredCommand{"deploy": {Name: "deploy", Description: "Ship it", Handler: func(context.Context, string) error { return nil }}},
	}}, cwd)
	bindSessionExtensionActions(runner, nil, func() *coding.Session { return session }, extension.ContextActions{})

	want := []extension.SlashCommandInfo{
		{Name: "deploy", Description: "Ship it", Source: "extension", SourceInfo: extInfo},
		{Name: "review", Description: "Review a diff", Source: "prompt", SourceInfo: templateInfo},
		{Name: "skill:lint", Description: "Lint code", Source: "skill", SourceInfo: skillInfo},
	}
	got := runner.Runtime().GetCommands()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime.GetCommands() = %+v, want %+v", got, want)
	}
}
