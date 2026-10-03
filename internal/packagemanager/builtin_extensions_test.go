package packagemanager

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

type builtinRow struct {
	Path    string
	Enabled bool
	Source  string
	Scope   string
}

func builtinRows(items []tui.ResourceItem) []builtinRow {
	rows := make([]builtinRow, 0, len(items))
	for _, item := range items {
		if item.ResourceType == tui.ResourceExtensions {
			rows = append(rows, builtinRow{item.Path, item.Enabled, item.Source, item.Scope})
		}
	}
	return rows
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/package-manager.test.ts:127-154 ("should resolve built-in extensions with
// user exclusions and project overrides"). Pi mutates one SettingsManager between three resolves; so does this test.
func TestResolveBuiltinExtensionsWithUserExclusionsAndProjectOverrides(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	names := []string{"mcp", "llama.cpp"}

	if got, want := builtinRows(ResolveBuiltinExtensions(sm, names)), []builtinRow{
		{"builtin:mcp", true, "builtin", "user"},
		{"builtin:llama.cpp", true, "builtin", "user"},
	}; !slices.Equal(got, want) {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}

	if err := sm.SetExtensionPaths([]string{"-builtin:mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectExtensionPaths([]string{"+builtin:mcp", "-builtin:llama.cpp"}); err != nil {
		t.Fatal(err)
	}
	if got, want := builtinRows(ResolveBuiltinExtensions(sm, names)), []builtinRow{
		{"builtin:mcp", true, "builtin", "project"},
		{"builtin:llama.cpp", false, "builtin", "project"},
	}; !slices.Equal(got, want) {
		t.Fatalf("project overrides = %+v, want %+v", got, want)
	}

	if err := sm.SetProjectExtensionPaths([]string{}); err != nil {
		t.Fatal(err)
	}
	if got, want := builtinRows(ResolveBuiltinExtensions(sm, names)), []builtinRow{
		{"builtin:mcp", false, "builtin", "user"},
		{"builtin:llama.cpp", true, "builtin", "user"},
	}; !slices.Equal(got, want) {
		t.Fatalf("project overrides removed = %+v, want %+v", got, want)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:970-986. Pi's project entry is matched with
// applyAutoloadDisabledPatterns over getOverridePatterns, so a plain entry or an entry for another name does not
// override, `!name` disables, and the last matching entry wins. An untrusted project contributes no settings.
func TestResolveBuiltinExtensionsProjectOverrideBranches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		global  []string
		project []string
		trusted bool
		want    builtinRow
	}{
		{"plain project entry is no override", []string{"-builtin:mcp"}, []string{"builtin:mcp"}, true, builtinRow{"builtin:mcp", false, "builtin", "user"}},
		{"bang project entry disables", nil, []string{"!builtin:mcp"}, true, builtinRow{"builtin:mcp", false, "builtin", "project"}},
		{"last entry wins", nil, []string{"-builtin:mcp", "+builtin:mcp"}, true, builtinRow{"builtin:mcp", true, "builtin", "project"}},
		{"other name is no override", []string{"-builtin:mcp"}, []string{"+builtin:llama"}, true, builtinRow{"builtin:mcp", false, "builtin", "user"}},
		{"global bang entry", []string{"!builtin:mcp"}, nil, true, builtinRow{"builtin:mcp", false, "builtin", "user"}},
		{"global force include after exclude", []string{"!builtin:mcp", "+builtin:mcp"}, nil, true, builtinRow{"builtin:mcp", true, "builtin", "user"}},
		{"untrusted project is ignored", []string{"-builtin:mcp"}, []string{"+builtin:mcp"}, false, builtinRow{"builtin:mcp", false, "builtin", "user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
			if err := sm.SetExtensionPaths(tc.global); err != nil {
				t.Fatal(err)
			}
			if err := sm.SetProjectExtensionPaths(tc.project); err != nil {
				t.Fatal(err)
			}
			sm.SetProjectTrusted(tc.trusted)
			got := builtinRows(ResolveBuiltinExtensions(sm, []string{"mcp"}))
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("rows = %+v, want %+v", got, tc.want)
			}
		})
	}
}
