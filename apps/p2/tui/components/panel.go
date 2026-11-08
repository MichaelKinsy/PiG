package components

// The side panel's frame: a title line over a scrollable body, both behind a one-column bar that separates the panel from the chat on its left. The
// runtime lays it out next to the screen's body and routes keys to it while focused.
//
// pi: apps/p2/src/tui/components/panel.ts

import (

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// PanelContent is what a plugin shows in the panel: lines at a width, released when the panel closes. Dispose is called when it implements Disposer.
type PanelContent = tui.Component

// Disposer is implemented by components that release resources (`dispose?()`).
type Disposer interface{ Dispose() }

// PanelContext is the `RenderContext & { hints: { key } }` PanelFrame takes.
type PanelContext interface {
	render.RenderContext
	// HintKey is hints.key: a key hint (`ctrl+o expand`) for a binding id.
	HintKey(id, description string) string
}

// PanelFrame is the side panel's frame.
type PanelFrame struct {
	ID      string
	Content PanelContent
	Scroll  *tui.ScrollView
	// Root is the component the runtime lays out (title over the scroll view).
	Root    *tui.VStack
	Focused bool
}

type funcComponent struct {
	render     func(width int) []string
	invalidate func()
}

func (c funcComponent) Render(width int) []string { return c.render(width) }
func (c funcComponent) Invalidate() {
	if c.invalidate != nil {
		c.invalidate()
	}
}

// NewPanelFrame is the PanelFrame constructor.
func NewPanelFrame(ctx PanelContext, id string, content PanelContent, title func() string) *PanelFrame {
	frame := &PanelFrame{ID: id, Content: content}
	bar := func(focused bool) string {
		token := "border"
		if focused {
			token = "accent"
		}
		return ctx.Theme().Current().Fg(token, "│")
	}
	// The bar must reach the bottom of the panel even when the content is shorter than the viewport.
	body := funcComponent{
		render: func(width int) []string {
			inner := max(1, width-2)
			lines := content.Render(inner)
			height := max(len(lines), frame.Scroll.ViewportHeight())
			prefix := bar(frame.Focused) + " "
			out := make([]string, height)
			for row := range out {
				line := ""
				if row < len(lines) {
					line = lines[row]
				}
				out[row] = prefix + line
			}
			return out
		},
		invalidate: content.Invalidate,
	}
	frame.Scroll = tui.NewScrollView(body, tui.ScrollViewOptions{
		Overscroll: "contain", Scrollbar: tui.ScrollbarAuto,
		ScrollbarTrackStyle: func(text string) string { return ctx.Theme().Current().Fg("scrollbarTrack", text) },
		ScrollbarThumbStyle: func(text string) string { return ctx.Theme().Current().Fg("scrollbarThumb", text) },
	})
	header := funcComponent{render: func(width int) []string {
		current := ctx.Theme().Current()
		var hint string
		if frame.Focused {
			hint = "  " + ctx.HintKey("tui.select.cancel", "back")
		} else {
			hint = "  " + ctx.HintKey("p2.panel.focus", "focus")
		}
		line := bar(frame.Focused) + " " + current.Bold(title()) + hint
		return []string{widthx.TruncateToWidth(line, width, "...", false)}
	}}
	frame.Root = tui.NewVStack([]tui.StackChild{
		{Component: header, StackEntryOptions: tui.StackEntryOptions{Basis: new(1), Shrink: new(0)}},
		{Component: frame.Scroll, StackEntryOptions: tui.StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
	}, tui.StackOptions{})
	return frame
}

