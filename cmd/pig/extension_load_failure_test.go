package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestExtensionLoadDiagnosticsMatchUpstreamFormat(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.ts")
	diagnostics := extensionLoadDiagnostics([]error{
		&subprocess.ExtensionLoadError{Name: "boom", Path: "/ext/boom", Err: errors.New("register boom")},
		&subprocess.ExtensionLoadError{Name: missing, Path: missing, Err: extensionPathMissingError{path: missing}},
		errors.New(`embedded cell "c": missing binary path`),
	})
	want := []string{
		`Failed to load extension "/ext/boom": Failed to load extension: register boom`,
		`Failed to load extension "` + missing + `": Extension path does not exist: ` + missing,
		`Failed to load extension: embedded cell "c": missing binary path`,
	}
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for i := range want {
		if diagnostics[i].Type != "error" || diagnostics[i].Message != want[i] {
			t.Fatalf("diagnostic %d = %#v, want error %q", i, diagnostics[i], want[i])
		}
	}
}

// Upstream resource-loader.ts reports a missing local -e path as
// "Extension path does not exist: <path>".
func TestCLIExtensionConfigsReportMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.ts")
	configs := cliExtensionConfigs(missing)
	if len(configs) != 1 {
		t.Fatalf("configs = %#v", configs)
	}
	if err := configs[0].ResolveError(); err == nil || err.Error() != "Extension path does not exist: "+missing {
		t.Fatalf("resolve error = %v", err)
	}
}

type startupRun struct {
	err    error
	stdout string
	stderr string
}

func runPigStartup(t *testing.T, binary, home, agentDir, cwd, stdin string, args ...string) startupRun {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"--model", "test-faux/echo", "--offline"}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return startupRun{err: err, stdout: stdout.String(), stderr: stderr.String()}
}

func requireUpstreamExtensionFailure(t *testing.T, run startupRun, wantErrors ...string) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("startup error = %v, want exit status 1\nstderr:\n%s", run.err, run.stderr)
	}
	lines := strings.Split(strings.TrimSpace(run.stderr), "\n")
	if lines[len(lines)-1] != `Hint: Start without extensions using "pig -ne".` {
		t.Fatalf("stderr does not end with the -ne hint:\n%s", run.stderr)
	}
	for _, want := range wantErrors {
		if !strings.Contains(run.stderr, "\n"+want) && !strings.HasPrefix(run.stderr, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, run.stderr)
		}
	}
}

