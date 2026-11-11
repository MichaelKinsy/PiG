// Package tui is PowerShell's TUI facet: its card is the shared shell card with PowerShell's prompt.
//
// pi: apps/p2/plugins/powershell/tui.ts
package tui

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// ToolName is the tool the card draws (`POWERSHELL_TOOL`).
const ToolName = "powershell"

// Renderers is PowerShell's card.
var Renderers = tools.CreateShellRenderers("PS>")

// Facet is the plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-powershell", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, tuiruntime.TuiToolRenderers, p2runtime.Entry(ToolName, Renderers))
		return nil
	})
})
