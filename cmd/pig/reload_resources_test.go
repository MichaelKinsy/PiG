package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestReloadResourceSnapshotProvider_RecomputesSettingsBackedPaths(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()

	promptA := filepath.Join(agentDir, "prompts", "a.md")
	promptB := filepath.Join(agentDir, "prompts", "b.md")
	skillA := filepath.Join(agentDir, "skills", "alpha", "SKILL.md")
	skillB := filepath.Join(agentDir, "skills", "beta", "SKILL.md")
	for path, body := range map[string]string{
		promptA: "# a",
		promptB: "# b",
		skillA:  "# alpha",
		skillB:  "# beta",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctxFile := filepath.Join(cwd, "AGENTS.md")
	if err := os.WriteFile(ctxFile, []byte("# context"), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *codingagent.Settings) {
		s.Prompts = []string{"!b.md", "+a.md"}
		s.Skills = []string{"!beta", "+alpha"}
	}); err != nil {
		t.Fatal(err)
	}

	provider := reloadResourceSnapshotProvider(cwd, agentDir, sm, CLIFlags{}, nil)
	first := provider()
	if !slices.Contains(first.PromptPaths, promptA) || slices.Contains(first.PromptPaths, promptB) {
		t.Fatalf("first PromptPaths = %v", first.PromptPaths)
	}
	if !slices.Contains(first.SkillPaths, filepath.Join(agentDir, "skills", "alpha")) {
		t.Fatalf("first SkillPaths = %v", first.SkillPaths)
	}
	if len(first.ContextFiles) != 1 || first.ContextFiles[0].Path != ctxFile {
		t.Fatalf("first ContextFiles = %+v", first.ContextFiles)
	}
	if _, ok := first.ResourceSourceInfo[promptA]; !ok {
		t.Fatalf("first ResourceSourceInfo missing %s: %#v", promptA, first.ResourceSourceInfo)
	}

	if err := sm.UpdateGlobal(func(s *codingagent.Settings) {
		s.Prompts = []string{"!a.md", "+b.md"}
		s.Skills = []string{"!alpha", "+beta"}
	}); err != nil {
		t.Fatal(err)
	}

	second := provider()
	if !slices.Contains(second.PromptPaths, promptB) || slices.Contains(second.PromptPaths, promptA) {
		t.Fatalf("second PromptPaths = %v", second.PromptPaths)
	}
	if !slices.Contains(second.SkillPaths, filepath.Join(agentDir, "skills", "beta")) || slices.Contains(second.SkillPaths, filepath.Join(agentDir, "skills", "alpha")) {
		t.Fatalf("second SkillPaths = %v", second.SkillPaths)
	}
	if info, ok := second.ResourceSourceInfo[promptB]; !ok || !info.Enabled {
		t.Fatalf("second ResourceSourceInfo enabled prompt missing %s: %#v", promptB, second.ResourceSourceInfo)
	}
	if info, ok := second.ResourceSourceInfo[promptA]; !ok || info.Enabled {
		t.Fatalf("second ResourceSourceInfo should retain disabled prompt metadata for %s: %#v", promptA, second.ResourceSourceInfo)
	}
}

// Reload preserves the exact active tool list and associated prompt rules from startup.
func TestSystemPromptRebuilderPreservesStartupTools(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	for _, selected := range [][]string{{"powershell"}, {"bash", "powershell"}, {"read", "edit"}} {
		prompt, opts := systemPromptRebuilder(cwd, agentDir, true, CLIFlags{}, selected)(nil, nil)
		if !slices.Equal(opts.SelectedTools, selected) {
			t.Errorf("startup tools %v: SelectedTools = %v", selected, opts.SelectedTools)
		}
		for _, name := range selected {
			if !strings.Contains(prompt, "- "+name+":") {
				t.Errorf("startup tools %v: prompt missing %q:\n%s", selected, name, prompt)
			}
		}
	}
}
