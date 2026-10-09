package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Pi's package manager retains its caller's SettingsManager (package-manager.ts:817,1747-1782).
// A user-scoped package may use a trusted project's command, but never an untrusted one's.
func TestPackageNpmCommandUsesResolvedTrust(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		for _, operation := range []string{"install", "remove", "update", "missing", "lookup", "git"} {
			t.Run(fmt.Sprintf("%s/trusted=%t", operation, trusted), func(t *testing.T) {
				cwd, agentDir, record := npmTrustFixture(t)
				sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, trusted)
				var err error
				switch operation {
				case "install":
					err = packageManagerFor(cwd, sm, nil).InstallAndPersist("npm:fake-package", false)
				case "remove":
					_, err = packageManagerFor(cwd, sm, nil).RemoveAndPersist("npm:fake-package", false)
				case "update":
					err = packageManagerFor(cwd, sm, nil).Update("")
				case "missing":
					if removeErr := os.RemoveAll(filepath.Join(agentDir, "npm", "node_modules")); removeErr != nil {
						t.Fatal(removeErr)
					}
					_, missing := packagemanager.EnsureConfiguredPackagesInstalled(cwd, sm.AgentDir(), sm)
					if len(missing) != 0 {
						t.Fatalf("missing packages: %v", missing)
					}
				case "git":
					// Clone only a local repository. Both Pi's installGit and updateGit use the same settings for dependency installation (package-manager.ts:1856-1857,1925-1927).
					repo := filepath.Join(cwd, "repo")
					writeNpmTrustFile(t, filepath.Join(repo, "package.json"), []byte(`{"name":"git-package"}`))
					if _, gitErr := packagemanager.RunCmdInDir(repo, "git", "init", "-q"); gitErr != nil {
						t.Fatal(gitErr)
					}
					if _, gitErr := packagemanager.RunCmdInDir(repo, "git", "add", "package.json"); gitErr != nil {
						t.Fatal(gitErr)
					}
					if _, gitErr := packagemanager.RunCmdInDir(repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"); gitErr != nil {
						t.Fatal(gitErr)
					}
					source := "git:file://localhost/" + strings.TrimPrefix(filepath.ToSlash(repo), "/")
					err = packageManagerFor(cwd, sm, nil).InstallAndPersist(source, false)
					if err == nil {
						err = packageManagerFor(cwd, sm, nil).Update(source)
					}
				case "lookup":
					if removeErr := os.RemoveAll(filepath.Join(agentDir, "npm", "node_modules")); removeErr != nil {
						t.Fatal(removeErr)
					}
					packageManagerFor(cwd, sm, nil).ListConfiguredPackages()
				}
				if err != nil {
					t.Fatal(err)
				}
				want := "global"
				if trusted {
					want = "project"
				}
				assertNpmTrustRecord(t, record, want)
			})
		}
	}
}

// package-manager-cli.ts:750-756 uses saved trust only for updates, not defaultProjectTrust.
func TestPackageUpdateNpmCommandTrust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		saved *bool
		flags []string
		want  string
	}{
		{"undecided", nil, nil, "global"},
		{"denied", new(false), nil, "global"},
		{"saved", new(true), nil, "project"},
		{"approve", new(false), []string{"--approve"}, "project"},
		{"no-approve", new(true), []string{"--no-approve"}, "global"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir, record := npmTrustFixture(t)
			t.Chdir(cwd)
			if tc.saved != nil {
				if err := codingagent.NewProjectTrustStore(agentDir).Set(cwd, tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			_, stderr, code := captureStdoutStderr(t, func() int {
				return runPackageCommand(append([]string{"update", "--extensions"}, tc.flags...))
			})
			if code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
			assertNpmTrustRecord(t, record, tc.want)
		})
	}
}

// Pi: packages/coding-agent/src/core/settings-manager.ts:1121 (SettingsManager.getNpmCommand).
func TestPackageNpmCommandRetainsOverrides(t *testing.T) {
	cwd, agentDir, record := npmTrustFixture(t)
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	command := sm.GetNpmCommand()
	command[3] = "override"
	sm.ApplyOverrides(codingagent.Settings{NpmCommand: command})
	if err := packageManagerFor(cwd, sm, nil).InstallAndPersist("npm:fake-package", false); err != nil {
		t.Fatal(err)
	}
	assertNpmTrustRecord(t, record, "override")
}

