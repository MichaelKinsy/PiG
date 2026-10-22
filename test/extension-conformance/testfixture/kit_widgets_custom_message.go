package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	const piece = "pi-coding-agent/CustomMessageComponent/"
	const body = "Build **passed** in 12s\n\n- 214 tests\n- 0 failures"
	custom := func(text string, set func(*kit.CustomMessage)) func() kit.View {
		return func() kit.View {
			node := kit.NewCustomMessage("ci-status", text)
			node.ID = "m"
			if set != nil {
				set(node)
			}
			return kit.View{Root: kit.NewContainer(node)}
		}
	}
	registerKitWidgetCase(piece+"default", custom(body, nil))
	registerKitWidgetCase(piece+"outputPad 0", custom(body, func(n *kit.CustomMessage) { n.SetOutputPad(0) }))
	registerKitWidgetCase(piece+"expanded changes nothing", custom(body, func(n *kit.CustomMessage) { n.SetExpanded(true) }))
	registerKitWidgetCase(piece+"empty text", custom("", nil))
	registerKitWidgetCase(piece+"no id", func() kit.View { return kit.View{Root: kit.NewCustomMessage("note", "plain")} })
}
