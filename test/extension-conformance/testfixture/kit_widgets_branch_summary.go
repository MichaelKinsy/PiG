package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	const piece = "pi-coding-agent/BranchSummaryMessageComponent/"
	const summary = "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`."
	branch := func(set func(*kit.BranchSummary)) func() kit.View {
		return func() kit.View {
			node := kit.NewBranchSummary(summary)
			node.ID = "b"
			if set != nil {
				set(node)
			}
			return kit.View{Root: kit.NewContainer(node)}
		}
	}
	registerKitWidgetCase(piece+"collapsed", branch(nil))
	registerKitWidgetCase(piece+"expanded", branch(func(n *kit.BranchSummary) { n.SetExpanded(true) }))
	registerKitWidgetCase(piece+"outputPad 0 collapsed", branch(func(n *kit.BranchSummary) { n.SetOutputPad(0) }))
	registerKitWidgetCase(piece+"outputPad 0 expanded", branch(func(n *kit.BranchSummary) { n.SetOutputPad(0); n.SetExpanded(true) }))
	registerKitWidgetCase(piece+"click toggles", branch(nil))
	registerKitWidgetCase(piece+"right click ignored", branch(nil))
	registerKitWidgetCase(piece+"empty summary expanded", func() kit.View {
		node := kit.NewBranchSummary("")
		node.SetExpanded(true)
		return kit.View{Root: node}
	})
}
