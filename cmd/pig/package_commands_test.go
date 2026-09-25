package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type pipeReadResult struct {
	data []byte
	err  error
}

func drainPipe(r *os.File) <-chan pipeReadResult {
	done := make(chan pipeReadResult, 1)
	go func() {
		data, err := io.ReadAll(r)
		done <- pipeReadResult{data: data, err: err}
	}()
	return done
}

func captureStdoutStderr(t *testing.T, fn func() int) (stdout, stderr string, code int) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	os.Stderr = wErr
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	outDone := drainPipe(rOut)
	errDone := drainPipe(rErr)
	code = fn()
	_ = wOut.Close()
	_ = wErr.Close()
	out := <-outDone
	if out.err != nil {
		t.Fatal(out.err)
	}
	errOutput := <-errDone
	if errOutput.err != nil {
		t.Fatal(errOutput.err)
	}
	_ = rOut.Close()
	_ = rErr.Close()
	return string(out.data), string(errOutput.data), code
}

func TestInstallPackageDoesNotProjectStaticComponents(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"side-effects"}`)
	write("plugin.json", `{"$schema":"https://vendor.example/plugin.schema.json","name":"side-effects","agents":"agents/","mcpServers":".mcp.json"}`)
	write(".pig-plugin/plugin.json", `{"extends":"../../plugin.json","piglets":["legacy.piglet.yaml"]}`)
	write("agents/review.agent.md", "# review\n")
	write(".mcp.json", `{"mcpServers":{"demo":{"command":"true"}}}`)
	write("legacy.piglet.yaml", "name: legacy\n")
	if err := installPackageArtifacts(t.TempDir(), codingagent.PackageSource{Source: root}, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(home, "mcp-servers.json"),
		filepath.Join(home, "agent", "agents", "review.md"),
		filepath.Join(home, "piglets", "legacy.yaml"),
		filepath.Join(home, "bin", "pig-legacy"),
		filepath.Join(home, "diagnostics", "mcp-pending.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Package install projected static component %s: %v", path, err)
		}
	}
}

func TestRetiredResourceCommandIsRejectedBeforeInteractiveStartup(t *testing.T) {
	_, stderr, code := captureStdoutStderr(t, func() int {
		return runRetiredCommand([]string{"resource", "list"})
	})
	if code != 2 || !strings.Contains(stderr, "unknown command resource") || !strings.Contains(stderr, "pig config") {
		t.Fatalf("retired resource command code=%d stderr=%q", code, stderr)
	}
	if code := runRetiredCommand([]string{"not-a-retired-command"}); code != -1 {
		t.Fatalf("unrelated command dispatch = %d, want -1", code)
	}
}

func TestListConfiguredPackagesPreservesBothSettingsScopesLikePi(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
	for _, dir := range []string{cwd, agentDir, packageRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
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
	configured := listConfiguredPackages(cwd, settings)
	if len(configured) != 2 || configured[0].Scope != "user" || configured[1].Scope != "project" {
		t.Fatalf("configured Packages = %#v", configured)
	}
	effective := configuredPackagesForResolution(cwd, settings)
	if len(effective) != 1 || effective[0].Scope != "user" || effective[0].ProjectDelta == nil {
		t.Fatalf("effective Packages = %#v", effective)
	}
}

func TestRunPackageCommandBareListMatchesUpstreamPackageList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"list"})
	})
	if code != 0 || stderr != "" || stdout != "No packages installed.\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestParsePackageCommand(t *testing.T) {
	opts, ok := parsePackageCommand([]string{"install", "npm:pi-web-access"})
	if !ok {
		t.Fatal("expected package command")
	}
	if opts.command != packageInstall || opts.source != "npm:pi-web-access" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParsePackageCommand_UninstallAliasRemoved(t *testing.T) {
	if _, ok := parsePackageCommand([]string{"uninstall", "foo"}); ok {
		t.Fatal("uninstall should not be parsed as a package command; use remove")
	}
}

func TestParsePackageCommand_LocalFlag(t *testing.T) {
	opts, ok := parsePackageCommand([]string{"install", "-l", "foo"})
	if !ok {
		t.Fatal("expected package command")
	}
	if !opts.local {
		t.Fatal("expected local flag to be set")
	}
}

func TestParsePackageCommand_ConfigNotHijacked(t *testing.T) {
	if _, ok := parsePackageCommand([]string{"config"}); ok {
		t.Fatal("config should not be parsed as a package command")
	}
}

func TestParsePackageCommand_InvalidOption(t *testing.T) {
	opts, ok := parsePackageCommand([]string{"list", "--local"})
	if !ok {
		t.Fatal("expected package command")
	}
	if opts.invalidOption != "--local" {
		t.Fatalf("invalidOption = %q, want --local", opts.invalidOption)
	}
}

func TestRunPackageCommand_HelpIncludesUpstreamExamples(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"install", "--help"})
	})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	for _, needle := range []string{
		"Examples:",
		"pig install npm:@foo/bar",
		"pig install ssh://git@github.com/user/repo",
		"pig install ./local/path",
	} {
		if !strings.Contains(stdout, needle) {
			t.Fatalf("help missing %q:\n%s", needle, stdout)
		}
	}
}

func TestRunPackageCommand_InvalidOptionMatchesUpstreamHint(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"list", "--local"})
	})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `Unknown option --local for "list".`) {
		t.Fatalf("stderr missing unknown-option line:\n%s", stderr)
	}
	if !strings.Contains(stderr, `Use "pig --help" or "pig list".`) {
		t.Fatalf("stderr missing usage hint:\n%s", stderr)
	}
}

func TestGetSelfUpdateUnavailableInstruction_PointsAtStandaloneFallback(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	got := codingagent.GetSelfUpdateUnavailableInstruction(codingagent.PackageName, []string{"pnpm", "--global"})
	if strings.Contains(got, "legacy-update-host") {
		t.Fatalf("instruction still references the stale legacy-update-host URL:\n%s", got)
	}
	if !strings.Contains(got, "PIG_UPDATE_URL") || !strings.Contains(got, "pig update") {
		t.Fatalf("instruction missing standalone-binary fallback:\n%s", got)
	}
}

func TestRunPackageCommand_SelfUpdateTargetWithoutSourceShowsFallback(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	for _, args := range [][]string{{"update", "self"}, {"update"}} {
		stdout, stderr, code := captureStdoutStderr(t, func() int {
			return runPackageCommand(args)
		})
		if code != 1 {
			t.Fatalf("%v: code = %d, want 1", args, code)
		}
		if stdout != "" {
			t.Fatalf("%v: stdout = %q, want empty", args, stdout)
		}
		// Writability alone no longer proves standalone ownership. Without the
		// installer receipt, the resolver refuses mutation and names the path.
		if !strings.Contains(stderr, "cannot self-update this installation") || !strings.Contains(stderr, "Executable:") {
			t.Fatalf("%v: stderr missing unknown-provenance remediation:\n%s", args, stderr)
		}
		if strings.Contains(stderr, "legacy-update-host") || strings.Contains(stderr, "pig update") {
			t.Fatalf("%v: stderr contains stale or looping remediation:\n%s", args, stderr)
		}
	}
}

func TestIsSelfUpdateTarget(t *testing.T) {
	for _, input := range []string{"self", "SELF", "pig"} {
		if !isSelfUpdateTarget(input) {
			t.Fatalf("isSelfUpdateTarget(%q) = false, want true", input)
		}
	}
	// "pi" is upstream's app name, not pig's: not a pig self-update target.
	for _, input := range []string{"", "pi", "npm:example", "example"} {
		if isSelfUpdateTarget(input) {
			t.Fatalf("isSelfUpdateTarget(%q) = true, want false", input)
		}
	}
}

func TestNPMInstallArgsMatchUpstreamManagedInstallContract(t *testing.T) {
	root := "/managed/npm"
	cases := []struct {
		manager string
		want    []string
	}{
		{manager: "npm", want: []string{"install", "@scope/pkg@1.2.3", "--prefix", root, "--legacy-peer-deps"}},
		{manager: "bun", want: []string{"install", "@scope/pkg@1.2.3", "--cwd", root, "--omit=peer"}},
		{manager: "pnpm", want: []string{"install", "@scope/pkg@1.2.3", "--prefix", root, "--config.auto-install-peers=false", "--config.strict-peer-dependencies=false", "--config.strict-dep-builds=false"}},
	}
	for _, tc := range cases {
		t.Run(tc.manager, func(t *testing.T) {
			if got := npmInstallArgs(tc.manager, "@scope/pkg@1.2.3", root, ""); !slices.Equal(got, tc.want) {
				t.Fatalf("args = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNPMInstallArgsPassCustomRegistryWithoutCredentials(t *testing.T) {
	registry := "https://npm.example.com/team"
	got := npmInstallArgs("npm", "@scope/pkg@1.2.3", "/managed/npm", registry)
	if !slices.Equal(got, []string{"install", "@scope/pkg@1.2.3", "--prefix", "/managed/npm", "--legacy-peer-deps", "--registry", registry}) {
		t.Fatalf("args = %v", got)
	}
	if strings.Contains(strings.Join(got, " "), "token") {
		t.Fatalf("registry arguments contain credential material: %v", got)
	}
}

func TestCustomNPMRegistryUsesDistinctManagedRoot(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	first, err := parseNpmInstallRef("npm:@acme/tools@1?registry=https%3A%2F%2Fnpm.one.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseNpmInstallRef("npm:@acme/tools@1?registry=https%3A%2F%2Fnpm.two.example")
	if err != nil {
		t.Fatal(err)
	}
	firstPath := npmInstallPath(cwd, first, false)
	secondPath := npmInstallPath(cwd, second, false)
	if firstPath == secondPath || !strings.Contains(firstPath, filepath.Join("npm", "registries")) || !strings.Contains(secondPath, filepath.Join("npm", "registries")) {
		t.Fatalf("custom registry paths = %q and %q", firstPath, secondPath)
	}
}

func TestInstallManagedNPMPassesCustomRegistryToPackageManager(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "npm.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$NPM_TEST_LOG\"\n"
	npm := writeStubScript(t, filepath.Join(binDir, "npm"), script)
	t.Setenv("NPM_TEST_LOG", logPath)
	settings, err := json.Marshal(map[string]any{"npmCommand": []string{npm}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), settings, 0o644); err != nil {
		t.Fatal(err)
	}
	source := "npm:@acme/tools@1.2.3?registry=https%3A%2F%2Fnpm.example.com%2Fteam"
	if err := installManagedNPM(cwd, source, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(data)
	if !strings.Contains(args, "install @acme/tools@1.2.3") || !strings.Contains(args, "--registry https://npm.example.com/team") {
		t.Fatalf("package manager args = %s", args)
	}
	if strings.Contains(args, "?registry=") {
		t.Fatalf("typed source query leaked into npm package spec: %s", args)
	}
}

func TestGetGitDependencyInstallArgs(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if got := getGitDependencyInstallArgs(sm); len(got) != 2 || got[0] != "install" || got[1] != "--omit=dev" {
		t.Fatalf("default args = %v", got)
	}
	if err := sm.SetNpmCommand([]string{"pnpm"}); err != nil {
		t.Fatal(err)
	}
	if got := getGitDependencyInstallArgs(sm); len(got) != 1 || got[0] != "install" {
		t.Fatalf("configured npmCommand args = %v", got)
	}
}

func TestDetectSourceKind(t *testing.T) {
	localDir := t.TempDir()
	cases := []struct {
		source string
		want   string
	}{
		{"npm:pi-web-access", "npm"},
		{"@scope/pkg", "npm"},
		{"registry:old/name", "unsupported"},
		{localDir, "local"},
	}
	for _, tc := range cases {
		if got := detectSourceKind(tc.source); got != tc.want {
			t.Fatalf("detectSourceKind(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

var contributedSourceKindSequence atomic.Uint64

func TestDetectRegisteredContributedSourceKind(t *testing.T) {
	scheme := fmt.Sprintf("test-catalog-%d", contributedSourceKindSequence.Add(1))
	if err := installresolver.RegisterSourceScheme(scheme); err != nil {
		t.Fatalf("register source scheme: %v", err)
	}
	if got := detectSourceKind(scheme + ":team/resource"); got != scheme {
		t.Fatalf("detectSourceKind = %q, want %q", got, scheme)
	}
	if got := detectSourceKind("unregistered:team/resource"); got != "unsupported" {
		t.Fatalf("unregistered kind = %q, want unsupported", got)
	}
}

func TestPackageSourceIdentityUsesSharedTypedContract(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		source string
		want   string
	}{
		{"npm:@scope/pkg@1.2.3", "npm:@scope/pkg"},
		{"npm:@scope/pkg@1.2.3?registry=https%3A%2F%2Fnpm.example.com", "npm:@scope/pkg?registry=https%3A%2F%2Fnpm.example.com"},
		{"git:github.com/acme/tools@v1", "git:github.com/acme/tools"},
		{"https://github.com/acme/tools.git@v2", "git:github.com/acme/tools"},
		{"git:https://github.com/acme/tools.git@v2#subdirectory=plugins%2Freview", "git:github.com/acme/tools#subdirectory=plugins/review"},
		{"marketplace:example/tools", "marketplace:example/tools"},
		{"./resources/tool", "local:" + filepath.Join(base, "resources", "tool")},
	}
	for _, tc := range cases {
		if got := packageSourceIdentity(base, tc.source); got != tc.want {
			t.Errorf("packageSourceIdentity(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestInstallManagedGitMaterializesSelectedSubdirectory(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "git.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$GIT_TEST_LOG"
if [ "$1" = clone ]; then
  mkdir -p "$3/plugins/review"
fi
`
	writeStubScript(t, filepath.Join(binDir, "git"), script)
	t.Setenv("GIT_TEST_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := "git:https://github.com/acme/tools.git@main#subdirectory=plugins%2Freview"
	if err := installManagedGit(cwd, source, false); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(agentDir, "git", "github.com", "acme", "tools")
	packageRoot, err := gitInstallPath(cwd, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if packageRoot != filepath.Join(checkout, "plugins", "review") {
		t.Fatalf("package root = %q", packageRoot)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logData)
	if !strings.Contains(log, "clone https://github.com/acme/tools.git "+checkout) {
		t.Fatalf("clone did not target the repository checkout: %s", log)
	}
	if strings.Contains(log, "subdirectory") || strings.Contains(log, "@main#") {
		t.Fatalf("git received a package selector instead of a repository URL: %s", log)
	}
}

func TestInstallManagedGitRejectsMissingOrEscapingSubdirectory(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	binDir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = clone ]; then mkdir -p "$3"; fi
`
	writeStubScript(t, filepath.Join(binDir, "git"), script)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := "git:https://github.com/acme/tools#subdirectory=plugins%2Freview"
	if err := installManagedGit(cwd, source, false); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing subdirectory error = %v", err)
	}
	checkout, err := gitCheckoutPath(cwd, source, false)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(checkout, "plugins", "review"))
	if err := installManagedGit(cwd, source, false); err == nil || !strings.Contains(err.Error(), "resolves outside checkout") {
		t.Fatalf("escaping subdirectory error = %v", err)
	}
}

func TestConfiguredGitCheckoutInUseBySiblingSubdirectory(t *testing.T) {
	cwd := t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, t.TempDir())
	removed := "git:https://github.com/acme/tools#subdirectory=plugins%2Freview"
	sibling := "git:https://github.com/acme/tools#subdirectory=plugins%2Ftrace"
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: sibling}}); err != nil {
		t.Fatal(err)
	}
	if !configuredGitCheckoutInUse(cwd, sm, removed, false) {
		t.Fatal("sibling Git subdirectory did not preserve the shared checkout")
	}
	if err := sm.SetPackages(nil); err != nil {
		t.Fatal(err)
	}
	if configuredGitCheckoutInUse(cwd, sm, removed, false) {
		t.Fatal("empty settings reported a shared checkout user")
	}
}

func TestRemoveGitSubdirectoryPreservesCheckoutUsedBySibling(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	removed := "git:https://github.com/acme/tools#subdirectory=plugins%2Freview"
	sibling := "git:https://github.com/acme/tools#subdirectory=plugins%2Ftrace"
	checkout, err := gitCheckoutPath(cwd, removed, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, codingagent.AgentDir())
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: sibling}}); err != nil {
		t.Fatal(err)
	}
	if err := removePackageArtifacts(cwd, sm, removed, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Fatalf("shared checkout was removed: %v", err)
	}
	if err := sm.SetPackages(nil); err != nil {
		t.Fatal(err)
	}
	if err := removePackageArtifacts(cwd, sm, removed, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatalf("unused checkout remains: %v", err)
	}
}

func TestAddSourceToSettings_NormalizesLocalPathsRelativeToScopeBase(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	globalPkg := filepath.Join(cwd, "packages", "global-pkg")
	projectPkg := filepath.Join(cwd, "project-pkg")
	for _, dir := range []string{globalPkg, projectPkg} {
		if err := os.MkdirAll(filepath.Join(dir, "extensions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := addSourceToSettings(cwd, sm, "./packages/global-pkg", false); err != nil {
		t.Fatal(err)
	}
	if err := addSourceToSettings(cwd, sm, "./project-pkg", true); err != nil {
		t.Fatal(err)
	}
	globalWant, err := filepath.Rel(agentDir, globalPkg)
	if err != nil {
		t.Fatal(err)
	}
	projectWant, err := filepath.Rel(filepath.Join(cwd, ".pig"), projectPkg)
	if err != nil {
		t.Fatal(err)
	}
	if got := sm.GetGlobalSettings().Packages[0].Source; got != globalWant {
		t.Fatalf("global package source = %q, want %q", got, globalWant)
	}
	if got := sm.GetProjectSettings().Packages[0].Source; got != projectWant {
		t.Fatalf("project package source = %q, want %q", got, projectWant)
	}
}

func TestRemoveSourceFromSettings_MatchesEquivalentLocalPaths(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkgDir := filepath.Join(cwd, "remove-local-pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := addSourceToSettings(cwd, sm, "./remove-local-pkg", false); err != nil {
		t.Fatal(err)
	}
	removed, err := removeSourceFromSettings(cwd, sm, pkgDir+string(filepath.Separator), false)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("expected removal to succeed")
	}
	if got := sm.GetGlobalSettings().Packages; len(got) != 0 {
		t.Fatalf("packages = %#v, want empty", got)
	}
}

func TestUpdatePackages_SuggestsNpmSourcePrefix(t *testing.T) {
	cwd := t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, t.TempDir())
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example"}}); err != nil {
		t.Fatal(err)
	}
	err := updatePackages(cwd, sm, "example", nil)
	if err == nil || err.Error() != "No matching package found for example. Did you mean npm:example?" {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdatePackages_SuggestsGitSourcePrefix(t *testing.T) {
	cwd := t.TempDir()
	sm := codingagent.NewSettingsManager(cwd, t.TempDir())
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: "git:github.com/example/repo"}}); err != nil {
		t.Fatal(err)
	}
	err := updatePackages(cwd, sm, "github.com/example/repo", nil)
	if err == nil || err.Error() != "No matching package found for github.com/example/repo. Did you mean git:github.com/example/repo?" {
		t.Fatalf("err = %v", err)
	}
}

func TestInstalledPathForSource_GlobalRelativeLocalUsesAgentBase(t *testing.T) {
	cwd := t.TempDir()
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	agentDir := filepath.Join(configRoot, "agent")
	pkgDir := filepath.Join(cwd, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stored, _ := filepath.Rel(agentDir, pkgDir)
	if !strings.HasPrefix(stored, ".") {
		stored = "." + string(filepath.Separator) + stored
	}
	if got := installedPathForSource(cwd, stored, false); got != pkgDir {
		t.Fatalf("installedPathForSource() = %q, want %q", got, pkgDir)
	}
}

func TestGetPnpmGlobalPackagePath_UsesConfiguredPnpmCommand(t *testing.T) {
	cwd := t.TempDir()
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	agentDir := filepath.Join(configRoot, "agent")
	pkgDir := filepath.Join(t.TempDir(), "node_modules", "@scope", "pkg")
	stub := filepath.Join(t.TempDir(), "pnpm")
	jsonOut := fmt.Sprintf(`[{"dependencies":{"@scope/pkg":{"path":%q}}}]`, filepath.ToSlash(pkgDir))
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo pnpm\nelif [ \"$1\" = \"list\" ]; then\n  printf '%s\\n' '" + jsonOut + "'\nfi\n"
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}
	if got := getPnpmGlobalPackagePath(cwd, "@scope/pkg"); got != filepath.ToSlash(pkgDir) && got != pkgDir {
		t.Fatalf("getPnpmGlobalPackagePath() = %q, want %q", got, pkgDir)
	}
}

func TestInstallPackageArtifacts_NPMUsesManagedAgentRoot(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)

	stub := filepath.Join(t.TempDir(), "npm")
	script := `#!/bin/sh
root=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "--prefix" ]; then root="$argument"; fi
  previous="$argument"
