package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/custom-entry.ts

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// CustomEntryComponent renders a custom session entry through an extension's entry renderer. The host owns transcript
// spacing; the renderer output is preceded by one Spacer(1). A renderer that panics is replaced by an error box
// (custom-entry.ts:46-52).
type CustomEntryComponent struct {
	*tui.Container
	entry           CustomEntry
	renderer        extension.EntryRenderer
	customComponent tui.Component
	expanded        bool
	outputPad       int
}

// NewCustomEntryComponent builds the component and renders the entry once (custom-entry.ts:18 constructor(entry, renderer, outputPad = 1));
// outputPad is the horizontal padding of the renderer-failed box.
func NewCustomEntryComponent(entry CustomEntry, renderer extension.EntryRenderer, outputPad int) *CustomEntryComponent {
	c := &CustomEntryComponent{Container: tui.NewContainer(), entry: entry, renderer: renderer, outputPad: outputPad}
	c.rebuild()
	return c
}

// SetOutputPad is Pi's setOutputPad(outputPad) (custom-entry.ts:37): the padding changes and the entry is rendered again.
func (c *CustomEntryComponent) SetOutputPad(outputPad int) {
	c.outputPad = outputPad
	c.rebuild()
}

// IsDirty reports a renderer component whose output changed on its own (an extension renderer proxy whose lines arrive after the first frame), so the parent container that caches this component re-renders it.
func (c *CustomEntryComponent) IsDirty() bool {
	if c.Container.IsDirty() {
		return true
	}
	dirty, ok := c.customComponent.(interface{ IsDirty() bool })
	return ok && dirty.IsDirty()
}

// pig additive (D91): SurfaceLive reports a renderer component that changes on its own, which
// IsDirty reads, so a TuiSurface rebuilds the entry every frame.
func (c *CustomEntryComponent) SurfaceLive() bool {
	_, ok := c.customComponent.(interface{ IsDirty() bool })
	return ok
}

// HasContent reports whether the renderer produced a component.
func (c *CustomEntryComponent) HasContent() bool { return c.customComponent != nil }

// SetExpanded re-renders the entry with the new expansion state when it changed.
func (c *CustomEntryComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.rebuild()
	}
}

// Invalidate re-renders the entry so theme changes reach the renderer's output.
func (c *CustomEntryComponent) Invalidate() {
	c.Container.Invalidate()
	c.rebuild()
}

func (c *CustomEntryComponent) rebuild() {
	c.Clear()
	c.customComponent = nil
	component := c.render()
	if component == nil {
		return
	}
	c.customComponent = component
	c.Add(tui.NewSpacer(1))
	c.Add(component)
}

// render calls the renderer, turning a panic into the error box upstream builds for a thrown error.
func (c *CustomEntryComponent) render() (component tui.Component) {
	defer func() {
		if r := recover(); r != nil {
			message := fmt.Sprint(r)
			if err, ok := r.(error); ok {
				message = err.Error()
			}
			box := tui.NewPaddedBox(c.outputPad, 1, func(s string) string { return tui.ActiveTheme().Bg("customMessageBg", s) })
			box.AddChild(tui.NewPaddedText(tui.ActiveTheme().Fg("error", fmt.Sprintf("[%s] renderer failed: %s", c.entry.CustomType, message)), 0, 0, nil))
			component = box
		}
	}()
	return c.renderer(c.entry, extension.EntryRenderOptions{Expanded: c.expanded}, nil)
}

var _ tui.Component = (*CustomEntryComponent)(nil)
