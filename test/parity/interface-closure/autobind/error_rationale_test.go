package main

import (
	"strings"
	"testing"
)

// The owner approval of 2026-10-01 (C5) covers only ai ModelsError.name and .stack and says not to widen it to any other row. Every
// other Error runtime member cites what does cover it: D103 for stack, reason class a for an always-unset cause, and Name() for name.
func TestErrorRuntimeRationaleCitesC5OnlyForModelsError(t *testing.T) {
	const c5 = "2026-10-01"
	for _, c := range []struct {
		member, owner string
		want          string
		cites         bool
	}{
		{"name", "ModelsError", c5, true},
		{"stack", "ModelsError", c5, true},
		{"cause", "ModelsError", "reason class a", false},
		{"stack", "PathError", "D103", false},
		{"cause", "McpError", "reason class a", false},
		{"name", "ClassifierHttpError", "Name()", false},
	} {
		got := errorRuntimeRationale(c.member, c.owner)
		if strings.Contains(got, c5) != c.cites || !strings.Contains(got, c.want) {
			t.Errorf("%s.%s: rationale %q, want it to contain %q and cite C5 = %t", c.owner, c.member, got, c.want, c.cites)
		}
	}
}
