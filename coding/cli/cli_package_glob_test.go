package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// cliGlobThrowMessage is the SyntaxError Node's globSync throws for "prompts/[[:alpha:]]*,md": minimatch escapes the ",", and the u flag rejects the escape. Node globs case-insensitively on Windows and macOS.
func cliGlobThrowMessage() string {
	flags := "u"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		flags = "iu"
	}
	return `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}][^/]*?\,md$/` + flags + `: Invalid escape`
}

// writeCLIGlobPackage writes a Package named eprobe at root with the given package.json fields, an extension, and members its globs can reach.
func writeCLIGlobPackage(t *testing.T, root, fields string) {
	t.Helper()
	writeResourceLoaderFixture(t, filepath.Join(root, "package.json"), `{"name":"eprobe","version":"1.0.0",`+fields+`}`)
	writeResourceLoaderFixture(t, filepath.Join(root, "extensions", "a.js"), "export default function() {}\n")
	writeResourceLoaderFixture(t, filepath.Join(root, "prompts", "a,md"), "x\n")
	writeResourceLoaderFixture(t, filepath.Join(root, "hooks", "a,json"), "{}\n")
}

// cliGlobPackageRoot is where an offline -e source's Package is installed: npm:eprobe in the temporary npm root, a git source in its temporary checkout.
func cliGlobPackageRoot(t *testing.T, agentDir, source string) string {
	t.Helper()
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind == sourceref.KindGit {
		root, err := packagemanager.TemporaryGitCheckoutPath(agentDir, ref)
		if err != nil {
			t.Fatal(err)
		}
		return root
	}
	tmp, err := packagemanager.TemporaryPackagePath(agentDir, "npm", "")
	if err != nil {
		t.Fatal(err)
	}
	return packagemanager.NpmPackagePath(tmp, ref)
}

const (
	cliThrowingPrompts = `"pi":{"extensions":["extensions/a.js"],"prompts":["prompts/[[:alpha:]]*,md"]}`
	cliGitSource       = "git:https://example.com/owner/eprobe"
)

