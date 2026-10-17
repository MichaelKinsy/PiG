package packagemanager

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func trustedSettings(t *testing.T) (cwd string, sm *codingagent.SettingsManager) {
	t.Helper()
	cwd = t.TempDir()
	sm = codingagent.NewSettingsManager(cwd, t.TempDir())
	return cwd, sm
}

// package-manager.ts:881-899 getInstalledPath: a local source resolves against the base directory of the scope (user: agent dir, project: <cwd>/.pig) and is undefined unless the path exists; addSourceToSettings (:826-851) stores an added path relative to that base and listConfiguredPackages (:977-1000) reports the same path per scope.
func TestPackageManagerReportsInstalledPathsPerScope(t *testing.T) {
	cwd, sm := trustedSettings(t)
	userPkg := filepath.Join(cwd, "pkgs", "user-pkg")
	projectPkg := filepath.Join(cwd, "pkgs", "project-pkg")
	for _, dir := range []string{userPkg, projectPkg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm})
	userRel, _ := filepath.Rel(sm.AgentDir(), userPkg)
	projectRel, _ := filepath.Rel(filepath.Join(cwd, ".pig"), projectPkg)
	if got := m.GetInstalledPath(userRel, "user"); got != userPkg {
		t.Errorf("user scope = %q, want %q", got, userPkg)
	}
	if got := m.GetInstalledPath(projectRel, "project"); got != projectPkg {
		t.Errorf("project scope = %q, want %q", got, projectPkg)
	}
	if got := m.GetInstalledPath(userRel, "project"); got != "" {
		t.Errorf("a path missing in the project scope = %q, want none", got)
	}
	if _, err := m.AddSourceToSettings("./pkgs/user-pkg", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddSourceToSettings("./pkgs/project-pkg", true); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, pkg := range m.ListConfiguredPackages() {
		seen = append(seen, pkg.Scope+"="+pkg.InstalledPath)
	}
	if want := []string{"user=" + userPkg, "project=" + projectPkg}; !slices.Equal(seen, want) {
		t.Errorf("ListConfiguredPackages = %v, want %v (user packages first)", seen, want)
	}
}

// package-manager.ts:setProgressCallback: the callback set on the manager receives the events of every later install, before the result returns.
// Pi: packages/coding-agent/src/core/package-manager.ts:116 (PackageManager.install); packages/coding-agent/src/core/package-manager.ts:128 (PackageManager.setProgressCallback).
func TestPackageManagerReportsProgressToTheInstalledCallback(t *testing.T) {
	cwd, sm := trustedSettings(t)
	pkg := filepath.Join(cwd, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm})
	var events []ProgressEvent
	m.SetProgressCallback(func(event ProgressEvent) { events = append(events, event) })
	if err := m.Install("./pkg", false); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Action != "install" || events[0].Source != "./pkg" {
		t.Fatalf("progress events = %+v, want an install event for ./pkg", events)
	}
	events = nil
	m.SetProgressCallback(nil)
	if err := m.Install("./pkg", false); err != nil || len(events) != 0 {
		t.Fatalf("after clearing the callback: err = %v, events = %+v", err, events)
	}
}

// package-manager.ts:installAndPersist / removeAndPersist: installing records the source, removal reports whether an entry was dropped and keeps an unmatched source untouched.
func TestPackageManagerPersistsInstallAndRemoval(t *testing.T) {
	cwd, sm := trustedSettings(t)
	if err := os.MkdirAll(filepath.Join(cwd, "pkg", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "pkg", "skills", "demo", "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm})
	if err := m.InstallAndPersist("./pkg", false); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetGlobalSettings().Packages; len(got) != 1 {
		t.Fatalf("packages after install = %+v", got)
	}
	if removed, err := m.RemoveAndPersist("./other", false); err != nil || removed {
		t.Fatalf("removing an unmatched source = %v, %v; want false, nil", removed, err)
	}
	if removed, err := m.RemoveAndPersist("./pkg", false); err != nil || !removed {
		t.Fatalf("removing the installed source = %v, %v; want true, nil", removed, err)
	}
	if got := sm.GetGlobalSettings().Packages; len(got) != 0 {
		t.Fatalf("packages after removal = %+v", got)
	}
}
