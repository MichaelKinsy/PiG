package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type startupPackageFixture struct {
	mixed, healthy, escaping           string
	badExtension, mixedGoodExtension   string
	healthyExtension, escapedExtension string
	mixedPrompt, healthyPrompt         string
	healthySkill, escapingPrompt       string
}

func writeStartupFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeStartupPackages lays out three Packages: "mixed" holds an extension
// with no factory beside a healthy extension and prompt, "healthy" is valid,
// and "escaping" declares an extension outside its root.
func writeStartupPackages(t *testing.T, root string, withGoodExtensions bool) startupPackageFixture {
	t.Helper()
	f := startupPackageFixture{
		mixed:    filepath.Join(root, "mixed"),
		healthy:  filepath.Join(root, "healthy"),
		escaping: filepath.Join(root, "escaping"),
	}
	f.badExtension = filepath.Join(f.mixed, "extensions", "bad")
	f.mixedGoodExtension = filepath.Join(f.mixed, "extensions", "good")
	f.healthyExtension = filepath.Join(f.healthy, "extensions", "healthy")
	f.escapedExtension = filepath.Join(root, "outside")
	f.mixedPrompt = filepath.Join(f.mixed, "prompts", "mixed-prompt.md")
	f.healthyPrompt = filepath.Join(f.healthy, "prompts", "healthy-prompt.md")
	f.healthySkill = filepath.Join(f.healthy, "skills", "healthy-skill")
	f.escapingPrompt = filepath.Join(f.escaping, "prompts", "escaping-prompt.md")

	goodExtension := func(dir, name string) {
		writeStartupFixtureFile(t, filepath.Join(dir, "go.mod"), "module example.com/"+name+"\n\ngo 1.26\n")
		writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), fmt.Sprintf("package %s\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(%q) }\n", name, name))
	}
	mixedExtensions := `["extensions/bad"]`
	healthyExtensions := `[]`
	if withGoodExtensions {
		mixedExtensions = `["extensions/bad","extensions/good"]`
		healthyExtensions = `["extensions/healthy"]`
		goodExtension(f.mixedGoodExtension, "good")
		goodExtension(f.healthyExtension, "healthy")
	}
	writeStartupFixtureFile(t, filepath.Join(f.badExtension, "go.mod"), "module example.com/bad\n\ngo 1.26\n")
	writeStartupFixtureFile(t, filepath.Join(f.badExtension, "extension.go"), "package bad\n\nfunc NotAFactory() { panic(\"bad factory executed\") }\n")
	writeStartupFixtureFile(t, filepath.Join(f.mixed, "package.json"), `{"name":"mixed","pi":{"extensions":`+mixedExtensions+`,"prompts":["prompts"]}}`)
	writeStartupFixtureFile(t, f.mixedPrompt, "---\ndescription: mixed prompt\n---\nmixed\n")

	writeStartupFixtureFile(t, filepath.Join(f.healthy, "package.json"), `{"name":"healthy","pi":{"extensions":`+healthyExtensions+`,"prompts":["prompts"]}}`)
	writeStartupFixtureFile(t, f.healthyPrompt, "---\ndescription: healthy prompt\n---\nhealthy\n")
	writeStartupFixtureFile(t, filepath.Join(f.healthySkill, "SKILL.md"), "---\nname: healthy-skill\ndescription: healthy skill\n---\nbody\n")

	goodExtension(f.escapedExtension, "outside")
	writeStartupFixtureFile(t, filepath.Join(f.escaping, "package.json"), `{"name":"escaping","pi":{"extensions":["../outside"],"prompts":["prompts"]}}`)
	writeStartupFixtureFile(t, f.escapingPrompt, "---\ndescription: escaping prompt\n---\nescaping\n")
	return f
}

func allScopesLoadExtensions(string) bool { return true }

// startupPackageValidationError reports the first Package failure, or the
// first unresolvable Package extension, that startup would exit on.
func startupPackageValidationError(cwd string, sm *codingagent.SettingsManager) error {
	if err := validateConfiguredPackagesForStartup(cwd, sm, allScopesLoadExtensions); err != nil {
		return err
	}
	for _, config := range collectPackageExtensionConfigs(cwd, sm, nil) {
		if resolveErr := config.ResolveError(); resolveErr != nil {
			return errors.New(extensionLoadFailureDiagnostic(config.Name, resolveErr).Message)
		}
	}
	return nil
}

