package status

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
)

func init() {
	server, worker, tuiFacet := Server, Worker, statustui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "status", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
