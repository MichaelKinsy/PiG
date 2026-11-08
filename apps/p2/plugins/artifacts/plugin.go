package artifacts

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	artifactstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/tui"
)

func init() {
	server, worker, tuiFacet := Server, Facet(), artifactstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "artifacts", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
