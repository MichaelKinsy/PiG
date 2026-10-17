package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/test/suite/regressions/2781-skill-collision-precedence.test.ts:66,81,96,112.
func TestSkillCollisionPrecedenceUpstream(t *testing.T) {
	for _, tc := range []struct {
		name          string
		user, project bool
	}{
		{"user overrides package", true, false},
		{"project overrides package", false, true},
		{"project overrides user and package", true, true},
		{"collision reports package loser", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home, cwd, agentDir := filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "agent")
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
			pkg := filepath.Join(root, "fake-package-web-fetch")
			writeSkill := func(dir, description, content string) string {
				t.Helper()
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "SKILL.md")
				if err := os.WriteFile(path, []byte("---\nname: web-fetch\ndescription: "+description+"\n---\n"+content), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			packagePath := writeSkill(filepath.Join(pkg, "skills", "web-fetch"), "Package web-fetch skill", "Package skill content")
			if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"fake-pkg-web-fetch","version":"1.0.0","pi":{"skills":["skills/web-fetch"]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var wantPath, wantDescription string
			if tc.user {
				wantDescription = "User web-fetch override"
				wantPath = writeSkill(filepath.Join(agentDir, "skills", "web-fetch"), wantDescription, "User skill content")
			}
			if tc.project {
				wantDescription = "Project web-fetch override"
				wantPath = writeSkill(filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "skills", "web-fetch"), wantDescription, "Project skill content")
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
				t.Fatal(err)
			}
			if err := validateConfiguredPackagesForStartup(cwd, sm, func(string) bool { return true }); err != nil {
				t.Fatal(err)
			}
			loaded, err := resolveAndLoadSkills(nil, collectSkillInputs(cwd, agentDir, sm, Args{}, nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Defs) != 1 || loaded.Defs[0].Name != "web-fetch" || loaded.Defs[0].FilePath != wantPath || loaded.Defs[0].Description != wantDescription {
				t.Fatalf("skills = %+v, want web-fetch at %s (%s)", loaded.Defs, wantPath, wantDescription)
			}
			for _, diagnostic := range loaded.Diagnostics {
				if diagnostic.Type == "collision" && diagnostic.Collision != nil && diagnostic.Collision.Name == "web-fetch" && diagnostic.Collision.LoserPath == packagePath {
					return
				}
			}
			t.Fatalf("missing package-loser collision: %+v", loaded.Diagnostics)
		})
	}
}

// Pi skills.ts:425-454 chooses declaration order, not directory or name order.
func TestConfiguredPackageSkillCollisionFirstWins(t *testing.T) {
	home, cwd, agentDir, pkg := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"collision","pi":{"skills":["second/SKILL.md","first/SKILL.md"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"first", "second"} {
		if err := os.MkdirAll(filepath.Join(pkg, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, dir, "SKILL.md"), []byte("---\nname: same\ndescription: "+dir+"\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
		t.Fatal(err)
	}
	if err := validateConfiguredPackagesForStartup(cwd, sm, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	inputs := collectSkillInputs(cwd, agentDir, sm, Args{}, nil)
	loaded, err := resolveAndLoadSkills(nil, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Defs) != 1 || loaded.Defs[0].Description != "second" {
		t.Fatalf("winner = %+v", loaded.Defs)
	}
	if len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].Message != `name "same" collision` || loaded.Diagnostics[0].Collision.LoserPath != filepath.Join(pkg, "first", "SKILL.md") {
		t.Fatalf("diagnostics = %+v", loaded.Diagnostics)
	}
}