// Upstream loader.ts loadExtensions records every extension that fails to load
// and main.ts reports all of them together. Startup validation therefore does
// not fail a Package for an unresolvable extension: the collector emits each
// one as an unresolved config for the final extension load to report, and
// Packages whose extensions are not loaded (`pig -ne`) skip resolution.
func TestValidateConfiguredPackagesForStartupLeavesExtensionFailuresToTheLoad(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "work"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := writeStartupPackages(t, filepath.Join(root, "packages"), true)
	other := filepath.Join(root, "packages", "other")
	otherBad := filepath.Join(other, "extensions", "worse")
	writeStartupFixtureFile(t, filepath.Join(other, "package.json"), `{"name":"other","pi":{"extensions":["extensions/worse"]}}`)
	writeStartupFixtureFile(t, filepath.Join(otherBad, "go.mod"), "module example.com/worse\n\ngo 1.26\n")
	writeStartupFixtureFile(t, filepath.Join(otherBad, "extension.go"), "package worse\n\nfunc NotAFactory() {}\n")
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: f.mixed}, {Source: f.healthy}, {Source: other}}); err != nil {
		t.Fatal(err)
	}

	for _, loads := range []bool{true, false} {
		if err := validateConfiguredPackagesForStartup(cwd, sm, func(string) bool { return loads }); err != nil {
			t.Fatalf("extensions loaded %v: %v", loads, err)
		}
	}
	var unresolved []string
	for _, config := range collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil) {
		if resolveErr := config.ResolveError(); resolveErr != nil {
			want := fmt.Sprintf(`Failed to load extension "%s": Failed to load extension: `, config.Name)
			if message := extensionLoadFailureDiagnostic(config.Name, resolveErr).Message; !strings.HasPrefix(message, want) {
				t.Fatalf("diagnostic = %q, want prefix %q", message, want)
			}
			unresolved = append(unresolved, config.Name)
		}
	}
	slices.Sort(unresolved)
	if want := []string{f.badExtension, otherBad}; !slices.Equal(unresolved, want) {
		t.Fatalf("unresolved extensions = %v, want %v", unresolved, want)
	}

	if err := sm.SetPackages([]codingagent.PackageSource{{Source: f.healthy}, {Source: f.escaping}}); err != nil {
		t.Fatal(err)
	}
	for _, loads := range []bool{true, false} {
		err := validateConfiguredPackagesForStartup(cwd, sm, func(string) bool { return loads })
		// The source is reported with Go quoting (%q), which doubles Windows
		// backslashes.
		if err == nil || !strings.Contains(err.Error(), "Package "+strconv.Quote(f.escaping)) || !strings.Contains(err.Error(), "escapes package root") {
			t.Fatalf("escaping Package (extensions loaded %v) error = %v", loads, err)
		}
	}
}

// Startup validation, pre-trust loading, final discovery, and source-info
// annotation all classify the same Package extension. The startup snapshot
// performs that source scan once; reload intentionally uses a fresh resolver.
func TestStartupExtensionSourceResolverScansEachSourceOnce(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "work"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture := writeStartupPackages(t, filepath.Join(root, "packages"), true)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: fixture.healthy}}); err != nil {
		t.Fatal(err)
	}

	calls := map[string]int{}
	resolver := newStartupExtensionSourceResolver(func(path string) (extsource.Definition, error) {
		calls[path]++
		return extsource.Resolve(path)
	})
	userScope := []string{"user"}
	_ = collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, &userScope, resolver.Resolve)
	_ = collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, true, resolver.Resolve)
	_ = collectThemePaths(cwd, agentDir, sm, CLIFlags{}, true, resolver.Resolve)
	if err := validateConfiguredPackagesForStartup(cwd, sm, allScopesLoadExtensions, resolver.Resolve); err != nil {
		t.Fatal(err)
	}
	_ = collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, nil, resolver.Resolve)
	configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil, resolver.Resolve)
	if len(configs) != 1 || configs[0].Source != fixture.healthyExtension {
		t.Fatalf("configs = %#v, want %s", configs, fixture.healthyExtension)
	}
	_ = resourceSourceInfoProvider(cwd, agentDir, sm, CLIFlags{}, resolver.Resolve)()

	if got := calls[fixture.healthyExtension]; got != 1 {
		t.Fatalf("source scans = %d, want 1; all scans: %v", got, calls)
	}
}

