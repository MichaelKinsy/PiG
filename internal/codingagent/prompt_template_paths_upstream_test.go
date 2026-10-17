package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// upstream: resource-loader.ts reload loads the prompts with the --prompt-template paths after the agent directory's (:610-612), keeps the
// first of a name (dedupePrompts), then reports each --prompt-template path that does not exist (:615-626). Probed natively with Pi 1.1.0
// (an agent prompt aa-auto.md, `--prompt-template other/aa-auto.md --prompt-template other/zz-cli.md --prompt-template nosuch.md`):
//
//	[Prompt conflicts]
//	  "aa-auto" collision:
//	    ✓ auto (user) <agent>/prompts/aa-auto.md
//	    ✗ <other>/aa-auto.md (skipped)
//	  <cwd>/nosuch.md
//	    Prompt template path does not exist
//
// An extension's resources_discover reloads the prompts without the existence check.
func TestMissingPromptTemplatePathsAreReportedAfterTheLoadersDiagnostics(t *testing.T) {
	agentDir, other, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path, description string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\ndescription: "+description+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	auto := write(filepath.Join(agentDir, "prompts", "aa-auto.md"), "Auto user prompt")
	cliSame := write(filepath.Join(other, "aa-auto.md"), "CLI prompt with the auto name")
	cli := write(filepath.Join(other, "zz-cli.md"), "CLI prompt")
	missing := filepath.Join(cwd, "nosuch.md")
	m := &InteractiveMode{
		opts: InteractiveModeOptions{
			CWD: cwd, AgentDir: agentDir, NoThemes: true,
			PromptPaths:            []string{auto, cliSame, cli},
			CLIPromptTemplatePaths: []string{cliSame, cli, missing},
		},
		loadedResourcesContainer: tui.NewContainer(),
		resourceSourceInfo: map[string]ResourceSourceInfo{
			auto: {Path: auto, ResourceType: "prompts", Enabled: true, Source: "auto", Scope: "user", Origin: "top-level", BaseDir: filepath.Join(agentDir, "prompts")},
		},
	}
	m.loadPromptTemplates()
	m.reportMissingPromptTemplatePaths()
	if len(m.promptTemplates) != 2 || m.promptTemplates[0].Description != "Auto user prompt" {
		t.Fatalf("prompt templates = %+v, want the agent directory's aa-auto first", m.promptTemplates)
	}
	_, conflicts, ok := strings.Cut(renderedListing(m), "[Prompt conflicts]\n")
	slash := func(path string) string { return strings.ReplaceAll(formatDisplayPath(path), `\`, "/") }
	want := "  \"aa-auto\" collision:\n    ✓ auto (user) " + slash(auto) + "\n    ✗ " + slash(cliSame) + " (skipped)\n  " + slash(missing) + "\n    Prompt template path does not exist"
	if !ok || conflicts != want {
		t.Fatalf("[Prompt conflicts] =\n%s\nwant\n%s", conflicts, want)
	}

	m.loadPromptTemplates()
	for _, diagnostic := range m.promptDiagnostics {
		if diagnostic.Type == extension.DiagnosticError {
			t.Fatalf("a reload without the existence check reported %+v", diagnostic)
		}
	}
}

// The existence check (resource-loader.ts:615-626) appends one error per missing path, after the loader's own diagnostics, and skips a
// path a diagnostic already names. An extension's resources_discover reloads only the kinds it returned (extendResources, :476-499), so
// a discovery of skills or themes keeps the error and a discovery of prompts drops it.
func TestMissingPromptTemplatePathErrorFollowsTheLoadersDiagnostics(t *testing.T) {
	cwd := t.TempDir()
	bad := filepath.Join(cwd, "bad.md")
	if err := os.WriteFile(bad, []byte("---\ndescription: [broken\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(cwd, "skills", "s")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: s\ndescription: skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(cwd, "found.md")
	if err := os.WriteFile(prompt, []byte("---\ndescription: found\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(cwd, "nosuch.md")
	m := &InteractiveMode{opts: InteractiveModeOptions{CWD: cwd, AgentDir: t.TempDir(), NoThemes: true,
		PromptPaths: []string{bad}, CLIPromptTemplatePaths: []string{missing, missing}}}
	m.loadPromptTemplates()
	m.reportMissingPromptTemplatePaths()
	errorFor := extension.ResourceDiagnostic{Type: extension.DiagnosticError, Message: "Prompt template path does not exist", Path: missing}
	if len(m.promptDiagnostics) != 2 || m.promptDiagnostics[0].Path != bad || m.promptDiagnostics[0].Type != extension.DiagnosticWarning || m.promptDiagnostics[1] != errorFor {
		t.Fatalf("diagnostics = %+v, want the loader's warning for bad.md, then one %+v", m.promptDiagnostics, errorFor)
	}

	// A themes-only discovery reloads neither the prompts, which would drop the error, nor the skills, which would drop a diagnostic
	// from the launch.
	launchSkillDiagnostic := extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: "from the launch", Path: filepath.Join(cwd, "launch", "SKILL.md")}
	m.opts.SkillDiagnostics = []extension.ResourceDiagnostic{launchSkillDiagnostic}
	theme := filepath.Join(cwd, "theme.json")
	if err := os.WriteFile(theme, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.applyDiscoveredResources(&extension.ResourcesDiscoverAggregateResult{ThemePaths: []extension.AttributedResourcePath{{Path: theme, ExtensionPath: "/ext.ts"}}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.promptDiagnostics, errorFor) || !slices.Contains(m.opts.SkillDiagnostics, launchSkillDiagnostic) {
		t.Fatalf("a themes-only discovery reloaded other kinds: prompts %+v, skills %+v", m.promptDiagnostics, m.opts.SkillDiagnostics)
	}

	if err := m.applyDiscoveredResources(&extension.ResourcesDiscoverAggregateResult{SkillPaths: []extension.AttributedResourcePath{{Path: skillDir, ExtensionPath: "/ext.ts"}}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.promptDiagnostics, errorFor) {
		t.Fatalf("a skills-only discovery dropped the missing-path error: %+v", m.promptDiagnostics)
	}
	m.opts.SkillDiagnostics = []extension.ResourceDiagnostic{launchSkillDiagnostic}
	if err := m.applyDiscoveredResources(&extension.ResourcesDiscoverAggregateResult{PromptPaths: []extension.AttributedResourcePath{{Path: prompt, ExtensionPath: "/ext.ts"}}}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(m.promptDiagnostics, errorFor) {
		t.Fatalf("a prompts discovery kept the missing-path error: %+v", m.promptDiagnostics)
	}
	if !slices.Contains(m.opts.SkillDiagnostics, launchSkillDiagnostic) {
		t.Fatalf("a prompts discovery reloaded the skills: %+v", m.opts.SkillDiagnostics)
	}
}

// resource-loader.ts reload trims each --prompt-template path and resolves it against the Session's cwd before it checks it with
// existsSync (resolveResourcePath, :1035-1036), so trailing whitespace does not hide an existing file, two spellings of one missing path
// give one error with the trimmed path, and on Windows the namespaced path keeps a trailing dot, so "dot.md." is not "dot.md".
func TestMissingPromptTemplatePathCheckTrimsAndStatsAsNode(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"tab.md", "nbsp.md", "space.md", "dot.md"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("---\ndescription: d\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	join := func(name string) string { return filepath.Join(cwd, name) }
	m := &InteractiveMode{opts: InteractiveModeOptions{CWD: cwd, AgentDir: t.TempDir(), NoThemes: true, CLIPromptTemplatePaths: []string{
		join("tab.md") + "\t", join("nbsp.md") + "\u00A0", join("space.md") + " ", join("nosuch.md") + " ", join("nosuch.md"),
		join("nbsp-missing.md") + "\u00A0", join("dot.md") + ".", "relative.md",
	}}}
	m.reportMissingPromptTemplatePaths()
	var got []string
	for _, d := range m.promptDiagnostics {
		got = append(got, d.Path)
	}
	if want := []string{join("nosuch.md"), join("nbsp-missing.md"), join("dot.md") + ".", join("relative.md")}; !slices.Equal(got, want) {
		t.Fatalf("missing paths = %q, want %q", got, want)
	}
}
