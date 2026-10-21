package subprocess

// view_kit_custom_message.go: the custom-message kind, coding-agent's
// CustomMessageComponent(message, undefined, getMarkdownTheme(), outputPad)
// (D107 §2.2; custom-message.ts:23): the default rendering of an extension's
// message, "[customType]" over the content's text in a customMessageBg box. A
// message renderer is a closure and has no kind: the message-renderer
// surfaces draw it.

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindCustomMessage, &viewWidget{build: buildCustomMessage})
}

func buildCustomMessage(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	out.Name, out.Text = n.CustomType, n.Text
	return b.expandableMessage(n, out, widgetCtorKey(n, func(c *ViewNode) { c.Expanded, c.OutputPad = false, nil }), func(pad int) expandableComponent {
		return tui.NewCustomMessageComponent(&tui.CustomMessage{CustomType: n.CustomType, Content: n.Text}, nil, nil, pad)
	}), nil
}
