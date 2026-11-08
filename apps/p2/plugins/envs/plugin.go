package envs

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	envstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/tui"
)

func init() {
	server, worker, tuiFacet := serverFacet, workerFacet, envstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "envs", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
