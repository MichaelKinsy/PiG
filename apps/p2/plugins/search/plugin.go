package search

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	searchtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/tui"
)

func init() {
	server, worker, tuiFacet := ServerFacet, WorkerFacet, searchtui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "search", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
