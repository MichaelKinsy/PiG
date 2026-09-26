package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Labels observed from Pi 0.87.1's startup listing for the nine npm packages
// of the extension e2e (getCompactExtensionLabels): a package entry is
// "<package>:<entry>", an index entry names its directory, an entry under
// extensions/ drops that prefix, and a root index names the package alone.
// Other extensions take the shortest trailing path no other one shares.
func TestCompactExtensionLabelsMatchUpstream(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "agent", "npm", "node_modules")
	pkg := func(name, entry string) loadedResource {
		base := filepath.Join(root, filepath.FromSlash(name))
		return loadedResource{
			path: filepath.Join(base, filepath.FromSlash(entry)),
			info: &PiSourceInfo{Source: "npm:" + name, Scope: "user", Origin: "package", BaseDir: base},
		}
	}
	gitBase := filepath.Join(string(filepath.Separator), "agent", "git", "github.com", "pig-parity", "listing-fixture")
	local := func(path string) loadedResource {
		return loadedResource{path: filepath.FromSlash(path), info: &PiSourceInfo{Source: "cli", Scope: "temporary", Origin: "top-level"}}
	}
	extensions := []loadedResource{
		pkg("@dietrichgebert/ponytail", "pi-extension/index.js"),
		pkg("@upstash/context7-pi", "extensions/context7.ts"),
		pkg("pi-auto-update", "extensions/auto-update.ts"),
		pkg("pi-hermes-memory", "src/index.ts"),
		pkg("pi-lens", "dist/index.js"),
		pkg("pi-mcp-adapter", "index.ts"),
		{path: filepath.Join(gitBase, "extensions", "beta", "index.ts"), info: &PiSourceInfo{Source: "git:github.com/pig-parity/listing-fixture", Scope: "user", Origin: "package", BaseDir: gitBase}},
		local("/work/probe/index.ts"),
		local("/work/a/tool.ts"),
		local("/work/b/tool.ts"),
	}
	want := []string{
		"@dietrichgebert/ponytail:pi-extension",
		"@upstash/context7-pi:context7.ts",
		"pi-auto-update:auto-update.ts",
		"pi-hermes-memory:src",
		"pi-lens:dist",
		"pi-mcp-adapter",
		"pig-parity/listing-fixture:beta",
		"probe",
		"a/tool.ts",
		"b/tool.ts",
	}
	if got := getCompactExtensionLabels(extensions); !slices.Equal(got, want) {
		t.Fatalf("labels = %q\nwant     %q", got, want)
	}
}

// Upstream showLoadedResources puts each section's heading on its own line,
// the list below it, and a blank line after the section; Ctrl+O switches every
// section to its scope-grouped expanded body.
func TestShowLoadedResourcesListsEachSectionUnderItsHeading(t *testing.T) {
	agentDir := t.TempDir()
	base := filepath.Join(agentDir, "npm", "node_modules", "pi-lens")
	entry := filepath.Join(base, "dist", "index.js")
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "dist", Path: entry, ResolvedPath: entry,
		SourceInfo: PiSourceInfo{Path: entry, Source: "npm:pi-lens", Scope: "user", Origin: "package", BaseDir: base},
	}}, agentDir)
	m := &InteractiveMode{
		opts: InteractiveOptions{
			CWD: t.TempDir(), AgentDir: agentDir, NoThemes: true,
			Skills: []*SkillDef{{Name: "zeta", Path: filepath.Join(agentDir, "skills", "zeta", "SKILL.md")}, {Name: "alpha", Path: filepath.Join(agentDir, "skills", "alpha", "SKILL.md")}},
		},
		newRunner:                runner,
		loadedResourcesContainer: tui.NewContainer(),
		resourceSourceInfo:       map[string]ResourceSourceInfo{},
	}
	render := func() string {
		lines := m.loadedResourcesContainer.Render(100)
		for i, line := range lines {
			lines[i] = strings.TrimRight(stripANSITest(line), " ")
		}
		return strings.Join(lines, "\n")
	}
	m.showLoadedResources(false)
	if got, want := render(), "[Skills]\n  alpha, zeta\n\n[Extensions]\n  pi-lens:dist\n"; got != want {
		t.Fatalf("listing = %q, want %q", got, want)
	}

	m.setAllToolsExpanded(true)
	if got := render(); !strings.Contains(got, "[Extensions]\n  user\n    npm:pi-lens\n      dist\n") {
		t.Fatalf("expanded listing = %q", got)
	}
}

// Ports the showLoadedResources cases of upstream
// packages/coding-agent/test/interactive-mode-status.test.ts.

type upstreamExtensionFixture struct {
	path    string
	source  string
	scope   string
	origin  string
	baseDir string
}

