package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// sentinel colors make the exact ANSI structure assertable without depending on
// the active theme's real palette.
func noticeTestTheme() *tui.Theme {
	return &tui.Theme{Warning: "<W>", Muted: "<M>", Accent: "<A>"}
}

func TestBinaryUpdateNoticeBodyMatchesUpstreamLayout(t *testing.T) {
	got := binaryUpdateNoticeBody(noticeTestTheme(), "1.2.3", "pig update")
	want := "\x1b[1m<W>Update Available\x1b[0m" +
		"\n<M>New version 1.2.3 is available. Run \x1b[0m<A>pig update\x1b[0m"
	if got != want {
		t.Fatalf("binary notice body mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestPackageUpdateNoticeBodyMatchesUpstreamLayout(t *testing.T) {
	got := packageUpdateNoticeBody(noticeTestTheme(), []string{"alpha", "beta"})
	want := "\x1b[1m<W>Package Updates Available\x1b[0m" +
		"\n<M>Package updates are available. Run \x1b[0m<A>pig update --extensions\x1b[0m" +
		"\n<M>Packages:\x1b[0m" +
		"\n- alpha\n- beta"
	if got != want {
		t.Fatalf("package notice body mismatch:\n got: %q\nwant: %q", got, want)
	}
}
