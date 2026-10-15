package piglet

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The Piglet scope narrows the session's selection without reordering it: the provider request lists the active tools
// in the order the session selected them. upstream: agent-session.ts:1561-1565 (_applyToolLoadout) walks setActiveTools' names in argument
// order and declares them in that order; Pi has no Piglets, so narrowing keeps the selection's order.
func TestPigletScopeKeepsTheSelectionOrder(t *testing.T) {
	builtin := func(name string) extension.ToolInfo {
		return extension.ToolInfo{Name: name, SourceInfo: extension.SourceInfo{Path: "builtin:" + name, Source: "builtin"}}
	}
	tools := []extension.ToolInfo{builtin("read"), builtin("bash"), builtin("grep"), builtin("find")}
	readGrep := []string{"read", "grep"}
	for _, test := range []struct {
		name          string
		builtinTools  *[]string
		currentActive []string
		want          []string
	}{
		{"a narrowed selection keeps its order", nil, []string{"read", "grep", "bash"}, []string{"read", "grep", "bash"}},
		{"the full set keeps its order", nil, []string{"find", "read", "grep", "bash"}, []string{"find", "read", "grep", "bash"}},
		{"a Piglet scope narrows in the selection's order", &readGrep, []string{"grep", "bash", "read"}, []string{"grep", "read"}},
		{"a repeated name stays once", nil, []string{"grep", "read", "grep"}, []string{"grep", "read"}},
		{"never narrowed gets the Piglet scope in registry order", &readGrep, nil, []string{"read", "grep"}},
	} {
		for _, event := range []string{"session_start", "before_agent_start"} {
			t.Run(test.name+"/"+event, func(t *testing.T) {
				var active []string
				runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPiglet(&Piglet{Name: "p", BuiltinTools: test.builtinTools})}, t.TempDir())
				runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
					GetAllTools:    func() []extension.ToolInfo { return tools },
					GetActiveTools: func() []string { return test.currentActive },
					SetActiveTools: func(names []string) { active = slices.Clone(names) },
				}, nil)
				var err error
				if event == "session_start" {
					_, err = runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"})
				} else {
					_, err = runner.Emit(context.Background(), extension.BeforeAgentStartEvent{Type: "before_agent_start"})
				}
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(active, test.want) {
					t.Errorf("active tools = %v, want %v", active, test.want)
				}
			})
		}
	}
}
