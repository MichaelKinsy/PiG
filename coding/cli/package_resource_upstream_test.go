package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

type packageResourceFixture struct {
	cwd, agent string
	settings   *codingagent.SettingsManager
}

func newPackageResourceFixture(t *testing.T) packageResourceFixture {
	t.Helper()
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("HOME", cwd)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	return packageResourceFixture{cwd, agent, codingagent.NewSettingsManager(cwd, agent)}
}
func (f packageResourceFixture) items(t *testing.T) []codingagent.ResourceSourceInfo {
	t.Helper()
	infos := resourceSourceInfoProvider(f.cwd, f.agent, f.settings, Args{})()
	items := make([]codingagent.ResourceSourceInfo, 0, len(infos))
	for _, info := range infos {
		items = append(items, info)
	}
	return items
}
func writePackageResource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
func requirePackageResource(t *testing.T, items []codingagent.ResourceSourceInfo, path string, kind tui.ResourceType, enabled bool) codingagent.ResourceSourceInfo {
	t.Helper()
	for _, item := range items {
		if item.Path == path && item.ResourceType == string(kind) && item.Enabled == enabled {
			return item
		}
	}
	t.Fatalf("missing %s resource path=%s enabled=%t: %+v", kind, path, enabled, items)
	return codingagent.ResourceSourceInfo{}
}

