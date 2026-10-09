package piglet

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A Piglet entry's `tools` allowlist scopes the tools its extension registered. The session reports an extension
// tool's sourceInfo as Pi's provenance object (path, source, scope, origin), which names no Piglet entry, and a
// built-in's as a typed struct; neither carries the entry name that ScopeTools keys on, so the owner supplies it.
// upstream: agent-session.ts _bindExtensionCore binds getAllTools/getActiveTools/setActiveTools in every mode.
func TestBuiltExtensionScopesToolsByTheOwningPigletEntry(t *testing.T) {
	empty, all := []string{}, []string{"delegate"}
	sourceInfo := func(path string) extension.SourceInfo {
		return extension.SourceInfo{Path: path, Source: "local", Scope: "temporary", Origin: "top-level"}
	}
	tools := []extension.ToolInfo{
		{Name: "read", SourceInfo: extension.SourceInfo{Path: "builtin:read", Source: "builtin"}},
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

// Real extension tools carry a SourceInfo, not a source name. Even with no owner resolver, an
// extension tool must never be labelled "builtin" and governed by root `tools`.
func TestExtensionToolWithPiSourceInfoIsNotScopedAsBuiltin(t *testing.T) {
	root, todo := []string{"read"}, []string{"todo"}
	info := codingagent.PiSourceInfo{Path: "/home/u/.pig/npm/node_modules/@juicesharp/rpiv-todo/index.ts", Source: "npm:@juicesharp/rpiv-todo", Scope: "temporary", Origin: "package"}
	piglet := &Piglet{Name: "p", BuiltinTools: &root, Extensions: []ExtensionEntry{{Name: "rpiv-todo", Tools: &todo}}}
	for name, sourceInfo := range map[string]extension.SourceInfo{"value": info} {
		for ownerName, owner := range map[string]func(extension.ToolInfo) string{
			"nil owner":   nil,
			"empty owner": func(extension.ToolInfo) string { return "" },
			"entry owner": func(tool extension.ToolInfo) string {
				if tool.Name == "todo" {
					return "rpiv-todo"
				}
				return ""
			},
		} {
			t.Run(name+"/"+ownerName, func(t *testing.T) {
				tools := []extension.ToolInfo{
					{Name: "read", SourceInfo: extension.SourceInfo{Path: "builtin:read", Source: "builtin"}},
					{Name: "write", SourceInfo: extension.SourceInfo{Path: "builtin:write", Source: "builtin"}},
					{Name: "todo", SourceInfo: sourceInfo},
				}
				var active []string
				runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPigletTools(piglet, owner)}, t.TempDir())
				runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
					GetAllTools:    func() []extension.ToolInfo { return tools },
					GetActiveTools: func() []string { return nil },
					SetActiveTools: func(names []string) { active = slices.Clone(names) },
				}, nil)
				if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
					t.Fatal(err)
				}
				if want := []string{"read", "todo"}; !slices.Equal(active, want) {
					t.Errorf("active = %v, want %v", active, want)
				}
			})
		}
	}
}
