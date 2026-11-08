package mygoshell

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// builder is what native content is: a function of its state building one frame (shell/mygoshell/native's App).
type builder interface {
	Build(c *ui.Context)
}

// pig additive (D481): a native window draws the content itself: no page, no web view, no bridge.
func (p *Platform) newNativeWindow(spec shell.WindowSpec) (shell.Window, error) {
	content, ok := spec.Content.(builder)
	if !ok {
		return nil, fmt.Errorf("mygoshell: %T cannot be drawn: it has no Build(*ui.Context)", spec.Content)
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:     spec.Title,
		Width:     spec.Width,
		Height:    spec.Height,
		MinWidth:  spec.MinWidth,
		MinHeight: spec.MinHeight,
		StateKey:  spec.StateKey,
		Content:   ui.View(content.Build),
	})
	spec.Content.Attach(w.Invalidate)
	w.OnClosed(spec.Content.Detach)
	return native{window{w: w}}, nil
}

// native is a window of native content: it has no page to load.
type native struct{ window }

func (native) Load(string) {}
