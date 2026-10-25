package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodehome"
)

// noHomeDirectory is a host where Node's os.homedir() throws: no HOME, and no entry for the process's user in the system's user database.
func noHomeDirectory(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	if err := os.Unsetenv("HOME"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nodehome.Override(func() (string, error) { return "", &nodehome.Error{} }))
}

// Pi's getHomeDir (package-manager.ts:233-235) is `process.env.HOME || homedir()`: a nonempty HOME, else Node's os.homedir(), which throws when no home
// directory can be found.
func TestPackageManagerHomeDirThrowsWhereNodesHomedirDoes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		home    *string
		lookup  func() (string, error)
		want    string
		wantErr bool
	}{
		{"nonempty HOME wins over everything", new("/h"), func() (string, error) { return "", &nodehome.Error{} }, "/h", false},
		{"HOME absent, the user database's home", nil, func() (string, error) { return "/db", nil }, "/db", false},
		{"HOME empty, os.homedir's answer", new(""), func() (string, error) { return "", nil }, "", false},
		{"HOME absent, no home directory", nil, func() (string, error) { return "", &nodehome.Error{} }, "", true},
		{"HOME empty, no home directory", new(""), func() (string, error) { return "", &nodehome.Error{} }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", "")
			if tc.home == nil {
				if err := os.Unsetenv("HOME"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("HOME", *tc.home)
			}
			t.Cleanup(nodehome.Override(tc.lookup))
			got, err := PackageManagerHomeDir()
			var thrown *nodehome.Error
			if tc.wantErr != errors.As(err, &thrown) {
				t.Fatalf("PackageManagerHomeDir() = %q, %v; want an error: %v", got, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("PackageManagerHomeDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

//go:fix inline
func ptr(s string) *string { return new(s) }

// package-manager.ts:2464 computes the user's ~/.agents/skills before addAutoDiscoveredResources checks any directory, so resolve() rejects with getHomeDir's error
// whatever the settings and the directories hold.
func TestValidateAutoDiscoveredOverridesRejectsWhereNoHomeDirectoryExists(t *testing.T) {
	noHomeDirectory(t)
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, trusted := range []bool{true, false} {
		err := ValidateAutoDiscoveredOverrides(cwd, agentDir, NewSettingsManagerWithProjectTrust(cwd, agentDir, trusted), trusted)
		if _, ok := errors.AsType[*nodehome.Error](err); !ok {
			t.Fatalf("trusted=%v: error = %v, want *nodehome.Error", trusted, err)
		}
	}
}

// With no home directory, a path relative to the working directory must not stand in for ~/.agents/skills: the project's own .agents/skills would then be
// listed as the user's, with the user's scope and no project trust asked. The collectors list no user .agents skills, as an unreadable directory has none, and
// keep every ancestor directory as the project's, since none of them is the user's.
func TestNoHomeDirectoryLeavesTheProjectsAgentsSkillsTheProjectsOnly(t *testing.T) {
	noHomeDirectory(t)
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	skill := filepath.Join(cwd, ".agents", "skills", "planted", "SKILL.md")
	writeOverrideFixture(t, skill, "---\nname: planted\ndescription: planted by the project\n---\nBody\n")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd) // the relative ".agents/skills" a missing home used to become is this directory's
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	for _, resource := range AmbientSkillResources(cwd, agentDir, sm, false, true) {
		t.Errorf("the user scope lists %+v with no home directory", resource)
	}
	if paths := AmbientSkillPaths(cwd, agentDir, sm, false, true); len(paths) != 0 {
		t.Errorf("the user scope's skill paths = %q with no home directory, want none", paths)
	}
	trusted := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	var projectDirs []string
	for _, dir := range NewAgentsSkillScan(cwd).Dirs() {
		projectDirs = append(projectDirs, dir.Dir)
	}
	if want := filepath.Join(cwd, ".agents", "skills"); !slices.Contains(projectDirs, want) {
		t.Errorf("the ancestor scan lists %q, want the project's own %s", projectDirs, want)
	}
	for _, resource := range AmbientSkillResources(cwd, agentDir, trusted, true, true) {
		if resource.Metadata.Scope == "user" && strings.Contains(resource.Path, "planted") {
			t.Errorf("the planted skill is listed as a user skill: %+v", resource)
		}
	}
}
