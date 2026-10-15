package packagemanager

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func resolveFixtureWithHome(t *testing.T) (cwd, agentDir string, sm *codingagent.SettingsManager, pm *PackageManager) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	return resolveFixture(t)
}

func localPackage(t *testing.T) string {
	t.Helper()
	pkg := filepath.Join(t.TempDir(), "local-pkg")
	writeResolveFile(t, filepath.Join(pkg, "package.json"), `{"name":"local-pkg","pi":{"extensions":["./ext/main.ts"],"skills":["./skills"],"prompts":["./prompts"],"themes":["./themes"]}}`)
	writeResolveFile(t, filepath.Join(pkg, "ext", "main.ts"), "export default function() {}")
	writeResolveFile(t, filepath.Join(pkg, "skills", "pkg-skill", "SKILL.md"), "---\nname: pkg-skill\ndescription: d\n---\nContent")
	writeResolveFile(t, filepath.Join(pkg, "prompts", "pkg-prompt.md"), "Prompt")
	writeResolveFile(t, filepath.Join(pkg, "themes", "pkg-theme.json"), "{}")
	return pkg
}

// package-manager.ts resolve: a configured Package lists its extensions, skills (keyed by SKILL.md), prompts and themes under the right type, each with the Package's metadata, and a package filter disables what it does not list.
// Pi: packages/coding-agent/src/core/package-manager.ts resolve (:920-985).
func TestResolveListsPackageResourcesPerTypeWithPackageMetadata(t *testing.T) {
	_, _, sm, pm := resolveFixtureWithHome(t)
	pkg := localPackage(t)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
		t.Fatal(err)
	}
	resolved, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		got    []codingagent.ResolvedResource
		suffix string
	}{
		"extension": {resolved.Extensions, "ext/main.ts"},
		"skill":     {resolved.Skills, "skills/pkg-skill/SKILL.md"},
		"prompt":    {resolved.Prompts, "prompts/pkg-prompt.md"},
		"theme":     {resolved.Themes, "themes/pkg-theme.json"},
	} {
		r := findResolved(tc.got, tc.suffix)
		if r == nil {
			t.Fatalf("%s %s is not listed: %+v", name, tc.suffix, tc.got)
		}
		if !r.Enabled || r.Metadata.Origin != "package" || r.Metadata.Scope != "user" || r.Metadata.Source != pkg || r.Metadata.BaseDir != pkg || r.Metadata.PackageRoot != pkg {
			t.Errorf("%s metadata = %+v enabled %v, want an enabled user Package resource of %s", name, r.Metadata, r.Enabled, pkg)
		}
	}

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkg, WasObject: true, Extensions: []string{"!ext/main.ts"}, Prompts: []string{}}}); err != nil {
		t.Fatal(err)
	}
	filtered, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := findResolved(filtered.Extensions, "ext/main.ts"); r == nil || r.Enabled {
		t.Errorf("an excluded Package extension stays listed disabled: %+v", r)
	}
	if r := findResolved(filtered.Prompts, "prompts/pkg-prompt.md"); r == nil || r.Enabled {
		t.Errorf("a Package filter with an empty list disables that type: %+v", r)
	}
	if r := findResolved(filtered.Themes, "themes/pkg-theme.json"); r == nil || !r.Enabled {
		t.Errorf("a type the filter does not name stays enabled: %+v", r)
	}
}

