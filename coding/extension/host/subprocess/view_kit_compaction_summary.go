package subprocess

// view_kit_compaction_summary.go: the compaction-summary kind, coding-agent's
// CompactionSummaryMessageComponent(message, getMarkdownTheme(), outputPad)
// (D107 §2.2; compaction-summary-message.ts:15).

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindCompactionSummary, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.TokensBefore != nil && *n.TokensBefore < 0 {
				c.fail("compaction-summary tokensBefore %d is negative", *n.TokensBefore)
			}
		},
		build: buildCompactionSummary,
	})
}

func buildCompactionSummary(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	out.Text, out.Tokens = n.Summary, intOr(n.TokensBefore, 0)
	return b.expandableMessage(n, out, widgetCtorKey(n, func(c *ViewNode) { c.Expanded, c.OutputPad = false, nil }), func(pad int) expandableComponent {
		return tui.NewCompactionSummaryMessageComponent(tui.CompactionSummaryMessage{Summary: n.Summary, TokensBefore: intOr(n.TokensBefore, 0)}, nil, pad)
	}), nil
}
