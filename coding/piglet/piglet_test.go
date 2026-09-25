package piglet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestParseRejectsPigletFormatVersion(t *testing.T) {
	_, err := ParseBytes([]byte("version: 1\nname: x\n"))
	if err == nil || !strings.Contains(err.Error(), "field version not found") {
		t.Fatalf("ParseBytes() error = %v, want removed top-level version rejection", err)
	}
}

func TestParseRejectsUnsafePigletName(t *testing.T) {
	for _, name := range []string{"../escape", "with space", "/absolute", "-leading"} {
		yaml := "name: " + fmt.Sprintf("%q", name) + "\n"
		if _, err := ParseBytes([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "piglet name") {
			t.Fatalf("name %q error = %v", name, err)
		}
	}
}

func TestParseRejectsDuplicateResourceNames(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"extensions", "extensions:\n  - trace\n  - trace\n"},
		{"skills", "skills:\n  - name: review\n    content: one\n  - name: review\n    content: two\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := "name: duplicate\n" + tc.yaml
			if _, err := ParseBytes([]byte(data)); err == nil || !strings.Contains(err.Error(), "duplicate name") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseBuildSpec(t *testing.T) {
	pigletYAML := `
name: release
build:
  targets: [darwin/arm64, linux/amd64]
  outputName: pig-release
  extensionRealization: fused
`
	parsed, err := ParseBytes([]byte(pigletYAML))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Build == nil || len(parsed.Build.Targets) != 2 || parsed.Build.OutputName != "pig-release" || parsed.Build.ExtensionRealization != "fused" {
		t.Fatalf("build = %#v", parsed.Build)
	}
}

// TestAC2ReleaseAndBuildShape: release SemVer lives in release.version, kept
// separate from portable build defaults; the transitional build fields
// (tier, strict, updateUrl, version) are rejected, and an invalid release
// SemVer fails.
func TestAC2ReleaseAndBuildShape(t *testing.T) {
	parsed, err := ParseBytes([]byte("name: release\nrelease:\n  version: 1.2.0\nbuild:\n  targets: [linux/amd64]\n  outputName: pig-release\n"))
	if err != nil {
		t.Fatalf("parse release.version: %v", err)
	}
	if parsed.Release == nil || parsed.Release.Version != "1.2.0" {
		t.Fatalf("release = %#v", parsed.Release)
	}
	for _, removed := range []string{"tier: fuse", "strict: true", "updateUrl: https://example.test/u.json", "version: 1.2.0"} {
		if _, err := ParseBytes([]byte("name: release\nbuild:\n  " + removed + "\n")); err == nil || !strings.Contains(err.Error(), "not found in type") {
			t.Fatalf("build.%q error = %v, want removed-field rejection", removed, err)
		}
	}
	if _, err := ParseBytes([]byte("name: release\nrelease:\n  version: not-semver\n")); err == nil || !strings.Contains(err.Error(), "release.version") {
		t.Fatalf("release.version error = %v, want SemVer rejection", err)
	}
}

func TestParseBuildSpecRejectsInvalidDefaults(t *testing.T) {
	cases := []struct {
		name  string
		build string
		want  string
	}{
		{"target", "targets: [linux]", "must be os/arch"},
		{"duplicate target", "targets: [linux/amd64, linux/amd64]", "duplicates"},
		{"output path", "outputName: dist/pig", "binary basename"},
		{"extension realization", "extensionRealization: subprocess", "must be fused"},
		{"unknown field", "builder: docker", "field builder not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pigletYAML := "name: release\nbuild:\n  " + tc.build + "\n"
			if _, err := ParseBytes([]byte(pigletYAML)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPigletAgentEnvironmentSchemaAndEffectiveDefaults(t *testing.T) {
	for _, document := range []string{
		"name: image\nagentEnv:\n  image: ghcr.io/acme/dev:1\n",
		"name: dev\nagentEnv:\n  devContainer: .devcontainer/devcontainer.json\n",
		"name: source\nagentEnv:\n  source: npm:@acme/dev@1\n",
	} {
		parsed, err := ParseBytes([]byte(document))
		if err != nil {
			t.Fatalf("ParseBytes(%s): %v", document, err)
		}
		effective := parsed.EffectiveAgentEnvironment()
		if effective == nil || effective.PigRuntime.Mode != "inject" || effective.PigRuntime.Version != "latest" || effective.Policy.Preset != "standard" {
			t.Fatalf("effective agent environment = %#v", effective)
		}
		if parsed.AgentEnv.PigRuntime != nil || parsed.AgentEnv.Policy != nil {
			t.Fatalf("effective defaults mutated source: %#v", parsed.AgentEnv)
		}
		if err := parsed.ValidateAgentEnvironmentRuntime(); err == nil || !strings.Contains(err.Error(), "host fallback is forbidden") {
			t.Fatalf("runtime availability error = %v", err)
		}
	}
	if parsed, err := ParseBytes([]byte("name: host\n")); err != nil || parsed.EffectiveAgentEnvironment() != nil {
		t.Fatalf("host Piglet = %#v, %v", parsed, err)
	}
}

func TestPigletAgentEnvironmentRejectsInvalidForms(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{name: "empty", env: "{}", want: "exactly one"},
		{name: "multiple", env: "{image: acme/dev:1, devContainer: .devcontainer/devcontainer.json}", want: "exactly one"},
		{name: "absolute devcontainer", env: "{devContainer: /tmp/devcontainer.json}", want: "relative workspace: path"},
		{name: "escaping devcontainer", env: "{devContainer: ../devcontainer.json}", want: "relative workspace: path"},
		{name: "runtime mode", env: "{image: acme/dev:1, pigRuntime: {mode: host}}", want: "inject or image"},
		{name: "policy preset", env: "{image: acme/dev:1, policy: {preset: unrestricted}}", want: "minimal, standard, or elevated"},
		{name: "unknown runtime field", env: "{image: acme/dev:1, pigRuntime: {engine: docker}}", want: "field engine not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes([]byte("name: invalid\nagentEnv: " + tc.env + "\n"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEffectivePigletValidatesWorkspaceDevContainerClosure(t *testing.T) {
	root := t.TempDir()
	pigletDir := filepath.Join(root, "piglets")
	workspace := filepath.Join(root, "workspace")
	pigletPath := filepath.Join(pigletDir, "piglet.yaml")
	if err := os.MkdirAll(pigletDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	devContainerPath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	if err := os.WriteFile(devContainerPath, []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	data := []byte("name: dev\nagentEnv:\n  devContainer: workspace:.devcontainer/devcontainer.json\n")
	if err := os.WriteFile(pigletPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Source inspection is portable and does not invent a Piglet-directory
	// workspace.
	if _, err := Parse(pigletPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEffective(pigletPath); err == nil || !strings.Contains(err.Error(), "requires a workspace anchor") {
		t.Fatalf("missing workspace error = %v", err)
	}
	if _, err := ResolveEffectiveWithOptions(pigletPath, ResolveOptions{Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(devContainerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEffectiveWithOptions(pigletPath, ResolveOptions{Workspace: workspace}); err == nil || !strings.Contains(err.Error(), "devcontainer.json") {
		t.Fatalf("missing closure error = %v", err)
	}
}

func TestParse_ToolAllowlists(t *testing.T) {
	piglet, err := ParseBytes([]byte(`name: tool-lists
tools: [read, grep]
extensions:
  - name: manager
    tools: [delegate, get_messages]
  - name: hidden
    tools: []
`))
	if err != nil {
		t.Fatal(err)
	}
	if piglet.BuiltinTools == nil || !slices.Equal(*piglet.BuiltinTools, []string{"read", "grep"}) {
		t.Fatalf("built-in tools = %#v", piglet.BuiltinTools)
	}
	if piglet.Extensions[0].Tools == nil || !slices.Equal(*piglet.Extensions[0].Tools, []string{"delegate", "get_messages"}) {
		t.Fatalf("manager tools = %#v", piglet.Extensions[0].Tools)
	}
	if piglet.Extensions[1].Tools == nil || len(*piglet.Extensions[1].Tools) != 0 {
		t.Fatalf("hidden tools = %#v", piglet.Extensions[1].Tools)
	}
}

func TestParse_OmittedToolAllowlistsMeanAll(t *testing.T) {
	piglet, err := ParseBytes([]byte("name: defaults\nextensions: [manager]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if piglet.BuiltinTools != nil || piglet.Extensions[0].Tools != nil {
		t.Fatalf("Piglet = %#v", piglet)
	}
}

func TestParse_RejectsRemovedCapabilityShapes(t *testing.T) {
	for name, source := range map[string]string{
		"builtin":            "name: x\nbuiltin: none\n",
		"extension command":  "name: x\nextensions:\n  - name: e\n    commands: []\n",
		"extension shortcut": "name: x\nextensions:\n  - name: e\n    shortcuts: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBytes([]byte(source)); err == nil || !strings.Contains(err.Error(), "field") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParse_RejectsInvalidToolAllowlists(t *testing.T) {
	for name, source := range map[string]string{
		"unknown built-in":     "name: x\ntools: [not-a-tool]\n",
		"duplicate built-in":   "name: x\ntools: [read, read]\n",
		"duplicate extension":  "name: x\nextensions:\n  - name: e\n    tools: [one, one]\n",
		"empty extension name": "name: x\nextensions:\n  - name: e\n    tools: [\"\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBytes([]byte(source)); err == nil {
				t.Fatal("invalid tool allowlist accepted")
			}
		})
	}
}

func TestParse_InvalidVersion(t *testing.T) {
	yaml := `
version: 2
name: bad-version
`
	_, err := ParseBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for version 2, got nil")
	}
}

func TestParse_ExtendsStringShapeRejected(t *testing.T) {
	yaml := `
name: extends-test
extends: base-piglet
`
	_, err := ParseBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for removed string extends shape, got nil")
	}
}

func TestScopeToolsUsesExactAllowlists(t *testing.T) {
	builtins := []string{"read"}
	manager := []string{"delegate"}
	mcp := []string{"github/search"}
	piglet := &Piglet{
		BuiltinTools: &builtins,
		Extensions: []ExtensionEntry{
			{Name: "manager", Tools: &manager},
			{Name: MCPAdapterExtensionName, Tools: &mcp},
		},
	}
	all := []ToolInfo{
		{Name: "read", Source: "builtin"},
		{Name: "bash", Source: "builtin"},
		{Name: "delegate", Source: "manager"},
		{Name: "delete", Source: "manager"},
		{Name: "github/search", Source: "mcp:github"},
		{Name: "github/write", Source: "mcp:github"},
		{Name: "ambient", Source: "workspace-extension"},
	}
	want := []string{"read", "delegate", "github/search", "ambient"}
	if got := ScopeTools(piglet, all); !slices.Equal(got, want) {
		t.Fatalf("ScopeTools = %v, want %v", got, want)
	}
}

func TestScopeToolsEmptyListsExposeNoOwnedTools(t *testing.T) {
	none := []string{}
	piglet := &Piglet{
		BuiltinTools: &none,
		Extensions: []ExtensionEntry{
			{Name: "manager", Tools: &none},
			{Name: MCPAdapterExtensionName, Tools: &none},
		},
	}
	all := []ToolInfo{
		{Name: "read", Source: "builtin"},
		{Name: "delegate", Source: "manager"},
		{Name: "github/search", Source: "mcp:github"},
	}
	if got := ScopeTools(piglet, all); len(got) != 0 {
		t.Fatalf("ScopeTools = %v", got)
	}
}

func TestScopeToolsOmittedListsExposeRegisteredTools(t *testing.T) {
	piglet := &Piglet{Extensions: []ExtensionEntry{{Name: "manager"}, {Name: MCPAdapterExtensionName}}}
	all := []ToolInfo{
		{Name: "read", Source: "builtin"},
		{Name: "delegate", Source: "manager"},
		{Name: "github/search", Source: "mcp:github"},
	}
	if got := ScopeTools(piglet, all); !slices.Equal(got, []string{"read", "delegate", "github/search"}) {
		t.Fatalf("ScopeTools = %v", got)
	}
}

func TestResolve_ExplicitPath(t *testing.T) {
	// Create a temp piglet file
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	_ = os.WriteFile(path, []byte("name: test\n"), 0o644)

	resolved, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", path, err)
	}
	if resolved != path {
		t.Errorf("Resolve = %q, want %q", resolved, path)
	}
}

func TestResolve_EnvPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env-piglet.yaml")
	_ = os.WriteFile(path, []byte("name: env-test\n"), 0o644)

	t.Setenv("PIG_PIGLET_PATH", path)
	t.Setenv("PIG_PIGLET_NAME", "")

	resolved, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != path {
		t.Errorf("Resolve = %q, want %q", resolved, path)
	}
}

func TestResolve_NotFound(t *testing.T) {
	t.Setenv("PIG_PIGLET_PATH", "")
	t.Setenv("PIG_PIGLET_NAME", "")

	_, err := Resolve("nonexistent-piglet")
	if err == nil {
		t.Fatal("expected error for missing piglet, got nil")
	}
}

func TestParse_RejectsInlineMCPAndAdapterConfig(t *testing.T) {
	for name, source := range map[string]string{
		"inline MCP":       "name: x\nmcpServers:\n  demo:\n    command: demo\n",
		"MCP config paths": "name: x\nmcpConfigPaths: [mcp.json]\n",
		"agent prompts":    "name: x\nagentPrompts:\n  - text: extra\n",
		"harness config":   "name: x\ndiscovery:\n  harness:\n    pig: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBytes([]byte(source)); err == nil || !strings.Contains(err.Error(), "field") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParse_DiscoverySourceLists(t *testing.T) {
	piglet, err := ParseBytes([]byte(`name: discovery
discovery:
  extensions: [workspace, user]
  skills: [workspace]
`))
	if err != nil {
		t.Fatal(err)
	}
	if piglet.Discovery == nil || !slices.Equal(piglet.Discovery.Extensions, []string{"workspace", "user"}) || !slices.Equal(piglet.Discovery.Skills, []string{"workspace"}) {
		t.Fatalf("discovery = %#v", piglet.Discovery)
	}
	for _, source := range []string{
		"name: x\ndiscovery:\n  extensions: [project]\n",
		"name: x\ndiscovery:\n  skills: [user, user]\n",
	} {
		if _, err := ParseBytes([]byte(source)); err == nil {
			t.Fatal("invalid discovery accepted")
		}
	}
}

func TestParse_Skills(t *testing.T) {
	yml := `
name: skills-test
skills:
  - name: commit
    origins: [local:./skills/commit]
  - name: github
    origins: [local:./skills/github]
  - name: write-todos
    origins: [local:./skills/write-todos]
`
	p, err := ParseBytes([]byte(yml))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(p.Skills) != 3 {
		t.Errorf("Skills = %d items, want 3", len(p.Skills))
	}
	if p.Skills[0].Name != "commit" {
		t.Errorf("Skills[0].Name = %q, want commit", p.Skills[0].Name)
	}
	if len(p.Skills[0].Origins) != 1 || p.Skills[0].Origins[0] == "" {
		t.Error("Skills[0] missing typed origin")
	}
}

func TestParse_SkillsStringShorthand(t *testing.T) {
	yml := `
name: skills-short
skills:
  - commit
  - github
`
	p, err := ParseBytes([]byte(yml))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(p.Skills) != 2 {
		t.Errorf("Skills = %d items, want 2", len(p.Skills))
	}
	if p.Skills[0].Name != "commit" {
		t.Errorf("Skills[0].Name = %q, want commit", p.Skills[0].Name)
	}
}

func TestParse_Model(t *testing.T) {
	yaml := `
name: model-test
model:
  provider: example
  name: copilot-claude-sonnet-4.5
  contextWindow: 200000
  thinking: medium
`
	p, err := ParseBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if p.Model.Provider != "example" {
		t.Errorf("Model.Provider = %q", p.Model.Provider)
	}
	if p.Model.ContextWindow != 200000 {
		t.Errorf("Model.ContextWindow = %d", p.Model.ContextWindow)
	}
}

func TestRunCommand_NotPiglet(t *testing.T) {
	// Args that don't start with "piglet" should return -1 (not our command).
	if code := RunCommand([]string{"docs"}, os.Stdout, os.Stderr); code != -1 {
		t.Errorf("RunCommand(docs) = %d, want -1", code)
	}
	if code := RunCommand(nil, os.Stdout, os.Stderr); code != -1 {
		t.Errorf("RunCommand(nil) = %d, want -1", code)
	}
}

func TestRunCommand_Help(t *testing.T) {
	var buf strings.Builder
	code := RunCommand([]string{"piglet"}, &buf, os.Stderr)
	if code != 0 {
		t.Errorf("RunCommand(piglet) = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "pig piglet list") {
		t.Errorf("help output missing 'pig piglet list': %s", buf.String())
	}
}

func TestRunCommand_List(t *testing.T) {
	// Create a temp piglet
	dir := t.TempDir()
	pigletDir := filepath.Join(dir, ".pig", "piglets")
	_ = os.MkdirAll(pigletDir, 0o755)
	_ = os.WriteFile(filepath.Join(pigletDir, "test.yaml"), []byte("name: test\ndescription: Test piglet\n"), 0o644)

	// Point the config root at the temp dir so List() discovers it. PIG_HOME
	// takes precedence over XDG_CONFIG_HOME, which CI runners set.
	t.Setenv("PIG_HOME", filepath.Join(dir, ".pig"))

	var buf strings.Builder
	code := RunCommand([]string{"piglet", "list"}, &buf, os.Stderr)
	if code != 0 {
		t.Errorf("RunCommand(piglet list) = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "test") {
		t.Errorf("list output missing 'test': %s", buf.String())
	}
}

func TestRunCommand_Show(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := t.TempDir()
	yamlContent := "name: show-test\ndescription: Show test\nextensions:\n  - web-search\ntools: [read]\n"
	path := filepath.Join(dir, "show-test.yaml")
	_ = os.WriteFile(path, []byte(yamlContent), 0o644)

	var buf strings.Builder
	code := RunCommand([]string{"piglet", "show", path}, &buf, os.Stderr)
	if code != 0 {
		t.Errorf("RunCommand(piglet show) = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "show-test") {
		t.Errorf("show output missing 'show-test': %s", buf.String())
	}
}

func TestRunCommand_ShowPackageOrigins(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := t.TempDir()
	yamlContent := `name: show-package
packages:
  base: npm:@acme/base@1.0.0
extensions:
  - name: trace
    origins: [package:base]
skills:
  - name: review
    origins: [local:./skills/review]
`
	path := filepath.Join(dir, "show-package.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	code := RunCommand([]string{"piglet", "show", path}, &buf, os.Stderr)
	if code != 0 {
		t.Fatalf("show code = %d", code)
	}
	for _, want := range []string{"Packages:", "base  source=npm:@acme/base@1.0.0", "origin=package:base", "origin=local:./skills/review"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("show output missing %q:\n%s", want, buf.String())
		}
	}
}

func TestRunCommandAddCopiesSystemPromptFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	sourceDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "prompts", "system.md"), []byte("system.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "complete.yaml")
	pigletYAML := "name: complete\nsystemPrompt:\n  file: prompts/system.md\n"
	if err := os.WriteFile(source, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "add", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "prompts", "system.md")); err != nil {
		t.Fatalf("missing copied prompt: %v", err)
	}
}

func TestRunCommandAddRefusesDifferentExistingPiglet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	pigletsDir := filepath.Join(home, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(pigletsDir, "same.yaml")
	original := []byte("name: same\ndescription: original\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "same.yaml")
	if err := os.WriteFile(source, []byte("name: same\ndescription: replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", source}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "different content") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing Piglet was overwritten: %s", got)
	}
}

func TestRunCommandAddRejectsUnresolvedOriginBeforeWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "broken.yaml")
	pigletYAML := "name: broken\nskills:\n  - name: missing\n    origins: [local:./missing-skill]\n"
	if err := os.WriteFile(source, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", source}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "missing") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "broken.yaml")); !os.IsNotExist(err) {
		t.Fatalf("invalid Piglet was added: %v", err)
	}
}

func TestRunCommandAddRejectsSymlinkedPromptBeforeWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	parent := t.TempDir()
	sourceDir := filepath.Join(parent, "piglets")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "prompt.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(sourceDir, "prompts"))
	source := filepath.Join(sourceDir, "broken.yaml")
	if err := os.WriteFile(source, []byte("name: broken\nsystemPrompt:\n  file: prompts/prompt.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", source}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "resolves outside") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "broken.yaml")); !os.IsNotExist(err) {
		t.Fatalf("invalid Piglet was added: %v", err)
	}
}

func TestRunCommandAddRejectsEscapingPromptBeforeWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	sourceDir := filepath.Join(t.TempDir(), "piglets")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(sourceDir), "prompt.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceDir, "broken.yaml")
	pigletYAML := "name: broken\nsystemPrompt:\n  file: ../prompt.md\n"
	if err := os.WriteFile(source, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", source}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "escapes the Piglet source directory") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "broken.yaml")); !os.IsNotExist(err) {
		t.Fatalf("invalid Piglet was added: %v", err)
	}
}

func TestRunCommand_Add_SingleFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_HOME", dir)

	// Create a piglet source file.
	srcDir := t.TempDir()
	pigletYAML := `
name: test-add
description: "add test"
`
	srcFile := filepath.Join(srcDir, "test-add.yaml")
	_ = os.WriteFile(srcFile, []byte(pigletYAML), 0o644)

	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", srcFile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, stderr.String())
	}

	// Verify it was copied to ~/.pig/piglets/test-add.yaml
	added := filepath.Join(dir, "piglets", "test-add.yaml")
	if _, err := os.Stat(added); err != nil {
		t.Errorf("added piglet not found: %v", err)
	}
	if !strings.Contains(stdout.String(), "Added test-add") {
		t.Errorf("stdout missing add confirmation: %s", stdout.String())
	}
}

func TestRunCommand_Add_NoArgs(t *testing.T) {
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit 2, got %d", code)
	}
}

func TestParse_RejectsUnknownTopLevelKey(t *testing.T) {
	yaml := `
name: bad-top
foobar: oops
`
	_, err := ParseBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown top-level key 'foobar', got nil")
	}
	if !strings.Contains(err.Error(), "foobar") {
		t.Errorf("error should mention 'foobar': %v", err)
	}
}

func TestParse_RejectsUnknownExtensionKey(t *testing.T) {
	yaml := `
name: bad-ext
extensions:
  - name: test
    bogus: true
`
	_, err := ParseBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown extension key 'bogus', got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention 'bogus': %v", err)
	}
}

