package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// getAllTools reports the tool registry upstream _refreshToolRegistry builds:
// --tools bounds it, --no-tools (an empty allowlist) empties it, and
// --exclude-tools removes names from it, for built-in and extension tools
// alike.
func TestExtensionToolInfosAppliesRegistryAllowlistAndDenylist(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "ext",
		Tools: map[string]extension.RegisteredTool{
			"one": {Definition: extension.ToolDefinition{Name: "one"}},
			"two": {Definition: extension.ToolDefinition{Name: "two"}},
		},
		ToolOrder: []string{"two", "one"},
	}}, t.TempDir())
	names := func(allowed, excluded map[string]struct{}) []string {
		var out []string
		for _, tool := range ExtensionToolInfos(runner, allowed, excluded) {
			out = append(out, tool.Name)
		}
		return out
	}
	set := func(names ...string) map[string]struct{} {
		out := make(map[string]struct{}, len(names))
		for _, name := range names {
			out[name] = struct{}{}
		}
		return out
	}
	for _, tc := range []struct {
		name              string
		allowed, excluded map[string]struct{}
		want              []string
	}{
		{"no filters", nil, nil, []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls", "two", "one"}},
		{"allowlist", set("grep", "one", "missing"), nil, []string{"grep", "one"}},
		{"no tools", set(), nil, nil},
		{"denylist", nil, set("bash", "two"), []string{"read", "powershell", "edit", "write", "grep", "find", "ls", "one"}},
	} {
		if got := names(tc.allowed, tc.excluded); !slices.Equal(got, tc.want) {
			t.Errorf("%s: getAllTools names = %v, want %v", tc.name, got, tc.want)
		}
	}
}
