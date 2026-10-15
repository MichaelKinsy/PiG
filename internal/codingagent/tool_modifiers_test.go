package codingagent

import (
	"slices"
	"testing"
)

// settings-manager.ts (v1.1.0, ddaa0a03) getToolListError: a tool list is plain names and patterns, or only +name/-name entries with exact names.
func TestGetToolListErrorMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
	}{
		{"empty", []string{}, ""},
		{"nil", nil, ""},
		{"plain names", []string{"read", "bash"}, ""},
		{"patterns are allowed in an allowlist", []string{"mcp__radius__*", "read"}, ""},
		{"only modifiers", []string{"+codemode", "-write"}, ""},
		{"a plain name mixed with a modifier", []string{"read", "+codemode"}, "tool names cannot be mixed with +name or -name entries"},
		{"a modifier first, then a plain name", []string{"-write", "read"}, "tool names cannot be mixed with +name or -name entries"},
		{"a pattern in a modifier", []string{"+mcp__radius__*"}, "+name and -name entries take exact tool names, not patterns: +mcp__radius__*"},
		{"the first pattern is reported", []string{"+read", "-gr*", "+f*"}, "+name and -name entries take exact tool names, not patterns: -gr*"},
		{"mixing wins over a pattern", []string{"gr*", "+a*"}, "tool names cannot be mixed with +name or -name entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetToolListError(tc.entries); got != tc.want {
				t.Errorf("GetToolListError(%q) = %q, want %q", tc.entries, got, tc.want)
			}
		})
	}
}

// settings-manager.ts (v1.1.0) applyToolModifiers: `+name` adds an absent tool, `-name` removes a present one, in order; other entries are ignored and base is not changed.
func TestApplyToolModifiersMatchesUpstream(t *testing.T) {
	base := []string{"read", "bash"}
	for _, tc := range []struct {
		name    string
		entries []string
		want    []string
	}{
		{"no entries", nil, []string{"read", "bash"}},
		{"add", []string{"+grep"}, []string{"read", "bash", "grep"}},
		{"remove", []string{"-read"}, []string{"bash"}},
		{"add of a present tool is ignored", []string{"+read"}, []string{"read", "bash"}},
		{"removal of an absent tool is ignored", []string{"-nope"}, []string{"read", "bash"}},
		{"a bare plus adds nothing", []string{"+", "-"}, []string{"read", "bash"}},
		{"entries apply in order", []string{"-read", "+read", "+x", "-x", "+x"}, []string{"bash", "read", "x"}},
		{"plain entries are ignored", []string{"write", "+grep"}, []string{"read", "bash", "grep"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyToolModifiers(base, tc.entries); !slices.Equal(got, tc.want) {
				t.Errorf("ApplyToolModifiers = %q, want %q", got, tc.want)
			}
			if !slices.Equal(base, []string{"read", "bash"}) {
				t.Errorf("base changed to %q", base)
			}
		})
	}
}
