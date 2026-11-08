package history

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	historytui "github.com/MichaelKinsy/PiG/apps/p2/plugins/history/tui"
)

func init() {
	worker, tuiFacet := Worker, historytui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "history", Worker: &worker, TUI: &tuiFacet})
}
