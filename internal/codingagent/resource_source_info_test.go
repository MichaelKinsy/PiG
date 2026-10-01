package codingagent

import "testing"

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/source-info.ts:14-29. Upstream has no test file for these
// helpers; the rows are the branches of getSyntheticPathSource and isSyntheticPath.
func TestSyntheticPathSource(t *testing.T) {
	for _, tc := range []struct {
		path      string
		source    string
		synthetic bool
	}{
		{"builtin:read", "builtin", true},
		{"builtin:llama.cpp", "builtin", true},
		{"<inline:llama>", "inline", true},
		{"<inline>", "inline", true},
		{"<temporary>", "temporary", true},
		{"<builtin:read>", "builtin", true},
		{"<>", "temporary", true},
		{"<:x>", "temporary", true},
		{"<a", "", true},
		{"a>", "", false},
		{"/tmp/extensions/a.ts", "", false},
		{"mcp", "", false},
		{"", "", false},
	} {
		if got := SyntheticPathSource(tc.path); got != tc.source {
			t.Errorf("SyntheticPathSource(%q) = %q, want %q", tc.path, got, tc.source)
		}
		if got := IsSyntheticPath(tc.path); got != tc.synthetic {
			t.Errorf("IsSyntheticPath(%q) = %t, want %t", tc.path, got, tc.synthetic)
		}
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/utils/paths.ts:45-64: a `builtin:` value is not a local path.
func TestIsLocalPathExcludesBuiltinExtensions(t *testing.T) {
	for value, want := range map[string]bool{
		"builtin:mcp": false, "  builtin:mcp  ": false, "builtin": true, "./builtin:mcp": true, "my-package": true,
	} {
		if got := IsLocalPath(value); got != want {
			t.Errorf("IsLocalPath(%q) = %t, want %t", value, got, want)
		}
	}
}
