package codingagent

import (
	pig "github.com/MichaelKinsy/PiG"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleChangelogCommand).
// handleChangelogCommand appends every released entry oldest-first, with a separate title and borders. CollapseChangelog controls startup notices only.
func (m *InteractiveMode) handleChangelogCommand() {
	body := tui.NewPaddedBox(1, 1, nil)
	body.AddChild(tui.NewMarkdown(changelogMarkdown(ParseChangelog(pig.Changelog))))
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewDynamicBorder(""))
	m.chatContainer.Add(tui.NewPaddedText("\x1b[1m"+tui.ActiveTheme().FgText("accent", "What's New")+tui.SGRBoldDimReset, 1, 0, nil))
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(body)
	m.chatContainer.Add(tui.NewDynamicBorder(""))
	m.requestRender()
}
