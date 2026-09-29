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
	tui.BaseComponent
	body *tui.Container
}

func newEarendilAnnouncementComponent() *earendilAnnouncementComponent {
	theme := tui.ActiveTheme()
	image := tui.NewImage(loadAnnouncementImage(), "image/png", tui.ImageOptions{MaxWidthCells: 56, Filename: "clankolas.png"}, nil)
	image.Theme.FallbackColor = func(text string) string { return tui.ActiveTheme().FgText("muted", text) }
	return &earendilAnnouncementComponent{body: tui.NewContainer(
		tui.NewPaddedText("\x1b[1m"+theme.FgText("accent", "pi has joined Earendil")+"\x1b[22m", 1, 0, nil),
		tui.NewSpacer(1),
		tui.NewPaddedText(theme.FgText("muted", "Read the blog post:"), 1, 0, nil),
		tui.NewPaddedText(theme.FgText("mdLink", "https://mariozechner.at/posts/2026-04-08-ive-sold-out/"), 1, 0, nil),
		tui.NewSpacer(1),
		image,
		tui.NewSpacer(1),
	)}
}

func (e *earendilAnnouncementComponent) Invalidate() {
	e.BaseComponent.Invalidate()
	e.body.Invalidate()
	for _, child := range e.body.Children() {
		child.Invalidate()
	}
}

func (e *earendilAnnouncementComponent) Render(width int) []string {
	border := tui.NewDynamicBorder(tui.ActiveTheme().Accent).Render(width)[0]
	return append(append([]string{border}, e.body.Render(width)...), border)
}
