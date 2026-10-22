package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	input := func(set func(*kit.Input), focus bool) func() kit.View {
		return func() kit.View {
			node := kit.NewInput("i")
			if set != nil {
				set(node)
			}
			view := kit.View{Root: kit.NewContainer(node)}
			if focus {
				view.Focus = "i"
			}
			return view
		}
	}
	prompt := func(value string) *string { return &value }
	const c = "pi-tui/Input/"
	registerKitWidgetCase(c+"empty", input(nil, false))
	registerKitWidgetCase(c+"empty focused", input(nil, true))
	registerKitWidgetCase(c+"placeholder", input(func(n *kit.Input) { n.Placeholder = "type here" }, false))
	registerKitWidgetCase(c+"placeholder focused colored", input(func(n *kit.Input) { n.Placeholder, n.PlaceholderColor = "type here", "dim" }, true))
	registerKitWidgetCase(c+"custom prompt", input(func(n *kit.Input) { n.Prompt, n.Value = prompt("$ "), "ls -la" }, true))
	registerKitWidgetCase(c+"value", input(func(n *kit.Input) { n.Value = "hello world" }, false))
	registerKitWidgetCase(c+"value focused", input(func(n *kit.Input) { n.Value = "hello world" }, true))
	registerKitWidgetCase(c+"wide characters", input(func(n *kit.Input) { n.Value = "漢字 and 😀 text" }, true))
	for _, name := range []string{"typing", "left then insert", "backspace and delete", "home and end", "kill word and yank", "kill to end", "paste scrolls", "submit"} {
		registerKitWidgetCase(c+name, input(nil, true))
	}
	registerKitWidgetCase(c+"submit newline key", input(func(n *kit.Input) { n.Value = "preset" }, true))
	registerKitWidgetCase(c+"escape", input(func(n *kit.Input) { n.Value = "keep" }, true))
	for _, name := range []string{"click moves the cursor", "right click ignored"} {
		registerKitWidgetCase(c+name, input(func(n *kit.Input) { n.Value = "hello world" }, true))
	}
}