type rpcStartupResult struct {
	err      error
	stderr   string
	commands []string
}

func runRPCStartup(t *testing.T, binary, home, agentDir, cwd string, args ...string) rpcStartupResult {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"--mode", "rpc", "--model", "test-faux/echo", "--offline"}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	cmd.Stdin = strings.NewReader(`{"id":"commands","type":"get_commands"}` + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	result := rpcStartupResult{err: cmd.Run(), stderr: stderr.String()}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record struct {
			ID      string             `json:"id"`
			Success bool               `json:"success"`
			Data    RPCGetCommandsData `json:"data"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.ID != "commands" {
			continue
		}
		if !record.Success {
			t.Fatalf("get_commands failed: %s", scanner.Text())
		}
		for _, command := range record.Data.Commands {
			result.commands = append(result.commands, command.Name)
		}
	}
	return result
}

// Pinned Pi 0.87.1, probed against upstream source, exits 1 in RPC and print
// mode when a configured Package extension fails to
// load, printing `Error: Failed to load extension "<path>": Failed to load
// extension: <message>` and `Hint: Start without extensions using "pi -ne".`;
// with -ne it starts. PiG used to abort with a Package validation error even
// under -ne, which made the hint's recovery impossible.
func TestRPCStartupConfiguredPackageExtensionFailureMatchesUpstream(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := writeStartupPackages(t, filepath.Join(home, "packages"), false)
	settings, err := json.Marshal(map[string]any{"packages": []string{f.mixed, f.healthy}})
	if err != nil {
		t.Fatal(err)
	}
	writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(settings))
	binary := buildPigBinaryForSignalTest(t)

	failed := runRPCStartup(t, binary, home, agentDir, cwd)
	var exitErr *exec.ExitError
	if !errors.As(failed.err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("RPC startup error = %v, want exit status 1\nstderr:\n%s", failed.err, failed.stderr)
	}
	wantError := fmt.Sprintf(`Error: Failed to load extension "%s": Failed to load extension: `, f.badExtension)
	wantHint := `Hint: Start without extensions using "pig -ne".`
	lines := strings.Split(strings.TrimSpace(failed.stderr), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[len(lines)-2], wantError) || lines[len(lines)-1] != wantHint {
		t.Fatalf("stderr does not end with the upstream extension failure and hint:\n%s", failed.stderr)
	}
	if strings.Contains(failed.stderr, "bad factory executed") || len(failed.commands) != 0 {
		t.Fatalf("startup continued past the failure: commands %v\nstderr:\n%s", failed.commands, failed.stderr)
	}

	recovered := runRPCStartup(t, binary, home, agentDir, cwd, "-ne")
	if recovered.err != nil {
		t.Fatalf("RPC startup with -ne: %v\nstderr:\n%s", recovered.err, recovered.stderr)
	}
	if strings.Contains(recovered.stderr, "Failed to load extension") {
		t.Fatalf("-ne startup reported an extension it does not load:\n%s", recovered.stderr)
	}
	for _, want := range []string{"mixed-prompt", "healthy-prompt", "skill:healthy-skill"} {
		if !slices.Contains(recovered.commands, want) {
			t.Fatalf("RPC commands %v lack %q\nstderr:\n%s", recovered.commands, want, recovered.stderr)
		}
	}
}

// Reload collects Package extensions without startup validation; an
// unresolvable one must reach the host as a load failure beside its healthy
// siblings instead of being dropped silently.
func TestCollectPackageExtensionConfigsKeepsUnresolvedExtensionAsFailure(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "work"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := writeStartupPackages(t, filepath.Join(root, "packages"), true)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: f.mixed}}); err != nil {
		t.Fatal(err)
	}
	configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil)
	var healthy, failed int
	for _, config := range configs {
		switch {
		case config.Source == f.mixedGoodExtension && config.ResolveError() == nil:
			healthy++
		case config.ResolveError() != nil:
			failed++
			if diagnostic := extensionLoadDiagnostics([]error{loadErrorFor(t, []subprocess.ExtConfig{config})})[0]; !strings.HasPrefix(diagnostic.Message, `Failed to load extension "`+f.badExtension+`": Failed to load extension: `) {
				t.Fatalf("diagnostic = %q", diagnostic.Message)
			}
		}
	}
	if healthy != 1 || failed != 1 {
		t.Fatalf("configs = %#v, want the healthy sibling and one failure", configs)
	}
}
