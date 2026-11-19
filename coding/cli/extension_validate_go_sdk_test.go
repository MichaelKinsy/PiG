//go:build !pig_strip_extension_sdk_go

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func TestAC58InstallValidationRefreshesSDKAndRejectsStaleSelectSignature(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension builds in short mode")
	}
	for _, tc := range []struct {
		name       string
		assignment string
		isolation  string
		wantValid  bool
	}{
		{"current-packed", "_, _, err := ctx.Select(\"Pick\", []string{\"one\"}); return err", "shared-ok", true},
		{"current-isolated", "_, _, err := ctx.Select(\"Pick\", []string{\"one\"}); return err", "strict", true},
		{"stale-two-result-signature", "_, _ = ctx.Select(\"Pick\", []string{\"one\"}); return nil", "shared-ok", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pigHome := t.TempDir()
			t.Setenv("PIG_HOME", pigHome)
			t.Setenv("PIG_SDK_GO_ROOT", "")
			staged := stagedSDKDir(t, pigHome, "go")
			if err := os.MkdirAll(staged, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, ".pig-sdk-version"), []byte("stale\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, "context.go"), []byte("package sdk\ntype Context struct{}\nfunc (Context) Select(string, []string) (string, bool) { return \"\", false }\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			root := filepath.Join(t.TempDir(), "select-abi")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/select-abi\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			source := `package selectabi
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension {
  ext := sdk.New("select-abi")
  ext.Command("select", "select", func(ctx sdk.Context, _ string) error { ` + tc.assignment + ` })
  return ext
}
`
			if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := captureStdoutStderr(t, func() int {
				return runPackageCommand([]string{"install", root, "--validate-only", "--json"})
			})
			if tc.wantValid && code != 0 {
				t.Fatalf("validation failed: code=%d\nstdout=%s\nstderr=%s", code, stdout, stderr)
			}
			if !tc.wantValid && (code == 0 || !strings.Contains(stdout+stderr, "assignment mismatch")) {
				t.Fatalf("stale signature validation: code=%d\nstdout=%s\nstderr=%s", code, stdout, stderr)
			}
			stagedContext, err := os.ReadFile(filepath.Join(staged, "context.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(stagedContext), "(any, bool, error)") {
				t.Fatalf("validation did not refresh stale staged SDK:\n%s", stagedContext)
			}
		})
	}
}

func TestInstallValidateOnlyValidatesUninstalledGitSourceInScratchDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	fixture := validateOnlySDKFixture(t)
	_, agentDir := validateOnlyFixtureEnv(t)
	source := validateOnlyGitFixture(t, fixture)
	if valid, out := runValidateOnlyJSON(t, source); !valid {
		t.Fatalf("uninstalled git source reported invalid: %s", out)
	}
	for _, name := range []string{"git", "settings.json"} {
		if _, err := os.Stat(filepath.Join(agentDir, name)); !os.IsNotExist(err) {
			t.Fatalf("validate-only left %s in the agent dir (stat err %v)", name, err)
		}
	}
}

func TestInstallValidateOnlyValidatesInstalledGitSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	fixture := validateOnlySDKFixture(t)
	_, agentDir := validateOnlyFixtureEnv(t)
	source := validateOnlyGitFixture(t, fixture)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := packagemanager.InstallManagedGit(cwd, agentDir, sm, source, false); err != nil {
		t.Fatal(err)
	}
	if valid, out := runValidateOnlyJSON(t, source); !valid {
		t.Fatalf("installed git source reported invalid: %s", out)
	}
}

func TestInstallValidateOnlyValidatesUninstalledNPMSourceInScratchDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension build in short mode")
	}
	fixture := validateOnlySDKFixture(t)
	_, agentDir := validateOnlyFixtureEnv(t)
	t.Setenv("VALIDATE_FIXTURE", fixture)
	npm := writeStubScript(t, filepath.Join(t.TempDir(), "npm"), `#!/bin/sh
root=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "--prefix" ]; then root="$argument"; fi
  previous="$argument"
done
mkdir -p "$root/node_modules/sdk-fixture"
cp -R "$VALIDATE_FIXTURE"/. "$root/node_modules/sdk-fixture/"
`)
	settings, err := json.Marshal(map[string]any{"npmCommand": []string{npm}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, settings, 0o644); err != nil {
		t.Fatal(err)
	}
	if valid, out := runValidateOnlyJSON(t, "npm:sdk-fixture@1.0.0"); !valid {
		t.Fatalf("uninstalled npm source reported invalid: %s", out)
	}
	if after, err := os.ReadFile(settingsPath); err != nil || !bytes.Equal(after, settings) {
		t.Fatalf("settings changed: %q, %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "npm")); !os.IsNotExist(err) {
		t.Fatalf("validate-only left an npm root in the agent dir (stat err %v)", err)
	}
}
