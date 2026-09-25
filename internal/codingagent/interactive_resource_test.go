package codingagent

import (
	"strings"
	"testing"
)

func TestResourceCollisionDiagnostics_SkillIncludesSourceInfo(t *testing.T) {
	winner := "/tmp/pkg-a/skills/demo"
	loser := "/tmp/pkg-b/skills/demo"
	m := &InteractiveMode{
		resourceSourceInfo: map[string]ResourceSourceInfo{
			winner: {Path: winner, ResourceType: "skills", Enabled: true, Scope: "user", Origin: "package", Source: "npm:pkg-a", BaseDir: "/tmp/pkg-a"},
			loser:  {Path: loser, ResourceType: "skills", Enabled: true, Scope: "project", Origin: "package", Source: "git:https://example.com/pkg-b.git", BaseDir: "/tmp/pkg-b"},
		},
		opts: InteractiveOptions{Skills: []*SkillDef{{Name: "demo", Path: winner}}},
	}
	got := m.collisionDiagnosticsForSkills()
	if len(got) != 1 {
		t.Fatalf("len(collisionDiagnosticsForSkills) = %d, want 1 (%v)", len(got), got)
	}
	for _, want := range []string{
		`[skill] "demo" collision:`,
		`npm:pkg-a (user)`,
		`git:https://example.com/pkg-b.git (project)`,
		`(skipped)`,
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("diagnostic missing %q in %q", want, got[0])
		}
	}
}
