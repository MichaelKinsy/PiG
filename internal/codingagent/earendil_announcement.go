package codingagent

import (
	_ "embed"
	"encoding/base64"
	"sync"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts.

//go:embed assets/clankolas.png
var clankolasPNG []byte

var loadAnnouncementImage = sync.OnceValue(func() string { return base64.StdEncoding.EncodeToString(clankolasPNG) })

type earendilAnnouncementComponent struct {
	*tui.Container
}

func newEarendilAnnouncementComponent() *earendilAnnouncementComponent {
	theme := tui.ActiveTheme()
	image := tui.NewImage(loadAnnouncementImage(), "image/png", tui.DefaultImageTheme(), tui.ImageOptions{MaxWidthCells: 56, Filename: "clankolas.png"}, nil)
	image.Theme.FallbackColor = func(text string) string { return tui.ActiveTheme().Fg("muted", text) }
	return &earendilAnnouncementComponent{Container: tui.NewContainer(
		tui.NewDynamicBorderToken("accent"),
		tui.NewPaddedText("\x1b[1m"+theme.Fg("accent", "pi has joined Earendil")+"\x1b[22m", 1, 0, nil),
		tui.NewSpacer(1),
		tui.NewPaddedText(theme.Fg("muted", "Read the blog post:"), 1, 0, nil),
		tui.NewPaddedText(theme.Fg("mdLink", "https://mariozechner.at/posts/2026-04-08-ive-sold-out/"), 1, 0, nil),
		tui.NewSpacer(1),
		image,
		tui.NewSpacer(1),
		tui.NewDynamicBorderToken("accent"),
	)}
}

// Invalidate forwards to the children, as upstream Container.invalidate does, so the image re-reads terminal capabilities.
func (e *earendilAnnouncementComponent) Invalidate() {
	e.Container.Invalidate()
	for _, child := range e.Children() {
		child.Invalidate()
	}
}