done
if [ -z "$root" ]; then exit 9; fi
mkdir -p "$root/node_modules/demo"
printf '{"name":"demo"}\n' > "$root/node_modules/demo/package.json"
`
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}
	if err := installPackageArtifacts(cwd, codingagent.PackageSource{Source: "npm:demo@1.0.0"}, false, nil); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(agentDir, "npm", "node_modules", "demo")
	if got := installedPathForSource(cwd, "npm:demo@1.0.0", false); got != want {
		t.Fatalf("installed path = %q, want %q", got, want)
	}
}

func TestPackageManagerContainsNoBranchLayoutMigration(t *testing.T) {
	data, err := os.ReadFile("package_commands.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"legacyPackageInstallDir", "migrateLegacyGitCheckout", "recordPackageLayoutMigration", "package-layout-v1.jsonl"} {
		if strings.Contains(string(data), banned) {
			t.Errorf("package manager retains removed branch migration %q", banned)
		}
	}
}

func TestPigletPackageOriginUsesCoreMaterializerWithoutSettings(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()

	packageRoot := filepath.Join(workspace, "packages", "base")
	extensionDir := filepath.Join(packageRoot, "extensions", "trace")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "index.js"), []byte("export default function extension(pi) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"base"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletDir := filepath.Join(workspace, ".pig", "piglets")
	if err := os.MkdirAll(pigletDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(pigletDir, "research.yaml")
	pigletYAML := `name: research
