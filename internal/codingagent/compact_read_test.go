package codingagent

import (
	"path/filepath"
	"testing"
)

// Upstream getCompactReadClassification: SKILL.md by its directory, the
// agent's documentation by its page, context files by their cwd-relative
// path, and nothing else.
func TestClassifyCompactRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	cwd := t.TempDir()
	docs := filepath.Join(ConfigRoot(), "docs")
	cases := []struct {
		path, kind, label string
	}{
		{filepath.Join(cwd, "skills", "demo", "SKILL.md"), "skill", "demo"},
		{filepath.Join(docs, "README.md"), "docs", "README.md"},
		{filepath.Join(docs, "extensions.md"), "docs", "docs/extensions.md"},
		{"sub/AGENTS.md", "resource", "sub/AGENTS.md"},
		{"CLAUDE.md", "resource", "CLAUDE.md"},
		{"notes.md", "", ""},
	}
	for _, c := range cases {
		got, ok := classifyCompactRead(c.path, cwd)
		if ok != (c.kind != "") || got.Kind != c.kind || got.Label != c.label {
			t.Errorf("classifyCompactRead(%q) = %+v, %v; want %s %q", c.path, got, ok, c.kind, c.label)
		}
	}
}
