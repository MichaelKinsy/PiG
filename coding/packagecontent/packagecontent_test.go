package packagecontent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

func TestDiscoverPigPackageMembers(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{
  "name": "pkg",
  "pig": {
    "hooks": ["hooks/validate.json"],
    "mcpServers": ["mcp/source-control.json"],
    "agentEnvironments": [".devcontainer/go/devcontainer.json"]
  }
}`)
	writeTestFile(t, filepath.Join(root, "hooks", "validate.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	writeTestFile(t, filepath.Join(root, "mcp", "source-control.json"), `{"command":"true"}`)
	writeTestFile(t, filepath.Join(root, ".devcontainer", "go", "devcontainer.json"), `{
  // URLs must survive JSONC comment removal.
  "name": "go-development",
  "image": "registry.example/image:latest",
}`)

	resources, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.HookFiles, filepath.Join(root, "hooks", "validate.json"))
	assertContains(t, resources.MCPFiles, filepath.Join(root, "mcp", "source-control.json"))
	assertContains(t, resources.AgentEnvironments, filepath.Join(root, ".devcontainer", "go", "devcontainer.json"))
	if got, err := FindMember(resources, AgentEnvironments, "go-development"); err != nil || got == "" {
		t.Fatalf("agent environment = %q, %v", got, err)
	}
}

func TestValidateRejectsUnsupportedHookDeclaration(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"hooks":["hooks/hooks.json"]}}`)
	writeTestFile(t, filepath.Join(root, "hooks", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	_, err := ValidatePackage(root)
	if err == nil || !strings.Contains(err.Error(), "PreToolUse is not supported") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsMissingDeclaredResource(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"hooks":["hooks/missing.json"]}}`)
	_, err := ValidatePackage(root)
	if err == nil || !strings.Contains(err.Error(), "hooks/missing.json") || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v", err)
	}
}

func TestDiscoverMalformedPiResourceKeepsValidSibling(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"bad-package","pi":{"skills":"./skills","prompts":["./prompts"]}}`)
	writeTestFile(t, filepath.Join(root, "skills", "bad", "SKILL.md"), "# must not load\n")
	writeTestFile(t, filepath.Join(root, "prompts", "valid.md"), "Valid prompt\n")

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, resources.SkillDirs, filepath.Join(root, "skills", "bad"))
	assertContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "valid.md"))
}

func TestValidatePackageRequiresManifestAndName(t *testing.T) {
	root := t.TempDir()
	if _, err := ValidatePackage(root); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing manifest error = %v", err)
	}
	writeTestFile(t, filepath.Join(root, "package.json"), `{"pig":{}}`)
	if _, err := ValidatePackage(root); err == nil || !strings.Contains(err.Error(), "non-empty name") {
		t.Fatalf("missing name error = %v", err)
	}
}