packages:
  base: local:../../packages/base
extensions:
  - name: trace
    origins: [package:base]
`
	if err := os.WriteFile(pigletPath, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, resolveErrs := piglet.ResolveExtensions(parsed)
	if len(resolveErrs) != 0 || len(resolved) != 1 || resolved[0].Path != canonicalTestPath(t, filepath.Join(extensionDir, "index.js")) {
		t.Fatalf("resolved=%+v errors=%v", resolved, resolveErrs)
	}
	sm := codingagent.NewSettingsManager(workspace, agentDir)
	if len(sm.GetGlobalSettings().Packages) != 0 || len(sm.GetProjectSettings().Packages) != 0 {
		t.Fatalf("Piglet dependency mutated settings: global=%v project=%v", sm.GetGlobalSettings().Packages, sm.GetProjectSettings().Packages)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}

func TestMaterializePackageSourceDoesNotMutateSettings(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)

	stub := filepath.Join(t.TempDir(), "npm")
	script := `#!/bin/sh
root=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "--prefix" ]; then root="$argument"; fi
  previous="$argument"
done
mkdir -p "$root/node_modules/demo"
printf '%s\n' '{"name":"demo","version":"1.2.3"}' > "$root/node_modules/demo/package.json"
printf '%s\n' '{"lockfileVersion":3,"packages":{"node_modules/demo":{"version":"1.2.3","integrity":"sha512-fixture"}}}' > "$root/package-lock.json"
`
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}

	root, err := installresolver.Materialize(cwd, "npm:demo", "user", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(agentDir, "npm", "node_modules", "demo"); root != want {
		t.Fatalf("materialized root = %q, want %q", root, want)
	}
	reloaded := codingagent.NewSettingsManager(cwd, agentDir)
	if len(reloaded.GetGlobalSettings().Packages) != 0 || len(reloaded.GetProjectSettings().Packages) != 0 {
		t.Fatalf("materialization mutated Package settings: global=%v project=%v", reloaded.GetGlobalSettings().Packages, reloaded.GetProjectSettings().Packages)
	}
}

func TestPigletAddNPMUsesCoreMaterializationMetadata(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Chdir(cwd)

	stub := filepath.Join(t.TempDir(), "npm")
	script := `#!/bin/sh