// Pi resolves the -e sources after the settings, in the reload and in the pre-trust extension load, outside any catch (resource-loader.ts:525-528, 676-680). An npm or git source's Package is collected without a catch (package-manager.ts:1315-1342), so a manifest glob that globSync throws for stops startup; a local source's throw is caught (:1372-1389) and keeps the kinds collected before it, and a glob in PiG's own "pig" block, which Pi never reads, throws nothing. Probed with Pi 1.1.0 (r24): the npm source exits 1 with this SyntaxError.
func TestCLIPackageSourceGlobThrowFollowsPi(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	for _, tc := range []struct {
		name, source, fields string
		fails                bool
	}{
		{"npm source", "npm:eprobe", cliThrowingPrompts, true},
		{"git source", cliGitSource, cliThrowingPrompts, true},
		{"local source", "", cliThrowingPrompts, false},
		{"npm source, glob in the pig block", "npm:eprobe", `"pi":{"extensions":["extensions/a.js"]},"pig":{"hooks":["hooks/[[:alpha:]]*,json"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			source := tc.source
			if source == "" {
				source = filepath.Join(t.TempDir(), "eprobe")
				writeCLIGlobPackage(t, source, tc.fields)
			} else {
				writeCLIGlobPackage(t, cliGlobPackageRoot(t, agentDir, source), tc.fields)
			}
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
			configs := collectExtensionConfigs(cwd, agentDir, sm, Args{Extensions: []string{source}, NoExtensions: true}, nil)
			err := cliPackageSourceError(configs)
			if tc.fails {
				if err == nil || err.Error() != cliGlobThrowMessage() {
					t.Fatalf("error = %v, want %q", err, cliGlobThrowMessage())
				}
				return
			}
			if err != nil || len(configs) != 1 || configs[0].ResolveError() != nil || filepath.Base(configs[0].Source) != "a.js" {
				t.Fatalf("configs = %+v, error %v; want the Package's extension a.js", configs, err)
			}
		})
	}
}

// Pi's resolveExtensionSources resolves the -e sources in order and rejects at the first throw (package-manager.ts:1293-1342), so of two throwing sources the earlier one's error is reported.
func TestCLIPackageSourceGlobThrowFirstSourceWins(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	cwd, agentDir := t.TempDir(), t.TempDir()
	writeCLIGlobPackage(t, cliGlobPackageRoot(t, agentDir, "npm:eprobe"), cliThrowingPrompts)
	writeCLIGlobPackage(t, cliGlobPackageRoot(t, agentDir, cliGitSource), `"pi":{"prompts":["prompts/[[:alpha:]]*,txt"]}`)
	gitMessage := strings.Replace(cliGlobThrowMessage(), `\,md$`, `\,txt$`, 1)
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	for _, tc := range []struct {
		sources []string
		want    string
	}{
		{[]string{"npm:eprobe", cliGitSource}, cliGlobThrowMessage()},
		{[]string{cliGitSource, "npm:eprobe"}, gitMessage},
	} {
		err := cliPackageSourceError(collectExtensionConfigs(cwd, agentDir, sm, Args{Extensions: tc.sources, NoExtensions: true}, nil))
		if err == nil || err.Error() != tc.want {
			t.Errorf("sources %q: error = %v, want %q", tc.sources, err, tc.want)
		}
	}
}

// The snapshot provider is /reload's resourceLoader.reload() (interactive-mode.ts:6222-6255): the settings errors come first, then the -e sources' throw, which becomes `Reload failed: <message>`.
func TestReloadSnapshotReportsCLIPackageGlobThrow(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	for _, tc := range []struct {
		name, settings, want string
	}{
		{"-e source throw", `{}`, cliGlobThrowMessage()},
		{"settings first", `{"prompts":["file:///a%2Fb"]}`, invalidFileURLMessage()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			writeCLIGlobPackage(t, cliGlobPackageRoot(t, agentDir, "npm:eprobe"), cliThrowingPrompts)
			writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), tc.settings)
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, Args{Extensions: []string{"npm:eprobe"}}, nil)()
			if snapshot.Err == nil || snapshot.Err.Error() != tc.want {
				t.Fatalf("snapshot error = %v, want %q", snapshot.Err, tc.want)
			}
		})
	}
}

// main.ts:736-787 and resource-loader.ts:525-528, 676-680: startup stops with the -e source's SyntaxError after the settings errors and before any extension factory runs. Without --approve, a Project with settings goes through the pre-trust load, where Pi throws before the user extension's factory; with it, the final load throws. Each stage needs its own check.
func TestStartupStopsAtACLIPackageGlobThrow(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name, source, fields, settings string
		approve                        bool
		want                           string
	}{
		{"final load, npm source", "npm:eprobe", cliThrowingPrompts, `{}`, true, cliGlobThrowMessage()},
		{"final load, git source", cliGitSource, cliThrowingPrompts, `{}`, true, cliGlobThrowMessage()},
		{"final load, settings first", "npm:eprobe", cliThrowingPrompts, `{"skills":["file:///a%2Fb"]}`, true, invalidFileURLMessage()},
		{"pre-trust load, npm source", "npm:eprobe", cliThrowingPrompts, `{}`, false, cliGlobThrowMessage()},
		{"pre-trust load, settings first", "npm:eprobe", cliThrowingPrompts, `{"skills":["file:///a%2Fb"]}`, false, invalidFileURLMessage()},
		{"pre-trust load, no throw", "npm:eprobe", `"pi":{"extensions":["extensions/a.js"]}`, `{}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			marker := filepath.Join(root, "factory-ran")
			quoted, err := json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			writeResourceLoaderFixture(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json"), `{}`)
			writeResourceLoaderFixture(t, filepath.Join(agentDir, "settings.json"), tc.settings)
			writeResourceLoaderFixture(t, filepath.Join(agentDir, "extensions", "probe.js"), `import {writeFileSync} from "node:fs"; export default function() { writeFileSync(`+string(quoted)+`, "ran"); }`)
			writeCLIGlobPackage(t, cliGlobPackageRoot(t, agentDir, tc.source), tc.fields)
			args := []string{"--no-session", "-e", tc.source, "--print", "hello"}
			if tc.approve {
				args = append([]string{"--approve"}, args...)
			}
			run := runPigStartup(t, binary, root, agentDir, cwd, "", args...)
			_, statErr := os.Stat(marker)
			if tc.want == "" {
				if statErr != nil {
					t.Fatalf("the pre-trust factory did not run: %v; startup %v, stderr %q", statErr, run.err, run.stderr)
				}
				return
			}
			if !os.IsNotExist(statErr) {
				t.Fatalf("an extension factory ran before the -e source's throw: %v", statErr)
			}
			var exitErr *exec.ExitError
			if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 1 || run.stdout != "" || run.stderr != "Error: "+tc.want+"\n" {
				t.Fatalf("startup = %v, stdout %q, stderr %q; want exit 1 and %q", run.err, run.stdout, run.stderr, tc.want)
			}
		})
	}
}

