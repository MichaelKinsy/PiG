package packagecontent

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Upstream collectFilesFromPaths (core/package-manager.ts) keeps a declared
// file as itself and searches a declared directory, so a "pi.skills" entry
// may name a SKILL.md file, and a skills directory may hold root-level
// Markdown files. loadSkillFromFile (core/skills.ts) names a file skill by its
// frontmatter or its parent directory, and skips, without a diagnostic, a
// Markdown file other than SKILL.md that has no description.
func TestValidateAcceptsSkillFileEntriesAndRootMarkdown(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"skills":["skills/planner/SKILL.md","suite"]}}`)
	writeTestFile(t, filepath.Join(root, "skills", "planner", "SKILL.md"), "---\ndescription: plans\n---\nPlan.\n")
	writeTestFile(t, filepath.Join(root, "suite", "SUITE.md"), "# Suite\n\nNot a skill.\n")
	writeTestFile(t, filepath.Join(root, "suite", "README.md"), "# Readme\n")
	writeTestFile(t, filepath.Join(root, "suite", "listed.md"), "---\nname: listed\ndescription: a file skill\n---\nListed.\n")
	writeTestFile(t, filepath.Join(root, "suite", "nested", "SKILL.md"), "---\nname: nested\ndescription: nested\n---\nNested.\n")

	for name, validate := range map[string]func() (Resources, error){
		"Validate":        func() (Resources, error) { return Validate(root) },
		"ValidatePackage": func() (Resources, error) { return ValidatePackage(root) },
		"ValidateConfigured": func() (Resources, error) {
			return ValidateConfigured(root, map[Kind][]string{Skills: {"skills/planner/SKILL.md", "suite/listed.md", "suite/nested/SKILL.md"}})
		},
		"ValidateConfiguredForStartup": func() (Resources, error) {
			resources, _, _, err := ValidateConfiguredForStartup(root, nil)
			return resources, err
		},
	} {
		resources, err := validate()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertContains(t, resources.SkillDirs, filepath.Join(root, "skills", "planner", "SKILL.md"))
		assertContains(t, resources.SkillDirs, filepath.Join(root, "suite", "listed.md"))
		assertContains(t, resources.SkillDirs, filepath.Join(root, "suite", "nested"))
	}

	resources, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	for want, name := range map[string]string{
		filepath.Join(root, "skills", "planner", "SKILL.md"): "planner",
		filepath.Join(root, "suite", "listed.md"):            "listed",
		filepath.Join(root, "suite", "nested"):               "nested",
	} {
		got, err := FindMember(resources, Skills, name)
		if err != nil || got != want {
			t.Fatalf("FindMember(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if got := SkillFile(filepath.Join(root, "suite", "nested")); got != filepath.Join(root, "suite", "nested", "SKILL.md") {
		t.Fatalf("SkillFile(dir) = %q", got)
	}
	if got := SkillFile(filepath.Join(root, "suite", "listed.md")); got != filepath.Join(root, "suite", "listed.md") {
		t.Fatalf("SkillFile(file) = %q", got)
	}
}

// A filter names a file skill by its own path, as upstream applyPatterns
// matches the resolved file.
func TestConfiguredFilterMatchesSkillFileEntries(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"skills":["skills/planner/SKILL.md","extra.md"]}}`)
	writeTestFile(t, filepath.Join(root, "skills", "planner", "SKILL.md"), "---\ndescription: plans\n---\n")
	writeTestFile(t, filepath.Join(root, "extra.md"), "---\nname: extra\ndescription: extra\n---\n")

	resources, err := ValidateConfigured(root, map[Kind][]string{Skills: {"extra.md"}})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.SkillDirs, filepath.Join(root, "extra.md"))
	assertNotContains(t, resources.SkillDirs, filepath.Join(root, "skills", "planner", "SKILL.md"))

	_, missing, err := InspectConfigured(root, nil)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %v, %v", missing, err)
	}
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"skills":["gone.md","gone-dir"]}}`)
	_, missing, err = InspectConfigured(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	patterns := map[string]bool{}
	for _, member := range missing {
		patterns[member.Pattern] = true
	}
	if !patterns["gone.md"] || !patterns["gone-dir/SKILL.md"] || len(missing) != 2 {
		t.Fatalf("missing patterns = %v", missing)
	}
}

// Upstream collectFiles skips dot entries inside a searched directory and
// applies .gitignore, .ignore and .fdignore; a declared dot directory itself
// is searched, and a glob entry matches no dot path (expandPackageGlob).
func TestPromptAndThemeDirectoriesFollowUpstreamCollection(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"prompts":["./prompts","docs/*.md"],"themes":["./.brand/themes"]}}`)
	writeTestFile(t, filepath.Join(root, "prompts", "visible.md"), "visible\n")
	writeTestFile(t, filepath.Join(root, "prompts", ".hidden.md"), "hidden\n")
	writeTestFile(t, filepath.Join(root, "prompts", ".gitignore"), "ignored.md\n")
	writeTestFile(t, filepath.Join(root, "prompts", "ignored.md"), "ignored\n")
	writeTestFile(t, filepath.Join(root, "prompts", "nested", "deep.md"), "deep\n")
	writeTestFile(t, filepath.Join(root, "docs", "doc.md"), "doc\n")
	writeTestFile(t, filepath.Join(root, "docs", ".dot.md"), "dot\n")
	writeTestFile(t, filepath.Join(root, ".brand", "themes", "brand.json"), `{}`)
	writeTestFile(t, filepath.Join(root, ".brand", "themes", ".draft.json"), `{}`)

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "visible.md"))
	assertContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "nested", "deep.md"))
	assertContains(t, resources.PromptFiles, filepath.Join(root, "docs", "doc.md"))
	assertNotContains(t, resources.PromptFiles, filepath.Join(root, "prompts", ".hidden.md"))
	assertNotContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "ignored.md"))
	assertNotContains(t, resources.PromptFiles, filepath.Join(root, "docs", ".dot.md"))
	assertContains(t, resources.ThemeFiles, filepath.Join(root, ".brand", "themes", "brand.json"))
	assertNotContains(t, resources.ThemeFiles, filepath.Join(root, ".brand", "themes", ".draft.json"))
}

