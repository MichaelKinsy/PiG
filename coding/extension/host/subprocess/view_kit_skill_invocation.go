package subprocess

// view_kit_skill_invocation.go: the skill-invocation kind, coding-agent's
// SkillInvocationMessageComponent(skillBlock, getMarkdownTheme(), outputPad)
// (D107 §2.2; skill-invocation-message.ts:16).

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindSkillInvocation, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.Skill == nil {
				c.fail("skill-invocation needs a skill")
			}
		},
		build: buildSkillInvocation,
	})
}

func buildSkillInvocation(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	out.Name, out.Text = n.Skill.Name, n.Skill.Content
	return b.expandableMessage(n, out, widgetCtorKey(n, func(c *ViewNode) { c.Expanded, c.OutputPad = false, nil }), func(pad int) expandableComponent {
		return tui.NewSkillInvocationMessageComponent(tui.ParsedSkillBlock{Name: n.Skill.Name, Content: n.Skill.Content}, nil, pad)
	}), nil
}
