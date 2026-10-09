package piglet

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// An extension that narrows the session to nothing with SetActiveTools([]) is a deny-all policy. The Piglet scope only
// ever narrows further: it must never widen what an earlier extension narrowed, including to zero tools.
// upstream: agent-session.ts setActiveToolsByName applies exactly the requested names; Pi has no Piglets, so the rule
// is that no later extension widens the set an earlier one chose.
// Pi: packages/coding-agent/src/core/extensions/types.ts:913 (BeforeAgentStartEvent.type).
func TestPigletScopeNeverWidensWhatAnExtensionNarrowed(t *testing.T) {
	tools := []extension.ToolInfo{
		{Name: "read", SourceInfo: extension.SourceInfo{Path: "builtin:read", Source: "builtin"}},
		{Name: "bash", SourceInfo: extension.SourceInfo{Path: "builtin:bash", Source: "builtin"}},
	}
	readOnly := []string{"read"}
	for _, test := range []struct {
		name          string
		builtinTools  *[]string
		currentActive []string
		want          []string
	}{
		{"narrowed to none stays none", nil, []string{}, []string{}},
		{"narrowed to one tool stays that tool", nil, []string{"bash"}, []string{"bash"}},
		{"never narrowed gets the Piglet scope", nil, nil, []string{"read", "bash"}},
		{"the full set gets the Piglet scope", nil, []string{"read", "bash"}, []string{"read", "bash"}},
		{"a Piglet scope narrows the full set", &readOnly, []string{"read", "bash"}, []string{"read"}},
		{"a Piglet scope disjoint from the narrowed set leaves none", &readOnly, []string{"bash"}, []string{}},
	} {
		for _, event := range []string{"session_start", "before_agent_start"} {
			t.Run(test.name+"/"+event, func(t *testing.T) {
				var active []string
				called := false
				runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPiglet(&Piglet{Name: "p", BuiltinTools: test.builtinTools})}, t.TempDir())
				runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
					GetAllTools:    func() []extension.ToolInfo { return tools },
					GetActiveTools: func() []string { return test.currentActive },
					SetActiveTools: func(names []string) {
						called = true
						if names == nil {
							t.Errorf("SetActiveTools(nil): an empty selection must be a non-nil empty slice")
						}
						active = slices.Clone(names)
					},
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
				if !called {
					t.Fatal("SetActiveTools was not called")
				}
				if !slices.Equal(active, test.want) {
					t.Errorf("active tools = %v, want %v", active, test.want)
				}
			})
		}
	}
}
