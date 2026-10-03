package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// sentinel colors make the exact ANSI structure assertable without depending on
// the active theme's real palette.
func noticeTestTheme() *tui.Theme {
	return &tui.Theme{Warning: "<W>", Muted: "<M>", Accent: "<A>"}
}

// upstream: interactive-mode.ts:4476-4477,4487. theme.fg closes its color with
// SGR 39 and theme.bold (chalk.bold) closes bold with SGR 22; neither resets
// every attribute.
func TestBinaryUpdateNoticeBodyMatchesUpstreamLayout(t *testing.T) {
	got := binaryUpdateNoticeBody(noticeTestTheme(), "1.2.3", "pig update")
	want := "\x1b[1m<W>Update Available\x1b[39m\x1b[22m" +
		"\n<M>New version 1.2.3 is available. Run \x1b[39m<A>pig update\x1b[39m"
	if got != want {
		t.Fatalf("binary notice body mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// upstream: interactive-mode.ts:4504-4515, with theme.fg and theme.bold
// closing as above.
func TestPackageUpdateNoticeBodyMatchesUpstreamLayout(t *testing.T) {
	got := packageUpdateNoticeBody(noticeTestTheme(), []string{"alpha", "beta"})
	want := "\x1b[1m<W>Package Updates Available\x1b[39m\x1b[22m" +
		"\n<M>Package updates are available. Run \x1b[39m<A>pig update --extensions\x1b[39m" +
		"\n<M>Packages:\x1b[39m" +
		"\n- alpha\n- beta"
	if got != want {
		t.Fatalf("package notice body mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// noticeLayout renders the chat notice a startup update check appends, as the
// interactive mode shows it: spacer, warning border, body, warning border.
func noticeLayout(t *testing.T, show func(*InteractiveMode), width int) []string {
	t.Helper()
	m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, width, 24)}
	show(m)
	lines := m.chatContainer.Render(width)
	visible := make([]string, len(lines))
	for i, line := range lines {
		visible[i] = strings.TrimRight(widthx.StripAnsi(line), " ")
	}
	return visible
}

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:4475-4502
// showNewVersionNotification renders every Text and Markdown with paddingX 1,
// a muted "Changelog: " line under the release note, and warning borders. The
// expected rows are the visible text of real Pi 0.87.1 (pi-tui Container.render
// at width 50) for the same structure: one column of left padding on every
// line, including the blank line between Markdown paragraphs.
func TestNewVersionNotificationMatchesPiLayout(t *testing.T) {
	const rule = "──────────────────────────────────────────────────"
	got := noticeLayout(t, func(m *InteractiveMode) {
		m.showNewVersionNotification(&BinaryUpdate{
			LatestVersion: "1.2.3",
			Command:       "pig update",
			Notes:         "First **bold** note line\n\n- item one",
			ChangelogURL:  "https://example.test/changelog",
		})
	}, 50)
	want := []string{
		"",
		rule,
		" Update Available",
		" New version 1.2.3 is available. Run pig update",
		"",
		" First bold note line",
		"",
		" - item one",
		"",
		" Changelog: https://example.test/changelog",
		rule,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notice rows:\n got: %q\nwant: %q", got, want)
	}
}

// upstream: interactive-mode.ts:4475-4502. Pi always pads the heading block;
// with no release note it renders no note block or spacers.
func TestNewVersionNotificationWithoutNote(t *testing.T) {
	const rule = "────────────────────────────────────────────────"
	got := noticeLayout(t, func(m *InteractiveMode) {
		m.showNewVersionNotification(&BinaryUpdate{LatestVersion: "1.2.3", Command: "pig update", ChangelogURL: "https://example.test/c"})
	}, 48)
	want := []string{
		"",
		rule,
		" Update Available",
		" New version 1.2.3 is available. Run pig update",
		" Changelog: https://example.test/c",
		rule,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notice rows:\n got: %q\nwant: %q", got, want)
	}
}

// PiG's release manifest carries the release page URL as its note. That URL is
// the changelog link: it is shown once, on the Changelog line, not again as a
// note block.
func TestBinaryUpdateChangelogURLComesFromTheManifestNote(t *testing.T) {
	for _, tc := range []struct{ notes, url, note string }{
		{"https://github.com/MichaelKinsy/PiG/releases/tag/v1.2.3", "https://github.com/MichaelKinsy/PiG/releases/tag/v1.2.3", ""},
		{"  https://updates.example/notes  ", "https://updates.example/notes", ""},
		{"Fixes the npm update path.", "", "Fixes the npm update path."},
		{"See https://example.test/x for details", "", "See https://example.test/x for details"},
		{"", "", ""},
	} {
		url, note := splitBinaryUpdateNotes(tc.notes)
		if url != tc.url || note != tc.note {
			t.Errorf("splitBinaryUpdateNotes(%q) = (%q, %q), want (%q, %q)", tc.notes, url, note, tc.url, tc.note)
		}
	}
}

// upstream: interactive-mode.ts:4504-4520. showPackageUpdateNotification uses
// Text(..., 1, 0), so its lines carry the same one-column padding.
func TestPackageUpdateNotificationPadsLikePi(t *testing.T) {
	const rule = "────────────────────────────────────────────────────────────"
	got := noticeLayout(t, func(m *InteractiveMode) {
		m.finishPackageUpdateCheck("linux", []string{"alpha", "beta"})
	}, 60)
	want := []string{
		"",
		rule,
		" Package Updates Available",
		" Package updates are available. Run pig update --extensions",
		" Packages:",
		" - alpha",
		" - beta",
		rule,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notice rows:\n got: %q\nwant: %q", got, want)
	}
}

// The startup check hands the notice its changelog link from the signed
// manifest's own note, not from a literal in the client.
func TestCheckForBinaryUpdateCarriesTheManifestChangelogURL(t *testing.T) {
	for _, tc := range []struct {
		notes, wantURL, wantNotes string
	}{
		{"https://github.com/MichaelKinsy/PiG/releases/tag/v9.9.9", "https://github.com/MichaelKinsy/PiG/releases/tag/v9.9.9", ""},
		{"Plain release note.", "", "Plain release note."},
	} {
		srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			body, _ := json.Marshal(map[string]any{
				"version": "9.9.9", "packageName": PackageName, "notes": tc.notes,
				"binaries": map[string]any{platformKey(): map[string]string{"url": "u", "sha256": strings.Repeat("0", 64)}},
			})
			_, _ = w.Write(body)
		}))
		t.Setenv("PIG_UPDATE_URL", srv.URL)
		t.Setenv("PI_SKIP_VERSION_CHECK", "")
		u := CheckForBinaryUpdate(context.Background(), srv.Client(), "0.1.1")
		srv.Close()
		if u == nil || u.ChangelogURL != tc.wantURL || u.Notes != tc.wantNotes {
			t.Errorf("notes %q: update = %#v, want ChangelogURL %q and Notes %q", tc.notes, u, tc.wantURL, tc.wantNotes)
		}
	}
}

// upstream: interactive-mode.ts:4479-4481. The changelog URL is an OSC 8
// hyperlink when the terminal supports them, plain accent text otherwise.
func TestNewVersionNotificationLinksTheChangelogOnlyWhereSupported(t *testing.T) {
	const url = "https://example.test/changelog"
	render := func(hyperlinks bool) string {
		caps := tui.GetCapabilities()
		caps.Hyperlinks = hyperlinks
		tui.SetCapabilities(caps)
		t.Cleanup(tui.ResetCapabilitiesCache)
		m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
		m.showNewVersionNotification(&BinaryUpdate{LatestVersion: "1.2.3", Command: "pig update", ChangelogURL: url})
		return strings.Join(m.chatContainer.Render(80), "\n")
	}
	if got := render(true); !strings.Contains(got, "\x1b]8;;"+url+"\x1b\\") {
		t.Errorf("hyperlink-capable terminal: no OSC 8 link to %s in %q", url, got)
	}
	if got := render(false); strings.Contains(got, "\x1b]8;;") || !strings.Contains(got, url) {
		t.Errorf("plain terminal: %q, want the URL without an OSC 8 link", got)
	}
}

// renderNoticeBytes renders a notice in the dark theme on a truecolor terminal
// without OSC 8 support and returns every row with its escape sequences.
func renderNoticeBytes(t *testing.T, show func(*InteractiveMode), width int) []string {
	t.Helper()
	previous := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(previous)
		tui.RefreshActiveThemeColorMode()
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.RefreshActiveThemeColorMode()
	restoreStartupTheme(t)
	tui.SetThemeByName("dark")
	m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, width, 24)}
	show(m)
	return m.chatContainer.Render(width)
}

