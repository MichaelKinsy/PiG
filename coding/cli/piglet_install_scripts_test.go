package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// scriptsFixture is a real-npm registry that serves a Package, and a dependency of that Package, whose postinstall
// script writes a sentinel file. A sentinel that exists after a Piglet command proves the command ran publisher code.
type scriptsFixture struct {
	sentinels string
	piglet    string
}

func (f scriptsFixture) ran(name string) bool {
	_, err := os.Stat(filepath.Join(f.sentinels, name))
	return err == nil
}

func newScriptsFixture(t *testing.T) scriptsFixture {
	t.Helper()
	return newScriptsFixtureAt(t, t.TempDir())
}

// newScriptsFixtureAt keeps PiG's home, and so npm's install root, below home. A home behind a symbolic link is how
// macOS names every temporary directory (/var is /private/var).
func newScriptsFixtureAt(t *testing.T, home string) scriptsFixture {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Fatalf("npm is required: %v", err)
	}
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	registry := newTarballRegistry(t)
	t.Setenv("npm_config_registry", registry.server.URL+"/")
	t.Setenv("npm_config_cache", t.TempDir())
	t.Setenv("npm_config_userconfig", filepath.Join(t.TempDir(), "npmrc"))
	t.Setenv("npm_config_update_notifier", "false")
	// The test proves what PiG asks npm to do, not what the machine's npm decides. Clear every npm switch that changes
	// lifecycle scripts, then state both policies: npm 12 blocks the scripts of a Package that allowScripts does not
	// cover, and any npm can ignore scripts by configuration.
	for _, name := range []string{"NPM_CONFIG_IGNORE_SCRIPTS", "NPM_CONFIG_ALLOW_SCRIPTS", "NPM_CONFIG_DANGEROUSLY_ALLOW_ALL_SCRIPTS", "NPM_CONFIG_STRICT_ALLOW_SCRIPTS"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("npm_config_ignore_scripts", "false")
	t.Setenv("npm_config_dangerously_allow_all_scripts", "true")
	t.Chdir(t.TempDir())

	sentinels := t.TempDir()
	t.Setenv("PIG_TEST_SENTINEL_DIR", sentinels)
	postinstall := "const fs = require('node:fs');\nfs.writeFileSync(require('node:path').join(process.env.PIG_TEST_SENTINEL_DIR, process.argv[2]), 'ran');\nfs.writeFileSync(require('node:path').join(__dirname, 'built.txt'), 'built');\n"
	author := t.TempDir()
	for name, body := range map[string]string{
		"dep/package.json":               `{"name":"scripts-dep","version":"1.0.0","scripts":{"postinstall":"node postinstall.js dep"}}`,
		"dep/postinstall.js":             postinstall,
		"pkg/package.json":               `{"name":"scripts-pkg","version":"1.0.0","license":"MIT","dependencies":{"scripts-dep":"1.0.0"},"scripts":{"postinstall":"node postinstall.js pkg"},"pi":{"extensions":["extensions/runner"]}}`,
		"pkg/postinstall.js":             postinstall,
		"pkg/extensions/runner/index.js": "export default function extension(pi) {}\n",
		"source/package.json":            `{"name":"scripts-piglet","version":"1.0.0","license":"MIT","scripts":{"postinstall":"node postinstall.js source"},"pig":{"piglet":"piglet.yaml"}}`,
		"source/postinstall.js":          postinstall,
		"source/piglet.yaml":             "name: scripted\npackages:\n  s: npm:scripts-pkg@1.0.0\nextensions:\n  - name: runner\n    origins: [package:s]\n",
		"scripted.yaml":                  "name: scripted\npackages:\n  s: npm:scripts-pkg@1.0.0\nextensions:\n  - name: runner\n    origins: [package:s]\n",
		"source/LICENSE":                 "MIT\n",
		"pkg/LICENSE":                    "MIT\n",
		"dep/LICENSE":                    "MIT\n",
	} {
		path := filepath.Join(author, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"dep", "pkg", "source"} {
		registry.pack(filepath.Join(author, dir))
	}
	return scriptsFixture{sentinels: sentinels, piglet: filepath.Join(author, "scripted.yaml")}
}

func runPigletForTest(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := piglet.RunCommand(append([]string{"piglet"}, args...), &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

// Piglet resolution installs npm Packages with lifecycle scripts disabled, so `pig piglet validate` and `pig piglet add`
// do not run a publisher's postinstall before the user has looked at the Piglet. --allow-install-scripts opts in.
func TestPigletCommandsInstallNPMPackagesWithoutRunningScripts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    func(f scriptsFixture) []string
		wantRan []string
	}{
		{name: "validate", args: func(f scriptsFixture) []string { return []string{"validate", f.piglet} }},
		{name: "validate opted in", wantRan: []string{"pkg", "dep"}, args: func(f scriptsFixture) []string {
			return []string{"validate", f.piglet, "--allow-install-scripts"}
		}},
		{name: "add local source", args: func(f scriptsFixture) []string { return []string{"add", f.piglet, "--no-input"} }},
		{name: "add local source opted in", wantRan: []string{"pkg", "dep"}, args: func(f scriptsFixture) []string {
			return []string{"add", f.piglet, "--no-input", "--allow-install-scripts"}
		}},
		{name: "add npm source", args: func(scriptsFixture) []string { return []string{"add", "npm:scripts-piglet", "--no-input"} }},
		{name: "add npm source opted in", wantRan: []string{"source", "pkg", "dep"}, args: func(scriptsFixture) []string {
			return []string{"add", "npm:scripts-piglet", "--no-input", "--allow-install-scripts"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newScriptsFixture(t)
			if code, out := runPigletForTest(t, tc.args(f)...); code != 0 {
				t.Fatalf("pig piglet %v: code=%d\n%s", tc.args(f), code, out)
			}
			for _, name := range []string{"source", "pkg", "dep"} {
				if want, got := slices.Contains(tc.wantRan, name), f.ran(name); got != want {
					t.Errorf("%s postinstall ran = %v, want %v", name, got, want)
				}
			}
		})
	}
}

// A Git Package installs its dependencies with `npm install` inside the checkout, which runs the root package's own
// lifecycle scripts as well. Piglet resolution disables them for Git Packages too.
func TestPigletValidateInstallsGitPackagesWithoutRunningScripts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	for _, allow := range []bool{false, true} {
		name := "default"
		if allow {
			name = "opted in"
		}
		t.Run(name, func(t *testing.T) {
			f := newScriptsFixture(t)
			repo := t.TempDir()
			for file, body := range map[string]string{
				"package.json":               `{"name":"scripts-git","version":"1.0.0","dependencies":{"scripts-dep":"1.0.0"},"scripts":{"postinstall":"node postinstall.js git"},"pi":{"extensions":["extensions/runner"]}}`,
				"postinstall.js":             "require('node:fs').writeFileSync(require('node:path').join(process.env.PIG_TEST_SENTINEL_DIR, process.argv[2]), 'ran');\n",
				"extensions/runner/index.js": "export default function extension(pi) {}\n",
			} {
				path := filepath.Join(repo, filepath.FromSlash(file))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture"}} {
				if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, output)
				}
			}
			source := "file://localhost/" + strings.TrimPrefix(filepath.ToSlash(repo), "/")
			path := filepath.Join(t.TempDir(), "gitscript.yaml")
			if err := os.WriteFile(path, []byte("name: gitscript\npackages:\n  g: git:"+source+"\nextensions:\n  - name: runner\n    origins: [package:g]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"validate", path}
			if allow {
				args = append(args, "--allow-install-scripts")
			}
			if code, out := runPigletForTest(t, args...); code != 0 {
				t.Fatalf("pig piglet %v: code=%d\n%s", args, code, out)
			}
			for _, sentinel := range []string{"git", "dep"} {
				if got := f.ran(sentinel); got != allow {
					t.Errorf("%s postinstall ran = %v, want %v", sentinel, got, allow)
				}
			}
		})
	}
}

// `pig install npm:` and the other package-manager paths that mirror Pi do not pass --ignore-scripts, as Pi does not
// (package-manager.ts installNpm), so the same Package runs its lifecycle scripts there.
func TestPackageInstallKeepsPisLifecycleScripts(t *testing.T) {
	f := newScriptsFixture(t)
	cwd := t.TempDir()
	agentDir := codingagent.AgentDir()
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := packagemanager.InstallManagedNPM(cwd, sm.AgentDir(), sm, "npm:scripts-pkg@1.0.0", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pkg", "dep"} {
		if !f.ran(name) {
			t.Errorf("%s postinstall did not run for a Pi-parity package install", name)
		}
	}
}

// What an opted-in install built stays in place: resolving the Piglet again with scripts disabled does not reinstall
// the Package without the files its postinstall wrote. npm reinstalls a Package whose --prefix runs through a symbolic
// link, so the install root must be the real path: macOS reaches its temporary directories through one.
func TestPigletResolutionKeepsFilesBuiltByAnOptedInInstall(t *testing.T) {
	t.Run("direct home", func(t *testing.T) { keepsFilesBuiltByAnOptedInInstall(t, t.TempDir()) })
	t.Run("home behind a symbolic link", func(t *testing.T) {
		base := t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "real", "home"), 0o755); err != nil {
			t.Fatal(err)
		}
		testenv.RequireDirectoryLink(t, filepath.Join(base, "real"), filepath.Join(base, "link"))
		keepsFilesBuiltByAnOptedInInstall(t, filepath.Join(base, "link", "home"))
	})
}

