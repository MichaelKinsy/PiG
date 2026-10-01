package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.99.2 interactive-mode.ts:1759-1766 lists `getExtensions().extensions.filter((extension) => !extension.hidden)`.
// types.ts:2015 states that a `builtin:<name>` extension "is hidden from the startup Extensions list";
// resource-loader.ts:729 sets `extension.hidden = true` on every built-in extension and :1136 sets it on an
// inline extension declared `hidden`. The listing and the source info that groups the expanded listing read the
// same filtered list.
func TestShowLoadedResourcesOmitsHiddenExtensions(t *testing.T) {
	agentDir := t.TempDir()
	visible := filepath.Join(agentDir, "extensions", "auto-one.ts")
	hidden := func(name string) extension.Extension {
		path := "builtin:" + name
		return extension.Extension{
			Name: name, Path: path, ResolvedPath: path, Hidden: true,
			SourceInfo: PiSourceInfo{Path: path, Source: "builtin", Scope: "temporary", Origin: "top-level"},
		}
	}
	runner := inproc.NewRunner([]extension.Extension{
		hidden("mcp"),
		{
			Name: "auto-one", Path: visible, ResolvedPath: visible,
			SourceInfo: PiSourceInfo{Path: visible, Source: "local", Scope: "user", Origin: "top-level", BaseDir: filepath.Dir(visible)},
		},
		hidden("codemode"),
	}, agentDir)
	m := &InteractiveMode{
		opts:                     InteractiveOptions{CWD: t.TempDir(), AgentDir: agentDir, NoThemes: true},
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
	m.showLoadedResources(false, false)
	if got, want := render(), "[Extensions]\n  auto-one.ts\n"; got != want {
		t.Fatalf("listing = %q, want %q", got, want)
	}
	m.setAllToolsExpanded(true)
	if got := render(); strings.Contains(got, "builtin") {
		t.Fatalf("expanded listing names a hidden extension: %q", got)
	}

	onlyHidden := &InteractiveMode{
		opts:                     InteractiveOptions{CWD: t.TempDir(), AgentDir: agentDir, NoThemes: true},
		newRunner:                inproc.NewRunner([]extension.Extension{hidden("tool-search")}, agentDir),
		loadedResourcesContainer: tui.NewContainer(),
		resourceSourceInfo:       map[string]ResourceSourceInfo{},
	}
	onlyHidden.showLoadedResources(false, false)
	if lines := onlyHidden.loadedResourcesContainer.Render(100); len(lines) != 0 {
		t.Fatalf("a listing of hidden extensions only renders %q, want no Extensions section", lines)
	}
}
