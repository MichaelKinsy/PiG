package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func upstreamSkillsFixture(t *testing.T, subdir string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".upstream", "current", "packages", "coding-agent", "test", "fixtures", subdir))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCoreSkillMetadataUsesYAMLTypes(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		count          int
		wantName       string
		disabled       bool
	}{
		{"boolean description is not a string", "description: true", 0, "", false},
		{"boolean name falls back to directory", "name: true\ndescription: valid", 1, "typed", false},
		{"string true does not disable invocation", "description: valid\ndisable-model-invocation: \"true\"", 1, "typed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "typed")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+tc.metadata+"\n---\nbody"), 0o600); err != nil {
				t.Fatal(err)
			}
			result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: dir, Source: "test"})
			if len(result.Skills) != tc.count {
				t.Fatalf("result=%#v", result)
			}
			if tc.count > 0 && (result.Skills[0].Name != tc.wantName || result.Skills[0].DisableModelInvocation != tc.disabled) {
				t.Fatalf("skill=%#v", result.Skills[0])
			}
		})
	}
}

func TestUpstreamCoreSkillsFromDir(t *testing.T) {
	root := upstreamSkillsFixture(t, "skills")
	for _, tc := range []struct {
		name, dir, wantName, description, warning, notWarning string
		count                                                 int
		disabled                                              bool
		multiline, minimum                                    bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:31
		{name: "should load a valid skill", dir: "valid-skill", count: 1, wantName: "valid-skill", description: "A valid skill for testing purposes."},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:44
		{name: "should allow names that don't match parent directory", dir: "name-mismatch", count: 1, wantName: "different-name", notWarning: "does not match parent directory"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:57
		{name: "should warn when name contains invalid characters", dir: "invalid-name-chars", count: 1, warning: "invalid characters"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:67
		{name: "should warn when name exceeds 64 characters", dir: "long-name", count: 1, warning: "exceeds 64 characters"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:77
		{name: "should warn and skip skill when description is missing", dir: "missing-description", count: 0, warning: "description is required"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:87
		{name: "should ignore unknown frontmatter fields", dir: "unknown-field", count: 1},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:97
		{name: "should load nested skills recursively", dir: "nested", count: 1, wantName: "child-skill"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:108
		{name: "should prefer a directory's root SKILL.md over nested SKILL.md files", dir: "root-skill-preferred", count: 1, wantName: "root-skill-preferred", description: "Root skill should win."},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:120
		{name: "should skip files without frontmatter", dir: "no-frontmatter", count: 0, warning: "description is required"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:131
		{name: "should warn and skip skill when YAML frontmatter is invalid", dir: "invalid-yaml", count: 0, warning: "at line"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:141
		{name: "should preserve multiline descriptions from YAML", dir: "multiline-description", count: 1, multiline: true},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:153
		{name: "should warn when name contains consecutive hyphens", dir: "consecutive-hyphens", count: 1, warning: "consecutive hyphens"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:163
		{name: "should load all skills from fixture directory", dir: "", minimum: true, count: 6},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:175
		{name: "should return empty for non-existent directory", dir: "/non/existent/path", count: 0},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:185
		{name: "should use parent directory name when name not in frontmatter", dir: "valid-skill", count: 1, wantName: "valid-skill"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:198
		{name: "should parse disable-model-invocation frontmatter field", dir: "disable-model-invocation", count: 1, wantName: "disable-model-invocation", disabled: true, notWarning: "unknown frontmatter field"},
		// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:213
		{name: "should default disableModelInvocation to false when not specified", dir: "valid-skill", count: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, tc.dir)
			if filepath.IsAbs(tc.dir) {
				dir = tc.dir
			}
			result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: dir, Source: "test"})
			if tc.minimum {
				if len(result.Skills) < tc.count {
					t.Fatalf("skills=%d; want at least %d", len(result.Skills), tc.count)
				}
				return
			}
			if len(result.Skills) != tc.count {
				t.Fatalf("skills=%#v diagnostics=%#v; want %d", result.Skills, result.Diagnostics, tc.count)
			}
			messages := []string{}
			for _, d := range result.Diagnostics {
				messages = append(messages, d.Message)
			}
			joined := strings.Join(messages, "\n")
			if tc.warning != "" && !strings.Contains(joined, tc.warning) {
				t.Fatalf("diagnostics=%q; want %q", joined, tc.warning)
			}
			if tc.notWarning != "" && strings.Contains(joined, tc.notWarning) {
				t.Fatalf("unexpected diagnostic=%q", joined)
			}
			if tc.warning == "" && tc.notWarning == "" && len(result.Diagnostics) != 0 {
				t.Fatalf("diagnostics=%#v", result.Diagnostics)
			}
			if tc.count == 0 {
				return
			}
			skill := result.Skills[0]
			if tc.wantName != "" && skill.Name != tc.wantName {
				t.Fatalf("name=%q; want %q", skill.Name, tc.wantName)
			}
			if tc.description != "" && skill.Description != tc.description {
				t.Fatalf("description=%q; want %q", skill.Description, tc.description)
			}
			if skill.DisableModelInvocation != tc.disabled {
				t.Fatalf("disableModelInvocation=%v; want %v", skill.DisableModelInvocation, tc.disabled)
			}
			if tc.multiline && (!strings.Contains(skill.Description, "\n") || !strings.Contains(skill.Description, "This is a multiline description.")) {
				t.Fatalf("description=%q", skill.Description)
			}
			if skill.SourceInfo.Source != "test" {
				t.Fatalf("source=%#v", skill.SourceInfo)
			}
		})
	}
}

func TestUpstreamCoreSkillsOptions(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:352
	t.Run("should load from explicit skillPaths", func(t *testing.T) {
		result, err := LoadSkills(LoadSkillsOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), SkillPaths: []string{filepath.Join(upstreamSkillsFixture(t, "skills"), "valid-skill")}, IncludeDefaults: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Skills) != 1 || result.Skills[0].SourceInfo.Scope != "temporary" || len(result.Diagnostics) != 0 {
			t.Fatalf("result=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:364
	t.Run("should warn when skill path does not exist", func(t *testing.T) {
		result, err := LoadSkills(LoadSkillsOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), SkillPaths: []string{"/non/existent/path"}, IncludeDefaults: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Skills) != 0 || len(result.Diagnostics) == 0 || !strings.Contains(result.Diagnostics[0].Message, "does not exist") {
			t.Fatalf("result=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:375
	t.Run("should expand ~ in skillPaths", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(home, ".pi", "agent", "skills", "home-skill", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: home-skill\ndescription: Home skill\n---\nbody"), 0o600); err != nil {
			t.Fatal(err)
		}
		opts := LoadSkillsOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), SkillPaths: []string{"~/.pi/agent/skills"}, IncludeDefaults: true}
		withTilde, err := LoadSkills(opts)
		if err != nil {
			t.Fatal(err)
		}
		opts.SkillPaths = []string{filepath.Join(home, ".pi", "agent", "skills")}
		withoutTilde, err := LoadSkills(opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(withTilde.Skills) != len(withoutTilde.Skills) || len(withTilde.Skills) != 1 {
			t.Fatalf("tilde=%#v absolute=%#v", withTilde, withoutTilde)
		}
	})
}

// The upstream case explicitly simulates collision handling after loading each source.
// .upstream/v0.87.1/packages/coding-agent/test/skills.test.ts:394
func TestUpstreamCoreSkillsDetectNameCollisionsAndKeepFirst(t *testing.T) {
	root := upstreamSkillsFixture(t, "skills-collision")
	first := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: filepath.Join(root, "first"), Source: "first"})
	second := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: filepath.Join(root, "second"), Source: "second"})
	skills := map[string]*SkillDef{}
	warnings := []string{}
	for _, skill := range first.Skills {
		skills[skill.Name] = skill
	}
	for _, skill := range second.Skills {
		if prior, exists := skills[skill.Name]; exists {
			warnings = append(warnings, "name collision: \""+skill.Name+"\" already loaded from "+prior.FilePath)
		} else {
			skills[skill.Name] = skill
		}
	}
	if len(skills) != 1 || skills["calendar"] == nil || skills["calendar"].SourceInfo.Source != "first" || len(warnings) != 1 || !strings.Contains(warnings[0], "name collision") {
		t.Fatalf("skills=%#v warnings=%q", skills, warnings)
	}
}

// Leaf discovery guard using sdk-skills.test.ts's fixture. It does not prove default discovery by a native SDK Session, which must own and invoke the loader.
func TestUpstreamSDKSkillsDefaultDiscovery(t *testing.T) {
	tempDir := t.TempDir()
	skillsDir := filepath.Join(tempDir, "skills", "test-skill")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: A test skill for SDK tests.\n---\n\n# Test Skill\n\nThis is a test skill.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := LoadSkills(LoadSkillsOptions{CWD: tempDir, AgentDir: tempDir, IncludeDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) == 0 {
		t.Fatalf("no skills discovered: %#v", result)
	}
	found := false
	for _, skill := range result.Skills {
		found = found || skill.Name == "test-skill"
	}
	if !found {
		t.Fatalf("test-skill missing: %#v", result.Skills)
	}
}
