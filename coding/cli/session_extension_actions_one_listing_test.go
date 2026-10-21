package cli

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi binds one getCommands (agent-session.ts:3361-3384 _bindExtensionCore), so the list an extension reads through the mode's binding is the
// list the Session reports. A resource that reached the Session without sourceInfo gets its path provenance in both, not only in Session.SlashCommands.
func TestModeGetCommandsIsTheSessionsCommandList(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_HOME", t.TempDir())
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
	live.SetPromptResources(
		[]coding.PromptTemplate{{Name: "mine", FilePath: filepath.Join(services.AgentDir(), "prompts", "mine.md")}},
		[]*coding.Skill{{Name: "lint", FilePath: filepath.Join(cwd, ".pig", "skills", "lint", "SKILL.md")}},
	)
	runtime := extension.CreateExtensionRuntime()
	runner := inproc.NewRunner(nil, cwd, runtime)
	coding.BindSessionExtensionActions(runner, nil, func() *coding.Session { return live }, extension.ContextActions{}, nil)

	got, want := runtime.GetCommands(), live.SlashCommands()
	if !slices.Equal(got, want) {
		t.Fatalf("GetCommands bound by the mode = %+v, want the Session's list %+v", got, want)
	}
	if len(got) != 2 || got[0].SourceInfo.Scope != "user" || got[1].SourceInfo.Scope != "project" {
		t.Fatalf("GetCommands = %+v, want the user prompt and the project skill placed by their paths", got)
	}
}