root=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "--prefix" ]; then root="$argument"; fi
  previous="$argument"
done
package="$root/node_modules/remote-piglet"
mkdir -p "$package/config/prompts"
printf '%s\n' '{"name":"remote-piglet","version":"1.4.0","pig":{"piglet":"config/piglet.yaml"}}' > "$package/package.json"
printf '%s\n' 'name: local-npm' 'systemPrompt:' '  file: prompts/system.md' > "$package/config/piglet.yaml"
printf '%s\n' 'from npm' > "$package/config/prompts/system.md"
printf '%s\n' '{"lockfileVersion":3,"packages":{"node_modules/remote-piglet":{"version":"1.4.0","integrity":"sha512-local-fixture"}}}' > "$root/package-lock.json"
`
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}

	const source = "npm:remote-piglet@^1.0.0"
	var stdout, stderr strings.Builder
	code := piglet.RunCommand([]string{"piglet", "add", source, "--no-input"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	originData, err := os.ReadFile(filepath.Join(home, "piglets", "local-npm.origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var origin struct {
		Source          string `json:"source"`
		ResolvedVersion string `json:"resolvedVersion"`
		Integrity       string `json:"integrity"`
	}
	if err := json.Unmarshal(originData, &origin); err != nil {
		t.Fatal(err)
	}
	if origin.Source != source || origin.ResolvedVersion != "1.4.0" || origin.Integrity != "sha512-local-fixture" {
		t.Fatalf("origin = %#v", origin)
	}
	if prompt, err := os.ReadFile(filepath.Join(home, "piglets", "prompts", "system.md")); err != nil || string(prompt) != "from npm\n" {
		t.Fatalf("installed prompt = %q, %v", prompt, err)
	}
}

func TestPigletAddFromLocalBareGitWritesCommitOrigin(t *testing.T) {
	// The host's git config must not rewrite the fixture: Git for Windows
	// sets core.autocrlf=true system-wide.
	emptyGitConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(emptyGitConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", emptyGitConfig)
	work := filepath.Join(t.TempDir(), "work")
	// The checkout nests the bare repository's full path under the agent
	// directory; short roots keep that under Windows' 260-character path
	// limit, which git for Windows enforces by default.
	bare := filepath.Join(shortTempDir(t), "owner", "piglet.git")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmdInDir(work, "git", "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "piglet.yaml"), []byte("name: local-git\nsystemPrompt:\n  file: prompts/system.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "prompts", "system.md"), []byte("from git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmdInDir(work, "git", "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmdInDir(work, "git", "-c", "user.name=Pig Test", "-c", "user.email=pig@example.test", "commit", "-q", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	commit, err := runCmdInDir(work, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runCmdInDir(work, "git", "tag", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd("git", "clone", "--bare", work, bare); err != nil {
		t.Fatal(err)
	}

	home := shortTempDir(t)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Chdir(t.TempDir())
	source := "git:file://localhost/" + strings.TrimPrefix(filepath.ToSlash(bare), "/") + "@v1.0.0"
	var stdout, stderr strings.Builder
	code := piglet.RunCommand([]string{"piglet", "add", source, "--no-input"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	originData, err := os.ReadFile(filepath.Join(home, "piglets", "local-git.origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var origin struct {
		Source          string `json:"source"`
		ResolvedVersion string `json:"resolvedVersion"`
		Commit          string `json:"commit"`
	}
	if err := json.Unmarshal(originData, &origin); err != nil {
		t.Fatal(err)
	}
	if origin.Source != source || origin.ResolvedVersion != "v1.0.0" || origin.Commit != commit {
		t.Fatalf("origin = %#v, commit %q", origin, commit)
	}
	if prompt, err := os.ReadFile(filepath.Join(home, "piglets", "prompts", "system.md")); err != nil || string(prompt) != "from git\n" {
		t.Fatalf("installed prompt = %q, %v", prompt, err)
	}
}

func TestInstallAndPersistCustomRegistryNPMKeepsRegistryIdentity(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	stub := filepath.Join(t.TempDir(), "npm")
	script := `#!/bin/sh