func TestParse_RejectsUnknownSkillKey(t *testing.T) {
	yaml := `
name: bad-skill
skills:
  - name: test
    priority: high
`
	_, err := ParseBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown skill key 'priority', got nil")
	}
	if !strings.Contains(err.Error(), "priority") {
		t.Errorf("error should mention 'priority': %v", err)
	}
}

func TestParse_RejectsUnknownDiscoveryKey(t *testing.T) {
	yaml := `
name: bad-discovery
discovery:
  workspaceExtensions: true
  magicMode: yes
`
	_, err := ParseBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown discovery key 'magicMode', got nil")
	}
	if !strings.Contains(err.Error(), "magicMode") {
		t.Errorf("error should mention 'magicMode': %v", err)
	}
}

// ── Slash command tests ──────────────────────────────────────────────────────

type pigletNotifyUI struct {
	extension.UIContext
	notified string
}

func (u *pigletNotifyUI) Notify(message, _ string) { u.notified = message }

func TestBuildExtensionRegistersReadOnlyPigletCommand(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPiglet(&Piglet{Name: "research"})}, t.TempDir())
	if _, exists := runner.Command("piglets"); exists {
		t.Fatal("mutable /piglets command must not be registered")
	}
	command, exists := runner.Command("piglet")
	if !exists {
		t.Fatal("read-only /piglet command not registered")
	}
	ui := &pigletNotifyUI{UIContext: extension.NoopUIContext}
	runner.SetUIContext(ui)
	if err := command.Handler(runner.DispatchContext(context.Background()), ""); err != nil {
		t.Fatal(err)
	}
	if ui.notified != "Piglet: research" {
		t.Fatalf("/piglet output = %q", ui.notified)
	}
}

func TestBuildExtensionPigletCommandRejectsMutation(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{BuildExtensionWithPiglet(&Piglet{Name: "research"})}, t.TempDir())
	command, exists := runner.Command("piglet")
	if !exists {
		t.Fatal("read-only /piglet command not registered")
	}
	ui := &pigletNotifyUI{UIContext: extension.NoopUIContext}
	runner.SetUIContext(ui)
	if err := command.Handler(runner.DispatchContext(context.Background()), "switch other"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ui.notified, "read-only") || !strings.Contains(ui.notified, "separate invocation") {
		t.Fatalf("notification = %q", ui.notified)
	}
}
