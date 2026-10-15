package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// fileTreeOf lists every path under root, relative to it, in lexical order.
func fileTreeOf(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." {
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return paths
}

// lazyBundleHome is a PIG_HOME whose agent directory configures only the test-faux model.
func lazyBundleHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// runLazyBundlePig runs the pig binary with the test-faux model in cwd under home and returns its stdout.
func runLazyBundlePig(t *testing.T, home, cwd string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(testbudget.Context(t), buildPigBinaryForSignalTest(t), append([]string{"--model", "test-faux/faux-1", "--no-session"}, args...)...)
	command.Dir = cwd
	command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=", "HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("pig %q: %v\nstderr:\n%s", args, err, stderr.String())
	}
	return stdout.String()
}

// A print run that calls no tool and loads no extension has no reader for the documentation bundle or the extension SDKs, so it writes
// neither: Pi writes no such files, and the run's output is the one the eager writes produced.
func TestPrintRunWithoutAReaderWritesNeitherBundle(t *testing.T) {
	t.Parallel()
	home := lazyBundleHome(t)
	stdout := runLazyBundlePig(t, home, t.TempDir(), "--print", "What is 20+22?")
	if stdout == "" {
		t.Fatal("the print run wrote no answer")
	}
	for _, dir := range []string{"docs", filepath.Join("state", "pigsdk")} {
		if _, err := os.Lstat(filepath.Join(home, dir)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a print run with no tool call and no extension created %s (err %v); tree: %v", dir, err, fileTreeOf(t, home))
		}
	}
}

// The system prompt names the documentation bundle, and a tool call is how the model reads a file. The tool sees the complete bundle, with the
// embedded pages' bytes, before it runs.
func TestPrintToolCallSeesTheDocsBundle(t *testing.T) {
	t.Parallel()
	home := lazyBundleHome(t)
	stdout := runLazyBundlePig(t, home, home, "--tools", "ls", "--mode", "json", "Run: ls here")
	var listing string
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var event struct {
			Type     string `json:"type"`
			ToolName string `json:"toolName"`
			Result   struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Type == "tool_execution_end" && event.ToolName == "ls" {
			for _, content := range event.Result.Content {
				listing += content.Text
			}
		}
	}
	if !slices.Contains(strings.Split(listing, "\n"), "docs/") {
		t.Fatalf("the ls tool did not see the docs bundle in %s:\n%q", home, listing)
	}
	dir := pigdocs.DocsDir(home)
	for _, name := range pigdocs.List() {
		want, err := pigdocs.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("bundle page %s differs from the embedded page (err %v)", name, err)
		}
	}
}

// tryLazyBundleBuild runs one production build with the given flags in a fresh project, counting the SDK stages; startup selects the startup
// build, which reports a stage failure on stderr, over a replacement build, which reports it as a diagnostic.
func tryLazyBundleBuild(t *testing.T, args []string, startup bool, stage func() error) (*cliBuild, int) {
	t.Helper()
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", agentDir)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseArgs(append([]string{"--model", "test-faux/faux-1", "--print"}, args...))
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	stages := 0
	builder := &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
		settingsManager: settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
		startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
		trustStore:       codingagent.NewProjectTrustStore(agentDir), trustByCWD: map[string]bool{},
		stageExtensionSDKs: func() error { stages++; return stage() },
	}
	build, err := builder.buildResources(t.Context(), cliBuildInput{CWD: cwd, Startup: startup})
	if err != nil {
		t.Fatalf("startup build failed: %v", err)
	}
	t.Cleanup(func() {
		if build.Host != nil {
			build.Host.Shutdown("test")
		}
		build.Services.Close()
	})
	// A second build of the same process stages nothing again.
	if second, err := builder.buildResources(t.Context(), cliBuildInput{CWD: cwd}); err == nil {
		if second.Host != nil {
			second.Host.Shutdown("test")
		}
		second.Services.Close()
	}
	return build, stages
}

