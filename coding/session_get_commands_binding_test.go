package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi: packages/coding-agent/src/core/agent-session.ts:3361-3383 (_bindExtensionCore getCommands), bound as runner.ts:433 runtime.getCommands:
// pi.getCommands() lists the extension commands under their invocation names, then the prompt templates, then the skills as skill:<name>,
// each with its own source info. A command two extensions both register is listed under name:1 and name:2.
func TestSessionBindsGetCommandsForExtensions(t *testing.T) {
	registered := func(path, name, description string) extension.Extension {
		info := extension.SourceInfo{Path: path, Source: "extension:" + path, Scope: "project", Origin: "top-level"}
		return extension.Extension{Path: path, Commands: map[string]extension.RegisteredCommand{name: {Name: name, Description: description, SourceInfo: info}}, CommandOrder: []string{name}}
	}
	runtime := extension.CreateExtensionRuntime()
	runner := inproc.NewRunner([]extension.Extension{registered("/ext/a", "deploy", "Deploy it"), registered("/ext/b", "deploy", "Deploy again")}, t.TempDir(), runtime)
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	templateInfo := extension.SourceInfo{Path: "/prompts/review.md", Source: "local", Scope: "user", Origin: "top-level"}
	skillInfo := extension.SourceInfo{Path: "/skills/lint/SKILL.md", Source: "local", Scope: "project", Origin: "top-level"}
	sess.SetPromptResources(
		[]PromptTemplate{{Name: "review", Description: "Review code", SourceInfo: templateInfo}},
		[]*Skill{{Name: "lint", Description: "Lint code", SourceInfo: skillInfo}},
	)

	want := []extension.SlashCommandInfo{
		{Name: "deploy:1", Description: "Deploy it", Source: "extension", SourceInfo: extension.SourceInfo{Path: "/ext/a", Source: "extension:/ext/a", Scope: "project", Origin: "top-level"}},
		{Name: "deploy:2", Description: "Deploy again", Source: "extension", SourceInfo: extension.SourceInfo{Path: "/ext/b", Source: "extension:/ext/b", Scope: "project", Origin: "top-level"}},
		{Name: "review", Description: "Review code", Source: "prompt", SourceInfo: templateInfo},
		{Name: "skill:lint", Description: "Lint code", Source: "skill", SourceInfo: skillInfo},
	}
	if runtime.GetCommands == nil {
		t.Fatal("the Session did not bind getCommands for its extensions")
	}
	if got := runtime.GetCommands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("getCommands = %+v, want %+v", got, want)
	}
	// A mode binds its own actions after the Session (cmd/pig sessionExtensionActions); omitting getCommands keeps the Session's.
	runner.BindCore(extension.ExtensionActions{SetSessionName: func(string) error { return nil }}, extension.ContextActions{}, nil)
	if got := runtime.GetCommands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("getCommands after a mode rebinds the core actions = %+v, want %+v", got, want)
	}
}
