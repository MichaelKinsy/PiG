package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// resource-loader.ts:566-575, :765-775 with package-manager.ts:192-198 resourcePrecedenceRank: the `-e builtin:<name>` extensions load after the
// extensions of the other -e sources and ahead of the settings extensions; the other built-ins and the inline factories follow the configured ones.
func TestExtensionsInLoadOrderWithExplicitPlacesExplicitBuiltinsAfterTheOtherDashESources(t *testing.T) {
	ext := func(path string) extension.Extension { return extension.Extension{Path: path} }
	cli := func(path string) extension.Extension {
		e := ext(path)
		e.SourceInfo = CLISourceInfo(path)
		return e
	}
	paths := func(exts []extension.Extension) []string {
		var out []string
		for _, e := range exts {
			out = append(out, e.Path)
		}
		return out
	}
	configured := []extension.Extension{cli("a.ts"), ext("user.ts"), ext("project.ts")}
	builtins := []extension.Extension{ext("builtin:llama.cpp"), ext("builtin:mcp"), ext("<inline:1>")}
	for _, tc := range []struct {
		name     string
		explicit []string
		want     []string
	}{
		{"none", nil, []string{"a.ts", "user.ts", "project.ts", "builtin:llama.cpp", "builtin:mcp", "<inline:1>"}},
		{"one: after the -e file, before the settings files", []string{"builtin:mcp"}, []string{"a.ts", "builtin:mcp", "user.ts", "project.ts", "builtin:llama.cpp", "<inline:1>"}},
		{"two keep the builtins' order", []string{"builtin:mcp", "builtin:llama.cpp"}, []string{"a.ts", "builtin:llama.cpp", "builtin:mcp", "user.ts", "project.ts", "<inline:1>"}},
		{"an explicit path that did not load", []string{"builtin:missing"}, []string{"a.ts", "user.ts", "project.ts", "builtin:llama.cpp", "builtin:mcp", "<inline:1>"}},
	} {
		if got := paths(ExtensionsInLoadOrderWithExplicit(configured, builtins, tc.explicit)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: order = %q, want %q", tc.name, got, tc.want)
		}
	}
}
