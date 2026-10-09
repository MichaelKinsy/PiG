package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi interactive-mode.ts:1741-1765 builds diagnostic provenance from surviving resources, not rejected duplicates.
// The winner retains its Package label; a loser with no surviving source ancestor uses its full path (formatDiagnostics:1664-1677).
func TestResourceCollisionDiagnostics_SkillIncludesSourceInfo(t *testing.T) {
	winner := filepath.FromSlash("/pkg-a/skills/demo/SKILL.md")
	loser := filepath.FromSlash("/pkg-b/skills/demo/SKILL.md")
	m := upstreamListingMode(t, false, nil)
	m.opts.Settings.QuietStartup = QuietStartupTrue
	m.resourceSourceInfo = map[string]ResourceSourceInfo{
		winner: {Path: winner, ResourceType: "skills", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg-a", BaseDir: filepath.FromSlash("/pkg-a")},
		loser:  {Path: loser, ResourceType: "skills", Enabled: true, Scope: "project", Origin: "package", Source: "git:https://example.com/pkg-b.git", BaseDir: filepath.FromSlash("/pkg-b")},
	}
	m.opts.Skills = []*SkillDef{{Name: "demo", FilePath: winner}}
	m.opts.SkillDiagnostics = []extension.ResourceDiagnostic{collisionDiagnostic("skill", "demo", winner, loser)}
	m.showLoadedResources(false, true)
	want := "[Skill conflicts]\n  \"demo\" collision:\n    ✓ npm:pkg-a (user) skills/demo/SKILL.md\n    ✗ /pkg-b/skills/demo/SKILL.md (skipped)"
	if got := strings.ReplaceAll(renderListing(m), `\`, "/"); got != want {
		t.Fatalf("diagnostics =\n%s\nwant\n%s", got, want)
	}
}