// Pi resolves each -e source once per load (resource-loader.ts:525-528): an unpinned git source's checkout is refreshed once at startup, in every mode, and once by the reload's resource snapshot. Probed with Pi 1.1.0 through a git that logs its calls: one refresh at RPC startup.
func TestCLIGitSourceRefreshesOncePerLoad(t *testing.T) {
	setup := func(t *testing.T) packageProcessFixture {
		t.Helper()
		f := newPackageProcessFixture(t, "process.exit(1);")
		root := cliGlobPackageRoot(t, f.agent, cliGitSource)
		writeResourceLoaderFixture(t, filepath.Join(root, "package.json"), `{"name":"eprobe","version":"1.0.0","pi":{"prompts":["prompts/*.md"]}}`)
		writeResourceLoaderFixture(t, filepath.Join(root, "prompts", "a.md"), "x\n")
		return f
	}
	refreshes := func(t *testing.T, f packageProcessFixture) int {
		t.Helper()
		n := 0
		for _, c := range f.calls(t) {
			if c.Command == "git" && slices.Contains(c.Args, "@{upstream}") {
				n++
			}
		}
		return n
	}
	for _, mode := range [][]string{{"--print"}, {"--mode", "json"}, {"--mode", "rpc"}} {
		t.Run(strings.Join(mode, " "), func(t *testing.T) {
			f := setup(t)
			builder := newCLIGlobTestBuilder(t, f, append([]string{"-e", cliGitSource}, mode...)...)
			build, err := builder.buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
			if err != nil {
				t.Fatal(err)
			}
			releaseCLIBuildAtCleanup(t, build)
			manager, err := coding.NewInMemorySessionManager(f.cwd)
			if err != nil {
				t.Fatal(err)
			}
			if err := builder.buildSession(t.Context(), build, cliBuildInput{CWD: f.cwd, Manager: manager}); err != nil {
				t.Fatal(err)
			}
			if n := refreshes(t, f); n != 1 {
				t.Fatalf("startup refreshed the checkout %d times, want 1; calls %+v", n, f.calls(t))
			}
		})
	}
	t.Run("reload snapshot", func(t *testing.T) {
		f := setup(t)
		snapshot := reloadResourceSnapshotProvider(f.cwd, f.agent, f.settings, Args{Extensions: []string{cliGitSource}}, nil)()
		if snapshot.Err != nil {
			t.Fatal(snapshot.Err)
		}
		if n := refreshes(t, f); n != 1 {
			t.Fatalf("the snapshot refreshed the checkout %d times, want 1; calls %+v", n, f.calls(t))
		}
	})
}

