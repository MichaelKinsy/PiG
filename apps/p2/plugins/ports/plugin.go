package ports

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	portstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/tui"
)

func init() {
	server, worker, tuiFacet := serverFacet, workerFacet, portstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "ports", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