func TestPackageSkillMetadataOriginalCases(t *testing.T) {
	for _, tc := range []struct{ name, base, scope, skill, description string }{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:301
		{"should use the agent dir as baseDir for user .pi/agent skills", "agent", "user", "user-pi", "user pi"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:314
		{"should use the project .pi dir as baseDir for project .pi skills", "project", "project", "project-pi", "project pi"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:328
		{"should use ~/.agents as baseDir for user .agents skills", "agents", "user", "user-agents", "user agents"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageResourceFixture(t)
			base := f.agent
			if tc.base == "project" {
				base = filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME)
			}
			if tc.base == "agents" {
				base = filepath.Join(f.cwd, ".agents")
			}
			path := filepath.Join(base, "skills", tc.skill, "SKILL.md")
			writePackageResource(t, path, "---\nname: "+tc.skill+"\ndescription: "+tc.description+"\n---\n")
			item := requirePackageResource(t, f.items(t), path, tui.ResourceSkills, true)
			if item.Source != "auto" || item.Scope != tc.scope || item.BaseDir != base {
				t.Fatalf("metadata=%+v, want auto/%s/%s", item, tc.scope, base)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:353
	t.Run("should use each project .agents dir as baseDir for project .agents skills", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		repo := filepath.Join(f.cwd, "repo")
		f.cwd = filepath.Join(repo, "packages", "feature")
		if err := os.MkdirAll(f.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		bases := []string{filepath.Join(repo, ".agents"), filepath.Join(repo, "packages", ".agents")}
		for i, name := range []string{"repo", "package"} {
			writePackageResource(t, filepath.Join(bases[i], "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: "+name+"\n---\n")
		}
		items := f.items(t)
		for i, name := range []string{"repo", "package"} {
			item := requirePackageResource(t, items, filepath.Join(bases[i], "skills", name, "SKILL.md"), tui.ResourceSkills, true)
			if item.Source != "auto" || item.Scope != "project" || item.BaseDir != bases[i] {
				t.Fatalf("metadata=%+v, want project base %s", item, bases[i])
			}
		}
	})
}

func TestCanonicalResourceWinnerUsesProjectAutoBeforeUserExplicit(t *testing.T) {
	f := newPackageResourceFixture(t)
	// The project extensions directory is an alias of a shared directory, so the auto-discovered project path and the user's explicit path canonicalize to one file without a file link.
	sharedDir := filepath.Join(f.cwd, "shared-extensions")
	shared := filepath.Join(sharedDir, "shared.ts")
	writePackageResource(t, shared, "export default function() {}")
	projectExtensions := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "extensions")
	if err := os.MkdirAll(filepath.Dir(projectExtensions), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, sharedDir, projectExtensions)
	project := filepath.Join(projectExtensions, "shared.ts")
	if err := f.settings.SetExtensionPaths([]string{shared}); err != nil {
		t.Fatal(err)
	}
	items := f.items(t)
	if len(items) != 1 || items[0].Path != project || items[0].Source != "auto" || items[0].Scope != "project" {
		t.Fatalf("winning metadata=%+v", items)
	}
	configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, Args{}, nil)
	if len(configs) != 1 || configs[0].Source != project {
		t.Fatalf("winning runtime config=%+v", configs)
	}
	rel, err := filepath.Rel(filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME), items[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]string{items[0].Scope, items[0].Source, filepath.ToSlash(rel)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("RESOURCE_PRECEDENCE %s\n", raw)
}

func TestPackageResolveOriginalCases(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:104
	t.Run("should return no package-sourced paths when no sources configured", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		for _, item := range f.items(t) {
			if item.ResourceType != string(tui.ResourceSkills) || item.Source != "auto" || item.Origin != "top-level" {
				t.Fatalf("unexpected resource=%+v", item)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:114
	t.Run("should resolve local extension paths from settings", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.agent, "extensions", "my-extension.ts")
		writePackageResource(t, path, "export default function() {}")
		if err := f.settings.SetExtensionPaths([]string{"extensions/my-extension.ts"}); err != nil {
			t.Fatal(err)
		}
		requirePackageResource(t, f.items(t), path, tui.ResourceExtensions, true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:125
	t.Run("should resolve skill paths from settings", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.agent, "skills", "my-skill", "SKILL.md")
		writePackageResource(t, path, "---\nname: test-skill\ndescription: A test skill\n---\nContent")
		if err := f.settings.SetSkillPaths([]string{"skills"}); err != nil {
			t.Fatal(err)
		}
		entry := requirePackageResource(t, f.items(t), path, tui.ResourceSkills, true)
		rel, err := filepath.Rel(f.agent, entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("RESOURCE_SKILL_FILE %s\n", filepath.ToSlash(rel))
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:145
	t.Run("should auto-discover root markdown skills from .pi skill dirs", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.agent, "skills", "single-file.md")
		writePackageResource(t, path, "---\nname: single-file\ndescription: A root markdown skill\n---\nContent")
		requirePackageResource(t, f.items(t), path, tui.ResourceSkills, true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:161
	t.Run("should resolve project paths relative to .pi", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "extensions", "project-ext.ts")
		writePackageResource(t, path, "export default function() {}")
		if err := f.settings.SetProjectExtensionPaths([]string{"extensions/project-ext.ts"}); err != nil {
			t.Fatal(err)
		}
		requirePackageResource(t, f.items(t), path, tui.ResourceExtensions, true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:173
	t.Run("should auto-discover user prompts with overrides", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.agent, "prompts", "auto.md")
		writePackageResource(t, path, "Auto prompt")
		if err := f.settings.SetPromptTemplatePaths([]string{"!prompts/auto.md"}); err != nil {
			t.Fatal(err)
		}
		requirePackageResource(t, f.items(t), path, tui.ResourcePrompts, false)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:253
	t.Run("should auto-discover project prompts with overrides", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "prompts", "is.md")
		writePackageResource(t, path, "Is prompt")
		if err := f.settings.SetProjectPromptTemplatePaths([]string{"!prompts/is.md"}); err != nil {
			t.Fatal(err)
		}
		requirePackageResource(t, f.items(t), path, tui.ResourcePrompts, false)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:265
	t.Run("should resolve directory with package.json pi.extensions in extensions setting", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "my-extensions-pkg")
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"my-extensions-pkg","pi":{"extensions":["./extensions/clip.ts","./extensions/cost.ts"]}}`)
		for _, name := range []string{"clip.ts", "cost.ts"} {
			writePackageResource(t, filepath.Join(pkg, "extensions", name), "export default function() {}")
		}
		writePackageResource(t, filepath.Join(pkg, "extensions", "helper.ts"), "export const x = 1;")
		if err := f.settings.SetExtensionPaths([]string{pkg}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		for _, name := range []string{"clip.ts", "cost.ts"} {
			requirePackageResource(t, items, filepath.Join(pkg, "extensions", name), tui.ResourceExtensions, true)
		}
		for _, item := range items {
			if filepath.Base(item.Path) == "helper.ts" {
				t.Fatalf("undeclared helper=%+v", item)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:185
	t.Run("should resolve symlinked user and project resources once", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		shared := filepath.Join(f.cwd, "shared-resources")
		writePackageResource(t, filepath.Join(shared, "extensions", "shared.ts"), "export default function() {}")
		writePackageResource(t, filepath.Join(shared, "skills", "shared-skill", "SKILL.md"), "---\nname: shared-skill\ndescription: Shared skill\n---\nContent")
		writePackageResource(t, filepath.Join(shared, "prompts", "shared.md"), "Shared prompt")
		writePackageResource(t, filepath.Join(shared, "themes", "shared.json"), `{"name":"shared-theme"}`)
		project := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME)
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		// Upstream's "dir" symlinks (package-manager.test.ts:215-222) need privilege on Windows; a junction there is the same symbolic link to Node.
		for _, kind := range []string{"extensions", "skills", "prompts", "themes"} {
			testenv.RequireDirectoryLink(t, filepath.Join(shared, kind), filepath.Join(f.agent, kind))
			testenv.RequireDirectoryLink(t, filepath.Join(shared, kind), filepath.Join(project, kind))
		}
		items := f.items(t)
		configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, Args{}, nil)
		if len(configs) != 1 {
			t.Errorf("runtime extension aliases=%+v", configs)
		} else if info := configs[0].SourceInfo; info.Scope != "project" {
			t.Errorf("runtime source info=%+v", configs[0].SourceInfo)
		}
		for _, paths := range [][]string{collectPromptPaths(f.cwd, f.agent, f.settings, Args{}, true), collectThemePaths(f.cwd, f.agent, f.settings, Args{}, true), collectSkillInputs(f.cwd, f.agent, f.settings, Args{}, nil)} {
			if len(paths) != 1 {
				t.Errorf("runtime resource aliases=%q", paths)
			}
		}
		counts := map[string]int{}
		for _, item := range items {
			counts[item.ResourceType]++
			if item.Scope != "project" {
				t.Errorf("surviving resource scope=%s: %+v", item.Scope, item)
			}
		}
		var rows [][]any
		for _, item := range items {
			rel, err := filepath.Rel(project, item.Path)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, []any{item.ResourceType, filepath.ToSlash(rel), item.Scope, item.Source, item.Origin, item.Enabled})
		}
		slices.SortFunc(rows, func(a, b []any) int { return cmp.Compare(a[0].(string), b[0].(string)) })
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("RESOURCE_SYMLINK %s\n", raw)
		for _, kind := range []tui.ResourceType{tui.ResourceExtensions, tui.ResourceSkills, tui.ResourcePrompts, tui.ResourceThemes} {
			if counts[string(kind)] != 1 {
				t.Errorf("%s count=%d, want the single shared resource", kind, counts[string(kind)])
			}
		}
	})
}
