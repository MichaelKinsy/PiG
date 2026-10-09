package cli

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi packages/coding-agent/src/core/agent-session.ts:3361-3390 (_bindExtensionCore getCommands) binds pi.getCommands() to the extension commands, then the prompt templates, then the
// skills of the Session, each with its own sourceInfo. An extension's own API reads it: the factory's api, loaded on the runtime that the production
// binding (bindSessionExtensionActions) fills from a real Session and Runner.
func TestExtensionAPIGetCommandsReadsTheBoundSession(t *testing.T) {
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

	runtime := extension.CreateExtensionRuntime()
	var api extension.API
	extInfo := extension.SourceInfo{Path: "/ext/deploy.ts", Source: "local", Scope: "project", Origin: "top-level"}
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		api = pi
		pi.RegisterCommand("deploy", extension.CommandOptions{Description: "Ship it", Handler: func(context.Context, string) error { return nil }})
		return nil
	}, cwd, nil, runtime, "/ext/deploy.ts", factoryload.WithSourceInfo(extInfo))
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd, runtime)
	bindSessionExtensionActions(runner, nil, func() *coding.Session { return session }, extension.ContextActions{})

	want := []extension.SlashCommandInfo{
		{Name: "deploy", Description: "Ship it", Source: "extension", SourceInfo: extInfo},
		{Name: "review", Description: "Review a diff", Source: "prompt", SourceInfo: templateInfo},
		{Name: "skill:lint", Description: "Lint code", Source: "skill", SourceInfo: skillInfo},
	}
	if got := api.GetCommands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("api.GetCommands() = %+v, want %+v", got, want)
	}
}