// package-manager.ts resolve: a malformed local Package source or settings entry throws before anything is collected; a Project's auto-discovered and settings resources need project trust; ambient resources come before the built-in extensions; ~/.agents/skills is listed.
// Pi: packages/coding-agent/src/core/package-manager.ts resolve (:912-985).
func TestResolveValidatesEntriesAppliesProjectTrustAndOrdersBuiltinsLast(t *testing.T) {
	cwd, agentDir, sm, _ := resolveFixtureWithHome(t)
	pm := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: sm, BuiltinExtensions: []string{"mcp"}})

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "file:///%ZZ"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Resolve(nil); err == nil || !strings.Contains(err.Error(), "resolve Packages") {
		t.Fatalf("a malformed Package source error = %v, want a resolve Packages error", err)
	}
	if err := sm.SetPackages(nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetExtensionPaths([]string{"file:///%ZZ"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Resolve(nil); err == nil || !strings.Contains(err.Error(), "resolve settings entries") {
		t.Fatalf("a malformed settings entry error = %v, want a resolve settings entries error", err)
	}
	if err := sm.SetExtensionPaths(nil); err != nil {
		t.Fatal(err)
	}

	writeResolveFile(t, filepath.Join(agentDir, "extensions", "user.ts"), "export default function() {}")
	writeResolveFile(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "extensions", "project.ts"), "export default function() {}")
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeResolveFile(t, filepath.Join(home, ".agents", "skills", "home-skill", "SKILL.md"), "---\nname: home-skill\ndescription: d\n---\nContent")

	resolved, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range resolved.Extensions {
		paths = append(paths, filepath.Base(r.Path))
	}
	for _, r := range resolved.Extensions {
		if r.Path == "builtin:mcp" && r.Metadata != (codingagent.PathMetadata{Source: "builtin", Scope: "user", Origin: "top-level"}) {
			t.Errorf("builtin metadata = %+v", r.Metadata)
		}
	}
	if got := strings.Join(paths, ","); !strings.HasSuffix(got, ",builtin:mcp") || !strings.Contains(got, "user.ts") || !strings.Contains(got, "project.ts") {
		t.Errorf("extensions = %s, want the ambient ones first and builtin:mcp last", got)
	}
	if findResolved(resolved.Skills, "home-skill/SKILL.md") == nil {
		t.Errorf("skills = %+v, want ~/.agents/skills listed", resolved.Skills)
	}

	sm.SetProjectTrusted(false)
	untrusted, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if findResolved(untrusted.Extensions, "project.ts") != nil || findResolved(untrusted.Extensions, "user.ts") == nil {
		t.Errorf("untrusted extensions = %+v, want the user's and none of the Project's", untrusted.Extensions)
	}
}

// package-manager.ts resolvePackageSources: only npm and git Packages that need installing ask onMissing (a local Package never does), a callback error stops the resolution, and offline mode asks nothing.
// Pi: packages/coding-agent/src/core/package-manager.ts resolvePackageSources (:1282-1340).
func TestResolveAsksOnMissingOnlyForRemotePackagesAndStopsOnItsError(t *testing.T) {
	_, agentDir, sm, pm := resolveFixtureWithHome(t)
	local := localPackage(t)
	writeResolveFile(t, filepath.Join(agentDir, "npm", "node_modules", "installed-one", "package.json"), `{"name":"installed-one","version":"1.0.0"}`)
	missingLocal := filepath.Join(t.TempDir(), "no-such-package")
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: local}, {Source: missingLocal}, {Source: "npm:installed-one"}, {Source: "npm:absent-one"}}); err != nil {
		t.Fatal(err)
	}
	var asked []string
	if _, err := pm.Resolve(func(source string) (MissingSourceAction, error) {
		asked = append(asked, source)
		return MissingSourceSkip, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "npm:absent-one" {
		t.Fatalf("asked = %v, want only the npm Package", asked)
	}

	boom := errors.New("callback failed")
	if _, err := pm.Resolve(func(string) (MissingSourceAction, error) { return MissingSourceSkip, boom }); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the callback's error", err)
	}
	if _, err := pm.Resolve(func(string) (MissingSourceAction, error) { return MissingSourceError, nil }); err == nil || err.Error() != "Missing source: npm:absent-one" {
		t.Fatalf("error = %v, want Missing source: npm:absent-one", err)
	}

	t.Setenv("PI_OFFLINE", "1")
	asked = nil
	if _, err := pm.Resolve(func(source string) (MissingSourceAction, error) {
		asked = append(asked, source)
		return MissingSourceError, nil
	}); err != nil || len(asked) != 0 {
		t.Fatalf("offline: asked %v, error %v, want no question and no error", asked, err)
	}
}

// package-manager.ts resolveExtensionSources: a local source that does not exist contributes nothing, a malformed one fails, a prompt and a theme source resolve into their own types, and skills are keyed by SKILL.md.
// Pi: packages/coding-agent/src/core/package-manager.ts resolveExtensionSources (:991-1010).
func TestResolveExtensionSourcesSkipsMissingLocalSourcesAndSortsTypes(t *testing.T) {
	_, _, _, pm := resolveFixtureWithHome(t)
	got, err := pm.ResolveExtensionSources([]string{filepath.Join(t.TempDir(), "does-not-exist")}, false, false)
	if err != nil || len(got.Extensions)+len(got.Skills)+len(got.Prompts)+len(got.Themes) != 0 {
		t.Fatalf("missing local source = %+v, %v, want nothing", got, err)
	}
	if _, err := pm.ResolveExtensionSources([]string{"file:///%ZZ"}, false, false); err == nil {
		t.Fatal("a malformed source succeeded")
	}
	pkg := localPackage(t)
	got, err = pm.ResolveExtensionSources([]string{pkg}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		got    []codingagent.ResolvedResource
		suffix string
	}{"extension": {got.Extensions, "ext/main.ts"}, "skill": {got.Skills, "skills/pkg-skill/SKILL.md"}, "prompt": {got.Prompts, "prompts/pkg-prompt.md"}, "theme": {got.Themes, "themes/pkg-theme.json"}} {
		r := findResolved(tc.got, tc.suffix)
		if r == nil || !r.Enabled || r.Metadata.Scope != "project" || r.Metadata.PackageRoot != pkg {
			t.Errorf("%s = %+v in %+v, want an enabled project-scope resource of the Package", name, r, tc.got)
		}
	}
}

// package-manager.ts resolvePackageSources installs a missing git Package into the scope that configured it: a user Package below <agentDir>/git, a Project Package below the project's config directory.
// Pi: packages/coding-agent/src/core/package-manager.ts resolvePackageSources (:1282-1340), getGitInstallRoot (:1357-1363).
func TestResolveInstallsAMissingGitPackageIntoItsConfiguredScope(t *testing.T) {
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
	writeResolveFile(t, filepath.Join(remote, "extensions", "main.ts"), "export default function() {}")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "-qm", "init"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = remote
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	source := "https://fixture.invalid/owner/repo"
	for _, project := range []bool{false, true} {
		cwd, agentDir, sm, pm := resolveFixtureWithHome(t)
		var err error
		if project {
			err = sm.SetProjectPackages([]codingagent.PackageSource{{Source: source}})
		} else {
			err = sm.SetPackages([]codingagent.PackageSource{{Source: source}})
		}
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := pm.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(agentDir, "git")
		scope := "user"
		if project {
			want, scope = filepath.Join(codingagent.ProjectConfigDir(cwd), "git"), "project"
		}
		r := findResolved(resolved.Extensions, "extensions/main.ts")
		if r == nil || !strings.HasPrefix(r.Path, want+string(filepath.Separator)) || r.Metadata.Scope != scope {
			t.Errorf("project=%v: extension = %+v, want one below %s with scope %s", project, r, want, scope)
		}
	}
}
