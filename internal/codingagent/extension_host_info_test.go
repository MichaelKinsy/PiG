package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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

// getAllTools carries each definition's exposure, namespace and annotations, and reports "direct" for a definition without an exposure, as upstream's ToolInfo does (a required field); a namespace or annotations object is copied only when the definition has one.
// upstream: agent-session.ts:1449-1460 (getAllTools), 1481 (_getToolExposure), types.ts:2063 (ToolInfo)
func TestExtensionToolInfosReportExposureNamespaceAndAnnotations(t *testing.T) {
	readOnly := true
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "ext",
		Tools: map[string]extension.RegisteredTool{
			"plain": {Definition: extension.ToolDefinition{Name: "plain"}},
			"rich": {Definition: extension.ToolDefinition{
				Name: "rich", Exposure: extension.ToolExposureDeferred,
				Namespace:   &extension.ToolNamespace{Name: "docs", Description: "Docs server"},
				Annotations: &extension.ToolAnnotations{ReadOnlyHint: &readOnly},
			}},
		},
		ToolOrder: []string{"plain", "rich"},
	}}, t.TempDir())
	byName := map[string]subprocess.ToolInfo{}
	for _, info := range ExtensionToolInfos(runner, nil, nil) {
		byName[info.Name] = info
	}
	for _, name := range []string{"read", "bash", "plain"} {
		info := byName[name]
		if info.Exposure != extension.ToolExposureDirect || info.Namespace != nil || info.Annotations != nil {
			t.Errorf("%s = exposure %q namespace %v annotations %v, want direct with neither", name, info.Exposure, info.Namespace, info.Annotations)
		}
	}
	rich := byName["rich"]
	if rich.Exposure != extension.ToolExposureDeferred {
		t.Errorf("rich exposure = %q, want deferred", rich.Exposure)
	}
	if rich.Namespace == nil || *rich.Namespace != (extension.ToolNamespace{Name: "docs", Description: "Docs server"}) {
		t.Errorf("rich namespace = %v", rich.Namespace)
	}
	if rich.Annotations == nil || rich.Annotations.ReadOnlyHint == nil || !*rich.Annotations.ReadOnlyHint {
		t.Errorf("rich annotations = %v", rich.Annotations)
	}
	// upstream copies the annotations object, so a caller's change to the reported value does not reach the definition.
	if rich.Annotations != nil && rich.Annotations == runner.Tools()[1].Definition.Annotations {
		t.Error("rich annotations alias the definition's object")
	}
}