// Pinned Pi 0.87.1, probed against upstream source, exits 1 when an
// auto-discovered extension fails to register, when a -e path
// does not exist, and when a -e directory is not an extension, printing
// `Error: Failed to load extension "<path>": ...` lines and the -ne hint.
// `-ne` skips discovered extensions but still loads -e paths. PiG used to warn
// and continue in all three cases.
func TestStartupExtensionLoadFailureMatchesUpstream(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	broken := filepath.Join(agentDir, "extensions", "brokendir")
	emptyDir := filepath.Join(home, "emptydir")
	for _, dir := range []string{cwd, broken, emptyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeStartupFixtureFile(t, filepath.Join(broken, "index.js"), "export default function () { throw new Error(\"register boom\"); }\n")
	missing := filepath.Join(home, "nope.ts")
	binary := buildPigBinaryForSignalTest(t)
	const commands = `{"id":"commands","type":"get_commands"}` + "\n"

	// Like upstream auto-discovery, the error names the discovered entry file.
	brokenError := `Error: Failed to load extension "` + filepath.Join(broken, "index.js") + `": Failed to load extension: `
	requireUpstreamExtensionFailure(t, runPigStartup(t, binary, home, agentDir, cwd, commands, "--mode", "rpc"), brokenError)
	requireUpstreamExtensionFailure(t, runPigStartup(t, binary, home, agentDir, cwd, "", "-p", "hello"), brokenError)

	recovered := runPigStartup(t, binary, home, agentDir, cwd, commands, "--mode", "rpc", "-ne")
	if recovered.err != nil || !strings.Contains(recovered.stdout, `"id":"commands"`) || strings.Contains(recovered.stderr, "Failed to load extension") {
		t.Fatalf("-ne startup: err=%v\nstdout:\n%s\nstderr:\n%s", recovered.err, recovered.stdout, recovered.stderr)
	}

	missingRun := runPigStartup(t, binary, home, agentDir, cwd, commands, "--mode", "rpc", "-ne", "-e", missing)
	requireUpstreamExtensionFailure(t, missingRun, `Error: Failed to load extension "`+missing+`": Extension path does not exist: `+missing)
	if strings.Contains(missingRun.stderr, broken) {
		t.Fatalf("-ne loaded a discovered extension:\n%s", missingRun.stderr)
	}
	requireUpstreamExtensionFailure(t, runPigStartup(t, binary, home, agentDir, cwd, commands, "--mode", "rpc", "-ne", "-e", emptyDir),
		`Error: Failed to load extension "`+emptyDir+`": Failed to load extension: `)
}

// Upstream discovers <dir>/index.js as the extension path and names that file
// in load errors. PiG loads the directory but reports the discovered file.
func TestAutoDiscoveredIndexExtensionReportsEntryFile(t *testing.T) {
	agentDir := t.TempDir()
	broken := filepath.Join(agentDir, "extensions", "brokendir")
	entry := filepath.Join(broken, "index.js")
	writeStartupFixtureFile(t, entry, "export default function () { throw new Error(\"register boom\"); }\n")
	configs := collectTopLevelExtensionConfigs(filepath.Join(agentDir, "extensions"), nil)
	if len(configs) != 1 || configs[0].Name != "brokendir" || configs[0].Source != broken {
		t.Fatalf("configs = %#v, want the brokendir directory selected", configs)
	}
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	_, errs := host.LoadAll(t.Context(), configs)
	diagnostics := extensionLoadDiagnostics(errs)
	want := `Failed to load extension "` + entry + `": Failed to load extension: `
	if len(diagnostics) != 1 || !strings.HasPrefix(diagnostics[0].Message, want) {
		t.Fatalf("diagnostics = %#v, want prefix %q", diagnostics, want)
	}
}

// A broken -e extension in a project that needs a trust decision is loaded by
// the pre-trust pass. Upstream loadFinalExtensionSet does not retry a failed
// preload and reports its error once; PiG used to load it again and print the
// failure twice, from two processes.
func TestStartupReportsFailedPreTrustExtensionOnce(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := trustProjectFixture(t)
	marker := filepath.Join(home, "loads")
	broken := filepath.Join(home, "broken.mjs")
	writeStartupFixtureFile(t, broken, "import {appendFileSync} from \"node:fs\";\nexport default function () { appendFileSync("+strconv.Quote(marker)+", \"load\\n\"); throw new Error(\"register boom\"); }\n")
	binary := buildPigBinaryForSignalTest(t)
	run := runPigStartup(t, binary, home, agentDir, project, "", "-e", broken, "-p", "hello")
	requireUpstreamExtensionFailure(t, run, `Error: Failed to load extension "`+broken+`": Failed to load extension: `)
	if count := strings.Count(run.stderr, "Failed to load extension \""+broken+"\""); count != 1 {
		t.Fatalf("failure printed %d times, want once:\n%s", count, run.stderr)
	}
	loads, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(loads), "load\n"); count != 1 {
		t.Fatalf("broken extension loaded %d times, want once", count)
	}
}