func upstreamMixedExtensionFixtures() []upstreamExtensionFixture {
	return []upstreamExtensionFixture{
		{"/tmp/project/.pi/extensions/answer.ts", "local", "project", "top-level", "/tmp/project/.pi/extensions"},
		{"/tmp/project/.pi/extensions/local-index/index.ts", "local", "project", "top-level", "/tmp/project/.pi/extensions"},
		{"/tmp/agent/extensions/user-index/index.ts", "local", "user", "top-level", "/tmp/agent/extensions"},
		{"/tmp/project/.pi/npm/node_modules/pi-markdown-preview/extensions/index.ts", "npm:pi-markdown-preview", "project", "package", "/tmp/project/.pi/npm/node_modules/pi-markdown-preview"},
		{"/tmp/project/.pi/npm/node_modules/@scope/pi-scoped/extensions/index.ts", "npm:@scope/pi-scoped", "project", "package", "/tmp/project/.pi/npm/node_modules/@scope/pi-scoped"},
		{"/tmp/project/.pi/git/github.com/HazAT/pi-interactive-subagents/extensions/index.ts", "git:github.com/HazAT/pi-interactive-subagents", "project", "package", "/tmp/project/.pi/git/github.com/HazAT/pi-interactive-subagents"},
		{"/tmp/project/.pi/git/github.com/HazAT/pi-interactive-subagents/extensions/subagents/index.ts", "git:github.com/HazAT/pi-interactive-subagents", "project", "package", "/tmp/project/.pi/git/github.com/HazAT/pi-interactive-subagents"},
		{"/tmp/temp/cli-extension.ts", "cli", "temporary", "top-level", "/tmp/temp"},
	}
}

func upstreamListingMode(t *testing.T, expanded bool, fixtures []upstreamExtensionFixture) *InteractiveMode {
	t.Helper()
	extensions := make([]extension.Extension, len(fixtures))
	for i, fixture := range fixtures {
		extensions[i] = extension.Extension{
			Name: fmt.Sprintf("ext-%d", i), Path: fixture.path, ResolvedPath: fixture.path,
			SourceInfo: PiSourceInfo{Path: fixture.path, Source: fixture.source, Scope: fixture.scope, Origin: fixture.origin, BaseDir: fixture.baseDir},
		}
	}
	return &InteractiveMode{
		opts:                     InteractiveOptions{CWD: "/tmp/project", AgentDir: "/tmp/agent", NoThemes: true},
		newRunner:                inproc.NewRunner(extensions, "/tmp/project"),
		loadedResourcesContainer: tui.NewContainer(),
		resourceSourceInfo:       map[string]ResourceSourceInfo{},
		toolsExpanded:            expanded,
	}
}

