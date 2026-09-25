package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestParseNpmSpec(t *testing.T) {
	cases := []struct {
		spec        string
		wantName    string
		wantVersion string
	}{
		{"foo", "foo", ""},
		{"foo@1.2.3", "foo", "1.2.3"},
		{"foo@latest", "foo", "latest"},
		{"@scope/foo", "@scope/foo", ""},
		{"@scope/foo@2.0.0", "@scope/foo", "2.0.0"},
	}
	for _, tc := range cases {
		gotName, gotVersion := parseNpmSpec(tc.spec)
		if gotName != tc.wantName || gotVersion != tc.wantVersion {
			t.Errorf("parseNpmSpec(%q) = (%q, %q), want (%q, %q)", tc.spec, gotName, gotVersion, tc.wantName, tc.wantVersion)
		}
	}
}

func TestIsPinnedNpm(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		{"npm:foo", false},
		{"npm:foo@1.2.3", true},
		{"npm:foo@latest", true},
		{"npm:@scope/foo", false},
		{"npm:@scope/foo@beta", true},
	}
	for _, tc := range cases {
		if got := isPinnedNpm(tc.source); got != tc.want {
			t.Errorf("isPinnedNpm(%q) = %v, want %v", tc.source, got, tc.want)
		}
	}
}

func TestEnsureConfiguredPackagesInstalledTreatsProjectDeltaAsInherited(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
	for _, dir := range []string{cwd, agentDir, packageRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pkg"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	globalSource, err := filepath.Rel(agentDir, packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(globalSource)}}); err != nil {
		t.Fatal(err)
	}
	projectSource, err := filepath.Rel(filepath.Join(cwd, ".pig"), packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(projectSource), Prompts: []string{"-prompts/one.md"}}}); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	t.Setenv("PIG_OFFLINE", "1")
	reinstalled, missing := EnsureConfiguredPackagesInstalled(cwd, settings)
	if len(reinstalled) != 0 || len(missing) != 0 {
		t.Fatalf("reinstalled=%v missing=%v", reinstalled, missing)
	}
}
