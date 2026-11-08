package tui

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Renderers are the coding tools' cards by tool name (bash's is the shared shell card).
func Renderers() []registries.RecordEntry[*tools.ToolRenderers] {
	return []registries.RecordEntry[*tools.ToolRenderers]{
		p2runtime.Entry("read", ReadRenderers), p2runtime.Entry("bash", tools.CreateShellRenderers("$")), p2runtime.Entry("edit", EditRenderers),
		p2runtime.Entry("write", WriteRenderers), p2runtime.Entry("grep", GrepRenderers), p2runtime.Entry("find", FindRenderers), p2runtime.Entry("ls", LsRenderers),
	}
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-coding-tools", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, tuiruntime.TuiToolRenderers, Renderers()...)
		return nil
	})
})
