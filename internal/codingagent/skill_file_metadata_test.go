package codingagent

import (
	"path/filepath"
	"strings"
	"testing"
)

// Resource-loader collisions come from deduplication, not a second scan of provenance metadata.
// Pi formatDiagnostics preserves the winner's file path and does not treat an unrelated skill as a collision.
func TestSkillFileMetadataRetainsCollisionDiagnostics(t *testing.T) {
	isolateDisplayHome(t)
	root := t.TempDir()
	winner := filepath.Join(root, "pkg-a", "skills", "demo", "SKILL.md")
	loser := filepath.Join(root, "pkg-b", "skills", "demo", "SKILL.md")
	other := filepath.Join(root, "pkg-c", "skills", "other", "SKILL.md")
	m := upstreamListingMode(t, false, nil)
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		winner: {Path: winner, ResourceType: "skills", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg-a", BaseDir: filepath.Join(root, "pkg-a")},
		loser:  {Path: loser, ResourceType: "skills", Enabled: true, Scope: "project", Origin: "package", Source: "git:https://example.com/pkg-b.git", BaseDir: filepath.Join(root, "pkg-b")},
		other:  {Path: other, ResourceType: "skills", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg-c"},
	}
	m.opts.Skills, m.opts.SkillDiagnostics = DeduplicateSkillsWithDiagnostics([]*SkillDef{
		{Name: "demo", Path: winner}, {Name: "demo", Path: loser}, {Name: "other", Path: other},
	})
	m.showLoadedResources(false, true)
	want := "[Skill conflicts]\n  \"demo\" collision:\n    ✓ npm:pkg-a (user) skills/demo/SKILL.md\n    ✗ " + filepath.ToSlash(loser) + " (skipped)"
	if got := strings.ReplaceAll(renderListing(m), `\`, "/"); got != want {
		t.Fatalf("diagnostics =\n%s\nwant\n%s", got, want)
	}
}
