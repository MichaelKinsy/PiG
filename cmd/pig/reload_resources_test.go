package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/resource-loader.test.ts:685-782. Extending the headless resource catalog must not erase npm Package provenance for any resource kind.
func TestResourceLoaderUpstreamPackageMetadata(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(agentDir, "npm", "node_modules", "metadata-pkg")
	extensionRoot := filepath.Join(root, "extension-resources")
	writeResourceLoaderFixture(t, filepath.Join(packageRoot, "package.json"), `{"name":"metadata-pkg","version":"1.0.0"}`)
	packageSkill := filepath.Join(packageRoot, "skills", "package-skill", "SKILL.md")
	packagePrompt := filepath.Join(packageRoot, "prompts", "package-prompt.md")
	packageTheme := filepath.Join(packageRoot, "themes", "package-theme.json")
	extensionSkill := filepath.Join(extensionRoot, "extension-skill", "SKILL.md")
	extensionPrompt := filepath.Join(extensionRoot, "prompts", "extension-prompt.md")
	extensionTheme := filepath.Join(extensionRoot, "themes", "extension.json")
	for path, content := range map[string]string{
		packageSkill:    "---\nname: package-skill\ndescription: Package skill\n---\nPackage skill content",
		packagePrompt:   "---\ndescription: Package prompt\n---\nPackage prompt content",
		packageTheme:    resourceLoaderTheme(t, "package-theme"),
		extensionSkill:  "---\nname: extension-skill\ndescription: Extension skill\n---\nExtension skill content",
		extensionPrompt: "---\ndescription: Extension prompt\n---\nExtension prompt content",
		extensionTheme:  resourceLoaderTheme(t, "extension-theme"),
	} {
		writeResourceLoaderFixture(t, path, content)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:metadata-pkg"}}); err != nil {
		t.Fatal(err)
	}
	snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, CLIFlags{}, nil)()
	skills, err := resolveAndLoadSkills(nil, snapshot.SkillPaths)
	if err != nil {
		t.Fatal(err)
	}
	loadedPrompts := codingagent.LoadPromptTemplates("", "", snapshot.PromptPaths...)
	catalog := headlessCommandCatalog{cwd: cwd, agentDir: agentDir, skills: skills.Defs, promptTemplates: loadedPrompts.Templates, sourceInfo: snapshot.ResourceSourceInfo}
	runner := inproc.NewRunner([]extension.Extension{{Path: filepath.Join(root, "discovery.ts"), Handlers: map[string][]extension.HandlerFn{
		codingagent.EventResourcesDiscover: {func(...any) (any, error) {
			return &extension.ResourcesDiscoverResult{SkillPaths: []string{filepath.Dir(extensionSkill)}, PromptPaths: []string{filepath.Dir(extensionPrompt)}, ThemePaths: []string{filepath.Dir(extensionTheme)}}, nil
		}},
	}}}, cwd)
	if changed, err := catalog.extendFromExtensions(t.Context(), runner, "startup"); err != nil || !changed {
		t.Fatalf("extend resources = %v, %v", changed, err)
	}
	if len(catalog.skills) != 2 || catalog.skills[0].Name != "package-skill" || catalog.skills[1].Name != "extension-skill" {
		t.Fatalf("skills = %#v", catalog.skills)
	}
	if len(catalog.promptTemplates) != 2 || catalog.promptTemplates[0].Name != "package-prompt" || catalog.promptTemplates[1].Name != "extension-prompt" {
		t.Fatalf("prompts = %#v", catalog.promptTemplates)
	}
	if !slices.Equal(snapshot.ThemePaths, []string{packageTheme}) {
		t.Fatalf("package theme paths = %v", snapshot.ThemePaths)
	}
	for path, name := range map[string]string{packageTheme: "package-theme", extensionTheme: "extension-theme"} {
		theme, err := tui.LoadThemeFile(path)
		if err != nil || theme.Name != name {
			t.Fatalf("theme %s = %#v, error = %v", path, theme, err)
		}
	}
	infoCatalog := codingagent.SlashCommandCatalog{CWD: cwd, AgentDir: agentDir, SourceInfo: catalog.sourceInfo}
	for path, kind := range map[string]string{packageSkill: "skills", packagePrompt: "prompts", packageTheme: "themes", extensionSkill: "skills", extensionPrompt: "prompts", extensionTheme: "themes"} {
		wantSource, wantScope, wantOrigin := "npm:metadata-pkg", "user", "package"
		if strings.HasPrefix(path, extensionRoot+string(filepath.Separator)) {
			wantSource, wantScope, wantOrigin = "extension:discovery", "temporary", "top-level"
		}
		info := infoCatalog.SourceInfoForPath(path, kind)
		if info.Source != wantSource || info.Scope != wantScope || info.Origin != wantOrigin || info.Path != path {
			t.Fatalf("source info %s = %#v; want %s/%s/%s", path, info, wantSource, wantScope, wantOrigin)
		}
	}
}

