package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	const wide = "A very long message that wraps around the narrow widths of the table"
	cancellable := func(message string, set func(*kit.CancellableLoader)) func() kit.View {
		return func() kit.View {
			node := kit.NewCancellableLoader("c", message)
			if set != nil {
				set(node)
			}
			return kit.View{Root: kit.NewContainer(node), Focus: "c"}
		}
	}
	const c = "pi-tui/CancellableLoader/"
	for _, name := range []string{"default", "escape aborts", "escape twice aborts twice", "other keys are ignored"} {
		registerKitWidgetCase(c+name, cancellable("Working", nil))
	}
	registerKitWidgetCase(c+"default message", cancellable("Loading...", nil))
	registerKitWidgetCase(c+"colors", cancellable("Working", func(n *kit.CancellableLoader) { n.SpinnerColor, n.MessageColor = "accent", "muted" }))
	registerKitWidgetCase(c+"frame 3", cancellable("Working", func(n *kit.CancellableLoader) { n.Frame = new(3) }))
	registerKitWidgetCase(c+"indicator frames", cancellable("Working", func(n *kit.CancellableLoader) {
		n.Indicator = &kit.LoaderIndicator{Frames: []string{"*", "+", "x"}, IntervalMs: 50}
		n.Frame = new(1)
	}))
	registerKitWidgetCase(c+"hidden indicator", cancellable("Working", func(n *kit.CancellableLoader) { n.Indicator = &kit.LoaderIndicator{Frames: []string{}} }))
	registerKitWidgetCase(c+"wide message", cancellable(wide, func(n *kit.CancellableLoader) { n.SpinnerColor = "accent" }))

	bordered := func(message string, set func(*kit.BorderedLoader)) func() kit.View {
		return func() kit.View {
			node := kit.NewBorderedLoader("b", message)
			if set != nil {
				set(node)
			}
			return kit.View{Root: kit.NewContainer(node), Focus: "b"}
		}
	}
	const b = "pi-coding-agent/BorderedLoader/"
	for _, name := range []string{"cancellable", "escape aborts", "escape twice aborts twice", "other keys are ignored"} {
		registerKitWidgetCase(b+name, bordered("Fetching models", nil))
	}
	for _, name := range []string{"not cancellable", "escape ignored when not cancellable"} {
		registerKitWidgetCase(b+name, bordered("Fetching models", func(n *kit.BorderedLoader) { n.Cancellable = false }))
	}
	registerKitWidgetCase(b+"default message", bordered("Loading...", nil))
	registerKitWidgetCase(b+"frame 4", bordered("Fetching models", func(n *kit.BorderedLoader) { n.Frame = new(4) }))
	registerKitWidgetCase(b+"wide message", bordered("Fetching the models of every configured provider, one after another", nil))
}
