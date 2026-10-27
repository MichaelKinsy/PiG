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

// announcementImage holds the base64 image, encoded on first use. It is not a sync.OnceValue package variable: that
// initializer runs at program start and keeps the 539 KB image in every program that links this package, the
// interactive mode or not. Here only the announcement component refers to the image.
var announcementImage struct {
	once  sync.Once
	value string
}

func loadAnnouncementImage() string {
	announcementImage.once.Do(func() { announcementImage.value = base64.StdEncoding.EncodeToString(clankolasPNG) })
	return announcementImage.value
}

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
