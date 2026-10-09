package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui"
)

// changelogVersionRE is /##\s+\[?(\d+\.\d+\.\d+)\]?/ (interactive-mode.ts:858), unanchored.
var changelogVersionRE = lazyregexp.New(`##[` + jsSpace + `]+\[?(\d+\.\d+\.\d+)\]?`)

// showStartupChangelog is showStartupNoticesIfNeeded's changelog block (interactive-mode.ts:843-870) for the entries newer than the
// last version seen, in file order with their links normalized (getChangelogForDisplay, :1353): a spacer when the chat is not empty,
// a border, then either one condensed line (collapseChangelog) or the title and padded Markdown, and a closing border.
func (m *InteractiveMode) showStartupChangelog(newEntries []ChangelogEntry) {
	parts := make([]string, len(newEntries))
	for i, entry := range newEntries {
		parts[i] = NormalizeChangelogLinks(entry.Content, entry.Version())
	}
	changelog := strings.Join(parts, "\n\n")
	if len(m.chatContainer.Children()) > 0 {
		m.chatContainer.Add(tui.NewSpacer(1))
	}
	m.chatContainer.Add(tui.NewDynamicBorder())
	collapse := m.opts.SettingsManager != nil && m.opts.SettingsManager.GetCollapseChangelog()
	if collapse {
		latestVersion := m.opts.AppVersion
		if match := changelogVersionRE.FindStringSubmatch(changelog); match != nil {
			latestVersion = match[1]
		}
		m.chatContainer.Add(tui.NewPaddedText("Updated to v"+latestVersion+". Use \x1b[1m/changelog\x1b[22m to view full changelog.", 1, 0, nil))
	} else {
		m.chatContainer.Add(tui.NewThemedText(func() string {
			return "\x1b[1m" + tui.ActiveTheme().Fg("accent", "What's New") + tui.SGRBoldDimReset
		}, 1, 0))
		m.chatContainer.Add(tui.NewSpacer(1))
		m.chatContainer.Add(tui.NewMarkdownWithOptions(strings.TrimSpace(changelog), 1, 0, m.markdownThemeWithSettings(), nil, nil))
		m.chatContainer.Add(tui.NewSpacer(1))
	}
	m.chatContainer.Add(tui.NewDynamicBorder())
}

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleChangelogCommand).
// handleChangelogCommand appends every released entry oldest-first, with a separate title and borders. CollapseChangelog controls startup notices only.
func (m *InteractiveMode) handleChangelogCommand() {
	body := tui.NewPaddedBox(1, 1, nil)
	body.AddChild(tui.NewMarkdownWithOptions(changelogMarkdown(ParseChangelog(bundledChangelog())), 0, 0, m.markdownThemeWithSettings(), nil, nil))
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewDynamicBorder())
	m.chatContainer.Add(tui.NewThemedText(func() string {
		return "\x1b[1m" + tui.ActiveTheme().Fg("accent", "What's New") + tui.SGRBoldDimReset
	}, 1, 0))
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(body)
	m.chatContainer.Add(tui.NewDynamicBorder())
	m.requestRender()
}
