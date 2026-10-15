package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func installedVersionFixture(t *testing.T, source, manifest string, local bool) (string, *codingagent.SettingsManager, string) {
	t.Helper()
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PIG_NPM_TEST_FAIL", "0")
	settings := codingagent.NewSettingsManager(cwd, agent)
	root := filepath.Join(agent, "npm")
	setPackages := settings.SetPackages
	if local {
		root = filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "npm")
		setPackages = settings.SetProjectPackages
	}
	if err := setPackages([]codingagent.PackageSource{{Source: source}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "node_modules", "example")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		writeNpmTrustFile(t, filepath.Join(path, "package.json"), []byte(manifest))
	}
	log := filepath.Join(cwd, "commands")
	t.Setenv("PIG_PM_COMMANDS", log)
	bin := t.TempDir()
	writeStubScript(t, filepath.Join(bin, "npm"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PIG_PM_COMMANDS\"\nexit \"$PIG_NPM_TEST_FAIL\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cwd, settings, log
}

func TestEnsureConfiguredNpmVersionMatchesUpstream(t *testing.T) {
	// packages/coding-agent/test/package-manager.test.ts:2546: an installed 1.0.0 must not satisfy the configured 2.0.0 pin; resolve invokes install once. The command spy is adapted from gate-close-00's exact port.
	// packages/coding-agent/src/core/package-manager.ts:1286-1293,1473-1478 also checks ranges and missing manifests locally, without npm view.
	for _, local := range []bool{true, false} {
		for _, tc := range []struct {
			name, source, manifest string
			install                bool
		}{
			{"pinned-mismatch", "npm:example@2.0.0", `{"name":"example","version":"1.0.0"}`, true},
			{"pinned-match", "npm:example@1.0.0", `{"name":"example","version":"1.0.0"}`, false},
			{"range-mismatch", "npm:example@^2.0.0", `{"version":"1.0.0"}`, true},
			{"range-match", "npm:example@^1.0.0", `{"version":"1.2.0"}`, false},
			{"tag", "npm:example@latest", `{"version":"1.0.0"}`, false},
			{"unpinned", "npm:example", `{"version":"1.0.0"}`, false},
			{"missing-manifest", "npm:example", "", true},
			{"missing-version", "npm:example", `{"name":"example"}`, true},
			{"invalid-manifest", "npm:example", "{", true},
			{"bom-manifest", "npm:example@1.0.0", "\ufeff" + `{"version":"1.0.0"}`, false},
			{"invalid-version", "npm:example@1.0.0", `{"version":"broken"}`, true},
			{"prerelease-excluded", "npm:example@>=1.0.0", `{"version":"2.0.0-beta.1"}`, true},
			{"prerelease-included", "npm:example@^2.0.0-beta.1", `{"version":"2.0.0-beta.2"}`, false},
		} {
			scope := "user/"
			if local {
				scope = "project/"
			}
			t.Run(scope+tc.name, func(t *testing.T) {
				cwd, settings, log := installedVersionFixture(t, tc.source, tc.manifest, local)
				reinstalled, missing := packagemanager.EnsureConfiguredPackagesInstalled(cwd, settings.AgentDir(), settings)
				var want []string
				if tc.install {
					want = []string{tc.source}
				}
				if !slices.Equal(reinstalled, want) || len(missing) != 0 {
					t.Fatalf("reinstalled=%v missing=%v, want installed=%v", reinstalled, missing, want)
				}
				data, err := os.ReadFile(log)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				var installs []string
				for line := range strings.SplitSeq(string(data), "\n") {
					if strings.HasPrefix(line, "view ") {
						t.Fatalf("resolve queried registry metadata: %s", data)
					}
					if strings.HasPrefix(line, "install ") {
						installs = append(installs, line)
					}
				}
				if len(installs) != len(want) {
					t.Fatalf("install calls=%v, want one per unsatisfied source %v", installs, want)
				}
			})
		}
	}
}

func BenchmarkInstalledNpmVersionMatches(b *testing.B) {
	path := b.TempDir()
	if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"name":"example","version":"1.2.3"}`), 0o644); err != nil {
		b.Fatal(err)
	}
	source, err := packagemanager.ParseNpmInstallRef("npm:example@^1.0.0")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if !packagemanager.InstalledNpmMatchesConfiguredVersion(source, path) {
			b.Fatal("installed version should match")
		}
	}
}

func TestEnsureConfiguredNpmMismatchOfflineAndFailure(t *testing.T) {
	for _, offline := range []bool{true, false} {
		t.Run(map[bool]string{true: "offline", false: "install-failure"}[offline], func(t *testing.T) {
			const source = "npm:example@2.0.0"
			cwd, settings, log := installedVersionFixture(t, source, `{"version":"1.0.0"}`, true)
			if offline {
				t.Setenv("PI_OFFLINE", "1")
			} else {
				t.Setenv("PIG_NPM_TEST_FAIL", "1")
			}
			reinstalled, missing := packagemanager.EnsureConfiguredPackagesInstalled(cwd, settings.AgentDir(), settings)
			if len(reinstalled) != 0 || !slices.Equal(missing, []string{source}) {
				t.Fatalf("reinstalled=%v missing=%v", reinstalled, missing)
			}
			if offline {
				if _, err := os.Stat(log); !os.IsNotExist(err) {
					t.Fatalf("offline resolution ran a command: %v", err)
				}
			}
		})
	}
}