root=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "--prefix" ]; then root="$argument"; fi
  previous="$argument"
done
mkdir -p "$root/node_modules/@acme/private"
`
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}
	source := "npm:@acme/private@1.2.3?registry=https%3A%2F%2Fnpm.example.com%2Fteam"
	if err := installAndPersistPackage(cwd, sm, source, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetGlobalSettings().Packages[0].Source; got != source {
		t.Fatalf("stored source = %q, want %q", got, source)
	}
	ref, err := parseNpmInstallRef(source)
	if err != nil {
		t.Fatal(err)
	}
	want := npmInstallPath(cwd, ref, false)
	if got := installedPathForSource(cwd, source, false); got != want {
		t.Fatalf("installed path = %q, want %q", got, want)
	}
}

func TestInstallAndPersistBareNPMRecordsCanonicalSource(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)

	stub := filepath.Join(t.TempDir(), "npm")
	script := `#!/bin/sh
root=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "--prefix" ]; then root="$argument"; fi
  previous="$argument"
done
mkdir -p "$root/node_modules/demo"
`
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}
	if err := installAndPersistPackage(cwd, sm, "demo", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetGlobalSettings().Packages[0].Source; got != "npm:demo" {
		t.Fatalf("stored source = %q, want npm:demo", got)
	}
	want := filepath.Join(agentDir, "npm", "node_modules", "demo")
	if got := installedPathForSource(cwd, "npm:demo", false); got != want {
		t.Fatalf("installed path = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "marketplace")); !os.IsNotExist(err) {
		t.Fatalf("Package install wrote marketplace state: %v", err)
	}
}

func TestInstallPackageArtifacts_GitUsesManagedAgentRoot(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)

	binDir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "clone" ]; then
  for target in "$@"; do :; done
  mkdir -p "$target"
  exit 0
fi
exit 0
`
	writeStubScript(t, filepath.Join(binDir, "git"), script)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := "git:https://github.com/acme/tools.git"
	if err := installPackageArtifacts(cwd, codingagent.PackageSource{Source: source}, false, nil); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(agentDir, "git", "github.com", "acme", "tools")
	if got := installedPathForSource(cwd, source, false); got != want {
		t.Fatalf("installed path = %q, want %q", got, want)
	}
}

