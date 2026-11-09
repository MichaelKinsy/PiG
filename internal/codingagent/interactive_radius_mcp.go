//go:build !pig_strip_mcp

package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (offerRadiusMcpServer).

import (
	"fmt"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
)

// offerRadiusMcpServer offers to point the Radius MCP server in the global mcp.json at the Radius login, adding the
// server when missing. Nothing is asked when a global server already uses this login. Mirrors Pi's
// offerRadiusMcpServer (interactive-mode.ts:6292-6343).
func (m *InteractiveMode) offerRadiusMcpServer(providerID, providerName string) {
	// pig additive (D92): a runtime stripped mcp offers nothing, like a build without it (interactive_radius_mcp_stripped.go).
	if pigstrip.Has(pigstrip.ListExtensions, "mcp") {
		return
	}
	// This runs on the login's goroutine, which is not the owner loop when the login was started off it; the chat container and the status line are the
	// loop's alone (Pi's TUI is single-threaded), so every use of them goes through the loop. On the loop itself the work runs in place.
	ctx := m.loginContext()
	onMain := func(work func()) {
		_ = m.runOnMainAndWait(ctx, func() error {
			work()
			return nil
		})
	}
	plan, err := PlanRadiusMcpServer(providerID, m.opts.AgentDir, m.opts.CWD)
	if err != nil {
		onMain(func() { m.showError(fmt.Sprintf("Could not update %s: %v", filepath.Join(m.opts.AgentDir, "mcp.json"), err)) })
		return
	}
	if plan == nil {
		return
	}
	mcpPath := plan.McpPath

	selector := tui.NewExtensionSelectorComponent(fmt.Sprintf("Configure %s MCP in %s?", providerName, mcpPath), []string{"Yes", "No"}, nil, nil)
	index, ok := m.runEditorSlotExtensionSelector(selector)
	if !ok || index != 0 {
		return
	}
	if _, err := plan.Apply(); err != nil {
		onMain(func() { m.showError(fmt.Sprintf("Could not update %s: %v", mcpPath, err)) })
		return
	}
	// The MCP extension reads mcp.json when the session starts. Pi runs its /reload handler directly
	// (interactive-mode.ts:6334 handleReloadCommand), not the slash command an extension may override.
	onMain(func() {
		if err := reloadHandler(m.buildSlashContext(ctx)); err != nil {
			m.showError(err.Error())
		}
		m.tuiInst.Render()
	})
}