// Pi 0.87.1 never reads .claude-plugin/plugin.json: a package with a "pi"
// manifest loads what that manifest declares. PiG's vendor-overlay reading is
// additive and so is skipped for such a package, whatever the overlay holds.
func TestPiManifestIgnoresVendorPluginOverlays(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["./ext/index.js"]}}`)
	writeTestFile(t, filepath.Join(root, "ext", "index.js"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{
  "name": "pkg",
  "author": {"name": "someone"},
  "mcpServers": {"pkg": {"command": "node", "args": ["${CLAUDE_PLUGIN_ROOT}/start.mjs"]}},
  "hooks": "./hooks/hooks.json",
  "skills": "./skills/"
}`)
	writeTestFile(t, filepath.Join(root, "hooks", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	writeTestFile(t, filepath.Join(root, "skills", "claude-only", "SKILL.md"), "---\ndescription: claude\n---\n")

	for name, validate := range map[string]func() (Resources, error){
		"Discover":        func() (Resources, error) { return Discover(root) },
		"Validate":        func() (Resources, error) { return Validate(root) },
		"ValidatePackage": func() (Resources, error) { return ValidatePackage(root) },
		"ValidateConfiguredForStartup": func() (Resources, error) {
			resources, _, _, err := ValidateConfiguredForStartup(root, nil)
			return resources, err
		},
	} {
		resources, err := validate()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertContains(t, resources.ExtensionEntries, filepath.Join(root, "ext", "index.js"))
		if len(resources.SkillDirs)+len(resources.HookFiles)+len(resources.MCPFiles)+len(resources.AgentFiles) != 0 {
			t.Fatalf("%s loaded overlay resources: %+v", name, resources)
		}
	}
}

// Plugin metadata never fills a Pi kind the "pi" manifest leaves out:
// upstream loads nothing for such a kind.
func TestPiManifestKindsIgnorePluginEntries(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"prompts":["prompts"]}}`)
	writeTestFile(t, filepath.Join(root, "plugin.json"), `{"name":"pkg","skills":["missing-skills"]}`)
	writeTestFile(t, filepath.Join(root, ".pig-plugin", "plugin.json"), `{"themes":["missing-themes"]}`)
	writeTestFile(t, filepath.Join(root, "prompts", "p.md"), "p\n")
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\ndescription: s\n---\n")
	writeTestFile(t, filepath.Join(root, "themes", "t.json"), `{}`)

	resources, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "p.md"))
	if len(resources.SkillDirs) != 0 || len(resources.ThemeFiles) != 0 {
		t.Fatalf("plugin metadata filled undeclared Pi kinds: skills %v themes %v", resources.SkillDirs, resources.ThemeFiles)
	}
	_, missing, err := InspectConfigured(root, nil)
	if err != nil || len(missing) != 0 {
		t.Fatalf("ignored plugin members reported missing: %v, %v", missing, err)
	}
}

// Without a "pi" manifest PiG still reads a Claude plugin manifest. Claude's
// inline mcpServers and hooks objects are configuration, not path lists, and
// declare no PiG members; other fields keep loading.
func TestClaudePluginInlineConfigurationDoesNotFailLoad(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg"}`)
	writeTestFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{
  "name": "pkg",
  "mcpServers": {"pkg": {"command": "node"}},
  "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "true"}]}]},
  "skills": "./skills/",
  "commands": ["./commands/a.md", {"unexpected": true}]
}`)
	writeTestFile(t, filepath.Join(root, "skills", "kept", "SKILL.md"), "---\ndescription: kept\n---\n")

	resources, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.SkillDirs, filepath.Join(root, "skills", "kept"))
	if len(resources.MCPFiles) != 0 || len(resources.HookFiles) != 0 {
		t.Fatalf("inline configuration became members: mcp %v hooks %v", resources.MCPFiles, resources.HookFiles)
	}
}

// Upstream collectAutoExtensionEntries follows symlinks and applies the
// extensions directory's own ignore files.
func TestExtensionDirectoryFollowsSymlinksAndIgnoreFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["./extensions"]}}`)
	writeTestFile(t, filepath.Join(root, "shared", "linked.ts"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(root, "extensions", "kept.ts"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(root, "extensions", "skipped.ts"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(root, "extensions", ".gitignore"), "skipped.ts\n")
	testenv.Symlink(t, filepath.Join(root, "shared", "linked.ts"), filepath.Join(root, "extensions", "linked.ts"))

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.ExtensionEntries, filepath.Join(root, "extensions", "kept.ts"))
	assertContains(t, resources.ExtensionEntries, filepath.Join(root, "extensions", "linked.ts"))
	assertNotContains(t, resources.ExtensionEntries, filepath.Join(root, "extensions", "skipped.ts"))
}
