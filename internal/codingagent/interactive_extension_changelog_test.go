//go:build !pig_strip_changelog

package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/tui"
)

const extensionChangelogFixture = `# Changelog

## [Unreleased]

- Not released yet

## [1.2.0]

- Added feature B

## [1.1.0]

- Added feature A

## [1.0.0]

- Initial release
`

// interactive-mode-extension-changelog.test.ts: getExtensionChangelogUpdates over two extensions of one package.
func TestExtensionChangelogUpdatesUpstream(t *testing.T) {
	packageRoot := filepath.Join(t.TempDir(), "pkg")
	if err := os.Mkdir(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "CHANGELOG.md"), []byte(extensionChangelogFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	writePackage := func(version string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pi-demo-extension","version":"`+version+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newSourceInfo := func() PiSourceInfo {
		return PiSourceInfo{
			Path:          filepath.Join(packageRoot, "a.ts"),
			Source:        "npm:pi-demo-extension",
			Scope:         "user",
			Origin:        "package",
			BaseDir:       packageRoot,
			ChangelogPath: filepath.Join(packageRoot, "CHANGELOG.md"),
		}
	}
	// Two extensions from the same package must only produce one changelog section.
	display := func(sm *SettingsManager, info PiSourceInfo, hasMessages bool) []extensionChangelogUpdate {
		second := info
		second.Path = filepath.Join(packageRoot, "b.ts")
		return getExtensionChangelogUpdates(sm, []PiSourceInfo{info, second}, hasMessages)
	}
	recorded := func(sm *SettingsManager, info PiSourceInfo) (string, bool) {
		return sm.GetExtensionChangelogVersion(source.GetSourcePackageKey(info.Source, source.SourceScope(info.Scope)))
	}

	t.Run("records the version on first launch without showing the changelog", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		writePackage("1.0.0")
		if got := display(sm, info, false); len(got) != 0 {
			t.Fatalf("updates = %+v, want none", got)
		}
		if version, ok := recorded(sm, info); !ok || version != "1.0.0" {
			t.Fatalf("recorded version = %q, %v, want 1.0.0", version, ok)
		}
	})

	t.Run("shows entries newer than the last seen version, headed by package name", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		writePackage("1.0.0")
		display(sm, info, false)
		writePackage("1.2.0")
		want := []extensionChangelogUpdate{{
			Name:     "pi-demo-extension",
			Version:  "1.2.0",
			Markdown: "## pi-demo-extension [1.2.0]\n\n- Added feature B\n\n## pi-demo-extension [1.1.0]\n\n- Added feature A",
		}}
		if got := display(sm, info, false); !slices.Equal(got, want) {
			t.Fatalf("updates = %+v, want %+v", got, want)
		}
		if version, _ := recorded(sm, info); version != "1.2.0" {
			t.Fatalf("recorded version = %q, want 1.2.0", version)
		}
		if got := display(sm, info, false); len(got) != 0 {
			t.Fatalf("second display = %+v, want none", got)
		}
	})

	t.Run("skips resumed sessions", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		writePackage("1.0.0")
		display(sm, info, false)
		writePackage("1.2.0")
		if got := display(sm, info, true); len(got) != 0 {
			t.Fatalf("updates = %+v, want none", got)
		}
		if version, _ := recorded(sm, info); version != "1.0.0" {
			t.Fatalf("recorded version = %q, want 1.0.0", version)
		}
	})

	t.Run("ignores non-package extensions and packages without a changelog", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		writePackage("1.0.0")
		info.Origin = "top-level"
		if got := display(sm, info, false); len(got) != 0 {
			t.Fatalf("top-level updates = %+v, want none", got)
		}
		info.Origin = "package"
		info.ChangelogPath = ""
		if got := display(sm, info, false); len(got) != 0 {
			t.Fatalf("no-changelog updates = %+v, want none", got)
		}
		if version, ok := recorded(sm, info); ok {
			t.Fatalf("recorded version = %q, want unset", version)
		}
	})

	t.Run("reads a package.json that starts with a byte order mark", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte("\ufeff{\"name\":\"pi-demo-extension\",\"version\":\"1.0.0\"}"), 0o644); err != nil {
			t.Fatal(err)
		}
		display(sm, info, false)
		if version, _ := recorded(sm, info); version != "1.0.0" {
			t.Fatalf("recorded version = %q, want 1.0.0", version)
		}
	})

	t.Run("skips a package.json that is unreadable or has no version", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pi-demo-extension"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := display(sm, info, false); len(got) != 0 {
			t.Fatalf("updates = %+v, want none", got)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{not json`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := display(sm, info, false); len(got) != 0 {
			t.Fatalf("updates = %+v, want none", got)
		}
		if version, ok := recorded(sm, info); ok {
			t.Fatalf("recorded version = %q, want unset", version)
		}
	})

	t.Run("names an update after the package source when package.json has no name", func(t *testing.T) {
		sm := NewInMemorySettingsManager(Settings{})
		info := newSourceInfo()
		writeUnnamed := func(version string) {
			if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeUnnamed("1.0.0")
		display(sm, info, false)
		writeUnnamed("1.2.0")
		got := display(sm, info, false)
		want := []extensionChangelogUpdate{{Name: "npm:pi-demo-extension", Version: "1.2.0", Markdown: "## npm:pi-demo-extension [1.2.0]\n\n- Added feature B\n\n## npm:pi-demo-extension [1.1.0]\n\n- Added feature A"}}
		if !slices.Equal(got, want) {
			t.Fatalf("updates = %+v, want %+v", got, want)
		}
	})
}

// interactive-mode.ts showStartupNoticesIfNeeded: the extension block follows Pi's own changelog block and sits between the same borders.
func TestStartupExtensionChangelogBlock(t *testing.T) {
	tui.SetCapabilityOverrides(tui.CapabilityOverrides{Hyperlinks: new(false)})
	t.Cleanup(func() { tui.SetCapabilityOverrides(tui.CapabilityOverrides{}); tui.ResetCapabilitiesCache() })
	updates := []extensionChangelogUpdate{
		{Name: "demo", Version: "1.2.0", Markdown: "## demo [1.2.0]\n\nAdded B"},
		{Name: "other", Version: "2.0.0", Markdown: "## other [2.0.0]\n\nAdded C"},
	}
	own := GetNewEntries(ParseChangelog("## [0.3.0]\nNewest\n\n## [0.2.0]\nMiddle"), "0.2.0")
	render := func(collapse bool, entries []ChangelogEntry, updates []extensionChangelogUpdate) []string {
		mode := &InteractiveMode{
			chatContainer: tui.NewContainer(),
			tuiInst:       tui.NewWithOutput(io.Discard, 80, 24),
			opts:          InteractiveModeOptions{SettingsManager: &SettingsManager{merged: Settings{CollapseChangelog: collapse}}, AppVersion: "9.9.9"},
		}
		mode.showStartupChangelog(entries, updates)
		var got []string
		for _, line := range mode.chatContainer.Render(80) {
			got = append(got, strings.TrimRight(stripANSI(line), " "))
		}
		return got
	}
	border := strings.Repeat("─", 80)
	if got, want := render(false, nil, updates[:1]), []string{border, " Extension Updates", "", " demo [1.2.0]", "", " Added B", "", border}; !slices.Equal(got, want) {
		t.Fatalf("expanded extension block\ngot:  %q\nwant: %q", got, want)
	}
	if got, want := render(true, nil, updates), []string{border, " Updated demo to v1.2.0.", " Updated other to v2.0.0.", border}; !slices.Equal(got, want) {
		t.Fatalf("collapsed extension block\ngot:  %q\nwant: %q", got, want)
	}
	want := []string{border, " Updated to v0.3.0. Use /changelog to view full changelog.", "", " Updated demo to v1.2.0.", border}
	if got := render(true, own, updates[:1]); !slices.Equal(got, want) {
		t.Fatalf("collapsed own and extension blocks\ngot:  %q\nwant: %q", got, want)
	}
	want = []string{border, " What's New", "", " [0.3.0]", "", " Newest", "", "", " Extension Updates", "", " demo [1.2.0]", "", " Added B", "", border}
	if got := render(false, own, updates[:1]); !slices.Equal(got, want) {
		t.Fatalf("expanded own and extension blocks\ngot:  %q\nwant: %q", got, want)
	}
}
