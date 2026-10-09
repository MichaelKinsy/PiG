//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi's unmodified dynamic-tools.ts registers echo_session in session_start and shout in a command.
// loader.ts:273-284 refreshes after each registration; no reload is required.
func TestUpstreamDynamicToolsExample(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			source, err := filepath.Abs("../../../../.upstream/current/packages/coding-agent/examples/extensions/dynamic-tools.ts")
			if err != nil {
				t.Fatal(err)
			}
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, failures := h.LoadAll(t.Context(), []ExtConfig{{Name: "dynamic-tools", Source: source, Enabled: true, Isolation: isolation}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v %v", loaded, failures)
			}
			runner := inproc.NewRunner(loaded, t.TempDir())
			if len(runner.Tools()) != 0 {
				t.Fatal("tools registered before session_start")
			}
			if _, err := runner.Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
				t.Fatal(err)
			}
			assertDynamicEcho(t, runner, "echo_session", "[session] hello")
			if !runner.ExecuteCommand(t.Context(), "add-echo-tool", "shout") {
				t.Fatal("command missing")
			}
			assertDynamicEcho(t, runner, "shout", "[shout] hello")
			if !runner.ExecuteCommand(t.Context(), "add-echo-tool", "shout") {
				t.Fatal("command missing")
			}
			if got := runner.Tools(); len(got) != 2 || got[0].Definition.Name != "echo_session" || got[1].Definition.Name != "shout" {
				t.Fatalf("registration order: %+v", got)
			}
		})
	}
}

func assertDynamicEcho(t *testing.T, runner *inproc.Runner, name, want string) {
	t.Helper()
	tool, ok := runner.GetToolDefinition(name)
	if !ok {
		t.Fatalf("late tool %q missing from registry", name)
	}
	result, err := tool.Execute(t.Context(), "dynamic", []byte(`{"message":"hello"}`), nil)
	if err != nil || !strings.Contains(fmt.Sprint(result), want) {
		t.Fatalf("tool %s: %+v, %v; want %q", name, result, err, want)
	}
	if tool.PromptSnippet == "" || len(tool.PromptGuidelines) != 1 {
		t.Fatalf("metadata missing: %+v", tool)
	}
}
