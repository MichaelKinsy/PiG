package codingagent

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// pig divergence (D39): only a note that is exactly one http or https URL without whitespace is the changelog link.
// Every other text, including a URL of another scheme or one inside prose, is a release note and names no changelog.
func TestBinaryUpdateNoteIsAChangelogLinkOnlyForOneHTTPOrHTTPSURL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		notes string
		link  string
		note  string
	}{
		{"https URL", "https://github.com/MichaelKinsy/PiG/releases/tag/v1.2.3", "https://github.com/MichaelKinsy/PiG/releases/tag/v1.2.3", ""},
		{"http URL", "http://updates.example/notes", "http://updates.example/notes", ""},
		{"URL with surrounding whitespace", "\n  https://updates.example/notes \t", "https://updates.example/notes", ""},
		{"javascript scheme", "javascript:alert(1)", "", "javascript:alert(1)"},
		{"file scheme", "file:///etc/passwd", "", "file:///etc/passwd"},
		{"javascript scheme with a host", "javascript://updates.example/%0Aalert(1)", "", "javascript://updates.example/%0Aalert(1)"},
		{"ftp scheme", "ftp://updates.example/notes", "", "ftp://updates.example/notes"},
		{"data scheme", "data:text/html,<b>x</b>", "", "data:text/html,<b>x</b>"},
		{"scheme without host", "https://", "", "https://"},
		{"scheme relative", "//updates.example/notes", "", "//updates.example/notes"},
		{"whitespace inside the URL", "https://updates.example/a b", "", "https://updates.example/a b"},
		{"URL inside prose", "Read https://updates.example/notes now", "", "Read https://updates.example/notes now"},
		{"two URLs", "https://updates.example/a\nhttps://updates.example/b", "", "https://updates.example/a\nhttps://updates.example/b"},
		{"plain note", "Fixes the update path.", "", "Fixes the update path."},
		{"no note", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, note := splitBinaryUpdateNotes(tc.notes)
			if link != tc.link || note != tc.note {
				t.Fatalf("splitBinaryUpdateNotes(%q) = (%q, %q), want (%q, %q)", tc.notes, link, note, tc.link, tc.note)
			}
		})
	}
}

// pig divergence (D39): the rendered update notice carries a `Changelog:` row only for a release page URL (shown once, as
// an OSC 8 link on a terminal that has them), and no row, and never an OSC 8 link, for a note of any other kind or none.
func TestNewVersionNotificationRendersAChangelogRowOnlyForAReleasePageURL(t *testing.T) {
	const page = "https://github.com/MichaelKinsy/PiG/releases/tag/v1.2.3"
	for _, tc := range []struct {
		name      string
		note      *string
		changelog bool
		noteText  string
	}{
		{"release page URL", new(page), true, ""},
		{"no note", nil, false, ""},
		{"blank note", new("   "), false, ""},
		{"plain note", new("Fixes the update path."), false, "Fixes the update path."},
		{"javascript scheme", new("javascript:alert(1)"), false, "javascript:alert(1)"},
		{"file scheme", new("file:///etc/passwd"), false, "file:///etc/passwd"},
		{"javascript scheme with a host", new("javascript://updates.example/%0Aalert(1)"), false, "javascript://updates.example/%0Aalert(1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := tui.GetCapabilities()
			caps.Hyperlinks = true
			tui.SetCapabilities(caps)
			t.Cleanup(tui.ResetCapabilitiesCache)
			m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
			m.showNewVersionNotification(LatestPiRelease{Version: "1.2.3", Note: tc.note})
			raw := strings.Join(m.chatContainer.Render(80), "\n")
			rows := noticeLayout(t, func(m *InteractiveMode) {
				m.showNewVersionNotification(LatestPiRelease{Version: "1.2.3", Note: tc.note})
			}, 80)

			changelogRows := 0
			for _, row := range rows {
				if strings.HasPrefix(row, " Changelog: ") {
					changelogRows++
				}
			}
			if tc.changelog {
				if changelogRows != 1 || strings.Count(raw, page) != 2 || !strings.Contains(raw, "\x1b]8;;"+page+"\x1b\\") {
					// The URL occurs twice in the bytes of one OSC 8 link: once as its target and once as its text.
					t.Fatalf("release page URL: %d Changelog rows, rows %q, bytes %q", changelogRows, rows, raw)
				}
			} else if changelogRows != 0 || strings.Contains(raw, "\x1b]8;") {
				t.Fatalf("no release page URL: %d Changelog rows, OSC 8 link present=%v, rows %q", changelogRows, strings.Contains(raw, "\x1b]8;"), rows)
			}
			if tc.noteText != "" && !slices.Contains(rows, " "+tc.noteText) {
				t.Fatalf("the note %q is not shown as a note block: %q", tc.noteText, rows)
			}
			if tc.noteText == "" && slices.ContainsFunc(rows, func(row string) bool { return strings.Contains(row, "javascript") || strings.Contains(row, "file:") }) {
				t.Fatalf("unexpected note block: %q", rows)
			}
		})
	}
}
