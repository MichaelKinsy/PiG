package codingagent

import (
	"io"
	"slices"
	"strings"
	"testing"

	pig "github.com/MichaelKinsy/PiG"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestChangelogCommandRetainsContentBeyondViewport(t *testing.T) {
	original := pig.Changelog
	t.Cleanup(func() { pig.Changelog = original })
	// Pi appends all entries; only the terminal viewport clips the head of a long release.
	pig.Changelog = "## [0.2.0]\n\n" + strings.Repeat("- Release detail\n", 600) + "\nNewest tail\n\n## [0.1.0]\n\nOldest entry"
	mode := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	mode.chatContainer.Add(tui.NewText("Retained transcript"))
	if err := changelogHandler(mode.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	lines := mode.chatContainer.Render(80)
	got := stripANSI(strings.Join(lines, "\n"))
	if !strings.HasPrefix(got, "Retained transcript") || strings.Count(got, "Release detail") != 600 {
		t.Fatal("changelog truncated content or replaced the existing transcript")
	}
	if strings.Index(got, "Oldest entry") > strings.Index(got, "Newest tail") {
		t.Fatal("entries are not oldest-first")
	}
	// Once the newest entry exceeds the pane, its heading legitimately lies outside the viewport.
	tail := stripANSI(strings.Join(lines[len(lines)-24:], "\n"))
	if strings.Contains(tail, "[0.2.0]") || !strings.Contains(tail, "Newest tail") {
		t.Fatalf("unexpected viewport tail: %q", tail)
	}
}

func BenchmarkChangelogCommandRender(b *testing.B) {
	original := pig.Changelog
	b.Cleanup(func() { pig.Changelog = original })
	pig.Changelog = "## [0.2.0]\n\n" + strings.Repeat("- Release detail\n", 600) + "\n## [0.1.0]\n\nOldest entry"
	mode := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 100, 60)}
	sc := mode.buildSlashContext(b.Context())
	b.ReportAllocs()
	for b.Loop() {
		mode.chatContainer.Clear()
		if err := changelogHandler(sc); err != nil {
			b.Fatal(err)
		}
		mode.chatContainer.Render(100)
	}
}

// Pi 0.87.1 interactive-mode.ts:6518-6524 uses separate Text, Spacer, Markdown and DynamicBorder components.
func TestChangelogCommandUsesUpstreamInlineLayout(t *testing.T) {
	original := pig.Changelog
	t.Cleanup(func() { pig.Changelog = original })
	for _, content := range []string{"", "## [0.2.0]\nNewest\n\n## [0.1.0]\nOldest"} {
		pig.Changelog = content
		mode := &InteractiveMode{
			chatContainer: tui.NewContainer(),
			tuiInst:       tui.NewWithOutput(io.Discard, 80, 24),
			opts:          InteractiveOptions{SettingsManager: &SettingsManager{merged: Settings{CollapseChangelog: true}}},
		}
		if err := changelogHandler(mode.buildSlashContext(t.Context())); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, line := range mode.chatContainer.Render(80) {
			got = append(got, strings.TrimRight(stripANSI(line), " "))
		}
		body := []string{" No changelog entries found."}
		if content != "" {
			body = []string{" [0.1.0]", "", " Oldest", "", " [0.2.0]", "", " Newest"}
		}
		want := append([]string{"", strings.Repeat("─", 80), " What's New", "", ""}, body...)
		want = append(want, "", strings.Repeat("─", 80))
		if !slices.Equal(got, want) {
			t.Fatalf("inline changelog layout\ngot:  %q\nwant: %q", got, want)
		}
	}
}