// upstream: interactive-mode.ts:4475-4502. The rows are the output of real Pi
// 0.87.1: pi-tui Container.render(48) of showNewVersionNotification's children
// (Spacer, DynamicBorder, Text(..., 1, 0), Text(changelogLine, 1, 0),
// DynamicBorder) built with initTheme("dark"), FORCE_COLOR=3 and a truecolor
// terminal without hyperlinks, APP_NAME "pig", version 1.2.3, no note and the
// changelog URL https://example.test/c.
func TestNewVersionNotificationBytesMatchPi(t *testing.T) {
	got := renderNoticeBytes(t, func(m *InteractiveMode) {
		m.showNewVersionNotification(&BinaryUpdate{LatestVersion: "1.2.3", Command: "pig update", ChangelogURL: "https://example.test/c"})
	}, 48)
	want := []string{
		"",
		"\x1b[38;2;205;154;34m────────────────────────────────────────────────\x1b[39m",
		" \x1b[1m\x1b[38;2;205;154;34mUpdate Available\x1b[39m\x1b[22m                               ",
		" \x1b[38;2;157;165;169mNew version 1.2.3 is available. Run \x1b[39m\x1b[38;2;167;152;215mpig update\x1b[39m ",
		" \x1b[38;2;157;165;169mChangelog: \x1b[39m\x1b[38;2;167;152;215mhttps://example.test/c\x1b[39m              ",
		"\x1b[38;2;205;154;34m────────────────────────────────────────────────\x1b[39m",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notice rows:\n got: %q\nwant: %q", got, want)
	}
}

// upstream: interactive-mode.ts:4504-4520. Real upstream 0.99.1 output of
// showPackageUpdateNotification(["alpha", "beta"]) at width 60, rendered as
// above.
func TestPackageUpdateNotificationBytesMatchPi(t *testing.T) {
	got := renderNoticeBytes(t, func(m *InteractiveMode) {
		m.finishPackageUpdateCheck("linux", []string{"alpha", "beta"})
	}, 60)
	want := []string{
		"",
		"\x1b[38;2;205;154;34m────────────────────────────────────────────────────────────\x1b[39m",
		" \x1b[1m\x1b[38;2;205;154;34mPackage Updates Available\x1b[39m\x1b[22m                                  ",
		" \x1b[38;2;157;165;169mPackage updates are available. Run \x1b[39m\x1b[38;2;167;152;215mpig update --extensions\x1b[39m ",
		" \x1b[38;2;157;165;169mPackages:\x1b[39m                                                  ",
		" - alpha                                                    ",
		" - beta                                                     ",
		"\x1b[38;2;205;154;34m────────────────────────────────────────────────────────────\x1b[39m",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notice rows:\n got: %q\nwant: %q", got, want)
	}
}
