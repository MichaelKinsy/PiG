package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:3125 binds getSystemPromptOptions to _baseSystemPromptOptions for every runner in every mode. A native in-process command context in print, JSON or RPC must read the Session's options, not runner.ts:653-656's {cwd} fallback.
func TestHeadlessNativeCommandContextReadsSessionPromptOptions(t *testing.T) {
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{
		NoSession:             true,
		SystemPromptResources: &coding.SystemPromptResources{CustomPrompt: "", CustomPromptSet: true, AppendSystemPrompt: "APPENDED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	runner := inproc.NewRunner(nil, cwd)
	bindSessionExtensionActions(runner, nil, func() *coding.Session { return session }, extension.ContextActions{})
	got, err := runner.CreateCommandContext().GetSystemPromptOptions()
	if err != nil {
		t.Fatal(err)
	}
	if got != session.GetSystemPromptOptions() {
		t.Fatalf("native command options = %+v, want the Session's base options", got)
	}
	if got.AppendSystemPrompt != "APPENDED" || !got.CustomPromptSet || got.ToolSnippets == nil || got.Skills == nil {
		t.Fatalf("native command options = %+v", got)
	}
}
