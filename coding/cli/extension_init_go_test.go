//go:build !pig_strip_extension_sdk_go

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests scaffold a Go extension and build or validate it, so the Go extension SDK has to be in the build.
func TestRunExtensionInitScaffoldsLoginExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	dir := filepath.Join(t.TempDir(), "science-pig")

	if code := runExtensionInit([]string{dir, "--login", "--json"}); code != 0 {
		t.Fatalf("login init exit code = %d, want 0", code)
	}
	source, err := os.ReadFile(filepath.Join(dir, "extension.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ctx.SetLogin(loginDefinition())", "Custom Extension", "Extension-provided login", "Replace these grids and text"} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("login scaffold missing %q:\n%s", want, source)
		}
	}
	reports, err := validateExtensionRuntimes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !reports[0].Valid {
		t.Fatalf("login scaffold validation reports = %#v", reports)
	}
	assertScaffoldHasNoYAML(t, dir)
}

func TestRunExtensionInitScaffoldsAndStagesSDK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "my-planner")
	code := runExtensionInit([]string{dir, "--json"})
	if code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}

	for _, rel := range []string{"go.mod", "extension.go"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("missing scaffolded %s: %v", rel, err)
		}
	}

	stagedSDK := stagedSDKDir(t, home, "go")
	if _, err := os.Stat(filepath.Join(stagedSDK, "go.mod")); err != nil {
		t.Fatalf("init did not stage SDK at %s: %v", stagedSDK, err)
	}

	goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), "require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0") {
		t.Fatalf("go.mod missing SDK requirement:\n%s", goMod)
	}
	if strings.Contains(string(goMod), "replace github.com/MichaelKinsy/PiG/extensions/sdk") || strings.Contains(string(goMod), filepath.ToSlash(stagedSDK)) {
		t.Fatalf("go.mod committed a machine-specific SDK path:\n%s", goMod)
	}

	main, err := os.ReadFile(filepath.Join(dir, "extension.go"))
	if err != nil {
		t.Fatalf("read extension.go: %v", err)
	}
	if !strings.Contains(string(main), `sdk.New("my-planner")`) {
		t.Fatalf("extension.go did not use the derived name:\n%s", main)
	}
	assertScaffoldHasNoYAML(t, dir)
}

func TestRunExtensionInitRefusesOverwriteWithoutForce(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runExtensionInit([]string{dir, "--name", "x", "--json"}); code == 0 {
		t.Fatal("init overwrote an existing go.mod without --force")
	}
	if code := runExtensionInit([]string{dir, "--name", "x", "--force", "--json"}); code != 0 {
		t.Fatalf("init --force failed: code %d", code)
	}
}