// resource-loader.test.ts:645-681 exercises file URLs; the headless production path must match interactive resource application and reject malformed URLs without publishing partial state.
func TestResourceLoaderUpstreamHeadlessFileURLs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "extra skills", "file-url-skill", "SKILL.md")
	writeResourceLoaderFixture(t, path, "---\nname: file-url-skill\ndescription: File URL skill\n---\nExtra content")
	for _, input := range []string{(&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(path)), "/")}).String(), "file:///%2Fbad"} {
		catalog := headlessCommandCatalog{cwd: root, agentDir: t.TempDir()}
		runner := inproc.NewRunner([]extension.Extension{{Path: filepath.Join(root, "file-url.ts"), Handlers: map[string][]extension.HandlerFn{
			codingagent.EventResourcesDiscover: {func(...any) (any, error) {
				return &extension.ResourcesDiscoverResult{SkillPaths: []string{input}}, nil
			}},
		}}}, root)
		changed, err := catalog.extendFromExtensions(t.Context(), runner, "startup")
		if input == "file:///%2Fbad" {
			if err == nil || changed || len(catalog.skills) != 0 || len(catalog.sourceInfo) != 0 {
				t.Fatalf("invalid URL changed state: changed=%v err=%v catalog=%#v", changed, err, catalog)
			}
			continue
		}
		if err != nil || !changed || len(catalog.skills) != 1 || catalog.skills[0].Path != path {
			t.Fatalf("file URL skills=%#v changed=%v error=%v", catalog.skills, changed, err)
		}
		info := (codingagent.SlashCommandCatalog{CWD: root, SourceInfo: catalog.sourceInfo}).SourceInfoForPath(path, "skills")
		if info.Source != "extension:file-url" {
			t.Fatalf("file URL source info = %#v", info)
		}
	}
}

// The complete RPC startup path carries decoded resources_discover URLs through the Node host into get_commands, including when automatic discovery is disabled.
func TestResourceLoaderUpstreamRPCFileURLs(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			for _, dir := range []string{cwd, agentDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			skillPath := filepath.Join(root, "extra skills", "file-url-skill", "SKILL.md")
			promptPath := filepath.Join(root, "extra prompts", "extra.md")
			writeResourceLoaderFixture(t, skillPath, "---\nname: file-url-skill\ndescription: File URL skill\n---\nExtra content")
			writeResourceLoaderFixture(t, promptPath, "---\ndescription: Extra prompt\n---\nExtra prompt content")
			input := fmt.Sprintf("pathToFileURL(%q).href", filepath.Dir(skillPath))
			if invalid {
				input = `"file:///%2Fbad"`
			}
			entry := filepath.Join(root, "file-url.ts")
			writeResourceLoaderFixture(t, entry, fmt.Sprintf(`import {pathToFileURL} from "node:url";
export default function(pi) {pi.on("resources_discover", () => ({skillPaths: [%s], promptPaths: [pathToFileURL(%q).href]}));}`, input, promptPath))
			run := runPigStartup(t, binary, root, agentDir, cwd, "{\"id\":\"commands\",\"type\":\"get_commands\"}\n", "--mode", "rpc", "--no-session", "--no-skills", "--no-prompt-templates", "-e", entry)
			if invalid {
				if run.err == nil || !strings.Contains(run.stderr, invalidFileURLMessage()) || strings.Contains(run.stderr, "extension resource") || strings.Contains(run.stdout, `"id":"commands"`) {
					t.Fatalf("invalid resource URL startup: err=%v stdout=%s stderr=%s", run.err, run.stdout, run.stderr)
				}
				return
			}
			if run.err != nil {
				t.Fatalf("RPC startup: %v\n%s\n%s", run.err, run.stdout, run.stderr)
			}
			var response struct {
				ID      string `json:"id"`
				Success bool   `json:"success"`
				Data    struct {
					Commands []struct {
						Name       string                   `json:"name"`
						SourceInfo codingagent.PiSourceInfo `json:"sourceInfo"`
					} `json:"commands"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(run.stdout)), &response); err != nil || response.ID != "commands" || !response.Success {
				t.Fatalf("RPC response=%s error=%v", run.stdout, err)
			}
			for name, path := range map[string]string{"skill:file-url-skill": skillPath, "extra": promptPath} {
				matches := 0
				for _, command := range response.Data.Commands {
					if command.Name != name {
						continue
					}
					matches++
					if command.SourceInfo.Path != path || command.SourceInfo.Source != "extension:file-url" || command.SourceInfo.Scope != "temporary" {
						t.Fatalf("command %s source info = %#v", name, command.SourceInfo)
					}
				}
				if matches != 1 {
					t.Fatalf("command %s matches=%d, want one for the single discovered file; output=%s", name, matches, run.stdout)
				}
			}
		})
	}
}

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
	for _, selected := range [][]string{{"powershell"}, {"bash", "powershell"}, {"read", "edit"}, nil, {}} {
		prompt, opts := systemPromptRebuilder(cwd, agentDir, true, CLIFlags{}, selected)(nil, nil)
		if !slices.Equal(opts.SelectedTools, selected) {
			t.Errorf("startup tools %v: SelectedTools = %v", selected, opts.SelectedTools)
		}
		if len(selected) == 0 && (!strings.Contains(prompt, "<tools>\n(none)\n") || strings.Contains(prompt, "Use bash for file operations")) {
			t.Errorf("empty startup tools acquired defaults on reload:\n%s", prompt)
		}
		for _, name := range selected {
			if !strings.Contains(prompt, "- "+name+":") {
				t.Errorf("startup tools %v: prompt missing %q:\n%s", selected, name, prompt)
			}
		}
	}
}