func keepsFilesBuiltByAnOptedInInstall(t *testing.T, home string) {
	t.Helper()
	f := newScriptsFixtureAt(t, home)
	if code, out := runPigletForTest(t, "add", f.piglet, "--no-input", "--allow-install-scripts"); code != 0 {
		t.Fatalf("add: code=%d\n%s", code, out)
	}
	built := filepath.Join(codingagent.AgentDir(), "npm", "node_modules", "scripts-pkg", "built.txt")
	if _, err := os.Stat(built); err != nil {
		t.Fatalf("the opted-in install did not run the postinstall: %v", err)
	}
	if code, out := runPigletForTest(t, "validate", "scripted"); code != 0 {
		t.Fatalf("validate: code=%d\n%s", code, out)
	}
	if _, err := os.Stat(built); err != nil {
		t.Fatalf("resolving with scripts disabled removed the postinstall's output: %v", err)
	}
}

// WithIgnoreScripts appends the one flag npm, pnpm and bun all read; without it the arguments are Pi's.
func TestInstallArgsIgnoreScriptsOnlyWhenAsked(t *testing.T) {
	root := "/managed/npm"
	for _, manager := range []string{"npm", "pnpm", "bun"} {
		plain := packagemanager.NpmInstallArgs(manager, "@scope/pkg@1.2.3", root, "")
		got := packagemanager.NpmInstallArgs(manager, "@scope/pkg@1.2.3", root, "", packagemanager.WithIgnoreScripts())
		if want := append(slices.Clone(plain), "--ignore-scripts"); !slices.Equal(got, want) {
			t.Errorf("%s install args = %v, want %v", manager, got, want)
		}
		if slices.Contains(plain, "--ignore-scripts") {
			t.Errorf("%s install args without the option = %v", manager, plain)
		}
	}
	registry := packagemanager.NpmInstallArgs("npm", "pkg", root, "https://npm.example.com/team", packagemanager.WithIgnoreScripts())
	if !slices.Equal(registry[len(registry)-3:], []string{"--registry", "https://npm.example.com/team", "--ignore-scripts"}) {
		t.Errorf("registry install args = %v", registry)
	}
	for _, command := range [][]string{nil, {"pnpm"}, {"bun"}, {"yarn"}} {
		sm := codingagent.NewSettingsManager(t.TempDir(), t.TempDir())
		if command != nil {
			if err := sm.SetNpmCommand(command); err != nil {
				t.Fatal(err)
			}
		}
		plain, err := packagemanager.GetGitDependencyInstallArgs(sm)
		if err != nil {
			t.Fatal(err)
		}
		got, err := packagemanager.GetGitDependencyInstallArgs(sm, packagemanager.WithIgnoreScripts())
		if want := append(slices.Clone(plain), "--ignore-scripts"); err != nil || !slices.Equal(got, want) {
			t.Errorf("%v git dependency args = %v, %v; want %v", command, got, err, want)
		}
	}
}