// lazyBundleExtension writes a one-file extension whose load starts an extension process.
func lazyBundleExtension(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "noop.ts")
	if err := os.WriteFile(path, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The extension SDKs are read only by extension builds and runtimes, so a build stages them once per process before it starts its first
// extension, and a build that starts none stages nothing.
func TestBuildStagesSDKsOnlyBeforeItStartsAnExtension(t *testing.T) {
	if _, stages := tryLazyBundleBuild(t, nil, true, func() error { return nil }); stages != 0 {
		t.Errorf("a build without extensions staged the SDKs %d times, want 0", stages)
	}
	if _, stages := tryLazyBundleBuild(t, []string{"-e", lazyBundleExtension(t)}, true, func() error { return nil }); stages != 1 {
		t.Errorf("a process whose builds load an extension staged the SDKs %d times, want 1", stages)
	}
}

// A replacement build stages while the mode owns the terminal, so its stage failure is a warning diagnostic of the build; the startup build
// prints the startup warning.
func TestReplacementBuildReportsSDKStageFailureAsADiagnostic(t *testing.T) {
	failure := errors.New("disk full")
	build, _ := tryLazyBundleBuild(t, []string{"-e", lazyBundleExtension(t)}, false, func() error { return failure })
	want := "extension SDK staging failed: disk full; extensions that need the SDK may fail to build; run 'pig diagnose' for detail"
	if !slices.ContainsFunc(build.ExtensionDiagnostics, func(d codingagent.AgentSessionRuntimeDiagnostic) bool {
		return d.Type == "warning" && d.Message == want
	}) {
		t.Fatalf("diagnostics %+v lack the stage failure %q", build.ExtensionDiagnostics, want)
	}
	_, stderr, _ := captureStdoutStderr(t, func() int {
		tryLazyBundleBuild(t, []string{"-e", lazyBundleExtension(t)}, true, func() error { return failure })
		return 0
	})
	if !strings.Contains(stderr, "pig: warning: extension SDK staging failed: disk full\n") {
		t.Fatalf("the startup build did not print the stage failure; stderr:\n%s", stderr)
	}
}

// `pig diagnose` names when unstaged SDKs are staged.
func TestDiagnoseSaysWhenSDKsStage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	stages, err := pigsdk.Status(home)
	if err != nil || len(stages) == 0 || stages[0].Current {
		t.Fatalf("a fresh config root reports %+v (err %v)", stages, err)
	}
	var out strings.Builder
	runDiagnose(&out, "")
	if !strings.Contains(out.String(), "not staged: stages when an extension loads") {
		t.Fatalf("diagnose output:\n%s", out.String())
	}
}

// A `!` command and an RPC bash command read files with no tool call before them, so interactive and RPC start write the documentation bundle.
func TestRPCStartWritesTheDocsBundle(t *testing.T) {
	t.Parallel()
	home := lazyBundleHome(t)
	runLazyBundlePig(t, home, t.TempDir(), "--mode", "rpc")
	if _, err := os.Stat(filepath.Join(pigdocs.DocsDir(home), "README.md")); err != nil {
		t.Fatalf("RPC start did not write the docs bundle: %v; tree: %v", err, fileTreeOf(t, home))
	}
}

// An extension process may read any file the system prompt names, so a build writes the documentation bundle before it starts an extension,
// and a build that starts none writes nothing.
func TestBuildWritesTheDocsBundleBeforeItStartsAnExtension(t *testing.T) {
	for _, test := range []struct {
		name  string
		args  []string
		wrote bool
	}{
		{"no extension", nil, false},
		{"extension", []string{"-e", lazyBundleExtension(t)}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Cleanup(func() { configroot.SetDocsWriter(nil); configroot.SetDocsPresent(true) })
			prepareDocsBundle()
			if !configroot.DocsPresent() {
				t.Fatal("a writable config root names no docs bundle")
			}
			tryLazyBundleBuild(t, test.args, true, func() error { return nil })
			// The deferred write resolves the config root when it runs, under the build's PIG_HOME.
			_, err := os.Stat(filepath.Join(pigdocs.DocsDir(codingagent.ConfigRoot()), "README.md"))
			if wrote := err == nil; wrote != test.wrote {
				t.Fatalf("docs bundle written = %v, want %v (err %v)", wrote, test.wrote, err)
			}
		})
	}
}
