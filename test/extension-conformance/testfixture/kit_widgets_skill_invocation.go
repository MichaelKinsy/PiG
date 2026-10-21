package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	const piece = "pi-coding-agent/SkillInvocationMessageComponent/"
	const content = "Review the diff for **bugs** first.\n\n- correctness\n- naming\n\nReport findings as a list."
	skill := func(set func(*kit.SkillInvocation)) func() kit.View {
		return func() kit.View {
			node := kit.NewSkillInvocation("code-review", content)
			node.ID = "s"
			if set != nil {
				set(node)
			}
			return kit.View{Root: kit.NewContainer(node)}
		}
	}
	registerKitWidgetCase(piece+"collapsed", skill(nil))
	registerKitWidgetCase(piece+"click toggles", skill(nil))
	registerKitWidgetCase(piece+"right click ignored", skill(nil))
	registerKitWidgetCase(piece+"expanded", skill(func(n *kit.SkillInvocation) { n.SetExpanded(true) }))
	registerKitWidgetCase(piece+"outputPad 0 collapsed", skill(func(n *kit.SkillInvocation) { n.SetOutputPad(0) }))
	registerKitWidgetCase(piece+"outputPad 0 expanded", skill(func(n *kit.SkillInvocation) { n.SetOutputPad(0); n.SetExpanded(true) }))
	registerKitWidgetCase(piece+"empty skill expanded", func() kit.View {
		node := kit.NewSkillInvocation("", "")
		node.SetExpanded(true)
		return kit.View{Root: node}
	})
}
