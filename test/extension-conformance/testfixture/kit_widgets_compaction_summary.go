package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	const piece = "pi-coding-agent/CompactionSummaryMessageComponent/"
	const summary = "Compacted the first **forty** turns:\n\n1. the build setup\n2. the failing test\n\nThe fix is pending."
	compaction := func(tokens int, set func(*kit.CompactionSummary)) func() kit.View {
		return func() kit.View {
			node := kit.NewCompactionSummary(summary, tokens)
			node.ID = "c"
			if set != nil {
				set(node)
			}
			return kit.View{Root: kit.NewContainer(node)}
		}
	}
	expand := func(n *kit.CompactionSummary) { n.SetExpanded(true) }
	registerKitWidgetCase(piece+"collapsed", compaction(123456, nil))
	registerKitWidgetCase(piece+"click toggles", compaction(123456, nil))
	registerKitWidgetCase(piece+"right click ignored", compaction(123456, nil))
	registerKitWidgetCase(piece+"expanded", compaction(123456, expand))
	registerKitWidgetCase(piece+"outputPad 0 collapsed", compaction(123456, func(n *kit.CompactionSummary) { n.SetOutputPad(0) }))
	registerKitWidgetCase(piece+"outputPad 0 expanded", compaction(123456, func(n *kit.CompactionSummary) { n.SetOutputPad(0); n.SetExpanded(true) }))
	registerKitWidgetCase(piece+"tokens 999", compaction(999, nil))
	registerKitWidgetCase(piece+"tokens 1234567 expanded", compaction(1234567, expand))
	registerKitWidgetCase(piece+"empty summary tokens 0 expanded", func() kit.View {
		node := kit.NewCompactionSummary("", 0)
		node.SetExpanded(true)
		return kit.View{Root: node}
	})
}