// Pinned Pi 0.87.1, probed against upstream source, loads two copies of one
// extension from two Packages side by side: their
// commands become ask:1 and ask:2 and every other extension loads. When both
// copies register the same tool, startup exits 1 with
// `Failed to load extension "<second path>": Tool "ask_user" conflicts with
// <first path>` and the -ne hint.
func TestStartupDuplicateExtensionCopiesMatchUpstream(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	const command = `pi.registerCommand("ask", { description: "ask", handler: async () => {} });`
	const tool = `pi.registerTool({ name: "ask_user", label: "Ask", description: "ask", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });`
	for _, withTool := range []bool{false, true} {
		home := t.TempDir()
		agentDir := filepath.Join(home, "agent")
		cwd := filepath.Join(home, "cwd")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		body := command
		if withTool {
			body += " " + tool
		}
		var packages, extensions []string
		for _, name := range []string{"one", "two"} {
			pkg := filepath.Join(home, "ask-"+name)
			writeStartupFixtureFile(t, filepath.Join(pkg, "package.json"), `{"name":"ask","pi":{"extensions":["extensions/ask.mjs"]}}`)
			writeStartupFixtureFile(t, filepath.Join(pkg, "extensions", "ask.mjs"), "export default function (pi) { "+body+" }\n")
			packages = append(packages, pkg)
			extensions = append(extensions, filepath.Join(pkg, "extensions", "ask.mjs"))
		}
		other := filepath.Join(home, "other", "other.mjs")
		writeStartupFixtureFile(t, other, "export default function (pi) { pi.registerCommand(\"other\", { description: \"other\", handler: async () => {} }); }\n")
		settings, err := json.Marshal(map[string]any{"packages": packages, "extensions": []string{other}})
		if err != nil {
			t.Fatal(err)
		}
		writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(settings))

		run := runRPCStartup(t, binary, home, agentDir, cwd)
		if withTool {
			var exitErr *exec.ExitError
			if !errors.As(run.err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("tool conflict startup error = %v, want exit 1\nstderr:\n%s", run.err, run.stderr)
			}
			want := `Error: Failed to load extension "` + extensions[1] + `": Tool "ask_user" conflicts with ` + extensions[0]
			if !strings.Contains(run.stderr, want) || !strings.Contains(run.stderr, `Hint: Start without extensions using "pig -ne".`) {
				t.Fatalf("stderr lacks %q and the hint:\n%s", want, run.stderr)
			}
			continue
		}
		if run.err != nil {
			t.Fatalf("duplicate copies startup: %v\nstderr:\n%s", run.err, run.stderr)
		}
		for _, want := range []string{"ask:1", "ask:2", "other"} {
			if !slices.Contains(run.commands, want) {
				t.Fatalf("RPC commands %v lack %q\nstderr:\n%s", run.commands, want, run.stderr)
			}
		}
	}
}

// Upstream resolves a -e directory with a "pi" manifest as a Package: each
// entry its manifest yields is its own extension with CLI provenance, and a
// manifest whose directory entry holds no extension loads nothing.
func TestCLIExtensionConfigsLoadEachPackageDirectoryEntry(t *testing.T) {
	root := t.TempDir()
	extension := "export default function extension(pi) {}\n"
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := filepath.Join(root, "pkg")
	write(filepath.Join(pkg, "package.json"), `{"pi":{"extensions":["./extensions"]}}`)
	write(filepath.Join(pkg, "extensions", "alpha.ts"), extension)
	write(filepath.Join(pkg, "extensions", "beta.js"), extension)
	write(filepath.Join(pkg, "extensions", "gamma", "index.ts"), extension)
	empty := filepath.Join(root, "empty")
	write(filepath.Join(empty, "package.json"), `{"pi":{"extensions":["./extensions"]}}`)
	write(filepath.Join(empty, "extensions", "README.md"), "nothing\n")

	configs := cliExtensionConfigs(pkg)
	var sources []string
	for _, config := range configs {
		if err := config.ResolveError(); err != nil {
			t.Fatalf("config %s did not resolve: %v", config.Name, err)
		}
		if info, ok := config.SourceInfo.(codingagent.PiSourceInfo); !ok || info.Source != "cli" || info.Scope != "temporary" {
			t.Fatalf("config %s source info = %#v, want CLI provenance", config.Name, config.SourceInfo)
		}
		sources = append(sources, config.Source)
	}
	want := []string{
		filepath.Join(pkg, "extensions", "alpha.ts"),
		filepath.Join(pkg, "extensions", "beta.js"),
		filepath.Join(pkg, "extensions", "gamma", "index.ts"),
	}
	if !slices.Equal(sources, want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	if configs := cliExtensionConfigs(empty); len(configs) != 0 {
		t.Fatalf("empty directory entry loaded %#v, want nothing", configs)
	}
}

// Upstream auto-discovery loads each entry an extension directory's manifest
// declares; an index among several entries is its own entry, not the whole
// directory.
func TestTopLevelExtensionDirectoryLoadsEachManifestEntry(t *testing.T) {
	autoDir := filepath.Join(t.TempDir(), "extensions")
	dir := filepath.Join(autoDir, "multi")
	for name, content := range map[string]string{
		"package.json": `{"pi":{"extensions":["./index.ts","./other.ts"]}}`,
		"index.ts":     "export default function extension(pi) {}\n",
		"other.ts":     "export default function extension(pi) {}\n",
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configs := collectTopLevelExtensionConfigs(autoDir, nil)
	if len(configs) != 2 {
		t.Fatalf("configs = %#v, want one per manifest entry", configs)
	}
	for _, config := range configs {
		if err := config.ResolveError(); err != nil {
			t.Fatalf("config %s did not resolve: %v", config.Name, err)
		}
	}
}
