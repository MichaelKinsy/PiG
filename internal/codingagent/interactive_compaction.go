// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (addCompactionCostNotice and compaction_end rendering).
func (m *InteractiveMode) addCompactionCostNotice(kind string, usage *ai.Usage) {
	if usage == nil || !m.showCacheMissNotices() {
		return
	}
	label := "Compaction"
	if kind == "branch_summary" {
		label = "Branch summary"
	}
	cost := ""
	if usage.Cost.Total >= .01 {
		cost = " (~$" + jsToFixed(usage.Cost.Total, 2) + ")"
	}
	tokens := usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewPaddedText(tui.ActiveTheme().FgText("warning", fmt.Sprintf("%s: %s tokens billed%s", label, formatTokens(tokens), cost)), 1, 0, nil))
}

func (m *InteractiveMode) addCompactionSummary(summary string, tokensBefore int, usage *ai.Usage) {
	component := tui.NewCompactionSummaryComponent(summary, tokensBefore)
	if m.toolsExpanded {
		component.SetExpanded(true)
	}
	m.compactionOrder = append(m.compactionOrder, component)
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(component)
	m.addCompactionCostNotice("compaction", usage)
}

func (m *InteractiveMode) renderCompactionResult(event agent.CompactionEndEvent, entries []SessionEntry) {
	m.disposeArminComponents()
	m.chatContainer.Clear()
	m.tuiInst.ForceFullRender()
	if len(entries) > 0 {
		entries = entries[1:]
	}
	m.renderSessionEntryList(entries, false)
	m.addCompactionSummary(event.Summary, event.TokensBefore, event.Usage)
}