func TestValidateRejectsSymlinkedManifest(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "package")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside-package.json")
	writeTestFile(t, outside, `{"name":"outside"}`)
	testenv.Symlink(t, outside, filepath.Join(root, "package.json"))
	_, err := ValidatePackage(root)
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "package")
	outside := filepath.Join(parent, "outside")
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"hooks":["hooks/escape/hook.json"]}}`)
	writeTestFile(t, filepath.Join(outside, "hook.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(root, "hooks", "escape"))
	_, err := ValidatePackage(root)
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsSymlinkedBuildContextEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "package")
	outside := filepath.Join(parent, "outside")
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"agentEnvironments":[".devcontainer/devcontainer.json"]}}`)
	writeTestFile(t, filepath.Join(root, ".devcontainer", "devcontainer.json"), `{"build":{"context":"context","dockerfile":"Dockerfile"}}`)
	writeTestFile(t, filepath.Join(outside, "Dockerfile"), "FROM scratch\n")
	testenv.Symlink(t, outside, filepath.Join(root, ".devcontainer", "context"))
	_, err := ValidatePackage(root)
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsRelativeBindAndMissingComposeService(t *testing.T) {
	cases := []struct {
		name       string
		definition string
		compose    string
		want       string
	}{
		{
			name:       "relative bind escape",
			definition: `{"image":"alpine:3.20","mounts":[{"type":"bind","source":"../../outside","target":"/workspace/out"}]}`,
			want:       "bind source",
		},
		{
			name:       "compose relative bind escape",
			definition: `{"dockerComposeFile":"compose.yaml","service":"workspace"}`,
			compose:    "services:\n  workspace:\n    image: alpine:3.20\n    volumes:\n      - type: bind\n        source: ../../outside\n        target: /workspace/out\n",
			want:       "bind source",
		},
		{
			name:       "missing compose service",
			definition: `{"dockerComposeFile":"compose.yaml","service":"missing"}`,
			compose:    "services:\n  workspace:\n    image: alpine:3.20\n",
			want:       `missing Compose service "missing"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"agentEnvironments":[".devcontainer/devcontainer.json"]}}`)
			writeTestFile(t, filepath.Join(root, ".devcontainer", "devcontainer.json"), tc.definition)
			if tc.compose != "" {
				writeTestFile(t, filepath.Join(root, ".devcontainer", "compose.yaml"), tc.compose)
			}
			_, err := ValidatePackage(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRootAgentEnvironmentUsesPackageName(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"@acme/dev","pig":{"agentEnvironments":[".devcontainer/devcontainer.json"]}}`)
	writeTestFile(t, filepath.Join(root, ".devcontainer", "devcontainer.json"), `{"image":"alpine:3.20"}`)
	resources, err := ValidatePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := FindMember(resources, AgentEnvironments, "@acme/dev"); err != nil || got == "" {
		t.Fatalf("agent environment = %q, %v", got, err)
	}
}

func TestValidateRejectsManifestResourceOutsidePackage(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "package")
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"hooks":["../outside.json"]}}`)
	writeTestFile(t, filepath.Join(parent, "outside.json"), `{}`)
	_, err := Validate(root)
	if err == nil || !strings.Contains(err.Error(), "escapes package root") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateDevContainerClosure(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"agentEnvironments":[".devcontainer/devcontainer.json"]}}`)
	writeTestFile(t, filepath.Join(root, ".devcontainer", "devcontainer.json"), `{
  "name": "compose",
  "dockerComposeFile": "compose.yaml",
  "service": "workspace"
}`)
	writeTestFile(t, filepath.Join(root, ".devcontainer", "compose.yaml"), `services:
  workspace:
    build:
      context: ..
      dockerfile: Dockerfile
    env_file: dev.env
    volumes:
      - ../scripts:/workspace/scripts
`)
	writeTestFile(t, filepath.Join(root, "Dockerfile"), "FROM scratch\n")
	writeTestFile(t, filepath.Join(root, ".devcontainer", "dev.env"), "DEMO=1\n")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(root); err != nil {
		t.Fatalf("valid closure rejected: %v", err)
	}
}

func TestValidateRejectsNonPortableDevContainer(t *testing.T) {
	cases := []struct {
		name       string
		definition string
		want       string
		notExist   bool
	}{
		{name: "escaping context", definition: `{"build":{"context":"../../outside"}}`, want: "escapes package root"},
		{name: "absolute mount", definition: `{"image":"alpine:3.20","mounts":["type=bind,source=/Users/example,target=/workspace"]}`, want: "not portable"},
		{name: "windows absolute mount", definition: `{"image":"alpine:3.20","mounts":["type=bind,source=C:\\\\Users\\\\example,target=/workspace"]}`, want: "not portable"},
		{name: "invalid image URL", definition: `{"image":"https://registry.example/image:latest"}`, want: "must be an OCI image reference"},
		{name: "mount missing type", definition: `{"image":"alpine:3.20","mounts":[{"source":"./workspace","target":"/workspace"}]}`, want: "requires an explicit type"},
		{name: "missing dockerfile", definition: `{"build":{"dockerfile":"Dockerfile"}}`, want: "build.dockerfile", notExist: true},
		{name: "unterminated comment", definition: `{"name":"bad" /*`, want: "unterminated block comment"},
		{name: "feature requires full semantics", definition: `{"image":"alpine:3.20","features":{"ghcr.io/devcontainers/features/go:1":{}}}`, want: "uses unsupported features"},
		{name: "lifecycle requires full semantics", definition: `{"image":"alpine:3.20","postCreateCommand":"./setup.sh"}`, want: "uses unsupported lifecycle command postCreateCommand"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pig":{"agentEnvironments":[".devcontainer/devcontainer.json"]}}`)
			writeTestFile(t, filepath.Join(root, ".devcontainer", "devcontainer.json"), tc.definition)
			_, err := Validate(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) || tc.notExist && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFindMemberUsesPathAndSkillIdentity(t *testing.T) {
	root := t.TempDir()
	extensionDir := filepath.Join(root, "extensions", "trace")
	writeTestFile(t, filepath.Join(extensionDir, "index.js"), "export default function extension(pi) {}\n")
	skillDir := filepath.Join(root, "skills", "directory-name")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: review\ndescription: Review code\n---\n# Review\n")

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := FindMember(resources, Extensions, "trace"); err != nil || got != filepath.Join(extensionDir, "index.js") {
		t.Fatalf("extension member = %q, %v", got, err)
	}
	if got, err := FindMember(resources, Skills, "review"); err != nil || got != skillDir {
		t.Fatalf("skill member = %q, %v", got, err)
	}
	if got, err := FindSourceMember(skillDir, Skills, "review"); err != nil || got != skillDir {
		t.Fatalf("direct skill member = %q, %v", got, err)
	}
	if _, err := FindMember(resources, Skills, "missing"); err == nil {
		t.Fatal("missing skill resolved")
	}
}

func TestFindMemberRejectsAmbiguousIdentity(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["one/duplicate","two/duplicate"]}}`)
	for _, parent := range []string{"one", "two"} {
		writeTestFile(t, filepath.Join(root, parent, "duplicate", "index.js"), "export default function extension(pi) {}\n")
	}
	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FindMember(resources, Extensions, "duplicate"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v, want ambiguous", err)
	}
}

func TestAgentPluginsV1UsesFixedSkillsAndVendorOverlay(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "plugin.json"), `{
  "$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name":"dev-flow",
  "unknownFutureField":true,
  "extensions":{"com.example.client":{"skills":["../must-not-parse"]}}
}`)
	writeTestFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{
  "name":"dev-flow",
  "skills":["./skills/only-listed"],
  "mcpServers":"./.mcp.json"
}`)
	writeTestFile(t, filepath.Join(root, "skills", "valid", "SKILL.md"), "---\nname: valid\ndescription: valid skill\n---\nUse it.\n")
	writeTestFile(t, filepath.Join(root, "skills", "only-listed", "SKILL.md"), "---\nname: only-listed\ndescription: listed skill\n---\nUse it.\n")
	writeTestFile(t, filepath.Join(root, "skills", "invalid", "SKILL.md"), "---\nname: INVALID\n---\nNo description.\n")
	writeTestFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"demo":{"url":"https://example.com/mcp"}}}`)

	resources, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.SkillDirs, filepath.Join(root, "skills", "valid"))
	assertContains(t, resources.SkillDirs, filepath.Join(root, "skills", "only-listed"))
	assertNotContains(t, resources.SkillDirs, filepath.Join(root, "skills", "invalid"))
	assertContains(t, resources.MCPFiles, filepath.Join(root, ".mcp.json"))
}

func TestAgentPluginsV1RejectsUnsupportedSchemaAndInvalidName(t *testing.T) {
	for _, manifest := range []string{
		`{"$schema":"https://agent-plugins.org/schemas/2.0.0/plugin.schema.json","name":"valid"}`,
		`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"Invalid_Name"}`,
	} {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "plugin.json"), manifest)
		if _, err := Validate(root); err == nil {
			t.Fatalf("manifest accepted: %s", manifest)
		}
	}
}

func TestDiscoverMergesPiAndPigPluginManifests(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{
  "name": "pkg",
  "pi": {
    "extensions": ["./ext/index.ts"],
    "skills": ["skills/alpha"],
    "prompts": ["docs/*.md", "!docs/private.md"],
    "themes": ["themes/*.json"]
  }
}`)
	writeTestFile(t, filepath.Join(root, "plugin.json"), `{
  "$schema": "https://vendor.example/plugin.schema.json",
  "name": "pkg",
  "agents": "agents/",
  "mcpServers": ".mcp.json"
}`)
	writeTestFile(t, filepath.Join(root, "ext", "index.ts"), "export default {};\n")
	writeTestFile(t, filepath.Join(root, "docs", "public.md"), "# public\n")
	writeTestFile(t, filepath.Join(root, "docs", "private.md"), "# private\n")
	writeTestFile(t, filepath.Join(root, "docs", "nested", "ignored.md"), "# nested\n")
	writeTestFile(t, filepath.Join(root, "skills", "alpha", "SKILL.md"), "# alpha\n")
	writeTestFile(t, filepath.Join(root, "skills", "beta", "SKILL.md"), "# beta\n")
	writeTestFile(t, filepath.Join(root, "themes", "dark.json"), `{}`)
	writeTestFile(t, filepath.Join(root, "agents", "review.agent.md"), "# review\n")
	writeTestFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"demo":{"command":"true"}}}`)

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, resources.ExtensionEntries, filepath.Join(root, "ext", "index.ts"))
	if slices.Contains(resources.ExtensionEntries, root) {
		t.Fatalf("package root should not be auto-added as extension: %v", resources.ExtensionEntries)
	}
	assertContains(t, resources.PromptFiles, filepath.Join(root, "docs", "public.md"))
	assertNotContains(t, resources.PromptFiles, filepath.Join(root, "docs", "private.md"))
	assertNotContains(t, resources.PromptFiles, filepath.Join(root, "docs", "nested", "ignored.md"))
	assertContains(t, resources.SkillDirs, filepath.Join(root, "skills", "alpha"))
	assertNotContains(t, resources.SkillDirs, filepath.Join(root, "skills", "beta"))
	assertContains(t, resources.ThemeFiles, filepath.Join(root, "themes", "dark.json"))
	assertContains(t, resources.AgentFiles, filepath.Join(root, "agents", "review.agent.md"))
	assertContains(t, resources.MCPFiles, filepath.Join(root, ".mcp.json"))
}

func TestDiscoverPigPluginAugmentationOverridesSharedPluginKind(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "extensions", "shared")
	pig := filepath.Join(root, "extensions", "pig")
	writeTestFile(t, filepath.Join(shared, "index.js"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(pig, "index.js"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(root, "plugin.json"), `{"extensions":["extensions/shared"]}`)
	writeTestFile(t, filepath.Join(root, ".pig-plugin", "plugin.json"), `{"extensions":["extensions/pig"]}`)

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.ExtensionEntries, filepath.Join(pig, "index.js"))
	assertNotContains(t, resources.ExtensionEntries, filepath.Join(shared, "index.js"))
}

func TestDiscoverUsesConventionalDirectories(t *testing.T) {
	root := t.TempDir()
	extensionDir := filepath.Join(root, "extensions", "trace")
	writeTestFile(t, filepath.Join(extensionDir, "index.js"), "export default function extension(pi) {}\n")
	writeTestFile(t, filepath.Join(root, "skills", "review", "SKILL.md"), "# review\n")
	writeTestFile(t, filepath.Join(root, "prompts", "review.md"), "# prompt\n")
	writeTestFile(t, filepath.Join(root, "themes", "dark.json"), `{}`)
	writeTestFile(t, filepath.Join(root, "agents", "helper.md"), "# helper\n")
	writeTestFile(t, filepath.Join(root, "mcp", "server.json"), `{"command":"true"}`)

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, resources.ExtensionEntries, filepath.Join(extensionDir, "index.js"))
	assertContains(t, resources.SkillDirs, filepath.Join(root, "skills", "review"))
	assertContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "review.md"))
	assertContains(t, resources.ThemeFiles, filepath.Join(root, "themes", "dark.json"))
	assertContains(t, resources.AgentFiles, filepath.Join(root, "agents", "helper.md"))
	assertContains(t, resources.MCPFiles, filepath.Join(root, "mcp", "server.json"))
}

func TestDiscoverDoesNotPromoteArbitraryPackageRootSource(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/package\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(root, "extension.go"), "package packagefixture\n")

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, resources.ExtensionEntries, root)
}

func TestApplyConfiguredDeltaOverlaysOnlyExactMembers(t *testing.T) {
	base := []string(nil)
	delta := []string{"-extensions/alpha", "+extensions/beta"}
	paths := []string{"extensions/alpha", "extensions/beta", "extensions/gamma"}
	got, err := ApplyConfiguredDelta(Extensions, paths, base, delta)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"extensions/alpha": false,
		"extensions/beta":  true,
		"extensions/gamma": true,
	} {
		if enabled := ResourceEnabled(path, got); enabled != want {
			t.Errorf("%s enabled = %t, want %t; filters=%v", path, enabled, want, got)
		}
	}
}

func TestApplyConfiguredDeltaRejectsUnsafePatterns(t *testing.T) {
	for name, pattern := range map[string]string{
		"lexical traversal": "-../outside",
		"absolute path":     "-/tmp/outside",
		"glob":              "-extensions/*",
		"empty target":      "-",
		"missing prefix":    "extensions/alpha",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ApplyConfiguredDelta(Extensions, []string{"extensions/alpha"}, nil, []string{pattern}); err == nil {
				t.Fatalf("unsafe delta pattern %q passed validation", pattern)
			}
		})
	}
}

func TestResourceEnabledPreservesPackageFilterSemantics(t *testing.T) {
	if ResourceEnabled("prompts/a.md", []string{}) {
		t.Fatal("explicit empty filter should disable all resources of that kind")
	}
	if !ResourceEnabled("prompts/a.md", nil) {
		t.Fatal("nil filter should leave resources enabled")
	}
	if ResourceEnabled("prompts/private.md", []string{"prompts/*.md", "-prompts/private.md"}) {
		t.Fatal("later exclude should disable an allowed resource")
	}
	if !ResourceEnabled("prompts/private.md", []string{"-prompts/*.md", "+prompts/private.md"}) {
		t.Fatal("later force include should re-enable an excluded resource")
	}
}

func TestDiscoverSkillDirsRecursesAndFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "group", "nested")
	writeTestFile(t, filepath.Join(nested, "SKILL.md"), "---\nname: nested\ndescription: nested\n---\n")
	flat := filepath.Join(root, "flat.md")
	writeTestFile(t, flat, "---\nname: flat\ndescription: flat\n---\n")
	target := filepath.Join(t.TempDir(), "linked")
	writeTestFile(t, filepath.Join(target, "SKILL.md"), "---\nname: linked\ndescription: linked\n---\n")
	link := filepath.Join(root, "linked")
	testenv.Symlink(t, target, link)

	got := DiscoverSkillDirs(root)
	for _, want := range []string{nested, flat, link} {
		assertContains(t, got, want)
	}
}

func TestDiscoverAgentSkillDirsUsesNestedMarkdownConvention(t *testing.T) {
	root := t.TempDir()
	rootMarkdown := filepath.Join(root, "README.md")
	nestedMarkdown := filepath.Join(root, "group", "nested.md")
	writeTestFile(t, rootMarkdown, "---\ndescription: documentation\n---\n")
	writeTestFile(t, nestedMarkdown, "---\ndescription: nested skill\n---\n")

	if got := DiscoverAgentSkillDirs(root); !slices.Equal(got, []string{nestedMarkdown}) {
		t.Fatalf("agent skills = %v", got)
	}
	if got := DiscoverSkillDirs(root); !slices.Contains(got, rootMarkdown) || slices.Contains(got, nestedMarkdown) {
		t.Fatalf("Pi skills = %v", got)
	}
}

func TestDiscoverSkillDirsHonorsIgnoreFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".gitignore"), "ignored/\n*.skip.md\n")
	writeTestFile(t, filepath.Join(root, "ignored", "SKILL.md"), "---\nname: ignored\ndescription: ignored\n---\n")
	writeTestFile(t, filepath.Join(root, "hidden.skip.md"), "---\nname: hidden\ndescription: hidden\n---\n")
	kept := filepath.Join(root, "kept", "SKILL.md")
	writeTestFile(t, kept, "---\nname: kept\ndescription: kept\n---\n")

	got := DiscoverSkillDirs(root)
	if !slices.Equal(got, []string{filepath.Dir(kept)}) {
		t.Fatalf("skills = %v", got)
	}
}

func TestApplyPatternsUsesSkillDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "review")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), "# review\n")
	paths := Collect([]string{filepath.Join(root, "skills")}, Skills)

	got := ApplyPatterns(paths, []string{"skills/review"}, root, Skills)
	assertContains(t, got, skillDir)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, paths []string, want string) {
	t.Helper()
	if !slices.Contains(paths, want) {
		t.Fatalf("%q missing from %v", want, paths)
	}
}

func assertNotContains(t *testing.T, paths []string, unwanted string) {
	t.Helper()
	if slices.Contains(paths, unwanted) {
		t.Fatalf("%q unexpectedly present in %v", unwanted, paths)
	}
}

func TestValidateRejectsPigletPackageMembership(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg"}`)
	writeTestFile(t, filepath.Join(root, "plugin.json"), `{"name":"pkg","piglets":["review.piglet.yaml"]}`)
	writeTestFile(t, filepath.Join(root, "review.piglet.yaml"), "name: review\n")
	if _, err := ValidatePackage(root); err == nil || !strings.Contains(err.Error(), "Piglets are independent") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateConfiguredSkipsOnlyDisabledMissingMembers(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		kind     Kind
		manifest string
		present  string
		disabled string
	}{
		{name: "extension", kind: Extensions, manifest: `{"name":"pkg","pi":{"extensions":["extensions/kept","extensions/removed"]}}`, present: "extensions/kept", disabled: "extensions/removed"},
		{name: "skill", kind: Skills, manifest: `{"name":"pkg","pi":{"skills":["skills/kept","skills/removed"]}}`, present: "skills/kept/SKILL.md", disabled: "skills/removed/SKILL.md"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "package.json"), fixture.manifest)
			presentPath := filepath.Join(root, filepath.FromSlash(fixture.present))
			writeTestFile(t, presentPath, "fixture")
			if fixture.kind == Extensions {
				if err := os.Chmod(presentPath, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ValidateConfigured(root, map[Kind][]string{fixture.kind: {"-" + fixture.disabled}}); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateConfigured(root, nil); err == nil {
				t.Fatal("enabled missing member passed")
			}
		})
	}
}

func TestValidateConfiguredDoesNotDisableManifestSafetyChecks(t *testing.T) {
	t.Run("lexical escape", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["../outside"]}}`)
		if _, err := ValidateConfigured(root, map[Kind][]string{Extensions: {"-../outside"}}); err == nil || !strings.Contains(err.Error(), "escapes package root") {
			t.Fatalf("disabled lexical escape error = %v", err)
		}
	})

	t.Run("symlink escape", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		testenv.Symlink(t, outside, filepath.Join(root, "outside-link"))
		writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["outside-link"]}}`)
		if _, err := ValidateConfigured(root, map[Kind][]string{Extensions: {"-outside-link"}}); err == nil || !strings.Contains(err.Error(), "escapes package root") {
			t.Fatalf("disabled symlink escape error = %v", err)
		}
	})
}

func TestInspectConfiguredReportsMissingWithoutWeakeningStartupValidation(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["extensions/missing"]}}`)

	_, missing, err := InspectConfigured(root, map[Kind][]string{Extensions: nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Kind != Extensions || missing[0].Pattern != "extensions/missing" || !missing[0].Enabled {
		t.Fatalf("missing = %#v", missing)
	}
	if _, err := ValidateConfigured(root, map[Kind][]string{Extensions: nil}); err == nil {
		t.Fatal("enabled missing declaration passed startup validation")
	}

	filters := map[Kind][]string{Extensions: {"-extensions/missing"}}
	_, missing, err = InspectConfigured(root, filters)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Enabled {
		t.Fatalf("disabled missing = %#v", missing)
	}
	if _, err := ValidateConfigured(root, filters); err != nil {
		t.Fatalf("disabled missing declaration blocked startup: %v", err)
	}
}

// Upstream loader.ts loadExtensions records every failing extension and keeps
// loading its siblings. Startup validation reports every unresolvable
// extension the same way, while strict validation and every other Package
// check still fail the whole Package.
func TestValidateConfiguredForStartupReportsEveryUnresolvableExtension(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["extensions/bad","extensions/good","extensions/worse","extensions/missing"],"prompts":["prompts"]}}`)
	bad := filepath.Join(root, "extensions", "bad")
	worse := filepath.Join(root, "extensions", "worse")
	good := filepath.Join(root, "extensions", "good")
	for _, dir := range []string{bad, worse} {
		writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/"+filepath.Base(dir)+"\n\ngo 1.26\n")
		writeTestFile(t, filepath.Join(dir, "extension.go"), "package "+filepath.Base(dir)+"\n\nfunc NotAFactory() {}\n")
	}
	writeTestFile(t, filepath.Join(good, "go.mod"), "module example.com/good\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(good, "extension.go"), "package good\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\"good\") }\n")
	prompt := filepath.Join(root, "prompts", "p.md")
	writeTestFile(t, prompt, "prompt\n")

	// An enabled member that matches nothing is skipped, as upstream skips it,
	// and the rest of the Package still loads.
	resources, missing, issues, err := ValidateConfiguredForStartup(root, map[Kind][]string{Extensions: nil, Prompts: nil})
	if err != nil || !slices.Equal(resources.ExtensionEntries, []string{good}) || len(issues) != 2 {
		t.Fatalf("enabled missing member: resources = %#v, issues = %#v, err = %v", resources, issues, err)
	}
	if len(missing) != 1 || missing[0].Pattern != "extensions/missing" || !missing[0].Enabled {
		t.Fatalf("missing = %#v", missing)
	}

	filters := map[Kind][]string{Extensions: {"-extensions/missing"}, Prompts: nil}
	resources, missing, issues, err = ValidateConfiguredForStartup(root, filters)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %#v, err = %v", missing, err)
	}
	if !slices.Equal(resources.ExtensionEntries, []string{good}) {
		t.Fatalf("extensions = %v, want only %s", resources.ExtensionEntries, good)
	}
	assertContains(t, resources.PromptFiles, prompt)
	if len(issues) != 2 || issues[0].Path != bad || issues[1].Path != worse || issues[0].Err == nil || issues[1].Err == nil {
		t.Fatalf("issues = %#v, want %s and %s", issues, bad, worse)
	}

	if _, err := ValidateConfigured(root, filters); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("strict validation error = %v, want failure naming %s", err, bad)
	}
	if _, _, err := InspectConfigured(root, filters); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("strict inspection error = %v, want failure naming %s", err, bad)
	}
	noExtensions := map[Kind][]string{Extensions: {}, Prompts: nil}
	if resources, missing, issues, err := ValidateConfiguredForStartup(root, noExtensions); err != nil || len(missing)+len(issues)+len(resources.ExtensionEntries) != 0 {
		t.Fatalf("disabled extensions: resources = %#v, missing = %#v, issues = %#v, err = %v", resources, missing, issues, err)
	}

	escaping := t.TempDir()
	writeTestFile(t, filepath.Join(escaping, "package.json"), `{"name":"pkg","pi":{"extensions":["../outside"]}}`)
	if _, _, _, err := ValidateConfiguredForStartup(escaping, map[Kind][]string{Extensions: {}}); err == nil || !strings.Contains(err.Error(), "escapes package root") {
		t.Fatalf("escaping Package error = %v", err)
	}
	hooks := t.TempDir()
	writeTestFile(t, filepath.Join(hooks, "package.json"), `{"name":"pkg","pig":{"hooks":["hooks/missing.json"]}}`)
	if _, _, _, err := ValidateConfiguredForStartup(hooks, nil); err == nil || !strings.Contains(err.Error(), "hooks/missing.json") {
		t.Fatalf("missing hook error = %v", err)
	}
}

// Startup used to validate a configured Package with InspectConfigured and then
// ValidateConfigured, resolving every enabled extension twice.
// ValidateConfiguredForStartup resolves each enabled extension once and never
// resolves a disabled one.
func TestValidateConfiguredForStartupResolvesEachEnabledExtensionOnce(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["extensions/a","extensions/b","extensions/bad","extensions/off"]}}`)
	for _, name := range []string{"a", "b", "off"} {
		dir := filepath.Join(root, "extensions", name)
		writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/"+name+"\n\ngo 1.26\n")
		writeTestFile(t, filepath.Join(dir, "extension.go"), "package "+name+"\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\""+name+"\") }\n")
	}
	bad := filepath.Join(root, "extensions", "bad")
	writeTestFile(t, filepath.Join(bad, "go.mod"), "module example.com/bad\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(bad, "extension.go"), "package bad\n\nfunc NotAFactory() {}\n")

	calls := map[string]int{}
	original := resolveExtensionSource
	t.Cleanup(func() { resolveExtensionSource = original })
	resolveExtensionSource = func(path string) (extsource.Definition, error) {
		calls[filepath.Base(path)]++
		return original(path)
	}
	count := func() int {
		total := 0
		for _, n := range calls {
			total += n
		}
		clear(calls)
		return total
	}

	enabledOnly := map[Kind][]string{Extensions: {"-extensions/off"}}
	withoutBad := map[Kind][]string{Extensions: {"-extensions/off", "-extensions/bad"}}
	if _, _, err := InspectConfigured(root, withoutBad); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateConfigured(root, withoutBad); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 4 {
		t.Fatalf("previous startup pair resolved %d times, want 4 (twice per enabled extension)", got)
	}

	if _, _, issues, err := ValidateConfiguredForStartup(root, enabledOnly); err != nil || len(issues) != 1 {
		t.Fatalf("issues = %#v, err = %v", issues, err)
	}
	if calls["off"] != 0 {
		t.Fatalf("disabled extension resolved %d times", calls["off"])
	}
	for _, name := range []string{"a", "b", "bad"} {
		if calls[name] != 1 {
			t.Fatalf("extension %s resolved %d times, want once: %v", name, calls[name], calls)
		}
	}
	count()

	if _, _, _, err := ValidateConfiguredForStartup(root, map[Kind][]string{Extensions: {}}); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 0 {
		t.Fatalf("extensions disabled: resolved %d times, want 0", got)
	}
}

// Upstream collects a declared Pi resource path as it exists and skips one
// that does not (collectFilesFromPaths in core/package-manager.ts). A skills
// directory holding several skills, as pi-lens, @upstash/context7-pi and
// @dietrichgebert/ponytail publish, is a present member, and a missing entry of
// any Pi kind never stops the Package from loading at startup.
func TestStartupAcceptsPiPackageResourceShapes(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{`+
		`"extensions":["./dist/index.js","./gone.js"],`+
		`"skills":["./skills","./no-skills"],`+
		`"prompts":["./prompts","./no-prompts"],`+
		`"themes":["./themes","./no-themes"]}}`)
	writeTestFile(t, filepath.Join(root, "dist", "index.js"), "export default function (pi) {}\n")
	for _, skill := range []string{"ast-grep", "lsp-navigation"} {
		writeTestFile(t, filepath.Join(root, "skills", skill, "SKILL.md"), "---\nname: "+skill+"\ndescription: d\n---\n")
	}
	writeTestFile(t, filepath.Join(root, "prompts", "p.md"), "prompt\n")
	writeTestFile(t, filepath.Join(root, "themes", "t.json"), "{}\n")

	_, missing, err := InspectConfigured(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, member := range missing {
		patterns = append(patterns, member.Pattern)
	}
	slices.Sort(patterns)
	if want := []string{"./gone.js", "./no-prompts", "./no-themes", "no-skills/SKILL.md"}; !slices.Equal(patterns, want) {
		t.Fatalf("missing = %v, want %v (the populated skills directory is present)", patterns, want)
	}

	resources, _, issues, err := ValidateConfiguredForStartupWithResolver(root, nil, func(string) (extsource.Definition, error) {
		return extsource.Definition{Language: "node", Form: extsource.Factory}, nil
	})
	if err != nil || len(issues) != 0 {
		t.Fatalf("startup refused a Package upstream accepts: issues = %#v, err = %v", issues, err)
	}
	if len(resources.ExtensionEntries) != 1 || len(resources.SkillDirs) != 2 || len(resources.PromptFiles) != 1 || len(resources.ThemeFiles) != 1 {
		t.Fatalf("resources = %#v, want 1 extension, 2 skills, 1 prompt, 1 theme", resources)
	}
}

// A package with a "pi" manifest loads from what it declares, as upstream does.
// @dietrichgebert/ponytail ships Claude Code, Codex and Cursor hook configs in
// hooks/ beside a pi manifest; PiG must neither read them as its own Hooks
// nor refuse the Package over events it does not support. Without a pi
// manifest, PiG's conventional directories still apply.
func TestPiManifestPackageSkipsPigConventionDirectories(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"extensions":["./pi-extension/index.js"],"skills":["./skills"]}}`)
	writeTestFile(t, filepath.Join(root, "pi-extension", "index.js"), "export default function (pi) {}\n")
	writeTestFile(t, filepath.Join(root, "skills", "one", "SKILL.md"), "---\nname: one\ndescription: d\n---\n")
	writeTestFile(t, filepath.Join(root, "hooks", "claude-codex-hooks.json"), `{"hooks":{"SubagentStart":[{"hooks":[{"type":"command","command":"node hooks/x.js"}]}]}}`)
	writeTestFile(t, filepath.Join(root, "mcp", "servers.json"), `{"mcpServers":{}}`)

	resources, _, issues, err := ValidateConfiguredForStartupWithResolver(root, nil, func(string) (extsource.Definition, error) {
		return extsource.Definition{Language: "node", Form: extsource.Factory}, nil
	})
	if err != nil || len(issues) != 0 {
		t.Fatalf("startup refused a Package upstream loads: issues = %#v, err = %v", issues, err)
	}
	if len(resources.HookFiles) != 0 || len(resources.MCPFiles) != 0 || len(resources.ExtensionEntries) != 1 || len(resources.SkillDirs) != 1 {
		t.Fatalf("resources = %#v, want the declared extension and skill only", resources)
	}

	conventional := t.TempDir()
	writeTestFile(t, filepath.Join(conventional, "package.json"), `{"name":"pkg"}`)
	writeTestFile(t, filepath.Join(conventional, "hooks", "h.json"), `{}`)
	if found, err := Discover(conventional); err != nil || len(found.HookFiles) != 1 {
		t.Fatalf("conventional hooks without a pi manifest = %#v, err = %v", found.HookFiles, err)
	}
}

// Upstream resolveExtensionEntries loads each existing entry an extension
// directory's Pi manifest declares as its own extension, falls back to the
// index when none exists, and loads nothing when neither exists. PiG used to
// return the directory itself, which then failed to resolve to one entry.
func TestDiscoverAutomaticLoadsEachDeclaredEntryOfAnExtensionDirectory(t *testing.T) {
	dir := t.TempDir()
	extension := "export default function extension(pi) {}\n"
	two := filepath.Join(dir, "two")
	writeTestFile(t, filepath.Join(two, "package.json"), `{"pi":{"extensions":["./a.ts","./b.js","./gone.ts"]}}`)
	writeTestFile(t, filepath.Join(two, "a.ts"), extension)
	writeTestFile(t, filepath.Join(two, "b.js"), extension)
	fallback := filepath.Join(dir, "fallback")
	writeTestFile(t, filepath.Join(fallback, "package.json"), `{"pi":{"extensions":["./gone.ts"]}}`)
	writeTestFile(t, filepath.Join(fallback, "index.ts"), extension)
	missing := filepath.Join(dir, "missing")
	writeTestFile(t, filepath.Join(missing, "package.json"), `{"pi":{"extensions":["./gone.ts"]}}`)
	writeTestFile(t, filepath.Join(missing, "main.ts"), extension)

	got := DiscoverAutomatic(dir, Extensions)
	want := []string{filepath.Join(fallback, "index.ts"), filepath.Join(two, "a.ts"), filepath.Join(two, "b.js")}
	if !slices.Equal(got, want) {
		t.Fatalf("DiscoverAutomatic = %v, want %v", got, want)
	}
}

// A Package's pi.extensions directory entry expands the way upstream
// collectAutoExtensionEntries does: every .ts/.js file and every subdirectory
// entry is its own extension, and a directory without entries yields none.
func TestDiscoverExpandsPiManifestDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	extension := "export default function extension(pi) {}\n"
	writeTestFile(t, filepath.Join(root, "package.json"), `{"pi":{"extensions":["./extensions","./empty"]}}`)
	writeTestFile(t, filepath.Join(root, "extensions", "alpha.ts"), extension)
	writeTestFile(t, filepath.Join(root, "extensions", "beta.js"), extension)
	writeTestFile(t, filepath.Join(root, "extensions", "gamma", "index.ts"), extension)
	writeTestFile(t, filepath.Join(root, "extensions", ".hidden.ts"), extension)
	writeTestFile(t, filepath.Join(root, "extensions", "notes.md"), "notes\n")
	writeTestFile(t, filepath.Join(root, "empty", "README.md"), "nothing here\n")

	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "extensions", "alpha.ts"),
		filepath.Join(root, "extensions", "beta.js"),
		filepath.Join(root, "extensions", "gamma", "index.ts"),
	}
	if !slices.Equal(resources.ExtensionEntries, want) {
		t.Fatalf("ExtensionEntries = %v, want %v", resources.ExtensionEntries, want)
	}
}
