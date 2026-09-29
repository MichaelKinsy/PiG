package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 registers every built-in tool that --tools, --no-tools and
// --exclude-tools leave (agent-session.ts:3143-3210), starts with the default
// active set (sdk.ts:258-265), and setActiveToolsByName selects from the whole
// registry in the requested order, ignoring unknown names (agent-session.ts:
// 1279-1291). Pi's examples/extensions/plan-mode activates grep, find and ls
// this way.
func TestSetActiveToolsActivatesRegisteredBuiltins(t *testing.T) {
	for _, tc := range []struct {
		name     string
		allowed  map[string]struct{}
		excluded map[string]struct{}
		request  []string
		want     []string
	}{
		{
			name:    "default registry",
			request: []string{"read", "bash", "grep", "find", "ls", "questionnaire"},
			want:    []string{"read", "bash", "grep", "find", "ls"},
		},
		{
			name:    "requested order",
			request: []string{"ls", "read"},
			want:    []string{"ls", "read"},
		},
		{
			name:    "tools allowlist bounds the registry",
			allowed: map[string]struct{}{"read": {}, "bash": {}},
			request: []string{"read", "grep"},
			want:    []string{"read"},
		},
		{
			name:     "excluded tools stay inactive",
			excluded: map[string]struct{}{"grep": {}},
			request:  []string{"read", "grep", "find"},
			want:     []string{"read", "find"},
		},
		{
			name:    "back to the default set",
			request: []string{"read", "bash", "edit", "write"},
			want:    []string{"read", "bash", "edit", "write"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := inproc.NewRunner([]extension.Extension{{Name: "plan"}}, t.TempDir())
			m := &InteractiveMode{
				newRunner: runner,
				tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
				layout:    tui.NewContainer(),
				agent:     agent.NewAgent(agent.AgentOptions{}),
				opts: InteractiveOptions{
					CWD:                 t.TempDir(),
					ActiveBuiltinTools:  map[string]struct{}{"read": {}, "bash": {}, "edit": {}, "write": {}},
					ToolRegistryAllowed: tc.allowed,
					ExcludedTools:       tc.excluded,
				},
			}
			m.wireInprocContextActions()
			m.refreshAgentTools()
			ctx := runner.CreateCommandContext()
			wantStart := []string{"read", "bash", "edit", "write"}
			if tc.allowed != nil || tc.excluded != nil {
				wantStart = nil
			}
			if got := ctx.GetActiveTools(); wantStart != nil && !slices.Equal(got, wantStart) {
				t.Fatalf("GetActiveTools() before SetActiveTools = %v, want the default set %v", got, wantStart)
			}
			ctx.SetActiveTools(tc.request)
			if got := ctx.GetActiveTools(); !slices.Equal(got, tc.want) {
				t.Fatalf("GetActiveTools() after SetActiveTools(%v) = %v, want %v", tc.request, got, tc.want)
			}
			if got := m.activeToolNames(); !slices.Equal(got, tc.want) {
				t.Fatalf("system prompt tools = %v, want %v", got, tc.want)
			}
		})
	}
}
