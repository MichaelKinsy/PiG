package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi skills.ts:425-454 suppresses the same real file before testing names,
// and records only winners in the real-path set.
func TestDeduplicateSkillsCollisionDiagnostics(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.md")
	alias := filepath.Join(root, "alias.md")
	if err := os.WriteFile(first, []byte("---\nname: same\ndescription: first\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, first, alias)
	defs := []*SkillDef{
		{Name: "same", FilePath: first}, {Name: "same", FilePath: alias},
		{Name: "same", FilePath: filepath.Join(root, "second.md")},
		{Name: "other", FilePath: filepath.Join(root, "other.md")},
		{Name: "same", FilePath: filepath.Join(root, "third.md")},
	}
	got, diagnostics := DeduplicateSkillsWithDiagnostics(defs)
	if !slices.Equal(got, []*SkillDef{defs[0], defs[3]}) {
		t.Fatalf("winners = %v", got)
	}
	losers := []string{defs[2].FilePath, defs[4].FilePath}
	if len(diagnostics) != len(losers) {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	for i, d := range diagnostics {
		if d.Type != "collision" || d.Message != `name "same" collision` || d.Path != losers[i] || d.Collision == nil || d.Collision.WinnerPath != first || d.Collision.LoserPath != losers[i] {
			t.Fatalf("diagnostic = %+v", d)
		}
	}
}

func TestReloadSkillsReplacesCollisionDiagnostics(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	root := t.TempDir()
	first, second := filepath.Join(root, "first.md"), filepath.Join(root, "second.md")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("---\nname: same\ndescription: a skill\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, paths := range [][]string{{first, second}, {second, first}} {
		m.opts.SkillPaths = paths
		m.reloadSkillsFromPaths()
		if len(m.opts.Skills) != 1 || m.opts.Skills[0].FilePath != paths[0] || len(m.opts.SkillDiagnostics) != 1 || m.opts.SkillDiagnostics[0].Collision.LoserPath != paths[1] {
			t.Fatalf("reload did not replace winners and diagnostics: %+v, %+v", m.opts.Skills, m.opts.SkillDiagnostics)
		}
	}
	m.opts.SkillPaths = []string{second}
	m.reloadSkillsFromPaths()
	if len(m.opts.Skills) != 1 || len(m.opts.SkillDiagnostics) != 0 {
		t.Fatalf("reload retained stale collisions: %+v", m.opts.SkillDiagnostics)
	}
	m.opts.SkillPaths = nil
	m.reloadSkillsFromPaths()
	if len(m.opts.Skills) != 0 || len(m.opts.SkillDiagnostics) != 0 {
		t.Fatal("reload retained removed skills")
	}
}

// Pi interactive-mode.ts:1642-1680,1855-1863 groups collisions in input
// order; only loaded resources contribute source metadata, not losers.
func TestShowLoadedResourcesSkillConflicts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "npm", "node_modules", "duplicates")
	winner := filepath.Join(root, "second", "SKILL.md")
	loser := filepath.Join(root, "first", "SKILL.md")
	defs, diagnostics := DeduplicateSkillsWithDiagnostics([]*SkillDef{{Name: "same", FilePath: winner}, {Name: "same", FilePath: loser}})
	m := &InteractiveMode{
		opts:                     InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true, Skills: defs, SkillDiagnostics: diagnostics},
		loadedResourcesContainer: tui.NewContainer(),
		resourceSourceInfo: map[string]ResourceSourceInfo{
			winner: {Path: winner, ResourceType: "skills", Enabled: true, Source: "npm:duplicates", Scope: "user", Origin: "package", BaseDir: root},
			loser:  {Path: loser, ResourceType: "skills", Enabled: true, Source: "npm:duplicates", Scope: "user", Origin: "package", BaseDir: root},
		},
	}
	// renderedListing spells separators as "/", as upstream's listing tests do.
	want := "[Skills]\n  same\n\n[Skill conflicts]\n  \"same\" collision:\n    ✓ npm:duplicates (user) second/SKILL.md\n    ✗ " + strings.ReplaceAll(formatDisplayPath(loser), `\`, "/") + " (skipped)"
	if got := renderedListing(m); got != want {
		t.Fatalf("listing = %q; want %q", got, want)
	}
	m.opts.SkillDiagnostics = nil
	if got := renderedListing(m); got != "[Skills]\n  same" {
		t.Fatalf("stale diagnostics = %q", got)
	}
}