func TestInstalledPathForSource_GlobalNpmUsesManagedAgentPath(t *testing.T) {
	cwd := t.TempDir()
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	agentDir := filepath.Join(configRoot, "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	pkgDir := filepath.Join(agentDir, "npm", "node_modules", "example")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := installedPathForSource(cwd, "npm:example", false); got != pkgDir {
		t.Fatalf("installedPathForSource() = %q, want %q", got, pkgDir)
	}
}

func TestInstalledPathForSource_GlobalNpmFallsBackToPnpmResolvedPath(t *testing.T) {
	cwd := t.TempDir()
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	agentDir := filepath.Join(configRoot, "agent")
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	pkgDir := filepath.Join(t.TempDir(), "pnpm-global", "node_modules", "example")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(t.TempDir(), "pnpm")
	// pnpm list --json reports native paths.
	jsonOut := fmt.Sprintf(`[{"dependencies":{"example":{"path":%q}}}]`, pkgDir)
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo pnpm\nelif [ \"$1\" = \"list\" ]; then\n  printf '%s\\n' '" + jsonOut + "'\nfi\n"
	stub = writeStubScript(t, stub, script)
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{stub}); err != nil {
		t.Fatal(err)
	}
	if got := installedPathForSource(cwd, "npm:example", false); got != pkgDir {
		t.Fatalf("installedPathForSource() = %q, want %q", got, pkgDir)
	}
}

