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
	docs := filepath.Dir(GetReadmePath()) // read.ts getPiDocsClassification: packageRoot = dirname(getReadmePath())
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

// config.ts:444-456 getReadmePath, getDocsPath and getExamplesPath: the README sits in the documentation bundle (D22: the bundle is the docs directory), so
// the bundle root read.ts classifies against is dirname(getReadmePath()) and the docs path is that same directory; examples are linked, not local.
func TestReadmeDocsAndExamplesPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	docs := GetDocsPath()
	if got, want := GetReadmePath(), filepath.Join(docs, "README.md"); got != want {
		t.Fatalf("GetReadmePath() = %q, want %q", got, want)
	}
	if filepath.Dir(GetReadmePath()) != docs {
		t.Fatalf("dirname(GetReadmePath()) = %q, want the docs path %q", filepath.Dir(GetReadmePath()), docs)
	}
	// A page outside the bundle (the cwd's own README.md) is not documentation.
	cwd := t.TempDir()
	if got, ok := classifyCompactRead(filepath.Join(cwd, "README.md"), cwd); ok && got.Kind == "docs" {
		t.Fatalf("a README.md outside the bundle classified as docs: %+v", got)
	}
}
