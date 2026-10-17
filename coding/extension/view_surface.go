package extension

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// ViewSurface is an extension surface whose lines the host draws from a view
// of Pi's tui components (D107): an authoritative view the host renders, or
// lines a Node runtime rendered together with the view that describes them.
// The terminal always draws Render; FrontendView only adds structure for a
// D91 frontend.
//
// pig additive (D107): Pi passes components in-process.
type ViewSurface interface {
	// Render returns the lines at width. It performs no I/O.
	Render(width int) []string
	// Invalidate drops cached lines, as a theme change requires.
	Invalidate()
	// HandleViewInput gives a key to the view's focused list when that list
	// binds it, and reports whether it did. A key it does not take goes to
	// the extension.
	HandleViewInput(data string) bool
	// FrontendView returns the structure of Render(width), or nil when the
	// surface has none (a Node view that did not reproduce its lines).
	FrontendView(width int) *frontend.View
}

// ViewTarget is implemented by a [RemoteOverlayHandle] that can draw a
// [ViewSurface]. The bridge calls UpdateViewAt for each accepted frame and
// for each change the surface makes on its own (a key its list took, a
// loader frame), with the terminal width the frame was laid out for (0 for
// any width).
type ViewTarget interface {
	UpdateViewAt(view ViewSurface, width int)
}

// FramedView is a header or footer frame drawn from a [ViewSurface]: Width
// is the terminal width a Node frame's lines were laid out at, or 0 for an
// authoritative view the host renders at any width.
type FramedView struct {
	View  ViewSurface
	Width int
}

// Render draws the view at width, so a framed view is the header or footer
// component its factory builds. A host that recognizes a FramedView installs
// the view instead, to keep its structure and its laid-out width.
func (f FramedView) Render(width int) []string { return f.View.Render(width) }

// Invalidate drops the view's cached lines.
func (f FramedView) Invalidate() { f.View.Invalidate() }

// Dispose is a no-op: the bridge that made the view closes it when the slot
// changes.
func (FramedView) Dispose() {}

// ViewHeader is the header factory of a frame drawn from a view.
func ViewHeader(view FramedView) HeaderFactory {
	return func(tui.TUI, *tui.Theme) DisposableComponent { return view }
}

// ViewFooter is the footer factory of a frame drawn from a view.
func ViewFooter(view FramedView) FooterFactory {
	return func(tui.TUI, *tui.Theme, ReadonlyFooterDataProvider) DisposableComponent { return view }
}