func TestNormalizePackageSourceForSettings_UsesScopeBase(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "work")
	pkg := filepath.Join(tmp, "ext", "sysmon")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, baseDir := range []string{filepath.Join(tmp, "home", ".pig"), filepath.Join(cwd, ".pig")} {
		want, err := filepath.Rel(baseDir, pkg)
		if err != nil {
			t.Fatal(err)
		}
		got := normalizePackageSourceForSettings(baseDir, cwd, pkg)
		if got != want {
			t.Errorf("base %q: stored path = %q, want %q", baseDir, got, want)
		}
	}

	for _, src := range []string{"npm:@foo/bar", "git:github.com/u/r"} {
		if got := normalizePackageSourceForSettings(tmp, cwd, src); got != src {
			t.Fatalf("non-local source %q was rewritten to %q", src, got)
		}
	}
	if got := normalizePackageSourceForSettings(tmp, cwd, "@foo/bar"); got != "npm:@foo/bar" {
		t.Fatalf("bare npm source = %q, want npm:@foo/bar", got)
	}
	custom := "@foo/bar@1?registry=https%3A%2F%2Fnpm.example.com"
	if got := normalizePackageSourceForSettings(tmp, cwd, custom); got != "npm:"+custom {
		t.Fatalf("bare custom-registry npm source = %q, want %q", got, "npm:"+custom)
	}
}

