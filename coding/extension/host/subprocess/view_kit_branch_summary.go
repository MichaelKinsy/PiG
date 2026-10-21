package subprocess

// view_kit_branch_summary.go: the branch-summary kind, coding-agent's
// BranchSummaryMessageComponent(message, getMarkdownTheme(), outputPad) (D107
// §2.2; branch-summary-message.ts:15). Like Pi's conversation components it
// draws with the active theme.

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindBranchSummary, &viewWidget{build: buildBranchSummary})
}

func buildBranchSummary(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	out.Text = n.Summary
	return b.expandableMessage(n, out, widgetCtorKey(n, func(c *ViewNode) { c.Expanded, c.OutputPad = false, nil }), func(pad int) expandableComponent {
		return tui.NewBranchSummaryMessageComponent(tui.BranchSummaryMessage{Summary: n.Summary}, nil, pad)
	}), nil
}
