package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Pi lists a resource directory with fs.readdirSync (skills.ts:192, prompt-templates.ts:168), whose order is libuv's scandir: sorted by
// name with strcmp on Unix, and the file system's own order on Windows, which on NTFS ignores case. The order decides the listing and
// which of two same-name skills wins. Probed natively with Pi 1.1.0 on Windows: prompts aprompt, Mprompt, Zprompt; skills alpha, beta, dup
// (from dupa), zeta.
func TestResourceDirectoriesKeepNodesReaddirOrder(t *testing.T) {
	agentDir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"Zeta", "alpha", "Beta"} {
		write(filepath.Join(agentDir, "skills", dir, "SKILL.md"), "---\nname: "+strings.ToLower(dir)+"\ndescription: skill "+dir+"\n---\nbody\n")
	}
	for _, dir := range []string{"dupB", "dupa"} {
		write(filepath.Join(agentDir, "skills", dir, "SKILL.md"), "---\nname: dup\ndescription: dup in "+dir+"\n---\nbody\n")
	}
	for _, name := range []string{"Zprompt", "aprompt", "Mprompt"} {
		write(filepath.Join(agentDir, "prompts", name+".md"), "---\ndescription: prompt "+name+"\n---\nbody\n")
	}

	wantSkills, wantWinner := []string{"beta", "zeta", "alpha", "dup"}, "dup in dupB"
	wantPrompts := []string{"Mprompt", "Zprompt", "aprompt"}
	if runtime.GOOS == "windows" {
		wantSkills, wantWinner = []string{"alpha", "beta", "dup", "zeta"}, "dup in dupa"
		wantPrompts = []string{"aprompt", "Mprompt", "Zprompt"}
	}

	loaded, err := LoadSkills(LoadSkillsOptions{CWD: t.TempDir(), AgentDir: agentDir, SkillPaths: []string{filepath.Join(agentDir, "skills")}})
	if err != nil {
		t.Fatal(err)
	}
	var skills []string
	winner := ""
	for _, skill := range loaded.Skills {
		skills = append(skills, skill.Name)
		if skill.Name == "dup" {
			winner = skill.Description
		}
	}
	if !slices.Equal(skills, wantSkills) || winner != wantWinner {
		t.Errorf("skills = %v with dup %q, want %v with %q", skills, winner, wantSkills, wantWinner)
	}

	var prompts []string
	for _, template := range LoadPromptTemplates("", "", filepath.Join(agentDir, "prompts")).Templates {
		prompts = append(prompts, template.Name)
	}
	if !slices.Equal(prompts, wantPrompts) {
		t.Errorf("prompts = %v, want %v", prompts, wantPrompts)
	}
}
