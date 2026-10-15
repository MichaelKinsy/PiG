package codingagent

import (
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts trims each stack line (:3056), the startup changelog (:866) and each compact-list label (:1788) with String.prototype.trim,
// which strips U+FEFF and keeps U+0085; strings.TrimSpace does the opposite for both.

func TestExtensionErrorStackLinesTrimLikeJavaScript(t *testing.T) {
	render := func(stack string) []string {
		return noticeLayout(t, func(m *InteractiveMode) { m.showExtensionError("ext.ts", "boom", stack) }, 60)
	}
	if got, want := render("Error: boom\n\ufeff\u00a0at f (a.ts:1)\u2028"), render("Error: boom\nat f (a.ts:1)"); !slices.Equal(got, want) {
		t.Fatalf("padded stack line:\n got: %q\nwant: %q", got, want)
	}
	if got, bare := render("Error: boom\n\u0085at f\u0085"), render("Error: boom\nat f"); slices.Equal(got, bare) {
		t.Fatalf("U+0085 is not JavaScript whitespace, yet the stack rows equal the bare line: %q", got)
	}
}

func TestStartupChangelogTrimsLikeJavaScript(t *testing.T) {
	render := func(content string) []string {
		mode := &InteractiveMode{
			chatContainer: tui.NewContainer(),
			tuiInst:       tui.NewWithOutput(io.Discard, 80, 24),
			opts:          InteractiveModeOptions{SettingsManager: &SettingsManager{}, AppVersion: "9.9.9"},
		}
		mode.showStartupChangelog([]ChangelogEntry{{Content: content}})
		var got []string
		for _, line := range mode.chatContainer.Render(80) {
			got = append(got, strings.TrimRight(stripANSI(line), " "))
		}
		return got
	}
	if got, want := render("\ufeff## [0.3.0]\nNewest\ufeff"), render("## [0.3.0]\nNewest"); !slices.Equal(got, want) {
		t.Fatalf("changelog with byte order marks at its ends:\n got: %q\nwant: %q", got, want)
	}
}

func TestCompactResourceLabelsTrimLikeJavaScript(t *testing.T) {
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "extensions", "\ufeffauto-one.ts")
	m := &InteractiveMode{
		opts: InteractiveModeOptions{CWD: t.TempDir(), AgentDir: agentDir, NoThemes: true},
		newRunner: inproc.NewRunner([]extension.Extension{{
			Name: "auto-one", Path: path, ResolvedPath: path,
			SourceInfo: PiSourceInfo{Path: path, Source: "local", Scope: "user", Origin: "top-level", BaseDir: filepath.Dir(path)},
		}}, agentDir),
		loadedResourcesContainer: tui.NewContainer(),
		resourceSourceInfo:       map[string]ResourceSourceInfo{},
	}
	m.showLoadedResources(false, false)
	lines := m.loadedResourcesContainer.Render(100)
	for i, line := range lines {
		lines[i] = strings.TrimRight(stripANSITest(line), " ")
	}
	if got, want := strings.Join(lines, "\n"), "[Extensions]\n  auto-one.ts\n"; got != want {
		t.Fatalf("listing = %q, want %q", got, want)
	}
}
