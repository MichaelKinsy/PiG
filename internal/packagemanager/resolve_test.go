package packagemanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func resolveFixture(t *testing.T) (cwd, agentDir string, sm *codingagent.SettingsManager, pm *PackageManager) {
	t.Helper()
	cwd, agentDir = t.TempDir(), t.TempDir()
	sm = codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	return cwd, agentDir, sm, NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm})
}

func writeResolveFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func findResolved(resources []codingagent.ResolvedResource, suffix string) *codingagent.ResolvedResource {
	for i := range resources {
		if strings.HasSuffix(filepath.ToSlash(resources[i].Path), suffix) {
			return &resources[i]
		}
	}
	return nil
}

// Ports packages/coding-agent/test/package-manager.test.ts "resolve" (:105-212) and "pattern filtering in top-level arrays" (:1614-1687): resolve() reports each settings entry and auto-discovered resource per type with its enabled state, and keeps a resource a pattern excludes as disabled.
func TestResolveReportsSettingsEntriesAutoDiscoveryAndPatterns(t *testing.T) {
	const skill = "---\nname: %s\ndescription: d\n---\nContent"
	cases := []struct {
		name     string
		setup    func(t *testing.T, cwd, agentDir string, sm *codingagent.SettingsManager)
		resource func(ResolvedPaths) []codingagent.ResolvedResource
		path     string
		enabled  bool
		source   string
		scope    string
	}{
		{"user extension entry (:116-125)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "extensions", "my-extension.ts"), "export default function() {}")
			_ = sm.SetExtensionPaths([]string{"extensions/my-extension.ts"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Extensions }, "extensions/my-extension.ts", true, "local", "user"},
		{"project extension entry resolves against .pig (:182-192)", func(t *testing.T, cwd, _ string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "extensions", "project-ext.ts"), "export default function() {}")
			_ = sm.SetProjectExtensionPaths([]string{"extensions/project-ext.ts"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Extensions }, "extensions/project-ext.ts", true, "local", "project"},
		{"auto-discovered user prompt disabled by override (:194-203)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "prompts", "auto.md"), "Auto prompt")
			_ = sm.SetPromptTemplatePaths([]string{"!prompts/auto.md"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Prompts }, "prompts/auto.md", false, "auto", "user"},
		{"auto-discovered root markdown skill (:171-181)", func(t *testing.T, _, agentDir string, _ *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "skills", "single-file.md"), "---\nname: single-file\ndescription: d\n---\nContent")
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Skills }, "skills/single-file.md", true, "auto", "user"},
		{"skill directory entry is keyed by its SKILL.md (:160-170)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "skills", "test-skill", "SKILL.md"), strings.Replace(skill, "%s", "test-skill", 1))
			_ = sm.SetSkillPaths([]string{"skills"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Skills }, "skills/test-skill/SKILL.md", true, "local", "user"},
		{"extension kept by a ! pattern (:1616-1628)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "extensions", "keep.ts"), "export default function() {}")
			writeResolveFile(t, filepath.Join(agentDir, "extensions", "remove.ts"), "export default function() {}")
			_ = sm.SetExtensionPaths([]string{"extensions", "!**/remove.ts"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Extensions }, "extensions/keep.ts", true, "local", "user"},
		{"extension excluded by a ! pattern stays listed disabled (:1616-1628)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "extensions", "keep.ts"), "export default function() {}")
			writeResolveFile(t, filepath.Join(agentDir, "extensions", "remove.ts"), "export default function() {}")
			_ = sm.SetExtensionPaths([]string{"extensions", "!**/remove.ts"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Extensions }, "extensions/remove.ts", false, "local", "user"},
		{"theme excluded by a glob (:1630-1644)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			for _, name := range []string{"dark", "light", "funky"} {
				writeResolveFile(t, filepath.Join(agentDir, "themes", name+".json"), "{}")
			}
			_ = sm.SetThemePaths([]string{"themes", "!funky.json"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Themes }, "themes/funky.json", false, "local", "user"},
		{"prompt kept beside an excluded one (:1646-1658)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "prompts", "review.md"), "Review code")
			writeResolveFile(t, filepath.Join(agentDir, "prompts", "explain.md"), "Explain code")
			_ = sm.SetPromptTemplatePaths([]string{"prompts", "!explain.md"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Prompts }, "prompts/review.md", true, "local", "user"},
		{"skill directory excluded by a pattern (:1660-1678)", func(t *testing.T, _, agentDir string, sm *codingagent.SettingsManager) {
			writeResolveFile(t, filepath.Join(agentDir, "skills", "good-skill", "SKILL.md"), strings.Replace(skill, "%s", "good-skill", 1))
			writeResolveFile(t, filepath.Join(agentDir, "skills", "bad-skill", "SKILL.md"), strings.Replace(skill, "%s", "bad-skill", 1))
			_ = sm.SetSkillPaths([]string{"skills", "!**/bad-skill"})
		}, func(r ResolvedPaths) []codingagent.ResolvedResource { return r.Skills }, "bad-skill/SKILL.md", false, "local", "user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir, sm, pm := resolveFixture(t)
			tc.setup(t, cwd, agentDir, sm)
			resolved, err := pm.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			got := findResolved(tc.resource(resolved), tc.path)
			if got == nil {
				t.Fatalf("%s is not resolved: %+v", tc.path, tc.resource(resolved))
			}
			if got.Enabled != tc.enabled || got.Metadata.Source != tc.source || got.Metadata.Scope != tc.scope {
				t.Fatalf("%s = enabled %v source %q scope %q, want %v %q %q", tc.path, got.Enabled, got.Metadata.Source, got.Metadata.Scope, tc.enabled, tc.source, tc.scope)
			}
		})
	}
}

// package-manager.ts:105-114: with no Package, extension, prompt or theme source configured the result lists no Package-sourced path.
func TestResolveWithoutSourcesListsNothingPackageSourced(t *testing.T) {
	_, _, _, pm := resolveFixture(t)
	resolved, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Extensions)+len(resolved.Prompts)+len(resolved.Themes) != 0 {
		t.Fatalf("resolved = %+v", resolved)
	}
	for _, skill := range resolved.Skills {
		if skill.Metadata.Source != "auto" || skill.Metadata.Origin != "top-level" {
			t.Fatalf("skill %+v is not auto-discovered top-level", skill)
		}
	}
}

// package-manager.ts:1282-1340 resolvePackageSources: a Package that is not installed asks onMissing; "skip" leaves it out and "error" throws `Missing source: <source>`.
func TestResolveAsksOnMissingForAnUninstalledPackage(t *testing.T) {
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	for _, tc := range []struct {
		action  MissingSourceAction
		wantErr string
	}{{MissingSourceSkip, ""}, {MissingSourceError, "Missing source: npm:not-installed-pkg"}} {
		t.Run(string(tc.action), func(t *testing.T) {
			_, _, sm, pm := resolveFixture(t)
			if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:not-installed-pkg"}}); err != nil {
				t.Fatal(err)
			}
			var asked []string
			_, err := pm.Resolve(func(source string) (MissingSourceAction, error) {
				asked = append(asked, source)
				return tc.action, nil
			})
			if len(asked) != 1 || asked[0] != "npm:not-installed-pkg" {
				t.Fatalf("asked = %v", asked)
			}
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func hasResolved(resources []codingagent.ResolvedResource, path string) bool {
	for _, resource := range resources {
		if resource.Path == path && resource.Enabled {
			return true
		}
	}
	return false
}

// Ports packages/coding-agent/test/package-manager.test.ts "resolveExtensionSources" (:602-698): a local file is an enabled extension, a directory resolves through its pi manifest (a leading ~ stays package-relative), its conventional layout, and a skill directory that holds SKILL.md is not searched further; a skill is keyed by its SKILL.md file.
func TestResolveExtensionSourcesLocalSources(t *testing.T) {
	const skill = "---\nname: %s\ndescription: d\n---\nContent"
	ext := "export default function() {}"
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) (source string)
		check func(t *testing.T, dir string, got ResolvedPaths)
	}{
		{"local file (:603-609)", func(t *testing.T, dir string) string {
			return writeResolveFile(t, filepath.Join(dir, "ext.ts"), ext)
		}, func(t *testing.T, dir string, got ResolvedPaths) {
			if !hasResolved(got.Extensions, filepath.Join(dir, "ext.ts")) {
				t.Fatalf("extensions = %+v", got.Extensions)
			}
		}},
		{"directory with pi manifest (:611-640)", func(t *testing.T, dir string) string {
			pkg := filepath.Join(dir, "my-package")
			writeResolveFile(t, filepath.Join(pkg, "package.json"), `{"name":"my-package","pi":{"extensions":["./src/index.ts"],"skills":["./skills"]}}`)
			writeResolveFile(t, filepath.Join(pkg, "src", "index.ts"), ext)
			writeResolveFile(t, filepath.Join(pkg, "skills", "my-skill", "SKILL.md"), strings.Replace(skill, "%s", "my-skill", 1))
			return pkg
		}, func(t *testing.T, dir string, got ResolvedPaths) {
			pkg := filepath.Join(dir, "my-package")
			if !hasResolved(got.Extensions, filepath.Join(pkg, "src", "index.ts")) || !hasResolved(got.Skills, filepath.Join(pkg, "skills", "my-skill", "SKILL.md")) {
				t.Fatalf("resolved = %+v", got)
			}
		}},
		{"leading tilde manifest entries stay package-relative (:642-675)", func(t *testing.T, dir string) string {
			pkg := filepath.Join(dir, "tilde-manifest-package")
			writeResolveFile(t, filepath.Join(pkg, "~extensions", "main.ts"), ext)
			writeResolveFile(t, filepath.Join(pkg, "~", "extensions", "alt.ts"), ext)
			writeResolveFile(t, filepath.Join(pkg, "~skills", "direct-skill", "SKILL.md"), strings.Replace(skill, "%s", "direct-skill", 1))
			writeResolveFile(t, filepath.Join(pkg, "~", "skills", "slash-skill", "SKILL.md"), strings.Replace(skill, "%s", "slash-skill", 1))
			writeResolveFile(t, filepath.Join(pkg, "package.json"), `{"name":"tilde-manifest-package","pi":{"extensions":["~extensions/main.ts","~/extensions/alt.ts"],"skills":["~skills","~/skills"]}}`)
			return pkg
		}, func(t *testing.T, dir string, got ResolvedPaths) {
			pkg := filepath.Join(dir, "tilde-manifest-package")
			for _, path := range []string{filepath.Join(pkg, "~extensions", "main.ts"), filepath.Join(pkg, "~", "extensions", "alt.ts")} {
				if !hasResolved(got.Extensions, path) {
					t.Fatalf("extension %s missing from %+v", path, got.Extensions)
				}
			}
			for _, path := range []string{filepath.Join(pkg, "~skills", "direct-skill", "SKILL.md"), filepath.Join(pkg, "~", "skills", "slash-skill", "SKILL.md")} {
				if !hasResolved(got.Skills, path) {
					t.Fatalf("skill %s missing from %+v", path, got.Skills)
				}
			}
		}},
		{"auto-discovery layout (:677-686)", func(t *testing.T, dir string) string {
			pkg := filepath.Join(dir, "auto-pkg")
			writeResolveFile(t, filepath.Join(pkg, "extensions", "main.ts"), ext)
			writeResolveFile(t, filepath.Join(pkg, "themes", "dark.json"), "{}")
			return pkg
		}, func(t *testing.T, dir string, got ResolvedPaths) {
			pkg := filepath.Join(dir, "auto-pkg")
			if !hasResolved(got.Extensions, filepath.Join(pkg, "extensions", "main.ts")) || !hasResolved(got.Themes, filepath.Join(pkg, "themes", "dark.json")) {
				t.Fatalf("resolved = %+v", got)
			}
		}},
		{"skill directory with SKILL.md stops the search (:688-697)", func(t *testing.T, dir string) string {
			pkg := filepath.Join(dir, "skill-root-pkg")
			writeResolveFile(t, filepath.Join(pkg, "skills", "root-skill", "SKILL.md"), "---\nname: root-skill\ndescription: Root skill\n---\n")
			writeResolveFile(t, filepath.Join(pkg, "skills", "root-skill", "nested-skill", "SKILL.md"), "---\nname: nested-skill\ndescription: Nested skill\n---\n")
			return pkg
		}, func(t *testing.T, dir string, got ResolvedPaths) {
			pkg := filepath.Join(dir, "skill-root-pkg")
			if !hasResolved(got.Skills, filepath.Join(pkg, "skills", "root-skill", "SKILL.md")) || findResolved(got.Skills, "nested-skill/SKILL.md") != nil {
				t.Fatalf("skills = %+v", got.Skills)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, pm := resolveFixture(t)
			dir := t.TempDir()
			source := tc.setup(t, dir)
			got, err := pm.ResolveExtensionSources([]string{source}, false, false)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, dir, got)
		})
	}
}

// package-manager.ts:996-1006 and :1303-1308: a builtin:<name> source is an enabled built-in extension of the requested scope; offline mode installs no npm or git source, so one that is not installed contributes nothing.
func TestResolveExtensionSourcesBuiltinsAndOfflineRemoteSources(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	_, _, _, pm := resolveFixture(t)
	for _, tc := range []struct {
		local, temporary bool
		scope            string
	}{{false, false, "user"}, {true, false, "project"}, {false, true, "temporary"}, {true, true, "temporary"}} {
		got, err := pm.ResolveExtensionSources([]string{"builtin:mcp", "npm:not-installed-pkg"}, tc.local, tc.temporary)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Extensions) != 1 || got.Extensions[0].Path != "builtin:mcp" || !got.Extensions[0].Enabled || got.Extensions[0].Metadata != (codingagent.PathMetadata{Source: "builtin", Scope: tc.scope, Origin: "top-level"}) {
			t.Fatalf("scope %s: extensions = %+v", tc.scope, got.Extensions)
		}
	}
}

// package-manager.ts:1328-1340 and :2155-2160: a temporary git source clones below the agent's tmp/extensions folder and its extensions resolve with the temporary scope; a user-scope source clones below <agentDir>/git instead (:1357-1363, getGitInstallRoot).
func TestResolveExtensionSourcesInstallsGitSourcesPerScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := t.TempDir()
	gitConfig := filepath.Join(t.TempDir(), "gitconfig")
	writeResolveFile(t, gitConfig, "[url \"file://"+filepath.ToSlash(remote)+"\"]\n insteadOf = https://fixture.invalid/owner/repo\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = remote
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeResolveFile(t, filepath.Join(remote, "extensions", "main.ts"), "export default function() {}")
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "-qm", "init"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = remote
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	source := "https://fixture.invalid/owner/repo"
	for _, tc := range []struct {
		temporary bool
		wantScope string
		under     func(agentDir string) string
	}{
		{true, "temporary", func(agentDir string) string { return filepath.Join(agentDir, "tmp", "extensions") }},
		{false, "user", func(agentDir string) string { return filepath.Join(agentDir, "git") }},
	} {
		t.Run(tc.wantScope, func(t *testing.T) {
			_, agentDir, _, pm := resolveFixture(t)
			got, err := pm.ResolveExtensionSources([]string{source}, false, tc.temporary)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Extensions) != 1 || !got.Extensions[0].Enabled || got.Extensions[0].Metadata.Scope != tc.wantScope || got.Extensions[0].Metadata.Origin != "package" || got.Extensions[0].Metadata.PackageRoot == "" {
				t.Fatalf("extensions = %+v", got.Extensions)
			}
			if path := got.Extensions[0].Path; !strings.HasPrefix(path, tc.under(agentDir)+string(filepath.Separator)) || !strings.HasSuffix(filepath.ToSlash(path), "extensions/main.ts") {
				t.Fatalf("extension path %s is not below %s", path, tc.under(agentDir))
			}
		})
	}
}