// A startup that went on past a failed -e install runs the -e extension in a host process of the build. Pi's extensions end with its process; here the build's Host.Shutdown must stop that process and wait until it has exited, so none outlives the build (a Windows process still ending holds its files open, and the test's temporary directories could not be removed).
func TestCLIPackageSourceInstallFailureHostProcessEndsWithShutdown(t *testing.T) {
	f := newPackageProcessFixture(t, "process.exit(1);")
	pidFile := filepath.Join(t.TempDir(), "host-pid")
	quoted, err := json.Marshal(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	writeResourceLoaderFixture(t, filepath.Join(f.agent, "extensions", "probe.js"), `import {writeFileSync} from "node:fs"; export default function() { writeFileSync(`+string(quoted)+`, String(process.pid)); }`)
	build, err := newCLIGlobTestBuilder(t, f, "--print", "-e", "npm:eprobe-missing").buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
	if err != nil {
		t.Fatalf("startup error = %v, want startup to go on after the failed npm command", err)
	}
	defer build.Services.Close()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the extension factory did not run: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if !extensionProcessAlive(pid) {
		t.Fatalf("the extension host process %d ended before the build was released, so the test does not show Shutdown waiting for it", pid)
	}
	build.Host.Shutdown("test")
	if extensionProcessAlive(pid) {
		t.Fatalf("the extension host process %d outlived Host.Shutdown", pid)
	}
}

// releaseCLIBuildAtCleanup shuts build's extension host down and closes its services before the test's temporary directories are removed (cleanups run last-in first-out, and the directories were made before the build). A build whose startup went on past a failed install runs the -e extension in a host process; the test context's end only starts killing that process, and Host.Shutdown is what waits until it has exited. Without the wait, RemoveAll of the agent directory failed with "The directory is not empty" on Windows in 80 of 80 runs.
func releaseCLIBuildAtCleanup(t *testing.T, build *cliBuild) {
	t.Helper()
	t.Cleanup(func() {
		if build.Host != nil {
			build.Host.Shutdown("test")
		}
		build.Services.Close()
	})
}

// newCLIGlobTestBuilder is the startup builder of a faux-model run with args in f's agent dir.
func newCLIGlobTestBuilder(t *testing.T, f packageProcessFixture, args ...string) *cliRuntimeBuilder {
	t.Helper()
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseArgs(append([]string{"--model", "test-faux/faux-1"}, args...))
	return &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: f.agent, launchCWD: f.cwd,
		settingsManager: f.settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(f.settings),
		startupUIOptions: startupUIOptions(flags, false, f.cwd, f.agent, f.settings),
		trustStore:       codingagent.NewProjectTrustStore(f.agent), trustByCWD: map[string]bool{},
		stageExtensionSDKs: func() error { return nil },
	}
}

// Pi 6dfb54678: resolveExtensionSources catches the failed install of a missing npm or git -e source, so the source contributes nothing and is reported as an extension warning, every
// later -e source is resolved too, and the extension factories still run.
func TestCLIPackageSourceInstallFailureWarnsAndStartupContinues(t *testing.T) {
	for _, tc := range []struct{ source, second, command string }{
		{"npm:eprobe-missing", "npm:eprobe-second", "npm"},
		{cliGitSource, "git:https://example.com/owner/second", "git"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			f := newPackageProcessFixture(t, "process.exit(1);")
			marker := filepath.Join(t.TempDir(), "factory-ran")
			quoted, err := json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			writeResourceLoaderFixture(t, filepath.Join(f.agent, "extensions", "probe.js"), `import {writeFileSync} from "node:fs"; export default function() { writeFileSync(`+string(quoted)+`, "ran"); }`)
			build, err := newCLIGlobTestBuilder(t, f, "--print", "-e", tc.source, "-e", tc.second).buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
			if err != nil {
				t.Fatalf("startup error = %v, want startup to go on after the failed %s command", err, tc.command)
			}
			releaseCLIBuildAtCleanup(t, build)
			if _, statErr := os.Stat(marker); statErr != nil {
				t.Fatalf("the extension factory did not run: %v", statErr)
			}
			secondResolved := false
			for _, call := range f.calls(t) {
				secondResolved = secondResolved || strings.Contains(strings.Join(call.Args, " "), "second")
			}
			if !secondResolved {
				t.Fatal("the source after the failure was not resolved")
			}
			for _, source := range []string{tc.source, tc.second} {
				warned := false
				for _, diagnostic := range build.ExtensionDiagnostics {
					warned = warned || strings.Contains(diagnostic.Message, "Extension package \""+source+"\": failed to install (") && strings.Contains(diagnostic.Message, "Check the source and try again.")
				}
				if !warned {
					t.Fatalf("no install warning for %s in %+v", source, build.ExtensionDiagnostics)
				}
			}
		})
	}
}