func TestPackageRemoveRetainsCommandOverridesAndSettingsOnFailure(t *testing.T) {
	cwd, agentDir, record := npmTrustFixture(t)
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	command := sm.GetNpmCommand()
	command[3] = "override"
	sm.ApplyOverrides(codingagent.Settings{NpmCommand: command})
	t.Setenv("NPM_TRUST_FAIL", "1")
	if _, err := packageManagerFor(cwd, sm, nil).RemoveAndPersist("npm:fake-package", false); err == nil {
		t.Fatal("failed uninstall reported success")
	}
	assertNpmTrustRecord(t, record, "override")
	if pkgs := sm.GetGlobalSettings().Packages; len(pkgs) != 1 || pkgs[0].Source != "npm:fake-package" {
		t.Fatalf("failed uninstall changed settings: %v", pkgs)
	}
}

func TestPackageNpmCommandFailureIsReturned(t *testing.T) {
	for _, operation := range []string{"install", "remove", "update"} {
		t.Run(operation, func(t *testing.T) {
			cwd, agentDir, record := npmTrustFixture(t)
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
			t.Setenv("NPM_TRUST_FAIL", "1")
			var err error
			switch operation {
			case "install":
				err = packageManagerFor(cwd, sm, nil).InstallAndPersist("npm:fake-package", false)
			case "remove":
				_, err = packageManagerFor(cwd, sm, nil).RemoveAndPersist("npm:fake-package", false)
			case "update":
				err = packageManagerFor(cwd, sm, nil).Update("")
			}
			// Pi package-manager.ts:2679 returns the command and numeric exit code after the child finishes.
			if err == nil || !strings.HasSuffix(err.Error(), "failed with code 7") {
				t.Fatalf("installer failure = %v", err)
			}
			assertNpmTrustRecord(t, record, "global")
		})
	}
}

func TestPackageRegistrySourcesRequireProjectTrust(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%t", trusted), func(t *testing.T) {
			cwd, agentDir, record := npmTrustFixture(t)
			projectPath := filepath.Join(cwd, ".pig", "settings.json")
			data, err := os.ReadFile(projectPath)
			if err != nil {
				t.Fatal(err)
			}
			var project map[string]any
			if err := json.Unmarshal(data, &project); err != nil {
				t.Fatal(err)
			}
			project["packages"] = []string{"npm:project-only?registry=https%3A%2F%2Fproject.invalid%2Fregistry"}
			data, err = json.Marshal(project)
			if err != nil {
				t.Fatal(err)
			}
			writeNpmTrustFile(t, projectPath, data)
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, trusted)
			if err := packageManagerFor(cwd, sm, nil).Update(""); err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(calls), "https://project.invalid/registry"); got != trusted {
				t.Fatalf("project registry used=%t, trusted=%t: %s", got, trusted, calls)
			}
		})
	}
}

func TestPackageContextNpmCommandRespectsDeniedTrust(t *testing.T) {
	cwd, agentDir, record := npmTrustFixture(t)
	t.Chdir(cwd)
	if err := codingagent.NewProjectTrustStore(agentDir).Set(cwd, new(false)); err != nil {
		t.Fatal(err)
	}
	_, _, sm, err := packageContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(agentDir, "npm", "node_modules")); err != nil {
		t.Fatal(err)
	}
	packageManagerFor(cwd, sm, nil).ListConfiguredPackages()
	assertNpmTrustRecord(t, record, "global")
}

func TestPackageInstallRemoveNpmCommandTrust(t *testing.T) {
	for _, operation := range []string{"install", "remove"} {
		for _, tc := range []struct {
			name  string
			saved *bool
			flags []string
			want  string
		}{
			{"default-always", nil, nil, "project"},
			{"denied-overrides-default", new(false), nil, "global"},
			{"no-approve-overrides-saved", new(true), []string{"--no-approve"}, "global"},
			{"approve-overrides-denied", new(false), []string{"--approve"}, "project"},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				cwd, agentDir, record := npmTrustFixture(t)
				t.Chdir(cwd)
				if tc.saved != nil {
					if err := codingagent.NewProjectTrustStore(agentDir).Set(cwd, tc.saved); err != nil {
						t.Fatal(err)
					}
				}
				_, stderr, code := captureStdoutStderr(t, func() int {
					return runPackageCommand(append([]string{operation, "npm:fake-package"}, tc.flags...))
				})
				if code != 0 {
					t.Fatalf("exit=%d stderr=%s", code, stderr)
				}
				assertNpmTrustRecord(t, record, tc.want)
			})
		}
	}
}