// TestAC1UpdateRoutingMatchesPi proves the Pi 0.84 update routing contract
// (R1/R2): bare/self/pig self-update; a positional non-self source updates one
// package; --extensions updates packages only; --all updates packages then
// self. `pi` is not accepted or advertised as a pig self target. Mirrors
// upstream package-manager-cli.ts updateTarget resolution.
func TestAC1UpdateRoutingMatchesPi(t *testing.T) {
	// Self targets are "self" and the app name; "pi" is upstream's name, not pig's.
	for _, target := range []string{"self", "SELF", "Pig", codingagent.AppName} {
		if !isSelfUpdateTarget(target) {
			t.Fatalf("isSelfUpdateTarget(%q) = false, want true", target)
		}
	}
	for _, target := range []string{"", "pi", "PI", "npm:example", "some-package"} {
		if isSelfUpdateTarget(target) {
			t.Fatalf("isSelfUpdateTarget(%q) = true, want false (pi is not a pig self target)", target)
		}
	}

	// Routing matrix: parsePackageCommand must classify each input shape so
	// runUpdateCommand routes it correctly.
	cases := []struct {
		name           string
		args           []string
		wantSource     string
		wantAll        bool
		wantExtensions bool
	}{
		{"bare self-update", []string{"update"}, "", false, false},
		{"explicit self", []string{"update", "self"}, "self", false, false},
		{"explicit pig", []string{"update", codingagent.AppName}, codingagent.AppName, false, false},
		{"positional package source", []string{"update", "npm:@foo/bar"}, "npm:@foo/bar", false, false},
		{"extensions only", []string{"update", "--extensions"}, "", false, true},
		{"all", []string{"update", "--all"}, "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, ok := parsePackageCommand(tc.args)
			if !ok {
				t.Fatalf("parsePackageCommand(%v) not recognized", tc.args)
			}
			if opts.command != packageUpdate {
				t.Fatalf("command = %q, want update", opts.command)
			}
			if opts.source != tc.wantSource {
				t.Fatalf("source = %q, want %q", opts.source, tc.wantSource)
			}
			if opts.allPackages != tc.wantAll {
				t.Fatalf("allPackages = %v, want %v", opts.allPackages, tc.wantAll)
			}
			if opts.extensionsOnly != tc.wantExtensions {
				t.Fatalf("extensionsOnly = %v, want %v", opts.extensionsOnly, tc.wantExtensions)
			}
		})
	}

	// Help advertises only valid targets: no `pi` self target, no stale URL.
	stdout, _, _ := captureStdoutStderr(t, func() int {
		printPackageCommandHelp(packageUpdate)
		return 0
	})
	if strings.Contains(stdout, "update pi") || strings.Contains(stdout, "pi ") && strings.Contains(stdout, "Update pi only") {
		t.Fatalf("update help advertises pi as a self target:\n%s", stdout)
	}
	if strings.Contains(stdout, "legacy-update-host") {
		t.Fatalf("update help references stale URL:\n%s", stdout)
	}
	for _, want := range []string{"pig update", "--self", "--extensions", "--extension", "--all", "--force"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("update help missing %q:\n%s", want, stdout)
		}
	}
}

func TestAC1UpdateRoutingRejectsConflicts(t *testing.T) {
	cases := [][]string{
		{"update", "--all", "npm:@foo/bar"},
		{"update", "npm:@foo/bar", "--extensions"},
		{"update", "npm:@foo/bar", "--self"},
		{"update", "--extension", "npm:@foo/bar", "--extensions"},
	}
	for _, args := range cases {
		opts, ok := parsePackageCommand(args)
		if !ok {
			t.Fatalf("parsePackageCommand(%v) not recognized", args)
		}
		if opts.conflict == "" {
			t.Fatalf("parsePackageCommand(%v) accepted conflicting update target", args)
		}
	}
}

func TestAC1UpdateRoutingCombinedSelfAndExtensionsMeansAll(t *testing.T) {
	for _, args := range [][]string{
		{"update", "--self", "--extensions"},
		{"update", "self", "--extensions"},
		{"update", "pig", "--extensions"},
	} {
		opts, ok := parsePackageCommand(args)
		if !ok || !opts.allPackages || opts.extensionsOnly || opts.source != "" {
			t.Fatalf("parsePackageCommand(%v) = %#v, want all", args, opts)
		}
	}
}

func TestAC1UpdateExtensionAndForceFlags(t *testing.T) {
	opts, ok := parsePackageCommand([]string{"update", "--extension", "npm:@foo/bar"})
	if !ok || opts.source != "npm:@foo/bar" || opts.conflict != "" {
		t.Fatalf("--extension parse = %#v", opts)
	}
	forced, ok := parsePackageCommand([]string{"update", "--self", "--force"})
	if !ok || !forced.selfOnly || !forced.force || forced.conflict != "" {
		t.Fatalf("--self --force parse = %#v", forced)
	}
	missing, ok := parsePackageCommand([]string{"update", "--extension"})
	if !ok || missing.missingValue != "--extension" {
		t.Fatalf("missing --extension value parse = %#v", missing)
	}
}

// shortTempDir is a temporary directory whose name, unlike t.TempDir's, does
// not repeat the test name.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pig-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove %s: %v", dir, err)
		}
	})
	return dir
}
