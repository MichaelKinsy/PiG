package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// resource-loader.ts normalizeExtensionPaths resolves all three resource kinds before extendResources publishes any paths or metadata.
func TestNormalizeExtensionPaths(t *testing.T) {
	cwd := t.TempDir()
	input := &extension.ResourcesDiscoverAggregateResult{
		SkillPaths:  []extension.AttributedResourcePath{{Path: "\uFEFF  extra skills/skill  \uFEFF", ExtensionPath: "/extra.ts"}},
		PromptPaths: []extension.AttributedResourcePath{{Path: fileURLForTest(filepath.Join(cwd, "extra prompts", "prompt.md")).String(), ExtensionPath: "/extra.ts"}},
		ThemePaths:  []extension.AttributedResourcePath{{Path: "themes/theme.json", ExtensionPath: "/extra.ts"}},
	}
	want := &extension.ResourcesDiscoverAggregateResult{
		SkillPaths:  []extension.AttributedResourcePath{{Path: filepath.Join(cwd, "extra skills", "skill"), ExtensionPath: "/extra.ts"}},
		PromptPaths: []extension.AttributedResourcePath{{Path: filepath.Join(cwd, "extra prompts", "prompt.md"), ExtensionPath: "/extra.ts"}},
		ThemePaths:  []extension.AttributedResourcePath{{Path: filepath.Join(cwd, "themes", "theme.json"), ExtensionPath: "/extra.ts"}},
	}
	got, err := NormalizeExtensionPaths(cwd, input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized paths=%#v, error=%v; want=%#v", got, err, want)
	}
	if input.SkillPaths[0].Path != "\uFEFF  extra skills/skill  \uFEFF" {
		t.Fatal("normalization mutated the extension result")
	}
	input.ThemePaths[0].Path = "file:///%2Fbad"
	if got, err := NormalizeExtensionPaths(cwd, input); err == nil || got != nil {
		t.Fatalf("invalid URL returned partial result: %#v, %v", got, err)
	}
	if got, err := NormalizeExtensionPaths(cwd, nil); err != nil || got != nil {
		t.Fatalf("absent resources = %#v, %v", got, err)
	}
}

func TestDedupBySymlinkBasic(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	got := DedupBySymlink([]string{a, b})
	if len(got) != 2 {
		t.Fatalf("two distinct files → 2 entries, got %d (%v)", len(got), got)
	}
}

func TestResourceSymlinkDedup(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md") // symlink → a.md
	if err := os.WriteFile(a, []byte("real"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	testenv.Symlink(t, a, b)

	got := DedupBySymlink([]string{a, b})
	if len(got) != 1 {
		t.Fatalf("symlinked alias should dedup to 1 entry, got %d (%v)", len(got), got)
	}

	canonA, _ := filepath.EvalSymlinks(a)
	if got[0].Canonical != canonA {
		t.Errorf("canonical: want %q got %q", canonA, got[0].Canonical)
	}
	// First-seen wins (a was input first).
	if got[0].Original != a {
		t.Errorf("first-seen wins: want %q got %q", a, got[0].Original)
	}
}

func TestDedupFallbackOnBrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	broken1 := filepath.Join(dir, "broken1")
	broken2 := filepath.Join(dir, "broken2")
	testenv.Symlink(t, filepath.Join(dir, "nonexistent-target-1"), broken1)
	testenv.Symlink(t, filepath.Join(dir, "nonexistent-target-2"), broken2)

	// Each broken symlink falls back to its own raw path → both kept.
	got := DedupBySymlink([]string{broken1, broken2})
	if len(got) != 2 {
		t.Fatalf("two broken symlinks → 2 entries (fallback to raw path), got %d (%v)", len(got), got)
	}
}

func TestDedupSymlinkLoopGuarded(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	testenv.Symlink(t, b, a)
	testenv.Symlink(t, a, b)

	// Loop: EvalSymlinks errors out → fallback to raw paths → both kept.
	// Important: the call must return, not loop forever.
	done := make(chan []ResolvedPath, 1)
	go func() {
		done <- DedupBySymlink([]string{a, b})
	}()
	select {
	case got := <-done:
		if len(got) != 2 {
			t.Fatalf("loop fallback → 2 entries, got %d (%v)", len(got), got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DedupBySymlink hung on a loop")
	}
}

func TestListSkillsDedupsSymlinkedDirs(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "commit")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte("---\nname: commit\n---\nbody"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	aliasDir := filepath.Join(root, "commit-alias")
	testenv.RequireDirectoryLink(t, realDir, aliasDir)

	names, err := ListSkills(root)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("symlinked skill dir must collapse to one entry, got %v", names)
	}
}

// (timeoutChan helper removed: using time.After inline.)