func renderedListing(m *InteractiveMode) string {
	m.showLoadedResources(false)
	lines := m.loadedResourcesContainer.Render(200)
	for i, line := range lines {
		lines[i] = strings.TrimRight(stripANSITest(line), " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func TestLoadedResourcesExtensionLabelsMatchUpstream(t *testing.T) {
	local := func(path, baseDir string) upstreamExtensionFixture {
		return upstreamExtensionFixture{path, "local", "project", "top-level", baseDir}
	}
	cli := func(path, baseDir string) upstreamExtensionFixture {
		return upstreamExtensionFixture{path, "cli", "temporary", "top-level", baseDir}
	}
	npm := func(path string) upstreamExtensionFixture {
		return upstreamExtensionFixture{path, "npm:primary-package", "project", "package", "/tmp/project/.pi/npm/node_modules/primary-package"}
	}
	windowsBase := `C:\Users\me\.pi\agent\npm\node_modules\primary-package`
	for _, tc := range []struct {
		name     string
		fixtures []upstreamExtensionFixture
		want     string
	}{
		{"captures mixed extension layouts in compact output", upstreamMixedExtensionFixtures(),
			"@scope/pi-scoped, answer.ts, cli-extension.ts, HazAT/pi-interactive-subagents, HazAT/pi-interactive-subagents:subagents, local-index, pi-markdown-preview, user-index"},
		{"adds more parent folders until local extension labels are unique", []upstreamExtensionFixture{
			cli("/tmp/alpha/one/index.ts", "/tmp/alpha"), cli("/tmp/beta/one/index.ts", "/tmp/beta"), cli("/tmp/gamma/one/index.ts", "/tmp/gamma"),
		}, "alpha/one, beta/one, gamma/one"},
		{"strips index.ts from local extension label, showing parent dir", []upstreamExtensionFixture{local("/tmp/extensions/plan-mode/index.ts", "/tmp/extensions")}, "plan-mode"},
		{"strips index.js from local extension label, showing parent dir", []upstreamExtensionFixture{local("/tmp/extensions/plan-mode/index.js", "/tmp/extensions")}, "plan-mode"},
		{"mixed single-file and subdirectory index.ts extensions strip index.ts", []upstreamExtensionFixture{
			local("/tmp/extensions/webfetch.ts", "/tmp/extensions"), local("/tmp/extensions/plan-mode/index.ts", "/tmp/extensions"),
		}, "plan-mode, webfetch.ts"},
		{"multiple index.ts with unique parent dirs need no disambiguation", []upstreamExtensionFixture{
			local("/tmp/extensions/foo/index.ts", "/tmp/extensions"), local("/tmp/extensions/bar/index.ts", "/tmp/extensions"),
		}, "bar, foo"},
		{"multiple index.ts with same parent dir name disambiguated with grandparent", []upstreamExtensionFixture{
			cli("/tmp/alpha/tools/index.ts", "/tmp/alpha"), cli("/tmp/beta/tools/index.ts", "/tmp/beta"),
		}, "alpha/tools, beta/tools"},
		{"non-index file in subdirectory stays as filename", []upstreamExtensionFixture{local("/tmp/extensions/my-ext/main.ts", "/tmp/extensions")}, "main.ts"},
		{"package extensions still strip index.ts correctly", []upstreamExtensionFixture{upstreamMixedExtensionFixtures()[3]}, "pi-markdown-preview"},
		{"labels npm sibling extensions relative to the declaring package", []upstreamExtensionFixture{
			npm("/tmp/project/.pi/npm/node_modules/primary-package/index.ts"), npm("/tmp/project/.pi/npm/node_modules/sibling-package/index.ts"),
		}, "primary-package, primary-package:../sibling-package"},
		{"labels Windows npm sibling extensions relative to the declaring package", []upstreamExtensionFixture{
			{windowsBase + `\index.ts`, "npm:primary-package", "user", "package", windowsBase},
			{`C:\Users\me\.pi\agent\npm\node_modules\sibling-package\index.ts`, "npm:primary-package", "user", "package", windowsBase},
		}, "primary-package, primary-package:../sibling-package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := renderedListing(upstreamListingMode(t, false, tc.fixtures)), "[Extensions]\n  "+tc.want; got != want {
				t.Fatalf("listing =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestLoadedResourcesExpandedExtensionsMatchUpstream(t *testing.T) {
	want := `[Extensions]
  project
    /tmp/project/.pi/extensions/answer.ts
    /tmp/project/.pi/extensions/local-index
    git:github.com/HazAT/pi-interactive-subagents
      extensions
      extensions/subagents
    npm:@scope/pi-scoped
      extensions
    npm:pi-markdown-preview
      extensions
  user
    /tmp/agent/extensions/user-index
  path
    /tmp/temp/cli-extension.ts`
	if got := renderedListing(upstreamListingMode(t, true, upstreamMixedExtensionFixtures())); got != want {
		t.Fatalf("expanded listing =\n%s\nwant\n%s", got, want)
	}
}

func TestLoadedResourcesContextMatchesUpstream(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cwd := filepath.Join(home, "Development", "pi-mono")
	context := []ContextFile{{Path: filepath.Join(home, ".pi", "agent", "AGENTS.md")}, {Path: filepath.Join(cwd, "AGENTS.md")}}
	m := &InteractiveMode{opts: InteractiveOptions{CWD: cwd, NoThemes: true, ContextFiles: context}, loadedResourcesContainer: tui.NewContainer()}
	// shows context paths relative to cwd while preserving full external paths
	if got := renderedListing(m); got != "\n[Context]\n  ~/.pi/agent/AGENTS.md, AGENTS.md" {
		t.Fatalf("compact context = %q", got)
	}
	// shows full context paths when expanded
	m.toolsExpanded = true
	if got := renderedListing(m); got != "\n[Context]\n  ~/.pi/agent/AGENTS.md\n  ~/Development/pi-mono/AGENTS.md" {
		t.Fatalf("expanded context = %q", got)
	}
	// shows system prompt context paths before project context files
	project := &InteractiveMode{opts: InteractiveOptions{
		CWD: "/tmp/project", NoThemes: true,
		SystemPromptSourcePaths: []string{"/tmp/project/.pi/SYSTEM.md", "/tmp/project/.pi/APPEND_SYSTEM.md"},
		ContextFiles:            []ContextFile{{Path: "/tmp/project/AGENTS.md"}},
	}, loadedResourcesContainer: tui.NewContainer()}
	if got := renderedListing(project); got != "\n[Context]\n  .pi/SYSTEM.md, .pi/APPEND_SYSTEM.md, AGENTS.md" {
		t.Fatalf("system prompt context = %q", got)
	}
}

// Upstream showLoadedResources omits the listing on quiet startup unless the
// startup is verbose, which also starts it expanded.
func TestLoadedResourcesQuietAndVerboseStartup(t *testing.T) {
	m := upstreamListingMode(t, false, []upstreamExtensionFixture{upstreamMixedExtensionFixtures()[0]})
	m.opts.Settings.QuietStartup = true
	if got := renderedListing(m); got != "" {
		t.Fatalf("quiet startup listing = %q", got)
	}
	m.opts.Verbose = true
	if got := renderedListing(m); !strings.Contains(got, "  project\n    /tmp/project/.pi/extensions/answer.ts") {
		t.Fatalf("verbose startup listing = %q, want the expanded listing", got)
	}
}
