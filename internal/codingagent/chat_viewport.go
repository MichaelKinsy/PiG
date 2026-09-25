package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// ChatViewportOptions mirrors upstream ChatViewportOptions (chat-viewport.ts).
// WidgetsAbove and WidgetsBelow are optional (nil omits the dock slot);
// Scrollbar "" takes upstream's "auto" default; nil styles take the ScrollView
// defaults.
type ChatViewportOptions struct {
	Document            tui.Component
	PendingMessages     tui.Component
	Status              tui.Component
	Editor              tui.Component
	Footer              tui.Component
	WidgetsAbove        tui.Component
	WidgetsBelow        tui.Component
	Scrollbar           string
	ScrollbarTrackStyle func(text string) string
	ScrollbarThumbStyle func(text string) string
}

// ChatViewport is the fullscreen layout root and its transcript scroll view.
// Mirrors upstream ChatViewport.
type ChatViewport struct {
	Root       tui.Component
	Transcript *tui.ScrollView
}

// CreateChatViewport builds the shared fullscreen transcript and fixed
// input-dock layout. Mirrors upstream createChatViewport.
func CreateChatViewport(options ChatViewportOptions) ChatViewport {
	scrollbar := options.Scrollbar
	if scrollbar == "" {
		scrollbar = "auto"
	}
	transcript := tui.NewScrollView(options.Document, tui.ScrollViewOptions{
		Follow:              "end",
		Primary:             true,
		Overscroll:          "chain",
		Scrollbar:           scrollbar,
		ScrollbarTrackStyle: options.ScrollbarTrackStyle,
		ScrollbarThumbStyle: options.ScrollbarThumbStyle,
	})
	shrinking := func(component tui.Component, minSize int) tui.StackChild {
		return tui.StackChild{Component: component, StackEntryOptions: tui.StackEntryOptions{Shrink: new(1), MinSize: new(minSize)}}
	}
	dockChildren := []tui.StackChild{
		shrinking(options.PendingMessages, 0),
		shrinking(options.Status, 0),
	}
	if options.WidgetsAbove != nil {
		dockChildren = append(dockChildren, shrinking(options.WidgetsAbove, 0))
	}
	dockChildren = append(dockChildren, shrinking(options.Editor, 3))
	if options.WidgetsBelow != nil {
		dockChildren = append(dockChildren, shrinking(options.WidgetsBelow, 0))
	}
	dockChildren = append(dockChildren, shrinking(options.Footer, 0))
	dock := tui.NewVStack(dockChildren, tui.StackOptions{})
	return ChatViewport{
		Transcript: transcript,
		Root: tui.NewVStack([]tui.StackChild{
			{Component: transcript, StackEntryOptions: tui.StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
			{Component: dock, StackEntryOptions: tui.StackEntryOptions{Grow: new(0), Shrink: new(1), MinSize: new(1)}},
		}, tui.StackOptions{}),
	}
}
