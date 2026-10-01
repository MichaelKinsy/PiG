package piglet

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A Piglet entry's `tools` allowlist scopes the tools its extension registered. The session reports an extension
// tool's sourceInfo as Pi's provenance object (path, source, scope, origin), which names no Piglet entry, and a
// built-in's as a typed struct; neither carries the entry name that ScopeTools keys on, so the owner supplies it.
// upstream: agent-session.ts _bindExtensionCore binds getAllTools/getActiveTools/setActiveTools in every mode.
func TestBuiltExtensionScopesToolsByTheOwningPigletEntry(t *testing.T) {
	empty, all := []string{}, []string{"delegate"}
	sourceInfo := func(path string) any {
		return map[string]any{"path": path, "source": "local", "scope": "temporary", "origin": "top-level"}
	}
	tools := []extension.ToolInfo{
		{Name: "read", SourceInfo: struct{ Path, Source string }{"builtin:read", "builtin"}},
		{Name: "pig_doctor", SourceInfo: sourceInfo("/p/pig-doctor")},
		{Name: "delegate", SourceInfo: sourceInfo("/p/delegator")},
		{Name: "delegate_other", SourceInfo: sourceInfo("/p/delegator")},
		{Name: "unowned", SourceInfo: sourceInfo("/p/unknown")},
	}
	owner := func(tool extension.ToolInfo) string {
		return map[string]string{"pig_doctor": "pig-doctor", "delegate": "delegator", "delegate_other": "delegator"}[tool.Name]
	}
	for _, test := range []struct {
		name    string
		piglet  *Piglet
		owner   func(extension.ToolInfo) string
		want    []string
		comment string
	}{
		{"an empty entry list hides that extension's tools", &Piglet{Name: "p", Extensions: []ExtensionEntry{{Name: "pig-doctor", Tools: &empty}}}, owner, []string{"read", "delegate", "delegate_other", "unowned"}, "pig_doctor hidden"},
		{"an allowlist keeps only the named tools", &Piglet{Name: "p", Extensions: []ExtensionEntry{{Name: "delegator", Tools: &all}}}, owner, []string{"read", "pig_doctor", "delegate", "unowned"}, "delegate_other hidden"},
		{"an entry without tools keeps every tool", &Piglet{Name: "p", Extensions: []ExtensionEntry{{Name: "pig-doctor"}}}, owner, []string{"read", "pig_doctor", "delegate", "delegate_other", "unowned"}, "nothing hidden"},
		{"a tool without an owner is scoped as before", &Piglet{Name: "p", Extensions: []ExtensionEntry{{Name: "pig-doctor", Tools: &empty}}}, nil, []string{"read", "pig_doctor", "delegate", "delegate_other", "unowned"}, "nil owner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var active []string
			runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPigletTools(test.piglet, test.owner)}, t.TempDir())
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
				GetAllTools:    func() []extension.ToolInfo { return tools },
				GetActiveTools: func() []string { return nil },
				SetActiveTools: func(names []string) { active = slices.Clone(names) },
			}, nil)
			if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(active, test.want) {
				t.Errorf("active tools = %v, want %v (%s)", active, test.want, test.comment)
			}
		})
	}
}