func TestPackageLocalMutationRequiresTrustBeforeCommand(t *testing.T) {
	for _, operation := range []string{"install", "remove"} {
		t.Run(operation, func(t *testing.T) {
			cwd, _, record := npmTrustFixture(t)
			t.Chdir(cwd)
			_, stderr, code := captureStdoutStderr(t, func() int {
				return runPackageCommand([]string{operation, "npm:fake-package", "--local", "--no-approve"})
			})
			if code != 1 || !strings.Contains(stderr, "Project is not trusted. Use --approve to modify local package config.") {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("untrusted local operation ran a command: %v", err)
			}
		})
	}
}

// .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:1780-1791,1821-1838 treats absent and empty argv as npm with --omit=dev --legacy-peer-deps for Git dependencies.
func TestPackageNpmCommandDefaultsRespectTrust(t *testing.T) {
	for _, argv := range [][]string{nil, {}} {
		cwd, agentDir, _ := npmTrustFixture(t)
		data, err := json.Marshal(map[string]any{"npmCommand": argv})
		if err != nil {
			t.Fatal(err)
		}
		writeNpmTrustFile(t, filepath.Join(agentDir, "settings.json"), data)
		sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
		if got := packagemanager.DefaultNpmCommand(sm); !slices.Equal(got, []string{"npm"}) {
			t.Fatalf("default command = %v", got)
		}
		if got, err := packagemanager.GetGitDependencyInstallArgs(sm); err != nil || !slices.Equal(got, []string{"install", "--omit=dev", "--legacy-peer-deps"}) {
			t.Fatalf("default dependency args = %v, %v", got, err)
		}
	}
}

func BenchmarkPackageCommandTrustResolution(b *testing.B) {
	cwd, agentDir := b.TempDir(), b.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pig", "settings.json"), []byte(`{"npmCommand":["untrusted"]}`), 0o600); err != nil {
		b.Fatal(err)
	}
	opts := &packageCLIOptions{command: packageUpdate}
	b.ReportAllocs()
	for b.Loop() {
		sm, err := createPackageCommandSettings(context.Background(), cwd, agentDir, opts)
		if err != nil {
			b.Fatal(err)
		}
		if command := packagemanager.DefaultNpmCommand(sm); !slices.Equal(command, []string{"npm"}) {
			b.Fatal(command)
		}
	}
}

func npmTrustFixture(t *testing.T) (cwd, agentDir, record string) {
	t.Helper()
	root := shortTempDir(t)
	cwd, agentDir, record = filepath.Join(root, "project"), filepath.Join(root, "agent"), filepath.Join(root, "calls.jsonl")
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("NPM_TRUST_FAIL", "")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "npm.cjs")
	writeNpmTrustFile(t, script, []byte(`const fs = require('node:fs');
const [record, label, ...args] = process.argv.slice(2);
fs.appendFileSync(record, JSON.stringify({label, args}) + '\n');
if (process.env.NPM_TRUST_FAIL === '1') process.exit(7);
const prefix = args.indexOf('--prefix');
if (args.includes('install') && prefix >= 0) fs.mkdirSync(require('node:path').join(args[prefix+1], 'node_modules', 'fake-package'), {recursive:true});
if (args.includes('list')) process.stdout.write('[]');
`))
	for _, scope := range []string{"global", "project"} {
		settings := map[string]any{"npmCommand": []string{node, script, record, scope, "--", "pnpm"}}
		path := filepath.Join(cwd, ".pig", "settings.json")
		if scope == "global" {
			settings["packages"] = []string{"npm:fake-package"}
			settings["defaultProjectTrust"] = "always"
			path = filepath.Join(agentDir, "settings.json")
		}
		data, marshalErr := json.Marshal(settings)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		writeNpmTrustFile(t, path, data)
	}
	writeNpmTrustFile(t, filepath.Join(agentDir, "npm", "node_modules", "fake-package", "package.json"), []byte(`{"name":"fake-package"}`))
	return cwd, agentDir, record
}

func writeNpmTrustFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNpmTrustRecord(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var call struct {
			Label string   `json:"label"`
			Args  []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		if call.Label != want || !slices.Contains(call.Args, "pnpm") {
			t.Errorf("npm call = %s, want command from %s settings", line, want)
		}
	}
}
